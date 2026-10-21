package provider

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/identity"
)

// TestLifecycleOtherMagpie is the CLI of TestLifecycleAcrossProcesses: in
// the same home, as its own process, it makes one change through the
// public API and exits.
func TestLifecycleOtherMagpie(t *testing.T) {
	step := os.Getenv("MAGPIE_LIFECYCLE_STEP")
	if step == "" {
		t.Skip("run by TestLifecycleAcrossProcesses")
	}
	upstream := os.Getenv("MAGPIE_LIFECYCLE_UPSTREAM")
	var err error
	switch step {
	case "rename":
		err = Rename("relay", "relay2")
	case "delete-unbound":
		err = Delete("plain")
	case "delete-bound":
		err = Delete("relay2")
	case "recreate":
		err = Save(Provider{ID: "relay2", Name: "relay2", Key: "sk-test", Chat: upstream + "/v1"})
	case "bind":
		err = identity.Bind("relay2", os.Getenv("MAGPIE_LIFECYCLE_PROFILE"))
	default:
		t.Fatalf("no step %q", step)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// R1.10: the gateway (this process) holds Provider values read before
// another magpie (a child process, same home) renames, deletes, recreates
// and binds; each value is signed again after each step, and the request
// the upstream receives (or the error) is the one its instance calls for:
// never another provider's binding under a reused id.
func TestLifecycleAcrossProcesses(t *testing.T) {
	h := readyHome(t, nil, "relay", "plain")
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	other := func(step string) {
		t.Helper()
		c := exec.Command(os.Args[0], "-test.run=^TestLifecycleOtherMagpie$", "-test.count=1", "-test.v")
		c.Env = append(os.Environ(), "MAGPIE_LIFECYCLE_STEP="+step, "MAGPIE_LIFECYCLE_UPSTREAM="+h.srv.URL,
			"MAGPIE_LIFECYCLE_PROFILE="+h.b.ID)
		out, err := c.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestLifecycleOtherMagpie") {
			t.Fatalf("the other magpie's %s: %v\n%s", step, err, out)
		}
	}
	relay, plain := h.find("relay"), h.find("plain")
	expect := func(when string, p Provider, want string, wantErr error) {
		t.Helper()
		got, err := h.sign(p)
		switch {
		case wantErr != nil:
			if !errors.Is(err, wantErr) {
				t.Fatalf("%s: sent %q (%v), want %v", when, got, err, wantErr)
			}
		case err != nil:
			t.Fatalf("%s: %v", when, err)
		case want == "own":
			if got == "" || got == uaOf(h.a) || got == uaOf(h.b) {
				t.Fatalf("%s: sent %q, want magpie's own User-Agent", when, got)
			}
		case got != want:
			t.Fatalf("%s: sent %q, want %q", when, got, want)
		}
	}
	expect("before anything", relay, uaOf(h.a), nil)

	other("rename")
	expect("relay's old value after the rename", relay, uaOf(h.a), nil)
	expect("relay2 read now", h.find("relay2"), uaOf(h.a), nil)

	other("delete-unbound")
	expect("plain's old value after its unbound delete", plain, "own", nil)

	other("delete-bound")
	expect("relay's old value after relay2's bound delete", relay, "", ErrStale)

	other("recreate")
	fresh := h.find("relay2")
	expect("relay's old value after relay2 is made anew", relay, "", ErrStale)
	expect("the new relay2", fresh, "own", nil)

	other("bind")
	expect("relay's old value after the new relay2 is bound", relay, "", ErrStale)
	expect("the new relay2, bound", fresh, uaOf(h.b), nil)
	expect("relay2 read now", h.find("relay2"), uaOf(h.b), nil)
}
