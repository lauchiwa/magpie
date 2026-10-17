package provider

// Provider lifecycle on the write side (R1 G2b-2). Every providers.json
// write (store) is commit(f, intent{}): under lifecycle's cross-process
// lock it finishes a journal a dead writer left, reads providers.json
// again, refuses a write whose read is out of date, and moves the identity
// bindings that follow the change of ids. A rename carries its bindings
// to the new id, a deleted id's binding goes, and a new id never inherits
// an orphan binding left under it. With lifecycle.json present it also
// keeps each row's instance and rev, retires deleted instances behind a
// tombstone, and commits providers.json, identities.json and
// lifecycle.json in one journal.
//
// With no lifecycle.json (legacy, and every test home) nothing is
// bootstrapped: a write that moves no binding is the write store always
// made (Keep, then the file), only under the lock.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/filememo"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/lifecycle"
)

// intent is what a write means beyond its new contents. The zero intent
// is store's: ids that disappear are deleted, new ones are added.
type intent struct {
	// renames maps an old id to its new one: the same provider, so its
	// instance and its binding go with it.
	renames map[string]string
	// create are ids this write adds; one there already is a conflict.
	create []string
	// expect are ids that must still be the instance the caller read, after
	// any adoption under this write: with a nonzero Row the row must still
	// be the one read (a read of a file lifecycle.json didn't describe),
	// else with a nonzero Rev the rev.
	expect map[string]lifecycle.Provider
	// fresh are ids read before lifecycle.json knew them: still not known,
	// before any adoption, and (a nonzero digest) the row still as read.
	fresh map[string]string
	// epochs is the lifecycle epoch each checked id was read under.
	epochs map[string]string
	// stale are ids whose value can't be matched to what holds them now:
	// read with no lifecycle state, or read twice as two providers.
	stale []string
	// out, when set, is told the lifecycle state the write committed.
	out *committed
	// meta, when read, is lifecycle.json as the read the write was decided
	// from found it (OnBase): one moved on since is a conflict.
	meta metaBase
}

// committed is what a write left: the lifecycle state it committed, nil
// with no lifecycle.json.
type committed struct{ meta *lifecycle.Meta }

// check adds a value's marker l to the intent: the provider it was read as
// must be the one at its id when the write commits. withRow checks its
// content too (a draft written back whole); without it only its instance
// (a delete, or a learner's one field). A zero or transient l adds
// nothing: written by id, as before lifecycle.json.
func (in *intent) check(l life, withRow bool) {
	id := l.issuedID
	switch l.kind {
	case lifeUnknown:
		in.stale = append(in.stale, id)
		return
	case lifeManaged:
		want := lifecycle.Provider{Instance: l.instance}
		if withRow {
			if want.Row = l.row; l.row == "" {
				want.Rev = l.rev
			}
		}
		had, ok := in.expect[id]
		_, unknown := in.fresh[id]
		switch {
		case unknown || ok && had.Instance != want.Instance:
			in.stale = append(in.stale, id) // read as two providers
			return
		case !ok || withRow:
			if in.expect == nil {
				in.expect = map[string]lifecycle.Provider{}
			}
			in.expect[id] = want
		}
	case lifeUnreconciled:
		if _, ok := in.expect[id]; ok {
			in.stale = append(in.stale, id) // read as known and as not
			return
		}
		if in.fresh == nil {
			in.fresh = map[string]string{}
		}
		row := in.fresh[id]
		if withRow {
			row = l.row
		}
		in.fresh[id] = row
	default:
		return
	}
	if had, ok := in.epochs[id]; ok && had != l.epoch {
		in.stale = append(in.stale, id)
	}
	if in.epochs == nil {
		in.epochs = map[string]string{}
	}
	in.epochs[id] = l.epoch
}

// staleUnder is the first checked id meta can't vouch for: one read with
// no lifecycle state, or as two providers; one read under another epoch;
// or any checked id with no lifecycle.json now. nil when there is none.
func (in intent) staleUnder(meta *lifecycle.Meta) error {
	if len(in.stale) > 0 {
		return &StaleError{ID: slices.Min(in.stale)}
	}
	if meta == nil {
		ids := append(sortedKeys(in.expect), sortedKeys(in.fresh)...)
		if len(ids) > 0 {
			return &StaleError{ID: slices.Min(ids)}
		}
		return nil
	}
	for _, id := range sortedKeys(in.epochs) {
		if in.epochs[id] != meta.Epoch {
			return &StaleError{ID: id}
		}
	}
	return nil
}

