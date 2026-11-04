package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func quotaSnapshotCheck(t *testing.T, record quotaRecord, scope string, at time.Time) QuotaSnapshot {
	t.Helper()
	v := quotaView(&record, record.Subject, scope, QuotaVersion{StoreEpoch: "epoch", Revision: record.Revision}, at)
	before, coverage := quotaCoreJSON(t, v), quotaCoreJSON(t, record.Coverage)
	got, c, err := CloneQuotaSnapshot(v, record.Coverage, at)
	if err != nil || !reflect.DeepEqual(got, v) || !reflect.DeepEqual(c, record.Coverage) {
		t.Fatalf("checked copy differs: %+v %v", got, err)
	}
	if quotaCoreJSON(t, v) != before || quotaCoreJSON(t, record.Coverage) != coverage {
		t.Fatal("checked copy mutated inputs")
	}
	return got
}

func TestQuotaSnapshotHistoricalViews(t *testing.T) {
	at := quotaCoreTime()
	for _, kind := range []QuotaEvidence{QuotaReportedAvailable, QuotaReportedFull, QuotaConfirmedExhaustion, QuotaResetHint, QuotaConfirmedRecovery} {
		t.Run(string(kind), func(t *testing.T) {
			fact := quotaCoreFact(kind, at)
			var old *quotaRecord
			revision := uint64(0)
			if kind == QuotaConfirmedRecovery {
				prior := quotaCoreReduce(t, nil, quotaCoreObservation(0, at.Add(-time.Minute), quotaCoreFact(QuotaConfirmedExhaustion, at.Add(-time.Minute))))
				old = &prior
				revision = 1
				fact.Recovery = &QuotaRecovery{BlockerID: prior.Windows[0].ObservationID, BlockerRevision: 1, PriorCycle: "cycle-1", Basis: "cycle", Provenance: "recovery-proof"}
				fact.Cycle = "cycle-2"
			}
			r := quotaCoreReduce(t, old, quotaCoreObservation(revision, at, fact))
			for _, offset := range []time.Duration{-time.Nanosecond, 0, 30 * time.Minute, time.Hour, 2 * time.Hour, 365 * 24 * time.Hour} {
				v := quotaSnapshotCheck(t, r, "account", at.Add(offset))
				want := QuotaStateUnknown
				if kind == QuotaReportedFull || kind == QuotaConfirmedExhaustion {
					want = QuotaStateExhausted
				}
				if (kind == QuotaReportedAvailable || kind == QuotaConfirmedRecovery) && offset >= 0 && offset < time.Hour {
					want = QuotaStateAvailable
				}
				if v.State != want || v.RecheckDue != (offset >= time.Hour) {
					t.Fatalf("at %s: state=%s due=%v", offset, v.State, v.RecheckDue)
				}
			}
			v := quotaSnapshotCheck(t, r, "uncovered", at)
			if v.State != QuotaStateUnknown || len(v.Windows) != 0 {
				t.Fatal("uncovered materialized scope became evidence")
			}
		})
	}
}

func TestQuotaSnapshotCompleteCoverageAndPlaceholders(t *testing.T) {
	at := quotaCoreTime()
	o := quotaCoreObservation(0, at, quotaCoreFact(QuotaReportedAvailable, at))
	o.Coverage = append(o.Coverage, QuotaCoverage{Window: "week", Scopes: []string{"account", "other"}})
	r := quotaCoreReduce(t, nil, o)
	v := quotaSnapshotCheck(t, r, "account", at)
	if v.State != QuotaStateUnknown || len(v.Windows) != 2 || v.Windows[1].Kind != "" {
		t.Fatal("missing window was not retained as unknown")
	}
	v.Windows = v.Windows[:1]
	v.State = QuotaStateAvailable
	quotaSnapshotInvalid(t, v, r.Coverage, at)
	// Completeness is relative to supplied coverage, not authentication of it.
	if _, _, err := CloneQuotaSnapshot(v, r.Coverage[:1], at); err != nil {
		t.Fatalf("internally complete view rejected: %v", err)
	}
	v = quotaSnapshotCheck(t, r, "account", at)
	v.Windows[1].ObservationID = "invented"
	quotaSnapshotInvalid(t, v, r.Coverage, at)
}

