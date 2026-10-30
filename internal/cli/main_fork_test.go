package cli

import (
	"testing"

	"github.com/yetone/magpie/internal/library"
)

// fork: TestMain has an npx server's package never installed. Without it
// `library mcp add … npx -y fs` (TestLibraryMCPHub) ran a real npm install
// against the registry, whose folder only sometimes outlived the test.
func TestNoNpmInstallInTheseTests(t *testing.T) {
	if !library.NpxInstallOff() {
		t.Fatal("an npx server's package is installed for real: TestMain doesn't call library.NoNpxInstall")
	}
}
