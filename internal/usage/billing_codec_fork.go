package usage

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/strictjson"
)

// Method-free price DTOs deliberately bypass catalog.Price.UnmarshalJSON, whose
// remote-catalogue repairs and missing-component fallbacks are not a wire schema.
type billingPriceWire struct {
	Input        float64           `json:"input"`
	Output       float64           `json:"output"`
	CacheRead    float64           `json:"cache_read"`
	CacheWrite   float64           `json:"cache_write"`
	CacheWrite1h float64           `json:"cache_write_1h"`
	Peak         catalog.Peak      `json:"peak"`
	Tiers        []billingTierWire `json:"tiers"`
}
type billingTierWire struct {
	Above        int     `json:"above"`
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite   float64 `json:"cache_write"`
	CacheWrite1h float64 `json:"cache_write_1h"`
}
type billingTariffWire struct {
	Currency   string           `json:"currency"`
	ProviderID string           `json:"provider_id"`
	Model      string           `json:"model"`
	Source     string           `json:"source"`
	SourceRef  string           `json:"source_ref"`
	MappingRef string           `json:"mapping_ref"`
	AcquiredAt time.Time        `json:"acquired_at"`
	RateSource string           `json:"rate_source"`
	RateRef    string           `json:"rate_ref"`
	Rate       float64          `json:"rate"`
	Original   billingPriceWire `json:"original"`
	PeakFactor float64          `json:"peak_factor"`
	Effective  billingPriceWire `json:"effective"`
}
type billingDecisionWire struct {
	Mode                 provider.BillingMode     `json:"mode"`
	Exhausted            provider.QuotaExhaustion `json:"exhausted"`
	Legacy               bool                     `json:"legacy"`
	Free                 bool                     `json:"free"`
	Source               provider.BillingSource   `json:"source"`
	AllowanceIndependent bool                     `json:"allowance_independent"`
}
type billingWindowWire struct {
	Window        string                  `json:"window"`
	State         provider.QuotaState     `json:"state"`
	Kind          provider.QuotaEvidence  `json:"kind"`
	ObservationID string                  `json:"observation_id"`
	Revision      uint64                  `json:"revision"`
	Source        string                  `json:"source"`
	EvidenceID    string                  `json:"evidence_id"`
	Cycle         string                  `json:"cycle"`
	ObservedAt    time.Time               `json:"observed_at"`
	FreshUntil    time.Time               `json:"fresh_until"`
	ResetAt       time.Time               `json:"reset_at"`
	RecheckDue    bool                    `json:"recheck_due"`
	Capacity      *provider.QuotaCapacity `json:"capacity,omitempty"`
	Recovery      *provider.QuotaRecovery `json:"recovery,omitempty"`
	Blocker       *provider.QuotaBlocker  `json:"blocker,omitempty"`
}
type billingSnapshotWire struct {
	Subject    provider.QuotaSubject `json:"subject"`
	Scope      string                `json:"scope"`
	Version    provider.QuotaVersion `json:"version"`
	State      provider.QuotaState   `json:"state"`
	RecheckDue bool                  `json:"recheck_due"`
	Windows    []billingWindowWire   `json:"windows"`
}
type billingQuotaWire struct {
	Status   string                   `json:"status"`
	Reason   string                   `json:"reason"`
	Snapshot *billingSnapshotWire     `json:"snapshot,omitempty"`
	Coverage []provider.QuotaCoverage `json:"coverage"`
}
type billingCaptureWire struct {
	Binding          BillingBinding      `json:"binding"`
	Scope            string              `json:"scope"`
	CapturedAt       time.Time           `json:"captured_at"`
	AttemptAt        time.Time           `json:"attempt_at"`
	PolicyRef        string              `json:"policy_ref"`
	Decision         billingDecisionWire `json:"decision"`
	Quota            billingQuotaWire    `json:"quota"`
	Spend            *billingTariffWire  `json:"spend,omitempty"`
	Reference        *billingTariffWire  `json:"reference,omitempty"`
	ReferenceMissing string              `json:"reference_missing"`
}
type billingEnvelopeWire struct {
	Version    int                 `json:"version"`
	Algorithm  int                 `json:"algorithm"`
	Capture    billingCaptureWire  `json:"capture"`
	Completion BillingCompletion   `json:"completion"`
	Spend      *billingCalculation `json:"spend,omitempty"`
	Reference  *billingCalculation `json:"reference,omitempty"`
	Amounts    BillingAmounts      `json:"amounts"`
}

