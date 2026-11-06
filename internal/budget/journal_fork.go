package budget

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/usage"
)

// fork: Billing D4, write failures. usage-holds.jsonl, beside usage.jsonl,
// keeps what a restart needs of a limited key's requests and nothing for
// any other request:
//   - "open": a request let in, written before it is sent, with what it
//     is reserved at in each window it counts in;
//   - "done": the request ended, and what it used is in usage.jsonl (or
//     a "rec" line);
//   - "rec": a call of a key whose record usage.jsonl couldn't take.
//
// A window read from the files counts usage.jsonl's calls, the "rec"
// lines, and every "open" with no "done", no record naming it
// (usage.Record.Hold) and not in flight in this process: a request cut
// off by a crash, a kill or a handover, at its reservation (pending). It
// is written without fsync: it outlives the process, not a power cut.

// holdLine is one line of usage-holds.jsonl.
type holdLine struct {
	Op    string        `json:"op"` // open, done, rec
	ID    string        `json:"id,omitempty"`
	At    time.Time     `json:"at"`
	Key   string        `json:"key,omitempty"`
	Holds []holdWindow  `json:"holds,omitempty"`
	Rec   *usage.Record `json:"rec,omitempty"`
}

// holdWindow is what an open request is reserved at in one window.
type holdWindow struct {
	Start  int64   `json:"start"` // the window's start, Unix seconds
	Tokens int64   `json:"tokens"`
	Cost   float64 `json:"cost,omitempty"`
}

// holdsCompactAt is the size past which the journal is written again
// with only the lines a window can still need.
const holdsCompactAt = 256 << 10

// holdsKeep is how long an "open" with no "done", or a "rec", is kept:
// the longest window a limit can have (access.MaxDays).
const holdsKeep = access.MaxDays * 24 * time.Hour

var journal = struct {
	sync.Mutex
	live      map[string]int  // this process's requests in flight: an issued key's holds twice
	lost      map[string]bool // ones a call of couldn't be written anywhere
	compacted map[string]bool // by path, once a process
	cache     struct {
		path  string
		size  int64
		mod   time.Time
		lines []holdLine
	}
}{live: map[string]int{}, lost: map[string]bool{}, compacted: map[string]bool{}}

func holdsPath() string { return filepath.Join(filepath.Dir(usage.Path()), "usage-holds.jsonl") }

// NewHold is an id for a request's reservation.
func NewHold() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// forgetJournal drops what the journal keeps in memory, as a restart would.
func forgetJournal() {
	journal.Lock()
	defer journal.Unlock()
	journal.live, journal.lost, journal.compacted = map[string]int{}, map[string]bool{}, map[string]bool{}
	journal.cache.path, journal.cache.lines = "", nil
}

