package provider

import (
	"errors"
	"testing"
)

// failSignIn makes the sign-in step fail until mended: the delete's
// forget, the rename's move.
func failSignIn(t *testing.T) (mend func()) {
	t.Helper()
	broken := true
	moved, forgot := signInMoved, signInForgotten
	signInMoved = func(from, to string) error {
		if broken {
			return errors.New("injected sign-in move")
		}
		return moved(from, to)
	}
	signInForgotten = func(id string) error {
		if broken {
			return errors.New("injected sign-in forget")
		}
		return forgot(id)
	}
	t.Cleanup(func() { signInMoved, signInForgotten = moved, forgot })
	return func() { broken = false }
}

// A delete whose StepFun sign-in can't be forgotten is made: the provider
// is gone, and the answer is a committed FollowUpError for StepSignIn whose
// Redo forgets the sign-in alone, never the delete again.
func TestADeleteItsSignInFollowsUp(t *testing.T) {
	for _, checked := range []bool{false, true} {
		h := readyHome(t, nil, "relay", "other")
		mend := failSignIn(t)
		var err error
		if checked {
			err = DeleteChecked(h.find("relay"))
		} else {
			err = Delete("relay")
		}
		var fu *FollowUpError
		if !errors.As(err, &fu) || !fu.Committed || fu.Step != StepSignIn {
			t.Fatalf("checked %v: %v", checked, err)
		}
		if _, ferr := Find("relay"); ferr == nil {
			t.Fatalf("checked %v: relay is still there after its delete", checked)
		}
		committed := readCommitFiles(t)
		if rerr := fu.Redo(); rerr == nil {
			t.Fatal("a redo with the sign-in still failing went through")
		}
		mend()
		if rerr := fu.Redo(); rerr != nil {
			t.Fatalf("checked %v: the redo once mended: %v", checked, rerr)
		}
		if got := readCommitFiles(t); string(got.providers) != string(committed.providers) || string(got.meta) != string(committed.meta) {
			t.Fatal("a redo wrote providers.json or lifecycle.json")
		}
		if _, ferr := Find("other"); ferr != nil {
			t.Fatalf("checked %v: the other provider: %v", checked, ferr)
		}
	}
}

// A rename whose sign-in step fails is made, its settings step too: the
// FollowUpError names the sign-in alone and its Redo does only that.
func TestARenameItsSignInFollowsUp(t *testing.T) {
	h := readyHome(t, nil, "relay")
	mend := failSignIn(t)
	_, err := RenameChecked(h.find("relay"), "relay2")
	var fu *FollowUpError
	if !errors.As(err, &fu) || !fu.Committed || fu.Step != StepSignIn {
		t.Fatalf("%v", err)
	}
	if _, ferr := Find("relay2"); ferr != nil {
		t.Fatalf("the rename isn't made: %v", ferr)
	}
	committed := readCommitFiles(t)
	mend()
	if rerr := fu.Redo(); rerr != nil {
		t.Fatalf("the redo once mended: %v", rerr)
	}
	if got := readCommitFiles(t); string(got.providers) != string(committed.providers) || string(got.meta) != string(committed.meta) {
		t.Fatal("the redo made the rename again")
	}
}
