package gui

import (
	"fmt"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// billingWildcard sets or clears p's `<id>/*` in settings.json.
func billingWildcard(t *testing.T, id string, m *settings.ModelPrice) {
	t.Helper()
	s := settings.Load()
	if m == nil {
		delete(s.ModelPrices, id+"/*")
	} else {
		if s.ModelPrices == nil {
			s.ModelPrices = map[string]settings.ModelPrice{}
		}
		s.ModelPrices[id+"/*"] = *m
	}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
}

// The billing control opens on the mode the legacy fields give today, as
// BillingPolicy derives it, for a key provider and a subscription alike;
// a `<id>/*` priced otherwise or that doesn't read locks it. An explicit
// Billing in the file is dormant and moves nothing.
func TestBillingViewReadsTheLegacyFields(t *testing.T) {
	zero := settings.StatedPrice(catalog.Price{})
	paid := settings.StatedPrice(catalog.Price{Input: 1, Output: 2})
	broken := settings.StatedPrice(catalog.Price{})
	broken.Output = nil
	cases := []struct {
		name      string
		countFree bool
		wildcard  *settings.ModelPrice
		mode      provider.BillingMode
		locked    bool
	}{
		{"none", false, nil, provider.BillingList, false},
		{"countFree", true, nil, provider.BillingQuota, false},
		{"zero wildcard", false, &zero, provider.BillingFree, false},
		{"both", true, &zero, provider.BillingFree, false},
		{"paid wildcard", false, &paid, provider.BillingList, true},
		{"paid wildcard and countFree", true, &paid, provider.BillingQuota, true},
		{"bad wildcard", false, &broken, provider.BillingList, true},
	}
	for _, kind := range []string{"key", "subscription"} {
		for _, c := range cases {
			for _, explicit := range []provider.BillingMode{"", provider.BillingFree, provider.BillingList} {
				t.Run(kind+"/"+c.name+"/"+string(explicit), func(t *testing.T) {
					home := billingGUIHome(t)
					if kind == "subscription" {
						billingGUIAccount(t, home)
					}
					p := billingGUISeed(t, kind, explicit, "")
					p.CountFree = c.countFree
					if err := provider.Save(p); err != nil {
						t.Fatal(err)
					}
					billingWildcard(t, p.ID, c.wildcard)
					held, err := provider.Find(p.ID)
					if err != nil {
						t.Fatal(err)
					}
					info := providerInfo(*held, nil)
					if info.BillingMode != c.mode || info.BillingLocked != c.locked {
						t.Fatalf("billing view = %q locked %v, want %q locked %v", info.BillingMode, info.BillingLocked, c.mode, c.locked)
					}
					legacy := *held
					legacy.Billing, legacy.QuotaExhausted = "", ""
					if b, err := legacy.BillingPolicy(settings.Load()); err != nil || b.Mode != info.BillingMode {
						t.Fatalf("view %q, BillingPolicy %+v %v", info.BillingMode, b, err)
					}
				})
			}
		}
	}
}

// What the control stages is what its editor's Save already sends:
// countFree and model prefs under "*". Each mode written that way reads
// back as that mode, from any mode before it, and a key provider's draft
// read before it changed elsewhere writes neither (a signed-in account's
// picks are saved by id, with no rev: provider.AsRead).
func TestBillingControlWritesTheLegacyFields(t *testing.T) {
	writes := map[provider.BillingMode]string{
		provider.BillingList:  `"countFree":false,"modelPrefs":{"*":{"ownPrice":true}}`,
		provider.BillingFree:  `"countFree":false,"modelPrefs":{"*":{"price":{"input":0,"output":0,"cache_read":0,"cache_write":0}}}`,
		provider.BillingQuota: `"countFree":true,"modelPrefs":{"*":{"ownPrice":true}}`,
	}
	modes := []provider.BillingMode{provider.BillingList, provider.BillingFree, provider.BillingQuota}
	for _, kind := range []string{"key", "subscription"} {
		for _, from := range modes {
			for _, to := range modes {
				t.Run(kind+"/"+string(from)+"-"+string(to), func(t *testing.T) {
					home := billingGUIHome(t)
					if kind == "subscription" {
						billingGUIAccount(t, home)
					}
					p := billingGUISeed(t, kind, "", "")
					if err := provider.ReadyLifecycle(); err != nil {
						t.Fatal(err)
					}
					save := func(mode provider.BillingMode, rev string) (int, map[string]string) {
						body := fmt.Sprintf(`{"id":%q,"from":%q,"name":%q,"key":"","models":%q,"rev":%q,%s`, p.ID, p.ID, p.Name, p.Models, rev, writes[mode])
						if kind == "key" {
							body += `,"chat":"http://127.0.0.1:1/v1"`
						}
						return providerPost(t, "save", body+"}")
					}
					rev := func() string {
						if kind == "subscription" {
							return ""
						}
						return pageRev(t, p.ID)
					}
					mode := func() provider.BillingMode {
						held, err := provider.Find(p.ID)
						if err != nil {
							t.Fatal(err)
						}
						return providerInfo(*held, nil).BillingMode
					}
					if code, out := save(from, rev()); code != 200 {
						t.Fatalf("save %s: %d %v", from, code, out)
					}
					if got := mode(); got != from {
						t.Fatalf("after saving %s it reads %s", from, got)
					}
					if code, out := save(to, rev()); code != 200 {
						t.Fatalf("save %s: %d %v", to, code, out)
					}
					if got := mode(); got != to {
						t.Fatalf("after saving %s it reads %s", to, got)
					}
					held, _ := provider.Find(p.ID)
					if held.FreeModel("unlisted-fixture") != (to != provider.BillingList) {
						t.Fatalf("%s: FreeModel = %v", to, held.FreeModel("unlisted-fixture"))
					}
					if kind == "subscription" {
						return
					}
					// stale: the page read the provider before it changed
					old := pageRev(t, p.ID)
					held.Name = "Changed elsewhere"
					if err := provider.Save(*held); err != nil {
						t.Fatal(err)
					}
					if code, out := save(from, old); code != 400 || out["code"] != "stale" {
						t.Fatalf("a save from an older read: %d %v", code, out)
					}
					if got := mode(); got != to {
						t.Fatalf("a refused save moved %s to %s", to, got)
					}
				})
			}
		}
	}
}
