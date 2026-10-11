package identity

import (
	"os"
	"strings"
	"testing"
	"time"
)

func newCandidate(t *testing.T, client Client, h map[string][]string) Profile {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p, err := Candidate(id, client, h, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ownStore gives one test a config folder of its own, so the store a test
// writes is never read by the next one.
func ownStore(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// A profile is stored, confirmed and bound, and only then does a provider
// present it. Binding is by provider id, so providers.json keeps its own
// shape and nothing of a profile is written into it.
func TestStoreBindsAProfileToAProvider(t *testing.T) {
	ownStore(t)
	p := newCandidate(t, ClientCodex, codexHeader())
	if err := Add(p); err != nil {
		t.Fatal(err)
	}

	// an unconfirmed profile cannot be bound: the confirmation is the
	// operator's step, not something a later failure reveals
	if err := Bind("relay", p.ID); err == nil {
		t.Fatal("an unconfirmed profile was bound")
	}
	if err := Confirm(p.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := Bind("relay", p.ID); err != nil {
		t.Fatal(err)
	}

	h, err := HeaderFor("relay")
	if err != nil {
		t.Fatal(err)
	}
	if h.Get("User-Agent") != p.Fingerprint.UserAgent {
		t.Errorf("User-Agent = %q, want the bound profile's", h.Get("User-Agent"))
	}
	if h.Get("Originator") != "codex_cli_rs" || h.Get("Version") != "0.159.2" {
		t.Errorf("bound headers = %v", h)
	}

	// a provider bound to nothing presents nothing, and that is no error
	none, err := HeaderFor("other")
	if err != nil || none != nil {
		t.Errorf("an unbound provider got %v, %v", none, err)
	}

	// nothing of the profile is kept in providers.json
	if _, err := os.Stat(Path()); err != nil {
		t.Errorf("the store was not written to %s: %v", Path(), err)
	}
	if !strings.HasSuffix(Path(), "identities.json") {
		t.Errorf("the store is at %s, not a file of its own", Path())
	}
}

// A binding whose profile is gone or unconfirmed fails the lookup. Sending
// under magpie's own User-Agent instead would look like success while
// presenting the wrong client to the vendor.
func TestABrokenBindingFailsClosed(t *testing.T) {
	ownStore(t)
	p := newCandidate(t, ClientCodex, codexHeader())
	if err := Add(p); err != nil {
		t.Fatal(err)
	}
	if err := Confirm(p.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := Bind("relay", p.ID); err != nil {
		t.Fatal(err)
	}

	// the profile is taken out from under the binding, as editing the
	// file by hand would
	f, err := read()
	if err != nil {
		t.Fatal(err)
	}
	f.Profiles = nil
	if err := write(f); err != nil {
		t.Fatal(err)
	}

	if _, err := HeaderFor("relay"); err == nil {
		t.Fatal("a binding to a missing profile sent nothing and reported no error")
	} else if !strings.Contains(err.Error(), "not stored") {
		t.Errorf("error = %q, want it to name the missing profile", err)
	}

	// an unconfirmed profile under the binding fails the same way
	p.ConfirmedAt = nil
	f.Profiles = []Profile{p}
	if err := write(f); err != nil {
		t.Fatal(err)
	}
	if _, err := HeaderFor("relay"); err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("a binding to an unconfirmed profile gave %v", err)
	}
}

// Removing a profile takes every binding to it, so no provider is left
// bound to an id that is gone.
func TestRemoveTakesItsBindings(t *testing.T) {
	ownStore(t)
	p := newCandidate(t, ClientClaudeCode, claudeHeader())
	if err := Add(p); err != nil {
		t.Fatal(err)
	}
	if err := Confirm(p.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, prov := range []string{"relay", "other"} {
		if err := Bind(prov, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	bound, err := Bindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bound) != 0 {
		t.Errorf("bindings survived the profile: %v", bound)
	}
	for _, prov := range []string{"relay", "other"} {
		if h, err := HeaderFor(prov); err != nil || h != nil {
			t.Errorf("%s got %v, %v after the profile was removed", prov, h, err)
		}
	}
}

// A replacement snapshot gets a new id rather than editing one in place,
// so a profile that was reviewed stays what was reviewed.
func TestAddRefusesADuplicateID(t *testing.T) {
	ownStore(t)
	p := newCandidate(t, ClientCodex, codexHeader())
	if err := Add(p); err != nil {
		t.Fatal(err)
	}
	if err := Add(p); err == nil {
		t.Fatal("a profile was stored twice under one id")
	}
}

// An invalid profile never reaches the file, so the store cannot hold a
// snapshot that could not be sent.
func TestAddRefusesAnInvalidProfile(t *testing.T) {
	ownStore(t)
	p := newCandidate(t, ClientCodex, codexHeader())
	p.Fingerprint.Version = "0.0.1" // no longer the version its User-Agent reports
	if err := Add(p); err == nil {
		t.Fatal("an inconsistent profile was stored")
	}
	ps, err := Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Errorf("the store holds %d profiles after a refused add", len(ps))
	}
}

// A stored profile cannot be reached through the value a caller gets, so
// nothing can change what a request will present without going through
// the store.
func TestProfilesAreCopied(t *testing.T) {
	ownStore(t)
	p := newCandidate(t, ClientCodex, codexHeader())
	if err := Add(p); err != nil {
		t.Fatal(err)
	}
	ps, err := Profiles()
	if err != nil {
		t.Fatal(err)
	}
	ps[0].Fingerprint.Headers["Originator"] = "mine_cli"
	again, err := Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Fingerprint.Headers["Originator"] != "codex_cli_rs" {
		t.Error("a caller changed the stored profile through the value it was given")
	}
}
