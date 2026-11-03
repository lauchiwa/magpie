package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func quotaCoreTime() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }

func quotaCoreFact(kind QuotaEvidence, at time.Time) QuotaFact {
	f := QuotaFact{Window: "day", Kind: kind, EvidenceID: "evidence-day", Cycle: "cycle-1", ResetAt: at.Add(time.Hour)}
	switch kind {
	case QuotaReportedFull:
		f.Capacity = &QuotaCapacity{Semantics: "included", Unit: "requests", Limit: 10}
	case QuotaReportedAvailable, QuotaConfirmedRecovery:
		f.Capacity = &QuotaCapacity{Semantics: "included", Unit: "requests", Remaining: 5, Limit: 10}
		f.FreshUntil = at.Add(2 * time.Hour)
	}
	return f
}

func quotaCoreObservation(revision uint64, at time.Time, facts ...QuotaFact) QuotaObservation {
	return QuotaObservation{
		Subject: QuotaSubject{Authority: "authority-1", Provider: "provider-1", Principal: "principal-1", Generation: "generation-1"},
		ID:      fmt.Sprintf("observation-%d", revision+1), Expected: QuotaVersion{StoreEpoch: "epoch-1", Revision: revision},
		Source: "source-1", ObservedAt: at,
		Coverage: []QuotaCoverage{{Window: "day", Scopes: []string{"account"}}}, Facts: facts,
	}
}

func quotaCoreJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func quotaCoreReduce(t *testing.T, current *quotaRecord, o QuotaObservation) quotaRecord {
	t.Helper()
	before, input := quotaCoreJSON(t, current), quotaCoreJSON(t, o)
	r, err := reduceQuota(current, o, o.ObservedAt)
	if err != nil {
		t.Fatalf("reduce %s: %v", o.ID, err)
	}
	if quotaCoreJSON(t, current) != before || quotaCoreJSON(t, o) != input {
		t.Fatal("reducer mutated an input")
	}
	stored := cloneQuotaRecord(r)
	stored.LastID, stored.Digest = o.ID, strings.Repeat("a", 64)
	if err := validateQuotaRecord(stored); err != nil {
		t.Fatalf("reducer produced an invalid stored record: %v", err)
	}
	return r
}

func quotaCoreView(r *quotaRecord, now time.Time) QuotaSnapshot {
	o := quotaCoreObservation(0, now)
	version := o.Expected
	if r != nil {
		version.Revision = r.Revision
	}
	return quotaView(r, o.Subject, "account", version, now)
}

func quotaCoreRecovery(w quotaWindow, at time.Time) QuotaFact {
	f := quotaCoreFact(QuotaConfirmedRecovery, at)
	f.Window, f.EvidenceID = w.Window, "recovery-"+w.Window
	f.Recovery = &QuotaRecovery{BlockerID: w.ObservationID, BlockerRevision: w.Revision, PriorCycle: w.Cycle, Basis: "superseding", Provenance: "correction-1"}
	if w.Blocker != nil {
		f.Recovery.BlockerID, f.Recovery.BlockerRevision, f.Recovery.PriorCycle = w.Blocker.ID, w.Blocker.Revision, w.Blocker.Cycle
	}
	return f
}

func TestQuotaCoreTransitionMatrix(t *testing.T) {
	at := quotaCoreTime()
	for _, prior := range []QuotaEvidence{QuotaResetHint, QuotaReportedAvailable, QuotaReportedFull, QuotaConfirmedExhaustion} {
		for _, incoming := range []QuotaEvidence{QuotaResetHint, QuotaReportedAvailable, QuotaReportedFull, QuotaConfirmedExhaustion, QuotaConfirmedRecovery} {
			t.Run(string(prior)+"/"+string(incoming), func(t *testing.T) {
				r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(prior, at)))
				f := quotaCoreFact(incoming, at.Add(time.Minute))
				if incoming == QuotaConfirmedRecovery {
					f = quotaCoreRecovery(r.Windows[0], at.Add(time.Minute))
				}
				o := quotaCoreObservation(1, at.Add(time.Minute), f)
				if incoming == QuotaConfirmedRecovery && r.Windows[0].State != QuotaStateExhausted {
					if _, err := reduceQuota(&r, o, o.ObservedAt); !errors.Is(err, ErrQuotaConflict) {
						t.Fatalf("recovery without exhaustion: %v", err)
					}
					return
				}
				want := QuotaStateUnknown
				switch incoming {
				case QuotaResetHint:
					want = r.Windows[0].State
				case QuotaReportedFull, QuotaConfirmedExhaustion:
					want = QuotaStateExhausted
				case QuotaReportedAvailable:
					want = QuotaStateAvailable
					if prior == QuotaConfirmedExhaustion {
						want = QuotaStateExhausted
					}
				case QuotaConfirmedRecovery:
					want = QuotaStateAvailable
				}
				next := quotaCoreReduce(t, &r, o)
				v := quotaCoreView(&next, o.ObservedAt)
				if v.State != want || next.Revision != 2 {
					t.Fatalf("got state %s revision %d, want %s revision 2", v.State, next.Revision, want)
				}
				wantBlocker := incoming == QuotaConfirmedExhaustion || prior == QuotaConfirmedExhaustion && incoming != QuotaConfirmedRecovery
				if (next.Windows[0].Blocker != nil) != wantBlocker {
					t.Fatalf("blocker=%+v, want present=%v", next.Windows[0].Blocker, wantBlocker)
				}
			})
		}
	}
}

