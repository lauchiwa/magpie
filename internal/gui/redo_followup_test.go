package gui

import (
	"errors"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A change made with a step after it failed has that step, only that,
// done once more: through, no error; failed again, a committed follow-up
// still, for the page to say the change stands. Any other error is as it
// was.
func TestRedoFollowUp(t *testing.T) {
	redone := 0
	ok := provider.FollowUp(provider.StepSignIn, errors.New("injected"), func() error { redone++; return nil })
	if err := redoFollowUp(ok); err != nil || redone != 1 {
		t.Fatalf("redone %d: %v", redone, err)
	}
	still := errors.New("still")
	err := redoFollowUp(provider.FollowUp(provider.StepSignIn, errors.New("injected"), func() error { redone++; return still }))
	var fu *provider.FollowUpError
	if !errors.As(err, &fu) || !fu.Committed || fu.Step != provider.StepSignIn || !errors.Is(err, still) || redone != 2 {
		t.Fatalf("redone %d: %v", redone, err)
	}
	out := map[string]string{}
	lifecycleFail(out, err)
	if out["code"] != "follow_up" || out["committed"] != "1" || out["step"] != provider.StepSignIn {
		t.Fatalf("the answer: %v", out)
	}
	other := errors.New("refused")
	if err := redoFollowUp(other); err != other {
		t.Fatalf("a change that failed: %v", err)
	}
	if redoFollowUp(nil) != nil {
		t.Fatal("no error made one")
	}
}
