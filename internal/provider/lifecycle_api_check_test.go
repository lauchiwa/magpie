package provider

import (
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
)

// Review tests for G3b-1 (research/g3b1-check.md): cases the write API
// guards that its own tests left open.

// A bind is checked against the row as stored under the lock, not the value
// read: a row that became a magpie service since (changed outside magpie)
// presents no profile, so binding it would be accepted and never sent.
func TestABindChecksTheRowAsStoredNow(t *testing.T) {
	h := readyHome(t, nil, "relay")
	read1 := h.find("relay")
	if why := read1.IdentityApplies(); why != "" {
		t.Fatalf("the value as read: %s", why)
	}
	now := h.row("relay")
	now.Preset = RemoteMagpiePreset
	writeOutside(t, now)
	before := configSnapshot(t)
	if err := BindIdentity(read1, h.a.ID); err == nil || !strings.Contains(err.Error(), "magpie service") {
		t.Fatalf("a bind of a row that became a magpie service: %v", err)
	}
	sameSnapshot(t, "a refused bind", before, configSnapshot(t))
}

// With no lifecycle.json, providers.json read from its .bak can't say which
// ids the primary held: an orphan unbind is refused, not taken as "no row
// holds it", and writes nothing.
func TestALegacyOrphanUnbindOverAFallbackIsRefused(t *testing.T) {
	h := newLifeHome(t)
	h.saveRows(h.row("relay"))
	if err := identity.Bind("gone", h.a.ID); err != nil {
		t.Fatal(err)
	}
	h.saveRows(h.row("relay"), h.row("other")) // a .bak with relay alone
	if err := os.WriteFile(Path(), make([]byte, len(providersBytes(t))), 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := read(); err != nil || !f.base.fallback {
		t.Fatalf("not read from its .bak: %v", err)
	}
	before := configSnapshot(t)
	if err := UnbindOrphan("gone"); err == nil || !strings.Contains(err.Error(), ".bak") {
		t.Fatalf("an orphan unbind over providers.json read from its .bak: %v", err)
	}
	sameSnapshot(t, "a refused orphan unbind", before, configSnapshot(t))
	if boundNow(t)["gone"] != h.a.ID {
		t.Fatal("the binding went")
	}
}

// With no lifecycle.json too, a learner never writes what it learned of a
// value never saved onto the stored row of the same id (the GUI's detect and
// list forms carry ids like that).
func TestALegacyLearnerOfATransientValueWritesNothing(t *testing.T) {
	h := newLifeHome(t)
	row := h.row("relay")
	row.AuthHeader = AuthBoth
	h.saveRows(row)
	if m, err := lifecycle.ReadMeta(); err != nil || m != nil {
		t.Fatalf("lifecycle.json: %v %v, want none", m, err)
	}
	LearnAuthHeader("relay", AuthBearer, Expect(Transient(h.row("relay"))))
	if p, _ := rawRow(t, "relay"); p.AuthHeader != AuthBoth {
		t.Fatalf("a transient value's learned header was written to the stored relay: %q", p.AuthHeader)
	}
}

// Add of a value read from the store (a stored provider with another key)
// is a new provider under the next free id: what it was read as is not
// checked, so its own id being taken is no reason to refuse it.
func TestAnAddOfAReadValueIsANewProvider(t *testing.T) {
	h := readyHome(t, nil, "relay")
	p := h.find("relay")
	if p.life.kind != lifeManaged {
		t.Fatalf("read: %+v", p.life)
	}
	p.Key = "sk-test-another"
	id, err := Add(p)
	if err != nil || id == "relay" {
		t.Fatalf("an add of a value read as another provider: %q %v", id, err)
	}
	m := readMetaT(t)
	if m.Providers[id].Instance == "" || m.Providers[id].Instance == m.Providers["relay"].Instance {
		t.Fatalf("an add of a value read as another provider: %s %+v, relay %+v", id, m.Providers[id], m.Providers["relay"])
	}
}
