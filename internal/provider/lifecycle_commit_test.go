package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/lifecycle"
)

// saveRows writes rows through store, as a provider write does.
func (h *lifeHome) saveRows(rows ...Provider) {
	h.t.Helper()
	f, err := read()
	if err != nil {
		h.t.Fatal(err)
	}
	f.Providers = rows
	if err := store(f); err != nil {
		h.t.Fatal(err)
	}
}

func boundNow(t *testing.T) map[string]string {
	t.Helper()
	b, err := identity.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// With no lifecycle.json (every magpie today), a renamed provider keeps
// presenting its profile: the binding moves with the id in the commit.
func TestALegacyRenameCarriesTheBinding(t *testing.T) {
	h := newLifeHome(t)
	h.saveRows(h.row("relay"))
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	if err := Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	got, err := h.sign(h.find("relay2"))
	if err != nil {
		t.Fatal(err)
	}
	if got != uaOf(h.a) {
		t.Fatalf("renamed relay2 sent %q, want its profile %q", got, uaOf(h.a))
	}
	if b := boundNow(t); b["relay2"] != h.a.ID || b["relay"] != "" {
		t.Fatalf("bindings after the rename: %v", b)
	}
	if _, err := os.Stat(lifecycle.Path()); !os.IsNotExist(err) {
		t.Fatalf("a legacy write bootstrapped lifecycle.json (%v)", err)
	}
}

// A provider deleted and added again under its id is another provider:
// it presents no profile until one is bound to it. So is a new provider
// added under an id an orphan binding was left on.
func TestADeletedThenAddedProviderInheritsNoBinding(t *testing.T) {
	for _, how := range []string{"delete then add", "orphan binding"} {
		t.Run(how, func(t *testing.T) {
			h := newLifeHome(t)
			if how == "delete then add" {
				h.saveRows(h.row("relay"))
				if err := identity.Bind("relay", h.a.ID); err != nil {
					t.Fatal(err)
				}
				if err := Delete("relay"); err != nil {
					t.Fatal(err)
				}
				if b := boundNow(t); b["relay"] != "" {
					t.Fatalf("the delete left relay bound: %v", b)
				}
			} else {
				h.saveRows(h.row("other"))
				if err := identity.Bind("relay", h.a.ID); err != nil {
					t.Fatal(err)
				}
			}
			h.saveRows(append(load().Providers, h.row("relay"))...)
			got, err := h.sign(h.find("relay"))
			if err != nil {
				t.Fatal(err)
			}
			if got == uaOf(h.a) {
				t.Fatalf("the new relay presented the old relay's profile %q", got)
			}
		})
	}
}

// TestLifecycleOtherWriter is the other magpie of
// TestAWriteBuiltOnAnOldReadIsRefused: it adds a provider, as Save does.
func TestLifecycleOtherWriter(t *testing.T) {
	if os.Getenv("MAGPIE_LIFECYCLE_WRITER") == "" {
		t.Skip("run by TestAWriteBuiltOnAnOldReadIsRefused")
	}
	f, err := read()
	if err != nil {
		t.Fatal(err)
	}
	f.Providers = append(f.Providers, Provider{ID: "theirs", Name: "theirs", Key: "k", Chat: "http://127.0.0.1:1/v1"})
	if err := store(f); err != nil {
		t.Fatal(err)
	}
}

// Another magpie commits between this one's read and its write: this
// write is refused with a ConflictError, and the other's change stays,
// rather than being silently written over.
func TestAWriteBuiltOnAnOldReadIsRefused(t *testing.T) {
	h := newLifeHome(t)
	h.saveRows(h.row("mine"))
	f, err := read()
	if err != nil {
		t.Fatal(err)
	}
	other := exec.Command(os.Args[0], "-test.run=^TestLifecycleOtherWriter$", "-test.count=1")
	other.Env = append(os.Environ(), "MAGPIE_LIFECYCLE_WRITER=1")
	if out, err := other.CombinedOutput(); err != nil {
		t.Fatalf("the other magpie: %v\n%s", err, out)
	}
	f.Providers[0].Name = "renamed by me"
	err = store(f)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrStale) {
		t.Fatalf("a write over another magpie's commit gave %v, want *ConflictError", err)
	}
	ids := []string{}
	for _, p := range load().Providers {
		ids = append(ids, p.ID+"="+p.Name)
	}
	if strings.Join(ids, ",") != "mine=mine,theirs=theirs" {
		t.Fatalf("providers after the refused write: %v", ids)
	}
	// a zero base (a literal file, as upstream's test fixtures store) is a
	// blind write, as before
	if err := store(file{Providers: []Provider{h.row("mine")}}); err != nil {
		t.Fatalf("a literal store failed: %v", err)
	}
}

