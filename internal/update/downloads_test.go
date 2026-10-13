package update

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A new version this computer won't start says where to get it by hand.
// That was usemagpie.ai, upstream's site: a build from there replaces this
// fork's and from then on updates from upstream. It is this fork's own
// Releases page now, MAGPIE_RELEASE_REPO's when set.
func TestABlockedUpdateSendsToTheForksReleases(t *testing.T) {
	t.Setenv("MAGPIE_RELEASE_REPO", "")
	for _, err := range []error{
		&BlockedError{Err: &os.PathError{Op: "fork/exec", Path: "magpie.exe", Err: errIntegrityPolicy}},
		&BlockedError{Err: errors.New("it quit as soon as it started")},
	} {
		msg := err.Error()
		if !strings.Contains(msg, "https://github.com/"+ForkRepo+"/releases") || strings.Contains(msg, Site) {
			t.Errorf("%q: want it to send to github.com/%s/releases, not %s", msg, ForkRepo, Site)
		}
	}
	t.Setenv("MAGPIE_RELEASE_REPO", "someone/magpie")
	if got := Downloads(); got != "https://github.com/someone/magpie/releases" {
		t.Errorf("Downloads() = %q with MAGPIE_RELEASE_REPO set", got)
	}
}
