package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/lifecycle"
)

// A binding change that adopts a change made outside magpie reads who was
// bound as every adoption does: identities.json itself. One that is blank
// is refused, with nothing written, never adopted from its .bak, which may
// hold a binding the file no longer has (relay's, here, from before it
// was renamed).
func TestABindingAdoptsOnlyFromIdentitiesItself(t *testing.T) {
	h := readyHome(t, nil, "relay")
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	if err := Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	// identities.json blanked: its .bak, the generation before the rename,
	// binds relay
	raw, _ := os.ReadFile(identity.Path())
	if err := os.WriteFile(identity.Path(), make([]byte, len(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	if bak, _ := os.ReadFile(lastgood.Bak(identity.Path())); !strings.Contains(string(bak), `"relay"`) {
		t.Fatalf("identities.json.bak doesn't bind relay: %s", bak)
	}
	// relay added again outside magpie: the next commit adopts it
	writeOutside(t, h.row("relay2"), h.row("relay"))
	held := h.find("relay2")
	before := configSnapshot(t)
	err := BindIdentity(held, h.b.ID)
	if err == nil {
		t.Fatalf("a bind adopting over a blank identities.json was written; bindings now %v", boundNow(t))
	}
	if !strings.HasPrefix(err.Error(), "identity store not changed: ") {
		t.Fatalf("the refusal: %v", err)
	}
	sameSnapshot(t, "a refused bind", before, configSnapshot(t))
}

// A binding change's commit errors name the identity store once: never
// "providers", which it doesn't change, nor its own prefix twice.
func TestABindingErrorNamesTheIdentityStore(t *testing.T) {
	check := func(t *testing.T, err error, prefix string) {
		t.Helper()
		if err == nil {
			t.Fatal("the bind was written")
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, prefix) || strings.Contains(msg, "providers") || strings.Count(msg, "identity store") != 1 {
			t.Fatalf("the error: %q, want one %q and no providers", msg, prefix)
		}
	}
	t.Run("an unusable .bak", func(t *testing.T) {
		for _, managed := range []bool{false, true} {
			h, _ := renameHome(t, managed)
			bak := lastgood.Bak(identity.Path())
			if err := os.Remove(bak); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if managed {
				if err := os.Mkdir(bak, 0o700); err != nil {
					t.Fatal(err)
				}
				check(t, BindIdentity(h.find("relay"), h.b.ID), "identity store not changed: ")
				continue
			}
			// no lifecycle.json: an orphan's unbind is identities.json's
			// commit alone
			if err := identity.Bind("orphan", h.b.ID); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(bak); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.Mkdir(bak, 0o700); err != nil {
				t.Fatal(err)
			}
			check(t, UnbindOrphan("orphan"), "identity store not changed: ")
		}
	})
	t.Run("a commit failing part way", func(t *testing.T) {
		h, _ := renameHome(t, true)
		lifecycle.TestStage = func(s string) error {
			if s == "identities.json:write" {
				return errors.New("injected")
			}
			return nil
		}
		t.Cleanup(func() { lifecycle.TestStage = nil })
		err := BindIdentity(h.find("relay"), h.b.ID)
		lifecycle.TestStage = nil
		check(t, err, "identity store: ")
		if !strings.Contains(err.Error(), "injected") {
			t.Fatalf("the error lost its cause: %v", err)
		}
	})
}

// A rename whose journal is written is finished forward by the next
// write or start-up, even when this commit fails after: its fetched list
// moves with it now, or the list would stay under an id that is gone
// then. One refused before its journal (an unusable .bak) is not made,
// and its list stays.
func TestARenamePendingInItsJournalMovesTheFetchedList(t *testing.T) {
	putList := func(t *testing.T) string {
		t.Helper()
		live := catalog.LivePath("relay")
		if err := os.MkdirAll(filepath.Dir(live), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(live, []byte(`{"models":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return live
	}
	for _, managed := range []bool{false, true} {
		for _, stage := range commitStages {
			if !managed && (stage == "meta:before" || stage == "lifecycle.json:write") {
				continue
			}
			t.Run(map[bool]string{false: "legacy ", true: "managed "}[managed]+stage, func(t *testing.T) {
				h, before := renameHome(t, managed)
				live := putList(t)
				lifecycle.TestStage = func(s string) error {
					if s == stage {
						return errors.New("injected")
					}
					return nil
				}
				t.Cleanup(func() { lifecycle.TestStage = nil })
				err := Rename("relay", "relay2")
				lifecycle.TestStage = nil
				if err == nil || !strings.Contains(err.Error(), "injected") {
					t.Fatalf("the rename failing at %s: %v", stage, err)
				}
				if _, err := os.Stat(catalog.LivePath("relay2")); err != nil {
					t.Fatalf("the fetched list did not go with the pending rename: %v", err)
				}
				if _, err := os.Stat(live); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the fetched list is still under relay (%v)", err)
				}
				checkRecovered(t, h, before, managed)
			})
		}
	}
	t.Run("refused before its journal", func(t *testing.T) {
		renameHome(t, true)
		live := putList(t)
		bak := lastgood.Bak(Path())
		if err := os.Remove(bak); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Mkdir(bak, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := Rename("relay", "relay2"); err == nil {
			t.Fatal("a rename past an unusable providers.json.bak was made")
		}
		if _, err := os.Stat(lifecycle.JournalPath()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a journal after a refused rename (%v)", err)
		}
		if _, err := os.Stat(live); err != nil {
			t.Fatalf("a refused rename moved the fetched list: %v", err)
		}
		if _, err := os.Stat(catalog.LivePath("relay2")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a refused rename put a fetched list under relay2 (%v)", err)
		}
	})
}
