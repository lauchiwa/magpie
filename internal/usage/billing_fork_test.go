package usage

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

func billingFixture() (BillingCapture, BillingCompletion) {
	at := time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC)
	b := BillingBinding{AttemptID: "attempt-1", Subject: provider.QuotaSubject{Authority: "authority-1", Provider: "provider-incarnation-1", Principal: "principal-1", Generation: "generation-1"}, ProviderID: "relay", Model: "maker/model"}
	tariff := &BillingTariff{Currency: "USD", ProviderID: b.ProviderID, Model: b.Model, Source: "provider_public", SourceRef: "published-1", AcquiredAt: at.Add(-time.Hour), Rate: 1, RateSource: "published", RateRef: "published-rate-1",
		Price: catalog.Price{Input: 2, Output: 4, CacheRead: .5, CacheWrite: 1.25, CacheWrite1h: 2.5, Tiers: []catalog.Tier{{Above: 100, Input: 4, Output: 8, CacheRead: 1, CacheWrite: 2.5, CacheWrite1h: 5}}}}
	ref := billingCloneTariff(*tariff)
	ref.Price = ref.Price.Times(2)
	ref.SourceRef = "reference-1"
	in := BillingCapture{Version: 1, Algorithm: 1, Binding: b, Scope: "chat", CapturedAt: at, AttemptAt: at.Add(time.Second), PolicyRef: "policy-revision-1", Decision: provider.BillingDecision{Policy: provider.BillingPolicy{Mode: provider.BillingList, Exhausted: provider.QuotaStop}, Source: provider.BillingSourceList}, Quota: BillingQuota{Status: "not_observed"}, Spend: tariff, Reference: &ref}
	completion := BillingCompletion{Binding: b, FinishedAt: at.Add(time.Minute), Usage: BillingUsage{Known: true, Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, CacheWrite1h: 10}, Disposition: BillingDisposition{Kind: "tariff"}}
	return in, completion
}
func billingMustSeal(t *testing.T, in BillingCapture, c BillingCompletion) BillingEnvelope {
	t.Helper()
	held, err := CaptureBilling(in)
	if err != nil {
		t.Fatal(err)
	}
	e, err := held.Seal(c)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func billingFind(t *testing.T, a BillingAmounts, dimension string) BillingValue {
	t.Helper()
	for _, v := range a.Values {
		if v.Dimension == dimension {
			return v
		}
	}
	t.Fatalf("missing dimension %s", dimension)
	return BillingValue{}
}
func billingAssertValue(t *testing.T, a BillingAmounts, dimension string, amount float64, known bool) {
	t.Helper()
	v := billingFind(t, a, dimension)
	if v.Amount != amount || v.Known != uint64(map[bool]int{true: 1, false: 0}[known]) || v.Unknown != uint64(map[bool]int{true: 0, false: 1}[known]) {
		t.Fatalf("%s = %+v; want %v known=%v", dimension, v, amount, known)
	}
}
func billingEvidence(in BillingCapture) *BillingEvidence {
	return &BillingEvidence{Binding: in.Binding, Scope: in.Scope, Source: "vendor", SourceRef: "charge-evidence-1", AcquiredAt: in.AttemptAt.Add(time.Second)}
}
func billingReport(in BillingCapture, amount float64) *BillingReport {
	return &BillingReport{Evidence: *billingEvidence(in), Kind: "vendor_response", Currency: "USD", Amount: amount}
}

func TestBillingCaptureDetachedAndRepeatedSeal(t *testing.T) {
	in, c := billingFixture()
	ctx, err := CaptureBilling(in)
	if err != nil {
		t.Fatal(err)
	}
	before := billingMustSeal(t, in, c)
	want, err := EncodeBilling(before)
	if err != nil {
		t.Fatal(err)
	}
	in.Spend.Price.Tiers[0].Input = 999
	in.Reference.Price.Tiers[0].Output = 999
	in.Spend.Rate = 999
	in.Binding.Subject.Generation = "recreated"
	in.Decision.Free = true
	gotInput := ctx.Input()
	gotInput.Spend.Price.Tiers[0].CacheRead = 999
	gotInput.Reference.Price.Input = 999
	for range 2 {
		got, err := ctx.Seal(c)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := EncodeBilling(got)
		if err != nil || !bytes.Equal(encoded, want) {
			t.Fatal("capture changed after caller/getter mutation", err)
		}
	}
	changed := c
	changed.Binding.AttemptID = "retry-2"
	if _, err := ctx.Seal(changed); !errors.Is(err, ErrBillingInvalid) {
		t.Fatal("retry reused prior capture")
	}
	for _, mutate := range []func(*BillingBinding){func(b *BillingBinding) { b.Subject.Authority = "new" }, func(b *BillingBinding) { b.Subject.Provider = "new" }, func(b *BillingBinding) { b.Subject.Principal = "new" }, func(b *BillingBinding) { b.Subject.Generation = "new" }, func(b *BillingBinding) { b.Model = "other" }} {
		bad := c
		mutate(&bad.Binding)
		if _, err := ctx.Seal(bad); !errors.Is(err, ErrBillingInvalid) {
			t.Fatal("identity mismatch accepted")
		}
	}
}

func TestBillingTariffArithmeticV1(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  int
		hour   float64
		rate   float64
		peak   catalog.Peak
		amount float64
		tier   int
	}{
		{"base-five-terms", 10, 2.5, 1, "", .0001775, -1},
		{"threshold-equal", 30, 2.5, 1, "", .0002175, -1},
		{"threshold-above", 31, 2.5, 1, "", .000439, 0},
		{"hour-fallback", 10, 0, 1, "", .000165, -1},
		{"independent-rate-peak", 10, 2.5, 3, catalog.PeakWeekdays, .001065, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, c := billingFixture()
			in.Spend.Price.CacheWrite1h = tc.hour
			in.Spend.Rate = tc.rate
			in.Spend.Price.Peak = tc.peak
			c.Usage.Input = tc.input
			e := billingMustSeal(t, in, c)
			billingAssertValue(t, e.Amounts(), "estimate", tc.amount, true)
			if e.data.Spend.Tier != tc.tier {
				t.Fatal("wrong selected tier")
			}
			if tc.rate != 1 {
				billingAssertValue(t, e.Amounts(), "reference", .000355, true)
				if billingFind(t, e.Amounts(), "difference").Amount >= 0 {
					t.Fatal("premium clamped")
				}
			}
		})
	}
	in, c := billingFixture()
	in.Spend.Price.Peak = catalog.PeakDeepSeek
	in.CapturedAt = time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	in.AttemptAt = in.CapturedAt
	in.Spend.AcquiredAt = in.CapturedAt
	in.Reference.AcquiredAt = in.CapturedAt
	c.FinishedAt = in.AttemptAt
	e := billingMustSeal(t, in, c)
	if e.data.Held.Spend.PeakFactor != 1 {
		t.Fatal("public holiday was peak")
	}
	in, c = billingFixture()
	in.Spend.Price.CacheWrite1h = 0
	in.Spend.Price.Tiers[0].CacheWrite1h = 0
	c.Usage.Input = 31
	e = billingMustSeal(t, in, c)
	if e.data.Spend.CacheWrite1h != 2.5 {
		t.Fatal("tier hour fallback not selected write rate")
	}
}

