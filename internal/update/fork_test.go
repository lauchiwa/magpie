package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// This fork updates itself from its own releases on GitHub, not from
// upstream's feed. These stand GitHub in for itself and check what the
// updater makes of the releases: the newest version, the notes, and every
// file with the checksum the release's own SHA256SUMS gives.

// fakeGitHub serves the releases of one repository, as GitHub's API and
// downloads do. Each release is a tag with the files named in it.
type fakeGitHub struct {
	*httptest.Server
	asked []string
}

// body is one file in a release.
const body = "magpie, built by the fork"

func newFakeGitHub(t *testing.T, repo string, rels ...ghRelease) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	sum := sha256.Sum256([]byte(body))
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.asked = append(f.asked, r.URL.Path)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	// The release list, as the API gives it. The files are served from this
	// same server, so the download goes through the updater's own code.
	mux.HandleFunc("/repos/"+repo+"/releases", func(w http.ResponseWriter, r *http.Request) {
		out := make([]ghRelease, len(rels))
		for i, rel := range rels {
			out[i] = rel
			for j := range out[i].Assets {
				out[i].Assets[j].URL = f.URL + "/download/" + rel.TagName + "/" + out[i].Assets[j].Name
			}
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		if name == sumsFile {
			// as sha256sum writes it: two spaces between
			var b strings.Builder
			for _, n := range []string{BinaryAsset(), AppAsset()} {
				fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), n)
			}
			w.Write([]byte(b.String()))
			return
		}
		w.Write([]byte(body))
	})
	t.Setenv("MAGPIE_RELEASE_API", f.URL)
	t.Setenv("MAGPIE_RELEASE_REPO", repo)
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	t.Setenv("MAGPIE_NOTES_FEED", "")
	return f
}

// rel is a published release of this fork, with the files a release has.
func rel(tag, notes string) ghRelease {
	r := ghRelease{TagName: tag, Body: notes, HTMLURL: "https://github.com/x/releases/tag/" + tag, Prerelease: true}
	for _, n := range []string{BinaryAsset(), AppAsset(), sumsFile} {
		r.Assets = append(r.Assets, struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			URL  string `json:"browser_download_url"`
		}{Name: n, Size: int64(len(body))})
	}
	return r
}

