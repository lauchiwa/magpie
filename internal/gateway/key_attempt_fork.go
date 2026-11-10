package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/gwbudget"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/usage"
)

type scopedBudgetKey struct{}
type scopedSendKey struct{}
type budgetSend struct {
	lease gwbudget.AttemptLease
	sent  bool
}
type scopedBudget struct{ limiter gwbudget.ScopedLimiter }

// Only implemented accounting adapters may enter scoped dispatch. This list
// is an integration boundary, not a downgrade to request-level reservations.
func scopedRouteReady(r *http.Request) bool {
	switch r.URL.Path {
	case "/v1/embeddings", "/embeddings", "/v1/rerank", "/rerank":
		return true
	case "/v1/audio/speech", "/audio/speech", "/v1/audio/transcriptions", "/audio/transcriptions", "/v1/audio/translations", "/audio/translations":
		return true
	}
	return false
}

func prepareScoped(r *http.Request, who access.Identity, now time.Time) (*http.Request, error) {
	l, err := gwbudget.Limiting(who)
	if err != nil {
		return r, err
	}
	sl, ok := l.(gwbudget.ScopedLimiter)
	if !ok {
		return r, gwbudget.ErrScopedUnsupported
	}
	states, err := sl.InspectScopes(who, now)
	if err != nil {
		return r, gwbudget.ErrUsageUnread
	}
	who.Spent = nil
	for _, st := range states {
		if st.Scope == "" {
			if strings.HasSuffix(r.URL.Path, "/count_tokens") {
				continue
			}
			if st.Err != nil {
				return r, st.Err
			}
			if st.Spent {
				if st.Refused == nil {
					return r, gwbudget.ErrUsageUnread
				}
				return r, st.Refused
			}
			continue
		}
		if st.Err != nil || st.Spent {
			why := gwbudget.ErrUsageUnread.Error()
			if st.Refused != nil {
				why = st.Refused.Error()
			}
			if st.Err != nil {
				why = gwbudget.ErrUsageUnread.Error()
			}
			who.Spent = append(who.Spent, access.SpentScope{Models: st.Models, Reset: st.Reset, Why: why, Unavailable: st.Err != nil})
		}
	}
	ctx := access.WithIdentity(r.Context(), who)
	return r.WithContext(context.WithValue(ctx, scopedBudgetKey{}, scopedBudget{sl})), nil
}

func beginBudgetSend(ctx context.Context, p provider.Provider, model string, bodyBytes int64) (context.Context, gwbudget.AttemptLease, error) {
	who := access.Caller(ctx)
	if !gwbudget.HasScopes(who) {
		return ctx, nil, nil
	}
	control, ok := ctx.Value(scopedBudgetKey{}).(scopedBudget)
	if !ok || p.Account != nil && !p.IsPlugin() {
		return ctx, nil, gwbudget.ErrScopedUnsupported
	}
	aliases := []string{p.ID + "/" + model, p.ID + "/" + p.OwnModelID(model), p.ID + "/" + p.ListedModelID(model)}
	l, err := control.limiter.ReserveAttempt(gwbudget.AttemptSpec{Who: who, Provider: p.ID, Model: model, Aliases: aliases, BodyBytes: bodyBytes, At: limitClock()})
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, scopedSendKey{}, &budgetSend{lease: l}), l, nil
}

func scopedSend(ctx context.Context) gwbudget.AttemptLease {
	s, _ := ctx.Value(scopedSendKey{}).(*budgetSend)
	if s == nil {
		return nil
	}
	return s.lease
}

func markBudgetSent(ctx context.Context) error {
	if !gwbudget.HasScopes(access.Caller(ctx)) {
		return nil
	}
	s, _ := ctx.Value(scopedSendKey{}).(*budgetSend)
	if s == nil {
		return gwbudget.ErrScopedUnsupported
	}
	if err := s.lease.MarkSent(); err != nil {
		return err
	}
	s.sent = true
	return nil
}

func abandonBudgetSend(ctx context.Context) {
	s, _ := ctx.Value(scopedSendKey{}).(*budgetSend)
	if s == nil {
		return
	}
	kind := gwbudget.NeverSent
	if s.sent {
		kind = gwbudget.Unknown
	}
	_ = s.lease.Finish(gwbudget.AttemptOutcome{Kind: kind})
}

// finishBudgetRecord is one final occurrence, complete only when the adapter
// observed authoritative usage. No token-count or HTTP-error heuristic is proof.
func finishBudgetRecord(r *http.Request, lease gwbudget.AttemptLease, rec usage.Record, complete bool) {
	if lease == nil {
		appendUsage(r, rec)
		return
	}
	if s, _ := r.Context().Value(scopedSendKey{}).(*budgetSend); s == nil || !s.sent {
		rec.Rejected = true
		appendUsage(r, rec)
		_ = lease.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.NeverSent})
		return
	}
	rec = attributedUsage(r, rec)
	rec.Hold = lease.Hold()
	if complete {
		if receipt, err := lease.Record(rec); err == nil {
			_ = lease.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Recorded, Receipt: receipt})
			return
		}
	} else {
		_ = lease.Diagnostic(rec)
	}
	_ = lease.Finish(gwbudget.AttemptOutcome{Kind: gwbudget.Unknown})
}

func budgetRefusal(w http.ResponseWriter, err error, now time.Time) (int, string) {
	status, kind := http.StatusServiceUnavailable, "gateway_key_limit_off"
	var refused gwbudget.Refusal
	switch {
	case errors.Is(err, gwbudget.ErrUsageUnread):
		kind = "gateway_key_usage_unread"
	case errors.Is(err, gwbudget.ErrUsageUnrecorded):
		kind = "gateway_key_usage_unrecorded"
	case errors.Is(err, gwbudget.ErrScopedUnsupported):
		kind = "gateway_key_scope_unsupported"
	case errors.As(err, &refused):
		status, kind = http.StatusTooManyRequests, "gateway_key_limit"
		w.Header().Set("Retry-After", strconv.Itoa(refused.RetryAfter(now)))
		if b, ok := refused.(*budget.Refusal); !ok || len(b.Models) == 0 {
			w.Header().Set(remoteKeyLimitHeader, refused.ResetAt().Format(time.RFC3339))
		}
	}
	w.Header().Set("X-Should-Retry", "false")
	return status, kind
}
