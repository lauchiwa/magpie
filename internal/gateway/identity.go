package gateway

import (
	"net/http"
	"sort"
	"time"

	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/provider"
)

// An identity capture window lives in the memory of the process that sees
// the requests, which is the gateway's: forwardOnce calls
// provider.ObserveIdentity there. `magpie identity arm` runs in a CLI
// process of its own, so arming in that process armed nothing the gateway
// would ever consult, and the window died with the command. These paths
// are how the CLI arms the gateway that is actually running.
//
// The window stays memory-only. It is not written to identities.json: an
// armed capture is consent to project the next matching request, and
// consent that outlives a restart is consent nobody granted for the
// requests that come after it.

// IdentityArmPath arms a one-shot capture on the running gateway
// (?provider=, &client=).
const IdentityArmPath = "/v1/magpie/identity/arm"

// IdentityDisarmPath revokes one before it is used (?provider=).
const IdentityDisarmPath = "/v1/magpie/identity/disarm"

// IdentityArmedPath lists the providers with a live window, so an
// operator can see that an arm took: a window is otherwise invisible.
const IdentityArmedPath = "/v1/magpie/identity/armed"

// identityOnlyHere refuses a caller that is not this machine.
//
// This is stricter than quotas or concurrency, which a magpie shared on
// the local network answers with its key. Arming is not a read: it is
// consent for this gateway to keep the identity fields of the next
// matching request, and it is granted at the machine magpie runs on. A
// shared gateway's key is held by other people's agents, and none of them
// may decide what this one captures.
func identityOnlyHere(w http.ResponseWriter, r *http.Request) bool {
	if local(r) {
		return true
	}
	writeError(w, provider.Chat, http.StatusForbidden,
		"magpie arms an identity capture for this machine alone, never for a request from elsewhere, even with a gateway key")
	return false
}

// identityTarget is the provider named in the query, checked as the CLI
// checks it: the gateway does not trust the caller's word that a profile
// would be sent, because a capture for an account or a plugin's provider
// would wait for a request that path never makes.
func identityTarget(w http.ResponseWriter, r *http.Request) (provider.Provider, bool) {
	id := r.URL.Query().Get("provider")
	if id == "" {
		writeError(w, provider.Chat, http.StatusBadRequest, "name the provider to capture for: ?provider=<id>")
		return provider.Provider{}, false
	}
	p, err := provider.Find(id)
	if err != nil {
		writeError(w, provider.Chat, http.StatusNotFound, err.Error())
		return provider.Provider{}, false
	}
	if why := p.IdentityApplies(); why != "" {
		writeError(w, provider.Chat, http.StatusBadRequest, p.Name+": "+why)
		return provider.Provider{}, false
	}
	return *p, true
}

// identityArm arms the one-shot capture in this process, where the
// forwarded requests are.
func (s *Server) identityArm(w http.ResponseWriter, r *http.Request) {
	if !identityOnlyHere(w, r) {
		return
	}
	p, ok := identityTarget(w, r)
	if !ok {
		return
	}
	client := identity.Client(r.URL.Query().Get("client"))
	if err := identity.Arm(p.ID, client, time.Now().UTC()); err != nil {
		writeError(w, provider.Chat, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"object": "identity.capture", "provider": p.ID, "name": p.Name,
		"client": string(client), "armed": true, "window": identity.CaptureWindow.String(),
	})
}

// identityDisarm revokes an armed capture. Disarming one that is not
// armed is not an error: the window may have been used or have expired,
// and the caller's intent — nothing armed for this provider — holds
// either way.
func (s *Server) identityDisarm(w http.ResponseWriter, r *http.Request) {
	if !identityOnlyHere(w, r) {
		return
	}
	p, ok := identityTarget(w, r)
	if !ok {
		return
	}
	identity.Disarm(p.ID)
	writeJSON(w, 200, map[string]any{
		"object": "identity.capture", "provider": p.ID, "name": p.Name, "armed": false,
	})
}

// identityArmed is which providers have a live window here. `data` keeps
// the original provider-id-only response for older clients; `details` adds
// the client and expiry that a GUI needs without exposing request headers.
func (s *Server) identityArmed(w http.ResponseWriter, r *http.Request) {
	if !identityOnlyHere(w, r) {
		return
	}
	captures := identity.ArmedCaptures(time.Now().UTC())
	sort.Slice(captures, func(i, j int) bool { return captures[i].Provider < captures[j].Provider })
	ids := make([]string, 0, len(captures))
	details := make([]map[string]any, 0, len(captures))
	for _, capture := range captures {
		ids = append(ids, capture.Provider)
		details = append(details, map[string]any{
			"provider":  capture.Provider,
			"client":    string(capture.Client),
			"expiresAt": capture.Expires.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": ids, "details": details})
}
