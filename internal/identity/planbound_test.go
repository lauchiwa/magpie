package identity

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"strings"
	"testing"
	"time"
)

// storeBytes is identities.json as the store itself writes it.
func storeBytes(t *testing.T, f file) []byte {
	t.Helper()
	ownStore(t)
	if err := write(f); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// PlanBound moves and drops bindings as a provider commit needs, in the
// bytes write would put on disk, and reads or writes no file.
func TestPlanBound(t *testing.T) {
	now := time.Date(2026, 10, 14, 1, 2, 3, 0, time.UTC)
	older := Profile{ID: "identity_a", Client: ClientCodex, Source: SourceCapture, CreatedAt: now.Add(-time.Hour),
		Fingerprint: Fingerprint{UserAgent: "codex_cli_rs/0.159.2", Version: "0.159.2", Headers: map[string]string{"Originator": "codex_cli_rs"}}}
	newer := older.Clone()
	newer.ID, newer.CreatedAt = "identity_b", now
	// profiles out of order on disk: an untouched one stays as it is, a
	// changed one comes out in write's order
	base := file{Profiles: []Profile{newer, older}, Bound: map[string]string{
		"a": "identity_a", "b": "identity_b", "orphan": "identity_a", "broken": "identity_gone", "keep": "identity_b",
	}}
	cases := []struct {
		name  string
		bound map[string]string // nil: base.Bound
		moves map[string]string
		drops []string
		want  map[string]string // nil with changed false
		err   string
	}{
		{name: "rename keeps the binding", moves: map[string]string{"a": "renamed"},
			want: map[string]string{"renamed": "identity_a", "b": "identity_b", "orphan": "identity_a", "broken": "identity_gone", "keep": "identity_b"}},
		{name: "rename over an orphan replaces it", moves: map[string]string{"b": "orphan"},
			want: map[string]string{"a": "identity_a", "orphan": "identity_b", "broken": "identity_gone", "keep": "identity_b"}},
		{name: "unbound rename still clears the orphan at its target", moves: map[string]string{"nobody": "orphan"},
			want: map[string]string{"a": "identity_a", "b": "identity_b", "broken": "identity_gone", "keep": "identity_b"}},
		{name: "broken binding moves as it is", moves: map[string]string{"broken": "fixed"},
			want: map[string]string{"a": "identity_a", "b": "identity_b", "orphan": "identity_a", "fixed": "identity_gone", "keep": "identity_b"}},
		{name: "moves happen at once", moves: map[string]string{"a": "b", "b": "c"},
			want: map[string]string{"b": "identity_a", "c": "identity_b", "orphan": "identity_a", "broken": "identity_gone", "keep": "identity_b"}},
		{name: "swap", moves: map[string]string{"a": "b", "b": "a"},
			want: map[string]string{"a": "identity_b", "b": "identity_a", "orphan": "identity_a", "broken": "identity_gone", "keep": "identity_b"}},
		{name: "drop", drops: []string{"a", "nobody"},
			want: map[string]string{"b": "identity_b", "orphan": "identity_a", "broken": "identity_gone", "keep": "identity_b"}},
		{name: "drop a moved-away source", moves: map[string]string{"a": "renamed"}, drops: []string{"a"},
			want: map[string]string{"renamed": "identity_a", "b": "identity_b", "orphan": "identity_a", "broken": "identity_gone", "keep": "identity_b"}},
		{name: "last binding dropped", bound: map[string]string{"a": "identity_a"}, drops: []string{"a"}, want: map[string]string{}},
		{name: "unbound rename is no change", moves: map[string]string{"nobody": "somebody"}},
		{name: "drop of an unbound id is no change", drops: []string{"nobody", ""}},
		{name: "self move is no change", moves: map[string]string{"a": "a"}},
		{name: "nothing to do", bound: map[string]string{}},
		{name: "empty source", moves: map[string]string{"": "a"}, err: "needs both"},
		{name: "empty target", moves: map[string]string{"a": ""}, err: "needs both"},
		{name: "two moves to one id", moves: map[string]string{"a": "x", "b": "x"}, err: "both move"},
		{name: "move onto a drop", moves: map[string]string{"a": "x"}, drops: []string{"x"}, err: "dropped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := file{Profiles: base.Profiles, Bound: maps.Clone(base.Bound)}
			if c.bound != nil {
				f.Bound = c.bound
			}
			cur := storeBytes(t, f)
			// write sorted the profiles; put them back out of order, as a
			// file written by hand could have them
			var raw file
			if err := json.Unmarshal(cur, &raw); err != nil {
				t.Fatal(err)
			}
			raw.Profiles = []Profile{newer, older}
			cur, _ = json.MarshalIndent(raw, "", "\t")
			if err := os.WriteFile(Path(), cur, 0o600); err != nil {
				t.Fatal(err)
			}
			listing := dirListing(t)

			after, changed, err := PlanBound(cur, c.moves, c.drops)
			// pure: the folder and the store are as they were
			if got := dirListing(t); got != listing {
				t.Errorf("PlanBound changed the folder:\n%s\nwas\n%s", got, listing)
			}
			if b, _ := os.ReadFile(Path()); !bytes.Equal(b, cur) {
				t.Error("PlanBound wrote the store")
			}
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) || after != nil || changed {
					t.Fatalf("PlanBound = %q, %v, %v; want an error naming %q", after, changed, err, c.err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if c.want == nil {
				if changed || after != nil {
					t.Fatalf("no change gave %q, %v", after, changed)
				}
			} else {
				if !changed {
					t.Fatal("a change reported none")
				}
				// the bytes write gives the same store
				want := file{Profiles: []Profile{newer, older}, Bound: c.want}
				ownStore(t)
				if err := write(want); err != nil {
					t.Fatal(err)
				}
				wantBytes, _ := os.ReadFile(Path())
				if !bytes.Equal(after, wantBytes) {
					t.Errorf("PlanBound gave\n%s\nwrite gives\n%s", after, wantBytes)
				}
				if !bytes.HasSuffix(after, []byte("}\n")) {
					t.Errorf("no final newline: %q", after)
				}
			}
		})
	}
}

// What PlanBound is given that isn't a valid store is unknown: an error,
// never a plan over no bindings. Only an absent file (nil) is empty.
func TestPlanBoundRefusesAStoreItCantRead(t *testing.T) {
	ownStore(t)
	bad := map[string][]byte{
		"empty":     {},
		"blank":     []byte(" \n"),
		"zeroed":    make([]byte, 32),
		"cut short": []byte(`{"bound": {"a": "identity_a"`),
		"not a map": []byte(`[]`),
		"bad bound": []byte(`{"bound": ["a"]}`),
	}
	for name, cur := range bad {
		after, changed, err := PlanBound(cur, map[string]string{"a": "b"}, []string{"c"})
		if err == nil || after != nil || changed {
			t.Errorf("%s: PlanBound = %q, %v, %v; want an error", name, after, changed, err)
		} else if !strings.Contains(err.Error(), "identities.json") {
			t.Errorf("%s: error %q does not name the file", name, err)
		}
	}
	// absent: no bindings to move, and nothing to write
	if after, changed, err := PlanBound(nil, map[string]string{"a": "b"}, []string{"c"}); err != nil || changed || after != nil {
		t.Errorf("absent store: %q, %v, %v", after, changed, err)
	}
	// `null` is a valid store with nothing in it, as read takes it
	if after, changed, err := PlanBound([]byte("null"), nil, []string{"c"}); err != nil || changed || after != nil {
		t.Errorf("null store: %q, %v, %v", after, changed, err)
	}
}

func dirListing(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(strings.TrimSuffix(Path(), "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s strings.Builder
	for _, e := range entries {
		info, _ := e.Info()
		s.WriteString(e.Name())
		if info != nil {
			s.WriteString(" " + info.ModTime().String())
		}
		s.WriteString("\n")
	}
	return s.String()
}
