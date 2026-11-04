package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var quotaT0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func quotaPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "quota.json")
}

func quotaWant(t *testing.T, s *QuotaStore, key string, at time.Time, want QuotaAdmit) QuotaEntry {
	t.Helper()
	got, e := s.Admit(key, at)
	if got != want {
		t.Fatalf("Admit(%q, %v) = %v (%+v), want %v", key, at, got, e, want)
	}
	return e
}

// A key never refused passes, and nothing is written for it, a success
// included: the store costs a stat on the path every request takes.
func TestQuotaNeverRefusedPasses(t *testing.T) {
	path := quotaPath(t)
	s := NewQuotaStore(path)
	e := quotaWant(t, s, "relay", quotaT0, QuotaPass)
	if err := s.Recover("relay", e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a success with nothing kept wrote the file: %v", err)
	}
	if got, err := s.Read("relay", quotaT0); err != nil || got.State != QuotaNone {
		t.Fatalf("Read = %+v, %v", got, err)
	}
}

// A refused key is held until its reset. At the reset one request is
// handed the try and the others are held; its refusal holds the key to
// the next reset, and its success lets every request through.
func TestQuotaHeldUntilResetThenOneTry(t *testing.T) {
	s := NewQuotaStore(quotaPath(t))
	reset := quotaT0.Add(time.Hour)
	if err := s.Exhaust("relay", "quota", reset, quotaT0); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "relay", reset.Add(-time.Nanosecond), QuotaHold)
	try := quotaWant(t, s, "relay", reset, QuotaTry)
	quotaWant(t, s, "relay", reset.Add(time.Second), QuotaHold)
	quotaWant(t, s, "other", reset, QuotaPass)

	next := reset.Add(time.Hour)
	if err := s.Exhaust("relay", "quota", next, reset.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "relay", next.Add(-time.Nanosecond), QuotaHold)
	try = quotaWant(t, s, "relay", next, QuotaTry)
	if err := s.Recover("relay", try); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "relay", next, QuotaPass)
	quotaWant(t, s, "relay", next.Add(time.Second), QuotaPass)
}

// A try that never answers holds the others back for QuotaTryHold, and
// then one more request may try.
func TestQuotaUnansweredTryEnds(t *testing.T) {
	s := NewQuotaStore(quotaPath(t))
	if err := s.Exhaust("relay", "credit", quotaT0, quotaT0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "relay", quotaT0, QuotaTry)
	quotaWant(t, s, "relay", quotaT0.Add(QuotaTryHold-time.Nanosecond), QuotaHold)
	quotaWant(t, s, "relay", quotaT0.Add(QuotaTryHold), QuotaTry)
}

// Refusals written at once, by two stores of one file and from many
// goroutines, are all kept, each key at the later of its resets. A refusal
// seen a little earlier, with a later reset, still moves the reset on.
func TestQuotaConcurrentRefusalsAllKept(t *testing.T) {
	path := quotaPath(t)
	a, b := NewQuotaStore(path), NewQuotaStore(path)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			s, k := a, fmt.Sprintf("k%d", i%8)
			if i%2 == 1 {
				s = b
			}
			if err := s.Exhaust(k, "quota", quotaT0.Add(time.Duration(i)*time.Minute), quotaT0); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	fresh := NewQuotaStore(path)
	for k := range 8 {
		e, err := fresh.Read(fmt.Sprintf("k%d", k), quotaT0)
		if want := quotaT0.Add(time.Duration(k+8) * time.Minute); err != nil || e.State != QuotaExhausted || !e.ResetAt.Equal(want) {
			t.Fatalf("k%d: %+v, %v; want exhausted until %v", k, e, err, want)
		}
	}
	if err := a.Exhaust("k0", "credit", quotaT0.Add(time.Hour), quotaT0.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if e, _ := fresh.Read("k0", quotaT0); !e.ResetAt.Equal(quotaT0.Add(time.Hour)) || !e.ObservedAt.Equal(quotaT0) || e.Reason != "credit" {
		t.Fatalf("an earlier sighting with a later reset: %+v", e)
	}
}

// A success clears only the refusal it was admitted under: one written
// after its Admit is newer than the success and stays, and a request let
// through before any refusal can't clear one.
func TestQuotaRecoverKeepsANewerRefusal(t *testing.T) {
	s := NewQuotaStore(quotaPath(t))
	if err := s.Exhaust("relay", "quota", quotaT0, quotaT0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	try := quotaWant(t, s, "relay", quotaT0, QuotaTry)
	if err := s.Exhaust("relay", "quota", quotaT0.Add(time.Hour), quotaT0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover("relay", try); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "relay", quotaT0.Add(time.Minute), QuotaHold)

	pass := quotaWant(t, s, "fresh", quotaT0, QuotaPass)
	if err := s.Exhaust("fresh", "quota", quotaT0.Add(time.Hour), quotaT0); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover("fresh", pass); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "fresh", quotaT0.Add(time.Minute), QuotaHold)
}

// Only the request handed the try ends it. Another, let through before
// the refusal and served after, neither clears the refusal nor hands out
// a second try.
func TestQuotaOnlyTheTryEndsIt(t *testing.T) {
	s := NewQuotaStore(quotaPath(t))
	early := quotaWant(t, s, "relay", quotaT0.Add(-2*time.Hour), QuotaPass)
	if err := s.Exhaust("relay", "quota", quotaT0, quotaT0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	try := quotaWant(t, s, "relay", quotaT0, QuotaTry)
	if err := s.Recover("relay", early); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "relay", quotaT0.Add(time.Second), QuotaHold)
	if err := s.Recover("relay", try); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "relay", quotaT0.Add(time.Second), QuotaPass)
}

