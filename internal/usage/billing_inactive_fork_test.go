package usage

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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

// The provider guard blocks outward consumers. This guard closes inward
// dependencies, including wrappers that would call live usage helpers.
func billingPureViolations(files map[string]*ast.File, fset *token.FileSet) []string {
	owned := map[string]bool{"billing_types_fork.go": true, "billing_fork.go": true, "billing_codec_fork.go": true}
	live := map[string]bool{}
	structNames := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if d, ok := decl.(*ast.GenDecl); ok {
				for _, sp := range d.Specs {
					if typ, ok := sp.(*ast.TypeSpec); ok {
						if _, ok := typ.Type.(*ast.StructType); ok {
							structNames[typ.Name.Name] = true
						}
					}
				}
			}
		}
	}
	for path, file := range files {
		if owned[filepath.Base(path)] {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					live[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					switch v := sp.(type) {
					case *ast.TypeSpec:
						live[v.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range v.Names {
							live[n.Name] = true
						}
					}
				}
			}
		}
	}
	APIs := map[string]string{
		"encoding/json": " Marshal Unmarshal RawMessage ",
		"math":          " IsNaN IsInf ", "math/big": " Rat ", "reflect": " TypeFor DeepEqual ",
		"slices": " Contains Sort SortFunc ", "strings": " Contains ContainsRune HasPrefix Compare ", "time": " Time ",
		"github.com/yetone/magpie/internal/catalog":    " Price Tier Peak PeakDeepSeek PeakWeekdays ",
		"github.com/yetone/magpie/internal/settings":   " StatedPrice ",
		"github.com/yetone/magpie/internal/provider":   " CloneQuotaSnapshot QuotaCoverage QuotaSnapshot QuotaWindowSnapshot QuotaSubject QuotaVersion QuotaState QuotaEvidence QuotaCapacity QuotaRecovery QuotaBlocker BillingDecision BillingPolicy BillingMode BillingSource QuotaExhaustion BillingList BillingFree BillingQuota QuotaStop QuotaPaid BillingSourceModel BillingSourceProvider BillingSourceVendor BillingSourceCatalogZero BillingSourceList BillingSourceQuota BillingSourceUnknown ",
		"github.com/yetone/magpie/internal/strictjson": " Validate Shape ",
	}
	methods := " After Before MarshalJSON IsZero Round UTC At OneHour CostSplit Same Price SetFloat64 Add Set Float64 Seal Merge billingAddTotal "
	var violations []string
	add := func(n ast.Node, why string) {
		violations = append(violations, fmt.Sprintf("%s: %s", fset.Position(n.Pos()), why))
	}
	noCallbacks := func(n ast.Node) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.FuncType, *ast.InterfaceType, *ast.ChanType:
				add(n, "callback/interface/channel boundary")
			}
			if id, ok := n.(*ast.Ident); ok && id.Name == "any" {
				add(id, "unbounded input")
			}
			return true
		})
	}
	for path, file := range files {
		if !owned[filepath.Base(path)] {
			continue
		}
		imports := map[string]string{}
		for _, i := range file.Imports {
			p, _ := strconv.Unquote(i.Path.Value)
			alias := filepath.Base(p)
			if i.Name != nil {
				alias = i.Name.Name
			}
			if _, ok := APIs[p]; !ok || alias == "." || alias == "_" {
				add(i, "unreviewed import")
			}
			imports[alias] = p
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name.Name == "init" {
					add(d, "init")
				}
				if d.Type.Params != nil {
					noCallbacks(d.Type.Params)
				}
				if d.Type.Results != nil {
					noCallbacks(d.Type.Results)
				}
			case *ast.GenDecl:
				if d.Tok == token.VAR {
					add(d, "package mutable state")
				}
				for _, sp := range d.Specs {
					if typ, ok := sp.(*ast.TypeSpec); ok {
						noCallbacks(typ.Type)
					}
				}
			}
		}
		selectorNames := map[*ast.Ident]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				selectorNames[sel.Sel] = true
			}
			if lit, ok := n.(*ast.CompositeLit); ok {
				if name, ok := lit.Type.(*ast.Ident); ok && structNames[name.Name] {
					for _, field := range lit.Elts {
						if kv, ok := field.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok {
								selectorNames[key] = true
							}
						}
					}
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.GoStmt, *ast.SendStmt:
				add(n, "concurrency/effect")
			case *ast.UnaryExpr:
				if v.Op == token.ARROW {
					add(v, "channel receive")
				}
			case *ast.Ident:
				if v.Obj == nil && !selectorNames[v] && live[v.Name] {
					add(v, "reference to live usage declaration "+v.Name)
				}
			case *ast.SelectorExpr:
				if id, ok := v.X.(*ast.Ident); ok {
					if p, ok := imports[id.Name]; ok && !strings.Contains(APIs[p], " "+v.Sel.Name+" ") {
						add(v, "unreviewed external API "+id.Name+"."+v.Sel.Name)
					}
				}
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
					isPackage := false
					if id, ok := sel.X.(*ast.Ident); ok {
						_, isPackage = imports[id.Name]
					}
					if !isPackage && !strings.Contains(methods, " "+sel.Sel.Name+" ") {
						add(v, "unreviewed method call "+sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
	return violations
}

func TestDormantBillingPureClosure(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = file
	}
	if violations := billingPureViolations(files, fset); len(violations) > 0 {
		t.Fatal(strings.Join(violations, "\n"))
	}
}
func TestDormantBillingPureClosureFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		bad       bool
	}{
		{"pure", "package usage; func held(x int) int { return x+1 }", false},
		{"struct-field", "package usage; type Input struct{Known int}; func held(){ _ = Input{Known:1} }", false},
		{"map-key-global", "package usage; func held(){ _ = map[int]int{Known:1} }", true},
		{"blank-import-only", `package usage; import _ "net/http/pprof"`, true},
		{"blank-reviewed-import", `package usage; import _ "time"`, true},
		{"pointer-callback", `package usage; func held(f *func()) { (*f)() }`, true},
		{"nested-callback-field", `package usage; type Input struct { F []*func() }; func held(x Input){ (*x.F[0])() }`, true},
		{"clock", `package usage; import "time"; func held() { _ = time.Now() }`, true},
		{"clock-alias", `package usage; import "time"; func held() { f := time.Now; _ = f() }`, true},
		{"current-state-wrapper", `package usage; func held(){ wrapper() }; func wrapper(){ live() }`, true},
		{"current-state-alias", `package usage; func held(){ f := live; f() }`, true},
		{"callback", `package usage; func held(f func()) { f() }`, true},
		{"callback-field", `package usage; type Input struct { F func() }; func held(x Input){ x.F() }`, true},
		{"interface-field", `package usage; type Input struct { F any }`, true},
		{"network", `package usage; import "net/http"; func held(){ http.Get("http://example.invalid") }`, true},
		{"reflect-call", `package usage; import "reflect"; func held(){ reflect.ValueOf(1).Call(nil) }`, true},
		{"init", `package usage; func init(){}`, true},
		{"mutable-state", `package usage; var current = 1`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "billing_fork.go", tc.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			live, err := parser.ParseFile(fset, "live.go", "package usage; func live(){}; var Known = 1", 0)
			if err != nil {
				t.Fatal(err)
			}
			got := billingPureViolations(map[string]*ast.File{"billing_fork.go": f, "live.go": live}, fset)
			if (len(got) > 0) != tc.bad {
				t.Fatalf("violations=%v want bad=%v", got, tc.bad)
			}
		})
	}
}
