package identity

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/steady"
)

// The store keeps profiles and their bindings in identities.json beside
// providers.json, so nothing is written into providers.json and an upstream
// change to that file's shape cannot collide with this one.
//
// A binding is a provider id and a profile id. The profile is the source of
// truth for what is sent; the binding only says which provider presents it.
// A binding to a profile that is missing or unconfirmed is kept rather than
// dropped, and HeaderFor fails on it: see Resolve.
//
// Every write takes lifecycle's lock across its whole read-modify-write,
// then identity's mutex (lifecycle → mu), so a provider commit moving
// bindings and a second magpie changing them never lose each other's
// change. Identity writes do not advance lifecycle's generation: a reader
// stays consistent by taking one atomic read of this file (ReadSnapshot).

// file is the store as it sits on disk.
type file struct {
	Profiles []Profile `json:"profiles,omitempty"`
	// Bound maps a provider id to a profile id.
	Bound map[string]string `json:"bound,omitempty"`
}

var mu sync.Mutex

// readHook runs at the start of every read, under mu; a test counts reads
// or widens the read-modify-write window with it. Nil in production.
var readHook func()

// lockWait, when set, bounds the wait for lifecycle's lock below its own
// limit; a test shortens it. Zero leaves lifecycle's own bound.
var lockWait time.Duration

// Path is where the store is kept.
func Path() string { return filepath.Join(appdir.Config(), "identities.json") }

// NewID is a profile id. A replacement snapshot gets a new one, so a
// profile that was reviewed is never edited underneath the review.
func NewID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("identity profile id: %w", err)
	}
	return "identity_" + hex.EncodeToString(b[:]), nil
}

// read loads the store. A file that can't be parsed is unknown, never
// empty: it holds what the operator confirmed, and which providers are
// bound. One a crash left all zero or cut short (#1505) is read from its
// last good generation (lastgood), which write keeps before each change.
// With no good generation either, the error names the file and leaves it
// as it is. A valid file is read alone: its .bak is never looked at.
//
// Every request Sign gives auth to reads the store (HeaderFor), bound or
// not, so an unreadable store fails every plain provider's requests: with
// the bindings unknown, sending under magpie's own User-Agent could be
// exactly what a bound provider must never do.
func read() (file, error) {
	if readHook != nil {
		readHook()
	}
	b, err := steady.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return file{}, nil
	}
	if err != nil {
		return file{}, fmt.Errorf("read %s: %w", Path(), err)
	}
	f, err := decode(b)
	if err == nil {
		return f, nil
	}
	bak, ferr := lastgood.Fallback(Path(), b, validStore)
	if ferr != nil {
		if len(bytes.TrimSpace(b)) == 0 {
			// An empty file with nothing kept before it, as one made by
			// hand: no profiles, as it has always been read. A .bak that is
			// there but can't be read means the store held something.
			if berr := noBackup(); berr != nil {
				return file{}, fmt.Errorf("%s is empty and its last good generation %s can't be read (%v), so which providers present an identity profile is unknown; magpie left both unchanged — fix the backup, or move it aside to read the empty file as no profiles", Path(), lastgood.Bak(Path()), berr)
			}
			return file{}, nil
		}
		return file{}, fmt.Errorf("%s can't be read (%v) and has no good backup, so which providers present an identity profile is unknown; magpie left it unchanged — fix it, or move it aside to bind none", Path(), err)
	}
	f, err = decode(bak)
	if err != nil {
		return file{}, fmt.Errorf("read %s: %w", lastgood.Bak(Path()), err)
	}
	return f, nil
}

// noBackup is nil when identities.json has no .bak at all, and why one
// that is there can't be read otherwise. Fallback has just found it not
// valid (or unreadable), so anything there is an error: invalid bytes, a
// directory, a link to nothing.
func noBackup() error {
	bak := lastgood.Bak(Path())
	if fi, err := steady.Stat(bak); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("it is %s, not a file", kind(fi))
		}
		if b, err := steady.ReadThere(bak); err != nil {
			return err
		} else if !validStore(b) {
			return errors.New(describeBad(b))
		}
		// valid now: written since Fallback looked, so unknown this time
		return errors.New("it changed while it was read")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Stat follows a link: one that names nothing is still a .bak there
	if fi, err := os.Lstat(bak); err == nil {
		return fmt.Errorf("it is %s, not a file", kind(fi))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func describeBad(b []byte) string {
	if len(bytes.Trim(b, "\x00")) == 0 && len(b) > 0 {
		return "every byte is zero"
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return "it is empty"
	}
	return "it doesn't parse"
}

// decode is identities.json's contents read as the store, the one decoder
// read and PlanBound share.
func decode(b []byte) (file, error) {
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return file{}, err
	}
	return f, nil
}

