package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
)

// readyHome saves rows the legacy way (no lifecycle.json), binds the
// given ids and readies lifecycle state, as a start-up would.
func readyHome(t *testing.T, binds map[string]identity.Profile, ids ...string) *lifeHome {
	t.Helper()
	h := newLifeHome(t)
	var rows []Provider
	for _, id := range ids {
		rows = append(rows, h.row(id))
	}
	h.saveRows(rows...)
	for id, p := range binds {
		if err := identity.Bind(id, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := readyLifecycle(); err != nil || out != readyBootstrapped {
		t.Fatalf("ReadyLifecycle: %v %v, want bootstrapped", out, err)
	}
	return h
}

// writeOutside writes providers.json as an older magpie or an editor
// would: lifecycle.json is not touched.
func writeOutside(t *testing.T, rows ...Provider) {
	t.Helper()
	b, err := json.MarshalIndent(file{Providers: rows}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// configSnapshot is every file under the config folder and its bytes'
// hash, the lock file left out (taking a lock is not a write of state).
func configSnapshot(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := appdir.Config()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		if rel == "lifecycle.lock" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(h[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSnapshot(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed %s", what, k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("%s created %s", what, k)
		}
	}
}

func asides(t *testing.T, base, kind string) []string {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(appdir.Config(), base+"."+kind+"-*"))
	return names
}

// ReadyLifecycle in each state lifecycle.json and providers.json can be in.
func TestReadyLifecycleStates(t *testing.T) {
	t.Run("no config folder", func(t *testing.T) {
		detectHome(t)
		if out, err := readyLifecycle(); err != nil || out != readyLegacy {
			t.Fatalf("%v %v", out, err)
		}
		if _, err := os.Stat(appdir.Config()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("ReadyLifecycle made the config folder (%v)", err)
		}
	})
	t.Run("no providers.json", func(t *testing.T) {
		detectHome(t)
		if err := os.MkdirAll(appdir.Config(), 0o700); err != nil {
			t.Fatal(err)
		}
		if out, err := readyLifecycle(); err != nil || out != readyLegacy {
			t.Fatalf("%v %v", out, err)
		}
		if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("lifecycle.json made for no providers.json (%v)", err)
		}
	})
	t.Run("bootstrap", func(t *testing.T) {
		h := readyHome(t, nil, "relay", "other")
		m := readMetaT(t)
		raw, _ := os.ReadFile(Path())
		if m.Gen != 1 || m.Digest != lifecycle.Digest(raw) || len(m.Providers) != 2 || len(m.Retired)+len(m.Unreconciled) != 0 {
			t.Fatalf("bootstrapped %+v", m)
		}
		rows, _ := rowsByID(load())
		for id, p := range m.Providers {
			if p.Rev != 1 || p.Row != lifecycle.Digest(rows[id]) {
				t.Fatalf("%s: %+v", id, p)
			}
		}
		if m.Providers["relay"].Instance == m.Providers["other"].Instance {
			t.Fatal("two ids share an instance")
		}
		before, _ := os.ReadFile(lifecycle.Path())
		if out, err := readyLifecycle(); err != nil || out != readyCurrent {
			t.Fatalf("second ReadyLifecycle: %v %v", out, err)
		}
		if after, _ := os.ReadFile(lifecycle.Path()); !bytes.Equal(before, after) {
			t.Fatal("a ReadyLifecycle with nothing to do wrote lifecycle.json")
		}
		if p := h.find("relay"); p.life.kind != lifeManaged {
			t.Fatalf("after bootstrap Find gave %+v", p.life)
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		h := readyHome(t, nil, "relay")
		if err := identity.Bind("relay", h.a.ID); err != nil {
			t.Fatal(err)
		}
		old := readMetaT(t)
		held := h.find("relay")
		if err := os.WriteFile(lifecycle.Path(), []byte(`{"version":1,`), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := readyLifecycle()
		if err != nil || out != readyRebootstrapped {
			t.Fatalf("%v %v", out, err)
		}
		m := readMetaT(t)
		if m.Epoch == old.Epoch || m.Gen != 1 {
			t.Fatalf("rebootstrapped %+v (old epoch %s)", m, old.Epoch)
		}
		if bad := asides(t, "lifecycle.json", "bad"); len(bad) != 1 {
			t.Fatalf("the corrupt lifecycle.json was not kept: %v", bad)
		}
		if _, err := h.sign(held); !errors.Is(err, ErrStale) {
			t.Fatalf("a value read before the rebootstrap: %v, want ErrStale", err)
		}
		if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
			t.Fatalf("relay after the rebootstrap sent %q (%v)", got, err)
		}
	})
	t.Run("corrupt, no providers.json", func(t *testing.T) {
		detectHome(t)
		if err := os.MkdirAll(appdir.Config(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lifecycle.Path(), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := readyLifecycle(); err != nil || out != readyLegacy {
			t.Fatalf("%v %v", out, err)
		}
		if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, fs.ErrNotExist) || len(asides(t, "lifecycle.json", "bad")) != 1 {
			t.Fatalf("a corrupt lifecycle.json with nothing to describe is kept aside and removed (%v)", err)
		}
	})
	for name, put := range map[string]func(*testing.T){
		"unsupported": func(t *testing.T) {
			if err := os.WriteFile(lifecycle.Path(), []byte(`{"version":99}`), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"unreadable": func(t *testing.T) {
			if err := os.Remove(lifecycle.Path()); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(lifecycle.Path(), 0o700); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			readyHome(t, nil, "relay")
			put(t)
			before := configSnapshot(t)
			out, err := readyLifecycle()
			if out != readyFailed || !errors.Is(err, lifecycle.ErrUnknown) {
				t.Fatalf("%v %v, want failed with ErrUnknown", out, err)
			}
			sameSnapshot(t, "ReadyLifecycle over "+name+" lifecycle.json", before, configSnapshot(t))
		})
	}
	t.Run("lock held", func(t *testing.T) {
		h := newLifeHome(t)
		h.saveRows(h.row("relay"))
		was := readyLockWait
		readyLockWait = 200 * time.Millisecond
		t.Cleanup(func() { readyLockWait = was })
		unlock, err := lifecycle.Lock(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		done := make(chan error, 1)
		var out readyOutcome
		go func() { var err error; out, err = readyLifecycle(); done <- err }()
		select {
		case err := <-done:
			if err != nil || out != readyLegacy {
				t.Fatalf("%v %v", out, err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("ReadyLifecycle waited for the lock")
		}
		if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("lifecycle.json made without the lock (%v)", err)
		}
	})
	t.Run("lock unusable", func(t *testing.T) {
		h := newLifeHome(t)
		h.saveRows(h.row("relay"))
		lock := filepath.Join(appdir.Config(), "lifecycle.lock")
		_ = os.Remove(lock)
		if err := os.Mkdir(lock, 0o700); err != nil {
			t.Fatal(err)
		}
		if out, err := readyLifecycle(); err != nil || out != readyLegacy {
			t.Fatalf("%v %v", out, err)
		}
		if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("lifecycle.json made without the lock (%v)", err)
		}
	})
	t.Run("a dead writer's journal", func(t *testing.T) {
		readyHome(t, nil, "relay")
		lifecycle.TestStage = func(s string) error {
			if s == "providers.json:write" {
				return errors.New("died")
			}
			return nil
		}
		t.Cleanup(func() { lifecycle.TestStage = nil })
		if err := Rename("relay", "relay2"); err == nil {
			t.Fatal("the injected failure didn't stop the rename")
		}
		lifecycle.TestStage = nil
		out, err := readyLifecycle()
		if err != nil || out != readyCurrent {
			t.Fatalf("%v %v", out, err)
		}
		if _, err := os.Stat(lifecycle.JournalPath()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("ReadyLifecycle left the journal")
		}
		if _, ok := readMetaT(t).Providers["relay2"]; !ok {
			t.Fatal("the rename was not finished")
		}
	})
	t.Run("a journal that can't be finished", func(t *testing.T) {
		h := readyHome(t, nil, "relay")
		lifecycle.TestStage = func(s string) error {
			if s == "journal:write" {
				return errors.New("died")
			}
			return nil
		}
		t.Cleanup(func() { lifecycle.TestStage = nil })
		_ = Rename("relay", "relay2")
		lifecycle.TestStage = nil
		writeOutside(t, h.row("someone-else"))
		before := configSnapshot(t)
		out, err := readyLifecycle()
		var amb *lifecycle.AmbiguousError
		if out != readyFailed || !errors.As(err, &amb) {
			t.Fatalf("%v %v, want failed with AmbiguousError", out, err)
		}
		after := configSnapshot(t)
		if after["lifecycle-journal.json"] == "" || after["lifecycle.json"] != before["lifecycle.json"] || after["providers.json"] != before["providers.json"] {
			t.Fatal("an ambiguous journal changed state")
		}
	})
	t.Run("providers.json read from its .bak", func(t *testing.T) {
		readyHome(t, nil, "relay")
		if err := Rename("relay", "relay2"); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(Path())
		if err := os.WriteFile(Path(), make([]byte, len(raw)), 0o600); err != nil {
			t.Fatal(err)
		}
		before := configSnapshot(t)
		if out, err := readyLifecycle(); err != nil || out != readyPending {
			t.Fatalf("%v %v", out, err)
		}
		sameSnapshot(t, "ReadyLifecycle over a providers.json read from its .bak", before, configSnapshot(t))
	})
}

// denyWrites makes the config folder one magpie can't create files in
// (lifecycle.lock, made by an earlier write, stays usable), until the
// test ends. As root on Unix nothing is denied, so the test is skipped.
func denyWrites(t *testing.T) {
	t.Helper()
	dir := appdir.Config()
	if _, err := os.Stat(filepath.Join(dir, "lifecycle.lock")); err != nil {
		t.Fatalf("no lifecycle.lock before the folder is shut: %v", err)
	}
	if runtime.GOOS == "windows" {
		icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
		// Everyone: no new file (WD), no append (AD), no removal of one (DC)
		if out, err := exec.Command(icacls, dir, "/deny", "*S-1-1-0:(WD,AD,DC)").CombinedOutput(); err != nil {
			t.Fatalf("icacls: %v %s", err, out)
		}
		t.Cleanup(func() {
			if out, err := exec.Command(icacls, dir, "/remove:d", "*S-1-1-0").CombinedOutput(); err != nil {
				t.Errorf("icacls: %v %s", err, out)
			}
		})
	} else {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}
	if f, err := os.CreateTemp(dir, "probe"); err == nil {
		f.Close()
		os.Remove(f.Name())
		if runtime.GOOS != "windows" {
			t.Skip("permissions are not enforced (root)")
		}
		t.Fatal("the config folder is still writable")
	}
}

// design §6: a config folder magpie can't write leaves start-up as it
// was, with no error: no lifecycle.json is made (legacy), and a change made
// outside magpie is left for a later write (signing projects it). The
// lock file is there already, as any earlier provider write leaves it.
func TestReadyLifecycleInAFolderItCantWrite(t *testing.T) {
	t.Run("bootstrap", func(t *testing.T) {
		h := newLifeHome(t)
		h.saveRows(h.row("relay"))
		if err := identity.Bind("relay", h.a.ID); err != nil {
			t.Fatal(err)
		}
		denyWrites(t)
		before := configSnapshot(t)
		if out, err := readyLifecycle(); err != nil || out != readyLegacy {
			t.Fatalf("ReadyLifecycle in a folder it can't write: %v %v, want legacy <nil>", out, err)
		}
		sameSnapshot(t, "ReadyLifecycle in a folder it can't write", before, configSnapshot(t))
		if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
			t.Fatalf("relay, legacy, sent %q (%v), want its profile", got, err)
		}
	})
	t.Run("adoption", func(t *testing.T) {
		h := readyHome(t, nil, "relay", "gone")
		if err := identity.Bind("relay", h.a.ID); err != nil {
			t.Fatal(err)
		}
		if err := identity.Bind("gone", h.b.ID); err != nil {
			t.Fatal(err)
		}
		writeOutside(t, h.row("relay"))
		denyWrites(t)
		before := configSnapshot(t)
		if out, err := readyLifecycle(); err != nil || out != readyPending {
			t.Fatalf("ReadyLifecycle in a folder it can't write: %v %v, want pending <nil>", out, err)
		}
		sameSnapshot(t, "ReadyLifecycle in a folder it can't write", before, configSnapshot(t))
		if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
			t.Fatalf("relay sent %q (%v), want its profile", got, err)
		}
	})
}

// A change made outside magpie is adopted into lifecycle.json alone: an
// edited row keeps its instance at the next rev and no other row's rev
// moves, an unbound id added or removed is a new instance or a tombstone,
// and only ids the change leaves a binding undecided for are marked.
func TestAdoptionMarksOnlyUndecidedBindings(t *testing.T) {
	h := newLifeHome(t)
	h.saveRows(h.row("relay"), h.row("plain"), h.row("gone"), h.row("dropped"))
	for id, p := range map[string]identity.Profile{"relay": h.a, "gone": h.b, "orphan": h.b} {
		if err := identity.Bind(id, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := readyLifecycle(); err != nil || out != readyBootstrapped {
		t.Fatalf("%v %v", out, err)
	}
	before := readMetaT(t)
	oldGone, oldDropped := h.find("gone"), h.find("dropped")
	relay := h.row("relay")
	relay.Name = "edited by hand"
	writeOutside(t, relay, h.row("plain"), h.row("added"), h.row("orphan"))
	ids0, _ := os.ReadFile(identity.Path())
	out, err := readyLifecycle()
	if err != nil || out != readyAdopted {
		t.Fatalf("%v %v", out, err)
	}
	if ids1, _ := os.ReadFile(identity.Path()); !bytes.Equal(ids0, ids1) {
		t.Fatal("adoption wrote identities.json")
	}
	m := readMetaT(t)
	raw, _ := os.ReadFile(Path())
	if m.Gen != before.Gen+1 || m.Digest != lifecycle.Digest(raw) || m.Epoch != before.Epoch {
		t.Fatalf("adopted gen %d digest ok %v", m.Gen, m.Digest == lifecycle.Digest(raw))
	}
	if p := m.Providers["relay"]; p.Instance != before.Providers["relay"].Instance || p.Rev != 2 {
		t.Fatalf("edited relay: %+v", p)
	}
	if p := m.Providers["plain"]; p != before.Providers["plain"] {
		t.Fatalf("unchanged plain moved: %+v → %+v", before.Providers["plain"], p)
	}
	for _, id := range []string{"added", "orphan"} {
		if p, ok := m.Providers[id]; !ok || p.Rev != 1 {
			t.Fatalf("%s: %+v", id, p)
		}
	}
	var retired []string
	for _, r := range m.Retired {
		for id, p := range before.Providers {
			if p.Instance == r.Instance {
				retired = append(retired, id+map[bool]string{true: ":bound", false: ""}[r.Bound])
			}
		}
	}
	slices.Sort(retired)
	if strings.Join(retired, ",") != "dropped,gone:bound" {
		t.Fatalf("retired %v", retired)
	}
	var marked []string
	for _, u := range m.Unreconciled {
		marked = append(marked, u.ID)
	}
	if strings.Join(marked, ",") != "gone,orphan" {
		t.Fatalf("marked %v, want gone and orphan alone", marked)
	}
	if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
		t.Errorf("the edited relay sent %q (%v), want its profile", got, err)
	}
	if got, err := h.sign(h.find("plain")); err != nil || got == uaOf(h.a) || got == uaOf(h.b) {
		t.Errorf("plain sent %q (%v)", got, err)
	}
	if _, err := h.sign(h.find("orphan")); !errors.Is(err, lifecycle.ErrUnknown) {
		t.Errorf("an orphan-bound new id: %v, want ErrUnknown", err)
	}
	if _, err := h.sign(oldGone); !errors.Is(err, ErrStale) {
		t.Errorf("a value of the bound id removed outside magpie: %v, want ErrStale", err)
	}
	if got, err := h.sign(oldDropped); err != nil || got == uaOf(h.a) || got == uaOf(h.b) {
		t.Errorf("a value of the unbound id removed outside magpie sent %q (%v), want magpie's own", got, err)
	}
}

// A provider write over a change made outside magpie adopts it, where G2b
// refused every write: the result is one generation on, with the
// undecided ids marked; a later rename carries a mark, a delete clears it.
func TestAManagedWriteAdoptsAChangeMadeOutside(t *testing.T) {
	h := readyHome(t, nil, "relay", "gone")
	for id, p := range map[string]identity.Profile{"relay": h.a, "gone": h.b, "orphan": h.b} {
		if err := identity.Bind(id, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	gen := readMetaT(t).Gen
	writeOutside(t, h.row("relay"), h.row("orphan"))
	h.saveRows(append(load().Providers, h.row("mine"))...)
	m := readMetaT(t)
	raw, _ := os.ReadFile(Path())
	if m.Gen != gen+1 || m.Digest != lifecycle.Digest(raw) {
		t.Fatalf("gen %d (was %d), digest ok %v", m.Gen, gen, m.Digest == lifecycle.Digest(raw))
	}
	if !m.IsUnreconciled("gone") || !m.IsUnreconciled("orphan") || m.IsUnreconciled("relay") || m.IsUnreconciled("mine") {
		t.Fatalf("marked %+v", m.Unreconciled)
	}
	if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
		t.Fatalf("relay after the adopting write sent %q (%v)", got, err)
	}
	// a rename carries the mark (and the binding) to the new id
	if err := Rename("orphan", "orphan2"); err != nil {
		t.Fatal(err)
	}
	if m = readMetaT(t); m.IsUnreconciled("orphan") || !m.IsUnreconciled("orphan2") {
		t.Fatalf("after the rename marked %+v", m.Unreconciled)
	}
	if _, err := h.sign(h.find("orphan2")); !errors.Is(err, lifecycle.ErrUnknown) {
		t.Fatalf("the renamed undecided id: %v, want ErrUnknown", err)
	}
	// a delete takes the binding, and the mark, with it
	if err := Delete("orphan2"); err != nil {
		t.Fatal(err)
	}
	if m = readMetaT(t); m.IsUnreconciled("orphan2") {
		t.Fatalf("after the delete marked %+v", m.Unreconciled)
	}
}

// providers.json read from its .bak (lastgood) is adopted by the next
// write, which writes the primary again: the id the .bak doesn't have is
// retired, and stays undecided while bound.
func TestAWriteOverAFallbackAdoptsTheBak(t *testing.T) {
	h := readyHome(t, nil, "relay")
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	if err := Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	held := h.find("relay2")
	raw, _ := os.ReadFile(Path())
	if err := os.WriteFile(Path(), make([]byte, len(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	// before any write: relay2 (bound) isn't in the .bak's rows
	if _, err := h.sign(held); !errors.Is(err, lifecycle.ErrUnknown) {
		t.Fatalf("relay2, bound, not in the .bak read: %v, want ErrUnknown", err)
	}
	if got, err := h.sign(h.find("relay")); err != nil || got == uaOf(h.a) {
		t.Fatalf("relay from the .bak sent %q (%v); its binding moved to relay2", got, err)
	}
	h.saveRows(append(load().Providers, h.row("mine"))...)
	m := readMetaT(t)
	raw, _ = os.ReadFile(Path())
	if m.Digest != lifecycle.Digest(raw) || !m.IsUnreconciled("relay2") {
		t.Fatalf("after the write: %+v", m)
	}
	if bad := asides(t, "providers.json", "bad"); len(bad) == 0 {
		t.Fatal("the zeroed providers.json was not kept aside")
	}
	if _, err := h.sign(held); !errors.Is(err, ErrStale) {
		t.Fatalf("relay2's old value after adoption: %v, want ErrStale", err)
	}
}

// Recovery A (accepted): identities.json read from its .bak may bring
// back the latest unbind. The lifecycle adds no other way back: an alias
// (Was) signs as its instance's current id, a tombstone keeps a deleted
// instance's value stale, and an id added again outside magpie over a
// binding the .bak brought back is undecided, not signed.
func TestRecoveryAAddsNoOtherWayBack(t *testing.T) {
	zero := func(t *testing.T) {
		t.Helper()
		raw, _ := os.ReadFile(identity.Path())
		if err := os.WriteFile(identity.Path(), make([]byte, len(raw)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("unbind", func(t *testing.T) {
		h := readyHome(t, map[string]identity.Profile{}, "relay")
		if err := identity.Bind("relay", h.a.ID); err != nil {
			t.Fatal(err)
		}
		if err := identity.Unbind("relay"); err != nil {
			t.Fatal(err)
		}
		zero(t)
		// documented: the .bak is the generation before the unbind
		if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
			t.Fatalf("relay after the store fell back sent %q (%v), want the binding the .bak holds, as documented", got, err)
		}
	})
	t.Run("rename, then unbind", func(t *testing.T) {
		h := readyHome(t, map[string]identity.Profile{}, "relay")
		if err := identity.Bind("relay", h.a.ID); err != nil {
			t.Fatal(err)
		}
		if err := Rename("relay", "relay2"); err != nil {
			t.Fatal(err)
		}
		h.saveRows(append(load().Providers, h.row("relay"))...) // another provider takes "relay"
		if err := identity.Unbind("relay2"); err != nil {
			t.Fatal(err)
		}
		zero(t)
		if got, _ := h.sign(h.find("relay2")); got != uaOf(h.a) {
			t.Fatalf("relay2 sent %q, want the documented comeback %q", got, uaOf(h.a))
		}
		// the new provider under the old id never borrows it
		if got, err := h.sign(h.find("relay")); err != nil || got == uaOf(h.a) {
			t.Fatalf("the new relay sent %q (%v)", got, err)
		}
	})
	t.Run("delete, then added again outside", func(t *testing.T) {
		h := readyHome(t, map[string]identity.Profile{}, "relay", "other")
		if err := identity.Bind("relay", h.a.ID); err != nil {
			t.Fatal(err)
		}
		held := h.find("relay")
		if err := Delete("relay"); err != nil {
			t.Fatal(err)
		}
		// the delete's commit kept the generation before it, relay bound
		zero(t)
		if _, err := h.sign(held); !errors.Is(err, ErrStale) {
			t.Fatalf("the deleted relay's value with its binding back: %v, want ErrStale", err)
		}
		if _, err := Find("relay"); err == nil {
			t.Fatal("a deleted provider was found again")
		}
		writeOutside(t, h.row("other"), h.row("relay"))
		if got, err := h.sign(h.find("relay")); !errors.Is(err, lifecycle.ErrUnknown) {
			t.Fatalf("relay added outside over the binding the .bak brought back sent %q (%v), want ErrUnknown", got, err)
		}
	})
}

// magpie identity reconcile's preview writes nothing; --yes keeps the old
// lifecycle.json aside, adopts, and keeps or drops the undecided bindings
// as asked; without a choice it writes nothing.
func TestReconcile(t *testing.T) {
	setup := func(t *testing.T) *lifeHome {
		h := readyHome(t, nil, "relay", "gone")
		for id, p := range map[string]identity.Profile{"relay": h.a, "gone": h.b, "orphan": h.b} {
			if err := identity.Bind(id, p.ID); err != nil {
				t.Fatal(err)
			}
		}
		writeOutside(t, h.row("relay"), h.row("orphan"))
		return h
	}
	affected := func(p ReconcilePlan) string {
		var s []string
		for _, a := range p.Affected {
			s = append(s, a.ID+map[bool]string{true: ":gone", false: ""}[a.Gone])
		}
		return strings.Join(s, ",")
	}
	t.Run("preview", func(t *testing.T) {
		setup(t)
		before := configSnapshot(t)
		lock := filepath.Join(appdir.Config(), "lifecycle.lock")
		_ = os.Remove(lock)
		plan, err := PreviewReconcile()
		if err != nil || plan.State != "adopt" || affected(plan) != "gone:gone,orphan" {
			t.Fatalf("%+v %v", plan, err)
		}
		sameSnapshot(t, "a preview", before, configSnapshot(t))
		if _, err := os.Stat(lock); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("a preview made the lock file")
		}
	})
	t.Run("no choice", func(t *testing.T) {
		setup(t)
		before := configSnapshot(t)
		plan, err := Reconcile(ReconcileUndecided)
		if !errors.Is(err, ErrReconcileChoice) || plan.Wrote || affected(plan) != "gone:gone,orphan" {
			t.Fatalf("%+v %v", plan, err)
		}
		sameSnapshot(t, "a reconcile with no choice", before, configSnapshot(t))
	})
	for _, choice := range []ReconcileChoice{ReconcileKeep, ReconcileDrop} {
		name := map[ReconcileChoice]string{ReconcileKeep: "keep", ReconcileDrop: "drop"}[choice]
		t.Run(name, func(t *testing.T) {
			h := setup(t)
			old, _ := os.ReadFile(lifecycle.Path())
			plan, err := Reconcile(choice)
			if err != nil || !plan.Wrote {
				t.Fatalf("%+v %v", plan, err)
			}
			kept := asides(t, "lifecycle.json", "reconciled")
			if len(kept) != 1 {
				t.Fatalf("old lifecycle.json kept as %v", kept)
			}
			if b, _ := os.ReadFile(kept[0]); !bytes.Equal(b, old) {
				t.Fatal("the kept copy is not the old lifecycle.json")
			}
			m := readMetaT(t)
			raw, _ := os.ReadFile(Path())
			if len(m.Unreconciled) != 0 || m.Digest != lifecycle.Digest(raw) {
				t.Fatalf("after reconcile: %+v", m)
			}
			b := boundNow(t)
			if b["relay"] != h.a.ID {
				t.Fatalf("reconcile changed a binding nothing left undecided: %v", b)
			}
			if choice == ReconcileDrop {
				if b["orphan"] != "" || b["gone"] != "" {
					t.Fatalf("--drop left %v", b)
				}
				if got, err := h.sign(h.find("orphan")); err != nil || got == uaOf(h.b) {
					t.Fatalf("orphan after --drop sent %q (%v)", got, err)
				}
			} else {
				if b["orphan"] != h.b.ID || b["gone"] != h.b.ID {
					t.Fatalf("--keep changed %v", b)
				}
				if got, err := h.sign(h.find("orphan")); err != nil || got != uaOf(h.b) {
					t.Fatalf("orphan after --keep sent %q (%v)", got, err)
				}
			}
			if plan, err := PreviewReconcile(); err != nil || plan.State != "current" || len(plan.Affected) != 0 {
				t.Fatalf("preview after reconcile: %+v %v", plan, err)
			}
		})
	}
	t.Run("adopted at start-up, then reconciled", func(t *testing.T) {
		h := setup(t)
		if out, err := readyLifecycle(); err != nil || out != readyAdopted {
			t.Fatalf("%v %v", out, err)
		}
		plan, err := PreviewReconcile()
		if err != nil || plan.State != "current" || affected(plan) != "gone:gone,orphan" {
			t.Fatalf("%+v %v", plan, err)
		}
		if plan, err := Reconcile(ReconcileKeep); err != nil || !plan.Wrote {
			t.Fatalf("%+v %v", plan, err)
		}
		if got, err := h.sign(h.find("orphan")); err != nil || got != uaOf(h.b) {
			t.Fatalf("orphan after --keep sent %q (%v)", got, err)
		}
	})
	t.Run("a journal that can't be finished", func(t *testing.T) {
		h := readyHome(t, nil, "relay")
		if err := identity.Bind("relay", h.a.ID); err != nil {
			t.Fatal(err)
		}
		lifecycle.TestStage = func(s string) error {
			if s == "journal:write" {
				return errors.New("died")
			}
			return nil
		}
		t.Cleanup(func() { lifecycle.TestStage = nil })
		_ = Rename("relay", "relay2")
		lifecycle.TestStage = nil
		writeOutside(t, h.row("someone-else"))
		journal, _ := os.ReadFile(lifecycle.JournalPath())
		before := configSnapshot(t)
		plan, err := PreviewReconcile()
		if err != nil || plan.Pending == "" {
			t.Fatalf("preview: %+v %v", plan, err)
		}
		sameSnapshot(t, "a preview with a journal", before, configSnapshot(t))
		plan, err = Reconcile(ReconcileDrop)
		if err != nil || !plan.Wrote {
			t.Fatalf("%+v %v", plan, err)
		}
		kept := asides(t, "lifecycle-journal.json", "reconciled")
		if len(kept) != 1 {
			t.Fatalf("journal kept as %v", kept)
		}
		if b, _ := os.ReadFile(kept[0]); !bytes.Equal(b, journal) {
			t.Fatal("the kept journal is not the journal")
		}
		if _, err := os.Stat(lifecycle.JournalPath()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("the journal is still there")
		}
		if affected(plan) != "relay:gone" || boundNow(t)["relay"] != "" {
			t.Fatalf("affected %q, bound %v", affected(plan), boundNow(t))
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		readyHome(t, nil, "relay")
		if err := os.WriteFile(lifecycle.Path(), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if plan, err := PreviewReconcile(); err != nil || plan.State != "corrupt" {
			t.Fatalf("preview: %+v %v", plan, err)
		}
		plan, err := Reconcile(ReconcileUndecided)
		if err != nil || !plan.Wrote || len(plan.Kept) != 1 {
			t.Fatalf("%+v %v", plan, err)
		}
		if m := readMetaT(t); m.Gen != 1 {
			t.Fatalf("rebootstrapped %+v", m)
		}
	})
	t.Run("legacy", func(t *testing.T) {
		h := newLifeHome(t)
		h.saveRows(h.row("relay"))
		before := configSnapshot(t)
		for _, f := range []func() (ReconcilePlan, error){PreviewReconcile, func() (ReconcilePlan, error) { return Reconcile(ReconcileDrop) }} {
			if plan, err := f(); err != nil || plan.State != "legacy" || plan.Wrote {
				t.Fatalf("%+v %v", plan, err)
			}
		}
		sameSnapshot(t, "a legacy reconcile", before, configSnapshot(t))
	})
}

// A managed commit records each row's digest as stored, so a later change
// made outside magpie bumps only the rows it changed: here a row edited
// through magpie, then another edited by hand.
func TestACommitKeepsRowDigests(t *testing.T) {
	h := readyHome(t, nil, "relay", "other")
	f, err := read()
	if err != nil {
		t.Fatal(err)
	}
	f.Providers[1].Name = "edited in magpie"
	if err := store(f); err != nil {
		t.Fatal(err)
	}
	m := readMetaT(t)
	rows, _ := rowsByID(load())
	for id, p := range m.Providers {
		if p.Row != lifecycle.Digest(rows[id]) {
			t.Fatalf("%s: row digest %q is not its row's", id, p.Row)
		}
	}
	relay := h.row("relay")
	relay.Name = "edited by hand"
	writeOutside(t, relay, load().Providers[1])
	if out, err := readyLifecycle(); err != nil || out != readyAdopted {
		t.Fatalf("%v %v", out, err)
	}
	after := readMetaT(t)
	if after.Providers["other"] != m.Providers["other"] || after.Providers["relay"].Rev != m.Providers["relay"].Rev+1 {
		t.Fatalf("revs after a hand edit of relay alone: %+v → %+v", m.Providers, after.Providers)
	}
}

// ReadyLifecycle's and Reconcile's locked sections read providers.json
// only directly, never through a provider or account listing, at
// bootstrap, adoption and reconcile.
func TestReadyAndReconcileMakeNoProviderReads(t *testing.T) {
	h := newLifeHome(t)
	h.saveRows(h.row("relay"), h.row("gone"))
	for _, id := range []string{"relay", "gone"} {
		if err := identity.Bind(id, h.a.ID); err != nil {
			t.Fatal(err)
		}
	}
	clean := watchCommitReads(t)
	defer clean()
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(commitReadViolation); !ok {
				panic(p)
			}
			t.Errorf("indirect provider read under lifecycle's lock: %v", p)
		}
	}()
	if out, err := readyLifecycle(); err != nil || out != readyBootstrapped {
		t.Fatalf("%v %v", out, err)
	}
	writeOutside(t, h.row("relay"))
	if out, err := readyLifecycle(); err != nil || out != readyAdopted {
		t.Fatalf("%v %v", out, err)
	}
	if err := os.WriteFile(lifecycle.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if plan, err := Reconcile(ReconcileDrop); err != nil || !plan.Wrote {
		t.Fatalf("%+v %v", plan, err)
	}
	writeOutside(t, h.row("relay"), h.row("new"))
	if plan, err := Reconcile(ReconcileDrop); err != nil || !plan.Wrote {
		t.Fatalf("%+v %v", plan, err)
	}
}
