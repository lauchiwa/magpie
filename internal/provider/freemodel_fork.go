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
	switch userPriced(s, id, model) {
	case PricedModel, PricedProvider:
		return true, true
	case PricedPaid:
		return false, true
	}
	return false, false
}

// Which of the user's own prices speaks for a model (userPriced): its own
// at nothing, its provider's at nothing, or its own at a price. ""
// is none of them, and the vendor's word stands.
const (
	PricedModel    = "model"
	PricedProvider = "provider"
	PricedPaid     = "paid"
)

// userPriced is userFree's reading, kept apart so the GUI can say which
// price made a model free: "<id>/<model>" first, then "<id>/*" when it
// is nothing.
func userPriced(s settings.Settings, id, model string) string {
	if m, ok := s.ModelPrices[id+"/"+model]; ok {
		if pr, bad := m.Price(); bad == "" {
			if costsNothing(pr) {
				return PricedModel
			}
			return PricedPaid
		}
	}
	if m, ok := s.ModelPrices[id+"/*"]; ok {
		if pr, bad := m.Price(); bad == "" && costsNothing(pr) {
			return PricedProvider
		}
	}
	return ""
}

// FreeBy is whether m counts as free on provider id by the rule
// PaidBesideFree goes by, and which of the user's prices said so
// (userPriced; "" when none did, and free is the vendor's word). The
// GUI's free badge reads it, so the editor and the model picker say what
// routing and resting do: a model the user priced at nothing is free, one
// they gave a price is not, even when its vendor or its name says free.
// A list price of $0 is not read, as in freeBy.
func FreeBy(s settings.Settings, id string, m catalog.Model) (free bool, priced string) {
	switch priced = userPriced(s, id, m.ID); priced {
	case PricedModel, PricedProvider:
		return true, priced
	case PricedPaid:
		return false, priced
	}
	return m.Free, ""
}

// freeBy is whether m counts as free on provider id for PaidBesideFree,
// which says whether a key out of balance or quota rests for one paid
// model alone: the user's own price when it says (userFree), else the
// vendor's word (catalog.Model.Free). A relay marks no model free, so a
// key the user priced some models of at nothing rested whole, those
// models with it, when a paid one was refused.
//
// Unlike FreeModel, a list price of $0 is not read here (the user's call
// on 10-27): only a price the user set changes which models rest, so a
// provider nobody priced rests as before. m is matched by its listed id,
// the spelling `magpie model price` stores it under.
func freeBy(s settings.Settings, id string, m catalog.Model) bool {
	free, _ := FreeBy(s, id, m)
	return free
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
