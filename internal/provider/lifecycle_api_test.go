package provider

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/settings"
)

// rawRow is id's row as providers.json holds it now, unnormalized.
func rawRow(t *testing.T, id string) (Provider, bool) {
	t.Helper()
	f, err := read()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

func providersBytes(t *testing.T) []byte {
	t.Helper()
	b, _ := os.ReadFile(Path())
	return b
}

// A zero value (a literal, or any value read with no lifecycle.json) is
// written by id, as before lifecycle.json: under legacy no write makes one,
// and none is refused. Under lifecycle.json it replaces the row there,
// keeping its instance and binding. A zero marker adds no check, so it can
// never be stale where no lifecycle.json is.
func TestAZeroValueIsWrittenByID(t *testing.T) {
	h := newLifeHome(t)
	if err := Save(h.row("relay")); err != nil {
		t.Fatal(err)
	}
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	p := h.find("relay")
	if p.life.kind != lifeLegacy {
		t.Fatalf("read under legacy: %+v", p.life)
	}
	p.Name = "edited"
	if _, err := SaveChecked(p); err != nil {
		t.Fatalf("a zero value's save: %v", err)
	}
	if _, err := RenameChecked(p, "relay2"); err != nil {
		t.Fatalf("a zero value's rename: %v", err)
	}
	if err := DeleteChecked(Provider{ID: "relay2"}); err != nil {
		t.Fatalf("a zero value's delete: %v", err)
	}
	// a learner of a zero value checks nothing: it writes by id
	both := h.row("relay")
	both.AuthHeader = AuthBoth
	h.saveRows(both)
	LearnAuthHeader("relay", AuthBearer, Expect(h.find("relay")))
	if p, _ := rawRow(t, "relay"); p.AuthHeader != AuthBearer {
		t.Fatalf("a learner of a zero value wrote nothing: %q", p.AuthHeader)
	}
	if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a legacy write made lifecycle.json (%v)", err)
	}
	// under lifecycle.json, a literal replaces the row by id
	h, inst := managedHome(t, "relay")
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	lit := h.row("relay")
	lit.Name = "literal"
	if err := Save(lit); err != nil {
		t.Fatal(err)
	}
	if m := readMetaT(t); m.Providers["relay"].Instance != inst["relay"] {
		t.Fatalf("a literal's save gave relay another instance: %+v", m.Providers["relay"])
	}
	if got, err := h.sign(h.find("relay")); err != nil || got != uaOf(h.a) {
		t.Fatalf("relay after a literal's save sent %q (%v)", got, err)
	}
}

// A marked value is written as the provider it was read as: an edit built
// on an older rev, an id changed by Save, and a value of a provider deleted
// and added again under its id are each refused, and write nothing.
// SaveChecked answers the value as this write left it, so the next write
// built on it goes through.
func TestAMarkedSaveIsCheckedAgainstWhatHoldsItsID(t *testing.T) {
	h := readyHome(t, nil, "relay")
	read1 := h.find("relay")
	if read1.life.kind != lifeManaged || read1.life.rev != 1 {
		t.Fatalf("read: %+v", read1.life)
	}
	edit := read1
	edit.Name = "first"
	saved, err := SaveChecked(edit)
	if err != nil {
		t.Fatal(err)
	}
	m := readMetaT(t)
	if saved.life.instance != m.Providers["relay"].Instance || saved.life.rev != 2 || m.Providers["relay"].Rev != 2 {
		t.Fatalf("SaveChecked's marker %+v, lifecycle.json %+v", saved.life, m.Providers["relay"])
	}
	before := providersBytes(t)
	again := read1
	again.Name = "second, on the old read"
	if err := Save(again); !errors.Is(err, ErrStale) {
		t.Fatalf("a save built on rev 1 over rev 2: %v, want ErrStale", err)
	}
	if !bytes.Equal(before, providersBytes(t)) {
		t.Fatal("the refused save wrote providers.json")
	}
	saved.Name = "second"
	if _, err := SaveChecked(saved); err != nil {
		t.Fatalf("a save built on SaveChecked's value: %v", err)
	}
	moved := h.find("relay")
	moved.ID = "relay2"
	if err := Save(moved); err == nil || !strings.Contains(err.Error(), "Rename") {
		t.Fatalf("a save under another id than it was read: %v", err)
	}
	if _, ok := rawRow(t, "relay2"); ok {
		t.Fatal("the refused save added relay2")
	}
	// deleted and added again: another provider holds the id
	gone := h.find("relay")
	if err := Delete("relay"); err != nil {
		t.Fatal(err)
	}
	if err := Save(h.row("relay")); err != nil {
		t.Fatal(err)
	}
	gone.Name = "the old one"
	if err := Save(gone); !errors.Is(err, ErrStale) {
		t.Fatalf("a save of the deleted provider over the new one: %v, want ErrStale", err)
	}
	if err := DeleteChecked(gone); !errors.Is(err, ErrStale) {
		t.Fatalf("a delete of the deleted provider: %v, want ErrStale", err)
	}
	if p, _ := rawRow(t, "relay"); p.Name != "relay" {
		t.Fatalf("the new relay was written over: %+v", p)
	}
	// a value read with no lifecycle state is never taken as current
	unknown := h.find("relay")
	unknown.life = life{kind: lifeUnknown, issuedID: "relay"}
	if err := Save(unknown); !errors.Is(err, ErrStale) {
		t.Fatalf("an unknown value's save: %v, want ErrStale", err)
	}
	if err := DeleteChecked(unknown); !errors.Is(err, ErrStale) {
		t.Fatalf("an unknown value's delete: %v, want ErrStale", err)
	}
	if _, err := RenameChecked(unknown, "relay3"); !errors.Is(err, ErrStale) {
		t.Fatalf("an unknown value's rename: %v, want ErrStale", err)
	}
	if err := BindIdentity(unknown, h.a.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("an unknown value's bind: %v, want ErrStale", err)
	}
}

