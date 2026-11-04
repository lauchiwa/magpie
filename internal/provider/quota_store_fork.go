package provider

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/steady"
)

// QuotaStore keeps which keys and accounts are out of their included
// allowance, and until when, across restarts. It is dormant: nothing in
// magpie makes one yet, so no file is read or written. A key is the
// gateway's rest key (a provider's id, id#keyID or an account's, with
// /model for an allowance pooled by model), opaque here.
//
// Each key holds one entry, merged so that no refusal is lost: an
// exhaustion is always written, and only a success that started after the
// last write clears it (Recover's ticket). Once the reset time has
// passed, Admit lets one real request through to find out (QuotaTry);
// there is no probe of its own. An entry or file that can't be read is
// unknown, and gets that one try too: unknown is never credit.
type QuotaStore struct {
	path string
	mu   sync.Mutex
	// read is the file as last read, kept while its size and time are
	// unchanged, so that Admit on each request costs one stat
	read  quotaRead
	tries map[string]time.Time // key: when its one try was handed out
}

type QuotaState string

const (
	QuotaNone      QuotaState = ""          // never refused: nothing to say
	QuotaExhausted QuotaState = "exhausted" // refused until ResetAt
	QuotaUnknown   QuotaState = "unknown"   // its entry or the file can't be read
)

// QuotaEntry is one key's state. Seq is the file's write that last set
// it, for Recover's ticket.
type QuotaEntry struct {
	State      QuotaState `json:"state"`
	Reason     string     `json:"reason"` // quota or credit
	ResetAt    time.Time  `json:"resetAt"`
	ObservedAt time.Time  `json:"observedAt"`
	Seq        uint64     `json:"seq"`
	tried      time.Time  // when Admit handed this ticket the try, else zero
}

type QuotaAdmit int

const (
	QuotaPass QuotaAdmit = iota // not out of its allowance
	QuotaHold                   // out until ResetAt, or another request is trying
	QuotaTry                    // the one request that finds out
)

// QuotaTryHold is how long a handed-out try holds the others back without
// an answer (a request that never ends), before another may try.
var QuotaTryHold = 10 * time.Minute

var (
	ErrQuotaInvalid     = errors.New("quota entry invalid")
	ErrQuotaUnsupported = errors.New("quota state schema unsupported")
	ErrQuotaIO          = errors.New("quota state I/O failure")
	ErrQuotaCorrupt     = errors.New("quota state corrupt")
)

// quotaLongest bounds how far past its sighting a reset is kept, as
// loadRests bounds a rest, so no refusal holds a key out for good.
// quotaSkew is how far ahead of now a sighting may be and still be this
// clock's: a refusal from further ahead was seen before the clock was set
// back, so its times say nothing of now.
const (
	quotaLongest = 31 * 24 * time.Hour
	quotaSkew    = time.Minute
)

type quotaFile struct {
	Version uint64 `json:"version"`
	Seq     uint64 `json:"seq"`
	// Unknown: written after a file that didn't parse was moved aside, so a
	// key with no entry is unknown, not never refused, until written
	Unknown bool                       `json:"unknown,omitempty"`
	Entries map[string]json.RawMessage `json:"entries"`
}

type quotaRead struct {
	size    int64
	mod     time.Time
	ok      bool
	err     error // the file as a whole: unsupported or I/O
	bad     bool  // the file doesn't parse: every key unknown
	unknown bool  // quotaFile.Unknown
	seq     uint64
	entries map[string]QuotaEntry // a bad entry is QuotaUnknown
}

func NewQuotaStore(path string) *QuotaStore { return &QuotaStore{path: path} }

// Read is key's entry as of now. A file or entry that can't be read is
// QuotaUnknown with the reason; a key never refused is QuotaNone. A refusal
// seen more than quotaSkew after now, by a clock set back since, is
// QuotaUnknown too, with its Seq for Recover.
func (s *QuotaStore) Read(key string, now time.Time) (QuotaEntry, error) {
	if !validQuotaKey(key) {
		return QuotaEntry{}, ErrQuotaInvalid
	}
	s.mu.Lock()
	r := s.load()
	s.mu.Unlock()
	switch {
	case r.err != nil:
		return QuotaEntry{State: QuotaUnknown}, r.err
	case r.bad:
		return QuotaEntry{State: QuotaUnknown}, ErrQuotaCorrupt
	}
	e, ok := r.entries[key]
	if e.State == QuotaUnknown || !ok && r.unknown {
		return QuotaEntry{State: QuotaUnknown}, ErrQuotaCorrupt
	}
	if e.State == QuotaExhausted && e.ObservedAt.After(now.Add(quotaSkew)) {
		e.State = QuotaUnknown
	}
	return e, nil
}

