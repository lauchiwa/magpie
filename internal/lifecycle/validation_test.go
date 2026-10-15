package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRecoverRejectsOutOfOrderImages(t *testing.T) {
	home(t)
	defer mustLock(t)()
	p := fixture(t)
	p.stage = func(s string) error {
		if s == "journal:write" {
			return errors.New("stop")
		}
		return nil
	}
	if err := Commit(p); err == nil {
		t.Fatal("not paused")
	}
	put(t, IdentitiesFile, afterIdentity)
	put(t, IdentitiesFile+".bak", beforeIdentity)
	if err := Recover(); !errors.Is(err, ErrUnknown) {
		t.Fatalf("out-of-order prefix accepted: %v", err)
	}
	if !bytes.Equal(get(t, ProvidersFile), beforeProvider) {
		t.Fatal("replayed before rejecting bad order")
	}
}
func TestUnknownMetaVersionIsNotBootstrap(t *testing.T) {
	home(t)
	defer mustLock(t)()
	put(t, ProvidersFile, beforeProvider)
	put(t, MetaFile, []byte(`{"version":2}`))
	p, err := Prepare(MetaFile, Present(encoded(t, meta(1, beforeProvider))), nil)
	if err == nil {
		err = Commit(Plan{Participants: []Participant{p}})
	}
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("future metadata overwritten: %v", err)
	}
	if !bytes.Equal(get(t, MetaFile), []byte(`{"version":2}`)) {
		t.Fatal("future metadata lost")
	}
}
func TestCommitKeepsExternallyReplacedJournal(t *testing.T) {
	home(t)
	defer mustLock(t)()
	p := fixture(t)
	p.stage = func(s string) error {
		if s == "journal:remove:before" {
			put(t, "lifecycle-journal.json", []byte("external journal"))
		}
		return nil
	}
	if err := Commit(p); !errors.Is(err, ErrUnknown) {
		t.Fatalf("replacement journal deleted: %v", err)
	}
	if !bytes.Equal(get(t, "lifecycle-journal.json"), []byte("external journal")) {
		t.Fatal("replacement journal lost")
	}
}

func TestMissingFieldsAreUnknownNotDefaults(t *testing.T) {
	home(t)
	p := fixture(t)
	b, err := EncodeJournal(Journal{Version: Version, ID: strings.Repeat("c", 32), Participants: p.Participants})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"exists", "bytes", "sha256", "valid"} {
		t.Run(field, func(t *testing.T) {
			var j map[string]any
			if err := json.Unmarshal(b, &j); err != nil {
				t.Fatal(err)
			}
			part := j["participants"].([]any)[0].(map[string]any)
			if field == "valid" {
				delete(part["backup"].(map[string]any), field)
			} else {
				delete(part["before"].(map[string]any), field)
			}
			raw, _ := json.Marshal(j)
			if _, err := DecodeJournal(raw); !errors.Is(err, ErrUnknown) {
				t.Fatalf("missing %s accepted: %v", field, err)
			}
		})
	}
	m := meta(1, beforeProvider)
	m.Retired = []Retired{{Instance: strings.Repeat("d", 32), Gen: 1, Bound: false}}
	b = encoded(t, m)
	b = bytes.Replace(b, []byte(`"bound": false,`), nil, 1)
	if _, err := DecodeMeta(b); !errors.Is(err, ErrUnknown) {
		t.Fatalf("missing bound became unbound: %v", err)
	}
}

func TestNewIDsAndImageIsolation(t *testing.T) {
	a, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !hexSize(a, 16) || !hexSize(b, 16) {
		t.Fatal("ids not independent 128-bit values")
	}
	data := []byte(`{}`)
	image := Present(data)
	data[0] = 'x'
	if !bytes.Equal(image.Bytes, []byte(`{}`)) || !image.valid() {
		t.Fatal("image aliased caller bytes")
	}
}
