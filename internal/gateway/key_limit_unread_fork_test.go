package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/usage"
)

// Billing D4: a limited key whose usage.jsonl can't be read after a
// restart is refused with a 503 before any provider is asked, token and
// cost caps alike, and /v1/magpie/limit says its usage is unavailable,
// not 0 used. The failed read is not kept: once the log reads again, the
// key is the spent one it was. A key with no limit and a keyless request
// go on.
func TestKeyLimitRefusesWhileUsageUnread(t *testing.T) {
	fresh(t)
	budget.Forget()
	t.Cleanup(budget.Forget)
	pinLimitClock(t)
	calls := limitedUpstream(t, 0)
	// 300 input tokens a call at $1000 a million: $0.30 a call
	in, zero := 1000.0, 0.0
	if err := settings.Save(settings.Settings{ModelPrices: map[string]settings.ModelPrice{
		"plan/m1": {Input: &in, Output: &zero, CacheRead: &zero, CacheWrite: &zero},
	}}); err != nil {
		t.Fatal(err)
	}
	keys, secrets := newCaller(t, "Tokens", "Cost", "Free")
	setLimit(t, keys[0].ID, &access.Limit{Period: "day", Tokens: 700})
	setLimit(t, keys[1].ID, &access.Limit{Period: "day", Cost: 0.5})
	h := New().Handler()
	call := func(secret string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatReq))
		if secret != "" {
			r.Header.Set("Authorization", "Bearer "+secret)
		} else {
			r.RemoteAddr = "127.0.0.1:5000"
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, s := range []string{secrets[0], secrets[0], secrets[1], secrets[1]} {
		if w := call(s); w.Code != 200 {
			t.Fatal("within the limit", w.Code, w.Body.String())
		}
	}
	for i := range 2 {
		if w := call(secrets[i]); w.Code != http.StatusTooManyRequests {
			t.Fatal("spent key let in", i, w.Code, w.Body.String())
		}
	}
	sent := calls.Load()

	// the gateway restarts, and usage.jsonl is there but can't be read
	log := usage.Path()
	moved := log + ".kept"
	if err := os.Rename(log, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(log, 0o700); err != nil {
		t.Fatal(err)
	}
	budget.Forget()
	for i := range 2 {
		w := call(secrets[i])
		var e struct {
			Error struct{ Message string }
		}
		json.Unmarshal(w.Body.Bytes(), &e)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(e.Error.Message, "couldn't read the key's usage") || w.Header().Get("X-Should-Retry") != "false" || w.Header().Get("Retry-After") != "" {
			t.Fatal("a key with unread usage", i, w.Code, w.Header(), w.Body.String())
		}
		if strings.Contains(w.Body.String(), log) {
			t.Fatal("the refusal names the local log", w.Body.String())
		}
	}
	if calls.Load() != sent {
		t.Fatal("a request of a key with unread usage reached the provider")
	}
	r := httptest.NewRequest("GET", "/v1/magpie/limit", nil)
	r.Header.Set("Authorization", "Bearer "+secrets[1])
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, r)
	var got struct {
		Limited bool
		Limit   budget.Status
		Limits  []budget.Status
	}
	json.Unmarshal(lw.Body.Bytes(), &got)
	if !got.Limited || !got.Limit.Unavailable || got.Limit.CostLeft != 0 || got.Limit.CostLimit != 0.5 || len(got.Limits) != 1 || !got.Limits[0].Unavailable {
		t.Fatal("limit endpoint with unread usage", lw.Body.String())
	}
	sent = calls.Load()
	if w := call(secrets[2]); w.Code != 200 {
		t.Fatal("a key with no limit refused", w.Code, w.Body.String())
	}
	if w := call(""); w.Code != 200 {
		t.Fatal("a keyless loopback request refused", w.Code, w.Body.String())
	}
	if calls.Load() != sent+2 {
		t.Fatal("unlimited requests didn't reach the provider", calls.Load()-sent)
	}

	// the log reads again: what the keys used is back
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, log); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if w := call(secrets[i]); w.Code != http.StatusTooManyRequests {
			t.Fatal("a key read again as unspent", i, w.Code, w.Body.String())
		}
	}
	st := budget.Of(access.Key{ID: keys[1].ID, Limit: &access.Limit{Period: "day", Cost: 0.5}}, limitClock())
	if st == nil || st.Unavailable || !st.Spent || st.Calls != 2 {
		t.Fatalf("status once the log reads: %+v", st)
	}
}

// A usage.jsonl the gateway may not read, as after a copy with the wrong
// owner, is unread usage too, not an empty log.
func TestKeyLimitRefusesWhileUsageUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a file's mode doesn't take away reading on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of any mode")
	}
	fresh(t)
	budget.Forget()
	t.Cleanup(budget.Forget)
	pinLimitClock(t)
	calls := limitedUpstream(t, 0)
	keys, secrets := newCaller(t, "Tokens")
	setLimit(t, keys[0].ID, &access.Limit{Period: "day", Tokens: 700})
	h := New().Handler()
	call := func() int {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatReq))
		r.Header.Set("Authorization", "Bearer "+secrets[0])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for range 2 {
		if c := call(); c != 200 {
			t.Fatal("within the limit", c)
		}
	}
	if c := call(); c != http.StatusTooManyRequests {
		t.Fatal("spent key let in", c)
	}
	sent := calls.Load()
	if err := os.Chmod(usage.Path(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(usage.Path(), 0o600) })
	budget.Forget()
	if c := call(); c != http.StatusServiceUnavailable || calls.Load() != sent {
		t.Fatal("a key with unreadable usage", c, calls.Load()-sent)
	}
	if err := os.Chmod(usage.Path(), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := call(); c != http.StatusTooManyRequests {
		t.Fatal("a key read again as unspent", c)
	}
}
