package provider

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// tokenHome is a managed home with relay saved and bound to nothing.
func tokenHome(t *testing.T) *lifeHome {
	t.Helper()
	h := newLifeHome(t)
	h.write([]Provider{h.row("relay")}, map[string]string{"relay": newInstance(t)})
	return h
}

// The page's rev is the marker the value was read with, and back from it
// the value writes as the provider read: a token that doesn't decode, or
// was issued for another id, is a value with no lifecycle state.
func TestTokenIsTheMarkerReadWith(t *testing.T) {
	h := tokenHome(t)
	p := h.find("relay")
	rev := p.Token()
	if rev == "" {
		t.Fatal("a managed provider has no token")
	}
	if got := FromToken(Provider{ID: "relay"}, rev).life; got != p.life {
		t.Fatalf("FromToken(Token) = %+v, read with %+v", got, p.life)
	}
	if got := FromToken(Provider{ID: "relay"}, "").life; got != (life{}) {
		t.Fatalf("no token is %+v, want the zero marker", got)
	}
	for name, bad := range map[string]string{
		"not base64":  "%%%",
		"not json":    "bm90IGpzb24",
		"another id":  FromToken(Provider{ID: "relay"}, rev).withID(t, "other").Token(),
		"no instance": tokenOf(t, pageToken{V: pageTokenV, Kind: "m", ID: "relay", E: p.life.epoch}),
		"future":      tokenOf(t, pageToken{V: pageTokenV + 1, Kind: "m", ID: "relay", E: p.life.epoch, I: p.life.instance}),
	} {
		if got := FromToken(Provider{ID: "relay"}, bad).life; got.kind != lifeUnknown || got.issuedID != "relay" {
			t.Errorf("%s: %+v, want unknown as relay", name, got)
		}
	}
	u := life{kind: lifeUnreconciled, epoch: "e", issuedID: "relay", row: "r"}
	if got := FromToken(Provider{ID: "relay"}, (Provider{ID: "relay", life: u}).Token()).life; got != u {
		t.Fatalf("an unreconciled marker back: %+v, want %+v", got, u)
	}
	unknown := Provider{ID: "relay", life: life{kind: lifeUnknown, issuedID: "relay"}}
	if unknown.Token() == "" {
		t.Fatal("an unknown marker has no token: the page would send none, and write by id")
	}
}

// withID is p as read under id: its token names id.
func (p Provider) withID(t *testing.T, id string) Provider {
	t.Helper()
	p.ID, p.life.issuedID = id, id
	return p
}