// validStore is identities.json's contents read as the store.
func validStore(b []byte) bool {
	_, err := decode(b)
	return err == nil
}

// encode is the store as write puts it on disk, the one encoder write and
// PlanBound share: profiles oldest first, no empty bound map, a final
// newline.
func encode(f file) ([]byte, error) {
	f.Profiles = slices.Clone(f.Profiles)
	slices.SortFunc(f.Profiles, func(a, b Profile) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return cmpString(a.ID, b.ID)
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	if len(f.Bound) == 0 {
		f.Bound = nil
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func write(f file) error {
	b, err := encode(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return err
	}
	if err := usableBackup(); err != nil {
		return err
	}
	// the file there now is the last good generation read falls back to;
	// one that isn't valid is copied aside before it is written over
	if err := lastgood.Keep(Path(), validStore, 0o600); err != nil {
		return err
	}
	// 0o600: a fingerprint is not a secret, but it describes the user's
	// machine, and the file sits beside providers.json.
	return steady.WriteFile(Path(), b, 0o600)
}

// usableBackup refuses a write when identities.json.bak is there but is
// not a plain file. Keep would follow a link and write the last good
// generation somewhere else, or fail on a directory after nothing was
// kept, and the generation recovery reads back would be lost. lifecycle's
// Prepare refuses the same layouts, so a provider commit and an identity
// write agree.
func usableBackup() error {
	bak := lastgood.Bak(Path())
	fi, err := os.Lstat(bak)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("identity store not changed: %s can't be checked: %w", bak, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("identity store not changed: %s is %s, not a file, so the last good generation can't be kept; move it aside and try again", bak, kind(fi))
	}
	return nil
}

// kind names what a path that isn't a plain file is, for an error.
func kind(fi fs.FileInfo) string {
	switch t := fi.Mode().Type(); {
	case t&fs.ModeSymlink != 0:
		return "a link"
	case t.IsDir():
		return "a folder"
	}
	return "a special file"
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// change runs one write's read-modify-write. It takes lifecycle's lock
// first and never goes on without it, finishes a journal a dead writer
// left, then takes mu and reads the store as it is now. modify reports
// whether it changed f; an unchanged store is not written.
func change(modify func(f *file) (bool, error)) error {
	ctx := context.Background()
	if lockWait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lockWait)
		defer cancel()
	}
	unlock, err := lifecycle.Lock(ctx)
	if err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	defer unlock()
	if err := lifecycle.Recover(); err != nil {
		return fmt.Errorf("identity store not changed: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return err
	}
	changed, err := modify(&f)
	if err != nil || !changed {
		return err
	}
	return write(f)
}

// Snapshot is the store as one read found it. Its fields are never handed
// out: every method gives a copy, so what one caller does with a result
// can't change another's, and nothing reads the file again. A signer or a
// view that takes all it needs from one Snapshot can't mix two generations.
type Snapshot struct {
	profiles []Profile
	bound    map[string]string
}

// ReadSnapshot reads the store once, with read's own choice of source.
func ReadSnapshot() (Snapshot, error) {
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return Snapshot{}, err
	}
	// read decoded these afresh, so the snapshot owns them
	return Snapshot{profiles: f.Profiles, bound: f.Bound}, nil
}

// Profiles are the stored profiles, oldest first.
func (s Snapshot) Profiles() []Profile {
	out := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, p.Clone())
	}
	return out
}

// Bindings are the provider-to-profile bindings.
func (s Snapshot) Bindings() map[string]string { return maps.Clone(s.bound) }

