package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/lastgood"
)

// listing records every name and its bytes under the config directory, so a
// read-only Begin can be shown to have created, locked or changed nothing.
func listing(t *testing.T) string {
	t.Helper()
	var out []string
	root := appdir.Config()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == root {
				out = append(out, "<no config dir>")
				return filepath.SkipDir
			}
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out = append(out, rel+"/")
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, rel+"="+Digest(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestBeginClassifiesMeta(t *testing.T) {
	future := encoded(t, meta(1, beforeProvider))
	future = bytes.Replace(future, []byte(`"version": 1`), []byte(`"version": 2`), 1)
	for name, c := range map[string]struct {
		body  []byte // nil: absent
		dir   bool
		state MetaState
		class error
	}{
		"absent":            {state: MetaAbsent},
		"valid":             {body: encoded(t, meta(1, beforeProvider)), state: MetaValid},
		"empty":             {body: []byte{}, state: MetaCorrupt, class: ErrMetaCorrupt},
		"zeroed":            {body: []byte{0, 0, 0}, state: MetaCorrupt, class: ErrMetaCorrupt},
		"text":              {body: []byte("damaged metadata"), state: MetaCorrupt, class: ErrMetaCorrupt},
		"no-version":        {body: []byte(`{}`), state: MetaCorrupt, class: ErrMetaCorrupt},
		"current-bad-field": {body: []byte(`{"version":1,"epoch":"x"}`), state: MetaCorrupt, class: ErrMetaCorrupt},
		"duplicate-version": {body: []byte(`{"version":1,"version":2}`), state: MetaCorrupt, class: ErrMetaCorrupt},
		"future":            {body: future, state: MetaUnsupported, class: ErrMetaUnsupported},
		"future-bare":       {body: []byte(`{"version":2}`), state: MetaUnsupported, class: ErrMetaUnsupported},
		"directory":         {dir: true, state: MetaIO, class: ErrMetaIO},
	} {
		t.Run(name, func(t *testing.T) {
			home(t)
			if c.dir {
				if err := os.Mkdir(Path(), 0700); err != nil {
					t.Fatal(err)
				}
			} else if c.body != nil {
				put(t, MetaFile, c.body)
			}
			for _, mode := range []Mode{Writable, ReadOnly} {
				m, err := Begin(mode)
				if err != nil {
					t.Fatalf("mode %d: Begin refused classifiable metadata: %v", mode, err)
				}
				if m.State() != c.state {
					t.Fatalf("mode %d: state %v, want %v", mode, m.State(), c.state)
				}
				if c.class == nil {
					if m.Err() != nil {
						t.Fatalf("unexpected class error %v", m.Err())
					}
					if (c.state == MetaValid) != (m.Meta() != nil) {
						t.Fatalf("decoded meta presence wrong: %v", m.Meta())
					}
				} else {
					var me *MetaError
					if !errors.As(m.Err(), &me) || me.State != c.state || !errors.Is(m.Err(), c.class) || !errors.Is(m.Err(), ErrUnknown) {
						t.Fatalf("class not identifiable: %v", m.Err())
					}
					if m.Meta() != nil {
						t.Fatal("unusable metadata decoded")
					}
				}
				err = m.Validate()
				if c.state == MetaIO {
					if !errors.Is(err, ErrMetaIO) {
						t.Fatalf("unreadable metadata validated: %v", err)
					}
				} else if err != nil {
					t.Fatalf("unchanged %s window rejected: %v", c.state, err)
				}
			}
			// ReadMeta reports the same class.
			if _, err := ReadMeta(); c.class != nil && !errors.Is(err, c.class) {
				t.Fatalf("ReadMeta class lost: %v", err)
			}
		})
	}
}

func TestBeginMetaReadErrorIsIO(t *testing.T) {
	home(t)
	put(t, MetaFile, encoded(t, meta(1, beforeProvider)))
	old := lstat
	t.Cleanup(func() { lstat = old })
	lstat = func(path string) (fs.FileInfo, error) {
		if path == Path() {
			return nil, &fs.PathError{Op: "lstat", Path: path, Err: errors.New("injected I/O error")}
		}
		return os.Lstat(path)
	}
	m, err := Begin(ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if m.State() != MetaIO || !errors.Is(m.Err(), ErrMetaIO) {
		t.Fatalf("read error not classified io: %v %v", m.State(), m.Err())
	}
	if m.Meta() != nil {
		t.Fatal("unreadable metadata produced a value")
	}
}

func TestValidateRejectsAnyMetaChange(t *testing.T) {
	valid := encoded(t, meta(1, beforeProvider))
	epoch := meta(1, beforeProvider)
	epoch.Epoch = strings.Repeat("d", 32)
	reformatted, _ := json.Marshal(meta(1, beforeProvider)) // same values, other bytes
	for name, c := range map[string]struct{ before, after []byte }{
		"gen":        {valid, encoded(t, meta(2, beforeProvider))},
		"epoch":      {valid, encoded(t, epoch)},
		"bytes-only": {valid, reformatted},
		"removed":    {valid, nil},
		"created":    {nil, valid},
		"corrupted":  {valid, []byte("damaged")},
	} {
		t.Run(name, func(t *testing.T) {
			home(t)
			if c.before != nil {
				put(t, MetaFile, c.before)
			}
			m, err := Begin(ReadOnly)
			if err != nil {
				t.Fatal(err)
			}
			if c.after == nil {
				if err := os.Remove(Path()); err != nil {
					t.Fatal(err)
				}
			} else {
				put(t, MetaFile, c.after)
			}
			if err := m.Validate(); !errors.Is(err, ErrBusy) {
				t.Fatalf("changed metadata accepted: %v", err)
			}
		})
	}
}

func TestValidateRejectsPendingJournal(t *testing.T) {
	home(t)
	p := fixture(t)
	m, err := Begin(Writable)
	if err != nil {
		t.Fatal(err)
	}
	// A writer starts inside the window and stops before touching any file:
	// metadata is still identical, only the journal says a commit is open.
	func() {
		defer mustLock(t)()
		p.stage = func(s string) error {
			if s == "journal:write" {
				return errors.New("paused")
			}
			return nil
		}
		if err := Commit(p); err == nil {
			t.Fatal("not paused")
		}
	}()
	if err := m.Validate(); !errors.Is(err, ErrBusy) {
		t.Fatalf("window across an open journal accepted: %v", err)
	}
	// The next writable Begin recovers the abandoned journal first.
	m, err = Begin(Writable)
	if err != nil {
		t.Fatal(err)
	}
	checkCommitted(t)
	if m.State() != MetaValid || m.Meta().Gen != 2 || m.Validate() != nil {
		t.Fatalf("recovered window wrong: %v %v", m.State(), m.Meta())
	}
}

// A writer paused after providers but before identities and metadata, then
// released before Validate: the owner read new providers with old identities.
func TestValidateRejectsCommitFinishedInsideWindow(t *testing.T) {
	home(t)
	savePlan(t, fixture(t))
	m, err := Begin(Writable)
	if err != nil {
		t.Fatal(err)
	}
	c, in := heldChild(t, "hold", "providers.json:write")
	providers, identities := get(t, ProvidersFile), get(t, IdentitiesFile)
	in.Close()
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(providers, afterProvider) || !bytes.Equal(identities, beforeIdentity) {
		t.Fatalf("fixture did not produce the mixed read: %s %s", providers, identities)
	}
	if err := m.Validate(); !errors.Is(err, ErrBusy) {
		t.Fatalf("mixed providers/identities accepted after the journal was retired: %v", err)
	}
}

func TestBeginWaitsForLiveWriterAndRecoversDeadWriter(t *testing.T) {
	home(t)
	savePlan(t, fixture(t))
	c, _ := heldChild(t, "hold", "providers.json:write")
	for _, mode := range []Mode{Writable, ReadOnly} {
		if _, err := Begin(mode); !errors.Is(err, ErrBusy) {
			t.Fatalf("mode %d admitted a live partial commit: %v", mode, err)
		}
	}
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = c.Wait()
	// Read-only never finishes another process's work.
	var pending *PendingError
	if _, err := Begin(ReadOnly); !errors.As(err, &pending) || !errors.Is(err, ErrBusy) {
		t.Fatalf("read-only Begin over a dead journal: %v", err)
	}
	m, err := Begin(Writable)
	if err != nil {
		t.Fatal(err)
	}
	checkCommitted(t)
	if m.State() != MetaValid || m.Meta().Gen != 2 {
		t.Fatalf("Begin did not read the recovered generation: %v", m.Meta())
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBeginWaitsForWriterToComplete(t *testing.T) {
	home(t)
	savePlan(t, fixture(t))
	c, in := heldChild(t, "hold", "providers.json:write")
	in.Close() // no wall-clock ordering assumption: Begin waits either way
	m, err := Begin(Writable)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
	checkCommitted(t)
	if m.Meta() == nil || m.Meta().Gen != 2 {
		t.Fatalf("Begin read before the writer finished: %v", m.Meta())
	}
}

func TestReadOnlyBeginWritesNothing(t *testing.T) {
	t.Run("no-config-dir", func(t *testing.T) {
		home(t)
		if err := os.RemoveAll(appdir.Config()); err != nil {
			t.Fatal(err)
		}
		m, err := Begin(ReadOnly)
		if err != nil || m.State() != MetaAbsent || m.Validate() != nil {
			t.Fatalf("missing config dir: %v %v", m.State(), err)
		}
		if _, err := os.Stat(appdir.Config()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read-only Begin created the config dir: %v", err)
		}
	})
	t.Run("pending-without-lock-file", func(t *testing.T) {
		home(t)
		p := fixture(t)
		// Write the journal as a crashed writer would have, but with no
		// lock file ever created in this directory.
		b, err := EncodeJournal(Journal{Version: Version, ID: strings.Repeat("c", 32), Participants: p.Participants})
		if err != nil {
			t.Fatal(err)
		}
		put(t, "lifecycle-journal.json", b)
		before := listing(t)
		var pending *PendingError
		if _, err := Begin(ReadOnly); !errors.As(err, &pending) {
			t.Fatalf("read-only Begin over a pending journal: %v", err)
		}
		if after := listing(t); after != before {
			t.Fatalf("read-only Begin changed the directory:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if _, err := os.Stat(filepath.Join(appdir.Config(), "lifecycle.lock")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read-only Begin created the lock: %v", err)
		}
	})
	t.Run("bad-journal", func(t *testing.T) {
		home(t)
		put(t, "lifecycle-journal.json", []byte("not a journal"))
		before := listing(t)
		if _, err := Begin(ReadOnly); !errors.Is(err, ErrUnknown) {
			t.Fatalf("read-only Begin over a bad journal: %v", err)
		}
		if listing(t) != before {
			t.Fatal("read-only Begin changed the directory")
		}
	})
}

func TestBeginFailsClosedOnBadJournal(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T){
		"directory": func(t *testing.T) {
			if err := os.Mkdir(JournalPath(), 0700); err != nil {
				t.Fatal(err)
			}
		},
		"garbage": func(t *testing.T) { put(t, "lifecycle-journal.json", []byte(`{"version":1} {}`)) },
	} {
		t.Run(name, func(t *testing.T) {
			home(t)
			setup(t)
			for _, mode := range []Mode{Writable, ReadOnly} {
				if _, err := Begin(mode); !errors.Is(err, ErrUnknown) {
					t.Fatalf("mode %d: bad journal ignored: %v", mode, err)
				}
			}
			if name == "garbage" && !bytes.Equal(get(t, "lifecycle-journal.json"), []byte(`{"version":1} {}`)) {
				t.Fatal("bad journal changed")
			}
		})
	}
}

func TestBeginRejectsInvalidModeAndZeroMark(t *testing.T) {
	home(t)
	if _, err := Begin(0); !errors.Is(err, ErrUnknown) {
		t.Fatalf("invalid mode accepted: %v", err)
	}
	if err := (Mark{}).Validate(); !errors.Is(err, ErrUnknown) {
		t.Fatalf("zero Mark validated: %v", err)
	}
}

// Migrated from TestReviewViewIgnoresUnusedBackup: Begin/Validate never read
// a backup, so a valid primary with a directory .bak is not blocked.
func TestBeginIgnoresUnusedBackup(t *testing.T) {
	for _, name := range []string{ProvidersFile, IdentitiesFile, MetaFile} {
		t.Run(name, func(t *testing.T) {
			home(t)
			if name == MetaFile {
				put(t, name, encoded(t, meta(1, beforeProvider)))
			} else {
				put(t, name, []byte(`{}`))
			}
			path := filepath.Join(appdir.Config(), name)
			if err := os.Mkdir(lastgood.Bak(path), 0700); err != nil {
				t.Fatal(err)
			}
			m, err := Begin(Writable)
			if err != nil {
				t.Fatalf("unused backup blocked Begin: %v", err)
			}
			if name != MetaFile {
				if _, err := lastgood.Read(path, lastgood.JSON); err != nil {
					t.Fatalf("owner cannot read valid primary: %v", err)
				}
			}
			if err := m.Validate(); err != nil {
				t.Fatalf("unused backup blocked Validate: %v", err)
			}
		})
	}
}

// Migrated from TestReviewViewAllowsCorruptMetaLegacyRead: the owner, not
// Begin, decides what corrupt metadata means (design §6: new values legacy).
func TestCorruptMetaReachesOwner(t *testing.T) {
	home(t)
	put(t, MetaFile, []byte("damaged metadata"))
	put(t, IdentitiesFile, []byte(`{}`))
	m, err := Begin(Writable)
	if err != nil {
		t.Fatal(err)
	}
	if m.State() != MetaCorrupt || !errors.Is(m.Err(), ErrMetaCorrupt) {
		t.Fatalf("corrupt metadata not classified: %v %v", m.State(), m.Err())
	}
	if _, err := lastgood.Read(filepath.Join(appdir.Config(), IdentitiesFile), identityValid); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("legacy read under unchanged corrupt metadata refused: %v", err)
	}
	// Begin is not bootstrap: the evidence is untouched.
	if !bytes.Equal(get(t, MetaFile), []byte("damaged metadata")) {
		t.Fatal("Begin changed corrupt metadata")
	}
}

// ---- ABA: single owner snapshot (design C.1/C.2) ----

// fakeOwner models the identity owner: one atomic read of identities.json,
// then pure in-memory projections. twoReads is the forbidden variant.
type fakeOwner struct {
	path     string
	twoReads bool
	between  func() // controlled writer runs between the two projections
	reads    int
}

type fakeSnapshot struct{ bound, resolved string }

func (o *fakeOwner) boundOf(b []byte) string {
	var f struct {
		Bound map[string]string `json:"bound"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return "<bad>"
	}
	return f.Bound["a"]
}

func (o *fakeOwner) snapshot() (fakeSnapshot, error) {
	o.reads++
	b, err := lastgood.Read(o.path, identityValid)
	if err != nil {
		return fakeSnapshot{}, err
	}
	s := fakeSnapshot{bound: o.boundOf(b)} // Bindings projection
	if o.between != nil {
		o.between()
	}
	if o.twoReads {
		o.reads++
		if b, err = lastgood.Read(o.path, identityValid); err != nil {
			return fakeSnapshot{}, err
		}
	}
	s.resolved = o.boundOf(b) // Resolve projection
	return s, nil
}

// capture is the closed collector shape: Begin, one owner snapshot, Validate,
// at most three attempts, failed rounds discarded.
func (o *fakeOwner) capture() (fakeSnapshot, error) {
	for range 3 {
		m, err := Begin(Writable)
		if err != nil {
			return fakeSnapshot{}, err
		}
		s, err := o.snapshot()
		if err != nil {
			return fakeSnapshot{}, err
		}
		if err := m.Validate(); err == nil {
			return s, nil
		} else if !errors.Is(err, ErrBusy) {
			return fakeSnapshot{}, err
		}
	}
	return fakeSnapshot{}, ErrBusy
}

func abaHome(t *testing.T) (bound, unbound []byte, unbind, rebind func()) {
	t.Helper()
	home(t)
	put(t, ProvidersFile, beforeProvider)
	put(t, MetaFile, encoded(t, meta(1, beforeProvider)))
	// Identity writer formatting; unbind then rebind returns BOTH primary and
	// its previous generation to these exact bytes, metadata untouched.
	bound, err := json.MarshalIndent(map[string]any{"bound": map[string]string{"a": "identity_fixture"}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	bound = append(bound, '\n')
	unbound = []byte("{}\n")
	put(t, IdentitiesFile, bound)
	put(t, IdentitiesFile+".bak", unbound)
	write := func(b []byte) {
		u := mustLock(t)
		defer u()
		if err := lastgood.Keep(filepath.Join(appdir.Config(), IdentitiesFile), identityValid, 0600); err != nil {
			t.Fatal(err)
		}
		put(t, IdentitiesFile, b)
	}
	return bound, unbound, func() { write(unbound) }, func() { write(bound) }
}

func TestSingleSnapshotSurvivesIdentityABA(t *testing.T) {
	_, _, unbind, rebind := abaHome(t)
	o := &fakeOwner{path: filepath.Join(appdir.Config(), IdentitiesFile)}
	o.between = func() { unbind(); rebind() }
	s, err := o.capture()
	if err != nil {
		t.Fatal(err)
	}
	if o.reads != 1 {
		t.Fatalf("accepted result came from %d reads", o.reads)
	}
	// Both projections are the one state that was read: bound (before the
	// pair). Every accepted projection agrees.
	if s.bound != s.resolved || s.bound != "identity_fixture" {
		t.Fatalf("stitched snapshot: bindings=%q resolve=%q", s.bound, s.resolved)
	}
	// Linearizable either side of the pair: a read wholly inside it is the
	// unbound state, still one state.
	_, _, unbind, rebind = abaHome(t)
	o = &fakeOwner{path: filepath.Join(appdir.Config(), IdentitiesFile)}
	unbind()
	o.between = rebind
	if s, err = o.capture(); err != nil || s.bound != s.resolved || s.bound != "" {
		t.Fatalf("snapshot inside the pair stitched: %+v %v", s, err)
	}
}

// The counterexample: an owner that reads twice is accepted by Validate
// (identity-only writes do not advance metadata) and returns a mixed state.
// This test's consistency check catches it, so a second read in a real
// collector must be guarded by the same assertion.
func TestTwoReadOwnerIsCaughtByConsistencyCheck(t *testing.T) {
	_, _, unbind, rebind := abaHome(t)
	o := &fakeOwner{path: filepath.Join(appdir.Config(), IdentitiesFile), twoReads: true}
	o.between = func() { unbind() }
	s, err := o.capture()
	if err != nil {
		t.Fatalf("validate refused identity-only writes (not the C contract): %v", err)
	}
	rebind()
	if s.bound == s.resolved {
		t.Fatalf("two-read owner was not caught: %+v", s)
	}
}

// The window still rejects a provider generation that moves under the
// owner's single read, even when an identity ABA restores identities' bytes.
func TestValidateRejectsGenerationMoveDuringABA(t *testing.T) {
	_, _, unbind, rebind := abaHome(t)
	o := &fakeOwner{path: filepath.Join(appdir.Config(), IdentitiesFile)}
	moved := false
	o.between = func() {
		unbind()
		if !moved {
			moved = true
			put(t, MetaFile, encoded(t, meta(2, beforeProvider)))
		}
		rebind()
	}
	s, err := o.capture()
	if err != nil {
		t.Fatal(err)
	}
	if o.reads != 2 {
		t.Fatalf("moved generation not retried: reads=%d", o.reads)
	}
	if s.bound != s.resolved {
		t.Fatalf("stitched: %+v", s)
	}
}

func TestBeginTimesOutOnHeldLock(t *testing.T) {
	home(t)
	savePlan(t, fixture(t))
	put(t, "lifecycle-journal.json", get(t, "lifecycle-journal.json.fixture"))
	c, _ := heldChild(t, "lock", "")
	defer func() { c.Process.Kill(); c.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*1e9)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Begin(Writable); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("Begin over a held lock: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Begin waited past its 2s budget")
	}
}

// Migrated from TestViewRetriesAtMostThreeTimes: the bound now belongs to
// the collector; Validate keeps refusing while metadata keeps moving.
func TestCaptureRetriesAtMostThreeTimes(t *testing.T) {
	_, _, _, _ = abaHome(t)
	o := &fakeOwner{path: filepath.Join(appdir.Config(), IdentitiesFile)}
	gen := uint64(1)
	o.between = func() { gen++; put(t, MetaFile, encoded(t, meta(gen, beforeProvider))) }
	if _, err := o.capture(); !errors.Is(err, ErrBusy) || o.reads != 3 {
		t.Fatalf("unbounded or torn capture: reads=%d err=%v", o.reads, err)
	}
}

// Legacy (no metadata) identity-only commit left open inside the window.
func TestValidateRejectsPendingLegacyJournal(t *testing.T) {
	home(t)
	put(t, IdentitiesFile, beforeIdentity)
	m, err := Begin(Writable)
	if err != nil || m.State() != MetaAbsent {
		t.Fatalf("legacy Begin: %v %v", m.State(), err)
	}
	func() {
		defer mustLock(t)()
		p, err := Prepare(IdentitiesFile, Present(afterIdentity), identityValid)
		if err != nil {
			t.Fatal(err)
		}
		plan := Plan{Participants: []Participant{p}, stage: func(s string) error {
			if s == "identities.json:write" {
				return errors.New("paused")
			}
			return nil
		}}
		if err := Commit(plan); err == nil {
			t.Fatal("not paused")
		}
	}()
	if err := m.Validate(); !errors.Is(err, ErrBusy) {
		t.Fatalf("legacy window across an open journal accepted: %v", err)
	}
}
