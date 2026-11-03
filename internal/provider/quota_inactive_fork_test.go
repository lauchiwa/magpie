package provider

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func quotaInactiveLibrary(path string) bool {
	switch filepath.ToSlash(path) {
	case "internal/provider/quota_types_fork.go", "internal/provider/quota_fork.go",
		"internal/provider/quota_store_fork.go", "internal/provider/quota_evidence_fork.go":
		return true
	}
	return false
}

// Discover declarations instead of a hand-maintained symbol list. Prohibiting
// even type and function-value references closes aliases and method-value paths
// without depending on type-checking every platform's production files together.
func quotaInactiveViolations(files map[string]*ast.File, fset *token.FileSet) []string {
	symbols := make(map[string]bool)
	for path, file := range files {
		if !quotaInactiveLibrary(path) {
			continue
		}
		for _, declaration := range file.Decls {
			switch d := declaration.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name != "init" {
					symbols[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						symbols[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if name.Name != "_" {
								symbols[name.Name] = true
							}
						}
					}
				}
			}
		}
	}
	var violations []string
	add := func(node ast.Node, reason string) {
		violations = append(violations, fmt.Sprintf("%s: %s", fset.Position(node.Pos()), reason))
	}
	for path, file := range files {
		if quotaInactiveLibrary(path) {
			for _, declaration := range file.Decls {
				switch d := declaration.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.Name == "init" {
						add(d, "inactive library has init function")
					}
				case *ast.GenDecl:
					if d.Tok != token.VAR {
						continue
					}
					for _, spec := range d.Specs {
						for _, value := range spec.(*ast.ValueSpec).Values {
							if !quotaInactiveInitializer(value) {
								add(value, "inactive library has effectful package initializer")
							}
						}
					}
				}
			}
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.Ident:
				if symbols[n.Name] && (ast.IsExported(n.Name) || filepath.ToSlash(filepath.Dir(path)) == "internal/provider") {
					add(n, "inactive quota symbol referenced: "+n.Name)
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					value, err := strconv.Unquote(n.Value)
					if err == nil && strings.Contains(value, "quota-state.json") {
						add(n, "inactive quota state filename referenced")
					}
				}
			}
			return true
		})
	}
	sort.Strings(violations)
	return violations
}

// Only expressions that cannot execute work at package initialization pass.
// A function literal is inert until called; its invocation is not permitted.
func quotaInactiveInitializer(expression ast.Expr) bool {
	switch e := expression.(type) {
	case *ast.BasicLit, *ast.Ident, *ast.FuncLit:
		return true
	case *ast.CompositeLit:
		for _, element := range e.Elts {
			if !quotaInactiveInitializer(element) {
				return false
			}
		}
		return true
	case *ast.KeyValueExpr:
		return quotaInactiveInitializer(e.Key) && quotaInactiveInitializer(e.Value)
	case *ast.ParenExpr:
		return quotaInactiveInitializer(e.X)
	case *ast.UnaryExpr:
		return e.Op != token.ARROW && quotaInactiveInitializer(e.X)
	case *ast.BinaryExpr:
		return quotaInactiveInitializer(e.X) && quotaInactiveInitializer(e.Y)
	case *ast.SelectorExpr:
		return quotaInactiveInitializer(e.X)
	case *ast.IndexExpr:
		return quotaInactiveInitializer(e.X) && quotaInactiveInitializer(e.Index)
	}
	return false
}

func TestQuotaInactiveNoProductionConsumers(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = file
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"internal/provider/quota_types_fork.go", "internal/provider/quota_fork.go", "internal/provider/quota_store_fork.go", "internal/provider/quota_evidence_fork.go"} {
		if files[path] == nil {
			t.Fatalf("guard did not inspect owned library %s", path)
		}
	}
	for _, violation := range quotaInactiveViolations(files, fset) {
		t.Error(violation)
	}
}

func quotaInactiveFixture(t *testing.T, path, extra string) []string {
	t.Helper()
	sources := map[string]string{
		"internal/provider/quota_types_fork.go": `package provider
 type QuotaObservation struct{}
 type QuotaState string
 const QuotaStateAvailable QuotaState = "available"
 type quotaRecord struct{}
 `,
		"internal/provider/quota_fork.go": `package provider
 func reduceQuota(){}
 func quotaView(){}
 `,
		"internal/provider/quota_store_fork.go": `package provider
 import "sync"
 type QuotaStore struct{}
 var quotaLocks sync.Map
 var quotaEmpty = map[string]int{}
 func NewQuotaStore() *QuotaStore {return nil}
 func (*QuotaStore) Read(){}
 func (*QuotaStore) Apply(){}
 func (*QuotaStore) Initialize(){}
 `,
		"internal/provider/quota_evidence_fork.go": `package provider
 func ValidateQuotaObservation(){}
 func validateQuotaRecord(){}
 `,
		"internal/provider/provider.go": "package provider\n",
		"internal/gateway/gateway.go":   "package gateway\n",
	}
	sources[path] += extra
	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for name, source := range sources {
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			t.Fatalf("invalid synthetic source %s: %v", name, err)
		}
		files[name] = file
	}
	return quotaInactiveViolations(files, fset)
}

