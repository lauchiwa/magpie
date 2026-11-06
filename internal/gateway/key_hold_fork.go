package gateway

import (
	"context"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/gwbudget"
)

// fork: Billing D4, write failures. A limited key's model request holds
// its reservation under an id its usage records name (usage.Record.Hold),
// so a restart that finds the reservation in usage-holds.jsonl counts the
// request once: by its records when they were written, else at what it
// was reserved at.

// holdKey carries a request's reservation id to appendUsage.
type holdKey struct{}

// reserveHeld is reserveFor under a reservation id the request's records
// name, "" when the limiter keeps none (a limiter from before it).
func reserveHeld(ctx context.Context, who access.Identity, size int64, model string, now time.Time) (context.Context, func(), error) {
	l, err := gwbudget.Limiting(who)
	if err != nil {
		return ctx, nil, err
	}
	if l == nil {
		return ctx, func() {}, nil
	}
	hl, ok := l.(gwbudget.HoldLimiter)
	if !ok {
		release, refused := l.Reserve(who, size, model, now)
		if refused != nil {
			return ctx, nil, refused
		}
		return ctx, release, nil
	}
	hold := budget.NewHold()
	release, refused := hl.ReserveHold(hold, who, size, model, now)
	if refused != nil {
		return ctx, nil, refused
	}
	return context.WithValue(ctx, holdKey{}, hold), release, nil
}

// holdOf is the reservation id ctx's request holds, "" for none.
func holdOf(ctx context.Context) string {
	h, _ := ctx.Value(holdKey{}).(string)
	return h
}
