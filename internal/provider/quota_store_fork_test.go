package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/testenv"
)

func quotaStoreSubject(id string) QuotaSubject {
	return QuotaSubject{Authority: "authority", Provider: "provider", Principal: id, Generation: "generation"}
}

func quotaStoreObservation(epoch, principal, id string) QuotaObservation {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	return QuotaObservation{
		Subject: quotaStoreSubject(principal), ID: id,
		Expected: QuotaVersion{StoreEpoch: epoch}, Source: "fixture", ObservedAt: now,
		Coverage: []QuotaCoverage{{Window: "week", Scopes: []string{"shared", "models"}}, {Window: "day", Scopes: []string{"models"}}},
		Facts: []QuotaFact{
			{Window: "week", Kind: QuotaReportedAvailable, EvidenceID: id + "-week", Cycle: "cycle", FreshUntil: now.Add(time.Hour), ResetAt: now.Add(2 * time.Hour), Capacity: &QuotaCapacity{Semantics: "included", Unit: "requests", Remaining: 5, Limit: 10}},
			{Window: "day", Kind: QuotaReportedAvailable, EvidenceID: id + "-day", Cycle: "cycle", FreshUntil: now.Add(time.Hour), ResetAt: now.Add(2 * time.Hour), Capacity: &QuotaCapacity{Semantics: "included", Unit: "requests", Remaining: 5, Limit: 10}},
		},
	}
}

func quotaStoreNew(t *testing.T) (*QuotaStore, string) {
	t.Helper()
	store := NewQuotaStore(filepath.Join(t.TempDir(), "state", "quota-state.json"))
	epoch, err := store.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return store, epoch
}

func quotaStoreBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func quotaStoreApply(t *testing.T, store *QuotaStore, observation QuotaObservation) QuotaCommit {
	t.Helper()
	result, err := store.Apply(context.Background(), observation, observation.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Transition == nil || result.Replayed {
		t.Fatalf("not a commit: %+v", result)
	}
	return result
}

func TestQuotaStoreExplicitInitializationAndReadOnly(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "absent", "quota-state.json")
	store := NewQuotaStore(path)
	observation := quotaStoreObservation("unused", "one", "first")
	view, err := store.Read(observation.Subject, "models", observation.ObservedAt)
	if !errors.Is(err, ErrQuotaMissing) || view.State != QuotaStateUnknown {
		t.Fatalf("missing read: %+v, %v", view, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("constructor/read created state: %v, %v", entries, err)
	}
	epoch, err := store.Initialize(context.Background())
	if err != nil || epoch == "" {
		t.Fatalf("initialize: %q, %v", epoch, err)
	}
	before := quotaStoreBytes(t, path)
	again, err := NewQuotaStore(path).Initialize(context.Background())
	if err != nil || again != epoch || !bytes.Equal(before, quotaStoreBytes(t, path)) {
		t.Fatalf("reinitialize: %q, %v", again, err)
	}
	for _, scope := range []string{"models", "uncovered"} {
		view, err = store.Read(observation.Subject, scope, observation.ObservedAt)
		if err != nil || view.State != QuotaStateUnknown || view.Version != (QuotaVersion{StoreEpoch: epoch}) {
			t.Fatalf("empty initialized view: %+v, %v", view, err)
		}
	}
	if runtime.GOOS != "windows" {
		for p, want := range map[string]fs.FileMode{path: 0600, path + ".lock": 0600, filepath.Dir(path): 0700} {
			info, err := os.Stat(p)
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("mode %s: %v, %v", p, info, err)
			}
		}
	}
	// Read remains lock-free even if the lock cannot be opened.
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(observation.Subject, "models", observation.ObservedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read made lock: %v", err)
	}
	if _, err := store.Read(observation.Subject, "", observation.ObservedAt); !errors.Is(err, ErrQuotaInvalid) {
		t.Fatalf("empty scope: %v", err)
	}
	if _, err := store.Read(observation.Subject, "models", time.Time{}); !errors.Is(err, ErrQuotaInvalid) {
		t.Fatalf("zero now: %v", err)
	}
}

func TestQuotaStoreApplyDoesNotInitialize(t *testing.T) {
	store := NewQuotaStore(filepath.Join(t.TempDir(), "absent", "quota-state.json"))
	observation := quotaStoreObservation("unused", "one", "first")
	_, err := store.Apply(context.Background(), observation, observation.ObservedAt)
	if !errors.Is(err, ErrQuotaMissing) {
		t.Fatalf("apply missing: %v", err)
	}
	if _, err := os.Stat(store.path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("apply made state: %v", err)
	}
}