// Resolve is the profile the provider presents. found is false when it is
// bound to none, which is the ordinary case and no error.
//
// A binding whose profile is missing or unconfirmed is an error, not an
// omission: forwarding without the identity the operator pinned would look
// like success while presenting the wrong client to the vendor.
func (s Snapshot) Resolve(providerID string) (p Profile, found bool, err error) {
	if providerID == "" {
		return Profile{}, false, nil
	}
	id, ok := s.bound[providerID]
	if !ok || id == "" {
		return Profile{}, false, nil
	}
	i := slices.IndexFunc(s.profiles, func(q Profile) bool { return q.ID == id })
	if i < 0 {
		return Profile{}, false, fmt.Errorf("identity profile %q is bound to %q but is not stored", id, providerID)
	}
	got := s.profiles[i]
	if got.ConfirmedAt == nil {
		return Profile{}, false, fmt.Errorf("identity profile %q is bound to %q but is not confirmed", id, providerID)
	}
	if err := got.Validate(); err != nil {
		return Profile{}, false, fmt.Errorf("identity profile %q is bound to %q but is invalid: %w", id, providerID, err)
	}
	return got.Clone(), true, nil
}

// HeaderFor is the headers the provider's bound profile presents, nil when
// it is bound to none. An error here fails the request rather than sending
// it under magpie's own User-Agent.
func (s Snapshot) HeaderFor(providerID string) (http.Header, error) {
	p, found, err := s.Resolve(providerID)
	if err != nil || !found {
		return nil, err
	}
	return p.Fingerprint.Header(p.Client), nil
}

// Profiles are the stored profiles, oldest first.
func Profiles() ([]Profile, error) {
	s, err := ReadSnapshot()
	if err != nil {
		return nil, err
	}
	return s.Profiles(), nil
}

// Bindings are the provider-to-profile bindings.
func Bindings() (map[string]string, error) {
	s, err := ReadSnapshot()
	if err != nil {
		return nil, err
	}
	return s.Bindings(), nil
}

// Add stores a profile. It is validated first, so the file never holds a
// snapshot that could not be sent.
func Add(p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return change(func(f *file) (bool, error) {
		if slices.ContainsFunc(f.Profiles, func(q Profile) bool { return q.ID == p.ID }) {
			return false, fmt.Errorf("identity profile %q exists already", p.ID)
		}
		f.Profiles = append(f.Profiles, p.Clone())
		return true, nil
	})
}

// Confirm marks a profile usable, at now. Confirming is the operator
// action that lets a request present it; an imported candidate sends
// nothing until this has happened. Confirming twice keeps the first time.
func Confirm(id string, now time.Time) error {
	return change(func(f *file) (bool, error) {
		i := slices.IndexFunc(f.Profiles, func(p Profile) bool { return p.ID == id })
		if i < 0 {
			return false, fmt.Errorf("no identity profile %q", id)
		}
		if f.Profiles[i].ConfirmedAt != nil {
			return false, nil
		}
		if err := f.Profiles[i].Validate(); err != nil {
			return false, err
		}
		f.Profiles[i].ConfirmedAt = &now
		if err := f.Profiles[i].Validate(); err != nil {
			return false, err
		}
		return true, nil
	})
}

// Remove deletes a profile and every binding to it, so no provider is
// left bound to an id that is gone.
func Remove(id string) error {
	return change(func(f *file) (bool, error) {
		before := len(f.Profiles)
		f.Profiles = slices.DeleteFunc(f.Profiles, func(p Profile) bool { return p.ID == id })
		if len(f.Profiles) == before {
			return false, fmt.Errorf("no identity profile %q", id)
		}
		for prov, bound := range f.Bound {
			if bound == id {
				delete(f.Bound, prov)
			}
		}
		return true, nil
	})
}

// Bind has the provider present the profile. A profile must exist and be
// confirmed before it can be bound: binding an unconfirmed one would hide
// the operator's confirmation step behind a later failure.
func Bind(providerID, profileID string) error {
	if providerID == "" {
		return fmt.Errorf("a binding needs a provider")
	}
	return change(func(f *file) (bool, error) {
		i := slices.IndexFunc(f.Profiles, func(p Profile) bool { return p.ID == profileID })
		if i < 0 {
			return false, fmt.Errorf("no identity profile %q", profileID)
		}
		if !f.Profiles[i].Usable() {
			return false, fmt.Errorf("identity profile %q is not confirmed yet", profileID)
		}
		if f.Bound == nil {
			f.Bound = map[string]string{}
		}
		f.Bound[providerID] = profileID
		return true, nil
	})
}

