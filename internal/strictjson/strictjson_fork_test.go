package strictjson

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStrictSyntax(t *testing.T) {
	cases := []struct {
		name, input string
		valid       bool
	}{
		{"object", `{"zero":0,"empty":[],"nested":{"a":true}}`, true},
		{"number preserved", `18446744073709551615`, true},
		{"null is syntax not shape", `null`, true},
		{"case aliases are shape policy", `{"version":1,"Version":2}`, true},
		{"surrogate parity", `"\ud800"`, true},
		{"duplicate", `{"a":1,"a":2}`, false},
		{"escaped duplicate", `{"a":1,"\u0061":2}`, false},
		{"nested duplicate", `[{"x":{"a":1,"a":2}}]`, false},
		{"empty", ``, false},
		{"trailing", `{} null`, false},
		{"malformed", `{"a":`, false},
		{"extra comma", `[1,]`, false},
		{"bad close", `{"a":]`, false},
		{"invalid raw UTF8", "\"" + string([]byte{0xff}) + "\"", false},
		{"depth 64", strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64), true},
		{"depth 65", strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), false},
		{"empty at depth 64", strings.Repeat("[", 65) + strings.Repeat("]", 65), true},
		{"empty at depth 65", strings.Repeat("[", 66) + strings.Repeat("]", 66), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate([]byte(c.input))
			if (err == nil) != c.valid || err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("valid=%v err=%v", c.valid, err)
			}
			if err != nil && err.Error() != "invalid strict JSON" {
				t.Fatal("input leaked in syntax error")
			}
		})
	}
}

type strictWire struct {
	Version  uint64    `json:"version"`
	At       time.Time `json:"at"`
	Values   []int     `json:"values"`
	Optional *int      `json:"optional,omitempty"`
}

func TestStrictShape(t *testing.T) {
	good := `{"version":1,"at":"2026-11-03T12:00:00Z","values":[]}`
	cases := []struct {
		name, input string
		valid       bool
	}{
		{"required zeros and empty", good, true},
		{"optional zero", strings.TrimSuffix(good, "}") + `,"optional":0}`, true},
		{"unknown", strings.TrimSuffix(good, "}") + `,"extra":0}`, false},
		{"alias", strings.Replace(good, `"version"`, `"Version"`, 1), false},
		{"missing required", strings.Replace(good, `"version":1,`, ``, 1), false},
		{"null object", `null`, false},
		{"null array", strings.Replace(good, `[]`, `null`, 1), false},
		{"null element", strings.Replace(good, `[]`, `[null]`, 1), false},
		{"null time", strings.Replace(good, `"2026-11-03T12:00:00Z"`, `null`, 1), false},
		{"invalid time", strings.Replace(good, `2026-11-03T12:00:00Z`, `no-time`, 1), false},
		{"null optional", strings.TrimSuffix(good, "}") + `,"optional":null}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate([]byte(c.input)); err != nil {
				t.Fatal(err)
			}
			if got := Shape([]byte(c.input), reflect.TypeFor[strictWire]()); got != c.valid {
				t.Fatalf("shape=%v want=%v", got, c.valid)
			}
			if Shape([]byte(c.input), reflect.TypeFor[*strictWire]()) != c.valid {
				t.Fatal("pointer shape differs")
			}
		})
	}
	// Shape intentionally leaves primitive ranges/types to the owner's decode.
	for _, bad := range []string{`-1`, `18446744073709551616`, `"one"`} {
		b := []byte(strings.Replace(good, `"version":1`, `"version":`+bad, 1))
		var v strictWire
		if !Shape(b, reflect.TypeFor[strictWire]()) || json.Unmarshal(b, &v) == nil {
			t.Fatal("primitive decode boundary changed")
		}
	}
}

func TestStrictConcurrentImmutableReads(t *testing.T) {
	b := []byte(`{"version":1,"at":"2026-11-03T12:00:00Z","values":[]}`)
	before := string(b)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 25 {
				if Validate(b) != nil || !Shape(b, reflect.TypeFor[strictWire]()) {
					t.Error("concurrent validation failed")
				}
			}
		})
	}
	wg.Wait()
	if string(b) != before {
		t.Fatal("validation mutated input")
	}
}
