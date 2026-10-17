package provider

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
)

// What a sync mirrors was decided from providers.json as collected. A
// rename, a delete, a delete and add again, or an edit made here after
// that is never undone by the Mirror: it is refused (*ConflictError) and
// nothing changes, the binding staying with the provider it is on. Under
// lifecycle.json and without; one added again with the same bytes is
// another instance only under lifecycle.json (without, an id is the
// provider, and the mirror is written by id, as before).
func TestAMirrorOnAnOlderBaseIsRefused(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, c := range []struct {
			name    string
			change  func(h *lifeHome)
			managed bool // refused only under lifecycle.json
		}{
			{"rename", func(h *lifeHome) {
				if err := Rename("relay", "relay2"); err != nil {
					h.t.Fatal(err)
				}
			}, false},
			{"delete", func(h *lifeHome) {
				if err := Delete("relay"); err != nil {
					h.t.Fatal(err)
				}
			}, false},
			{"recreate with the same bytes", func(h *lifeHome) {
				if err := Delete("relay"); err != nil {
					h.t.Fatal(err)
				}
				if err := Save(h.row("relay")); err != nil {
					h.t.Fatal(err)
				}
				if err := identity.Bind("relay", h.b.ID); err != nil {
					h.t.Fatal(err)
				}
			}, true},
			{"recreate", func(h *lifeHome) {
				if err := Delete("relay"); err != nil {
					h.t.Fatal(err)
				}
				p := h.row("relay")
				p.Name = "another relay"
				if err := Save(p); err != nil {
					h.t.Fatal(err)
				}
				if err := identity.Bind("relay", h.b.ID); err != nil {
					h.t.Fatal(err)
				}
			}, false},
			{"edit", func(h *lifeHome) {
				p := h.find("relay")
				p.Name = "edited here"
				if err := Save(p); err != nil {
					h.t.Fatal(err)
				}
			}, false},
		} {
			t.Run(map[bool]string{false: "legacy ", true: "managed "}[managed]+c.name, func(t *testing.T) {
				var h *lifeHome
				if managed {
					h = readyHome(t, nil, "relay")
				} else {
					h = newLifeHome(t)
					h.saveRows(h.row("relay"))
				}
				if err := identity.Bind("relay", h.a.ID); err != nil {
					t.Fatal(err)
				}
				collected, base, err := StoredBase()
				if err != nil {
					t.Fatal(err)
				}
				c.change(h)
				before, bound := configSnapshot(t), boundNow(t)
				// the server's: what was collected, and one added elsewhere
				remote := append(collected, h.row("other"))
				if c.managed && !managed {
					if err := Mirror(remote, nil, OnBase(base)); err != nil {
						t.Fatalf("without lifecycle.json, a mirror over the same bytes: %v", err)
					}
					return
				}
				if err := Mirror(remote, nil, OnBase(base)); !errors.As(err, new(*ConflictError)) || !errors.Is(err, ErrStale) {
					t.Fatalf("a mirror on the base before a %s: %v, want *ConflictError", c.name, err)
				}
				sameSnapshot(t, "a refused mirror", before, configSnapshot(t))
				if b := boundNow(t); len(b) != len(bound) || b["relay"] != bound["relay"] || b["relay2"] != bound["relay2"] {
					t.Fatalf("bindings after a refused mirror: %v, were %v", b, bound)
				}
				// collected again, the same mirror is written
				_, base, err = StoredBase()
				if err != nil {
					t.Fatal(err)
				}
				if err := Mirror(remote, nil, OnBase(base)); err != nil {
					t.Fatalf("a mirror on the base as it is: %v", err)
				}
				if _, ok := rawRow(t, "other"); !ok {
					t.Fatal("the mirror on the base as it is wrote no other")
				}
			})
		}
	}
}

// A Mirror given no base, or the zero FileBase (an opened bundle's), is
// checked against its own read only, as before: written over what is here.
func TestAMirrorWithNoBaseIsWrittenAsBefore(t *testing.T) {
	for _, opts := range [][]WriteOption{nil, {OnBase(FileBase{})}} {
		h := newLifeHome(t)
		h.saveRows(h.row("relay"))
		if err := Rename("relay", "relay2"); err != nil {
			t.Fatal(err)
		}
		if err := Mirror([]Provider{h.row("relay")}, nil, opts...); err != nil {
			t.Fatalf("a mirror with %d options: %v", len(opts), err)
		}
		if _, ok := rawRow(t, "relay"); !ok {
			t.Fatal("the mirror wrote no relay")
		}
		if _, ok := rawRow(t, "relay2"); ok {
			t.Fatal("the mirror kept relay2")
		}
	}
}

