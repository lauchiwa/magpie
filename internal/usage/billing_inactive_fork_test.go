package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func TestDormantBillingDoesNotRepriceHistory(t *testing.T) {
	pageHome(t)
	p := provider.Provider{ID: "relay", Name: "Relay", Key: "test-key", Chat: "http://127.0.0.1:1/v1", Catalog: "openai"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	rec := Record{Time: time.Now().Add(-24 * time.Hour), Agent: "codex", Provider: p.ID, Model: "m", Input: 1_000_000, Output: 1_000_000, Status: 200}
	Append(rec)
	check := func(t *testing.T) {
		t.Helper()
		if cost, priced := NewCoster()(rec); !priced || cost != 10 {
			t.Fatalf("historical cost = %v, priced = %v; want 10, true", cost, priced)
		}
		if got := QueryPage(All, Filter{}, 0, 100); got.Sum.Cost != 10 || len(got.Rows) != 1 || got.Rows[0].Cost != 10 || !got.Rows[0].Priced {
			t.Fatalf("historical page: %+v, want one priced row costing 10", got)
		}
	}
	check(t)
	for _, mode := range []provider.BillingMode{provider.BillingList, provider.BillingFree, provider.BillingQuota} {
		for _, exhausted := range []provider.QuotaExhaustion{provider.QuotaStop, provider.QuotaPaid} {
			t.Run(string(mode)+"/"+string(exhausted), func(t *testing.T) {
				p.Billing, p.QuotaExhausted = mode, exhausted
				if err := provider.Save(p); err != nil {
					t.Fatal(err)
				}
				check(t)
			})
		}
	}
}

// The provider guard blocks outward consumers of ResolveBilling. This one
// keeps the usage biller dormant: no production file names it but its own.
func TestDormantBillerHasNoProductionConsumer(t *testing.T) {
	root := filepath.Join("..", "..")
	names := []string{"CallBilling", "NewBiller", "BillingTotals"}
	own := filepath.Join("internal", "usage", "billing_fork.go")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "node_modules" || (strings.HasPrefix(n, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == own {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range names {
			if strings.Contains(string(b), name) {
				t.Errorf("%s names %s; the usage biller has no production consumer yet", filepath.ToSlash(rel), name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
