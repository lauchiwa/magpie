package lifecycle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/lastgood"
)

// linkedPlan prepares the standard three-participant plan while
// providers.json is (already) whatever link the caller set up; it never
// writes providers.json through its logical name.
func linkedPlan(t *testing.T) Plan {
	t.Helper()
	put(t, IdentitiesFile, beforeIdentity)
	put(t, MetaFile, encoded(t, meta(1, beforeProvider)))
	put(t, IdentitiesFile+".bak", []byte(`{"profiles":[]}`))
	var ps []Participant
	for _, v := range []struct {
		name string
		b    []byte
	}{{ProvidersFile, afterProvider}, {IdentitiesFile, afterIdentity}, {MetaFile, encoded(t, meta(2, afterProvider))}} {
		p, err := Prepare(v.name, Present(v.b), lastgood.JSON)
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p)
	}
	return Plan{Participants: ps}
}

func logicalProviders() string { return filepath.Join(appdir.Config(), ProvidersFile) }

func want(t *testing.T, path string, b []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if !bytes.Equal(got, b) {
		t.Fatalf("%s: got %s want %s", path, got, b)
	}
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists: %v", path, err)
	}
}

// seam stands resolveTarget in for a link: the logical providers.json
// resolves to *to. Portable to platforms where creating a link needs
// privileges; real links are tested below where they can be made.
func seam(t *testing.T) (a, b string, to *string) {
	t.Helper()
	home(t)
	dir := filepath.Join(appdir.Config(), "elsewhere")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	a, b = filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	if err := os.WriteFile(a, beforeProvider, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, beforeProvider, 0600); err != nil { // same bytes
		t.Fatal(err)
	}
	target := a
	old := resolveTarget
	t.Cleanup(func() { resolveTarget = old })
	resolveTarget = func(p string) (string, error) {
		if p == logicalProviders() {
			return target, nil
		}
		return old(p)
	}
	return a, b, &target
}

func TestLinkedProvidersCommitWritesTarget(t *testing.T) {
	a, b, _ := seam(t)
	defer mustLock(t)()
	p := linkedPlan(t)
	if p.Participants[0].Target != a {
		t.Fatalf("journal target %q, want %q", p.Participants[0].Target, a)
	}
	if err := Commit(p); err != nil {
		t.Fatal(err)
	}
	want(t, a, afterProvider)
	want(t, b, beforeProvider)
	// The backup stays at the logical name, as lastgood reads it.
	want(t, lastgood.Bak(logicalProviders()), beforeProvider)
	absent(t, lastgood.Bak(a))
	absent(t, logicalProviders()) // the link (here: the seam) is not replaced
	checkMetaGen(t, 2)
}

func checkMetaGen(t *testing.T, gen uint64) {
	t.Helper()
	m, err := ReadMeta()
	if err != nil || m == nil || m.Gen != gen {
		t.Fatalf("meta gen: %v %v", m, err)
	}
}

func TestLinkedProvidersRecoverEveryStage(t *testing.T) {
	for _, stage := range []string{"journal:write", "providers.json:keep:before", "providers.json:keep:after", "providers.json:write", "identities.json:write", "journal:remove:before"} {
		t.Run(stage, func(t *testing.T) {
			a, _, _ := seam(t)
			defer mustLock(t)()
			p := linkedPlan(t)
			p.stage = func(s string) error {
				if s == stage {
					return errors.New("crash")
				}
				return nil
			}
			if err := Commit(p); err == nil {
				t.Fatal("not stopped")
			}
			for range 2 {
				if err := Recover(); err != nil {
					t.Fatal(err)
				}
			}
			want(t, a, afterProvider)
			want(t, lastgood.Bak(logicalProviders()), beforeProvider)
			absent(t, logicalProviders())
			checkMetaGen(t, 2)
		})
	}
}

// The link moves to another file with the SAME bytes: still a third state.
func TestLinkedProvidersTargetSwitchIsThirdState(t *testing.T) {
	for _, stage := range []string{"journal:write", "providers.json:keep:before", "providers.json:keep:after", "providers.json:write", "journal:remove:before"} {
		t.Run(stage, func(t *testing.T) {
			_, b, to := seam(t)
			defer mustLock(t)()
			p := linkedPlan(t)
			p.stage = func(s string) error {
				if s == stage {
					*to = b
				}
				return nil
			}
			err := Commit(p)
			var amb *AmbiguousError
			if !errors.As(err, &amb) {
				t.Fatalf("target switch at %s accepted: %v", stage, err)
			}
			want(t, b, beforeProvider) // the new target is never written
			if _, err := os.Stat(JournalPath()); err != nil {
				t.Fatalf("journal not kept as evidence: %v", err)
			}
			if !strings.Contains(err.Error(), b) {
				t.Fatalf("error does not name the new target: %v", err)
			}
			// Recover refuses the same way and changes nothing.
			journal := get(t, "lifecycle-journal.json")
			if err := Recover(); !errors.As(err, &amb) {
				t.Fatalf("recover accepted a switched target: %v", err)
			}
			want(t, b, beforeProvider)
			if !bytes.Equal(get(t, "lifecycle-journal.json"), journal) {
				t.Fatal("journal changed")
			}
		})
	}
}

func TestLinkedProvidersSwitchBeforeCommitPublishesNothing(t *testing.T) {
	a, b, to := seam(t)
	defer mustLock(t)()
	p := linkedPlan(t)
	*to = b
	var amb *AmbiguousError
	if err := Commit(p); !errors.As(err, &amb) {
		t.Fatalf("switched before journal: %v", err)
	}
	absent(t, JournalPath())
	want(t, a, beforeProvider)
	want(t, b, beforeProvider)
}