func TestQuotaStoreReplayIsCanonicalDurableAndNotAWrite(t *testing.T) {
	store, epoch := quotaStoreNew(t)
	original := quotaStoreObservation(epoch, "one", "first")
	originalBytes, _ := json.Marshal(original)
	committed := quotaStoreApply(t, store, original)
	if committed.Version.Revision != 1 {
		t.Fatalf("first revision: %+v", committed)
	}
	originalAfter, _ := json.Marshal(original)
	if !bytes.Equal(originalBytes, originalAfter) {
		t.Fatal("mutated input")
	}
	// A different subject's commit cannot evict the retained operation.
	quotaStoreApply(t, store, quotaStoreObservation(epoch, "two", "other"))
	before := quotaStoreBytes(t, store.path)
	replay := original
	replay.Coverage = []QuotaCoverage{{Window: "day", Scopes: []string{"models"}}, {Window: "week", Scopes: []string{"models", "shared"}}}
	replay.Facts = slices.Clone(original.Facts)
	slices.Reverse(replay.Facts)
	zone := time.FixedZone("fixture", 8*60*60)
	replay.ObservedAt = replay.ObservedAt.In(zone)
	for i := range replay.Facts {
		replay.Facts[i].FreshUntil = replay.Facts[i].FreshUntil.In(zone)
		replay.Facts[i].ResetAt = replay.Facts[i].ResetAt.In(zone)
	}
	reopened := NewQuotaStore(store.path)
	writes := 0
	reopened.write = func(string, []byte, fs.FileMode) error { writes++; return errors.New("unexpected replay write") }
	for _, now := range []time.Time{original.ObservedAt.Add(3 * time.Hour), {}, original.ObservedAt.Add(-time.Hour)} {
		result, err := reopened.Apply(context.Background(), replay, now)
		if err != nil || !result.Replayed || result.Transition != nil || result.Version != committed.Version {
			t.Fatalf("retry: %+v, %v", result, err)
		}
	}
	if writes != 0 || !bytes.Equal(before, quotaStoreBytes(t, store.path)) {
		t.Fatal("replay rewrote state")
	}
	view, err := reopened.Read(original.Subject, "models", original.ObservedAt.Add(3*time.Hour))
	if err != nil || view.State != QuotaStateUnknown || view.Version != committed.Version {
		t.Fatalf("expired replay view: %+v, %v", view, err)
	}
	// A snapshot/transition never aliases persisted state or another view.
	committed.Transition.After[0].Windows[0].Capacity.Remaining = 900
	fresh, err := reopened.Read(original.Subject, "models", original.ObservedAt)
	if err != nil || fresh.State != QuotaStateAvailable || fresh.Windows[0].Capacity.Remaining != 5 {
		t.Fatalf("view alias: %+v, %v", fresh, err)
	}
	fresh.Windows[0].Capacity.Remaining = 800
	fresh, _ = reopened.Read(original.Subject, "models", original.ObservedAt)
	if fresh.Windows[0].Capacity.Remaining != 5 {
		t.Fatal("read aliases stored state")
	}
	for name, change := range map[string]func(*QuotaObservation){
		"source":   func(o *QuotaObservation) { o.Source = "changed" },
		"expected": func(o *QuotaObservation) { o.Expected.Revision++ },
		"time":     func(o *QuotaObservation) { o.ObservedAt = o.ObservedAt.Add(time.Second) },
		"evidence": func(o *QuotaObservation) { o.Facts = slices.Clone(o.Facts); o.Facts[0].EvidenceID = "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := original
			change(&changed)
			if _, err := reopened.Apply(context.Background(), changed, changed.ObservedAt); !errors.Is(err, ErrQuotaConflict) {
				t.Fatalf("changed replay: %v", err)
			}
		})
	}
	malformed := original
	malformed.Source = ""
	if _, err := reopened.Apply(context.Background(), malformed, original.ObservedAt); !errors.Is(err, ErrQuotaInvalid) {
		t.Fatalf("malformed replay: %v", err)
	}
	next := quotaStoreObservation(epoch, "one", "second")
	next.Expected = committed.Version
	next.ObservedAt = next.ObservedAt.Add(time.Minute)
	for i := range next.Facts {
		next.Facts[i].Kind = QuotaConfirmedExhaustion
		next.Facts[i].Capacity = nil
		next.Facts[i].FreshUntil = time.Time{}
	}
	quotaStoreApply(t, store, next)
	if _, err := store.Apply(context.Background(), original, original.ObservedAt.Add(24*time.Hour)); !errors.Is(err, ErrQuotaConflict) {
		t.Fatalf("older replay: %v", err)
	}
	held, err := NewQuotaStore(store.path).Read(original.Subject, "models", original.ObservedAt.Add(24*time.Hour))
	if err != nil || held.State != QuotaStateExhausted || !held.RecheckDue {
		t.Fatalf("restarted exhaustion: %+v, %v", held, err)
	}
}

