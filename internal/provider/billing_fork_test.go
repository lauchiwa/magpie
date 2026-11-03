package provider

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/settings"
)

func TestBillingPolicy(t *testing.T) {
	zero, paid := stated(0, 0, 0, 0), stated(1, 2, 0, 0)
	broken := zero
	broken.Output = nil
	for _, c := range []struct {
		name   string
		p      Provider
		prices map[string]settings.ModelPrice
		want   BillingPolicy
	}{
		{"omitted", Provider{}, nil, BillingPolicy{BillingList, QuotaStop, true}},
		{"legacy countfree", Provider{CountFree: true}, nil, BillingPolicy{BillingQuota, QuotaStop, true}},
		{"legacy zero wins", Provider{CountFree: true}, map[string]settings.ModelPrice{"rv/*": zero}, BillingPolicy{BillingFree, QuotaStop, true}},
		{"nonzero wildcard", Provider{}, map[string]settings.ModelPrice{"rv/*": paid}, BillingPolicy{BillingList, QuotaStop, true}},
		{"broken wildcard", Provider{CountFree: true}, map[string]settings.ModelPrice{"rv/*": broken}, BillingPolicy{BillingQuota, QuotaStop, true}},
		{"global ignored", Provider{}, map[string]settings.ModelPrice{"*/m": zero}, BillingPolicy{BillingList, QuotaStop, true}},
		{"explicit list wins", Provider{Billing: BillingList, CountFree: true}, map[string]settings.ModelPrice{"rv/*": zero}, BillingPolicy{BillingList, QuotaStop, false}},
		{"explicit free", Provider{Billing: BillingFree}, nil, BillingPolicy{BillingFree, QuotaStop, false}},
		{"explicit quota wins", Provider{Billing: BillingQuota, QuotaExhausted: QuotaPaid}, map[string]settings.ModelPrice{"rv/*": zero}, BillingPolicy{BillingQuota, QuotaPaid, false}},
		{"paid preference alone", Provider{QuotaExhausted: QuotaPaid}, nil, BillingPolicy{BillingList, QuotaPaid, true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.p.ID = "rv"
			got, err := c.p.BillingPolicy(settings.Settings{ModelPrices: c.prices})
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestBillingResolve(t *testing.T) {
	zero, paid := stated(0, 0, 0, 0), stated(1, 2, 0, 0)
	broken := zero
	broken.Output = nil
	for _, c := range []struct {
		name              string
		p                 Provider
		prices            map[string]settings.ModelPrice
		m                 catalog.Model
		free, independent bool
		source            BillingSource
	}{
		{"unknown", Provider{}, nil, catalog.Model{}, false, false, BillingSourceUnknown},
		{"catalog paid", Provider{}, nil, catalog.Model{Price: &catalog.Price{Input: 1}}, false, false, BillingSourceList},
		{"catalog zero not exemption", Provider{}, nil, catalog.Model{Price: &catalog.Price{}}, true, false, BillingSourceCatalogZero},
		{"zero tiers", Provider{}, nil, catalog.Model{Price: &catalog.Price{Tiers: []catalog.Tier{{Above: 1}, {Above: 2}}}}, true, false, BillingSourceCatalogZero},
		{"vendor", Provider{}, nil, catalog.Model{Free: true, Price: &catalog.Price{Input: 1}}, true, true, BillingSourceVendor},
		{"free policy", Provider{Billing: BillingFree}, nil, catalog.Model{}, true, true, BillingSourceProvider},
		{"quota policy", Provider{Billing: BillingQuota}, nil, catalog.Model{Price: &catalog.Price{}}, true, false, BillingSourceQuota},
		{"quota vendor exemption", Provider{Billing: BillingQuota}, nil, catalog.Model{Free: true}, true, true, BillingSourceQuota},
		{"legacy countfree", Provider{CountFree: true}, nil, catalog.Model{}, true, false, BillingSourceQuota},
		{"legacy wildcard", Provider{}, map[string]settings.ModelPrice{"rv/*": zero}, catalog.Model{}, true, true, BillingSourceProvider},
		{"exact free over quota", Provider{Billing: BillingQuota}, map[string]settings.ModelPrice{"rv/m": zero}, catalog.Model{}, true, true, BillingSourceModel},
		{"exact paid over free", Provider{Billing: BillingFree}, map[string]settings.ModelPrice{"rv/m": paid}, catalog.Model{Free: true}, false, false, BillingSourceModel},
		{"exact paid over quota", Provider{Billing: BillingQuota, CountFree: true}, map[string]settings.ModelPrice{"rv/m": paid}, catalog.Model{Free: true}, false, false, BillingSourceModel},
		{"exact paid over wildcard", Provider{}, map[string]settings.ModelPrice{"rv/m": paid, "rv/*": zero}, catalog.Model{}, false, false, BillingSourceModel},
		{"broken exact is unknown", Provider{}, map[string]settings.ModelPrice{"rv/m": broken}, catalog.Model{}, false, false, BillingSourceUnknown},
		{"broken exact leaves policy", Provider{Billing: BillingFree}, map[string]settings.ModelPrice{"rv/m": broken}, catalog.Model{}, true, true, BillingSourceProvider},
		{"broken exact leaves vendor", Provider{}, map[string]settings.ModelPrice{"rv/m": broken}, catalog.Model{Free: true}, true, true, BillingSourceVendor},
		{"global zero ignored", Provider{}, map[string]settings.ModelPrice{"*/m": zero}, catalog.Model{}, false, false, BillingSourceUnknown},
		{"nonzero wildcard leaves vendor", Provider{}, map[string]settings.ModelPrice{"rv/*": paid}, catalog.Model{Free: true}, true, true, BillingSourceVendor},
		{"explicit list ignores wildcard", Provider{Billing: BillingList}, map[string]settings.ModelPrice{"rv/*": zero}, catalog.Model{}, false, false, BillingSourceUnknown},
		{"exact spelling only", Provider{}, map[string]settings.ModelPrice{"rv/M": zero, "other/m": zero}, catalog.Model{}, false, false, BillingSourceUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.p.ID, c.m.ID = "rv", "m"
			s := settings.Settings{ModelPrices: c.prices}
			before, err := json.Marshal([]any{c.p, s, c.m})
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.p.ResolveBilling(s, c.m)
			if err != nil || got.Free != c.free || got.AllowanceIndependent != c.independent || got.Source != c.source {
				t.Fatalf("got %+v, %v; want free=%v independent=%v source=%s", got, err, c.free, c.independent, c.source)
			}
			policy, err := c.p.BillingPolicy(s)
			if err != nil || got.Policy != policy {
				t.Fatalf("decision policy: %+v, want %+v (%v)", got.Policy, policy, err)
			}
			after, err := json.Marshal([]any{c.p, s, c.m})
			if err != nil || string(before) != string(after) {
				t.Fatal("resolution mutated inputs", err)
			}
		})
	}
}

func TestBillingAllPriceComponents(t *testing.T) {
	for _, c := range []struct {
		name string
		pr   catalog.Price
	}{
		{"input", catalog.Price{Input: 1}},
		{"output", catalog.Price{Output: 1}},
		{"read", catalog.Price{CacheRead: 1}},
		{"write", catalog.Price{CacheWrite: 1}},
		{"hour", catalog.Price{CacheWrite1h: 1}},
		{"tier input", catalog.Price{Tiers: []catalog.Tier{{Above: 1, Input: 1}}}},
		{"tier output", catalog.Price{Tiers: []catalog.Tier{{Above: 1, Output: 1}}}},
		{"tier read", catalog.Price{Tiers: []catalog.Tier{{Above: 1, CacheRead: 1}}}},
		{"tier write", catalog.Price{Tiers: []catalog.Tier{{Above: 1, CacheWrite: 1}}}},
		{"later tier hour", catalog.Price{Tiers: []catalog.Tier{{Above: 1}, {Above: 2, CacheWrite1h: 1}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := Provider{ID: "rv"}
			m := catalog.Model{ID: "m", Price: &c.pr}
			d, err := p.ResolveBilling(settings.Settings{}, m)
			if err != nil || d.Free || d.AllowanceIndependent || d.Source != BillingSourceList {
				t.Fatalf("catalog: %+v, %v", d, err)
			}
			p.Billing = BillingFree
			d, err = p.ResolveBilling(settings.Settings{ModelPrices: map[string]settings.ModelPrice{"rv/m": settings.StatedPrice(c.pr)}}, m)
			if err != nil || d.Free || d.AllowanceIndependent || d.Source != BillingSourceModel {
				t.Fatalf("exact: %+v, %v", d, err)
			}
			p.Billing = ""
			b, err := p.BillingPolicy(settings.Settings{ModelPrices: map[string]settings.ModelPrice{"rv/*": settings.StatedPrice(c.pr)}})
			if err != nil || b.Mode != BillingList {
				t.Fatalf("wildcard: %+v, %v", b, err)
			}
		})
	}
}

func TestBillingMalformedPrices(t *testing.T) {
	for _, pr := range []catalog.Price{
		{Input: -1}, {Output: math.NaN()}, {CacheRead: math.Inf(1)}, {CacheWrite: -1},
		{CacheWrite1h: -1}, {CacheWrite1h: math.NaN()}, {CacheWrite1h: math.Inf(-1)},
		{Tiers: []catalog.Tier{{Above: 0}}}, {Tiers: []catalog.Tier{{Above: 2}, {Above: 1}}},
		{Tiers: []catalog.Tier{{Above: 1, CacheWrite1h: -1}}},
	} {
		p := Provider{ID: "rv"}
		m := catalog.Model{ID: "m", Price: &pr}
		d, err := p.ResolveBilling(settings.Settings{}, m)
		if err != nil || d.Free || d.AllowanceIndependent || d.Source != BillingSourceUnknown {
			t.Fatalf("malformed catalog %+v: %+v, %v", pr, d, err)
		}
		mp := settings.StatedPrice(pr)
		mp.CacheWrite1h = new(pr.CacheWrite1h)
		s := settings.Settings{ModelPrices: map[string]settings.ModelPrice{"rv/m": mp, "rv/*": mp}}
		d, err = p.ResolveBilling(s, catalog.Model{ID: "m"})
		if err != nil || d.Free || d.AllowanceIndependent || d.Source != BillingSourceUnknown || d.Policy.Mode != BillingList {
			t.Fatalf("malformed saved %+v: %+v, %v", pr, d, err)
		}
	}
}

func TestBillingInvalidEnumsFailClosed(t *testing.T) {
	for _, p := range []Provider{
		{Billing: "FREE"}, {Billing: " free"}, {Billing: "future"},
		{QuotaExhausted: "PAID"}, {QuotaExhausted: "paid "}, {QuotaExhausted: "continue"},
	} {
		p.ID, p.CountFree = "rv", true
		s := settings.Settings{ModelPrices: map[string]settings.ModelPrice{"rv/m": stated(0, 0, 0, 0)}}
		if p.CheckBilling() == nil {
			t.Fatalf("accepted %+v", p)
		}
		b, err := p.BillingPolicy(s)
		if err == nil || b != (BillingPolicy{}) {
			t.Fatalf("invalid policy: %+v, %v", b, err)
		}
		d, err := p.ResolveBilling(s, catalog.Model{ID: "m", Free: true})
		if err == nil || d != (BillingDecision{}) {
			t.Fatalf("invalid decision: %+v, %v", d, err)
		}
	}
}

func TestBillingSourceValues(t *testing.T) {
	got := []BillingSource{BillingSourceModel, BillingSourceProvider, BillingSourceQuota, BillingSourceVendor, BillingSourceCatalogZero, BillingSourceList, BillingSourceUnknown}
	want := []BillingSource{"model", "provider", "quota", "vendor", "catalog-zero", "list", "unknown"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source contract: %v", got)
	}
}
