package usage

import (
	"math"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// CaptureBilling holds call-time facts. It performs no lookup, store operation
// or clock read; only construction resolves the supplied peak rule at AttemptAt.
func CaptureBilling(in BillingCapture) (BillingContext, error) {
	h, err := billingCapture(in, nil, nil, true)
	if err != nil {
		return BillingContext{}, err
	}
	return BillingContext{data: h}, nil
}

func billingCapture(in BillingCapture, spend, reference *billingTariffHeld, construct bool) (*billingHeld, error) {
	if in.Version != BillingVersion || in.Algorithm != BillingAlgorithm || !billingBindingValid(in.Binding) ||
		!billingID(in.Scope) || !billingID(in.PolicyRef) || !billingTime(in.CapturedAt) || !billingTime(in.AttemptAt) ||
		in.CapturedAt.After(in.AttemptAt) || !billingDecisionValid(in.Decision) {
		return nil, ErrBillingInvalid
	}
	in.CapturedAt, in.AttemptAt = billingUTC(in.CapturedAt), billingUTC(in.AttemptAt)
	q := in.Quota
	switch q.Status {
	case "materialized":
		if q.Reason != "" || q.Snapshot == nil || q.Snapshot.Subject != in.Binding.Subject || q.Snapshot.Scope != in.Scope {
			return nil, ErrBillingInvalid
		}
		snap, coverage, err := provider.CloneQuotaSnapshot(*q.Snapshot, q.Coverage, in.CapturedAt)
		if err != nil {
			return nil, ErrBillingInvalid
		}
		for i := range snap.Windows {
			w := &snap.Windows[i]
			if !billingWireTime(w.ObservedAt) || !billingWireTime(w.FreshUntil) || !billingWireTime(w.ResetAt) ||
				w.ObservedAt.After(in.CapturedAt) || w.Blocker != nil && (!billingWireTime(w.Blocker.ObservedAt) || w.Blocker.ObservedAt.After(in.CapturedAt)) {
				return nil, ErrBillingInvalid
			}
			if w.Capacity != nil {
				w.Capacity.Remaining = billingZero(w.Capacity.Remaining)
				w.Capacity.Limit = billingZero(w.Capacity.Limit)
			}
		}
		q.Snapshot, q.Coverage = &snap, coverage
	case "not_applicable", "not_observed", "unavailable":
		if q.Snapshot != nil || len(q.Coverage) != 0 {
			return nil, ErrBillingInvalid
		}
		if q.Status == "unavailable" {
			if !slices.Contains([]string{"missing", "corrupt", "unsupported", "io", "busy"}, q.Reason) {
				return nil, ErrBillingInvalid
			}
		} else if q.Reason != "" {
			return nil, ErrBillingInvalid
		}
		q.Coverage = []provider.QuotaCoverage{}
	default:
		return nil, ErrBillingInvalid
	}
	in.Quota = q
	if in.Reference == nil {
		if in.ReferenceMissing != "not_provided" && in.ReferenceMissing != "unavailable" {
			return nil, ErrBillingInvalid
		}
	} else if in.ReferenceMissing != "" {
		return nil, ErrBillingInvalid
	}
	var err error
	spend, err = billingHoldTariff(in.Spend, in, false, spend, construct)
	if err != nil {
		return nil, err
	}
	reference, err = billingHoldTariff(in.Reference, in, true, reference, construct)
	if err != nil {
		return nil, err
	}
	if spend != nil {
		source := in.Decision.Source
		zero := billingPriceZero(spend.Original.Price)
		if source == provider.BillingSourceUnknown ||
			source == provider.BillingSourceModel && (spend.Original.Source != "model_override" || zero != in.Decision.Free) ||
			source != provider.BillingSourceModel && spend.Original.Source == "model_override" ||
			source == provider.BillingSourceCatalogZero && !zero ||
			(source == provider.BillingSourceProvider || source == provider.BillingSourceVendor) && !zero ||
			source == provider.BillingSourceList && zero {
			return nil, ErrBillingInvalid
		}
		t := billingCloneTariff(spend.Original)
		in.Spend = &t
	}
	if reference != nil {
		t := billingCloneTariff(reference.Original)
		in.Reference = &t
	}
	return &billingHeld{Input: in, Spend: spend, Reference: reference}, nil
}

func billingDecisionValid(d provider.BillingDecision) bool {
	if d.Policy.Mode != provider.BillingList && d.Policy.Mode != provider.BillingFree && d.Policy.Mode != provider.BillingQuota {
		return false
	}
	if d.Policy.Exhausted != provider.QuotaStop && d.Policy.Exhausted != provider.QuotaPaid {
		return false
	}
	switch d.Source {
	case provider.BillingSourceModel:
		return d.AllowanceIndependent == d.Free
	case provider.BillingSourceProvider:
		return d.Policy.Mode == provider.BillingFree && d.Free && d.AllowanceIndependent
	case provider.BillingSourceVendor:
		return d.Policy.Mode == provider.BillingList && d.Free && d.AllowanceIndependent
	case provider.BillingSourceCatalogZero:
		return d.Policy.Mode == provider.BillingList && d.Free && !d.AllowanceIndependent
	case provider.BillingSourceList, provider.BillingSourceUnknown:
		return d.Policy.Mode == provider.BillingList && !d.Free && !d.AllowanceIndependent
	case provider.BillingSourceQuota:
		return d.Policy.Mode == provider.BillingQuota && d.Free
	}
	return false
}

func billingHoldTariff(t *BillingTariff, in BillingCapture, reference bool, saved *billingTariffHeld, construct bool) (*billingTariffHeld, error) {
	if t == nil {
		if saved != nil {
			return nil, ErrBillingInvalid
		}
		return nil, nil
	}
	if t.Currency != "USD" || t.ProviderID != in.Binding.ProviderID || t.Model != in.Binding.Model ||
		!billingID(t.SourceRef) || !billingTime(t.AcquiredAt) || t.AcquiredAt.After(in.CapturedAt) ||
		!billingFinite(t.Rate) || t.Rate <= 0 || !billingID(t.RateRef) || (t.RateSource != "published" && t.RateSource != "user") {
		return nil, ErrBillingInvalid
	}
	switch t.Source {
	case "provider_public", "model_override":
		if t.MappingRef != "" {
			return nil, ErrBillingInvalid
		}
	case "maker_public":
		if !billingID(t.MappingRef) {
			return nil, ErrBillingInvalid
		}
	default:
		return nil, ErrBillingInvalid
	}
	if reference && (t.Source == "model_override" || t.RateSource != "published") {
		return nil, ErrBillingInvalid
	}
	raw, ok := billingPrice(t.Price)
	if !ok {
		return nil, ErrBillingInvalid
	}
	factor := float64(1)
	if construct {
		if raw.Peak.At(in.AttemptAt) {
			factor = 2
		}
	} else {
		if saved == nil {
			return nil, ErrBillingInvalid
		}
		factor = saved.PeakFactor
		if factor != 1 && factor != 2 || raw.Peak == "" && factor != 1 {
			return nil, ErrBillingInvalid
		}
	}
	effective, ok := billingEffective(raw, t.Rate, factor)
	if !ok || !construct && !effective.Same(saved.Effective) {
		return nil, ErrBillingInvalid
	}
	original := *t
	original.Price = raw
	original.AcquiredAt = billingUTC(t.AcquiredAt)
	return &billingTariffHeld{Original: original, PeakFactor: factor, Effective: effective}, nil
}

func billingPrice(p catalog.Price) (catalog.Price, bool) {
	if p.Peak != "" && p.Peak != catalog.PeakDeepSeek && p.Peak != catalog.PeakWeekdays {
		return catalog.Price{}, false
	}
	m := settings.StatedPrice(p)
	m.CacheWrite1h = &p.CacheWrite1h
	out, bad := m.Price()
	if bad != "" {
		return catalog.Price{}, false
	}
	out.Peak = p.Peak
	out.Input, out.Output = billingZero(out.Input), billingZero(out.Output)
	out.CacheRead, out.CacheWrite, out.CacheWrite1h = billingZero(out.CacheRead), billingZero(out.CacheWrite), billingZero(out.CacheWrite1h)
	if out.Tiers == nil {
		out.Tiers = []catalog.Tier{}
	}
	for i := range out.Tiers {
		t := &out.Tiers[i]
		t.Input, t.Output = billingZero(t.Input), billingZero(t.Output)
		t.CacheRead, t.CacheWrite, t.CacheWrite1h = billingZero(t.CacheRead), billingZero(t.CacheWrite), billingZero(t.CacheWrite1h)
	}
	return out, true
}

func billingEffective(p catalog.Price, rate, peak float64) (catalog.Price, bool) {
	// Explicit conversions pin binary64 rounding before the next multiplier.
	part := func(v float64) float64 { return billingZero(float64(float64(v*rate) * peak)) }
	out := catalog.Price{Input: part(p.Input), Output: part(p.Output), CacheRead: part(p.CacheRead), CacheWrite: part(p.CacheWrite), CacheWrite1h: part(p.CacheWrite1h), Tiers: []catalog.Tier{}}
	for _, t := range p.Tiers {
		out.Tiers = append(out.Tiers, catalog.Tier{Above: t.Above, Input: part(t.Input), Output: part(t.Output), CacheRead: part(t.CacheRead), CacheWrite: part(t.CacheWrite), CacheWrite1h: part(t.CacheWrite1h)})
	}
	return billingPrice(out)
}

func billingPriceZero(p catalog.Price) bool {
	if p.Input != 0 || p.Output != 0 || p.CacheRead != 0 || p.CacheWrite != 0 || p.CacheWrite1h != 0 {
		return false
	}
	for _, t := range p.Tiers {
		if t.Input != 0 || t.Output != 0 || t.CacheRead != 0 || t.CacheWrite != 0 || t.CacheWrite1h != 0 {
			return false
		}
	}
	return true
}

// Seal is repeatable pure construction, NOT an idempotent durable commit.
func (c BillingContext) Seal(in BillingCompletion) (BillingEnvelope, error) {
	if c.data == nil {
		return BillingEnvelope{}, ErrBillingInvalid
	}
	out, err := billingSeal(*c.data, in, true)
	if err != nil {
		return BillingEnvelope{}, err
	}
	return BillingEnvelope{data: out}, nil
}

func billingSeal(h billingHeld, in BillingCompletion, construct bool) (*billingSealed, error) {
	c := h.Input
	if in.Binding != c.Binding || !billingTime(in.FinishedAt) || in.FinishedAt.Before(c.AttemptAt) || !billingUsageValid(in.Usage) {
		return nil, ErrBillingInvalid
	}
	in = billingCloneCompletion(in)
	in.FinishedAt = billingUTC(in.FinishedAt)
	d, decision := in.Disposition, c.Decision
	quota := decision.Source == provider.BillingSourceQuota
	unconditional := decision.Free && !quota
	switch d.Kind {
	case "policy_zero":
		if !unconditional || d.Evidence != nil {
			return nil, ErrBillingInvalid
		}
	case "tariff":
		if unconditional || decision.Source == provider.BillingSourceUnknown || quota != (d.Evidence != nil) {
			return nil, ErrBillingInvalid
		}
	case "included":
		if !quota || d.Evidence == nil {
			return nil, ErrBillingInvalid
		}
	case "unknown":
		if d.Evidence != nil {
			return nil, ErrBillingInvalid
		}
	default:
		return nil, ErrBillingInvalid
	}
	if d.Evidence != nil && !billingEvidenceValid(*d.Evidence, c.Binding, c.Scope, c.AttemptAt) {
		return nil, ErrBillingInvalid
	}
	if !billingReportsValid(in.Reported, in.Credits, c.Binding, c.Scope, c.AttemptAt, false) {
		return nil, ErrBillingInvalid
	}
	spend, err := billingCalculate(h.Spend, in.Usage, construct)
	if err != nil {
		return nil, err
	}
	reference, err := billingCalculate(h.Reference, in.Usage, construct)
	if err != nil {
		return nil, err
	}
	amounts := billingSelect(h, in, spend, reference)
	for _, v := range amounts.Values {
		if !billingValueValid(v) {
			return nil, ErrBillingInvalid
		}
	}
	return &billingSealed{Held: h, Completion: in, Spend: spend, Reference: reference, Amounts: amounts}, nil
}

func billingCalculate(h *billingTariffHeld, u BillingUsage, construct bool) (*billingCalculation, error) {
	if h == nil || !u.Known {
		return nil, nil
	}
	p := h.Effective
	prompt := u.Input + u.CacheRead + u.CacheWrite
	selected := billingCalculation{Tier: -1, Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite, CacheWrite1h: p.CacheWrite1h}
	for i, t := range p.Tiers {
		if prompt > t.Above {
			selected = billingCalculation{Tier: i, Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, CacheWrite1h: t.CacheWrite1h}
		}
	}
	if selected.CacheWrite1h == 0 {
		selected.CacheWrite1h = selected.CacheWrite
	}
	// V1 is five rounded products, a left-associated sum, then division. Do not
	// reassociate or consult today's catalogue/calendar while checking saved data.
	total := float64(float64(u.Input) * selected.Input)
	total = float64(total + float64(float64(u.Output)*selected.Output))
	total = float64(total + float64(float64(u.CacheRead)*selected.CacheRead))
	total = float64(total + float64(float64(u.CacheWrite-u.CacheWrite1h)*selected.CacheWrite))
	total = float64(total + float64(float64(u.CacheWrite1h)*selected.CacheWrite1h))
	selected.Amount = billingZero(float64(total / 1e6))
	if !billingFinite(selected.Amount) {
		return nil, ErrBillingInvalid
	}
	if construct {
		flat := p.At(prompt)
		if flat.Input != selected.Input || flat.Output != selected.Output || flat.CacheRead != selected.CacheRead || flat.CacheWrite != selected.CacheWrite || flat.OneHour() != selected.CacheWrite1h ||
			p.CostSplit(u.Input, u.Output, u.CacheRead, u.CacheWrite, u.CacheWrite1h) != selected.Amount {
			return nil, ErrBillingInvalid
		}
	}
	return &selected, nil
}

func billingSelect(h billingHeld, in BillingCompletion, spend, reference *billingCalculation) BillingAmounts {
	origin := "frozen"
	estimate := billingUnknown("estimate", origin, string(h.Input.Decision.Source), "no_basis")
	switch in.Disposition.Kind {
	case "policy_zero":
		estimate = billingKnown("estimate", origin, string(h.Input.Decision.Source), 0)
	case "included":
		estimate = billingKnown("estimate", origin, "included", 0)
	case "tariff":
		if spend != nil {
			estimate = billingKnown("estimate", origin, h.Spend.Original.Source, spend.Amount)
		} else {
			estimate.Reason = "missing_tariff"
			if !in.Usage.Known {
				estimate.Reason = "missing_usage"
			}
		}
	}
	ref := billingUnknown("reference", origin, "none", h.Input.ReferenceMissing)
	if reference != nil {
		ref = billingKnown("reference", origin, h.Reference.Original.Source, reference.Amount)
	} else if h.Reference != nil {
		ref.Source, ref.Reason = h.Reference.Original.Source, "missing_usage"
	}
	report := billingUnknown("reported", origin, "none", "not_provided")
	charge := estimate
	charge.Dimension = "charge"
	issues := []string{}
	if in.Disposition.Kind == "tariff" && h.Input.Decision.Source == provider.BillingSourceQuota && h.Input.Decision.Policy.Exhausted != provider.QuotaPaid {
		issues = append(issues, "paid_without_permission")
	}
	if in.Reported != nil {
		report = billingKnown("reported", origin, in.Reported.Kind, in.Reported.Amount)
		charge = report
		charge.Dimension = "charge"
		assertedPaid := in.Disposition.Kind == "tariff" && (in.Disposition.Evidence != nil || spend != nil && spend.Amount > 0)
		if report.Amount > 0 && (in.Disposition.Kind == "policy_zero" || in.Disposition.Kind == "included") || report.Amount == 0 && assertedPaid {
			charge = billingUnknown("charge", origin, "conflict", "conflict")
			issues = append(issues, "charge_conflict")
		}
	}
	difference := billingUnknown("difference", origin, "paired", "unpaired")
	if charge.Known == 1 && ref.Known == 1 {
		difference = billingKnown("difference", origin, ref.Source+"-minus-"+charge.Source, billingZero(ref.Amount-charge.Amount))
	}
	out := BillingAmounts{Values: []BillingValue{estimate, report, ref, charge, difference}, Issues: issues}
	for _, credit := range in.Credits {
		out.Values = append(out.Values, billingCreditValue(credit, origin))
	}
	return out
}

func billingReportsValid(report *BillingReport, credits []BillingCredit, binding BillingBinding, scope string, at time.Time, legacy bool) bool {
	kindOK := func(s string) bool { return s == "vendor_response" || legacy && s == "external_client" }
	if report != nil && (!kindOK(report.Kind) || report.Currency != "USD" || !billingMoney(report.Amount) || !billingEvidenceValid(report.Evidence, binding, scope, at)) {
		return false
	}
	if len(credits) > 1024 {
		return false
	}
	seen := make(map[string]bool)
	for _, c := range credits {
		if !kindOK(c.Kind) || !billingID(c.Authority) || !billingID(c.Unit) || c.Unit == "USD" || !billingMoney(c.Amount) || !billingEvidenceValid(c.Evidence, binding, scope, at) {
			return false
		}
		key := c.Authority + "\x00" + c.Unit + "\x00" + c.Evidence.SourceRef
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func billingEvidenceValid(e BillingEvidence, binding BillingBinding, scope string, at time.Time) bool {
	return e.Binding == binding && e.Scope == scope && billingID(e.Source) && billingID(e.SourceRef) && billingTime(e.AcquiredAt) && !e.AcquiredAt.Before(at)
}

func billingUsageValid(u BillingUsage) bool {
	if u.Input < 0 || u.Output < 0 || u.CacheRead < 0 || u.CacheWrite < 0 || u.CacheWrite1h < 0 || u.CacheWrite1h > u.CacheWrite {
		return false
	}
	maxInt := int(^uint(0) >> 1)
	if u.Input > maxInt-u.CacheRead || u.Input+u.CacheRead > maxInt-u.CacheWrite {
		return false
	}
	return u.Known || u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheWrite == 0 && u.CacheWrite1h == 0
}

func billingID(s string) bool {
	if s == "" || len(s) > 256 || strings.HasPrefix(s, "/") || strings.Contains(s, "://") || len(s) >= 3 && s[1] == ':' && s[2] == '/' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:/@+", c)) {
			return false
		}
	}
	return true
}
func billingBindingValid(b BillingBinding) bool {
	s := b.Subject
	return billingID(b.AttemptID) && billingID(b.ProviderID) && billingID(b.Model) && billingID(s.Authority) && billingID(s.Provider) && billingID(s.Principal) && billingID(s.Generation)
}
func billingTime(t time.Time) bool {
	return !t.IsZero() && billingWireTime(t)
}

// The envelope encodes UTC, whose year can cross a JSON boundary even when
// the original offset timestamp is representable. Optional quota zeroes remain valid.
func billingWireTime(t time.Time) bool {
	if _, err := t.MarshalJSON(); err != nil {
		return false
	}
	_, err := billingUTC(t).MarshalJSON()
	return err == nil
}
func billingUTC(t time.Time) time.Time { return t.Round(0).UTC() }
func billingFinite(v float64) bool     { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func billingMoney(v float64) bool      { return billingFinite(v) && v >= 0 }
func billingZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}
func billingKnown(dimension, origin, source string, amount float64) BillingValue {
	return BillingValue{Dimension: dimension, Origin: origin, Source: source, Unit: "USD", Amount: billingZero(amount), Known: 1}
}
func billingUnknown(dimension, origin, source, reason string) BillingValue {
	return BillingValue{Dimension: dimension, Origin: origin, Source: source, Reason: reason, Unit: "USD", Unknown: 1}
}
func billingCreditValue(c BillingCredit, origin string) BillingValue {
	return BillingValue{Dimension: "credits", Origin: origin, Source: c.Kind, Unit: c.Unit, Authority: c.Authority, Amount: billingZero(c.Amount), Known: 1}
}

func billingCloneTariff(t BillingTariff) BillingTariff {
	t.Price.Tiers = append([]catalog.Tier{}, t.Price.Tiers...)
	return t
}
func billingCloneCompletion(c BillingCompletion) BillingCompletion {
	if c.Disposition.Evidence != nil {
		e := *c.Disposition.Evidence
		e.AcquiredAt = billingUTC(e.AcquiredAt)
		c.Disposition.Evidence = &e
	}
	if c.Reported != nil {
		r := *c.Reported
		r.Amount = billingZero(r.Amount)
		r.Evidence.AcquiredAt = billingUTC(r.Evidence.AcquiredAt)
		c.Reported = &r
	}
	c.Credits = append([]BillingCredit{}, c.Credits...)
	for i := range c.Credits {
		c.Credits[i].Amount = billingZero(c.Credits[i].Amount)
		c.Credits[i].Evidence.AcquiredAt = billingUTC(c.Credits[i].Evidence.AcquiredAt)
	}
	return c
}
func billingCloneInput(in BillingCapture) BillingCapture {
	if in.Spend != nil {
		t := billingCloneTariff(*in.Spend)
		in.Spend = &t
	}
	if in.Reference != nil {
		t := billingCloneTariff(*in.Reference)
		in.Reference = &t
	}
	if in.Quota.Snapshot != nil {
		s, c, _ := provider.CloneQuotaSnapshot(*in.Quota.Snapshot, in.Quota.Coverage, in.CapturedAt)
		in.Quota.Snapshot, in.Quota.Coverage = &s, c
	} else {
		in.Quota.Coverage = []provider.QuotaCoverage{}
	}
	return in
}
func (c BillingContext) Input() BillingCapture {
	if c.data == nil {
		return BillingCapture{}
	}
	return billingCloneInput(c.data.Input)
}
func (e BillingEnvelope) Capture() BillingCapture {
	if e.data == nil {
		return BillingCapture{}
	}
	return billingCloneInput(e.data.Held.Input)
}
func (e BillingEnvelope) Completion() BillingCompletion {
	if e.data == nil {
		return BillingCompletion{}
	}
	return billingCloneCompletion(e.data.Completion)
}
func (e BillingEnvelope) Amounts() BillingAmounts {
	if e.data == nil {
		return BillingAmounts{}
	}
	return BillingAmounts{Values: append([]BillingValue{}, e.data.Amounts.Values...), Issues: append([]string{}, e.data.Amounts.Issues...)}
}

// ProjectLegacyBilling labels supplied current-price estimates without inventing
// historical policy, reference savings or eligibility for gateway budgets.
func ProjectLegacyBilling(in BillingLegacyInput) (BillingAmounts, error) {
	if !billingBindingValid(in.Binding) || !billingID(in.Scope) || !billingTime(in.At) || !billingReportsValid(in.Reported, in.Credits, in.Binding, in.Scope, in.At, true) {
		return BillingAmounts{}, ErrBillingInvalid
	}
	estimate := billingUnknown("estimate", "legacy", "current_price", in.EstimateMissing)
	if in.CurrentEstimate == nil {
		if in.EstimateSource != "" || in.EstimateMissing != "not_provided" && in.EstimateMissing != "unavailable" {
			return BillingAmounts{}, ErrBillingInvalid
		}
	} else {
		if !billingMoney(*in.CurrentEstimate) || !billingID(in.EstimateSource) || in.EstimateMissing != "" {
			return BillingAmounts{}, ErrBillingInvalid
		}
		estimate = billingKnown("estimate", "legacy", in.EstimateSource, *in.CurrentEstimate)
	}
	report := billingUnknown("reported", "legacy", "none", "not_provided")
	charge := estimate
	charge.Dimension = "charge"
	if in.Reported != nil {
		report = billingKnown("reported", "legacy", in.Reported.Kind, in.Reported.Amount)
		charge = report
		charge.Dimension = "charge"
	}
	out := BillingAmounts{Values: []BillingValue{estimate, report, billingUnknown("reference", "legacy", "none", "legacy_history"), charge, billingUnknown("difference", "legacy", "paired", "legacy_history")}, Issues: []string{}}
	for _, c := range in.Credits {
		out.Values = append(out.Values, billingCreditValue(c, "legacy"))
	}
	return out, nil
}

func billingValueValid(v BillingValue) bool {
	if v.Origin != "frozen" && v.Origin != "legacy" || !billingID(v.Source) || !billingFinite(v.Amount) || v.Known == 0 && v.Unknown == 0 || v.Known == 0 && v.Amount != 0 || v.Amount < 0 && v.Dimension != "difference" {
		return false
	}
	if v.Unknown == 0 && v.Reason != "" || v.Unknown > 0 && !slices.Contains([]string{"not_provided", "unavailable", "missing_tariff", "missing_usage", "no_basis", "conflict", "unpaired", "legacy_history"}, v.Reason) {
		return false
	}
	if v.Origin == "legacy" && (v.Dimension == "reference" || v.Dimension == "difference") && (v.Known != 0 || v.Reason != "legacy_history") {
		return false
	}
	if (v.Dimension == "reported" || v.Dimension == "credits") && v.Known > 0 && v.Source != "vendor_response" && !(v.Origin == "legacy" && v.Source == "external_client") {
		return false
	}
	switch v.Dimension {
	case "estimate", "reported", "reference", "charge", "difference":
		return v.Unit == "USD" && v.Authority == ""
	case "credits":
		return billingID(v.Unit) && v.Unit != "USD" && billingID(v.Authority)
	}
	return false
}
func billingKey(v BillingValue) billingValueKey {
	return billingValueKey{v.Dimension, v.Origin, v.Source, v.Reason, v.Unit, v.Authority}
}

// Add validates the entire input and coverage counters before mutation.
func (t *BillingTotals) Add(a BillingAmounts) error {
	if t == nil || len(a.Values) == 0 {
		return ErrBillingInvalid
	}
	delta := BillingTotals{values: make(map[billingValueKey]billingTotal), issues: make(map[string]bool)}
	for _, v := range a.Values {
		if !billingValueValid(v) {
			return ErrBillingInvalid
		}
		entry := billingTotal{Value: v}
		entry.Exact.SetFloat64(v.Amount)
		if err := delta.billingAddTotal(billingKey(v), entry); err != nil {
			return err
		}
	}
	for _, issue := range a.Issues {
		if issue != "charge_conflict" && issue != "paid_without_permission" {
			return ErrBillingInvalid
		}
		delta.issues[issue] = true
	}
	return t.Merge(&delta)
}
func (t *BillingTotals) billingAddTotal(key billingValueKey, incoming billingTotal) error {
	old, exists := t.values[key]
	if !exists {
		old.Value = incoming.Value
		old.Value.Known, old.Value.Unknown = 0, 0
	}
	if old.Value.Known > ^uint64(0)-incoming.Value.Known || old.Value.Unknown > ^uint64(0)-incoming.Value.Unknown {
		return ErrBillingInvalid
	}
	old.Value.Known += incoming.Value.Known
	old.Value.Unknown += incoming.Value.Unknown
	var exact big.Rat
	exact.Add(&old.Exact, &incoming.Exact)
	old.Exact = exact
	old.Value.Amount = 0
	t.values[key] = old
	return nil
}
func (t *BillingTotals) Merge(other *BillingTotals) error {
	if t == nil || other == nil {
		return ErrBillingInvalid
	}
	next := BillingTotals{values: make(map[billingValueKey]billingTotal), issues: make(map[string]bool)}
	for key, v := range t.values {
		var exact big.Rat
		exact.Set(&v.Exact)
		v.Exact = exact
		next.values[key] = v
	}
	for key, v := range other.values {
		if err := next.billingAddTotal(key, v); err != nil {
			return err
		}
	}
	for issue := range t.issues {
		next.issues[issue] = true
	}
	for issue := range other.issues {
		next.issues[issue] = true
	}
	*t = next
	return nil
}
func (t *BillingTotals) Project() (BillingAmounts, error) {
	if t == nil {
		return BillingAmounts{}, ErrBillingInvalid
	}
	out := BillingAmounts{Values: []BillingValue{}, Issues: []string{}}
	for _, total := range t.values {
		v := total.Value
		v.Amount, _ = total.Exact.Float64()
		v.Amount = billingZero(v.Amount)
		if !billingValueValid(v) {
			return BillingAmounts{}, ErrBillingInvalid
		}
		out.Values = append(out.Values, v)
	}
	slices.SortFunc(out.Values, func(a, b BillingValue) int {
		for _, pair := range [][2]string{{a.Origin, b.Origin}, {a.Dimension, b.Dimension}, {a.Source, b.Source}, {a.Reason, b.Reason}, {a.Unit, b.Unit}, {a.Authority, b.Authority}} {
			if n := strings.Compare(pair[0], pair[1]); n != 0 {
				return n
			}
		}
		return 0
	})
	for issue := range t.issues {
		out.Issues = append(out.Issues, issue)
	}
	slices.Sort(out.Issues)
	return out, nil
}
