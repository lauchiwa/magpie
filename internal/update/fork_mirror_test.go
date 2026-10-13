package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// The release list and its SHA256SUMS say what an install must hash to, so
// they come from GitHub alone. With mirror fallback on (MAGPIE_MIRRORS) a
// failing GitHub must not hand them to the mirror: one that answered could
// name a file of its own and the hash it is checked against, and an ad-hoc
// signed app has no team for sameSigner to tell the two apart.

const forgedMirror = "https://mirror.invalid"

// mirrorTrap answers as GitHub (api.github.com, github.com) and as the
// fallback mirror, by host. GitHub answers what github says for a path;
// the mirror answers a forged release and its hashes to everything, and
// records that it was asked.
type mirrorTrap struct {
	mu     sync.Mutex
	github func(path string) (int, string)
	mirror []string
}

func (m *mirrorTrap) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(code int, body string) *http.Response {
		return &http.Response{
			StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)),
			Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r,
		}
	}
	if r.URL.Host == "mirror.invalid" {
		m.mu.Lock()
		m.mirror = append(m.mirror, r.URL.String())
		m.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/"+sumsFile) {
			forged := sha256.Sum256([]byte("forged"))
			return reply(200, hex.EncodeToString(forged[:])+"  "+BinaryAsset()+"\n"), nil
		}
		return reply(200, forgedList()), nil
	}
	code, body := m.github(r.URL.Path)
	return reply(code, body), nil
}

// forgedList is a release list naming a version far past any real one.
func forgedList() string {
	b, _ := json.Marshal([]ghRelease{realRelease("v9.9.9-rc.0")})
	return string(b)
}

// realRelease is a release as GitHub lists it, its files on github.com.
func realRelease(tag string) ghRelease {
	r := rel(tag, "notes")
	for i := range r.Assets {
		r.Assets[i].URL = "https://github.com/fork/magpie/releases/download/" + tag + "/" + r.Assets[i].Name
	}
	return r
}

func trapMirror(t *testing.T, github func(path string) (int, string)) *mirrorTrap {
	t.Helper()
	m := &mirrorTrap{github: github}
	was := client
	t.Cleanup(func() { client = was })
	client = &http.Client{Transport: m}
	t.Setenv("MAGPIE_MIRRORS", "on")
	t.Setenv("MAGPIE_GITHUB_MIRROR", forgedMirror)
	t.Setenv("MAGPIE_RELEASE_API", "")
	t.Setenv("MAGPIE_RELEASE_REPO", "fork/magpie")
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	t.Setenv("MAGPIE_NOTES_FEED", "")
	return m
}

func (m *mirrorTrap) asked() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.mirror...)
}

func TestTheReleaseListIsNeverAskedOfAMirror(t *testing.T) {
	m := trapMirror(t, func(string) (int, string) { return http.StatusServiceUnavailable, "down" })
	got, err := LatestIn(context.Background(), "")
	if err == nil {
		t.Fatalf("a failing GitHub gave release %s, want an error", got.Version)
	}
	if asked := m.asked(); len(asked) > 0 {
		t.Fatalf("the release list was asked of the mirror: %v", asked)
	}
}

func TestChecksumsAreNeverAskedOfAMirror(t *testing.T) {
	list, _ := json.Marshal([]ghRelease{realRelease("v0.1.1186-rc.1")})
	m := trapMirror(t, func(path string) (int, string) {
		if path == "/repos/fork/magpie/releases" {
			return 200, string(list)
		}
		return http.StatusServiceUnavailable, "down"
	})
	got, err := LatestIn(context.Background(), "")
	if err == nil {
		forged := sha256.Sum256([]byte("forged"))
		if a := got.Assets[BinaryAsset()]; a.SHA256 == hex.EncodeToString(forged[:]) {
			t.Fatalf("%s is checked against the mirror's hash", BinaryAsset())
		}
		t.Fatalf("a release whose SHA256SUMS failed was offered: %+v", got)
	}
	if asked := m.asked(); len(asked) > 0 {
		t.Fatalf("SHA256SUMS was asked of the mirror: %v", asked)
	}
}

// The files themselves still come through a mirror, which is what one is
// for: the hash from GitHub is what stops a mirror's changed byte, and the
// refusal names where that hash came from.
func TestAFileStillComesThroughTheMirror(t *testing.T) {
	sum := sha256.Sum256([]byte("forged"))
	m := trapMirror(t, func(string) (int, string) { return http.StatusServiceUnavailable, "down" })
	path := t.TempDir() + "/magpie"
	a := Asset{URL: "https://github.com/fork/magpie/releases/download/v0.1.1186-rc.1/" + BinaryAsset(), SHA256: hex.EncodeToString(sum[:])}
	ctx := WithMirror(context.Background(), forgedMirror+"/")
	// the mirror answers the forged list's bytes, not "forged": refused
	err := fetch(ctx, a, path)
	if err == nil {
		t.Fatal("a file the mirror changed was accepted")
	}
	if !strings.Contains(err.Error(), "github.com/fork/magpie") || strings.Contains(err.Error(), Site) {
		t.Errorf("refusal = %q, want it to name the release on github.com/fork/magpie, not %s", err, Site)
	}
	if asked := m.asked(); len(asked) == 0 || !bytes.Contains([]byte(asked[0]), []byte(BinaryAsset())) {
		t.Fatalf("the file was not asked of the mirror: %v", asked)
	}
}
