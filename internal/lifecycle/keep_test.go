package lifecycle

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/lastgood"
	"github.com/yetone/magpie/internal/steady"
)

// badStamp hides the second a .bad-<time> copy was named in, which two
// homes written a moment apart need not share.
var badStamp = regexp.MustCompile(`\.bad-\d{8}-\d{6}(-\d+)?`)

// keepState is every file in the config directory except the protocol's
// own lock, with its bytes, its mode where the platform has one, and
// whether its mtime is the fixture's (an unchanged .bak was not rewritten).
func keepState(t *testing.T, old time.Time) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(appdir.Config())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "lifecycle.lock" {
			continue
		}
		p := filepath.Join(appdir.Config(), e.Name())
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := Digest(b)
		if runtime.GOOS != "windows" {
			s += " " + fi.Mode().Perm().String()
		}
		if fi.ModTime().Equal(old) {
			s += " untouched"
		}
		out[badStamp.ReplaceAllString(e.Name(), ".bad-T")] = s
	}
	return out
}

// TestCommitKeepMatchesLastgoodKeep runs the owner's old write (lastgood.Keep
// then replace) and a one-participant Commit over the same fixture on the
// non-link path, and requires the same directory afterwards: a valid primary
// becomes .bak, an identical .bak is not rewritten, an invalid primary is
// kept aside once (deduplicated) with .bak left as it is, an absent primary
// keeps nothing, and every file written is 0600.
func TestCommitKeepMatchesLastgoodKeep(t *testing.T) {
	old := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	valid := []byte(`{"providers":[{"id":"a"}]}`)
	other := []byte(`{"providers":[]}`)
	zeroed := []byte{0, 0, 0, 0}
	after := []byte(`{"providers":[{"id":"b"}]}`)
	type fixture struct {
		primary, bak, bad []byte // nil: absent
	}
	cases := map[string]fixture{
		"absent":              {},
		"absent-with-bak":     {bak: other},
		"valid-no-bak":        {primary: valid},
		"valid-same-bak":      {primary: valid, bak: valid},
		"valid-other-bak":     {primary: valid, bak: other},
		"invalid-keeps-bak":   {primary: zeroed, bak: other},
		"invalid-no-bak":      {primary: zeroed},
		"invalid-kept-before": {primary: zeroed, bak: other, bad: zeroed},
		"empty-is-invalid":    {primary: []byte{}, bak: other},
	}
	for _, name := range []string{ProvidersFile, IdentitiesFile} {
		for cname, c := range cases {
			t.Run(name+"/"+cname, func(t *testing.T) {
				setup := func() string {
					home(t)
					path := filepath.Join(appdir.Config(), name)
					for _, f := range []struct {
						path string
						b    []byte
					}{{path, c.primary}, {lastgood.Bak(path), c.bak}, {path + ".bad-20000101-000000", c.bad}} {
						if f.b == nil {
							continue
						}
						if err := steady.WriteFile(f.path, f.b, 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.Chtimes(f.path, old, old); err != nil {
							t.Fatal(err)
						}
					}
					return path
				}
				// The owner's write as it is today.
				path := setup()
				if err := lastgood.Keep(path, lastgood.JSON, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := steady.WriteFile(path, after, 0o600); err != nil {
					t.Fatal(err)
				}
				want := keepState(t, old)

				// The same write through the lifecycle protocol.
				setup()
				unlock := mustLock(t)
				p, err := Prepare(name, Present(after), lastgood.JSON)
				if err == nil {
					err = Commit(Plan{Participants: []Participant{p}})
				}
				unlock()
				if err != nil {
					t.Fatal(err)
				}
				got := keepState(t, old)
				if len(got) != len(want) {
					t.Fatalf("files differ:\nlastgood: %v\ncommit:   %v", want, got)
				}
				for k, v := range want {
					if got[k] != v {
						t.Fatalf("%s differs: lastgood %q, commit %q\nlastgood: %v\ncommit:   %v", k, v, got[k], want, got)
					}
				}
				if !bytes.Equal(get(t, name), after) {
					t.Fatal("primary not replaced")
				}
			})
		}
	}
}