// Admit says whether a request may go to key now. Out of its allowance it
// is held until ResetAt; past it, or with nothing readable to go by, one
// request at a time is QuotaTry and the others are held. The entry is the
// ticket for Recover.
func (s *QuotaStore) Admit(key string, now time.Time) (QuotaAdmit, QuotaEntry) {
	e, err := s.Read(key, now)
	switch {
	case errors.Is(err, ErrQuotaInvalid), e.State == QuotaNone:
		return QuotaPass, e
	case e.State == QuotaExhausted && now.Before(e.ResetAt):
		return QuotaHold, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at, ok := s.tries[key]; ok && !now.Before(at) && now.Sub(at) < QuotaTryHold {
		return QuotaHold, e
	}
	if s.tries == nil {
		s.tries = map[string]time.Time{}
	}
	s.tries[key] = now
	e.tried = now
	return QuotaTry, e
}

// Exhaust records a refusal for key's allowance, back at resetAt (the
// vendor's time, else the provider's own: OwnQuotaRest), at most
// quotaLongest after observedAt, when it was seen: Exhaust is called as it
// is. It is always written: beside a refusal already kept, the later reset
// and sighting stand, so no refusal of several at once is lost. One kept
// from more than quotaSkew after observedAt was seen by a clock since set
// back, and is replaced.
func (s *QuotaStore) Exhaust(key, reason string, resetAt, observedAt time.Time) error {
	if !validQuotaKey(key) || reason != "quota" && reason != "credit" || resetAt.IsZero() || observedAt.IsZero() {
		return ErrQuotaInvalid
	}
	// the refusal holds key until its reset, so whichever request saw it,
	// the try is over
	defer s.release(key, nil)
	return s.change(key, func(cur QuotaEntry) (QuotaEntry, bool) {
		e := QuotaEntry{State: QuotaExhausted, Reason: reason, ResetAt: resetAt.UTC(), ObservedAt: observedAt.UTC()}
		if longest := e.ObservedAt.Add(quotaLongest); e.ResetAt.After(longest) {
			e.ResetAt = longest
		}
		if cur.State == QuotaExhausted && !cur.ObservedAt.After(e.ObservedAt.Add(quotaSkew)) {
			if cur.ResetAt.After(e.ResetAt) {
				e.ResetAt = cur.ResetAt
			}
			if cur.ObservedAt.After(e.ObservedAt) {
				e.ObservedAt = cur.ObservedAt
			}
		}
		return e, true
	})
}

// Recover clears key's refusal after a request to it was served. ticket
// is the entry Admit gave that request: a refusal written since then is
// newer than the success and stays. Only the request handed the try ends
// it; another one served meanwhile, admitted before the refusal, doesn't.
func (s *QuotaStore) Recover(key string, ticket QuotaEntry) error {
	if !validQuotaKey(key) {
		return ErrQuotaInvalid
	}
	defer s.release(key, &ticket)
	s.mu.Lock()
	r := s.load()
	s.mu.Unlock()
	if e, ok := r.entries[key]; r.err == nil && !r.bad && (ok && e.State == QuotaNone || !ok && !r.unknown) {
		return nil // nothing kept for it: nothing to clear (the hot path)
	}
	return s.change(key, func(cur QuotaEntry) (QuotaEntry, bool) {
		if cur.State == QuotaExhausted && cur.Seq != ticket.Seq {
			return cur, false
		}
		return QuotaEntry{}, true
	})
}

// release lets another request try key: after any refusal (ticket nil),
// or the answer of the request that was handed the try.
func (s *QuotaStore) release(key string, ticket *QuotaEntry) {
	s.mu.Lock()
	if at, ok := s.tries[key]; ok && (ticket == nil || !ticket.tried.IsZero() && ticket.tried.Equal(at)) {
		delete(s.tries, key)
	}
	s.mu.Unlock()
}

// load is the file as last read, read again when its size or time
// changed. Called with s.mu held; a read is one small file.
func (s *QuotaStore) load() quotaRead {
	target, err := edit.Target(s.path)
	if err != nil {
		return quotaRead{err: ErrQuotaIO}
	}
	st, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		s.read = quotaRead{}
		return s.read
	}
	if err != nil {
		return quotaRead{err: ErrQuotaIO}
	}
	if s.read.ok && s.read.size == st.Size() && s.read.mod.Equal(st.ModTime()) {
		return s.read
	}
	b, err := steady.ReadFile(target)
	if err != nil {
		return quotaRead{err: ErrQuotaIO}
	}
	s.read = decodeQuota(b)
	s.read.size, s.read.mod, s.read.ok = st.Size(), st.ModTime(), true
	return s.read
}