func TestBillingDecisionAndDispositionTable(t *testing.T) {
	for _, tc := range []struct {
		name              string
		mode              provider.BillingMode
		source            provider.BillingSource
		free, independent bool
		kind              string
		tariff            bool
		permission        provider.QuotaExhaustion
		evidence          bool
		known             bool
		invalid           bool
		issue             bool
	}{
		{name: "list", mode: provider.BillingList, source: provider.BillingSourceList, kind: "tariff", tariff: true, known: true},
		{name: "list-missing", mode: provider.BillingList, source: provider.BillingSourceList, kind: "tariff"},
		{name: "exact-over-quota", mode: provider.BillingQuota, source: provider.BillingSourceModel, kind: "tariff", tariff: true, known: true},
		{name: "exact-over-free", mode: provider.BillingFree, source: provider.BillingSourceModel, kind: "tariff", tariff: true, known: true},
		{name: "model-zero", mode: provider.BillingQuota, source: provider.BillingSourceModel, free: true, independent: true, kind: "policy_zero", known: true},
		{name: "provider-zero", mode: provider.BillingFree, source: provider.BillingSourceProvider, free: true, independent: true, kind: "policy_zero", known: true},
		{name: "vendor-zero", mode: provider.BillingList, source: provider.BillingSourceVendor, free: true, independent: true, kind: "policy_zero", known: true},
		{name: "catalog-zero-not-independent", mode: provider.BillingList, source: provider.BillingSourceCatalogZero, free: true, kind: "policy_zero", known: true},
		{name: "quota-not-proof", mode: provider.BillingQuota, source: provider.BillingSourceQuota, free: true, kind: "unknown"},
		{name: "quota-independent-not-proof", mode: provider.BillingQuota, source: provider.BillingSourceQuota, free: true, independent: true, kind: "unknown"},
		{name: "quota-included", mode: provider.BillingQuota, source: provider.BillingSourceQuota, free: true, kind: "included", evidence: true, known: true},
		{name: "quota-paid-opt-in", mode: provider.BillingQuota, source: provider.BillingSourceQuota, free: true, kind: "tariff", tariff: true, evidence: true, permission: provider.QuotaPaid, known: true},
		{name: "quota-paid-no-permission", mode: provider.BillingQuota, source: provider.BillingSourceQuota, free: true, kind: "tariff", tariff: true, evidence: true, known: true, issue: true},
		{name: "unknown", mode: provider.BillingList, source: provider.BillingSourceUnknown, kind: "unknown"},
		{name: "quota-zero-invalid", mode: provider.BillingQuota, source: provider.BillingSourceQuota, free: true, kind: "policy_zero", invalid: true},
		{name: "included-list-invalid", mode: provider.BillingList, source: provider.BillingSourceList, kind: "included", evidence: true, invalid: true},
		{name: "quota-paid-without-evidence", mode: provider.BillingQuota, source: provider.BillingSourceQuota, free: true, kind: "tariff", tariff: true, invalid: true},
		{name: "list-extra-evidence", mode: provider.BillingList, source: provider.BillingSourceList, kind: "tariff", tariff: true, evidence: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, c := billingFixture()
			if tc.permission == "" {
				tc.permission = provider.QuotaStop
			}
			in.Decision = provider.BillingDecision{Policy: provider.BillingPolicy{Mode: tc.mode, Exhausted: tc.permission}, Source: tc.source, Free: tc.free, AllowanceIndependent: tc.independent}
			if !tc.tariff {
				in.Spend = nil
			} else if tc.source == provider.BillingSourceModel {
				in.Spend.Source = "model_override"
			}
			c.Disposition = BillingDisposition{Kind: tc.kind}
			if tc.evidence {
				c.Disposition.Evidence = billingEvidence(in)
			}
			ctx, err := CaptureBilling(in)
			if err != nil {
				t.Fatal(err)
			}
			e, err := ctx.Seal(c)
			if tc.invalid {
				if !errors.Is(err, ErrBillingInvalid) {
					t.Fatal("invalid branch accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := float64(0)
			if tc.tariff && tc.known {
				want = .0001775
			}
			billingAssertValue(t, e.Amounts(), "estimate", want, tc.known)
			if slices.Contains(e.Amounts().Issues, "paid_without_permission") != tc.issue {
				t.Fatal("authorization issue lost or invented")
			}
		})
	}
	// A decision from the real shared resolver satisfies the same contract.
	in, c := billingFixture()
	p := provider.Provider{ID: in.Binding.ProviderID, Billing: provider.BillingQuota}
	s := settings.Settings{ModelPrices: map[string]settings.ModelPrice{p.ID + "/" + in.Binding.Model: settings.StatedPrice(in.Spend.Price)}}
	decision, err := p.ResolveBilling(s, catalog.Model{ID: in.Binding.Model})
	if err != nil {
		t.Fatal(err)
	}
	in.Decision = decision
	in.Spend.Source = "model_override"
	e := billingMustSeal(t, in, c)
	if len(e.Amounts().Issues) != 0 {
		t.Fatal("paid exact model inherited quota permission")
	}
}

func TestBillingReportSelectionAndUnknownUsage(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		report     *float64
		usage      bool
		zeroPolicy bool
		known      bool
		charge     float64
		conflict   bool
	}{
		{"report-wins", "tariff", new(.5), true, false, true, .5, false},
		{"reported-zero-conflicts-paid", "tariff", new(0.0), true, false, false, 0, true},
		{"reported-zero-unknown-usage", "unknown", new(0.0), false, false, true, 0, false},
		{"reported-money-zero-tokens", "unknown", new(2.0), false, false, true, 2, false},
		{"missing-usage-not-zero", "tariff", nil, false, false, false, 0, false},
		{"unconditional-zero-no-usage", "policy_zero", nil, false, true, true, 0, false},
		{"positive-versus-policy-zero", "policy_zero", new(2.0), true, true, false, 0, true},
		{"zero-versus-policy-zero", "policy_zero", new(0.0), true, true, true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, c := billingFixture()
			c.Disposition.Kind = tc.kind
			if !tc.usage {
				c.Usage = BillingUsage{}
			}
			if tc.zeroPolicy {
				in.Decision = provider.BillingDecision{Policy: provider.BillingPolicy{Mode: provider.BillingFree, Exhausted: provider.QuotaStop}, Source: provider.BillingSourceProvider, Free: true, AllowanceIndependent: true}
				in.Spend = nil
			}
			if tc.report != nil {
				c.Reported = billingReport(in, *tc.report)
			}
			e := billingMustSeal(t, in, c)
			a := e.Amounts()
			billingAssertValue(t, a, "charge", tc.charge, tc.known)
			if tc.report != nil {
				billingAssertValue(t, a, "reported", *tc.report, true)
			}
			if slices.Contains(a.Issues, "charge_conflict") != tc.conflict {
				t.Fatal("conflict not retained")
			}
			if !tc.usage || tc.conflict {
				billingAssertValue(t, a, "difference", 0, false)
			}
		})
	}
	in, c := billingFixture()
	in.Spend = nil
	in.Decision.Source = provider.BillingSourceUnknown
	c.Disposition.Kind = "unknown"
	c.Reported = billingReport(in, .2)
	e := billingMustSeal(t, in, c)
	billingAssertValue(t, e.Amounts(), "charge", .2, true)
	billingAssertValue(t, e.Amounts(), "estimate", 0, false)
	c.Reported.Kind = "external_client"
	ctx, _ := CaptureBilling(in)
	if _, err := ctx.Seal(c); !errors.Is(err, ErrBillingInvalid) {
		t.Fatal("external report accepted in gateway envelope")
	}
	in, c = billingFixture()
	c.Usage = BillingUsage{Known: true}
	e = billingMustSeal(t, in, c)
	billingAssertValue(t, e.Amounts(), "estimate", 0, true)
}

