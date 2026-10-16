package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/plugin"
)

// lifeHome is a test home with an upstream that records the User-Agent it
// was sent, and two confirmed Codex profiles told apart by version.
type lifeHome struct {
	t       *testing.T
	srv     *httptest.Server
	ua      atomic.Value
	a, b    identity.Profile
	epoch   string
	gen     uint64
	retired []lifecycle.Retired
}

func newLifeHome(t *testing.T) *lifeHome {
	t.Helper()
	detectHome(t)
	h := &lifeHome{t: t, gen: 1}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ua.Store(r.Header.Get("User-Agent"))
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(h.srv.Close)
	h.a, h.b = lifeProfile(t, "0.159.2"), lifeProfile(t, "0.160.0")
	for _, p := range []identity.Profile{h.a, h.b} {
		if err := identity.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if h.epoch, err = lifecycle.NewID(); err != nil {
		t.Fatal(err)
	}
	return h
}

func lifeProfile(t *testing.T, version string) identity.Profile {
	t.Helper()
	hd := http.Header{
		"User-Agent": {"codex_cli_rs/" + version + " (Mac OS 26.6.0; arm64) Apple_Terminal/455"},
		"Originator": {"codex_cli_rs"},
		"Version":    {version},
	}
	id, err := identity.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p, err := identity.Candidate(id, identity.ClientCodex, hd, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Second)
	p.ConfirmedAt = &at
	return p
}

func (h *lifeHome) row(id string) Provider {
	return Provider{ID: id, Name: id, Key: "sk-test", Chat: h.srv.URL + "/v1", Models: []string{"gpt-5"}}
}

// write puts providers.json and, when ids is not nil, lifecycle.json
// describing it: each id with its instance, as a coordinated commit leaves them.
func (h *lifeHome) write(rows []Provider, ids map[string]string) {
	h.t.Helper()
	b, err := json.MarshalIndent(file{Providers: rows}, "", "  ")
	if err != nil {
		h.t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(appdir.Config(), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(Path(), b, 0o600); err != nil {
		h.t.Fatal(err)
	}
	if ids == nil {
		return
	}
	h.gen++
	m := lifecycle.Meta{Version: lifecycle.Version, Epoch: h.epoch, Gen: h.gen, Digest: lifecycle.Digest(b),
		Providers: map[string]lifecycle.Provider{}, Retired: h.retired}
	for id, inst := range ids {
		m.Providers[id] = lifecycle.Provider{Instance: inst, Rev: 1}
	}
	mb, err := lifecycle.EncodeMeta(m)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(lifecycle.Path(), mb, 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *lifeHome) retire(inst string, bound bool) {
	h.retired = append(h.retired, lifecycle.Retired{Instance: inst, Bound: bound, Gen: h.gen + 1})
}

func newInstance(t *testing.T) string {
	t.Helper()
	id, err := lifecycle.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// sign signs p's request and asks the upstream with it: the User-Agent the
// upstream received, or the error that stopped it.
func (h *lifeHome) sign(p Provider) (string, error) {
	h.t.Helper()
	h.ua.Store("")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader("{}"))
	if err != nil {
		h.t.Fatal(err)
	}
	if err := p.Sign(context.Background(), req, Chat, nil); err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return h.ua.Load().(string), nil
}

func (h *lifeHome) find(id string) Provider {
	h.t.Helper()
	p, err := Find(id)
	if err != nil {
		h.t.Fatal(err)
	}
	return *p
}

func uaOf(p identity.Profile) string { return p.Fingerprint.UserAgent }

// A value read before its provider was renamed signs as the same provider,
// under its new id's binding; the old id, taken by another provider since,
// is never borrowed.
func TestARenamedProviderKeepsItsOwnProfile(t *testing.T) {
	h := newLifeHome(t)
	i1 := newInstance(t)
	h.write([]Provider{h.row("relay")}, map[string]string{"relay": i1})
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	old := h.find("relay")

	// relay → relay2 (binding moved with it); a new provider takes "relay"
	i2 := newInstance(t)
	h.write([]Provider{h.row("relay2"), h.row("relay")}, map[string]string{"relay2": i1, "relay": i2})
	if err := identity.Unbind("relay"); err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind("relay2", h.a.ID); err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind("relay", h.b.ID); err != nil {
		t.Fatal(err)
	}
	got, err := h.sign(old)
	if err != nil {
		t.Fatalf("the renamed provider's old value failed: %v", err)
	}
	if got != uaOf(h.a) {
		t.Fatalf("old value of renamed relay sent %q, want its own profile %q (not the new relay's %q)", got, uaOf(h.a), uaOf(h.b))
	}
	if got, _ := h.sign(h.find("relay")); got != uaOf(h.b) {
		t.Fatalf("the new relay sent %q, want %q", got, uaOf(h.b))
	}
}

// A provider deleted while bound and added again under the same id is
// another provider: a value read before the delete is stale rather than
// signed with the new one's profile.
func TestARecreatedIDIsNotTheOldProvider(t *testing.T) {
	for _, bound := range []bool{true, false} {
		name := map[bool]string{true: "deleted bound", false: "deleted unbound"}[bound]
		t.Run(name, func(t *testing.T) {
			h := newLifeHome(t)
			i1 := newInstance(t)
			h.write([]Provider{h.row("relay")}, map[string]string{"relay": i1})
			if bound {
				if err := identity.Bind("relay", h.a.ID); err != nil {
					t.Fatal(err)
				}
			}
			old := h.find("relay")
			if old.life.kind != lifeManaged || old.life.instance != i1 {
				t.Fatalf("Find gave marker %+v, want managed %s", old.life, i1)
			}

			h.retire(i1, bound)
			h.write([]Provider{h.row("relay")}, map[string]string{"relay": newInstance(t)})
			if err := identity.Bind("relay", h.b.ID); err != nil {
				t.Fatal(err)
			}
			got, err := h.sign(old)
			if bound {
				if !errors.Is(err, ErrStale) {
					t.Fatalf("old value of a provider deleted while bound sent %q (err %v), want ErrStale", got, err)
				}
			} else {
				if err != nil || got == uaOf(h.b) || got == uaOf(h.a) {
					t.Fatalf("old value of a provider deleted unbound sent %q (err %v), want magpie's own User-Agent as before", got, err)
				}
			}
			if got, _ := h.sign(h.find("relay")); got != uaOf(h.b) {
				t.Fatalf("the new relay sent %q, want %q", got, uaOf(h.b))
			}
		})
	}
}

// A tombstone gone (more deletes since) is not taken as "deleted unbound".
func TestAnEvictedTombstoneIsStale(t *testing.T) {
	h := newLifeHome(t)
	i1 := newInstance(t)
	h.write([]Provider{h.row("relay")}, map[string]string{"relay": i1})
	old := h.find("relay")
	h.write([]Provider{h.row("other")}, map[string]string{"other": newInstance(t)})
	if _, err := h.sign(old); !errors.Is(err, ErrStale) {
		t.Fatalf("a value whose instance is neither stored nor retired signed: %v", err)
	}
}

// Sign takes the profile from one identity read. An unbind and a bind to
// another profile between the snapshot and the header lookup can't give a
// request the second profile under a decision taken on the first.
func TestSignDecidesFromOneIdentityRead(t *testing.T) {
	h := newLifeHome(t)
	i1 := newInstance(t)
	h.write([]Provider{h.row("relay")}, map[string]string{"relay": i1})
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	p := h.find("relay")
	was := readSnapshot
	t.Cleanup(func() { readSnapshot = was })
	var flipped atomic.Bool
	readSnapshot = func() (identity.Snapshot, error) {
		s, err := was()
		if flipped.CompareAndSwap(false, true) {
			// another magpie unbinds and binds another profile now
			if err := identity.Unbind("relay"); err != nil {
				t.Error(err)
			}
			if err := identity.Bind("relay", h.b.ID); err != nil {
				t.Error(err)
			}
		}
		return s, err
	}
	got, err := h.sign(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != uaOf(h.a) {
		t.Fatalf("Sign sent %q, want the profile of the one read it decided on, %q", got, uaOf(h.a))
	}
	if got, _ := h.sign(p); got != uaOf(h.b) {
		t.Fatalf("the next Sign sent %q, want the new binding %q", got, uaOf(h.b))
	}
}

// Without lifecycle.json a value carries no marker and signs by its
// current id, bound or not, exactly as before (legacy).
func TestNoLifecycleStateIsLegacy(t *testing.T) {
	h := newLifeHome(t)
	h.write([]Provider{h.row("relay"), h.row("plain")}, nil)
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	p := h.find("relay")
	if p.life != (life{}) {
		t.Fatalf("Find without lifecycle.json gave marker %+v, want the zero value", p.life)
	}
	if got, err := h.sign(p); err != nil || got != uaOf(h.a) {
		t.Fatalf("legacy bound relay sent %q (%v), want %q", got, err, uaOf(h.a))
	}
	if got, err := h.sign(h.find("plain")); err != nil || got == uaOf(h.a) || got == "" {
		t.Fatalf("legacy unbound provider sent %q (%v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(appdir.Config(), "lifecycle.lock")); err == nil {
		// identity.Bind takes it; drop it to check Sign doesn't
		t.Log("lock file from Bind")
	}
}

// lifecycle.json that is from a newer magpie, or can't be read, fails a
// plain provider's request closed, marked or not. A corrupt one makes new
// values legacy and an old managed one stale.
func TestUnusableLifecycleStateFailsClosed(t *testing.T) {
	h := newLifeHome(t)
	i1 := newInstance(t)
	h.write([]Provider{h.row("relay")}, map[string]string{"relay": i1})
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	old := h.find("relay")

	if err := os.WriteFile(lifecycle.Path(), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sign(old); !errors.Is(err, lifecycle.ErrUnknown) {
		t.Fatalf("an old value under unsupported lifecycle.json: %v, want ErrUnknown", err)
	}
	if got, err := h.sign(h.row("relay")); !errors.Is(err, lifecycle.ErrUnknown) {
		t.Fatalf("a literal under unsupported lifecycle.json sent %q (%v), want ErrUnknown", got, err)
	}
	if p := h.find("relay"); p.life.kind != lifeUnknown {
		t.Fatalf("Find under unsupported lifecycle.json gave %+v, want unknown", p.life)
	}

	if err := os.WriteFile(lifecycle.Path(), []byte(`{"version":1,`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sign(old); !errors.Is(err, ErrStale) {
		t.Fatalf("an old managed value under corrupt lifecycle.json: %v, want ErrStale", err)
	}
	if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
		t.Fatalf("a new value under corrupt lifecycle.json sent %q (%v), want legacy by id %q", got, err, uaOf(h.a))
	}
}

// providers.json changed by something that didn't update lifecycle.json
// (an older magpie, an editor), before anything adopted it: a provider in
// the file and in lifecycle.json signs as its instance, whatever changed
// (G2b-1's U1: one added row failed every bound provider closed). Only an
// id the change leaves a binding undecided for fails closed: a bound one
// gone from the file, and a new one an orphan binding already names.
func TestAnUnreconciledProviderFailsClosedWhenBound(t *testing.T) {
	h := newLifeHome(t)
	h.write([]Provider{h.row("relay"), h.row("plain"), h.row("gone")}, map[string]string{"relay": newInstance(t), "plain": newInstance(t), "gone": newInstance(t)})
	for id, p := range map[string]identity.Profile{"relay": h.a, "gone": h.b, "orphan": h.b} {
		if err := identity.Bind(id, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	managed, gone := h.find("relay"), h.find("gone")
	// an editor adds rows, edits relay's and removes gone's: the file no
	// longer matches lifecycle.json
	relay := h.row("relay")
	relay.Name = "edited by hand"
	b, _ := json.MarshalIndent(file{Providers: []Provider{relay, h.row("plain"), h.row("added"), h.row("orphan")}}, "", "  ")
	if err := os.WriteFile(Path(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	meta0, _ := os.ReadFile(lifecycle.Path())
	for name, p := range map[string]Provider{"old managed": managed, "new read": h.find("relay")} {
		if got, err := h.sign(p); err != nil || got != uaOf(h.a) {
			t.Errorf("%s bound relay, edited outside magpie, sent %q (%v), want its profile %q", name, got, err, uaOf(h.a))
		}
	}
	if got, err := h.sign(h.find("plain")); err != nil || got == "" || got == uaOf(h.a) {
		t.Errorf("an unbound provider under a mismatched digest sent %q (%v), want magpie's own", got, err)
	}
	if got, err := h.sign(h.find("added")); err != nil || got == uaOf(h.a) || got == uaOf(h.b) {
		t.Errorf("an unbound provider lifecycle.json doesn't know sent %q (%v)", got, err)
	}
	if got, err := h.sign(h.find("orphan")); !errors.Is(err, lifecycle.ErrUnknown) {
		t.Errorf("a new id an orphan binding names sent %q (%v), want ErrUnknown", got, err)
	}
	if got, err := h.sign(gone); !errors.Is(err, lifecycle.ErrUnknown) {
		t.Errorf("a bound provider removed outside magpie sent %q (%v), want ErrUnknown", got, err)
	}
	if _, err := os.Stat(lifecycle.JournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("signing wrote a journal (%v)", err)
	}
	// a request never adopts: that is a writer's, under the lock
	if meta1, _ := os.ReadFile(lifecycle.Path()); !bytes.Equal(meta0, meta1) {
		t.Errorf("signing wrote lifecycle.json")
	}
}

// A form not saved yet presents no binding, even one under the same id.
func TestATransientValuePresentsNoBinding(t *testing.T) {
	h := newLifeHome(t)
	h.write([]Provider{h.row("relay")}, nil)
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	got, err := h.sign(Transient(h.row("relay")))
	if err != nil || got == uaOf(h.a) {
		t.Fatalf("a transient value sent %q (%v), want no profile", got, err)
	}
	ms, err := Transient(h.row("relay")).List(context.Background())
	if err != nil || len(ms) == 0 || h.ua.Load().(string) == uaOf(h.a) {
		t.Fatalf("a transient list sent %q (%v, %d models)", h.ua.Load(), err, len(ms))
	}
	// an unreadable store still fails it closed
	if err := os.WriteFile(identity.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(identity.Path() + ".bak"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sign(Transient(h.row("relay"))); err == nil {
		t.Fatal("a transient value signed over an unreadable identity store")
	}
}

// Marked values are copies: WithKey and clone carry the marker they were
// read with, and an alias (Was) finds the canonical row's marker.
func TestMarkersRideOnCopies(t *testing.T) {
	h := newLifeHome(t)
	i1 := newInstance(t)
	r := h.row("relay2")
	r.Was = []string{"relay"}
	h.write([]Provider{r}, map[string]string{"relay2": i1})
	byWas := h.find("relay")
	if byWas.life.instance != i1 || byWas.life.issuedID != "relay2" {
		t.Fatalf("Find by Was gave %+v, want relay2's marker", byWas.life)
	}
	if k := byWas.WithKey(KeyAccount{Key: "sk-2"}); k.life != byWas.life {
		t.Fatalf("WithKey dropped the marker: %+v", k.life)
	}
	if c := byWas.clone(); c.life != byWas.life {
		t.Fatalf("clone dropped the marker: %+v", c.life)
	}
}

// A healthy managed sign takes no OS lock: it signs while another process
// holds lifecycle's lock.
func TestAHealthySignTakesNoLock(t *testing.T) {
	h := newLifeHome(t)
	h.write([]Provider{h.row("relay")}, map[string]string{"relay": newInstance(t)})
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	p := h.find("relay")
	unlock, err := lifecycle.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	done := make(chan error, 1)
	go func() { _, err := h.sign(p); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		unlock()
		<-done
		t.Fatal("a managed sign waited for lifecycle's lock")
	}
}

// Accounts and plugins never read lifecycle state: their sign-in signs.
func TestAccountsAndPluginsReadNoLifecycleState(t *testing.T) {
	h := newLifeHome(t)
	var begun atomic.Int64
	was := beginLife
	t.Cleanup(func() { beginLife = was })
	beginLife = func(m lifecycle.Mode) (lifecycle.Mark, error) { begun.Add(1); return was(m) }
	for name, p := range map[string]Provider{
		"account": {ID: "codex", Account: &Account{}},
		"plugin":  {ID: "relay-plugin", Account: &Account{plugin: &plugin.Provider{}}},
	} {
		req := httptest.NewRequest(http.MethodPost, h.srv.URL, nil)
		if err := p.putIdentity(req); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := p.identityListHeaders(); err != nil {
			t.Errorf("%s list: %v", name, err)
		}
	}
	if n := begun.Load(); n != 0 {
		t.Fatalf("accounts and plugins read lifecycle state %d times", n)
	}
}

// A combined identity view's capture in a home with nothing stored creates
// nothing: no config folder, no lock file. (Building its providers lists
// accounts, which may, as provider.All did for this view before.)
func TestAnIdentityViewOfAnEmptyHomeWritesNothing(t *testing.T) {
	detectHome(t)
	if _, err := os.Stat(appdir.Config()); !errors.Is(err, os.ErrNotExist) {
		t.Skipf("the test home has %s before the view: %v", appdir.Config(), err)
	}
	v, err := ReadIdentityView(true)
	if err != nil {
		t.Fatal(err)
	}
	if es, err := os.ReadDir(appdir.Config()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an identity view's capture created %s: %v %v", appdir.Config(), es, err)
	}
	_ = v.Providers()
}

// The view's profiles, bindings and providers are from one capture: a
// bind made after it is not in it.
func TestAnIdentityViewIsOneCapture(t *testing.T) {
	h := newLifeHome(t)
	h.write([]Provider{h.row("relay")}, map[string]string{"relay": newInstance(t)})
	v, err := ReadIdentityView(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	if b := v.Snapshot.Bindings(); len(b) != 0 {
		t.Fatalf("the view saw a later bind: %v", b)
	}
	ps := v.Providers()
	if len(ps) != 1 || ps[0].ID != "relay" || ps[0].life.kind != lifeManaged {
		t.Fatalf("view providers = %+v", ps)
	}
}
