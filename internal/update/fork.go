package update

// This fork updates itself from its own releases, on its own GitHub
// repository, rather than from upstream's.
//
// Upstream's updater asks a JSON feed (usemagpie.ai/api/latest) that a
// Cloudflare worker builds from upstream's release repository. This fork has
// no such feed, so it asks GitHub for the releases of Repo() and builds the
// same Release value itself: the newest version, its notes, and every file
// with the SHA-256 the release's own SHA256SUMS gives. Everything after that
// — the download, the checksum check, the staging and the swap — is upstream's
// code unchanged, so a file that doesn't match its checksum is still never
// installed.
//
// Two things about this fork's releases decide the shape of this code:
//
//   - Every version it publishes is vX.Y.Z-rc.N, which GitHub counts as a
//     pre-release (docs/fork-releases.md). GitHub's "latest release" leaves
//     pre-releases out, so that endpoint reports nothing at all here. The
//     releases are listed instead and the newest is picked with Newer, the
//     same comparison the updater uses everywhere else.
//   - The release repository has to be public, because an install downloads
//     from it without credentials.
//   - The release list and SHA256SUMS are asked of GitHub alone
//     (source.DoOfficial), never of a fallback mirror. They say what the
//     files must hash to; a mirror that served them could serve a file
//     and the hash it is checked against, and an ad-hoc signed app has no
//     team for sameSigner to catch the swap. Only the files themselves may
//     come through a mirror (fetch), where the hash is what stops one.
//
// MAGPIE_UPDATE_FEED still points the updater at a JSON feed instead, for a
// test or for anyone who runs a feed of their own; MAGPIE_RELEASE_REPO points
// it at another GitHub repository.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/yetone/magpie/internal/source"
)

// ForkRepo is the GitHub repository this fork's releases are published to,
// as the Release workflow uploads them (owner/name).
const ForkRepo = "lauchiwa/magpie"

// Repo is the repository the updater takes releases from.
// MAGPIE_RELEASE_REPO points it at another one.
func Repo() string {
	if r := strings.TrimSpace(os.Getenv("MAGPIE_RELEASE_REPO")); r != "" {
		return r
	}
	return ForkRepo
}

// ownFeed reports whether the updater reads GitHub itself. It does unless a
// JSON feed is named, which keeps MAGPIE_UPDATE_FEED working as before.
func ownFeed() bool { return os.Getenv("MAGPIE_UPDATE_FEED") == "" }

// api is where the releases are listed. MAGPIE_RELEASE_API points it
// elsewhere, for a test.
func api() string {
	if a := strings.TrimSpace(os.Getenv("MAGPIE_RELEASE_API")); a != "" {
		return a
	}
	return "https://api.github.com"
}

// ghRelease is the part of GitHub's release that the updater needs.
type ghRelease struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// forkReleases is the repository's releases, newest first by version.
// Drafts are left out; pre-releases are not, because every version this fork
// publishes is one.
func forkReleases(ctx context.Context) ([]ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=100", strings.TrimSuffix(api(), "/"), Repo())
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// GitHub alone: the list names the release and where its checksums are
	res, err := source.DoOfficial(client, req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode == 403 || res.StatusCode == 429 {
			return nil, fmt.Errorf("the releases of %s: %s (GitHub is limiting this address; try again later)", Repo(), res.Status)
		}
		return nil, fmt.Errorf("the releases of %s: %s", Repo(), res.Status)
	}
	var all []ghRelease
	if err := json.NewDecoder(res.Body).Decode(&all); err != nil {
		return nil, fmt.Errorf("the releases of %s: %w", Repo(), err)
	}
	var out []ghRelease
	for _, r := range all {
		if r.Draft || parse(r.TagName) == nil {
			continue
		}
		out = append(out, r)
	}
	// newest first, by the updater's own comparison and not by GitHub's order
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && Newer(out[j].TagName, out[j-1].TagName); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// forkLatest is the newest release of Repo(), its notes in lang.
func forkLatest(ctx context.Context, lang string) (*Release, error) {
	rels, err := forkReleases(ctx)
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("%s has no release yet", Repo())
	}
	return release(ctx, rels[0], lang)
}

// release turns one of GitHub's releases into the Release the updater
// installs: every file with the SHA-256 from the release's own SHA256SUMS.
// A release whose files have no checksums is not offered, because download
// refuses an asset without one.
func release(ctx context.Context, gh ghRelease, lang string) (*Release, error) {
	sums, err := checksums(ctx, gh)
	if err != nil {
		return nil, err
	}
	rel := &Release{
		Version: strings.TrimPrefix(gh.TagName, "v"),
		Notes:   InLang(gh.Body, lang),
		URL:     gh.HTMLURL,
		Assets:  map[string]Asset{},
	}
	for _, a := range gh.Assets {
		if a.Name == sumsFile {
			continue
		}
		rel.Assets[a.Name] = Asset{URL: a.URL, Size: a.Size, SHA256: sums[a.Name]}
	}
	return rel, nil
}

// sumsFile is the file the release workflow writes the checksums to.
const sumsFile = "SHA256SUMS"

// checksums is the release's SHA256SUMS, by file name: the hashes the
// download is checked against.
func checksums(ctx context.Context, gh ghRelease) (map[string]string, error) {
	var url string
	for _, a := range gh.Assets {
		if a.Name == sumsFile {
			url = a.URL
			break
		}
	}
	if url == "" {
		return nil, fmt.Errorf("release %s has no %s, so its files can't be checked", gh.TagName, sumsFile)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub alone: these are the hashes a mirrored file is checked against
	res, err := source.DoOfficial(client, req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s of %s: %s", sumsFile, gh.TagName, res.Status)
	}
	// "<sha256>  <name>", as sha256sum writes it
	sums := map[string]string{}
	body, err := readLimited(res.Body)
	if err != nil {
		return nil, fmt.Errorf("%s of %s: %w", sumsFile, gh.TagName, err)
	}
	for _, line := range strings.Split(body, "\n") {
		hash, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || len(hash) != 64 {
			continue
		}
		sums[strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "*"))] = hash
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("%s of %s lists no checksum", sumsFile, gh.TagName)
	}
	return sums, nil
}

// readLimited reads a small file the updater parses, bounded so an answer
// from somewhere unexpected can't be read into memory without end.
// SHA256SUMS is about 16 lines.
func readLimited(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	return string(b), err
}

// forkNotes is the notes of every release of Repo() after after up to and
// including upto, newest first, in lang.
func forkNotes(ctx context.Context, after, upto, lang string) ([]Note, error) {
	rels, err := forkReleases(ctx)
	if err != nil {
		return nil, err
	}
	notes := make([]Note, 0, len(rels))
	for _, r := range rels {
		notes = append(notes, Note{
			Version: strings.TrimPrefix(r.TagName, "v"),
			Notes:   InLang(r.Body, lang),
			URL:     r.HTMLURL,
		})
	}
	return Between(notes, after, upto), nil
}
