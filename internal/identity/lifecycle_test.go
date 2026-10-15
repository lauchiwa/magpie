package identity

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/lifecycle"
)

// confirmed stores and confirms a Codex profile without binding it.
func confirmed(t *testing.T) Profile {
	t.Helper()
	p := newCandidate(t, ClientCodex, codexHeader())
	if err := Add(p); err != nil {
		t.Fatal(err)
	}
	if err := Confirm(p.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestIdentityWriterProcess is the second magpie in the concurrency test:
// it binds and unbinds its own providers once told to start, each
// read-modify-write widened by a pause so two processes overlap.
func TestIdentityWriterProcess(t *testing.T) {
	prefix := os.Getenv("MAGPIE_IDENTITY_TEST_WRITER")
	if prefix == "" {
		t.Skip("subprocess helper")
	}
	profile := os.Getenv("MAGPIE_IDENTITY_TEST_PROFILE")
	n, err := strconv.Atoi(os.Getenv("MAGPIE_IDENTITY_TEST_N"))
	if err != nil {
		t.Fatal(err)
	}
	readHook = func() { time.Sleep(15 * time.Millisecond) }
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := Bind(fmt.Sprintf("%s-new-%d", prefix, i), profile); err != nil {
			t.Fatal(err)
		}
		if err := Unbind(fmt.Sprintf("%s-old-%d", prefix, i)); err != nil {
			t.Fatal(err)
		}
	}
}

type writer struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out bytes.Buffer
}

