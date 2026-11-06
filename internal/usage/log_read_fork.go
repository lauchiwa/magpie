package usage

import (
	"errors"
	"io/fs"
	"os"
	"time"
)

// errLogNotFile is a usage.jsonl that is there but no file: a folder in
// its place reads as no log on Windows and as a failed read elsewhere.
var errLogNotFile = errors.New("usage.jsonl is not a file")

// VisitRead is Visit for a reader that must not take a log it couldn't
// read for an empty one (a gateway key's limits, Billing D4): err is why
// the log couldn't be read, and then fn is never called. No log at all is
// no error, as a fresh install has none. Visit itself still reads a
// failed read as no calls, as the Usage page always has.
func VisitRead(since time.Time, fn func(Record)) error {
	s := readLogSnapshot()
	if s.err != nil {
		return s.err
	}
	s.visit(since, fn)
	return nil
}

// logStatErr is the error a stat of the log gives, a log that is no file
// included.
func logStatErr(info os.FileInfo, err error) error {
	if err == nil && !info.Mode().IsRegular() {
		return errLogNotFile
	}
	return err
}

// unreadErr is err as a snapshot keeps it: nil for a log that isn't there.
func unreadErr(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// sameLogErr reports whether two failed stats of the log fail alike: both
// for no log, or both for a log that can't be read. A snapshot of no log
// isn't kept for one that can't be read, nor the other way.
func sameLogErr(kept, err error) bool {
	return (kept == nil) == (unreadErr(err) == nil)
}
