package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/lifecycle"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// stepcodeOn is a home whose StepCode is on deepseek/pro through magpie,
// with lifecycle.json readied: the path a configured magpie takes.
func stepcodeOn(t *testing.T) (config string) {
	t.Helper()
	home, _ := museHome(t)
	t.Setenv("STEP_CODING_AGENT_DIR", "")
	t.Setenv("STEPCODE_CONFIG_DIR", "")
	config = filepath.Join(home, ".stepcode", "config.toml")
	stepcodeWriteTestFile(t, config, "defaultProvider = \"anthropic\"\ndefaultModel = \"claude\"\n")
	stepcodeWriteTestFile(t, filepath.Join(home, ".stepcode", "models.json"), `{"providers":{}}`)
	if err := stepcode(home).Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	if err := provider.ReadyLifecycle(); err != nil {
		t.Fatal(err)
	}
	return config
}

func stepcodeModel(t *testing.T, config string) string {
	t.Helper()
	cfg, err := readStepcodeTOML(config)
	if err != nil {
		t.Fatal(err)
	}
	return stepcodeValue(cfg.Model)
}

// A checked rename whose agents step fails after the commit is a
// FollowUpError for that step: the rename stands, and Redo moves the
// agents (the same fields, read before the rename) without renaming again.
func TestRenameCheckedRedoesOnlyTheAgents(t *testing.T) {
	config := stepcodeOn(t)
	p, err := provider.Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	applyMoves = func(moves []renameMove, from, to string) ([]string, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("injected: StepCode's config can't be written")
		}
		return applyRenameMoves(moves, from, to)
	}
	t.Cleanup(func() { applyMoves = applyRenameMoves })
	got, _, err := RenameChecked(*p, "ds")
	var fu *provider.FollowUpError
	if !errors.As(err, &fu) || !fu.Committed || fu.Step != StepAgents {
		t.Fatalf("a rename whose agents step failed: %v", err)
	}
	if got.ID != "ds" {
		t.Fatalf("RenameChecked gave %q with its follow-up", got.ID)
	}
	if m := stepcodeModel(t, config); m != "deepseek/pro" {
		t.Fatalf("StepCode is on %q before the redo", m)
	}
	committed, _ := os.ReadFile(provider.Path())
	meta, _ := os.ReadFile(lifecycle.Path())
	if err := fu.Redo(); err != nil {
		t.Fatalf("the agents step again: %v", err)
	}
	if m := stepcodeModel(t, config); m != "ds/pro" {
		t.Fatalf("StepCode is on %q after the redo, want ds/pro", m)
	}
	after, _ := os.ReadFile(provider.Path())
	metaAfter, _ := os.ReadFile(lifecycle.Path())
	if string(after) != string(committed) || string(metaAfter) != string(meta) {
		t.Fatal("the redo wrote providers.json or lifecycle.json: the rename was made again")
	}
}

// A checked rename whose settings and agents steps both fail is one
// FollowUpError naming both, and its Redo does both, never the rename.
func TestRenameCheckedRedoesBothSteps(t *testing.T) {
	config := stepcodeOn(t)
	// a theme Save refuses: the settings step fails until it is mended
	if err := os.WriteFile(settings.Path(), []byte(`{"theme":"no-such-theme","visible":{"codex":["deepseek"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := provider.Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	applyMoves = func(moves []renameMove, from, to string) ([]string, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("injected: StepCode's config can't be written")
		}
		return applyRenameMoves(moves, from, to)
	}
	t.Cleanup(func() { applyMoves = applyRenameMoves })
	_, _, err = RenameChecked(*p, "ds")
	var fu *provider.FollowUpError
	if !errors.As(err, &fu) || !fu.Committed || fu.Step != provider.StepSettings+", "+StepAgents {
		t.Fatalf("a rename whose settings and agents steps failed: %v", err)
	}
	committed, _ := os.ReadFile(provider.Path())
	meta, _ := os.ReadFile(lifecycle.Path())
	if err := os.WriteFile(settings.Path(), []byte(`{"visible":{"codex":["deepseek"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fu.Redo(); err != nil {
		t.Fatalf("both steps again: %v", err)
	}
	if v := settings.Load().Visible["codex"]; len(v) != 1 || v[0] != "ds" {
		t.Fatalf("codex is shown %v after the redo, want ds", v)
	}
	if m := stepcodeModel(t, config); m != "ds/pro" {
		t.Fatalf("StepCode is on %q after the redo, want ds/pro", m)
	}
	after, _ := os.ReadFile(provider.Path())
	metaAfter, _ := os.ReadFile(lifecycle.Path())
	if string(after) != string(committed) || string(metaAfter) != string(meta) {
		t.Fatal("the redo wrote providers.json or lifecycle.json: the rename was made again")
	}
}

// A checked rename that is refused (the value is stale) or left pending
// in its journal moves no agent: neither is a rename made now.
func TestRenameCheckedMovesNoAgentUnlessCommitted(t *testing.T) {
	config := stepcodeOn(t)
	calls := 0
	applyMoves = func(moves []renameMove, from, to string) ([]string, error) {
		calls++
		return applyRenameMoves(moves, from, to)
	}
	t.Cleanup(func() { applyMoves = applyRenameMoves })
	old, err := provider.Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1", Key: "k2", Models: []string{"pro", "flash"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RenameChecked(*old, "ds"); !errors.Is(err, provider.ErrStale) {
		t.Fatalf("a rename of an older rev: %v", err)
	}
	if calls != 0 || stepcodeModel(t, config) != "deepseek/pro" {
		t.Fatalf("a refused rename moved the agents (%d calls)", calls)
	}

	cur, err := provider.Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.TestStage = func(s string) error {
		if s == "providers.json:write" {
			return errors.New("injected")
		}
		return nil
	}
	t.Cleanup(func() { lifecycle.TestStage = nil })
	_, _, err = RenameChecked(*cur, "ds")
	lifecycle.TestStage = nil
	if !errors.Is(err, provider.ErrPending) {
		t.Fatalf("a rename failing after its journal: %v", err)
	}
	if calls != 0 || stepcodeModel(t, config) != "deepseek/pro" {
		t.Fatalf("a pending rename moved the agents (%d calls)", calls)
	}
}
