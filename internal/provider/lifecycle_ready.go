package provider

// Lifecycle state set up and kept in step with providers.json (R1 G3a).
// lifecycle.json is made at start-up only (ReadyLifecycle): ordinary reads
// and writes never bootstrap it, so a home without one, and every test
// home, stays legacy (by id).
//
// providers.json changed outside magpie (an older magpie, an editor, its
// .bak read in its place) no longer matches lifecycle.json's digest. Under
// lifecycle's lock, at start-up and in the next provider write, that
// change is adopted into lifecycle.json alone: a row whose content changed
// keeps its instance at the next rev, an unbound id added or removed gets
// a new instance or a tombstone. An added or removed id the change leaves
// a binding undecided for is marked unreconciled: it fails closed while
// bound until magpie identity reconcile decides, and nothing else stops
// signing. A request never writes; until a writer adopts, signing projects
// the same rule from what it read (signDecision).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/yetone/magpie/internal/filememo"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/lifecycle"
)

// adoption is what adopt changed, by id, for a preview to show.
type adoption struct {
	edited, added, deleted []string
	undecided              []string // ids marked unreconciled by this adoption
}

func (a adoption) any() bool { return len(a.edited)+len(a.added)+len(a.deleted) > 0 }

// adopt is meta describing rows (providers.json's rows by id, as stored),
// written outside magpie: a kept id keeps its instance, at the next rev
// when its row changed (or its row digest isn't known); a new id gets a
// new instance, and is marked unreconciled when an orphan binding already
// names it; a gone id is retired with whether it was bound, and marked
// unreconciled when it was. bound is which ids identities.json binds
// now. gen is the generation the result is committed as, which a
// tombstone made here carries. Marks of ids no longer bound are dropped.
func adopt(meta lifecycle.Meta, rows map[string][]byte, bound map[string]bool, gen uint64) (lifecycle.Meta, adoption, error) {
	m := meta.Clone()
	var res adoption
	undecided := map[string]lifecycle.Unreconciled{}
	for _, u := range m.Unreconciled {
		undecided[u.ID] = u
	}
	m.Providers = make(map[string]lifecycle.Provider, len(rows))
	for _, id := range sortedKeys(rows) {
		row := lifecycle.Digest(rows[id])
		rec, ok := meta.Providers[id]
		if !ok {
			inst, err := lifecycle.NewID()
			if err != nil {
				return lifecycle.Meta{}, adoption{}, err
			}
			rec = lifecycle.Provider{Instance: inst, Rev: 1, Row: row}
			res.added = append(res.added, id)
			delete(undecided, id) // a mark under this id was another instance's
			if bound[id] {
				undecided[id] = lifecycle.Unreconciled{ID: id, Instance: inst}
				res.undecided = append(res.undecided, id)
			}
		} else if rec.Row != row {
			rec.Rev++
			rec.Row = row
			res.edited = append(res.edited, id)
		}
		m.Providers[id] = rec
	}
	for _, id := range sortedKeys(meta.Providers) {
		if _, ok := rows[id]; ok {
			continue
		}
		inst := meta.Providers[id].Instance
		m.Retire(lifecycle.Retired{Instance: inst, Bound: bound[id], Gen: gen})
		res.deleted = append(res.deleted, id)
		delete(undecided, id)
		if bound[id] {
			undecided[id] = lifecycle.Unreconciled{ID: id, Instance: inst}
			res.undecided = append(res.undecided, id)
		}
	}
	m.Unreconciled = nil
	for _, id := range sortedKeys(undecided) {
		u := undecided[id]
		if !bound[id] {
			continue // no binding left to decide
		}
		if p, ok := m.Providers[id]; ok && p.Instance != u.Instance {
			continue
		}
		m.Unreconciled = append(m.Unreconciled, u)
	}
	m.Gen = gen
	return m, res, nil
}

