package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/identity"
)

// codexProfile is a confirmed Codex profile, as one imported from a real
// request and confirmed by the operator.
func codexProfile(t *testing.T) identity.Profile {
	t.Helper()
	h := http.Header{
		"User-Agent": {"codex_cli_rs/0.159.2 (Mac OS 26.6.0; arm64) Apple_Terminal/455"},
		"Originator": {"codex_cli_rs"},
		"Version":    {"0.159.2"},
	}
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p, err := identity.Candidate(id, identity.ClientCodex, h, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Second)
	p.ConfirmedAt = &at
	return p
}

// bindIdentity has the lookup answer for providerID alone, so the test
// states the binding instead of writing the store.
func bindIdentity(t *testing.T, providerID string, p identity.Profile) {
	t.Helper()
	was := identityHeaders
	t.Cleanup(func() { identityHeaders = was })
	identityHeaders = func(id string) (http.Header, error) {
		if id != providerID {
			return nil, nil
		}
		return p.Fingerprint.Header(p.Client), nil
	}
}

// A provider bound to an identity profile presents it on every request
// magpie makes to it: the forwarded call's signing, the endpoint test and
// the model list. A relay that serves only its own clients turns magpie's
// own User-Agent away, so a test and a model list that still carried it
// would report the provider broken while the agent's requests worked.
func TestBoundIdentityRidesOnEveryRequest(t *testing.T) {
	profile := codexProfile(t)
	for _, entry := range []string{"sign", "list", "endpoints"} {
		t.Run(entry, func(t *testing.T) {
			detectHome(t)
			var got http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			p := Provider{ID: "relay", Name: "Relay", Key: "sk-test", Chat: srv.URL + "/v1",
				Models: []string{"gpt-5"}}
			bindIdentity(t, p.ID, profile)
			ctx := context.Background()
			switch entry {
			case "sign":
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat/completions", nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := p.Sign(ctx, req, Chat, nil); err != nil {
					t.Fatal(err)
				}
				got = req.Header.Clone()
			case "list":
				if _, err := p.Fetch(ctx); err != nil {
					t.Fatalf("model list: %v", err)
				}
			case "endpoints":
				p.Test(ctx)
			}
			if ua := got.Get("User-Agent"); ua != profile.Fingerprint.UserAgent {
				t.Errorf("User-Agent = %q, want the bound profile's %q", ua, profile.Fingerprint.UserAgent)
			}
			if o := got.Get("Originator"); o != "codex_cli_rs" {
				t.Errorf("Originator = %q, want the bound profile's", o)
			}
			if v := got.Get("Version"); v != "0.159.2" {
				t.Errorf("Version = %q, want the bound profile's", v)
			}
			if n := len(got.Values("User-Agent")); n != 1 {
				t.Errorf("User-Agent was sent %d times, want once", n)
			}
		})
	}
}

// A bound profile owns its closed identity fields, even when the provider
// has an open user header with the same name. This prevents a mixed
// fingerprint from being constructed accidentally.
func TestABoundProfileOwnsItsClosedFields(t *testing.T) {
	profile := codexProfile(t)
	for _, entry := range []string{"sign", "list"} {
		t.Run(entry, func(t *testing.T) {
			detectHome(t)
			var got http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			p := Provider{ID: "relay", Name: "Relay", Key: "sk-test", Chat: srv.URL + "/v1",
				Models: []string{"gpt-5"},
				// typed in lower case, as a browser's devtools show it
				Headers: map[string]string{"user-agent": "mine/9.9"}}
			bindIdentity(t, p.ID, profile)
			ctx := context.Background()
			if entry == "sign" {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat/completions", nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := p.Sign(ctx, req, Chat, nil); err != nil {
					t.Fatal(err)
				}
				got = req.Header.Clone()
			} else if _, err := p.Fetch(ctx); err != nil {
				t.Fatalf("model list: %v", err)
			}
			var uas []string
			for k, v := range got {
				if http.CanonicalHeaderKey(k) == "User-Agent" {
					uas = append(uas, v...)
				}
			}
			if len(uas) != 1 || uas[0] != profile.Fingerprint.UserAgent {
				t.Errorf("User-Agent = %q, want the profile's alone", uas)
			}
			// the profile owns the closed identity field; the rest of
			// the profile still rides too
			if o := got.Get("Originator"); o != "codex_cli_rs" {
				t.Errorf("Originator = %q, want the profile's", o)
			}
		})
	}
}

