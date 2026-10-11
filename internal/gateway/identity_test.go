package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

// armable is a plain key+URL provider, which a profile applies to.
func armable(t *testing.T, id, name string) provider.Provider {
	t.Helper()
	p := provider.Provider{ID: id, Name: name, Chat: "https://example.invalid/v1", Key: "k1"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A capture window lives in the memory of the process that forwards the
// requests. `magpie identity arm` runs in a CLI process of its own, so
// before these paths existed it armed a window in a process that sees no
// request and exits, and the running gateway captured nothing however
// many requests went through. Arming now reaches the gateway over its own
// local control path, and the request it then forwards is projected
// there.
func TestIdentityCaptureArmedOnTheRunningGateway(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := armable(t, "arm-here", "Arm Here")
	t.Cleanup(func() { identity.Disarm(p.ID) })
	h := New().Handler()

	call := func(method, path string) (int, string) {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:5000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}

	// nothing is armed to begin with, and the gateway says so
	if code, b := call("GET", IdentityArmedPath); code != 200 || strings.Contains(b, p.ID) {
		t.Fatalf("armed before arming: %d %s", code, b)
	}
	if code, b := call("POST", IdentityArmPath+"?provider="+p.ID+"&client=codex_cli"); code != 200 {
		t.Fatalf("arm: %d %s", code, b)
	}
	if code, b := call("GET", IdentityArmedPath); code != 200 || !strings.Contains(b, p.ID) {
		t.Fatalf("the gateway doesn't hold the window it was asked to arm: %d %s", code, b)
	}

	// the request the gateway then forwards is the one that is projected:
	// this is what the CLI's own arm could never reach
	seen := http.Header{}
	seen.Set("User-Agent", "codex-tui/0.162.1 (Mac OS 15.7.9; x86_64)")
	seen.Set("Originator", "codex-tui")
	provider.ObserveIdentity(p.ID, provider.ClientCodex, seen)

	ps, err := identity.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("profiles after the armed request: %d, want the captured candidate", len(ps))
	}
	got := ps[0]
	if got.Client != identity.ClientCodex || got.Source != identity.SourceCapture {
		t.Errorf("captured %s from %s, want codex_cli from request_capture", got.Client, got.Source)
	}
	if got.ConfirmedAt != nil {
		t.Error("a captured profile is confirmed; it must be a candidate until the operator confirms it")
	}
	if got.Fingerprint.UserAgent != "codex-tui/0.162.1 (Mac OS 15.7.9; x86_64)" || got.Fingerprint.Version != "0.162.1" {
		t.Errorf("captured %q / %q", got.Fingerprint.UserAgent, got.Fingerprint.Version)
	}
	// the window was one-shot: it is gone with the request it took
	if code, b := call("GET", IdentityArmedPath); code != 200 || strings.Contains(b, p.ID) {
		t.Errorf("still armed after its one request: %d %s", code, b)
	}
}

// Disarm revokes a window before it is used, on the gateway holding it.
func TestIdentityCaptureDisarmedOnTheRunningGateway(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := armable(t, "disarm-here", "Disarm Here")
	t.Cleanup(func() { identity.Disarm(p.ID) })
	h := New().Handler()
	call := func(method, path string) (int, string) {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:5000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if code, b := call("POST", IdentityArmPath+"?provider="+p.ID+"&client=codex_cli"); code != 200 {
		t.Fatalf("arm: %d %s", code, b)
	}
	if code, b := call("POST", IdentityDisarmPath+"?provider="+p.ID); code != 200 {
		t.Fatalf("disarm: %d %s", code, b)
	}
	if code, b := call("GET", IdentityArmedPath); code != 200 || strings.Contains(b, p.ID) {
		t.Fatalf("still armed after disarm: %d %s", code, b)
	}
	// a request through it now captures nothing
	seen := http.Header{}
	seen.Set("User-Agent", "codex-tui/0.162.1 (Mac OS 15.7.9; x86_64)")
	seen.Set("Originator", "codex-tui")
	provider.ObserveIdentity(p.ID, provider.ClientCodex, seen)
	ps, err := identity.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Errorf("a disarmed provider captured %d profiles, want none", len(ps))
	}
	// disarming again is not an error: nothing armed is what was asked for
	if code, b := call("POST", IdentityDisarmPath+"?provider="+p.ID); code != 200 {
		t.Errorf("disarm twice: %d %s", code, b)
	}
}

// Arming is consent for this gateway to keep the identity fields of the
// next matching request, and it is granted at the machine magpie runs on.
// A shared gateway's key is held by other people's agents, so unlike
// quotas and concurrency these paths answer this machine alone, with or
// without a key.
func TestIdentityCaptureArmsForThisMachineAlone(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := armable(t, "arm-elsewhere", "Arm Elsewhere")
	t.Cleanup(func() { identity.Disarm(p.ID) })
	_, secrets := newCaller(t, "Host")
	// through the real guard, so a remote caller's key marks it shared
	// (sharedWith), which is what quotas and concurrency answer on
	h := lanGuard(New().Handler())
	for _, rt := range [][2]string{
		{"POST", IdentityArmPath + "?provider=" + p.ID + "&client=codex_cli"},
		{"POST", IdentityDisarmPath + "?provider=" + p.ID},
		{"GET", IdentityArmedPath},
	} {
		// without a key the guard stops it first (401); with an enabled
		// gateway key it reaches these paths, and they refuse it (403),
		// where quotas and concurrency would answer
		for _, c := range []struct {
			what string
			hdr  []string
			want int
		}{
			{"no key", nil, http.StatusUnauthorized},
			{"a gateway key", []string{"Authorization", "Bearer " + secrets[0]}, http.StatusForbidden},
		} {
			r := httptest.NewRequest(rt[0], rt[1], nil)
			r.RemoteAddr = "192.168.1.9:5000"
			for i := 0; i+1 < len(c.hdr); i += 2 {
				r.Header.Set(c.hdr[i], c.hdr[i+1])
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Errorf("%s %s from another machine with %s: %d %.140s, want %d", rt[0], rt[1], c.what, w.Code, w.Body.String(), c.want)
			}
		}
	}
	// the same key does answer on a path that is shared, so the refusal
	// above is these paths' own and not a broken key
	r := httptest.NewRequest("GET", "/v1/magpie/concurrency", nil)
	r.RemoteAddr = "192.168.1.9:5000"
	r.Header.Set("Authorization", "Bearer "+secrets[0])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("concurrency with the same key: %d %.140s, want 200", w.Code, w.Body.String())
	}
	if identity.CaptureArmed(p.ID, time.Now().UTC()) {
		t.Error("a request from another machine armed a capture")
	}
}

