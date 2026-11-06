package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/settings"
)

// Billing D4, write failures: a request a restart cut off is in a key's
// counts at its reservation, and the list and the key's limit say how
// many are, so its tokens aren't taken for calls the Usage page lists.
func TestGatewayKeyLimitPendingCLI(t *testing.T) {
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
	if _, err := call("limit", id, "day", "--tokens", "1000"); err != nil {
		t.Fatal(err)
	}
	keys, _ = access.List()
	who := access.Identity{KeyID: id, KeyName: keys[0].Name, Limit: keys[0].Limit}
	if _, refused := budget.Reserve(who, 400, "", time.Now()); refused != nil {
		t.Fatal(refused)
	}
	budget.Forget() // a restart, the request cut off
	got, err := call("limit", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "100 used of 1000") || !strings.Contains(got, "1 request cut off before it ended") {
		t.Fatalf("a key with a cut-off request shows %q", got)
	}
	list, err := call("list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "100/1000 tokens per day (1 cut off, counted as reserved)") {
		t.Fatalf("a key with a cut-off request lists as %q", list)
	}
}
