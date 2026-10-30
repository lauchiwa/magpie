package gateway

import (
	"runtime"
	"testing"
)

// fork: noHandOffHere skips a test that hands the gateway's socket on
// (ListenerFile, FileListener) on Windows, where a TCP listener has no
// File and the gui hands over on darwin and linux only (handover.go). A
// `make dev` case (dev) takes no socket and runs there too.
func noHandOffHere(t *testing.T, dev bool) {
	t.Helper()
	if runtime.GOOS == "windows" && !dev {
		t.Skip("no socket handover on Windows")
	}
}