// An entry that can't be read is its own key's unknown, and the others
// stand. Unknown is never credit: it gets one try, as a reset passed, and
// the bad entry's bytes are kept until its key is written.
func TestQuotaBadEntryIsItsKeysUnknown(t *testing.T) {
	path := quotaPath(t)
	good := fmt.Sprintf(`{"state":"exhausted","reason":"quota","resetAt":%q,"observedAt":%q,"seq":3}`,
		quotaT0.Add(time.Hour).Format(time.RFC3339Nano), quotaT0.Format(time.RFC3339Nano))
	raw := `{"version":1,"seq":5,"entries":{"good":` + good + `,"bad":{"state":"exhausted"},"later":` + strings.Replace(good, `"seq":3`, `"seq":9`, 1) + `,"odd":[1]}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewQuotaStore(path)
	if e, err := s.Read("good", quotaT0); err != nil || e.State != QuotaExhausted {
		t.Fatalf("good: %+v, %v", e, err)
	}
	for _, k := range []string{"bad", "later", "odd"} {
		if e, err := s.Read(k, quotaT0); !errors.Is(err, ErrQuotaCorrupt) || e.State != QuotaUnknown {
			t.Fatalf("%s: %+v, %v; want unknown", k, e, err)
		}
	}
	quotaWant(t, s, "good", quotaT0, QuotaHold)
	try := quotaWant(t, s, "bad", quotaT0, QuotaTry)
	quotaWant(t, s, "bad", quotaT0, QuotaHold)
	quotaWant(t, s, "never", quotaT0, QuotaPass)
	if err := s.Recover("bad", try); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "bad", quotaT0, QuotaPass)
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `"odd":[1]`) || !strings.Contains(string(b), `"seq":9`) {
		t.Fatalf("the other bad entries' bytes were not kept: %s, %v", b, err)
	}
	quotaWant(t, s, "good", quotaT0, QuotaHold)
}

// A file that doesn't parse makes every key unknown, each with its one
// try. The first write moves it aside, as logins.json is, and the file
// written in its place still holds every key it doesn't name unknown, so
// a refusal kept only in the bad file is not read as credit.
func TestQuotaBadFileIsUnknownUntilWritten(t *testing.T) {
	for _, bad := range []string{`{"version":1,`, `{}`, `null`, "\x00"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			path := quotaPath(t)
			if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			s := NewQuotaStore(path)
			if e, err := s.Read("a", quotaT0); !errors.Is(err, ErrQuotaCorrupt) || e.State != QuotaUnknown {
				t.Fatalf("Read = %+v, %v; want unknown", e, err)
			}
			try := quotaWant(t, s, "a", quotaT0, QuotaTry)
			quotaWant(t, s, "a", quotaT0, QuotaHold)
			quotaWant(t, s, "b", quotaT0, QuotaTry)
			if err := s.Recover("a", try); err != nil {
				t.Fatal(err)
			}
			aside, _ := filepath.Glob(path + ".bad-*")
			if len(aside) != 1 {
				t.Fatalf("moved aside: %v", aside)
			}
			if b, _ := os.ReadFile(aside[0]); string(b) != bad {
				t.Fatalf("the bad file's bytes: %q", b)
			}
			fresh := NewQuotaStore(path)
			quotaWant(t, fresh, "a", quotaT0, QuotaPass)
			if e, err := fresh.Read("c", quotaT0); !errors.Is(err, ErrQuotaCorrupt) || e.State != QuotaUnknown {
				t.Fatalf("a key the new file doesn't name: %+v, %v; want unknown", e, err)
			}
			if err := fresh.Exhaust("c", "quota", quotaT0.Add(time.Hour), quotaT0); err != nil {
				t.Fatal(err)
			}
			quotaWant(t, fresh, "c", quotaT0, QuotaHold)
			d := quotaWant(t, fresh, "d", quotaT0, QuotaTry)
			if err := fresh.Recover("d", d); err != nil {
				t.Fatal(err)
			}
			quotaWant(t, fresh, "d", quotaT0, QuotaPass)
			quotaWant(t, NewQuotaStore(path), "d", quotaT0, QuotaPass)
		})
	}
}

// A file of a newer schema is unknown and left alone: a refusal is not
// written over it.
func TestQuotaNewerSchemaLeftAlone(t *testing.T) {
	path := quotaPath(t)
	raw := []byte(`{"version":2,"seq":1,"entries":{}}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewQuotaStore(path)
	if e, err := s.Read("a", quotaT0); !errors.Is(err, ErrQuotaUnsupported) || e.State != QuotaUnknown {
		t.Fatalf("Read = %+v, %v", e, err)
	}
	if err := s.Exhaust("a", "quota", quotaT0.Add(time.Hour), quotaT0); !errors.Is(err, ErrQuotaUnsupported) {
		t.Fatalf("Exhaust = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(raw) {
		t.Fatalf("newer file written over: %s", b)
	}
}

// A refusal seen more than a minute after now was seen before the clock
// was set back, so it is unknown and gets its one try, rather than holding
// the key for as long as the clock was wrong. A new refusal replaces it.
// One a few seconds ahead, from a writer whose clock reads a little later,
// still holds.
func TestQuotaClockSetBack(t *testing.T) {
	s := NewQuotaStore(quotaPath(t))
	if err := s.Exhaust("a", "quota", quotaT0.Add(25*time.Hour), quotaT0.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if e, err := s.Read("a", quotaT0); err != nil || e.State != QuotaUnknown {
		t.Fatalf("Read = %+v, %v; want unknown", e, err)
	}
	try := quotaWant(t, s, "a", quotaT0, QuotaTry)
	if err := s.Exhaust("a", "quota", quotaT0.Add(time.Hour), quotaT0); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Read("a", quotaT0); e.State != QuotaExhausted || !e.ResetAt.Equal(quotaT0.Add(time.Hour)) {
		t.Fatalf("the new refusal didn't replace the old clock's: %+v", e)
	}
	if err := s.Recover("a", try); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "a", quotaT0.Add(time.Minute), QuotaHold)

	if err := s.Exhaust("b", "quota", quotaT0.Add(time.Hour), quotaT0.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "b", quotaT0, QuotaHold)
	if err := s.Exhaust("b", "quota", quotaT0.Add(time.Minute), quotaT0); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Read("b", quotaT0); !e.ResetAt.Equal(quotaT0.Add(time.Hour)) {
		t.Fatalf("a refusal seconds ahead was dropped: %+v", e)
	}

	// the success of a try at an unknown clock's refusal clears it
	if err := s.Exhaust("c", "quota", quotaT0.Add(48*time.Hour), quotaT0.Add(47*time.Hour)); err != nil {
		t.Fatal(err)
	}
	try = quotaWant(t, s, "c", quotaT0, QuotaTry)
	if err := s.Recover("c", try); err != nil {
		t.Fatal(err)
	}
	quotaWant(t, s, "c", quotaT0, QuotaPass)
}

// No refusal holds a key more than 31 days past its sighting: the reset is
// held to that when written, and a kept one further out is unknown.
func TestQuotaResetWithin31Days(t *testing.T) {
	path := quotaPath(t)
	s := NewQuotaStore(path)
	if err := s.Exhaust("a", "credit", quotaT0.Add(400*24*time.Hour), quotaT0); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Read("a", quotaT0.Add(30*24*time.Hour)); !e.ResetAt.Equal(quotaT0.Add(quotaLongest)) {
		t.Fatalf("reset %v, want %v", e.ResetAt, quotaT0.Add(quotaLongest))
	}
	quotaWant(t, s, "a", quotaT0.Add(quotaLongest), QuotaTry)

	far := fmt.Sprintf(`{"version":1,"seq":2,"entries":{"far":{"state":"exhausted","reason":"quota","resetAt":%q,"observedAt":%q,"seq":2}}}`,
		quotaT0.Add(quotaLongest+time.Second).Format(time.RFC3339Nano), quotaT0.Format(time.RFC3339Nano))
	if err := os.WriteFile(path, []byte(far), 0o600); err != nil {
		t.Fatal(err)
	}
	if e, err := NewQuotaStore(path).Read("far", quotaT0); !errors.Is(err, ErrQuotaCorrupt) || e.State != QuotaUnknown {
		t.Fatalf("a reset past 31 days: %+v, %v; want unknown", e, err)
	}
}

// A refusal without a reason, reset or sighting is refused, and so is a
// key that couldn't be a rest key. Admit lets an unusable key through:
// there is nothing to hold it by.
func TestQuotaInvalidInput(t *testing.T) {
	path := quotaPath(t)
	s := NewQuotaStore(path)
	for _, c := range []struct {
		key, reason    string
		reset, sighted time.Time
	}{
		{"", "quota", quotaT0, quotaT0},
		{"a\nb", "quota", quotaT0, quotaT0},
		{strings.Repeat("k", 513), "quota", quotaT0, quotaT0},
		{"a", "", quotaT0, quotaT0},
		{"a", "rate", quotaT0, quotaT0},
		{"a", "quota", time.Time{}, quotaT0},
		{"a", "quota", quotaT0, time.Time{}},
	} {
		if err := s.Exhaust(c.key, c.reason, c.reset, c.sighted); !errors.Is(err, ErrQuotaInvalid) {
			t.Errorf("Exhaust(%q, %q, %v, %v) = %v", c.key, c.reason, c.reset, c.sighted, err)
		}
	}
	if err := s.Recover("", QuotaEntry{}); !errors.Is(err, ErrQuotaInvalid) {
		t.Errorf("Recover of no key = %v", err)
	}
	quotaWant(t, s, "", quotaT0, QuotaPass)
	quotaWant(t, s, "relay#k1/gpt-5", quotaT0, QuotaPass)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an invalid refusal wrote the file: %v", err)
	}
}

// The file is the user's alone, and JSON a person can read.
func TestQuotaFileIsPrivate(t *testing.T) {
	path := quotaPath(t)
	if err := NewQuotaStore(path).Exhaust("relay", "quota", quotaT0.Add(time.Hour), quotaT0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	var f quotaFile
	if err != nil || json.Unmarshal(b, &f) != nil || f.Version != 1 || len(f.Entries) != 1 {
		t.Fatalf("file: %s, %v", b, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v; want 0600", st.Mode().Perm(), err)
	}
}

// The store is dormant: no production file but its own names it or its
// file, so nothing reads or writes quota.json yet.
func TestQuotaStoreHasNoProductionConsumer(t *testing.T) {
	root := filepath.Join("..", "..")
	own := filepath.Join("internal", "provider", "quota_store_fork.go")
	names := []string{"QuotaStore", "QuotaEntry", "QuotaAdmit", "QuotaTryHold", "QuotaPass", "QuotaHold", "QuotaTry", "QuotaUnknown", "QuotaNone", "QuotaState", "quota.json"}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "node_modules" || (strings.HasPrefix(n, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == own {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range names {
			if strings.Contains(string(b), name) {
				t.Errorf("%s names %s; the quota store has no production consumer yet", filepath.ToSlash(rel), name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