func EncodeBilling(e BillingEnvelope) ([]byte, error) {
	if e.data == nil {
		return nil, ErrBillingInvalid
	}
	w := billingToWire(*e.data)
	if _, err := billingFromWire(w); err != nil {
		return nil, ErrBillingInvalid
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, ErrBillingInvalid
	}
	if len(b) > BillingMaxBytes {
		return nil, ErrBillingTooLarge
	}
	return b, nil
}

// DecodeBilling never falls back to legacy pricing. Size and ambiguity precede
// version; an unambiguous future schema is unsupported, not a malformed v1.
func DecodeBilling(b []byte) (BillingEnvelope, error) {
	if len(b) > BillingMaxBytes {
		return BillingEnvelope{}, ErrBillingTooLarge
	}
	if strictjson.Validate(b) != nil {
		return BillingEnvelope{}, ErrBillingCorrupt
	}
	var header map[string]json.RawMessage
	if json.Unmarshal(b, &header) != nil || header == nil {
		return BillingEnvelope{}, ErrBillingCorrupt
	}
	raw, ok := header["version"]
	var version *int
	if !ok || json.Unmarshal(raw, &version) != nil || version == nil {
		return BillingEnvelope{}, ErrBillingCorrupt
	}
	if *version != BillingVersion {
		return BillingEnvelope{}, ErrBillingUnsupported
	}
	if !strictjson.Shape(b, reflect.TypeFor[billingEnvelopeWire]()) {
		return BillingEnvelope{}, ErrBillingCorrupt
	}
	var w billingEnvelopeWire
	if json.Unmarshal(b, &w) != nil {
		return BillingEnvelope{}, ErrBillingCorrupt
	}
	e, err := billingFromWire(w)
	if err != nil {
		return BillingEnvelope{}, ErrBillingCorrupt
	}
	return e, nil
}

func billingFromWire(w billingEnvelopeWire) (BillingEnvelope, error) {
	c, d := w.Capture, w.Capture.Decision
	in := BillingCapture{Version: w.Version, Algorithm: w.Algorithm, Binding: c.Binding, Scope: c.Scope, CapturedAt: c.CapturedAt, AttemptAt: c.AttemptAt, PolicyRef: c.PolicyRef,
		Decision: provider.BillingDecision{Policy: provider.BillingPolicy{Mode: d.Mode, Exhausted: d.Exhausted, Legacy: d.Legacy}, Free: d.Free, Source: d.Source, AllowanceIndependent: d.AllowanceIndependent},
		Quota:    billingQuotaFromWire(c.Quota), ReferenceMissing: c.ReferenceMissing}
	spend, reference := billingTariffFromWire(c.Spend), billingTariffFromWire(c.Reference)
	if spend != nil {
		in.Spend = &spend.Original
	}
	if reference != nil {
		in.Reference = &reference.Original
	}
	held, err := billingCapture(in, spend, reference, false)
	if err != nil {
		return BillingEnvelope{}, err
	}
	sealed, err := billingSeal(*held, w.Completion, false)
	if err != nil {
		return BillingEnvelope{}, err
	}
	if !reflect.DeepEqual(sealed.Spend, w.Spend) || !reflect.DeepEqual(sealed.Reference, w.Reference) || !reflect.DeepEqual(sealed.Amounts, w.Amounts) {
		return BillingEnvelope{}, ErrBillingInvalid
	}
	return BillingEnvelope{data: sealed}, nil
}