// A value read while providers.json differed from lifecycle.json (changed
// outside magpie, not adopted yet) is checked by its row, not its rev: the
// adoption under its own write bumps the rev, and must not refuse it. A
// second change outside to the same row since the read refuses it.
func TestAMarkedSaveOverAChangeMadeOutside(t *testing.T) {
	h := readyHome(t, nil, "relay", "other")
	relay := h.row("relay")
	relay.Name = "edited by hand"
	writeOutside(t, relay, h.row("other"))
	read1 := h.find("relay")
	if read1.life.kind != lifeManaged {
		t.Fatalf("read over a file lifecycle.json doesn't describe: %+v", read1.life)
	}
	edit := read1
	edit.Name = "edited in magpie"
	saved, err := SaveChecked(edit)
	if err != nil {
		t.Fatalf("a save of what was read, over the change it was read with: %v", err)
	}
	if saved.life.row != "" || saved.life.rev != readMetaT(t).Providers["relay"].Rev {
		t.Fatalf("SaveChecked's marker after the adoption: %+v", saved.life)
	}
	// the row changed outside again since this read
	read2 := h.find("relay")
	relay.Name = "edited by hand again"
	writeOutside(t, relay, h.row("other"))
	before := providersBytes(t)
	read2.Name = "on the read before the second change"
	if err := Save(read2); !errors.Is(err, ErrStale) {
		t.Fatalf("a save over a second change outside: %v, want ErrStale", err)
	}
	if !bytes.Equal(before, providersBytes(t)) {
		t.Fatal("the refused save wrote providers.json")
	}
	// read over a change outside, then changed outside again
	relay.Name = "edited by hand, a third time"
	writeOutside(t, relay, h.row("other"))
	read4 := h.find("relay")
	relay.Name = "edited by hand, a fourth time"
	writeOutside(t, relay, h.row("other"))
	before = providersBytes(t)
	read4.Name = "on a read over a change outside"
	if err := Save(read4); !errors.Is(err, ErrStale) {
		t.Fatalf("a save read over a change outside, changed outside again: %v, want ErrStale", err)
	}
	if !bytes.Equal(before, providersBytes(t)) {
		t.Fatal("the refused save wrote providers.json")
	}
	// another row changed outside is no reason to refuse this one
	read3 := h.find("relay")
	other := h.row("other")
	other.Name = "other, by hand"
	writeOutside(t, relay, other)
	read3.Name = "fine"
	if err := Save(read3); err != nil {
		t.Fatalf("a save while only another row changed outside: %v", err)
	}
}

