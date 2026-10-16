package provider

// Provider lifecycle on the read and signing side (R1 G2b-1). A stored
// provider's id can be reused: deleted and added again, or renamed away and
// another renamed onto it. lifecycle.json (internal/lifecycle) gives each
// stored row a stable instance; this file marks the Provider values magpie
// hands out with the instance they were read as, and signs a marked value
// by its instance rather than by whatever now holds its id.
//
// The zero marker is legacy: no lifecycle.json, a Provider literal, or a
// path upstream added. It signs by the current id, as before this existed,
// so an unbound provider and every upstream test are unchanged.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sync"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/identity"
	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/settings"
)

// ErrStale is a Provider value read before its provider was deleted, or
// before the lifecycle state it was read under was replaced: it can't be
// told apart from what holds its id now, so it is not signed as that.
var ErrStale = errors.New("provider changed since it was read; read it again")

// StaleError names the provider a stale value was read as.
type StaleError struct{ ID string }

func (e *StaleError) Error() string {
	return fmt.Sprintf("provider %q: %v", e.ID, ErrStale)
}
func (e *StaleError) Unwrap() error { return ErrStale }

type lifeKind uint8

const (
	lifeLegacy       lifeKind = iota // zero: sign by the current id
	lifeManaged                      // read under an id lifecycle.json gives an instance
	lifeUnreconciled                 // read under an id lifecycle.json doesn't know (changed outside magpie)
	lifeTransient                    // not saved: no binding is used
	lifeUnknown                      // the lifecycle state could not be read: fail closed
)

// life is the immutable lifecycle marker a Provider value carries. It is a
// value, so copies (clone, WithKey, typed) carry it; nothing re-marks a value
// once handed out.
type life struct {
	kind     lifeKind
	epoch    string
	instance string
	issuedID string
}

// fileBase is what a read of providers.json found, not serialized: present
// (raw bytes' digest, whether the .bak was used) or absent. A failed read
// has no file and so no base. The zero value is "not read" (a literal).
type fileBase struct {
	read     bool
	exists   bool
	digest   string
	fallback bool
}

func presentBase(raw []byte, fallback bool) fileBase {
	return fileBase{read: true, exists: true, digest: lifecycle.Digest(raw), fallback: fallback}
}

var absentBase = fileBase{read: true}

// Transient marks p as not saved: a form being tried before its Add. It
// presents no binding, even one stored under the same id, since that
// binding belongs to another provider.
func Transient(p Provider) Provider {
	p.life = life{kind: lifeTransient}
	return p
}

// beginLife is lifecycle.Begin, counted by tests that assert accounts and
// plugins never read lifecycle state.
var beginLife = lifecycle.Begin

// readSnapshot is identity.ReadSnapshot; a test pauses it to write between.
var readSnapshot = identity.ReadSnapshot

// issue is how rows of one providers.json read are marked.
type issue struct {
	unknown bool
	meta    *lifecycle.Meta // nil: legacy
	managed bool            // digest matched the primary file
}

func (is issue) of(id string) life {
	switch {
	case is.unknown:
		return life{kind: lifeUnknown, issuedID: id}
	case is.meta == nil:
		return life{}
	}
	rec, ok := is.meta.Providers[id]
	if !ok {
		// an id lifecycle.json doesn't know (added outside magpie, not
		// adopted yet): a binding under it is not known to be its own
		return life{kind: lifeUnreconciled, epoch: is.meta.Epoch, issuedID: id}
	}
	// a known id is its instance even while providers.json differs from
	// lifecycle.json: a change of content keeps the instance (design §6)
	return life{kind: lifeManaged, epoch: is.meta.Epoch, instance: rec.Instance, issuedID: id}
}

// issueFor is how a read of f under lifecycle state m marks its rows.
func issueFor(f file, m lifecycle.Mark) issue {
	switch m.State() {
	case lifecycle.MetaValid:
		meta := m.Meta()
		return issue{meta: meta, managed: f.base.exists && !f.base.fallback && f.base.digest == meta.Digest}
	case lifecycle.MetaAbsent, lifecycle.MetaCorrupt:
		return issue{} // corrupt: new values are legacy, old managed ones stale
	}
	return issue{unknown: true}
}