// Unbind stops the provider presenting a profile.
func Unbind(providerID string) error {
	return change(func(f *file) (bool, error) {
		if _, ok := f.Bound[providerID]; !ok {
			return false, nil
		}
		delete(f.Bound, providerID)
		return true, nil
	})
}

// Resolve is the profile the provider presents, from one read of the
// store (Snapshot.Resolve).
func Resolve(providerID string) (p Profile, found bool, err error) {
	if providerID == "" {
		return Profile{}, false, nil
	}
	s, err := ReadSnapshot()
	if err != nil {
		return Profile{}, false, err
	}
	return s.Resolve(providerID)
}

// HeaderFor is the headers the provider's bound profile presents, from one
// read of the store (Snapshot.HeaderFor).
func HeaderFor(providerID string) (http.Header, error) {
	if providerID == "" {
		return nil, nil
	}
	s, err := ReadSnapshot()
	if err != nil {
		return nil, err
	}
	return s.HeaderFor(providerID)
}

// ValidStore is identities.json's own save validator, for a provider
// commit that writes the file through lifecycle's journal.
func ValidStore(b []byte) bool { return validStore(b) }

// BoundIn is the provider ids bound in identities.json's bytes cur, nil
// when the file is absent: for a provider commit to record whether a
// deleted provider was bound. Bytes that are not a valid store are an
// error, as in PlanBound.
func BoundIn(cur []byte) (map[string]bool, error) {
	if cur == nil {
		return nil, nil
	}
	f, err := decode(cur)
	if err != nil {
		return nil, fmt.Errorf("%s can't be read (%v), so which providers present an identity profile is unknown; nothing was changed — fix it, or move it aside, and try again", Path(), err)
	}
	out := make(map[string]bool, len(f.Bound))
	for id, profile := range f.Bound {
		if profile != "" {
			out[id] = true
		}
	}
	return out, nil
}

// PlanBound is identities.json with bindings moved and dropped, for a
// provider commit to write in its journal: it reads and writes nothing.
// cur is the file's bytes, nil when the file is absent. moves maps an old
// provider id to its new one, all at once (a→b with b→c moves both); a
// move replaces any orphan binding at its target. drops are ids whose
// binding goes, after the moves; one that is not bound is no change.
// Bindings move as they are, profile id and all: one that is broken stays
// broken under its new id and still fails closed.
//
// cur that isn't a valid store, an empty file included, is an error: read
// would fall back to its .bak there, which PlanBound can't see, and a plan
// built over it could lose or misplace a binding. after is the encoding
// write would put on disk; with changed false it is nil and nothing needs
// writing.
func PlanBound(cur []byte, moves map[string]string, drops []string) (after []byte, changed bool, err error) {
	var f file
	if cur != nil {
		if f, err = decode(cur); err != nil {
			return nil, false, fmt.Errorf("%s can't be read (%v), so which providers present an identity profile is unknown; no binding was moved — fix it, or move it aside, and try again", Path(), err)
		}
	}
	targets := make(map[string]string, len(moves))
	for from, to := range moves {
		if from == "" || to == "" {
			return nil, false, fmt.Errorf("identity binding move %q → %q needs both provider ids", from, to)
		}
		if other, ok := targets[to]; ok {
			return nil, false, fmt.Errorf("identity bindings of %q and %q can't both move to %q", other, from, to)
		}
		targets[to] = from
	}
	for _, id := range drops {
		if from, ok := targets[id]; ok {
			return nil, false, fmt.Errorf("identity binding of %q can't move to %q and be dropped there", from, id)
		}
	}
	next := make(map[string]string, len(f.Bound))
	for id, profile := range f.Bound {
		if _, moving := moves[id]; moving {
			continue
		}
		if _, target := targets[id]; target {
			continue // an orphan the move replaces
		}
		next[id] = profile
	}
	for from, to := range moves {
		if profile, ok := f.Bound[from]; ok {
			next[to] = profile
		}
	}
	for _, id := range drops {
		delete(next, id)
	}
	if maps.Equal(next, f.Bound) {
		return nil, false, nil
	}
	f.Bound = next
	after, err = encode(f)
	if err != nil {
		return nil, false, err
	}
	return after, true, nil
}
