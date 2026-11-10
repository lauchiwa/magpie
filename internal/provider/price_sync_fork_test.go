package provider

import (
	"math"
	"os"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
)

func TestPublishedPriceContinuesPastTheMaker(t *testing.T) {
	prefsHome(t)
	if err := os.WriteFile(catalog.CachePath(), []byte(`{"openai":{"models":{"shared":{"id":"shared","cost":{"input":2,"output":8}}}},"commandcode":{"models":{"shared":{"id":"shared","cost":{"input":7,"output":9}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog.Reset()
	t.Cleanup(catalog.Reset)
	p := Provider{ID: CommandCodePlanID}
	listed, ok := p.ListPrice("shared")
	if !ok || listed.Input != 2 {
		t.Fatalf("ordinary lookup: %+v, %v", listed, ok)
	}
	published, ok := p.PublishedPrice("shared")
	if !ok || published.Input != 7 {
		t.Fatalf("skipping maker did not reach vendor price: %+v, %v", published, ok)
	}
}

func TestZeroPriceIncludesHourlyTiersAndValidity(t *testing.T) {
	for _, pr := range []catalog.Price{
		{CacheWrite1h: 1},
		{Tiers: []catalog.Tier{{Above: 100, CacheWrite1h: 1}}},
		{Tiers: []catalog.Tier{{Above: 0}}},
		{Tiers: []catalog.Tier{{Above: 100}, {Above: 50}}},
		{Input: math.NaN()},
	} {
		if zeroPrice(pr) || costsNothing(pr) {
			t.Fatalf("nonzero or invalid tariff is free: %+v", pr)
		}
	}
	if !zeroPrice(catalog.Price{Tiers: []catalog.Tier{{Above: 100}}}) {
		t.Fatal("valid zero tier is not zero")
	}
}
