// Package identity holds reusable outbound client fingerprints: the
// User-Agent and the companion headers one coding agent tells itself by.
//
// A relay can serve only the agents its plan is sold for and tell them by
// those headers. A provider's own Headers can already carry any header the
// user types, but they are an open map: nothing checks that the values
// agree with each other, nothing records where they came from, and nothing
// stops a session or a credential header from being typed in. A profile is
// the narrow alternative: a frozen snapshot of one client's identity, over
// a closed set of header names, which an operator confirms before any
// request may present it.
//
// What a profile is deliberately not:
//
//   - It is not authentication. A matching product name is a format check,
//     never proof that a client binary sent the request.
//   - It holds no credential, no session or thread id, no request id and no
//     attestation. HeaderAllowed is a closed projection, not a prefix rule:
//     an arbitrary X-Stainless-* field is not safe to keep.
//   - It never names magpie. A value that does is refused, so a profile
//     cannot be made that advertises the proxy it is sent through.
//
// A profile is bound to a provider by id in the store (Bind). Nothing here
// imports provider: the binding is a plain id, so providers.json keeps its
// own shape and a profile is never written into it.
package identity

import (
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Client is which agent's fingerprint a profile holds. It is explicit
// configuration the operator chose, not a classification of an inbound
// request and not proof of trust.
type Client string

const (
	ClientCodex      Client = "codex_cli"
	ClientClaudeCode Client = "claude_code"
)

// The bounds a fingerprint is held to. A profile is a short identity
// snapshot; anything larger is a sign that something else was pasted in.
const (
	MaxUserAgentBytes   = 1024
	MaxHeaderValueBytes = 256
	MaxHeaders          = 16
	MaxVersionBytes     = 64
)

// appName is refused anywhere in a fingerprint, so no profile can present
// the proxy the request is sent through.
const appName = "magpie"

func (c Client) Valid() bool { return c == ClientCodex || c == ClientClaudeCode }

// Clients are the clients a profile can be made for, in a stable order.
func Clients() []Client { return []Client{ClientCodex, ClientClaudeCode} }

// codexProducts are the User-Agent products Codex sends. A product in this
// list is a format check alone, never authentication of a client binary.
var codexProducts = []string{
	"codex_cli_rs", "codex-tui", "codex_vscode", "codex_vscode_copilot",
	"codex_app", "codex_chatgpt_desktop", "codex_atlas", "codex_exec", "codex_sdk_ts",
}

// IsCodexProduct is shared by the import projection and saved-profile
// validation, so both judge a product name the same way.
func IsCodexProduct(product string) bool {
	for _, p := range codexProducts {
		if p == product {
			return true
		}
	}
	return false
}

// HeaderAllowed is the closed set of header names a profile may keep for
// client. It is a projection, not a prefix rule: Codex keeps only its
// originator, and Claude Code only the fields that describe the install,
// never an arbitrary X-Stainless-* field, which would let a session or a
// request id ride along under a plausible name.
//
// User-Agent and the version are fields of Fingerprint, not entries here,
// so they are never stored twice.
func HeaderAllowed(client Client, name string) bool {
	switch client {
	case ClientCodex:
		return name == "Originator"
	case ClientClaudeCode:
		switch name {
		case "X-App", "X-Stainless-Lang", "X-Stainless-Package-Version",
			"X-Stainless-Os", "X-Stainless-Arch", "X-Stainless-Runtime",
			"X-Stainless-Runtime-Version":
			return true
		}
	}
	return false
}

var versionRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*([.\-+][0-9A-Za-z.\-+]+)?$`)

// ValidVersion is the version a client reports. It is checked for shape
// only: a pinned profile must not be invalidated when a vendor's current
// version moves on.
func ValidVersion(v string) bool {
	return v != "" && len(v) <= MaxVersionBytes && versionRE.MatchString(v)
}

// identityText is a printable, trimmed, ASCII-only value that does not
// name magpie. Non-ASCII is refused because a header value carrying it
// would itself stand out at the vendor.
func identityText(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value ||
		strings.Contains(strings.ToLower(value), appName) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

// Fingerprint is the reusable part of an identity: the User-Agent, the
// version it reports, and the companion headers of the closed set. The
// versions of generated companion headers are derived from Version rather
// than stored beside it.
type Fingerprint struct {
	UserAgent string            `json:"user_agent"`
	Version   string            `json:"version"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// Validate checks the frozen tuple agrees with itself. This is what an
// open header map cannot do: a User-Agent saying one version while the
// version header says another is a fingerprint no real client produces,
// and is easier to single out than either value alone.
func (f Fingerprint) Validate(client Client) error {
	if !client.Valid() {
		return fmt.Errorf("identity client %q is not one of codex_cli, claude_code", client)
	}
	if !ValidVersion(f.Version) {
		return fmt.Errorf("identity version %q is invalid", f.Version)
	}
	if !identityText(f.UserAgent, MaxUserAgentBytes) {
		return fmt.Errorf("identity user_agent is invalid: it must be printable ASCII, trimmed, at most %d bytes, and must not name %s", MaxUserAgentBytes, appName)
	}
	product, tail, found := strings.Cut(f.UserAgent, "/")
	version, _, _ := strings.Cut(tail, " ")
	if !found || version != f.Version {
		return fmt.Errorf("identity user_agent %q does not report version %q", f.UserAgent, f.Version)
	}
	if len(f.Headers) > MaxHeaders {
		return fmt.Errorf("identity holds at most %d headers, not %d", MaxHeaders, len(f.Headers))
	}
	for name, value := range f.Headers {
		if name != http.CanonicalHeaderKey(name) {
			return fmt.Errorf("identity header %q is not a canonical header name", name)
		}
		if !HeaderAllowed(client, name) {
			return fmt.Errorf("identity header %q is not one %s keeps", name, client)
		}
		if !identityText(value, MaxHeaderValueBytes) {
			return fmt.Errorf("identity header %q has an invalid value: it must be printable ASCII, trimmed, at most %d bytes, and must not name %s", name, MaxHeaderValueBytes, appName)
		}
	}
	if client == ClientCodex {
		if !IsCodexProduct(product) {
			return fmt.Errorf("identity user_agent product %q is not one Codex sends", product)
		}
		if o := f.Headers["Originator"]; o != "" && o != product {
			return fmt.Errorf("identity originator %q does not match user_agent product %q", o, product)
		}
	}
	return nil
}

// Header renders the fingerprint as the headers a request presents. The
// version header Codex sends is derived here from Version, so it can never
// disagree with the User-Agent that Validate checked against it.
func (f Fingerprint) Header(client Client) http.Header {
	h := http.Header{"User-Agent": {f.UserAgent}}
	for name, value := range f.Headers {
		h.Set(name, value)
	}
	if client == ClientCodex {
		h.Set("Version", f.Version)
	}
	return h
}

// Source says where a profile came from, which an operator needs in order
// to judge it.
type Source string

const (
	// SourceBuiltin is a fingerprint magpie composes itself.
	SourceBuiltin Source = "builtin"
	// SourceImport is a fingerprint explicitly imported from a request
	// header file the operator supplied.
	SourceImport Source = "request_import"
	// SourceCapture is a fingerprint projected from a live request during
	// an explicitly armed, one-shot observation window.
	SourceCapture Source = "request_capture"
)

func (s Source) Valid() bool {
	return s == SourceBuiltin || s == SourceImport || s == SourceCapture
}

var idRE = regexp.MustCompile(`^identity_[0-9a-f]{16}$`)

// Profile is one confirmed-or-candidate snapshot. A replacement snapshot
// gets a new id rather than editing one in place, so a profile a request
// presented stays the thing that was reviewed.
//
// A profile is not usable for forwarding until ConfirmedAt is set by an
// operator action: an imported fingerprint is a candidate first.
type Profile struct {
	ID          string      `json:"id"`
	Client      Client      `json:"client"`
	Source      Source      `json:"source"`
	Fingerprint Fingerprint `json:"fingerprint"`
	CreatedAt   time.Time   `json:"created_at"`
	ObservedAt  *time.Time  `json:"observed_at,omitempty"`
	ConfirmedAt *time.Time  `json:"confirmed_at,omitempty"`
}

func (p Profile) Validate() error {
	if !idRE.MatchString(p.ID) {
		return fmt.Errorf("identity profile id %q is invalid", p.ID)
	}
	if !p.Source.Valid() {
		return fmt.Errorf("identity profile source %q is not one of builtin, request_import, request_capture", p.Source)
	}
	if err := p.Fingerprint.Validate(p.Client); err != nil {
		return err
	}
	if p.CreatedAt.IsZero() {
		return fmt.Errorf("identity profile created_at is required")
	}
	if p.Source == SourceImport && p.ObservedAt != nil {
		return fmt.Errorf("an imported identity cannot have an observation time")
	}
	if p.Source == SourceCapture && p.ObservedAt == nil {
		return fmt.Errorf("a captured identity requires observed_at")
	}
	if p.Source == SourceBuiltin && p.ObservedAt != nil {
		return fmt.Errorf("a built-in identity cannot have an observation time")
	}
	if p.ObservedAt != nil && (p.ObservedAt.IsZero() || p.ObservedAt.After(p.CreatedAt)) {
		return fmt.Errorf("an identity observation must precede its creation")
	}
	if p.ConfirmedAt != nil && (p.ConfirmedAt.IsZero() || p.ConfirmedAt.Before(p.CreatedAt)) {
		return fmt.Errorf("an identity confirmation must follow its creation")
	}
	return nil
}

// Usable reports whether a request may present this profile: a valid
// snapshot an operator has confirmed.
func (p Profile) Usable() bool { return p.Validate() == nil && p.ConfirmedAt != nil }

// Clone copies the profile so a caller cannot reach the stored map.
func (p Profile) Clone() Profile {
	p.Fingerprint.Headers = maps.Clone(p.Fingerprint.Headers)
	if p.ObservedAt != nil {
		t := *p.ObservedAt
		p.ObservedAt = &t
	}
	if p.ConfirmedAt != nil {
		t := *p.ConfirmedAt
		p.ConfirmedAt = &t
	}
	return p
}