// managedHome is a home whose lifecycle.json describes rows.
func managedHome(t *testing.T, ids ...string) (*lifeHome, map[string]string) {
	h := newLifeHome(t)
	inst := map[string]string{}
	rows := []Provider{}
	for _, id := range ids {
		inst[id] = newInstance(t)
		rows = append(rows, h.row(id))
	}
	h.write(rows, inst)
	return h, inst
}

func readMetaT(t *testing.T) lifecycle.Meta {
	t.Helper()
	m, err := lifecycle.ReadMeta()
	if err != nil || m == nil {
		t.Fatalf("lifecycle.json: %v %v", m, err)
	}
	return *m
}

// With lifecycle.json: a rename keeps the instance and moves the binding;
// an edit bumps only its own row's rev; a delete retires the instance
// with whether it was bound; a new id gets a new instance; each commit
// advances gen and records the new file's digest.
func TestAManagedCommitKeepsInstances(t *testing.T) {
	h, inst := managedHome(t, "relay", "other")
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind("other", h.b.ID); err != nil {
		t.Fatal(err)
	}
	gen := readMetaT(t).Gen
	check := func(what string) lifecycle.Meta {
		t.Helper()
		m := readMetaT(t)
		raw, _ := os.ReadFile(Path())
		if m.Gen != gen+1 || m.Digest != lifecycle.Digest(raw) {
			t.Fatalf("%s: gen %d (want %d), digest matches %v", what, m.Gen, gen+1, m.Digest == lifecycle.Digest(raw))
		}
		gen = m.Gen
		return m
	}

	old := h.find("relay")
	if err := Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	m := check("rename")
	if m.Providers["relay2"].Instance != inst["relay"] || m.Providers["relay2"].Rev != 2 {
		t.Fatalf("rename: relay2 is %+v, want instance %s rev 2", m.Providers["relay2"], inst["relay"])
	}
	if m.Providers["other"].Rev != 1 {
		t.Fatalf("rename bumped other's rev: %+v", m.Providers["other"])
	}
	if got, err := h.sign(old); err != nil || got != uaOf(h.a) {
		t.Fatalf("the value read before the rename sent %q (%v), want %q", got, err, uaOf(h.a))
	}

	f, _ := read()
	f.Providers[1].Name = "edited"
	if err := store(f); err != nil {
		t.Fatal(err)
	}
	m = check("edit")
	if m.Providers["other"].Rev != 2 || m.Providers["relay2"].Rev != 2 || m.Providers["other"].Instance != inst["other"] {
		t.Fatalf("edit: %+v", m.Providers)
	}

	f, _ = read()
	f.Order = []string{"other", "relay2"}
	if err := store(f); err != nil {
		t.Fatal(err)
	}
	m = check("order")
	if m.Providers["other"].Rev != 2 || m.Providers["relay2"].Rev != 2 {
		t.Fatalf("an order change bumped a row's rev: %+v", m.Providers)
	}

	oldOther := h.find("other")
	if err := Delete("other"); err != nil {
		t.Fatal(err)
	}
	m = check("delete")
	if _, ok := m.Providers["other"]; ok || len(m.Retired) != 1 || m.Retired[0].Instance != inst["other"] || !m.Retired[0].Bound || m.Retired[0].Gen != m.Gen {
		t.Fatalf("delete: providers %+v retired %+v", m.Providers, m.Retired)
	}
	if boundNow(t)["other"] != "" {
		t.Fatal("the delete left other bound")
	}
	if _, err := h.sign(oldOther); !errors.Is(err, ErrStale) {
		t.Fatalf("a value of the provider deleted while bound signed (%v)", err)
	}
	if err := Delete("relay2"); err != nil {
		t.Fatal(err)
	}
	if m = check("delete relay2"); !m.Retired[1].Bound {
		t.Fatalf("relay2 was bound when deleted: %+v", m.Retired)
	}

	h.saveRows(append(load().Providers, h.row("other"))...)
	m = check("add")
	if n := m.Providers["other"].Instance; n == "" || n == inst["other"] || m.Providers["other"].Rev != 1 {
		t.Fatalf("a re-added id got %+v", m.Providers["other"])
	}

	// a store that changes nothing writes nothing
	f, _ = read()
	if err := store(f); err != nil {
		t.Fatal(err)
	}
	if readMetaT(t).Gen != gen {
		t.Fatal("a store that changed nothing advanced gen")
	}
}

