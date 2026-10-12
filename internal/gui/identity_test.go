package gui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/provider"
)

// identityHome isolates the store and the providers from the real machine.
func identityHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// identityMux is the API with the gateway control client stood in for, so
// no test reaches a real gateway. armed is what the fake gateway holds.
func identityMux(t *testing.T, running bool) (*http.ServeMux, *[]gateway.ArmedIdentity, *[]string) {
	t.Helper()
	armed := &[]gateway.ArmedIdentity{}
	calls := &[]string{}
	t.Cleanup(func() {
		gatewayRunning, armedIdentities = gateway.Running, gateway.ArmedIdentities
		armIdentity, disarmIdentity = gateway.ArmIdentity, gateway.DisarmIdentity
	})
	gatewayRunning = func() bool { return running }
	armedIdentities = func(context.Context, string) ([]gateway.ArmedIdentity, error) { return *armed, nil }
	armIdentity = func(_ context.Context, _, id string, c identity.Client) error {
		*calls = append(*calls, "arm "+id+" "+string(c))
		at := time.Now().UTC().Add(identity.CaptureWindow)
		*armed = append(*armed, gateway.ArmedIdentity{Provider: id, Client: c, Expires: &at})
		return nil
	}
	disarmIdentity = func(_ context.Context, _, id string) error {
		*calls = append(*calls, "disarm "+id)
		kept := (*armed)[:0]
		for _, a := range *armed {
			if a.Provider != id {
				kept = append(kept, a)
			}
		}
		*armed = kept
		return nil
	}
	mux := http.NewServeMux()
	identityRoutes(mux)
	return mux, armed, calls
}

func identityCall(t *testing.T, mux *http.ServeMux, method, path, body string) (int, string) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func identityStateOf(t *testing.T, raw string) identityStateJSON {
	t.Helper()
	var s identityStateJSON
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("decode state: %v\n%s", err, raw)
	}
	return s
}

