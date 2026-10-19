package provider

// Provider lifecycle's write API (R1 G3b-1). A value read with a marker
// (lifecycle_fork.go) is written back as the provider it was read as: its
// instance, and for a draft written back whole its rev (or, read while
// providers.json differed from lifecycle.json, its row), must still be
// what holds its id, or the write is refused with a StaleError and nothing
// changes. Its id changes only through Rename. A value read before
// lifecycle.json knew its id is written only while that is still so. A
// zero or transient value is written by id, as before lifecycle.json: the
// one there is replaced, keeping its instance and binding, or one is
// added. A value read with no lifecycle state is never taken as current.
//
// A rename commits first; the fetched lists and settings.json follow, and
// one that fails after the commit is a *FollowUpError: the rename is done,
// and is never replayed by id.

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"slices"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/filememo"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
)

// FollowUpError is a change that was committed with a step after it that
// failed. Committed says the change itself is saved: redo Step alone, never
// the change (a rename replayed by id could move another provider).
type FollowUpError struct {
	Committed bool
	Step      string // StepSettings, or a step its caller names (agent.StepAgents)
	Err       error
	redo      func() error
}

// StepSettings is settings.json's model preferences and agent lists,
// renamed after the provider was.
const StepSettings = "settings"

// StepSignIn is the provider's own sign-in kept by its id (StepFun's),
// moved after the provider was renamed, forgotten after it was removed.
const StepSignIn = "sign-in"

// signInMoved and signInForgotten are that step for a rename and a delete;
// vars so a test can make them fail.
var (
	signInMoved     = renameStepFun
	signInForgotten = forgetStepFun
)

