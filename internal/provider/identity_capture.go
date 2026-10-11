package provider

import (
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/identity"
)

// identityClients maps the client ids the gateway recognizes a request by
// (ClientCodex, ClientClaudeCode here) to the identity package's own.
//
// The two vocabularies are not the same word: this package says "codex"
// and "claude-code", identity says "codex_cli" and "claude_code". Handing
// one straight to the other named a client identity.Client never matches,
// so an armed capture was never consumed and no request was ever
// projected. Anything not in this map is a client no profile is kept for.
var identityClients = map[string]identity.Client{
	ClientCodex:      identity.ClientCodex,
	ClientClaudeCode: identity.ClientClaudeCode,
}

// ObserveIdentity projects an explicitly armed, one-shot identity capture
// from a request that came through providerID. Capture is best-effort for
// the live request: a malformed candidate is kept out of the store, but it
// must not turn a successful upstream request into a gateway error.
func ObserveIdentity(providerID, client string, h http.Header) {
	known, ok := identityClients[client]
	if !ok {
		return
	}
	_, _, _ = identity.Observe(providerID, known, h, time.Now().UTC())
}