func quotaSnapshotInvalid(t *testing.T, v QuotaSnapshot, c []QuotaCoverage, at time.Time) {
	t.Helper()
	got, coverage, err := CloneQuotaSnapshot(v, c, at)
	if !errors.Is(err, ErrQuotaInvalid) || !reflect.DeepEqual(got, QuotaSnapshot{}) || coverage != nil {
		t.Fatalf("invalid snapshot returned usable result: %+v %v %v", got, coverage, err)
	}
}

func TestQuotaSnapshotRejectsInconsistentViews(t *testing.T) {
	at := quotaCoreTime()
	o := quotaStoreObservation("epoch", "one", "first")
	at = o.ObservedAt
	r := quotaCoreReduce(t, nil, o)
	cases := map[string]func(*QuotaSnapshot, *[]QuotaCoverage, *time.Time){
		"authority":        func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Subject.Authority = "" },
		"provider":         func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Subject.Provider = "" },
		"principal":        func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Subject.Principal = "" },
		"generation":       func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Subject.Generation = "bad generation" },
		"epoch":            func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Version.StoreEpoch = "" },
		"not materialized": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Version.Revision = 0 },
		"scope":            func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Scope = "other" },
		"invalid scope":    func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Scope = "" },
		"drop window":      func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows = v.Windows[:1] },
		"duplicate window": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[1] = v.Windows[0] },
		"drop coverage":    func(_ *QuotaSnapshot, c *[]QuotaCoverage, _ *time.Time) { *c = (*c)[:1] },
		"missing coverage": func(_ *QuotaSnapshot, c *[]QuotaCoverage, _ *time.Time) { *c = nil },
		"duplicate scope": func(_ *QuotaSnapshot, c *[]QuotaCoverage, _ *time.Time) {
			(*c)[0].Scopes = append((*c)[0].Scopes, (*c)[0].Scopes[0])
		},
		"wrong aggregate":       func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.State = QuotaStateUnknown },
		"wrong due":             func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.RecheckDue = true },
		"wrong window due":      func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].RecheckDue = true },
		"wrong window state":    func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].State = QuotaStateExhausted },
		"missing fact metadata": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Source = "" },
		"missing evidence":      func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].EvidenceID = "" },
		"future revision":       func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Revision++ },
		"zero fact revision":    func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Revision = 0 },
		"unknown kind":          func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Kind = "future" },
		"absent kind with retained metadata": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) {
			v.Windows[0].Kind = ""
			v.Windows[0].State = QuotaStateUnknown
		},
		"missing capacity":          func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Capacity = nil },
		"paid wallet":               func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Capacity.Semantics = "paid" },
		"nan":                       func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Capacity.Remaining = math.NaN() },
		"inf":                       func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Capacity.Limit = math.Inf(1) },
		"negative":                  func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Capacity.Remaining = -1 },
		"full contradicts positive": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Capacity.Remaining = 0 },
		"expired interval": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) {
			v.Windows[0].FreshUntil = v.Windows[0].ObservedAt
		},
		"same operation ID":     func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].ObservationID = "different" },
		"same operation source": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) { v.Windows[0].Source = "different" },
		"same operation time": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) {
			v.Windows[0].ObservedAt = v.Windows[0].ObservedAt.Add(-time.Second)
		},
		"duplicate operation evidence": func(v *QuotaSnapshot, _ *[]QuotaCoverage, _ *time.Time) {
			v.Windows[0].EvidenceID = v.Windows[1].EvidenceID
		},
		"zero evaluation": func(_ *QuotaSnapshot, _ *[]QuotaCoverage, a *time.Time) { *a = time.Time{} },
		"unrepresentable evaluation": func(_ *QuotaSnapshot, _ *[]QuotaCoverage, a *time.Time) {
			*a = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		},
		"stale is not available": func(_ *QuotaSnapshot, _ *[]QuotaCoverage, a *time.Time) { *a = a.Add(time.Hour) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			v := quotaSnapshotCheck(t, r, "models", at)
			c := quotaCoverageCopy(r.Coverage)
			a := at
			change(&v, &c, &a)
			quotaSnapshotInvalid(t, v, c, a)
		})
	}
	v := quotaSnapshotCheck(t, r, "models", at.Add(3*time.Hour))
	v.Windows[0].Capacity.Remaining = -1
	quotaSnapshotInvalid(t, v, r.Coverage, at.Add(3*time.Hour))
}

