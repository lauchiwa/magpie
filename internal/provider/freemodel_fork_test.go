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
