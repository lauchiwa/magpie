package provider

import (
	"encoding/hex"
	"fmt"
	"math"
	"time"
	"unicode"
	"unicode/utf8"
)

func validQuotaID(id string) bool {
	if id == "" || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func quotaInvalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrQuotaInvalid, reason)
}

func validateQuotaSubject(subject QuotaSubject) error {
	if !validQuotaID(subject.Authority) || !validQuotaID(subject.Provider) ||
		!validQuotaID(subject.Principal) || !validQuotaID(subject.Generation) {
		return quotaInvalid("incomplete subject identity")
	}
	return nil
}

func validateQuotaCoverage(coverage []QuotaCoverage) error {
	if len(coverage) == 0 {
		return quotaInvalid("missing coverage")
	}
	windows := make(map[string]bool, len(coverage))
	for _, entry := range coverage {
		if !validQuotaID(entry.Window) || windows[entry.Window] || len(entry.Scopes) == 0 {
			return quotaInvalid("invalid coverage window")
		}
		windows[entry.Window] = true
		scopes := make(map[string]bool, len(entry.Scopes))
		for _, scope := range entry.Scopes {
			if !validQuotaID(scope) || scopes[scope] {
				return quotaInvalid("invalid coverage scope")
			}
			scopes[scope] = true
		}
	}
	return nil
}

func validQuotaTime(t time.Time) bool {
	_, err := t.MarshalJSON()
	return err == nil
}

func validateQuotaCapacity(capacity *QuotaCapacity, positive bool) error {
	if capacity == nil || capacity.Semantics != "included" || !validQuotaID(capacity.Unit) ||
		math.IsNaN(capacity.Limit) || math.IsInf(capacity.Limit, 0) || capacity.Limit <= 0 ||
		math.IsNaN(capacity.Remaining) || math.IsInf(capacity.Remaining, 0) ||
		capacity.Remaining < 0 || capacity.Remaining > capacity.Limit {
		return quotaInvalid("invalid included capacity")
	}
	if positive && capacity.Remaining == 0 || !positive && capacity.Remaining != 0 {
		return quotaInvalid("capacity contradicts evidence")
	}
	return nil
}

func validateQuotaRecovery(recovery *QuotaRecovery, cycle string, revision uint64) error {
	if recovery == nil || !validQuotaID(recovery.BlockerID) || recovery.BlockerRevision == 0 ||
		recovery.BlockerRevision > revision || !validQuotaID(recovery.Provenance) ||
		recovery.PriorCycle != "" && !validQuotaID(recovery.PriorCycle) {
		return quotaInvalid("invalid recovery reference")
	}
	switch recovery.Basis {
	case "cycle":
		if cycle == "" || recovery.PriorCycle == "" || cycle == recovery.PriorCycle {
			return quotaInvalid("recovery requires distinct cycles")
		}
	case "superseding":
	default:
		return quotaInvalid("unsupported recovery basis")
	}
	return nil
}

// This checks normalized assertions, not vendor authenticity or live identity
// binding. Legacy Allowance/SubscriptionQuota values cannot supply that proof.
func validateQuotaFact(fact QuotaFact, observed time.Time, revision uint64) error {
	if !validQuotaID(fact.Window) || !validQuotaID(fact.EvidenceID) ||
		fact.Cycle != "" && !validQuotaID(fact.Cycle) ||
		!validQuotaTime(fact.FreshUntil) || !validQuotaTime(fact.ResetAt) {
		return quotaInvalid("invalid fact metadata")
	}
	positive := fact.Kind == QuotaReportedAvailable || fact.Kind == QuotaConfirmedRecovery
	if positive {
		if !fact.FreshUntil.After(observed) ||
			!fact.ResetAt.IsZero() && !fact.ResetAt.After(observed) {
			return quotaInvalid("invalid positive evidence interval")
		}
	} else if !fact.FreshUntil.IsZero() {
		return quotaInvalid("freshness without positive evidence")
	}
	if fact.Kind != QuotaConfirmedRecovery && fact.Recovery != nil {
		return quotaInvalid("recovery without recovery evidence")
	}
	switch fact.Kind {
	case QuotaReportedFull, QuotaReportedAvailable, QuotaConfirmedRecovery:
		if err := validateQuotaCapacity(fact.Capacity, positive); err != nil {
			return err
		}
		if fact.Kind == QuotaConfirmedRecovery {
			return validateQuotaRecovery(fact.Recovery, fact.Cycle, revision)
		}
	case QuotaConfirmedExhaustion, QuotaResetHint:
		if fact.Capacity != nil {
			return quotaInvalid("capacity without capacity evidence")
		}
		if fact.Kind == QuotaResetHint && fact.ResetAt.IsZero() {
			return quotaInvalid("reset hint without reset time")
		}
	default:
		return quotaInvalid("unsupported evidence kind")
	}
	return nil
}