func TestBillingReportsAndCreditsDetach(t *testing.T) {
	in, c := billingFixture()
	c.Reported = billingReport(in, .5)
	c.Credits = []BillingCredit{{Evidence: *billingEvidence(in), Kind: "vendor_response", Authority: "vendor-a", Unit: "credit", Amount: 4}, {Evidence: *billingEvidence(in), Kind: "vendor_response", Authority: "vendor-b", Unit: "credit", Amount: 8}}
	e := billingMustSeal(t, in, c)
	want, _ := EncodeBilling(e)
	c.Reported.Amount = 999
	c.Credits[0].Amount = 999
	exposed := e.Completion()
	exposed.Reported.Amount = 777
	exposed.Credits[0].Amount = 777
	amounts := e.Amounts()
	amounts.Values[0].Amount = 777
	got, _ := EncodeBilling(e)
	if !bytes.Equal(want, got) {
		t.Fatal("completion/getter aliased envelope")
	}
	var totals BillingTotals
	if err := totals.Add(e.Amounts()); err != nil {
		t.Fatal(err)
	}
	a, err := totals.Project()
	if err != nil {
		t.Fatal(err)
	}
	credits := 0
	for _, v := range a.Values {
		if v.Dimension == "credits" {
			credits++
			if v.Amount != 4 && v.Amount != 8 {
				t.Fatal("credits crossed authorities")
			}
		}
	}
	if credits != 2 {
		t.Fatal("credits collapsed")
	}
}

