package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/gwbudget"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/usage"
)

func TestScopedRetrievalReservesBeforeSendAndSettles(t *testing.T) {
	for _, response := range []string{`{"usage":{"prompt_tokens":5}}`, `{"usage":{"prompt_tokens":0}}`, `{"data":[]}`} {
		t.Run(response, func(t *testing.T) {
			fresh(t)
			budget.Forget()
			t.Cleanup(budget.Forget)
			keys, secrets := newCaller(t, "Synthetic scoped key")
			limit := &access.Limit{Period: "day", Tokens: 5, Models: []string{"library/embedding"}}
			setLimit(t, keys[0].ID, limit)
			key := keys[0]
			key.Limit = limit
			var sends atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				st := budget.All(key, time.Now())[0]
				if st.InFlight != 1 || st.Reserved <= 0 {
					t.Errorf("upstream entered without reservation: %+v", st)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, response)
			}))
			t.Cleanup(up.Close)
			if err := provider.Save(provider.Provider{ID: "library", Name: "Synthetic library", Chat: up.URL + "/v1", Key: "synthetic-test-key", Models: []string{"embedding"}}); err != nil {
				t.Fatal(err)
			}
			h := New().Handler()
			call := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(`{"model":"library/embedding","input":"hello"}`))
				r.Header.Set("Authorization", "Bearer "+secrets[0])
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := call(); w.Code != 200 {
				t.Fatalf("first call: %d %s", w.Code, w.Body.String())
			}
			if sends.Load() != 1 {
				t.Fatalf("first send count %d", sends.Load())
			}
			for _, restart := range []bool{false, true} {
				if restart {
					budget.Forget()
				}
				st := budget.All(key, time.Now())[0]
				if st.InFlight != 0 {
					t.Fatalf("hold leaked: %+v", st)
				}
				switch response {
				case `{"usage":{"prompt_tokens":5}}`:
					if st.Unavailable || st.Tokens != 5 || st.Pending != 0 {
						t.Fatalf("complete usage: %+v", st)
					}
					if w := call(); w.Code != 429 {
						t.Fatalf("spent admitted: %d %s", w.Code, w.Body.String())
					}
				case `{"usage":{"prompt_tokens":0}}`:
					if st.Unavailable || st.Tokens != 0 || st.Pending != 0 {
						t.Fatalf("reported zero: %+v", st)
					}
				default:
					if !st.Unavailable || st.Pending != 1 {
						t.Fatalf("missing usage became zero: %+v", st)
					}
					if w := call(); w.Code != 503 {
						t.Fatalf("unknown admitted: %d %s", w.Code, w.Body.String())
					}
				}
			}
			if sends.Load() != 1 {
				t.Fatalf("refused request reached upstream: %d", sends.Load())
			}
		})
	}
}

func TestRetrievedUsagePresenceIsNotTokenMagnitude(t *testing.T) {
	for _, tt := range []struct {
		body  string
		known bool
	}{
		{`{"usage":{"prompt_tokens":0}}`, true}, {`{"usage":{"input_tokens":7}}`, true},
		{`{"meta":{"billed_units":{"input_tokens":0}}}`, true},
		{`{}`, false}, {`{"usage":null}`, false}, {`{"usage":{"prompt_tokens":null}}`, false},
		{`{"usage":{"prompt_tokens":-1}}`, false}, {`{"usage":{"prompt_tokens":"0"}}`, false},
		{`{"usage":{"prompt_tokens":1.5}}`, false}, {`{"usage":{"prompt_tokens":3}} trailing`, false},
	} {
		if got := retrievedUsageKnown([]byte(tt.body)); got != tt.known {
			t.Errorf("%s: known=%v", tt.body, got)
		}
	}
}

type scopedTransportLease struct{}

func (scopedTransportLease) Hold() string    { return "synthetic" }
func (scopedTransportLease) MarkSent() error { return nil }
func (scopedTransportLease) Record(usage.Record) (gwbudget.AttemptReceipt, error) {
	return nil, errors.New("not used")
}
func (scopedTransportLease) Diagnostic(usage.Record) error        { return nil }
func (scopedTransportLease) Finish(gwbudget.AttemptOutcome) error { return nil }

type scopedTransportProbe func(*http.Request) (*http.Response, error)

func (f scopedTransportProbe) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestScopedTransportDoesNotCreateHiddenReplay(t *testing.T) {
	for _, realKey := range []bool{false, true} {
		r := httptest.NewRequest("POST", "https://synthetic.invalid/inference", strings.NewReader("body"))
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("body")), nil }
		if realKey {
			r.Header.Set("Idempotency-Key", "synthetic-id")
		}
		r = r.WithContext(context.WithValue(r.Context(), scopedSendKey{}, &budgetSend{lease: scopedTransportLease{}}))
		calls := 0
		_, err := (replayable{scopedTransportProbe(func(got *http.Request) (*http.Response, error) {
			calls++
			if got.GetBody != nil {
				t.Error("scoped request replayable")
			}
			if _, set := got.Header["X-Idempotency-Key"]; set {
				t.Error("synthetic retry marker injected")
			}
			if realKey && got.Header.Get("Idempotency-Key") != "synthetic-id" {
				t.Error("real header removed")
			}
			return nil, errors.New("synthetic transport failure")
		})}).RoundTrip(r)
		if err == nil || calls != 1 {
			t.Fatalf("scoped retry: calls=%d err=%v", calls, err)
		}
		if r.GetBody == nil {
			t.Fatal("transport mutated caller request")
		}
	}
}