// plainProvider is a key+URL provider, which a profile applies to.
func plainProvider(t *testing.T, id, name string) provider.Provider {
	t.Helper()
	p := provider.Provider{ID: id, Name: name, Chat: "https://example.invalid/v1", Key: "k1"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func codexCandidate(t *testing.T) identity.Profile {
	t.Helper()
	h := http.Header{}
	h.Set("User-Agent", "codex-tui/0.162.1 (Mac OS 15.7.9; x86_64)")
	h.Set("Originator", "codex-tui")
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	p, err := identity.ImportedCandidate(id, identity.ClientCodex, h, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Add(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// With no profile the page is told so plainly, with the providers a
// capture could be armed for and how long a window lasts.
func TestIdentityStateEmpty(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	mux, _, _ := identityMux(t, true)
	code, body := identityCall(t, mux, "GET", "/api/identity", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	s := identityStateOf(t, body)
	if len(s.Profiles) != 0 || len(s.Bindings) != 0 || len(s.Armed) != 0 {
		t.Fatalf("state = %+v, want nothing yet", s)
	}
	if !s.Gateway || s.Window != identity.CaptureWindow.String() {
		t.Errorf("gateway = %v, window = %q", s.Gateway, s.Window)
	}
	if len(s.Providers) != 1 || !s.Providers[0].Applicable || s.Providers[0].Why != "" {
		t.Fatalf("providers = %+v", s.Providers)
	}
}

// The whole lifecycle through the API: a candidate is sent to nobody and
// cannot be bound, confirming makes it usable, a binding is listed on
// both sides, and removing it takes every binding with it.
func TestIdentityLifecycleThroughTheAPI(t *testing.T) {
	identityHome(t)
	a := plainProvider(t, "relay-a", "Relay A")
	b := plainProvider(t, "relay-b", "Relay B")
	p := codexCandidate(t)
	mux, _, _ := identityMux(t, true)

	_, body := identityCall(t, mux, "GET", "/api/identity", "")
	s := identityStateOf(t, body)
	if len(s.Profiles) != 1 || s.Profiles[0].State != "candidate" || s.Profiles[0].ConfirmedAt != nil {
		t.Fatalf("profiles = %+v, want one candidate", s.Profiles)
	}
	if len(s.Profiles[0].Bindings) != 0 {
		t.Errorf("a candidate is bound: %+v", s.Profiles[0].Bindings)
	}

	// a candidate cannot be bound: confirming is the operator's step
	if code, body := identityCall(t, mux, "POST", "/api/identity/bind", `{"provider":"relay-a","profile":"`+p.ID+`"}`); code == 200 || !strings.Contains(body, "not confirmed") {
		t.Fatalf("binding a candidate: %d %s", code, body)
	}

	if code, body := identityCall(t, mux, "POST", "/api/identity/confirm", `{"profile":"`+p.ID+`"}`); code != 200 {
		t.Fatalf("confirm: %d %s", code, body)
	} else if s := identityStateOf(t, body); s.Profiles[0].State != "confirmed" || s.Profiles[0].ConfirmedAt == nil {
		t.Fatalf("after confirm: %+v", s.Profiles[0])
	}

	// one profile, two providers: a binding is a provider id and a
	// profile id, so the same snapshot serves both
	for _, id := range []string{a.ID, b.ID} {
		if code, body := identityCall(t, mux, "POST", "/api/identity/bind", `{"provider":"`+id+`","profile":"`+p.ID+`"}`); code != 200 {
			t.Fatalf("bind %s: %d %s", id, code, body)
		}
	}
	_, body = identityCall(t, mux, "GET", "/api/identity", "")
	s = identityStateOf(t, body)
	if s.Bindings[a.ID] != p.ID || s.Bindings[b.ID] != p.ID {
		t.Fatalf("bindings = %+v", s.Bindings)
	}
	if got := s.Profiles[0].Bindings; len(got) != 2 || got[0] != a.ID || got[1] != b.ID {
		t.Fatalf("profile bindings = %v, want both in id order", got)
	}
	for _, pr := range s.Providers {
		if pr.ProfileID != p.ID {
			t.Errorf("provider %s presents %q", pr.ID, pr.ProfileID)
		}
	}

	if code, body := identityCall(t, mux, "POST", "/api/identity/unbind", `{"provider":"`+a.ID+`"}`); code != 200 {
		t.Fatalf("unbind: %d %s", code, body)
	} else if s := identityStateOf(t, body); s.Bindings[a.ID] != "" || s.Bindings[b.ID] != p.ID {
		t.Fatalf("after unbind: %+v", s.Bindings)
	}

	// removing takes the remaining binding with it, so no provider is
	// left pointing at an id that is gone
	if code, body := identityCall(t, mux, "POST", "/api/identity/remove", `{"profile":"`+p.ID+`"}`); code != 200 {
		t.Fatalf("remove: %d %s", code, body)
	} else {
		s := identityStateOf(t, body)
		if len(s.Profiles) != 0 || len(s.Bindings) != 0 {
			t.Fatalf("after remove: %+v / %+v", s.Profiles, s.Bindings)
		}
		for _, pr := range s.Providers {
			if pr.ProfileID != "" {
				t.Errorf("provider %s still presents %q", pr.ID, pr.ProfileID)
			}
		}
	}
}

// The page is given the closed projection a request presents, never the
// request a profile came from: a credential, a session, a window or a
// request id cannot reach it even when one was in the observed headers.
func TestIdentityStateSendsOnlyTheClosedProjection(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	h := http.Header{}
	h.Set("User-Agent", "claude-cli/2.1.296 (external, cli)")
	h.Set("X-App", "cli")
	h.Set("X-Stainless-Lang", "js")
	h.Set("X-Stainless-Os", "MacOS")
	// what a real request also carries, and a profile must never keep
	h.Set("Authorization", "Bearer sk-SECRET-VALUE")
	h.Set("X-Stainless-Retry-Count", "0")
	h.Set("X-Session-Id", "session-SECRET")
	h.Set("X-Request-Id", "request-SECRET")
	h.Set("X-Codex-Window-Id", "window-SECRET")
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	p, err := identity.ImportedCandidate(id, identity.ClientClaudeCode, h, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Add(p); err != nil {
		t.Fatal(err)
	}
	mux, _, _ := identityMux(t, true)
	_, body := identityCall(t, mux, "GET", "/api/identity", "")
	for _, secret := range []string{"SECRET", "Authorization", "X-Session-Id", "X-Request-Id", "X-Codex-Window-Id", "Retry-Count"} {
		if strings.Contains(body, secret) {
			t.Errorf("the state carries %q:\n%s", secret, body)
		}
	}
	s := identityStateOf(t, body)
	f := s.Profiles[0].Fingerprint
	if f.UserAgent != "claude-cli/2.1.296 (external, cli)" || f.Version != "2.1.296" {
		t.Errorf("fingerprint = %+v", f)
	}
	for name := range f.Headers {
		if !identity.HeaderAllowed(identity.ClientClaudeCode, name) {
			t.Errorf("fingerprint carries %q, which is not one claude_code keeps", name)
		}
	}
}

// A provider no profile would be sent on is marked so, with the reason,
// and binding to it is refused rather than saved and never used.
func TestIdentityRefusesProvidersNoProfileReaches(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	remote := provider.Provider{ID: "remote", Name: "Remote", Preset: provider.RemoteMagpiePreset,
		Chat: "http://127.0.0.1:9/v1", Key: "k1"}
	if err := provider.Save(remote); err != nil {
		t.Fatal(err)
	}
	p := codexCandidate(t)
	if err := identity.Confirm(p.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	mux, _, calls := identityMux(t, true)

	_, body := identityCall(t, mux, "GET", "/api/identity", "")
	s := identityStateOf(t, body)
	var got identityProviderJSON
	for _, pr := range s.Providers {
		if pr.ID == remote.ID {
			got = pr
		}
	}
	if got.Applicable || !strings.Contains(got.Why, "magpie service") {
		t.Fatalf("remote provider = %+v, want it marked inapplicable with a reason", got)
	}
	for _, c := range []struct{ what, path, body, says string }{
		{"bind", "/api/identity/bind", `{"provider":"remote","profile":"` + p.ID + `"}`, "magpie service"},
		{"arm", "/api/identity/arm", `{"provider":"remote","client":"codex_cli"}`, "magpie service"},
		{"no such provider", "/api/identity/arm", `{"provider":"nope","client":"codex_cli"}`, "nope"},
		{"no such client", "/api/identity/arm", `{"provider":"relay","client":"emacs"}`, "codex_cli"},
	} {
		code, body := identityCall(t, mux, "POST", c.path, c.body)
		if code == 200 {
			t.Errorf("%s: accepted (%d %s)", c.what, code, body)
			continue
		}
		if !strings.Contains(body, c.says) {
			t.Errorf("%s: %d %.160s, want it to name %q", c.what, code, body, c.says)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("the gateway was asked anyway: %v", *calls)
	}
	if bound, err := identity.Bindings(); err != nil || len(bound) != 0 {
		t.Errorf("bindings = %v (%v), want none", bound, err)
	}
}

// Arming and disarming go to the gateway that forwards the requests, and
// the state then shows the window with its client and expiry.
func TestIdentityArmGoesToTheGateway(t *testing.T) {
	identityHome(t)
	p := plainProvider(t, "relay", "Relay")
	mux, _, calls := identityMux(t, true)
	code, body := identityCall(t, mux, "POST", "/api/identity/arm", `{"provider":"relay","client":"codex_cli"}`)
	if code != 200 {
		t.Fatalf("arm: %d %s", code, body)
	}
	s := identityStateOf(t, body)
	if len(s.Armed) != 1 || s.Armed[0].Provider != p.ID || s.Armed[0].Client != "codex_cli" || s.Armed[0].ExpiresAt == nil {
		t.Fatalf("armed = %+v, want the window with its client and expiry", s.Armed)
	}
	if code, body := identityCall(t, mux, "POST", "/api/identity/disarm", `{"provider":"relay"}`); code != 200 {
		t.Fatalf("disarm: %d %s", code, body)
	} else if s := identityStateOf(t, body); len(s.Armed) != 0 {
		t.Fatalf("armed after disarm = %+v", s.Armed)
	}
	want := []string{"arm relay codex_cli", "disarm relay"}
	if len(*calls) != len(want) || (*calls)[0] != want[0] || (*calls)[1] != want[1] {
		t.Fatalf("gateway calls = %v, want %v", *calls, want)
	}
}

// A window belongs to the gateway, so with none running the page is told
// to start one rather than being shown a capture that holds nothing.
func TestIdentityCaptureNeedsAGateway(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	mux, _, calls := identityMux(t, false)
	_, body := identityCall(t, mux, "GET", "/api/identity", "")
	if s := identityStateOf(t, body); s.Gateway || len(s.Armed) != 0 {
		t.Fatalf("state = %+v, want no gateway and no windows", s)
	}
	for _, c := range [][2]string{
		{"/api/identity/arm", `{"provider":"relay","client":"codex_cli"}`},
		{"/api/identity/disarm", `{"provider":"relay"}`},
	} {
		code, body := identityCall(t, mux, "POST", c[0], c[1])
		if code == 200 || !strings.Contains(body, "no gateway runs here") {
			t.Errorf("%s with no gateway: %d %s", c[0], code, body)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("the gateway was asked while none runs: %v", *calls)
	}
}

// An older gateway answers the armed status with provider ids alone. The
// window is still shown, without a countdown, rather than being dropped
// or given an invented expiry.
func TestIdentityArmedFromAnOlderGateway(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	mux, armed, _ := identityMux(t, true)
	*armed = []gateway.ArmedIdentity{{Provider: "relay"}}
	_, body := identityCall(t, mux, "GET", "/api/identity", "")
	s := identityStateOf(t, body)
	if len(s.Armed) != 1 || s.Armed[0].Provider != "relay" {
		t.Fatalf("armed = %+v", s.Armed)
	}
	if s.Armed[0].Client != "" || s.Armed[0].ExpiresAt != nil {
		t.Errorf("armed = %+v, want no invented client or expiry", s.Armed[0])
	}
	if s.ArmedWhy != "" {
		t.Errorf("armedWhy = %q, want none: the status was read", s.ArmedWhy)
	}
}

// A gateway that cannot answer its armed status leaves the profiles
// listed and says why the windows are unknown, rather than failing the
// whole page or showing nothing armed as a fact.
func TestIdentityArmedUnreadableSaysWhy(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	codexCandidate(t)
	mux, _, _ := identityMux(t, true)
	armedIdentities = func(context.Context, string) ([]gateway.ArmedIdentity, error) {
		return nil, errors.New("gateway identity control: 404 Not Found: not found")
	}
	code, body := identityCall(t, mux, "GET", "/api/identity", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	s := identityStateOf(t, body)
	if len(s.Profiles) != 1 {
		t.Fatalf("profiles = %+v, want them listed anyway", s.Profiles)
	}
	if !strings.Contains(s.ArmedWhy, "404") || len(s.Armed) != 0 {
		t.Fatalf("armedWhy = %q, armed = %+v", s.ArmedWhy, s.Armed)
	}
}

// A binding whose profile is gone or unusable is an error the operator
// sees, never a quiet fallback to magpie's own identity.
func TestIdentityFailsClosedOnABrokenBinding(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	p := codexCandidate(t)
	if err := identity.Confirm(p.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	mux, _, _ := identityMux(t, true)
	if code, body := identityCall(t, mux, "POST", "/api/identity/bind", `{"provider":"relay","profile":"`+p.ID+`"}`); code != 200 {
		t.Fatalf("bind: %d %s", code, body)
	}
	// the profile goes while the binding stays, as an edited store could
	// leave it: HeaderFor must refuse rather than send magpie's own
	if err := identity.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind("relay", p.ID); err == nil {
		t.Error("bound to a profile that is gone")
	}
	if _, err := identity.HeaderFor("relay"); err != nil {
		t.Logf("a removed profile's binding is cleared with it: %v", err)
	}
	if bound, err := identity.Bindings(); err != nil || bound["relay"] != "" {
		t.Errorf("bindings = %v (%v), want relay cleared with its profile", bound, err)
	}
	if code, body := identityCall(t, mux, "POST", "/api/identity/confirm", `{"profile":"`+p.ID+`"}`); code == 200 || !strings.Contains(body, "no identity profile") {
		t.Errorf("confirming a profile that is gone: %d %s", code, body)
	}
	if code, body := identityCall(t, mux, "POST", "/api/identity/remove", `{"profile":"`+p.ID+`"}`); code == 200 || !strings.Contains(body, "no identity profile") {
		t.Errorf("removing a profile that is gone: %d %s", code, body)
	}
}
