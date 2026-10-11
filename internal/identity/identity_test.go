package identity

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// A real Codex CLI request's identity fields, as it sends them.
func codexHeader() http.Header {
	return http.Header{
		"User-Agent":    {"codex_cli_rs/0.159.2 (Mac OS 26.6.0; arm64) Apple_Terminal/455"},
		"Originator":    {"codex_cli_rs"},
		"Version":       {"0.159.2"},
		"Authorization": {"Bearer sk-secret"},
		"Session-Id":    {"019de403-7ebc-7262-bfef-a76eb583b0e2"},
		"Thread-Id":     {"01a128bb-567a-7580-b659-73975e79e360"},
		"Content-Type":  {"application/json"},
	}
}

// A real Claude Code request's identity fields.
func claudeHeader() http.Header {
	return http.Header{
		"User-Agent":                  {"claude-cli/2.0.44 (external, cli)"},
		"X-App":                       {"cli"},
		"X-Stainless-Lang":            {"js"},
		"X-Stainless-Package-Version": {"0.70.1"},
		"X-Stainless-Os":              {"MacOS"},
		"X-Stainless-Arch":            {"arm64"},
		"X-Stainless-Runtime":         {"node"},
		"X-Stainless-Runtime-Version": {"v24.16.0"},
		"X-Api-Key":                   {"sk-ant-secret"},
		"Anthropic-Beta":              {"claude-code-20250219", "fine-grained-tool-streaming-2025-05-14"},
		"X-Stainless-Retry-Count":     {"0"},
		"X-Stainless-Timeout":         {"600"},
	}
}

// A profile is imported from a request, and what it keeps is the closed
// set alone: the credential, the session, the thread, the per-request
// fields and the betas are all dropped, so none of them can be stored and
// presented later.
func TestProjectKeepsOnlyTheClosedSet(t *testing.T) {
	f, err := Project(ClientCodex, codexHeader())
	if err != nil {
		t.Fatal(err)
	}
	if f.UserAgent != "codex_cli_rs/0.159.2 (Mac OS 26.6.0; arm64) Apple_Terminal/455" {
		t.Errorf("user agent = %q", f.UserAgent)
	}
	if f.Version != "0.159.2" {
		t.Errorf("version = %q", f.Version)
	}
	if f.Headers["Originator"] != "codex_cli_rs" {
		t.Errorf("originator = %q", f.Headers["Originator"])
	}
	for _, gone := range []string{"Authorization", "Session-Id", "Thread-Id", "Content-Type"} {
		if v, ok := f.Headers[gone]; ok {
			t.Errorf("%s was kept as %q; a profile must hold no credential, session, thread or framing header", gone, v)
		}
	}

	c, err := Project(ClientClaudeCode, claudeHeader())
	if err != nil {
		t.Fatal(err)
	}
	if c.Headers["X-Stainless-Os"] != "MacOS" || c.Headers["X-App"] != "cli" {
		t.Errorf("claude headers = %v", c.Headers)
	}
	// X-Stainless-Retry-Count and X-Stainless-Timeout describe this one
	// request, not the install: a prefix rule would have kept them.
	for _, gone := range []string{"X-Api-Key", "Anthropic-Beta", "X-Stainless-Retry-Count", "X-Stainless-Timeout"} {
		if v, ok := c.Headers[gone]; ok {
			t.Errorf("%s was kept as %q; HeaderAllowed is a closed set, not an X-Stainless-* prefix rule", gone, v)
		}
	}
}

// The whitelist is closed: an arbitrary X-Stainless-* field is not
// allowed, which is what separates it from a prefix rule.
func TestHeaderAllowedIsClosed(t *testing.T) {
	for _, want := range []struct {
		client Client
		name   string
		ok     bool
	}{
		{ClientCodex, "Originator", true},
		{ClientCodex, "X-Stainless-Os", false},
		{ClientCodex, "Session-Id", false},
		{ClientClaudeCode, "X-Stainless-Os", true},
		{ClientClaudeCode, "X-Stainless-Retry-Count", false},
		{ClientClaudeCode, "X-Stainless-Anything", false},
		{ClientClaudeCode, "Authorization", false},
		{ClientClaudeCode, "Originator", false},
	} {
		if got := HeaderAllowed(want.client, want.name); got != want.ok {
			t.Errorf("HeaderAllowed(%s, %s) = %v, want %v", want.client, want.name, got, want.ok)
		}
	}
}

