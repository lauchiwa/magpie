package provider

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// reduceQuota is independent of storage and billing policy. The store checks
// its epoch and replay ledger before calling it with a validated current record.
func reduceQuota(current *quotaRecord, observation QuotaObservation, now time.Time) (quotaRecord, error) {
	if err := ValidateQuotaObservation(observation, now); err != nil {
		return quotaRecord{}, err
	}
	var next quotaRecord
	if current == nil {
		next.Subject = observation.Subject
		next.Coverage = quotaCanonicalCoverage(observation.Coverage)
	} else {
		if current.Subject != observation.Subject {
			return quotaRecord{}, fmt.Errorf("%w: subject changed", ErrQuotaConflict)
		}
		next = cloneQuotaRecord(*current)
		if !slices.EqualFunc(quotaCanonicalCoverage(current.Coverage), quotaCanonicalCoverage(observation.Coverage), func(a, b QuotaCoverage) bool {
			return a.Window == b.Window && slices.Equal(a.Scopes, b.Scopes)
		}) {
			return quotaRecord{}, fmt.Errorf("%w: coverage changed", ErrQuotaInvalid)
		}
	}
	if observation.Expected.Revision != next.Revision || next.Revision == ^uint64(0) {
		return quotaRecord{}, fmt.Errorf("%w: subject revision changed", ErrQuotaConflict)
	}
	next.Revision++
	for _, fact := range observation.Facts {
		i := slices.IndexFunc(next.Windows, func(w quotaWindow) bool { return w.Window == fact.Window })
		var old quotaWindow
		if i >= 0 {
			old = next.Windows[i]
			if !observation.ObservedAt.After(old.UpdatedAt) {
				return quotaRecord{}, fmt.Errorf("%w: window observation is not newer", ErrQuotaConflict)
			}
		}
		w := quotaWindow{
			Window: fact.Window, Kind: fact.Kind, ObservationID: observation.ID,
			Revision: next.Revision, Source: observation.Source, EvidenceID: fact.EvidenceID,
			ObservedAt: observation.ObservedAt, UpdatedAt: observation.ObservedAt,
			FreshUntil: fact.FreshUntil, ResetAt: fact.ResetAt, Cycle: fact.Cycle,
			Capacity: fact.Capacity, Recovery: fact.Recovery, Blocker: old.Blocker,
		}
		switch fact.Kind {
		case QuotaResetHint:
			w.State = QuotaStateUnknown
			if i >= 0 {
				if (old.Kind == QuotaReportedAvailable || old.Kind == QuotaConfirmedRecovery) && !fact.ResetAt.After(old.ObservedAt) {
					return quotaRecord{}, fmt.Errorf("%w: reset predates positive evidence", ErrQuotaInvalid)
				}
				w = old
				w.UpdatedAt = observation.ObservedAt
				// A hint can shorten, but cannot extend, existing availability.
				if old.State != QuotaStateAvailable || old.ResetAt.IsZero() || fact.ResetAt.Before(old.ResetAt) {
					w.ResetAt = fact.ResetAt
				}
			}
		case QuotaReportedFull:
			w.State = QuotaStateExhausted
		case QuotaConfirmedExhaustion:
			w.State = QuotaStateExhausted
			w.Blocker = &QuotaBlocker{
				ID: observation.ID, Revision: next.Revision, Source: observation.Source,
				EvidenceID: fact.EvidenceID, Cycle: fact.Cycle, ObservedAt: observation.ObservedAt,
			}
		case QuotaReportedAvailable:
			w.State = QuotaStateAvailable
			if old.Blocker != nil {
				w.State = QuotaStateExhausted
			}
		case QuotaConfirmedRecovery:
			id, revision, cycle := old.ObservationID, old.Revision, old.Cycle
			if old.Blocker != nil {
				id, revision, cycle = old.Blocker.ID, old.Blocker.Revision, old.Blocker.Cycle
			}
			if old.State != QuotaStateExhausted || fact.Recovery.BlockerID != id || fact.Recovery.BlockerRevision != revision || fact.Recovery.PriorCycle != cycle {
				return quotaRecord{}, fmt.Errorf("%w: recovery does not supersede current exhaustion", ErrQuotaConflict)
			}
			w.State, w.Blocker = QuotaStateAvailable, nil
		}
		w = cloneQuotaWindow(w)
		if i >= 0 {
			next.Windows[i] = w
		} else {
			next.Windows = append(next.Windows, w)
		}
	}
	slices.SortFunc(next.Windows, func(a, b quotaWindow) int { return strings.Compare(a.Window, b.Window) })
	return next, nil
}

