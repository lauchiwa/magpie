package cli

// Provider lifecycle at start-up (R1 G3b-2): run readies lifecycle.json
// once, after settings.Migrate has put the config folder in place and
// before the providers are held (provider.ReadyLifecycle). It never stops
// magpie from starting: a folder it can't write stays as before (by id),
// and what it can't settle is said on stderr, and refused where a request
// needs it.

import (
	"fmt"
	"os"

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
