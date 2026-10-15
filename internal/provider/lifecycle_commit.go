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
	// expect are ids that must still be the instance (and, nonzero, the
	// rev) the caller read.
	expect map[string]lifecycle.Provider
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
	// what agents were handed of the catalog may be out of date now
	catalog.Touched()
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
	if meta == nil && len(in.expect) > 0 {
		for id := range in.expect {
			return &StaleError{ID: id}
		}
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
	// under the lock no writer is replacing identities.json (its writers
	// take the lock too), so not there is absent: no bindings, and no
	// wait for a replace (ReadImage's, ~20ms on Windows)
	if _, err := os.Lstat(identity.Path()); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil, nil
	}
	image, err := lifecycle.ReadImage(lifecycle.IdentitiesFile)
	if err != nil {
		return nil, false, nil, fmt.Errorf("providers not changed: %w", err)
	}
	var cur []byte
	if image.Exists {
		cur = image.Bytes
		if cur == nil {
			cur = []byte{}
		}
	}
	if bound, err = identity.BoundIn(cur); err != nil {
		return nil, false, nil, fmt.Errorf("providers not changed: %w", err)
	}
	// a new id's orphan binding goes; so does a deleted one's
	drops := append(slices.Clone(d.drops), d.adds...)
	after, changed, err = identity.PlanBound(cur, d.moves, drops)
	if err != nil {
		return nil, false, nil, fmt.Errorf("providers not changed: %w", err)
	}
	return after, changed, bound, nil
}

// usableBak refuses a commit when name's .bak is there but is not a plain
// file: its last good generation could not be kept (design C.3a).
func usableBak(name string) error {
	bak := lastgood.Bak(filepath.Join(filepath.Dir(Path()), name))
	fi, err := os.Lstat(bak)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("providers not changed: %s can't be checked: %w", bak, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("providers not changed: %s is not a plain file, so the last good generation can't be kept; move it aside and try again", bak)
	}
	return nil
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
	return commitPlan(b, after, nil)
}

// commitPlan prepares and commits providers.json, identities.json when
// ids is not nil, and lifecycle.json when meta is not nil.
func commitPlan(b, ids, meta []byte) error {
	type part struct {
		name  string
		after []byte
		valid lastgood.Valid
	}
	parts := []part{{lifecycle.ProvidersFile, b, validProviders}}
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
			if err := usableBak(p.name); err != nil {
				return err
			}
		}
	}
	var ps []lifecycle.Participant
	for _, part := range parts {
		p, err := lifecycle.Prepare(part.name, lifecycle.Present(part.after), part.valid)
		if err != nil {
			return fmt.Errorf("providers not changed: %w", err)
		}
		if !p.Before.Exists || !bytes.Equal(p.Before.Bytes, part.after) {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return nil
	}
	if err := lifecycle.Commit(lifecycle.Plan{Participants: ps}); err != nil {
		return fmt.Errorf("providers: %w", err)
	}
	return nil
}

// commitManaged is a write with lifecycle.json: instances kept, revs
// bumped, deleted instances retired, all three files in one journal.
func commitManaged(cur, next file, b []byte, d diff, meta *lifecycle.Meta, in intent) error {
	if cur.base.fallback || cur.base.digest != meta.Digest {
		return fmt.Errorf("providers not changed: %w: %s differs from %s; run magpie identity reconcile", lifecycle.ErrUnknown, Path(), lifecycle.Path())
	}
	for id := range d.oldRows {
		if _, ok := meta.Providers[id]; !ok {
			return fmt.Errorf("providers not changed: %w: %s has no instance for %q; run magpie identity reconcile", lifecycle.ErrUnknown, lifecycle.Path(), id)
		}
	}
	for id, want := range in.expect {
		got, ok := meta.Providers[id]
		if !ok || got.Instance != want.Instance || (want.Rev != 0 && got.Rev != want.Rev) {
			return &StaleError{ID: id}
		}
	}
	ids, idsChanged, bound, err := identitiesPlan(d)
	if err != nil {
		return err
	}
	m := *meta
	m.Providers = make(map[string]lifecycle.Provider, len(d.newRows))
	m.Retired = slices.Clone(meta.Retired)
	m.Gen = meta.Gen + 1
	changed := false
	for id, row := range d.newRows {
		old, was := id, true
		for from, to := range d.moves {
			if to == id {
				old = from
			}
		}
		rec, ok := meta.Providers[old]
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
		m.Providers[id] = rec
	}
	for _, id := range d.drops {
		m.Retire(lifecycle.Retired{Instance: meta.Providers[id].Instance, Bound: bound[id], Gen: m.Gen})
		changed = true
	}
	if !changed && !idsChanged && lifecycle.Digest(b) == cur.base.digest {
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
	return commitPlan(b, ids, mb)
}
