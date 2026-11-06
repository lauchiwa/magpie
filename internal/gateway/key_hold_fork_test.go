package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/usage"
)

// fork: Billing D4, write failures. A limited key's request cut off
// mid-call (here, the gateway "restarts" while its upstream holds the
// reply) is counted at its reservation, and /v1/magpie/limit says so;
// once its call is recorded it counts by its record, once. Before the
// journal the restart gave the key its whole cap back.
func TestKeyLimitCountsACutOffRequest(t *testing.T) {
	fresh(t)
	budget.Forget()
	t.Cleanup(budget.Forget)
	at := pinLimitClock(t)
	var calls atomic.Int64
	hold, entered := make(chan struct{}), make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-hold
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":300,"completion_tokens":50}}`)
	}))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "plan", Name: "Plan", Key: "upstream-secret", Chat: up.URL + "/v1", Models: []string{"m1"}}); err != nil {
		t.Fatal(err)
	}
	keys, secrets := newCaller(t, "Tokens")
	setLimit(t, keys[0].ID, &access.Limit{Period: "day", Tokens: 15}) // a request of chatReq reserves ~16
	h := New().Handler()
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatReq))
		r.Header.Set("Authorization", "Bearer "+secrets[0])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	limit := func() budget.Status {
		r := httptest.NewRequest("GET", "/v1/magpie/limit", nil)
		r.Header.Set("Authorization", "Bearer "+secrets[0])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got struct{ Limit budget.Status }
		json.Unmarshal(w.Body.Bytes(), &got)
		return got.Limit
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call() }()
	<-entered
	budget.Forget() // a restart while the call is on its way
	if st := limit(); st.Pending != 1 || st.Tokens == 0 || !st.Spent {
		t.Fatalf("a cut-off request after a restart: %+v", st)
	}
	if w := call(); w.Code != http.StatusTooManyRequests || calls.Load() != 1 {
		t.Fatal("a key whose cap a cut-off request holds was let in", w.Code, calls.Load(), w.Body.String())
	}
	close(hold)
	if w := <-done; w.Code != 200 {
		t.Fatal("the held call", w.Code, w.Body.String())
	}
	var held string
	usage.Visit(at, func(r usage.Record) {
		if r.CallerKeyID == keys[0].ID && !r.Rejected {
			held = r.Hold
		}
	})
	if held == "" {
		t.Fatal("the call's record names no reservation")
	}
	budget.Forget()
	if st := limit(); st.Pending != 0 || st.Tokens != 350 || st.Calls != 1 {
		t.Fatalf("the call once recorded: %+v", st)
	}
}

// While usage-holds.jsonl can't be written, a limited key's request is
// refused 503 before any provider is asked; a key with no limit goes on.
func TestKeyLimitRefusesWhileReservationsUnwritable(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("root writes a read-only file")
	}
	fresh(t)
	budget.Forget()
	t.Cleanup(budget.Forget)
	pinLimitClock(t)
	calls := limitedUpstream(t, 0)
	keys, secrets := newCaller(t, "Tokens", "Free")
	setLimit(t, keys[0].ID, &access.Limit{Period: "day", Tokens: 700})
	h := New().Handler()
	call := func(secret string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatReq))
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	journal := filepath.Join(filepath.Dir(usage.Path()), "usage-holds.jsonl")
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(journal, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(journal, 0o644) })
	w := call(secrets[0])
	var e struct {
		Error struct{ Message string }
	}
	json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(e.Error.Message, "couldn't write down the request's reservation") || w.Header().Get("X-Should-Retry") != "false" || calls.Load() != 0 {
		t.Fatal("a reservation that can't be written", w.Code, calls.Load(), w.Body.String())
	}
	if strings.Contains(w.Body.String(), journal) {
		t.Fatal("the refusal names the local journal", w.Body.String())
	}
	if w := call(secrets[1]); w.Code != 200 || calls.Load() != 1 {
		t.Fatal("a key with no limit refused", w.Code, w.Body.String())
	}
	os.Chmod(journal, 0o644)
	if w := call(secrets[0]); w.Code != 200 {
		t.Fatal("once the journal takes writes", w.Code, w.Body.String())
	}
}