// ConflictError is a write refused because providers.json is not what
// the write was built on: another magpie (or another write here) changed
// it since it was read. Nothing was written; read again and redo it.
type ConflictError struct {
	ID     string // the provider it is about, "" for the whole file
	Reason string
}

func (e *ConflictError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("provider %q: %s; nothing was changed — read it again and retry", e.ID, e.Reason)
	}
	return fmt.Sprintf("%s: %s; nothing was changed — read it again and retry", Path(), e.Reason)
}
func (e *ConflictError) Unwrap() error { return ErrStale }

// lockCommit is lifecycle.Lock; a test counts or orders around it.
var lockCommit = func() (func(), error) { return lifecycle.Lock(context.Background()) }

// commitReadHook lets tests guard indirect reads on the commitHeld call
// stack, including cache hits. Nil in production. A process-wide "locked"
// counter would mistake another goroutine's ordinary read for reentry.
var commitReadHook atomic.Pointer[func(string)]

func notUnderCommit(name string) {
	if hook := commitReadHook.Load(); hook != nil {
		(*hook)(name)
	}
}

// commit is store with an intent. pruneIcons runs under the lock (it only
// reads plugin icons); catalog.Touched only after it is released, since
// what it calls back may read or write providers again.
func commit(f file, in intent) error {
	if err := commitQuiet(f, in); err != nil {
		return err
	}
	// what agents were handed of the catalog may be out of date now
	catalog.Touched()
	return nil
}

// commitQuiet is commit without the Touched after it, for a write that has
// more to move first (a rename's fetched lists) and calls Touched itself.
func commitQuiet(f file, in intent) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// a group as it was read has the models its patterns matched then
	// among its members: they are found again each time, never written
	f.Groups = slices.Clone(f.Groups)
	for i := range f.Groups {
		f.Groups[i] = f.Groups[i].stored()
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	unlock, err := lockCommit()
	if err != nil {
		return fmt.Errorf("providers not changed: %w", err)
	}
	err = commitHeld(f, b, in, unlock)
	if err != nil {
		// a commit that failed part way may have written a file, and no
		// Touched follows: what a request holds (Hold) is dropped here, or it
		// would list the providers as before the write for its second
		Changed()
		return err
	}
	return nil
}

// commitHeld is the entire locked section, including pruning. Always
// release the lock, even when a test guard stops a forbidden callback.
func commitHeld(f file, b []byte, in intent, unlock func()) error {
	defer unlock()
	// Journal writes and recovery bypass writePrivate. Invalidate before
	// unlock, on success or partial failure, independently of callbacks.
	defer filememo.Forget()
	if err := commitLocked(f, b, in); err != nil {
		return err
	}
	pruneIcons(f)
	return nil
}

// commitLocked is commit's read-modify-write, under lifecycle's lock. It
// calls nothing that lists providers or accounts, reaches the network,
// or takes the lock again (identity's writers do).
func commitLocked(f file, b []byte, in intent) error {
	if err := lifecycle.Recover(); err != nil {
		return fmt.Errorf("providers not changed: %w", err)
	}
	cur, rerr := read()
	if f.base.read {
		switch {
		case rerr != nil:
			return &ConflictError{Reason: "it can't be read now, though it could when this change was made"}
		case cur.base.exists != f.base.exists || cur.base.digest != f.base.digest:
			return &ConflictError{Reason: "it changed since it was read"}
		}
	}
	meta, err := lifecycle.ReadMeta()
	if err != nil {
		return fmt.Errorf("providers not changed: %w", err)
	}
	if err := in.meta.since(meta); err != nil {
		return err
	}
	if err := in.staleUnder(meta); err != nil {
		return err
	}
	if rerr != nil {
		if meta != nil {
			return fmt.Errorf("providers not changed: %w", rerr)
		}
		// legacy blind write over a file that can't be read: what store
		// always did (its bad bytes are kept aside), and no binding moves,
		// since which ids were there is unknown
		return writeLegacy(b)
	}
	d, err := diffOf(cur, f, in)
	if err != nil {
		return err
	}
	if meta == nil {
		return commitLegacy(b, d)
	}
	return commitManaged(cur, f, b, d, meta, in)
}

// diff is how the ids of providers.json change in one write.
type diff struct {
	moves   map[string]string // renamed: old id → new id
	drops   []string          // deleted
	adds    []string          // new
	oldRows map[string][]byte // id → its rows as stored, before
	newRows map[string][]byte // id → its rows as stored, after
}

