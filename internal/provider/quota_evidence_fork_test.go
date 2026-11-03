package provider

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func quotaEvidenceObservation(kind QuotaEvidence) QuotaObservation {
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	fact := QuotaFact{Window: "day", Kind: kind, EvidenceID: "fact-2", Cycle: "cycle-2"}
	switch kind {
	case QuotaReportedFull:
		fact.Capacity = &QuotaCapacity{Semantics: "included", Unit: "requests", Limit: 100}
	case QuotaReportedAvailable, QuotaConfirmedRecovery:
		fact.Capacity = &QuotaCapacity{Semantics: "included", Unit: "requests", Limit: 100, Remaining: 50}
		fact.FreshUntil, fact.ResetAt = at.Add(time.Hour), at.Add(2*time.Hour)
		if kind == QuotaConfirmedRecovery {
			fact.Recovery = &QuotaRecovery{BlockerID: "refusal-1", BlockerRevision: 1, PriorCycle: "cycle-1", Basis: "cycle", Provenance: "proof-2"}
		}
	case QuotaResetHint:
		fact.ResetAt = at.Add(time.Hour)
	}
	return QuotaObservation{
		Subject: QuotaSubject{Authority: "authority", Provider: "provider-instance", Principal: "principal-instance", Generation: "generation"},
		ID:      "observation-2", Expected: QuotaVersion{StoreEpoch: "epoch", Revision: 1}, Source: "adapter", ObservedAt: at,
		Coverage: []QuotaCoverage{{Window: "day", Scopes: []string{"pool"}}, {Window: "week", Scopes: []string{"pool", "shared"}}},
		Facts:    []QuotaFact{fact},
	}
}

func quotaEvidenceRecord(kind QuotaEvidence) quotaRecord {
	o := quotaEvidenceObservation(kind)
	f := o.Facts[0]
	w := quotaWindow{
		Window: f.Window, Kind: kind, ObservationID: o.ID, Revision: 2,
		Source: o.Source, EvidenceID: f.EvidenceID, ObservedAt: o.ObservedAt, UpdatedAt: o.ObservedAt,
		FreshUntil: f.FreshUntil, ResetAt: f.ResetAt, Cycle: f.Cycle, Capacity: f.Capacity, Recovery: f.Recovery,
	}
	switch kind {
	case QuotaReportedFull, QuotaConfirmedExhaustion:
		w.State = QuotaStateExhausted
	case QuotaReportedAvailable, QuotaConfirmedRecovery:
		w.State = QuotaStateAvailable
	case QuotaResetHint:
		w.State = QuotaStateUnknown
	}
	if kind == QuotaConfirmedExhaustion {
		w.Blocker = &QuotaBlocker{ID: o.ID, Revision: 2, Source: o.Source, EvidenceID: f.EvidenceID, Cycle: f.Cycle, ObservedAt: o.ObservedAt}
	}
	return quotaRecord{Subject: o.Subject, Revision: 2, Coverage: o.Coverage, Windows: []quotaWindow{w}, LastID: o.ID, Digest: strings.Repeat("a", 64)}
}

func quotaEvidenceAnchor(record *quotaRecord) {
	w := &record.Windows[0]
	w.State = QuotaStateExhausted
	w.Blocker = &QuotaBlocker{ID: "refusal-1", Revision: 1, Source: "refusal-adapter", EvidenceID: "refusal-fact", Cycle: "cycle-1", ObservedAt: w.ObservedAt.Add(-time.Minute)}
}

func TestQuotaEvidenceAcceptsExplicitNormalizedFacts(t *testing.T) {
	for _, kind := range []QuotaEvidence{QuotaReportedFull, QuotaReportedAvailable, QuotaConfirmedExhaustion, QuotaConfirmedRecovery, QuotaResetHint} {
		t.Run(string(kind), func(t *testing.T) {
			o := quotaEvidenceObservation(kind)
			before, err := json.Marshal(o)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateQuotaObservation(o, o.ObservedAt); err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(o)
			if err != nil || string(before) != string(after) {
				t.Fatalf("validation changed input: %v", err)
			}
		})
	}
	// No vendor is certified by these fixtures, and omitted covered windows are
	// not evidence of availability. The reducer/view owns their unknown state.
	o := quotaEvidenceObservation(QuotaConfirmedRecovery)
	o.Facts[0].Recovery.Basis, o.Facts[0].Recovery.PriorCycle, o.Facts[0].Cycle = "superseding", "", ""
	if err := ValidateQuotaObservation(o, o.ObservedAt); err != nil {
		t.Fatalf("cycle-less explicit superseding evidence: %v", err)
	}
}

