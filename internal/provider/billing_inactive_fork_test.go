package provider

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/settings"
)

// Check references too, so taking a method value cannot evade the inactive gate.
func TestBillingResolverHasNoProductionConsumer(t *testing.T) {
	fset := token.NewFileSet()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "ResolveBilling" {
				t.Errorf("inactive resolver referenced in production at %s", fset.Position(sel.Pos()))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBillingMetadataDoesNotActivateRuntime(t *testing.T) {
	for _, c := range []struct {
		name      string
		countFree bool
		prices    map[string]settings.ModelPrice
	}{
		{"list", false, nil},
		{"countfree", true, nil},
		{"exact zero", false, map[string]settings.ModelPrice{"rv/plain": stated(0, 0, 0, 0)}},
		{"wildcard and paid override", false, map[string]settings.ModelPrice{"rv/*": stated(0, 0, 0, 0), "rv/plain": stated(2, 3, 0, 0)}},
		{"countfree and paid override", true, map[string]settings.ModelPrice{"rv/plain": stated(2, 3, 0, 0)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			prefsHome(t)
			p := Provider{ID: "rv", Name: "RV", Key: "sk-test", Chat: "http://127.0.0.1:9/v1", CountFree: c.countFree}
			if err := Save(p); err != nil {
				t.Fatal(err)
			}
			liveFree(t, "rv", `{"models":[{"id":"plain","cost":{"input":3,"output":15}},{"id":"vfree","free":true}]}`)
			pricesSaved(t, c.prices)
			s := settings.Load()
			type result struct {
				Free, FreeBy, Beside, Known bool
				Priced                      string
				Price                       catalog.Price
			}
			observe := func(p Provider, m catalog.Model) result {
				free, priced := FreeBy(s, p.ID, m)
				pr, ok := p.PriceAt(s, m.ID, time.Time{})
				return result{p.FreeModel(m.ID), free, p.PaidBesideFree(m.ID), ok, priced, pr}
			}
			models := []catalog.Model{{ID: "plain"}, {ID: "vfree", Free: true}, {ID: "missing"}}
			baseline := make([]result, len(models))
			for i, m := range models {
				baseline[i] = observe(p, m)
			}
			for _, mode := range []BillingMode{"", BillingList, BillingFree, BillingQuota} {
				for _, exhausted := range []QuotaExhaustion{"", QuotaStop, QuotaPaid} {
					p.Billing, p.QuotaExhausted = mode, exhausted
					if err := Save(p); err != nil {
						t.Fatal(err)
					}
					held, err := Find(p.ID)
					if err != nil {
						t.Fatal(err)
					}
					for i, m := range models {
						if got := observe(*held, m); !reflect.DeepEqual(got, baseline[i]) {
							t.Fatalf("%s %s/%s: got %+v, legacy %+v", m.ID, mode, exhausted, got, baseline[i])
						}
					}
				}
			}
		})
	}
}
