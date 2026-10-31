package gateway

import (
	"bytes"
	"encoding/json"
	"log"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/wslrun"
)

// A Claude run is Claude Code's interactive mode on macOS, Linux and
// Windows, for this machine's Claude Code, unless the user turned it off
// or the run needs what only -p takes.
func TestClaudeTUIChosen(t *testing.T) {
	was := settings.Load()
	t.Cleanup(func() { _ = settings.Save(was) })
	claudeTUIRuns = true
	t.Cleanup(func() { claudeTUIRuns = false })
	here := claudeCLI{path: "/usr/local/bin/claude"}
	text := func(s string) []Message {
		return []Message{{Role: "user", Parts: []Part{{Kind: Text, Text: s}}}}
	}
	prompt := []map[string]any{{"type": "text", "text": "hi"}}
	on := runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows"
	cases := []struct {
		name   string
		req    *Request
		binary claudeCLI
		prompt []map[string]any
		print  bool
		want   bool
		why    string // what is logged, on macOS, Linux and Windows
	}{
		{"a plain turn", &Request{Messages: text("hi")}, here, prompt, false, on, ""},
		{"turned off", &Request{Messages: text("hi")}, here, prompt, true, false, "the interactive mode is turned off"},
		{"in WSL", &Request{Messages: text("hi")}, claudeCLI{path: "claude", wsl: &wslrun.Tool{}}, prompt, false, false, "Claude Code in WSL"},
		{"safeguards", &Request{Messages: text("hi"), Safeguards: json.RawMessage(`{"a":1}`)}, here, prompt, false, false, "safeguards"},
		{"client cache marks", &Request{Messages: text("hi"), clientCache: &clientCache{}}, here, prompt, false, false, "the client's cache marks"},
		{"a schema", &Request{Messages: text("hi"), Format: &Format{Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`)}}, here, prompt, false, false, "a JSON schema"},
		{"an image", &Request{Messages: []Message{{Role: "user", Parts: []Part{{Kind: Image, MediaType: "image/png", Data: "AA=="}}}}}, here, prompt, false, on, ""},
		{"an image by its URL", &Request{Messages: []Message{{Role: "user", Parts: []Part{{Kind: Image, URL: "https://example.com/a.png"}}}}}, here, prompt, false, false, "an image known by its URL"},
		{"a tool's image by its URL", &Request{Messages: []Message{{Role: "user", Parts: []Part{{Kind: ToolResult, Images: []Part{{Kind: Image, URL: "https://example.com/a.png"}}}}}}}, here, prompt, false, false, "an image known by its URL"},
		{"a large image", &Request{Messages: text("hi")}, here, []map[string]any{{"type": "text", "text": "hi"}, {"type": "image", "source": map[string]any{"type": "base64", "data": strings.Repeat("A", tuiPromptMost)}}}, false, on, ""},
		{"a long first message", &Request{Messages: text("hi")}, here, []map[string]any{{"type": "text", "text": strings.Repeat("x", tuiPromptMost)}}, false, false, "a first message over 400KB"},
	}
	for _, c := range cases {
		st := settings.Load()
		st.ClaudePrintMode = c.print
		if err := settings.Save(st); err != nil {
			t.Fatal(err)
		}
		if got := claudeTUI(c.req, c.binary, c.prompt); got != c.want {
			t.Errorf("%s: claudeTUI = %v, want %v", c.name, got, c.want)
		}
		if got := claudePrintWhy(c.req, c.binary, c.prompt); on && got != c.why {
			t.Errorf("%s: -p because %q, want %q", c.name, got, c.why)
		}
	}
}

// The helper's line saying why it became -p is logged; the rest of Claude
// Code's stderr isn't.
func TestClaudeTUIFallbackLogged(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	w := &lockedWriter{run: &subscriptionRun{}}
	_, _ = w.Write([]byte("warming up\nclaude-tui: Claude Code ended before it showed its prompt (exit status 1): B; this run is -p\n"))
	_, _ = w.Write([]byte("Error: something else\n"))
	got := logs.String()
	if !strings.Contains(got, "claude: Claude Code ended before it showed its prompt (exit status 1): B; this run is -p") || strings.Contains(got, "warming up") || strings.Contains(got, "something else") {
		t.Fatalf("logged %q", got)
	}
	if !strings.Contains(w.run.stderr.String(), "something else") {
		t.Fatalf("stderr kept %q", w.run.stderr.String())
	}
}
