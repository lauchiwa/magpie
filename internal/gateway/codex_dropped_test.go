package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// edgeDropped is what ylorn's ChatGPT backend answered to a request whose
// screenshot took it past about 1 MiB (Discord, v0.1.1179): Envoy's own
// 503 behind Cloudflare, as text, before any reply.
const edgeDropped = "upstream connect error or disconnect/reset before headers. reset reason: remote connection failure"

// dropsBig stands in for that ChatGPT backend: a body past 1 MiB is
// answered with edgeDropped, as ylorn's was, and a smaller one is served.
// It notes the account asked each time.
func dropsBig(t *testing.T, tried *[]string) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*tried = append(*tried, r.Header.Get("chatgpt-account-id"))
		if len(b) > 1<<20 {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Server", "cloudflare")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, edgeDropped)
			return
		}
		io.WriteString(w, sse(
			`data: {"type":"response.created","response":{"id":"r1","model":"gpt-5.5"}}`,
			`data: {"type":"response.output_text.delta","delta":"pong"}`,
			`data: {"type":"response.completed","response":{"id":"r1","usage":{"input_tokens":7,"output_tokens":1}}}`))
	}))
	t.Cleanup(up.Close)
	was := provider.CodexBase
	provider.CodexBase = up.URL + "/backend-api/codex"
	t.Cleanup(func() { provider.CodexBase = was })
}

// screenshotTurn is a Responses turn with a PNG screenshot of about n
// bytes inline, base64 as an agent sends it. Its bytes aren't an image
// any decoder reads, so nothing on the way makes it smaller.
func screenshotTurn(t *testing.T, n int) string {
	t.Helper()
	raw := make([]byte, n*3/4)
	rand.Read(raw)
	b, _ := json.Marshal(map[string]any{"model": "gpt-5.5", "stream": true, "input": []any{
		map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "what does this screenshot show?"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)},
		}}}})
	return string(b)
}

// With two ChatGPT accounts on, a turn the backend drops for its size is
// asked of one account once: the other would be dropped the same, a retry
// sends the same bytes again, and neither account is at fault, so nobody
// rests. The agent is told the request is too large, at a status it
// doesn't retry, and what to do about it. A small turn after it goes to
// the first account as before.
func TestCodexDroppedBigBodyIsTheRequests(t *testing.T) {
	codexSignedIn(t, "spare@example.com")
	var tried []string
	dropsBig(t, &tried)

	code, body := codexPost(t, screenshotTurn(t, 1200<<10))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %s", code, body)
	}
	for _, want := range []string{"MiB", "compact", "image"} {
		if !strings.Contains(body, want) {
			t.Errorf("the agent isn't told %q: %s", want, body)
		}
	}
	if strings.Join(tried, ",") != "acct-1" {
		t.Errorf("asked %v, want acct-1 once", tried)
	}
	if r := restingNow(t); len(r) != 0 {
		t.Errorf("rest over the request's size: %v", r)
	}

	tried = nil
	code, body = codexPost(t, `{"model":"gpt-5.5","stream":true,"input":"ping"}`)
	if code != 200 || !strings.Contains(body, "pong") || strings.Join(tried, ",") != "acct-1" {
		t.Fatalf("small turn after: %d %s tried %v", code, body, tried)
	}
}

// With Codex's own sign-in alone, relayed as it came: Codex is answered
// the same 413 and words, not the 503 it would send the same bytes again
// for, five times over.
func TestCodexDroppedBigBodyRelayed(t *testing.T) {
	codexSignedIn(t)
	var tried []string
	dropsBig(t, &tried)

	code, body := codexPost(t, screenshotTurn(t, 1200<<10))
	if code != http.StatusRequestEntityTooLarge || !strings.Contains(body, "compact") {
		t.Fatalf("status %d: %s", code, body)
	}
	if strings.Join(tried, ",") != "acct-1" {
		t.Errorf("asked %v, want acct-1 once", tried)
	}
}

// The same 503 to a small request is the backend's trouble, as it is in
// an outage: it is relayed as the 503 it is, for the client to try again.
func TestCodexEdgeResetSmallBodyStays503(t *testing.T) {
	codexSignedIn(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, edgeDropped)
	}))
	t.Cleanup(up.Close)
	was := provider.CodexBase
	provider.CodexBase = up.URL + "/backend-api/codex"
	t.Cleanup(func() { provider.CodexBase = was })

	code, body := codexPost(t, `{"model":"gpt-5.5","stream":true,"input":"ping"}`)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "reset reason") {
		t.Fatalf("status %d: %s", code, body)
	}
}
