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

// identityRealControl points the API at a fake gateway over the real
// control client (internal/gateway/control.go), the one the CLI uses: no
// stand-in, so what the page gets is what that client made of the
// gateway's own answer. handler is the gateway; MAGPIE_ADDR is how
// gateway.URL finds it. Returns the API with nothing stood in for.
func identityRealControl(t *testing.T, handler http.HandlerFunc) *http.ServeMux {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// what gateway.ServedBy asks to know a magpie answers here
		if r.URL.Path == "/" {
			writeJSON(rw, map[string]any{"name": "magpie", "version": "test", "window": false})
			return
		}
		handler(rw, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MAGPIE_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	mux := http.NewServeMux()
	identityRoutes(mux)
	return mux
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

// The page and the CLI ask one gateway over one client. Here nothing is
// stood in for: the API's arm, armed read and disarm go through
// gateway.ArmIdentity and friends to a fake gateway, so the query string
// the gateway is asked with, and the state made of its answer, are the
// real ones.
func TestIdentityControlGoesThroughTheRealClient(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	var asked []string
	expires := time.Now().UTC().Add(identity.CaptureWindow).Format(time.RFC3339Nano)
	armed := false
	mux := identityRealControl(t, func(rw http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case gateway.IdentityArmPath:
			armed = true
			writeJSON(rw, map[string]any{"data": []string{"relay"}})
		case gateway.IdentityDisarmPath:
			armed = false
			writeJSON(rw, map[string]any{"data": []string{}})
		case gateway.IdentityArmedPath:
			if !armed {
				writeJSON(rw, map[string]any{"data": []string{}, "details": []any{}})
				return
			}
			writeJSON(rw, map[string]any{
				"data":    []string{"relay"},
				"details": []any{map[string]string{"provider": "relay", "client": "codex_cli", "expiresAt": expires}},
			})
		default:
			http.NotFound(rw, r)
		}
	})

	code, body := identityCall(t, mux, "POST", "/api/identity/arm", `{"provider":"relay","client":"codex_cli"}`)
	if code != 200 {
		t.Fatalf("arm: %d %s", code, body)
	}
	s := identityStateOf(t, body)
	if !s.Gateway {
		t.Error("a gateway answers at MAGPIE_ADDR, and the state says none does")
	}
	if len(s.Armed) != 1 || s.Armed[0].Provider != "relay" || s.Armed[0].Client != "codex_cli" || s.Armed[0].ExpiresAt == nil {
		t.Fatalf("armed = %+v, want the window the gateway reported", s.Armed)
	}
	if s.ArmedWhy != "" {
		t.Errorf("armedWhy = %q, want none", s.ArmedWhy)
	}
	if code, body := identityCall(t, mux, "POST", "/api/identity/disarm", `{"provider":"relay"}`); code != 200 {
		t.Fatalf("disarm: %d %s", code, body)
	} else if s := identityStateOf(t, body); len(s.Armed) != 0 {
		t.Fatalf("armed after disarm = %+v", s.Armed)
	}
	// the gateway is asked for this provider and this client, by the names
	// its own control paths use
	want := []string{
		"POST " + gateway.IdentityArmPath + "?client=codex_cli&provider=relay",
		"GET " + gateway.IdentityArmedPath + "?",
		"POST " + gateway.IdentityDisarmPath + "?provider=relay",
		"GET " + gateway.IdentityArmedPath + "?",
	}
	if len(asked) != len(want) {
		t.Fatalf("the gateway was asked %v, want %v", asked, want)
	}
	for i := range want {
		if asked[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, asked[i], want[i])
		}
	}
}

// A gateway that refuses an arm or a disarm is reported with what it said.
// The page's own state is not moved by a refusal: nothing is armed, and
// the profiles are the ones that were there.
func TestIdentityControlRefusalIsSaidAndChangesNothing(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	p := codexCandidate(t)
	mux := identityRealControl(t, func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case gateway.IdentityArmedPath:
			writeJSON(rw, map[string]any{"data": []string{}, "details": []any{}})
		default:
			rw.WriteHeader(http.StatusBadRequest)
			writeJSON(rw, map[string]any{"error": map[string]string{"message": "another capture is already armed for relay"}})
		}
	})
	for _, c := range [][2]string{
		{"/api/identity/arm", `{"provider":"relay","client":"codex_cli"}`},
		{"/api/identity/disarm", `{"provider":"relay"}`},
	} {
		code, body := identityCall(t, mux, "POST", c[0], c[1])
		if code == 200 {
			t.Errorf("%s: accepted a refusal as success (%d %s)", c[0], code, body)
		}
		if !strings.Contains(body, "already armed") {
			t.Errorf("%s: %d %.200s, want the gateway's own words", c[0], code, body)
		}
	}
	_, body := identityCall(t, mux, "GET", "/api/identity", "")
	s := identityStateOf(t, body)
	if len(s.Armed) != 0 {
		t.Errorf("armed = %+v after two refusals", s.Armed)
	}
	if len(s.Profiles) != 1 || s.Profiles[0].ID != p.ID || s.Profiles[0].State != "candidate" {
		t.Errorf("profiles = %+v, want the candidate untouched", s.Profiles)
	}
}

