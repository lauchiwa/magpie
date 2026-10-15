// Package lifecycle coordinates providers.json, identities.json and their
// instance metadata. Writers hold Lock across reading, planning and Commit
// (or Recover). Readers take one snapshot of their sources between Begin
// and Mark.Validate, the optimistic side of that protocol; the window is
// not a callback, so a collector cannot read an owner twice through it.
package lifecycle

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/yetone/magpie/internal/appdir"
)

var (
	// ErrBusy means the caller did not obtain an exclusive or consistent view.
	ErrBusy = errors.New("lifecycle busy; retry the operation")
	// ErrUnknown means state could not be verified; it must not mean unbound.
	ErrUnknown = errors.New("lifecycle state unknown")
)

// AmbiguousError preserves a journal whose files no longer match its intent.
// No file contents (which may include credentials) are included in the error.
type AmbiguousError struct{ Journal string }

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s: files differ from the recorded operation; keep the journal and reconcile the files", e.Journal)
}
func (e *AmbiguousError) Unwrap() error { return ErrUnknown }

const (
	Version        = 1 // metadata and journal format
	RetiredLimit   = 256
	ProvidersFile  = "providers.json"
	IdentitiesFile = "identities.json"
	MetaFile       = "lifecycle.json"
)

// Path is the instance metadata, never recovered from a lastgood generation.
func Path() string { return filepath.Join(appdir.Config(), MetaFile) }

// JournalPath is private: before and after images can contain credentials.
func JournalPath() string { return filepath.Join(appdir.Config(), "lifecycle-journal.json") }

// Digest includes every byte, including a BOM and trailing whitespace.
func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// NewID returns a random 128-bit epoch, instance or operation identifier.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("lifecycle id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
