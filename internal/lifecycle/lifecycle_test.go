package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/steady"
)

func home(t *testing.T) {
	t.Helper()
	// Go's GOTMPDIR may be on another volume; child testenv requires every
	// fixture environment path beneath os.TempDir, not the build temp root.
	dir, err := os.MkdirTemp(os.TempDir(), "magpie-lifecycle-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(appdir.Config(), 0700); err != nil {
		t.Fatal(err)
	}
}
func put(t *testing.T, name string, b []byte) {
	t.Helper()
	if err := steady.WriteFile(filepath.Join(appdir.Config(), name), b, 0600); err != nil {
		t.Fatal(err)
	}
}
func get(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(appdir.Config(), name))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func mustLock(t *testing.T) func() {
	t.Helper()
	u, e := Lock(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return u
}
func meta(gen uint64, b []byte) Meta {
	return Meta{Version: Version, Epoch: strings.Repeat("a", 32), Gen: gen, Digest: Digest(b), Providers: map[string]Provider{"a": {Instance: strings.Repeat("b", 32), Rev: gen}}}
}
func encoded(t *testing.T, m Meta) []byte {
	t.Helper()
	b, e := EncodeMeta(m)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

var beforeProvider = []byte(`{"providers":[{"id":"a","key":"fixture-key"}]}`)
var afterProvider = []byte(`{"providers":[{"id":"a","key":"replacement-key"}]}`)
var beforeIdentity = []byte(`{"bound":{"a":"identity_fixture"}}`)
var afterIdentity = []byte(`{}`)

func fixture(t *testing.T) Plan {
	t.Helper()
	put(t, ProvidersFile, beforeProvider)
	put(t, IdentitiesFile, beforeIdentity)
	put(t, MetaFile, encoded(t, meta(1, beforeProvider)))
	put(t, ProvidersFile+".bak", []byte(`{"providers":[]}`))
	put(t, IdentitiesFile+".bak", []byte(`{"profiles":[]}`))
	var ps []Participant
	for _, v := range []struct {
		name string
		b    []byte
	}{{ProvidersFile, afterProvider}, {IdentitiesFile, afterIdentity}, {MetaFile, encoded(t, meta(2, afterProvider))}} {
		p, e := Prepare(v.name, Present(v.b), lastgood.JSON)
		if e != nil {
			t.Fatal(e)
		}
		ps = append(ps, p)
	}
	return Plan{Participants: ps}
}
func checkCommitted(t *testing.T) {
	t.Helper()
	for _, v := range []struct {
		name string
		b    []byte
	}{{ProvidersFile, afterProvider}, {IdentitiesFile, afterIdentity}, {ProvidersFile + ".bak", beforeProvider}, {IdentitiesFile + ".bak", beforeIdentity}} {
		if b := get(t, v.name); !bytes.Equal(b, v.b) {
			t.Errorf("%s wrong generation: got %s want %s", v.name, b, v.b)
		}
	}
	m, e := ReadMeta()
	if e != nil || m == nil || m.Gen != 2 {
		t.Errorf("meta not committed: %v %v", m, e)
	}
	if _, e := os.Stat(JournalPath()); !errors.Is(e, os.ErrNotExist) {
		t.Errorf("journal not retired: %v", e)
	}
}

func TestCommitPublishesAndKeepsPreviousGeneration(t *testing.T) {
	home(t)
	defer mustLock(t)()
	p := fixture(t)
	if e := Commit(p); e != nil {
		t.Fatal(e)
	}
	checkCommitted(t)
}
func TestMetaRejectsUnknownAndBoundsRetired(t *testing.T) {
	home(t)
	if m, e := ReadMeta(); e != nil || m != nil {
		t.Fatalf("missing meta: %v %v", m, e)
	}
	for _, b := range []string{"", `null`, `{}`, `{"version":2}`, `{"version":1,"version":2}`} {
		put(t, MetaFile, []byte(b))
		put(t, MetaFile+".bak", encoded(t, meta(1, beforeProvider)))
		if _, e := ReadMeta(); !errors.Is(e, ErrUnknown) {
			t.Errorf("bad meta %q silently accepted: %v", b, e)
		}
	}
	m := meta(300, beforeProvider)
	for i := 300; i >= 1; i-- {
		m.Retire(Retired{Instance: fmt.Sprintf("%032x", i+1024), Gen: uint64(i), Bound: i%2 == 0})
	}
	if len(m.Retired) != RetiredLimit || m.Retired[0].Gen != 45 || m.Retired[255].Gen != 300 {
		t.Fatalf("retired not bounded oldest-first: %d", len(m.Retired))
	}
	b, e := EncodeMeta(m)
	if e != nil {
		t.Fatal(e)
	}
	r, e := DecodeMeta(b)
	if e != nil || len(r.Retired) != 256 {
		t.Fatalf("roundtrip: %v %v", r, e)
	}
	for name, change := range map[string]func(*Meta){"version": func(m *Meta) { m.Version++ }, "epoch": func(m *Meta) { m.Epoch = "bad" }, "rev": func(m *Meta) { m.Providers["a"] = Provider{Instance: strings.Repeat("b", 32)} }, "duplicate": func(m *Meta) { m.Providers["b"] = m.Providers["a"] }, "retired duplicate": func(m *Meta) { m.Retired = []Retired{{Instance: m.Providers["a"].Instance, Gen: 1}} }, "digest": func(m *Meta) { m.Digest = "bad" }} {
		t.Run(name, func(t *testing.T) {
			m := meta(1, beforeProvider)
			change(&m)
			b, _ := json.Marshal(m)
			if _, e := DecodeMeta(b); !errors.Is(e, ErrUnknown) {
				t.Fatalf("accepted invalid meta: %v", e)
			}
		})
	}
}
func TestLockDoesNotContinueOnTimeout(t *testing.T) {
	home(t)
	u := mustLock(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	v, e := Lock(ctx)
	if v != nil {
		v()
	}
	u()
	if !errors.Is(e, ErrBusy) {
		t.Fatalf("lock continued without ownership: %v", e)
	}
	defer mustLock(t)()
	if _, e := os.Stat(filepath.Join(appdir.Config(), "lifecycle.lock")); e != nil {
		t.Fatal("persistent lock missing", e)
	}
}

var stages = []string{"journal:write", "providers.json:keep:before", "providers.json:keep:after", "providers.json:write", "identities.json:keep:before", "identities.json:keep:after", "identities.json:write", "meta:before", "lifecycle.json:write", "journal:remove:before"}

func TestRecoverEveryStageKeepsBackup(t *testing.T) {
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			home(t)
			defer mustLock(t)()
			p := fixture(t)
			injected := errors.New("injected")
			p.stage = func(s string) error {
				if s == stage {
					return injected
				}
				return nil
			}
			if e := Commit(p); !errors.Is(e, injected) {
				t.Fatalf("stage not reached: %s: %v", stage, e)
			}
			if e := Recover(); e != nil {
				t.Fatal(e)
			}
			if e := Recover(); e != nil {
				t.Fatal(e)
			}
			checkCommitted(t)
		})
	}
}
func TestRecoverThirdStatePreservesAllFiles(t *testing.T) {
	home(t)
	defer mustLock(t)()
	p := fixture(t)
	p.stage = func(s string) error {
		if s == "journal:write" {
			return errors.New("pause")
		}
		return nil
	}
	if e := Commit(p); e == nil {
		t.Fatal("did not pause")
	}
	put(t, IdentitiesFile, []byte(`{"bound":{"other":"external"}}`))
	journal := get(t, "lifecycle-journal.json")
	e := Recover()
	var ambiguous *AmbiguousError
	if !errors.As(e, &ambiguous) {
		t.Fatalf("third state overwritten: %v", e)
	}
	if !bytes.Equal(get(t, ProvidersFile), beforeProvider) || !bytes.Equal(get(t, IdentitiesFile), []byte(`{"bound":{"other":"external"}}`)) || !bytes.Equal(get(t, "lifecycle-journal.json"), journal) {
		t.Fatal("ambiguous recovery changed participants or journal")
	}
	copies, _ := filepath.Glob(filepath.Join(appdir.Config(), IdentitiesFile+".bad-*"))
	if len(copies) == 0 {
		t.Error("no evidence copy for third state")
	}
}
func TestJournalRejectsUnsafePlans(t *testing.T) {
	for name, mutate := range map[string]func(*Journal){
		"path":              func(j *Journal) { j.Participants[0].Path = "../providers.json" },
		"duplicate":         func(j *Journal) { j.Participants = append(j.Participants[:1], j.Participants...) },
		"order":             func(j *Journal) { j.Participants[0], j.Participants[1] = j.Participants[1], j.Participants[0] },
		"meta-first":        func(j *Journal) { j.Participants[0], j.Participants[2] = j.Participants[2], j.Participants[0] },
		"hash":              func(j *Journal) { j.Participants[0].After.Hash = Digest(nil) },
		"absent-with-bytes": func(j *Journal) { j.Participants[0].Before.Exists = false },
		"version":           func(j *Journal) { j.Version++ },
		"op-id":             func(j *Journal) { j.ID = "" },
		"no-backup":         func(j *Journal) { j.Participants[0].Backup = nil },
		"backup-target":     func(j *Journal) { j.Participants[0].Backup.After = Present([]byte(`{}`)) },
		"meta-gen": func(j *Journal) {
			m := meta(1, afterProvider)
			b, _ := json.Marshal(m)
			j.Participants[2].After = Present(b)
		},
		"meta-digest": func(j *Journal) {
			m := meta(2, beforeProvider)
			b, _ := json.Marshal(m)
			j.Participants[2].After = Present(b)
		},
	} {
		t.Run(name, func(t *testing.T) {
			home(t)
			p := fixture(t)
			j := Journal{Version: Version, ID: strings.Repeat("c", 32), Participants: p.Participants}
			mutate(&j)
			b, _ := json.Marshal(j)
			if _, e := DecodeJournal(b); !errors.Is(e, ErrUnknown) {
				t.Fatalf("unsafe journal accepted: %v", e)
			}
		})
	}
}