func TestBillingInvalidInput(t *testing.T) {
	for name, change := range map[string]func(*BillingCapture){
		"version": func(c *BillingCapture) { c.Version = 2 }, "algorithm": func(c *BillingCapture) { c.Algorithm = 2 }, "capture-after-attempt": func(c *BillingCapture) { c.CapturedAt = c.AttemptAt.Add(time.Second) },
		"zero-time": func(c *BillingCapture) { c.AttemptAt = time.Time{} }, "bad-time": func(c *BillingCapture) { c.AttemptAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"future-tariff": func(c *BillingCapture) { c.Spend.AcquiredAt = c.AttemptAt }, "negative-hour": func(c *BillingCapture) { c.Spend.Price.CacheWrite1h = -1 }, "nan": func(c *BillingCapture) { c.Spend.Price.Input = math.NaN() }, "inf": func(c *BillingCapture) { c.Spend.Rate = math.Inf(1) }, "zero-rate": func(c *BillingCapture) { c.Spend.Rate = 0 },
		"rate-overflow": func(c *BillingCapture) { c.Spend.Rate = math.MaxFloat64 }, "negative-tier": func(c *BillingCapture) { c.Spend.Price.Tiers[0].Output = -1 }, "unordered-tier": func(c *BillingCapture) { c.Spend.Price.Tiers = append(c.Spend.Price.Tiers, c.Spend.Price.Tiers[0]) }, "bad-peak": func(c *BillingCapture) { c.Spend.Price.Peak = "other" },
		"reference-discount": func(c *BillingCapture) { c.Reference.Rate = .5; c.Reference.RateSource = "user" }, "reference-override": func(c *BillingCapture) { c.Reference.Source = "model_override" }, "maker-no-mapping": func(c *BillingCapture) { c.Reference.Source = "maker_public" }, "missing-reason": func(c *BillingCapture) { c.Reference = nil },
		"currency": func(c *BillingCapture) { c.Spend.Currency = "CNY" }, "model-mismatch": func(c *BillingCapture) { c.Spend.Model = "other" }, "noncanonical-id": func(c *BillingCapture) { c.Binding.AttemptID = "private raw payload" },
		"unknown-quota-status": func(c *BillingCapture) { c.Quota.Status = "maybe" }, "unavailable-no-category": func(c *BillingCapture) { c.Quota.Status = "unavailable" },
		"bad-decision-mode": func(c *BillingCapture) { c.Decision.Policy.Mode = "maybe" }, "bad-exhaustion": func(c *BillingCapture) { c.Decision.Policy.Exhausted = "maybe" }, "free-list": func(c *BillingCapture) { c.Decision.Free = true }, "independent-list": func(c *BillingCapture) { c.Decision.AllowanceIndependent = true },
		"catalog-independent": func(c *BillingCapture) {
			c.Spend = nil
			c.Decision.Source = provider.BillingSourceCatalogZero
			c.Decision.Free = true
			c.Decision.AllowanceIndependent = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			in, _ := billingFixture()
			change(&in)
			got, err := CaptureBilling(in)
			if !errors.Is(err, ErrBillingInvalid) || got.data != nil {
				t.Fatal("invalid capture accepted", err)
			}
		})
	}
	for name, change := range map[string]func(*BillingCapture, *BillingCompletion){
		"negative-token": func(_ *BillingCapture, c *BillingCompletion) { c.Usage.Input = -1 }, "unknown-counts": func(_ *BillingCapture, c *BillingCompletion) { c.Usage.Known = false }, "hour-over-write": func(_ *BillingCapture, c *BillingCompletion) { c.Usage.CacheWrite1h = c.Usage.CacheWrite + 1 }, "sum-overflow": func(_ *BillingCapture, c *BillingCompletion) { c.Usage.Input = int(^uint(0) >> 1) },
		"cost-overflow": func(in *BillingCapture, c *BillingCompletion) {
			in.Spend.Price = catalog.Price{Input: math.MaxFloat64}
			c.Usage.Input = 2
		}, "finish-before": func(in *BillingCapture, c *BillingCompletion) { c.FinishedAt = in.AttemptAt.Add(-time.Second) },
		"report-binding": func(in *BillingCapture, c *BillingCompletion) {
			c.Reported = billingReport(*in, 2)
			c.Reported.Evidence.Binding.AttemptID = "other"
		}, "report-time": func(in *BillingCapture, c *BillingCompletion) {
			c.Reported = billingReport(*in, 2)
			c.Reported.Evidence.AcquiredAt = in.CapturedAt
		}, "report-nan": func(in *BillingCapture, c *BillingCompletion) { c.Reported = billingReport(*in, math.NaN()) },
	} {
		t.Run(name, func(t *testing.T) {
			in, c := billingFixture()
			change(&in, &c)
			ctx, err := CaptureBilling(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ctx.Seal(c)
			if !errors.Is(err, ErrBillingInvalid) || got.data != nil {
				t.Fatal("invalid completion accepted", err)
			}
		})
	}
}

func billingQuotaFixture(in BillingCapture, available bool) BillingQuota {
	q := BillingQuota{Status: "materialized", Coverage: []provider.QuotaCoverage{{Window: "day", Scopes: []string{in.Scope}}, {Window: "week", Scopes: []string{in.Scope}}}}
	q.Snapshot = &provider.QuotaSnapshot{Subject: in.Binding.Subject, Scope: in.Scope, Version: provider.QuotaVersion{StoreEpoch: "epoch-1", Revision: 1}, State: provider.QuotaStateUnknown, Windows: []provider.QuotaWindowSnapshot{{Window: "day", State: provider.QuotaStateUnknown}, {Window: "week", State: provider.QuotaStateUnknown}}}
	q.Snapshot.State = provider.QuotaStateAvailable
	for i := range q.Snapshot.Windows {
		w := &q.Snapshot.Windows[i]
		w.State = provider.QuotaStateAvailable
		w.Kind = provider.QuotaReportedAvailable
		w.ObservationID = "observation-1"
		w.Revision = 1
		w.Source = "vendor"
		w.EvidenceID = "evidence-" + w.Window
		w.ObservedAt = in.CapturedAt.Add(-time.Minute)
		w.FreshUntil = in.CapturedAt.Add(time.Hour)
		w.ResetAt = in.CapturedAt.Add(time.Hour)
		w.Cycle = "cycle-1"
		w.Capacity = &provider.QuotaCapacity{Semantics: "included", Unit: "requests", Remaining: 5, Limit: 10}
	}
	if !available {
		q.Snapshot.State = provider.QuotaStateUnknown
		for i := range q.Snapshot.Windows {
			q.Snapshot.Windows[i].State = provider.QuotaStateUnknown
			q.Snapshot.Windows[i].FreshUntil = in.CapturedAt
		}
	}
	return q
}
func TestBillingQuotaCoverageAndNotFreeProof(t *testing.T) {
	for _, available := range []bool{false, true} {
		in, c := billingFixture()
		in.Quota = billingQuotaFixture(in, available)
		if !available && (in.Quota.Snapshot.Windows[0].Kind == "" || in.Quota.Snapshot.Windows[0].FreshUntil.After(in.CapturedAt)) {
			t.Fatal("unknown fixture must retain expired materialized evidence")
		}
		in.Decision = provider.BillingDecision{Policy: provider.BillingPolicy{Mode: provider.BillingQuota, Exhausted: provider.QuotaStop}, Free: true, Source: provider.BillingSourceQuota}
		c.Disposition.Kind = "unknown"
		ctx, err := CaptureBilling(in)
		if err != nil {
			t.Fatal(err)
		}
		e, err := ctx.Seal(c)
		if err != nil {
			t.Fatal(err)
		}
		billingAssertValue(t, e.Amounts(), "charge", 0, false)
		want, _ := EncodeBilling(e)
		in.Quota.Coverage[0].Scopes[0] = "other"
		in.Quota.Snapshot.Windows[0].Window = "other"
		if available {
			in.Quota.Snapshot.Windows[0].Capacity.Remaining = 0
		}
		returned := ctx.Input()
		returned.Quota.Coverage[0].Scopes[0] = "changed"
		returned.Quota.Snapshot.Windows[0].Window = "changed"
		again, _ := ctx.Seal(c)
		got, _ := EncodeBilling(again)
		if !bytes.Equal(want, got) {
			t.Fatal("quota snapshot aliases caller or getter")
		}
		decoded, err := DecodeBilling(want)
		if err != nil {
			t.Fatal(err)
		}
		if len(decoded.Capture().Quota.Snapshot.Windows) != 2 {
			t.Fatal("coverage lost")
		}
	}
	for _, change := range []func(*BillingCapture){func(in *BillingCapture) { in.Quota.Snapshot.Windows = in.Quota.Snapshot.Windows[:1] }, func(in *BillingCapture) { in.Quota.Snapshot.Subject.Generation = "new" }, func(in *BillingCapture) { in.Quota.Snapshot.Version.Revision = 0 }, func(in *BillingCapture) { in.Quota.Snapshot.Scope = "other" }, func(in *BillingCapture) { in.Quota.Snapshot.Windows[0].ObservedAt = in.CapturedAt.Add(time.Second) }} {
		in, _ := billingFixture()
		in.Quota = billingQuotaFixture(in, true)
		change(&in)
		if _, err := CaptureBilling(in); !errors.Is(err, ErrBillingInvalid) {
			t.Fatal("invalid/partial quota accepted")
		}
	}
}

func TestBillingImmutableConcurrentReads(t *testing.T) {
	in, c := billingFixture()
	ctx, err := CaptureBilling(in)
	if err != nil {
		t.Fatal(err)
	}
	e := billingMustSeal(t, in, c)
	want, _ := EncodeBilling(e)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 20 {
				got, err := ctx.Seal(c)
				if err != nil {
					t.Error(err)
					return
				}
				b, err := EncodeBilling(got)
				if err != nil || !bytes.Equal(b, want) {
					t.Error("concurrent result changed")
					return
				}
				_ = e.Amounts()
				_ = e.Capture()
				_ = e.Completion()
			}
		})
	}
	wg.Wait()
}

