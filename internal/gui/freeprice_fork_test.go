package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// fork: the provider editor draws a model's free badge from the page's
// free and says which of the user's prices made it free from priced
// (provider.FreeBy): a relay's model the user priced at nothing is free,
// every model of a provider priced at nothing is free, and a model the
// user gave a price is not, its vendor's free mark or not. A
// settings.json that can't be read, with no last good generation, holds
// no price, so each model is as its vendor lists it.
func TestEditorFreeReadsTheUsersOwnPrice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, id := range []string{"rv", "all"} {
		if err := provider.Save(provider.Provider{ID: id, Name: id, Key: "sk-" + id, Chat: "http://127.0.0.1:9/v1"}); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(catalog.LivePath(id)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(catalog.LivePath(id), []byte(`{"models":[{"id":"plain","cost":{"input":3,"output":15}},{"id":"other","cost":{"input":1,"output":2}},{"id":"vfree","free":true}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	catalog.Reset()
	type word struct {
		free   bool
		priced string
	}
	shown := func() map[string]word {
		t.Helper()
		got := map[string]word{}
		for _, id := range []string{"rv", "all"} {
			p, err := provider.Find(id)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range providerInfo(*p, nil).Models {
				got[id+"/"+m.ID] = word{m.Free, m.Priced}
			}
		}
		return got
	}
	check := func(when string, want map[string]word) {
		t.Helper()
		got := shown()
		for ref, w := range want {
			if g, ok := got[ref]; !ok || g != w {
				t.Errorf("%s: the editor's %s = %+v (listed %v), want %+v", when, ref, g, ok, w)
			}
		}
	}
	vendor := map[string]word{"rv/plain": {}, "rv/other": {}, "rv/vfree": {true, ""}, "all/plain": {}, "all/vfree": {true, ""}}
	check("no price", vendor)

	s := settings.Load()
	s.ModelPrices = map[string]settings.ModelPrice{
		"rv/other": settings.StatedPrice(catalog.Price{}),
		"rv/vfree": settings.StatedPrice(catalog.Price{Input: 1, Output: 2}),
		"all/*":    settings.StatedPrice(catalog.Price{}),
	}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	check("priced", map[string]word{
		"rv/plain": {}, "rv/other": {true, provider.PricedModel}, "rv/vfree": {false, provider.PricedPaid},
		"all/plain": {true, provider.PricedProvider}, "all/vfree": {true, provider.PricedProvider},
	})

	for _, f := range []string{settings.Path(), settings.Path() + ".bak"} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(settings.Path(), []byte(`{"modelPrices":{"rv/plain":{"input":0,`), 0o644); err != nil {
		t.Fatal(err)
	}
	check("settings.json unreadable", vendor)
}

// fork: the editor's whole-provider toggle is drawn from whole, the saved
// `<id>/*` (provider.WholeOf): none, free at $0 in every part, a price
// set otherwise, or one that doesn't read. A settings.json that can't be
// read, with no last good generation, holds no price, so none is sent.
func TestEditorSaysTheWholeProviderPrice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ids := []string{"none", "free", "paid", "broken"}
	for _, id := range ids {
		if err := provider.Save(provider.Provider{ID: id, Name: id, Key: "sk-" + id, Chat: "http://127.0.0.1:9/v1"}); err != nil {
			t.Fatal(err)
		}
	}
	paid := settings.StatedPrice(catalog.Price{Input: 0.5, Output: 2})
	broken := settings.StatedPrice(catalog.Price{})
	broken.CacheRead = nil
	s := settings.Load()
	s.ModelPrices = map[string]settings.ModelPrice{
		"free/*": settings.StatedPrice(catalog.Price{}), "paid/*": paid, "broken/*": broken,
		"none/m": settings.StatedPrice(catalog.Price{}),
	}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	whole := func(id string) *provider.Whole {
		t.Helper()
		p, err := provider.Find(id)
		if err != nil {
			t.Fatal(err)
		}
		return providerInfo(*p, nil).Whole
	}
	if w := whole("none"); w != nil {
		t.Errorf("none's whole = %+v, want none (a model's own price is no whole one)", w)
	}
	if w := whole("free"); w == nil || !w.Free || w.Price != nil || w.Bad != "" {
		t.Errorf("free's whole = %+v, want free", w)
	}
	if w := whole("paid"); w == nil || w.Free || w.Price == nil || w.Price.Input != 0.5 || w.Price.Output != 2 {
		t.Errorf("paid's whole = %+v, want its price 0.5,2", w)
	}
	if w := whole("broken"); w == nil || w.Free || w.Price != nil || w.Bad != "cache read" {
		t.Errorf("broken's whole = %+v, want bad cache read", w)
	}

	for _, f := range []string{settings.Path(), settings.Path() + ".bak"} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(settings.Path(), []byte(`{"modelPrices":{"free/*":{"input":0,`), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := whole("free"); w != nil {
		t.Errorf("settings.json unreadable: free's whole = %+v, want none", w)
	}
}
