package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// relayRow is the provider these tests save, edit and make again: on an
// address that refuses at once, so a fetch after a new key asks nothing.
func relayRow(key string) provider.Provider {
	return provider.Provider{ID: "relay", Name: "Relay", Chat: "http://127.0.0.1:1/v1", Key: key, Models: []string{"m"}}
}

// lifeGUIHome is a home with relay saved and lifecycle.json readied, as a
// configured magpie's is.
func lifeGUIHome(t *testing.T) {
	t.Helper()
	identityHome(t)
	if err := provider.Save(relayRow("sk-a")); err != nil {
		t.Fatal(err)
	}
	if err := provider.ReadyLifecycle(); err != nil {
		t.Fatal(err)
	}
}

// pageRev is relay's rev as the page is shown it.
func pageRev(t *testing.T, id string) string {
	t.Helper()
	p, err := provider.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	rev := providerInfo(*p, nil).Rev
	if rev == "" {
		t.Fatalf("%s is shown with no rev", id)
	}
	return rev
}

// remake deletes relay and saves another under its id, as another magpie
// would between two of this one's writes.
func remake(t *testing.T) {
	t.Helper()
	if err := provider.Delete("relay"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(relayRow("sk-new")); err != nil {
		t.Fatal(err)
	}
}

func providerPost(t *testing.T, action, body string) (int, map[string]string) {
	t.Helper()
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/"+action, strings.NewReader(body)))
	out := map[string]string{}
	if w.Code != 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body)
		}
	}
	return w.Code, out
}

// An editor's Save is the provider, then its pasted keys, model names,
// reply limits and compaction thresholds, each its own write. Another
// magpie deleting relay and making a new one under its id between two of
// them: the steps after are refused, the new relay is given none of them,
// and the answer says the save stands and which step was refused.
func TestASaveChainLeavesARemadeProviderAlone(t *testing.T) {
	steps := []string{stepKeys, stepPrefs, stepOutputs, stepCompacts}
	for _, at := range steps {
		t.Run(at, func(t *testing.T) {
			lifeGUIHome(t)
			rev := pageRev(t, "relay")
			done := false
			betweenSteps = func(step string) {
				if step == at && !done {
					done = true
					remake(t)
				}
			}
			t.Cleanup(func() { betweenSteps = nil })
			body := fmt.Sprintf(`{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"key":"sk-a\nsk-b","rev":%q,`+
				`"modelPrefs":{"m":{"name":"Mine"}},"outputs":{"m":1000},"compacts":{"m":5000}}`, rev)
			code, out := providerPost(t, "save", body)
			if !done {
				t.Fatalf("the save never reached its %s step (%d %v)", at, code, out)
			}
			if code != 400 || out["code"] != "stale" || out["step"] != at || out["committed"] != "1" {
				t.Fatalf("a save whose %s step met a remade relay: %d %v", at, code, out)
			}
			p, err := provider.Find("relay")
			if err != nil {
				t.Fatal(err)
			}
			if p.Key != "sk-new" || len(p.Keys) != 0 {
				t.Fatalf("the new relay was given the old one's keys: %q %+v", p.Key, p.Keys)
			}
			// the steps before at were made on the old relay, which is
			// right; at and those after it are not made at all
			s := settings.Load()
			after := slices.Index(steps, at)
			for i, made := range []bool{s.ModelNames["relay/m"] != "", s.ModelOutputs["relay/m"] != 0, s.ModelCompacts["relay/m"] != 0} {
				if i+1 >= after && made {
					t.Fatalf("the %s step was made after the %s step was refused", steps[i+1], at)
				}
			}
		})
	}
}

// The same chain with nothing in between: every step lands, on relay.
func TestASaveChainOnTheProviderSaved(t *testing.T) {
	lifeGUIHome(t)
	body := fmt.Sprintf(`{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"key":"sk-a\nsk-b","rev":%q,`+
		`"modelPrefs":{"m":{"name":"Mine"}},"outputs":{"m":1000},"compacts":{"m":5000}}`, pageRev(t, "relay"))
	if code, out := providerPost(t, "save", body); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	p, err := provider.Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	if p.Key != "sk-a" || len(p.Keys) != 1 || p.Keys[0].Key != "sk-b" {
		t.Fatalf("relay's keys: %q %+v", p.Key, p.Keys)
	}
	s := settings.Load()
	if s.ModelNames["relay/m"] != "Mine" || s.ModelOutputs["relay/m"] != 1000 || s.ModelCompacts["relay/m"] != 5000 {
		t.Fatalf("relay's settings: %q %d %d", s.ModelNames["relay/m"], s.ModelOutputs["relay/m"], s.ModelCompacts["relay/m"])
	}
}