func startWriter(t *testing.T, prefix, profile string, n int) *writer {
	t.Helper()
	w := &writer{cmd: exec.Command(os.Args[0], "-test.run=^TestIdentityWriterProcess$", "-test.count=1")}
	w.cmd.Env = append(os.Environ(),
		"MAGPIE_IDENTITY_TEST_WRITER="+prefix,
		"MAGPIE_IDENTITY_TEST_PROFILE="+profile,
		"MAGPIE_IDENTITY_TEST_N="+strconv.Itoa(n))
	in, err := w.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	w.in = in
	pipe, err := w.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	w.cmd.Stderr = &w.out
	if err := w.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); w.cmd.Process.Kill(); w.cmd.Wait() })
	ready := make(chan string, 1)
	r := bufio.NewReader(pipe)
	go func() { s, _ := r.ReadString('\n'); ready <- s; io.Copy(io.Discard, r) }()
	select {
	case s := <-ready:
		if s != "ready\n" {
			w.cmd.Wait()
			t.Fatalf("writer %s did not start: %q\n%s", prefix, s, w.out.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("writer %s did not start", prefix)
	}
	return w
}

// Two magpies changing bindings at once (a GUI and a CLI, or the gateway
// and a CLI) never lose each other's change: identity's mutex is one
// process's, so every write also takes lifecycle's lock across its whole
// read-modify-write.
func TestBindingsFromTwoProcessesAreNotLost(t *testing.T) {
	// t.TempDir may sit under GOTMPDIR, another volume; a child's testenv
	// requires every folder it is given beneath os.TempDir.
	dir, err := os.MkdirTemp(os.TempDir(), "magpie-identity-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_CONFIG_HOME", dir)
	p := confirmed(t)
	const n = 6
	for _, prefix := range []string{"a", "b"} {
		for i := 0; i < n; i++ {
			if err := Bind(fmt.Sprintf("%s-old-%d", prefix, i), p.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	ws := []*writer{startWriter(t, "a", p.ID, n), startWriter(t, "b", p.ID, n)}
	for _, w := range ws {
		if _, err := io.WriteString(w.in, "go\n"); err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range ws {
		done := make(chan error, 1)
		go func() { done <- w.cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("writer failed: %v\n%s", err, w.out.String())
			}
		case <-time.After(25 * time.Second):
			t.Fatal("writer did not finish")
		}
	}
	bound, err := Bindings()
	if err != nil {
		t.Fatal(err)
	}
	var lost, revived []string
	for _, prefix := range []string{"a", "b"} {
		for i := 0; i < n; i++ {
			if id := fmt.Sprintf("%s-new-%d", prefix, i); bound[id] != p.ID {
				lost = append(lost, id)
			}
			if id := fmt.Sprintf("%s-old-%d", prefix, i); bound[id] != "" {
				revived = append(revived, id)
			}
		}
	}
	if len(lost) > 0 || len(revived) > 0 {
		t.Errorf("concurrent writers lost binds %v and undid unbinds %v", lost, revived)
	}
}

// A write that can't take lifecycle's lock fails and changes nothing: it
// never goes ahead unlocked.
func TestAWriteWithoutTheLockChangesNothing(t *testing.T) {
	ownStore(t)
	p := confirmed(t)
	before, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lifecycle.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	old := lockWait
	lockWait = 50 * time.Millisecond
	defer func() { lockWait = old }()

	writes := map[string]func() error{
		"bind":    func() error { return Bind("relay", p.ID) },
		"unbind":  func() error { return Unbind("relay") },
		"confirm": func() error { return Confirm(p.ID, time.Now()) },
		"remove":  func() error { return Remove(p.ID) },
		"add":     func() error { return Add(newCandidate(t, ClientCodex, codexHeader())) },
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, lifecycle.ErrBusy) {
			t.Errorf("%s with the lock held elsewhere: %v, want lifecycle.ErrBusy", name, err)
		}
	}
	if after, _ := os.ReadFile(Path()); !bytes.Equal(before, after) {
		t.Errorf("the store changed while the lock was held elsewhere:\n%s", after)
	}
}

// A writer that died with its journal published is finished before the
// next identity write builds on the file, so that write lands on the
// state the journal promised rather than the one before it.
func TestAWriteRecoversAPendingJournalFirst(t *testing.T) {
	ownStore(t)
	p := confirmed(t)
	if err := Bind("relay", p.ID); err != nil {
		t.Fatal(err)
	}
	// the dead writer's intent: relay moved to renamed, as a provider rename would
	cur, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	after := bytes.Replace(cur, []byte(`"relay"`), []byte(`"renamed"`), 1)
	part, err := lifecycle.Prepare(lifecycle.IdentitiesFile, lifecycle.Present(after), validStore)
	if err != nil {
		t.Fatal(err)
	}
	id, err := lifecycle.NewID()
	if err != nil {
		t.Fatal(err)
	}
	j, err := lifecycle.EncodeJournal(lifecycle.Journal{Version: lifecycle.Version, ID: id, Participants: []lifecycle.Participant{part}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lifecycle.JournalPath(), j, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Bind("other", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lifecycle.JournalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the journal is still pending after a write: %v", err)
	}
	bound, err := Bindings()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"renamed": p.ID, "other": p.ID}
	if len(bound) != len(want) || bound["renamed"] != p.ID || bound["other"] != p.ID {
		t.Errorf("bindings = %v, want %v", bound, want)
	}
}

// A blank store with a .bak beside it that can't be read is unknown, not
// "no profiles": the .bak is there because the store held something.
// Without any .bak a blank file is still no profiles (TestAnEmptyStoreIsNoProfiles).
func TestABlankStoreWithABadBackupFailsClosed(t *testing.T) {
	cases := map[string]func(t *testing.T, bak string){
		"invalid": func(t *testing.T, bak string) {
			if err := os.WriteFile(bak, []byte(`{"bound": {`), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"zeroed": func(t *testing.T, bak string) {
			if err := os.WriteFile(bak, make([]byte, 64), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(t *testing.T, bak string) {
			if err := os.Mkdir(bak, 0o700); err != nil {
				t.Fatal(err)
			}
		},
	}
	for _, blank := range []string{"", " \n\t"} {
		for name, makeBak := range cases {
			t.Run(fmt.Sprintf("%s/%q", name, blank), func(t *testing.T) {
				ownStore(t)
				bindOne(t)
				if err := os.Remove(lastgood.Bak(Path())); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(Path(), []byte(blank), 0o600); err != nil {
					t.Fatal(err)
				}
				makeBak(t, lastgood.Bak(Path()))
				_, err := HeaderFor("relay")
				if err == nil {
					t.Fatal("a blank store with a bad backup was read as nothing bound")
				}
				if !strings.Contains(err.Error(), "identities.json") {
					t.Errorf("error = %q, want it to name the file", err)
				}
				if _, err := Bindings(); err == nil {
					t.Error("Bindings read a blank store with a bad backup as none")
				}
			})
		}
	}
}

// A .bak that is a directory or a link can't hold the last good
// generation, so a write refuses with the path and what to do rather than
// writing on without one (design C.3a).
func TestAWriteRefusesAnOddBackup(t *testing.T) {
	odd := map[string]func(t *testing.T, bak string){
		"directory": func(t *testing.T, bak string) {
			if err := os.Mkdir(bak, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, bak string) {
			target := filepath.Join(t.TempDir(), "elsewhere.json")
			if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, bak); err != nil {
				t.Skipf("symlinks unavailable here: %v", err)
			}
		},
	}
	for name, makeBak := range odd {
		for _, primary := range []string{"valid", "absent"} {
			t.Run(name+"/"+primary, func(t *testing.T) {
				ownStore(t)
				p := confirmed(t)
				if err := os.Remove(lastgood.Bak(Path())); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				var before []byte
				if primary == "absent" {
					if err := os.Remove(Path()); err != nil {
						t.Fatal(err)
					}
				} else {
					var err error
					if before, err = os.ReadFile(Path()); err != nil {
						t.Fatal(err)
					}
				}
				makeBak(t, lastgood.Bak(Path()))
				var err error
				if primary == "absent" {
					err = Add(p)
				} else {
					err = Bind("relay", p.ID)
				}
				if err == nil {
					t.Fatal("a write went ahead without a usable .bak")
				}
				if !strings.Contains(err.Error(), lastgood.Bak(Path())) || !strings.Contains(err.Error(), "move it aside") {
					t.Errorf("error = %q, want the .bak's path and what to do", err)
				}
				after, rerr := os.ReadFile(Path())
				if primary == "absent" {
					if !os.IsNotExist(rerr) {
						t.Errorf("the store was written: %q, %v", after, rerr)
					}
				} else if !bytes.Equal(before, after) {
					t.Errorf("the store changed:\n%s", after)
				}
			})
		}
	}
}

// Recovery A, unchanged: .bak is the generation before the last write.
// Bind, Unbind, then a crash that zeroes the store reads relay as bound
// again, as documented; a snapshot shows exactly that generation and no
// binding the generation before it did not hold.
func TestRecoveryAIsTheGenerationBeforeTheLastWrite(t *testing.T) {
	ownStore(t)
	p := confirmed(t)
	if err := Bind("relay", p.ID); err != nil {
		t.Fatal(err)
	}
	if err := Bind("gone", p.ID); err != nil {
		t.Fatal(err)
	}
	if err := Unbind("gone"); err != nil {
		t.Fatal(err)
	}
	if err := Unbind("relay"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), make([]byte, st.Size()), 0o600); err != nil {
		t.Fatal(err)
	}

	snap, err := ReadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	// relay's unbind was the last write: lost, as recovery A accepts
	if got, found, err := snap.Resolve("relay"); err != nil || !found || got.ID != p.ID {
		t.Errorf("relay over the generation before its unbind: %q, %v, %v", got.ID, found, err)
	}
	// gone's unbind was the write before: never revived
	if b := snap.Bindings(); len(b) != 1 || b["relay"] != p.ID {
		t.Errorf("snapshot bindings = %v, want relay alone", b)
	}
	if h, err := HeaderFor("gone"); err != nil || h != nil {
		t.Errorf("gone over the recovered store presents %v, %v", h, err)
	}
	if h, err := HeaderFor("relay"); err != nil || h.Get("User-Agent") != p.Fingerprint.UserAgent {
		t.Errorf("relay over the recovered store presents %v, %v", h, err)
	}
	// the next write keeps the zeroed file aside and builds on what was read
	if err := Unbind("relay"); err != nil {
		t.Fatal(err)
	}
	if b, err := Bindings(); err != nil || len(b) != 0 {
		t.Errorf("after unbinding again: %v, %v", b, err)
	}
}
