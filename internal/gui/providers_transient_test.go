package gui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/provider"
)

// The add form's Fetch models and Detect, and a copy's Detect, ask with a
// provider that isn't saved yet. A profile bound under the preset's id, or
// under the copied provider's, belongs to that other provider: the request
// carries none of it (R1, sign-callers G0 §5).
func TestAnUnsavedFormPresentsNoBinding(t *testing.T) {
	identityHome(t)
	var mu sync.Mutex
	var uas []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uas = append(uas, r.Method+" "+r.URL.Path+" "+r.Header.Get("User-Agent"))
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet:
			w.Write([]byte(`{"data":[{"id":"deepseek-chat"}]}`))
		default:
			w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
		}
	}))
	defer up.Close()

	h := http.Header{
		"User-Agent": {"codex_cli_rs/0.159.2 (Mac OS 26.6.0; arm64) Apple_Terminal/455"},
		"Originator": {"codex_cli_rs"},
		"Version":    {"0.159.2"},
	}
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p, err := identity.Candidate(id, identity.ClientCodex, h, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Add(p); err != nil {
		t.Fatal(err)
	}
	if err := identity.Confirm(p.ID, now); err != nil {
		t.Fatal(err)
	}
	// a provider saved as "deepseek" and bound; the form below is another
	if err := identity.Bind("deepseek", p.ID); err != nil {
		t.Fatal(err)
	}

	// the saved deepseek, which a copy's Detect takes its key from
	saved := `{"providers":[{"id":"deepseek","name":"DeepSeek","key":"sk-saved","chat":"` + up.URL + `/v1","models":["m"]}]}`
	if err := os.MkdirAll(appdir.Config(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provider.Path(), []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(action, body string) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/"+action, strings.NewReader(body)))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body)
		}
	}
	urls := `"chat":"` + up.URL + `/v1","anthropic":"` + up.URL + `","modelsURL":"` + up.URL + `/v1/models"`
	post("list", `{"preset":"deepseek","key":"sk-new",`+urls+`}`)
	post("detect", `{"new":true,"copyOf":"deepseek","typed":true,"base":"`+up.URL+`","model":"m"}`)
	mu.Lock()
	defer mu.Unlock()
	if len(uas) == 0 {
		t.Fatal("the upstream was never asked")
	}
	for _, ua := range uas {
		if strings.Contains(ua, "codex_cli_rs") {
			t.Errorf("an unsaved form presented another provider's profile: %s", ua)
		}
	}
}