// quotaView evaluates the complete stored coverage, including unobserved windows.
// An exhausted window wins over missing or stale evidence in any other window.
func quotaView(record *quotaRecord, subject QuotaSubject, scope string, version QuotaVersion, now time.Time) QuotaSnapshot {
	v := QuotaSnapshot{Subject: subject, Scope: scope, Version: version, State: QuotaStateUnknown}
	if record == nil || record.Subject != subject || scope == "" {
		return v
	}
	allAvailable := true
	for _, coverage := range record.Coverage {
		if !slices.Contains(coverage.Scopes, scope) {
			continue
		}
		window := QuotaWindowSnapshot{Window: coverage.Window, State: QuotaStateUnknown}
		if i := slices.IndexFunc(record.Windows, func(w quotaWindow) bool { return w.Window == coverage.Window }); i >= 0 {
			w := cloneQuotaWindow(record.Windows[i])
			window = projectQuotaWindow(quotaWindowSnapshot(w), now)
		}
		if window.State == QuotaStateExhausted {
			v.State = QuotaStateExhausted
		}
		allAvailable = allAvailable && window.State == QuotaStateAvailable
		v.RecheckDue = v.RecheckDue || window.RecheckDue
		v.Windows = append(v.Windows, window)
	}
	if len(v.Windows) != 0 && allAvailable {
		v.State = QuotaStateAvailable
	}
	slices.SortFunc(v.Windows, func(a, b QuotaWindowSnapshot) int { return strings.Compare(a.Window, b.Window) })
	return v
}

func quotaViews(record *quotaRecord, subject QuotaSubject, coverage []QuotaCoverage, version QuotaVersion, now time.Time) []QuotaSnapshot {
	var scopes []string
	for _, c := range coverage {
		scopes = append(scopes, c.Scopes...)
	}
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	var views []QuotaSnapshot
	for _, scope := range scopes {
		views = append(views, quotaView(record, subject, scope, version, now))
	}
	return views
}

func cloneQuotaRecord(record quotaRecord) quotaRecord {
	record.Coverage = quotaCoverageCopy(record.Coverage)
	record.Windows = slices.Clone(record.Windows)
	for i, w := range record.Windows {
		record.Windows[i] = cloneQuotaWindow(w)
	}
	return record
}

func quotaCoverageCopy(coverage []QuotaCoverage) []QuotaCoverage {
	coverage = slices.Clone(coverage)
	for i := range coverage {
		coverage[i].Scopes = slices.Clone(coverage[i].Scopes)
	}
	return coverage
}

func quotaCanonicalCoverage(coverage []QuotaCoverage) []QuotaCoverage {
	coverage = quotaCoverageCopy(coverage)
	for i := range coverage {
		slices.Sort(coverage[i].Scopes)
	}
	slices.SortFunc(coverage, func(a, b QuotaCoverage) int { return strings.Compare(a.Window, b.Window) })
	return coverage
}

func cloneQuotaWindow(w quotaWindow) quotaWindow {
	if w.Capacity != nil {
		capacity := *w.Capacity
		w.Capacity = &capacity
	}
	if w.Recovery != nil {
		recovery := *w.Recovery
		w.Recovery = &recovery
	}
	if w.Blocker != nil {
		blocker := *w.Blocker
		w.Blocker = &blocker
	}
	return w
}

// quotaWindowSnapshot copies retained fields only. UpdatedAt belongs to storage,
// not a historical snapshot, and must not be inferred from ObservedAt.
func quotaWindowSnapshot(w quotaWindow) QuotaWindowSnapshot {
	return QuotaWindowSnapshot{
		Window: w.Window, State: w.State, Kind: w.Kind, ObservedAt: w.ObservedAt,
		ObservationID: w.ObservationID, Revision: w.Revision, Source: w.Source,
		EvidenceID: w.EvidenceID, Cycle: w.Cycle, Recovery: w.Recovery,
		FreshUntil: w.FreshUntil, ResetAt: w.ResetAt, Capacity: w.Capacity, Blocker: w.Blocker,
	}
}

func projectQuotaWindow(w QuotaWindowSnapshot, at time.Time) QuotaWindowSnapshot {
	w.RecheckDue = !w.ResetAt.IsZero() && !at.Before(w.ResetAt)
	if w.State == QuotaStateAvailable && (at.Before(w.ObservedAt) || !at.Before(w.FreshUntil) || w.RecheckDue) {
		w.State = QuotaStateUnknown
	}
	return w
}