func TestQuotaCoreViewClock(t *testing.T) {
	at := quotaCoreTime()
	for _, tc := range []struct {
		name    string
		kind    QuotaEvidence
		noReset bool
		delta   time.Duration
		state   QuotaState
		due     bool
	}{
		{"positive before acquisition", QuotaReportedAvailable, false, -time.Nanosecond, QuotaStateUnknown, false},
		{"positive at acquisition", QuotaReportedAvailable, false, 0, QuotaStateAvailable, false},
		{"positive before reset", QuotaReportedAvailable, false, time.Hour - time.Nanosecond, QuotaStateAvailable, false},
		{"positive at reset", QuotaReportedAvailable, false, time.Hour, QuotaStateUnknown, true},
		{"positive after reset", QuotaReportedAvailable, false, time.Hour + time.Nanosecond, QuotaStateUnknown, true},
		{"positive before freshness", QuotaReportedAvailable, true, 2*time.Hour - time.Nanosecond, QuotaStateAvailable, false},
		{"positive at freshness", QuotaReportedAvailable, true, 2 * time.Hour, QuotaStateUnknown, false},
		{"positive after freshness", QuotaReportedAvailable, true, 3 * time.Hour, QuotaStateUnknown, false},
		{"full before reset", QuotaReportedFull, false, time.Hour - time.Nanosecond, QuotaStateExhausted, false},
		{"full at reset", QuotaReportedFull, false, time.Hour, QuotaStateExhausted, true},
		{"refusal long after reset", QuotaConfirmedExhaustion, false, 365 * 24 * time.Hour, QuotaStateExhausted, true},
		{"refusal before acquisition", QuotaConfirmedExhaustion, false, -time.Hour, QuotaStateExhausted, false},
		{"full without reset", QuotaReportedFull, true, 365 * 24 * time.Hour, QuotaStateExhausted, false},
		{"hint before reset", QuotaResetHint, false, time.Minute, QuotaStateUnknown, false},
		{"hint at reset", QuotaResetHint, false, time.Hour, QuotaStateUnknown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := quotaCoreFact(tc.kind, at)
			if tc.noReset {
				f.ResetAt = time.Time{}
			}
			r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, f))
			before := quotaCoreJSON(t, r)
			v := quotaCoreView(&r, at.Add(tc.delta))
			if v.State != tc.state || v.RecheckDue != tc.due || len(v.Windows) != 1 || v.Windows[0].State != tc.state {
				t.Fatalf("view=%+v, want %s due=%v", v, tc.state, tc.due)
			}
			if quotaCoreJSON(t, r) != before {
				t.Fatal("read changed stored evidence")
			}
		})
	}
	// The supplied clock cannot reveal an earlier read after expiry. This is not
	// an irreversible-expiry or wall-clock-rollback guarantee.
	r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(QuotaReportedAvailable, at)))
	if quotaCoreView(&r, at.Add(3*time.Hour)).State != QuotaStateUnknown || quotaCoreView(&r, at.Add(time.Minute)).State != QuotaStateAvailable {
		t.Fatal("views do not follow the supplied interval")
	}
}

