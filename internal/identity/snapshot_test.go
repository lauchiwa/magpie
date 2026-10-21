package identity

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/lifecycle"
)

// countReads counts read calls until the test ends.
func countReads(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	old := readHook
	readHook = func() { n.Add(1) }
	t.Cleanup(func() { readHook = old })
	return &n
}

// otherCodex is a second confirmed Codex profile with a User-Agent of its
// own, so which profile a projection used shows in what it gives.
func otherCodex(t *testing.T) Profile {
	t.Helper()
	h := codexHeader()
	h.Set("User-Agent", "codex_cli_rs/0.159.2 (Windows 10.0.26100; x86_64) WindowsTerminal")
	p := newCandidate(t, ClientCodex, h)
	if err := Add(p); err != nil {
		t.Fatal(err)
	}
	if err := Confirm(p.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return p
}

// Every projection of a snapshot comes from its one read. Unbinding relay
// and binding it to another profile after the snapshot was taken changes
// none of them: a projection that read the file again would give the new
// profile, and a signer combining two projections could send one
// generation's binding with another's profile.
func TestASnapshotIsOneRead(t *testing.T) {
	ownStore(t)
	a := bindOne(t)
	b := otherCodex(t)
	reads := countReads(t)

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("ReadSnapshot read the store %d times, want once", n)
	}
	if err := Unbind("relay"); err != nil {
		t.Fatal(err)
	}
	if err := Bind("relay", b.ID); err != nil {
		t.Fatal(err)
	}
	reads.Store(0)

	if got := snap.Bindings()["relay"]; got != a.ID {
		t.Errorf("snapshot binding = %q, want the one read (%q)", got, a.ID)
	}
	p, found, err := snap.Resolve("relay")
	if err != nil || !found || p.ID != a.ID {
		t.Errorf("snapshot Resolve = %q, %v, %v; want %q", p.ID, found, err, a.ID)
	}
	h, err := snap.HeaderFor("relay")
	if err != nil || h.Get("User-Agent") != a.Fingerprint.UserAgent {
		t.Errorf("snapshot HeaderFor = %q, %v; want %q", h.Get("User-Agent"), err, a.Fingerprint.UserAgent)
	}
	if ps := snap.Profiles(); len(ps) != 2 {
		t.Errorf("snapshot profiles = %d, want 2", len(ps))
	}
	if h, err := snap.HeaderFor("nobody"); err != nil || h != nil {
		t.Errorf("an unbound provider in the snapshot got %v, %v", h, err)
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("projections read the store %d more times, want none", n)
	}

	// the store itself has moved on
	if h, err := HeaderFor("relay"); err != nil || h.Get("User-Agent") != b.Fingerprint.UserAgent {
		t.Errorf("HeaderFor after the rebind = %q, %v", h.Get("User-Agent"), err)
	}
}

// Each public read is one snapshot: one read of the store, and none for an
// empty provider id, as before.
func TestPublicReadsAreOneSnapshotEach(t *testing.T) {
	ownStore(t)
	bindOne(t)
	reads := countReads(t)
	calls := map[string]func() error{
		"Profiles":  func() error { _, err := Profiles(); return err },
		"Bindings":  func() error { _, err := Bindings(); return err },
		"Resolve":   func() error { _, _, err := Resolve("relay"); return err },
		"HeaderFor": func() error { _, err := HeaderFor("relay"); return err },
	}
	for name, call := range calls {
		reads.Store(0)
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n := reads.Load(); n != 1 {
			t.Errorf("%s read the store %d times, want once", name, n)
		}
	}
	reads.Store(0)
	if _, _, err := Resolve(""); err != nil {
		t.Fatal(err)
	}
	if h, err := HeaderFor(""); err != nil || h != nil {
		t.Fatalf("HeaderFor(\"\") = %v, %v", h, err)
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("an empty provider id read the store %d times", n)
	}
}

