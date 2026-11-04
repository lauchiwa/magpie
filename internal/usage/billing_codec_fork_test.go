package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

func billingWireJSON(t *testing.T, w billingEnvelopeWire) []byte {
	t.Helper()
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestBillingCodecRoundTrip(t *testing.T) {
	in, c := billingFixture()
	in.Spend.Price.Peak = "weekdays"
	in.Spend.Rate = .3
	in.Reference.Source = "maker_public"
	in.Reference.MappingRef = "unambiguous-map-1"
	c.Reported = billingReport(in, .2)
	in.Quota = billingQuotaFixture(in, true)
	e := billingMustSeal(t, in, c)
	b, err := EncodeBilling(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBilling(b)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodeBilling(decoded)
	if err != nil || !bytes.Equal(b, again) {
		t.Fatal("noncanonical re-encode", err)
	}
	if !reflect.DeepEqual(e.Amounts(), decoded.Amounts()) {
		t.Fatal("amount/provenance lost")
	}
	if bytes.Contains(b, []byte("CacheWrite")) || bytes.Contains(b, []byte("Windows")) {
		t.Fatal("untagged business fields leaked")
	}
	exposed := decoded.Capture()
	exposed.Spend.Price.Tiers[0].Input = 99
	exposed.Quota.Snapshot.Windows[0].Capacity.Remaining = 0
	copyAfter, _ := EncodeBilling(decoded)
	if !bytes.Equal(copyAfter, b) {
		t.Fatal("decoded getter aliases retained data")
	}
	neg, c := billingFixture()
	neg.Spend.Price.CacheRead = math.Copysign(0, -1)
	neg.Spend.Price.Tiers[0].CacheRead = math.Copysign(0, -1)
	normalized := billingMustSeal(t, neg, c)
	if math.Signbit(normalized.Capture().Spend.Price.CacheRead) || math.Signbit(normalized.Capture().Spend.Price.Tiers[0].CacheRead) {
		t.Fatal("negative zero not normalized")
	}
}

func TestBillingCodecStrictOrder(t *testing.T) {
	in, c := billingFixture()
	e := billingMustSeal(t, in, c)
	valid, _ := EncodeBilling(e)
	cases := []struct {
		name string
		b    []byte
		err  error
	}{
		{"empty", nil, ErrBillingCorrupt}, {"array", []byte("[]"), ErrBillingCorrupt}, {"null", []byte("null"), ErrBillingCorrupt},
		{"future-fields", []byte(`{"version":2,"future":null}`), ErrBillingUnsupported},
		{"future-duplicate", []byte(`{"version":2,"future":1,"future":2}`), ErrBillingCorrupt},
		{"future-trailing", []byte(`{"version":2} {}`), ErrBillingCorrupt},
		{"version-alias", []byte(`{"Version":2}`), ErrBillingCorrupt},
		{"version-null", []byte(`{"version":null}`), ErrBillingCorrupt},
		{"version-string", []byte(`{"version":"2"}`), ErrBillingCorrupt},
		{"version-fraction", []byte(`{"version":1.0}`), ErrBillingCorrupt},
		{"extra", bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"secret_field":"do-not-echo"`), 1), ErrBillingCorrupt},
		{"duplicate", bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), ErrBillingCorrupt},
		{"escaped-duplicate", bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"ver\u0073ion":1`), 1), ErrBillingCorrupt},
		{"case-alias", bytes.Replace(valid, []byte(`"algorithm":1`), []byte(`"Algorithm":1`), 1), ErrBillingCorrupt},
		{"null-optional", bytes.Replace(valid, []byte(`"credits":[]`), []byte(`"credits":[],"reported":null`), 1), ErrBillingCorrupt},
		{"null-array", bytes.Replace(valid, []byte(`"credits":[]`), []byte(`"credits":null`), 1), ErrBillingCorrupt},
		{"wrong-primitive", bytes.Replace(valid, []byte(`"known":true`), []byte(`"known":"true"`), 1), ErrBillingCorrupt},
		{"malformed-utf8", append([]byte(`{"version":2,"future":"`), 0xff, '"', '}'), ErrBillingCorrupt},
		{"large-first", bytes.Repeat([]byte{0xff}, BillingMaxBytes+1), ErrBillingTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := DecodeBilling(tc.b)
			if !errors.Is(err, tc.err) || e.data != nil {
				t.Fatalf("got %v (usable=%v), want %v", err, e.data != nil, tc.err)
			}
			if strings.Contains(err.Error(), "do-not-echo") {
				t.Fatal("error echoed payload")
			}
		})
	}
	// The root object is depth zero. A leaf below 63 nested arrays is at 64.
	for _, n := range []int{63, 64} {
		b := []byte(`{"version":2,"future":` + strings.Repeat("[", n) + "0" + strings.Repeat("]", n) + "}")
		_, err := DecodeBilling(b)
		want := error(ErrBillingUnsupported)
		if n == 64 {
			want = ErrBillingCorrupt
		}
		if !errors.Is(err, want) {
			t.Fatalf("depth %d = %v", n, err)
		}
	}
	if _, err := DecodeBilling(append(valid, bytes.Repeat([]byte(" "), BillingMaxBytes-len(valid))...)); err != nil {
		t.Fatal("exact size cap rejected", err)
	}
}