func TestQuotaCoreMissingViews(t *testing.T) {
	at := quotaCoreTime()
	o := quotaCoreObservation(0, at, quotaCoreFact(QuotaReportedAvailable, at))
	r := quotaCoreReduce(t, nil, o)
	for _, scope := range []string{"", "unknown", "account/day"} {
		v := quotaView(&r, o.Subject, scope, o.Expected, at)
		if v.State != QuotaStateUnknown || len(v.Windows) != 0 || v.Scope != scope {
			t.Fatalf("unknown scope %q: %+v", scope, v)
		}
	}
	if v := quotaCoreView(nil, at); v.State != QuotaStateUnknown || len(v.Windows) != 0 || v.RecheckDue {
		t.Fatalf("missing record: %+v", v)
	}
	before := quotaViews(nil, o.Subject, []QuotaCoverage{{Window: "week", Scopes: []string{"pool", "account"}}, {Window: "day", Scopes: []string{"account"}}}, o.Expected, at)
	if len(before) != 2 || before[0].Scope != "account" || before[1].Scope != "pool" || before[0].State != QuotaStateUnknown || before[1].State != QuotaStateUnknown {
		t.Fatalf("first insertion before views: %+v", before)
	}
}

func TestQuotaCoreResetHintsPreserveEvidence(t *testing.T) {
	at := quotaCoreTime()
	for _, tc := range []struct {
		name  string
		kind  QuotaEvidence
		reset time.Duration
		cap   time.Duration
	}{
		{"later hint does not extend positive cap", QuotaReportedAvailable, 3 * time.Hour, time.Hour},
		{"earlier hint shortens positive cap", QuotaReportedAvailable, 30 * time.Minute, 30 * time.Minute},
		{"elapsed hint invalidates positive", QuotaReportedAvailable, 30 * time.Second, 30 * time.Second},
		{"full keeps acquisition", QuotaReportedFull, 3 * time.Hour, 3 * time.Hour},
		{"refusal keeps anchor", QuotaConfirmedExhaustion, 3 * time.Hour, 3 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(tc.kind, at)))
			f := quotaCoreFact(QuotaResetHint, at.Add(time.Minute))
			f.ResetAt = at.Add(tc.reset)
			next := quotaCoreReduce(t, &r, quotaCoreObservation(1, at.Add(time.Minute), f))
			want := cloneQuotaWindow(r.Windows[0])
			want.ResetAt, want.UpdatedAt = at.Add(tc.cap), at.Add(time.Minute)
			if !reflect.DeepEqual(next.Windows[0], want) {
				t.Fatalf("hint changed evidence: got %+v, want %+v", next.Windows[0], want)
			}
			if tc.kind == QuotaReportedAvailable && quotaCoreView(&next, at.Add(time.Hour)).State != QuotaStateUnknown {
				t.Fatal("hint extended availability")
			}
			late := quotaCoreFact(QuotaReportedAvailable, at.Add(30*time.Second))
			if _, err := reduceQuota(&next, quotaCoreObservation(2, at.Add(30*time.Second), late), at.Add(time.Minute)); !errors.Is(err, ErrQuotaConflict) {
				t.Fatalf("fact older than last hint accepted: %v", err)
			}
		})
	}
	f := quotaCoreFact(QuotaReportedAvailable, at)
	f.ResetAt = time.Time{}
	r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, f))
	hint := quotaCoreFact(QuotaResetHint, at.Add(time.Minute))
	r = quotaCoreReduce(t, &r, quotaCoreObservation(1, at.Add(time.Minute), hint))
	if !r.Windows[0].ResetAt.Equal(hint.ResetAt) {
		t.Fatal("new cap was not installed")
	}
	later := quotaCoreFact(QuotaResetHint, at.Add(3*time.Hour))
	r = quotaCoreReduce(t, &r, quotaCoreObservation(2, at.Add(3*time.Hour), later))
	if quotaCoreView(&r, at.Add(3*time.Hour)).State != QuotaStateUnknown || !r.Windows[0].ResetAt.Equal(hint.ResetAt) {
		t.Fatal("hint revived expired evidence")
	}
}

