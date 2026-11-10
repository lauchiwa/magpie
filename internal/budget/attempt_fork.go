package budget

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/gwbudget"
	"github.com/yetone/magpie/internal/usage"
)

// An attempt owns one possibly chargeable invocation, not a model request
// containing several sends. Its state is protected by state, then journal.
type attemptLease struct {
	open                           holdLine
	sent, closed, recording, wrote bool
	receipt                        *attemptReceipt
}

type attemptReceipt struct {
	lease       *attemptLease
	fingerprint string
}

// Shared with Append; tests can model a full primary write returning an error.
var appendUsageRecord = usage.AppendErr

func (r *attemptReceipt) Hold() string { return r.lease.Hold() }
func (a *attemptLease) Hold() string   { return a.open.ID }

func (Limiter) InspectScopes(who access.Identity, now time.Time) ([]gwbudget.ScopeState, error) {
	var out []gwbudget.ScopeState
	people := []access.Identity{who}
	if who.Issuer != nil && who.Issuer.KeyID != who.KeyID {
		people = append(people, *who.Issuer)
	}
	state.Lock()
	defer state.Unlock()
	for _, person := range people {
		ls, starts, resets, ss := windowsOf(person, now, func(*access.Limit) bool { return true })
		for i, l := range ls {
			s := ss[i]
			h := state.held[window{slot(person.KeyID), starts[i].Unix(), l.Scope()}]
			st := status(person.KeyID, l, s, h, resets[i])
			x := gwbudget.ScopeState{KeyID: person.KeyID, Scope: l.Scope(), Models: slices.Clone(s.models), Start: starts[i], Reset: resets[i]}
			if s.err != nil {
				x.Err = &Refusal{Status: st, Key: person.KeyName}
			}
			x.Spent = l.Tokens > 0 && st.Tokens+st.Reserved >= l.Tokens || l.Cost > 0 && st.Cost+st.ReservedCost >= l.Cost
			if x.Spent {
				x.Refused = &Refusal{Status: st, Key: person.KeyName}
			}
			out = append(out, x)
		}
	}
	return out, nil
}

func (Limiter) ReserveAttempt(spec gwbudget.AttemptSpec) (gwbudget.AttemptLease, error) {
	if spec.Who.KeyID == "" || spec.Provider == "" || spec.Model == "" || spec.At.IsZero() || spec.At.UnixNano() <= 0 {
		return nil, gwbudget.ErrScopedUnsupported
	}
	aliases := append(slices.Clone(spec.Aliases), spec.Provider+"/"+spec.Model)
	slices.Sort(aliases)
	aliases = slices.Compact(aliases)
	for _, id := range aliases {
		if !strings.HasPrefix(id, spec.Provider+"/") {
			return nil, gwbudget.ErrScopedUnsupported
		}
	}
	people := []access.Identity{spec.Who}
	if spec.Who.Issuer != nil && spec.Who.Issuer.KeyID != spec.Who.KeyID {
		people = append(people, *spec.Who.Issuer)
	}
	price, priced := priceOf(spec.Provider + "/" + spec.Model)
	state.Lock()
	defer state.Unlock()
	open := holdLine{Version: 2, Op: "open", At: spec.At.UTC(), Caller: spec.Who.KeyID, Issuer: spec.Who.IssuerID(), Provider: spec.Provider, Model: spec.Model, Aliases: aliases}
	var refused *Refusal
	for _, who := range people {
		ls, starts, resets, ss := windowsOf(who, spec.At, func(l *access.Limit) bool { return !l.Scoped() || scopeHas(l, aliases) })
		for i, l := range ls {
			s := ss[i]
			w := window{slot(who.KeyID), starts[i].Unix(), l.Scope()}
			h := state.held[w]
			st := status(who.KeyID, l, s, h, resets[i])
			if s.err != nil {
				return nil, &Refusal{Status: st, Key: who.KeyName}
			}
			if l.Tokens > 0 && st.Tokens+st.Reserved >= l.Tokens || l.Cost > 0 && st.Cost+st.ReservedCost >= l.Cost {
				if refused == nil || resets[i].After(refused.Reset) {
					refused = &Refusal{Status: st, Key: who.KeyName}
				}
			}
			est := max(spec.BodyBytes, 0) / 4
			if s.calls > 0 {
				est += s.output / int64(s.calls)
			}
			amount := float64(0)
			if l.Cost > 0 {
				if used := s.tokens(l); used > 0 && s.cost > 0 {
					amount = float64(est) * s.cost / float64(used)
				} else if priced {
					amount = float64(est) * price
				}
			}
			hw := holdWindow{Key: who.KeyID, Start: starts[i].Unix(), Reset: resets[i].Unix(), Scope: l.Scope(), Definition: s.definition, Tokens: est, Cost: amount}
			j := slices.IndexFunc(open.Holds, func(x holdWindow) bool { return x.Key == hw.Key && x.Start == hw.Start && x.Scope == hw.Scope })
			if j < 0 {
				open.Holds = append(open.Holds, hw)
			} else {
				open.Holds[j].Tokens = max(open.Holds[j].Tokens, hw.Tokens)
				open.Holds[j].Cost = max(open.Holds[j].Cost, hw.Cost)
				open.Holds[j].Reset = max(open.Holds[j].Reset, hw.Reset)
			}
		}
	}
	if refused != nil {
		return nil, refused
	}
	if len(open.Holds) == 0 {
		return &attemptLease{}, nil
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, gwbudget.ErrUsageUnrecorded
	}
	open.ID = fmt.Sprintf("s2:%s:%d", hex.EncodeToString(entropy[:]), open.At.UnixNano())
	open.Set = openSet(open)
	if !validOpen(open) {
		return nil, gwbudget.ErrUsageUnrecorded
	}
	journal.Lock()
	err := writeHoldLocked(open)
	if err == nil {
		journal.live[open.ID] = 1
	}
	journal.Unlock()
	// One atomic JSON line holds both principals: no partially opened set
	// is handed to the dispatcher. A torn line remains unknown on replay.
	if err != nil {
		for _, who := range people {
			invalidatePrincipal(who.KeyID)
		}
		return nil, gwbudget.ErrUsageUnrecorded
	}
	for _, hw := range open.Holds {
		w := window{slot(hw.Key), hw.Start, hw.Scope}
		h := state.held[w]
		if h == nil {
			h = &held{}
			state.held[w] = h
		}
		h.n++
		h.tokens += hw.Tokens
		h.cost += hw.Cost
	}
	return &attemptLease{open: open}, nil
}