func TestBillingLegacyAndExactTotals(t *testing.T) {
	in, c := billingFixture()
	e := billingMustSeal(t, in, c)
	legacy := BillingLegacyInput{Binding: in.Binding, Scope: in.Scope, At: in.AttemptAt, CurrentEstimate: new(8.0), EstimateSource: "current_price"}
	old, err := ProjectLegacyBilling(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacy.CurrentEstimate = new(16.0)
	now, err := ProjectLegacyBilling(legacy)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []BillingAmounts{old, now} {
		billingAssertValue(t, a, "reference", 0, false)
		billingAssertValue(t, a, "difference", 0, false)
	}
	var first, second BillingTotals
	for _, a := range []BillingAmounts{e.Amounts(), old} {
		if err := first.Add(a); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []BillingAmounts{e.Amounts(), now} {
		if err := second.Add(a); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := first.Project()
	b, _ := second.Project()
	for _, v := range a.Values {
		if v.Origin == "frozen" && !slices.Contains(b.Values, v) {
			t.Fatal("legacy edit repriced frozen value")
		}
	}
	legacy.Reported = billingReport(in, 0)
	legacy.Reported.Kind = "external_client"
	reported, err := ProjectLegacyBilling(legacy)
	if err != nil {
		t.Fatal(err)
	}
	billingAssertValue(t, reported, "charge", 0, true)
	billingAssertValue(t, reported, "difference", 0, false)
	values := []float64{1e16, 1, -1e16, math.SmallestNonzeroFloat64}
	fold := func(order []float64) BillingAmounts {
		var totals BillingTotals
		for _, amount := range order {
			if err := totals.Add(BillingAmounts{Values: []BillingValue{billingKnown("difference", "frozen", "paired", amount)}}); err != nil {
				t.Fatal(err)
			}
		}
		a, err := totals.Project()
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	want := fold(values)
	for _, order := range [][]float64{{1, 1e16, -1e16, math.SmallestNonzeroFloat64}, {-1e16, math.SmallestNonzeroFloat64, 1e16, 1}} {
		if !reflect.DeepEqual(want, fold(order)) {
			t.Fatal("permutation changed exact totals")
		}
	}
	var left, right BillingTotals
	for i, v := range values {
		dst := &left
		if i > 1 {
			dst = &right
		}
		if err := dst.Add(BillingAmounts{Values: []BillingValue{billingKnown("difference", "frozen", "paired", v)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := left.Merge(&right); err != nil {
		t.Fatal(err)
	}
	merged, _ := left.Project()
	if !reflect.DeepEqual(merged, want) {
		t.Fatal("chunk merge rounded intermediates")
	}
	var overflow BillingTotals
	for range 2 {
		if err := overflow.Add(BillingAmounts{Values: []BillingValue{billingKnown("difference", "frozen", "paired", math.MaxFloat64)}}); err != nil {
			t.Fatal("intermediate overflow rejected")
		}
	}
	if _, err := overflow.Project(); !errors.Is(err, ErrBillingInvalid) {
		t.Fatal("final overflow accepted")
	}
	if err := overflow.Add(BillingAmounts{Values: []BillingValue{billingKnown("difference", "frozen", "paired", -math.MaxFloat64)}}); err != nil {
		t.Fatal(err)
	}
	cancelled, err := overflow.Project()
	if err != nil || cancelled.Values[0].Amount != math.MaxFloat64 {
		t.Fatal("projection mutated exact sum")
	}
}

func TestBillingUnpairedPopulationAndAtomicTotals(t *testing.T) {
	in, c := billingFixture()
	unknownRef := in
	unknownRef.Reference = nil
	unknownRef.ReferenceMissing = "unavailable"
	a := billingMustSeal(t, unknownRef, c).Amounts()
	in.Spend = nil
	in.Decision.Source = provider.BillingSourceUnknown
	c.Disposition.Kind = "unknown"
	b := billingMustSeal(t, in, c).Amounts()
	var totals BillingTotals
	if err := totals.Add(a); err != nil {
		t.Fatal(err)
	}
	if err := totals.Add(b); err != nil {
		t.Fatal(err)
	}
	out, err := totals.Project()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range out.Values {
		if v.Dimension == "difference" && (v.Amount != 0 || v.Known != 0) {
			t.Fatal("unpaired totals fabricated savings")
		}
	}
	before := out
	bad := BillingAmounts{Values: []BillingValue{billingKnown("estimate", "frozen", "list", 10), billingKnown("estimate", "frozen", "list", -1)}}
	if err := totals.Add(bad); !errors.Is(err, ErrBillingInvalid) {
		t.Fatal("invalid total input accepted")
	}
	after, _ := totals.Project()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("invalid add partially mutated totals")
	}
	var counters BillingTotals
	v := billingKnown("estimate", "frozen", "list", 0)
	v.Known = ^uint64(0)
	if err := counters.Add(BillingAmounts{Values: []BillingValue{v}}); err != nil {
		t.Fatal(err)
	}
	v.Known = 1
	if err := counters.Add(BillingAmounts{Values: []BillingValue{v}}); !errors.Is(err, ErrBillingInvalid) {
		t.Fatal("counter overflow accepted")
	}
	still, _ := counters.Project()
	if still.Values[0].Known != ^uint64(0) {
		t.Fatal("overflow mutated count")
	}
}

func TestBillingQuotaExhaustionAndChargeEvidence(t *testing.T) {
	in, c := billingFixture()
	in.Quota = billingQuotaFixture(in, true)
	in.Decision = provider.BillingDecision{Policy: provider.BillingPolicy{Mode: provider.BillingQuota, Exhausted: provider.QuotaStop}, Source: provider.BillingSourceQuota, Free: true}
	in.Quota.Snapshot.State = provider.QuotaStateExhausted
	in.Quota.Snapshot.RecheckDue = true
	for i := range in.Quota.Snapshot.Windows {
		w := &in.Quota.Snapshot.Windows[i]
		w.State = provider.QuotaStateExhausted
		w.Kind = provider.QuotaConfirmedExhaustion
		w.Capacity = nil
		w.FreshUntil = time.Time{}
		w.ResetAt = in.CapturedAt.Add(-30 * time.Second)
		w.RecheckDue = true
		w.Blocker = &provider.QuotaBlocker{ID: w.ObservationID, Revision: w.Revision, Source: w.Source, EvidenceID: w.EvidenceID, Cycle: w.Cycle, ObservedAt: w.ObservedAt}
	}
	ctx, err := CaptureBilling(in)
	if err != nil {
		t.Fatal(err)
	}
	c.Disposition = BillingDisposition{Kind: "unknown"}
	e, err := ctx.Seal(c)
	if err != nil {
		t.Fatal(err)
	}
	billingAssertValue(t, e.Amounts(), "charge", 0, false)
	q := e.Capture().Quota.Snapshot
	if q.State != provider.QuotaStateExhausted || !q.RecheckDue || q.Windows[0].Blocker == nil {
		t.Fatal("elapsed reset recovered allowance")
	}
	encoded, err := EncodeBilling(e)
	if err != nil {
		t.Fatal(err)
	}
	round, err := DecodeBilling(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if round.Capture().Quota.Snapshot.Windows[0].Blocker.EvidenceID != q.Windows[0].Blocker.EvidenceID {
		t.Fatal("refusal provenance lost")
	}
	for _, kind := range []string{"included", "tariff"} {
		c.Disposition = BillingDisposition{Kind: kind, Evidence: billingEvidence(in)}
		c.Reported = billingReport(in, 2)
		if kind == "tariff" {
			c.Reported.Amount = 0
		}
		e, err := ctx.Seal(c)
		if err != nil {
			t.Fatal(err)
		}
		billingAssertValue(t, e.Amounts(), "charge", 0, false)
		billingAssertValue(t, e.Amounts(), "difference", 0, false)
		if !slices.Contains(e.Amounts().Issues, "charge_conflict") {
			t.Fatal("contradictory included/paid report was erased")
		}
		if e.Capture().Quota.Snapshot.State != provider.QuotaStateExhausted {
			t.Fatal("accounting changed quota")
		}
	}
	c.Reported = nil
	c.Disposition.Evidence.Binding.AttemptID = "other"
	if _, err := ctx.Seal(c); !errors.Is(err, ErrBillingInvalid) {
		t.Fatal("misbound charge evidence accepted")
	}
}

func TestBillingReferenceRateProvenance(t *testing.T) {
	in, c := billingFixture()
	in.Reference.Rate = 3
	in.Reference.RateRef = "published-rate-3"
	in.Spend.Rate = .5
	in.Spend.RateSource = "user"
	in.Spend.RateRef = "user-discount-1"
	e := billingMustSeal(t, in, c)
	billingAssertValue(t, e.Amounts(), "reference", .001065, true)
	billingAssertValue(t, e.Amounts(), "estimate", .00008875, true)
	b, err := EncodeBilling(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBilling(b)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Capture().Reference.RateRef != "published-rate-3" || decoded.Capture().Spend.RateSource != "user" {
		t.Fatal("rate provenance lost")
	}
	var totals BillingTotals
	v := billingKnown("difference", "legacy", "paired", 1)
	if err := totals.Add(BillingAmounts{Values: []BillingValue{v}}); !errors.Is(err, ErrBillingInvalid) {
		t.Fatal("legacy savings accepted")
	}
}

func TestBillingPublishedZeroVersusMissingReference(t *testing.T) {
	in, c := billingFixture()
	in.Reference.Price = catalog.Price{}
	e := billingMustSeal(t, in, c)
	billingAssertValue(t, e.Amounts(), "reference", 0, true)
	billingAssertValue(t, e.Amounts(), "difference", -.0001775, true)
	if billingFind(t, e.Amounts(), "reference").Source != "provider_public" {
		t.Fatal("published zero provenance lost")
	}
	for _, reason := range []string{"not_provided", "unavailable"} {
		in.Reference = nil
		in.ReferenceMissing = reason
		e = billingMustSeal(t, in, c)
		billingAssertValue(t, e.Amounts(), "reference", 0, false)
		billingAssertValue(t, e.Amounts(), "difference", 0, false)
		if billingFind(t, e.Amounts(), "reference").Reason != reason {
			t.Fatal("missing reference category lost")
		}
	}
	legacy := BillingLegacyInput{Binding: in.Binding, Scope: in.Scope, At: in.AttemptAt, CurrentEstimate: new(0.0), EstimateSource: "current_price"}
	amount, err := ProjectLegacyBilling(legacy)
	if err != nil {
		t.Fatal(err)
	}
	billingAssertValue(t, amount, "estimate", 0, true)
	legacy.CurrentEstimate = nil
	legacy.EstimateSource = ""
	legacy.EstimateMissing = "unavailable"
	amount, err = ProjectLegacyBilling(legacy)
	if err != nil {
		t.Fatal(err)
	}
	billingAssertValue(t, amount, "estimate", 0, false)
}

func TestBillingUTCRepresentability(t *testing.T) {
	low := time.Date(0, 1, 1, 0, 0, 0, 0, time.FixedZone("positive", 3600))
	high := time.Date(9999, 12, 31, 23, 59, 0, 0, time.FixedZone("negative", -3600))
	for _, boundary := range []time.Time{low, high} {
		if _, err := boundary.MarshalJSON(); err != nil {
			t.Fatal("boundary must be locally representable", err)
		}
		if _, err := boundary.UTC().MarshalJSON(); err == nil {
			t.Fatal("boundary must cross the UTC wire year range")
		}
		in, _ := billingFixture()
		in.CapturedAt, in.AttemptAt = boundary, boundary
		in.Spend.AcquiredAt, in.Reference.AcquiredAt = boundary, boundary
		if got, err := CaptureBilling(in); !errors.Is(err, ErrBillingInvalid) || got.data != nil {
			t.Fatal("capture accepted timestamp outside UTC wire range", err)
		}
	}
	for _, field := range []string{"spend", "reference", "observed", "reset", "fresh", "blocker"} {
		t.Run(field, func(t *testing.T) {
			in, _ := billingFixture()
			in.Quota = billingQuotaFixture(in, true)
			switch field {
			case "spend":
				in.Spend.AcquiredAt = low
			case "reference":
				in.Reference.AcquiredAt = low
			case "observed":
				for i := range in.Quota.Snapshot.Windows {
					in.Quota.Snapshot.Windows[i].ObservedAt = low
				}
			case "reset":
				in.Quota.Snapshot.Windows[0].ResetAt = high
			case "fresh":
				in.Quota.Snapshot.Windows[0].FreshUntil = high
			case "blocker":
				w := &in.Quota.Snapshot.Windows[0]
				in.Quota.Snapshot.Version.Revision = 2
				w.Revision = 2
				w.ObservationID = "later"
				w.Blocker = &provider.QuotaBlocker{ID: "early", Revision: 1, Source: "earlier-vendor", EvidenceID: "early-evidence", ObservedAt: low}
				w.State = provider.QuotaStateExhausted
				in.Quota.Snapshot.State = provider.QuotaStateExhausted
				in.Quota.Snapshot.Windows[1].Revision = 2
				in.Quota.Snapshot.Windows[1].ObservationID = "later"
			}
			if _, _, err := provider.CloneQuotaSnapshot(*in.Quota.Snapshot, in.Quota.Coverage, in.CapturedAt); err != nil {
				t.Fatal("provider rules must still accept original offset view", err)
			}
			if got, err := CaptureBilling(in); !errors.Is(err, ErrBillingInvalid) || got.data != nil {
				t.Fatal("capture accepted unencodable normalized source", err)
			}
		})
	}
	for _, field := range []string{"finish", "report", "credit", "disposition", "legacy"} {
		t.Run(field, func(t *testing.T) {
			in, c := billingFixture()
			if field == "disposition" {
				in.Decision = provider.BillingDecision{Policy: provider.BillingPolicy{Mode: provider.BillingQuota, Exhausted: provider.QuotaStop}, Source: provider.BillingSourceQuota, Free: true}
				c.Disposition = BillingDisposition{Kind: "included", Evidence: billingEvidence(in)}
				c.Disposition.Evidence.AcquiredAt = high
			}
			ctx, err := CaptureBilling(in)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "finish":
				c.FinishedAt = high
			case "report":
				c.Reported = billingReport(in, 1)
				c.Reported.Evidence.AcquiredAt = high
			case "credit":
				c.Credits = []BillingCredit{{Evidence: *billingEvidence(in), Kind: "vendor_response", Authority: "vendor", Unit: "points", Amount: 1}}
				c.Credits[0].Evidence.AcquiredAt = high
			case "legacy":
				if _, err := ProjectLegacyBilling(BillingLegacyInput{Binding: in.Binding, Scope: in.Scope, At: high, EstimateMissing: "unavailable"}); !errors.Is(err, ErrBillingInvalid) {
					t.Fatal("legacy accepted timestamp outside UTC wire range", err)
				}
				return
			}
			if got, err := ctx.Seal(c); !errors.Is(err, ErrBillingInvalid) || got.data != nil {
				t.Fatal("completion accepted timestamp outside UTC wire range", err)
			}
		})
	}
}

func TestBillingUTCEquivalentInstantsAndQuotaZeroes(t *testing.T) {
	in, c := billingFixture()
	in.Quota = billingQuotaFixture(in, true)
	for i := range in.Quota.Snapshot.Windows {
		in.Quota.Snapshot.Windows[i].ResetAt = time.Time{}
	}
	want, err := EncodeBilling(billingMustSeal(t, in, c))
	if err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("offset", 9*3600)
	in.CapturedAt, in.AttemptAt = in.CapturedAt.In(zone), in.AttemptAt.In(zone)
	in.Spend.AcquiredAt, in.Reference.AcquiredAt = in.Spend.AcquiredAt.In(zone), in.Reference.AcquiredAt.In(zone)
	for i := range in.Quota.Snapshot.Windows {
		w := &in.Quota.Snapshot.Windows[i]
		w.ObservedAt, w.FreshUntil = w.ObservedAt.In(zone), w.FreshUntil.In(zone)
	}
	c.FinishedAt = c.FinishedAt.In(zone)
	got, err := EncodeBilling(billingMustSeal(t, in, c))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("equivalent timezone instants or optional zeroes changed wire", err)
	}
}