func TestQuotaStoreFailedWritePreservesBytesAndVersion(t *testing.T) {
	store, epoch := quotaStoreNew(t)
	observation := quotaStoreObservation(epoch, "one", "first")
	quotaStoreApply(t, store, observation)
	before := quotaStoreBytes(t, store.path)
	failure := errors.New("fixture before rename")
	store.write = func(path string, b []byte, mode fs.FileMode) error {
		temp, err := os.CreateTemp(filepath.Dir(path), ".quota-failure-*")
		if err != nil {
			return err
		}
		defer os.Remove(temp.Name())
		if _, err := temp.Write(b); err != nil {
			temp.Close()
			return err
		}
		if err := temp.Chmod(mode); err != nil {
			temp.Close()
			return err
		}
		if err := temp.Sync(); err != nil {
			temp.Close()
			return err
		}
		if err := temp.Close(); err != nil {
			return err
		}
		return failure // All new bytes reached the temp file, but not the rename.
	}
	next := quotaStoreObservation(epoch, "one", "second")
	next.Expected.Revision = 1
	next.ObservedAt = next.ObservedAt.Add(time.Minute)
	result, err := store.Apply(context.Background(), next, next.ObservedAt)
	if !errors.Is(err, ErrQuotaIO) || !errors.Is(err, failure) || !reflect.DeepEqual(result, QuotaCommit{}) {
		t.Fatalf("failed commit: %+v, %v", result, err)
	}
	if !bytes.Equal(before, quotaStoreBytes(t, store.path)) {
		t.Fatal("failed write replaced old bytes")
	}
	view, err := NewQuotaStore(store.path).Read(next.Subject, "models", next.ObservedAt)
	if err != nil || view.Version.Revision != 1 {
		t.Fatalf("failed revision: %+v, %v", view, err)
	}
	store.write = nil
	if result := quotaStoreApply(t, store, next); result.Version.Revision != 2 {
		t.Fatalf("retry after failure: %+v", result)
	}
	absent := NewQuotaStore(filepath.Join(t.TempDir(), "quota-state.json"))
	absent.write = func(string, []byte, fs.FileMode) error { return failure }
	if _, err := absent.Initialize(context.Background()); !errors.Is(err, ErrQuotaIO) {
		t.Fatalf("failed init: %v", err)
	}
	if _, err := os.Stat(absent.path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed init created data: %v", err)
	}
}

