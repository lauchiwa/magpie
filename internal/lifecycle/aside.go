package lifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/steady"
)

// KeepAside copies the file name (one of the fixed participants, or the
// journal) to name.<why>-<time> before an explicit reconcile writes over
// it, so what it held stays readable. A file that is not there keeps
// nothing. The copy is private (0600): a journal holds credentials. It
// returns the copy's path, "" when nothing was kept.
func KeepAside(name, why string) (string, error) {
	var path string
	switch {
	case name == filepath.Base(JournalPath()):
		path = JournalPath()
	case rank(name) != 0:
		path = filepath.Join(filepath.Dir(Path()), name)
	default:
		return "", fmt.Errorf("%w: invalid participant", ErrUnknown)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s can't be kept aside: %w", path, err)
	}
	// the same bytes kept for the same reason before are kept already, in
	// whichever second that was: a reconcile run again a second later made
	// a second copy of the same file
	prefix := filepath.Base(path) + "." + why + "-"
	if es, err := os.ReadDir(filepath.Dir(path)); err == nil {
		for _, e := range es {
			if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), prefix) {
				continue
			}
			old := filepath.Join(filepath.Dir(path), e.Name())
			if ob, rerr := os.ReadFile(old); rerr == nil && bytes.Equal(ob, b) {
				return old, nil
			}
		}
	}
	stamp := path + "." + why + "-" + time.Now().Format("20060102-150405")
	for i := 1; i < 1000; i++ {
		aside := stamp
		if i > 1 {
			aside += "-" + strconv.Itoa(i)
		}
		f, err := os.OpenFile(aside, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			if old, rerr := os.ReadFile(aside); rerr == nil && bytes.Equal(old, b) {
				return aside, nil
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("%s can't be kept aside: %w", path, err)
		}
		_, err = f.Write(b)
		if err == nil {
			err = steady.Sync(f)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(aside)
			return "", fmt.Errorf("%s can't be kept aside: %w", path, err)
		}
		return aside, nil
	}
	return "", fmt.Errorf("%s can't be kept aside: no free name", path)
}

// DiscardJournal sets a journal Recover refused (one that doesn't decode,
// or whose files are in a third state) aside and removes it, for an
// explicit reconcile that then describes the files as they are. The
// caller holds Lock. It returns where the journal was kept.
func DiscardJournal() (string, error) {
	aside, err := KeepAside(filepath.Base(JournalPath()), "reconciled")
	if err != nil {
		return "", err
	}
	if err := os.Remove(JournalPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return aside, fmt.Errorf("lifecycle retire journal: %w", err)
	}
	return aside, nil
}