// A gateway that takes the connection and then answers nothing fails the
// one call it was asked, rather than holding the page's request open until
// the browser gives up. The bound is identityControlTimeout, shortened
// here: the point is that there is one, not how long it is.
func TestIdentityControlDoesNotWaitForever(t *testing.T) {
	identityHome(t)
	plainProvider(t, "relay", "Relay")
	done := make(chan struct{})
	reached := make(chan string, 4)
	mux := identityRealControl(t, func(rw http.ResponseWriter, r *http.Request) {
		reached <- r.URL.Path
		select {
		case <-done:
		case <-r.Context().Done():
		}
	})
	// Released after the server is made, so that this cleanup runs before
	// the server's: httptest's Close waits for a handler still in flight,
	// and a call the page left behind is held by one of these. Registered
	// the other way round, an unbounded call deadlocks the teardown instead
	// of failing the test.
	t.Cleanup(func() { close(done) })
	old := identityControlTimeout
	identityControlTimeout = 150 * time.Millisecond
	t.Cleanup(func() { identityControlTimeout = old })

	// room on both sides: the call must outlast the timeout it is bounded
	// by, and finish well inside a bound no loaded machine reaches. The
	// wait is the test's own, so an unbounded call fails here instead of
	// hanging until the whole package times out.
	start := time.Now()
	code, body, ok := identityCallBounded(t, mux, "POST", "/api/identity/arm", `{"provider":"relay","client":"codex_cli"}`)
	took := time.Since(start)
	if !ok {
		t.Fatalf("arm did not return in %v: the control call is not bounded", identityPatience)
	}
	if code == 200 {
		t.Fatalf("arm: a gateway that never answered reported success (%d %s)", code, body)
	}
	if !strings.Contains(body, "context deadline exceeded") && !strings.Contains(body, "Client.Timeout") {
		t.Errorf("arm: %d %.200s, want the timeout said", code, body)
	}
	if took < identityControlTimeout {
		t.Errorf("arm returned in %v, before its own %v bound: it was not the timeout that ended it", took, identityControlTimeout)
	}
	if took > 20*time.Second {
		t.Errorf("arm took %v: the bound did not hold", took)
	}
	if got := <-reached; got != gateway.IdentityArmPath {
		t.Errorf("the gateway was asked %q", got)
	}

	// the read is bounded too: the profiles still answer, with the windows
	// unknown and said to be
	start = time.Now()
	code, body, ok = identityCallBounded(t, mux, "GET", "/api/identity", "")
	took = time.Since(start)
	if !ok {
		t.Fatalf("the read did not return in %v: the armed read is not bounded", identityPatience)
	}
	if code != 200 {
		t.Fatalf("the read failed whole over an unreadable window: %d %s", code, body)
	}
	s := identityStateOf(t, body)
	if s.ArmedWhy == "" || len(s.Armed) != 0 {
		t.Errorf("armedWhy = %q, armed = %+v, want the windows unknown and said", s.ArmedWhy, s.Armed)
	}
	if took < identityControlTimeout || took > identityPatience {
		t.Errorf("the read took %v, bound %v", took, identityControlTimeout)
	}
}

// identityPatience is how long a bounded call is waited for before the
// test calls it unbounded. Far above identityControlTimeout as the test
// shortens it, and far below the package's own timeout, so neither side is
// a wall-clock guess a loaded machine can cross.
const identityPatience = 5 * time.Second

// identityCallBounded is identityCall with a wait of its own: ok is false
// when the handler had not returned in identityPatience, which is a call
// that is not bounded rather than a slow one. The goroutine is left to the
// handler's own end (the fake gateway is released on cleanup), so nothing
// here outlives the test still running.
func identityCallBounded(t *testing.T, mux *http.ServeMux, method, path, body string) (int, string, bool) {
	t.Helper()
	type reply struct {
		code int
		body string
	}
	out := make(chan reply, 1)
	go func() {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		out <- reply{w.Code, w.Body.String()}
	}()
	select {
	case got := <-out:
		return got.code, got.body, true
	case <-time.After(identityPatience):
		return 0, "", false
	}
}
