package provider

import (
	"slices"
	"time"
)

// CloneQuotaSnapshot checks a materialized view at its supplied historical
// evaluation time, relative to the supplied complete coverage, then detaches it.
// It cannot authenticate coverage or evidence, reconstruct an earlier view, or
// replay a recovery: snapshots omit UpdatedAt, prior cleared facts and the store
// replay ledger. A before-acquisition unknown view is valid. Callers claiming
// acquired evidence at capture must separately check that temporal constraint.
// Missing/unavailable contexts are not materialized snapshots. On invalid input
// it returns ErrQuotaInvalid and no usable snapshot or coverage.
func CloneQuotaSnapshot(snapshot QuotaSnapshot, coverage []QuotaCoverage, at time.Time) (QuotaSnapshot, []QuotaCoverage, error) {
	bad := func(reason string) (QuotaSnapshot, []QuotaCoverage, error) {
		return QuotaSnapshot{}, nil, quotaInvalid(reason)
	}
	if err := validateQuotaSubject(snapshot.Subject); err != nil {
		return QuotaSnapshot{}, nil, err
	}
	if err := validateQuotaCoverage(coverage); err != nil {
		return QuotaSnapshot{}, nil, err
	}
	if !validQuotaID(snapshot.Scope) || !validQuotaID(snapshot.Version.StoreEpoch) ||
		snapshot.Version.Revision == 0 || at.IsZero() || !validQuotaTime(at) {
		return bad("invalid materialized snapshot metadata")
	}
	applicable := make(map[string]bool)
	for _, c := range coverage {
		if slices.Contains(c.Scopes, snapshot.Scope) {
			applicable[c.Window] = true
		}
	}
	operations := newQuotaOperations()
	hasFacts := false
	state, due, allAvailable := QuotaStateUnknown, false, len(snapshot.Windows) != 0
	for _, w := range snapshot.Windows {
		if !applicable[w.Window] {
			return bad("duplicate or uncovered snapshot window")
		}
		delete(applicable, w.Window)
		if w.Kind == "" {
			if !quotaUnknownPlaceholder(w) {
				return bad("unknown placeholder carries evidence")
			}
		} else {
			hasFacts = true
			retained := w
			switch w.Kind {
			case QuotaReportedFull, QuotaConfirmedExhaustion:
				retained.State = QuotaStateExhausted
			case QuotaReportedAvailable, QuotaConfirmedRecovery:
				retained.State = QuotaStateAvailable
				if w.Blocker != nil {
					retained.State = QuotaStateExhausted
				}
			default:
				retained.State = QuotaStateUnknown
			}
			if err := validateQuotaRetained(retained, snapshot.Version.Revision); err != nil {
				return QuotaSnapshot{}, nil, err
			}
			projected := projectQuotaWindow(retained, at)
			if w.State != projected.State || w.RecheckDue != projected.RecheckDue {
				return bad("snapshot projection differs at evaluation time")
			}
			if !operations.remember(retained) {
				return bad("inconsistent snapshot operation")
			}
			if b := w.Blocker; b != nil && !operations.remember(QuotaWindowSnapshot{
				Window: w.Window, ObservationID: b.ID, Revision: b.Revision,
				Source: b.Source, EvidenceID: b.EvidenceID, ObservedAt: b.ObservedAt,
			}) {
				return bad("inconsistent snapshot refusal")
			}
			if r := w.Recovery; r != nil && !operations.rememberID(r.BlockerRevision, r.BlockerID) {
				return bad("inconsistent snapshot recovery reference")
			}
		}
		if w.State == QuotaStateExhausted {
			state = QuotaStateExhausted
		}
		allAvailable = allAvailable && w.State == QuotaStateAvailable
		due = due || w.RecheckDue
	}
	if allAvailable {
		state = QuotaStateAvailable
	}
	if len(applicable) != 0 || snapshot.State != state || snapshot.RecheckDue != due {
		return bad("incomplete or inconsistent snapshot projection")
	}
	// A materialized record has a fact somewhere in its declared coverage.
	// Outside-scope facts are absent here, so only the full-coverage case proves
	// that an all-placeholder snapshot could not come from such a record.
	if !hasFacts && len(snapshot.Windows) == len(coverage) {
		return bad("materialized snapshot has no retained fact")
	}
	snapshot.Windows = slices.Clone(snapshot.Windows)
	for i, w := range snapshot.Windows {
		if w.Capacity != nil {
			value := *w.Capacity
			snapshot.Windows[i].Capacity = &value
		}
		if w.Recovery != nil {
			value := *w.Recovery
			snapshot.Windows[i].Recovery = &value
		}
		if w.Blocker != nil {
			value := *w.Blocker
			snapshot.Windows[i].Blocker = &value
		}
	}
	return snapshot, quotaCoverageCopy(coverage), nil
}

func quotaUnknownPlaceholder(w QuotaWindowSnapshot) bool {
	return w.State == QuotaStateUnknown && w.ObservationID == "" && w.Revision == 0 &&
		w.Source == "" && w.EvidenceID == "" && w.Cycle == "" && w.ObservedAt.IsZero() &&
		w.FreshUntil.IsZero() && w.ResetAt.IsZero() && !w.RecheckDue &&
		w.Capacity == nil && w.Recovery == nil && w.Blocker == nil &&
		validQuotaTime(w.ObservedAt) && validQuotaTime(w.FreshUntil) && validQuotaTime(w.ResetAt)
}