func TestQuotaEvidenceRejectsMalformedObservations(t *testing.T) {
	cases := []struct {
		name string
		edit func(*QuotaObservation)
	}{
		{"authority", func(o *QuotaObservation) { o.Subject.Authority = "" }},
		{"provider", func(o *QuotaObservation) { o.Subject.Provider = "" }},
		{"principal", func(o *QuotaObservation) { o.Subject.Principal = "" }},
		{"generation", func(o *QuotaObservation) { o.Subject.Generation = "" }},
		{"space identity", func(o *QuotaObservation) { o.Subject.Principal = " principal " }},
		{"control identity", func(o *QuotaObservation) { o.Subject.Principal = "principal\x00" }},
		{"invalid utf8", func(o *QuotaObservation) { o.Subject.Principal = string([]byte{255}) }},
		{"operation id", func(o *QuotaObservation) { o.ID = "" }},
		{"source", func(o *QuotaObservation) { o.Source = "" }},
		{"epoch", func(o *QuotaObservation) { o.Expected.StoreEpoch = "" }},
		{"no observation time", func(o *QuotaObservation) { o.ObservedAt = time.Time{} }},
		{"unserializable observation time", func(o *QuotaObservation) { o.ObservedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"coverage", func(o *QuotaObservation) { o.Coverage = nil }},
		{"no coverage window", func(o *QuotaObservation) { o.Coverage[0].Window = "" }},
		{"duplicate coverage window", func(o *QuotaObservation) { o.Coverage = append(o.Coverage, o.Coverage[0]) }},
		{"no scopes", func(o *QuotaObservation) { o.Coverage[0].Scopes = nil }},
		{"empty scope", func(o *QuotaObservation) { o.Coverage[0].Scopes = []string{""} }},
		{"duplicate scope", func(o *QuotaObservation) { o.Coverage[0].Scopes = []string{"pool", "pool"} }},
		{"no facts", func(o *QuotaObservation) { o.Facts = nil }},
		{"unknown window", func(o *QuotaObservation) { o.Facts[0].Window = "month" }},
		{"duplicate fact", func(o *QuotaObservation) { o.Facts = append(o.Facts, o.Facts[0]) }},
		{"evidence id", func(o *QuotaObservation) { o.Facts[0].EvidenceID = "" }},
		{"duplicate evidence id", func(o *QuotaObservation) { f := o.Facts[0]; f.Window = "week"; o.Facts = append(o.Facts, f) }},
		{"missing kind", func(o *QuotaObservation) { o.Facts[0].Kind = "" }},
		{"generic success", func(o *QuotaObservation) { o.Facts[0].Kind = "http_200" }},
		{"generic refusal", func(o *QuotaObservation) { o.Facts[0].Kind = "http_429" }},
		{"bad cycle", func(o *QuotaObservation) { o.Facts[0].Cycle = "cycle\n2" }},
		{"no capacity", func(o *QuotaObservation) { o.Facts[0].Capacity = nil }},
		{"no capacity semantics", func(o *QuotaObservation) { o.Facts[0].Capacity.Semantics = "" }},
		{"paid wallet", func(o *QuotaObservation) { o.Facts[0].Capacity.Semantics = "credits" }},
		{"no capacity unit", func(o *QuotaObservation) { o.Facts[0].Capacity.Unit = "" }},
		{"negative limit", func(o *QuotaObservation) { o.Facts[0].Capacity.Limit = -1 }},
		{"zero limit", func(o *QuotaObservation) { o.Facts[0].Capacity.Limit = 0 }},
		{"nan limit", func(o *QuotaObservation) { o.Facts[0].Capacity.Limit = math.NaN() }},
		{"infinite limit", func(o *QuotaObservation) { o.Facts[0].Capacity.Limit = math.Inf(1) }},
		{"negative remaining", func(o *QuotaObservation) { o.Facts[0].Capacity.Remaining = -1 }},
		{"zero positive", func(o *QuotaObservation) { o.Facts[0].Capacity.Remaining = 0 }},
		{"over limit", func(o *QuotaObservation) { o.Facts[0].Capacity.Remaining = 101 }},
		{"nan remaining", func(o *QuotaObservation) { o.Facts[0].Capacity.Remaining = math.NaN() }},
		{"infinite remaining", func(o *QuotaObservation) { o.Facts[0].Capacity.Remaining = math.Inf(-1) }},
		{"missing freshness", func(o *QuotaObservation) { o.Facts[0].FreshUntil = time.Time{} }},
		{"empty freshness interval", func(o *QuotaObservation) { o.Facts[0].FreshUntil = o.ObservedAt }},
		{"reverse freshness interval", func(o *QuotaObservation) { o.Facts[0].FreshUntil = o.ObservedAt.Add(-time.Second) }},
		{"unserializable freshness", func(o *QuotaObservation) { o.Facts[0].FreshUntil = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"elapsed reset", func(o *QuotaObservation) { o.Facts[0].ResetAt = o.ObservedAt }},
		{"unserializable reset", func(o *QuotaObservation) { o.Facts[0].ResetAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"stray recovery", func(o *QuotaObservation) { o.Facts[0].Recovery = &QuotaRecovery{} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := quotaEvidenceObservation(QuotaReportedAvailable)
			c.edit(&o)
			if err := validateQuotaObservationStructure(o); !errors.Is(err, ErrQuotaInvalid) {
				t.Fatalf("malformed observation accepted: %v", err)
			}
		})
	}
}

func TestQuotaEvidenceKindFieldMatrix(t *testing.T) {
	for _, kind := range []QuotaEvidence{QuotaReportedFull, QuotaConfirmedExhaustion, QuotaResetHint} {
		for _, field := range []string{"freshness", "recovery", "capacity"} {
			t.Run(string(kind)+"/"+field, func(t *testing.T) {
				o := quotaEvidenceObservation(kind)
				switch field {
				case "freshness":
					o.Facts[0].FreshUntil = o.ObservedAt.Add(time.Hour)
				case "recovery":
					o.Facts[0].Recovery = quotaEvidenceObservation(QuotaConfirmedRecovery).Facts[0].Recovery
				case "capacity":
					o.Facts[0].Capacity = quotaEvidenceObservation(QuotaReportedAvailable).Facts[0].Capacity
				}
				if err := validateQuotaObservationStructure(o); !errors.Is(err, ErrQuotaInvalid) {
					t.Fatalf("unrelated or contradictory %s accepted: %v", field, err)
				}
			})
		}
	}
	o := quotaEvidenceObservation(QuotaResetHint)
	o.Facts[0].ResetAt = time.Time{}
	if err := validateQuotaObservationStructure(o); !errors.Is(err, ErrQuotaInvalid) {
		t.Fatalf("empty hint accepted: %v", err)
	}
	for _, kind := range []QuotaEvidence{QuotaReportedFull, QuotaConfirmedExhaustion, QuotaResetHint} {
		o := quotaEvidenceObservation(kind)
		o.Facts[0].ResetAt = o.ObservedAt.Add(-time.Hour)
		if err := ValidateQuotaObservation(o, o.ObservedAt.Add(24*time.Hour)); err != nil {
			t.Fatalf("elapsed nonpositive reset for %s: %v", kind, err)
		}
	}
}

func TestQuotaEvidenceRecoveryNegativeMatrix(t *testing.T) {
	cases := []struct {
		name string
		edit func(*QuotaObservation)
	}{
		{"no reference", func(o *QuotaObservation) { o.Facts[0].Recovery = nil }},
		{"no blocker", func(o *QuotaObservation) { o.Facts[0].Recovery.BlockerID = "" }},
		{"self blocker", func(o *QuotaObservation) { o.Facts[0].Recovery.BlockerID = o.ID }},
		{"zero revision", func(o *QuotaObservation) { o.Facts[0].Recovery.BlockerRevision = 0 }},
		{"future revision", func(o *QuotaObservation) { o.Facts[0].Recovery.BlockerRevision = 2 }},
		{"no provenance", func(o *QuotaObservation) { o.Facts[0].Recovery.Provenance = "" }},
		{"no basis", func(o *QuotaObservation) { o.Facts[0].Recovery.Basis = "" }},
		{"reset basis", func(o *QuotaObservation) { o.Facts[0].Recovery.Basis = "elapsed" }},
		{"same cycle", func(o *QuotaObservation) { o.Facts[0].Recovery.PriorCycle = o.Facts[0].Cycle }},
		{"no prior cycle", func(o *QuotaObservation) { o.Facts[0].Recovery.PriorCycle = "" }},
		{"no new cycle", func(o *QuotaObservation) { o.Facts[0].Cycle = "" }},
		{"bad prior cycle", func(o *QuotaObservation) { o.Facts[0].Recovery.PriorCycle = "bad cycle" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := quotaEvidenceObservation(QuotaConfirmedRecovery)
			c.edit(&o)
			if err := validateQuotaObservationStructure(o); !errors.Is(err, ErrQuotaInvalid) {
				t.Fatalf("malformed recovery accepted: %v", err)
			}
		})
	}
}

func TestQuotaEvidenceClockAndReplayBoundary(t *testing.T) {
	o := quotaEvidenceObservation(QuotaReportedAvailable)
	for _, now := range []time.Time{time.Time{}, o.ObservedAt.Add(-time.Nanosecond), o.Facts[0].FreshUntil, o.Facts[0].FreshUntil.Add(time.Nanosecond)} {
		if err := ValidateQuotaObservation(o, now); !errors.Is(err, ErrQuotaInvalid) {
			t.Fatalf("invalid clock or stale new evidence accepted at %s: %v", now, err)
		}
	}
	o.Facts[0].ResetAt = o.ObservedAt.Add(time.Minute)
	if err := ValidateQuotaObservation(o, o.Facts[0].ResetAt); !errors.Is(err, ErrQuotaInvalid) {
		t.Fatalf("new positive evidence accepted at reset: %v", err)
	}
	if err := ValidateQuotaObservation(o, o.Facts[0].ResetAt.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if err := validateQuotaObservationStructure(o); err != nil {
		t.Fatalf("structural replay validation depends on current time: %v", err)
	}
}

func TestQuotaRecordAcceptsRetainedEvidence(t *testing.T) {
	for _, kind := range []QuotaEvidence{QuotaReportedFull, QuotaReportedAvailable, QuotaConfirmedExhaustion, QuotaConfirmedRecovery, QuotaResetHint} {
		for _, hint := range []bool{false, true} {
			r := quotaEvidenceRecord(kind)
			if hint {
				r.Revision++
				r.LastID = "hint-3"
				r.Windows[0].UpdatedAt = r.Windows[0].ObservedAt.Add(time.Minute)
				r.Windows[0].ResetAt = r.Windows[0].ObservedAt.Add(30 * time.Minute)
			}
			if err := validateQuotaRecord(r); err != nil {
				t.Fatalf("%s hint=%v: %v", kind, hint, err)
			}
		}
	}
	for _, kind := range []QuotaEvidence{QuotaReportedFull, QuotaReportedAvailable} {
		r := quotaEvidenceRecord(kind)
		quotaEvidenceAnchor(&r)
		if err := validateQuotaRecord(r); err != nil {
			t.Fatalf("retained anchor for %s: %v", kind, err)
		}
	}
	for _, year := range []int{2001, 2099} {
		r := quotaEvidenceRecord(QuotaReportedAvailable)
		w := &r.Windows[0]
		w.ObservedAt = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		w.UpdatedAt, w.FreshUntil, w.ResetAt = w.ObservedAt, w.ObservedAt.Add(time.Hour), w.ObservedAt.Add(2*time.Hour)
		if err := validateQuotaRecord(r); err != nil {
			t.Fatalf("stored time evaluated against now (%d): %v", year, err)
		}
	}
}

func TestQuotaRecordRejectsCorruptStructure(t *testing.T) {
	cases := []struct {
		name string
		edit func(*quotaRecord)
	}{
		{"subject", func(r *quotaRecord) { r.Subject.Generation = "" }},
		{"coverage", func(r *quotaRecord) { r.Coverage = nil }},
		{"revision", func(r *quotaRecord) { r.Revision = 0 }},
		{"no last id", func(r *quotaRecord) { r.LastID = "" }},
		{"wrong last id", func(r *quotaRecord) { r.LastID = "not-current" }},
		{"short digest", func(r *quotaRecord) { r.Digest = "aa" }},
		{"not hex digest", func(r *quotaRecord) { r.Digest = strings.Repeat("z", 64) }},
		{"no windows", func(r *quotaRecord) { r.Windows = nil }},
		{"duplicate windows", func(r *quotaRecord) { r.Windows = append(r.Windows, r.Windows[0]) }},
		{"unknown window", func(r *quotaRecord) { r.Windows[0].Window = "month" }},
		{"zero evidence revision", func(r *quotaRecord) { r.Windows[0].Revision = 0 }},
		{"future evidence revision", func(r *quotaRecord) { r.Windows[0].Revision = 3 }},
		{"no observation id", func(r *quotaRecord) { r.Windows[0].ObservationID = "" }},
		{"no source", func(r *quotaRecord) { r.Windows[0].Source = "" }},
		{"no evidence id", func(r *quotaRecord) { r.Windows[0].EvidenceID = "" }},
		{"no observation time", func(r *quotaRecord) { r.Windows[0].ObservedAt = time.Time{} }},
		{"no update time", func(r *quotaRecord) { r.Windows[0].UpdatedAt = time.Time{} }},
		{"update before evidence", func(r *quotaRecord) { r.Windows[0].UpdatedAt = r.Windows[0].ObservedAt.Add(-time.Second) }},
		{"unknown state", func(r *quotaRecord) { r.Windows[0].State = "paid" }},
		{"unknown kind", func(r *quotaRecord) { r.Windows[0].Kind = "success" }},
		{"positive with zero", func(r *quotaRecord) { r.Windows[0].Capacity.Remaining = 0 }},
		{"positive without capacity", func(r *quotaRecord) { r.Windows[0].Capacity = nil }},
		{"positive without freshness", func(r *quotaRecord) { r.Windows[0].FreshUntil = time.Time{} }},
		{"positive with past reset no hint", func(r *quotaRecord) { r.Windows[0].ResetAt = r.Windows[0].ObservedAt }},
		{"positive exhausted without anchor", func(r *quotaRecord) { r.Windows[0].State = QuotaStateExhausted }},
		{"positive available with anchor", func(r *quotaRecord) { quotaEvidenceAnchor(r); r.Windows[0].State = QuotaStateAvailable }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := quotaEvidenceRecord(QuotaReportedAvailable)
			c.edit(&r)
			if err := validateQuotaRecord(r); !errors.Is(err, ErrQuotaCorrupt) {
				t.Fatalf("corrupt record accepted: %v", err)
			}
		})
	}
}

func TestQuotaRecordStateKindMatrix(t *testing.T) {
	for _, kind := range []QuotaEvidence{QuotaResetHint, QuotaReportedFull, QuotaReportedAvailable, QuotaConfirmedExhaustion, QuotaConfirmedRecovery} {
		for _, state := range []QuotaState{QuotaStateUnknown, QuotaStateAvailable, QuotaStateExhausted, ""} {
			r := quotaEvidenceRecord(kind)
			valid := r.Windows[0].State == state
			r.Windows[0].State = state
			err := validateQuotaRecord(r)
			if (err == nil) != valid {
				t.Fatalf("kind=%s state=%s accepted=%v, want=%v", kind, state, err == nil, valid)
			}
		}
	}
}

func TestQuotaRecordRejectsCorruptAnchorsAndRecovery(t *testing.T) {
	for _, kind := range []QuotaEvidence{QuotaConfirmedExhaustion, QuotaReportedFull, QuotaReportedAvailable} {
		for _, field := range []string{"id", "revision", "source", "evidence", "cycle", "time"} {
			t.Run(string(kind)+"/"+field, func(t *testing.T) {
				r := quotaEvidenceRecord(kind)
				if kind != QuotaConfirmedExhaustion {
					quotaEvidenceAnchor(&r)
				}
				b := r.Windows[0].Blocker
				switch field {
				case "id":
					b.ID = ""
				case "revision":
					b.Revision = r.Revision + 1
				case "source":
					b.Source = ""
				case "evidence":
					b.EvidenceID = ""
				case "cycle":
					b.Cycle = "bad cycle"
				case "time":
					b.ObservedAt = r.Windows[0].ObservedAt.Add(time.Second)
				}
				if err := validateQuotaRecord(r); !errors.Is(err, ErrQuotaCorrupt) {
					t.Fatalf("corrupt anchor accepted: %v", err)
				}
			})
		}
	}
	for _, field := range []string{"missing", "retained", "self", "future", "same cycle"} {
		r := quotaEvidenceRecord(QuotaConfirmedRecovery)
		switch field {
		case "missing":
			r.Windows[0].Recovery = nil
		case "retained":
			quotaEvidenceAnchor(&r)
			r.Windows[0].State = QuotaStateAvailable
		case "self":
			r.Windows[0].Recovery.BlockerID = r.LastID
		case "future":
			r.Windows[0].Recovery.BlockerRevision = r.Revision
		case "same cycle":
			r.Windows[0].Recovery.PriorCycle = r.Windows[0].Cycle
		}
		if err := validateQuotaRecord(r); !errors.Is(err, ErrQuotaCorrupt) {
			t.Fatalf("corrupt recovery %s accepted: %v", field, err)
		}
	}
	r := quotaEvidenceRecord(QuotaConfirmedExhaustion)
	r.Windows[0].Blocker = nil
	if err := validateQuotaRecord(r); !errors.Is(err, ErrQuotaCorrupt) {
		t.Fatalf("confirmed refusal lost its anchor: %v", err)
	}
}

func TestQuotaEvidenceErrorsDoNotEchoCallerMetadata(t *testing.T) {
	o := quotaEvidenceObservation(QuotaReportedAvailable)
	o.Source = "private caller metadata"
	err := ValidateQuotaObservation(o, o.ObservedAt)
	if err == nil || strings.Contains(err.Error(), o.Source) {
		t.Fatalf("error includes caller metadata: %v", err)
	}
	r := quotaEvidenceRecord(QuotaReportedAvailable)
	before := quotaEvidenceRecord(QuotaReportedAvailable)
	if err := validateQuotaRecord(r); err != nil || !reflect.DeepEqual(r, before) {
		t.Fatalf("record validation mutated input: %v", err)
	}
}

func TestQuotaRecordCorruptOperationRelationships(t *testing.T) {
	for _, field := range []string{"observation", "source", "time", "evidence"} {
		t.Run(field, func(t *testing.T) {
			r := quotaEvidenceRecord(QuotaReportedAvailable)
			w := r.Windows[0]
			w.Window, w.EvidenceID = "week", "week-evidence"
			r.Windows = append(r.Windows, w)
			if err := validateQuotaRecord(r); err != nil {
				t.Fatalf("valid same-operation windows: %v", err)
			}
			switch field {
			case "observation":
				r.Windows[1].ObservationID = "different-operation"
			case "source":
				r.Windows[1].Source = "different-source"
			case "time":
				r.Windows[1].ObservedAt = r.Windows[1].ObservedAt.Add(-time.Second)
			case "evidence":
				r.Windows[1].EvidenceID = r.Windows[0].EvidenceID
			}
			if err := validateQuotaRecord(r); !errors.Is(err, ErrQuotaCorrupt) {
				t.Fatalf("corrupt operation accepted: %v", err)
			}
		})
	}
	for _, hint := range []bool{false, true} {
		r := quotaEvidenceRecord(QuotaReportedAvailable)
		r.Windows[0].UpdatedAt = r.Windows[0].ObservedAt.Add(time.Minute)
		if hint {
			r.Revision++
			r.LastID = "hint-3"
			r.Windows[0].ResetAt = r.Windows[0].ObservedAt
		}
		if err := validateQuotaRecord(r); !errors.Is(err, ErrQuotaCorrupt) {
			t.Fatalf("invalid hint relationship accepted (hint=%v): %v", hint, err)
		}
	}
}

func TestQuotaRecordRefusalMetadataMustMatchExactly(t *testing.T) {
	for _, field := range []string{"id", "source", "evidence", "cycle", "revision", "time"} {
		r := quotaEvidenceRecord(QuotaConfirmedExhaustion)
		b := r.Windows[0].Blocker
		switch field {
		case "id":
			b.ID = "different-id"
		case "source":
			b.Source = "different-source"
		case "evidence":
			b.EvidenceID = "different-evidence"
		case "cycle":
			b.Cycle = "different-cycle"
		case "revision":
			b.Revision--
		case "time":
			b.ObservedAt = b.ObservedAt.Add(-time.Second)
		}
		if err := validateQuotaRecord(r); !errors.Is(err, ErrQuotaCorrupt) {
			t.Fatalf("mismatched anchor %s accepted: %v", field, err)
		}
	}
	for _, field := range []string{"equal revision", "equal time", "same id"} {
		r := quotaEvidenceRecord(QuotaReportedAvailable)
		quotaEvidenceAnchor(&r)
		b := r.Windows[0].Blocker
		switch field {
		case "equal revision":
			b.Revision = r.Windows[0].Revision
		case "equal time":
			b.ObservedAt = r.Windows[0].ObservedAt
		case "same id":
			b.ID = r.Windows[0].ObservationID
		}
		if err := validateQuotaRecord(r); !errors.Is(err, ErrQuotaCorrupt) {
			t.Fatalf("nonpreceding anchor %s accepted: %v", field, err)
		}
	}
}

func TestQuotaRecordRetainedOperationsAgree(t *testing.T) {
	at := quotaCoreTime()
	day := quotaCoreFact(QuotaConfirmedExhaustion, at)
	week := quotaCoreFact(QuotaReportedAvailable, at)
	week.Window, week.EvidenceID = "week", "evidence-week"
	o := quotaCoreObservation(0, at, day, week)
	o.Coverage = append(o.Coverage, QuotaCoverage{Window: "week", Scopes: []string{"separate"}})
	first := quotaCoreReduce(t, nil, o)
	for _, retained := range []string{"anchor-and-window", "two-anchors", "recovery-and-window"} {
		t.Run(retained, func(t *testing.T) {
			base := cloneQuotaRecord(first)
			if retained == "two-anchors" {
				refusals := o
				refusals.Facts = append([]QuotaFact(nil), o.Facts...)
				refusals.Facts[1].Kind, refusals.Facts[1].Capacity, refusals.Facts[1].FreshUntil = QuotaConfirmedExhaustion, nil, time.Time{}
				base = quotaCoreReduce(t, nil, refusals)
			}
			fact := quotaCoreFact(QuotaReportedFull, at.Add(time.Minute))
			if retained == "recovery-and-window" {
				fact = quotaCoreRecovery(base.Windows[0], at.Add(time.Minute))
			}
			next := quotaCoreObservation(1, at.Add(time.Minute), fact)
			next.Coverage = base.Coverage
			if retained == "two-anchors" {
				f := fact
				f.Window, f.EvidenceID = "week", "full-week"
				next.Facts = append(next.Facts, f)
			}
			r := quotaCoreReduce(t, &base, next)
			r.LastID, r.Digest = next.ID, strings.Repeat("a", 64)
			fields := []string{"id", "source", "time", "evidence"}
			if retained == "recovery-and-window" {
				fields = []string{"id"}
			}
			for _, field := range fields {
				t.Run(field, func(t *testing.T) {
					bad := cloneQuotaRecord(r)
					if retained == "recovery-and-window" {
						bad.Windows[0].Recovery.BlockerID = "inconsistent-operation"
					} else {
						b := bad.Windows[0].Blocker
						switch field {
						case "id":
							b.ID = "inconsistent-operation"
						case "source":
							b.Source = "inconsistent-source"
						case "time":
							b.ObservedAt = b.ObservedAt.Add(-time.Second)
						case "evidence":
							b.EvidenceID = "evidence-week"
						}
					}
					if err := validateQuotaRecord(bad); !errors.Is(err, ErrQuotaCorrupt) {
						t.Errorf("inconsistent retained operation accepted: %v", err)
					}
					b, err := json.Marshal(quotaFile{Version: 1, Epoch: "epoch", CommitSequence: bad.Revision, Records: []quotaRecord{bad}})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := decodeQuotaFile(b); !errors.Is(err, ErrQuotaCorrupt) {
						t.Fatalf("malformed relationship exposed a scope: %v", err)
					}
				})
			}
		})
	}
}