// lifeLoad is load with the marking its rows get, the file and the
// lifecycle state from one window. A window that can't be held marks the
// rows unknown, so a bound one fails rather than signs as some other.
func lifeLoad() (file, issue) {
	notUnderCommit("lifeLoad")
	for range 3 {
		m, err := beginLife(lifecycle.Writable)
		if err != nil {
			if errors.Is(err, lifecycle.ErrBusy) {
				continue
			}
			return load(), issue{unknown: true}
		}
		if m.State() == lifecycle.MetaAbsent {
			return load(), issue{} // legacy: no marker to agree with the file
		}
		f := load()
		if err := m.Validate(); err != nil {
			if errors.Is(err, lifecycle.ErrBusy) {
				continue
			}
			return f, issue{unknown: true}
		}
		return f, issueFor(f, m)
	}
	return load(), issue{unknown: true}
}

// headersFor is the identity headers p presents, from one identity read
// that agrees with the lifecycle state it was taken under.
func (p Provider) headersFor() (http.Header, error) {
	notUnderCommit("headersFor")
	switch p.life.kind {
	case lifeTransient:
		// no binding, but an unreadable store still fails closed
		if _, err := readSnapshot(); err != nil {
			return nil, err
		}
		return nil, nil
	case lifeUnknown:
		return nil, fmt.Errorf("%w: providers.json was read without a lifecycle state", lifecycle.ErrUnknown)
	case lifeLegacy:
		m, err := beginLife(lifecycle.Writable)
		if err != nil {
			return nil, err
		}
		switch m.State() {
		case lifecycle.MetaUnsupported, lifecycle.MetaIO:
			return nil, m.Err()
		}
		// one atomic read of the one owner's file, by the current id
		return identityHeaders(p.ID)
	}
	var last error
	for range 3 {
		h, err := p.capturedHeaders()
		if !errors.Is(err, lifecycle.ErrBusy) {
			return h, err
		}
		last = err
	}
	return nil, last
}

// capturedHeaders is one window for a marked value: lifecycle state, then
// providers.json, then exactly one identity snapshot, then Validate. Every
// decision is taken from those three reads alone.
func (p Provider) capturedHeaders() (http.Header, error) {
	m, err := beginLife(lifecycle.Writable)
	if err != nil {
		return nil, err
	}
	switch m.State() {
	case lifecycle.MetaAbsent, lifecycle.MetaCorrupt:
		// what this value was marked under is gone; its instance can't be
		// matched to what holds an id now
		return nil, &StaleError{ID: p.life.issuedID}
	case lifecycle.MetaUnsupported, lifecycle.MetaIO:
		return nil, m.Err()
	}
	f, rerr := read()
	snap, serr := readSnapshot()
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if rerr != nil {
		return nil, fmt.Errorf("%w: %w", lifecycle.ErrUnknown, rerr)
	}
	if serr != nil {
		return nil, serr
	}
	meta := m.Meta()
	is := issueFor(f, m)
	var inFile map[string]bool
	if !is.managed {
		inFile = make(map[string]bool, len(f.Providers))
		for _, q := range f.Providers {
			inFile[q.ID] = true
		}
	}
	return signDecision(p.life, meta, is.managed, inFile, snap)
}

// signDecision is design C.3's table for a marked value, decided from one
// lifecycle state, one providers.json read and one identity snapshot.
//
// providers.json that differs from lifecycle.json (managed false; inFile
// is its ids then) is projected as an adoption would settle it, without
// writing: an id in both is its instance, whatever its content; a bound id
// lifecycle.json lists that is no longer in the file, and a new id an
// orphan binding already names, are unreconciled and fail closed while
// bound. Every other provider signs as before. Ids an adoption marked
// (meta.Unreconciled) fail closed while bound.
func signDecision(l life, meta *lifecycle.Meta, managed bool, inFile map[string]bool, snap identity.Snapshot) (http.Header, error) {
	if meta.Epoch != l.epoch {
		return nil, &StaleError{ID: l.issuedID}
	}
	bindings := snap.Bindings()
	unknown := func(id string) error {
		return fmt.Errorf("%w: provider %q is not reconciled; run magpie identity reconcile", lifecycle.ErrUnknown, id)
	}
	if l.instance == "" {
		// read while unreconciled under an id the lifecycle state didn't
		// know: a binding under that id is not known to be its own
		if _, bound := bindings[l.issuedID]; bound {
			return nil, unknown(l.issuedID)
		}
		return nil, nil
	}
	cur := ""
	for id, rec := range meta.Providers {
		if rec.Instance == l.instance {
			cur = id
			break
		}
	}
	if cur == "" {
		i := slices.IndexFunc(meta.Retired, func(r lifecycle.Retired) bool { return r.Instance == l.instance })
		if i < 0 || meta.Retired[i].Bound {
			// deleted while bound, or so long ago its tombstone is gone
			return nil, &StaleError{ID: l.issuedID}
		}
		return nil, nil // deleted unbound: signed as it was, with none
	}
	_, bound := bindings[cur]
	if bound && (meta.IsUnreconciled(cur) || !managed && !inFile[cur]) {
		// marked undecided, or removed outside magpie while bound and
		// not adopted yet
		return nil, unknown(cur)
	}
	if !managed && !inFile[cur] {
		return nil, nil // removed outside magpie unbound: as a tombstone would
	}
	return snap.HeaderFor(cur)
}

