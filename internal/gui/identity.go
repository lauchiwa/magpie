package gui

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/provider"
)

// The page's side of outbound identity profiles (internal/identity). The
// store stays its own: nothing here writes a profile into providers.json,
// and providerJSON carries no fingerprint. A binding is a provider id and
// a profile id, so the two files keep their own shapes.
//
// What this layer deliberately does not do:
//
//   - It never shows the request a profile came from. Only the closed
//     projection is sent (identity.Fingerprint.Header): a credential, a
//     session, thread, request or window id was already dropped at capture,
//     and is dropped again here by sending the projection rather than
//     anything the page could read a raw header out of.
//   - It never decides for itself whether a profile would be sent.
//     Provider.IdentityApplies is the one answer, given to the page as
//     applicable and why, and checked again on every bind.
//   - It holds no capture window. An armed window lives in the memory of
//     the gateway that forwards the requests, reached over its local
//     control paths (internal/gateway/control.go), which is also what the
//     CLI uses.

// identityFingerprintJSON is the closed projection a request presents:
// what the profile sends, not what was observed.
type identityFingerprintJSON struct {
	UserAgent string            `json:"userAgent"`
	Version   string            `json:"version"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// identityProfileJSON is one profile as the page lists it. State is
// "candidate" or "confirmed": a candidate is sent to nobody until the
// operator confirms it, and cannot be bound.
type identityProfileJSON struct {
	ID          string                  `json:"id"`
	Client      string                  `json:"client"`
	Source      string                  `json:"source"`
	State       string                  `json:"state"`
	Fingerprint identityFingerprintJSON `json:"fingerprint"`
	CreatedAt   time.Time               `json:"createdAt"`
	ObservedAt  *time.Time              `json:"observedAt,omitempty"`
	ConfirmedAt *time.Time              `json:"confirmedAt,omitempty"`
	// Bindings are the providers presenting this profile, in id order.
	Bindings []string `json:"bindings"`
}

// identityProviderJSON is a provider as the identity panel offers it.
// Applicable is Provider.IdentityApplies: Why says what to tell the
// operator when a profile bound here would never be sent.
type identityProviderJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Icon       string `json:"icon,omitempty"`
	Applicable bool   `json:"applicable"`
	Why        string `json:"why,omitempty"`
	ProfileID  string `json:"profileId,omitempty"`
}

// identityArmedJSON is a live capture window. ExpiresAt is absent from an
// older gateway, which answered with provider ids alone: the page then
// shows it armed with no countdown rather than inventing one.
type identityArmedJSON struct {
	Provider  string     `json:"provider"`
	Client    string     `json:"client,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// identityStateJSON is the whole page state, answered by the read and by
// every change: a change gives the state back so the page never stitches
// one together from its own guesses while another refresh is in flight.
type identityStateJSON struct {
	Profiles  []identityProfileJSON  `json:"profiles"`
	Bindings  map[string]string      `json:"bindings"`
	Providers []identityProviderJSON `json:"providers"`
	Armed     []identityArmedJSON    `json:"armed"`
	// Gateway says whether a capture can be armed at all: a window needs
	// the gateway that forwards the requests, and ArmedWhy says why the
	// windows could not be read when they could not.
	Gateway  bool   `json:"gateway"`
	ArmedWhy string `json:"armedWhy,omitempty"`
	// Window is how long an armed capture waits, as the page says so
	// before arming one.
	Window string `json:"window"`
}

// identityControlTimeout bounds a control call to the local gateway, so a
// gateway that accepts the connection and then answers nothing fails the
// one call rather than holding the page's request open. A var so a test
// can shorten it instead of waiting out the real bound.
var identityControlTimeout = 10 * time.Second

// armedIdentities is the control client, which tests stand in for.
var armedIdentities = gateway.ArmedIdentities

// armIdentity and disarmIdentity likewise.
var (
	armIdentity    = gateway.ArmIdentity
	disarmIdentity = gateway.DisarmIdentity
)

// gatewayRunning says whether a gateway answers at the local address.
var gatewayRunning = gateway.Running

// errNoGateway is the one error that a missing gateway gives every
// capture call, so the page says the same thing in each.
var errNoGateway = errors.New("no gateway runs here, and a capture window belongs to the gateway that forwards the requests: start one (the app, magpie web or magpie serve), then arm it")

func identityState() (identityStateJSON, error) {
	// one capture for profiles, bindings and providers (lifecycle_fork.go)
	view, err := provider.ReadIdentityView(true)
	if err != nil {
		return identityStateJSON{}, err
	}
	profiles, bound := view.Snapshot.Profiles(), view.Snapshot.Bindings()
	s := identityStateJSON{
		Profiles:  []identityProfileJSON{},
		Bindings:  map[string]string{},
		Providers: []identityProviderJSON{},
		Armed:     []identityArmedJSON{},
		Window:    identity.CaptureWindow.String(),
	}
	byProfile := map[string][]string{}
	for providerID, profileID := range bound {
		s.Bindings[providerID] = profileID
		byProfile[profileID] = append(byProfile[profileID], providerID)
	}
	for _, p := range profiles {
		state := "candidate"
		if p.ConfirmedAt != nil {
			state = "confirmed"
		}
		bindings := byProfile[p.ID]
		sort.Strings(bindings)
		if bindings == nil {
			bindings = []string{}
		}
		// the closed projection, never the observed request: Header is
		// what a request would present, so no other field can ride along
		head := p.Fingerprint.Header(p.Client)
		shown := map[string]string{}
		for name, values := range head {
			if name == "User-Agent" || name == "Version" || len(values) == 0 {
				continue
			}
			shown[name] = values[0]
		}
		if len(shown) == 0 {
			shown = nil
		}
		s.Profiles = append(s.Profiles, identityProfileJSON{
			ID: p.ID, Client: string(p.Client), Source: string(p.Source), State: state,
			Fingerprint: identityFingerprintJSON{
				UserAgent: p.Fingerprint.UserAgent, Version: p.Fingerprint.Version, Headers: shown,
			},
			CreatedAt: p.CreatedAt, ObservedAt: p.ObservedAt, ConfirmedAt: p.ConfirmedAt,
			Bindings: bindings,
		})
	}
	for _, p := range view.Providers() {
		why := p.IdentityApplies()
		s.Providers = append(s.Providers, identityProviderJSON{
			ID: p.ID, Name: cmp.Or(p.Name, p.ID), Icon: p.Icon,
			Applicable: why == "", Why: why, ProfileID: s.Bindings[p.ID],
		})
	}
	s.Gateway = gatewayRunning()
	if s.Gateway {
		ctx, cancel := context.WithTimeout(context.Background(), identityControlTimeout)
		defer cancel()
		armed, err := armedIdentities(ctx, gateway.URL())
		if err != nil {
			// the profiles still list; only the windows are unknown, and
			// the page says which of the two it is
			s.ArmedWhy = err.Error()
		}
		for _, a := range armed {
			s.Armed = append(s.Armed, identityArmedJSON{
				Provider: a.Provider, Client: string(a.Client), ExpiresAt: a.Expires,
			})
		}
		slices.SortFunc(s.Armed, func(a, b identityArmedJSON) int {
			switch {
			case a.Provider < b.Provider:
				return -1
			case a.Provider > b.Provider:
				return 1
			}
			return 0
		})
	}
	return s, nil
}

// identityCaptureTarget is the provider a capture is asked for, checked
// here as the gateway checks it again: a window armed for an account or a
// plugin's provider would wait for a request that path never makes.
func identityCaptureTarget(id string) (provider.Provider, error) {
	p, err := provider.Find(id)
	if err != nil {
		return provider.Provider{}, err
	}
	if why := p.IdentityApplies(); why != "" {
		return provider.Provider{}, errors.New(cmp.Or(p.Name, p.ID) + ": " + why)
	}
	return *p, nil
}

func identityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/identity", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-store")
		s, err := identityState()
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, s)
	})
	// Arming asks the gateway that forwards the requests, which is the
	// only process a window can live in. It is never armed here.
	mux.HandleFunc("POST /api/identity/arm", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ Provider, Client string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		p, err := identityCaptureTarget(in.Provider)
		if err != nil {
			fail(rw, err)
			return
		}
		client := identity.Client(in.Client)
		if !client.Valid() {
			fail(rw, errors.New("no such client "+in.Client+": codex_cli or claude_code"))
			return
		}
		if !gatewayRunning() {
			fail(rw, errNoGateway)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), identityControlTimeout)
		defer cancel()
		if err := armIdentity(ctx, gateway.URL(), p.ID, client); err != nil {
			fail(rw, err)
			return
		}
		answerIdentity(rw)
	})
	mux.HandleFunc("POST /api/identity/disarm", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ Provider string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		p, err := provider.Find(in.Provider)
		if err != nil {
			fail(rw, err)
			return
		}
		if !gatewayRunning() {
			fail(rw, errNoGateway)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), identityControlTimeout)
		defer cancel()
		if err := disarmIdentity(ctx, gateway.URL(), p.ID); err != nil {
			fail(rw, err)
			return
		}
		answerIdentity(rw)
	})
	// Confirming is the operator action that lets a request present a
	// profile: a candidate is sent to nobody before it.
	mux.HandleFunc("POST /api/identity/confirm", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ Profile string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		if err := identity.Confirm(in.Profile, time.Now().UTC()); err != nil {
			fail(rw, err)
			return
		}
		answerIdentity(rw)
	})
	// Binding is refused for a provider a profile would never be sent on,
	// rather than saved and silently never used.
	mux.HandleFunc("POST /api/identity/bind", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ Provider, Profile string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		p, err := identityCaptureTarget(in.Provider)
		if err != nil {
			fail(rw, err)
			return
		}
		if err := identity.Bind(p.ID, in.Profile); err != nil {
			fail(rw, err)
			return
		}
		answerIdentity(rw)
	})
	mux.HandleFunc("POST /api/identity/unbind", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ Provider string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		// a provider since removed can still hold a binding: unbinding it
		// is exactly what clears that, so the id is taken as it is
		if err := identity.Unbind(in.Provider); err != nil {
			fail(rw, err)
			return
		}
		answerIdentity(rw)
	})
	// Removing a profile removes every binding to it, so no provider is
	// left bound to an id that is gone.
	mux.HandleFunc("POST /api/identity/remove", func(rw http.ResponseWriter, r *http.Request) {
		var in struct{ Profile string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		if err := identity.Remove(in.Profile); err != nil {
			fail(rw, err)
			return
		}
		answerIdentity(rw)
	})
}

// answerIdentity gives the state back after a change.
func answerIdentity(rw http.ResponseWriter) {
	s, err := identityState()
	if err != nil {
		fail(rw, err)
		return
	}
	writeJSON(rw, s)
}
