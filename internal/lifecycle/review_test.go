package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestReviewReadImageWaitsOutReplace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows replacement semantics")
	}
	home(t)
	put(t, IdentitiesFile, beforeIdentity)
	old := lstat
	t.Cleanup(func() { lstat = old })
	for _, cause := range []error{fs.ErrNotExist, fs.ErrPermission} {
		calls := 0
		lstat = func(path string) (fs.FileInfo, error) {
			calls++
			if calls == 1 {
				return nil, &fs.PathError{Op: "lstat", Path: path, Err: cause}
			}
			return os.Lstat(path)
		}
		image, err := ReadImage(IdentitiesFile)
		if err != nil || !image.Exists || !bytes.Equal(image.Bytes, beforeIdentity) {
			t.Errorf("replacement taken for absent/unknown (%v): exists=%v err=%v", cause, image.Exists, err)
		}
	}
}

func TestReviewReadImageSecondReplaceIsUnknown(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows replacement semantics")
	}
	home(t)
	put(t, IdentitiesFile, beforeIdentity)
	old := lstat
	t.Cleanup(func() { lstat = old })
	lstat = func(path string) (fs.FileInfo, error) {
		return nil, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
	}
	if _, err := ReadImage(IdentitiesFile); !errors.Is(err, ErrUnknown) {
		t.Fatalf("file observed by Stat was treated as absent: %v", err)
	}
}

func TestReviewJournalRejectsNullPolicyFields(t *testing.T) {
	home(t)
	p := fixture(t)
	b, err := EncodeJournal(Journal{Version: Version, ID: strings.Repeat("c", 32), Participants: p.Participants})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"exists", "sha256", "valid"} {
		t.Run(field, func(t *testing.T) {
			var j map[string]any
			if err := json.Unmarshal(b, &j); err != nil {
				t.Fatal(err)
			}
			part := j["participants"].([]any)[0].(map[string]any)
			if field == "valid" {
				part["backup"].(map[string]any)[field] = nil
			} else {
				part["before"].(map[string]any)[field] = nil
			}
			raw, _ := json.Marshal(j)
			if _, err := DecodeJournal(raw); !errors.Is(err, ErrUnknown) {
				t.Fatalf("null %s became a default: %v", field, err)
			}
		})
	}
	m := meta(1, beforeProvider)
	m.Retired = []Retired{{Instance: strings.Repeat("d", 32), Gen: 1}}
	b = bytes.Replace(encoded(t, m), []byte(`"bound": false`), []byte(`"bound": null`), 1)
	if _, err := DecodeMeta(b); !errors.Is(err, ErrUnknown) {
		t.Fatalf("null bound became unbound: %v", err)
	}
}