// An intent's create, rename source and expectation are checked against
// the file under the lock.
func TestACommitChecksItsIntent(t *testing.T) {
	h, inst := managedHome(t, "relay")
	f, _ := read()
	if err := commit(f, intent{create: []string{"relay"}}); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("create of an id that is there: %v", err)
	}
	if err := commit(f, intent{expect: map[string]lifecycle.Provider{"relay": {Instance: newInstance(t)}}}); !errors.As(err, new(*StaleError)) {
		t.Fatalf("expect of another instance: %v", err)
	}
	if err := commit(f, intent{expect: map[string]lifecycle.Provider{"relay": {Instance: inst["relay"], Rev: 7}}}); !errors.As(err, new(*StaleError)) {
		t.Fatalf("expect of another rev: %v", err)
	}
	if err := commit(f, intent{expect: map[string]lifecycle.Provider{"relay": {Instance: inst["relay"], Rev: 1}}}); err != nil {
		t.Fatalf("expect of what is there: %v", err)
	}
	g := f
	g.Providers = []Provider{h.row("x")}
	if err := commit(g, intent{renames: map[string]string{"gone": "x"}}); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("rename of an id not there: %v", err)
	}
}

// commitStages are the steps of a three-file commit, in order.
var commitStages = []string{
	"journal:write",
	"providers.json:keep:before", "providers.json:keep:after", "providers.json:write",
	"identities.json:keep:before", "identities.json:keep:after", "identities.json:write",
	"meta:before", "lifecycle.json:write",
	"journal:remove:before",
}

type commitFiles struct{ providers, identities, meta []byte }

func readCommitFiles(t *testing.T) commitFiles {
	t.Helper()
	r := func(p string) []byte { b, _ := os.ReadFile(p); return b }
	return commitFiles{r(Path()), r(identity.Path()), r(lifecycle.Path())}
}

// renameHome is the state each failure test starts from: relay bound,
// renamed to relay2 by the commit under test.
func renameHome(t *testing.T, managed bool) (*lifeHome, commitFiles) {
	h := newLifeHome(t)
	if managed {
		h.write([]Provider{h.row("relay")}, map[string]string{"relay": newInstance(t)})
	} else {
		h.saveRows(h.row("relay"))
	}
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	return h, readCommitFiles(t)
}

