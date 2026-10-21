package agent

import (
	"errors"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A change made with a step after it failed (a *provider.FollowUpError,
// committed: a removed provider's StepFun sign-in not forgotten) is made,
// so its agents are moved, and that error comes back with the moves for
// the caller to redo the step. A change that failed moves nothing.
func TestReseatAfterAFollowUp(t *testing.T) {
	reseatHome(t)
	c := mustFind(t, "claude")
	mustApply(t, c, "model", "cop/only-here")
	stepErr := errors.New("injected sign-in forget")
	moves, err := Reseat(func() error {
		if err := provider.Delete("cop"); err != nil {
			return err
		}
		return provider.FollowUp(provider.StepSignIn, stepErr, nil)
	})
	var fu *provider.FollowUpError
	if !errors.As(err, &fu) || !fu.Committed || !errors.Is(err, stepErr) {
		t.Fatalf("the follow-up isn't answered: %v", err)
	}
	if len(moves) != 1 {
		t.Fatalf("a removed provider's agent isn't moved: %+v", moves)
	}
	if got := c.Field("model").Get(); got != "" {
		t.Fatalf("claude: %q", got)
	}

	// failed, not made: nothing is moved
	mustApply(t, c, "model", "ds/gpt-5")
	refused := errors.New("refused")
	moves, err = Reseat(func() error { return refused })
	if !errors.Is(err, refused) || moves != nil {
		t.Fatalf("a failed change: %+v %v", moves, err)
	}
	if got := c.Field("model").Get(); got != "ds/gpt-5" {
		t.Fatalf("claude moved by a failed change: %q", got)
	}
}