func TestReadImageFollowsProvidersLink(t *testing.T) {
	a, _, _ := seam(t)
	image, err := ReadImage(ProvidersFile)
	if err != nil || !image.Exists || !bytes.Equal(image.Bytes, beforeProvider) {
		t.Fatalf("linked providers not read: %v %v", image, err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if image, err = ReadImage(ProvidersFile); err != nil || image.Exists {
		t.Fatalf("dangling target not absent: %v %v", image, err)
	}
}

// A providers.json that resolves onto another protocol file is refused.
func TestProvidersTargetAliasRefused(t *testing.T) {
	for _, name := range []string{IdentitiesFile, IdentitiesFile + ".bak", MetaFile, "lifecycle-journal.json", "lifecycle.lock", ProvidersFile + ".bak"} {
		t.Run(name, func(t *testing.T) {
			_, _, to := seam(t)
			put(t, IdentitiesFile, beforeIdentity)
			*to = filepath.Join(appdir.Config(), name)
			if _, err := ReadImage(ProvidersFile); !errors.Is(err, ErrUnknown) {
				t.Fatalf("alias of %s read: %v", name, err)
			}
			if _, err := Prepare(ProvidersFile, Present(afterProvider), lastgood.JSON); !errors.Is(err, ErrUnknown) {
				t.Fatalf("alias of %s prepared: %v", name, err)
			}
			if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
				*to = filepath.Join(appdir.Config(), strings.ToUpper(name))
				if _, err := ReadImage(ProvidersFile); !errors.Is(err, ErrUnknown) {
					t.Fatalf("case alias of %s read: %v", name, err)
				}
			}
		})
	}
}

func TestProvidersTargetDirectoryRefused(t *testing.T) {
	a, _, _ := seam(t)
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(a, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadImage(ProvidersFile); !errors.Is(err, ErrUnknown) {
		t.Fatalf("directory target read: %v", err)
	}
}

// ---- real links, where the platform lets this process make them ----

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
}

func TestRealProvidersLinkCommitAndRecover(t *testing.T) {
	for _, kind := range []string{"absolute", "relative", "dangling"} {
		for _, crash := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "", true: "-recover"}[crash], func(t *testing.T) {
				home(t)
				dir := filepath.Join(filepath.Dir(appdir.Config()), "linked-target")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(dir, "providers.json")
				before := beforeProvider
				if kind == "dangling" {
					before = nil
				} else if err := os.WriteFile(target, beforeProvider, 0600); err != nil {
					t.Fatal(err)
				}
				dest := target
				if kind == "relative" {
					dest = filepath.Join("..", "linked-target", "providers.json")
				}
				symlink(t, dest, logicalProviders())
				defer mustLock(t)()
				p := linkedPlan(t)
				if crash {
					p.stage = func(s string) error {
						if s == "providers.json:keep:after" {
							return errors.New("crash")
						}
						return nil
					}
					if err := Commit(p); err == nil {
						t.Fatal("not stopped")
					}
					if err := Recover(); err != nil {
						t.Fatal(err)
					}
				} else if err := Commit(p); err != nil {
					t.Fatal(err)
				}
				want(t, target, afterProvider)
				fi, err := os.Lstat(logicalProviders())
				if err != nil || fi.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("link replaced by a file: %v %v", fi, err)
				}
				if before == nil {
					absent(t, lastgood.Bak(logicalProviders()))
				} else {
					want(t, lastgood.Bak(logicalProviders()), before)
				}
				absent(t, lastgood.Bak(target))
				checkMetaGen(t, 2)
			})
		}
	}
}

func TestRealProvidersLinkRetargetIsThirdState(t *testing.T) {
	home(t)
	dir := filepath.Join(filepath.Dir(appdir.Config()), "linked-target")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, beforeProvider, 0600); err != nil {
			t.Fatal(err)
		}
	}
	symlink(t, a, logicalProviders())
	defer mustLock(t)()
	p := linkedPlan(t)
	p.stage = func(s string) error {
		if s == "providers.json:keep:after" {
			if err := os.Remove(logicalProviders()); err != nil {
				return err
			}
			return os.Symlink(b, logicalProviders())
		}
		return nil
	}
	var amb *AmbiguousError
	if err := Commit(p); !errors.As(err, &amb) {
		t.Fatalf("retargeted link accepted: %v", err)
	}
	want(t, a, beforeProvider)
	want(t, b, beforeProvider)
	if _, err := os.Stat(JournalPath()); err != nil {
		t.Fatal("journal not kept")
	}
}

func TestRealProvidersLinkAliasRefused(t *testing.T) {
	home(t)
	put(t, IdentitiesFile, beforeIdentity)
	symlink(t, filepath.Join(appdir.Config(), IdentitiesFile), logicalProviders())
	if _, err := ReadImage(ProvidersFile); !errors.Is(err, ErrUnknown) {
		t.Fatalf("providers aliasing identities read: %v", err)
	}
}

func TestIdentityAndMetaLinksStillRefused(t *testing.T) {
	for _, name := range []string{IdentitiesFile, MetaFile} {
		t.Run(name, func(t *testing.T) {
			home(t)
			target := filepath.Join(appdir.Config(), "target")
			if err := os.WriteFile(target, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			symlink(t, target, filepath.Join(appdir.Config(), name))
			if _, err := ReadImage(name); !errors.Is(err, ErrUnknown) {
				t.Fatalf("%s link accepted: %v", name, err)
			}
		})
	}
}