// A value read before lifecycle.json knew its id (added outside magpie) is
// written only while that is still so: its own write adopts it. Adopted by
// another write meanwhile, it has an instance of its own, and the value is
// stale; so is one whose row changed outside since.
func TestAnUnreconciledValueIsWrittenOnlyWhileUnknown(t *testing.T) {
	h := readyHome(t, nil, "relay")
	writeOutside(t, h.row("relay"), h.row("added"), h.row("later"))
	added := h.find("added")
	if added.life.kind != lifeUnreconciled {
		t.Fatalf("added outside: %+v", added.life)
	}
	stale := added
	added.Name = "named in magpie"
	saved, err := SaveChecked(added)
	if err != nil {
		t.Fatalf("an unreconciled value's own save: %v", err)
	}
	m := readMetaT(t)
	if saved.life.kind != lifeManaged || saved.life.instance != m.Providers["added"].Instance {
		t.Fatalf("SaveChecked of an adopted id: %+v, lifecycle.json %+v", saved.life, m.Providers["added"])
	}
	stale.Name = "on the read before the adoption"
	if err := Save(stale); !errors.Is(err, ErrStale) {
		t.Fatalf("an unreconciled value after the id was adopted: %v, want ErrStale", err)
	}
	if err := DeleteChecked(stale); !errors.Is(err, ErrStale) {
		t.Fatalf("an unreconciled delete after the adoption: %v, want ErrStale", err)
	}
	if _, err := RenameChecked(stale, "added2"); !errors.Is(err, ErrStale) {
		t.Fatalf("an unreconciled rename after the adoption: %v, want ErrStale", err)
	}
	if err := BindIdentity(stale, h.a.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("an unreconciled bind after the adoption: %v, want ErrStale", err)
	}
	// a row changed outside since its read
	writeOutside(t, h.row("relay"), h.find("added"), h.row("late"))
	late := h.find("late")
	changed := h.row("late")
	changed.Name = "late, by hand"
	writeOutside(t, h.row("relay"), h.find("added"), changed)
	late.Name = "on the old row"
	if err := Save(late); !errors.Is(err, ErrStale) {
		t.Fatalf("an unreconciled value whose row changed outside: %v, want ErrStale", err)
	}
	// a delete checks only that the id is still unknown
	if err := DeleteChecked(late); err != nil {
		t.Fatalf("an unreconciled delete: %v", err)
	}
	if _, ok := rawRow(t, "late"); ok {
		t.Fatal("late is still there")
	}
}

// A transient value (a form not saved yet) is saved by id, as a new
// provider is; it can't be deleted, renamed or bound, as it is not stored,
// and a learner never writes what it found of one onto the stored row
// under the same id, which is another provider.
func TestATransientValueIsOnlyEverSaved(t *testing.T) {
	h := readyHome(t, nil, "relay")
	tr := Transient(h.row("relay"))
	for name, err := range map[string]error{
		"delete": DeleteChecked(tr),
		"rename": func() error { _, err := RenameChecked(tr, "relay2"); return err }(),
		"bind":   BindIdentity(tr, h.a.ID),
		"unbind": UnbindIdentity(tr),
	} {
		if !errors.Is(err, errTransient) {
			t.Errorf("a transient value's %s: %v, want errTransient", name, err)
		}
	}
	row, _ := rawRow(t, "relay")
	row.AuthHeader = AuthBoth
	h.saveRows(row)
	LearnAuthHeader("relay", AuthBearer, Expect(Transient(h.row("relay"))))
	if p, _ := rawRow(t, "relay"); p.AuthHeader != AuthBoth {
		t.Fatalf("a transient value's learned header was written to the stored relay: %q", p.AuthHeader)
	}
	base := strings.TrimSuffix(h.row("relay").Chat, "/v1")
	row.Chat = base
	h.saveRows(row)
	if got := Transient(h.row("relay")).fixV1(base, base+"/v1/models"); got != base {
		t.Fatalf("fixV1 of a transient value: %q, want the base as it was", got)
	}
	if p, _ := rawRow(t, "relay"); p.Chat != base {
		t.Fatalf("fixV1 of a transient value wrote the stored relay's Chat: %q", p.Chat)
	}
	tr.Name = "saved from the form"
	if err := Save(tr); err != nil {
		t.Fatalf("a transient value's save: %v", err)
	}
	if p, _ := rawRow(t, "relay"); p.Name != "saved from the form" {
		t.Fatalf("after the transient save: %+v", p)
	}
}

