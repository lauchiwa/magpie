package gwbudget

import (
	"errors"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/usage"
)

var ErrScopedUnsupported = errors.New("this scoped key's request has no durable per-send accounting adapter, so nothing was sent")

type ScopeState struct {
	KeyID, Scope string
	Models       []string
	Start, Reset time.Time
	Spent        bool
	Err          error
	Refused      Refusal
}

type ScopedInspector interface {
	InspectScopes(access.Identity, time.Time) ([]ScopeState, error)
}

type AttemptSpec struct {
	Who             access.Identity
	Provider, Model string
	Aliases         []string
	BodyBytes       int64
	At              time.Time
}

type ScopedLimiter interface {
	ScopedInspector
	ReserveAttempt(AttemptSpec) (AttemptLease, error)
}

type AttemptEnd uint8

const (
	NeverSent AttemptEnd = iota
	Recorded
	Nonbillable
	Unknown
)

// AttemptReceipt is issued by the lease's accounting sink, never by a
// gateway guessing that an Append succeeded. A lease validates its own type.
type AttemptReceipt interface{ Hold() string }

type AttemptOutcome struct {
	Kind    AttemptEnd
	Receipt AttemptReceipt
}

// Record writes one complete accounting occurrence and issues its receipt.
// Partial diagnostics may use the ordinary writer, but cannot close a lease.
// Finish(Unknown) is required for every sent attempt without a valid receipt.
type AttemptLease interface {
	Hold() string
	MarkSent() error
	Record(usage.Record) (AttemptReceipt, error)
	Diagnostic(usage.Record) error
	Finish(AttemptOutcome) error
}

func HasScopes(who access.Identity) bool {
	for _, l := range who.Limits() {
		if l.Scoped() {
			return true
		}
	}
	return who.Issuer != nil && HasScopes(*who.Issuer)
}
