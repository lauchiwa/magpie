package gui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// What the window's core may weigh, in bytes of script: everything
// index.html loads that isn't a plugin's (assets/plugins/<id>/) and isn't
// the words (i18n.js). A page belongs in a plugin. Each page moved out
// lowers these to what is left, with about 1.5% room for the core's own work,
// so a page can't quietly grow back into the core; a change that needs more
// raises them and says why in its commit.
const (
	appScriptBudget  = 1_317_000 // app.js: 1,198,239 after Usage left (P2); 1,218,098 with the 10-15 fixes to pages still in the core; 1,232,466 with the provider editor's Advanced fold and every dialog holding its place, the core's own (10-16); 1,250,533 with the quota refresh kept through redraws, the core's own since the tray panel draws it too, plus 1.5% (#2397); 1,272,497 with this fork's identity profiles panel, the editor's identity summary and the update mirror, all on core pages (Providers, Settings), plus 1.5%; 1,284,948 with upstream v0.1.1297 (Free first in the provider editor's ROUTINGS), inside it; 1,296,582 with upstream v0.1.1303 (the dialog grown from its row, the agents' own settings in their rows) and this fork's together, plus 1.5%
	coreScriptBudget = 1_511_000 // every core script: 1,358,034 after Usage left; 1,380,003 with them; 1,405,937 with the fold; 1,426,102 with 10-16's core work (the GitHub list loading, the move to plugins, Settings › Long conversations), plus 1.5% (#2612); 1,462,824 with this fork's, plus 1.5%; 1,469,693 with upstream v0.1.1294's loadBack (the window's one state read per return) and OpenCode Zen's balance note, inside it; 1,488,094 with R1's lifecycle.js and upstream v0.1.1297 together, plus 1.5%; 1,499,728 with upstream v0.1.1303, inside it
)

func TestCoreScriptBudget(t *testing.T) {
	page, err := os.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	total, n := 0, 0
	for _, m := range regexp.MustCompile(`<script src="([^"?]+)`).FindAllStringSubmatch(string(page), -1) {
		name := m[1]
		if strings.HasPrefix(name, "plugins/") || name == "i18n.js" {
			continue
		}
		b, err := os.ReadFile(filepath.Join("assets", name))
		if os.IsNotExist(err) {
			continue // made by the server as it serves the page (boot.js)
		}
		if err != nil {
			t.Fatal(err)
		}
		if name == "app.js" && len(b) > appScriptBudget {
			t.Errorf("app.js is %d bytes, over its budget of %d: put the page's code in its plugin, or raise the budget and say why", len(b), appScriptBudget)
		}
		total += len(b)
		n++
	}
	if n < 5 {
		t.Fatalf("found only %d core scripts in index.html", n)
	}
	if total > coreScriptBudget {
		t.Errorf("the core's scripts are %d bytes, over their budget of %d: put the page's code in its plugin, or raise the budget and say why", total, coreScriptBudget)
	}
}
