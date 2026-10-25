package cli

// Provider lifecycle at start-up (R1 G3b-2): run readies lifecycle.json
// once, after settings.Migrate has put the config folder in place and
// before the providers are held (provider.ReadyLifecycle). It never stops
// magpie from starting: a folder it can't write stays as before (by id),
// and what it can't settle is said on stderr, and refused where a request
// needs it.

import (
	"errors"
	"fmt"
	"os"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/provider"
)

// testSandbox is testenv.Marker: set in a test binary testenv isolated,
// and in the Agents page's dry run (agent.DryRun), under a home of its
// own.
const testSandbox = "MAGPIE_TEST_SANDBOX"

// readyInSandbox readies lifecycle.json in a test binary too, for the
// tests of that call itself. Every other test's home stays without one,
// as before lifecycle.json, so upstream's tests run as they were written.
var readyInSandbox = false

// readyLifecycle is provider.ReadyLifecycle; a var so a test can make it
// fail.
var readyLifecycle = provider.ReadyLifecycle

// startArgs are the arguments run was given, for readyAtStart in
// startUp, which takes none.
var startArgs []string

// readyAtStart readies lifecycle.json for run with args. `magpie identity
// reconcile` is left out: its preview writes nothing, and its --yes
// settles lifecycle.json itself, under the lock.
func readyAtStart(args []string) {
	if !readyInSandbox && os.Getenv(testSandbox) != "" {
		return
	}
	if len(args) > 1 && (args[0] == "identity" || args[0] == "identities") && args[1] == "reconcile" {
		return
	}
	if err := readyLifecycle(); err != nil {
		fmt.Fprintf(os.Stderr, "magpie: %v\n", err)
	}
}

// Provider writes from the command line (R1 G4a): each writes the provider
// as `provider.Find` read it in this command, never one looked up again by
// its id, so a provider renamed, deleted or made anew by another magpie in
// between is refused (provider.ErrStale) rather than changed. Nothing is
// retried: the command says the provider changed and to run it again.

// betweenReadAndWrite runs after a command read its provider and before it
// writes it; nil but in tests, which change the provider there as another
// magpie would.
var betweenReadAndWrite func(step string)

// beforeRedo runs before a step after a committed rename is done again; nil
// but in tests, which mend what made the step fail.
var beforeRedo func(step string)

func between(step string) {
	if betweenReadAndWrite != nil {
		betweenReadAndWrite(step)
	}
}

// saveFound saves p, read by Find in this command, and answers it as the
// write left it: its marker this commit's, for a rename after it.
func saveFound(p provider.Provider) (provider.Provider, error) {
	between("save")
	return provider.SaveChecked(p)
}

// deleteFound deletes p as Find read it in this command.
func deleteFound(p provider.Provider) error {
	between("delete")
	return provider.DeleteChecked(p)
}

// renameSaved renames saved, the value saveFound answered, to to, and moves
// the agents on its models. A step after the committed rename that failed is
// said and done once more, and only that step: the rename is never made
// again. A rename left pending in its journal is said as that.
func renameSaved(saved provider.Provider, to string) ([]string, error) {
	between("rename")
	_, moved, err := agent.RenameChecked(saved, to)
	var fu *provider.FollowUpError
	switch {
	case err == nil:
		return moved, nil
	case errors.Is(err, provider.ErrPending):
		return nil, fmt.Errorf("%s is not renamed to %s yet: %w; agents and model preferences still name %s, which reaches it", saved.ID, to, err, saved.ID)
	case !errors.As(err, &fu) || !fu.Committed:
		return nil, err
	}
	fmt.Println(amber.Render("!"), saved.ID, "is renamed to", to+", but its", fu.Step, "step failed:", fu.Err, muted.Render("· doing that step again"))
	if beforeRedo != nil {
		beforeRedo(fu.Step)
	}
	if rerr := fu.Redo(); rerr != nil {
		return moved, fmt.Errorf("%s is renamed to %s, but its %s step failed again: %w; %s still reaches it, so don't rename it again: once that is mended, pick its models as %s in the settings and agents still on %s", saved.ID, to, fu.Step, rerr, saved.ID, to, saved.ID)
	}
	return moved, nil
}

// removedFollowUp is err from deleteFound: a delete committed with the step
// after it failed (its StepFun sign-in not forgotten) is said, and that
// step, only that, is done once more. The delete is never made again.
func removedFollowUp(name string, err error) error {
	var fu *provider.FollowUpError
	if err == nil || !errors.As(err, &fu) || !fu.Committed {
		return err
	}
	fmt.Println(amber.Render("!"), name, "is removed, but its", fu.Step, "step failed:", fu.Err, muted.Render("· doing that step again"))
	if beforeRedo != nil {
		beforeRedo(fu.Step)
	}
	if rerr := fu.Redo(); rerr != nil {
		return fmt.Errorf("%s is removed, but its %s step failed again: %w; the removal stands, so don't remove it again", name, fu.Step, rerr)
	}
	return nil
}

// lifecycleSaid is err as the command line says it: a write refused because
// what it was made from changed since this command read it says to run the
// command again, and one left in the lifecycle journal says not to.
func lifecycleSaid(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, provider.ErrPending):
		return fmt.Errorf("%w — don't run it again: the next provider change or magpie start finishes it", err)
	case errors.Is(err, provider.ErrStale):
		return fmt.Errorf("%w — it changed since this command read it: run the command again, which reads it as it is now", err)
	}
	return err
}