// A fingerprint whose parts disagree is refused. This is the check an open
// header map cannot make: a User-Agent saying one version while the
// version says another is a mixture no real client sends.
func TestValidateRefusesAnInconsistentFingerprint(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Fingerprint
		want string
	}{
		{
			name: "version disagrees with the user agent",
			f:    Fingerprint{UserAgent: "codex_cli_rs/0.159.2 (Mac OS; arm64)", Version: "0.100.0", Headers: map[string]string{"Originator": "codex_cli_rs"}},
			want: "does not report version",
		},
		{
			name: "originator disagrees with the user agent product",
			f:    Fingerprint{UserAgent: "codex_cli_rs/0.159.2 (Mac OS; arm64)", Version: "0.159.2", Headers: map[string]string{"Originator": "mine_cli"}},
			want: "does not match user_agent product",
		},
		{
			name: "a product Codex does not send",
			f:    Fingerprint{UserAgent: "mine/9.9", Version: "9.9"},
			want: "is not one Codex sends",
		},
		{
			name: "a header outside the closed set",
			f:    Fingerprint{UserAgent: "codex_cli_rs/0.159.2", Version: "0.159.2", Headers: map[string]string{"Session-Id": "s"}},
			want: "is not one",
		},
		{
			name: "a value naming magpie",
			f:    Fingerprint{UserAgent: "codex_cli_rs/0.159.2 via magpie", Version: "0.159.2"},
			want: "must not name magpie",
		},
		{
			name: "a non-ASCII value",
			f:    Fingerprint{UserAgent: "codex_cli_rs/0.159.2 (macOS 中文)", Version: "0.159.2"},
			want: "printable ASCII",
		},
		{
			name: "an untrimmed value",
			f:    Fingerprint{UserAgent: "codex_cli_rs/0.159.2 ", Version: "0.159.2"},
			want: "invalid",
		},
		{
			name: "an oversized value",
			f:    Fingerprint{UserAgent: "codex_cli_rs/0.159.2 " + strings.Repeat("x", MaxUserAgentBytes), Version: "0.159.2"},
			want: "invalid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.f.Validate(ClientCodex)
			if err == nil {
				t.Fatalf("an inconsistent fingerprint was accepted: %+v", tc.f)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A header the request repeats is refused rather than reconciled: picking
// one value would invent a fingerprint the client never sent.
func TestProjectRefusesARepeatedIdentityHeader(t *testing.T) {
	h := codexHeader()
	h["Originator"] = []string{"codex_cli_rs", "codex_exec"}
	if _, err := Project(ClientCodex, h); err == nil {
		t.Fatal("a repeated identity header was accepted")
	}
}

// Rendering derives the version header from Version, so what is sent can
// never disagree with the User-Agent that Validate checked against it.
func TestHeaderDerivesTheVersion(t *testing.T) {
	f, err := Project(ClientCodex, codexHeader())
	if err != nil {
		t.Fatal(err)
	}
	h := f.Header(ClientCodex)
	if h.Get("Version") != "0.159.2" {
		t.Errorf("Version = %q, want it derived from the fingerprint's version", h.Get("Version"))
	}
	if h.Get("User-Agent") != f.UserAgent || h.Get("Originator") != "codex_cli_rs" {
		t.Errorf("rendered = %v", h)
	}
	if h.Get("Authorization") != "" || h.Get("Session-Id") != "" {
		t.Errorf("rendered headers carry a credential or a session: %v", h)
	}
}

// An imported profile is a candidate: it is not usable until an operator
// confirms it.
func TestCandidateIsNotUsableUntilConfirmed(t *testing.T) {
	now := time.Now().UTC()
	p, err := Candidate("identity_0123456789abcdef", ClientCodex, codexHeader(), now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Usable() {
		t.Fatal("an imported candidate is usable before it was confirmed")
	}
	if p.Source != SourceCapture || p.ObservedAt == nil {
		t.Errorf("an imported profile must record that it was observed: %+v", p)
	}
	at := now.Add(time.Minute)
	p.ConfirmedAt = &at
	if !p.Usable() {
		t.Fatalf("a confirmed profile is not usable: %v", p.Validate())
	}
}

// A profile's times must hold together: an observation precedes the
// snapshot, a confirmation follows it, and a built-in was never observed.
func TestProfileTimesMustHoldTogether(t *testing.T) {
	now := time.Now().UTC()
	f, err := Project(ClientCodex, codexHeader())
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	earlier := now.Add(-time.Hour)
	for _, tc := range []struct {
		name string
		p    Profile
	}{
		{"a capture with no observation", Profile{ID: "identity_0123456789abcdef", Client: ClientCodex, Source: SourceCapture, Fingerprint: f, CreatedAt: now}},
		{"an observation after the snapshot", Profile{ID: "identity_0123456789abcdef", Client: ClientCodex, Source: SourceCapture, Fingerprint: f, CreatedAt: now, ObservedAt: &later}},
		{"a confirmation before the snapshot", Profile{ID: "identity_0123456789abcdef", Client: ClientCodex, Source: SourceCapture, Fingerprint: f, CreatedAt: now, ObservedAt: &earlier, ConfirmedAt: &earlier}},
		{"a built-in that was observed", Profile{ID: "identity_0123456789abcdef", Client: ClientCodex, Source: SourceBuiltin, Fingerprint: f, CreatedAt: now, ObservedAt: &earlier}},
		{"an id of the wrong shape", Profile{ID: "nope", Client: ClientCodex, Source: SourceBuiltin, Fingerprint: f, CreatedAt: now}},
	} {
		if err := tc.p.Validate(); err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

// Recognition reads a header the sender controls. It decides which closed
// set applies, and says nothing about trust.
func TestRecognize(t *testing.T) {
	if c, ok := Recognize(codexHeader()); !ok || c != ClientCodex {
		t.Errorf("codex recognized as %q, %v", c, ok)
	}
	if c, ok := Recognize(claudeHeader()); !ok || c != ClientClaudeCode {
		t.Errorf("claude code recognized as %q, %v", c, ok)
	}
	if _, ok := Recognize(http.Header{"User-Agent": {"curl/8.4.0"}}); ok {
		t.Error("an unrelated client was recognized")
	}
}
