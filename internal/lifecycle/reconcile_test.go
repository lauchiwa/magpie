package lifecycle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/appdir"
)

// Row and Unreconciled round-trip; metadata without them encodes exactly
// as before they existed (omitempty), so G1/G2 metadata reads unchanged.
func TestMetaRowAndUnreconciledRoundTrip(t *testing.T) {
	m := meta(3, []byte("p"))
	plain := encoded(t, m)
	if bytes.Contains(plain, []byte(`"row"`)) || bytes.Contains(plain, []byte(`"unreconciled"`)) {
		t.Fatalf("metadata without rows or marks names them:\n%s", plain)
	}
	inst := m.Providers["a"].Instance
	p := m.Providers["a"]
	p.Row = Digest([]byte("row"))
	m.Providers["a"] = p
	m.Unreconciled = []Unreconciled{{ID: "a", Instance: inst}, {ID: "gone", Instance: strings.Repeat("c", 32)}}
	got, err := DecodeMeta(encoded(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if got.Providers["a"].Row != p.Row || len(got.Unreconciled) != 2 || !got.IsUnreconciled("gone") || !got.IsUnreconciled("a") || got.IsUnreconciled("b") {
		t.Fatalf("decoded %+v", got)
	}
	c := got.Clone()
	c.Providers["a"] = Provider{}
	c.Unreconciled[0].ID = "x"
	c.Retired = append(c.Retired, Retired{})
	if got.Providers["a"].Row != p.Row || got.Unreconciled[0].ID != "a" || len(got.Retired) != 0 {
		t.Fatal("Clone shares its maps or slices")
	}
}

// Each malformed row digest or mark is refused by the codec, never
// normalized away.
func TestMetaRefusesBadRowsAndMarks(t *testing.T) {
	inst := strings.Repeat("b", 32)
	other := strings.Repeat("c", 32)
	for name, change := range map[string]func(*Meta){
		"short row":       func(m *Meta) { p := m.Providers["a"]; p.Row = "ab"; m.Providers["a"] = p },
		"upper row":       func(m *Meta) { p := m.Providers["a"]; p.Row = strings.Repeat("A", 64); m.Providers["a"] = p },
		"unsorted":        func(m *Meta) { m.Unreconciled = []Unreconciled{{"z", other}, {"b", other}} },
		"duplicate":       func(m *Meta) { m.Unreconciled = []Unreconciled{{"b", other}, {"b", other}} },
		"empty id":        func(m *Meta) { m.Unreconciled = []Unreconciled{{"", other}} },
		"bad instance":    func(m *Meta) { m.Unreconciled = []Unreconciled{{"b", "x"}} },
		"another's":       func(m *Meta) { m.Unreconciled = []Unreconciled{{"a", other}} },
		"live is its own": nil,
	} {
		t.Run(name, func(t *testing.T) {
			m := meta(1, []byte("p"))
			if change == nil {
				m.Unreconciled = []Unreconciled{{"a", inst}}
				if _, err := EncodeMeta(m); err != nil {
					t.Fatalf("a live id marked with its own instance: %v", err)
				}
				return
			}
			change(&m)
			if _, err := EncodeMeta(m); err == nil {
				t.Fatal("encoded")
			}
		})
	}
	// on disk: a mark without its instance, and one with an unknown field
	for name, mark := range map[string]string{"no instance": `{"id":"b"}`, "unknown field": `{"id":"b","instance":"` + other + `","x":1}`} {
		b := encoded(t, meta(1, []byte("p")))
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(b, &obj); err != nil {
			t.Fatal(err)
		}
		obj["unreconciled"] = json.RawMessage("[" + mark + "]")
		raw, _ := json.Marshal(obj)
		if _, err := DecodeMeta(raw); err == nil {
			t.Errorf("%s: decoded %s", name, raw)
		}
	}
}

// KeepAside copies a file privately and leaves it; DiscardJournal keeps
// the journal aside, then removes it.
func TestKeepAsideAndDiscardJournal(t *testing.T) {
	home(t)
	if aside, err := KeepAside(MetaFile, "reconciled"); err != nil || aside != "" {
		t.Fatalf("keeping a file that isn't there: %q %v", aside, err)
	}
	if _, err := KeepAside("other.json", "x"); err == nil {
		t.Fatal("kept a file that isn't a participant")
	}
	put(t, MetaFile, []byte("meta"))
	aside, err := KeepAside(MetaFile, "reconciled")
	if err != nil || !strings.HasPrefix(filepath.Base(aside), "lifecycle.json.reconciled-") {
		t.Fatalf("aside %q %v", aside, err)
	}
	if b, _ := os.ReadFile(aside); string(b) != "meta" || string(get(t, MetaFile)) != "meta" {
		t.Fatal("aside copy or the file itself changed")
	}
	if again, err := KeepAside(MetaFile, "reconciled"); err != nil || again != aside {
		t.Fatalf("the same bytes kept twice: %q %q %v", aside, again, err)
	}
	// kept in an earlier second: still the same copy, not a second one
	earlier := filepath.Join(filepath.Dir(aside), "lifecycle.json.reconciled-20000101-000000")
	if err := os.Rename(aside, earlier); err != nil {
		t.Fatal(err)
	}
	if again, err := KeepAside(MetaFile, "reconciled"); err != nil || again != earlier {
		t.Fatalf("the same bytes kept again a second later: %q %q %v", earlier, again, err)
	}
	if err := os.WriteFile(JournalPath(), []byte("not a journal"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := DiscardJournal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(JournalPath()); !os.IsNotExist(err) {
		t.Fatalf("journal still there (%v)", err)
	}
	b, _ := os.ReadFile(kept)
	if string(b) != "not a journal" || filepath.Dir(kept) != appdir.Config() {
		t.Fatalf("journal kept as %q: %q", kept, b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(kept); fi.Mode().Perm() != 0o600 {
			t.Fatalf("journal copy is %v, want 0600", fi.Mode().Perm())
		}
	}
}
