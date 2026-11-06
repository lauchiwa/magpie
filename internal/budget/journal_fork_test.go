package budget

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/gwbudget"
	"github.com/yetone/magpie/internal/testenv"
	"github.com/yetone/magpie/internal/usage"
)

// fork: Billing D4, write failures (journal_fork.go).

func journalHome(t *testing.T) (access.Key, access.Identity) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	Forget()
	t.Cleanup(Forget)
	key := access.Key{ID: "held-key", Name: "Held", Limit: &access.Limit{Period: "day", Tokens: 1000}}
	return key, access.Identity{KeyID: key.ID, KeyName: key.Name, Limit: key.Limit}
}

// readOnly makes path a file that reads but can't be written to.
func readOnly(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("root writes a read-only file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) }) // else Windows can't remove it
}

// A request cut off before it ended (the process crashed, was killed, or
// handed the gateway over) counts at its reservation after a restart:
// what it was sent for may have been billed. Before the journal it was
// gone, and the key had its whole cap back.
func TestCutOffRequestCountsAtItsReservation(t *testing.T) {
	key, who := journalHome(t)
	now := time.Now()
	if _, refused := Reserve(who, 4000, "", now); refused != nil { // 1000 tokens reserved
		t.Fatal(refused)
	}
	Forget() // a restart, the request never released
	st := All(key, now)[0]
	if st.Pending != 1 || st.Tokens != 1000 || st.TokensLeft != 0 || !st.Spent {
		t.Fatalf("after a restart, a request cut off: %+v", st)
	}
	if _, refused := Reserve(who, 0, "", now); refused == nil || refused.Unavailable || refused.Unrecorded {
		t.Fatalf("the key's cap is held by the cut-off request: %+v", refused)
	}
}

// A request whose call was recorded counts by its record, once, whether
// or not it lived to write its "done"; one that ended counts nothing more.
func TestHeldRequestCountsOnce(t *testing.T) {
	key, who := journalHome(t)
	now := time.Now()
	if _, refused := ReserveHold("h-recorded", who, 4000, "", now); refused != nil {
		t.Fatal(refused)
	}
	Append(usage.Record{Time: now, Provider: "p", Model: "m", Input: 300, Output: 50, Status: 200, CallerKeyID: key.ID, Hold: "h-recorded"})
	Forget() // crashed after the record, before the done
	if st := All(key, now)[0]; st.Pending != 0 || st.Tokens != 350 || st.Calls != 1 {
		t.Fatalf("a recorded request after a restart: %+v", st)
	}
	release, refused := Reserve(who, 4000, "", now)
	if refused != nil {
		t.Fatal(refused)
	}
	release()
	Forget()
	if st := All(key, now)[0]; st.Pending != 0 || st.Tokens != 350 {
		t.Fatalf("a request that ended without a call: %+v", st)
	}
}

// An issued key's request holds its own key and its issuer's under one
// reservation: cut off, it counts in both; ended, in neither.
func TestIssuedRequestHeldInBoth(t *testing.T) {
	key, who := journalHome(t)
	issued := access.Key{ID: "issued-key", Name: "Issued", Limit: &access.Limit{Period: "day", Tokens: 5000}}
	as := access.Identity{KeyID: issued.ID, KeyName: issued.Name, Limit: issued.Limit, Issuer: &who}
	now := time.Now()
	release, refused := Reserve(as, 400, "", now)
	if refused != nil {
		t.Fatal(refused)
	}
	if _, refused := Reserve(as, 400, "", now); refused != nil { // left cut off
		t.Fatal(refused)
	}
	release()
	Forget()
	for _, k := range []access.Key{issued, key} {
		if st := All(k, now)[0]; st.Pending != 1 || st.Tokens != 100 {
			t.Errorf("%s after a restart: %+v", k.ID, st)
		}
	}
}

// A key's call that usage.jsonl couldn't take still counts after a
// restart: it is kept in the journal. Before, the process counted it
// until it ended, and then it was gone.
func TestUnwrittenCallStillCounts(t *testing.T) {
	key, _ := journalHome(t)
	readOnly(t, usage.Path())
	now := time.Now()
	Append(usage.Record{Time: now, Provider: "p", Model: "m", Input: 300, Output: 50, Status: 200, CallerKeyID: key.ID})
	if st := All(key, now)[0]; st.Tokens != 350 || st.Unavailable {
		t.Fatalf("a call usage.jsonl refused, before a restart: %+v", st)
	}
	Forget()
	if st := All(key, now)[0]; st.Tokens != 350 || st.Calls != 1 || st.Unavailable {
		t.Fatalf("a call usage.jsonl refused, after a restart: %+v", st)
	}
}

// While a reservation can't be written down, a limited key's request is
// refused, nothing sent, as a restart couldn't count it; its status still
// reads. Once the journal takes writes again, it is let in.
func TestUnrecordedReservationIsRefused(t *testing.T) {
	key, who := journalHome(t)
	readOnly(t, holdsPath())
	now := time.Now()
	_, refused := Reserve(who, 400, "", now)
	if refused == nil || !refused.Unrecorded || refused.Unavailable || !errors.Is(refused, gwbudget.ErrUsageUnrecorded) {
		t.Fatalf("a reservation the journal refused: %+v", refused)
	}
	if !strings.Contains(refused.Error(), "usage-holds.jsonl") {
		t.Errorf("the refusal: %s", refused.Error())
	}
	if st := All(key, now)[0]; st.Unavailable || st.Spent || st.InFlight != 0 {
		t.Fatalf("the key's status while its reservations can't be written: %+v", st)
	}
	os.Chmod(holdsPath(), 0o644)
	release, refused := Reserve(who, 400, "", now)
	if refused != nil {
		t.Fatalf("once the journal takes writes: %v", refused)
	}
	release()
}