func quotaSnapshotAnchoredRecord(t *testing.T) (quotaRecord, time.Time) {
	t.Helper()
	o := quotaStoreObservation("epoch", "one", "first")
	for i := range o.Facts {
		o.Facts[i].Kind = QuotaConfirmedExhaustion
		o.Facts[i].Capacity = nil
		o.Facts[i].FreshUntil = time.Time{}
	}
	r := quotaCoreReduce(t, nil, o)
	next := quotaStoreObservation("epoch", "one", "second")
	next.Expected.Revision = 1
	next.ObservedAt = o.ObservedAt.Add(time.Minute)
	for i := range next.Facts {
		f := &next.Facts[i]
		if f.Window == "day" {
			f.Kind = QuotaConfirmedRecovery
			f.Cycle = "next-cycle"
			f.Recovery = &QuotaRecovery{BlockerID: "first", BlockerRevision: 1, PriorCycle: "cycle", Basis: "cycle", Provenance: "proof"}
		}
	}
	return quotaCoreReduce(t, &r, next), next.ObservedAt
}

func TestQuotaSnapshotRecoveryAnchorsAndIsolation(t *testing.T) {
	r, at := quotaSnapshotAnchoredRecord(t)
	v := quotaSnapshotCheck(t, r, "models", at)
	if v.State != QuotaStateExhausted || v.Windows[0].Recovery == nil || v.Windows[1].Blocker == nil {
		t.Fatal("recovery removed another window's refusal")
	}
	c := quotaCoverageCopy(r.Coverage)
	original := quotaCoreJSON(t, v)
	copied, coverage, err := CloneQuotaSnapshot(v, c, at)
	if err != nil {
		t.Fatal(err)
	}
	v.Windows[0].Capacity.Remaining = 1
	v.Windows[0].Recovery.Provenance = "changed"
	v.Windows[1].Blocker.ID = "changed"
	c[0].Scopes[0] = "changed"
	c[0].Window = "changed"
	if quotaCoreJSON(t, copied) != original || coverage[0].Scopes[0] == "changed" || coverage[0].Window == "changed" {
		t.Fatal("original mutation reached detached copy")
	}
	again, _, err := CloneQuotaSnapshot(copied, coverage, at)
	if err != nil {
		t.Fatal(err)
	}
	copied.Windows[0].Capacity.Unit = "changed"
	copied.Windows[0].Recovery.BlockerID = "changed"
	copied.Windows[1].Blocker.EvidenceID = "changed"
	if quotaCoreJSON(t, again) != original {
		t.Fatal("copy mutation reached subsequent checked copy")
	}
	cases := map[string]func(*QuotaSnapshot){
		"wrong recovery id":       func(v *QuotaSnapshot) { v.Windows[0].Recovery.BlockerID = "not-first" },
		"wrong recovery revision": func(v *QuotaSnapshot) { v.Windows[0].Recovery.BlockerRevision = 2 },
		"same cycle":              func(v *QuotaSnapshot) { v.Windows[0].Recovery.PriorCycle = v.Windows[0].Cycle },
		"missing provenance":      func(v *QuotaSnapshot) { v.Windows[0].Recovery.Provenance = "" },
		"anchor source":           func(v *QuotaSnapshot) { v.Windows[1].Blocker.Source = "" },
		"anchor revision":         func(v *QuotaSnapshot) { v.Windows[1].Blocker.Revision = 2 },
		"anchor time":             func(v *QuotaSnapshot) { v.Windows[1].Blocker.ObservedAt = at },
		"refusal cleared":         func(v *QuotaSnapshot) { v.Windows[1].State = QuotaStateAvailable; v.State = QuotaStateAvailable },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := quotaSnapshotCheck(t, r, "models", at)
			mutate(&v)
			quotaSnapshotInvalid(t, v, r.Coverage, at)
		})
	}
	// Equal instants with distinct locations are the same operation, including
	// the retained refusal anchor. Struct equality would incorrectly reject it.
	v = quotaSnapshotCheck(t, r, "models", at)
	v.Windows[1].ObservedAt = v.Windows[1].ObservedAt.In(time.FixedZone("offset", 3600))
	v.Windows[1].Blocker.ObservedAt = v.Windows[1].Blocker.ObservedAt.In(time.FixedZone("offset", 3600))
	if _, _, err := CloneQuotaSnapshot(v, r.Coverage, at); err != nil {
		t.Fatalf("equal instants rejected: %v", err)
	}
}

