package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/settings"
)

// pricesSaved replaces the user's model prices with these, as `magpie
// model price` leaves them in settings.json.
func pricesSaved(t *testing.T, prices map[string]settings.ModelPrice) {
	t.Helper()
	s := settings.Load()
	s.ModelPrices = prices
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
}

// stated is a price as the user gives it: input, output, cache read and
// cache write, in USD per million tokens.
func stated(in, out, read, write float64) settings.ModelPrice {
	return settings.StatedPrice(catalog.Price{Input: in, Output: out, CacheRead: read, CacheWrite: write})
}

// The user's own price is a word on whether a model costs nothing, read
// before the vendor's: a relay's model a user priced at nothing is free,
// though the relay lists no "free" and models.dev prices the model. A
// price for that one model is the user's last word either way, while a
// price for every model of the provider only adds what is free: a
// relay's multiplier set as id/* takes no vendor's free model away. A
// price from any provider (*/model) is no word on this one.
func TestFreeModelReadsTheUsersOwnPrice(t *testing.T) {
	zero := stated(0, 0, 0, 0)
	paid := stated(1, 2, 0.1, 1.25)
	withTier := zero
	withTier.Tiers = []catalog.Tier{{Above: 272000, Input: 1}}
	withHour := zero
	withHour.CacheWrite1h = new(2.0)
	broken := zero
	broken.Output = nil // a hand edit left a part out: no price at all
	cases := []struct {
		name   string
		prices map[string]settings.ModelPrice
		model  string
		want   bool
	}{
		{"no price: a model the vendor prices is paid", nil, "plain", false},
		{"no price: the vendor's free one is free", nil, "vfree", true},
		{"the model priced at nothing", map[string]settings.ModelPrice{"rv/plain": zero}, "plain", true},
		{"every model priced at nothing", map[string]settings.ModelPrice{"rv/*": zero}, "plain", true},
		{"the model's own price over every model's", map[string]settings.ModelPrice{"rv/*": zero, "rv/plain": paid}, "plain", false},
		{"the vendor's free one priced by the user", map[string]settings.ModelPrice{"rv/vfree": paid}, "vfree", false},
		{"every model priced: the vendor's free one stays free", map[string]settings.ModelPrice{"rv/*": paid}, "vfree", true},
		{"every model priced: a paid one stays paid", map[string]settings.ModelPrice{"rv/*": paid}, "plain", false},
		{"from any provider at nothing is no word here", map[string]settings.ModelPrice{"*/plain": zero}, "plain", false},
		{"a tier that costs is not nothing", map[string]settings.ModelPrice{"rv/plain": withTier}, "plain", false},
		{"an hour's cache write that costs is not nothing", map[string]settings.ModelPrice{"rv/plain": withHour}, "plain", false},
		{"every model with a tier that costs", map[string]settings.ModelPrice{"rv/*": withTier}, "plain", false},
		{"a broken price is no price", map[string]settings.ModelPrice{"rv/plain": broken}, "plain", false},
		{"a broken price leaves the vendor's free one", map[string]settings.ModelPrice{"rv/vfree": broken}, "vfree", true},
		{"another provider's price is no word here", map[string]settings.ModelPrice{"other/plain": zero, "other/*": zero}, "plain", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefsHome(t)
			if err := Save(Provider{ID: "rv", Name: "RV", Key: "sk-rv", Chat: "http://127.0.0.1:9/v1"}); err != nil {
				t.Fatal(err)
			}
			liveFree(t, "rv", `{"models":[{"id":"plain","cost":{"input":3,"output":15}},{"id":"vfree","free":true}]}`)
			pricesSaved(t, c.prices)
			p, err := Find("rv")
			if err != nil {
				t.Fatal(err)
			}
			if got := p.FreeModel(c.model); got != c.want {
				t.Fatalf("FreeModel(%q) = %v, want %v (prices %v)", c.model, got, c.want, c.prices)
			}
		})
	}
}