// writeHoldLocked appends l. Called with journal held.
func writeHoldLocked(l holdLine) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	path := holdsPath()
	compactLocked(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrNotExist) && os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return err
	}
	// a line cut off by a crash ends at the next one's leading newline,
	// and is skipped as one that doesn't parse
	_, err = f.Write(append(append([]byte{'\n'}, b...), '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// openHold writes id's "open" for keyID, before the request is sent, and
// marks it in flight here.
func openHold(id, keyID string, holds []holdWindow) error {
	journal.Lock()
	defer journal.Unlock()
	if err := writeHoldLocked(holdLine{Op: "open", ID: id, At: time.Now(), Key: keyID, Holds: holds}); err != nil {
		return err
	}
	journal.live[id]++
	return nil
}

// settle writes id's "done" once the request has ended (its last hold
// let go), unless a call of
// it couldn't be written anywhere: then its "open" stays, and a restart
// counts it at its reservation rather than not at all.
func settle(id string) {
	journal.Lock()
	defer journal.Unlock()
	if journal.live[id] == 0 {
		return
	}
	if journal.live[id]--; journal.live[id] > 0 {
		return
	}
	delete(journal.live, id)
	if !journal.lost[id] {
		_ = writeHoldLocked(holdLine{Op: "done", ID: id, At: time.Now()})
	}
}

// keepUnwritten keeps rec, a key's call usage.jsonl couldn't take, as a
// "rec" line. Called with state held.
func keepUnwritten(rec usage.Record) {
	journal.Lock()
	defer journal.Unlock()
	if writeHoldLocked(holdLine{Op: "rec", At: time.Now(), Rec: &rec}) != nil && rec.Hold != "" {
		journal.lost[rec.Hold] = true
	}
}

// holdLines is the journal's lines and the requests in flight here, read
// together; no journal is none, one that isn't a file is an error.
func holdLines() ([]holdLine, map[string]bool, error) {
	journal.Lock()
	defer journal.Unlock()
	path := holdsPath()
	compactLocked(path)
	live := make(map[string]bool, len(journal.live))
	for id := range journal.live {
		live[id] = true
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, live, nil
	}
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("usage-holds.jsonl is not a file")
	}
	if err != nil {
		return nil, nil, err
	}
	c := &journal.cache
	if c.path == path && c.size == info.Size() && c.mod.Equal(info.ModTime()) {
		return c.lines, live, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	lines := parseHolds(b)
	c.path, c.size, c.mod, c.lines = path, info.Size(), info.ModTime(), lines
	if int64(len(b)) != info.Size() { // written to between the stat and the read
		c.path = ""
	}
	return lines, live, nil
}

func parseHolds(b []byte) []holdLine {
	var out []holdLine
	for _, s := range strings.Split(string(b), "\n") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		var l holdLine
		if json.Unmarshal([]byte(s), &l) == nil && l.Op != "" {
			out = append(out, l)
		}
	}
	return out
}

// compactLocked writes the journal at path again with only what a window
// can still need, once a process and whenever it has grown past
// holdsCompactAt: an "open" with a "done" and that "done" go, as do lines
// older than holdsKeep. A journal written to meanwhile is left as it is.
// Called with journal held.
func compactLocked(path string) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || (journal.compacted[path] && info.Size() < holdsCompactAt) {
		return
	}
	journal.compacted[path] = true
	b, err := os.ReadFile(path)
	if err != nil || int64(len(b)) != info.Size() {
		return
	}
	lines := parseHolds(b)
	done := map[string]bool{}
	for _, l := range lines {
		if l.Op == "done" {
			done[l.ID] = true
		}
	}
	cut := time.Now().Add(-holdsKeep)
	var keep []byte
	dropped := 0
	for _, l := range lines {
		if l.Op == "done" || (l.Op == "open" && done[l.ID]) || l.At.Before(cut) {
			dropped++
			continue
		}
		if j, err := json.Marshal(l); err == nil {
			keep = append(append(keep, j...), '\n')
		}
	}
	// nothing to drop is nothing to write: the journal is left as it is
	if now, err := os.Stat(path); dropped == 0 || err != nil || now.Size() != info.Size() {
		return
	}
	tmp := path + ".tmp-" + NewHold()
	if os.WriteFile(tmp, keep, 0o600) != nil || os.Rename(tmp, path) != nil {
		os.Remove(tmp)
		return
	}
	journal.cache.path = ""
}

// addHeld adds to s, keyID's sums in the window from s.start, the
// journal's calls usage.jsonl couldn't take and the requests cut off
// before they ended, at their reservation. recorded is the holds that
// usage.jsonl's calls name.
func addHeld(s *sums, keyID string, lines []holdLine, live, recorded map[string]bool, cost func(usage.Record) (float64, bool)) {
	done := map[string]bool{}
	for _, l := range lines {
		switch {
		case l.Op == "done":
			done[l.ID] = true
		case l.Op == "rec" && l.Rec != nil && l.Rec.Hold != "":
			recorded[l.Rec.Hold] = true
		}
	}
	for _, l := range lines {
		switch l.Op {
		case "rec":
			r := l.Rec
			if r != nil && (r.CallerKeyID == keyID || r.CallerIssuerID == keyID) && counts(*r) && !r.Time.Before(s.start) {
				c, ok := cost(*r)
				s.add(*r, c, ok)
			}
		case "open":
			if l.Key != keyID || done[l.ID] || recorded[l.ID] || live[l.ID] {
				continue
			}
			for _, h := range l.Holds {
				if h.Start == s.start.Unix() {
					s.pending++
					s.pendTokens += h.Tokens
					s.cost += h.Cost
				}
			}
		}
	}
}
