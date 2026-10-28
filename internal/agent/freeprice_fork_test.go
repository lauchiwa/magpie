package agent

import (
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// fork: the Agents page's model picker draws its free badge from an
// option's Free and says which of the user's prices made it free from
// Priced (provider.FreeBy): a relay's model the user priced at nothing is
// free there with the word "model", every model of a provider priced at
// nothing with "provider", and one the user gave a price is not free.
func TestPickerFreeReadsTheUsersOwnPrice(t *testing.T) {
	syncHome(t)
	if err := provider.Save(provider.Provider{ID: "pz", Name: "PZ", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"m1", "m2"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "all", Name: "All", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"a1"}}); err != nil {
		t.Fatal(err)
	}
	s := settings.Load()
	s.ModelPrices = map[string]settings.ModelPrice{
		"pz/m1": settings.StatedPrice(catalog.Price{}),
		"pz/m2": settings.StatedPrice(catalog.Price{Input: 1, Output: 2}),
		"all/*": settings.StatedPrice(catalog.Price{}),
	}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	type word struct {
		free   bool
		priced string
	}
	got := map[string]word{}
	for _, o := range viaMagpie("claude", "") {
		got[o.Ref] = word{o.Free, o.Priced}
	}
	want := map[string]word{
		"pz/m1":         {true, provider.PricedModel},
		"pz/m2":         {false, provider.PricedPaid},
		"all/a1":        {true, provider.PricedProvider},
		"relay/glm-4.6": {},
	}
	for ref, w := range want {
		if g, ok := got[ref]; !ok || g != w {
			t.Errorf("the picker's %s = %+v (listed %v), want %+v", ref, g, ok, w)
		}
	}
}