func TestQuotaStoreRejectsWholeCorruptOrUnsupportedFile(t *testing.T) {
	store, epoch := quotaStoreNew(t)
	observation := quotaStoreObservation(epoch, "one", "first")
	quotaStoreApply(t, store, observation)
	quotaStoreApply(t, store, quotaStoreObservation(epoch, "two", "second"))
	good := string(quotaStoreBytes(t, store.path))
	mutated := func(change func(*quotaFile)) string {
		var data quotaFile
		if err := json.Unmarshal([]byte(good), &data); err != nil {
			t.Fatal(err)
		}
		change(&data)
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cases := map[string]string{
		"truncated": good[:len(good)-1], "trailing": good + "{}", "empty": "", "null": "null",
		"newer":                strings.Replace(good, `"version":1`, `"version":2`, 1),
		"duplicate-version":    strings.Replace(good, `"version":1`, `"version":1,"version":1`, 1),
		"escaped-duplicate":    strings.Replace(good, `"version":1`, `"version":1,"\u0076ersion":1`, 1),
		"case-alias":           strings.Replace(good, `"version":1`, `"Version":1`, 1),
		"nested-case":          strings.Replace(good, `"state":`, `"State":`, 1),
		"nested-duplicate":     strings.Replace(good, `"remaining":5`, `"remaining":5,"remaining":5`, 1),
		"unknown":              strings.Replace(good, `"version":1`, `"version":1,"extra":true`, 1),
		"nested-unknown":       strings.Replace(good, `"remaining":5`, `"remaining":5,"extra":true`, 1),
		"missing-zero":         strings.Replace(good, `"commit_sequence":2,`, ``, 1),
		"null-capacity":        strings.Replace(good, `"capacity":{"semantics":"included","unit":"requests","remaining":5,"limit":10}`, `"capacity":null`, 1),
		"wrong-enum":           mutated(func(d *quotaFile) { d.Records[1].Windows[0].State = "free" }),
		"invalid-other-record": mutated(func(d *quotaFile) { d.Records[1].Subject.Principal = "" }),
		"duplicate-record":     mutated(func(d *quotaFile) { d.Records = append(d.Records, d.Records[0]); d.CommitSequence++ }),
		"duplicate-window":     mutated(func(d *quotaFile) { d.Records[1].Windows = append(d.Records[1].Windows, d.Records[1].Windows[0]) }),
		"invalid-digest":       mutated(func(d *quotaFile) { d.Records[1].Digest = "not-a-digest" }),
		"future-revision":      mutated(func(d *quotaFile) { d.Records[1].Windows[0].Revision = 100 }),
		"bad-sequence":         mutated(func(d *quotaFile) { d.CommitSequence++ }),
		"bad-time":             mutated(func(d *quotaFile) { d.Records[1].Windows[0].FreshUntil = d.Records[1].Windows[0].ObservedAt }),
		"invalid-utf8":         good[:len(good)-1] + string([]byte{0xff}) + "}",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "quota-state.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".bak", []byte(good), 0600); err != nil {
				t.Fatal(err)
			}
			store := NewQuotaStore(path)
			want := error(ErrQuotaCorrupt)
			if name == "newer" {
				want = ErrQuotaUnsupported
			}
			view, err := store.Read(observation.Subject, "models", observation.ObservedAt)
			if !errors.Is(err, want) || view.State != QuotaStateUnknown {
				t.Fatalf("read bad: %+v, %v; want %v", view, err, want)
			}
			if _, err := store.Initialize(context.Background()); !errors.Is(err, want) {
				t.Fatalf("initialize bad: %v", err)
			}
			if _, err := store.Apply(context.Background(), observation, observation.ObservedAt); !errors.Is(err, want) {
				t.Fatalf("apply bad: %v", err)
			}
			if string(quotaStoreBytes(t, path)) != content {
				t.Fatal("bad bytes overwritten")
			}
		})
	}
}

