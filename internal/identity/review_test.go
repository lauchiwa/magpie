package identity

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/lifecycle"
)

// A write waits for lifecycle's lock before it takes mu (lifecycle → mu),
// so a reader that only needs mu, as every Sign does, is never held behind
// a writer that is itself waiting for a provider commit.
func TestReviewAReadDoesNotWaitBehindAWriterWaitingForTheLock(t *testing.T) {
	ownStore(t)
	p := confirmed(t)
	unlock, err := lifecycle.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bound := make(chan error, 1)
	go func() { bound <- Bind("relay", p.ID) }()
	// the writer ends before the test does, whatever it fails on
	defer func() {
		unlock()
		select {
		case <-bound:
		case <-time.After(15 * time.Second):
			t.Error("Bind did not finish")
		}
	}()
	time.Sleep(100 * time.Millisecond) // let Bind reach its wait

	read := make(chan error, 1)
	go func() { _, err := ReadSnapshot(); read <- err }()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		unlock()
		<-read
		t.Fatal("ReadSnapshot waited behind a writer waiting for lifecycle's lock")
	}
}

// An error over a store or a .bak that can't be read names the files and
// says what is wrong, without the bytes in them: a fingerprint or a
// hand-pasted header could sit there.
func TestReviewAnUnreadableStoreErrorHoldsNoContent(t *testing.T) {
	const secret = "sk-test-4242-not-real"
	cases := map[string]struct{ primary, bak string }{
		"blank over a bad .bak":   {" \n", `{"bound": {"relay": "` + secret + `"`},
		"bad primary, no backup":  {`{"bound": {"relay": "` + secret + `"`, ""},
		"bad primary, bad backup": {`{"x": ` + secret + `}`, secret},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ownStore(t)
			if err := os.MkdirAll(strings.TrimSuffix(Path(), "identities.json"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(Path(), []byte(c.primary), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.bak != "" {
				if err := os.WriteFile(lastgood.Bak(Path()), []byte(c.bak), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := ReadSnapshot()
			if err == nil {
				t.Fatal("an unreadable store was read")
			}
			if strings.Contains(err.Error(), "sk-test") || strings.Contains(err.Error(), "4242") {
				t.Errorf("error carries the file's contents: %q", err)
			}
			if !strings.Contains(err.Error(), Path()) {
				t.Errorf("error %q does not name %s", err, Path())
			}
			if c.bak != "" && strings.TrimSpace(c.primary) == "" && !strings.Contains(err.Error(), lastgood.Bak(Path())) {
				t.Errorf("error %q does not name the .bak", err)
			}
		})
	}
}

// A journal whose identities.json is now neither its before nor its after
// can't be finished, and an identity write on top of it would bury the
// evidence: the write is refused, and the file and journal stay as they are.
func TestReviewAnAmbiguousJournalStopsTheWrite(t *testing.T) {
	ownStore(t)
	p := confirmed(t)
	if err := Bind("relay", p.ID); err != nil {
		t.Fatal(err)
	}
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
	// a third state: someone else changed the file after the journal
	third := bytes.Replace(cur, []byte(`"relay"`), []byte(`"elsewhere"`), 1)
	if err := os.WriteFile(Path(), third, 0o600); err != nil {
		t.Fatal(err)
	}

	err = Bind("other", p.ID)
	var amb *lifecycle.AmbiguousError
	if !errors.As(err, &amb) || !errors.Is(err, lifecycle.ErrUnknown) {
		t.Fatalf("Bind over an ambiguous journal = %v, want an AmbiguousError", err)
	}
	if got, _ := os.ReadFile(Path()); !bytes.Equal(got, third) {
		t.Errorf("identities.json changed under an ambiguous journal:\n%s", got)
	}
	if got, _ := os.ReadFile(lifecycle.JournalPath()); !bytes.Equal(got, j) {
		t.Error("the ambiguous journal was changed or removed")
	}
}
