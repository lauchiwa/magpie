package usage

import (
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"math/big"
	"time"
)

// Dormant inputs are not dispatch permission or durable settlement. A future
// caller owns identity, evidence trust, persistence and occurrence uniqueness.
const BillingVersion = 1
const BillingAlgorithm = 1
const BillingMaxBytes = 1 << 20

type billingError string

func (e billingError) Error() string { return string(e) }

const (
	ErrBillingInvalid     billingError = "billing input invalid"
	ErrBillingCorrupt     billingError = "billing envelope corrupt"
	ErrBillingUnsupported billingError = "billing version unsupported"
	ErrBillingTooLarge    billingError = "billing envelope too large"
)

type BillingBinding struct {
	AttemptID  string                `json:"attempt_id"`
	Subject    provider.QuotaSubject `json:"subject"`
	ProviderID string                `json:"provider_id"`
	Model      string                `json:"model"`
}

type BillingTariff struct {
	Currency   string
	ProviderID string
	Model      string
	Source     string // provider_public, maker_public or model_override
	SourceRef  string
	MappingRef string // required only for maker_public
	AcquiredAt time.Time
	RateSource string // published or user; reference permits published only
	RateRef    string
	Rate       float64
	Price      catalog.Price
}

type BillingQuota struct {
	Status   string // materialized, not_applicable, not_observed or unavailable
	Reason   string // unavailable: missing, corrupt, unsupported, io or busy
	Snapshot *provider.QuotaSnapshot
	Coverage []provider.QuotaCoverage
}

type BillingCapture struct {
	Version          int
	Algorithm        int
	Binding          BillingBinding
	Scope            string
	CapturedAt       time.Time
	AttemptAt        time.Time
	PolicyRef        string
	Decision         provider.BillingDecision
	Quota            BillingQuota
	Spend            *BillingTariff
	Reference        *BillingTariff
	ReferenceMissing string // not_provided or unavailable, only without Reference
}

// Known=false requires zero counts; it never means observed zero-token usage.
type BillingUsage struct {
	Known        bool `json:"known"`
	Input        int  `json:"input"`
	Output       int  `json:"output"`
	CacheRead    int  `json:"cache_read"`
	CacheWrite   int  `json:"cache_write"`
	CacheWrite1h int  `json:"cache_write_1h"`
}

type BillingEvidence struct {
	Binding    BillingBinding `json:"binding"`
	Scope      string         `json:"scope"`
	Source     string         `json:"source"`
	SourceRef  string         `json:"source_ref"`
	AcquiredAt time.Time      `json:"acquired_at"`
}

type BillingDisposition struct {
	Kind     string           `json:"kind"`               // policy_zero, tariff, included or unknown
	Evidence *BillingEvidence `json:"evidence,omitempty"` // included or quota-paid
}

type BillingReport struct {
	Evidence BillingEvidence `json:"evidence"`
	Kind     string          `json:"kind"` // vendor_response or external_client (legacy only)
	Currency string          `json:"currency"`
	Amount   float64         `json:"amount"`
}

type BillingCredit struct {
	Evidence  BillingEvidence `json:"evidence"`
	Kind      string          `json:"kind"`
	Authority string          `json:"authority"`
	Unit      string          `json:"unit"`
	Amount    float64         `json:"amount"`
}

type BillingCompletion struct {
	Binding     BillingBinding     `json:"binding"`
	FinishedAt  time.Time          `json:"finished_at"`
	Usage       BillingUsage       `json:"usage"`
	Disposition BillingDisposition `json:"disposition"`
	Reported    *BillingReport     `json:"reported,omitempty"`
	Credits     []BillingCredit    `json:"credits"`
}

// Each value is a provenance/coverage bucket, never an inferred invoice. Only
// difference may be negative. Credits retain their own unit and authority.
type BillingValue struct {
	Dimension string  `json:"dimension"` // estimate, reported, reference, charge, difference, credits
	Origin    string  `json:"origin"`    // frozen or legacy
	Source    string  `json:"source"`
	Reason    string  `json:"reason"`
	Unit      string  `json:"unit"`
	Authority string  `json:"authority"`
	Amount    float64 `json:"amount"`
	Known     uint64  `json:"known"`
	Unknown   uint64  `json:"unknown"`
}

type BillingAmounts struct {
	Values []BillingValue `json:"values"`
	Issues []string       `json:"issues"` // charge_conflict or paid_without_permission
}

type BillingLegacyInput struct {
	Binding         BillingBinding
	Scope           string
	At              time.Time
	CurrentEstimate *float64
	EstimateSource  string
	EstimateMissing string
	Reported        *BillingReport
	Credits         []BillingCredit
}

// Inputs must not be concurrently mutated during construction. Afterward all
// retained data is private and detached, including values returned by getters.
type BillingContext struct{ data *billingHeld }
type BillingEnvelope struct{ data *billingSealed }
type billingHeld struct {
	Input     BillingCapture
	Spend     *billingTariffHeld
	Reference *billingTariffHeld
}
type billingTariffHeld struct {
	Original   BillingTariff
	PeakFactor float64
	Effective  catalog.Price
}
type billingCalculation struct {
	Tier         int     `json:"tier"` // -1 is base; otherwise a zero-based held tier index
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite   float64 `json:"cache_write"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	Amount       float64 `json:"amount"`
}
type billingSealed struct {
	Held       billingHeld
	Completion BillingCompletion
	Spend      *billingCalculation
	Reference  *billingCalculation
	Amounts    BillingAmounts
}
type billingTotal struct {
	Value BillingValue
	Exact big.Rat
}

// Do not copy a totals value after first use. Merge retains exact sums rather
// than a rounded projection; Project failure does not change the accumulator.
type BillingTotals struct {
	values map[billingValueKey]billingTotal
	issues map[string]bool
}
type billingValueKey struct{ Dimension, Origin, Source, Reason, Unit, Authority string }
