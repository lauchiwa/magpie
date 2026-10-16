package provider

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/lifecycle"
)

type commitReadViolation string

// watchCommitReads detects the caller's locked stack, not all readers in
// the process. Stop before the forbidden read can try to recover its own
// journal and deadlock. Only this test hook panics; production never does.
func watchCommitReads(t *testing.T) (assertClean func()) {
	t.Helper()
	var mu sync.Mutex
	var violations []string
	name := func(f any) string { return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name() }
	// the locked sections, and the one function in each allowed its own
	// direct read of providers.json
	held := map[string]bool{name(commitHeld): true, name(readyHeld): true, name(reconcileHeld): true}
	direct := map[string]bool{name(commitLocked): true, name(readyLocked): true, name(reconcileLocked): true}
	readName := runtime.FuncForPC(reflect.ValueOf(read).Pointer()).Name()
	hook := func(name string) {
		// Grow until the entire stack fits: helpers do not evade the guard
		// merely by adding depth. CallersFrames also expands inline frames.
		pcs := make([]uintptr, 32)
		for runtime.Callers(2, pcs) == len(pcs) {
			pcs = make([]uintptr, len(pcs)*2)
		}
		n := runtime.Callers(2, pcs)
		frames := runtime.CallersFrames(pcs[:n])
		locked, afterRead, directRead := false, false, false
		for {
			f, more := frames.Next()
			if afterRead {
				directRead = direct[f.Function]
				afterRead = false
			}
			if f.Function == readName {
				afterRead = true
			}
			locked = locked || held[f.Function]
			if !more {
				break
			}
		}
		if !locked || name == "read" && directRead {
			return
		}
		mu.Lock()
		violations = append(violations, name)
		mu.Unlock()
		panic(commitReadViolation(name))
	}
	old := commitReadHook.Swap(&hook)
	if old != nil {
		commitReadHook.Store(old)
		t.Fatal("another commit read guard is active")
	}
	t.Cleanup(func() { commitReadHook.Store(old) })
	return func() {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(violations) != 0 {
			t.Errorf("provider reads under the lifecycle commit lock: %s", strings.Join(violations, ", "))
		}
	}
}

// An indirect call from any helper must fail this test even though the
// AST guard cannot see through that helper. Exercise the legacy fast
// write, binding-moving journal, managed journal, no-op and failed write.
func TestCommitRuntimeMakesNoProviderReads(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "managed"}[managed], func(t *testing.T) {
			h, _ := renameHome(t, managed)
			clean := watchCommitReads(t)
			defer clean()
			defer func() {
				if p := recover(); p != nil {
					if _, ok := p.(commitReadViolation); !ok {
						panic(p)
					}
					t.Errorf("indirect provider read under lifecycle commit lock: %v", p)
				}
			}()
			oldChanged := catalog.Changed
			catalog.Changed = func() {
				// These are legitimate after unlock and must not trip the guard.
				_, _ = Find("relay")
				_ = Accounts()
			}
			t.Cleanup(func() { catalog.Changed = oldChanged })
			f, err := read()
			if err != nil {
				t.Fatal(err)
			}
			f.Providers[0].Name = "edited"
			if err := store(f); err != nil {
				t.Fatal(err)
			}
			f, err = read()
			if err != nil {
				t.Fatal(err)
			}
			if err := store(f); err != nil {
				t.Fatal(err)
			}
			if err := Rename("relay", "relay2"); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("stop after provider write")
			lifecycle.TestStage = func(s string) error {
				if s == "providers.json:write" {
					return injected
				}
				return nil
			}
			t.Cleanup(func() { lifecycle.TestStage = nil })
			if err := Rename("relay2", "relay3"); !errors.Is(err, injected) {
				t.Fatalf("failed commit: %v", err)
			}
			lifecycle.TestStage = nil
			if err := Rename("relay3", "relay4"); err != nil {
				t.Fatalf("write after recovery: %v", err)
			}
			if got, err := h.sign(h.find("relay4")); err != nil || got != uaOf(h.a) {
				t.Fatalf("signed after recovery: %q (%v)", got, err)
			}
		})
	}
}

func callCommitRead(f func()) (caught any) {
	defer func() { caught = recover() }()
	f()
	return nil
}

// Exercise each guard from a real Commit stage, where an accidental
// read would otherwise recover/wait on its own journal. The held All
// case would bypass a guard only in allProviders; read via Stored covers
// other public entry points without instrumenting every one separately.
func TestCommitReadGuardDetectsIndirectCalls(t *testing.T) {
	tests := []struct {
		name string
		call func()
		warm func()
	}{
		{"All", func() { _ = All() }, nil},
		{"held All", func() { _ = All() }, func() { _ = All() }},
		{"held entries", func() { _ = providerEntries() }, func() { _ = heldEntries(func() []Entry { return []Entry{{ID: "relay/gpt-5"}} }) }},
		{"Accounts", func() { _ = Accounts() }, nil},
		{"Find", func() { _, _ = Find("relay") }, nil},
		{"load", func() { _ = load() }, nil},
		{"lifeLoad", func() { _, _ = lifeLoad() }, nil},
		{"Stored", func() { _, _ = Stored() }, nil},
		{"IdentityView", func() { _, _ = ReadIdentityView(true) }, nil},
		{"signing", func() { _, _ = (Provider{ID: "relay"}).headersFor() }, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			renameHome(t, false)
			f, err := read()
			if err != nil {
				t.Fatal(err)
			}
			f.Providers[0].ID = "relay2"
			// A fake clock makes the held entry stay warm across disk fsyncs.
			synctest.Test(t, func(t *testing.T) {
				watchCommitReads(t) // expected violation; do not assertClean
				if tc.warm != nil {
					defer Hold()()
					tc.warm()
				}
				stop := errors.New("probe finished")
				var caught any
				hit := false
				lifecycle.TestStage = func(s string) error {
					if s == "journal:write" {
						hit = true
						caught = callCommitRead(tc.call)
						return stop
					}
					return nil
				}
				t.Cleanup(func() { lifecycle.TestStage = nil })
				err := commit(f, intent{renames: map[string]string{"relay": "relay2"}})
				if !errors.Is(err, stop) || !hit {
					t.Fatalf("guard probe did not reach the commit stage: %v, hit %v", err, hit)
				}
				if _, ok := caught.(commitReadViolation); !ok {
					t.Fatalf("indirect %s was not caught under commit lock: %v", tc.name, caught)
				}
				lifecycle.TestStage = nil
			})
		})
	}
}

// A different goroutine is allowed to read while a writer holds the
// lock. Its stack is not the writer's stack: process-wide counters would
// report a false violation here. Use raw/discovery-off reads so it need
// not wait for the journal whose writer is waiting for this probe.
func TestCommitReadGuardAllowsOtherReaders(t *testing.T) {
	renameHome(t, false)
	t.Setenv("MAGPIE_NO_ACCOUNTS", "1")
	clean := watchCommitReads(t)
	defer clean()
	hit := false
	lifecycle.TestStage = func(s string) error {
		if s == "journal:write" {
			hit = true
			done := make(chan any, 1)
			go func() {
				done <- callCommitRead(func() { _ = Accounts(); _ = load(); _, _ = Stored() })
			}()
			if caught := <-done; caught != nil {
				t.Errorf("another goroutine's read was called commit reentry: %v", caught)
			}
		}
		return nil
	}
	t.Cleanup(func() { lifecycle.TestStage = nil })
	if err := Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("concurrent read never ran inside the commit")
	}
}
