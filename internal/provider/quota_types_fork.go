package provider

import "time"

// QuotaSubject is supplied by a future identity owner, never derived from a
// display name, login ID or short key fingerprint. This component is inactive.
type QuotaSubject struct {
	Authority  string `json:"authority"`
	Provider   string `json:"provider"`
	Principal  string `json:"principal"`
	Generation string `json:"generation"`
}

// QuotaCoverage is immutable within a subject generation. Reads select a scope,
// not a subset of its applicable windows.
type QuotaCoverage struct {
	Window string   `json:"window"`
	Scopes []string `json:"scopes"`
}

type QuotaState string

const (
	QuotaStateUnknown   QuotaState = "unknown"
	QuotaStateAvailable QuotaState = "available"
	QuotaStateExhausted QuotaState = "exhausted"
)

type QuotaEvidence string

const (
	QuotaReportedFull        QuotaEvidence = "reported_full"
	QuotaReportedAvailable   QuotaEvidence = "reported_available"
	QuotaConfirmedExhaustion QuotaEvidence = "confirmed_exhaustion"
	QuotaConfirmedRecovery   QuotaEvidence = "confirmed_recovery"
	QuotaResetHint           QuotaEvidence = "reset_hint"
)

// QuotaCapacity always denotes included allowance, never a paid wallet. Explicit
// semantics and unit fields let validation reject missing/unsupported inputs.
type QuotaCapacity struct {
	Semantics string  `json:"semantics"`
	Unit      string  `json:"unit"`
	Remaining float64 `json:"remaining"`
	Limit     float64 `json:"limit"`
}

// QuotaRecovery must reference the held blocker. Basis is "cycle" (different
// nonempty cycles) or "superseding" (vendor-specific correction); neither is a
// certification of the caller's assertions. Provenance is an opaque reference.
type QuotaRecovery struct {
	BlockerID       string `json:"blocker_id"`
	BlockerRevision uint64 `json:"blocker_revision"`
	PriorCycle      string `json:"prior_cycle"`
	Basis           string `json:"basis"`
	Provenance      string `json:"provenance"`
}

type QuotaFact struct {
	Window     string         `json:"window"`
	Kind       QuotaEvidence  `json:"kind"`
	EvidenceID string         `json:"evidence_id"`
	FreshUntil time.Time      `json:"fresh_until"`
	ResetAt    time.Time      `json:"reset_at"`
	Cycle      string         `json:"cycle"`
	Capacity   *QuotaCapacity `json:"capacity,omitempty"`
	Recovery   *QuotaRecovery `json:"recovery,omitempty"`
}

// QuotaVersion is captured before acquiring evidence. A conflict requires a new
// observation, not relabeling old evidence with a freshly read revision.
type QuotaVersion struct {
	StoreEpoch string `json:"store_epoch"`
	Revision   uint64 `json:"revision"`
}

// QuotaObservation contains normalized facts, not raw vendor responses. Source
// authenticity, live incarnation binding and a trustworthy clock are deferred.
type QuotaObservation struct {
	Subject    QuotaSubject    `json:"subject"`
	ID         string          `json:"id"`
	Expected   QuotaVersion    `json:"expected"`
	Source     string          `json:"source"`
	ObservedAt time.Time       `json:"observed_at"`
	Coverage   []QuotaCoverage `json:"coverage"`
	Facts      []QuotaFact     `json:"facts"`
}

// QuotaBlocker retains the strongest unresolved refusal independently of later
// telemetry. Its ID is the accepted observation ID for this subject/window.
type QuotaBlocker struct {
	ID         string    `json:"id"`
	Revision   uint64    `json:"revision"`
	Source     string    `json:"source"`
	EvidenceID string    `json:"evidence_id"`
	Cycle      string    `json:"cycle"`
	ObservedAt time.Time `json:"observed_at"`
}

type quotaWindow struct {
	Window        string         `json:"window"`
	State         QuotaState     `json:"state"`
	Kind          QuotaEvidence  `json:"kind"`
	ObservationID string         `json:"observation_id"`
	Revision      uint64         `json:"revision"`
	Source        string         `json:"source"`
	EvidenceID    string         `json:"evidence_id"`
	ObservedAt    time.Time      `json:"observed_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	FreshUntil    time.Time      `json:"fresh_until"`
	ResetAt       time.Time      `json:"reset_at"`
	Cycle         string         `json:"cycle"`
	Capacity      *QuotaCapacity `json:"capacity,omitempty"`
	Recovery      *QuotaRecovery `json:"recovery,omitempty"`
	Blocker       *QuotaBlocker  `json:"blocker,omitempty"`
}

type quotaRecord struct {
	Subject  QuotaSubject    `json:"subject"`
	Revision uint64          `json:"revision"`
	Coverage []QuotaCoverage `json:"coverage"`
	Windows  []quotaWindow   `json:"windows"`
	LastID   string          `json:"last_id"`
	Digest   string          `json:"digest"`
}

// QuotaWindowSnapshot and QuotaSnapshot own all nested data. They are detached
// values for future call-time accounting, not authorization to send a request.
type QuotaWindowSnapshot struct {
	Window        string
	State         QuotaState
	Kind          QuotaEvidence
	ObservationID string
	Revision      uint64
	Source        string
	EvidenceID    string
	Cycle         string
	ObservedAt    time.Time
	FreshUntil    time.Time
	ResetAt       time.Time
	RecheckDue    bool
	Capacity      *QuotaCapacity
	Recovery      *QuotaRecovery
	Blocker       *QuotaBlocker
}

type QuotaSnapshot struct {
	Subject    QuotaSubject
	Scope      string
	Version    QuotaVersion
	State      QuotaState
	RecheckDue bool
	Windows    []QuotaWindowSnapshot
}

type QuotaTransition struct {
	Before []QuotaSnapshot
	After  []QuotaSnapshot
}

type QuotaCommit struct {
	Version    QuotaVersion
	Replayed   bool
	Transition *QuotaTransition
}

type quotaError string

func (e quotaError) Error() string { return string(e) }

const (
	ErrQuotaMissing     quotaError = "quota state missing"
	ErrQuotaCorrupt     quotaError = "quota state corrupt"
	ErrQuotaUnsupported quotaError = "quota state schema unsupported"
	ErrQuotaIO          quotaError = "quota state I/O failure"
	ErrQuotaBusy        quotaError = "quota state busy"
	ErrQuotaConflict    quotaError = "quota state conflict"
	ErrQuotaInvalid     quotaError = "quota observation invalid"
)