// The gateway checks the target itself rather than trusting the caller:
// a capture for a provider no profile is sent on would wait for a request
// that path never makes, and a client it doesn't know would never match.
func TestIdentityCaptureRefusesWhatItCannotCapture(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ok := armable(t, "arm-checks", "Arm Checks")
	t.Cleanup(func() { identity.Disarm(ok.ID) })
	// a magpie service, which IdentityApplies rules out: a profile bound
	// to one would never be sent
	remote := provider.Provider{ID: "arm-remote", Name: "Arm Remote", Preset: provider.RemoteMagpiePreset,
		Chat: "http://127.0.0.1:9/v1", Key: "k1"}
	if err := provider.Save(remote); err != nil {
		t.Fatal(err)
	}
	h := New().Handler()
	call := func(path string) (int, string) {
		r := httptest.NewRequest("POST", path, nil)
		r.RemoteAddr = "127.0.0.1:5000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	for _, c := range []struct{ what, path, says string }{
		{"no provider", IdentityArmPath + "?client=codex_cli", "name the provider"},
		{"no such provider", IdentityArmPath + "?provider=nope&client=codex_cli", "nope"},
		{"a magpie service", IdentityArmPath + "?provider=" + remote.ID + "&client=codex_cli", "magpie service"},
		{"no such client", IdentityArmPath + "?provider=" + ok.ID + "&client=emacs", "codex_cli"},
		{"no client", IdentityArmPath + "?provider=" + ok.ID, "codex_cli"},
	} {
		code, b := call(c.path)
		if code == http.StatusOK {
			t.Errorf("%s: armed anyway (%d %s)", c.what, code, b)
			continue
		}
		if !strings.Contains(b, c.says) {
			t.Errorf("%s: %d %.160s, want it to name %q", c.what, code, b, c.says)
		}
	}
	if identity.CaptureArmed(remote.ID, time.Now().UTC()) {
		t.Error("a magpie service has a capture window armed")
	}
	// a client that doesn't match consumes nothing: the one-shot window
	// stays for the client it was armed for
	if code, b := call(IdentityArmPath + "?provider=" + ok.ID + "&client=codex_cli"); code != 200 {
		t.Fatalf("arm: %d %s", code, b)
	}
	claude := http.Header{}
	claude.Set("User-Agent", "claude-cli/2.1.296 (external, cli)")
	claude.Set("X-App", "cli")
	provider.ObserveIdentity(ok.ID, provider.ClientClaudeCode, claude)
	if !identity.CaptureArmed(ok.ID, time.Now().UTC()) {
		t.Error("another client's request consumed the window armed for codex_cli")
	}
	if ps, err := identity.Profiles(); err != nil || len(ps) != 0 {
		t.Errorf("profiles after a mismatched request: %d (%v), want none", len(ps), err)
	}
}

// The armed list is the providers with a live window, as JSON the CLI
// reads to show that an arm took.
func TestIdentityArmedListsTheWindows(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := armable(t, "armed-a", "Armed A")
	b := armable(t, "armed-b", "Armed B")
	armable(t, "armed-c", "Armed C")
	t.Cleanup(func() { identity.Disarm(a.ID); identity.Disarm(b.ID) })
	h := New().Handler()
	post := func(path string) {
		r := httptest.NewRequest("POST", path, nil)
		r.RemoteAddr = "127.0.0.1:5000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	post(IdentityArmPath + "?provider=" + a.ID + "&client=codex_cli")
	post(IdentityArmPath + "?provider=" + b.ID + "&client=claude_code")
	r := httptest.NewRequest("GET", IdentityArmedPath, nil)
	r.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got struct{ Data []string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 2 || got.Data[0] != a.ID || got.Data[1] != b.ID {
		t.Errorf("armed: %v, want [%s %s] in order", got.Data, a.ID, b.ID)
	}
}