func TestQuotaStoreIOAndPrivacy(t *testing.T) {
	directory := t.TempDir()
	store := NewQuotaStore(directory)
	observation := quotaStoreObservation("epoch", "one", "first")
	view, err := store.Read(observation.Subject, "models", observation.ObservedAt)
	if !errors.Is(err, ErrQuotaIO) || view.State != QuotaStateUnknown || strings.Contains(err.Error(), directory) {
		t.Fatalf("read I/O: %+v, %v", view, err)
	}
	store, epoch := quotaStoreNew(t)
	observation.Expected.StoreEpoch = epoch
	// No provider/account/config reads are needed even if those files contain secrets.
	for _, secret := range []string{"Bearer fixture-private-key", "contact fixture-person@example.invalid", "raw vendor response"} {
		if err := os.WriteFile(store.path+".bak", []byte(secret), 0600); err != nil {
			t.Fatal(err)
		}
		malformed := observation
		malformed.Source = secret
		if _, err := store.Apply(context.Background(), malformed, malformed.ObservedAt); !errors.Is(err, ErrQuotaInvalid) || strings.Contains(err.Error(), secret) {
			t.Fatalf("private validation error: %v", err)
		}
	}
	// Fresh store avoids conflating allowed opaque IDs with secrecy certification.
	store, epoch = quotaStoreNew(t)
	quotaStoreApply(t, store, quotaStoreObservation(epoch, "one", "first"))
	bytes := quotaStoreBytes(t, store.path)
	for _, private := range []string{"Bearer", "@", "raw-vendor-response", "api_key", "authorization"} {
		if strings.Contains(string(bytes), private) {
			t.Fatalf("private field in normalized state: %s", private)
		}
	}
	corrupt := []byte(`{"version":1,"secret":"Bearer fixture-private-key"}`)
	if err := os.WriteFile(store.path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(observation.Subject, "models", observation.ObservedAt)
	if !errors.Is(err, ErrQuotaCorrupt) || strings.Contains(err.Error(), "fixture-private-key") {
		t.Fatalf("corrupt error exposed bytes: %v", err)
	}
}

func TestQuotaStoreConcurrentInitializeAndSubjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "quota-state.json")
	const count = 12
	type initialized struct {
		epoch string
		err   error
	}
	started := make(chan struct{})
	inits := make(chan initialized, count)
	for range count {
		go func() {
			<-started
			epoch, err := NewQuotaStore(path).Initialize(context.Background())
			inits <- initialized{epoch, err}
		}()
	}
	close(started)
	epoch := ""
	for range count {
		result := <-inits
		if result.err != nil {
			t.Error(result.err)
			continue
		}
		if epoch == "" {
			epoch = result.epoch
		} else if result.epoch != epoch {
			t.Errorf("competing initialization: %q vs %q", epoch, result.epoch)
		}
	}
	if t.Failed() {
		return
	}
	started = make(chan struct{})
	results := make(chan error, count)
	for i := range count {
		go func() {
			<-started
			observation := quotaStoreObservation(epoch, fmt.Sprintf("subject-%d", i), "first")
			_, err := NewQuotaStore(path).Apply(context.Background(), observation, observation.ObservedAt)
			results <- err
		}()
	}
	close(started)
	for range count {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	data, err := readQuotaFile(path)
	if err != nil || len(data.Records) != count || data.CommitSequence != count {
		t.Fatalf("lost subjects: %+v, %v", data, err)
	}
	// On one subject both writers acquired their evidence at revision zero.
	started = make(chan struct{})
	for i := range 2 {
		go func() {
			<-started
			observation := quotaStoreObservation(epoch, "same", fmt.Sprintf("same-%d", i))
			_, err := NewQuotaStore(path).Apply(context.Background(), observation, observation.ObservedAt)
			results <- err
		}()
	}
	close(started)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrQuotaConflict) {
			conflicts++
		} else {
			t.Error(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS: %d successes, %d conflicts", successes, conflicts)
	}
	observation := quotaStoreObservation(epoch+"-different", "new", "first")
	if _, err := NewQuotaStore(path).Apply(context.Background(), observation, observation.ObservedAt); !errors.Is(err, ErrQuotaConflict) {
		t.Fatalf("epoch CAS: %v", err)
	}
}

func TestQuotaStoreBothLocksAreMandatoryAndCancellable(t *testing.T) {
	store, epoch := quotaStoreNew(t)
	observation := quotaStoreObservation(epoch, "one", "first")
	before := quotaStoreBytes(t, store.path)
	target, err := quotaTarget(store.path)
	if err != nil {
		t.Fatal(err)
	}
	localRelease, err := quotaLocalLock(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer localRelease()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := store.Apply(ctx, observation, observation.ObservedAt)
		if !errors.Is(err, ErrQuotaBusy) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("local ownership: %v", err)
		}
	}()
	lock, err := os.OpenFile(target+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	held, err := filelock.TryLock(lock)
	if err != nil || !held {
		t.Fatalf("test OS ownership: %v, %v", held, err)
	}
	func() {
		defer filelock.Unlock(lock)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := store.Apply(ctx, observation, observation.ObservedAt)
		if !errors.Is(err, ErrQuotaBusy) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("OS ownership: %v", err)
		}
		// A read must not wait behind a writer.
		view, err := store.Read(observation.Subject, "models", observation.ObservedAt)
		if err != nil || view.State != QuotaStateUnknown {
			t.Fatalf("read under lock: %+v, %v", view, err)
		}
	}()
	if !bytes.Equal(before, quotaStoreBytes(t, store.path)) {
		t.Fatal("blocked lock wrote")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Apply(ctx, observation, observation.ObservedAt); !errors.Is(err, ErrQuotaBusy) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ownership: %v", err)
	}
	quotaStoreApply(t, store, observation)
	if _, err := os.Stat(target + ".lock"); err != nil {
		t.Fatalf("lock file removed: %v", err)
	}
	// Opening the persistent lock must not silently fall back to unlocked writes.
	lock.Close()
	if err := os.Remove(target + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	before = quotaStoreBytes(t, store.path)
	if _, err := store.Initialize(context.Background()); !errors.Is(err, ErrQuotaIO) {
		t.Fatalf("lock I/O initialize: %v", err)
	}
	if _, err := store.Apply(context.Background(), observation, observation.ObservedAt); !errors.Is(err, ErrQuotaIO) {
		t.Fatalf("lock I/O apply: %v", err)
	}
	if !bytes.Equal(before, quotaStoreBytes(t, store.path)) {
		t.Fatal("lock I/O failure wrote")
	}
}

