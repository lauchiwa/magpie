package cli

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// TestCLIOtherMagpie is another magpie's command line in the same home: run
// by the tests below as a process of its own, it makes one change and exits.
func TestCLIOtherMagpie(t *testing.T) {
	step := os.Getenv("MAGPIE_CLI_OTHER")
	if step == "" {
		t.Skip("run by the lifecycle write tests")
	}
	relay := provider.Provider{ID: "a", Name: "A", Key: "fresh", Chat: "http://127.0.0.1:1/v1"}
	var err error
	switch step {
	case "rename":
		err = provider.Rename("a", "a2")
	case "recreate":
		if err = provider.Delete("a"); err == nil {
			err = provider.Save(relay)
		}
	case "edit":
		relay.Key = "edited"
		err = provider.Save(relay)
	default:
		t.Fatalf("no step %q", step)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// other has another magpie make step in this home, as its own process.
func other(t *testing.T, step string) {
	t.Helper()
	c := exec.Command(os.Args[0], "-test.run=^TestCLIOtherMagpie$", "-test.count=1", "-test.v")
	c.Env = append(os.Environ(), "MAGPIE_CLI_OTHER="+step)
	out, err := c.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestCLIOtherMagpie") {
		t.Fatalf("the other magpie's %s: %v\n%s", step, err, out)
	}
}

// otherAt has the other magpie make change at the command's step named at,
// once: between its read of the provider and its write.
func otherAt(t *testing.T, at, change string) {
	t.Helper()
	done := false
	betweenReadAndWrite = func(step string) {
		if step == at && !done {
			done = true
			other(t, change)
		}
	}
	t.Cleanup(func() { betweenReadAndWrite = nil })
}

// writesHome is groupsHome (providers a and b) with lifecycle.json readied,
// as a configured magpie's is, and a confirmed profile to bind.
func writesHome(t *testing.T) identity.Profile {
	t.Helper()
	groupsHome(t)
	p := confirmedProfile(t)
	if err := provider.ReadyLifecycle(); err != nil {
		t.Fatal(err)
	}
	return p
}

func confirmedProfile(t *testing.T) identity.Profile {
	t.Helper()
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p, err := identity.Candidate(id, identity.ClientCodex, http.Header{
		"User-Agent": {"codex_cli_rs/0.159.2 (Mac OS 26.6.0; arm64) Apple_Terminal/455"},
		"Originator": {"codex_cli_rs"},
		"Version":    {"0.159.2"},
	}, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Second)
	p.ConfirmedAt = &at
	if err := identity.Add(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func stored(t *testing.T, id string) (provider.Provider, bool) {
	t.Helper()
	ps, err := provider.Stored()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.ID == id {
			return p, true
		}
	}
	return provider.Provider{}, false
}

func bindings(t *testing.T) map[string]string {
	t.Helper()
	b, err := identity.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// magpie provider set a key=… after another magpie renamed a to a2, or
// deleted a and made a new one under its id: the value read is refused, and
// neither a2 nor the new a is given the change.
func TestSetRefusesAProviderChangedMeanwhile(t *testing.T) {
	writesHome(t)
	otherAt(t, "save", "rename")
	_, err := printed(t, func() error { return providerCmd([]string{"provider", "set", "a", "key=mine"}) })
	if !errors.Is(err, provider.ErrStale) {
		t.Fatalf("set over a rename made meanwhile: %v", err)
	}
	if p, ok := stored(t, "a2"); !ok || p.Key != "ka" {
		t.Fatalf("a2 after the refused set: %+v %v", p.Key, ok)
	}
	if _, ok := stored(t, "a"); ok {
		t.Fatal("the refused set made a anew")
	}

	writesHome(t)
	otherAt(t, "save", "recreate")
	_, err = printed(t, func() error { return providerCmd([]string{"provider", "set", "a", "key=mine"}) })
	if !errors.Is(err, provider.ErrStale) {
		t.Fatalf("set over a provider made anew meanwhile: %v", err)
	}
	if p, ok := stored(t, "a"); !ok || p.Key != "fresh" {
		t.Fatalf("the new a after the refused set: %+v %v", p.Key, ok)
	}
}

// magpie provider set a id=a3 after another magpie deleted a and made a new
// one under its id between the save and the rename: the rename is refused,
// and the new a is neither renamed nor given the save's change.
func TestSetRenamesOnlyTheProviderItSaved(t *testing.T) {
	writesHome(t)
	otherAt(t, "rename", "recreate")
	_, err := printed(t, func() error { return providerCmd([]string{"provider", "set", "a", "key=mine", "id=a3"}) })
	if !errors.Is(err, provider.ErrStale) {
		t.Fatalf("a rename over a provider made anew meanwhile: %v", err)
	}
	if p, ok := stored(t, "a"); !ok || p.Key != "fresh" {
		t.Fatalf("the new a after the refused rename: %+v %v", p.Key, ok)
	}
	if _, ok := stored(t, "a3"); ok {
		t.Fatal("the new a was renamed to a3")
	}
}

// magpie provider set a id=a2 whose settings step fails: it says the rename
// is saved and that step failed, does only that step again (here mended
// before it is), and never renames again; failing again, it says so and
// exits nonzero. The rename itself is never replayed.
func TestSetRedoesOnlyTheStepAfterItsRename(t *testing.T) {
	for _, mend := range []bool{true, false} {
		writesHome(t)
		if err := os.MkdirAll(filepath.Dir(settings.Path()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings.Path(), []byte(`{"theme":"no-such-theme","visible":{"codex":["a"]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var redone []string
		var providersAtRedo []byte
		beforeRedo = func(step string) {
			redone = append(redone, step)
			providersAtRedo, _ = os.ReadFile(provider.Path())
			if mend {
				os.WriteFile(settings.Path(), []byte(`{"visible":{"codex":["a"]}}`), 0o600)
			}
		}
		t.Cleanup(func() { beforeRedo = nil })
		out, err := printed(t, func() error { return providerCmd([]string{"provider", "set", "a", "id=a2"}) })
		if len(redone) != 1 || redone[0] != provider.StepSettings {
			t.Fatalf("mend %v: steps done again %v, want one settings", mend, redone)
		}
		if !strings.Contains(out, "a is renamed to a2, but its settings step failed") {
			t.Fatalf("mend %v: it didn't say the rename is saved and the step failed:\n%s", mend, out)
		}
		after, _ := os.ReadFile(provider.Path())
		if string(after) != string(providersAtRedo) {
			t.Fatalf("mend %v: providers.json was written after the commit: the rename was made again", mend)
		}
		if _, ok := stored(t, "a2"); !ok {
			t.Fatalf("mend %v: no a2", mend)
		}
		if mend {
			if err != nil || !strings.Contains(out, "saved A") {
				t.Fatalf("set with its step done again: %v\n%s", err, out)
			}
			if v := settings.Load().Visible["codex"]; len(v) != 1 || v[0] != "a2" {
				t.Fatalf("codex is shown %v, want a2", v)
			}
			continue
		}
		var fu *provider.FollowUpError
		if err == nil || errors.As(err, &fu) || !strings.Contains(err.Error(), "settings step failed again") || strings.Contains(out, "saved A") {
			t.Fatalf("a step failing twice: %v\n%s", err, out)
		}
	}
}

// magpie provider set a id=a2 whose commit fails once its journal is
// written: it says the rename is finished by the next write or start-up,
// doesn't retry it, and is finished by the next one.
func TestSetSaysARenamePendingInItsJournal(t *testing.T) {
	writesHome(t)
	journals, failed := 0, false
	betweenReadAndWrite = func(step string) {
		if step != "rename" {
			return
		}
		lifecycle.TestStage = func(s string) error {
			if s == "journal:write" {
				journals++
			}
			if s == "providers.json:write" && !failed {
				failed = true
				return errors.New("injected")
			}
			return nil
		}
	}
	t.Cleanup(func() { betweenReadAndWrite, lifecycle.TestStage = nil, nil })
	_, err := printed(t, func() error { return providerCmd([]string{"provider", "set", "a", "id=a2"}) })
	lifecycle.TestStage = nil
	if !errors.Is(err, provider.ErrPending) || !strings.Contains(err.Error(), "a is not renamed to a2 yet") {
		t.Fatalf("a rename pending in its journal: %v", err)
	}
	if journals != 1 {
		t.Fatalf("%d journals written for one rename: it was made again", journals)
	}
	if said := lifecycleSaid(err).Error(); !strings.Contains(said, "don't run it again") {
		t.Fatalf("magpie says %q", said)
	}
	if err := provider.ReadyLifecycle(); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored(t, "a2"); !ok {
		t.Fatal("the next start-up didn't finish the rename")
	}
}

// magpie provider rm a after another magpie deleted a and made a new one
// under its id: the new one is not removed.
func TestRemoveRefusesAProviderMadeAnewMeanwhile(t *testing.T) {
	writesHome(t)
	otherAt(t, "delete", "recreate")
	_, err := printed(t, func() error { return providerCmd([]string{"provider", "rm", "a"}) })
	if !errors.Is(err, provider.ErrStale) {
		t.Fatalf("rm over a provider made anew meanwhile: %v", err)
	}
	if p, ok := stored(t, "a"); !ok || p.Key != "fresh" {
		t.Fatalf("the new a after the refused rm: %+v %v", p.Key, ok)
	}
}

// Without lifecycle.json (as before it, and every test home that doesn't
// ready it) set, rm and a rename go by id, as they always did.
func TestSetAndRemoveByIDWithoutLifecycle(t *testing.T) {
	groupsHome(t)
	otherAt(t, "save", "edit")
	if _, err := printed(t, func() error { return providerCmd([]string{"provider", "set", "a", "family=x", "id=a2"}) }); err != nil {
		t.Fatal(err)
	}
	if p, ok := stored(t, "a2"); !ok || p.Family != "x" {
		t.Fatalf("a2 after a legacy set: %+v %v", p, ok)
	}
	if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a legacy set made lifecycle.json (%v)", err)
	}
	betweenReadAndWrite = nil
	if _, err := printed(t, func() error { return providerCmd([]string{"provider", "rm", "a2"}) }); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored(t, "a2"); ok {
		t.Fatal("a legacy rm left a2")
	}
}

// magpie identity bind a <profile> after another magpie renamed a to a2:
// the binding goes to a2, the provider it was read as, not to an orphan a.
// After a was deleted and made anew instead, the bind is refused.
func TestIdentityBindFollowsTheProviderItRead(t *testing.T) {
	prof := writesHome(t)
	otherAt(t, "bind", "rename")
	if _, err := printed(t, func() error { return identityCmd([]string{"identity", "bind", "a", prof.ID}) }); err != nil {
		t.Fatal(err)
	}
	if b := bindings(t); b["a2"] != prof.ID || b["a"] != "" {
		t.Fatalf("bindings after a bind over a rename: %v", b)
	}

	prof = writesHome(t)
	otherAt(t, "bind", "recreate")
	_, err := printed(t, func() error { return identityCmd([]string{"identity", "bind", "a", prof.ID}) })
	if !errors.Is(err, provider.ErrStale) {
		t.Fatalf("a bind over a provider made anew meanwhile: %v", err)
	}
	if b := bindings(t); b["a"] != "" {
		t.Fatalf("the new a was bound: %v", b)
	}
}

// magpie identity unbind a after another magpie renamed a to a2 unbinds
// a2. One whose provider is gone removes the binding it left; one with
// neither is "no provider", and bindings that can't be read are said, with
// nothing written.
func TestIdentityUnbind(t *testing.T) {
	prof := writesHome(t)
	if err := identity.Bind("a", prof.ID); err != nil {
		t.Fatal(err)
	}
	otherAt(t, "unbind", "rename")
	if _, err := printed(t, func() error { return identityCmd([]string{"identity", "unbind", "a"}) }); err != nil {
		t.Fatal(err)
	}
	if b := bindings(t); b["a2"] != "" || b["a"] != "" {
		t.Fatalf("bindings after an unbind over a rename: %v", b)
	}
	betweenReadAndWrite = nil

	// a binding left under an id no provider has
	if err := identity.Bind("gone", prof.ID); err != nil {
		t.Fatal(err)
	}
	out, err := printed(t, func() error { return identityCmd([]string{"identity", "unbind", "Gone"}) })
	if err != nil || !strings.Contains(out, "the binding left under it is removed") {
		t.Fatalf("an orphan unbind: %v\n%s", err, out)
	}
	if b := bindings(t); b["gone"] != "" {
		t.Fatalf("the orphan binding is still there: %v", b)
	}
	if _, err := printed(t, func() error { return identityCmd([]string{"identity", "unbind", "never"}) }); err == nil || !strings.Contains(err.Error(), `no provider "never"`) {
		t.Fatalf("an unbind of nothing: %v", err)
	}

	if err := os.WriteFile(identity.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(identity.Path() + ".bak")
	before := configTree(t)
	if _, err := printed(t, func() error { return identityCmd([]string{"identity", "unbind", "never"}) }); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("an unbind with bindings that can't be read: %v", err)
	}
	sameTree(t, "an unbind with bindings that can't be read", before)
}

// Without lifecycle.json bind and unbind go by id, as they always did.
func TestIdentityBindByIDWithoutLifecycle(t *testing.T) {
	groupsHome(t)
	prof := confirmedProfile(t)
	if _, err := printed(t, func() error { return identityCmd([]string{"identity", "bind", "a", prof.ID}) }); err != nil {
		t.Fatal(err)
	}
	if b := bindings(t); b["a"] != prof.ID {
		t.Fatalf("bindings after a legacy bind: %v", b)
	}
	if _, err := printed(t, func() error { return identityCmd([]string{"identity", "unbind", "a"}) }); err != nil {
		t.Fatal(err)
	}
	if b := bindings(t); b["a"] != "" {
		t.Fatalf("bindings after a legacy unbind: %v", b)
	}
	if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a legacy bind made lifecycle.json (%v)", err)
	}
}

// What magpie prints for a refused write: a stale or conflicting one says to
// run the command again, a pending one says not to, any other is as it
// was, and the error is still the one it wraps.
func TestLifecycleSaid(t *testing.T) {
	stale := &provider.StaleError{ID: "a"}
	conflict := &provider.ConflictError{ID: "a", Reason: "it changed since it was read"}
	for _, err := range []error{stale, conflict} {
		said := lifecycleSaid(err)
		if !errors.Is(said, err) || !strings.Contains(said.Error(), "run the command again") {
			t.Fatalf("%v said as %v", err, said)
		}
	}
	plain := errors.New("x")
	if lifecycleSaid(plain) != plain || lifecycleSaid(nil) != nil {
		t.Fatal("an error that isn't a refused write was changed")
	}
	code := exitError{code: 3}
	if !errors.Is(lifecycleSaid(code), code) {
		t.Fatal("an exit code was lost")
	}
}
