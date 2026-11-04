// Package strictjson checks unambiguous JSON syntax and explicit wire shapes.
// It owns no schema, persistence, domain validation or input-size policy.
package strictjson

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

type syntaxError string

func (e syntaxError) Error() string { return string(e) }

// ErrInvalid never includes the rejected input.
const ErrInvalid syntaxError = "invalid strict JSON"

// Validate checks raw UTF-8, duplicate members, syntax, nesting and trailing
// values, in that order. It does not inspect field names or schema versions.
// Values at depth 64 are allowed (the root is depth zero). Like encoding/json,
// it accepts escaped unpaired surrogates; owners may validate decoded strings.
func Validate(b []byte) error {
	if !utf8.Valid(b) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err := members(decoder, 0); err != nil {
		return ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func members(d *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrInvalid
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
			if err := members(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := members(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrInvalid
	}
	end, err := d.Token()
	if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return ErrInvalid
	}
	return nil
}

// Shape requires canonical field spelling and presence, including explicit zeroes.
// Optional pointer fields may be absent, but JSON null is not a stored value.
// Call Validate first; the final typed decode must check primitive types/ranges.
// Wire structs must use explicit JSON tags and have no embedded/custom fields
// (time.Time is supported). Schema/version and size policies belong to callers.
func Shape(b []byte, typ reflect.Type) bool {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return false
	}
	if typ.Kind() == reflect.Pointer {
		return Shape(b, typ.Elem())
	}
	if typ == reflect.TypeFor[time.Time]() {
		var value time.Time
		return json.Unmarshal(b, &value) == nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(b, &object) != nil || object == nil {
			return false
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			raw, ok := object[name]
			if !ok {
				if options == "omitempty" {
					continue
				}
				return false
			}
			if !Shape(raw, field.Type) {
				return false
			}
			delete(object, name)
		}
		return len(object) == 0
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(b, &values) != nil {
			return false
		}
		for _, value := range values {
			if !Shape(value, typ.Elem()) {
				return false
			}
		}
	}
	return true // The final typed decode checks primitive types and ranges.
}
