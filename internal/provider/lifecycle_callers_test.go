package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/settings"
)

// A commit that fails once its journal is written says so to errors.Is
// (ErrPending), with its own error and words unchanged: a caller tells
// "finished by the next write, don't make it again" from a refusal without
// reading the text. A commit refused before its journal is not pending.
func TestAPendingCommitIsErrPending(t *testing.T) {
	for _, managed := range []bool{false, true} {
		renameHome(t, managed)
		lifecycle.TestStage = func(s string) error {
			if s == "providers.json:write" {
				return errors.New("injected")
			}
			return nil
		}
		t.Cleanup(func() { lifecycle.TestStage = nil })
		err := Rename("relay", "relay2")
		lifecycle.TestStage = nil
		if !errors.Is(err, ErrPending) {
			t.Fatalf("managed %v: a rename failing after its journal: %v, not ErrPending", managed, err)
		}
		if !strings.Contains(err.Error(), "injected") || !strings.Contains(err.Error(), "(it is finished by the next provider write or start-up)") {
			t.Fatalf("its words changed: %v", err)
		}
		if strings.Contains(err.Error(), ErrPending.Error()) {
			t.Fatalf("ErrPending's own words were added to the error: %v", err)
		}
		if errors.Is(err, ErrStale) {
			t.Fatalf("a pending rename reads as stale: %v", err)
		}
	}
	// refused before any journal: not pending
	h := readyHome(t, nil, "relay")
	old := h.find("relay")
	if err := Delete("relay"); err != nil {
		t.Fatal(err)
	}
	if err := Save(h.row("relay")); err != nil {
		t.Fatal(err)
	}
	if err := DeleteChecked(old); errors.Is(err, ErrPending) || !errors.Is(err, ErrStale) {
		t.Fatalf("a delete of a provider made anew since: %v", err)
	}
}

// A rename whose settings step fails is a FollowUpError whose Redo does
// that step alone: once what failed is mended, Redo renames the model
// preferences and agent lists and leaves providers.json and lifecycle.json
// as the rename committed them. Still failing, it says so and changes
// nothing else.
func TestAFollowUpRedoesOnlyItsStep(t *testing.T) {
	for _, checked := range []bool{false, true} {
		h := readyHome(t, nil, "relay")
		if err := os.MkdirAll(filepath.Dir(settings.Path()), 0o700); err != nil {
			t.Fatal(err)
		}
		bad := []byte(`{"theme":"no-such-theme","visible":{"codex":["relay"]}}`)
		if err := os.WriteFile(settings.Path(), bad, 0o600); err != nil {
			t.Fatal(err)
		}
		var err error
		if checked {
			_, err = RenameChecked(h.find("relay"), "relay2")
		} else {
			err = Rename("relay", "relay2")
		}
		var fu *FollowUpError
		if !errors.As(err, &fu) || !fu.Committed || fu.Step != StepSettings {
			t.Fatalf("checked %v: %v", checked, err)
		}
		committed := readCommitFiles(t)
		if rerr := fu.Redo(); rerr == nil {
			t.Fatal("a redo over the same bad settings.json went through")
		}
		if got := readCommitFiles(t); string(got.providers) != string(committed.providers) || string(got.meta) != string(committed.meta) {
			t.Fatal("a failed redo wrote providers.json or lifecycle.json")
		}
		if err := os.WriteFile(settings.Path(), []byte(`{"visible":{"codex":["relay"]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fu.Redo(); err != nil {
			t.Fatalf("checked %v: the redo after mending settings.json: %v", checked, err)
		}
		if v := settings.Load().Visible["codex"]; len(v) != 1 || v[0] != "relay2" {
			t.Fatalf("checked %v: codex is shown %v after the redo, want relay2", checked, v)
		}
		if got := readCommitFiles(t); string(got.providers) != string(committed.providers) || string(got.meta) != string(committed.meta) {
			t.Fatal("the redo made the rename again")
		}
	}
	// a FollowUpError no step can redo answers its own error
	fu := &FollowUpError{Committed: true, Step: "x", Err: errors.New("x failed")}
	if err := fu.Redo(); err == nil || err.Error() != "x failed" {
		t.Fatalf("Redo with no step: %v", err)
	}
	if FollowUp("x", nil, nil) != nil {
		t.Fatal("FollowUp of no error is an error")
	}
}

// SetOff with Expect switches off the provider it was read as, never one
// that holds its id since; without, it goes by id as before.
func TestSetOffChecksTheValueItWasGiven(t *testing.T) {
	h := readyHome(t, nil, "relay")
	old := h.find("relay")
	if err := Delete("relay"); err != nil {
		t.Fatal(err)
	}
	if err := Save(h.row("relay")); err != nil {
		t.Fatal(err)
	}
	if err := SetOff("relay", true, Expect(old)); !errors.Is(err, ErrStale) {
		t.Fatalf("SetOff of the old relay over the new one: %v", err)
	}
	if p, _ := rawRow(t, "relay"); p.Off {
		t.Fatal("the new relay was switched off")
	}
	if err := SetOff("relay", true); err != nil {
		t.Fatal(err)
	}
	if p, _ := rawRow(t, "relay"); !p.Off {
		t.Fatal("SetOff by id didn't switch it off")
	}
}
