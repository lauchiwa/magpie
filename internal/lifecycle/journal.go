package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/steady"
)

// Image distinguishes a missing file from an empty, present file.
// The zero value is absence; use Present even for an empty file.
type Image struct {
	Exists bool   `json:"exists"`
	Bytes  []byte `json:"bytes"`
	Hash   string `json:"sha256"`
}

// Present copies bytes so a prepared image does not alias its caller's buffer.
func Present(b []byte) Image { return Image{true, bytes.Clone(b), Digest(b)} }
func (i Image) valid() bool {
	if !i.Exists {
		return len(i.Bytes) == 0 && i.Hash == ""
	}
	return hexSize(i.Hash, 32) && i.Hash == Digest(i.Bytes)
}
func (i Image) equal(j Image) bool {
	return i.Exists == j.Exists && i.Hash == j.Hash && bytes.Equal(i.Bytes, j.Bytes)
}

// Backup records the old backup and the exact result of Keep. Valid is the
// owning package's validator applied to Before, not a generic JSON guess.
// This is serialized: recovery after process exit needs no callback registry.
type Backup struct {
	Before Image `json:"before"`
	After  Image `json:"after"`
	Valid  bool  `json:"valid"`
}

// Participant may name only one of the three fixed basenames, never a path.
// A nil Backup is required for metadata, which has no lastgood fallback.
type Participant struct {
	Path string `json:"path"`
	// Target is the resolved, canonical file providers.json names (it may
	// be a link, as the owner's writePrivate keeps it). Only providers.json
	// has one; identities.json and metadata are never links.
	Target string  `json:"target,omitempty"`
	Before Image   `json:"before"`
	After  Image   `json:"after"`
	Backup *Backup `json:"backup,omitempty"`
}

// Journal is private on disk; never log or send its images to the GUI.
type Journal struct {
	Version      int           `json:"version"`
	ID           string        `json:"id"`
	Participants []Participant `json:"participants"`
}

// Plan is prepared under Lock. Schema validation belongs to each file's owner.
// stage is a package-test seam, not a production callback under the lock.
type Plan struct {
	Participants []Participant
	stage        func(string) error
}

func rank(name string) int {
	switch name {
	case ProvidersFile:
		return 1
	case IdentitiesFile:
		return 2
	case MetaFile:
		return 3
	}
	return 0
}

// ReadImage reads a primary without fallback. Only the fixed participants are
// accepted. providers.json is read through its link (edit.Target), unless
// it resolves onto another lifecycle file; the others refuse links.
func ReadImage(name string) (Image, error) {
	if rank(name) == 0 {
		return Image{}, fmt.Errorf("%w: invalid participant", ErrUnknown)
	}
	if name == ProvidersFile {
		_, image, err := providersImage()
		return image, err
	}
	if name == MetaFile {
		return readMeta()
	}
	return readImage(filepath.Join(appdir.Config(), name))
}

// lstat is a test seam for Windows' momentary absence during replacement.
var lstat = os.Lstat

// waitReplace is steady's bounded wait for a file being replaced; a seam
// so tests can count the waits a read takes.
var waitReplace = steady.Stat

// readJournal reads the journal. It is only ever created where none is
// (Commit refuses a pending one) and removed, never replaced, so not being
// there is never a replace under way and is not waited out: waiting cost
// every Begin about 20ms on Windows for the journal that is almost never
// there.
func readJournal() (Image, error) {
	path := JournalPath()
	fi, err := lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Image{}, nil
	}
	if err != nil {
		return readImage(path) // denied: the bounded wait of a replace
	}
	return readFound(path, fi)
}

