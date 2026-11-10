package provider

import (
	"fmt"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/settings"
)

// BillingMode is dormant billing metadata, not a live routing or tariff rule.
type BillingMode string

const (
	BillingList  BillingMode = "list"
	BillingFree  BillingMode = "free"
	BillingQuota BillingMode = "quota"
)

// QuotaExhaustion records a future preference; paid grants no permission today.
type QuotaExhaustion string

const (
	QuotaStop QuotaExhaustion = "stop"
	QuotaPaid QuotaExhaustion = "paid"
)

// BillingPolicy is a proposed policy derived only from the supplied snapshot.
// Legacy means Billing was absent, not that a default was written to storage.
type BillingPolicy struct {
	Mode      BillingMode
	Exhausted QuotaExhaustion
	Legacy    bool
}

// BillingSource names the evidence used by ResolveBilling, never a live tariff.
type BillingSource string

const (
	BillingSourceModel       BillingSource = "model"
	BillingSourceProvider    BillingSource = "provider"
	BillingSourceQuota       BillingSource = "quota"
	BillingSourceVendor      BillingSource = "vendor"
	BillingSourceCatalogZero BillingSource = "catalog-zero"
	BillingSourceList        BillingSource = "list"
	BillingSourceUnknown     BillingSource = "unknown"
)

// BillingDecision is an inactive proposal, not today's cost or rest behavior.
// Free classification alone does not prove independence from shared allowance.
type BillingDecision struct {
	Policy               BillingPolicy
	Free                 bool
	Source               BillingSource
	AllowanceIndependent bool
}

// CheckBilling accepts omission for legacy data, but never repairs unknown enums.
func (p Provider) CheckBilling() error {
	switch p.Billing {
	case "", BillingList, BillingFree, BillingQuota:
	default:
		return fmt.Errorf("provider %q: unknown billing mode %q", p.ID, p.Billing)
	}
	switch p.QuotaExhausted {
	case "", QuotaStop, QuotaPaid:
	default:
		return fmt.Errorf("provider %q: unknown quota exhaustion policy %q", p.ID, p.QuotaExhausted)
	}
	return nil
}

// BillingPolicy derives omitted metadata without reading or changing any state.
// Only a valid zero provider wildcard stands over legacy CountFree.
func (p Provider) BillingPolicy(s settings.Settings) (BillingPolicy, error) {
	if err := p.CheckBilling(); err != nil {
		return BillingPolicy{}, err
	}
	b := BillingPolicy{Mode: p.Billing, Exhausted: p.QuotaExhausted, Legacy: p.Billing == ""}
	if b.Exhausted == "" {
		b.Exhausted = QuotaStop
	}
	if b.Legacy {
		b.Mode = BillingList
		if p.CountFree {
			b.Mode = BillingQuota
		}
		if m, ok := s.ModelPrices[p.ID+"/*"]; ok {
			if pr, bad := m.Price(); bad == "" && costsNothing(pr) {
				b.Mode = BillingFree
			}
		}
	}
	return b, nil
}

// ResolveBilling classifies supplied facts for future integration. No production
// routing, budget, Usage or UI consumer uses it. Exact valid prices win over an
// explicitly set free/quota policy; global and nonzero provider wildcards grant
// no exemption. Legacy CountFree stands over an exact paid price, as FreeModel
// reads it, so deriving a mode from it never turns one of its models paid.
func (p Provider) ResolveBilling(s settings.Settings, m catalog.Model) (BillingDecision, error) {
	b, err := p.BillingPolicy(s)
	if err != nil {
		return BillingDecision{}, err
	}
	d := BillingDecision{Policy: b, Source: BillingSourceUnknown}
	if mp, ok := s.ModelPrices[p.ID+"/"+m.ID]; ok {
		if pr, bad := mp.Price(); bad == "" && !(b.Legacy && p.CountFree && !costsNothing(pr)) {
			d.Source = BillingSourceModel
			d.Free = costsNothing(pr)
			d.AllowanceIndependent = d.Free
			return d, nil
		}
	}
	switch b.Mode {
	case BillingFree:
		d.Source, d.Free, d.AllowanceIndependent = BillingSourceProvider, true, true
		return d, nil
	case BillingQuota:
		d.Source, d.Free, d.AllowanceIndependent = BillingSourceQuota, true, m.Free
		return d, nil
	}
	if m.Free {
		d.Source, d.Free, d.AllowanceIndependent = BillingSourceVendor, true, true
	} else if m.Price != nil {
		// Validate catalog facts too: malformed tiers are not a zero tariff.
		mp := settings.StatedPrice(*m.Price)
		mp.CacheWrite1h = new(m.Price.CacheWrite1h) // StatedPrice omits nonpositive hour prices
		if pr, bad := mp.Price(); bad == "" {
			d.Source = BillingSourceList
			if costsNothing(pr) {
				d.Source, d.Free = BillingSourceCatalogZero, true
			}
		}
	}
	return d, nil
}

// PublishedPrice is model's price as p itself publishes it: ListPrice
// without the fallback to the maker's price, so a caller can tell the two
// apart. Only the dormant usage biller in internal/usage uses it.
func (p Provider) PublishedPrice(model string) (catalog.Price, bool) {
	pr, ok, _ := p.listPrice(model, false)
	return pr, ok
}

// checkBillingBatch validates the whole incoming batch before any write.
func checkBillingBatch(ps []Provider) error {
	for _, p := range ps {
		if err := p.CheckBilling(); err != nil {
			return err
		}
	}
	return nil
}
