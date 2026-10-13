package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/update"
)

// `magpie update` from a build from source sends to where releases are:
// this fork's own, not usemagpie.ai, upstream's, whose build would replace
// this fork's.
func TestUpdateFromSourceSendsToTheForksReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(update.Release{Version: "0.1.11-rc.0", URL: "https://github.com/" + update.ForkRepo + "/releases/tag/v0.1.11-rc.0"})
	}))
	defer srv.Close()
	t.Setenv("MAGPIE_UPDATE_FEED", srv.URL)
	t.Setenv("MAGPIE_RELEASE_REPO", "")
	was := version
	t.Cleanup(func() { version = was })
	version = "v0.1.10-rc.0-3-g0bcb2cc"

	err := updateCmd([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "built from source") {
		t.Fatalf("a build from source: %v", err)
	}
	if !strings.Contains(err.Error(), "https://github.com/"+update.ForkRepo+"/releases") || strings.Contains(err.Error(), update.Site) {
		t.Errorf("%q: want it to send to the fork's releases, not %s", err, update.Site)
	}
}