// A binding that cannot be honoured fails the request and the model list.
// Forwarding without the identity the operator pinned would reach the
// vendor as magpie's own client while reporting success.
func TestABrokenBindingFailsTheRequest(t *testing.T) {
	detectHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	p := Provider{ID: "relay", Name: "Relay", Key: "sk-test", Chat: srv.URL + "/v1", Models: []string{"gpt-5"}}

	was := identityHeaders
	t.Cleanup(func() { identityHeaders = was })
	identityHeaders = func(string) (http.Header, error) {
		return nil, errorf("identity profile %q is bound to %q but is not confirmed", "identity_0123456789abcdef", "relay")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Sign(context.Background(), req, Chat, nil); err == nil {
		t.Fatal("a request was signed under magpie's own client while a profile was bound")
	}
	if _, err := p.Fetch(context.Background()); err == nil {
		t.Fatal("a model list was asked under magpie's own client while a profile was bound")
	}
}

// A signed-in account and a plugin's provider send their own requests, so
// a profile bound to one would be read and never sent. Saying so at the
// binding is better than appearing to work.
func TestIdentityDoesNotApplyToAccountsOrPlugins(t *testing.T) {
	if why := (Provider{ID: "relay", Key: "k"}).IdentityApplies(); why != "" {
		t.Errorf("a plain key+URL provider was refused: %s", why)
	}
	acct := Provider{ID: "codex", Account: &Account{User: "someone@example.com"}}
	if why := acct.IdentityApplies(); why == "" {
		t.Error("an account took a binding, though its own sign-in signs its requests")
	}
	if acct.identityApplies() {
		t.Error("an account would have had a profile applied")
	}
}

// An account's request never reads the binding: Sign returns at the
// account's own signing, before the profile.
func TestAnAccountIgnoresABoundProfile(t *testing.T) {
	detectHome(t)
	read := false
	was := identityHeaders
	t.Cleanup(func() { identityHeaders = was })
	identityHeaders = func(string) (http.Header, error) {
		read = true
		return http.Header{"User-Agent": {"codex_cli_rs/0.159.2"}}, nil
	}
	p := Provider{ID: "codex", Name: "Codex", Account: &Account{User: "someone@example.com",
		sign: func(context.Context, *http.Request, []byte) error { return nil }}}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.invalid/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Sign(context.Background(), req, Responses, nil); err != nil {
		t.Fatal(err)
	}
	if read {
		t.Error("an account's request read an identity binding, which it can never send")
	}
	if req.Header.Get("User-Agent") != "" {
		t.Errorf("User-Agent = %q, want the account's own signing to decide it", req.Header.Get("User-Agent"))
	}
}

// A provider bound to nothing is unchanged: no identity header is added,
// so every existing provider keeps sending exactly what it sent before.
func TestAnUnboundProviderIsUnchanged(t *testing.T) {
	detectHome(t)
	p := Provider{ID: "relay", Name: "Relay", Key: "sk-test", Chat: "https://example.invalid/v1"}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.invalid/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Sign(context.Background(), req, Chat, nil); err != nil {
		t.Fatal(err)
	}
	if ua := req.Header.Get("User-Agent"); ua != "" {
		t.Errorf("User-Agent = %q, want none added for an unbound provider", ua)
	}
	if o := req.Header.Get("Originator"); o != "" {
		t.Errorf("Originator = %q, want none added for an unbound provider", o)
	}
}

// An identities.json a crash left all zero (#1505) does not stop a plain
// provider's requests. Sign reads the store for every one, bound or not, so
// before it was read from its last good generation every key+URL provider
// failed with "invalid character '\x00'". Through the real store, not the
// lookup a test replaces.
func TestAZeroedIdentityStoreLeavesProvidersSigning(t *testing.T) {
	detectHome(t)
	profile := codexProfile(t)
	if err := identity.Add(profile); err != nil {
		t.Fatal(err)
	}
	// one more write after the profile is there, so the last good
	// generation holds it
	if err := identity.Unbind("nobody"); err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind("other", profile.ID); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(identity.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity.Path(), make([]byte, st.Size()), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Provider{ID: "relay", Name: "Relay", Key: "sk-test", Chat: "https://example.invalid/v1"}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.invalid/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Sign(context.Background(), req, Chat, nil); err != nil {
		t.Fatalf("an unbound provider over a zeroed identities.json: %v", err)
	}
	if ua := req.Header.Get("User-Agent"); ua != "" {
		t.Errorf("User-Agent = %q, want none for an unbound provider", ua)
	}
}
