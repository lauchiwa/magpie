package gwbudget

import (
	"errors"
	"time"

	"github.com/yetone/magpie/internal/access"
)

// fork: Billing D4, write failures.

// ErrUsageUnrecorded is what a Refusal unwraps to when a key's limits had
// room for its request but the reservation couldn't be written down, so
// a restart couldn't count it: the gateway answers it 503 and sends
// nothing, as it does ErrUsageUnread.
var ErrUsageUnrecorded = errors.New("the gateway key's reservation couldn't be recorded")

// HoldLimiter is a Limiter that keeps its reservation under the id it is
// given, so the gateway can name it on the request's usage records
// (usage.Record.Hold) and a restart counts the request once.
type HoldLimiter interface {
	Limiter
	ReserveHold(hold string, who access.Identity, body int64, model string, now time.Time) (release func(), refused Refusal)
}
