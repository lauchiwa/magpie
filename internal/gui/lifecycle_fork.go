package gui

// Provider lifecycle on the page's side (R1 G4b). providerJSON carries
// each provider's marker as rev (provider.Token); a save, a delete, an
// off/on and a binding send it back, and provider.FromToken puts it on
// the value the request names, so the write lands on the provider the page
// showed or is refused (code "stale"), never on another one that took its
// id meanwhile. A request with no rev is written by id, as before.
//
// An editor's Save is a chain: the provider itself, then its rename, its
// pasted keys, its model names, its reply limits. Each step after the
// first is checked against the provider the first one committed, so a
// delete and a new one under the same id between two steps leaves the new
// one alone; the steps already made stay, and the answer says which step
// was refused (code "follow_up", or "stale"/"pending" with its step) and
// that the change before it is saved. A rename's settings and agents step
// that fails is done again once here, as the CLI does; nothing else is
// retried, and the page keeps its draft rather than send it again with a
// newer rev by itself.

import (
	"errors"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/provider"
)

// Steps after a save, as the answer names them.
const (
	stepRename   = "rename"
	stepKeys     = "keys"
	stepPrefs    = "model-prefs"
	stepOutputs  = "outputs"
	stepCompacts = "compacts"
)

// stepFetch is the model list's fetch after a save. It is never in an
// answer: one refused is only not made.
const stepFetch = "fetch"

// betweenSteps, set by a test, runs before each step after the save: a
// change made there by another magpie meanwhile.
var betweenSteps func(step string)

// renameChecked is agent.RenameChecked; a test makes its follow-up fail.
var renameChecked = agent.RenameChecked

func between(step string) {
	if betweenSteps != nil {
		betweenSteps(step)
	}
}

// afterSave is err from step, after the save was committed: a refusal
// because the provider is not the one saved any more, or a write left to
// its journal, comes back as a *provider.FollowUpError naming step, the
// save standing. Any other error is the step's own, as before.
func afterSave(step string, err error) error {
	if err == nil || errors.As(err, new(*provider.FollowUpError)) {
		return err
	}
	if errors.Is(err, provider.ErrStale) || errors.Is(err, provider.ErrPending) {
		return provider.FollowUp(step, err, nil)
	}
	return err
}

// stepOn runs write, a step that writes by snap's id outside
// providers.json, only while snap is still the provider at that id.
func stepOn(snap provider.Provider, step string, write func() error) error {
	between(step)
	if err := provider.CheckCurrent(snap); err != nil {
		return afterSave(step, err)
	}
	return afterSave(step, write())
}

// addKeysTo is AddKeys checked against snap, in the same commit.
func addKeysTo(snap provider.Provider, keys []string, proto provider.Protocol) (added, had int, err error) {
	between(stepKeys)
	added, had, err = provider.AddKeys(snap.ID, keys, proto, provider.Expect(snap))
	return added, had, afterSave(stepKeys, err)
}

// savedFind is the provider at snap's id now, refused (stale) when it is
// not the one snap is.
func savedFind(snap provider.Provider) (*provider.Provider, error) {
	between(stepFetch)
	p, err := provider.Find(snap.ID)
	if err != nil {
		return nil, err
	}
	if !snap.SameInstance(*p) {
		return nil, &provider.StaleError{ID: snap.ID}
	}
	return p, nil
}

// renameSaved renames snap, the provider as its save committed it, to to:
// its settings and agents step, failed, is done once more; failed again,
// the answer says the rename is saved and that step is not.
func renameSaved(snap provider.Provider, to string) (provider.Provider, error) {
	between(stepRename)
	q, _, err := renameChecked(snap, to)
	var fu *provider.FollowUpError
	if errors.As(err, &fu) && fu.Committed {
		if rerr := fu.Redo(); rerr != nil {
			return q, provider.FollowUp(fu.Step, rerr, nil)
		}
		return q, nil
	}
	return q, afterSave(stepRename, err)
}

// redoFollowUp is err from a change committed with a step after it failed
// (a removed provider's StepFun sign-in): that step is done once more,
// never the change; failed again, it is a committed follow-up still. Any
// other error is as it was.
func redoFollowUp(err error) error {
	var fu *provider.FollowUpError
	if !errors.As(err, &fu) || !fu.Committed {
		return err
	}
	if rerr := fu.Redo(); rerr != nil {
		return provider.FollowUp(fu.Step, rerr, nil)
	}
	return nil
}

// lifecycleFail adds to a failed request's answer what the page tells
// apart: "pending" (finished by the next write or start, not to be made
// again), "stale" (changed since the page read it; read it again) and
// "follow_up" (saved, but a step after it failed), with that step.
func lifecycleFail(out map[string]string, err error) {
	var fu *provider.FollowUpError
	if errors.As(err, &fu) && fu.Committed {
		out["step"], out["committed"] = fu.Step, "1"
	}
	switch {
	case errors.Is(err, provider.ErrPending):
		out["code"] = "pending"
	case errors.Is(err, provider.ErrStale):
		out["code"] = "stale"
	case fu != nil:
		out["code"] = "follow_up"
	}
}