// A draft the page read before relay changed is refused (stale), and what
// changed meanwhile stays; one sent with no rev (an older page) is written
// by id, as before.
func TestASaveFromAnOlderReadIsRefused(t *testing.T) {
	lifeGUIHome(t)
	rev := pageRev(t, "relay")
	if err := provider.Save(relayRow("sk-edited")); err != nil {
		t.Fatal(err)
	}
	draft := `{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"key":"sk-page"%s}`
	code, out := providerPost(t, "save", fmt.Sprintf(draft, fmt.Sprintf(`,"rev":%q`, rev)))
	if code != 400 || out["code"] != "stale" || out["committed"] != "" {
		t.Fatalf("a save over a newer rev: %d %v", code, out)
	}
	if p, _ := provider.Find("relay"); p.Key != "sk-edited" {
		t.Fatalf("relay's key is %q after the refused save", p.Key)
	}
	if code, out := providerPost(t, "save", fmt.Sprintf(draft, "")); code != 200 {
		t.Fatalf("a save with no rev: %d %v", code, out)
	}
	if p, _ := provider.Find("relay"); p.Key != "sk-page" {
		t.Fatalf("relay's key is %q after a save with no rev", p.Key)
	}
}

// A rename from the editor goes as the provider saved: the rename after a
// save whose provider was renamed away meanwhile is refused, and the one
// there is not renamed.
func TestASaveRenamesTheProviderSaved(t *testing.T) {
	lifeGUIHome(t)
	body := fmt.Sprintf(`{"id":"relay2","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"rev":%q}`, pageRev(t, "relay"))
	if code, out := providerPost(t, "save", body); code != 200 {
		t.Fatalf("save and rename: %d %v", code, out)
	}
	if _, err := provider.Find("relay2"); err != nil {
		t.Fatalf("relay2 after the rename: %v", err)
	}
	if err := provider.Rename("relay2", "relay"); err != nil {
		t.Fatal(err)
	}
	done := false
	betweenSteps = func(step string) {
		if step == stepRename && !done {
			done = true
			remake(t)
		}
	}
	t.Cleanup(func() { betweenSteps = nil })
	body = fmt.Sprintf(`{"id":"relay3","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"rev":%q}`, pageRev(t, "relay"))
	code, out := providerPost(t, "save", body)
	if code != 400 || out["code"] != "stale" || out["step"] != stepRename || out["committed"] != "1" {
		t.Fatalf("a rename after the saved provider was remade: %d %v", code, out)
	}
	if _, err := provider.Find("relay3"); err == nil {
		t.Fatal("the new relay was renamed")
	}
	if p, _ := provider.Find("relay"); p.Key != "sk-new" {
		t.Fatalf("relay is %q, want the new one", p.Key)
	}
}

