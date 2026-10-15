package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/lastgood"
)

// This mimics the identity owner's save validator, deliberately stricter than
// json.Valid. Empty input is allowed by its reader, but is kept as bad on save.
func identityValid(b []byte) bool {
	var f struct {
		Profiles []struct {
			ID string `json:"id"`
		} `json:"profiles"`
		Bound map[string]string `json:"bound"`
	}
	return json.Unmarshal(b, &f) == nil
}
func TestRecoveryPreservesOwnerValidationAndRecoveryA(t *testing.T) {
	for name, before := range map[string][]byte{"empty": {}, "whitespace": []byte(" \n"), "zeroed": {0, 0}, "wrong-type": []byte(`{"bound":[]}`), "valid": beforeIdentity} {
		t.Run(name, func(t *testing.T) {
			home(t)
			defer mustLock(t)()
			put(t, IdentitiesFile, before)
			put(t, IdentitiesFile+".bak", beforeIdentity)
			p, err := Prepare(IdentitiesFile, Present(afterIdentity), identityValid)
			if err != nil {
				t.Fatal(err)
			}
			plan := Plan{Participants: []Participant{p}, stage: func(s string) error {
				if s == "identities.json:keep:after" {
					return errors.New("crash after Keep")
				}
				return nil
			}}
			if err := Commit(plan); err == nil {
				t.Fatal("not paused")
			}
			if err := Recover(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(get(t, IdentitiesFile+".bak"), beforeIdentity) {
				t.Fatal("backup advanced or invalid source became backup")
			}
			if !identityValid(before) {
				names, _ := filepath.Glob(filepath.Join(appdir.Config(), IdentitiesFile+".bad-*"))
				if len(names) != 1 {
					t.Fatalf("Keep retry did not preserve/deduplicate bad evidence: %v", names)
				}
				b, err := os.ReadFile(names[0])
				if err != nil || !bytes.Equal(b, before) {
					t.Fatal("bad input lost", err)
				}
			}
			// Recovery A remains a previous-generation fallback, not a revocation barrier.
			put(t, IdentitiesFile, []byte{0, 0, 0})
			recovered, err := lastgood.Read(filepath.Join(appdir.Config(), IdentitiesFile), identityValid)
			if err != nil || !bytes.Equal(recovered, beforeIdentity) {
				t.Fatalf("recovery A changed: %s %v", recovered, err)
			}
		})
	}
}
func TestRecoveryHandlesAbsentAndEmptyImages(t *testing.T) {
	home(t)
	defer mustLock(t)()
	p, err := Prepare(IdentitiesFile, Present(nil), identityValid)
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit(Plan{Participants: []Participant{p}}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadImage(IdentitiesFile)
	if err != nil || !got.Exists || len(got.Bytes) != 0 {
		t.Fatalf("empty is not present: %v %v", got, err)
	}
	p, err = Prepare(IdentitiesFile, Image{}, identityValid)
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit(Plan{Participants: []Participant{p}}); err != nil {
		t.Fatal(err)
	}
	got, err = ReadImage(IdentitiesFile)
	if err != nil || got.Exists {
		t.Fatalf("removal not absent: %v %v", got, err)
	}
}
func TestRecoveryRefusesMissingOrChangedBackup(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-write", true: "after-write"}[afterWrite], func(t *testing.T) {
			home(t)
			defer mustLock(t)()
			p := fixture(t)
			p.stage = func(s string) error {
				if (!afterWrite && s == "journal:write") || (afterWrite && s == "providers.json:write") {
					return errors.New("stop")
				}
				return nil
			}
			if err := Commit(p); err == nil {
				t.Fatal("not paused")
			}
			put(t, ProvidersFile+".bak", []byte(`{"external":true}`))
			err := Recover()
			var ambiguous *AmbiguousError
			if !errors.As(err, &ambiguous) {
				t.Fatalf("changed backup was overwritten: %v", err)
			}
			if !bytes.Equal(get(t, ProvidersFile+".bak"), []byte(`{"external":true}`)) {
				t.Fatal("lost external backup")
			}
		})
	}
}
func TestCommitChecksBeforeImagesBeforeJournal(t *testing.T) {
	home(t)
	defer mustLock(t)()
	p := fixture(t)
	put(t, ProvidersFile, []byte(`{"external":true}`))
	if err := Commit(p); !errors.Is(err, ErrUnknown) {
		t.Fatalf("stale plan committed: %v", err)
	}
	if _, err := os.Stat(JournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale plan published journal: %v", err)
	}
}
func TestReadFailuresAndBadJournalsFailClosed(t *testing.T) {
	for _, name := range []string{MetaFile, "lifecycle-journal.json", ProvidersFile} {
		t.Run(name, func(t *testing.T) {
			home(t)
			if err := os.Mkdir(filepath.Join(appdir.Config(), name), 0700); err != nil {
				t.Fatal(err)
			}
			// Unknown never becomes empty: metadata is classified io and its
			// window never validates; a journal blocks Begin; providers (read
			// by the owner, not Begin) is unknown through ReadImage.
			switch name {
			case MetaFile:
				m, err := Begin(Writable)
				if err != nil || m.State() != MetaIO || !errors.Is(m.Validate(), ErrUnknown) {
					t.Fatalf("unknown metadata became usable: %v %v", m.State(), err)
				}
			case ProvidersFile:
				if _, err := ReadImage(name); !errors.Is(err, ErrUnknown) {
					t.Fatalf("unknown providers became empty: %v", err)
				}
			default:
				if _, err := Begin(Writable); !errors.Is(err, ErrUnknown) {
					t.Fatalf("unknown journal ignored: %v", err)
				}
			}
		})
	}
	for _, body := range []string{"", `null`, `{"version":2}`, `{"version":1,"version":1}`, `{"version":1} {}`, `{"version":1,"unexpected":true}`} {
		t.Run(body, func(t *testing.T) {
			home(t)
			put(t, "lifecycle-journal.json", []byte(body))
			defer mustLock(t)()
			if err := Recover(); !errors.Is(err, ErrUnknown) {
				t.Fatalf("bad journal ignored: %v", err)
			}
			if !bytes.Equal(get(t, "lifecycle-journal.json"), []byte(body)) {
				t.Fatal("bad journal changed")
			}
		})
	}
}
func TestLockOpenFailureAndCancellation(t *testing.T) {
	home(t)
	if err := os.Mkdir(filepath.Join(appdir.Config(), "lifecycle.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	u, err := Lock(context.Background())
	if u != nil {
		u()
	}
	if err == nil {
		t.Fatal("continued after lock open failure")
	}
	if err := os.Remove(filepath.Join(appdir.Config(), "lifecycle.lock")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u, err = Lock(ctx)
	if u != nil {
		u()
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("cancelled lock granted: %v", err)
	}
	defer mustLock(t)()
}
func TestMetaMissingAndCorruptBootstrap(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "corrupt"}[bad], func(t *testing.T) {
			home(t)
			defer mustLock(t)()
			put(t, ProvidersFile, beforeProvider)
			if bad {
				put(t, MetaFile, []byte("bad metadata"))
			}
			p, err := Prepare(MetaFile, Present(encoded(t, meta(1, beforeProvider))), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := Commit(Plan{Participants: []Participant{p}}); err != nil {
				t.Fatal(err)
			}
			m, err := ReadMeta()
			if err != nil || m == nil || m.Gen != 1 {
				t.Fatalf("bootstrap failed: %v %v", m, err)
			}
			if _, err := os.Stat(Path() + ".bak"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("metadata got lastgood backup")
			}
			if bad {
				copies, _ := filepath.Glob(Path() + ".bad-*")
				if len(copies) != 1 {
					t.Fatal("bad metadata not kept")
				}
			}
		})
	}
}
func TestJournalRoundTripAndPrivateMode(t *testing.T) {
	home(t)
	defer mustLock(t)()
	p := fixture(t)
	savePlan(t, p)
	j, err := DecodeJournal(get(t, "lifecycle-journal.json.fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Participants) != 3 || !bytes.Equal(j.Participants[0].Before.Bytes, beforeProvider) {
		t.Fatal("journal bytes not preserved")
	}
	p.stage = func(s string) error {
		if s == "journal:write" {
			return errors.New("inspect")
		}
		return nil
	}
	if err := Commit(p); err == nil {
		t.Fatal("not paused")
	}
	fi, err := os.Stat(JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not implement Unix permission bits; writing 0600 is checked
	// by the common path, with real mode asserted where supported.
	if fi.Mode().Perm()&0077 != 0 && os.PathSeparator != '\\' {
		t.Fatalf("journal permissions %v", fi.Mode())
	}
	if strings.Contains((&AmbiguousError{Journal: JournalPath()}).Error(), "fixture-key") {
		t.Fatal("credential in error")
	}
}
