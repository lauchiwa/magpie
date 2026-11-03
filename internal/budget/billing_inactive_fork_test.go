package budget

import (
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/testenv"
	"github.com/yetone/magpie/internal/usage"
)

func TestDormantBillingKeepsMonetaryBudget(t *testing.T) {
	for _, mode := range []provider.BillingMode{provider.BillingList, provider.BillingFree, provider.BillingQuota} {
		for _, exhausted := range []provider.QuotaExhaustion{provider.QuotaStop, provider.QuotaPaid} {
			t.Run(string(mode)+"/"+string(exhausted), func(t *testing.T) {
				testenv.SetHome(t, t.TempDir())
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				t.Setenv("XDG_CACHE_HOME", t.TempDir())
				Forget()
				t.Cleanup(Forget)
				if err := settings.Save(settings.Settings{ModelPrices: map[string]settings.ModelPrice{
					"*/m": {Input: new(1000.0), Output: new(0.0), CacheRead: new(0.0), CacheWrite: new(0.0)},
				}}); err != nil {
					t.Fatal(err)
				}
				p := provider.Provider{ID: "relay", Name: "Relay", Key: "test-key", Chat: "http://127.0.0.1:1/v1"}
				if err := provider.Save(p); err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				key := access.Key{ID: "test-key", Name: "Test", Limit: &access.Limit{Period: "day", Cost: 1}}
				who := access.Identity{KeyID: key.ID, KeyName: key.Name, Limit: key.Limit}
				reserve := func(wantCost float64, wantCalls int) {
					t.Helper()
					release, refused := Reserve(who, 1200, "relay/m", now)
					if refused != nil {
						t.Fatal(refused)
					}
					defer release()
					if st := Of(key, now); st == nil || st.Cost != wantCost || st.Calls != wantCalls || st.Unpriced != 0 || st.ReservedCost != 0.3 || st.InFlight != 1 {
						t.Fatalf("reservation: %+v, want cost %v, calls %d, reserved cost 0.3", st, wantCost, wantCalls)
					}
				}
				reserve(0, 0)
				p.Billing, p.QuotaExhausted = mode, exhausted
				if err := provider.Save(p); err != nil {
					t.Fatal(err)
				}
				reserve(0, 0) // The cold reservation still uses the monetary input price.
				Append(usage.Record{Time: now, Provider: p.ID, Model: "m", Input: 300, Status: 200, CallerKeyID: key.ID})
				reserve(0.3, 1)
				Append(usage.Record{Time: now, Provider: p.ID, Model: "m", Input: 900, Status: 200, CallerKeyID: key.ID})
				for _, restart := range []bool{false, true} {
					if restart {
						Forget()
					}
					release, refused := Reserve(who, 1200, "relay/m", now)
					if release != nil {
						release()
					}
					if refused == nil || refused.Cost != 1.2 || refused.Calls != 2 || refused.Unpriced != 0 || !refused.Spent {
						t.Fatalf("restart=%v: refusal %+v, want two calls costing 1.2 against a 1 dollar cap", restart, refused)
					}
				}
			})
		}
	}
}