func TestQuotaStoreCanonicalSymlinkOwnership(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		t.Run(fmt.Sprintf("dangling-%v", dangling), func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "target")
			if !dangling {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			aliasDirectory := filepath.Join(root, "directory-link")
			if err := os.Symlink(directory, aliasDirectory); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			path := filepath.Join(directory, "nested", "quota-state.json")
			alias := filepath.Join(root, "file-link.json")
			if err := os.Symlink(path, alias); err != nil {
				t.Fatal(err)
			}
			aliases := []string{path, alias, filepath.Join(aliasDirectory, "nested", "quota-state.json")}
			canonical, err := quotaTarget(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, spelling := range aliases {
				got, err := quotaTarget(spelling)
				if err != nil || got != canonical {
					t.Fatalf("canonical %s: %q, %v; want %q", spelling, got, err, canonical)
				}
			}
			_, release, err := NewQuotaStore(alias).lock(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer release()
				for _, spelling := range aliases {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
					_, err := NewQuotaStore(spelling).Initialize(ctx)
					cancel()
					if !errors.Is(err, ErrQuotaBusy) {
						t.Fatalf("alias escaped ownership: %s: %v", spelling, err)
					}
				}
			}()
			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("lock created data: %v", err)
			}
			epoch, err := NewQuotaStore(alias).Initialize(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			observation := quotaStoreObservation(epoch, "one", "first")
			quotaStoreApply(t, NewQuotaStore(aliases[2]), observation)
			for _, spelling := range aliases {
				got, err := quotaTarget(spelling)
				if err != nil || got != canonical {
					t.Fatalf("canonical after creation: %q, %v", got, err)
				}
				view, err := NewQuotaStore(spelling).Read(observation.Subject, "models", observation.ObservedAt)
				if err != nil || view.State != QuotaStateAvailable {
					t.Fatalf("alias read: %+v, %v", view, err)
				}
			}
			for _, link := range []string{alias, aliasDirectory} {
				info, err := os.Lstat(link)
				if err != nil || info.Mode()&fs.ModeSymlink == 0 {
					t.Fatalf("replaced link: %s, %v", link, err)
				}
			}
			if _, err := os.Stat(alias + ".lock"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("lock at noncanonical alias: %v", err)
			}
		})
	}
}

// The child goes through kiro_main_test.go's real TestMain and inherits its
// sandbox marker and TEMP/TMP. It never creates a second custom TestMain path.
func TestQuotaStoreProcessHelper(t *testing.T) {
	at := slices.Index(os.Args, "--quota-store-helper")
	if at < 0 {
		return
	}
	args := os.Args[at+1:]
	if len(args) != 4 {
		t.Fatal("helper args")
	}
	mode, path, ready, principal := args[0], args[1], args[2], args[3]
	if os.Getenv(testenv.Marker) == "" {
		t.Fatal("helper escaped test sandbox")
	}
	switch mode {
	case "hold":
		file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		held, err := filelock.TryLock(file)
		if err != nil || !held {
			t.Fatalf("child lock: %v, %v", held, err)
		}
		defer filelock.Unlock(file)
		if err := os.WriteFile(ready, []byte("held"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatal(err)
		}
	case "apply":
		if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatal(err)
		}
		store := NewQuotaStore(path)
		epoch, err := store.Initialize(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		observation := quotaStoreObservation(epoch, principal, "child-"+principal)
		if _, err := store.Apply(context.Background(), observation, observation.ObservedAt); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unknown helper mode")
	}
}

func quotaStoreStartChild(t *testing.T, mode, path, principal string) (io.WriteCloser, func()) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestQuotaStoreProcessHelper$", "--", "--quota-store-helper", mode, path, ready, principal)
	cmd.Env = os.Environ()
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		cancel()
		t.Fatal(err)
	}
	var once sync.Once
	var waitErr error
	wait := func() { once.Do(func() { input.Close(); waitErr = cmd.Wait(); cancel() }) }
	t.Cleanup(func() { cancel(); wait() })
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-timer.C:
			cancel()
			wait()
			t.Fatalf("child readiness: %v: %s", waitErr, output.String())
		case <-tick.C:
		}
	}
	return input, func() {
		t.Helper()
		wait()
		if waitErr != nil {
			t.Fatalf("child failed: %v: %s", waitErr, output.String())
		}
	}
}