func TestQuotaInactiveGuardDetectsSyntheticMutations(t *testing.T) {
	cases := []struct{ name, path, source, reason string }{
		{"bare reducer call", "internal/provider/provider.go", "func live(){reduceQuota()}", "inactive quota symbol"},
		{"bare reducer value", "internal/provider/provider.go", "var hook = reduceQuota", "inactive quota symbol"},
		{"bare validator value", "internal/provider/provider.go", "var hook = ValidateQuotaObservation", "inactive quota symbol"},
		{"bare private validator", "internal/provider/provider.go", "func live(){validateQuotaRecord()}", "inactive quota symbol"},
		{"bare constructor", "internal/provider/provider.go", "func live(){_ = NewQuotaStore()}", "inactive quota symbol"},
		{"bare state", "internal/provider/provider.go", "var state = QuotaStateAvailable", "inactive quota symbol"},
		{"bare type alias", "internal/provider/provider.go", "type live = quotaRecord", "inactive quota symbol"},
		{"external selector", "internal/gateway/gateway.go", "import p \"github.com/yetone/magpie/internal/provider\"\nvar hook = p.NewQuotaStore", "inactive quota symbol"},
		{"external validator", "internal/gateway/gateway.go", "import p \"github.com/yetone/magpie/internal/provider\"\nvar hook = p.ValidateQuotaObservation", "inactive quota symbol"},
		{"method expression", "internal/provider/provider.go", "var hook = (*QuotaStore).Apply", "inactive quota symbol"},
		{"method value", "internal/provider/provider.go", "var store *QuotaStore\nvar hook = store.Read", "inactive quota symbol"},
		{"external method value", "internal/gateway/gateway.go", "import p \"github.com/yetone/magpie/internal/provider\"\nfunc live(s *p.QuotaStore){_ = s.Initialize}", "inactive quota symbol"},
		{"global library value", "internal/provider/provider.go", "var hook = quotaLocks.Load", "inactive quota symbol"},
		{"direct state file", "internal/provider/provider.go", "var file = \"quota-state.json\"", "inactive quota state filename"},
		{"backup path", "internal/gateway/gateway.go", "var file = `/private/quota-state.json`", "inactive quota state filename"},
		{"escaped state file", "internal/gateway/gateway.go", "var file = \"quota\\x2dstate.json\"", "inactive quota state filename"},
	}
	for _, path := range []string{"internal/provider/quota_types_fork.go", "internal/provider/quota_fork.go", "internal/provider/quota_store_fork.go", "internal/provider/quota_evidence_fork.go"} {
		cases = append(cases,
			struct{ name, path, source, reason string }{"init/" + path, path, "func init(){}", "init function"},
			struct{ name, path, source, reason string }{"initializer/" + path, path, "var live = NewQuotaStore()", "effectful package initializer"},
			struct{ name, path, source, reason string }{"nested initializer/" + path, path, "var live = []any{NewQuotaStore()}", "effectful package initializer"},
			struct{ name, path, source, reason string }{"closure invocation/" + path, path, "var live = func() int { ValidateQuotaObservation(); return 1 }()", "effectful package initializer"},
			struct{ name, path, source, reason string }{"channel receive/" + path, path, "var live = <-updates", "effectful package initializer"},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			violations := quotaInactiveFixture(t, c.path, c.source)
			if !strings.Contains(strings.Join(violations, "\n"), c.reason) {
				t.Fatalf("guard missed synthetic mutation %q; violations=%v", c.source, violations)
			}
		})
	}
}

func TestQuotaInactiveGuardAllowsDormantDefinitions(t *testing.T) {
	for _, source := range []string{
		"", "var uninitialized *QuotaStore", "var zero = QuotaStore{}", "var zero = &QuotaStore{}",
		"var nilMap map[string]*QuotaStore", "var empty = map[string]*QuotaStore{}",
		"func explicitOnly(){_ = NewQuotaStore()}", "var deferred = func(){_ = NewQuotaStore()}",
	} {
		if violations := quotaInactiveFixture(t, "internal/provider/quota_store_fork.go", source); len(violations) != 0 {
			t.Fatalf("inert library %q rejected: %v", source, violations)
		}
	}
	if violations := quotaInactiveFixture(t, "internal/provider/provider.go", "// NewQuotaStore quota-state.json\nfunc live(){_ = 1}"); len(violations) != 0 {
		t.Fatalf("comment counted as activation: %v", violations)
	}
}