func TestQuotaSnapshotHintDoesNotInventHistory(t *testing.T) {
	at := quotaCoreTime()
	r := quotaCoreReduce(t, nil, quotaCoreObservation(0, at, quotaCoreFact(QuotaReportedAvailable, at)))
	hint := quotaCoreFact(QuotaResetHint, at.Add(time.Minute))
	hint.ResetAt = at.Add(30 * time.Minute)
	r = quotaCoreReduce(t, &r, quotaCoreObservation(1, at.Add(time.Minute), hint))
	// This passes despite the later hint acceptance: a snapshot omits UpdatedAt
	// and cannot prove the latest reset value already existed at evaluation.
	v := quotaSnapshotCheck(t, r, "account", at)
	if v.Windows[0].Revision != 1 || v.Version.Revision != 2 || !v.Windows[0].ResetAt.Equal(hint.ResetAt) {
		t.Fatal("hint metadata was invented or discarded")
	}
}

func TestQuotaSnapshotConcurrentImmutableReads(t *testing.T) {
	r, at := quotaSnapshotAnchoredRecord(t)
	v := quotaSnapshotCheck(t, r, "models", at)
	before := quotaCoreJSON(t, v)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 25 {
				got, c, err := CloneQuotaSnapshot(v, r.Coverage, at)
				if err != nil {
					t.Error(err)
					return
				}
				got.Windows[0].Capacity.Remaining = 0
				got.Windows[0].Recovery.Provenance = "mutated"
				got.Windows[1].Blocker.ID = "mutated"
				c[0].Scopes[0] = "mutated"
			}
		})
	}
	wg.Wait()
	if quotaCoreJSON(t, v) != before {
		t.Fatal("concurrent copies share mutable data")
	}
}

func TestQuotaStrictJSONExtractionParity(t *testing.T) {
	good := `{"version":1,"epoch":"epoch","commit_sequence":0,"records":[]}`
	cases := []struct {
		name, input string
		want        error
	}{
		{"v1 golden", good, nil},
		{"escaped-surrogate remains accepted", strings.Replace(good, `"epoch":"epoch"`, `"epoch":"\ud800"`, 1), nil},
		{"future nulls and fields", `{"version":2,"future":null,"records":null}`, ErrQuotaUnsupported},
		{"future plus alias", `{"version":2,"Version":1}`, ErrQuotaUnsupported},
		{"future case-only version", `{"Version":2}`, ErrQuotaCorrupt},
		{"null version", `{"version":null}`, ErrQuotaCorrupt},
		{"float version", `{"version":2.0}`, ErrQuotaCorrupt},
		{"escaped exact version", `{"\u0076ersion":2}`, ErrQuotaUnsupported},
		{"duplicate future", `{"version":2,"version":2}`, ErrQuotaCorrupt},
		{"escaped duplicate future", `{"version":2,"\u0076ersion":2}`, ErrQuotaCorrupt},
		{"nested duplicate future", `{"version":2,"future":{"a":1,"a":2}}`, ErrQuotaCorrupt},
		{"future malformed", `{"version":2,}`, ErrQuotaCorrupt},
		{"future trailing", `{"version":2} {}`, ErrQuotaCorrupt},
		{"future UTF8", `{"version":2,"future":"` + string([]byte{0xff}) + `"}`, ErrQuotaCorrupt},
		{"depth 64", `{"version":2,"future":` + strings.Repeat("[", 63) + "0" + strings.Repeat("]", 63) + `}`, ErrQuotaUnsupported},
		{"depth 65", `{"version":2,"future":` + strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64) + `}`, ErrQuotaCorrupt},
		{"v1 alias", strings.Replace(good, `"epoch"`, `"Epoch"`, 1), ErrQuotaCorrupt},
		{"v1 missing zero", strings.Replace(good, `"commit_sequence":0,`, ``, 1), ErrQuotaCorrupt},
		{"v1 null", strings.Replace(good, `[]`, `null`, 1), ErrQuotaCorrupt},
		{"v1 unknown", strings.TrimSuffix(good, `}`) + `,"future":null}`, ErrQuotaCorrupt},
		{"no billing size cap", `{"version":2,"future":"` + strings.Repeat("x", (1<<20)+1) + `"}`, ErrQuotaUnsupported},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeQuotaFile([]byte(c.input))
			if !errors.Is(err, c.want) {
				t.Fatalf("error %v; want %v", err, c.want)
			}
			if err != nil && !reflect.DeepEqual(got, quotaFile{}) {
				t.Fatal("partial decode escaped")
			}
			if c.name == "v1 golden" {
				encoded, err := json.Marshal(got)
				if err != nil || !bytes.Equal(encoded, []byte(good)) {
					t.Fatal("v1 golden bytes changed")
				}
			}
		})
	}
}