func TestQuotaStoreProcessOwnershipAndConcurrentInitialization(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "quota-state.json")
	_, release := quotaStoreStartChild(t, "hold", path, "unused")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	_, err := NewQuotaStore(path).Initialize(ctx)
	cancel()
	if !errors.Is(err, ErrQuotaBusy) {
		t.Fatalf("initialize escaped child lock: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("blocked init wrote state: %v", err)
	}
	release()
	firstIn, firstDone := quotaStoreStartChild(t, "apply", path, "first")
	secondIn, secondDone := quotaStoreStartChild(t, "apply", path, "second")
	firstIn.Close()
	secondIn.Close()
	firstDone()
	secondDone()
	data, err := readQuotaFile(path)
	if err != nil || len(data.Records) != 2 || data.CommitSequence != 2 {
		t.Fatalf("process commits lost: %+v, %v", data, err)
	}
	before := quotaStoreBytes(t, path)
	_, release = quotaStoreStartChild(t, "hold", path, "unused")
	observation := quotaStoreObservation(data.Epoch, "third", "first")
	ctx, cancel = context.WithTimeout(context.Background(), 80*time.Millisecond)
	_, err = NewQuotaStore(path).Apply(ctx, observation, observation.ObservedAt)
	cancel()
	if !errors.Is(err, ErrQuotaBusy) || !bytes.Equal(before, quotaStoreBytes(t, path)) {
		t.Fatalf("apply escaped child lock: %v", err)
	}
	release()
	quotaStoreApply(t, NewQuotaStore(path), observation)
}

func TestQuotaStoreProcessAliasesShareOwnership(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "quota-state.json")
	alias := filepath.Join(root, "alias.json")
	if err := os.Symlink(path, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, done := quotaStoreStartChild(t, "hold", path, "unused")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	_, err := NewQuotaStore(alias).Initialize(ctx)
	cancel()
	if !errors.Is(err, ErrQuotaBusy) {
		t.Fatalf("dangling alias escaped OS lock: %v", err)
	}
	done()
	// Initializers entering via a dangling alias and its direct target must agree
	// on one epoch and preserve both commits across the first atomic rename.
	firstIn, firstDone := quotaStoreStartChild(t, "apply", alias, "first")
	secondIn, secondDone := quotaStoreStartChild(t, "apply", path, "second")
	firstIn.Close()
	secondIn.Close()
	firstDone()
	secondDone()
	data, err := readQuotaFile(path)
	if err != nil || len(data.Records) != 2 || data.CommitSequence != 2 {
		t.Fatalf("alias process commits: %+v, %v", data, err)
	}
	before := quotaStoreBytes(t, path)
	_, done = quotaStoreStartChild(t, "hold", path, "unused")
	observation := quotaStoreObservation(data.Epoch, "third", "third")
	ctx, cancel = context.WithTimeout(context.Background(), 60*time.Millisecond)
	_, err = NewQuotaStore(alias).Apply(ctx, observation, observation.ObservedAt)
	cancel()
	if !errors.Is(err, ErrQuotaBusy) || !bytes.Equal(before, quotaStoreBytes(t, path)) {
		t.Fatalf("existing alias escaped OS lock: %v", err)
	}
	done()
	quotaStoreApply(t, NewQuotaStore(alias), observation)
	info, err := os.Lstat(alias)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("process replaced alias: %v", err)
	}
}