// A rename whose settings and agents step fails is done again once, here,
// as the CLI does, and never the rename; failing again, the answer says the
// rename is saved and that step is not.
func TestARenameStepIsDoneAgainOnce(t *testing.T) {
	lifeGUIHome(t)
	redos := 0
	renameChecked = func(p provider.Provider, to string) (provider.Provider, []string, error) {
		q, moved, err := agent.RenameChecked(p, to)
		if err != nil {
			return q, moved, err
		}
		return q, moved, provider.FollowUp(agent.StepAgents, errors.New("injected"), func() error { redos++; return nil })
	}
	t.Cleanup(func() { renameChecked = agent.RenameChecked })
	body := fmt.Sprintf(`{"id":"relay2","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"rev":%q}`, pageRev(t, "relay"))
	if code, out := providerPost(t, "save", body); code != 200 || redos != 1 {
		t.Fatalf("a rename whose agents step failed once: %d %v, done again %d times", code, out, redos)
	}
	renameChecked = agent.RenameChecked
	// a settings.json Save refuses, both times
	if err := os.WriteFile(settings.Path(), []byte(`{"theme":"no-such-theme","visible":{"codex":["relay2"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	body = fmt.Sprintf(`{"id":"relay3","from":"relay2","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"rev":%q}`, pageRev(t, "relay2"))
	code, out := providerPost(t, "save", body)
	if code != 400 || out["code"] != "follow_up" || !strings.Contains(out["step"], provider.StepSettings) || out["committed"] != "1" {
		t.Fatalf("a rename whose settings step failed twice: %d %v", code, out)
	}
	if _, err := provider.Find("relay3"); err != nil {
		t.Fatalf("the rename itself is not saved: %v", err)
	}
}

// Delete and off go to the provider the page read: one made under its id
// since is left as it is (stale). With no rev they go by id, as before.
func TestDeleteAndOffAsRead(t *testing.T) {
	for _, action := range []string{"delete", "off"} {
		t.Run(action, func(t *testing.T) {
			lifeGUIHome(t)
			rev := pageRev(t, "relay")
			remake(t)
			code, out := providerPost(t, action, fmt.Sprintf(`{"id":"relay","rev":%q}`, rev))
			if code != 400 || out["code"] != "stale" {
				t.Fatalf("%s of the provider before the new relay: %d %v", action, code, out)
			}
			p, err := provider.Find("relay")
			if err != nil || p.Off || p.Key != "sk-new" {
				t.Fatalf("the new relay after the refused %s: %v %+v", action, err, p)
			}
			if code, out := providerPost(t, action, fmt.Sprintf(`{"id":"relay","rev":%q}`, pageRev(t, "relay"))); code != 200 {
				t.Fatalf("%s as read now: %d %v", action, code, out)
			}
			p, err = provider.Find("relay")
			if action == "delete" && err == nil || action == "off" && (err != nil || !p.Off) {
				t.Fatalf("relay after its %s: %v %+v", action, err, p)
			}
			if action == "off" {
				if code, out := providerPost(t, "on", `{"id":"relay"}`); code != 200 {
					t.Fatalf("on with no rev: %d %v", code, out)
				}
				if p, _ := provider.Find("relay"); p.Off {
					t.Fatal("relay still off after an on with no rev")
				}
			}
		})
	}
}

// A binding goes to the provider the identity panel listed, by the rev it
// listed with: once relay is deleted and made again, binding or unbinding
// with that rev is refused, and the new relay is left unbound.
func TestBindAndUnbindAsRead(t *testing.T) {
	lifeGUIHome(t)
	prof := codexCandidate(t)
	if err := identity.Confirm(prof.ID, prof.CreatedAt); err != nil {
		t.Fatal(err)
	}
	mux, _, _ := identityMux(t, false)
	_, raw := identityCall(t, mux, "GET", "/api/identity", "")
	var rev string
	for _, p := range identityStateOf(t, raw).Providers {
		if p.ID == "relay" {
			rev = p.Rev
		}
	}
	if rev == "" || rev != pageRev(t, "relay") {
		t.Fatalf("the identity panel lists relay with rev %q, the providers page with %q", rev, pageRev(t, "relay"))
	}
	if code, body := identityCall(t, mux, "POST", "/api/identity/bind", fmt.Sprintf(`{"provider":"relay","profile":%q,"rev":%q}`, prof.ID, rev)); code != 200 {
		t.Fatalf("bind as listed: %d %s", code, body)
	}
	if b, _ := identity.Bindings(); b["relay"] != prof.ID {
		t.Fatalf("bindings %v", b)
	}
	remake(t)
	for _, path := range []string{"/api/identity/bind", "/api/identity/unbind"} {
		code, body := identityCall(t, mux, "POST", path, fmt.Sprintf(`{"provider":"relay","profile":%q,"rev":%q}`, prof.ID, rev))
		if code != 400 || !strings.Contains(body, `"code":"stale"`) {
			t.Fatalf("%s with the rev of the relay deleted: %d %s", path, code, body)
		}
	}
	if b, _ := identity.Bindings(); b["relay"] != "" {
		t.Fatalf("the new relay was bound: %v", b)
	}
	// an older page, with no rev: by id, as before
	if code, body := identityCall(t, mux, "POST", "/api/identity/bind", fmt.Sprintf(`{"provider":"relay","profile":%q}`, prof.ID)); code != 200 {
		t.Fatalf("bind with no rev: %d %s", code, body)
	}
	if code, body := identityCall(t, mux, "POST", "/api/identity/unbind", `{"provider":"relay"}`); code != 200 {
		t.Fatalf("unbind with no rev: %d %s", code, body)
	}
	if b, _ := identity.Bindings(); b["relay"] != "" {
		t.Fatalf("relay still bound after an unbind with no rev: %v", b)
	}
}

// fail tells the page what it tells apart, from the error's kind and never
// its words: a commit left to its journal, a value read before a change, a
// sync or restore refused over a change made here, and a step after a
// committed change. Each is still a 400.
func TestFailSaysLifecycleCodes(t *testing.T) {
	stale := &provider.StaleError{ID: "relay"}
	for name, c := range map[string]struct {
		err             error
		code, step, com string
	}{
		"stale":            {fmt.Errorf("save: %w", stale), "stale", "", ""},
		"conflict":         {&provider.ConflictError{Reason: "it changed since it was read"}, "stale", "", ""},
		"restore conflict": {fmt.Errorf("restore: %w", &provider.ConflictError{Reason: "x"}), "stale", "", ""},
		"pending":          {fmt.Errorf("rename: %w", provider.ErrPending), "pending", "", ""},
		"follow-up":        {provider.FollowUp(provider.StepSettings, errors.New("disk full"), nil), "follow_up", provider.StepSettings, "1"},
		"stale step":       {afterSave(stepKeys, stale), "stale", stepKeys, "1"},
		"pending step":     {afterSave(stepPrefs, fmt.Errorf("x: %w", provider.ErrPending)), "pending", stepPrefs, "1"},
		"step's own":       {afterSave(stepOutputs, errors.New("disk full")), "", "", ""},
		"other":            {errors.New("provider changed since it was read; read it again"), "", "", ""},
	} {
		w := httptest.NewRecorder()
		fail(w, c.err)
		var out map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if w.Code != 400 || out["code"] != c.code || out["step"] != c.step || out["committed"] != c.com || out["error"] != c.err.Error() {
			t.Errorf("%s: %d %v", name, w.Code, out)
		}
		// the step's own cause, for the page to say beside its own words
		if want := map[string]string{"follow-up": "disk full"}[name]; out["reason"] != want {
			t.Errorf("%s: reason %q, want %q", name, out["reason"], want)
		}
	}
}

// A new provider's save is a chain too, checked against the one added:
// another magpie deleting it and adding one under its id before its pasted
// keys leaves the new one without them.
func TestANewProviderChainIsTheOneAdded(t *testing.T) {
	lifeGUIHome(t)
	done := false
	betweenSteps = func(step string) {
		if step == stepKeys && !done {
			done = true
			if err := provider.Delete("other"); err != nil {
				t.Fatal(err)
			}
			if err := provider.Save(provider.Provider{ID: "other", Name: "Other", Chat: "http://127.0.0.1:1/v1", Key: "sk-theirs", Models: []string{"m"}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { betweenSteps = nil })
	code, out := providerPost(t, "save", `{"new":true,"id":"other","name":"Other","chat":"http://127.0.0.1:1/v1","models":["m"],"key":"sk-mine\nsk-more"}`)
	if !done {
		t.Fatalf("the new provider's save never reached its keys step (%d %v)", code, out)
	}
	if code != 400 || out["code"] != "stale" || out["step"] != stepKeys || out["committed"] != "1" {
		t.Fatalf("keys after the new provider was made again: %d %v", code, out)
	}
	if p, _ := provider.Find("other"); p == nil || p.Key != "sk-theirs" || len(p.Keys) != 0 {
		t.Fatalf("the one made again was given the new one's keys: %+v", p)
	}
}

// The model list a save fetches is the saved provider's: one made under its
// id after the save, with another key, is not asked for its list there.
func TestASaveFetchesOnlyTheProviderSaved(t *testing.T) {
	lifeGUIHome(t)
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	t.Cleanup(srv.Close)
	rev := pageRev(t, "relay")
	betweenSteps = func(step string) {
		if step == stepFetch {
			betweenSteps = nil
			if err := provider.Delete("relay"); err != nil {
				t.Fatal(err)
			}
			if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Chat: srv.URL + "/v1", Key: "sk-theirs", Models: []string{"m"}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { betweenSteps = nil })
	body := fmt.Sprintf(`{"id":"relay","from":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":["m"],"key":"sk-b","rev":%q}`, rev)
	if code, out := providerPost(t, "save", body); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	if asked != 0 {
		t.Fatalf("the provider made after the save was asked for its list %d times", asked)
	}
}