// checkRecovered says the rename is whole after Recover, with each .bak
// the generation before it.
func checkRecovered(t *testing.T, h *lifeHome, before commitFiles, managed bool) {
	t.Helper()
	unlock, err := lifecycle.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = lifecycle.Recover()
	unlock()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if _, err := os.Stat(lifecycle.JournalPath()); !os.IsNotExist(err) {
		t.Fatalf("journal left after Recover (%v)", err)
	}
	if b := boundNow(t); b["relay2"] != h.a.ID || b["relay"] != "" {
		t.Fatalf("bindings after recovery: %v", b)
	}
	if _, err := Find("relay2"); err != nil {
		t.Fatalf("relay2 after recovery: %v", err)
	}
	for _, c := range []struct {
		path string
		was  []byte
	}{{Path(), before.providers}, {identity.Path(), before.identities}} {
		if bak, _ := os.ReadFile(lastgood.Bak(c.path)); !bytes.Equal(bak, c.was) {
			t.Fatalf("%s.bak is not the generation before the rename:\n%s\nwant\n%s", filepath.Base(c.path), bak, c.was)
		}
	}
	if managed {
		m := readMetaT(t)
		raw, _ := os.ReadFile(Path())
		if m.Digest != lifecycle.Digest(raw) || m.Providers["relay2"].Rev != 2 {
			t.Fatalf("lifecycle.json after recovery: %+v", m)
		}
		if got, err := h.sign(h.find("relay2")); err != nil || got != uaOf(h.a) {
			t.Fatalf("relay2 after recovery sent %q (%v)", got, err)
		}
	}
}

// A rename's commit that fails at any step is finished by Recover: every
// file reaches the rename, and each .bak is the generation before it.
func TestARenameFailingAtAnyStepIsRecovered(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, stage := range commitStages {
			if !managed && (stage == "meta:before" || stage == "lifecycle.json:write") {
				continue
			}
			t.Run(map[bool]string{false: "legacy ", true: "managed "}[managed]+stage, func(t *testing.T) {
				h, before := renameHome(t, managed)
				hit := false
				lifecycle.TestStage = func(s string) error {
					if s == stage {
						hit = true
						return errors.New("injected")
					}
					return nil
				}
				t.Cleanup(func() { lifecycle.TestStage = nil })
				if err := Rename("relay", "relay2"); err == nil || !hit {
					t.Fatalf("the rename failing at %s gave %v (stage reached %v)", stage, err, hit)
				}
				lifecycle.TestStage = nil
				checkRecovered(t, h, before, managed)
			})
		}
	}
}

// TestLifecycleCrashingRename is the magpie of
// TestARenameExitingAtAStepIsRecovered: it exits at a step of the rename.
func TestLifecycleCrashingRename(t *testing.T) {
	stage := os.Getenv("MAGPIE_LIFECYCLE_EXIT_AT")
	if stage == "" {
		t.Skip("run by TestARenameExitingAtAStepIsRecovered")
	}
	lifecycle.TestStage = func(s string) error {
		if s == stage {
			os.Exit(3)
		}
		return nil
	}
	_ = Rename("relay", "relay2")
	os.Exit(4) // the step was never reached
}

// A magpie that exits in the middle of a rename's commit leaves a journal
// the next one finishes.
func TestARenameExitingAtAStepIsRecovered(t *testing.T) {
	for _, stage := range []string{"journal:write", "providers.json:write", "identities.json:keep:after", "lifecycle.json:write", "journal:remove:before"} {
		t.Run(stage, func(t *testing.T) {
			h, before := renameHome(t, true)
			c := exec.Command(os.Args[0], "-test.run=^TestLifecycleCrashingRename$", "-test.count=1")
			c.Env = append(os.Environ(), "MAGPIE_LIFECYCLE_EXIT_AT="+stage)
			out, err := c.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 3 {
				t.Fatalf("the crashing magpie: %v\n%s", err, out)
			}
			if _, err := os.Stat(lifecycle.JournalPath()); err != nil {
				t.Fatalf("no journal after a crash at %s: %v", stage, err)
			}
			checkRecovered(t, h, before, true)
		})
	}
}

