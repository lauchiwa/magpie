package plugin

import "testing"

// A plugin from a folder is told from a package the same on every OS. A
// Mac's plugins.json, carried to Windows by a sync, names its folder
// "/Users/…": Windows' filepath.IsAbs says that isn't absolute, but Node
// and Bun read it as a path there too, so it stays the Mac's own and isn't
// installed from npm (TestPluginsCarried in internal/provider).
func TestIsPathOnEveryOS(t *testing.T) {
	for spec, want := range map[string]bool{
		"/Users/me/my-plugin":                  true,
		`\\server\share\plugin`:                true,
		`\plugins\x`:                           true,
		`C:\plugins\x`:                         true,
		"C:/plugins/x":                         true,
		"file:///abs/plugin":                   true,
		"./my-plugin.js":                       true,
		"../my-plugin":                         true,
		"opencode-gemini-auth@latest":          false,
		"@magpie-community/opencode-kiro-auth": false,
		"owner/repo":                           false,
		"github:owner/repo":                    false,
		"C:x":                                  false,
		"":                                     false,
	} {
		if got := IsPath(spec); got != want {
			t.Errorf("IsPath(%q) = %v, want %v", spec, got, want)
		}
	}
}
