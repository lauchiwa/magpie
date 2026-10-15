package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/edit"
)

// resolveTarget is edit.Target, the provider owner's own link resolution
// (writePrivate). Tests stand in for it where links need privileges.
var resolveTarget = edit.Target

// canonical makes two spellings of one location compare equal: the deepest
// existing ancestor is resolved and the rest appended, so a dangling link's
// target and the file later created there are the same string.
func canonical(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	p = filepath.Clean(p)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// equalPath compares canonical paths, case-insensitively where the default
// file systems are (Windows, macOS).
func equalPath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// reserved are the protocol's own files; providers.json may not resolve
// onto any of them, or a write to one would silently be a write to another.
var reserved = []string{IdentitiesFile, IdentitiesFile + ".bak", MetaFile, "lifecycle-journal.json", "lifecycle.lock", ProvidersFile + ".bak"}

// providersTarget resolves the file providers.json names. It is resolved
// again at every step of a commit; the journal records this result.
func providersTarget() (string, error) {
	cfg := appdir.Config()
	logical := filepath.Join(cfg, ProvidersFile)
	t, err := resolveTarget(logical)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrUnknown, logical, err)
	}
	t = canonical(t)
	linked := !equalPath(t, canonical(logical))
	var fi os.FileInfo
	if linked {
		fi, _ = os.Stat(t)
	}
	for _, name := range reserved {
		other := canonical(filepath.Join(cfg, name))
		alias := equalPath(t, other)
		// A linked target is also compared by file identity (a hard link).
		if !alias && fi != nil {
			if ofi, err := os.Stat(other); err == nil {
				alias = os.SameFile(fi, ofi)
			}
		}
		if alias {
			return "", fmt.Errorf("%w: %s resolves to %s, a lifecycle file", ErrUnknown, logical, t)
		}
	}
	return t, nil
}

// providersImage reads the resolved target. Its absence is an absent image,
// as the owner's reader takes a dangling link.
func providersImage() (string, Image, error) {
	t, err := providersTarget()
	if err != nil {
		return "", Image{}, err
	}
	image, err := readImage(t)
	return t, image, err
}

// current reads a participant's primary. providers.json is resolved again
// each time: a target other than the recorded one is a third state even
// when it holds identical bytes, and its bytes are kept as evidence.
func current(p Participant) (Image, error) {
	if p.Path != ProvidersFile {
		return ReadImage(p.Path)
	}
	t, image, err := providersImage()
	if err != nil {
		return Image{}, err
	}
	if !equalPath(t, p.Target) {
		logical := filepath.Join(appdir.Config(), ProvidersFile)
		return Image{}, fmt.Errorf("%s now resolves to %s, not %s as recorded: %w", logical, t, p.Target, ambiguous(logical, image))
	}
	return image, nil
}