func TestQuotaCoreOverlappingWindows(t *testing.T) {
	at := quotaCoreTime()
	coverage := []QuotaCoverage{
		{Window: "week", Scopes: []string{"pool-a", "pool-b"}},
		{Window: "day", Scopes: []string{"pool-a"}},
		{Window: "other", Scopes: []string{"pool-c"}},
	}
	day := quotaCoreFact(QuotaReportedAvailable, at)
	o := quotaCoreObservation(0, at, day)
	o.Coverage = coverage
	r := quotaCoreReduce(t, nil, o)
	view := func(scope string) QuotaSnapshot {
		return quotaView(&r, o.Subject, scope, QuotaVersion{StoreEpoch: "epoch-1", Revision: r.Revision}, at.Add(10*time.Minute))
	}
	if v := view("pool-a"); v.State != QuotaStateUnknown || len(v.Windows) != 2 || v.Windows[1].State != QuotaStateUnknown {
		t.Fatalf("missing weekly reading grants coverage: %+v", v)
	}
	week := quotaCoreFact(QuotaConfirmedExhaustion, at.Add(time.Minute))
	week.Window, week.EvidenceID = "week", "evidence-week"
	o = quotaCoreObservation(1, at.Add(time.Minute), week)
	o.Coverage = coverage
	r = quotaCoreReduce(t, &r, o)
	if view("pool-a").State != QuotaStateExhausted || view("pool-b").State != QuotaStateExhausted || view("pool-c").State != QuotaStateUnknown {
		t.Fatal("overlap or pool isolation lost")
	}
	day = quotaCoreFact(QuotaConfirmedExhaustion, at.Add(2*time.Minute))
	o = quotaCoreObservation(2, at.Add(2*time.Minute), day)
	o.Coverage = coverage
	r = quotaCoreReduce(t, &r, o)
	o = quotaCoreObservation(3, at.Add(3*time.Minute), quotaCoreRecovery(r.Windows[0], at.Add(3*time.Minute)))
	o.Coverage = coverage
	r = quotaCoreReduce(t, &r, o)
	if v := view("pool-a"); v.State != QuotaStateExhausted || v.Windows[0].State != QuotaStateAvailable || v.Windows[1].Blocker == nil {
		t.Fatalf("day recovery cleared week: %+v", v)
	}
	other := quotaCoreFact(QuotaReportedAvailable, at.Add(4*time.Minute))
	other.Window, other.EvidenceID = "other", "evidence-other"
	o = quotaCoreObservation(4, at.Add(4*time.Minute), other)
	o.Coverage = coverage
	r = quotaCoreReduce(t, &r, o)
	if view("pool-c").State != QuotaStateAvailable || view("pool-a").State != QuotaStateExhausted {
		t.Fatal("unrelated pool changed")
	}
	i := slices.IndexFunc(r.Windows, func(w quotaWindow) bool { return w.Window == "week" })
	o = quotaCoreObservation(5, at.Add(5*time.Minute), quotaCoreRecovery(r.Windows[i], at.Add(5*time.Minute)))
	o.Coverage = coverage
	r = quotaCoreReduce(t, &r, o)
	if view("pool-a").State != QuotaStateAvailable || view("pool-b").State != QuotaStateAvailable || view("pool-c").State != QuotaStateAvailable {
		t.Fatal("all covered recoveries did not resolve aggregate")
	}
	if v := view("pool-a"); v.Version.Revision != 6 || v.Version.StoreEpoch != "epoch-1" {
		t.Fatalf("version stamp lost: %+v", v.Version)
	}
}

func TestQuotaCoreRefusalSurvivesTelemetryAndSerialization(t *testing.T) {
	at := quotaCoreTime()
	r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(QuotaConfirmedExhaustion, at)))
	anchor := *r.Windows[0].Blocker
	r = quotaCoreReduce(t, &r, quotaCoreObservation(1, at.Add(time.Minute), quotaCoreFact(QuotaReportedFull, at.Add(time.Minute))))
	// The real store's reopen tests cover disk I/O; this round-trip independently
	// proves that retaining the blocker does not depend on process-only state.
	var reopened quotaRecord
	if err := json.Unmarshal([]byte(quotaCoreJSON(t, r)), &reopened); err != nil {
		t.Fatal(err)
	}
	r = quotaCoreReduce(t, &reopened, quotaCoreObservation(2, at.Add(2*time.Minute), quotaCoreFact(QuotaReportedAvailable, at.Add(2*time.Minute))))
	if v := quotaCoreView(&r, at.Add(2*time.Minute)); v.State != QuotaStateExhausted || !reflect.DeepEqual(v.Windows[0].Blocker, &anchor) {
		t.Fatalf("weaker telemetry erased refusal: %+v", v)
	}
	f := quotaCoreRecovery(r.Windows[0], at.Add(3*time.Minute))
	f.Cycle, f.Recovery.Basis = "cycle-2", "cycle"
	next := quotaCoreReduce(t, &r, quotaCoreObservation(3, at.Add(3*time.Minute), f))
	if v := quotaCoreView(&next, at.Add(3*time.Minute)); v.State != QuotaStateAvailable || v.Windows[0].Blocker != nil {
		t.Fatalf("valid recovery did not clear refusal: %+v", v)
	}
	// A second confirmed refusal supersedes the original anchor, even if later
	// weaker telemetry still carries the first cycle's label.
	newAt := at.Add(4 * time.Minute)
	r = quotaCoreReduce(t, &r, quotaCoreObservation(3, newAt, quotaCoreFact(QuotaConfirmedExhaustion, newAt)))
	f = quotaCoreRecovery(r.Windows[0], at.Add(5*time.Minute))
	f.Recovery.BlockerID, f.Recovery.BlockerRevision = anchor.ID, anchor.Revision
	if _, err := reduceQuota(&r, quotaCoreObservation(4, at.Add(5*time.Minute), f), at.Add(5*time.Minute)); !errors.Is(err, ErrQuotaConflict) {
		t.Fatalf("superseded blocker recovery: %v", err)
	}
}

