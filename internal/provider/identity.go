package provider

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/identity"
)

// A provider can be bound to an identity profile: a frozen snapshot of one
// coding agent's outbound fingerprint, which an operator confirmed
// (internal/identity). A relay that serves only the agents its plan is
// sold for tells them by those headers, and magpie's own requests — a
// connectivity test, a model list, a balance read — carry magpie's
// User-Agent, so they are turned away while the agent's own are served.
//
// Where a profile applies, and where it deliberately does not:
//
//   - It applies to a plain key+URL provider, on every request Sign gives
//     auth to: forwarded calls, probes, decisions, drawing, video and
//     embeddings.
//   - It does not apply to a signed-in account, nor to a plugin's
//     provider. Sign returns early for both (the account's own sign-in
//     does the signing), so a profile would be read and never sent. Saying
//     so at the binding is better than appearing to work: identityApplies.
//
// The profile is applied after the user's own Headers. Its closed fields
// therefore cannot be combined with a manually typed value into a mixed
// fingerprint; an explicit binding remains the source of truth.

// identityApplies says whether a profile bound to p would be sent. A
// signed-in account and a plugin's provider sign their own requests, so
// Sign never reaches the profile for them.
func (p Provider) identityApplies() bool {
	return p.Account == nil && !p.IsPlugin() && !p.IsOpenCode() && !p.IsCline() &&
		!p.IsKilo() && !p.IsRemoteMagpie() && !p.DecideOnly()
}

// identityListHeaders is p's model-list headers with its bound identity
// profile presented, which the list fetches ask with (fetchOne). The list
// is asked over HTTP by catalog, which takes headers as a map rather than
// a request, so the profile is merged here instead of in Sign.
//
// The user's own headers go in first, then the confirmed profile owns
// its closed fields. A binding that cannot be honoured fails the fetch:
// a list asked under magpie's own User-Agent is exactly what a relay that
// serves only its own clients turns away, and reporting "no models" for
// that would hide the cause.
func (p Provider) identityListHeaders() (map[string]string, error) {
	base := p.listHeaders()
	if !p.identityApplies() {
		return base, nil
	}
	h, err := identityHeaders(p.ID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.Name, err)
	}
	if len(h) == 0 {
		return base, nil
	}
	// keep the existing user headers first; the profile owns its closed
	// identity fields when there is a binding
	out := make(map[string]string, len(base)+len(h))
	for k, v := range base {
		out[k] = v
	}
	for name, values := range h {
		if len(values) > 0 {
			for had := range out {
				if strings.EqualFold(had, name) {
					delete(out, had)
				}
			}
			out[name] = values[0]
		}
	}
	return out, nil
}

// IdentityApplies says why a profile bound to p would not be sent ("" when
// it would). The editor and the CLI say this before a binding is made,
// rather than letting a request look signed as a client it never was.
func (p Provider) IdentityApplies() string {
	if p.Account != nil {
		return "a signed-in account is asked as its own client already: its sign-in signs its requests"
	}
	if p.IsPlugin() {
		return "a plugin's provider sends its own requests: bind the identity in the plugin, not here"
	}
	if p.IsOpenCode() || p.IsCline() || p.IsKilo() {
		return "this provider already presents its own client identity"
	}
	if p.IsRemoteMagpie() || p.DecideOnly() {
		return "this provider is a magpie service, not an ordinary upstream API provider"
	}
	return ""
}

// identityHeaders is how p's bound profile is looked up; tests replace it.
var identityHeaders = identity.HeaderFor

// putIdentity presents p's bound identity profile on req, after the
// user's own Headers.
//
// A binding that cannot be honoured fails the request. A profile the
// operator pinned and magpie then left off would reach the vendor as
// magpie's own User-Agent while reporting success, which is the failure
// this feature exists to prevent.
func (p Provider) putIdentity(req *http.Request) error {
	if !p.identityApplies() {
		return nil
	}
	h, err := identityHeaders(p.ID)
	if err != nil {
		return fmt.Errorf("%s: %w", p.Name, err)
	}
	for name, values := range h {
		if len(values) == 0 {
			continue
		}
		// PutUserHeader so the name goes as the profile holds it, in place
		// of the same header in any other case: sent once, the profile's.
		catalog.PutUserHeader(req.Header, name, values[0])
	}
	return nil
}
