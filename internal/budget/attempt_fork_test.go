package budget

import (
	"errors"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/gwbudget"
	"github.com/yetone/magpie/internal/usage"
)

func attemptHome(t *testing.T) (access.Key, access.Identity, time.Time) {
	t.Helper()
	k, who := journalHome(t)
	k.More = []access.Limit{{Period: "day", Tokens: 1000, Models: []string{"p/m"}}, {Period: "day", Tokens: 1000, Models: []string{"p/other"}}}
	who.More = k.More
	return k, who, time.Now()
}

func newAttempt(t *testing.T, who access.Identity, at time.Time) gwbudget.AttemptLease {
	t.Helper()
	l, err := (Limiter{}).ReserveAttempt(gwbudget.AttemptSpec{Who: who, Provider: "p", Model: "m", BodyBytes: 400, At: at})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func attemptRow(who access.Identity, l gwbudget.AttemptLease, at time.Time) usage.Record {
	return usage.Record{Time: at, Hold: l.Hold(), Provider: "p", Model: "m", CallerKeyID: who.KeyID, CallerIssuerID: who.IssuerID(), Input: 30, Output: 7, Status: 200}
}

func journalLine(t *testing.T, line holdLine) {
	t.Helper()
	journal.Lock()
	err := writeHoldLocked(line)
	journal.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestAttemptUnknownLiveAndRestart(t *testing.T) {
	k, who, now := attemptHome(t)
	l := newAttempt(t, who, now)
	if err := l.MarkSent(); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.NeverSent}); err == nil {
		t.Fatal("sent request canceled as never sent")
	}
	if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Unknown}); err != nil {
		t.Fatal(err)
	}
	for _, restart := range []bool{false, true} {
		if restart {
			Forget()
		}
		all := All(k, now)
		for i, st := range all {
			if i < 2 {
				if !st.Unavailable || st.Pending != 1 || st.InFlight != 0 || st.TokensLeft != 0 || st.Tokens != 100 {
					t.Fatalf("restart=%v affected %d: %+v", restart, i, st)
				}
			} else if st.Unavailable || st.Pending != 0 || st.Tokens != 0 {
				t.Fatalf("unrelated scope changed: %+v", st)
			}
		}
		if _, err := (Limiter{}).ReserveAttempt(gwbudget.AttemptSpec{Who: who, Provider: "p", Model: "m", At: now}); !errors.Is(err, gwbudget.ErrUsageUnread) {
			t.Fatalf("unknown admitted: %v", err)
		}
	}
}

func TestAttemptNeverSentAndExplicitZero(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(map[bool]string{false: "never-sent", true: "reported-zero"}[sent], func(t *testing.T) {
			k, who, now := attemptHome(t)
			l := newAttempt(t, who, now)
			out := gwbudget.AttemptOutcome{Kind: gwbudget.NeverSent}
			if sent {
				if err := l.MarkSent(); err != nil {
					t.Fatal(err)
				}
				r := attemptRow(who, l, now)
				r.Input, r.Output = 0, 0
				receipt, err := l.Record(r)
				if err != nil {
					t.Fatal(err)
				}
				out = gwbudget.AttemptOutcome{Kind: gwbudget.Recorded, Receipt: receipt}
			}
			if err := l.Finish(out); err != nil {
				t.Fatal(err)
			}
			Forget()
			for _, st := range All(k, now) {
				if st.Unavailable || st.Pending != 0 || st.Tokens != 0 {
					t.Fatalf("valid terminal: %+v", st)
				}
			}
		})
	}
}

func TestAttemptCompletePrimaryErrorMirrorCountsOnce(t *testing.T) {
	k, who, now := attemptHome(t)
	l := newAttempt(t, who, now)
	if err := l.MarkSent(); err != nil {
		t.Fatal(err)
	}
	saved := appendUsageRecord
	t.Cleanup(func() { appendUsageRecord = saved })
	appendUsageRecord = func(r usage.Record) error {
		if err := usage.AppendErr(r); err != nil {
			return err
		}
		return errors.New("synthetic error after complete primary write")
	}
	receipt, err := l.Record(attemptRow(who, l, now))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Recorded, Receipt: receipt}); err != nil {
		t.Fatal(err)
	}
	for _, restart := range []bool{false, true} {
		if restart {
			Forget()
		}
		for i, st := range All(k, now) {
			want := int64(37)
			if i == 2 {
				want = 0
			}
			if st.Unavailable || st.Pending != 0 || st.Tokens != want || i < 2 && st.Calls != 1 {
				t.Fatalf("restart=%v mirror counted twice: %+v", restart, st)
			}
		}
	}
}

