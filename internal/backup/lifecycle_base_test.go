package backup

import (
	"errors"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A collected bundle keeps providers.json as Collect read it (Base): a
// Mirror of a decision made from it, after a write here since, is refused
// and writes nothing. A sealed and opened bundle carries no base, and its
// Mirror is checked against its own read, as before.
func TestACollectedBundleCarriesItsBase(t *testing.T) {
	home(t)
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	b, err := Collect(true, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Mirror(b.Providers, b.Groups, provider.OnBase(b.Base())); !errors.As(err, new(*provider.ConflictError)) {
		t.Fatalf("a mirror on the collect before a rename: %v, want *provider.ConflictError", err)
	}
	if ps, _ := provider.Stored(); len(ps) != 1 || ps[0].ID != "relay2" {
		t.Fatalf("a refused mirror changed the providers: %+v", ps)
	}
	sealed, err := Seal(b, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(sealed, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if opened.Base() != (provider.FileBase{}) {
		t.Fatal("an opened bundle carries a base")
	}
	if err := provider.Mirror(opened.Providers, opened.Groups, provider.OnBase(opened.Base())); err != nil {
		t.Fatalf("a mirror of an opened bundle: %v", err)
	}
	if ps, _ := provider.Stored(); len(ps) != 1 || ps[0].ID != "relay" {
		t.Fatalf("an opened bundle's mirror: %+v", ps)
	}
}