func TestQuotaCoreRecoveryReferences(t *testing.T) {
	at := quotaCoreTime()
	for _, kind := range []QuotaEvidence{QuotaReportedFull, QuotaConfirmedExhaustion} {
		t.Run(string(kind), func(t *testing.T) {
			r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(kind, at)))
			// Another window's update advances the subject revision, not this blocker.
			r.Revision = 2
			for _, tc := range []struct {
				name string
				edit func(*QuotaFact)
				want error
			}{
				{"wrong id", func(f *QuotaFact) { f.Recovery.BlockerID = "different" }, ErrQuotaConflict},
				{"wrong revision", func(f *QuotaFact) { f.Recovery.BlockerRevision = 2 }, ErrQuotaConflict},
				{"wrong prior cycle", func(f *QuotaFact) { f.Recovery.PriorCycle = "other-cycle" }, ErrQuotaConflict},
				{"same cycle is not a renewal", func(f *QuotaFact) { f.Recovery.Basis = "cycle" }, ErrQuotaInvalid},
				{"missing provenance", func(f *QuotaFact) { f.Recovery.Provenance = "" }, ErrQuotaInvalid},
				{"missing positive capacity", func(f *QuotaFact) { f.Capacity = nil }, ErrQuotaInvalid},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := quotaCoreRecovery(r.Windows[0], at.Add(time.Minute))
					tc.edit(&f)
					before := quotaCoreJSON(t, r)
					if _, err := reduceQuota(&r, quotaCoreObservation(2, at.Add(time.Minute), f), at.Add(time.Minute)); !errors.Is(err, tc.want) {
						t.Fatalf("got %v, want %v", err, tc.want)
					}
					if quotaCoreJSON(t, r) != before {
						t.Fatal("rejected recovery changed current state")
					}
				})
			}
			f := quotaCoreRecovery(r.Windows[0], at.Add(time.Minute))
			next := quotaCoreReduce(t, &r, quotaCoreObservation(2, at.Add(time.Minute), f))
			if quotaCoreView(&next, at.Add(time.Minute)).State != QuotaStateAvailable {
				t.Fatal("exact superseding correction rejected")
			}
		})
	}
	// Recovery references are window-bound, not merely valid IDs on this subject.
	day, week := quotaCoreFact(QuotaConfirmedExhaustion, at), quotaCoreFact(QuotaConfirmedExhaustion, at)
	week.Window, week.EvidenceID = "week", "evidence-week"
	o := quotaCoreObservation(0, at, day, week)
	o.Coverage = append(o.Coverage, QuotaCoverage{Window: "week", Scopes: []string{"account"}})
	r := quotaCoreReduce(t, nil, o)
	week = quotaCoreFact(QuotaConfirmedExhaustion, at.Add(time.Minute))
	week.Window, week.EvidenceID = "week", "new-week"
	o = quotaCoreObservation(1, at.Add(time.Minute), week)
	o.Coverage = r.Coverage
	r = quotaCoreReduce(t, &r, o)
	f := quotaCoreRecovery(r.Windows[0], at.Add(2*time.Minute))
	f.Window = "week"
	o = quotaCoreObservation(2, at.Add(2*time.Minute), f)
	o.Coverage = r.Coverage
	if _, err := reduceQuota(&r, o, o.ObservedAt); !errors.Is(err, ErrQuotaConflict) {
		t.Fatalf("cross-window recovery: %v", err)
	}
}