// freshMeta is lifecycle.json for rows, new: a new epoch, and every id a
// new instance at rev 1.
func freshMeta(rows map[string][]byte, digest string) (lifecycle.Meta, error) {
	epoch, err := lifecycle.NewID()
	if err != nil {
		return lifecycle.Meta{}, err
	}
	m := lifecycle.Meta{Version: lifecycle.Version, Epoch: epoch, Gen: 1, Digest: digest, Providers: map[string]lifecycle.Provider{}}
	for _, id := range sortedKeys(rows) {
		inst, err := lifecycle.NewID()
		if err != nil {
			return lifecycle.Meta{}, err
		}
		m.Providers[id] = lifecycle.Provider{Instance: inst, Rev: 1, Row: lifecycle.Digest(rows[id])}
	}
	return m, nil
}

// readyOutcome is what ReadyLifecycle found and did.
type readyOutcome int

const (
	readyLegacy         readyOutcome = iota + 1 // no lifecycle.json and none made: by id
	readyCurrent                                // lifecycle.json matches providers.json
	readyBootstrapped                           // lifecycle.json made
	readyRebootstrapped                         // a corrupt one kept aside and made anew
	readyAdopted                                // a change made outside magpie adopted
	readyPending                                // a mismatch left for the next write (providers.json read from its .bak, or not there)
	readyFailed                                 // the state is unknown; nothing written
)

func (o readyOutcome) String() string {
	return map[readyOutcome]string{readyLegacy: "legacy", readyCurrent: "current", readyBootstrapped: "bootstrapped",
		readyRebootstrapped: "rebootstrapped", readyAdopted: "adopted", readyPending: "pending", readyFailed: "failed"}[o]
}

// readyLockWait bounds start-up's wait for lifecycle's lock: another
// magpie holding it leaves this one legacy for its run, not stuck.
var readyLockWait = 3 * time.Second

// ReadyLifecycle readies lifecycle.json at start-up, before the providers
// are held: it finishes a journal a dead writer left, makes lifecycle.json
// when there is none (or keeps a corrupt one aside and makes it anew, so a
// value read under the old one is stale), and adopts a change made to
// providers.json outside magpie. It is the only call that bootstraps.
//
// With no config folder, one it can't write, or the lock held by another
// magpie past readyLockWait, it stays legacy and returns nil. lifecycle.json
// from a newer magpie or that can't be read, and a journal that can't be
// finished, are left as they are and returned: requests fail closed then.
func ReadyLifecycle() error {
	_, err := readyLifecycle()
	return err
}

func readyLifecycle() (readyOutcome, error) {
	notUnderCommit("ReadyLifecycle")
	if !configExists() {
		return readyLegacy, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), readyLockWait)
	defer cancel()
	unlock, err := lifecycle.Lock(ctx)
	if err != nil {
		log.Printf("lifecycle: not readied, providers stay by id this run: %v", err)
		return readyLegacy, nil
	}
	out, err := readyHeld(unlock)
	// what is held was read, and marked, before: a journal finished, a
	// bootstrap or an adoption changes what a read gives
	Changed()
	if err != nil {
		return out, fmt.Errorf("lifecycle: %w", err)
	}
	return out, nil
}

// readyHeld is ReadyLifecycle's locked section, as commitHeld is commit's.
func readyHeld(unlock func()) (readyOutcome, error) {
	defer unlock()
	defer filememo.Forget()
	return readyLocked()
}

// readyLocked calls nothing that lists providers or accounts, reaches the
// network, or takes the lock again.
func readyLocked() (readyOutcome, error) {
	if err := lifecycle.Recover(); err != nil {
		return readyFailed, err
	}
	meta, err := lifecycle.ReadMeta()
	corrupt := errors.Is(err, lifecycle.ErrMetaCorrupt)
	if err != nil && !corrupt {
		return readyFailed, err // unsupported or unreadable: left as it is
	}
	f, rerr := read()
	if meta == nil {
		out, err := bootstrap(f, rerr, corrupt)
		if err != nil && unwritable(err) {
			// a folder it can't write stays legacy (by id) for this run;
			// a corrupt lifecycle.json left there reads as legacy too
			log.Printf("lifecycle: not readied, the config folder can't be written; providers stay by id this run: %v", err)
			return readyLegacy, nil
		}
		return out, err
	}
	switch {
	case rerr != nil:
		return readyFailed, rerr
	case !f.base.exists || f.base.fallback:
		// adopting the .bak's rows would describe a providers.json that
		// isn't on disk; the next provider write writes it, and adopts
		return readyPending, nil
	case f.base.digest == meta.Digest:
		return readyCurrent, nil
	}
	rows, err := rowsByID(f)
	if err != nil {
		return readyFailed, err
	}
	_, bound, err := boundLocked()
	if err != nil {
		return readyFailed, err
	}
	m, _, err := adopt(*meta, rows, bound, meta.Gen+1)
	if err != nil {
		return readyFailed, err
	}
	m.Digest = f.base.digest
	mb, err := lifecycle.EncodeMeta(m)
	if err != nil {
		return readyFailed, err
	}
	if err := commitPlan(nil, nil, mb); err != nil {
		if unwritable(err) {
			// signing projects the same rule from what it reads; the next
			// write that can go through adopts
			log.Printf("lifecycle: a change made outside magpie not adopted, the config folder can't be written: %v", err)
			return readyPending, nil
		}
		return readyFailed, err
	}
	return readyAdopted, nil
}

