package budget

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/gwbudget"
	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/usage"
)

type attemptProcessFixture struct {
	Key  access.Key
	Who  access.Identity
	At   time.Time
	Mode string
}

func attemptFixturePath() string {
	return filepath.Join(filepath.Dir(usage.Path()), "budget-process-fixture.json")
}

// A separate test binary has no lease or sums from the writer's process.
// The writer exits before Finish; neither child starts further processes.
func TestAttemptProcessHelper(t *testing.T) {
	role := os.Getenv("MAGPIE_BUDGET_TEST_ROLE")
	if role == "" {
		t.Skip("subprocess helper")
	}
	b, err := os.ReadFile(attemptFixturePath())
	if err != nil {
		t.Fatal(err)
	}
	var f attemptProcessFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if role == "writer" {
		l := newAttempt(t, f.Who, f.At)
		if err := l.MarkSent(); err != nil {
			t.Fatal(err)
		}
		if f.Mode != "sent-no-usage" {
			if f.Mode != "primary-no-receipt" {
				appendUsageRecord = func(usage.Record) error { return errors.New("synthetic primary write failure") }
			}
			r := attemptRow(f.Who, l, f.At)
			if f.Mode == "partial-fallback" {
				err = l.Diagnostic(r)
			} else {
				_, err = l.Record(r)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(73)
	}
	if role != "reader" {
		t.Fatal("unexpected fixture role")
	}
	known := f.Mode == "complete-fallback"
	wantTokens, wantCalls, wantPending := int64(100), 0, 1
	if f.Mode != "sent-no-usage" {
		wantTokens, wantCalls = 137, 1
	}
	if known {
		wantTokens, wantPending = 37, 0
	}
	issuer := f.Who.Issuer
	for _, k := range []access.Key{f.Key, {ID: issuer.KeyID, Limit: issuer.Limit, More: issuer.More}} {
		states := All(k, f.At)
		if len(states) != 3 {
			t.Fatalf("new process principal=%s: got %d states, want 3", k.ID, len(states))
		}
		for i, st := range states {
			if i == 2 {
				if st.Unavailable || st.Tokens != 0 || st.Pending != 0 {
					t.Fatalf("unrelated scope changed: %+v", st)
				}
				continue
			}
			if st.Unavailable == known || st.Tokens != wantTokens || st.Calls != wantCalls || st.Pending != wantPending || st.InFlight != 0 || !known && st.TokensLeft != 0 {
				t.Fatalf("new process mode=%s principal=%s: %+v", f.Mode, k.ID, st)
			}
		}
	}
	l, err := (Limiter{}).ReserveAttempt(gwbudget.AttemptSpec{Who: f.Who, Provider: "p", Model: "m", At: f.At})
	if !known {
		if !errors.Is(err, gwbudget.ErrUsageUnread) {
			t.Fatalf("new process admitted unknown: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.NeverSent}); err != nil {
		t.Fatal(err)
	}
}

func TestAttemptIndependentProcessReplay(t *testing.T) {
	deadline := time.Now().Add(120 * time.Second)
	if packageDeadline, ok := t.Deadline(); ok && packageDeadline.Add(-30*time.Second).Before(deadline) {
		deadline = packageDeadline.Add(-30 * time.Second)
	}
	shared, cancelShared := context.WithDeadline(t.Context(), deadline)
	defer cancelShared()
	for _, mode := range []string{"sent-no-usage", "primary-no-receipt", "partial-fallback", "complete-fallback"} {
		if shared.Err() != nil || time.Until(deadline) < time.Minute {
			t.Fatal("insufficient shared deadline remaining for another replay mode")
		}
		if !t.Run(mode, func(t *testing.T) {
			k, who, now := attemptHome(t)
			who.Issuer = &access.Identity{KeyID: "synthetic-issuer", Limit: k.Limit, More: k.More}
			b, err := json.Marshal(attemptProcessFixture{k, who, now, mode})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(attemptFixturePath()), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(attemptFixturePath(), b, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, role := range []string{"writer", "reader"} {
				// Reserve the full child allowance before starting; keep the
				// package's final 30 seconds available for cancellation/reaping.
				if shared.Err() != nil || time.Until(deadline) < time.Minute {
					t.Fatal("insufficient shared deadline remaining for another replay child")
				}
				ctx, cancel := context.WithTimeout(shared, time.Minute)
				cmd := proc.CommandContext(ctx, os.Args[0], "-test.run=^TestAttemptProcessHelper$", "-test.count=1", "-test.v", "-test.timeout=45s")
				// Inherit testenv.Marker, isolated folder variables, inert
				// tools and TMP/TEMP; TestMain validates all child paths.
				cmd.Env = append(os.Environ(), "MAGPIE_BUDGET_TEST_ROLE="+role)
				out, err := cmd.CombinedOutput()
				timedOut := ctx.Err()
				cancel()
				t.Logf("%s %s: %s", mode, role, out)
				var exit *exec.ExitError
				if timedOut != nil || role == "writer" && (!errors.As(err, &exit) || exit.ExitCode() != 73) || role == "reader" && err != nil {
					t.Fatalf("%s process failed: %v (context %v)", role, err, timedOut)
				}
			}
		}) {
			t.Fatal("replay mode failed; no further children will be started")
		}
	}
}