// A learner writes what it found of a provider only to that provider: one
// deleted and added again under its id since is another, and is not
// written. The value of the provider there now still learns.
func TestALearnerWritesOnlyToTheProviderItLearnedOf(t *testing.T) {
	h := readyHome(t, nil, "relay")
	both := func() {
		row, _ := rawRow(t, "relay")
		row.AuthHeader = AuthBoth
		row.Models = []string{"gpt-5", "gone-model"}
		h.saveRows(row)
	}
	both()
	old := h.find("relay")
	if err := Delete("relay"); err != nil {
		t.Fatal(err)
	}
	h.saveRows(h.row("relay"))
	both()
	LearnAuthHeader("relay", AuthBearer, Expect(old))
	gone := map[string]bool{"gone-model": true}
	if _, err := dropGonePicks("relay", gone, map[string]bool{"gpt-5": true}, Expect(old)); !errors.Is(err, ErrStale) {
		t.Fatalf("dropGonePicks of the old relay: %v, want ErrStale", err)
	}
	if p, _ := rawRow(t, "relay"); p.AuthHeader != AuthBoth || len(p.Models) != 2 {
		t.Fatalf("a learner wrote the old relay's findings to the new one: %+v", p)
	}
	cur := h.find("relay")
	LearnAuthHeader("relay", AuthBearer, Expect(cur))
	if _, err := dropGonePicks("relay", gone, map[string]bool{"gpt-5": true}, Expect(cur)); err != nil {
		t.Fatal(err)
	}
	if p, _ := rawRow(t, "relay"); p.AuthHeader != AuthBearer || len(p.Models) != 1 {
		t.Fatalf("the relay there now did not learn: %+v", p)
	}
	// LearnZhipuCoding, and fixV1, the same way
	glm := Provider{ID: "glm", Name: "glm", Key: "sk-test", Chat: "https://open.bigmodel.cn/api/paas/v4"}
	h.saveRows(h.find("relay"), glm)
	oldGLM := h.find("glm")
	if err := Delete("glm"); err != nil {
		t.Fatal(err)
	}
	h.saveRows(h.find("relay"), glm)
	LearnZhipuCoding(oldGLM, glm.Chat)
	if p, _ := rawRow(t, "glm"); p.Chat != glm.Chat {
		t.Fatalf("LearnZhipuCoding moved the new glm on the old one's refusal: %q", p.Chat)
	}
	LearnZhipuCoding(h.find("glm"), glm.Chat)
	if p, _ := rawRow(t, "glm"); !strings.Contains(p.Chat, "/api/coding/paas/v4") {
		t.Fatalf("LearnZhipuCoding of the glm there now: %q", p.Chat)
	}
	base := strings.TrimSuffix(h.row("relay").Chat, "/v1")
	row := h.row("relay")
	row.Chat = base
	h.saveRows(row, glm)
	oldRelay := h.find("relay")
	if err := Delete("relay"); err != nil {
		t.Fatal(err)
	}
	h.saveRows(row, glm)
	if got := oldRelay.fixV1(base, base+"/v1/models"); got != base {
		t.Fatalf("fixV1 of the old relay: %q", got)
	}
	if got := h.find("relay").fixV1(base, base+"/v1/models"); got != base+"/v1" {
		t.Fatalf("fixV1 of the relay there now: %q", got)
	}
	if p, _ := rawRow(t, "relay"); p.Chat != base+"/v1" {
		t.Fatalf("fixV1 did not write the relay there now: %q", p.Chat)
	}
}

// A rename is committed before the vendor's fetched lists move: one the
// commit refuses (a stale value, an id taken meanwhile) leaves them where
// they are. RenameChecked answers the provider under its new id with this
// commit's marker, which the next checked write is built on.
func TestARenameMovesTheFetchedListOnlyOnceCommitted(t *testing.T) {
	h := readyHome(t, nil, "relay")
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	live := catalog.LivePath("relay")
	if err := os.MkdirAll(filepath.Dir(live), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := h.find("relay")
	edit := h.find("relay")
	edit.Name = "edited since"
	if err := Save(edit); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameChecked(old, "relay2"); !errors.Is(err, ErrStale) {
		t.Fatalf("a rename of an older rev: %v, want ErrStale", err)
	}
	// relay2 taken by another write between Rename's read and its commit
	f, err := read()
	if err != nil {
		t.Fatal(err)
	}
	f.Providers[0].ID = "relay2"
	h.saveRows(append(load().Providers, h.row("relay2"))...)
	if err := renameCommit(f, 0, "relay", "relay2", nil); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("a rename over an id taken meanwhile: %v, want *ConflictError", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("a refused rename moved the fetched list: %v", err)
	}
	if _, err := os.Stat(catalog.LivePath("relay2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused rename put a fetched list under relay2 (%v)", err)
	}
	if err := Delete("relay2"); err != nil {
		t.Fatal(err)
	}
	cur := h.find("relay")
	got, err := RenameChecked(cur, "relay2")
	if err != nil {
		t.Fatal(err)
	}
	m := readMetaT(t)
	if got.ID != "relay2" || got.life.issuedID != "relay2" || got.life.instance != m.Providers["relay2"].Instance ||
		got.life.instance != cur.life.instance || got.life.rev != m.Providers["relay2"].Rev {
		t.Fatalf("RenameChecked gave %s %+v, lifecycle.json %+v", got.ID, got.life, m.Providers["relay2"])
	}
	if _, err := os.Stat(catalog.LivePath("relay2")); err != nil {
		t.Fatalf("the fetched list did not follow the rename: %v", err)
	}
	if _, err := os.Stat(live); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old fetched list is still there (%v)", err)
	}
	if sig, err := h.sign(got); err != nil || sig != uaOf(h.a) {
		t.Fatalf("the renamed value sent %q (%v)", sig, err)
	}
	if got, err = RenameChecked(got, "relay3"); err != nil || got.ID != "relay3" {
		t.Fatalf("a rename built on RenameChecked's value: %v", err)
	}
	if same, err := RenameChecked(got, "relay3"); err != nil || same.ID != "relay3" {
		t.Fatalf("a rename to its own id: %+v %v", same.ID, err)
	}
}

