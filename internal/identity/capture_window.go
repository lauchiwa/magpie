package identity

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// CaptureWindow is how long an explicitly armed capture waits for a
// matching request before expiring. Consent is memory-only: it disappears
// on restart and after its one matching request is observed.
const CaptureWindow = 5 * time.Minute

type armedCapture struct {
	client  Client
	expires time.Time
}

// ArmedCapture is the safe control-plane view of a live capture window. It
// contains only the client and expiry; the raw request and its headers are
// never retained by the capture window.
type ArmedCapture struct {
	Provider string
	Client   Client
	Expires  time.Time
}

var captures struct {
	sync.Mutex
	byProvider map[string]armedCapture
}

// Arm explicitly permits one matching request through providerID to be
// projected to a candidate profile. The arm expires after CaptureWindow,
// does not survive a restart, and is consumed by the first matching
// request. It never retains the raw request.
func Arm(providerID string, client Client, now time.Time) error {
	if providerID == "" {
		return fmt.Errorf("a capture needs a provider")
	}
	if !client.Valid() {
		return fmt.Errorf("no such client %q: codex_cli or claude_code", client)
	}
	captures.Lock()
	defer captures.Unlock()
	if captures.byProvider == nil {
		captures.byProvider = map[string]armedCapture{}
	}
	captures.byProvider[providerID] = armedCapture{client: client, expires: now.Add(CaptureWindow)}
	return nil
}

// Observe consumes an armed capture only when this provider receives the
// requested client. It stores only the projected, closed identity fields as
// an unconfirmed candidate; raw request data is discarded.
//
// The caller has already identified the client from the inbound request.
// A mismatch does not consume consent, so unrelated traffic cannot steal
// the one-shot capture. The window is consumed under the captures mutex,
// and the candidate stored after it is released: Add waits for
// lifecycle's lock, and Arm, Disarm and ArmedCaptures must not wait on it.
func Observe(providerID string, client Client, h http.Header, now time.Time) (Profile, bool, error) {
	if !takeCapture(providerID, client, now) {
		return Profile{}, false, nil
	}
	id, err := NewID()
	if err != nil {
		return Profile{}, true, err
	}
	p, err := Candidate(id, client, h, now, now)
	if err != nil {
		return Profile{}, true, err
	}
	if err := Add(p); err != nil {
		return Profile{}, true, err
	}
	return p, true, nil
}

// takeCapture consumes providerID's armed window when it is live and for
// client; an expired one is removed.
func takeCapture(providerID string, client Client, now time.Time) bool {
	captures.Lock()
	defer captures.Unlock()
	armed, ok := captures.byProvider[providerID]
	if !ok {
		return false
	}
	if now.After(armed.expires) {
		delete(captures.byProvider, providerID)
		return false
	}
	if armed.client != client {
		return false
	}
	delete(captures.byProvider, providerID)
	return true
}

// Disarm revokes an armed capture before it is used. It is safe to call
// when none is armed.
func Disarm(providerID string) {
	captures.Lock()
	delete(captures.byProvider, providerID)
	captures.Unlock()
}

// ArmedCaptures returns the live windows for control-plane status. Expired
// windows are removed here too. It contains no request data or headers.
func ArmedCaptures(now time.Time) []ArmedCapture {
	captures.Lock()
	defer captures.Unlock()
	out := make([]ArmedCapture, 0, len(captures.byProvider))
	for providerID, armed := range captures.byProvider {
		if now.After(armed.expires) {
			delete(captures.byProvider, providerID)
			continue
		}
		out = append(out, ArmedCapture{Provider: providerID, Client: armed.client, Expires: armed.expires})
	}
	return out
}

// CaptureArmed says whether the provider has a live capture window, for
// control-plane status. Expired windows are removed here too.
func CaptureArmed(providerID string, now time.Time) bool {
	for _, armed := range ArmedCaptures(now) {
		if armed.Provider == providerID {
			return true
		}
	}
	return false
}
