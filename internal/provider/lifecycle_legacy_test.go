package provider

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/lastgood"
)

// legacyStoreBeforeLifecycle is store at e4cf6bf7, before the lifecycle
// delegate. Keep this independent of commit/writeLegacy: their output,
// including the previous generation and bad-file evidence, is the contract.
func legacyStoreBeforeLifecycle(f file) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f.Groups = slices.Clone(f.Groups)
	for i := range f.Groups {
		f.Groups[i] = f.Groups[i].stored()
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := lastgood.Keep(p, validProviders, 0o600); err != nil {
		return err
	}
	if err := writePrivate(p, append(b, '\n')); err != nil {
		return err
	}
	pruneIcons(f)
	catalog.Touched()
	return nil
}

type commitDiskEntry struct {
	mode fs.FileMode
	data string
}

// commitDisk records every file and directory, not only providers.json.
// Bad-copy timestamps are normalized, but their bytes, modes and count
// remain significant. The persistent coordination lock is the only addition.
func commitDisk(t *testing.T) map[string][]commitDiskEntry {
	t.Helper()
	root := appdir.Config()
	out := map[string][]commitDiskEntry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "lifecycle.lock" {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		e := commitDiskEntry{mode: fi.Mode()}
		switch {
		case fi.Mode().IsRegular():
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			e.data = string(b)
		case fi.Mode()&os.ModeSymlink != 0:
			e.data, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		if i := strings.Index(rel, ".bad-"); i >= 0 {
			rel = rel[:i] + ".bad-*"
		}
		out[rel] = append(out[rel], e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, es := range out {
		slices.SortFunc(es, func(a, b commitDiskEntry) int {
			if a.mode != b.mode {
				return cmp.Compare(a.mode, b.mode)
			}
			return strings.Compare(a.data, b.data)
		})
	}
	return out
}

// Each intermediate state must match, not just the final primary: writing
// before Keep, skipping Keep, or changing the encoding must all fail here.
func TestLegacyStoreIsByteIdentical(t *testing.T) {
	oldChanged := catalog.Changed
	catalog.Changed = nil // no agent callbacks; this compares local persistence
	t.Cleanup(func() { catalog.Changed = oldChanged; lastgood.Reset() })
	type state = map[string][]commitDiskEntry
	run := func(t *testing.T, st func(file) error, want []state) []state {
		t.Helper()
		detectHome(t)
		lastgood.Reset()
		var states []state
		for i, step := range []struct {
			name    string
			primary []byte
		}{
			{"first", nil}, {"edited", nil}, {"edited", nil},
			{"after zeroed primary", make([]byte, 64)},
			{"after truncated primary", []byte("{")},
			{"after BOM", []byte("\xef\xbb\xbf{\"providers\":[]}")},
		} {
			if step.primary != nil {
				if err := os.WriteFile(Path(), step.primary, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f, err := read()
			if err != nil {
				t.Fatal(err)
			}
			f.Providers = []Provider{{ID: "relay", Name: step.name, Key: "sk-fixture", Chat: "http://127.0.0.1:1/v1",
				Headers: map[string]string{"X-Label": "a & b"}, Models: []string{"gpt-5"}}}
			f.Order = []string{"relay"}
			f.Groups = []Group{{ID: "fast", Name: "Fast", Members: []string{"relay/gpt-5", "relay/found"},
				Match: []string{"relay/*"}, Matched: []string{"relay/found"}}}
			groupBefore, err := json.Marshal(f.Groups)
			if err != nil {
				t.Fatal(err)
			}
			if err := st(f); err != nil {
				t.Fatalf("%s: %v", step.name, err)
			}
			groupAfter, err := json.Marshal(f.Groups)
			if err != nil || !bytes.Equal(groupBefore, groupAfter) {
				t.Fatalf("%s: storing changed the caller's groups (%v)", step.name, err)
			}
			got := commitDisk(t)
			if want != nil && !reflect.DeepEqual(got, want[i]) {
				t.Errorf("legacy write %d changed persisted bytes, modes or evidence:\n got %#v\nwant %#v", i+1, got, want[i])
			}
			states = append(states, got)
		}
		return states
	}
	var want []state
	if !t.Run("reference", func(t *testing.T) { want = run(t, legacyStoreBeforeLifecycle, nil) }) {
		return
	}
	t.Run("coordinated", func(t *testing.T) { run(t, store, want) })
}

// A zero-base fixture can still replace an undecodable primary, but must
// keep its literal bytes aside just as the original store did.
func TestLegacyBlindWriteIsByteIdentical(t *testing.T) {
	oldChanged := catalog.Changed
	catalog.Changed = nil
	t.Cleanup(func() { catalog.Changed = oldChanged; lastgood.Reset() })
	run := func(t *testing.T, st func(file) error) map[string][]commitDiskEntry {
		t.Helper()
		detectHome(t)
		if err := os.MkdirAll(appdir.Config(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(Path(), []byte("{\"providers\":"), 0o600); err != nil {
			t.Fatal(err)
		}
		f := load()
		if f.base.read {
			t.Fatal("the unreadable fixture unexpectedly has a read base")
		}
		f.Providers = []Provider{{ID: "relay", Name: "new", Key: "sk-fixture", Chat: "http://127.0.0.1:1/v1"}}
		if err := st(f); err != nil {
			t.Fatal(err)
		}
		return commitDisk(t)
	}
	var want, got map[string][]commitDiskEntry
	if !t.Run("reference", func(t *testing.T) { want = run(t, legacyStoreBeforeLifecycle) }) {
		return
	}
	if !t.Run("coordinated", func(t *testing.T) { got = run(t, store) }) {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy blind write changed bytes, modes or evidence:\n got %#v\nwant %#v", got, want)
	}
}

// A write that moves no binding is the original store even when
// providers.json.bak is not a plain file: a linked .bak (live or
// dangling) is replaced by the kept generation, and a folder fails before
// the primary is touched. Only a journal refuses such a .bak up front.
func TestLegacyStoreKeepsAnUnusualBackupAsBefore(t *testing.T) {
	oldChanged := catalog.Changed
	catalog.Changed = nil
	t.Cleanup(func() { catalog.Changed = oldChanged; lastgood.Reset() })
	const primary = "{\"providers\":[{\"id\":\"relay\",\"name\":\"a\",\"chat\":\"http://127.0.0.1:1/v1\"}]}\n"
	type outcome struct {
		failed           bool
		primary, bak     string
		bakType, tgtData string
	}
	run := func(t *testing.T, kind string, st func(file) error) outcome {
		t.Helper()
		detectHome(t)
		lastgood.Reset()
		if err := os.MkdirAll(appdir.Config(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(Path(), []byte(primary), 0o600); err != nil {
			t.Fatal(err)
		}
		bak := lastgood.Bak(Path())
		target := filepath.Join(t.TempDir(), "target")
		switch kind {
		case "directory":
			if err := os.Mkdir(bak, 0o700); err != nil {
				t.Fatal(err)
			}
		case "symlink":
			if err := os.WriteFile(target, []byte("must stay"), 0o600); err != nil {
				t.Fatal(err)
			}
			fallthrough
		default:
			if err := os.Symlink(target, bak); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}
		f, err := read()
		if err != nil {
			t.Fatal(err)
		}
		f.Order = []string{"relay"} // no id changes: the legacy fast path
		var o outcome
		o.failed = st(f) != nil
		b, err := os.ReadFile(Path())
		if err != nil {
			t.Fatal(err)
		}
		o.primary = string(b)
		fi, err := os.Lstat(bak)
		if err != nil {
			t.Fatal(err)
		}
		o.bakType = fi.Mode().Type().String()
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(bak)
			if err != nil {
				t.Fatal(err)
			}
			o.bak = string(b)
		}
		if b, err := os.ReadFile(target); err == nil {
			o.tgtData = string(b)
		}
		return o
	}
	for _, kind := range []string{"directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			var want, got outcome
			if !t.Run("reference", func(t *testing.T) { want = run(t, kind, legacyStoreBeforeLifecycle) }) {
				return
			}
			if !t.Run("coordinated", func(t *testing.T) { got = run(t, kind, store) }) {
				return
			}
			if got != want {
				t.Fatalf("a %s providers.json.bak is handled unlike the original store:\n got %+v\nwant %+v", kind, got, want)
			}
		})
	}
}
