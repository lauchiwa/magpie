package lifecycle

import (
	"bytes"
	"io/fs"
	"os"
	"runtime"
	"testing"
)

// countWaits replaces waitReplace with a counter for the test.
func countWaits(t *testing.T) *int {
	t.Helper()
	old := waitReplace
	t.Cleanup(func() { waitReplace = old })
	n := 0
	waitReplace = func(p string) (fs.FileInfo, error) {
		n++
		return old(p)
	}
	return &n
}

// A config with no lifecycle.json and no journal (every legacy config and
// test home) is read as absent at no wait. Waiting out a replace for each
// file not there cost every Begin about 45ms on Windows, and so every Sign
// and every provider read of a legacy config.
func TestAbsentMetaAndJournalTakeNoWait(t *testing.T) {
	home(t)
	waits := countWaits(t)
	for _, mode := range []Mode{Writable, ReadOnly} {
		m, err := Begin(mode)
		if err != nil {
			t.Fatal(err)
		}
		if m.State() != MetaAbsent {
			t.Fatalf("state %v, want absent", m.State())
		}
		if err := m.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := Recover(); err != nil {
		t.Fatal(err)
	}
	if image, err := ReadImage(MetaFile); err != nil || image.Exists {
		t.Fatalf("absent meta read as %v, %v", image.Exists, err)
	}
	if *waits != 0 {
		t.Fatalf("%d waits for files that were never being replaced", *waits)
	}
}

// lifecycle.json is replaced only while a journal is there, so a moment of
// it not there with the journal present is a replace, waited out, never
// taken for a legacy config.
func TestMetaGoneUnderJournalIsWaitedOut(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows reports a file being replaced as not there")
	}
	home(t)
	want := encoded(t, meta(1, beforeProvider))
	put(t, MetaFile, want)
	put(t, "lifecycle-journal.json", []byte("{}")) // a commit under way
	waits := countWaits(t)
	old := lstat
	t.Cleanup(func() { lstat = old })
	gone := 2 // the look, and readImage's own look before it waits
	lstat = func(p string) (fs.FileInfo, error) {
		if p == Path() && gone > 0 {
			gone--
			return nil, &fs.PathError{Op: "lstat", Path: p, Err: fs.ErrNotExist}
		}
		return os.Lstat(p)
	}
	image, err := ReadImage(MetaFile)
	if err != nil || !image.Exists || !bytes.Equal(image.Bytes, want) {
		t.Fatalf("meta being replaced read as exists=%v err=%v", image.Exists, err)
	}
	if *waits != 1 {
		t.Fatalf("%d waits, want the one for the replace", *waits)
	}
}

// A commit that runs wholly between the two looks (journal written, meta
// replaced, journal removed) has left lifecycle.json there: the second look
// finds it, so it is never read as absent.
func TestMetaWrittenBetweenLooksIsFound(t *testing.T) {
	home(t)
	want := encoded(t, meta(1, beforeProvider))
	put(t, MetaFile, want)
	old := lstat
	t.Cleanup(func() { lstat = old })
	first := true
	lstat = func(p string) (fs.FileInfo, error) {
		if p == Path() && first {
			first = false
			return nil, &fs.PathError{Op: "lstat", Path: p, Err: fs.ErrNotExist}
		}
		return os.Lstat(p)
	}
	image, err := ReadImage(MetaFile)
	if err != nil || !image.Exists || !bytes.Equal(image.Bytes, want) {
		t.Fatalf("meta written between looks read as exists=%v err=%v", image.Exists, err)
	}
}