// unwritable is a commit refused for permission (or a read-only file
// system) before its journal was published: nothing changed on disk, so a
// start-up in a config folder it can't write is left as it was. A failure
// once the journal is there is the commit's error, for Recover.
func unwritable(err error) bool {
	if !errors.Is(err, fs.ErrPermission) && !errors.Is(err, syscall.EROFS) {
		return false
	}
	_, jerr := os.Lstat(lifecycle.JournalPath())
	return errors.Is(jerr, fs.ErrNotExist)
}

// bootstrap makes lifecycle.json for f, the providers.json read under the
// lock (rerr its read's error), where there is none or it is corrupt.
// lifecycle.json describes a providers.json on disk: one that is not
// there, can't be read or is read from its .bak gets none, and a corrupt
// lifecycle.json is then kept aside and removed (legacy, by id).
func bootstrap(f file, rerr error, corrupt bool) (readyOutcome, error) {
	if rerr != nil || !f.base.exists || f.base.fallback {
		if corrupt {
			if err := dropCorruptMeta(); err != nil {
				return readyFailed, err
			}
		}
		return readyLegacy, nil
	}
	rows, err := rowsByID(f)
	if err != nil {
		return readyFailed, err
	}
	m, err := freshMeta(rows, f.base.digest)
	if err != nil {
		return readyFailed, err
	}
	mb, err := lifecycle.EncodeMeta(m)
	if err != nil {
		return readyFailed, err
	}
	// a corrupt one is kept as lifecycle.json.bad-<time> by the commit
	if err := commitPlan(nil, nil, mb); err != nil {
		return readyFailed, err
	}
	if corrupt {
		return readyRebootstrapped, nil
	}
	return readyBootstrapped, nil
}

// dropCorruptMeta keeps a corrupt lifecycle.json aside and removes it.
func dropCorruptMeta() error {
	b, err := os.ReadFile(lifecycle.Path())
	if err != nil {
		return err
	}
	if _, err := lastgood.KeepBad(lifecycle.Path(), b); err != nil {
		return err
	}
	return os.Remove(lifecycle.Path())
}

// ReconcileChoice is what magpie identity reconcile --yes does with the
// bindings a change made outside magpie left undecided.
type ReconcileChoice int

const (
	ReconcileUndecided ReconcileChoice = iota // refuse when any is undecided
	ReconcileKeep                             // leave each binding as it is
	ReconcileDrop                             // unbind each
)

// ErrReconcileChoice is a reconcile with undecided bindings and no choice.
var ErrReconcileChoice = errors.New("bindings are undecided: choose --keep or --drop")

// ReconcileRow is one provider id and the instance lifecycle.json gives it.
type ReconcileRow struct {
	ID       string
	Instance string // "" for one a reconcile would add
	Change   string // "", "edited", "added" or "deleted" (outside magpie)
}

// ReconcileBinding is a binding a change made outside magpie left undecided.
type ReconcileBinding struct {
	ID      string
	Profile string
	// Gone: the id was removed from providers.json while bound. Otherwise
	// it was added under an id an orphan binding already named.
	Gone bool
}

// ReconcilePlan is what magpie identity reconcile found, and did with --yes.
type ReconcilePlan struct {
	// State is "legacy" (no lifecycle.json), "current", "adopt" (a change
	// made outside magpie not adopted yet), "corrupt", "fallback"
	// (providers.json read from its .bak) or "missing" (no providers.json).
	State    string
	Pending  string // a journal waiting to be finished or set aside
	Rows     []ReconcileRow
	Affected []ReconcileBinding
	Kept     []string // files kept aside before writing
	Wrote    bool
}