// A rename whose settings.json step fails after the commit is a
// FollowUpError that says it was committed: the rename is done (redoing
// it by id would fail, or move another provider), and errors.Is still
// reaches the step's own error.
func TestARenameFailingAfterItsCommitSaysSo(t *testing.T) {
	for _, checked := range []bool{false, true} {
		h := readyHome(t, nil, "relay")
		// a settings.json Save refuses: its theme is not one there is
		if err := os.MkdirAll(filepath.Dir(settings.Path()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings.Path(), []byte(`{"theme":"no-such-theme","visible":{"codex":["relay"]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var err error
		var got Provider
		if checked {
			got, err = RenameChecked(h.find("relay"), "relay2")
		} else {
			err = Rename("relay", "relay2")
		}
		var fu *FollowUpError
		if !errors.As(err, &fu) || !fu.Committed || fu.Step != StepSettings || fu.Err == nil || !errors.Is(err, fu.Err) {
			t.Fatalf("checked %v: a rename whose settings step failed: %v", checked, err)
		}
		if !strings.Contains(err.Error(), "saved") {
			t.Fatalf("its message doesn't say the rename was saved: %v", err)
		}
		if _, ok := rawRow(t, "relay2"); !ok {
			t.Fatal("the rename was not committed")
		}
		if checked && got.ID != "relay2" {
			t.Fatalf("RenameChecked with a FollowUpError gave %q", got.ID)
		}
	}
}

// Add's id, free when it looked, taken by another write before its commit:
// the provider moves on to the next free id rather than writing over the
// one there. A copy is a new provider: no aliases, instance or binding of
// the one it was made from.
func TestAddNeverWritesOverAnIDTakenMeanwhile(t *testing.T) {
	h := readyHome(t, nil, "relay")
	if err := identity.Bind("relay", h.a.ID); err != nil {
		t.Fatal(err)
	}
	mine := h.row("relay")
	mine.Name = "mine"
	got, err := saveNew(mine, "relay", "mine", nil)
	if err != nil || got.ID != "relay-2" {
		t.Fatalf("an add whose id was taken meanwhile: %q %v, want relay-2", got.ID, err)
	}
	if p, _ := rawRow(t, "relay"); p.Name != "relay" {
		t.Fatalf("the relay there was written over: %+v", p)
	}
	if err := Rename("relay", "relay3"); err != nil {
		t.Fatal(err)
	}
	cp := h.row("copy")
	cp.Key = "sk-test-copy"
	cp.Was = []string{"relay"}
	id, err := AddCopy(cp, "relay3")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := rawRow(t, id)
	if len(p.Was) != 0 {
		t.Fatalf("the copy kept aliases: %v", p.Was)
	}
	m := readMetaT(t)
	if m.Providers[id].Instance == m.Providers["relay3"].Instance {
		t.Fatal("the copy has its source's instance")
	}
	if b := boundNow(t); b[id] != "" {
		t.Fatalf("the copy inherited a binding: %v", b)
	}
	if sig, err := h.sign(h.find(id)); err != nil || sig == uaOf(h.a) {
		t.Fatalf("the copy sent %q (%v)", sig, err)
	}
	if sig, err := h.sign(h.find("relay3")); err != nil || sig != uaOf(h.a) {
		t.Fatalf("relay3 sent %q (%v)", sig, err)
	}
}

// BindIdentity binds the provider a value was read as, under the id it has
// now: renamed since, the binding goes to its new id. Deleted and added
// again since, the value is stale and nothing is written. Each change is
// one commit with lifecycle.json, which still describes providers.json.
func TestBindIdentityBindsTheInstanceItWasReadAs(t *testing.T) {
	h := readyHome(t, nil, "relay")
	read1 := h.find("relay")
	if err := Rename("relay", "relay2"); err != nil {
		t.Fatal(err)
	}
	gen := readMetaT(t).Gen
	if err := BindIdentity(read1, h.a.ID); err != nil {
		t.Fatalf("a bind of a value read before its rename: %v", err)
	}
	if b := boundNow(t); b["relay2"] != h.a.ID || b["relay"] != "" {
		t.Fatalf("bindings: %v", b)
	}
	m := readMetaT(t)
	if m.Gen != gen+1 || m.Digest != lifecycle.Digest(providersBytes(t)) {
		t.Fatalf("lifecycle.json after the bind: gen %d (was %d), digest ok %v", m.Gen, gen, m.Digest == lifecycle.Digest(providersBytes(t)))
	}
	if sig, err := h.sign(h.find("relay2")); err != nil || sig != uaOf(h.a) {
		t.Fatalf("relay2 sent %q (%v)", sig, err)
	}
	// the same bind again changes nothing
	if err := BindIdentity(h.find("relay2"), h.a.ID); err != nil || readMetaT(t).Gen != m.Gen {
		t.Fatalf("a bind already there: %v, gen %d", err, readMetaT(t).Gen)
	}
	if err := UnbindIdentity(read1); err != nil {
		t.Fatal(err)
	}
	if b := boundNow(t); b["relay2"] != "" {
		t.Fatalf("after the unbind: %v", b)
	}
	old := h.find("relay2")
	if err := Delete("relay2"); err != nil {
		t.Fatal(err)
	}
	if err := Save(h.row("relay2")); err != nil {
		t.Fatal(err)
	}
	ids0, _ := os.ReadFile(identity.Path())
	meta0, _ := os.ReadFile(lifecycle.Path())
	if err := BindIdentity(old, h.a.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("a bind of a deleted provider's value: %v, want ErrStale", err)
	}
	if err := UnbindIdentity(old); !errors.Is(err, ErrStale) {
		t.Fatalf("an unbind of a deleted provider's value: %v, want ErrStale", err)
	}
	ids1, _ := os.ReadFile(identity.Path())
	meta1, _ := os.ReadFile(lifecycle.Path())
	if !bytes.Equal(ids0, ids1) || !bytes.Equal(meta0, meta1) {
		t.Fatal("a refused bind wrote identities.json or lifecycle.json")
	}
	// one that can't present a profile is refused as the old Bind never was
	pl := h.find("relay2")
	pl.Preset = RemoteMagpiePreset
	if err := BindIdentity(pl, h.a.ID); err == nil {
		t.Fatal("a magpie service was bound")
	}
	// with no lifecycle.json a zero value binds by id, as identity.Bind
	h2 := newLifeHome(t)
	h2.saveRows(h2.row("relay"))
	if err := BindIdentity(h2.find("relay"), h2.a.ID); err != nil {
		t.Fatal(err)
	}
	if b := boundNow(t); b["relay"] != h2.a.ID {
		t.Fatalf("a legacy bind: %v", b)
	}
	if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a legacy bind made lifecycle.json (%v)", err)
	}
}

// An adoption leaves an id's binding undecided (meta.Unreconciled) when it
// can't tell the binding is the provider's own; that provider fails closed.
// A bind or an unbind of a value read as that provider decides it, and
// clears the mark. A zero value's bind names only an id, and decides
// nothing: the provider still fails closed.
func TestAnExplicitBindDecidesAnUndecidedBinding(t *testing.T) {
	for _, how := range []string{"bind", "unbind", "zero bind"} {
		t.Run(how, func(t *testing.T) {
			h := readyHome(t, nil, "relay")
			if err := identity.Bind("orphan", h.b.ID); err != nil {
				t.Fatal(err)
			}
			writeOutside(t, h.row("relay"), h.row("orphan"))
			h.saveRows(load().Providers...) // a write adopts it, and marks orphan
			if !readMetaT(t).IsUnreconciled("orphan") {
				t.Fatal("orphan not marked")
			}
			p := h.find("orphan")
			if _, err := h.sign(p); !errors.Is(err, lifecycle.ErrUnknown) {
				t.Fatalf("the undecided orphan: %v, want ErrUnknown", err)
			}
			var err error
			switch how {
			case "bind":
				err = BindIdentity(p, h.a.ID)
			case "unbind":
				err = UnbindIdentity(p)
			case "zero bind":
				p.life = life{}
				err = BindIdentity(p, h.a.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			m := readMetaT(t)
			sig, serr := h.sign(h.find("orphan"))
			switch how {
			case "bind":
				if m.IsUnreconciled("orphan") || serr != nil || sig != uaOf(h.a) {
					t.Fatalf("after the bind: marked %v, sent %q (%v)", m.IsUnreconciled("orphan"), sig, serr)
				}
			case "unbind":
				if m.IsUnreconciled("orphan") || serr != nil || sig == uaOf(h.a) || sig == uaOf(h.b) || boundNow(t)["orphan"] != "" {
					t.Fatalf("after the unbind: marked %v, sent %q (%v)", m.IsUnreconciled("orphan"), sig, serr)
				}
			case "zero bind":
				if !m.IsUnreconciled("orphan") || !errors.Is(serr, lifecycle.ErrUnknown) {
					t.Fatalf("after a zero bind: marked %v, sent %q (%v)", m.IsUnreconciled("orphan"), sig, serr)
				}
			}
			if m.Digest != lifecycle.Digest(providersBytes(t)) {
				t.Fatal("lifecycle.json no longer describes providers.json")
			}
		})
	}
}

// A bind of a value read before lifecycle.json knew its id adopts the
// change outside in the same commit, and binds the instance that gives the
// id: no mark is left on a binding the operator just chose. One adopted by
// another write since is stale.
func TestABindOfAnUnreconciledValueAdoptsIt(t *testing.T) {
	h := readyHome(t, nil, "relay")
	writeOutside(t, h.row("relay"), h.row("added"))
	added := h.find("added")
	if added.life.kind != lifeUnreconciled {
		t.Fatalf("%+v", added.life)
	}
	if err := BindIdentity(added, h.a.ID); err != nil {
		t.Fatal(err)
	}
	m := readMetaT(t)
	if _, ok := m.Providers["added"]; !ok || m.IsUnreconciled("added") || m.Digest != lifecycle.Digest(providersBytes(t)) {
		t.Fatalf("after the bind: %+v", m)
	}
	if sig, err := h.sign(h.find("added")); err != nil || sig != uaOf(h.a) {
		t.Fatalf("added sent %q (%v)", sig, err)
	}
	writeOutside(t, h.row("relay"), h.find("added"), h.row("late"))
	late := h.find("late")
	h.saveRows(load().Providers...)
	if err := BindIdentity(late, h.a.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("a bind of a value adopted since: %v, want ErrStale", err)
	}
}

// A binding change over providers.json lifecycle.json can't describe is
// refused, with how to get past it, and writes nothing: the file read from
// its .bak, and the file not there.
func TestABindOverAFileItCantDescribeIsRefused(t *testing.T) {
	for _, how := range []string{"fallback", "missing"} {
		t.Run(how, func(t *testing.T) {
			h := readyHome(t, nil, "relay")
			p := h.find("relay")
			h.saveRows(append(load().Providers, h.row("other"))...) // a .bak with relay in it
			if how == "fallback" {
				if err := os.WriteFile(Path(), make([]byte, len(providersBytes(t))), 0o600); err != nil {
					t.Fatal(err)
				}
				_, _ = read() // the zeroed file kept aside before the snapshot
			} else if err := os.Remove(Path()); err != nil {
				t.Fatal(err)
			}
			before := configSnapshot(t)
			err := BindIdentity(p, h.a.ID)
			want := map[string]string{"fallback": ".bak", "missing": "is not there"}[how]
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("a bind over providers.json %s: %v", how, err)
			}
			if err := UnbindOrphan("gone"); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("an orphan unbind over providers.json %s: %v", how, err)
			}
			sameSnapshot(t, "a refused bind", before, configSnapshot(t))
		})
	}
}

// UnbindOrphan removes a binding no provider holds, and the mark on it; it
// is refused while one is stored under the id, which is unbound through its
// value. With no lifecycle.json it writes identities.json alone.
func TestUnbindOrphan(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		h := newLifeHome(t)
		h.saveRows(h.row("relay"))
		for _, id := range []string{"relay", "gone"} {
			if err := identity.Bind(id, h.a.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := UnbindOrphan("relay"); err == nil || !strings.Contains(err.Error(), "is stored") {
			t.Fatalf("an orphan unbind of a stored provider: %v", err)
		}
		if err := UnbindOrphan(""); err == nil {
			t.Fatal("an orphan unbind of no id")
		}
		ids0, _ := os.ReadFile(identity.Path())
		if err := UnbindOrphan("never-bound"); err != nil {
			t.Fatal(err)
		}
		if ids1, _ := os.ReadFile(identity.Path()); !bytes.Equal(ids0, ids1) {
			t.Fatal("an unbind of nothing wrote identities.json")
		}
		if err := UnbindOrphan("gone"); err != nil {
			t.Fatal(err)
		}
		if b := boundNow(t); b["gone"] != "" || b["relay"] != h.a.ID {
			t.Fatalf("bindings: %v", b)
		}
		if _, err := os.Stat(lifecycle.Path()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a legacy orphan unbind made lifecycle.json (%v)", err)
		}
	})
	t.Run("marked", func(t *testing.T) {
		h := readyHome(t, nil, "relay", "gone")
		if err := identity.Bind("gone", h.b.ID); err != nil {
			t.Fatal(err)
		}
		writeOutside(t, h.row("relay"))
		h.saveRows(load().Providers...) // gone retired, its binding undecided
		m := readMetaT(t)
		if !m.IsUnreconciled("gone") {
			t.Fatalf("gone not marked: %+v", m.Unreconciled)
		}
		if err := UnbindOrphan("gone"); err != nil {
			t.Fatal(err)
		}
		m2 := readMetaT(t)
		if m2.IsUnreconciled("gone") || boundNow(t)["gone"] != "" || m2.Gen != m.Gen+1 || m2.Digest != lifecycle.Digest(providersBytes(t)) {
			t.Fatalf("after the orphan unbind: %+v, bound %v", m2, boundNow(t))
		}
	})
}

// A value read under one lifecycle state is not matched to another: with
// lifecycle.json made anew (a new epoch), or gone, a marked value's write
// and bind are stale. Under no lifecycle.json no zero value is.
func TestAMarkedWriteUnderAnotherLifecycleStateIsStale(t *testing.T) {
	h := readyHome(t, nil, "relay")
	held := h.find("relay")
	if err := os.WriteFile(lifecycle.Path(), []byte(`{"version":1,`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := readyLifecycle(); err != nil || out != readyRebootstrapped {
		t.Fatalf("%v %v", out, err)
	}
	if err := Save(held); !errors.Is(err, ErrStale) {
		t.Fatalf("a save from another epoch: %v, want ErrStale", err)
	}
	if err := BindIdentity(held, h.a.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("a bind from another epoch: %v, want ErrStale", err)
	}
	cur := h.find("relay")
	if err := os.Remove(lifecycle.Path()); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"save":   Save(cur),
		"delete": DeleteChecked(cur),
		"bind":   BindIdentity(cur, h.a.ID),
	} {
		if !errors.Is(err, ErrStale) {
			t.Errorf("a marked %s with lifecycle.json gone: %v, want ErrStale", name, err)
		}
	}
	if err := Save(h.find("relay")); err != nil {
		t.Fatalf("a zero value's save with no lifecycle.json: %v", err)
	}
}

// What a bind and an orphan unbind run under lifecycle's lock reads
// providers.json once, directly, and lists nothing: with the adoption, the
// mark settled and the refusal each run under the runtime guard.
func TestBindingMakesNoProviderReadsUnderTheLock(t *testing.T) {
	h := readyHome(t, nil, "relay")
	if err := identity.Bind("gone", h.b.ID); err != nil {
		t.Fatal(err)
	}
	writeOutside(t, h.row("relay"), h.row("added"))
	added, relay := h.find("added"), h.find("relay")
	clean := watchCommitReads(t)
	defer clean()
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(commitReadViolation); !ok {
				panic(p)
			}
			t.Errorf("a provider read under the lock: %v", p)
		}
	}()
	if err := BindIdentity(added, h.a.ID); err != nil { // adopts
		t.Fatal(err)
	}
	if err := UnbindIdentity(relay); err != nil {
		t.Fatal(err)
	}
	if err := UnbindOrphan("added"); err == nil {
		t.Fatal("an orphan unbind of a stored id")
	}
	if err := UnbindOrphan("gone"); err != nil {
		t.Fatal(err)
	}
}