func (d diff) movesBindings() bool { return len(d.moves)+len(d.drops)+len(d.adds) > 0 }

func rowsByID(f file) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, p := range f.Providers {
		b, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		out[p.ID] = append(append(out[p.ID], b...), '\n')
	}
	return out, nil
}

func diffOf(cur, next file, in intent) (diff, error) {
	var d diff
	var err error
	if d.oldRows, err = rowsByID(cur); err != nil {
		return d, err
	}
	if d.newRows, err = rowsByID(next); err != nil {
		return d, err
	}
	for _, id := range in.create {
		if _, ok := d.oldRows[id]; ok {
			return d, &ConflictError{ID: id, Reason: "a provider with this id was added meanwhile"}
		}
	}
	targets := map[string]bool{}
	for from, to := range in.renames {
		if _, ok := d.oldRows[from]; !ok {
			return d, &ConflictError{ID: from, Reason: "it is not there to rename any more"}
		}
		if _, ok := d.oldRows[to]; ok {
			return d, &ConflictError{ID: to, Reason: "a provider with this id was added meanwhile"}
		}
		if _, ok := d.newRows[to]; !ok {
			return d, fmt.Errorf("providers not changed: rename of %q to %q writes no %q", from, to, to)
		}
		if _, ok := d.newRows[from]; ok {
			return d, fmt.Errorf("providers not changed: rename of %q to %q keeps %q", from, to, from)
		}
		targets[to] = true
	}
	d.moves = maps.Clone(in.renames)
	for _, id := range sortedKeys(d.oldRows) {
		if _, ok := d.newRows[id]; !ok && in.renames[id] == "" {
			d.drops = append(d.drops, id)
		}
	}
	for _, id := range sortedKeys(d.newRows) {
		if _, ok := d.oldRows[id]; !ok && !targets[id] {
			d.adds = append(d.adds, id)
		}
	}
	return d, nil
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// identitiesPlan is identities.json's new bytes for d, nil when its
// bindings don't change, and which ids were bound before. An
// identities.json that is there but blank or can't be read refuses the
// write: read would fall back to its .bak, which may hold the bindings
// this write must move.
func identitiesPlan(d diff) (after []byte, changed bool, bound map[string]bool, err error) {
	if !d.movesBindings() {
		return nil, false, nil, nil
	}
	cur, bound, err := boundLocked(whatProviders)
	if err != nil {
		return nil, false, nil, err
	}
	after, changed, err = planBindings(d, cur)
	return after, changed, bound, err
}

// boundLocked is identities.json's bytes (nil when it is not there) and
// the ids bound in them, read under lifecycle's lock. One that is there
// but blank or can't be read is an error: read would fall back to its
// .bak, which may hold other bindings. what names the store the change is
// for in its errors.
func boundLocked(what string) (cur []byte, bound map[string]bool, err error) {
	// under the lock no writer is replacing identities.json (its writers
	// take the lock too), so not there is absent: no bindings, and no
	// wait for a replace (ReadImage's, ~20ms on Windows)
	if _, err := os.Lstat(identity.Path()); errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	image, err := lifecycle.ReadImage(lifecycle.IdentitiesFile)
	if err != nil {
		return nil, nil, fmt.Errorf("%s not changed: %w", what, err)
	}
	if image.Exists {
		cur = image.Bytes
		if cur == nil {
			cur = []byte{}
		}
	}
	if bound, err = identity.BoundIn(cur); err != nil {
		return nil, nil, fmt.Errorf("%s not changed: %w", what, err)
	}
	return cur, bound, nil
}

// planBindings is identities.json's new bytes for d over cur: a renamed
// id's binding moves, a deleted one's goes, and so does a new id's orphan.
func planBindings(d diff, cur []byte) (after []byte, changed bool, err error) {
	if !d.movesBindings() || cur == nil {
		return nil, false, nil
	}
	drops := append(slices.Clone(d.drops), d.adds...)
	after, changed, err = identity.PlanBound(cur, d.moves, drops)
	if err != nil {
		return nil, false, fmt.Errorf("providers not changed: %w", err)
	}
	return after, changed, nil
}

// usableBak refuses a commit when name's .bak is there but is not a plain
// file: its last good generation could not be kept (design C.3a). what is
// the store the commit is for, as its errors name it.
func usableBak(what, name string) error {
	bak := lastgood.Bak(filepath.Join(filepath.Dir(Path()), name))
	fi, err := os.Lstat(bak)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s not changed: %s can't be checked: %w", what, bak, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s not changed: %s is not a plain file, so the last good generation can't be kept; move it aside and try again", what, bak)
	}
	return nil
}

