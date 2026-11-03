package usage_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/middleware"
	"github.com/yetone/magpie/internal/plugin"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/testenv"
	"github.com/yetone/magpie/internal/usage"
)

// Exercise the real Append -> observer queue -> JS onUsage path. No dormant
// envelope is wired into that path and configured modes cannot reprice its row.
func TestDormantBillingMiddlewareUsageHook(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	catalog.Reset()
	t.Cleanup(catalog.Reset)
	if err := os.MkdirAll(filepath.Dir(catalog.CachePath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog.CachePath(), []byte(`{"openai":{"id":"openai","models":{"m":{"id":"m","cost":{"input":2,"output":8,"cache_read":0.5,"cache_write":2.5}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(settings.Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	quota := filepath.Join(settings.Dir(), "quota-state.json")
	quotaBytes := []byte(`{"version":999,"synthetic":"must-not-read-or-write"}`)
	if err := os.WriteFile(quota, quotaBytes, 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "billing.middleware.js")
	if err := os.WriteFile(source, []byte(`export function onUsage(row) { throw new Error("BILLING_ROW:" + JSON.stringify(row)); }`), 0600); err != nil {
		t.Fatal(err)
	}
	plugins := filepath.Join(settings.Dir(), "plugins.json")
	b, err := json.Marshal(plugin.List{Plugins: []plugin.Entry{{Spec: source}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugins, b, 0600); err != nil {
		t.Fatal(err)
	}
	middleware.Reload()
	t.Cleanup(func() {
		if err := os.Remove(plugins); err != nil {
			t.Error(err)
		}
		middleware.Reload()
	})
	p := provider.Provider{ID: "billing-relay", Name: "Billing fixture", Key: "test-key", Chat: "http://127.0.0.1:1/v1", Catalog: "openai"}
	for _, mode := range []provider.BillingMode{provider.BillingList, provider.BillingFree, provider.BillingQuota} {
		t.Run(string(mode), func(t *testing.T) {
			p.Billing = mode
			p.QuotaExhausted = provider.QuotaPaid
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			// Warm only the current-price reader, so the bounded queue assertion does not
			// measure a cold catalogue startup under a loaded test runner.
			cost, known := usage.NewCoster()(usage.Record{Provider: p.ID, Model: "m", Input: 1_000_000, Output: 1_000_000})
			if !known || cost != 10 {
				t.Fatalf("live price=%v known=%v", cost, known)
			}
			observer := middleware.Points()
			remove := usage.Observe(observer)
			defer remove()
			stop := usage.ServeObservers()
			defer stop()
			for _, tc := range []struct {
				model    string
				reported *float64
				want     *float64
			}{{"m", nil, new(10.0)}, {"m", new(0.0), new(0.0)}, {"m", new(.5), new(.5)}, {"unknown-model-no-price", nil, nil}} {
				before := middleware.States()[source]
				usage.Append(usage.Record{Time: time.Now(), Provider: p.ID, Model: tc.model, Input: 1_000_000, Output: 1_000_000, Status: 200, OwnCost: tc.reported, Error: "do-not-export", RequestID: "private-request", Session: "private-session"})
				deadline := time.Now().Add(10 * time.Second)
				for middleware.States()[source].Rows <= before.Rows || middleware.States()[source].Last == before.Last {
					if time.Now().After(deadline) {
						t.Fatal("onUsage did not receive the appended row")
					}
					time.Sleep(5 * time.Millisecond)
				}
				last := middleware.States()[source].Last
				_, raw, ok := strings.Cut(last, "BILLING_ROW:")
				if !ok {
					t.Fatalf("hook did not record row: %s", last)
				}
				var row map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &row); err != nil {
					t.Fatalf("hook row: %v (%s)", err, last)
				}
				if tc.want == nil {
					if string(row["cost"]) != "null" {
						t.Fatalf("unknown cost became %s", row["cost"])
					}
				} else {
					var got float64
					if err := json.Unmarshal(row["cost"], &got); err != nil || got != *tc.want {
						t.Fatalf("mode %s hook cost=%s want %v", mode, row["cost"], *tc.want)
					}
				}
				for _, field := range []string{"billing", "envelope", "reference", "difference", "session", "requestId", "error"} {
					if _, ok := row[field]; ok {
						t.Fatalf("unexpected live hook field %s", field)
					}
				}
			}
		})
	}
	after, err := os.ReadFile(quota)
	if err != nil || !bytes.Equal(after, quotaBytes) {
		t.Fatal("live hook touched dormant quota state", err)
	}
}
