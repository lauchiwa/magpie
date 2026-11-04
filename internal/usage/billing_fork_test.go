package usage

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// billingHome is pageHome with a DeepSeek model in the catalog, and the
// providers the biller's tests bill through: relay publishes the openai
// list, bare publishes none (its maker's price lists it), and ds and cc
// send DeepSeek's model to DeepSeek and Command Code, which bill it double
// at their peak hours.
func billingHome(t *testing.T) {
	t.Helper()
	pageHome(t)
	if err := os.WriteFile(catalog.CachePath(), []byte(`{"openai":{"id":"openai","models":{"m":{"id":"m","cost":{"input":2,"output":8,"cache_read":0.5,"cache_write":2.5}},"claude-opus-4-1":{"id":"claude-opus-4-1","cost":{"input":15,"output":75}},"nope":{"id":"nope"}}},"deepseek":{"id":"deepseek","models":{"deepseek-v4-pro":{"id":"deepseek-v4-pro","cost":{"input":1,"output":2}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	catalog.Reset()
	for _, p := range []provider.Provider{
		{ID: "relay", Name: "Relay", Key: "test-key", Chat: "http://127.0.0.1:1/v1", Catalog: "openai"},
		{ID: "bare", Name: "Bare", Key: "test-key", Chat: "http://127.0.0.1:2/v1", Catalog: "nothere"},
		{ID: "ds", Name: "DS", Key: "test-key", Chat: "https://api.deepseek.com/v1", Catalog: "deepseek"},
		{ID: "cc", Name: "CC", Key: "test-key", Chat: "https://api.commandcode.ai/v1", Catalog: "deepseek"},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
}

func billNear(a *float64, b float64) bool { return a != nil && math.Abs(*a-b) < 1e-9 }

func billAmount(a *float64) string {
	if a == nil {
		return "nil"
	}
	return strconv.FormatFloat(*a, 'g', -1, 64)
}

var billListed = provider.BillingDecision{Policy: provider.BillingPolicy{Mode: provider.BillingList, Exhausted: provider.QuotaStop}, Source: provider.BillingSourceList}

// The charge is the ledger's cost of the same record at every peak edge,
// DeepSeek's own hours and Command Code's: the biller costs it through
// NewCoster, so the hour it was made at decides, and the list is the
// provider's published price at that hour too, double at peak.
func TestBillerChargeIsTheLedgers(t *testing.T) {
	billingHome(t)
	for _, tc := range []struct {
		name string
		id   string
		at   time.Time
		peak bool
	}{
		{"peak-opens", "cc", time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC), true},
		{"peak-closes", "cc", time.Date(2026, 11, 2, 4, 0, 0, 0, time.UTC), false},
		{"second-window-opens", "cc", time.Date(2026, 11, 2, 6, 0, 0, 0, time.UTC), true},
		{"last-peak-second", "cc", time.Date(2026, 11, 2, 9, 59, 59, 0, time.UTC), true},
		{"weekend", "cc", time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC), false},
		{"holiday-deepseek", "ds", time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC), false},
		{"holiday-weekdays", "cc", time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC), true},
		{"china-zone", "ds", time.Date(2026, 11, 2, 9, 0, 0, 0, time.FixedZone("CST", 8*3600)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Record{Time: tc.at, Provider: tc.id, Model: "deepseek-v4-pro", Input: 1_000_000, Output: 1_000_000, Status: 200}
			want := 3.0
			if tc.peak {
				want = 6
			}
			ledger, priced := NewCoster()(r)
			if !priced || ledger != want {
				t.Fatalf("ledger %v (priced %v), want %v", ledger, priced, want)
			}
			b := NewBiller()(r, billListed, "")
			if !billNear(b.Charge, ledger) || !billNear(b.List, want) || b.ListSource != "provider" {
				t.Fatalf("charge %s, list %s (%s); want %v and %v from the provider", billAmount(b.Charge), billAmount(b.List), b.ListSource, ledger, want)
			}
		})
	}
}

// What a call is charged follows its decision and the quota state it was
// made in: free and within-quota calls cost nothing, a call past a quota
// charged for then costs the ledger's price (Paid), one in an unknown
// quota state is unknown, and a model with no price is unknown under the
// list. The list price is the same whatever the mode.
func TestBillerChargeByDecision(t *testing.T) {
	billingHome(t)
	d := func(mode provider.BillingMode, ex provider.QuotaExhaustion, src provider.BillingSource, free, ai bool) provider.BillingDecision {
		return provider.BillingDecision{Policy: provider.BillingPolicy{Mode: mode, Exhausted: ex}, Free: free, Source: src, AllowanceIndependent: ai}
	}
	quota := func(ex provider.QuotaExhaustion, ai bool) provider.BillingDecision {
		return d(provider.BillingQuota, ex, provider.BillingSourceQuota, true, ai)
	}
	stop, paid := provider.QuotaStop, provider.QuotaPaid
	unpriced := -1.0
	for _, tc := range []struct {
		name   string
		model  string
		d      provider.BillingDecision
		quota  string
		charge float64 // unpriced: nil
		paid   bool
	}{
		{"list", "m", billListed, "", 10, false},
		{"list-unpriced", "nope", d(provider.BillingList, stop, provider.BillingSourceUnknown, false, false), "", unpriced, false},
		{"catalog-zero", "m", d(provider.BillingList, stop, provider.BillingSourceCatalogZero, true, false), "", 0, false},
		{"vendor-free", "m", d(provider.BillingList, stop, provider.BillingSourceVendor, true, true), "", 0, false},
		{"model-price-zero", "m", d(provider.BillingQuota, paid, provider.BillingSourceModel, true, true), "exhausted", 0, false},
		{"model-price-paid", "m", d(provider.BillingQuota, paid, provider.BillingSourceModel, false, false), "exhausted", 10, false},
		{"free", "m", d(provider.BillingFree, stop, provider.BillingSourceProvider, true, true), "", 0, false},
		{"free-unpriced", "nope", d(provider.BillingFree, stop, provider.BillingSourceProvider, true, true), "", 0, false},
		{"quota-stop", "m", quota(stop, false), "", 0, false},
		{"quota-stop-exhausted", "m", quota(stop, false), "exhausted", 0, false},
		{"quota-stop-unknown", "m", quota(stop, false), "unknown", 0, false},
		{"quota-paid", "m", quota(paid, false), "", 0, false},
		{"quota-paid-exhausted", "m", quota(paid, false), "exhausted", 10, true},
		{"quota-paid-exhausted-unpriced", "nope", quota(paid, false), "exhausted", unpriced, true},
		{"quota-paid-unknown", "m", quota(paid, false), "unknown", unpriced, false},
		{"quota-paid-vendor-free", "m", quota(paid, true), "exhausted", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Record{Time: time.Now(), Provider: "relay", Model: tc.model, Input: 1_000_000, Output: 1_000_000, Status: 200}
			b := NewBiller()(r, tc.d, tc.quota)
			if tc.charge == unpriced {
				if b.Charge != nil {
					t.Fatalf("charge %s, want unknown", billAmount(b.Charge))
				}
			} else if !billNear(b.Charge, tc.charge) {
				t.Fatalf("charge %s, want %v", billAmount(b.Charge), tc.charge)
			}
			if b.Paid != tc.paid || b.Mode != tc.d.Policy.Mode || b.Source != tc.d.Source || b.Quota != tc.quota {
				t.Fatalf("got %+v for %+v in %q", b, tc.d, tc.quota)
			}
			if tc.model == "m" && (!billNear(b.List, 10) || b.ListSource != "provider") {
				t.Fatalf("list %s (%s), want 10 from the provider", billAmount(b.List), b.ListSource)
			}
			if tc.model == "nope" && (b.List != nil || b.ListSource != "") {
				t.Fatalf("list %s (%s) for a model with no price", billAmount(b.List), b.ListSource)
			}
		})
	}
}

// The list price is the provider's own when it publishes one, its maker's
// when it doesn't or is gone, and nothing when neither prices the model. A
// [1m] id lists at its model's price, and a model the vendor served under
// another name at that name's. Peak hours follow the provider, a maker's
// price too.
func TestBillerListSource(t *testing.T) {
	billingHome(t)
	peak := time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, id, model, served string
		at                      time.Time
		list                    float64 // -1: nil
		source                  string
	}{
		{"published", "relay", "m", "", time.Now(), 10, "provider"},
		{"maker", "bare", "m", "", time.Now(), 10, "maker"},
		{"maker-of-a-gone-provider", "gone", "m", "", time.Now(), 10, "maker"},
		{"neither", "relay", "nothing-lists-this", "", time.Now(), -1, ""},
		{"one-million-context", "relay", "claude-opus-4-1[1m]", "", time.Now(), 90, "provider"},
		{"served", "relay", "an-alias", "m", time.Now(), 10, "provider"},
		{"published-at-peak", "ds", "deepseek-v4-pro", "", peak, 6, "provider"},
		{"maker-at-peak", "bare", "deepseek-v4-pro", "", peak, 3, "maker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Record{Time: tc.at, Provider: tc.id, Model: tc.model, Served: tc.served, Input: 1_000_000, Output: 1_000_000, Status: 200}
			b := NewBiller()(r, billListed, "")
			if tc.list < 0 {
				if b.List != nil || b.ListSource != "" {
					t.Fatalf("list %s (%s), want none", billAmount(b.List), b.ListSource)
				}
				return
			}
			if !billNear(b.List, tc.list) || b.ListSource != tc.source {
				t.Fatalf("list %s (%s), want %v (%s)", billAmount(b.List), b.ListSource, tc.list, tc.source)
			}
			if c, priced := NewCoster()(r); !priced || !billNear(b.Charge, c) {
				t.Fatalf("charge %s, ledger %v (priced %v)", billAmount(b.Charge), c, priced)
			}
		})
	}
}

// A price the user set and a provider's price rate change what the call is
// charged, the ledger's cost, and never its list price: the list is what
// the user's own prices are measured against. A call's own reported cost
// is its charge, and the list is still the catalog's.
func TestBillerListIgnoresTheUsersPrices(t *testing.T) {
	billingHome(t)
	if err := provider.Save(provider.Provider{ID: "half", Name: "Half", Key: "test-key", Chat: "http://127.0.0.1:3/v1", Catalog: "openai", PriceRate: 0.5}); err != nil {
		t.Fatal(err)
	}
	one := 1.0
	if err := settings.Save(settings.Settings{ModelPrices: map[string]settings.ModelPrice{"relay/m": {Input: &one, Output: &one, CacheRead: new(0.0), CacheWrite: new(0.0)}}}); err != nil {
		t.Fatal(err)
	}
	own := 0.25
	for _, tc := range []struct {
		name   string
		r      Record
		charge float64
	}{
		{"user-price", Record{Provider: "relay", Model: "m"}, 2},
		{"price-rate", Record{Provider: "half", Model: "m"}, 5},
		{"own-cost", Record{Provider: "bare", Model: "m", OwnCost: &own}, 0.25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.r
			r.Time, r.Input, r.Output, r.Status = time.Now(), 1_000_000, 1_000_000, 200
			b := NewBiller()(r, billListed, "")
			if c, _ := NewCoster()(r); !billNear(b.Charge, tc.charge) || c != tc.charge {
				t.Fatalf("charge %s, ledger %v; want %v", billAmount(b.Charge), c, tc.charge)
			}
			if !billNear(b.List, 10) {
				t.Fatalf("list %s, want the catalog's 10", billAmount(b.List))
			}
		})
	}
}

// Totals add what is known and count what isn't: a saving is summed only
// over calls with both a charge and a list price, and goes negative where
// the provider charged more than the list.
func TestBillingTotals(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	var tot BillingTotals
	for _, b := range []CallBilling{
		{Charge: f(2), List: f(10)},
		{Charge: f(12), List: f(10)},
		{Charge: f(0), List: f(5)},
		{List: f(7)},
		{Charge: f(3)},
		{},
	} {
		tot.Add(b)
	}
	want := BillingTotals{Charge: 17, Charged: 4, Uncharged: 2, List: 32, Listed: 4, Unlisted: 2, Saved: 11, Paired: 3}
	if tot != want {
		t.Fatalf("totals %+v, want %+v", tot, want)
	}
	var over BillingTotals
	over.Add(CallBilling{Charge: f(12), List: f(10)})
	if over.Saved != -2 || over.Paired != 1 {
		t.Fatalf("a charge over the list saves %v over %d, want -2 over 1", over.Saved, over.Paired)
	}
}

// A charge of nothing is written as 0, and an unknown one not at all, so a
// reader tells a free call from one whose cost is not known.
func TestCallBillingJSON(t *testing.T) {
	free, err := json.Marshal(CallBilling{Mode: provider.BillingFree, Source: provider.BillingSourceProvider, Charge: new(0.0)})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := json.Marshal(CallBilling{Mode: provider.BillingQuota, Source: provider.BillingSourceQuota, Quota: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(free); got != `{"mode":"free","source":"provider","charge":0}` {
		t.Errorf("free call: %s", got)
	}
	if got := string(unknown); got != `{"mode":"quota","source":"quota","quota":"unknown"}` {
		t.Errorf("unknown charge: %s", got)
	}
}

// A call made before its provider was deleted, and another added under its
// id, lists at the deleted one's price, as the ledger costs it: here its
// maker's, not the new one's published price doubled at peak.
func TestBillerListsADeletedProvidersCallAsItsOwn(t *testing.T) {
	billingHome(t)
	if err := provider.Save(provider.Provider{ID: "x", Name: "Old", Key: "test-key", Chat: "http://127.0.0.1:4/v1", Catalog: "nothere"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "x", Name: "New", Key: "test-key", Chat: "https://api.deepseek.com/v1", Catalog: "deepseek"}); err != nil {
		t.Fatal(err)
	}
	r := Record{Time: time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC), Provider: "x", Model: "deepseek-v4-pro", Input: 1_000_000, Output: 1_000_000, Status: 200}
	b := NewBiller()(r, billListed, "")
	if c, priced := NewCoster()(r); !priced || c != 3 || !billNear(b.Charge, 3) {
		t.Fatalf("charge %s, ledger %v (priced %v); want the deleted one's 3", billAmount(b.Charge), c, priced)
	}
	if !billNear(b.List, 3) || b.ListSource != "maker" {
		t.Fatalf("list %s (%s), want the maker's 3", billAmount(b.List), b.ListSource)
	}
}
