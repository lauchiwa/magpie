package provider

import (
	"bytes"
	"errors"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
)

// A Save of what providers.json holds already (#1962) writes nothing and
// tells no agent, and is a checked write still (fork): SaveChecked answers
// the value as it is, which the GUI's steps after the save (its keys, model
// prefs, outputs) write by, and a value read before its provider was
// deleted and added again as it was is refused, not taken as saved.
func TestAnUnchangedSaveIsStillChecked(t *testing.T) {
	h := readyHome(t, nil, "relay")
	read := h.find("relay")
	before := providersBytes(t)
	told := 0
	catalog.Changed = func() { told++ }
	t.Cleanup(func() { catalog.Changed = nil })

	saved, err := SaveChecked(read)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID != "relay" || saved.life.kind != lifeManaged || saved.life.instance != read.life.instance || saved.life.rev != read.life.rev {
		t.Fatalf("an unchanged SaveChecked answered %q %+v, want relay as read %+v", saved.ID, saved.life, read.life)
	}
	if told != 0 || !bytes.Equal(before, providersBytes(t)) {
		t.Fatalf("an unchanged save told the agents %d times or wrote providers.json", told)
	}
	if _, err := SaveChecked(saved); err != nil {
		t.Fatalf("a save built on its value: %v", err)
	}

	// deleted and added again as it was: the old value is another's
	if err := Delete("relay"); err != nil {
		t.Fatal(err)
	}
	if err := Save(h.row("relay")); err != nil {
		t.Fatal(err)
	}
	before, told = providersBytes(t), 0
	if err := Save(read); !errors.Is(err, ErrStale) {
		t.Fatalf("an unchanged save of the deleted provider: %v, want ErrStale", err)
	}
	if told != 0 || !bytes.Equal(before, providersBytes(t)) {
		t.Fatal("the refused save wrote or told")
	}
}
