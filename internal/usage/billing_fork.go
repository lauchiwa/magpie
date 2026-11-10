package usage

import (
	"slices"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// CallBilling is what one call costs under its provider's billing mode,
// beside what its list price would have made it. Nothing in production
// writes or shows it yet. A nil amount is unknown, not zero.
type CallBilling struct {
	Mode   provider.BillingMode   `json:"mode"`
	Source provider.BillingSource `json:"source"`
	// Charge is what the call costs the user: the ledger's cost, nothing
	// for a free or quota call, and the ledger's again for a quota call
	// past its quota on a provider that charges then (Paid)
	Charge *float64 `json:"charge,omitempty"`
	// List is the call at its list price, the provider's published one
	// ("provider") or its maker's ("maker"), with no user price, price
	// rate or rate window: what the user saves against
	List       *float64 `json:"list,omitempty"`
	ListSource string   `json:"listSource,omitempty"`
	// Quota is the provider's quota state when the call was made:
	// "", "exhausted" or "unknown"
	Quota string `json:"quota,omitempty"`
	Paid  bool   `json:"paid,omitempty"`
}

// NewBiller bills one record at a time from the decision ResolveBilling
// made for it and the quota state the call was made in, at the prices the
// ledger uses, read once (NewCoster). Not safe for concurrent use.
func NewBiller() func(r Record, d provider.BillingDecision, quota string) CallBilling {
	cost, renamed, all := NewCoster(), provider.UsageRenames(), provider.All()
	wires := settings.Load().Wires()
	type key struct{ id, model string }
	type listed struct {
		price  *catalog.Price
		source string
	}
	lists := map[key]listed{}
	listOf := func(id, model string) listed {
		k := key{id, model}
		if l, ok := lists[k]; ok {
			return l
		}
		// a provider gone since is held to its id's peak hours, as tariffOf does
		p, l := provider.Provider{ID: id}, listed{}
		if i := slices.IndexFunc(all, func(q provider.Provider) bool { return q.ID == id || slices.Contains(q.Was, id) }); i >= 0 {
			p = all[i]
			if pr, ok := p.PublishedPrice(model); ok {
				l = listed{&pr, "provider"}
			}
		}
		if l.price == nil {
			if pr, ok := provider.MakerPrice(model); ok {
				l = listed{&pr, "maker"}
			}
		}
		if peak := p.PeakOf(model); peak != "" && l.price != nil {
			l.price.Peak = peak
		}
		lists[k] = l
		return l
	}
	return func(r Record, d provider.BillingDecision, quota string) CallBilling {
		b := CallBilling{Mode: d.Policy.Mode, Source: d.Source, Quota: quota}
		c, priced := cost(r)
		// past a quota that is charged for then, or not known to be
		pays := d.Source == provider.BillingSourceQuota && !d.AllowanceIndependent && d.Policy.Exhausted == provider.QuotaPaid
		switch {
		case pays && quota == "exhausted":
			b.Paid = true
			if priced {
				b.Charge = &c
			}
		case pays && quota == "unknown":
			// it may have been charged or not: unknown
		case d.Free:
			b.Charge = new(0.0)
		case priced:
			b.Charge = &c
		}
		r.renameBy(renamed)
		r.OwnCost = nil
		model, alias := priceModels(wires, r)
		var l listed
		if model != "" {
			l = listOf(r.priceID(), model)
		}
		if l.price == nil && alias != "" {
			l = listOf(r.priceID(), alias)
		}
		if l.price != nil {
			var t Totals
			t.add(r, l.price)
			b.List, b.ListSource = &t.Cost, l.source
		}
		return b
	}
}

// BillingTotals sums calls' billing. Saved is list minus charge over the
// calls both are known for (Paired), so it can be negative: a provider that
// charges more than the list price.
type BillingTotals struct {
	Charge             float64
	Charged, Uncharged int
	List               float64
	Listed, Unlisted   int
	Saved              float64
	Paired             int
}

func (t *BillingTotals) Add(b CallBilling) {
	if b.Charge != nil {
		t.Charge += *b.Charge
		t.Charged++
	} else {
		t.Uncharged++
	}
	if b.List != nil {
		t.List += *b.List
		t.Listed++
	} else {
		t.Unlisted++
	}
	if b.Charge != nil && b.List != nil {
		t.Saved += *b.List - *b.Charge
		t.Paired++
	}
}