func billingToWire(e billingSealed) billingEnvelopeWire {
	c, d := e.Held.Input, e.Held.Input.Decision
	return billingEnvelopeWire{Version: c.Version, Algorithm: c.Algorithm,
		Capture: billingCaptureWire{Binding: c.Binding, Scope: c.Scope, CapturedAt: c.CapturedAt, AttemptAt: c.AttemptAt, PolicyRef: c.PolicyRef,
			Decision: billingDecisionWire{Mode: d.Policy.Mode, Exhausted: d.Policy.Exhausted, Legacy: d.Policy.Legacy, Free: d.Free, Source: d.Source, AllowanceIndependent: d.AllowanceIndependent},
			Quota:    billingQuotaToWire(c.Quota), Spend: billingTariffToWire(e.Held.Spend), Reference: billingTariffToWire(e.Held.Reference), ReferenceMissing: c.ReferenceMissing},
		Completion: billingCloneCompletion(e.Completion), Spend: e.Spend, Reference: e.Reference, Amounts: e.Amounts}
}
func billingPriceToWire(p catalog.Price) billingPriceWire {
	w := billingPriceWire{Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite, CacheWrite1h: p.CacheWrite1h, Peak: p.Peak, Tiers: []billingTierWire{}}
	for _, t := range p.Tiers {
		w.Tiers = append(w.Tiers, billingTierWire{Above: t.Above, Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, CacheWrite1h: t.CacheWrite1h})
	}
	return w
}
func billingPriceFromWire(w billingPriceWire) catalog.Price {
	p := catalog.Price{Input: w.Input, Output: w.Output, CacheRead: w.CacheRead, CacheWrite: w.CacheWrite, CacheWrite1h: w.CacheWrite1h, Peak: w.Peak, Tiers: []catalog.Tier{}}
	for _, t := range w.Tiers {
		p.Tiers = append(p.Tiers, catalog.Tier{Above: t.Above, Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, CacheWrite1h: t.CacheWrite1h})
	}
	return p
}
func billingTariffToWire(h *billingTariffHeld) *billingTariffWire {
	if h == nil {
		return nil
	}
	t := h.Original
	return &billingTariffWire{Currency: t.Currency, ProviderID: t.ProviderID, Model: t.Model, Source: t.Source, SourceRef: t.SourceRef, MappingRef: t.MappingRef, AcquiredAt: t.AcquiredAt, RateSource: t.RateSource, RateRef: t.RateRef, Rate: t.Rate, Original: billingPriceToWire(t.Price), PeakFactor: h.PeakFactor, Effective: billingPriceToWire(h.Effective)}
}
func billingTariffFromWire(w *billingTariffWire) *billingTariffHeld {
	if w == nil {
		return nil
	}
	return &billingTariffHeld{Original: BillingTariff{Currency: w.Currency, ProviderID: w.ProviderID, Model: w.Model, Source: w.Source, SourceRef: w.SourceRef, MappingRef: w.MappingRef, AcquiredAt: w.AcquiredAt, RateSource: w.RateSource, RateRef: w.RateRef, Rate: w.Rate, Price: billingPriceFromWire(w.Original)}, PeakFactor: w.PeakFactor, Effective: billingPriceFromWire(w.Effective)}
}
func billingQuotaToWire(q BillingQuota) billingQuotaWire {
	w := billingQuotaWire{Status: q.Status, Reason: q.Reason, Coverage: append([]provider.QuotaCoverage{}, q.Coverage...)}
	if q.Snapshot == nil {
		return w
	}
	s := q.Snapshot
	w.Snapshot = &billingSnapshotWire{Subject: s.Subject, Scope: s.Scope, Version: s.Version, State: s.State, RecheckDue: s.RecheckDue, Windows: []billingWindowWire{}}
	for _, v := range s.Windows {
		var blocker *provider.QuotaBlocker
		if v.Blocker != nil {
			b := *v.Blocker
			b.ObservedAt = billingUTC(b.ObservedAt)
			blocker = &b
		}
		w.Snapshot.Windows = append(w.Snapshot.Windows, billingWindowWire{Window: v.Window, State: v.State, Kind: v.Kind, ObservationID: v.ObservationID, Revision: v.Revision, Source: v.Source, EvidenceID: v.EvidenceID, Cycle: v.Cycle, ObservedAt: billingUTC(v.ObservedAt), FreshUntil: billingUTC(v.FreshUntil), ResetAt: billingUTC(v.ResetAt), RecheckDue: v.RecheckDue, Capacity: v.Capacity, Recovery: v.Recovery, Blocker: blocker})
	}
	return w
}
func billingQuotaFromWire(q billingQuotaWire) BillingQuota {
	out := BillingQuota{Status: q.Status, Reason: q.Reason, Coverage: q.Coverage}
	if q.Snapshot == nil {
		return out
	}
	s := q.Snapshot
	out.Snapshot = &provider.QuotaSnapshot{Subject: s.Subject, Scope: s.Scope, Version: s.Version, State: s.State, RecheckDue: s.RecheckDue, Windows: []provider.QuotaWindowSnapshot{}}
	for _, v := range s.Windows {
		out.Snapshot.Windows = append(out.Snapshot.Windows, provider.QuotaWindowSnapshot{Window: v.Window, State: v.State, Kind: v.Kind, ObservationID: v.ObservationID, Revision: v.Revision, Source: v.Source, EvidenceID: v.EvidenceID, Cycle: v.Cycle, ObservedAt: v.ObservedAt, FreshUntil: v.FreshUntil, ResetAt: v.ResetAt, RecheckDue: v.RecheckDue, Capacity: v.Capacity, Recovery: v.Recovery, Blocker: v.Blocker})
	}
	return out
}
