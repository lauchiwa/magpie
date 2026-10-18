package provider

// Provider lifecycle's page token (R1 G4b). A Provider value handed to the
// GUI loses its marker in JSON (life is not serialized); Token is that
// marker as an opaque string the page sends back with a save, a delete or
// a binding, and FromToken puts it back on the value the request names.
// It carries no authority: a write with it is checked against
// lifecycle.json under the commit lock, as any marked value's is, and a
// token that doesn't decode, or that names another id, is a value read
// with no lifecycle state (stale), never one taken as current. No token at
// all is the zero marker: written by id, as before lifecycle.json.

import (
	"encoding/base64"
	"encoding/json"
	"slices"

	"github.com/yetone/magpie/internal/filememo"
	"github.com/yetone/magpie/internal/lifecycle"
)

// pageToken is a marker as the page carries it. Kind is a letter, not
// lifeKind's number, so a page kept open across an update still decodes.
type pageToken struct {
	V    int    `json:"v"`
	Kind string `json:"k"` // "m" managed, "u" unreconciled, "x" unknown
	ID   string `json:"id"`
	E    string `json:"e,omitempty"`
	I    string `json:"i,omitempty"`
	R    uint64 `json:"r,omitempty"`
	Row  string `json:"w,omitempty"`
}

const pageTokenV = 1

// Token is p's marker for the page: "" for a value with none (legacy, or
// never saved), which the page sends back as none.
func (p Provider) Token() string {
	t := pageToken{V: pageTokenV, ID: p.life.issuedID, E: p.life.epoch, I: p.life.instance, R: p.life.rev, Row: p.life.row}
	switch p.life.kind {
	case lifeManaged:
		t.Kind = "m"
	case lifeUnreconciled:
		t.Kind = "u"
	case lifeUnknown:
		t.Kind = "x"
	default:
		return ""
	}
	b, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// FromToken is p carrying the marker rev, a Token of the provider the page
// read under p.ID. "" is the zero marker. One that doesn't decode, or that
// was issued for another id, is unknown: every checked write of it is
// refused as stale.
func FromToken(p Provider, rev string) Provider {
	p.life = lifeOfToken(p.ID, rev)
	return p
}

func lifeOfToken(id, rev string) life {
	if rev == "" {
		return life{}
	}
	unknown := life{kind: lifeUnknown, issuedID: id}
	b, err := base64.RawURLEncoding.DecodeString(rev)
	if err != nil {
		return unknown
	}
	var t pageToken
	if json.Unmarshal(b, &t) != nil || t.V != pageTokenV || t.ID != id {
		return unknown
	}
	switch t.Kind {
	case "m":
		if t.E == "" || t.I == "" {
			return unknown
		}
		return life{kind: lifeManaged, epoch: t.E, instance: t.I, issuedID: id, rev: t.R, row: t.Row}
	case "u":
		if t.E == "" {
			return unknown
		}
		return life{kind: lifeUnreconciled, epoch: t.E, issuedID: id, row: t.Row}
	}
	return unknown
}

// AddChecked is Add, and the provider as its write left it, with this
// commit's marker: a step after it (AddKeys) is checked against the one
// added, not whatever holds its id by then.
func AddChecked(p Provider) (Provider, error) {
	var snap Provider
	_, err := add(p, true, func(o *writeOpts) { o.snap = &snap })
	return snap, err
}

// AddCopyChecked is AddCopy, answered as AddChecked is.
func AddCopyChecked(p Provider, from string) (Provider, error) {
	var snap Provider
	_, err := AddCopy(p, from, func(o *writeOpts) { o.snap = &snap })
	return snap, err
}

// SameInstance says whether q, read later, is the provider p was: the same
// id, and with lifecycle.json the same instance under the same epoch. A
// zero p is any provider under its id, as before lifecycle.json.
func (p Provider) SameInstance(q Provider) bool {
	if p.ID != q.ID {
		return false
	}
	switch p.life.kind {
	case lifeLegacy:
		return true
	case lifeManaged:
		return q.life.kind == lifeManaged && q.life.epoch == p.life.epoch && q.life.instance == p.life.instance
	}
	return false
}

// CheckCurrent refuses (StaleError) a step after a committed change, one
// that writes something other than providers.json by p's id (settings.json's
// model names, reply limits), once another provider holds p's id: deleted
// and made again, or renamed away. It reads under lifecycle's lock and
// writes nothing. A zero p passes, as before lifecycle.json; a transient
// one is not saved.
func CheckCurrent(p Provider) error {
	switch p.life.kind {
	case lifeLegacy:
		return nil
	case lifeTransient:
		return errTransient
	}
	notUnderCommit("CheckCurrent")
	unlock, err := lockCommit()
	if err != nil {
		return err
	}
	err = currentHeld(p.life, unlock)
	Changed() // a journal finished on the way wrote providers.json
	return err
}

func currentHeld(l life, unlock func()) error {
	defer unlock()
	defer filememo.Forget()
	return currentLocked(l)
}

// currentLocked is CheckCurrent's, under lifecycle's lock: lifecycle.json
// and providers.json as they are, nothing that lists providers.
func currentLocked(l life) error {
	if err := lifecycle.Recover(); err != nil {
		return err
	}
	meta, err := lifecycle.ReadMeta()
	if err != nil {
		return err
	}
	stale := &StaleError{ID: l.issuedID}
	if l.kind == lifeUnknown || meta == nil || meta.Epoch != l.epoch {
		return stale
	}
	cur, err := read()
	if err != nil {
		return err
	}
	if err := unsettled(cur); err != nil {
		return err
	}
	if !slices.ContainsFunc(cur.Providers, func(q Provider) bool { return q.ID == l.issuedID }) {
		return stale // gone from the file, whatever lifecycle.json says yet
	}
	rec, known := meta.Providers[l.issuedID]
	switch {
	case l.kind == lifeManaged && (!known || rec.Instance != l.instance):
		return stale
	case l.kind == lifeUnreconciled && known:
		return stale // adopted meanwhile: an instance of its own
	}
	return nil
}
