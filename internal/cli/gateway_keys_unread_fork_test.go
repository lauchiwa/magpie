package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/settings"
	usagelog "github.com/yetone/magpie/internal/usage"
)

// Billing D4: when a limited key's usage can't be read, the list and the
// key's limit say so, never "0 used" or "open", since the gateway refuses
// the key until the log reads again.
func TestGatewayKeyLimitUnreadCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	budget.Forget()
	t.Cleanup(budget.Forget)
	if err := settings.Save(settings.Settings{LAN: true}); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := gatewayKeysTo(&out, append([]string{"gateway-key"}, args...))
		return out.String(), err
	}
	if _, err := call("add", "Team"); err != nil {
		t.Fatal(err)
	}
	keys, _ := access.List()
	id := keys[0].ID
	if _, err := call("limit", id, "day", "--tokens", "1000", "--cost", "5"); err != nil {
		t.Fatal(err)
	}
	// usage.jsonl is there, and isn't a file that can be read
	if err := os.MkdirAll(filepath.Dir(usagelog.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(usagelog.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	budget.Forget()
	got, err := call("limit", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "couldn't read the key's usage") || strings.Contains(got, "used of") || strings.Contains(got, "State   open") {
		t.Fatalf("an unread key's limit shows %q", got)
	}
	list, err := call("list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "usage unread per day (refused until it can be read)") || strings.Contains(list, "0/1000") || strings.Contains(list, "$0.00") {
		t.Fatalf("an unread key lists as %q", list)
	}
	// once it reads again, the key is open with nothing used
	if err := os.Remove(usagelog.Path()); err != nil {
		t.Fatal(err)
	}
	budget.Forget()
	if got, _ := call("limit", id); !strings.Contains(got, "0 used of 1000") || !strings.Contains(got, "State   open") {
		t.Fatalf("a read log shows %q", got)
	}
}
