package budget

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/usage"
)

func replaceAttemptJournal(t *testing.T, lines []holdLine) {
	t.Helper()
	var b []byte
	for _, l := range lines {
		row, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b = append(append(b, row...), '\n')
	}
	if err := os.WriteFile(holdsPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	Forget()
}

func TestAttemptUnprovenRecordsKeepKnownCharges(t *testing.T) {
	for _, mode := range []string{"orphan-primary", "orphan-rec", "wrong-set", "wrong-fingerprint", "wrong-principal", "wrong-model", "unsupported", "done-with-record", "conflicting-open"} {
		t.Run(mode, func(t *testing.T) {
			k, who, now := attemptHome(t)
			l := newAttempt(t, who, now).(*attemptLease)
			r := attemptRow(who, l, now)
			rec := holdLine{Version: 2, Op: "rec", ID: l.Hold(), At: now, Set: l.open.Set, Outcome: "recorded", Rec: &r}
			lines := []holdLine{l.open}
			wantTokens, wantPending, index := int64(137), 1, 0
			switch mode {
			case "orphan-primary":
				if err := usage.AppendErr(r); err != nil {
					t.Fatal(err)
				}
				lines = nil
				wantTokens, wantPending = 37, 0
			case "orphan-rec":
				lines = nil
				wantTokens, wantPending = 37, 0
			case "wrong-set":
				rec.Set = "unrelated-open-set"
			case "wrong-principal":
				r.CallerKeyID = "other-principal"
				k.ID = r.CallerKeyID
				wantTokens, wantPending = 37, 0
			case "wrong-model":
				r.Model = "other"
				wantTokens, wantPending, index = 37, 0, 2
			case "unsupported":
				rec.Version = 3
			case "done-with-record":
				rec.Op = "done"
			case "conflicting-open":
				other := l.open
				other.Model, other.Aliases = "other", []string{"p/other"}
				other.Set = openSet(other)
				lines = append(lines, other)
			}
			rec.Fingerprint = rowFingerprint(r)
			if mode == "wrong-fingerprint" {
				rec.Fingerprint = "not-the-record"
			}
			if mode != "orphan-primary" {
				lines = append(lines, rec)
			}
			replaceAttemptJournal(t, lines)
			st := All(k, now)[index]
			if !st.Unavailable || st.Calls != 1 || st.Tokens != wantTokens || st.Pending != wantPending || st.TokensLeft != 0 {
				t.Fatalf("lost charge or minted completion: %+v", st)
			}
		})
	}
}

func TestAttemptProvenMirrorsDoNotEraseOccurrences(t *testing.T) {
	for _, primaryCount := range []int{0, 1, 2} {
		t.Run(strconv.Itoa(primaryCount), func(t *testing.T) {
			k, who, now := attemptHome(t)
			l := newAttempt(t, who, now).(*attemptLease)
			r := attemptRow(who, l, now)
			for range primaryCount {
				if err := usage.AppendErr(r); err != nil {
					t.Fatal(err)
				}
			}
			rec := holdLine{Version: 2, Op: "rec", ID: l.Hold(), At: now, Set: l.open.Set, Outcome: "recorded", Fingerprint: rowFingerprint(r), Rec: &r}
			replaceAttemptJournal(t, []holdLine{l.open, rec, rec})
			st := All(k, now)[0]
			wantCalls, wantTokens, wantPending := max(1, primaryCount), int64(37), 0
			if primaryCount == 2 {
				wantTokens, wantPending = 174, 1
			}
			if st.Calls != wantCalls || st.Tokens != wantTokens || st.Pending != wantPending || st.Unavailable != (primaryCount == 2) {
				t.Fatalf("primary=%d: %+v", primaryCount, st)
			}
		})
	}
}

func TestAttemptOrphanPrimaryDoesNotAffectOtherScope(t *testing.T) {
	k, who, now := attemptHome(t)
	l := newAttempt(t, who, now)
	if err := usage.AppendErr(attemptRow(who, l, now)); err != nil {
		t.Fatal(err)
	}
	replaceAttemptJournal(t, nil)
	if st := All(k, now)[2]; st.Unavailable || st.Tokens != 0 || st.Pending != 0 {
		t.Fatalf("unrelated scope made unavailable: %+v", st)
	}
	k.ID = "unrelated-principal"
	if st := All(k, now)[0]; st.Unavailable || st.Tokens != 0 {
		t.Fatalf("unrelated principal affected: %+v", st)
	}
}

func TestAttemptLegacyPendingWithChangedWindow(t *testing.T) {
	_, who, _ := attemptHome(t)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	week := access.Limit{Period: "week"}
	start, _ := week.Window(now)
	journalLine(t, holdLine{Op: "open", ID: "legacy-unknown-model", Key: who.KeyID, At: now, Holds: []holdWindow{{Start: start.Unix(), Tokens: 100}}})
	Forget()
	// A newly added shorter scoped window overlaps the legacy attempt even
	// though that attempt's original whole-key window started earlier.
	k := access.Key{ID: who.KeyID, Limit: &access.Limit{Period: "day", Tokens: 1000, Models: []string{"p/m"}}}
	if st := All(k, now)[0]; !st.Unavailable || st.TokensLeft != 0 {
		t.Fatalf("unattributed legacy hold became scoped credit: %+v", st)
	}
}

func TestAttemptCompactionRetainsUncertainEvidence(t *testing.T) {
	for _, mode := range []string{"v2", "downgraded", "unsupported", "torn"} {
		t.Run(mode, func(t *testing.T) {
			k, who, now := attemptHome(t)
			l := newAttempt(t, who, now).(*attemptLease)
			open := l.open
			done := holdLine{Version: 2, Op: "done", ID: l.Hold(), At: now, Set: open.Set, Outcome: "recorded", Fingerprint: "no-primary"}
			if mode == "downgraded" {
				open.Version, done.Version = 0, 0
			} else if mode == "unsupported" {
				open.Version, done.Version = 3, 3
			}
			replaceAttemptJournal(t, []holdLine{open, done})
			if mode == "torn" {
				f, err := os.OpenFile(holdsPath(), os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("{\"v\":2,\"op\":\"open\"")
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
			}
			before, err := os.ReadFile(holdsPath())
			if err != nil {
				t.Fatal(err)
			}
			if st := All(k, now)[0]; !st.Unavailable || st.TokensLeft != 0 {
				t.Fatalf("unproven done granted credit: %+v", st)
			}
			after, err := os.ReadFile(holdsPath())
			if err != nil || string(after) != string(before) {
				t.Fatal("compaction erased uncertain evidence", err)
			}
		})
	}
}
