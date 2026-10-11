package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// edgeReset is the 503 ChatGPT's front (Cloudflare, then Envoy) answers,
// as text, when the backend behind it closed the connection before any
// reply.
var edgeReset = regexp.MustCompile(`(?i)upstream connect error or disconnect/reset before headers`)

// codexDropBody is the request size from which that 503 is taken to be
// about the request. ylorn (Discord, v0.1.1179) got it for every Codex
// turn a PNG screenshot took past about 1 MiB, also without magpie, and
// never once the same image was a smaller JPEG. It isn't the backend's
// limit for everyone: bodies of 1 to 20 MiB were answered from here.
const codexDropBody = 1 << 20

// codexDropped is the 413 a ChatGPT Codex reply stands for when the
// backend dropped a request of size bytes this large. Sent again — to
// the same account or another one — the same bytes are dropped the same
// way, so it isn't a 503 to retry, and no account is at fault for it. A
// smaller request dropped the same way is the backend's own trouble and
// stays the 503 it is.
func codexDropped(res *http.Response, size int) *http.Response {
	if res == nil || res.StatusCode != http.StatusServiceUnavailable || size < codexDropBody {
		return res
	}
	head, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	if !edgeReset.Match(head) {
		res.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head), res.Body), res.Body}
		return res
	}
	res.Body.Close()
	msg := fmt.Sprintf("ChatGPT's Codex backend dropped this %.1f MiB request before answering (503: %s). "+
		"It does this to requests this large, most often from images, on any account, so magpie didn't retry it or rest the account. "+
		"Run /compact, or remove or shrink the images (a JPEG in place of a large PNG screenshot), then try again.",
		float64(size)/(1<<20), bytes.TrimSpace(head))
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": "request_too_large", "code": "request_too_large"}})
	h := res.Header.Clone()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Encoding", "Transfer-Encoding"} {
		h.Del(k)
	}
	h.Set("Content-Type", "application/json")
	out := *res
	out.StatusCode, out.Status = http.StatusRequestEntityTooLarge, "413 Request Entity Too Large"
	out.Header, out.Body, out.ContentLength = h, io.NopCloser(bytes.NewReader(b)), int64(len(b))
	return &out
}