func TestQuotaCoreOrderingCoverageAndAtomicity(t *testing.T) {
	at := quotaCoreTime()
	r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(QuotaReportedFull, at)))
	for _, tc := range []struct {
		name string
		edit func(*QuotaObservation)
		want error
	}{
		{"stale revision", func(o *QuotaObservation) { o.Expected.Revision = 0 }, ErrQuotaConflict},
		{"future revision", func(o *QuotaObservation) { o.Expected.Revision = 2 }, ErrQuotaConflict},
		{"same acquisition", func(o *QuotaObservation) { o.ObservedAt = at }, ErrQuotaConflict},
		{"earlier acquisition", func(o *QuotaObservation) { o.ObservedAt = at.Add(-time.Minute) }, ErrQuotaConflict},
		{"future acquisition", func(o *QuotaObservation) { o.ObservedAt = at.Add(2 * time.Minute) }, ErrQuotaInvalid},
		{"changed scopes", func(o *QuotaObservation) { o.Coverage[0].Scopes = []string{"narrower"} }, ErrQuotaInvalid},
		{"missing coverage", func(o *QuotaObservation) { o.Coverage = nil }, ErrQuotaInvalid},
		{"unsupported success", func(o *QuotaObservation) { o.Facts[0].Kind = "http_200" }, ErrQuotaInvalid},
		{"generic refusal", func(o *QuotaObservation) { o.Facts[0].Kind = "http_429" }, ErrQuotaInvalid},
		{"paid is not included", func(o *QuotaObservation) { o.Facts[0].Capacity.Semantics = "paid" }, ErrQuotaInvalid},
		{"empty evidence", func(o *QuotaObservation) { o.Facts = nil }, ErrQuotaInvalid},
		{"expired positive", func(o *QuotaObservation) { o.Facts[0].FreshUntil = o.ObservedAt }, ErrQuotaInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := quotaCoreObservation(1, at.Add(time.Minute), quotaCoreFact(QuotaReportedAvailable, at.Add(time.Minute)))
			tc.edit(&o)
			before, input := quotaCoreJSON(t, r), quotaCoreJSON(t, o)
			if _, err := reduceQuota(&r, o, at.Add(time.Minute)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if quotaCoreJSON(t, r) != before || quotaCoreJSON(t, o) != input {
				t.Fatal("rejected fact mutated input")
			}
		})
	}
	maxed := cloneQuotaRecord(r)
	maxed.Revision = ^uint64(0)
	o := quotaCoreObservation(maxed.Revision, at.Add(time.Minute), quotaCoreFact(QuotaReportedFull, at.Add(time.Minute)))
	if _, err := reduceQuota(&maxed, o, o.ObservedAt); !errors.Is(err, ErrQuotaConflict) {
		t.Fatalf("revision overflow: %v", err)
	}
	o = quotaCoreObservation(1, at, quotaCoreFact(QuotaReportedFull, at))
	if _, err := reduceQuota(nil, o, at); !errors.Is(err, ErrQuotaConflict) {
		t.Fatalf("absent subject nonzero CAS: %v", err)
	}

	// Coverage order is not semantic, and a failed final fact cannot partially
	// apply an earlier recovery in the same observation.
	o = quotaCoreObservation(0, at, quotaCoreFact(QuotaConfirmedExhaustion, at))
	o.Coverage[0].Scopes = []string{"pool", "account"}
	o.Coverage = append(o.Coverage, QuotaCoverage{Window: "week", Scopes: []string{"account"}})
	week := quotaCoreFact(QuotaConfirmedExhaustion, at)
	week.Window, week.EvidenceID = "week", "evidence-week"
	o.Facts = append(o.Facts, week)
	r = quotaCoreReduce(t, nil, o)
	dayRecovery, weekRecovery := quotaCoreRecovery(r.Windows[0], at.Add(time.Minute)), quotaCoreRecovery(r.Windows[1], at.Add(time.Minute))
	weekRecovery.Recovery.BlockerID = "wrong-week"
	o = quotaCoreObservation(1, at.Add(time.Minute), dayRecovery, weekRecovery)
	o.Coverage = quotaCoverageCopy(r.Coverage)
	slices.Reverse(o.Coverage)
	slices.Reverse(o.Coverage[1].Scopes)
	before := quotaCoreJSON(t, r)
	if _, err := reduceQuota(&r, o, o.ObservedAt); !errors.Is(err, ErrQuotaConflict) {
		t.Fatalf("partial commit: %v", err)
	}
	if quotaCoreJSON(t, r) != before {
		t.Fatal("failed observation partially recovered day")
	}
	o.Facts = o.Facts[:1]
	next := quotaCoreReduce(t, &r, o)
	if next.Windows[0].State != QuotaStateAvailable || next.Windows[1].State != QuotaStateExhausted {
		t.Fatal("canonical coverage or omitted window lost")
	}
	// Evidence acquired at one time for disjoint windows is not reordered within
	// either window; CAS still protects the subject as a whole.
	o = quotaCoreObservation(2, at.Add(time.Minute), quotaCoreRecovery(next.Windows[1], at.Add(time.Minute)))
	o.Coverage = next.Coverage
	next = quotaCoreReduce(t, &next, o)
	if quotaCoreView(&next, o.ObservedAt).State != QuotaStateAvailable {
		t.Fatal("disjoint same-time recovery rejected")
	}
}

