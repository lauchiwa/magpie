package update

import "testing"

// This fork publishes its own releases as X.Y.Z-rc.N: X.Y.Z is the upstream
// version it synced from, N counts its releases on top of that one
// (scripts/release-version.mjs). The updater picks a version with Newer, so
// these check that the tags the script produces really do increase here, and
// that the bare upstream version the rule forbids is forbidden for a reason.
func TestForkReleaseTagsIncrease(t *testing.T) {
	// The order release-version.mjs produces: rc.0 first, counting up past
	// nine, then the next upstream version from rc.0 again.
	order := []string{
		"0.1.1182-rc.0",
		"0.1.1182-rc.1",
		"0.1.1182-rc.2",
		"0.1.1182-rc.9",
		"0.1.1182-rc.10",
		"0.1.1183-rc.0",
	}
	for i := 1; i < len(order); i++ {
		if !Newer(order[i], order[i-1]) {
			t.Errorf("Newer(%q, %q) = false, want the later release to win", order[i], order[i-1])
		}
		if Newer(order[i-1], order[i]) {
			t.Errorf("Newer(%q, %q) = true, want the earlier release to lose", order[i-1], order[i])
		}
	}
}

// A bare upstream version outranks every -rc.N after it, so an install that
// saw one would never move again. That is why the fork never publishes one.
func TestBareUpstreamVersionOutranksTheForksReleases(t *testing.T) {
	if !Newer("0.1.1182", "0.1.1182-rc.10") {
		t.Error("a bare version no longer outranks its pre-releases; the rc rule's reason has changed")
	}
	if Newer("0.1.1182-rc.11", "0.1.1182") {
		t.Error("an rc now outranks the bare version; the rc rule's reason has changed")
	}
}

// A fork release is a release: it updates itself, and is not mistaken for a
// build from source the way git describe's output is.
func TestForkReleaseIsAReleasedVersion(t *testing.T) {
	for _, v := range []string{"0.1.1182-rc.0", "v0.1.1182-rc.10"} {
		if !Released(v) {
			t.Errorf("Released(%q) = false, want a fork release to update itself", v)
		}
	}
	if Released("v0.1.1182-rc.0-3-g0bcb2cc") {
		t.Error("a build from source past a fork tag counts as a release")
	}
}