func (e *FollowUpError) Error() string {
	if e.Committed {
		return fmt.Sprintf("the change was saved, but its %s step failed (%v); do that step again, not the change", e.Step, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Step, e.Err)
}
func (e *FollowUpError) Unwrap() error { return e.Err }

// Redo does Step again, and only Step: the change before it stays as it was
// committed. nil once the step went through; with no way known to redo it,
// the step's own error.
func (e *FollowUpError) Redo() error {
	if e.redo == nil {
		return e.Err
	}
	return e.redo()
}

// FollowUp is err, from step after a committed change, as a *FollowUpError
// whose Redo is redo; nil when err is. A step that follows a rename outside
// this package (the agents') says so with it.
func FollowUp(step string, err error, redo func() error) error {
	if err == nil {
		return nil
	}
	return &FollowUpError{Committed: true, Step: step, Err: err, redo: redo}
}

// WriteOption adds a check, or asks for the result, of one write.
type WriteOption func(*writeOpts)

type writeOpts struct {
	in     intent
	refuse error     // the write is not made: a value never saved
	create bool      // Add's: the id must not be there yet
	snap   *Provider // a Checked write's committed value
	base   fileBase  // OnBase's: checked instead of the write's own read
}

func writeOptsOf(opts []WriteOption) writeOpts {
	var o writeOpts
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

var errTransient = errors.New("this provider is not saved; add it first")

// Expect checks that a write finds p as it was read: the same instance at
// its id, or an id still unknown to lifecycle.json when read so. A learner
// that found something about p writes it only to p, never to another
// provider that holds p's id since. A value never saved writes nothing.
func Expect(p Provider) WriteOption {
	return func(o *writeOpts) {
		if p.life.kind == lifeTransient {
			o.refuse = errTransient
			return
		}
		o.in.check(p.life, false)
	}
}

// storeWith is store with opts' checks. A refused write is an error and
// writes nothing.
func storeWith(f file, opts ...WriteOption) error {
	o := writeOptsOf(opts)
	if o.refuse != nil {
		return o.refuse
	}
	if o.base.read {
		f.base = o.base
	}
	return commit(f, o.in)
}

// errIDChanged is a marked value saved under another id than it was read.
func errIDChanged(l life, id string) error {
	return fmt.Errorf("provider %q was read as %q: an id is changed by Rename, not by Save", id, l.issuedID)
}

// saveCommit is Save's write of p into f, by its marker l: as the provider
// it was read as, or by id (a zero or transient l). account is p saved as
// a signed-in account's picks: by id, whatever l is. unchanged is p as
// providers.json holds it already: checked the same, written not (#1962).
func saveCommit(f file, p Provider, l life, account, unchanged bool, opts []WriteOption) error {
	o := writeOptsOf(opts)
	if o.refuse != nil {
		return o.refuse
	}
	in := o.in
	in.unchanged = unchanged
	if account {
		l = life{}
	}
	switch l.kind {
	case lifeManaged, lifeUnreconciled, lifeUnknown:
		if l.issuedID != p.ID {
			return errIDChanged(l, p.ID)
		}
		in.check(l, true)
	}
	if o.create {
		in.create = append(in.create, p.ID)
	}
	var out committed
	if o.snap != nil {
		in.out = &out
	}
	if err := commit(f, in); err != nil {
		return err
	}
	if o.snap != nil {
		*o.snap = p.clone()
		if !account {
			o.snap.life = committedLife(out.meta, p.ID)
		}
	}
	return nil
}

// committedLife is the marker of id under the lifecycle state a write left,
// nil with no lifecycle.json: its rev as that write left it.
func committedLife(meta *lifecycle.Meta, id string) life {
	if meta == nil {
		return life{}
	}
	return issue{meta: meta, managed: true}.of(id)
}

// SaveChecked is Save, and the provider as this write left it: its marker
// from this commit, so the next write of it (AddKeys, a rename) is checked
// against this one, not against the value read before.
func SaveChecked(p Provider, opts ...WriteOption) (Provider, error) {
	var snap Provider
	opts = append(slices.Clip(opts), func(o *writeOpts) { o.snap = &snap })
	if err := Save(p, opts...); err != nil {
		return Provider{}, err
	}
	return snap, nil
}

// saveNew is Add's Save: the id must not be there. Taken meanwhile by
// another magpie (freeID ran before the write's lock), it moves on to the
// next free id, once; then the conflict is the answer.
func saveNew(p Provider, id, name string, opts []WriteOption) (Provider, error) {
	p.life = life{} // a new provider, whatever value it was made from
	create := append(slices.Clip(opts), func(o *writeOpts) { o.create = true })
	err := Save(p, create...)
	if errors.As(err, new(*ConflictError)) {
		p.ID, p.Name = freeID(id), freeName(name)
		err = Save(p, create...)
	}
	return p, err
}

// DeleteChecked is Delete of p as it was read: refused (StaleError) when
// another provider holds p's id since. An account is hidden, by id.
func DeleteChecked(p Provider) error {
	switch p.life.kind {
	case lifeTransient:
		return errTransient
	case lifeLegacy:
		return Delete(p.ID)
	}
	if p.life.issuedID != p.ID {
		return errIDChanged(p.life, p.ID)
	}
	return Delete(p.ID, Expect(p))
}

// renameCommit is Rename's commit of f, row i renamed from → to, and what
// follows it once committed: the fetched lists, moved after the commit so
// that a refused rename leaves them where they are.
func renameCommit(f file, i int, from, to string, opts []WriteOption) error {
	o := writeOptsOf(opts)
	if o.refuse != nil {
		return o.refuse
	}
	in := o.in
	in.renames = map[string]string{from: to}
	var out committed
	in.out = &out
	err := commitQuiet(f, in)
	pending := pendingJournal(err)
	if err != nil && !pending {
		return err
	}
	// the vendor's lists last fetched go with it: one that can't is fetched
	// again under the new id; the rename stands either way. One whose
	// journal is written is finished forward by the next write or start-up,
	// so its lists go now too, or they would stay under an id gone then.
	for _, id := range []string{from, decisionsID(from)} {
		dst := to
		if id != from {
			dst = decisionsID(to)
		}
		if err := os.Rename(catalog.LivePath(id), catalog.LivePath(dst)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("provider %s renamed to %s, but its fetched list was not moved: %v", from, to, err)
		}
	}
	catalog.Touched()
	if pending {
		return err // its settings step is left: the old id stays in Was
	}
	if o.snap != nil {
		*o.snap = normalize(f.Providers[i]).clone()
		o.snap.life = committedLife(out.meta, to)
	}
	return nil
}

// RenameChecked is Rename of p as it was read: refused (StaleError) when
// p's id holds another provider, or another rev of it, since. It answers p
// under its new id with this commit's marker; a *FollowUpError after the
// commit comes with it.
func RenameChecked(p Provider, to string) (Provider, error) {
	var opts []WriteOption
	switch p.life.kind {
	case lifeTransient:
		return Provider{}, errTransient
	case lifeLegacy:
	default:
		if p.life.issuedID != p.ID {
			return Provider{}, errIDChanged(p.life, p.ID)
		}
		opts = append(opts, func(o *writeOpts) { o.in.check(p.life, true) })
	}
	var snap Provider
	opts = append(opts, func(o *writeOpts) { o.snap = &snap })
	err := Rename(p.ID, to, opts...)
	if snap.ID == "" {
		if err == nil {
			return p, nil // to is its id already: nothing written
		}
		return Provider{}, err
	}
	return snap, err
}

// BindIdentity has p, as it was read, present the profile: refused
// (StaleError) when p's instance is no longer stored. The binding goes to
// the id that instance has now, in one commit with lifecycle.json, and it
// settles a binding there an adoption left undecided (meta.Unreconciled):
// the operator chose this profile for this provider. A zero value binds
// its id as identity.Bind always did, and settles nothing: it names an id,
// not the provider that was looked at.
func BindIdentity(p Provider, profile string) error {
	if why := p.IdentityApplies(); why != "" {
		return fmt.Errorf("%s: %s", p.Name, why)
	}
	switch p.life.kind {
	case lifeTransient:
		return errTransient
	case lifeLegacy:
		return identity.Bind(p.ID, profile)
	}
	if p.life.issuedID != p.ID {
		return errIDChanged(p.life, p.ID)
	}
	return changeBinding(p.life, profile)
}

// UnbindIdentity stops p, as it was read, presenting a profile, as
// BindIdentity binds: by the instance, and settling an undecided binding
// there (there is none left to decide). A zero value unbinds its id.
func UnbindIdentity(p Provider) error {
	switch p.life.kind {
	case lifeTransient:
		return errTransient
	case lifeLegacy:
		return identity.Unbind(p.ID)
	}
	if p.life.issuedID != p.ID {
		return errIDChanged(p.life, p.ID)
	}
	return changeBinding(p.life, "")
}

// UnbindOrphan removes the binding under id that no provider holds: one a
// deleted provider left, or one bound by hand. It is refused while a
// provider is stored under id, as read under lifecycle's lock: unbind that
// one through its value (UnbindIdentity). An undecided mark on id goes too.
func UnbindOrphan(id string) error {
	notUnderCommit("UnbindOrphan")
	if id == "" {
		return errors.New("a binding needs a provider")
	}
	unlock, err := lockCommit()
	if err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	err = orphanHeld(id, unlock)
	Changed()
	return err
}

func changeBinding(l life, profile string) error {
	notUnderCommit("changeBinding")
	unlock, err := lockCommit()
	if err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	err = bindHeld(l, profile, unlock)
	Changed()
	return err
}

func bindHeld(l life, profile string, unlock func()) error {
	defer unlock()
	defer filememo.Forget()
	return bindLocked(l, profile)
}

func orphanHeld(id string, unlock func()) error {
	defer unlock()
	defer filememo.Forget()
	return orphanLocked(id)
}

// bindLocked is changeBinding's read-modify-write, under lifecycle's lock:
// the id l's provider has now, from lifecycle.json and providers.json as
// they are, then identities.json and lifecycle.json in one journal. It
// calls nothing that lists providers or takes the lock again.
func bindLocked(l life, profile string) error {
	if err := lifecycle.Recover(); err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	meta, err := lifecycle.ReadMeta()
	if err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	if l.kind == lifeUnknown || meta == nil || meta.Epoch != l.epoch {
		return &StaleError{ID: l.issuedID}
	}
	cur, err := read()
	if err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	if err := unsettled(cur); err != nil {
		return err
	}
	rows, err := rowsByID(cur)
	if err != nil {
		return err
	}
	id := l.issuedID
	if l.kind == lifeManaged {
		id = ""
		for pid, rec := range meta.Providers {
			if rec.Instance == l.instance {
				id = pid
			}
		}
	} else if _, known := meta.Providers[id]; known {
		id = "" // adopted meanwhile: another instance of its own
	}
	i := slices.IndexFunc(cur.Providers, func(q Provider) bool { return q.ID == id })
	if _, there := rows[id]; id == "" || !there || i < 0 {
		return &StaleError{ID: l.issuedID}
	}
	if q := normalize(cur.Providers[i]); profile != "" {
		if why := identityRefused(q); why != "" {
			return fmt.Errorf("%s: %s", cmpName(q), why)
		}
	}
	var after []byte
	var changed bool
	if profile != "" {
		after, changed, _, err = identity.PlanBind(id, profile)
	} else {
		after, changed, _, err = identity.PlanUnbind(id)
	}
	if err != nil {
		return err
	}
	return commitBinding(cur, rows, meta, id, after, changed)
}

// orphanLocked is UnbindOrphan's, under lifecycle's lock: refused while
// providers.json, as it is now, has a provider under id.
func orphanLocked(id string) error {
	if err := lifecycle.Recover(); err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	meta, err := lifecycle.ReadMeta()
	if err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	cur, err := read()
	if err != nil {
		return fmt.Errorf("identity store not changed: which providers are stored is unknown: %w", err)
	}
	if cur.base.fallback || meta != nil {
		// which ids the primary holds is unknown, or lifecycle.json must
		// describe it in the same journal
		if err := unsettled(cur); err != nil {
			return err
		}
	}
	for _, q := range cur.Providers {
		if q.ID == id || normalize(q).ID == id {
			return fmt.Errorf("provider %q is stored: unbind it as that provider, not as an orphan", id)
		}
	}
	after, changed, _, err := identity.PlanUnbind(id)
	if err != nil {
		return err
	}
	if meta == nil {
		if !changed {
			return nil
		}
		return commitPlan(whatIdentity, nil, after, nil)
	}
	rows, err := rowsByID(cur)
	if err != nil {
		return err
	}
	return commitBinding(cur, rows, meta, id, after, changed)
}

// commitBinding commits identities.json's after (when changed) with
// lifecycle.json advanced over providers.json cur as it is: a change made
// outside magpie adopted, and id's undecided mark settled. Nothing to
// change writes nothing. Who was bound before, for the adoption, is read
// as every adoption reads it (boundLocked): identities.json itself, never
// its .bak, which may hold bindings the file no longer has.
func commitBinding(cur file, rows map[string][]byte, meta *lifecycle.Meta, id string, after []byte, changed bool) error {
	gen := meta.Gen + 1
	m := meta.Clone()
	adopting := cur.base.digest != meta.Digest
	if adopting {
		_, bound, err := boundLocked(whatIdentity)
		if err != nil {
			return err
		}
		if m, _, err = adopt(m, rows, bound, gen); err != nil {
			return err
		}
	}
	marked := m.IsUnreconciled(id)
	m.Unreconciled = slices.DeleteFunc(m.Unreconciled, func(u lifecycle.Unreconciled) bool { return u.ID == id })
	if !changed && !adopting && !marked {
		return nil
	}
	m.Gen, m.Digest = gen, cur.base.digest
	mb, err := lifecycle.EncodeMeta(m)
	if err != nil {
		return err
	}
	if !changed {
		after = nil
	}
	return commitPlan(whatIdentity, nil, after, mb)
}

// unsettled refuses a binding change over providers.json that is not
// there, or whose .bak is read in its place: lifecycle.json can't describe
// a file it would have to write first.
func unsettled(cur file) error {
	switch {
	case !cur.base.exists:
		return fmt.Errorf("identity store not changed: %s is not there; add a provider (or put it back), then try again", Path())
	case cur.base.fallback:
		return fmt.Errorf("identity store not changed: %s can't be read and its .bak is read in its place; fix it, or save any provider change (which writes it again), then try again", Path())
	}
	return nil
}

// identityRefused is why a stored row q presents no identity profile, ""
// when it can: IdentityApplies, and a row with no endpoint, which is only
// a signed-in account's picks.
func identityRefused(q Provider) string {
	if why := q.IdentityApplies(); why != "" {
		return why
	}
	if !hasEndpoint(q) {
		return "a signed-in account is asked as its own client already: its sign-in signs its requests"
	}
	return ""
}

func cmpName(q Provider) string {
	if q.Name != "" {
		return q.Name
	}
	return q.ID
}