func TestQuotaCoreIdentityGenerations(t *testing.T) {
	at := quotaCoreTime()
	for _, kind := range []QuotaEvidence{QuotaReportedAvailable, QuotaConfirmedExhaustion} {
		for _, tc := range []struct {
			name string
			edit func(*QuotaSubject)
		}{
			{"authority", func(s *QuotaSubject) { s.Authority = "authority-2" }},
			{"provider incarnation", func(s *QuotaSubject) { s.Provider = "provider-2" }},
			{"principal incarnation", func(s *QuotaSubject) { s.Principal = "principal-2" }},
			{"evidence generation", func(s *QuotaSubject) { s.Generation = "generation-2" }},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				o := quotaCoreObservation(0, at, quotaCoreFact(kind, at))
				r := quotaCoreReduce(t, nil, o)
				changed := o.Subject
				tc.edit(&changed)
				v := quotaView(&r, changed, "account", QuotaVersion{StoreEpoch: "epoch-1"}, at)
				if v.State != QuotaStateUnknown || len(v.Windows) != 0 || v.Subject != changed {
					t.Fatalf("identity inherited old state: %+v", v)
				}
				o = quotaCoreObservation(1, at.Add(time.Minute), quotaCoreFact(QuotaReportedAvailable, at.Add(time.Minute)))
				o.Subject = changed
				if _, err := reduceQuota(&r, o, o.ObservedAt); !errors.Is(err, ErrQuotaConflict) {
					t.Fatalf("old identity accepted replacement: %v", err)
				}
				o.Expected.Revision = 0
				replacement := quotaCoreReduce(t, nil, o)
				if replacement.Revision != 1 || replacement.Subject != changed || replacement.Windows[0].Blocker != nil {
					t.Fatal("replacement carried prior identity state")
				}
				if quotaCoreView(&r, at).State != r.Windows[0].State {
					t.Fatal("replacement changed old generation")
				}
			})
		}
	}
}