func TestAttemptPartialFallbackIsNotAReceipt(t *testing.T) {
	k, who, now := attemptHome(t)
	l := newAttempt(t, who, now)
	if err := l.MarkSent(); err != nil {
		t.Fatal(err)
	}
	saved := appendUsageRecord
	t.Cleanup(func() { appendUsageRecord = saved })
	appendUsageRecord = func(usage.Record) error { return errors.New("synthetic primary failure") }
	if err := l.Diagnostic(attemptRow(who, l, now)); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Unknown}); err != nil {
		t.Fatal(err)
	}
	Forget()
	st := All(k, now)[0]
	if !st.Unavailable || st.Pending != 1 || st.Calls != 1 || st.Tokens != 137 {
		t.Fatalf("partial usage became complete or disappeared: %+v", st)
	}
}

func TestAttemptPrimaryMultiplicityAndWrongPrincipal(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "duplicate-primary", true: "wrong-principal"}[wrong], func(t *testing.T) {
			k, who, now := attemptHome(t)
			l := newAttempt(t, who, now)
			if err := l.MarkSent(); err != nil {
				t.Fatal(err)
			}
			r := attemptRow(who, l, now)
			if wrong {
				r.CallerKeyID = "other-key"
				if _, err := l.Record(r); err == nil {
					t.Fatal("wrong principal obtained receipt")
				}
				if err := usage.AppendErr(r); err != nil {
					t.Fatal(err)
				}
				_ = l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Unknown})
			} else {
				receipt, err := l.Record(r)
				if err != nil {
					t.Fatal(err)
				}
				if err := usage.AppendErr(r); err != nil {
					t.Fatal(err)
				}
				if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Recorded, Receipt: receipt}); err != nil {
					t.Fatal(err)
				}
			}
			Forget()
			st := All(k, now)[0]
			want := int64(174)
			if wrong {
				want = 100
			}
			if !st.Unavailable || st.Pending != 1 || st.Tokens != want {
				t.Fatalf("conflicting primary settled or swallowed charge: %+v", st)
			}
		})
	}
}

func TestAttemptConflictingOpenUnknown(t *testing.T) {
	k, who, now := attemptHome(t)
	l := newAttempt(t, who, now).(*attemptLease)
	duplicate := l.open
	duplicate.Model = "other"
	duplicate.Aliases = []string{"p/other"}
	duplicate.Set = openSet(duplicate)
	journalLine(t, duplicate)
	Forget()
	if st := All(k, now)[0]; !st.Unavailable || st.Pending != 1 {
		t.Fatalf("duplicate open guessed: %+v", st)
	}
}

func TestAttemptFallbackClockAndIssuer(t *testing.T) {
	k, who, _ := attemptHome(t)
	cut := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	parent := access.Identity{KeyID: "issuer", Limit: &access.Limit{Period: "week", Tokens: 1000}}
	who.Issuer = &parent
	l := newAttempt(t, who, cut.Add(time.Second))
	if err := l.MarkSent(); err != nil {
		t.Fatal(err)
	}
	receipt, err := l.Record(attemptRow(who, l, cut.Add(-time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Recorded, Receipt: receipt}); err != nil {
		t.Fatal(err)
	}
	Forget()
	if st := All(k, cut.Add(time.Second))[0]; st.Unavailable || st.Tokens != 37 {
		t.Fatalf("late display row missing new window: %+v", st)
	}
	if st := All(k, cut.Add(-time.Second))[0]; st.Unavailable || st.Tokens != 0 {
		t.Fatalf("admission charged old window: %+v", st)
	}
	if st := All(access.Key{ID: parent.KeyID, Limit: parent.Limit}, cut)[0]; st.Unavailable || st.Tokens != 37 {
		t.Fatalf("issuer correspondence: %+v", st)
	}
}

func TestAttemptExternalAppendInvalidatesWarmSums(t *testing.T) {
	_, who, now := attemptHome(t)
	l := newAttempt(t, who, now)
	if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.NeverSent}); err != nil {
		t.Fatal(err)
	}
	// Warm the cached windows, then bypass budget.Append as another writer does.
	if _, err := (Limiter{}).InspectScopes(who, now); err != nil {
		t.Fatal(err)
	}
	if err := usage.AppendErr(usage.Record{Time: now, Provider: "p", Model: "m", CallerKeyID: who.KeyID, Input: 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err := (Limiter{}).ReserveAttempt(gwbudget.AttemptSpec{Who: who, Provider: "p", Model: "m", At: now}); err == nil {
		t.Fatal("warm sums ignored external charge")
	}
}

func TestAttemptSharedWriterObserverOutsideBudgetLock(t *testing.T) {
	k, who, now := attemptHome(t)
	l := newAttempt(t, who, now)
	if err := l.MarkSent(); err != nil {
		t.Fatal(err)
	}
	saved := appendUsageRecord
	t.Cleanup(func() { appendUsageRecord = saved })
	appendUsageRecord = func(r usage.Record) error {
		if st := All(k, now)[0]; !st.Unavailable {
			t.Fatal("in-progress writer exposed known sums")
		}
		return usage.AppendErr(r)
	}
	receipt, err := l.Record(attemptRow(who, l, now))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Recorded, Receipt: receipt}); err != nil {
		t.Fatal(err)
	}
}