// decodeQuota reads each entry on its own: one that doesn't read is that
// key's QuotaUnknown, and the others stand.
func decodeQuota(b []byte) quotaRead {
	var f quotaFile
	if json.Unmarshal(b, &f) != nil || f.Version == 0 {
		return quotaRead{bad: true}
	}
	if f.Version != 1 {
		return quotaRead{err: ErrQuotaUnsupported}
	}
	r := quotaRead{seq: f.Seq, unknown: f.Unknown, entries: make(map[string]QuotaEntry, len(f.Entries))}
	for k, raw := range f.Entries {
		if !validQuotaKey(k) {
			continue // no key to give it to
		}
		var e QuotaEntry
		if json.Unmarshal(raw, &e) != nil || !validQuotaEntry(e, f.Seq) {
			e = QuotaEntry{State: QuotaUnknown}
		}
		r.entries[k] = e
	}
	return r
}

// change writes key's entry as fn makes it from the one kept (QuotaNone
// takes it out), under the file's lock, through a temp file renamed over
// it. A file that doesn't parse is moved aside first, as logins.json is;
// one of a newer schema is left alone. Other keys' bytes are kept as
// they are, a bad entry's too.
func (s *QuotaStore) change(key string, fn func(QuotaEntry) (QuotaEntry, bool)) error {
	target, err := edit.Target(s.path)
	if err != nil {
		return ErrQuotaIO
	}
	quotaWriting.Lock()
	defer quotaWriting.Unlock()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return ErrQuotaIO
	}
	unlock, _ := filelock.Lock(target+".lock", filelock.Wait)
	defer unlock()
	defer func() { s.mu.Lock(); s.read = quotaRead{}; s.mu.Unlock() }()
	// a new file's writes count on from a number no earlier one reached,
	// so a ticket from before it can't match
	f := quotaFile{Version: 1, Seq: uint64(time.Now().UnixNano()), Entries: map[string]json.RawMessage{}}
	cur := QuotaEntry{}
	b, err := steady.ReadFile(target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return ErrQuotaIO
	default:
		r := decodeQuota(b)
		switch {
		case r.err != nil:
			return r.err
		case r.bad:
			aside := target + ".bad-" + time.Now().UTC().Format("20060102T150405.000000000")
			if steady.Rename(target, aside) != nil {
				return ErrQuotaIO
			}
			f.Unknown = true
		default:
			var kept quotaFile
			if json.Unmarshal(b, &kept) == nil && kept.Entries != nil {
				f.Entries = kept.Entries
			}
			f.Seq, f.Unknown, cur = r.seq, r.unknown, r.entries[key]
		}
	}
	next, write := fn(cur)
	if !write {
		return nil
	}
	return s.put(target, f, key, next)
}

// quotaWriting keeps this process's writers one at a time; filelock keeps
// them apart from another magpie's.
var quotaWriting sync.Mutex

func (s *QuotaStore) put(target string, f quotaFile, key string, e QuotaEntry) error {
	f.Seq++
	if e.State == QuotaNone && !f.Unknown {
		delete(f.Entries, key)
	} else {
		// in a file whose keys are unknown, a key served is kept as clear
		e.Seq = f.Seq
		raw, err := json.Marshal(e)
		if err != nil {
			return ErrQuotaInvalid
		}
		f.Entries[key] = raw
	}
	b, err := json.Marshal(f)
	if err != nil {
		return ErrQuotaInvalid
	}
	if steady.WriteFile(target, b, 0o600) != nil {
		return ErrQuotaIO
	}
	return nil
}

func validQuotaEntry(e QuotaEntry, seq uint64) bool {
	if e.Seq == 0 || e.Seq > seq {
		return false
	}
	if e.State == QuotaNone {
		return e.Reason == "" && e.ResetAt.IsZero() && e.ObservedAt.IsZero()
	}
	return e.State == QuotaExhausted && (e.Reason == "quota" || e.Reason == "credit") &&
		!e.ResetAt.IsZero() && !e.ObservedAt.IsZero() && !e.ResetAt.After(e.ObservedAt.Add(quotaLongest))
}

// validQuotaKey: a rest key is text with no control characters, of a
// bounded length.
func validQuotaKey(k string) bool {
	if k == "" || len(k) > 512 || !utf8.ValidString(k) {
		return false
	}
	for _, r := range k {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