// A providers.json.bak or identities.json.bak that is not a plain file
// refuses the write (design C.3a), and nothing is changed.
func TestAnUnusableBackupRefusesTheWrite(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, name := range []string{"providers.json.bak", "identities.json.bak"} {
			for _, kind := range []string{"directory", "symlink", "dangling symlink"} {
				t.Run(fmt.Sprintf("managed=%v/%s/%s", managed, name, kind), func(t *testing.T) {
					renameHome(t, managed)
					t.Setenv("MAGPIE_NO_ACCOUNTS", "1") // no discovery side effects before commit
					bak := filepath.Join(filepath.Dir(Path()), name)
					if err := os.Remove(bak); err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					if kind == "directory" {
						if err := os.Mkdir(bak, 0o700); err != nil {
							t.Fatal(err)
						}
					} else {
						target := filepath.Join(t.TempDir(), "backup-target")
						if kind == "symlink" {
							if err := os.WriteFile(target, []byte("must stay"), 0o600); err != nil {
								t.Fatal(err)
							}
						}
						if err := os.Symlink(target, bak); err != nil {
							t.Skipf("symlink unavailable: %v", err)
						}
						t.Cleanup(func() {
							b, err := os.ReadFile(target)
							if kind == "symlink" && (err != nil || string(b) != "must stay") || kind == "dangling symlink" && !os.IsNotExist(err) {
								t.Errorf("backup link target changed: %q (%v)", b, err)
							}
						})
					}
					before := commitDisk(t)
					hit := false
					lifecycle.TestStage = func(string) error { hit = true; return nil }
					t.Cleanup(func() { lifecycle.TestStage = nil })
					err := Rename("relay", "relay2")
					if err == nil || !strings.Contains(err.Error(), bak) || !strings.Contains(err.Error(), "move it aside and try again") {
						t.Fatalf("a rename with %s a %s gave %v; want its full path and advice to move it aside and try again", name, kind, err)
					}
					if hit {
						t.Fatal("unusable backup reached a journal write")
					}
					if after := commitDisk(t); !reflect.DeepEqual(after, before) {
						t.Fatalf("the refused rename changed a file, backup or evidence:\n got %#v\nwant %#v", after, before)
					}
				})
			}
		}
	}
}