// IdentityView is the identity store and the providers from one capture,
// for the GUI's identity panel and magpie identity ls. Nothing in it is
// read again, so a rename or an unbind meanwhile can't mix two states.
type IdentityView struct {
	Snapshot  identity.Snapshot
	providers []Provider
	f         file
	is        issue
	built     bool
}

// Providers is All as the view's capture had it, built outside any lock.
func (v *IdentityView) Providers() []Provider {
	if !v.built {
		v.providers, v.built = handOut(allProvidersFrom(v.f, v.is)), true
	}
	return slices.Clone(v.providers)
}

// ReadIdentityView captures the identity store, and when withProviders the
// providers too, from one window. A legacy combined view holds lifecycle's
// lock for its two reads (Validate can't see a legacy commit), taken only
// after Begin, and only where the config folder already exists: a read that
// creates it would leave a fresh home not empty.
func ReadIdentityView(withProviders bool) (*IdentityView, error) {
	notUnderCommit("ReadIdentityView")
	var last error
	for range 3 {
		v, err := captureView(withProviders)
		if !errors.Is(err, lifecycle.ErrBusy) {
			return v, err
		}
		last = err
	}
	return nil, last
}

func captureView(withProviders bool) (*IdentityView, error) {
	m, err := beginLife(lifecycle.Writable)
	if err != nil {
		return nil, err
	}
	switch m.State() {
	case lifecycle.MetaUnsupported, lifecycle.MetaIO:
		return nil, m.Err()
	}
	if !withProviders {
		snap, err := readSnapshot()
		if err != nil {
			return nil, err
		}
		return &IdentityView{Snapshot: snap}, nil
	}
	if m.State() != lifecycle.MetaValid {
		return lockedView(m)
	}
	f := load()
	snap, serr := readSnapshot()
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if serr != nil {
		return nil, serr
	}
	return &IdentityView{Snapshot: snap, f: f, is: issueFor(f, m)}, nil
}

// lockedView is a legacy combined capture under lifecycle's lock.
func lockedView(m lifecycle.Mark) (*IdentityView, error) {
	if !configExists() {
		// nothing stored yet: one read of each is all there is
		snap, err := readSnapshot()
		if err != nil {
			return nil, err
		}
		f := load()
		return &IdentityView{Snapshot: snap, f: f, is: issueFor(f, m)}, nil
	}
	unlock, err := lockLife()
	if err != nil {
		return nil, fmt.Errorf("identity view unavailable: %w", err)
	}
	defer unlock()
	if err := lifecycle.Recover(); err != nil {
		return nil, err
	}
	f := load()
	snap, err := readSnapshot()
	if err != nil {
		return nil, err
	}
	return &IdentityView{Snapshot: snap, f: f, is: issueFor(f, m)}, nil
}

// handOut is All's filter and copy over a built list.
func handOut(held []Provider) []Provider {
	out := make([]Provider, 0, len(held))
	off := sync.OnceValue(func() bool { return settings.AccountDiscoveryOffIn(heldSettings()) })
	for _, p := range held {
		if p.Account != nil && off() {
			continue
		}
		out = append(out, p.clone())
	}
	return out
}

// configExists says whether magpie's config folder is there already.
func configExists() bool {
	fi, err := os.Stat(appdir.Config())
	return err == nil && fi.IsDir()
}

// lockLife is lifecycle's lock, for a legacy combined capture.
func lockLife() (func(), error) { return lifecycle.Lock(context.Background()) }