func TestQuotaSnapshotRetainedOperationsAndHistoryLimits(t *testing.T) {
	o := quotaStoreObservation("epoch", "one", "first")
	for i := range o.Facts {
		o.Facts[i].Kind = QuotaConfirmedExhaustion
		o.Facts[i].Capacity = nil
		o.Facts[i].FreshUntil = time.Time{}
	}
	r := quotaCoreReduce(t, nil, o)
	next := quotaStoreObservation("epoch", "one", "second")
	next.Expected.Revision = 1
	next.ObservedAt = o.ObservedAt.Add(time.Minute)
	r = quotaCoreReduce(t, &r, next)
	for _, field := range []string{"source", "time", "evidence", "id"} {
		t.Run(field, func(t *testing.T) {
			v := quotaSnapshotCheck(t, r, "models", next.ObservedAt)
			switch field {
			case "source":
				v.Windows[0].Blocker.Source = "other-source"
			case "time":
				v.Windows[0].Blocker.ObservedAt = o.ObservedAt.Add(-time.Second)
			case "evidence":
				v.Windows[0].Blocker.EvidenceID = v.Windows[1].Blocker.EvidenceID
			case "id":
				v.Windows[0].Blocker.ID = "other-id"
			}
			quotaSnapshotInvalid(t, v, r.Coverage, next.ObservedAt)
		})
	}
	// No earlier cleared blocker is present in a recovered-only scope. A
	// well-shaped reference is not proof that it was the actual superseded ID.
	recovered, at := quotaSnapshotAnchoredRecord(t)
	recovered.Coverage = []QuotaCoverage{{Window: "day", Scopes: []string{"models"}}}
	recovered.Windows = recovered.Windows[:1]
	v := quotaSnapshotCheck(t, recovered, "models", at)
	v.Windows[0].Recovery.BlockerID = "unverifiable-prior-id"
	if _, _, err := CloneQuotaSnapshot(v, recovered.Coverage, at); err != nil {
		t.Fatalf("invented prior history requirement: %v", err)
	}
}

func TestQuotaSnapshotMaterializedPlaceholders(t *testing.T) {
	at := quotaCoreTime()
	o := quotaCoreObservation(0, at, quotaCoreFact(QuotaReportedAvailable, at))
	o.Coverage = append(o.Coverage, QuotaCoverage{Window: "week", Scopes: []string{"account"}})
	r := quotaCoreReduce(t, nil, o)
	before := quotaSnapshotCheck(t, r, "account", at.Add(-time.Nanosecond))
	if before.State != QuotaStateUnknown || before.Windows[0].Kind == "" {
		t.Fatal("before-acquisition view lost retained evidence")
	}
	v := quotaSnapshotCheck(t, r, "account", at)
	for i, w := range v.Windows {
		v.Windows[i] = QuotaWindowSnapshot{Window: w.Window, State: QuotaStateUnknown}
	}
	v.State, v.RecheckDue = QuotaStateUnknown, false
	quotaSnapshotInvalid(t, v, r.Coverage, at)

	// Evidence may exist outside the captured scope; do not invent or demand it.
	coverage := append(quotaCoverageCopy(r.Coverage), QuotaCoverage{Window: "other", Scopes: []string{"elsewhere"}})
	got, copied, err := CloneQuotaSnapshot(v, coverage, at)
	if err != nil || !reflect.DeepEqual(got, v) || !reflect.DeepEqual(copied, coverage) {
		t.Fatalf("possible outside-scope materialization rejected: %+v %v", got, err)
	}
}
