package provider

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/filememo"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
)

// A journal commit bypasses writePrivate's Forget. Cache invalidation
// must still cover success and every partial write, not rely on a later
// catalog/settings notification. Freeze time so a slow fsync cannot make
// the one-second Hold expire and accidentally hide a missing Forget.
func TestLifecycleCommitForgetsHeldFiles(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, stage := range []string{"", "providers.json:write", "identities.json:write", "journal:remove:before"} {
			name := map[bool]string{false: "legacy/", true: "managed/"}[managed] + stage
			t.Run(name, func(t *testing.T) {
				renameHome(t, managed)
				f, err := read()
				if err != nil {
					t.Fatal(err)
				}
				f.Providers[0].ID = "relay2"
				synctest.Test(t, func(t *testing.T) {
					// The fast held branch only applies to settled files. Its
					// time is deliberately old; a normal write gets a new time.
					old := time.Now().Add(-time.Hour)
					paths := []string{Path(), identity.Path()}
					if managed {
						paths = append(paths, lifecycle.Path())
					}
					defer filememo.Hold()()
					t.Cleanup(filememo.Forget)
					memo := func(path string) []byte {
						t.Helper()
						v, err := filememo.Read("lifecycle commit test", path, func(b []byte) ([]byte, error) { return bytes.Clone(b), nil })
						if err != nil {
							t.Fatal(err)
						}
						return v
					}
					before := map[string][]byte{}
					for _, p := range paths {
						if err := os.Chtimes(p, old, old); err != nil {
							t.Fatal(err)
						}
						before[p] = memo(p)
					}
					// A sentinel proves Hold really is active, and that the
					// write invalidates filememo globally just as writePrivate did.
					sentinel := Path() + ".memo-probe"
					if err := os.WriteFile(sentinel, []byte("old"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(sentinel, old, old); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(memo(sentinel), []byte("old")) {
						t.Fatal("sentinel did not prime")
					}
					if err := os.WriteFile(sentinel, []byte("new content"), 0o600); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(memo(sentinel), []byte("old")) {
						t.Fatal("fixture is not holding its old parse")
					}
					forget, changed := catalog.Forget, catalog.Changed
					catalog.Forget, catalog.Changed = nil, nil
					t.Cleanup(func() { catalog.Forget, catalog.Changed = forget, changed })
					injected := errors.New("stop this commit")
					hit := false
					lifecycle.TestStage = func(s string) error {
						if stage != "" && s == stage {
							hit = true
							return injected
						}
						return nil
					}
					t.Cleanup(func() { lifecycle.TestStage = nil })
					// Read at unlock, before even the error path's Changed. A
					// missing invalidation must not be masked by that callback.
					oldLock := lockCommit
					unlocked := false
					lockCommit = func() (func(), error) {
						unlock, err := oldLock()
						if err != nil {
							return nil, err
						}
						return func() {
							unlock()
							unlocked = true
							changedFiles := 0
							for _, path := range paths {
								disk, err := os.ReadFile(path)
								if err != nil {
									t.Fatal(err)
								}
								if !bytes.Equal(disk, before[path]) {
									changedFiles++
								}
								if got := memo(path); !bytes.Equal(got, disk) {
									t.Errorf("%s: Hold returned the pre-commit bytes at unlock after %q", path, stage)
								}
							}
							if changedFiles == 0 {
								t.Fatal("fixture did not reach a participant write")
							}
							if got := memo(sentinel); !bytes.Equal(got, []byte("new content")) {
								t.Errorf("commit failed to Forget held files after %q: %q", stage, got)
							}
						}, nil
					}
					t.Cleanup(func() { lockCommit = oldLock })
					err := commit(f, intent{renames: map[string]string{"relay": "relay2"}})
					if stage == "" && err != nil || stage != "" && (!hit || !errors.Is(err, injected)) {
						t.Fatalf("commit stage %q: %v (hit %v)", stage, err, hit)
					}
					if !unlocked {
						t.Fatal("commit did not release the lock")
					}
				})
			})
		}
	}
}

// The public path also drops provider.Hold: immediately after Rename a
// Find must return the new id and Sign must present that id's binding.
func TestLifecycleRenameRefreshesHeldProvider(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "managed"}[managed], func(t *testing.T) {
			h, _ := renameHome(t, managed)
			synctest.Test(t, func(t *testing.T) {
				defer Hold()()
				defer filememo.Hold()()
				p := h.find("relay")
				if p.ID != "relay" {
					t.Fatal(p.ID)
				}
				if err := Rename("relay", "relay2"); err != nil {
					t.Fatal(err)
				}
				p = h.find("relay2")
				if p.ID != "relay2" {
					t.Fatalf("held provider still has id %q", p.ID)
				}
				if got, err := h.sign(p); err != nil || got != uaOf(h.a) {
					t.Fatalf("renamed held provider sent %q (%v), want %q", got, err, uaOf(h.a))
				}
			})
		})
	}
}
