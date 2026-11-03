package gui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/testenv"
)

func billingGUIHome(t *testing.T) string {
	t.Helper()
	transport := http.DefaultTransport
	local := transport.(*http.Transport).Clone()
	http.DefaultTransport = local
	testenv.Offline()
	t.Cleanup(func() {
		http.DefaultTransport = transport
		local.CloseIdleConnections()
	})
	h := t.TempDir()
	testenv.SetHome(t, h)
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, filepath.Join(h, name))
	}
	provider.ForgetAccounts()
	t.Cleanup(provider.ForgetAccounts)
	return h
}

func billingGUIAccount(t *testing.T, home string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"slug":"gpt-5.5","display_name":"Fixture","visibility":"list"}]}`))
	}))
	base := provider.CodexBase
	provider.CodexBase = server.URL + "/backend-api/codex"
	t.Cleanup(func() {
		server.Close()
		provider.CodexBase = base
	})
	claims := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return "h." + base64.RawURLEncoding.EncodeToString(b) + ".s"
	}
	b, err := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]any{
		"id_token":      claims(map[string]any{"email": "billing@example.com"}),
		"access_token":  claims(map[string]any{"exp": time.Now().Add(time.Hour).Unix()}),
		"refresh_token": "fixture-refresh", "account_id": "fixture-account",
	}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	provider.ForgetAccounts()
	p, err := provider.Find("codex")
	if err != nil || p.Account == nil {
		t.Fatalf("subscription fixture: %+v, %v", p, err)
	}
}

func billingGUISeed(t *testing.T, kind string, mode provider.BillingMode, exhausted provider.QuotaExhaustion) provider.Provider {
	t.Helper()
	p := provider.Provider{ID: "relay", Name: "Relay", Chat: "http://127.0.0.1:1/v1", Key: "fixture-key", Models: []string{"m"}}
	switch kind {
	case "preset":
		var err error
		p, err = provider.FromPreset("modelscope")
		if err != nil {
			t.Fatal(err)
		}
		p.Key = "fixture-key"
	case "subscription":
		p = provider.Provider{ID: "codex", Models: []string{"gpt-5.5"}}
	}
	p.Billing, p.QuotaExhausted = mode, exhausted
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	saved, err := provider.Find(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Billing != mode || saved.QuotaExhausted != exhausted {
		t.Fatalf("seed metadata: %q/%q, want %q/%q", saved.Billing, saved.QuotaExhausted, mode, exhausted)
	}
	return *saved
}

func billingGUICheck(t *testing.T, id string, mode provider.BillingMode, exhausted provider.QuotaExhaustion) *provider.Provider {
	t.Helper()
	p, err := provider.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Billing != mode || p.QuotaExhausted != exhausted {
		t.Fatalf("%s metadata = %q/%q, want %q/%q", id, p.Billing, p.QuotaExhausted, mode, exhausted)
	}
	b, err := json.Marshal(providerInfo(*p, nil))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"billing", "quotaExhausted"} {
		if _, ok := out[field]; ok {
			t.Errorf("providerJSON exposes inactive %s", field)
		}
	}
	return p
}

func TestBillingGUIOmissionKeepsDormantPolicies(t *testing.T) {
	policies := []struct {
		mode      provider.BillingMode
		exhausted provider.QuotaExhaustion
	}{
		{"", ""}, {provider.BillingFree, provider.QuotaPaid},
		{provider.BillingQuota, provider.QuotaStop}, {provider.BillingList, ""}, {"", provider.QuotaPaid},
	}
	for _, kind := range []string{"key", "preset", "subscription"} {
		for _, policy := range policies {
			t.Run(kind+"/"+string(policy.mode)+"/"+string(policy.exhausted), func(t *testing.T) {
				home := billingGUIHome(t)
				if kind == "subscription" {
					billingGUIAccount(t, home)
				}
				p := billingGUISeed(t, kind, policy.mode, policy.exhausted)
				for _, free := range []bool{true, false} {
					body := fmt.Sprintf(`{"id":%q,"name":%q,"preset":%q,"key":"","countFree":%t}`, p.ID, p.Name, p.Preset, free)
					if kind == "key" {
						body = strings.TrimSuffix(body, "}") + `,"chat":"http://127.0.0.1:1/v1"}`
					}
					if code, out := providerPost(t, "save", body); code != 200 {
						t.Fatalf("save: %d %v", code, out)
					}
					got := billingGUICheck(t, p.ID, policy.mode, policy.exhausted)
					if got.CountFree != free {
						t.Fatalf("legacy countFree = %v, want %v", got.CountFree, free)
					}
					if got.FreeModel("unlisted-fixture") != free {
						t.Fatalf("dormant metadata displaced legacy classification")
					}
				}
			})
		}
	}
}

func TestBillingGUICopyKeepsDormantPolicies(t *testing.T) {
	for _, kind := range []string{"key", "preset"} {
		t.Run(kind, func(t *testing.T) {
			billingGUIHome(t)
			p := billingGUISeed(t, kind, provider.BillingQuota, provider.QuotaPaid)
			body := fmt.Sprintf(`{"id":"copied","name":"Copied","preset":%q,"copyOf":%q,"new":true,"countFree":true}`, p.Preset, p.ID)
			if kind == "key" {
				body = strings.TrimSuffix(body, "}") + `,"chat":"http://127.0.0.1:1/v1"}`
			}
			if code, out := providerPost(t, "save", body); code != 200 {
				t.Fatalf("copy: %d %v", code, out)
			}
			got := billingGUICheck(t, "copied", p.Billing, p.QuotaExhausted)
			if got.Key != p.Key || !got.CountFree {
				t.Fatalf("copy lost legacy fields: %+v", got)
			}
			billingGUICheck(t, p.ID, p.Billing, p.QuotaExhausted)
		})
	}
}

func TestBillingGUIRenameKeepsDormantPolicies(t *testing.T) {
	billingGUIHome(t)
	p := billingGUISeed(t, "key", provider.BillingFree, provider.QuotaPaid)
	if code, out := providerPost(t, "save", `{"id":"renamed","from":"relay","name":"Renamed","chat":"http://127.0.0.1:1/v1"}`); code != 200 {
		t.Fatalf("rename: %d %v", code, out)
	}
	billingGUICheck(t, "renamed", p.Billing, p.QuotaExhausted)
	if alias, err := provider.Find("relay"); err != nil || alias.ID != "renamed" {
		t.Fatalf("old id must resolve to the renamed provider: %+v, %v", alias, err)
	}
}

// Snapshot the sandbox bytes, including credentials, picks and lifecycle
// state, so rejection cannot hide an earlier partial write.
func billingGUIFiles(t *testing.T, home string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestBillingGUIExplicitFieldsFailBeforeSideEffects(t *testing.T) {
	home := billingGUIHome(t)
	billingGUIAccount(t, home)
	billingGUISeed(t, "key", provider.BillingQuota, provider.QuotaPaid)
	billingGUISeed(t, "preset", provider.BillingFree, provider.QuotaStop)
	billingGUISeed(t, "subscription", provider.BillingList, provider.QuotaPaid)
	if err := provider.ReadyLifecycle(); err != nil {
		t.Fatal(err)
	}
	s := settings.Load()
	s.ModelNames = map[string]string{"relay/m": "Original"}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	before := billingGUIFiles(t, home)
	for _, action := range []struct{ name, action, body string }{
		{"edit", "save", `{"id":"relay","name":"Changed","chat":"http://127.0.0.1:1/v1","key":"new-key\nsecond-key","modelPrefs":{"m":{"name":"Changed"}},"outputs":{"m":123}}`},
		{"add", "save", `{"id":"added","name":"Added","chat":"http://127.0.0.1:1/v1","key":"fixture-key","new":true}`},
		{"copy", "save", `{"id":"copied","name":"Copied","chat":"http://127.0.0.1:1/v1","copyOf":"relay","new":true}`},
		{"rename", "save", `{"id":"renamed","from":"relay","chat":"http://127.0.0.1:1/v1"}`},
		{"preset", "save", `{"id":"modelscope","preset":"modelscope","countFree":true}`},
		{"subscription", "save", `{"id":"codex","models":["gpt-5.4-mini"],"countFree":true}`},
		{"delete", "delete", `{"id":"relay"}`},
	} {
		for _, field := range []string{"billing", "quotaExhausted", "Billing", "QuotaExhausted"} {
			for _, value := range []string{`null`, `""`, `"free"`, `"paid"`, `"invalid"`, `42`, `{}`, `[]`} {
				t.Run(action.name+"/"+field+"/"+value, func(t *testing.T) {
					body := strings.TrimSuffix(action.body, "}") + fmt.Sprintf(`,%q:%s}`, field, value)
					code, out := providerPost(t, action.action, body)
					if code != 400 || !strings.Contains(out["error"], "not active yet") {
						t.Fatalf("explicit dormant field: %d %v", code, out)
					}
					if after := billingGUIFiles(t, home); !reflect.DeepEqual(after, before) {
						t.Fatal("rejected request changed sandbox files")
					}
				})
			}
		}
	}
}

func TestBillingGUILegacyPriceAnnotationStaysActive(t *testing.T) {
	billingGUIHome(t)
	p := billingGUISeed(t, "key", provider.BillingFree, provider.QuotaPaid)
	s := settings.Load()
	s.ModelPrices = map[string]settings.ModelPrice{"relay/*": settings.StatedPrice(catalog.Price{}), "relay/m": settings.StatedPrice(catalog.Price{Input: 1})}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	if free, priced := provider.FreeBy(s, p.ID, catalog.Model{ID: "m"}); p.FreeModel("m") || free || priced != provider.PricedPaid {
		t.Fatal("dormant free displaced exact paid price")
	}
	if free, priced := provider.FreeBy(s, p.ID, catalog.Model{ID: "other"}); !p.FreeModel("other") || !free || priced != provider.PricedProvider {
		t.Fatal("legacy wildcard free no longer works")
	}
	info := providerInfo(p, nil)
	if info.Whole == nil || !info.Whole.Free {
		t.Fatalf("legacy whole-provider control: %+v", info.Whole)
	}
	found := false
	for _, m := range info.Models {
		if m.ID == "m" {
			found = true
			if m.Free || m.Priced != provider.PricedPaid {
				t.Fatalf("legacy model annotation: %+v", m)
			}
		}
	}
	if !found {
		t.Fatal("fixture model is missing from providerJSON")
	}
	delete(s.ModelPrices, "relay/*")
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	if p.FreeModel("other") {
		t.Fatal("removing legacy free price had no effect")
	}
	if info := providerInfo(p, nil); info.Whole != nil {
		t.Fatalf("legacy whole-provider control stayed set: %+v", info.Whole)
	}
}
