package agent

// A provider rename checked against the value it was read as (R1 G4a,
// provider.RenameChecked), with the agents moved after it as its own step.

import (
	"errors"
	"strings"

	"github.com/yetone/magpie/internal/provider"
)

// StepAgents is the agents' configs moved onto a renamed provider's new id:
// the step after a committed rename that RenameChecked names when it fails.
const StepAgents = "agents"

// applyMoves is applyRenameMoves; a var so a test can make it fail.
var applyMoves = applyRenameMoves

// RenameChecked is RenameProvider of p as it was read: refused
// (provider.ErrStale) when p's id holds another provider, or another rev of
// it, since. Once the rename is committed the agents are moved even when the
// settings step failed, as the rename stands either way; a step that fails
// comes back as one *provider.FollowUpError whose Redo does those steps
// again, never the rename. A rename left pending in its journal
// (provider.ErrPending) moves no agent: it is not made until the next write
// or start-up finishes it, and the old id still reaches the provider then.
// It answers the provider under its new id and the agents it moved.
func RenameChecked(p provider.Provider, to string) (provider.Provider, []string, error) {
	from := p.ID
	to = strings.ToLower(strings.TrimSpace(to))
	moves := renameMoves(from, to)
	q, err := provider.RenameChecked(p, to)
	var settings *provider.FollowUpError
	if err != nil && !(errors.As(err, &settings) && settings.Committed) {
		return provider.Provider{}, nil, err
	}
	moved, aerr := applyMoves(moves, from, to)
	if aerr == nil {
		return q, moved, err
	}
	agents := func() error { _, err := applyMoves(moves, from, to); return err }
	if settings == nil {
		return q, moved, provider.FollowUp(StepAgents, aerr, agents)
	}
	both := func() error { return errors.Join(settings.Redo(), agents()) }
	return q, moved, provider.FollowUp(settings.Step+", "+StepAgents, errors.Join(settings.Err, aerr), both)
}