// Which store a commit is for, as its errors name it.
const (
	whatProviders = "providers"
	whatIdentity  = "identity store"
)

// journalPending is a commit that failed after its journal was written:
// the next Recover (the next write, or start-up) finishes it, so what
// follows a rename's commit (its fetched lists) is moved as for one that
// went through. It unwraps to the commit's own error.
type journalPending struct{ err error }

func (e *journalPending) Error() string {
	return e.err.Error() + " (it is finished by the next provider write or start-up)"
}
func (e *journalPending) Unwrap() error { return e.err }

// pendingJournal is whether err left a journal for Recover to finish. A
// commit runs under the lock and after its own Recover, so a journal there
// now is the one this commit wrote.
func pendingJournal(err error) bool {
	return errors.As(err, new(*journalPending))
}

// writeLegacy is the write store always made: the file there now kept as
// the last good generation, then the new one over it. It has no usableBak
// check: lastgood.Keep replaces a linked .bak as it always did, and only
// a journal (commitPlan) needs every .bak to be a plain file.
func writeLegacy(b []byte) error {
	if err := lastgood.Keep(Path(), validProviders, 0o600); err != nil {
		return err
	}
	return writePrivate(Path(), b)
}

// commitLegacy is a write with no lifecycle.json: by id only. One that
// moves no binding is writeLegacy; one that does goes through a journal
// of providers.json and identities.json.
func commitLegacy(b []byte, d diff) error {
	after, changed, _, err := identitiesPlan(d)
	if err != nil {
		return err
	}
	if !changed {
		return writeLegacy(b)
	}
	return commitPlan(whatProviders, b, after, nil)
}