func TestQuotaCoreCopiesAndSnapshotProvenance(t *testing.T) {
	at := quotaCoreTime()
	day, week := quotaCoreFact(QuotaConfirmedExhaustion, at), quotaCoreFact(QuotaConfirmedExhaustion, at)
	week.Window, week.EvidenceID = "week", "evidence-week"
	o := quotaCoreObservation(0, at, day, week)
	o.Coverage[0].Scopes = []string{"pool", "account"}
	o.Coverage = append(o.Coverage, QuotaCoverage{Window: "week", Scopes: []string{"account"}})
	r := quotaCoreReduce(t, nil, o)
	// The reducer must neither manufacture nor replace the store's replay ledger.
	if r.LastID != "" || r.Digest != "" {
		t.Fatal("reducer populated the store replay ledger")
	}
	r.LastID, r.Digest = "store-operation", strings.Repeat("b", 64)
	recovery := quotaCoreRecovery(r.Windows[0], at.Add(time.Minute))
	o = quotaCoreObservation(1, at.Add(time.Minute), recovery)
	o.Coverage = quotaCoverageCopy(r.Coverage)
	next := quotaCoreReduce(t, &r, o)
	if next.LastID != r.LastID || next.Digest != r.Digest {
		t.Fatal("reducer replaced store replay ledger")
	}
	baseline := quotaCoreJSON(t, next)
	o.Coverage[0].Scopes[0] = "input-mutated"
	o.Facts[0].Capacity.Remaining = 1
	o.Facts[0].Recovery.Provenance = "input-mutated"
	r.Windows[1].Blocker.ID = "old-mutated"
	if quotaCoreJSON(t, next) != baseline {
		t.Fatal("result aliases observation or previous record")
	}

	for _, tc := range []struct {
		name string
		edit func(*quotaRecord)
	}{
		{"coverage list", func(r *quotaRecord) { r.Coverage[0].Window = "changed" }},
		{"coverage scopes", func(r *quotaRecord) { r.Coverage[0].Scopes[0] = "changed" }},
		{"window list", func(r *quotaRecord) { r.Windows[0].Window = "changed" }},
		{"capacity", func(r *quotaRecord) { r.Windows[0].Capacity.Remaining = 0 }},
		{"recovery", func(r *quotaRecord) { r.Windows[0].Recovery.Provenance = "changed" }},
		{"blocker", func(r *quotaRecord) { r.Windows[1].Blocker.ID = "changed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := cloneQuotaRecord(next)
			if !reflect.DeepEqual(copy, next) {
				t.Fatal("clone changed data")
			}
			tc.edit(&copy)
			if quotaCoreJSON(t, next) != baseline {
				t.Fatal("clone shares nested data")
			}
		})
	}
	// Clone is not a canonicalizer: preserve even the caller's slice ordering.
	unsorted := cloneQuotaRecord(next)
	slices.Reverse(unsorted.Coverage)
	slices.Reverse(unsorted.Coverage[1].Scopes)
	if !reflect.DeepEqual(cloneQuotaRecord(unsorted), unsorted) {
		t.Fatal("clone reordered input")
	}
	version := QuotaVersion{StoreEpoch: "epoch-1", Revision: next.Revision}
	views := quotaViews(&next, next.Subject, next.Coverage, version, at.Add(time.Minute))
	if len(views) != 2 || views[0].Scope != "account" || views[1].Scope != "pool" {
		t.Fatalf("scope projection: %+v", views)
	}
	w := next.Windows[0]
	v := views[0].Windows[0]
	if v.ObservationID != w.ObservationID || v.Revision != w.Revision || v.Source != w.Source || v.EvidenceID != w.EvidenceID || v.Cycle != w.Cycle || !reflect.DeepEqual(v.Recovery, w.Recovery) {
		t.Fatalf("snapshot lost recovery provenance: %+v", v)
	}
	beforePool := quotaCoreJSON(t, views[1])
	views[0].Windows[0].Capacity.Remaining = 0
	views[0].Windows[0].Recovery.Provenance = "view-mutated"
	views[0].Windows[1].Blocker.ID = "view-mutated"
	views[0].Windows[0].Window = "view-mutated"
	if quotaCoreJSON(t, next) != baseline || quotaCoreJSON(t, views[1]) != beforePool {
		t.Fatal("snapshots alias record or sibling scope")
	}
	again := quotaView(&next, next.Subject, "pool", version, at.Add(time.Minute))
	if quotaCoreJSON(t, again) != beforePool {
		t.Fatal("snapshot mutation changed later read")
	}
}

func TestQuotaCoreHintsRejectContradictoryReset(t *testing.T) {
	at := quotaCoreTime()
	for _, state := range []string{"positive", "recovered", "positive-with-blocker"} {
		t.Run(state, func(t *testing.T) {
			r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(QuotaReportedAvailable, at)))
			if state != "positive" {
				r = quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(QuotaConfirmedExhaustion, at)))
				f := quotaCoreFact(QuotaReportedAvailable, at.Add(time.Minute))
				if state == "recovered" {
					f = quotaCoreRecovery(r.Windows[0], at.Add(time.Minute))
				}
				r = quotaCoreReduce(t, &r, quotaCoreObservation(1, at.Add(time.Minute), f))
			}
			for _, reset := range []time.Time{{}, r.Windows[0].ObservedAt.Add(-time.Nanosecond), r.Windows[0].ObservedAt} {
				f := quotaCoreFact(QuotaResetHint, at.Add(2*time.Minute))
				f.ResetAt = reset
				o := quotaCoreObservation(r.Revision, at.Add(2*time.Minute), f)
				before := quotaCoreJSON(t, r)
				if _, err := reduceQuota(&r, o, o.ObservedAt); !errors.Is(err, ErrQuotaInvalid) {
					t.Fatalf("reset %s accepted: %v", reset, err)
				}
				if quotaCoreJSON(t, r) != before {
					t.Fatal("invalid hint changed evidence")
				}
			}
		})
	}
}
