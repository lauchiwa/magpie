package provider

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
)

// identityReads are the identity package's reads of identities.json. A
// capture that called one beside its snapshot would read the file twice
// and could mix two generations (design C.2).
var identityReads = map[string]bool{"Profiles": true, "Bindings": true, "Resolve": true, "HeaderFor": true, "ReadSnapshot": true}

// The signing and view captures read the identity store only through
// readSnapshot, once; the legacy branch's identityHeaders (one atomic read
// of the one owner's file) is the only other read, in headersFor alone.
func TestLifecycleCapturesReadIdentityOnlyThroughTheSnapshot(t *testing.T) {
	scan := func(path string, funcs map[string]bool) map[string][]string {
		t.Helper()
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string][]string{}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || funcs != nil && !funcs[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch x := c.Fun.(type) {
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok && id.Name == "identity" && identityReads[x.Sel.Name] {
						found[fn.Name.Name] = append(found[fn.Name.Name], "identity."+x.Sel.Name)
					}
				case *ast.Ident:
					if x.Name == "identityHeaders" || x.Name == "readSnapshot" {
						found[fn.Name.Name] = append(found[fn.Name.Name], x.Name)
					}
				}
				return true
			})
		}
		return found
	}

	got := scan("lifecycle_fork.go", nil)
	want := map[string][]string{
		"headersFor":      {"readSnapshot", "identityHeaders"}, // transient's store check; legacy's one read
		"capturedHeaders": {"readSnapshot"},
		"captureView":     {"readSnapshot", "readSnapshot"}, // one per branch
		"lockedView":      {"readSnapshot", "readSnapshot"}, // one per branch
	}
	for fn, calls := range got {
		w := want[fn]
		if len(calls) != len(w) {
			t.Errorf("%s reads the identity store through %v, want %v", fn, calls, w)
			continue
		}
		for i := range calls {
			if calls[i] != w[i] {
				t.Errorf("%s reads the identity store through %v, want %v", fn, calls, w)
				break
			}
		}
	}
	for fn := range want {
		if _, ok := got[fn]; !ok {
			t.Errorf("%s no longer reads the identity store; update this guard", fn)
		}
	}

	// the GUI and CLI views take profiles and bindings from the view alone
	for path, fn := range map[string]string{"../gui/identity.go": "identityState", "../cli/identity_cli.go": "identityList"} {
		if calls := scan(path, map[string]bool{fn: true})[fn]; len(calls) != 0 {
			t.Errorf("%s (%s) reads the identity store beside its view: %v", fn, path, calls)
		}
	}
}

// Without lifecycle.json a combined view can't validate a commit made
// between its two reads, so it reads both under lifecycle's lock.
func TestALegacyCombinedViewReadsUnderTheLock(t *testing.T) {
	h := newLifeHome(t)
	h.write([]Provider{h.row("relay")}, nil)
	unlock, err := lifecycle.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var released, early atomic.Bool
	was := readSnapshot
	t.Cleanup(func() { readSnapshot = was })
	readSnapshot = func() (identity.Snapshot, error) {
		if !released.Load() {
			early.Store(true)
		}
		return was()
	}
	done := make(chan error, 1)
	go func() { _, err := ReadIdentityView(true); done <- err }()
	time.Sleep(500 * time.Millisecond)
	released.Store(true)
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if early.Load() {
		t.Fatal("a legacy combined view read the identity store while another held lifecycle's lock")
	}
}

// The view follows design C.3 for unusable lifecycle state: unsupported
// fails closed for both views, corrupt reads as legacy.
func TestAnIdentityViewUnderUnusableLifecycleState(t *testing.T) {
	h := newLifeHome(t)
	h.write([]Provider{h.row("relay")}, map[string]string{"relay": newInstance(t)})
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lifecycle.Path(), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, with := range []bool{true, false} {
		if _, err := ReadIdentityView(with); !errors.Is(err, lifecycle.ErrUnknown) {
			t.Errorf("view(withProviders=%v) under unsupported lifecycle.json: %v, want ErrUnknown", with, err)
		}
	}
	if err := os.WriteFile(lifecycle.Path(), []byte(`{"version":1,`), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := ReadIdentityView(true)
	if err != nil {
		t.Fatalf("view under corrupt lifecycle.json: %v, want legacy", err)
	}
	if b := v.Snapshot.Bindings(); b["relay"] != h.a.ID {
		t.Fatalf("view under corrupt lifecycle.json bindings %v", b)
	}
	ps := v.Providers()
	if len(ps) != 1 || ps[0].life != (life{}) {
		t.Fatalf("view under corrupt lifecycle.json providers %+v, want legacy", ps)
	}
}