// Every version this fork publishes is a pre-release (-rc.N), which GitHub's
// "latest release" leaves out entirely. The updater must still find it.
func TestLatestIsTheNewestPreRelease(t *testing.T) {
	newFakeGitHub(t, "fork/magpie",
		rel("v0.1.1182-rc.1", "second"),
		rel("v0.1.1182-rc.10", "tenth"),
		rel("v0.1.1182-rc.2", "third"),
	)
	got, err := LatestIn(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	// rc.10 is the newest by the updater's own comparison, not by the order
	// GitHub listed them in and not by text (where rc.2 would win).
	if got.Version != "0.1.1182-rc.10" {
		t.Fatalf("version %q, want 0.1.1182-rc.10", got.Version)
	}
	if got.Notes != "tenth" {
		t.Errorf("notes %q", got.Notes)
	}
}

// The checksum an install is verified against comes from the release's own
// SHA256SUMS, so a download is still refused when it doesn't match.
func TestLatestCarriesEachFilesChecksum(t *testing.T) {
	newFakeGitHub(t, "fork/magpie", rel("v0.1.1182-rc.0", "first"))
	got, err := LatestIn(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := got.Assets[BinaryAsset()]
	if !ok {
		t.Fatalf("no %s in %v", BinaryAsset(), got.Assets)
	}
	sum := sha256.Sum256([]byte(body))
	if a.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("checksum %q", a.SHA256)
	}
	if _, ok := got.Assets[sumsFile]; ok {
		t.Error("SHA256SUMS is offered as something to install")
	}
	// the download goes through the updater's own check
	path := filepath.Join(t.TempDir(), "magpie.new")
	if err := download(context.Background(), a, path); err != nil {
		t.Fatalf("download: %v", err)
	}
	// and a changed checksum is refused
	a.SHA256 = strings.Repeat("0", 64)
	if err := download(context.Background(), a, path); err == nil {
		t.Error("a file that doesn't match its checksum was accepted")
	}
}

// A release with no SHA256SUMS can't be checked, so it is not offered at
// all rather than offered and refused at install time.
func TestAReleaseWithoutChecksumsIsNotOffered(t *testing.T) {
	bad := rel("v0.1.1182-rc.0", "first")
	bad.Assets = bad.Assets[:1] // the binary alone, no SHA256SUMS
	newFakeGitHub(t, "fork/magpie", bad)
	if _, err := LatestIn(context.Background(), ""); err == nil || !strings.Contains(err.Error(), sumsFile) {
		t.Fatalf("err = %v, want it to name %s", err, sumsFile)
	}
}

// Drafts are not published versions, and a tag that is not a version at all
// is not one either.
func TestDraftsAndNonVersionsAreLeftOut(t *testing.T) {
	draft := rel("v0.1.1183-rc.0", "unfinished")
	draft.Draft = true
	newFakeGitHub(t, "fork/magpie", draft, rel("nightly", "not a version"), rel("v0.1.1182-rc.3", "real"))
	got, err := LatestIn(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.1.1182-rc.3" {
		t.Fatalf("version %q, want the newest published release", got.Version)
	}
}

// An empty repository says so, rather than looking like a network failure.
func TestNoReleaseYetSaysSo(t *testing.T) {
	newFakeGitHub(t, "fork/magpie")
	_, err := LatestIn(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "no release yet") {
		t.Fatalf("err = %v", err)
	}
}

// What's new reads the notes of the releases between two versions from the
// same place.
func TestNotesBetweenReadsTheForksReleases(t *testing.T) {
	newFakeGitHub(t, "fork/magpie",
		rel("v0.1.1182-rc.0", "### Changes\n\n- first"),
		rel("v0.1.1182-rc.1", "### Changes\n\n- second"),
		rel("v0.1.1182-rc.2", "### Changes\n\n- third"),
	)
	got, err := NotesBetween(context.Background(), "0.1.1182-rc.0", "0.1.1182-rc.2", "")
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, n := range got {
		versions = append(versions, n.Version)
	}
	// after rc.0, up to rc.2, newest first
	if strings.Join(versions, ",") != "0.1.1182-rc.2,0.1.1182-rc.1" {
		t.Fatalf("versions %v", versions)
	}
}

// A release's page is this fork's own, so "what changed" doesn't send
// someone to a version upstream published.
func TestReleasePageIsThisForksRepository(t *testing.T) {
	t.Setenv("MAGPIE_RELEASE_REPO", "fork/magpie")
	if got, want := ReleasePage("0.1.1182-rc.3"), "https://github.com/fork/magpie/releases/tag/v0.1.1182-rc.3"; got != want {
		t.Errorf("ReleasePage = %q, want %q", got, want)
	}
	t.Setenv("MAGPIE_RELEASE_REPO", "")
	if got := ReleasePage("v0.1.1182-rc.3"); !strings.Contains(got, ForkRepo) {
		t.Errorf("ReleasePage = %q, want this fork's repository %s", got, ForkRepo)
	}
}

// The fork's own repository is the default, and upstream's release
// repository is not read.
func TestRepoDefaultsToTheFork(t *testing.T) {
	t.Setenv("MAGPIE_RELEASE_REPO", "")
	if Repo() != ForkRepo {
		t.Errorf("Repo() = %q, want %q", Repo(), ForkRepo)
	}
	if strings.Contains(ForkRepo, "yetone") {
		t.Errorf("the fork's releases come from %q, which is upstream's", ForkRepo)
	}
}

// A JSON feed still works when one is named: a test's server, or a feed run
// by whoever deploys this.
func TestAFeedIsUsedWhenOneIsNamed(t *testing.T) {
	f := newFakeGitHub(t, "fork/magpie", rel("v0.1.1182-rc.0", "from github"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Version: "9.9.9", Notes: "from the feed"})
	}))
	defer srv.Close()
	t.Setenv("MAGPIE_UPDATE_FEED", srv.URL)
	got, err := LatestIn(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "9.9.9" || got.Notes != "from the feed" {
		t.Fatalf("got %q %q, want the feed's answer", got.Version, got.Notes)
	}
	for _, p := range f.asked {
		if strings.HasPrefix(p, "/repos/") {
			t.Error("GitHub was asked even though a feed was named")
		}
	}
}

// GitHub limits unauthenticated callers by address. The message says what
// happened rather than only a status code.
func TestRateLimitSaysWhatHappened(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("MAGPIE_RELEASE_API", srv.URL)
	t.Setenv("MAGPIE_RELEASE_REPO", "fork/magpie")
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	_, err := LatestIn(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "limiting") {
		t.Fatalf("err = %v, want it to explain the limit", err)
	}
}
