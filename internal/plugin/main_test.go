package plugin

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/testenv"
)

// realNode is the node program the host.js tests run, found before the
// tests' home replaces the user's: a version manager's shim on PATH (mise's
// node) reads which version to run from the user's own config, and in a
// new home it has none and downloads node from the network. Asked of node
// itself (process.execPath), it is the program, which needs no config. ""
// when there is no node here.
var realNode string

func findRealNode() string {
	shim, err := exec.LookPath("node")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, shim, "-p", "process.execPath").Output()
	if p := strings.TrimSpace(string(out)); err == nil && filepath.IsAbs(p) {
		return p
	}
	return ""
}

// The tests run in a home of their own, never the user's real files, and
// off the network (testenv.Offline): each sandbox is a new home with no
// models.dev catalog, so every host start fetched the real models.dev for
// up to 30s, and on a runner where it didn't answer two starts spent a
// test's whole minute (#1922).
func TestMain(m *testing.M) {
	if os.Getenv(testenv.Marker) == "" {
		realNode = findRealNode()
	}
	os.Exit(testenv.RunIn(m, func(string) { testenv.Offline() }))
}

// A host start's models.dev fetch is refused at once, never sent: the
// tests don't wait on a vendor's server (#1922).
func TestStaysOffTheNetwork(t *testing.T) {
	resp, err := http.Get("https://models.dev/api.json")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("models.dev answered %s: the package's tests reach the network", resp.Status)
	}
	if !strings.Contains(err.Error(), "tests stay off the network") {
		t.Fatalf("models.dev: %v, not testenv's refusal", err)
	}
}