// Nothing a caller does with what a snapshot gave it reaches the snapshot
// or another caller: profiles, their headers and times, the bindings and a
// header are all copies.
func TestASnapshotSharesNothing(t *testing.T) {
	ownStore(t)
	a := bindOne(t)
	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	ps := snap.Profiles()
	ps[0].Fingerprint.Headers["Originator"] = "mine_cli"
	ps[0].Fingerprint.UserAgent = "changed"
	*ps[0].ConfirmedAt = time.Time{}
	*ps[0].ObservedAt = time.Time{}
	ps[0] = Profile{}
	bound := snap.Bindings()
	bound["relay"] = "identity_0000000000000000"
	bound["other"] = a.ID
	r, _, err := snap.Resolve("relay")
	if err != nil {
		t.Fatal(err)
	}
	r.Fingerprint.Headers["Originator"] = "resolved_cli"
	*r.ConfirmedAt = time.Time{}
	h, err := snap.HeaderFor("relay")
	if err != nil {
		t.Fatal(err)
	}
	h.Set("User-Agent", "header-changed")

	again := snap.Profiles()
	if len(again) != 1 || again[0].ID != a.ID || again[0].Fingerprint.Headers["Originator"] != "codex_cli_rs" ||
		again[0].Fingerprint.UserAgent != a.Fingerprint.UserAgent || again[0].ConfirmedAt == nil ||
		again[0].ConfirmedAt.IsZero() || again[0].ObservedAt.IsZero() {
		t.Errorf("a caller changed the snapshot's profiles: %+v", again)
	}
	if b := snap.Bindings(); len(b) != 1 || b["relay"] != a.ID {
		t.Errorf("a caller changed the snapshot's bindings: %v", b)
	}
	p, found, err := snap.Resolve("relay")
	if err != nil || !found || p.Fingerprint.Headers["Originator"] != "codex_cli_rs" || p.ConfirmedAt.IsZero() {
		t.Errorf("a caller changed the resolved profile: %+v, %v, %v", p, found, err)
	}
	if h, err := snap.HeaderFor("relay"); err != nil || h.Get("User-Agent") != a.Fingerprint.UserAgent {
		t.Errorf("a caller changed the header: %v, %v", h, err)
	}
	// and nothing reached the file
	if h, err := HeaderFor("relay"); err != nil || h.Get("User-Agent") != a.Fingerprint.UserAgent || h.Get("Originator") != "codex_cli_rs" {
		t.Errorf("the stored profile changed: %v, %v", h, err)
	}
}

// A snapshot of a store that can't be read is an error, as every read is:
// never an empty snapshot that would sign as unbound.
func TestASnapshotOfAnUnreadableStoreFails(t *testing.T) {
	ownStore(t)
	bindOne(t)
	if err := os.Remove(Path() + ".bak"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(`{"bound": {`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(); err == nil {
		t.Fatal("a snapshot of an unreadable store was taken")
	}
}

// A capture's candidate is stored after its window is consumed, outside
// the captures mutex: while it waits for lifecycle's lock, the window
// already reads as used and arming another provider is not held up.
func TestObserveWaitsForTheLockOutsideTheCaptures(t *testing.T) {
	ownStore(t)
	now := time.Now().UTC()
	if err := Arm("relay", ClientCodex, now); err != nil {
		t.Fatal(err)
	}
	unlock, err := lifecycle.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		consumed bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		_, consumed, err := Observe("relay", ClientCodex, http.Header(codexHeader()), now.Add(time.Second))
		done <- result{consumed, err}
	}()
	// Observe ends before the test does, whatever it fails on
	finished := false
	var last result
	wait := func() {
		if !finished {
			unlock()
			select {
			case last = <-done:
			case <-time.After(15 * time.Second):
				t.Error("Observe did not finish")
			}
			finished = true
		}
	}
	defer wait()

	// each call that touches the captures mutex must answer while Observe
	// waits for lifecycle's lock
	answers := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for CaptureArmed("relay", now.Add(time.Second)) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		answers <- Arm("other", ClientCodex, now) == nil && !CaptureArmed("relay", now.Add(time.Second))
	}()
	select {
	case ok := <-answers:
		if !ok {
			t.Fatal("the window still read as armed, or arming another provider failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the captures mutex was held while Observe waited for lifecycle's lock")
	}
	select {
	case r := <-done:
		t.Fatalf("Observe finished with lifecycle's lock held elsewhere: %+v", r)
	default:
	}
	wait()
	if last.err != nil || !last.consumed {
		t.Fatalf("Observe = %v, %v", last.consumed, last.err)
	}
	if ps, err := Profiles(); err != nil || len(ps) != 1 {
		t.Errorf("stored candidates = %v, %v", ps, err)
	}
}