func TestBillingCodecEveryRequiredField(t *testing.T) {
	in, c := billingFixture()
	in.Quota = billingQuotaFixture(in, true)
	c.Reported = billingReport(in, .5)
	c.Credits = []BillingCredit{{Evidence: *billingEvidence(in), Kind: "vendor_response", Authority: "vendor", Unit: "points", Amount: 1}}
	b, _ := EncodeBilling(billingMustSeal(t, in, c))
	// Collect required fields from the actual method-free DTO, not a second schema.
	var visit func(reflect.Type, []string)
	visit = func(typ reflect.Type, path []string) {
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.PkgPath() == "time" {
			return
		}
		if typ.Kind() == reflect.Slice {
			visit(typ.Elem(), append(slicesCopy(path), "0"))
			return
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name, opt, _ := strings.Cut(f.Tag.Get("json"), ",")
			p := append(slicesCopy(path), name)
			if opt != "omitempty" && name != "version" {
				for _, remove := range []bool{false, true} {
					var data map[string]any
					if err := json.Unmarshal(b, &data); err != nil {
						t.Fatal(err)
					}
					var node any = data
					present := true
					for _, part := range path {
						if part == "0" {
							a, ok := node.([]any)
							if !ok || len(a) == 0 {
								present = false
								break
							}
							node = a[0]
						} else {
							m, ok := node.(map[string]any)
							if !ok {
								present = false
								break
							}
							node = m[part]
						}
					}
					if !present {
						continue
					}
					object, ok := node.(map[string]any)
					if !ok {
						continue
					}
					if remove {
						delete(object, name)
					} else {
						object[name] = nil
					}
					bad, _ := json.Marshal(data)
					if _, err := DecodeBilling(bad); !errors.Is(err, ErrBillingCorrupt) {
						t.Fatalf("required field %s remove=%v accepted: %v", strings.Join(p, "."), remove, err)
					}
				}
			}
			visit(f.Type, p)
		}
	}
	visit(reflect.TypeFor[billingEnvelopeWire](), nil)
}
func slicesCopy(in []string) []string { return append([]string{}, in...) }

