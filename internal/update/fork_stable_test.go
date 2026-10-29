package update

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/settings"
)

// fork: Settings' Stable channel picks from this fork's own releases as
// upstream's feed picks from upstream's (site/worker.js stableOf): as the
// week turns (Monday 00:00 UTC), the newest release that was out three
// days by then; an urgent one at once, until a weekly pick passes it.
func TestStableOfPicksAsUpstreamsFeedDoes(t *testing.T) {
	// Wednesday 2026-10-28 12:00 UTC: the week began Monday 10-26, so the
	// cut is Friday 10-23 00:00 UTC
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	at := func(r ghRelease, when string) ghRelease {
		ts, err := time.Parse(time.RFC3339, when)
		if err != nil {
			t.Fatal(err)
		}
		r.PublishedAt = ts
		return r
	}
	old := at(rel("v0.1.1551-rc.1", "- a fix"), "2026-10-20T08:00:00Z")
	onCut := at(rel("v0.1.1551-rc.2", "- another"), "2026-10-23T00:00:00Z")
	late := at(rel("v0.1.1551-rc.3", "- out after the cut"), "2026-10-23T00:00:01Z")
	newest := at(rel("v0.1.1969-rc.0", "- synced"), "2026-10-28T09:00:00Z")
	urgent := at(rel("v0.1.1969-rc.1", "- fixed\n\n> **Urgent release:** lost data"), "2026-10-28T10:00:00Z")
	future := at(rel("v0.1.1969-rc.2", "**urgent RELEASE:** clock skew"), "2026-10-28T13:00:00Z")
	unpublished := rel("v0.1.1969-rc.3", "**Urgent release:** a draft's date")

	for _, c := range []struct {
		name string
		rels []ghRelease
		want string
	}{
		{"the newest out by the cut, one out on it included", []ghRelease{newest, late, onCut, old}, "v0.1.1551-rc.2"},
		{"an urgent one at once", []ghRelease{urgent, newest, late, onCut, old}, "v0.1.1969-rc.1"},
		{"a release dated after now isn't one yet", []ghRelease{future, newest, onCut}, "v0.1.1551-rc.2"},
		{"one without a date isn't either", []ghRelease{unpublished, newest, onCut}, "v0.1.1551-rc.2"},
		{"an urgent one older than the weekly pick is passed", []ghRelease{onCut, at(rel("v0.1.1551-rc.0", "**Urgent release:** old"), "2026-10-19T00:00:00Z")}, "v0.1.1551-rc.2"},
		{"nothing old enough: the urgent one alone", []ghRelease{urgent, newest}, "v0.1.1969-rc.1"},
	} {
		got, ok := stableOf(c.rels, now)
		if !ok || got.TagName != c.want {
			t.Errorf("%s: %s %v, want %s", c.name, got.TagName, ok, c.want)
		}
	}
	if got, ok := stableOf([]ghRelease{newest, late}, now); ok {
		t.Errorf("nothing out by the cut and nothing urgent: picked %s", got.TagName)
	}
	// the week turns at Monday 00:00 UTC, whatever the zone now is read in
	for _, c := range []struct{ now, start string }{
		{"2026-10-26T00:00:00Z", "2026-10-26T00:00:00Z"},
		{"2026-10-25T23:59:59Z", "2026-10-19T00:00:00Z"},
		{"2026-10-26T07:30:00+08:00", "2026-10-19T00:00:00Z"},
		{"2026-11-01T23:00:00Z", "2026-10-26T00:00:00Z"},
	} {
		n, _ := time.Parse(time.RFC3339, c.now)
		if got := weekStart(n).Format(time.RFC3339); got != c.start {
			t.Errorf("weekStart(%s) = %s, want %s", c.now, got, c.start)
		}
	}
}

// fork: on the Stable channel the updater offers Stable's release from
// this fork's GitHub releases, not the newest; every version still gets
// the newest. Before, the setting changed nothing here: GitHub's path never
// read it, and every rc was offered.
func TestLatestOnStableReadsTheForksReleases(t *testing.T) {
	week := weekStart(time.Now())
	soaked := rel("v0.1.1551-rc.2", "soaked")
	soaked.PublishedAt = week.Add(-stableSoak - time.Hour)
	fresh := rel("v0.1.1969-rc.0", "fresh")
	fresh.PublishedAt = time.Now().Add(-time.Minute)
	newFakeGitHub(t, "fork/magpie", fresh, soaked)

	stableFor(t, "")
	if got, err := LatestIn(context.Background(), ""); err != nil || got.Version != "0.1.1969-rc.0" {
		t.Fatalf("every version: %v %v, want 0.1.1969-rc.0", got, err)
	}
	stableFor(t, settings.UpdateStable)
	got, err := LatestIn(context.Background(), "")
	if err != nil || got.Version != "0.1.1551-rc.2" || got.Notes != "soaked" {
		t.Fatalf("stable: %v %v, want 0.1.1551-rc.2", got, err)
	}
	if len(got.Assets) == 0 || got.Assets[AppAsset()].SHA256 == "" {
		t.Fatalf("stable's release came without its checksums: %+v", got.Assets)
	}
}

// fork: with nothing on the Stable channel yet (a fork whose releases are
// all younger than the cut), the updater says so instead of offering the
// newest.
func TestStableWithNothingOldEnoughSaysSo(t *testing.T) {
	fresh := rel("v0.1.1969-rc.0", "fresh")
	fresh.PublishedAt = time.Now().Add(-time.Minute)
	newFakeGitHub(t, "fork/magpie", fresh)
	stableFor(t, settings.UpdateStable)
	got, err := LatestIn(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "Stable channel") {
		t.Fatalf("got %v, %v; want an error naming the Stable channel", got, err)
	}
}