// Under lifecycle.json a Mirror is a commit as any write: an id the server
// holds too keeps its instance and binding (the same provider, edited on
// another computer), one renamed there comes as one deleted and one added
// (the binding stays behind, and goes with the deleted one), and the
// deleted instance is retired.
func TestAManagedMirrorKeepsInstancesByID(t *testing.T) {
	h := readyHome(t, nil, "relay", "old")
	for id, p := range map[string]identity.Profile{"relay": h.a, "old": h.b} {
		if err := identity.Bind(id, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	m0 := readMetaT(t)
	_, base, err := StoredBase()
	if err != nil {
		t.Fatal(err)
	}
	edited := h.row("relay")
	edited.Name = "edited elsewhere"
	if err := Mirror([]Provider{edited, h.row("new")}, nil, OnBase(base)); err != nil {
		t.Fatal(err)
	}
	m := readMetaT(t)
	if got, was := m.Providers["relay"], m0.Providers["relay"]; got.Instance != was.Instance || got.Rev != was.Rev+1 {
		t.Fatalf("relay after the mirror: %+v, was %+v", got, was)
	}
	if got := m.Providers["new"]; got.Instance == "" || got.Instance == m0.Providers["old"].Instance {
		t.Fatalf("new took old's instance: %+v", got)
	}
	if _, ok := m.Providers["old"]; ok {
		t.Fatal("old is still in lifecycle.json")
	}
	if !retiredIn(m, m0.Providers["old"].Instance) {
		t.Fatalf("old's instance is not retired: %+v", m.Retired)
	}
	if b := boundNow(t); b["relay"] != h.a.ID || b["new"] != "" || b["old"] != "" {
		t.Fatalf("bindings after the mirror: %v", b)
	}
	if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
		t.Fatalf("relay after the mirror sent %q (%v)", got, err)
	}
	if got, err := h.sign(h.find("new")); err != nil || got == uaOf(h.b) {
		t.Fatalf("new presented old's profile: %q (%v)", got, err)
	}
}

func retiredIn(m lifecycle.Meta, inst string) bool {
	for _, r := range m.Retired {
		if r.Instance == inst {
			return true
		}
	}
	return false
}

// A restore is made over providers.json as it is: a write that got in
// between its read and its commit has it read and merge again, once, and
// that write is kept. Two in a row are a *ConflictError, and nothing of
// the restore is written.
func TestARestoreMergesAgainOnce(t *testing.T) {
	for _, times := range []int{1, 2} {
		h := readyHome(t, nil, "relay")
		if err := identity.Bind("relay", h.a.ID); err != nil {
			t.Fatal(err)
		}
		// a write between restoreOnce's read and its commit, made by the
		// first times commits the restore takes the lock for
		old := lockCommit
		t.Cleanup(func() { lockCommit = old })
		injected := 0
		var hook func() (func(), error)
		hook = func() (func(), error) {
			if injected < times {
				injected++
				lockCommit = old
				err := Save(h.row(fmt.Sprintf("meanwhile%d", injected)))
				lockCommit = hook
				if err != nil {
					t.Fatal(err)
				}
			}
			return old()
		}
		lockCommit = hook
		backup := h.row("relay")
		backup.Name = "from the backup"
		added, replaced, err := Restore([]Provider{backup, h.row("restored")})
		lockCommit = old
		if times == 2 {
			if !errors.As(err, new(*ConflictError)) {
				t.Fatalf("a restore met by two writes: %v, want *ConflictError", err)
			}
			if _, ok := rawRow(t, "restored"); ok {
				t.Fatal("a refused restore wrote restored")
			}
			continue
		}
		if err != nil || added != 1 || replaced != 1 {
			t.Fatalf("a restore met by one write: %d %d %v", added, replaced, err)
		}
		if _, ok := rawRow(t, "meanwhile1"); !ok {
			t.Fatal("the restore wrote over the write that got in between")
		}
		if p, ok := rawRow(t, "relay"); !ok || p.Name != "from the backup" {
			t.Fatalf("relay after the restore: %+v", p)
		}
	}
}
