package davsync

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// syncedPair is two computers on one server, a's relay brought in on b,
// and a's next change (another provider) on the server for b to bring in.
func syncedPair(t *testing.T) (a, b computer) {
	t.Helper()
	fake := &fakeDAV{files: map[string][]byte{}, etags: map[string]string{}, dirs: map[string]bool{"/dav": true}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	cfg := Config{URL: srv.URL + "/dav/", User: "me", Password: "pw", Passphrase: "correct horse", Keys: true}
	ctx := context.Background()
	a, b = newComputer(t), newComputer(t)
	a.use(t)
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	if err := Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Now(ctx); err != nil {
		t.Fatal(err)
	}
	b.use(t)
	if err := Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Now(ctx); err != nil {
		t.Fatal(err)
	}
	// b under lifecycle.json, as a start-up leaves it
	if err := provider.ReadyLifecycle(); err != nil {
		t.Fatal(err)
	}
	a.use(t)
	if err := provider.Save(provider.Provider{ID: "other", Name: "Other", Chat: "https://other.example/v1", Key: "k2"}); err != nil {
		t.Fatal(err)
	}
	if err := Now(ctx); err != nil {
		t.Fatal(err)
	}
	b.use(t)
	return a, b
}

// renameMeanwhile renames relay on this computer between the collect and
// the bring the next sync makes, once.
func renameMeanwhile(t *testing.T) *int {
	t.Helper()
	n := 0
	afterCollect = func() {
		if n++; n == 1 {
			if err := provider.Rename("relay", "relay2"); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { afterCollect = nil })
	return &n
}

func storedIDs(t *testing.T) map[string]bool {
	t.Helper()
	ps, err := provider.Stored()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, p := range ps {
		out[p.ID] = true
	}
	return out
}

// A rename made here between a sync's collect and its bring is not undone
// by what was decided before it: the mirror is refused, and the sync runs
// once more over the file as it is now, which keeps the rename (without
// the second run the sync fails with the refusal).
func TestASyncDoesNotUndoARenameMadeMeanwhile(t *testing.T) {
	syncedPair(t)
	n := renameMeanwhile(t)
	if err := Now(context.Background()); err != nil {
		t.Fatalf("the sync: %v", err)
	}
	// the second run finds the providers changed here too, and newer
	// here: they go up, nothing is brought in
	if *n != 1 {
		t.Fatalf("the providers were brought in %d times, want the one refused", *n)
	}
	if ids := storedIDs(t); !ids["relay2"] || ids["relay"] {
		t.Fatalf("after the sync: %v, want the rename kept", ids)
	}
}

// A restore from the server met by a rename here meanwhile is refused,
// and the rename stands.
func TestARestoreRefusesOverARenameMadeMeanwhile(t *testing.T) {
	syncedPair(t)
	renameMeanwhile(t)
	_, err := Restore(context.Background())
	if !errors.As(err, new(*provider.ConflictError)) {
		t.Fatalf("the restore: %v, want *provider.ConflictError", err)
	}
	if ids := storedIDs(t); !ids["relay2"] || ids["relay"] || ids["other"] {
		t.Fatalf("after the refused restore: %v", ids)
	}
}