// A settings.json that can't be read says nothing about what the user
// set, so no model is free on its word: FreeModel answers as with no
// price, never as with the zero price it may have held.
func TestFreeModelWithUnreadableSettingsIsTheVendorsWord(t *testing.T) {
	prefsHome(t)
	if err := Save(Provider{ID: "rv", Name: "RV", Key: "sk-rv", Chat: "http://127.0.0.1:9/v1"}); err != nil {
		t.Fatal(err)
	}
	liveFree(t, "rv", `{"models":[{"id":"plain","cost":{"input":3,"output":15}},{"id":"vfree","free":true}]}`)
	p, err := Find("rv")
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]bool{"plain": p.FreeModel("plain"), "vfree": p.FreeModel("vfree")}
	if before["plain"] || !before["vfree"] {
		t.Fatalf("with no price: %v", before)
	}
	if err := os.MkdirAll(filepath.Dir(settings.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings.Path(), []byte(`{"modelPrices":{"rv/plain":{"input":0,`), 0o644); err != nil {
		t.Fatal(err)
	}
	for model, want := range before {
		if got := p.FreeModel(model); got != want {
			t.Errorf("FreeModel(%q) with settings.json unreadable = %v, want %v", model, got, want)
		}
	}
}

// A settings.json that doesn't parse is read as its last good generation,
// as billing reads it: a model priced at nothing there is still free, and
// routing and the ledger agree on it.
func TestFreeModelWithBrokenSettingsReadsItsLastGoodGeneration(t *testing.T) {
	prefsHome(t)
	if err := Save(Provider{ID: "rv", Name: "RV", Key: "sk-rv", Chat: "http://127.0.0.1:9/v1"}); err != nil {
		t.Fatal(err)
	}
	liveFree(t, "rv", `{"models":[{"id":"plain","cost":{"input":3,"output":15}}]}`)
	zero := map[string]settings.ModelPrice{"rv/plain": stated(0, 0, 0, 0)}
	pricesSaved(t, zero)
	pricesSaved(t, zero) // the save before keeps the one written first
	if err := os.WriteFile(settings.Path(), []byte(`{"modelPrices":`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Find("rv")
	if err != nil {
		t.Fatal(err)
	}
	pr, ok := EffectivePrice("rv", "plain")
	if !ok || !costsNothing(pr) {
		t.Fatalf("billing reads %+v (%v), want the kept $0", pr, ok)
	}
	if !p.FreeModel("plain") {
		t.Fatal("FreeModel should read the kept $0 as the ledger does")
	}
}

// PaidBesideFree, which decides whether a key out of balance or quota
// rests for one paid model alone, reads the user's price before the
// vendor's word too: a relay key the user priced some models of at
// nothing keeps serving them when a paid one is refused. A list price of
// $0 with no vendor mark is no word here (the user's call on 10-27), so
// a provider nobody priced behaves as before.
func TestPaidBesideFreeReadsTheUsersOwnPrice(t *testing.T) {
	zero := stated(0, 0, 0, 0)
	paid := stated(1, 2, 0.1, 1.25)
	broken := zero
	broken.Output = nil
	cases := []struct {
		name   string
		prices map[string]settings.ModelPrice
		want   map[string]bool
	}{
		{"no price: upstream's word, the vendor's free one beside", nil,
			map[string]bool{"plain": true, "other": true, "vfree": false, "zero": true}},
		{"a model priced at nothing: the paid ones beside it", map[string]settings.ModelPrice{"rv/other": zero},
			map[string]bool{"plain": true, "other": false, "vfree": false, "zero": true}},
		{"every model at nothing: none is paid", map[string]settings.ModelPrice{"rv/*": zero},
			map[string]bool{"plain": false, "other": false, "vfree": false, "zero": false}},
		{"every model at nothing, one priced: it alone is paid", map[string]settings.ModelPrice{"rv/*": zero, "rv/plain": paid},
			map[string]bool{"plain": true, "other": false, "vfree": false, "zero": false}},
		{"the vendor's free one priced by the user is paid", map[string]settings.ModelPrice{"rv/vfree": paid},
			map[string]bool{"plain": false, "other": false, "vfree": false, "zero": false}},
		{"every model priced: the vendor's word stands", map[string]settings.ModelPrice{"rv/*": paid},
			map[string]bool{"plain": true, "other": true, "vfree": false, "zero": true}},
		{"from any provider at nothing is no word here", map[string]settings.ModelPrice{"*/other": zero},
			map[string]bool{"plain": true, "other": true, "vfree": false, "zero": true}},
		{"a broken price is no price", map[string]settings.ModelPrice{"rv/vfree": broken},
			map[string]bool{"plain": true, "other": true, "vfree": false, "zero": true}},
		{"a model not listed is not paid beside free", map[string]settings.ModelPrice{"rv/other": zero},
			map[string]bool{"gone": false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefsHome(t)
			if err := Save(Provider{ID: "rv", Name: "RV", Key: "sk-rv", Chat: "http://127.0.0.1:9/v1"}); err != nil {
				t.Fatal(err)
			}
			liveFree(t, "rv", `{"models":[{"id":"plain","cost":{"input":3,"output":15}},{"id":"other","cost":{"input":1,"output":2}},{"id":"vfree","free":true},{"id":"zero","cost":{"input":0,"output":0}}]}`)
			pricesSaved(t, c.prices)
			p, err := Find("rv")
			if err != nil {
				t.Fatal(err)
			}
			for model, want := range c.want {
				if got := p.PaidBesideFree(model); got != want {
					t.Errorf("PaidBesideFree(%q) = %v, want %v (prices %v)", model, got, want, c.prices)
				}
			}
		})
	}
}

// A model listed at $0 with no vendor mark is no free model beside a
// paid one (unlike FreeModel, which reads the list price): with nothing
// the user priced, a key with such a model rests whole as before.
func TestPaidBesideFreeTakesNoListPriceForFree(t *testing.T) {
	prefsHome(t)
	zero := catalog.Model{ID: "zero", Price: &catalog.Price{}}
	if freeBy(settings.Settings{}, "rv", zero) {
		t.Fatal("a $0 list price with no vendor mark and no user price should not count as free")
	}
	if !freeBy(settings.Settings{}, "rv", catalog.Model{ID: "vfree", Free: true}) {
		t.Fatal("the vendor's free mark should count as free")
	}
	if !freeBy(settings.Settings{ModelPrices: map[string]settings.ModelPrice{"rv/zero": stated(0, 0, 0, 0)}}, "rv", zero) {
		t.Fatal("a model the user priced at nothing should count as free")
	}
}

// A settings.json that can't be read, with no last good generation, holds
// no price: PaidBesideFree answers as with none, never as with the zero
// price it may have held.
func TestPaidBesideFreeWithUnreadableSettingsIsTheVendorsWord(t *testing.T) {
	prefsHome(t)
	if err := Save(Provider{ID: "rv", Name: "RV", Key: "sk-rv", Chat: "http://127.0.0.1:9/v1"}); err != nil {
		t.Fatal(err)
	}
	liveFree(t, "rv", `{"models":[{"id":"plain","cost":{"input":3,"output":15}},{"id":"other","cost":{"input":1,"output":2}}]}`)
	p, err := Find("rv")
	if err != nil {
		t.Fatal(err)
	}
	if p.PaidBesideFree("plain") || p.PaidBesideFree("other") {
		t.Fatal("with no price nothing is paid beside free")
	}
	if err := os.MkdirAll(filepath.Dir(settings.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings.Path(), []byte(`{"modelPrices":{"rv/other":{"input":0,`), 0o644); err != nil {
		t.Fatal(err)
	}
	if p.PaidBesideFree("plain") {
		t.Fatal("an unreadable settings.json should hold no price")
	}
}