// PreviewReconcile is what Reconcile would do. It writes nothing: no lock
// file, no recovery, no evidence copy.
func PreviewReconcile() (ReconcilePlan, error) {
	notUnderCommit("PreviewReconcile")
	var plan ReconcilePlan
	if _, err := os.Lstat(lifecycle.JournalPath()); err == nil {
		plan.Pending = lifecycle.JournalPath()
	}
	meta, err := lifecycle.ReadMeta()
	corrupt := errors.Is(err, lifecycle.ErrMetaCorrupt)
	if err != nil && !corrupt {
		return plan, err
	}
	if meta == nil && !corrupt {
		plan.State = "legacy"
		return plan, nil
	}
	f, err := read()
	if err != nil {
		return plan, err
	}
	rows, err := rowsByID(f)
	if err != nil {
		return plan, err
	}
	if corrupt {
		plan.State = "corrupt"
		for _, id := range sortedKeys(rows) {
			plan.Rows = append(plan.Rows, ReconcileRow{ID: id, Change: "added"})
		}
		return plan, nil
	}
	switch {
	case !f.base.exists:
		plan.State = "missing"
		return plan, nil
	case f.base.fallback:
		plan.State = "fallback"
	case f.base.digest != meta.Digest:
		plan.State = "adopt"
	default:
		plan.State = "current"
	}
	snap, err := identity.ReadSnapshot()
	if err != nil {
		return plan, err
	}
	bindings := snap.Bindings()
	bound := map[string]bool{}
	for id, profile := range bindings {
		bound[id] = profile != ""
	}
	base, res := *meta, adoption{}
	if plan.State == "adopt" || plan.State == "fallback" {
		if base, res, err = adopt(*meta, rows, bound, meta.Gen+1); err != nil {
			return plan, err
		}
	}
	plan.Rows, plan.Affected = describeReconcile(base, res, bindings, bound)
	return plan, nil
}

// describeReconcile is the rows and undecided bindings of base (meta as
// it is, or as adopting would leave it), for a preview or a result.
func describeReconcile(base lifecycle.Meta, res adoption, bindings map[string]string, bound map[string]bool) ([]ReconcileRow, []ReconcileBinding) {
	change := map[string]string{}
	for _, id := range res.edited {
		change[id] = "edited"
	}
	for _, id := range res.added {
		change[id] = "added"
	}
	var rows []ReconcileRow
	for _, id := range sortedKeys(base.Providers) {
		r := ReconcileRow{ID: id, Instance: base.Providers[id].Instance, Change: change[id]}
		if r.Change == "added" {
			r.Instance = "" // new here: the one --yes writes is its own
		}
		rows = append(rows, r)
	}
	for _, id := range res.deleted {
		rows = append(rows, ReconcileRow{ID: id, Change: "deleted"})
	}
	var affected []ReconcileBinding
	for _, u := range base.Unreconciled {
		if !bound[u.ID] {
			continue
		}
		_, live := base.Providers[u.ID]
		affected = append(affected, ReconcileBinding{ID: u.ID, Profile: bindings[u.ID], Gone: !live})
	}
	return rows, affected
}

// Reconcile settles lifecycle.json with providers.json and identities.json
// under lifecycle's lock: a journal that can't be finished is kept aside
// (lifecycle-journal.json.reconciled-<time>) and removed, a corrupt
// lifecycle.json is made anew, a change made outside magpie is adopted,
// and the bindings it left undecided are kept or dropped as choice says.
// lifecycle.json is kept aside (lifecycle.json.reconciled-<time>) before
// it is written over. With undecided bindings and no choice nothing is
// written (ErrReconcileChoice).
func Reconcile(choice ReconcileChoice) (ReconcilePlan, error) {
	notUnderCommit("Reconcile")
	unlock, err := lockCommit()
	if err != nil {
		return ReconcilePlan{}, fmt.Errorf("nothing reconciled: %w", err)
	}
	plan, err := reconcileHeld(choice, unlock)
	Changed()
	return plan, err
}

