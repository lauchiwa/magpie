package cli

import (
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/provider"
)

// readyHere readies lifecycle.json at start-up in this test, as outside
// a test binary.
func readyHere(t *testing.T) {
	t.Helper()
	readyInSandbox = true
	t.Cleanup(func() { readyInSandbox = false })
}

// A test binary's run leaves its home without lifecycle.json, so every
// other test runs as before it; outside one, run readies it.
func TestRunReadiesLifecycleOutsideTests(t *testing.T) {
	groupsHome(t)
	if _, err := printed(t, func() error { return run([]string{"identity", "ls"}) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a test's run made lifecycle.json (%v)", err)
	}
	readyHere(t)
	if _, err := printed(t, func() error { return run([]string{"identity", "ls"}) }); err != nil {
		t.Fatal(err)
	}
	m, err := lifecycle.ReadMeta()
	if err != nil || m == nil || len(m.Providers) != 2 {
		t.Fatalf("lifecycle.json after run: %+v %v", m, err)
	}
}

// `magpie identity reconcile` is not readied first: its preview writes
// nothing, though lifecycle.json no longer matches providers.json (an
// adoption at start-up would write it).
func TestReconcilePreviewIsNotReadiedFirst(t *testing.T) {
	reconcileHome(t)
	readyHere(t)
	// every file but what any start-up writes first (plugins.yml), not
	// lifecycle's
	tree := func() map[string]string {
		out := configTree(t)
		for k := range out {
			if strings.HasPrefix(k, "plugins.yml") {
				delete(out, k)
			}
		}
		return out
	}
	before := tree()
	for _, args := range [][]string{{"identity", "reconcile"}, {"identities", "reconcile"}} {
		out, err := printed(t, func() error { return run(args) })
		if err != nil || !strings.Contains(out, "nothing was written") {
			t.Fatalf("magpie %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if after := tree(); !reflect.DeepEqual(after, before) {
			t.Fatalf("magpie %s wrote:\nbefore %v\nafter  %v", strings.Join(args, " "), before, after)
		}
	}
}

// --help readies nothing: it prints its usage before run reaches the call.
// The home has a providers.json, so a run readied would write
// lifecycle.json beside it.
func TestHelpReadiesNothing(t *testing.T) {
	root := helpHome(t)
	if err := provider.Save(provider.Provider{ID: "a", Name: "A", Key: "ka", Models: []string{"m"}, Chat: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
	readyHere(t)
	before := tree(t, root)
	for _, args := range [][]string{{"--help"}, {"identity", "--help"}, {"serve", "-h"}, {"providers", "help"}} {
		if _, err := runOut(t, args...); err != nil {
			t.Fatalf("magpie %s: %v", strings.Join(args, " "), err)
		}
		if got := tree(t, root); !reflect.DeepEqual(got, before) {
			t.Fatalf("magpie %s wrote under the home:\nbefore %v\nafter  %v", strings.Join(args, " "), before, got)
		}
	}
}

// A lifecycle.json that can't be readied never stops magpie from
// starting: it is said on stderr and the command runs.
func TestRunStartsWhenLifecycleCantBeReadied(t *testing.T) {
	groupsHome(t)
	readyHere(t)
	old := readyLifecycle
	readyLifecycle = func() error { return errors.New("lifecycle: injected") }
	t.Cleanup(func() { readyLifecycle = old })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	_, runErr := printed(t, func() error { return run([]string{"identity", "ls"}) })
	os.Stderr = stderr
	w.Close()
	said, _ := io.ReadAll(r)
	r.Close()
	if runErr != nil {
		t.Fatalf("run failed with lifecycle.json not readied: %v", runErr)
	}
	if !strings.Contains(string(said), "magpie: lifecycle: injected") {
		t.Fatalf("stderr doesn't say it: %q", said)
	}
}