func validateQuotaObservationStructure(observation QuotaObservation) error {
	if err := validateQuotaSubject(observation.Subject); err != nil {
		return err
	}
	if err := validateQuotaCoverage(observation.Coverage); err != nil {
		return err
	}
	if !validQuotaID(observation.ID) || !validQuotaID(observation.Source) ||
		!validQuotaID(observation.Expected.StoreEpoch) || observation.ObservedAt.IsZero() ||
		!validQuotaTime(observation.ObservedAt) || len(observation.Facts) == 0 {
		return quotaInvalid("incomplete observation metadata")
	}
	coverage := make(map[string]bool, len(observation.Coverage))
	for _, entry := range observation.Coverage {
		coverage[entry.Window] = true
	}
	windows := make(map[string]bool, len(observation.Facts))
	evidence := make(map[string]bool, len(observation.Facts))
	for _, fact := range observation.Facts {
		if !coverage[fact.Window] || windows[fact.Window] || evidence[fact.EvidenceID] {
			return quotaInvalid("duplicate or uncovered fact")
		}
		windows[fact.Window], evidence[fact.EvidenceID] = true, true
		if err := validateQuotaFact(fact, observation.ObservedAt, observation.Expected.Revision); err != nil {
			return err
		}
		if fact.Recovery != nil && fact.Recovery.BlockerID == observation.ID {
			return quotaInvalid("self-referencing recovery")
		}
	}
	return nil
}

// ValidateQuotaObservation validates new input against the supplied clock. Exact
// committed replay is structural only and is handled by the store first.
func ValidateQuotaObservation(observation QuotaObservation, now time.Time) error {
	if err := validateQuotaObservationStructure(observation); err != nil {
		return err
	}
	if now.IsZero() || !validQuotaTime(now) || observation.ObservedAt.After(now) {
		return quotaInvalid("invalid observation clock")
	}
	for _, fact := range observation.Facts {
		if fact.Kind == QuotaReportedAvailable || fact.Kind == QuotaConfirmedRecovery {
			if !now.Before(fact.FreshUntil) || !fact.ResetAt.IsZero() && !now.Before(fact.ResetAt) {
				return quotaInvalid("positive evidence expired")
			}
		}
	}
	return nil
}