func (a *attemptLease) MarkSent() error {
	state.Lock()
	defer state.Unlock()
	if a.closed || a.sent {
		return errors.New("reservation already sent or closed")
	}
	a.sent = true
	return nil
}

// Record is the once-only accounting sink. Observers and usage writers run
// outside budget locks; the durable open protects the concurrent read meanwhile.
func (a *attemptLease) Record(rec usage.Record) (gwbudget.AttemptReceipt, error) {
	return a.record(rec, true)
}

// Diagnostic preserves known partial usage, never a completion receipt.
func (a *attemptLease) Diagnostic(rec usage.Record) error {
	_, err := a.record(rec, false)
	return err
}

func (a *attemptLease) record(rec usage.Record, complete bool) (gwbudget.AttemptReceipt, error) {
	state.Lock()
	if !a.sent || a.closed || a.wrote || a.recording {
		state.Unlock()
		return nil, errors.New("reservation cannot record twice or before send")
	}
	if a.open.ID != "" && !rowOfOpen(a.open, rec) {
		state.Unlock()
		return nil, errors.New("usage does not belong to the reserved send")
	}
	fp := rowFingerprint(rec)
	if fp == "" {
		state.Unlock()
		return nil, errPendingUsage
	}
	a.recording = true
	for _, id := range uniquePrincipals(rec.CallerKeyID, rec.CallerIssuerID) {
		state.writing[slot(id)]++
	}
	state.Unlock()
	err := appendUsageRecord(rec)
	state.Lock()
	defer state.Unlock()
	a.recording, a.wrote = false, true
	for _, id := range uniquePrincipals(rec.CallerKeyID, rec.CallerIssuerID) {
		state.writing[slot(id)]--
		invalidatePrincipal(id)
	}
	if a.open.ID == "" {
		// No cap applies to this selected model; optional I/O cannot deny it.
		a.receipt = &attemptReceipt{a, fp}
		return a.receipt, nil
	}
	if err != nil {
		outcome := "unknown"
		if complete {
			outcome = "recorded"
		}
		journal.Lock()
		err = writeHoldLocked(holdLine{Version: 2, Op: "rec", ID: a.open.ID, At: time.Now().UTC(), Set: a.open.Set, Outcome: outcome, Fingerprint: fp, Rec: &rec})
		journal.Unlock()
	}
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, nil
	}
	a.receipt = &attemptReceipt{a, fp}
	return a.receipt, nil
}

func (a *attemptLease) Finish(out gwbudget.AttemptOutcome) error {
	state.Lock()
	defer state.Unlock()
	if a.closed {
		return nil
	}
	if a.recording {
		return errors.New("reservation accounting is in progress")
	}
	terminal := holdLine{Version: 2, Op: "done", ID: a.open.ID, At: time.Now().UTC(), Set: a.open.Set}
	var err error
	switch out.Kind {
	case gwbudget.NeverSent:
		if a.sent {
			return errors.New("a sent reservation cannot be canceled as never sent")
		}
		terminal.Outcome = "never-sent"
	case gwbudget.Recorded:
		r, ok := out.Receipt.(*attemptReceipt)
		if !a.sent || !ok || r != a.receipt || r.lease != a {
			return errors.New("unattributable accounting receipt")
		}
		terminal.Outcome, terminal.Fingerprint = "recorded", r.fingerprint
	case gwbudget.Unknown:
		if !a.sent {
			return errors.New("an unsent reservation must use never-sent cleanup")
		}
	case gwbudget.Nonbillable:
		// No generic HTTP/token-count/transport-error class is proof. An
		// explicit vendor proof needs its own reviewed protocol adapter.
		return gwbudget.ErrScopedUnsupported
	default:
		return errors.New("unknown reservation outcome")
	}
	if a.open.ID != "" {
		journal.Lock()
		if terminal.Outcome != "" {
			err = writeHoldLocked(terminal)
		}
		delete(journal.live, a.open.ID)
		journal.Unlock()
		for _, hw := range a.open.Holds {
			w := window{slot(hw.Key), hw.Start, hw.Scope}
			if h := state.held[w]; h != nil {
				h.n--
				h.tokens -= hw.Tokens
				h.cost -= hw.Cost
				if h.n == 0 {
					delete(state.held, w)
				}
			}
			invalidatePrincipal(hw.Key)
		}
	}
	a.closed = true
	return err
}
