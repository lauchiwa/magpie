package identity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
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

// file is the store as it sits on disk.
type file struct {
	Profiles []Profile `json:"profiles,omitempty"`
	// Bound maps a provider id to a profile id.
	Bound map[string]string `json:"bound,omitempty"`
}

var mu sync.Mutex

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

// read loads the store. A file that can't be parsed is reported rather
// than silently replaced: it holds what the operator confirmed.
func read() (file, error) {
	b, err := steady.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return file{}, nil
		}
		return file{}, fmt.Errorf("read %s: %w", Path(), err)
	}
	if len(b) == 0 {
		return file{}, nil
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return file{}, fmt.Errorf("read %s: %w", Path(), err)
	}
	return f, nil
}

func write(f file) error {
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
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return err
	}
	// 0o600: a fingerprint is not a secret, but it describes the user's
	// machine, and the file sits beside providers.json.
	return steady.WriteFile(Path(), append(b, '\n'), 0o600)
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

// Profiles are the stored profiles, oldest first.
func Profiles() ([]Profile, error) {
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(f.Profiles))
	for _, p := range f.Profiles {
		out = append(out, p.Clone())
	}
	return out, nil
}

// Bindings are the provider-to-profile bindings.
func Bindings() (map[string]string, error) {
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return nil, err
	}
	return maps.Clone(f.Bound), nil
}

// Add stores a profile. It is validated first, so the file never holds a
// snapshot that could not be sent.
func Add(p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(f.Profiles, func(q Profile) bool { return q.ID == p.ID }) {
		return fmt.Errorf("identity profile %q exists already", p.ID)
	}
	f.Profiles = append(f.Profiles, p.Clone())
	return write(f)
}

// Confirm marks a profile usable, at now. Confirming is the operator
// action that lets a request present it; an imported candidate sends
// nothing until this has happened. Confirming twice keeps the first time.
func Confirm(id string, now time.Time) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(f.Profiles, func(p Profile) bool { return p.ID == id })
	if i < 0 {
		return fmt.Errorf("no identity profile %q", id)
	}
	if f.Profiles[i].ConfirmedAt != nil {
		return nil
	}
	if err := f.Profiles[i].Validate(); err != nil {
		return err
	}
	f.Profiles[i].ConfirmedAt = &now
	if err := f.Profiles[i].Validate(); err != nil {
		return err
	}
	return write(f)
}

// Remove deletes a profile and every binding to it, so no provider is
// left bound to an id that is gone.
func Remove(id string) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return err
	}
	before := len(f.Profiles)
	f.Profiles = slices.DeleteFunc(f.Profiles, func(p Profile) bool { return p.ID == id })
	if len(f.Profiles) == before {
		return fmt.Errorf("no identity profile %q", id)
	}
	for prov, bound := range f.Bound {
		if bound == id {
			delete(f.Bound, prov)
		}
	}
	return write(f)
}

// Bind has the provider present the profile. A profile must exist and be
// confirmed before it can be bound: binding an unconfirmed one would hide
// the operator's confirmation step behind a later failure.
func Bind(providerID, profileID string) error {
	if providerID == "" {
		return fmt.Errorf("a binding needs a provider")
	}
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(f.Profiles, func(p Profile) bool { return p.ID == profileID })
	if i < 0 {
		return fmt.Errorf("no identity profile %q", profileID)
	}
	if !f.Profiles[i].Usable() {
		return fmt.Errorf("identity profile %q is not confirmed yet", profileID)
	}
	if f.Bound == nil {
		f.Bound = map[string]string{}
	}
	f.Bound[providerID] = profileID
	return write(f)
}

// Unbind stops the provider presenting a profile.
func Unbind(providerID string) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := read()
	if err != nil {
		return err
	}
	if _, ok := f.Bound[providerID]; !ok {
		return nil
	}
	delete(f.Bound, providerID)
	return write(f)
}

// Resolve is the profile the provider presents. found is false when it is
// bound to none, which is the ordinary case and no error.
//
// A binding whose profile is missing or unconfirmed is an error, not an
// omission: forwarding without the identity the operator pinned would look
// like success while presenting the wrong client to the vendor.
func Resolve(providerID string) (p Profile, found bool, err error) {
	if providerID == "" {
		return Profile{}, false, nil
	}
	mu.Lock()
	defer mu.Unlock()
	f, rerr := read()
	if rerr != nil {
		return Profile{}, false, rerr
	}
	id, ok := f.Bound[providerID]
	if !ok || id == "" {
		return Profile{}, false, nil
	}
	i := slices.IndexFunc(f.Profiles, func(q Profile) bool { return q.ID == id })
	if i < 0 {
		return Profile{}, false, fmt.Errorf("identity profile %q is bound to %q but is not stored", id, providerID)
	}
	got := f.Profiles[i]
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
func HeaderFor(providerID string) (http.Header, error) {
	p, found, err := Resolve(providerID)
	if err != nil || !found {
		return nil, err
	}
	return p.Fingerprint.Header(p.Client), nil
}
