package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Mode says whether Begin may take Lock and Recover an abandoned journal.
type Mode int

const (
	// Writable may take Lock and Recover a pending journal before reading.
	Writable Mode = iota + 1
	// ReadOnly never creates, locks, recovers or writes any file.
	ReadOnly
)

// MetaState classifies lifecycle.json for the owner's policy decision.
// The codec stays strict; classification never loosens decoding.
type MetaState int

const (
	MetaValid MetaState = iota + 1
	MetaAbsent
	MetaCorrupt
	MetaUnsupported
	MetaIO
)

func (s MetaState) String() string {
	switch s {
	case MetaValid:
		return "valid"
	case MetaAbsent:
		return "absent"
	case MetaCorrupt:
		return "corrupt"
	case MetaUnsupported:
		return "unsupported"
	case MetaIO:
		return "io"
	}
	return "invalid"
}

var (
	// ErrMetaCorrupt is metadata that is present but not a valid encoding.
	ErrMetaCorrupt = errors.New("lifecycle metadata corrupt")
	// ErrMetaUnsupported is metadata written by a newer format version.
	ErrMetaUnsupported = errors.New("lifecycle metadata version unsupported")
	// ErrMetaIO is metadata that could not be read; its content is unknown.
	ErrMetaIO = errors.New("lifecycle metadata unreadable")
)

// MetaError says which class of unusable metadata was found. errors.Is
// matches ErrUnknown and the class sentinel (ErrMetaCorrupt, …).
type MetaError struct {
	State MetaState
	Path  string
	Err   error
}

func (e *MetaError) Error() string {
	return fmt.Sprintf("%s: lifecycle metadata %s: %v", e.Path, e.State, e.Err)
}
func (e *MetaError) Unwrap() []error {
	var class error
	switch e.State {
	case MetaCorrupt:
		class = ErrMetaCorrupt
	case MetaUnsupported:
		class = ErrMetaUnsupported
	default:
		class = ErrMetaIO
	}
	return []error{ErrUnknown, class, e.Err}
}

// PendingError is a read-only Begin that found a journal it may not recover.
// It matches ErrBusy: a writable caller (or a later attempt) can finish it.
type PendingError struct{ Journal string }

func (e *PendingError) Error() string {
	return fmt.Sprintf("%s: an operation is pending recovery; run magpie with write access", e.Journal)
}
func (e *PendingError) Unwrap() error { return ErrBusy }

// Mark is the state Begin saw. A collector reads its sources once between
// Begin and Validate and discards everything if Validate fails.
type Mark struct {
	state MetaState
	image Image
	meta  *Meta
	err   error
}

// State is lifecycle.json's class at Begin.
func (m Mark) State() MetaState { return m.state }

// Meta is a copy of the decoded metadata when State is MetaValid.
func (m Mark) Meta() *Meta {
	if m.meta == nil {
		return nil
	}
	c := m.meta.Clone()
	return &c
}

// Err is the *MetaError for corrupt, unsupported or unreadable metadata.
func (m Mark) Err() error { return m.err }

// beginWait bounds Begin's wait for a pending journal, including Lock.
const beginWait = 2 * time.Second

// Begin opens a collection window: no journal pending, then lifecycle.json's
// complete bytes, classified. A pending journal is waited out; Writable may
// take Lock and Recover an abandoned one, ReadOnly never writes and returns
// a *PendingError. Both give ErrBusy after two seconds. A journal that does
// not decode is ErrUnknown. Never call Begin holding Lock.
func Begin(mode Mode) (Mark, error) {
	if mode != Writable && mode != ReadOnly {
		return Mark{}, fmt.Errorf("%w: invalid lifecycle mode", ErrUnknown)
	}
	ctx, cancel := context.WithTimeout(context.Background(), beginWait)
	defer cancel()
	for {
		pending, err := readJournal()
		if err != nil {
			return Mark{}, err
		}
		if !pending.Exists {
			return readMark()
		}
		if _, err := DecodeJournal(pending.Bytes); err != nil {
			return Mark{}, fmt.Errorf("%s: %w", JournalPath(), err)
		}
		if err := ctx.Err(); err != nil {
			if mode == ReadOnly {
				return Mark{}, &PendingError{Journal: JournalPath()}
			}
			return Mark{}, fmt.Errorf("%w: %w", ErrBusy, err)
		}
		if mode == ReadOnly {
			select {
			case <-ctx.Done():
			case <-time.After(lockPoll):
			}
			continue
		}
		unlock, err := Lock(ctx)
		if err != nil {
			return Mark{}, err
		}
		err = Recover()
		unlock()
		if err != nil {
			return Mark{}, err
		}
	}
}

func readMark() (Mark, error) {
	image, err := ReadImage(MetaFile)
	m := Mark{image: image}
	var meta Meta
	m.state, meta, m.err = classify(image, err)
	if m.state == MetaValid {
		m.meta = &meta
	}
	return m, nil
}

// Validate accepts the window when no journal is pending and lifecycle.json
// holds exactly the bytes Begin saw. Otherwise ErrBusy: the collector drops
// everything it read and may begin again (at most three times). Unreadable
// metadata never validates.
//
// With MetaAbsent there is no generation to compare: a legacy commit of
// several files that starts and finishes entirely inside the window leaves
// no trace here, so Validate cannot prove such a window coherent. A
// collector combining more than one owner's file under MetaAbsent must hold
// Lock for its capture instead (design C.3: take it after Begin returns,
// Recover under it, then read); without Lock it reports the
// view unavailable rather than return a mixed one. A single atomic read of
// one owner's file needs no Lock.
func (m Mark) Validate() error {
	if m.state == 0 {
		return fmt.Errorf("%w: lifecycle window was not begun", ErrUnknown)
	}
	if m.state == MetaIO {
		return m.err
	}
	// Journal first: a commit removes it only after writing metadata, so a
	// commit finishing between these reads still changes the metadata.
	pending, err := readJournal()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	if pending.Exists {
		return fmt.Errorf("%w: an operation is pending", ErrBusy)
	}
	after, err := ReadImage(MetaFile)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	if !m.image.equal(after) {
		return fmt.Errorf("%w: lifecycle metadata changed", ErrBusy)
	}
	return nil
}
