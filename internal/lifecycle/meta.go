package lifecycle

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// Provider describes a stored row; identity content belongs to identities.json.
type Provider struct {
	Instance string `json:"instance"`
	Rev      uint64 `json:"rev"`
}

// Retired describes a deleted instance, not a reusable provider ID.
type Retired struct {
	Instance string `json:"instance"`
	Bound    bool   `json:"bound"`
	Gen      uint64 `json:"gen"`
}

// Meta is separate from the providers.json disk schema.
type Meta struct {
	Version   int                 `json:"version"`
	Epoch     string              `json:"epoch"`
	Gen       uint64              `json:"gen"`
	Digest    string              `json:"digest"`
	Providers map[string]Provider `json:"providers"`
	Retired   []Retired           `json:"retired,omitempty"`
}

// Retire retains the newest 256 tombstones, ordered by generation. Validation
// still rejects duplicates; truncation never turns an old object into unbound.
func (m *Meta) Retire(r Retired) {
	m.Retired = append(m.Retired, r)
	slices.SortStableFunc(m.Retired, func(a, b Retired) int {
		if a.Gen < b.Gen {
			return -1
		}
		if a.Gen > b.Gen {
			return 1
		}
		return 0
	})
	if len(m.Retired) > RetiredLimit {
		m.Retired = slices.Clone(m.Retired[len(m.Retired)-RetiredLimit:])
	}
}
func hexSize(s string, n int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == n && hex.EncodeToString(b) == s
}
func (m Meta) valid() bool {
	if m.Version != Version || !hexSize(m.Epoch, 16) || m.Gen == 0 || !hexSize(m.Digest, 32) || m.Providers == nil || len(m.Retired) > RetiredLimit {
		return false
	}
	seen := make(map[string]bool)
	for id, p := range m.Providers {
		if id == "" || !hexSize(p.Instance, 16) || p.Rev == 0 || seen[p.Instance] {
			return false
		}
		seen[p.Instance] = true
	}
	var gen uint64
	for _, r := range m.Retired {
		if !hexSize(r.Instance, 16) || seen[r.Instance] || r.Gen == 0 || r.Gen > m.Gen || r.Gen < gen {
			return false
		}
		seen[r.Instance] = true
		gen = r.Gen
	}
	return true
}

// EncodeMeta rejects invalid metadata rather than normalizing away evidence.
func EncodeMeta(m Meta) ([]byte, error) {
	if !m.valid() {
		return nil, fmt.Errorf("%w: invalid metadata", ErrUnknown)
	}
	return json.MarshalIndent(m, "", "  ")
}

// DecodeMeta does not consult lifecycle.json.bak or accept future versions.
func DecodeMeta(b []byte) (Meta, error) {
	var m Meta
	if err := decode(b, &m); err != nil || !m.valid() || !metaFields(b) {
		return Meta{}, fmt.Errorf("%w: invalid metadata", ErrUnknown)
	}
	return m, nil
}

// ReadMeta returns nil only for an absent primary, never for an unreadable
// one. Unusable metadata is a *MetaError naming its class.
func ReadMeta() (*Meta, error) {
	state, m, err := classify(ReadImage(MetaFile))
	switch state {
	case MetaAbsent:
		return nil, nil
	case MetaValid:
		return &m, nil
	}
	return nil, err
}

// classify sorts lifecycle.json for its owner without loosening the codec:
// only what DecodeMeta accepts is valid. A unique, integer "version" above
// Version is unsupported (a newer magpie's); anything else that fails is
// corrupt. A read error is io: unknown, never absent.
func classify(image Image, readErr error) (MetaState, Meta, error) {
	if readErr != nil {
		return MetaIO, Meta{}, &MetaError{State: MetaIO, Path: Path(), Err: readErr}
	}
	if !image.Exists {
		return MetaAbsent, Meta{}, nil
	}
	m, err := DecodeMeta(image.Bytes)
	if err == nil {
		return MetaValid, m, nil
	}
	var format struct {
		Version int `json:"version"`
	}
	if uniqueValue(json.NewDecoder(bytes.NewReader(image.Bytes))) == nil && json.Unmarshal(image.Bytes, &format) == nil && format.Version > Version {
		return MetaUnsupported, Meta{}, &MetaError{State: MetaUnsupported, Path: Path(), Err: fmt.Errorf("version %d", format.Version)}
	}
	return MetaCorrupt, Meta{}, &MetaError{State: MetaCorrupt, Path: Path(), Err: err}
}

func required(b []byte, keys ...string) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil || obj == nil {
		return nil, false
	}
	for _, key := range keys {
		if _, ok := obj[key]; !ok {
			return nil, false
		}
	}
	return obj, true
}

func metaFields(b []byte) bool {
	obj, ok := required(b, "version", "epoch", "gen", "digest", "providers")
	if !ok {
		return false
	}
	var ps map[string]json.RawMessage
	if json.Unmarshal(obj["providers"], &ps) != nil {
		return false
	}
	for _, p := range ps {
		if _, ok := required(p, "instance", "rev"); !ok {
			return false
		}
	}
	var retired []json.RawMessage
	if b, ok := obj["retired"]; ok {
		if json.Unmarshal(b, &retired) != nil {
			return false
		}
		for _, r := range retired {
			fields, ok := required(r, "instance", "bound", "gen")
			if !ok || (string(fields["bound"]) != "true" && string(fields["bound"]) != "false") {
				return false
			}
		}
	}
	return true
}

// decode rejects duplicate keys, unknown fields and trailing values. The
// fixed journal must not mean different things to two decoders on recovery.
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	if err := uniqueValue(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrUnknown
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func uniqueValue(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return ErrUnknown
			}
			seen[s] = true
			if err := uniqueValue(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueValue(d); err != nil {
				return err
			}
		}
	default:
		return ErrUnknown
	}
	_, err = d.Token()
	return err
}
