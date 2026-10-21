package lifecycle

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
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
)

// This binary is the second magpie, inheriting testenv's sandbox and fixtures.
func TestLifecycleProcess(t *testing.T) {
	mode := os.Getenv("MAGPIE_LIFECYCLE_TEST_MODE")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	unlock, err := Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if mode == "lock" {
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	image, err := readImage(JournalPath() + ".fixture")
	if err != nil {
		t.Fatal(err)
	}
	j, err := DecodeJournal(image.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	p := Plan{Participants: j.Participants, stage: func(stage string) error {
		if stage == os.Getenv("MAGPIE_LIFECYCLE_TEST_STAGE") {
			if mode == "crash" {
				os.Exit(73)
			}
			fmt.Println("ready")
			_, _ = io.Copy(io.Discard, os.Stdin)
		}
		return nil
	}}
	if err := Commit(p); err != nil {
		t.Fatal(err)
	}
}
func child(t *testing.T, mode, stage string) *exec.Cmd {
	t.Helper()
	c := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcess$", "-test.count=1")
	c.Env = append(os.Environ(), "MAGPIE_LIFECYCLE_TEST_MODE="+mode, "MAGPIE_LIFECYCLE_TEST_STAGE="+stage)
	return c
}
func heldChild(t *testing.T, mode, stage string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	c := child(t, mode, stage)
	in, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); c.Process.Kill(); c.Wait() })
	ready := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(out).ReadString('\n'); ready <- s }()
	select {
	case s := <-ready:
		if s != "ready\n" {
			t.Fatalf("child failed to hold: %q", s)
		}
	case <-time.After(10 * time.Second):
		c.Process.Kill()
		c.Wait()
		out.Close()
		<-ready
		t.Fatalf("child did not start: %s", stderr.String())
	}
	return c, in
}
func savePlan(t *testing.T, p Plan) {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeJournal(Journal{Version: Version, ID: id, Participants: p.Participants})
	if err != nil {
		t.Fatal(err)
	}
	put(t, "lifecycle-journal.json.fixture", b)
}
func TestLockAcrossProcessesExitReleases(t *testing.T) {
	home(t)
	c, _ := heldChild(t, "lock", "")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	u, err := Lock(ctx)
	cancel()
	if u != nil {
		u()
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second process acquired held lock: %v", err)
	}
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = c.Wait()
	u = mustLock(t)
	u()
	if _, err := os.Stat(filepath.Join(appdir.Config(), "lifecycle.lock")); err != nil {
		t.Fatalf("unlock removed persistent lock: %v", err)
	}
}
func TestCrashRecoveryEveryStage(t *testing.T) {
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			home(t)
			savePlan(t, fixture(t))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			base := child(t, "crash", stage)
			c := exec.CommandContext(ctx, base.Path, base.Args[1:]...)
			c.Env = base.Env
			out, err := c.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("not a crash at %s: %v %s", stage, err, out)
			}
			defer mustLock(t)()
			if err := Recover(); err != nil {
				t.Fatal(err)
			}
			if err := Recover(); err != nil {
				t.Fatal(err)
			}
			checkCommitted(t)
		})
	}
}
