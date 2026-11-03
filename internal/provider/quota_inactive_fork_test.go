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

var quotaInactiveFiles = []string{
	"internal/provider/quota_types_fork.go", "internal/provider/quota_fork.go",
	"internal/provider/quota_store_fork.go", "internal/provider/quota_evidence_fork.go",
	"internal/provider/quota_snapshot_fork.go",
	"internal/usage/billing_types_fork.go", "internal/usage/billing_fork.go",
	"internal/usage/billing_codec_fork.go",
}

func quotaInactiveLibrary(path string) bool {
	for _, owned := range quotaInactiveFiles {
		if filepath.ToSlash(path) == owned {
			return true
		}
	}
	return false
}

// Discover declarations with their declaring package, including private usage
// helpers. Blocking values and types closes aliases and method-value consumers.
// Only methods on types owned by this closed component may be declared inside
// it; a method on a live receiver would otherwise be a hidden outward bridge.
func quotaInactiveViolations(files map[string]*ast.File, fset *token.FileSet) []string {
	symbols := make(map[string]map[string]string)
	types := make(map[string]map[string]*ast.TypeSpec)
	for path, file := range files {
		if !quotaInactiveLibrary(path) {
			continue
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		if symbols[pkg] == nil {
			symbols[pkg] = make(map[string]string)
			types[pkg] = make(map[string]*ast.TypeSpec)
		}
		for _, declaration := range file.Decls {
			switch d := declaration.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name != "init" {
					symbols[pkg][d.Name.Name] = path
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						symbols[pkg][s.Name.Name] = path
						types[pkg][s.Name.Name] = s
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if name.Name != "_" {
								symbols[pkg][name.Name] = path
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
		pkg := filepath.ToSlash(filepath.Dir(path))
		imports := quotaInactiveImports(file)
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			if spec.Name != nil && spec.Name.Name == "." && symbols[strings.TrimPrefix(imported, "github.com/yetone/magpie/")] != nil {
				add(spec, "inactive quota symbol package dot-imported")
			}
		}
		if quotaInactiveLibrary(path) {
			for _, declaration := range file.Decls {
				switch d := declaration.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.Name == "init" {
						add(d, "inactive library has init function")
					}
					if d.Recv != nil && !quotaInactiveReceiver(d.Recv.List[0].Type, types[pkg], make(map[string]bool)) {
						add(d, "inactive library method bridges live receiver")
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
			if pkg == "internal/usage" {
				// Billing may hold quota data and use its checked copy, never
				// open the store or invoke reducer/collector machinery.
				ast.Inspect(file, func(node ast.Node) bool {
					if n, ok := node.(*ast.SelectorExpr); ok {
						if x, ok := n.X.(*ast.Ident); ok && imports[x.Name] == "github.com/yetone/magpie/internal/provider" {
							owner := symbols["internal/provider"][n.Sel.Name]
							if owner != "" && owner != "internal/provider/quota_types_fork.go" && n.Sel.Name != "CloneQuotaSnapshot" {
								add(n, "billing references non-snapshot quota machinery: "+n.Sel.Name)
							}
						}
					}
					return true
				})
			}
			continue
		}
		selected := make(map[*ast.Ident]bool)
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.Ident:
				if !selected[n] && symbols[pkg][n.Name] != "" {
					add(n, "inactive quota symbol referenced: "+n.Name)
				}
				if imported := imports["."]; imported != "" {
					owner := strings.TrimPrefix(imported, "github.com/yetone/magpie/")
					if ast.IsExported(n.Name) && symbols[owner][n.Name] != "" {
						add(n, "inactive quota symbol referenced: "+n.Name)
					}
				}
			case *ast.SelectorExpr:
				selected[n.Sel] = true
				if x, ok := n.X.(*ast.Ident); ok && x.Obj == nil {
					owner := strings.TrimPrefix(imports[x.Name], "github.com/yetone/magpie/")
					if symbols[owner][n.Sel.Name] != "" {
						add(n, "inactive quota symbol referenced: "+n.Sel.Name)
					}
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
	violations = append(violations, quotaPureViolations(files, fset)...)
	sort.Strings(violations)
	return violations
}

func quotaInactiveImports(file *ast.File) map[string]string {
	imports := make(map[string]string)
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = path
	}
	return imports
}

func quotaInactiveReceiver(expr ast.Expr, types map[string]*ast.TypeSpec, seen map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return quotaInactiveReceiver(e.X, types, seen)
	case *ast.ParenExpr:
		return quotaInactiveReceiver(e.X, types, seen)
	case *ast.IndexExpr:
		return quotaInactiveReceiver(e.X, types, seen)
	case *ast.IndexListExpr:
		return quotaInactiveReceiver(e.X, types, seen)
	case *ast.Ident:
		typ := types[e.Name]
		if typ == nil || seen[e.Name] {
			return false
		}
		if !typ.Assign.IsValid() {
			return true
		}
		seen[e.Name] = true
		return quotaInactiveReceiver(typ.Type, types, seen)
	}
	return false
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
	for _, path := range quotaInactiveFiles {
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
 type QuotaSnapshot struct{}
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
		"internal/provider/quota_snapshot_fork.go": `package provider
 func CloneQuotaSnapshot(){}
 func quotaSnapshotPrivate(){}
 `,
		"internal/usage/billing_types_fork.go": `package usage
 type BillingContext struct{}
 type BillingQuota struct{}
 type billingPrivate struct{}
 `,
		"internal/usage/billing_fork.go": `package usage
 func CaptureBilling(){}
 func billingCopy(){}
 func (BillingContext) Seal(){}
 `,
		"internal/usage/billing_codec_fork.go":   "package usage\n",
		"internal/usage/usage.go":                "package usage\ntype Record struct{}\n",
		"internal/strictjson/strictjson_fork.go": "package strictjson\n",
		"internal/provider/provider.go":          "package provider\nconst BillingQuota = 0\n",
		"internal/gateway/gateway.go":            "package gateway\n",
	}
	if strings.HasPrefix(extra, "import ") {
		first, rest, _ := strings.Cut(extra, "\n")
		sources[path] = strings.Replace(sources[path], "\n", "\n"+first+"\n", 1) + rest
	} else {
		sources[path] += extra
	}
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
	cases = append(cases,
		struct{ name, path, source, reason string }{"private usage call", "internal/usage/usage.go", "func live(){billingCopy()}", "inactive quota symbol"},
		struct{ name, path, source, reason string }{"private usage alias", "internal/usage/usage.go", "type alias = billingPrivate", "inactive quota symbol"},
		struct{ name, path, source, reason string }{"usage method value", "internal/usage/usage.go", "func live(){var c BillingContext; _ = c.Seal}", "inactive quota symbol"},
		struct{ name, path, source, reason string }{"usage local alias", "internal/usage/usage.go", "func live(){hook := billingCopy; hook()}", "inactive quota symbol"},
		struct{ name, path, source, reason string }{"live receiver", "internal/usage/billing_fork.go", "func (*Record) Charge(){CaptureBilling()}", "bridges live receiver"},
		struct{ name, path, source, reason string }{"aliased live receiver", "internal/usage/billing_fork.go", "type bridge = Record; func (*bridge) Charge(){CaptureBilling()}", "bridges live receiver"},
		struct{ name, path, source, reason string }{"quota from usage", "internal/usage/billing_fork.go", "import p \"github.com/yetone/magpie/internal/provider\"\nfunc live(){_ = p.NewQuotaStore()}", "non-snapshot quota machinery"},
		struct{ name, path, source, reason string }{"quota validator from usage", "internal/usage/billing_codec_fork.go", "import p \"github.com/yetone/magpie/internal/provider\"\nvar check = p.ValidateQuotaObservation", "non-snapshot quota machinery"},
		struct{ name, path, source, reason string }{"bridge through generic helper", "internal/strictjson/strictjson_fork.go", "import u \"github.com/yetone/magpie/internal/usage\"\nfunc Validate(){u.CaptureBilling()}", "inactive quota symbol"},
		struct{ name, path, source, reason string }{"private snapshot helper", "internal/provider/provider.go", "func live(){quotaSnapshotPrivate()}", "inactive quota symbol"},
	)
	for _, path := range quotaInactiveFiles {
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

func TestQuotaInactiveGuardAllowsOnlyNamedBillingBridge(t *testing.T) {
	for _, path := range []string{"internal/usage/billing_fork.go", "internal/usage/billing_codec_fork.go"} {
		source := "import p \"github.com/yetone/magpie/internal/provider\"\nfunc explicit(){ var s p.QuotaSnapshot; _ = s; p.CloneQuotaSnapshot(); billingCopy() }"
		if violations := quotaInactiveFixture(t, path, source); len(violations) != 0 {
			t.Fatalf("pure bridge rejected: %v", violations)
		}
	}
	if quotaInactiveLibrary("internal/usage/usage.go") || quotaInactiveLibrary("internal/usage/billing_extra_fork.go") || quotaInactiveLibrary("internal/strictjson/strictjson_fork.go") {
		t.Fatal("dormant domain allowlist widened to a package or generic helper")
	}
	// A same-spelled private helper in a different package is not a consumer.
	if violations := quotaInactiveFixture(t, "internal/provider/provider.go", "func billingCopy(){}"); len(violations) != 0 {
		t.Fatalf("private usage ownership confused: %v", violations)
	}
	if violations := quotaInactiveFixture(t, "internal/provider/provider.go", "const BillingQuota = 0"); len(violations) != 0 {
		t.Fatalf("exported spelling confused with other package: %v", violations)
	}
	if violations := quotaInactiveFixture(t, "internal/usage/usage.go", "import p \"github.com/yetone/magpie/internal/provider\"\nvar legacy = p.BillingQuota"); len(violations) != 0 {
		t.Fatalf("selector confused with own declaration: %v", violations)
	}

}

// Close the pure API over same-package declarations, not just direct selectors
// in the exported function. A wrapper or function-valued alias cannot launder
// current state, store access, a clock, or an I/O call into snapshot capture.
// Standard methods are a narrow explicit-input set; callback-bearing local
// data types and mutable package state are not inputs to this component.
func quotaPureViolations(files map[string]*ast.File, fset *token.FileSet) []string {
	type declaration struct {
		path    string
		node    ast.Node
		mutable bool
	}
	declarations := make(map[string]map[string]declaration)
	methods := make(map[string][]declaration)
	var roots []declaration
	for path, file := range files {
		pkg := filepath.ToSlash(filepath.Dir(path))
		if pkg != "internal/provider" && pkg != "internal/strictjson" {
			continue
		}
		if declarations[pkg] == nil {
			declarations[pkg] = make(map[string]declaration)
		}
		for _, raw := range file.Decls {
			switch d := raw.(type) {
			case *ast.FuncDecl:
				entry := declaration{path: path, node: d}
				if d.Recv == nil {
					declarations[pkg][d.Name.Name] = entry
				} else {
					receiver := d.Recv.List[0].Type
					if p, ok := receiver.(*ast.StarExpr); ok {
						receiver = p.X
					}
					if id, ok := receiver.(*ast.Ident); ok {
						methods[pkg+"/"+id.Name] = append(methods[pkg+"/"+id.Name], entry)
					}
				}
				if pkg == "internal/strictjson" || path == "internal/provider/quota_types_fork.go" || d.Name.Name == "CloneQuotaSnapshot" {
					roots = append(roots, entry)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					entry := declaration{path: path, node: spec, mutable: d.Tok == token.VAR}
					switch v := spec.(type) {
					case *ast.TypeSpec:
						declarations[pkg][v.Name.Name] = entry
					case *ast.ValueSpec:
						for _, name := range v.Names {
							declarations[pkg][name.Name] = entry
						}
					}
					if pkg == "internal/strictjson" {
						roots = append(roots, entry)
					}
				}
			}
		}
	}
	permitted := map[string]string{
		"bytes":         " Equal TrimSpace NewReader ",
		"encoding/json": " Decoder Delim NewDecoder RawMessage Unmarshal ",
		"fmt":           " Errorf ", "io": " EOF ",
		"math":    " IsNaN IsInf ",
		"reflect": " Type TypeFor Pointer Struct Slice ",
		"slices":  " Clone Contains ", "strings": " Cut ",
		"time": " Time ", "unicode": " IsSpace IsControl ",
		"unicode/utf8": " Valid ValidString ",
	}
	pureMethods := " After Before Equal IsZero MarshalJSON Kind Elem NumField Field Get Token More UseNumber remember rememberID "
	var violations []string
	add := func(n ast.Node, reason string) {
		violations = append(violations, fmt.Sprintf("%s: impure snapshot/JSON closure: %s", fset.Position(n.Pos()), reason))
	}
	// Imports execute even in a file with no declarations to visit.
	for path, file := range files {
		generic := filepath.ToSlash(filepath.Dir(path)) == "internal/strictjson"
		if !generic && !quotaInactiveLibrary(path) {
			continue
		}
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			if spec.Name != nil && (spec.Name.Name == "." || spec.Name.Name == "_") || generic && permitted[imported] == "" {
				add(spec, "unreviewed import "+imported)
			}
		}
	}
	checkInput := func(expr ast.Expr) {
		ast.Inspect(expr, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncType:
				add(x, "callback input or type")
				return false
			case *ast.Ident:
				if x.Name == "any" {
					add(x, "unbounded input")
				}
			}
			return true
		})
	}
	seen := make(map[ast.Node]bool)
	var visit func(declaration)
	visit = func(d declaration) {
		if seen[d.node] {
			return
		}
		seen[d.node] = true
		pkg := filepath.ToSlash(filepath.Dir(d.path))
		if pkg == "internal/provider" && d.path != "internal/provider/quota_types_fork.go" &&
			d.path != "internal/provider/quota_fork.go" && d.path != "internal/provider/quota_evidence_fork.go" &&
			d.path != "internal/provider/quota_snapshot_fork.go" {
			add(d.node, "reference escapes pure provider files")
			return
		}
		if d.mutable {
			add(d.node, "mutable package state")
		}
		if fn, ok := d.node.(*ast.FuncDecl); ok && fn.Name.Name == "init" {
			add(fn, "package initialization")
		}
		imports := quotaInactiveImports(files[d.path])
		ignored := make(map[*ast.Ident]bool)
		ast.Inspect(d.node, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				ignored[x.Sel] = true
			case *ast.Field:
				for _, name := range x.Names {
					ignored[name] = true
				}
			case *ast.CompositeLit:
				if id, ok := x.Type.(*ast.Ident); ok {
					if typ, ok := declarations[pkg][id.Name].node.(*ast.TypeSpec); ok {
						if _, ok := typ.Type.(*ast.StructType); ok {
							for _, element := range x.Elts {
								if field, ok := element.(*ast.KeyValueExpr); ok {
									if key, ok := field.Key.(*ast.Ident); ok {
										ignored[key] = true
									}
								}
							}
						}
					}
				}
			}
			return true
		})
		ast.Inspect(d.node, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if ignored[x] {
					return true
				}
				if target, ok := declarations[pkg][x.Name]; ok {
					// A local variable is not a same-spelled package declaration.
					if x.Obj == nil || x.Obj.Decl == target.node {
						visit(target)
					}
				}
				for _, method := range methods[pkg+"/"+x.Name] {
					visit(method)
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Obj == nil && imports[id.Name] != "" {
					if !strings.Contains(permitted[imports[id.Name]], " "+x.Sel.Name+" ") {
						add(x, "external symbol "+imports[id.Name]+"."+x.Sel.Name)
					}
				}
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
					id, imported := sel.X.(*ast.Ident)
					imported = imported && id.Obj == nil && imports[id.Name] != ""
					if !imported && !strings.Contains(pureMethods, " "+sel.Sel.Name+" ") {
						add(x, "unreviewed method "+sel.Sel.Name)
					}
				}
			case *ast.Field:
				checkInput(x.Type)
			case *ast.TypeSpec:
				checkInput(x.Type)
			case *ast.InterfaceType, *ast.ChanType, *ast.GoStmt, *ast.SendStmt, *ast.SelectStmt:
				add(n, "effectful or unbounded input")
			case *ast.UnaryExpr:
				if x.Op == token.ARROW {
					add(x, "channel receive")
				}
			}
			return true
		})
	}
	for _, root := range roots {
		visit(root)
	}
	return violations
}

