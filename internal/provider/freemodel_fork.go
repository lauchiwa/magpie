package provider

import (
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/settings"
)

// userFree is what the user's own price (settings' ModelPrices, `magpie
// model price`) says about whether model costs nothing on provider id,
// for FreeModel and so for routing "free" (routes.FreeFirst). said is
// false when it says nothing, and FreeModel then goes by the vendor's
// word and list price as before.
//
// A relay's /v1/models marks no model free, and its model ids are priced
// at their maker's list price, so a model the user knows the relay serves
// for nothing was put behind the paid ones while its calls were already
// counted at the user's $0 (priceOf reads the same keys first).
//
//   - "<id>/<model>" is the user's word on that one model: free when it is
//     nothing in every part, paid otherwise, the vendor's free mark too.
//   - "<id>/*" priced at nothing makes every model of the provider free. Any
//     other price there says nothing to routing: it is mostly a relay's
//     multiplier, and taking it for "paid" would put a vendor's own free
//     models (OpenRouter's :free) behind the paid ones. Their calls are
//     still counted at that price, as they were before.
//   - "*/<model>" is the price from any provider the user hasn't priced the
//     model for. Billing counts it, but it is no word on what this one
//     provider serves for nothing (one relay may give a model away that
//     another charges for), so routing doesn't read it.
//   - A price that doesn't parse (a part left out by hand) is no price, as
//     priceOf skips it. settings.json is read as settings.Load reads it:
//     one that doesn't parse is its last good generation, else holds none.
func userFree(s settings.Settings, id, model string) (free, said bool) {
	if m, ok := s.ModelPrices[id+"/"+model]; ok {
		if pr, bad := m.Price(); bad == "" {
			return costsNothing(pr), true
		}
	}
	if m, ok := s.ModelPrices[id+"/*"]; ok {
		if pr, bad := m.Price(); bad == "" && costsNothing(pr) {
			return true, true
		}
	}
	return false, false
}

// costsNothing: every part of the price is 0, the 1-hour cache write and
// each tier's too, so no request to the model is charged anything.
func costsNothing(pr catalog.Price) bool {
	if pr.Input != 0 || pr.Output != 0 || pr.CacheRead != 0 || pr.CacheWrite != 0 || pr.CacheWrite1h != 0 {
		return false
	}
	for _, t := range pr.Tiers {
		if t.Input != 0 || t.Output != 0 || t.CacheRead != 0 || t.CacheWrite != 0 || t.CacheWrite1h != 0 {
			return false
		}
	}
	return true
}