// commitPlan prepares and commits providers.json when b is not nil,
// identities.json when ids is not nil, and lifecycle.json when meta is
// not nil. what names the store the change is for in its errors.
func commitPlan(what string, b, ids, meta []byte) error {
	type part struct {
		name  string
		after []byte
		valid lastgood.Valid
	}
	var parts []part
	if b != nil {
		parts = append(parts, part{lifecycle.ProvidersFile, b, validProviders})
	}
	if ids != nil {
		parts = append(parts, part{lifecycle.IdentitiesFile, ids, identity.ValidStore})
	}
	if meta != nil {
		parts = append(parts, part{lifecycle.MetaFile, meta, nil}) // no .bak
	}
	// every .bak first: Prepare reads it too, and would refuse one that is
	// a folder or a link without saying what to do about it (C.3a)
	for _, p := range parts {
		if p.valid != nil {
			if err := usableBak(what, p.name); err != nil {
				return err
			}
		}
	}
	var ps []lifecycle.Participant
	for _, part := range parts {
		p, err := lifecycle.Prepare(part.name, lifecycle.Present(part.after), part.valid)
		if err != nil {
			return fmt.Errorf("%s not changed: %w", what, err)
		}
		if !p.Before.Exists || !bytes.Equal(p.Before.Bytes, part.after) {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return nil
	}
	if err := lifecycle.Commit(lifecycle.Plan{Participants: ps}); err != nil {
		busy := errors.Is(err, lifecycle.ErrBusy) // another's journal, not this one
		err = fmt.Errorf("%s: %w", what, err)
		if _, jerr := os.Lstat(lifecycle.JournalPath()); jerr == nil && !busy {
			return &journalPending{err}
		}
		return err
	}
	return nil
}

// commitManaged is a write with lifecycle.json: instances kept, revs
// bumped, deleted instances retired, all three files in one journal.
func commitManaged(cur, next file, b []byte, d diff, meta *lifecycle.Meta, in intent) error {
	gen := meta.Gen + 1
	// providers.json changed outside magpie (an older magpie, an editor,
	// its .bak read instead): adopt that first, then this write on top
	adopting := cur.base.fallback || cur.base.digest != meta.Digest
	// which ids are bound is needed to move bindings, to adopt an added or
	// removed id, and to settle what is undecided
	var cur0 []byte
	var bound map[string]bool
	known := false
	if d.movesBindings() || adopting {
		var err error
		if cur0, bound, err = boundLocked(whatProviders); err != nil {
			return err
		}
		known = true
	}
	// an id read before lifecycle.json knew it is the row read only while
	// still not known: an adoption meanwhile gave it an instance of its own
	for _, id := range sortedKeys(in.fresh) {
		_, known := meta.Providers[id]
		row, there := d.oldRows[id]
		if known || !there || in.fresh[id] != "" && lifecycle.Digest(row) != in.fresh[id] {
			return &StaleError{ID: id}
		}
	}
	base := meta.Clone()
	adopted := false
	if adopting {
		var err error
		if base, _, err = adopt(base, d.oldRows, bound, gen); err != nil {
			return err
		}
		adopted = true
	}
	for _, id := range sortedKeys(in.expect) {
		want := in.expect[id]
		got, ok := base.Providers[id]
		if !ok || got.Instance != want.Instance || want.Row != "" && got.Row != want.Row || want.Row == "" && want.Rev != 0 && got.Rev != want.Rev {
			return &StaleError{ID: id}
		}
	}
	ids, idsChanged, err := planBindings(d, cur0)
	if err != nil {
		return err
	}
	m := base.Clone()
	m.Providers = make(map[string]lifecycle.Provider, len(d.newRows))
	m.Gen = gen
	changed := adopted
	for id, row := range d.newRows {
		old, was := id, true
		for from, to := range d.moves {
			if to == id {
				old = from
			}
		}
		rec, ok := base.Providers[old]
		if _, kept := d.oldRows[old]; !kept || !ok {
			was = false
		}
		switch {
		case !was:
			inst, err := lifecycle.NewID()
			if err != nil {
				return err
			}
			rec, changed = lifecycle.Provider{Instance: inst, Rev: 1}, true
		case old != id || !bytes.Equal(d.oldRows[old], row):
			rec.Rev, changed = rec.Rev+1, true
		}
		// the row as stored now: what a later change outside magpie is
		// told apart from (not a change on its own)
		rec.Row = lifecycle.Digest(row)
		m.Providers[id] = rec
	}
	for _, id := range d.drops {
		m.Retire(lifecycle.Retired{Instance: base.Providers[id].Instance, Bound: bound[id], Gen: m.Gen})
		changed = true
	}
	m.Unreconciled = carryUnreconciled(base, m, d, bound, known)
	if !changed && !idsChanged && lifecycle.Digest(b) == cur.base.digest {
		in.out.set(meta) // nothing to write: what is there is what it left
		return nil
	}
	m.Digest = lifecycle.Digest(b)
	mb, err := lifecycle.EncodeMeta(m)
	if err != nil {
		return err
	}
	if !idsChanged {
		ids = nil
	}
	if err := commitPlan(whatProviders, b, ids, mb); err != nil {
		return err
	}
	in.out.set(&m)
	return nil
}

func (c *committed) set(meta *lifecycle.Meta) {
	if c != nil {
		c.meta = meta
	}
}

// carryUnreconciled is base's undecided ids after this write: a renamed
// one goes with its new id, a deleted or newly added one goes (its
// binding went with it), and with the bindings known only those still
// bound stay. An entry whose id now holds another instance is dropped:
// the binding there is not the one that was undecided.
func carryUnreconciled(base, next lifecycle.Meta, d diff, bound map[string]bool, known bool) []lifecycle.Unreconciled {
	gone := map[string]bool{}
	for _, id := range d.drops {
		gone[id] = true
	}
	for _, id := range d.adds {
		gone[id] = true
	}
	// bound after: renamed bindings at their new ids, dropped ones gone
	after := map[string]bool{}
	for id := range bound {
		if _, moving := d.moves[id]; !moving && !gone[id] {
			after[id] = true
		}
	}
	for from, to := range d.moves {
		delete(after, to)
		if bound[from] {
			after[to] = true
		}
	}
	var out []lifecycle.Unreconciled
	for _, u := range base.Unreconciled {
		if to, ok := d.moves[u.ID]; ok {
			u.ID = to
		} else if gone[u.ID] {
			continue
		}
		if known && !after[u.ID] {
			continue
		}
		if p, ok := next.Providers[u.ID]; ok && p.Instance != u.Instance {
			continue
		}
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b lifecycle.Unreconciled) int { return strings.Compare(a.ID, b.ID) })
	return slices.CompactFunc(out, func(a, b lifecycle.Unreconciled) bool { return a.ID == b.ID })
}
