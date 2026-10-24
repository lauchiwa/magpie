package gui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// accountModelsPost posts accountmodels and answers its code, the failed
// answer, and on 200 the rev the providers reply lists id with.
func accountModelsPost(t *testing.T, id, body string) (int, map[string]string, string) {
	t.Helper()
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/accountmodels", strings.NewReader(body)))
	if w.Code != 200 {
		out := map[string]string{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("accountmodels: %d %s", w.Code, w.Body)
		}
		return w.Code, out, ""
	}
	var st struct {
		Providers []struct {
			ID  string `json:"id"`
			Rev string `json:"rev"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("accountmodels reply: %v", err)
	}
	for _, p := range st.Providers {
		if p.ID == id {
			return 200, nil, p.Rev
		}
	}
	t.Fatalf("the accountmodels reply doesn't list %s", id)
	return 0, nil, ""
}

// relayKey is the id of relay's first key, as the editor's picker names it.
func relayKey(t *testing.T) string {
	t.Helper()
	p, err := provider.Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	return p.KeyList()[0].ID
}

// The editor's Save posts its open account-model picks first, then the
// provider (#2862). The picks go with the rev the editor was opened with,
// and the save after them with the rev their reply gave: it is not refused
// as changed elsewhere, and both land.
func TestASaveAfterItsOwnAccountModelsStands(t *testing.T) {
	lifeGUIHome(t)
	rev := pageRev(t, "relay")
	code, out, next := accountModelsPost(t, "relay", fmt.Sprintf(`{"id":"relay","account":%q,"allow":["m"],"rev":%q}`, relayKey(t), rev))
	if code != 200 {
		t.Fatalf("accountmodels as read: %d %v", code, out)
	}
	if next == "" || next == rev {
		t.Fatalf("the accountmodels reply gave relay rev %q, the page read %q", next, rev)
	}
	body := fmt.Sprintf(`{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"key":"sk-a","rev":%q,"modelPrefs":{"m":{"name":"Mine"}}}`, next)
	if code, out := providerPost(t, "save", body); code != 200 {
		t.Fatalf("the save after its own account-model picks: %d %v", code, out)
	}
	p, err := provider.Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.AccountModels; len(got) != 1 {
		t.Fatalf("relay's account models after the save: %v", got)
	}
	if s := settings.Load(); s.ModelNames["relay/m"] != "Mine" {
		t.Fatalf("the save's model name was not made: %q", s.ModelNames["relay/m"])
	}
}

// A signed-in account's picks are its own, by id, as its Save is: the rev
// an account editor sends with them refuses nothing, a stale one neither.
func TestAnAccountsPicksAreByIDWithARev(t *testing.T) {
	h := sandboxHome(t)
	claims := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return "h." + base64.RawURLEncoding.EncodeToString(b) + ".s"
	}
	auth, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]any{
		"id_token":      claims(map[string]any{"email": "me@example.com"}),
		"access_token":  claims(map[string]any{"exp": time.Now().Add(time.Hour).Unix()}),
		"refresh_token": "r", "account_id": "acct-1"}})
	if err := os.MkdirAll(filepath.Join(h, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h, ".codex", "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	provider.ForgetAccounts()
	t.Cleanup(provider.ForgetAccounts)
	if err := provider.Save(relayRow("sk-a")); err != nil {
		t.Fatal(err)
	}
	if err := provider.ReadyLifecycle(); err != nil {
		t.Fatal(err)
	}
	for i, rev := range []string{pageRevOf(t, "codex"), "not-a-rev"} {
		allow := []string{"gpt-5.5", "gpt-5.4-mini"}[i]
		code, out, _ := accountModelsPost(t, "codex", fmt.Sprintf(`{"id":"codex","account":"me@example.com","allow":[%q],"rev":%q}`, allow, rev))
		if code != 200 {
			t.Fatalf("an account's picks with rev %q: %d %v", rev, code, out)
		}
		if _, got, err := provider.AccountModelsOf("codex", "me@example.com"); err != nil || len(got) != 1 || got[0] != allow {
			t.Fatalf("the account's models after picks with rev %q: %v %v", rev, got, err)
		}
	}
}

// pageRevOf is id's rev as the page is shown it, "" when it has none.
func pageRevOf(t *testing.T, id string) string {
	t.Helper()
	p, err := provider.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	return providerInfo(*p, nil).Rev
}

// Picks sent with a rev read before relay changed elsewhere are refused
// (stale) and write nothing; picks with no rev (a pill's own save, an older
// page) are written by id, as before.
func TestAccountModelsFromAnOlderReadAreRefused(t *testing.T) {
	lifeGUIHome(t)
	rev := pageRev(t, "relay")
	if err := provider.Save(relayRow("sk-edited")); err != nil {
		t.Fatal(err)
	}
	picks := `{"id":"relay","account":%q,"allow":["m"]%s}`
	key := relayKey(t)
	code, out, _ := accountModelsPost(t, "relay", fmt.Sprintf(picks, key, fmt.Sprintf(`,"rev":%q`, rev)))
	if code != 400 || out["code"] != "stale" {
		t.Fatalf("picks over a newer rev: %d %v", code, out)
	}
	if p, _ := provider.Find("relay"); len(p.AccountModels) != 0 || p.Key != "sk-edited" {
		t.Fatalf("relay after the refused picks: %q %v", p.Key, p.AccountModels)
	}
	if code, out, _ := accountModelsPost(t, "relay", fmt.Sprintf(picks, key, "")); code != 200 {
		t.Fatalf("picks with no rev: %d %v", code, out)
	}
	if p, _ := provider.Find("relay"); len(p.AccountModels) != 1 {
		t.Fatalf("relay's account models after picks with no rev: %v", p.AccountModels)
	}
}