func TestBillingCodecRateChainTamper(t *testing.T) {
	in, c := billingFixture()
	c.Usage.Input = 31
	e := billingMustSeal(t, in, c)
	valid, _ := EncodeBilling(e)
	for name, change := range map[string]func(*billingEnvelopeWire){
		"algorithm": func(w *billingEnvelopeWire) { w.Algorithm++ },
		"rate":      func(w *billingEnvelopeWire) { w.Capture.Spend.Rate *= 2 },
		"raw-base":  func(w *billingEnvelopeWire) { w.Capture.Spend.Original.Input *= 2 },
		"raw-unselected-tier": func(w *billingEnvelopeWire) {
			w.Capture.Spend.Original.Tiers = append(w.Capture.Spend.Original.Tiers, billingTierWire{Above: 1000, Input: 3})
		},
		"effective-base":      func(w *billingEnvelopeWire) { w.Capture.Spend.Effective.Input *= 2 },
		"effective-tier":      func(w *billingEnvelopeWire) { w.Capture.Spend.Effective.Tiers[0].Output *= 2 },
		"effective-peak":      func(w *billingEnvelopeWire) { w.Capture.Spend.Effective.Peak = "weekdays" },
		"factor":              func(w *billingEnvelopeWire) { w.Capture.Spend.PeakFactor = 3 },
		"factor-without-rule": func(w *billingEnvelopeWire) { w.Capture.Spend.PeakFactor = 2 },
		"selected-tier":       func(w *billingEnvelopeWire) { w.Spend.Tier = -1 },
		"selected-hour":       func(w *billingEnvelopeWire) { w.Spend.CacheWrite1h *= 2 },
		"selected-and-amount": func(w *billingEnvelopeWire) {
			w.Spend.Input *= 2
			w.Spend.Amount += float64(w.Completion.Usage.Input) * w.Spend.Input / 2 / 1e6
			billingTamperAmounts(w)
		},
		"effective-selected-and-amount": func(w *billingEnvelopeWire) {
			w.Capture.Spend.Effective.Tiers[0].Input *= 2
			w.Spend.Input *= 2
			w.Spend.Amount += float64(w.Completion.Usage.Input) * w.Spend.Input / 2 / 1e6
			billingTamperAmounts(w)
		},
		"amount":              func(w *billingEnvelopeWire) { w.Amounts.Values[0].Amount *= 2 },
		"unpaired-difference": func(w *billingEnvelopeWire) { w.Amounts.Values[4].Known = 0; w.Amounts.Values[4].Unknown = 1 },
		"identity":            func(w *billingEnvelopeWire) { w.Completion.Binding.Subject.Generation = "recreated" },
		"missing-selected":    func(w *billingEnvelopeWire) { w.Spend = nil },
	} {
		t.Run(name, func(t *testing.T) {
			var w billingEnvelopeWire
			if err := json.Unmarshal(valid, &w); err != nil {
				t.Fatal(err)
			}
			change(&w)
			if e, err := DecodeBilling(billingWireJSON(t, w)); !errors.Is(err, ErrBillingCorrupt) || e.data != nil {
				t.Fatal("contradiction accepted", err)
			}
		})
	}
	// V1 decoding uses the saved factor, not today's calendar. A self-consistent
	// saved off-peak interpretation stays frozen even at a currently peak instant.
	var w billingEnvelopeWire
	_ = json.Unmarshal(valid, &w)
	w.Capture.Spend.Original.Peak = "weekdays"
	if _, err := DecodeBilling(billingWireJSON(t, w)); err != nil {
		t.Fatal("decoder re-evaluated recorded peak rule", err)
	}
}
func billingTamperAmounts(w *billingEnvelopeWire) {
	u, p := w.Completion.Usage, w.Spend
	total := float64(float64(u.Input) * p.Input)
	total = float64(total + float64(float64(u.Output)*p.Output))
	total = float64(total + float64(float64(u.CacheRead)*p.CacheRead))
	total = float64(total + float64(float64(u.CacheWrite-u.CacheWrite1h)*p.CacheWrite))
	total = float64(total + float64(float64(u.CacheWrite1h)*p.CacheWrite1h))
	p.Amount = float64(total / 1e6)
	for i := range w.Amounts.Values {
		v := &w.Amounts.Values[i]
		switch v.Dimension {
		case "estimate", "charge":
			v.Amount = w.Spend.Amount
		case "difference":
			v.Amount = w.Reference.Amount - w.Spend.Amount
		}
	}
}

func TestBillingCodecAcrossProcess(t *testing.T) {
	if file := os.Getenv("MAGPIE_BILLING_CODEC_CHILD"); file != "" {
		pageHome(t)
		if err := provider.Save(provider.Provider{ID: "relay", Key: "test-key", Chat: "http://127.0.0.1:1/v1", Catalog: "openai"}); err != nil {
			t.Fatal(err)
		}
		current := settings.Load()
		current.ModelPrices = map[string]settings.ModelPrice{"relay/maker/model": settings.StatedPrice(catalog.Price{Input: 999, Output: 999, CacheRead: 999, CacheWrite: 999})}
		if err := settings.Save(current); err != nil {
			t.Fatal(err)
		}
		live, known := NewCoster()(Record{Provider: "relay", Model: "maker/model", Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, CacheWrite1h: 10})
		if !known || live == .0001775 {
			t.Fatal("changed current-price fixture was not active")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		e, err := DecodeBilling(b)
		if err != nil {
			t.Fatal(err)
		}
		billingAssertValue(t, e.Amounts(), "estimate", .0001775, true)
		encoded, err := EncodeBilling(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file+".out", encoded, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	in, c := billingFixture()
	b, err := EncodeBilling(billingMustSeal(t, in, c))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "frozen.json")
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBillingCodecAcrossProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "MAGPIE_BILLING_CODEC_CHILD="+file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child decode: %v: %s", err, output)
	}
	got, err := os.ReadFile(file + ".out")
	if err != nil || !bytes.Equal(got, b) {
		t.Fatal("another process/current price changed frozen bytes", err)
	}
}

func TestBillingEncodeSizeLimit(t *testing.T) {
	in, c := billingFixture()
	in.Spend.Price.Tiers = make([]catalog.Tier, 9000)
	for i := range in.Spend.Price.Tiers {
		in.Spend.Price.Tiers[i] = catalog.Tier{Above: i + 1, Input: 2, Output: 4, CacheRead: .5, CacheWrite: 1.25, CacheWrite1h: 2.5}
	}
	e := billingMustSeal(t, in, c)
	if b, err := EncodeBilling(e); !errors.Is(err, ErrBillingTooLarge) || b != nil {
		t.Fatalf("oversize encode returned %d bytes, %v", len(b), err)
	}
}