func TestQuotaInactivePureClosureGuard(t *testing.T) {
	cases := []struct{ name, path, extra string }{
		{"clock", "internal/provider/quota_snapshot_fork.go", "import \"time\"\nfunc CloneQuotaSnapshot(){_ = time.Now()}"},
		{"clock alias", "internal/provider/quota_snapshot_fork.go", "import \"time\"\nfunc CloneQuotaSnapshot(){clock := time.Now; _ = clock()}"},
		{"allowed-file wrapper", "internal/provider/quota_evidence_fork.go", "import \"os\"\nfunc wrapped(){_ = os.Getenv(\"HOME\")}"},
		{"live-file wrapper", "internal/provider/provider.go", "func wrapped(){}"},
		{"store wrapper", "internal/provider/quota_store_fork.go", "func wrapped(){_ = NewQuotaStore()}"},
		{"global alias", "internal/provider/quota_evidence_fork.go", "var wrapped = func(){}"},
		{"callback input", "internal/provider/quota_snapshot_fork.go", "func CloneQuotaSnapshot(callback func()){callback()}"},
		{"pointer callback input", "internal/provider/quota_snapshot_fork.go", "func CloneQuotaSnapshot(callback *func()){(*callback)()}"},
		{"slice callback input", "internal/provider/quota_snapshot_fork.go", "func CloneQuotaSnapshot(callback []func()){callback[0]()}"},
		{"map callback input", "internal/provider/quota_snapshot_fork.go", "func CloneQuotaSnapshot(callback map[string]func()){callback[\"call\"]()}"},
		{"pointer callback alias", "internal/provider/quota_snapshot_fork.go", "type callback = *func(); func CloneQuotaSnapshot(call callback){(*call)()}"},
		{"wrapped unbounded input", "internal/provider/quota_snapshot_fork.go", "func CloneQuotaSnapshot(input []any){}"},
		{"import-only JSON side effect", "internal/strictjson/strictjson_fork.go", "import _ \"net/http/pprof\""},
		{"store side-effect import", "internal/provider/quota_store_fork.go", "import _ \"net/http/pprof\""},
		{"JSON clock", "internal/strictjson/strictjson_fork.go", "import \"time\"\nfunc Validate(){_ = time.Now()}"},
		{"JSON state", "internal/strictjson/strictjson_fork.go", "var cache int"},
		{"JSON init", "internal/strictjson/strictjson_fork.go", "func init(){}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sources := map[string]string{
				"internal/provider/quota_snapshot_fork.go": "package provider\nfunc CloneQuotaSnapshot(){wrapped()}",
				"internal/provider/quota_evidence_fork.go": "package provider\n",
				"internal/provider/provider.go":            "package provider\n",
				"internal/provider/quota_store_fork.go":    "package provider\nfunc NewQuotaStore()int{return 0}\n",
				"internal/strictjson/strictjson_fork.go":   "package strictjson\n",
			}
			if c.path == "internal/provider/quota_snapshot_fork.go" {
				sources[c.path] = "package provider\n"
			}
			if strings.HasPrefix(c.extra, "import ") {
				first, rest, _ := strings.Cut(c.extra, "\n")
				sources[c.path] = strings.Replace(sources[c.path], "\n", "\n"+first+"\n", 1) + rest
			} else {
				sources[c.path] += c.extra
			}
			fset := token.NewFileSet()
			files := make(map[string]*ast.File)
			for path, source := range sources {
				file, err := parser.ParseFile(fset, path, source, 0)
				if err != nil {
					t.Fatal(err)
				}
				files[path] = file
			}
			if got := quotaPureViolations(files, fset); len(got) == 0 {
				t.Fatal("guard missed impure closure")
			}
		})
	}
}

func TestQuotaInactivePureMapKey(t *testing.T) {
	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for path, source := range map[string]string{
		"internal/provider/quota_snapshot_fork.go": "package provider; func CloneQuotaSnapshot(){ _ = map[string]int{current: 1} }",
		"internal/provider/quota_evidence_fork.go": "package provider; var current string",
	} {
		file, err := parser.ParseFile(fset, path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = file
	}
	violations := quotaPureViolations(files, fset)
	if !strings.Contains(strings.Join(violations, "\n"), "mutable package state") {
		t.Fatalf("map key bypassed pure closure: %v", violations)
	}
}
