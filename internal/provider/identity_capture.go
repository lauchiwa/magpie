package provider

import (
	"net/http"
	"time"

	"github.com/yetone/magpie/internal/identity"
)

// ObserveIdentity projects an explicitly armed, one-shot identity capture
// from a request that came through providerID. Capture is best-effort for
// the live request: a malformed candidate is kept out of the store, but it
// must not turn a successful upstream request into a gateway error.
func ObserveIdentity(providerID, client string, h http.Header) {
	_, _, _ = identity.Observe(providerID, identity.Client(client), h, time.Now().UTC())
}
