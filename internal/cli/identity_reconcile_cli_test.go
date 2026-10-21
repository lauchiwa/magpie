package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/provider"
)

// reconcileHome is a home whose lifecycle.json was readied over providers a
// (bound) and b, then b was removed by hand from providers.json while
// bound: one binding left undecided.
func reconcileHome(t *testing.T) string {
	t.Helper()
	groupsHome(t)
	hd := http.Header{
		"User-Agent": {"codex_cli_rs/0.159.2 (Mac OS 26.6.0; arm64) Apple_Terminal/455"},
		"Originator": {"codex_cli_rs"},
		"Version":    {"0.159.2"},
	}
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p, err := identity.Candidate(id, identity.ClientCodex, hd, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Second)
	p.ConfirmedAt = &at
	if err := identity.Add(p); err != nil {
		t.Fatal(err)
	}
	for _, prov := range []string{"a", "b"} {
		if err := identity.Bind(prov, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.ReadyLifecycle(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lifecycle.Path()); err != nil {
		t.Fatalf("ReadyLifecycle made no lifecycle.json: %v", err)
	}
	// b removed by hand: lifecycle.json is not told
	raw, err := os.ReadFile(provider.Path())
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f["providers"] = f["providers"].([]any)[:1]
	raw, _ = json.MarshalIndent(f, "", "  ")
	if err := os.WriteFile(provider.Path(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// configTree is every file under the config folder with its bytes' hash.
func configTree(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Dir(provider.Path())
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		h := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(h[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, what string, before map[string]string) {
	t.Helper()
	after := configTree(t)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed %s", what, k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("%s created %s", what, k)
		}
	}
}

// magpie identity reconcile alone shows what it would do and writes
// nothing; a flag it doesn't know, --keep with --drop, and --help write
// nothing; --yes with an undecided binding and no choice writes nothing;
// --yes --drop keeps the old lifecycle.json aside and unbinds b.
func TestIdentityReconcileCmd(t *testing.T) {
	reconcileHome(t)
	before := configTree(t)
	out, err := printed(t, func() error { return identityCmd([]string{"identity", "reconcile"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"changed outside magpie", "b ", "removed from providers.json while bound", "nothing was written", "--drop"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview doesn't say %q:\n%s", want, out)
		}
	}
	sameTree(t, "the preview", before)

	for _, args := range [][]string{{"--bogus"}, {"--yes", "--bogus"}, {"--yes", "--keep", "--drop"}, {"b"}, {"help"}} {
		if _, err := printed(t, func() error { return identityCmd(append([]string{"identity", "reconcile"}, args...)) }); err == nil {
			t.Errorf("reconcile %v: no error", args)
		}
		sameTree(t, "reconcile "+strings.Join(args, " "), before)
	}
	help, err := printed(t, func() error { return run([]string{"identity", "reconcile", "--help"}) })
	if err != nil || !strings.Contains(help, "magpie identity reconcile") {
		t.Errorf("--help: %v\n%s", err, help)
	}
	sameTree(t, "reconcile --help", before)

	out, err = printed(t, func() error { return identityCmd([]string{"identity", "reconcile", "--yes"}) })
	if err == nil || !strings.Contains(err.Error(), "--keep") {
		t.Fatalf("--yes with an undecided binding: %v\n%s", err, out)
	}
	sameTree(t, "--yes without a choice", before)

	out, err = printed(t, func() error { return identityCmd([]string{"identity", "reconcile", "--yes", "--drop"}) })
	if err != nil || !strings.Contains(out, "reconciled") || !strings.Contains(out, "lifecycle.json.reconciled-") {
		t.Fatalf("--yes --drop: %v\n%s", err, out)
	}
	b, err := identity.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	if b["b"] != "" || b["a"] == "" {
		t.Fatalf("bindings after --drop: %v", b)
	}
	m, err := lifecycle.ReadMeta()
	if err != nil || m == nil || len(m.Unreconciled) != 0 {
		t.Fatalf("lifecycle.json after --yes --drop: %+v %v", m, err)
	}
	out, err = printed(t, func() error { return identityCmd([]string{"identity", "reconcile"}) })
	if err != nil || !strings.Contains(out, "lifecycle.json matches providers.json") {
		t.Fatalf("after reconciling: %v\n%s", err, out)
	}
}

// --yes --keep keeps the old lifecycle.json aside, settles it, and leaves
// the undecided binding (b's) where it is: --keep is not --drop.
func TestIdentityReconcileKeep(t *testing.T) {
	profile := reconcileHome(t)
	out, err := printed(t, func() error { return identityCmd([]string{"identity", "reconcile", "--yes", "--keep"}) })
	if err != nil || !strings.Contains(out, "reconciled") || !strings.Contains(out, "lifecycle.json.reconciled-") {
		t.Fatalf("--yes --keep: %v\n%s", err, out)
	}
	b, err := identity.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	if b["a"] != profile || b["b"] != profile {
		t.Fatalf("bindings after --keep: %v, want a and b on %s", b, profile)
	}
	m, err := lifecycle.ReadMeta()
	if err != nil || m == nil || len(m.Unreconciled) != 0 {
		t.Fatalf("lifecycle.json after --yes --keep: %+v %v", m, err)
	}
}

// In a home with no lifecycle.json (every magpie until it is readied) the
// command says so and writes nothing, --yes included.
func TestIdentityReconcileLegacy(t *testing.T) {
	groupsHome(t)
	before := configTree(t)
	for _, args := range [][]string{{}, {"--yes"}, {"--yes", "--keep"}} {
		out, err := printed(t, func() error { return identityCmd(append([]string{"identity", "reconcile"}, args...)) })
		if err != nil || !strings.Contains(out, "no lifecycle.json") {
			t.Fatalf("reconcile %v: %v\n%s", args, err, out)
		}
	}
	sameTree(t, "a legacy reconcile", before)
	if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("reconcile bootstrapped lifecycle.json")
	}
}
