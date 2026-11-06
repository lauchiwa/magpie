package budget

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/gwbudget"
	"github.com/yetone/magpie/internal/testenv"
	"github.com/yetone/magpie/internal/usage"
)

// fork: Billing D4. While usage.jsonl can't be read, every limit of a key
// is unavailable, never 0 used: All says so of each, Binding picks it
// over a spent one, and Reserve refuses with ErrUsageUnread. The failed
// read isn't kept, so once the log reads again the key is let in.
func TestUnreadUsageIsNoZero(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	Forget()
	t.Cleanup(Forget)
	now := time.Now()
	key := access.Key{ID: "unread-key", Name: "Unread", Limit: &access.Limit{Period: "day", Tokens: 1000},
		More: []access.Limit{{Period: "week", Cost: 5}}}
	who := access.Identity{KeyID: key.ID, KeyName: key.Name, Limit: key.Limit, More: key.More}
	// a directory where the log goes: a read fails with an error that
	// isn't "no log"
	if err := os.MkdirAll(usage.Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	all := All(key, now)
	if len(all) != 2 {
		t.Fatalf("statuses: %+v", all)
	}
	for _, st := range all {
		if !st.Unavailable || st.TokensLeft != 0 || st.CostLeft != 0 || st.Spent {
			t.Errorf("an unread limit: %+v", st)
		}
	}
	if b := Binding(all); b == nil || !b.Unavailable {
		t.Errorf("binding of unread limits: %+v", b)
	}
	release, refused := Reserve(who, 10, "", now)
	if refused == nil {
		release()
		t.Fatal("a key with unread usage is let in")
	}
	if !errors.Is(refused, gwbudget.ErrUsageUnread) || !refused.Unavailable {
		t.Errorf("refusal: %v %+v", refused, refused)
	}

	// Binding prefers an unread limit to a spent one, whichever comes first
	spent := Status{Period: "week", Spent: true, Reset: now.Add(72 * time.Hour)}
	unread := Status{Period: "day", Unavailable: true, Reset: now.Add(time.Hour)}
	for _, l := range [][]Status{{spent, unread}, {unread, spent}} {
		if b := Binding(l); !b.Unavailable {
			t.Errorf("Binding(%+v) = %+v", l, b)
		}
	}

	// the log reads again: 0 used, let in
	if err := os.Remove(usage.Path()); err != nil {
		t.Fatal(err)
	}
	for _, st := range All(key, now) {
		if st.Unavailable {
			t.Errorf("a readable log is still unread: %+v", st)
		}
	}
	release, refused = Reserve(who, 10, "", now)
	if refused != nil {
		t.Fatalf("a key whose log reads again is refused: %v", refused)
	}
	release()
}