// readMeta reads lifecycle.json. Only a journal's commit or recovery
// replaces it, and the journal is there throughout, so the wait for a
// replace under way is taken only when one is: lifecycle.json not there
// with no journal is checked once more and is then absent, at no wait (a
// legacy config, and every test home, has none). A commit wholly between
// those checks has written the file, which the second check then finds.
func readMeta() (Image, error) {
	path := filepath.Join(appdir.Config(), MetaFile)
	for range 2 {
		fi, err := lstat(path)
		if err == nil {
			return readFound(path, fi)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return readImage(path)
		}
		if _, err := lstat(JournalPath()); !errors.Is(err, fs.ErrNotExist) {
			return readImage(path) // a commit may be replacing it: wait
		}
	}
	return Image{}, nil
}

func readImage(path string) (Image, error) {
	fi, err := lstat(path)
	if err != nil && runtime.GOOS == "windows" {
		// Reuse steady's bounded wait for replace-time absence/denial, then
		// inspect the link itself again; Stat alone would follow symlinks.
		if _, waitErr := waitReplace(path); waitErr != nil {
			err = waitErr
		} else {
			fi, err = lstat(path)
			if err != nil {
				// Stat just observed it. A second replace cannot mean absence.
				return Image{}, fmt.Errorf("%w: %s: %w", ErrUnknown, path, err)
			}
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return Image{}, nil
	}
	if err != nil {
		return Image{}, fmt.Errorf("%w: %s: %w", ErrUnknown, path, err)
	}
	return readFound(path, fi)
}

// readFound reads the regular file path that lstat just found as fi.
func readFound(path string, fi fs.FileInfo) (Image, error) {
	if !fi.Mode().IsRegular() {
		return Image{}, fmt.Errorf("%w: %s is not a regular file", ErrUnknown, path)
	}
	b, err := steady.ReadThere(path)
	if err != nil {
		return Image{}, fmt.Errorf("%w: %s: %w", ErrUnknown, path, err)
	}
	return Present(b), nil
}

// Prepare freezes before/after and backup decisions. valid must be the
// owner's actual save validator (identity's empty-file read exception does
// not make an empty file a valid backup). It is not retained by the plan.
// After may be absent, though normal provider/identity writes replace a file.
func Prepare(name string, after Image, valid lastgood.Valid) (Participant, error) {
	if !after.valid() {
		return Participant{}, fmt.Errorf("%w: invalid after image", ErrUnknown)
	}
	var before Image
	var target string
	var err error
	if name == ProvidersFile {
		// A linked providers.json is replaced at its target, so never removed.
		if !after.Exists {
			return Participant{}, fmt.Errorf("%w: providers.json cannot be removed by a commit", ErrUnknown)
		}
		target, before, err = providersImage()
	} else {
		before, err = ReadImage(name)
	}
	if err != nil {
		return Participant{}, err
	}
	p := Participant{Path: name, Target: target, Before: before, After: Present(after.Bytes)}
	if !after.Exists {
		p.After = Image{}
	}
	if name == MetaFile {
		if !after.Exists {
			return Participant{}, fmt.Errorf("%w: metadata cannot be removed by a commit", ErrUnknown)
		}
		if _, err := DecodeMeta(after.Bytes); err != nil {
			return Participant{}, err
		}
	} else {
		if valid == nil {
			return Participant{}, fmt.Errorf("%w: missing owner validator", ErrUnknown)
		}
		// Empty identity files are a supported input, not generic invalid JSON.
		if after.Exists && !valid(after.Bytes) && !(name == IdentitiesFile && len(bytes.TrimSpace(after.Bytes)) == 0) {
			return Participant{}, fmt.Errorf("%w: invalid after image for %s", ErrUnknown, name)
		}
		bak, err := readImage(lastgood.Bak(filepath.Join(appdir.Config(), name)))
		if err != nil {
			return Participant{}, err
		}
		p.Backup = &Backup{Before: bak, After: bak, Valid: before.Exists && valid(before.Bytes)}
		if p.Backup.Valid {
			p.Backup.After = before
		}
	}
	return p, nil
}

func (j Journal) validate() error {
	bad := func() error { return fmt.Errorf("%w: invalid lifecycle journal", ErrUnknown) }
	if j.Version != Version || !hexSize(j.ID, 16) || len(j.Participants) == 0 || len(j.Participants) > 3 {
		return bad()
	}
	order := 0
	var providers *Participant
	for _, p := range j.Participants {
		n := rank(p.Path)
		if n == 0 || n <= order || !p.Before.valid() || !p.After.valid() || p.Before.equal(p.After) {
			return bad()
		}
		order = n
		if (p.Path == ProvidersFile) != (p.Target != "") {
			return bad()
		}
		if p.Path == ProvidersFile {
			if !filepath.IsAbs(p.Target) || filepath.Clean(p.Target) != p.Target || !p.After.Exists {
				return bad()
			}
			q := p
			providers = &q
		}
		if p.Path != MetaFile {
			b := p.Backup
			if b == nil || !b.Before.valid() || !b.After.valid() || (!p.Before.Exists && b.Valid) {
				return bad()
			}
			expected := b.Before
			if b.Valid {
				expected = p.Before
			}
			if !expected.equal(b.After) {
				return bad()
			}
			continue
		}
		if p.Backup != nil || !p.After.Exists {
			return bad()
		}
		after, err := DecodeMeta(p.After.Bytes)
		if err != nil {
			return bad()
		}
		if p.Before.Exists {
			var format struct {
				Version int `json:"version"`
			}
			if json.Unmarshal(p.Before.Bytes, &format) == nil && format.Version != 0 && format.Version != Version {
				return bad()
			}
			before, err := DecodeMeta(p.Before.Bytes)
			if err == nil {
				if before.Gen == ^uint64(0) || after.Gen != before.Gen+1 || after.Epoch != before.Epoch {
					return bad()
				}
			} else if after.Gen != 1 {
				return bad()
			}
		} else if after.Gen != 1 {
			return bad()
		}
		if providers != nil && after.Digest != Digest(providers.After.Bytes) {
			return bad()
		}
	}
	return nil
}

// EncodeJournal checks paths, ordering, hashes and generations before writing.
func EncodeJournal(j Journal) ([]byte, error) {
	if err := j.validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(j, "", "  ")
}

// DecodeJournal accepts only a complete, verifiable intent; no fallback.
func DecodeJournal(b []byte) (Journal, error) {
	var j Journal
	if err := decode(b, &j); err != nil || !journalFields(b) {
		return Journal{}, fmt.Errorf("%w: invalid lifecycle journal", ErrUnknown)
	}
	if err := j.validate(); err != nil {
		return Journal{}, err
	}
	return j, nil
}

func imageFields(b []byte) bool {
	fields, ok := required(b, "exists", "bytes", "sha256")
	if !ok {
		return false
	}
	var hash string
	if bytes.Equal(fields["sha256"], []byte("null")) || json.Unmarshal(fields["sha256"], &hash) != nil {
		return false
	}
	return string(fields["exists"]) == "true" || string(fields["exists"]) == "false"
}
func journalFields(b []byte) bool {
	fields, ok := required(b, "version", "id", "participants")
	if !ok {
		return false
	}
	var ps []json.RawMessage
	if json.Unmarshal(fields["participants"], &ps) != nil {
		return false
	}
	for _, raw := range ps {
		p, ok := required(raw, "path", "before", "after")
		if !ok || !imageFields(p["before"]) || !imageFields(p["after"]) {
			return false
		}
		if raw, ok := p["backup"]; ok {
			backup, ok := required(raw, "before", "after", "valid")
			if !ok || !imageFields(backup["before"]) || !imageFields(backup["after"]) {
				return false
			}
			if string(backup["valid"]) != "true" && string(backup["valid"]) != "false" {
				return false
			}
		}
	}
	return true
}

// Commit publishes the intent before changing files, and metadata last. The
// caller holds Lock. On failure the journal stays for Recover; no blind rollback.
func Commit(p Plan) error {
	existing, err := readJournal()
	if err != nil {
		return err
	}
	if existing.Exists {
		return fmt.Errorf("%w: recover %s before another commit", ErrBusy, JournalPath())
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	j := Journal{Version: Version, ID: id, Participants: p.Participants}
	b, err := EncodeJournal(j)
	if err != nil {
		return err
	}
	// Freeze slices/maps from the plan before executing any test seam.
	j, err = DecodeJournal(b)
	if err != nil {
		return err
	}
	if err := check(j, true); err != nil {
		return err
	}
	if err := steady.WriteFile(JournalPath(), b, 0600); err != nil {
		return err
	}
	if err := at(p.stage, "journal:write"); err != nil {
		return err
	}
	return replay(j, p.stage)
}

// Recover deterministically finishes a journal while the caller holds Lock.
// Already-after participants are skipped, including Keep, so the backup stays
// one generation behind even after repeated recovery or a crash during Keep.
func Recover() error {
	image, err := readJournal()
	if err != nil || !image.Exists {
		return err
	}
	j, err := DecodeJournal(image.Bytes)
	if err != nil {
		return fmt.Errorf("%s: %w", JournalPath(), err)
	}
	return replay(j, nil)
}
func at(stage func(string) error, name string) error {
	if stage == nil {
		return nil
	}
	if err := stage(name); err != nil {
		return fmt.Errorf("lifecycle %s: %w", name, err)
	}
	return nil
}
func ambiguous(path string, cur Image) error {
	err := error(&AmbiguousError{Journal: JournalPath()})
	if cur.Exists {
		if _, e := lastgood.KeepBad(path, cur.Bytes); e != nil {
			err = errors.Join(err, e)
		}
	}
	return err
}

// check verifies ALL primaries and backups before changing ANY of them. A
// third state later in the list must not leave earlier files partly replayed.
func check(j Journal, initial bool) error {
	hasMeta := false
	waiting := false
	for _, p := range j.Participants {
		path := filepath.Join(appdir.Config(), p.Path)
		cur, err := current(p)
		if err != nil {
			return err
		}
		if !cur.equal(p.Before) && (initial || !cur.equal(p.After)) {
			return ambiguous(path, cur)
		}
		if !initial {
			if cur.equal(p.After) && waiting {
				return ambiguous(path, cur)
			}
			waiting = waiting || cur.equal(p.Before)
		}
		if p.Path == MetaFile {
			hasMeta = true
			// A metadata-only bootstrap/reconciliation still names the exact
			// provider bytes it describes, not an unchecked caller digest.
			hasProviders := false
			for _, q := range j.Participants {
				hasProviders = hasProviders || q.Path == ProvidersFile
			}
			if !hasProviders {
				providers, err := ReadImage(ProvidersFile)
				if err != nil {
					return err
				}
				m, err := DecodeMeta(p.After.Bytes)
				if err != nil {
					return err
				}
				if m.Digest != Digest(providers.Bytes) {
					return ambiguous(filepath.Join(appdir.Config(), ProvidersFile), providers)
				}
			}
			continue
		}
		bak, err := readImage(lastgood.Bak(path))
		if err != nil {
			return err
		}
		if initial {
			if !bak.equal(p.Backup.Before) {
				return ambiguous(lastgood.Bak(path), bak)
			}
		} else if cur.equal(p.After) {
			if !bak.equal(p.Backup.After) {
				return ambiguous(lastgood.Bak(path), bak)
			}
		} else if !bak.equal(p.Backup.Before) && !bak.equal(p.Backup.After) {
			return ambiguous(lastgood.Bak(path), bak)
		}
	}
	// Managed commits must advance generation even if only identities changed.
	// Legacy (no metadata) writes remain possible without bootstrapping a store.
	if !hasMeta {
		m, err := ReadImage(MetaFile)
		if err != nil {
			return err
		}
		if m.Exists {
			return fmt.Errorf("%w: managed commit needs metadata", ErrUnknown)
		}
	}
	return nil
}
func replay(j Journal, stage func(string) error) error {
	if err := sameJournal(j); err != nil {
		return err
	}
	if err := check(j, false); err != nil {
		return err
	}
	for _, p := range j.Participants {
		path := filepath.Join(appdir.Config(), p.Path)
		cur, err := current(p)
		if err != nil {
			return err
		}
		if cur.equal(p.After) {
			continue
		}
		if !cur.equal(p.Before) {
			return ambiguous(path, cur)
		}
		if p.Path == MetaFile {
			if err := at(stage, "meta:before"); err != nil {
				return err
			}
			// Invalid metadata may be replaced by explicit bootstrap, but is kept
			// as evidence, never as an alternate source of instance identifiers.
			if cur.Exists {
				if _, e := DecodeMeta(cur.Bytes); e != nil {
					if _, e = lastgood.KeepBad(path, cur.Bytes); e != nil {
						return e
					}
				}
			}
		} else {
			if err := at(stage, p.Path+":keep:before"); err != nil {
				return err
			}
			// Keep reads through the link; resolve it again first so it
			// keeps the bytes of the recorded target, then once more below.
			if cur, err = current(p); err != nil {
				return err
			}
			if !cur.equal(p.Before) {
				return ambiguous(path, cur)
			}
			b := p.Backup
			if err := keep(path, cur, b.Valid); err != nil {
				return err
			}
			bak, err := readImage(lastgood.Bak(path))
			if err != nil {
				return err
			}
			if !bak.equal(b.After) {
				return ambiguous(lastgood.Bak(path), bak)
			}
			if err := at(stage, p.Path+":keep:after"); err != nil {
				return err
			}
		}
		// Recheck after Keep: never overwrite an observed external third state.
		cur, err = current(p)
		if err != nil {
			return err
		}
		if !cur.equal(p.Before) {
			return ambiguous(path, cur)
		}
		if p.Target != "" {
			// The link stays; its target is written, as writePrivate does.
			if err = os.MkdirAll(filepath.Dir(p.Target), 0700); err == nil {
				err = steady.WriteFile(p.Target, p.After.Bytes, 0600)
			}
		} else if p.After.Exists {
			err = steady.WriteFile(path, p.After.Bytes, 0600)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			return fmt.Errorf("lifecycle write %s: %w", p.Path, err)
		}
		if err := at(stage, p.Path+":write"); err != nil {
			return err
		}
	}
	if err := check(j, false); err != nil {
		return err
	}
	if err := at(stage, "journal:remove:before"); err != nil {
		return err
	}
	if err := sameJournal(j); err != nil {
		return err
	}
	for _, p := range j.Participants {
		cur, err := current(p)
		if err != nil {
			return err
		}
		if !cur.equal(p.After) {
			return ambiguous(filepath.Join(appdir.Config(), p.Path), cur)
		}
	}
	if err := check(j, false); err != nil {
		return err
	}
	if err := os.Remove(JournalPath()); err != nil {
		return fmt.Errorf("lifecycle retire journal: %w", err)
	}
	return nil
}

func sameJournal(j Journal) error {
	cur, err := readJournal()
	if err != nil {
		return err
	}
	other, err := DecodeJournal(cur.Bytes)
	if err != nil {
		return ambiguous(JournalPath(), cur)
	}
	want, err := EncodeJournal(j)
	if err != nil {
		return err
	}
	got, err := EncodeJournal(other)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return ambiguous(JournalPath(), cur)
	}
	return nil
}

// keep is lastgood.Keep for bytes this replay just verified as the
// participant's before image (read through a link, for providers.json).
// Keep itself would read the logical name again, which a link may by then
// resolve elsewhere. The backup stays at the logical name, where
// lastgood.Fallback reads it; the verdict is the serialized owner's.
func keep(path string, before Image, valid bool) error {
	if !before.Exists {
		return nil
	}
	if !valid {
		_, err := lastgood.KeepBad(path, before.Bytes)
		return err
	}
	bak := lastgood.Bak(path)
	if old, err := os.ReadFile(bak); err == nil && bytes.Equal(old, before.Bytes) {
		return nil
	}
	return steady.WriteFile(bak, before.Bytes, 0600)
}