// An identities.json that is there but blank may stand for its .bak's
// bindings: a write that would move one is refused, not planned over none.
func TestABlankIdentityStoreRefusesAMove(t *testing.T) {
	h, _ := renameHome(t, false)
	if err := os.WriteFile(identity.Path(), []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Rename("relay", "relay2"); err == nil || !strings.Contains(err.Error(), "identities.json") {
		t.Fatalf("a rename over a blank identities.json gave %v", err)
	}
	if _, err := Find("relay"); err != nil {
		t.Fatalf("the refused rename moved relay: %v", err)
	}
	// a write that moves no binding needs no identity store
	f, _ := read()
	f.Order = []string{"relay"}
	if err := store(f); err != nil {
		t.Fatalf("an order change over a blank identities.json: %v", err)
	}
	_ = h
}

// catalog.Touched runs after lifecycle's lock is released: what it calls
// back may write providers again.
func TestACommitTouchesTheCatalogUnlocked(t *testing.T) {
	h := newLifeHome(t)
	h.saveRows(h.row("relay"))
	old := catalog.Changed
	t.Cleanup(func() { catalog.Changed = old })
	var lockErr error
	called := false
	catalog.Changed = func() {
		called = true
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		unlock, err := lifecycle.Lock(ctx)
		if err == nil {
			unlock()
		}
		// any call under the lock fails, not only the last one
		lockErr = errors.Join(lockErr, err)
	}
	if err := Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	if !called || lockErr != nil {
		t.Fatalf("catalog.Touched called %v, under the lock: %v", called, lockErr)
	}
}

// What commit runs under the lock lists no providers or accounts and
// calls none of identity's writers (which take the lock again).
func TestACommitCallsNothingUnderTheLock(t *testing.T) {
	// per file, the functions that run under lifecycle's lock
	files := map[string]map[string]bool{
		"lifecycle_commit.go": {"commitHeld": true, "commitLocked": true, "diffOf": true, "identitiesPlan": true, "commitLegacy": true,
			"commitPlan": true, "commitManaged": true, "writeLegacy": true, "usableBak": true, "rowsByID": true,
			"boundLocked": true, "planBindings": true, "carryUnreconciled": true},
		"lifecycle_ready.go": {"readyHeld": true, "readyLocked": true, "bootstrap": true, "dropCorruptMeta": true, "adopt": true,
			"freshMeta": true, "reconcileHeld": true, "reconcileLocked": true, "unfinishable": true, "describeReconcile": true, "unwritable": true},
	}
	// plain calls, and package.func calls, that must not run under the lock
	banned := map[string]bool{"All": true, "Accounts": true, "Find": true, "load": true, "commit": true, "store": true,
		"lockCommit": true, "catalog.Touched": true, "lifecycle.Lock": true, "identity.Add": true,
		"identity.Remove": true, "identity.Confirm": true, "identity.Bind": true, "identity.Unbind": true,
		"Changed": true, "ReadyLifecycle": true, "readyLifecycle": true, "Reconcile": true, "PreviewReconcile": true,
		"identity.ReadSnapshot": true, "readSnapshot": true, "ReadIdentityView": true}
	for file, locked := range files {
		checkLockedCalls(t, file, locked, banned)
	}
}

func checkLockedCalls(t *testing.T, file string, locked, banned map[string]bool) {
	t.Helper()
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || !locked[fn.Name.Name] {
			continue
		}
		seen++
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				if x, ok := fun.X.(*ast.Ident); ok {
					name = x.Name + "." + fun.Sel.Name
				}
			}
			if banned[name] {
				t.Errorf("%s calls %s under lifecycle's lock", fn.Name.Name, name)
			}
			return true
		})
	}
	if seen != len(locked) {
		t.Fatalf("%s: found %d of the %d functions run under the lock", file, seen, len(locked))
	}
}

// No production write builds providers.json from nothing: store(file{…})
// has no base, so it would write over another magpie's commit unseen.
func TestNoProductionStoreOfALiteralFile(t *testing.T) {
	names, _ := filepath.Glob("*.go")
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("store(file{")) {
			t.Errorf("%s stores a literal file: read it first, so the write has a base", name)
		}
	}
	if len(names) < 50 {
		t.Fatalf("only %d source files found", len(names))
	}
}

// A third state lifecycle keeps aside is named for what it is: it differs
// from the recorded operation, it is not unparseable.
func TestAThirdStateIsNotCalledUnparseable(t *testing.T) {
	h, _ := renameHome(t, true)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	lifecycle.TestStage = func(s string) error {
		if s == "providers.json:write" {
			return errors.New("injected")
		}
		return nil
	}
	t.Cleanup(func() { lifecycle.TestStage = nil })
	_ = Rename("relay", "relay2")
	lifecycle.TestStage = nil
	// someone writes identities.json by hand before recovery
	if err := os.WriteFile(identity.Path(), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lifecycle.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = lifecycle.Recover()
	unlock()
	if !errors.As(err, new(*lifecycle.AmbiguousError)) {
		t.Fatalf("Recover over a third state: %v", err)
	}
	if strings.Contains(logs.String(), "doesn't parse") || !strings.Contains(logs.String(), "differs from") {
		t.Fatalf("log: %s", logs.String())
	}
	if kept, _ := filepath.Glob(identity.Path() + ".bad-*"); len(kept) != 1 {
		t.Fatalf("third state kept as %v", kept)
	}
	_ = h
}