func TestQuotaStoreRefusalAnchorSurvivesTelemetryAndRestart(t *testing.T) {
	store, epoch := quotaStoreNew(t)
	refusal := quotaStoreObservation(epoch, "one", "refusal")
	for i := range refusal.Facts {
		refusal.Facts[i].Kind = QuotaConfirmedExhaustion
		refusal.Facts[i].Capacity = nil
		refusal.Facts[i].FreshUntil = time.Time{}
	}
	committed := quotaStoreApply(t, store, refusal)
	for step, kind := range []QuotaEvidence{QuotaReportedFull, QuotaReportedAvailable} {
		telemetry := quotaStoreObservation(epoch, "one", fmt.Sprintf("telemetry-%d", step))
		telemetry.Expected = committed.Version
		telemetry.ObservedAt = telemetry.ObservedAt.Add(time.Duration(step+1) * time.Minute)
		for i := range telemetry.Facts {
			telemetry.Facts[i].Kind = kind
			if kind == QuotaReportedFull {
				telemetry.Facts[i].Capacity.Remaining = 0
				telemetry.Facts[i].FreshUntil = time.Time{}
			}
		}
		committed = quotaStoreApply(t, NewQuotaStore(store.path), telemetry)
		view, err := NewQuotaStore(store.path).Read(telemetry.Subject, "models", telemetry.ObservedAt)
		if err != nil || view.State != QuotaStateExhausted {
			t.Fatalf("telemetry cleared refusal: %+v, %v", view, err)
		}
		for _, window := range view.Windows {
			if window.Blocker == nil || window.Blocker.ID != "refusal" || window.Blocker.Revision != 1 {
				t.Fatalf("lost durable anchor: %+v", window)
			}
		}
	}
	recovery := quotaStoreObservation(epoch, "one", "recovery")
	recovery.Expected = committed.Version
	recovery.ObservedAt = recovery.ObservedAt.Add(3 * time.Minute)
	recovery.Facts = recovery.Facts[1:]
	recovery.Facts[0].Kind = QuotaConfirmedRecovery
	recovery.Facts[0].Cycle = "new-cycle"
	recovery.Facts[0].Recovery = &QuotaRecovery{BlockerID: "refusal", BlockerRevision: 1, PriorCycle: "cycle", Basis: "cycle", Provenance: "fixture-cycle"}
	wrong := recovery
	wrong.Facts = slices.Clone(recovery.Facts)
	badReference := *recovery.Facts[0].Recovery
	badReference.BlockerID = "telemetry-0"
	wrong.Facts[0].Recovery = &badReference
	if _, err := store.Apply(context.Background(), wrong, wrong.ObservedAt); !errors.Is(err, ErrQuotaConflict) {
		t.Fatalf("wrong blocker accepted: %v", err)
	}
	quotaStoreApply(t, store, recovery)
	view, err := NewQuotaStore(store.path).Read(recovery.Subject, "models", recovery.ObservedAt)
	if err != nil || view.State != QuotaStateExhausted {
		t.Fatalf("partial recovery cleared every window: %+v, %v", view, err)
	}
	for _, window := range view.Windows {
		if window.Window == "day" && (window.State != QuotaStateAvailable || window.Blocker != nil) {
			t.Fatalf("day not recovered: %+v", window)
		}
		if window.Window == "week" && (window.State != QuotaStateExhausted || window.Blocker == nil) {
			t.Fatalf("omitted week lost: %+v", window)
		}
	}
	for _, changed := range []QuotaSubject{
		{Authority: "another", Provider: "provider", Principal: "one", Generation: "generation"},
		{Authority: "authority", Provider: "another", Principal: "one", Generation: "generation"},
		{Authority: "authority", Provider: "provider", Principal: "another", Generation: "generation"},
		{Authority: "authority", Provider: "provider", Principal: "one", Generation: "another"},
	} {
		view, err := store.Read(changed, "models", recovery.ObservedAt)
		if err != nil || view.State != QuotaStateUnknown || view.Version.Revision != 0 {
			t.Fatalf("incarnation inherited old state: %+v, %v", view, err)
		}
	}
}

func TestQuotaStoreCanonicalParentAfterSymlink(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "real", "nested")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "directory-link")
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	// Do not filepath.Join: cleaning '..' before resolving directory-link
	// selects root/quota-state.json, not real/quota-state.json.
	linkedPath := linkDir + string(filepath.Separator) + ".." + string(filepath.Separator) + "quota-state.json"
	alias := filepath.Join(root, "file-link.json")
	relativeLink := filepath.Base(linkDir) + string(filepath.Separator) + ".." + string(filepath.Separator) + "quota-state.json"
	if err := os.Symlink(relativeLink, alias); err != nil {
		t.Fatal(err)
	}
	direct := filepath.Join(root, "real", "quota-state.json")
	want, err := quotaTarget(direct)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{linkedPath, alias} {
		got, err := quotaTarget(path)
		if err != nil || got != want {
			t.Fatalf("parent after symlink %q: got %q, %v; want %q", path, got, err, want)
		}
	}
	epoch, err := NewQuotaStore(alias).Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	observation := quotaStoreObservation(epoch, "one", "first")
	quotaStoreApply(t, NewQuotaStore(direct), observation)
	for _, path := range []string{direct, linkedPath, alias} {
		view, err := NewQuotaStore(path).Read(observation.Subject, "models", observation.ObservedAt)
		if err != nil || view.State != QuotaStateAvailable {
			t.Fatalf("parent alias lost state: %+v, %v", view, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "quota-state.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("wrote lexically cleaned target: %v", err)
	}
}
