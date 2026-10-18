package tui

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/provider"
)

// managedHome is home with lifecycle.json readied, as a configured
// magpie's is, on the Providers page with a selected.
func managedHome(t *testing.T, ready bool) model {
	t.Helper()
	home(t)
	if ready {
		if err := provider.ReadyLifecycle(); err != nil {
			t.Fatal(err)
		}
	}
	m := press(t, model{w: 120, h: 40}, "2")
	for i, q := range m.provs {
		if q.ID == "a" {
			m.prow = i
		}
	}
	return m
}

// recreate is another magpie deleting a and making a new one under its id.
func recreate(t *testing.T) {
	t.Helper()
	if err := provider.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "a", Name: "A", Key: "fresh", Models: []string{"m", "claude-opus-5-5"}, Chat: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
}

func found(t *testing.T) *provider.Provider {
	t.Helper()
	p, err := provider.Find("a")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// d, then a made anew under the same id by another magpie, then d again:
// the second d removes the provider the first one named, which is gone, so
// the new a stays, and the page says it changed. Whether or not the list
// was read again in between (another key's flash reads it again).
func TestTUIDeleteRemovesOnlyTheProviderItConfirmed(t *testing.T) {
	for _, reload := range []bool{false, true} {
		m := managedHome(t, true)
		m = press(t, m, "d")
		wantFlash(t, m, false, "press d again to remove A")
		recreate(t)
		if reload {
			// read again with the cursor on a: the new one, at the list's end
			m.reloadProviders()
			m.prow = slices.IndexFunc(m.provs, func(q provider.Provider) bool { return q.ID == "a" })
		}
		m = press(t, m, "d")
		wantFlash(t, m, false, "changed since it was read")
		if p := found(t); p.Key != "fresh" {
			t.Fatalf("reload %v: the new a is %+v", reload, p)
		}
	}
}

// Without lifecycle.json d d removes by id, as it always did.
func TestTUIDeleteByIDWithoutLifecycle(t *testing.T) {
	m := managedHome(t, false)
	m = press(t, m, "d")
	recreate(t)
	m = press(t, m, "d")
	wantFlash(t, m, true, "removed A")
	if _, err := provider.Find("a"); err == nil {
		t.Fatal("a legacy d d left a")
	}
	if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a legacy delete made lifecycle.json (%v)", err)
	}
}

// What a line or key on the Providers page saves (family, unlisted, off) is
// refused when a was made anew since the list was read, and the new a keeps
// what it has. Without lifecycle.json it is saved by id, as before.
func TestTUIProviderEditsRefuseAProviderMadeAnew(t *testing.T) {
	for _, ready := range []bool{true, false} {
		for _, edit := range []string{"family", "unlisted", "off"} {
			m := managedHome(t, ready)
			recreate(t)
			switch edit {
			case "family":
				m = press(t, m, "f")
				m = press(t, m, "ctrl+u")
				m = typeIn(m, "relay")
				m = press(t, m, "enter")
			case "unlisted":
				m = press(t, m, "u")
			case "off":
				m = press(t, m, "o")
			}
			p := found(t)
			changed := p.Family == "relay" || p.Unlisted || p.Off
			if ready {
				wantFlash(t, m, false, "changed since it was read")
				if changed || p.Key != "fresh" {
					t.Fatalf("%s: the new a was changed: %+v", edit, p)
				}
				continue
			}
			if !m.flashOK || !changed || p.Key != "fresh" {
				t.Fatalf("legacy %s: flash %q, a %+v", edit, m.flash, p)
			}
		}
	}
}

// A model turned on or off in a's list, opened before a was made anew, is
// refused; a list opened after it saves.
func TestTUIModelToggleRefusesAProviderMadeAnew(t *testing.T) {
	m := managedHome(t, true)
	m = press(t, m, "enter")
	recreate(t)
	m = press(t, m, "enter")
	wantFlash(t, m, false, "changed since it was read")
	if p := found(t); !slices.Equal(p.Models, []string{"m", "claude-opus-5-5"}) {
		t.Fatalf("the new a's models: %v", p.Models)
	}

	m = press(t, m, "esc")
	m.reloadProviders()
	m.prow = slices.IndexFunc(m.provs, func(q provider.Provider) bool { return q.ID == "a" })
	m = press(t, m, "enter")
	before := found(t).Picks()
	m = press(t, m, "enter")
	if !m.flashOK || slices.Equal(found(t).Picks(), before) {
		t.Fatalf("a toggle in a list opened after: flash %q, models %v", m.flash, found(t).Picks())
	}
}
