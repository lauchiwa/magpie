package agent

import (
	"testing"
	"time"
)

// fork: with NoWSL, magpie looks in no WSL and never runs wsl.exe, though
// it would (wslOn); its func puts WSL back.
func TestNoWSLRunsNoWSL(t *testing.T) {
	on, run := wslOn, wslRun
	t.Cleanup(func() { wslOn, wslRun = on, run })
	wslOn = true
	ran := 0
	wslRun = func(time.Duration, ...string) ([]byte, error) { ran++; return nil, nil }
	back := NoWSL()
	if a, h := wslAgents(), wslHomes(); len(a) != 0 || len(h) != 0 || ran != 0 {
		t.Fatalf("with NoWSL: %d agents, %d homes, wsl.exe run %d times", len(a), len(h), ran)
	}
	back()
	if !wslOn {
		t.Fatal("NoWSL's func didn't put WSL back")
	}
}