// tokenOf is tok as the page would send it, made by hand so that a bad
// one can be.
func tokenOf(t *testing.T, tok pageToken) string {
	t.Helper()
	b, err := json.Marshal(tok)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// A value back from the page's rev is written only to the provider read:
// another one made under its id, or a newer rev of it, is refused.
func TestFromTokenWritesOnlyTheProviderRead(t *testing.T) {
	h := tokenHome(t)
	rev := h.find("relay").Token()
	edit := h.row("relay")
	edit.Key = "edited"
	if err := Save(edit); err != nil {
		t.Fatal(err)
	}
	draft := h.row("relay")
	draft.Key = "from-the-page"
	if err := Save(FromToken(draft, rev)); !errors.Is(err, ErrStale) {
		t.Fatalf("a draft saved over a newer rev: %v", err)
	}
	if got := h.find("relay").Key; got != "edited" {
		t.Fatalf("relay's key is %q after the refused save", got)
	}
	// the instance alone, for a delete: a newer rev is still the one read
	rev = h.find("relay").Token()
	if err := Delete("relay"); err != nil {
		t.Fatal(err)
	}
	if err := Save(h.row("relay")); err != nil {
		t.Fatal(err)
	}
	if err := DeleteChecked(FromToken(Provider{ID: "relay"}, rev)); !errors.Is(err, ErrStale) {
		t.Fatalf("a delete of the provider before the one made under its id: %v", err)
	}
	if _, err := Find("relay"); err != nil {
		t.Fatalf("the new relay is gone: %v", err)
	}
}

// CheckCurrent passes while the provider is the one at its id, whatever
// rev, and refuses once it was deleted and made again, or renamed away.
func TestCheckCurrentIsTheInstance(t *testing.T) {
	h := tokenHome(t)
	p := h.find("relay")
	if err := CheckCurrent(p); err != nil {
		t.Fatalf("relay as read: %v", err)
	}
	edit := h.row("relay")
	edit.Key = "edited"
	if err := Save(edit); err != nil {
		t.Fatal(err)
	}
	if err := CheckCurrent(p); err != nil {
		t.Fatalf("relay after an edit of it: %v", err)
	}
	// taken out of providers.json by hand, lifecycle.json not told yet
	before, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	h.write([]Provider{h.row("other")}, nil)
	if err := CheckCurrent(p); !errors.Is(err, ErrStale) {
		t.Fatalf("relay gone from providers.json: %v", err)
	}
	if err := os.WriteFile(Path(), before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	if err := CheckCurrent(p); !errors.Is(err, ErrStale) {
		t.Fatalf("relay renamed away: %v", err)
	}
	if err := Save(h.row("relay")); err != nil {
		t.Fatal(err)
	}
	if err := CheckCurrent(p); !errors.Is(err, ErrStale) {
		t.Fatalf("another relay made under its id: %v", err)
	}
	if !h.find("relay").SameInstance(h.find("relay")) || p.SameInstance(h.find("relay")) {
		t.Fatal("SameInstance doesn't tell the new relay from the one read")
	}
	if err := CheckCurrent(Transient(p)); err == nil {
		t.Fatal("a value never saved passed")
	}
	if err := CheckCurrent(Provider{ID: "gone"}); err != nil {
		t.Fatalf("a zero marker is by id, as before: %v", err)
	}
}

// CheckCurrent passes a value read before lifecycle.json knew its id while
// that is still so, and refuses it once a write adopted the id: the
// adopted provider has an instance of its own.
func TestCheckCurrentRefusesAnIdAdoptedSince(t *testing.T) {
	h := readyHome(t, nil, "relay")
	writeOutside(t, h.row("relay"), h.row("added"))
	added := h.find("added")
	if added.life.kind != lifeUnreconciled {
		t.Fatalf("added outside: %+v", added.life)
	}
	if err := CheckCurrent(added); err != nil {
		t.Fatalf("an unreconciled value, not adopted yet: %v", err)
	}
	if _, err := SaveChecked(h.find("added")); err != nil { // adopts it
		t.Fatal(err)
	}
	if err := CheckCurrent(added); !errors.Is(err, ErrStale) {
		t.Fatalf("an unreconciled value after its id was adopted: %v, want ErrStale", err)
	}
}

// AddChecked answers the provider with the marker its own commit gave it:
// a write checked against it is refused once another holds its id.
func TestAddCheckedIsTheOneAdded(t *testing.T) {
	h := tokenHome(t)
	row := h.row("added")
	row.Key = "sk-added"
	snap, err := AddChecked(row)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID != "added" || snap.life.kind != lifeManaged || !snap.SameInstance(h.find("added")) {
		t.Fatalf("AddChecked gave %q %+v", snap.ID, snap.life)
	}
	cp, err := AddCopyChecked(Provider{Name: "copy", Chat: row.Chat}, "added")
	if err != nil {
		t.Fatal(err)
	}
	if cp.ID == "added" || cp.life.kind != lifeManaged || cp.life.instance == snap.life.instance {
		t.Fatalf("AddCopyChecked gave %q %+v", cp.ID, cp.life)
	}
	if err := Delete("added"); err != nil {
		t.Fatal(err)
	}
	if err := Save(row); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddKeys("added", []string{"sk-more"}, "", Expect(snap)); !errors.Is(err, ErrStale) {
		t.Fatalf("keys added to the provider made after the one added: %v", err)
	}
	if p := h.find("added"); len(p.Keys) != 0 {
		t.Fatalf("the new one was given keys: %+v", p.Keys)
	}
}

// With no lifecycle.json every value is written by id, as before: no
// token, and nothing refused.
func TestNoLifecycleNoToken(t *testing.T) {
	h := newLifeHome(t)
	h.saveRows(h.row("relay"))
	p := h.find("relay")
	if p.Token() != "" {
		t.Fatalf("a legacy provider has token %q", p.Token())
	}
	if err := CheckCurrent(p); err != nil {
		t.Fatal(err)
	}
	if err := Save(FromToken(h.row("relay"), p.Token())); err != nil {
		t.Fatal(err)
	}
}
