package identity

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// A fingerprint is imported from a request that really came through this
// magpie, rather than typed in: a real client's headers agree with each
// other, which hand-written ones often don't.
//
// Importing is deliberately narrow. Project keeps only the closed set
// (HeaderAllowed) and drops everything else, so a session id, a thread
// id, a request id, an attestation or a credential cannot be carried into
// a profile even when the request had one. The result is then validated as
// a saved profile is, so an implausible mixture is refused at the door
// rather than stored and presented later.
//
// Importing is also not arming a capture: the caller hands over headers it
// already has. Nothing here watches traffic or keeps a consent window.

// Recognize says which client sent a request, by the identity fields it
// carries. It is a format check on a header the sender controls, never
// authentication: a request can claim any client. The answer decides which
// closed set applies, nothing about trust.
func Recognize(h http.Header) (Client, bool) {
	ua := strings.TrimSpace(h.Get("User-Agent"))
	product, _, _ := strings.Cut(ua, "/")
	if IsCodexProduct(product) || IsCodexProduct(strings.TrimSpace(h.Get("Originator"))) {
		return ClientCodex, true
	}
	// Claude Code names itself in X-App with a claude-cli User-Agent.
	if strings.HasPrefix(strings.ToLower(ua), "claude-cli/") {
		return ClientClaudeCode, true
	}
	return "", false
}

// versionOf is the version a request reports: the one in its User-Agent
// product, which Validate requires the two to agree on anyway. Codex also
// sends a Version header; it is read only as a fallback, so a request
// whose two values disagree is refused rather than quietly reconciled.
func versionOf(h http.Header) string {
	ua := strings.TrimSpace(h.Get("User-Agent"))
	_, tail, found := strings.Cut(ua, "/")
	if found {
		if v, _, _ := strings.Cut(tail, " "); ValidVersion(v) {
			return v
		}
	}
	return strings.TrimSpace(h.Get("Version"))
}

// Project turns a request's headers into a fingerprint, keeping only the
// closed set for client. A repeated identity field is refused even when
// its map keys differ in case: two values for one identity header is
// ambiguous, and picking one would invent a fingerprint the client never
// sent.
func Project(client Client, h http.Header) (Fingerprint, error) {
	if !client.Valid() {
		return Fingerprint{}, fmt.Errorf("identity client %q is not one of codex_cli, claude_code", client)
	}
	f := Fingerprint{Headers: map[string]string{}}
	seen := map[string]bool{}
	var reportedVersion string
	for name, values := range h {
		canonical := http.CanonicalHeaderKey(name)
		if canonical != "User-Agent" && canonical != "Version" && !HeaderAllowed(client, canonical) {
			continue
		}
		if len(values) != 1 || seen[canonical] {
			return Fingerprint{}, fmt.Errorf("the request repeats identity header %q", canonical)
		}
		seen[canonical] = true
		switch canonical {
		case "User-Agent":
			f.UserAgent = values[0]
		case "Version":
			reportedVersion = values[0]
		default:
			f.Headers[canonical] = values[0]
		}
	}
	f.Version = versionOf(h)
	if reportedVersion != "" && reportedVersion != f.Version {
		return Fingerprint{}, fmt.Errorf("request Version %q does not match user_agent version %q", reportedVersion, f.Version)
	}
	if len(f.Headers) == 0 {
		f.Headers = nil
	}
	if err := f.Validate(client); err != nil {
		return Fingerprint{}, err
	}
	return f, nil
}

// Candidate is a profile captured from a request: a snapshot with its
// observation time, not yet usable. An operator confirms it (Store.Confirm)
// before any request may present it.
func Candidate(id string, client Client, h http.Header, observed, now time.Time) (Profile, error) {
	f, err := Project(client, h)
	if err != nil {
		return Profile{}, err
	}
	if observed.After(now) {
		observed = now
	}
	p := Profile{
		ID: id, Client: client, Source: SourceCapture, Fingerprint: f,
		CreatedAt: now, ObservedAt: &observed,
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// ImportedCandidate is a profile projected from a header file the operator
// explicitly supplied. Unlike a live capture, it has no trustworthy
// observation timestamp, so it records the import source but no ObservedAt.
func ImportedCandidate(id string, client Client, h http.Header, now time.Time) (Profile, error) {
	f, err := Project(client, h)
	if err != nil {
		return Profile{}, err
	}
	p := Profile{ID: id, Client: client, Source: SourceImport, Fingerprint: f, CreatedAt: now}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}
