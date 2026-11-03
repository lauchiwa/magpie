package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestDormantBillingOptionsNotExposed(t *testing.T) {
	groupsHome(t)
	p := mustFind(t, "a")
	p.Billing, p.QuotaExhausted = provider.BillingQuota, provider.QuotaPaid
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	before := mustFind(t, p.ID)
	for _, option := range []string{"billing=list", "billing=free", "billing=quota", "quota-exhausted=stop", "quota-exhausted=paid"} {
		t.Run(option, func(t *testing.T) {
			field, _, _ := strings.Cut(option, "=")
			if strings.Contains(providerUsage, field+"=") {
				t.Fatalf("inactive option %s advertised in provider help", field)
			}
			for _, pairs := range [][]string{{"free=yes", option}, {option, "free=yes"}} {
				err := providerCmd(append([]string{"provider", "set", p.ID}, pairs...))
				if err == nil || !strings.Contains(err.Error(), "unknown field \""+field+"\"") {
					t.Fatalf("%v: %v, want unknown field", pairs, err)
				}
				if got := mustFind(t, p.ID); !reflect.DeepEqual(got, before) {
					t.Fatalf("rejected options %v changed the provider", pairs)
				}
			}
		})
	}
}

func TestDormantBillingKeepsLegacyFreeCLI(t *testing.T) {
	groupsHome(t)
	for _, mode := range []provider.BillingMode{provider.BillingList, provider.BillingFree, provider.BillingQuota} {
		t.Run(string(mode), func(t *testing.T) {
			p := mustFind(t, "a")
			p.Billing, p.QuotaExhausted = mode, provider.QuotaPaid
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"yes", "no"} {
				if _, err := printed(t, func() error { return providerCmd([]string{"provider", "set", p.ID, "free=" + value}) }); err != nil {
					t.Fatal(err)
				}
				got := mustFind(t, p.ID)
				want := value == "yes"
				if got.CountFree != want || got.FreeModel("unlisted-test-model") != want || got.Billing != mode || got.QuotaExhausted != provider.QuotaPaid {
					t.Fatalf("free=%s: CountFree=%v, FreeModel=%v, billing=%s, quotaExhausted=%s", value, got.CountFree, got.FreeModel("unlisted-test-model"), got.Billing, got.QuotaExhausted)
				}
			}
		})
	}
}
