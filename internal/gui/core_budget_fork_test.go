package gui

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fork: the core scripts of ours. TestCoreScriptBudget leaves them out, so
// its budgets and their history stay upstream's own and a sync meets no
// conflict there; they are weighed here instead, on the same terms: a
// change that needs more raises this and says why in its commit.
var forkScripts = []string{"lifecycle.js", "identity.js"}

const forkScriptBudget = 44_700 // 43,962 with lifecycle.js (R1) and identity.js (the identity GUI moved out of app.js), plus 1.5%

func forkScript(name string) bool { return slices.Contains(forkScripts, name) }

// fork: the bytes our hooks add inside app.js (lifecycleError, identityRead,
// renderIdentityProfiles, identityField, the rev the save bodies carry, the
// stale and committed catches). TestCoreScriptBudget weighs app.js and the
// core against upstream's budgets plus this, so those budgets stay
// upstream's text as they come; a hook that needs more raises this and
// says why in its commit.
const forkHookBudget = 2_200 // 1,678 on upstream v0.1.1353, plus room for the accountmodels rev

func TestForkScriptBudget(t *testing.T) {
	page, err := os.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	loaded := map[string]bool{}
	for _, m := range regexp.MustCompile(`<script src="([^"?]+)`).FindAllStringSubmatch(string(page), -1) {
		loaded[m[1]] = true
	}
	total := 0
	for _, name := range forkScripts {
		// one not loaded is left out of both budgets: a name gone stale
		if !loaded[name] {
			t.Errorf("index.html doesn't load %s: take it out of forkScripts", name)
		}
		b, err := os.ReadFile(filepath.Join("assets", name))
		if err != nil {
			t.Fatal(err)
		}
		total += len(b)
	}
	if total > forkScriptBudget {
		t.Errorf("the fork's scripts are %d bytes, over their budget of %d: raise it and say why", total, forkScriptBudget)
	}
	// the page's words test reads the same files (tests/strings.cjs's FORK)
	src, err := os.ReadFile(filepath.Join("tests", "strings.cjs"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const FORK = \[([^\]]*)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatal("tests/strings.cjs has no FORK list")
	}
	var fork []string
	for _, q := range strings.Split(string(m[1]), ",") {
		fork = append(fork, strings.Trim(strings.TrimSpace(q), `"`))
	}
	if !slices.Equal(fork, forkScripts) {
		t.Errorf("tests/strings.cjs's FORK is %q, forkScripts %q: keep them the same", fork, forkScripts)
	}
}