func reconcileHeld(choice ReconcileChoice, unlock func()) (ReconcilePlan, error) {
	defer unlock()
	defer filememo.Forget()
	return reconcileLocked(choice)
}

// reconcileLocked calls nothing that lists providers or accounts, reaches
// the network, or takes the lock again.
func reconcileLocked(choice ReconcileChoice) (ReconcilePlan, error) {
	var plan ReconcilePlan
	if err := lifecycle.Recover(); err != nil {
		if !unfinishable(err) {
			return plan, err
		}
		aside, derr := lifecycle.DiscardJournal()
		if aside != "" {
			plan.Kept = append(plan.Kept, aside)
		}
		if derr != nil {
			return plan, derr
		}
	}
	meta, err := lifecycle.ReadMeta()
	corrupt := errors.Is(err, lifecycle.ErrMetaCorrupt)
	if err != nil && !corrupt {
		return plan, err
	}
	f, rerr := read()
	if meta == nil {
		if !corrupt {
			plan.State = "legacy"
			return plan, nil
		}
		plan.State = "corrupt"
		aside, err := lifecycle.KeepAside(lifecycle.MetaFile, "reconciled")
		if aside != "" {
			plan.Kept = append(plan.Kept, aside)
		}
		if err != nil {
			return plan, err
		}
		out, err := bootstrap(f, rerr, true)
		plan.Wrote = out == readyRebootstrapped || out == readyLegacy && err == nil
		return plan, err
	}
	switch {
	case rerr != nil:
		return plan, rerr
	case !f.base.exists:
		plan.State = "missing"
		return plan, fmt.Errorf("nothing reconciled: %s is not there; add a provider (or put it back), then reconcile", Path())
	case f.base.fallback:
		plan.State = "fallback"
		return plan, fmt.Errorf("nothing reconciled: %s can't be read and its .bak is read in its place; fix it, or save any provider change (which writes it again and adopts it), then reconcile", Path())
	}
	rows, err := rowsByID(f)
	if err != nil {
		return plan, err
	}
	cur, bound, err := boundLocked()
	if err != nil {
		return plan, err
	}
	bindings, err := identity.BindingsIn(cur)
	if err != nil {
		return plan, err
	}
	gen := meta.Gen + 1
	base, res := *meta, adoption{}
	adopting := f.base.digest != meta.Digest
	plan.State = "current"
	if adopting {
		plan.State = "adopt"
		if base, res, err = adopt(*meta, rows, bound, gen); err != nil {
			return plan, err
		}
	}
	plan.Rows, plan.Affected = describeReconcile(base, res, bindings, bound)
	if len(plan.Affected) > 0 && choice == ReconcileUndecided {
		return plan, ErrReconcileChoice
	}
	var ids []byte
	if choice == ReconcileDrop && len(plan.Affected) > 0 {
		var drops []string
		for _, a := range plan.Affected {
			drops = append(drops, a.ID)
		}
		if ids, _, err = identity.PlanBound(cur, nil, drops); err != nil {
			return plan, err
		}
	}
	if !adopting && len(meta.Unreconciled) == 0 {
		return plan, nil // nothing to settle
	}
	base = base.Clone()
	base.Unreconciled = nil
	base.Gen = gen
	base.Digest = f.base.digest
	mb, err := lifecycle.EncodeMeta(base)
	if err != nil {
		return plan, err
	}
	aside, err := lifecycle.KeepAside(lifecycle.MetaFile, "reconciled")
	if aside != "" {
		plan.Kept = append(plan.Kept, aside)
	}
	if err != nil {
		return plan, err
	}
	if err := commitPlan(nil, ids, mb); err != nil {
		return plan, err
	}
	plan.Wrote = true
	return plan, nil
}

// unfinishable is a Recover error a reconcile may set the journal aside
// for: the journal itself doesn't decode, or the files are in a third
// state. An I/O error is not one: it is returned, and the journal stays.
func unfinishable(err error) bool {
	var amb *lifecycle.AmbiguousError
	if errors.As(err, &amb) {
		return true
	}
	b, rerr := os.ReadFile(lifecycle.JournalPath())
	if rerr != nil {
		return false
	}
	_, derr := lifecycle.DecodeJournal(b)
	return derr != nil
}
