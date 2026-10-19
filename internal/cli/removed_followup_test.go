package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A delete made with its sign-in step failed is said as removed: the step
// alone is done once more, and failed again, the error says the removal
// stands. Any other error is as it was.
func TestRemovedFollowUp(t *testing.T) {
	redone := 0
	ok := provider.FollowUp(provider.StepSignIn, errors.New("injected"), func() error { redone++; return nil })
	if err := removedFollowUp("Relay", ok); err != nil || redone != 1 {
		t.Fatalf("redone %d: %v", redone, err)
	}
	again := provider.FollowUp(provider.StepSignIn, errors.New("injected"), func() error { redone++; return errors.New("still") })
	err := removedFollowUp("Relay", again)
	if err == nil || redone != 2 || !strings.Contains(err.Error(), "Relay is removed") || !strings.Contains(err.Error(), "still") {
		t.Fatalf("redone %d: %v", redone, err)
	}
	other := errors.New("refused")
	if err := removedFollowUp("Relay", other); err != other || redone != 2 {
		t.Fatalf("a delete that failed: %v", err)
	}
	if removedFollowUp("Relay", nil) != nil {
		t.Fatal("no error made one")
	}
}
