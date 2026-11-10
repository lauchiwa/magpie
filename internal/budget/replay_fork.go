package budget

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/usage"
)

var errPendingUsage = errors.New("a sent reservation has no attributable complete usage")

func uniquePrincipals(caller, issuer string) []string {
	out := []string{}
	for _, id := range []string{caller, issuer} {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// Called with state held, including the active -> pending transition.
func invalidatePrincipal(id string) { delete(state.sums, slot(id)) }

func scopeDefinition(models []string) string {
	ms := slices.Clone(models)
	slices.Sort(ms)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(slices.Compact(ms), "\x00"))))
}

func (s *sums) eligible(key string, r usage.Record) bool {
	if (r.CallerKeyID != key && r.CallerIssuerID != key) || !counts(r) || !s.counts(r) {
		return false
	}
	at, err := usage.BudgetTime(r)
	if err != nil {
		s.err = err
		return false
	}
	return !at.Before(s.start) && (!strings.HasPrefix(r.Hold, "s2:") || s.reset.IsZero() || at.Before(s.reset))
}

// Fingerprints contain accounting facts only, not mutable derived tariffs or
// diagnostics. Time is an instant so a disk block's equivalent zone matches.
func rowFingerprint(r usage.Record) string {
	p := struct {
		At                                                              int64
		Hold, Caller, Issuer, Provider, Model, Served, Source, Computer string
		Input, Output, Read, Write, Hour, Reasoning                     int
		Tier, Effort                                                    string
		Rejected, CacheUnknown                                          bool
		Cost                                                            *float64
	}{r.Time.UnixNano(), r.Hold, r.CallerKeyID, r.CallerIssuerID, r.Provider, r.Model, r.Served, r.Source, r.Computer,
		r.Input, r.Output, r.CacheRead, r.CacheWrite, r.CacheWrite1h, r.Reasoning, r.TierServed, r.Effort, r.Rejected, r.CacheUnknown, r.OwnCost}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func openSet(l holdLine) string {
	l.Set, l.Outcome, l.Fingerprint, l.Rec = "", "", "", nil
	b, err := json.Marshal(l)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func validOpen(l holdLine) bool {
	if l.Version != 2 || l.Op != "open" || l.Caller == "" || l.Provider == "" || l.Model == "" || len(l.Holds) == 0 || len(l.Aliases) == 0 || l.Set != openSet(l) {
		return false
	}
	at, err := usage.BudgetTime(usage.Record{Hold: l.ID})
	if err != nil || !strings.HasPrefix(l.ID, "s2:") || !at.Equal(l.At) || !slices.Contains(l.Aliases, l.Provider+"/"+l.Model) {
		return false
	}
	seen := map[string]bool{}
	for _, h := range l.Holds {
		k := fmt.Sprintf("%s/%d/%d/%s", h.Key, h.Start, h.Reset, h.Scope)
		if !slices.Contains(uniquePrincipals(l.Caller, l.Issuer), h.Key) || h.Start > at.Unix() || h.Reset <= at.Unix() || h.Start >= h.Reset || h.Tokens < 0 || h.Cost < 0 || math.IsNaN(h.Cost) || math.IsInf(h.Cost, 0) || h.Definition == "" || seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}

func rowOfOpen(l holdLine, r usage.Record) bool {
	at, err := usage.BudgetTime(r)
	return err == nil && at.Equal(l.At) && r.Hold == l.ID && r.CallerKeyID == l.Caller && r.CallerIssuerID == l.Issuer &&
		counts(r) && r.Provider == l.Provider && slices.Contains(l.Aliases, r.Provider+"/"+r.Model)
}

func legacyConflicts(lines []holdLine) bool {
	seen := map[string]holdLine{}
	for _, l := range lines {
		if l.Version != 0 || l.Op != "open" {
			continue
		}
		key := l.ID + "\x00" + l.Key
		if old, ok := seen[key]; ok && !reflect.DeepEqual(old, l) {
			return true
		}
		seen[key] = l
	}
	return false
}

// addHeld deduplicates proven journal mirrors, never primary occurrences.
// A v2 terminal is useful only beside its exact opens AND a matching row.
func addHeld(s *sums, key string, lines []holdLine, live map[string]bool, primary map[string][]usage.Record, cost func(usage.Record) (float64, bool)) {
	groups := map[string][]holdLine{}
	for _, l := range lines {
		if l.invalid || l.Version != 0 && l.Version != 2 || l.Op != "open" && l.Op != "rec" && l.Op != "done" {
			s.err = errPendingUsage
		}
		id := l.ID
		if l.Op == "rec" && l.Rec != nil && id == "" {
			id = l.Rec.Hold
		}
		groups[id] = append(groups[id], l)
	}
	for id := range primary {
		if strings.HasPrefix(id, "s2:") {
			if _, ok := groups[id]; !ok {
				groups[id] = nil
			}
		}
	}
	for id, group := range groups {
		v2 := strings.HasPrefix(id, "s2:")
		for _, l := range group {
			v2 = v2 || l.Version == 2
		}
		if v2 {
			replayAttempt(s, key, group, live[id], primary[id], cost)
			continue
		}
		addLegacy(s, key, group, live[id], primary[id], cost)
	}
}

func addLegacy(s *sums, key string, lines []holdLine, live bool, primary []usage.Record, cost func(usage.Record) (float64, bool)) {
	matched := map[string]int{}
	recorded := false
	for _, r := range primary {
		matched[rowFingerprint(r)]++
		recorded = recorded || s.eligible(key, r)
	}
	done, conflict := false, legacyConflicts(lines)
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Op == "done" && l.ID != "" {
			done = true
		}
		if l.Op != "rec" || l.Rec == nil {
			continue
		}
		r := *l.Rec
		fp := rowFingerprint(r)
		if fp == "" {
			s.err = errPendingUsage
			continue
		}
		if matched[fp] > 0 {
			matched[fp]--
			continue
		}
		if s.eligible(key, r) {
			c, ok := cost(r)
			s.add(r, c, ok)
			recorded = true
		}
	}
	for _, l := range lines {
		if l.Op != "open" || l.Key != key {
			continue
		}
		if conflict {
			s.err = errPendingUsage
		}
		if !conflict && (done || recorded || live) {
			continue
		}
		for _, h := range l.Holds {
			if s.scope != "" && (h.Start == s.start.Unix() || l.At.IsZero() || !l.At.Before(s.start)) {
				s.err = errPendingUsage
				continue
			}
			if h.Start != s.start.Unix() {
				continue
			}
			if s.scope != "" {
				s.err = errPendingUsage
				continue
			}
			identity := fmt.Sprintf("%s/%s/%d", l.ID, key, h.Start)
			if seen[identity] {
				continue
			}
			seen[identity] = true
			s.pending++
			s.pendTokens += h.Tokens
			s.cost += h.Cost
		}
	}
}

// Keep the known part even when the envelope cannot prove completion. Only
// a valid occurrence tied to its exact open can identify a journal mirror.
func addAttemptRecords(s *sums, key string, lines []holdLine, primary []usage.Record, open *holdLine, cost func(usage.Record) (float64, bool)) {
	seen := map[string]bool{}
	for _, r := range primary {
		seen[rowFingerprint(r)] = true
	}
	for _, l := range lines {
		if l.Rec == nil {
			continue
		}
		r := *l.Rec
		fp := rowFingerprint(r)
		mirror := open != nil && l.Version == 2 && l.Op == "rec" && l.ID == open.ID && l.Set == open.Set &&
			(l.Outcome == "recorded" || l.Outcome == "unknown") && fp != "" && l.Fingerprint == fp && rowOfOpen(*open, r)
		if mirror && seen[fp] {
			continue
		}
		if s.eligible(key, r) {
			c, ok := cost(r)
			s.add(r, c, ok)
		}
		if mirror {
			seen[fp] = true
		}
	}
}

func replayAttempt(s *sums, key string, lines []holdLine, live bool, primary []usage.Record, cost func(usage.Record) (float64, bool)) {
	var open *holdLine
	conflict := false
	for i := range lines {
		l := &lines[i]
		if l.Op != "open" {
			continue
		}
		if !validOpen(*l) {
			conflict = true
		}
		if open != nil && !reflect.DeepEqual(*open, *l) {
			conflict = true
		}
		if open == nil {
			open = l
		}
	}
	proof := open
	if conflict {
		proof = nil
	}
	addAttemptRecords(s, key, lines, primary, proof, cost)
	if open == nil {
		// A receipt/rec without its open set cannot establish attribution.
		attributed := false
		for _, l := range lines {
			if l.Rec != nil {
				attributed = true
				if s.eligible(key, *l.Rec) {
					s.err = errPendingUsage
				}
			}
		}
		for _, r := range primary {
			attributed = true
			if s.eligible(key, r) {
				s.err = errPendingUsage
			}
		}
		if !attributed {
			s.err = errPendingUsage
		}
		return
	}
	principal := open.Caller == key || open.Issuer == key
	for _, l := range lines {
		if l.Key == key || l.Caller == key || l.Issuer == key {
			principal = true
		}
		for _, h := range l.Holds {
			if h.Key == key {
				principal = true
			}
		}
		if l.Rec != nil && s.eligible(key, *l.Rec) {
			principal = true
		}
	}
	for _, r := range primary {
		if s.eligible(key, r) {
			principal = true
		}
	}
	if !principal && open.Caller == "" && open.Issuer == "" {
		s.err = errPendingUsage
		return
	}
	if !principal {
		return
	}
	if conflict {
		// Contradictory identity/time/model may name any affected window.
		s.err = errPendingUsage
	}
	if len(primary) > 1 {
		conflict = true
	}
	rows := map[string]usage.Record{}
	for _, r := range primary {
		fp := rowFingerprint(r)
		if fp == "" || !rowOfOpen(*open, r) {
			conflict = true
		}
		rows[fp] = r
	}
	var terminal *holdLine
	partial := false
	for i := range lines {
		l := &lines[i]
		if l.Op == "open" {
			continue
		}
		if l.Version != 2 || l.ID != open.ID || l.Set != open.Set || l.Op != "rec" && l.Op != "done" || l.Op == "done" && l.Rec != nil || l.Outcome != "recorded" && l.Outcome != "never-sent" && !(l.Op == "rec" && l.Outcome == "unknown") {
			conflict = true
			continue
		}
		if l.Op == "rec" {
			if l.Rec == nil || l.Outcome == "never-sent" || l.Fingerprint == "" || l.Fingerprint != rowFingerprint(*l.Rec) || !rowOfOpen(*open, *l.Rec) {
				conflict = true
				continue
			}
			rows[l.Fingerprint] = *l.Rec
			if l.Outcome == "unknown" {
				partial = true
				continue
			}
		}
		if terminal != nil && (l.Outcome != terminal.Outcome || l.Fingerprint != terminal.Fingerprint) {
			conflict = true
		}
		terminal = l
	}
	if len(rows) > 1 || partial && terminal != nil {
		conflict = true
	}
	if !conflict && (open.At.Before(s.start) || !s.reset.IsZero() && !open.At.Before(s.reset) ||
		s.scope != "" && !slices.ContainsFunc(open.Aliases, func(id string) bool { return access.ModelAllowed(s.models, id) })) {
		return
	}
	complete := false
	if terminal != nil && !conflict {
		switch terminal.Outcome {
		case "never-sent":
			complete = len(rows) == 0 && terminal.Fingerprint == ""
		case "recorded":
			r, ok := rows[terminal.Fingerprint]
			complete = ok && rowOfOpen(*open, r) && s.eligible(key, r)
		}
	}
	if complete {
		return
	}
	if live && !conflict {
		return
	}
	s.err = errPendingUsage
	for _, h := range open.Holds {
		if h.Key == key && h.Start == s.start.Unix() && h.Reset >= s.reset.Unix() && h.Scope == s.scope && h.Definition == s.definition {
			s.pending++
			s.pendTokens += h.Tokens
			s.cost += h.Cost
			break
		}
	}
}