func validateQuotaWindow(window quotaWindow, revision uint64) error {
	if window.Revision == 0 || window.Revision > revision ||
		!validQuotaID(window.ObservationID) || !validQuotaID(window.Source) ||
		window.ObservedAt.IsZero() || !validQuotaTime(window.ObservedAt) ||
		!validQuotaTime(window.UpdatedAt) || window.UpdatedAt.Before(window.ObservedAt) {
		return quotaInvalid("invalid stored evidence metadata")
	}
	fact := QuotaFact{
		Window: window.Window, Kind: window.Kind, EvidenceID: window.EvidenceID,
		FreshUntil: window.FreshUntil, ResetAt: window.ResetAt, Cycle: window.Cycle,
		Capacity: window.Capacity, Recovery: window.Recovery,
	}
	if err := validateQuotaFact(fact, window.ObservedAt, window.Revision-1); err != nil {
		return err
	}
	switch window.Kind {
	case QuotaResetHint:
		if window.State != QuotaStateUnknown || window.Blocker != nil {
			return quotaInvalid("reset hint cannot establish state")
		}
	case QuotaReportedFull, QuotaConfirmedExhaustion:
		if window.State != QuotaStateExhausted {
			return quotaInvalid("exhaustion contradicts stored state")
		}
	case QuotaReportedAvailable:
		if window.Blocker == nil && window.State != QuotaStateAvailable ||
			window.Blocker != nil && window.State != QuotaStateExhausted {
			return quotaInvalid("positive telemetry contradicts held refusal")
		}
	case QuotaConfirmedRecovery:
		if window.State != QuotaStateAvailable || window.Blocker != nil ||
			window.Recovery.BlockerID == window.ObservationID {
			return quotaInvalid("recovery contradicts stored state")
		}
	}
	blocker := window.Blocker
	if window.Kind == QuotaConfirmedExhaustion {
		if blocker == nil || blocker.ID != window.ObservationID || blocker.Revision != window.Revision ||
			blocker.Source != window.Source || blocker.EvidenceID != window.EvidenceID ||
			blocker.Cycle != window.Cycle || !blocker.ObservedAt.Equal(window.ObservedAt) {
			return quotaInvalid("refusal anchor differs from confirmed exhaustion")
		}
	}
	if blocker != nil {
		if window.State != QuotaStateExhausted || !validQuotaID(blocker.ID) || !validQuotaID(blocker.Source) ||
			!validQuotaID(blocker.EvidenceID) || blocker.Cycle != "" && !validQuotaID(blocker.Cycle) ||
			blocker.Revision == 0 || blocker.Revision > window.Revision ||
			blocker.ObservedAt.IsZero() || !validQuotaTime(blocker.ObservedAt) {
			return quotaInvalid("invalid refusal anchor")
		}
		if window.Kind != QuotaConfirmedExhaustion &&
			(blocker.Revision >= window.Revision || !blocker.ObservedAt.Before(window.ObservedAt) || blocker.ID == window.ObservationID) {
			return quotaInvalid("refusal anchor does not precede telemetry")
		}
	}
	return nil
}

// Stored validity has no wall clock: expired evidence is valid storage and its
// current eligibility is decided only by quotaView.
func validateQuotaRecord(record quotaRecord) error {
	invalid := func() error { return fmt.Errorf("%w: invalid stored record", ErrQuotaCorrupt) }
	if validateQuotaSubject(record.Subject) != nil || validateQuotaCoverage(record.Coverage) != nil ||
		record.Revision == 0 || !validQuotaID(record.LastID) || len(record.Digest) != 64 || len(record.Windows) == 0 {
		return invalid()
	}
	if _, err := hex.DecodeString(record.Digest); err != nil {
		return invalid()
	}
	coverage := make(map[string]bool, len(record.Coverage))
	for _, entry := range record.Coverage {
		coverage[entry.Window] = true
	}
	windows := make(map[string]bool, len(record.Windows))
	operations := make(map[uint64]quotaWindow)
	evidence := make(map[uint64]map[string]string)
	ids := map[uint64]string{record.Revision: record.LastID}
	rememberID := func(revision uint64, id string) bool {
		if prior, ok := ids[revision]; ok && prior != id {
			return false
		}
		ids[revision] = id
		return true
	}
	// Latest facts and retained anchors from one operation must agree. The
	// confirmed-refusal fact and its own anchor may repeat the same evidence.
	remember := func(window quotaWindow) bool {
		if !rememberID(window.Revision, window.ObservationID) {
			return false
		}
		if prior, ok := operations[window.Revision]; ok {
			if prior.Source != window.Source || !prior.ObservedAt.Equal(window.ObservedAt) {
				return false
			}
		} else {
			operations[window.Revision] = window
			evidence[window.Revision] = make(map[string]string)
		}
		if other, ok := evidence[window.Revision][window.EvidenceID]; ok && other != window.Window {
			return false
		}
		evidence[window.Revision][window.EvidenceID] = window.Window
		return true
	}
	for _, window := range record.Windows {
		if !coverage[window.Window] || windows[window.Window] || validateQuotaWindow(window, record.Revision) != nil ||
			window.Revision == record.Revision && !window.UpdatedAt.Equal(window.ObservedAt) || !remember(window) {
			return invalid()
		}
		if b := window.Blocker; b != nil && !remember(quotaWindow{
			Window: window.Window, ObservationID: b.ID, Revision: b.Revision,
			Source: b.Source, EvidenceID: b.EvidenceID, ObservedAt: b.ObservedAt,
		}) {
			return invalid()
		}
		if r := window.Recovery; r != nil && !rememberID(r.BlockerRevision, r.BlockerID) {
			return invalid()
		}
		windows[window.Window] = true
	}
	return nil
}
