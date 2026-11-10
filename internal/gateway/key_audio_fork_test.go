package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func scopedAudioRequest(t *testing.T, path, model, secret string) *http.Request {
	t.Helper()
	var body []byte
	contentType := "application/json"
	if strings.HasSuffix(path, "/speech") {
		body = []byte(`{"model":"` + model + `","input":"synthetic hello","voice":"coral","stream_format":"sse"}`)
	} else {
		body, contentType = audioFormBody(t, "model", model)
	}
	r := httptest.NewRequest("POST", path, bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Authorization", "Bearer "+secret)
	return r
}

func TestScopedAudioAliasesCountInputAndOutput(t *testing.T) {
	for _, path := range []string{"/v1/audio/speech", "/audio/speech", "/v1/audio/transcriptions", "/audio/transcriptions", "/v1/audio/translations", "/audio/translations"} {
		t.Run(path, func(t *testing.T) {
			fresh(t)
			budget.Forget()
			t.Cleanup(budget.Forget)
			now := pinLimitClock(t)
			keys, secrets := newCaller(t, "Synthetic scoped audio")
			key := keys[0]
			key.Limit = &access.Limit{Period: "day", Tokens: 100000, Models: []string{"sound/voice"}}
			setLimit(t, key.ID, key.Limit)
			body, contentType := `{"text":"synthetic hello","usage":{"type":"tokens","input_tokens":14,"output_tokens":7,"total_tokens":21}}`, "application/json"
			if strings.HasSuffix(path, "/speech") {
				body, contentType = scopedAudioDone, "text/event-stream"
			}
			var sends atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				states := budget.All(key, now)
				if len(states) != 1 || states[0].InFlight != 1 || states[0].Reserved <= 0 {
					t.Errorf("sent without scoped reservation: %+v", states)
				}
				w.Header().Set("Content-Type", contentType)
				io.WriteString(w, body)
			}))
			t.Cleanup(up.Close)
			if err := provider.Save(provider.Provider{ID: "sound", Name: "Synthetic sound", Chat: up.URL + "/v1", Key: "synthetic-key", Models: []string{"voice"}}); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			New().Handler().ServeHTTP(w, scopedAudioRequest(t, path, "sound/voice", secrets[0]))
			if w.Code != 200 || w.Body.String() != body || w.Header().Get("Content-Type") != contentType || sends.Load() != 1 {
				t.Fatalf("audio forwarding: code=%d sends=%d body=%s", w.Code, sends.Load(), w.Body.String())
			}
			for _, reset := range []bool{false, true} {
				if reset {
					budget.Forget()
				}
				states := budget.All(key, now)
				if len(states) != 1 || states[0].Unavailable || states[0].Tokens != 21 || states[0].Calls != 1 || states[0].Pending != 0 || states[0].InFlight != 0 {
					t.Fatalf("audio accounting after reset=%v: %+v", reset, states)
				}
			}
			rows := usage.Load(time.Time{})
			if len(rows) != 1 || rows[0].Input != 14 || rows[0].Output != 7 || !strings.HasPrefix(rows[0].Hold, "s2:") || rows[0].CallerKeyID != key.ID || rows[0].Operation != path[strings.LastIndex(path, "/")+1:] {
				t.Fatalf("audio occurrence: %+v", rows)
			}
		})
	}
}

func TestScopedAudioUnknownDoesNotBecomeFree(t *testing.T) {
	for _, tt := range []struct {
		name, body, contentType string
		cut, known              bool
	}{
		{"zero", strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(scopedAudioDone, ":14", ":0"), ":7", ":0"), ":21", ":0"), "text/event-stream", false, true},
		{"missing-output", "data: {\"type\":\"speech.audio.done\",\"usage\":{\"input_tokens\":14}}\n\n", "text/event-stream", false, false},
		{"binary", string(mp3), "audio/mpeg", false, false},
		{"json-no-usage", `{"audio_data":"AAAA"}`, "application/json", false, false},
		{"cut-after-usage", scopedAudioDone, "text/event-stream", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fresh(t)
			budget.Forget()
			t.Cleanup(budget.Forget)
			now := pinLimitClock(t)
			keys, secrets := newCaller(t, "Synthetic scoped audio", "Synthetic unlimited audio")
			key := keys[0]
			key.Limit = &access.Limit{Period: "day", Tokens: 100000, Models: []string{"sound/voice"}}
			setLimit(t, key.ID, key.Limit)
			var sends atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				w.Header().Set("Content-Type", tt.contentType)
				if tt.cut {
					w.Header().Set("Content-Length", strconv.Itoa(len(tt.body)+32))
				}
				io.WriteString(w, tt.body)
			}))
			t.Cleanup(up.Close)
			if err := provider.Save(provider.Provider{ID: "sound", Name: "Synthetic sound", Chat: up.URL + "/v1", Key: "synthetic-key", Models: []string{"voice"}}); err != nil {
				t.Fatal(err)
			}
			h := New().Handler()
			call := func(secret string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, scopedAudioRequest(t, "/v1/audio/speech", "sound/voice", secret))
				return w
			}
			if w := call(secrets[0]); w.Code != 200 || w.Body.String() != tt.body {
				t.Fatalf("first response changed: %d %s", w.Code, w.Body.String())
			}
			for _, reset := range []bool{false, true} {
				if reset {
					budget.Forget()
				}
				states := budget.All(key, now)
				if len(states) != 1 || states[0].Unavailable == tt.known || states[0].InFlight != 0 {
					t.Fatalf("known=%v after reset=%v: %+v", tt.known, reset, states)
				}
				if tt.known {
					if states[0].Tokens != 0 || states[0].Pending != 0 {
						t.Fatalf("explicit zero not settled: %+v", states)
					}
				} else if states[0].Pending != 1 || states[0].TokensLeft != 0 {
					t.Fatalf("unknown debt cleared: %+v", states)
				}
			}
			if !tt.known {
				if w := call(secrets[0]); w.Code != 503 || sends.Load() != 1 {
					t.Fatalf("unknown sent again: %d sends=%d", w.Code, sends.Load())
				}
			}
			if w := call(secrets[1]); w.Code != 200 || w.Body.String() != tt.body || sends.Load() != 2 {
				t.Fatalf("unlimited path changed: %d sends=%d", w.Code, sends.Load())
			}
		})
	}
}

func TestScopedAudioFallbackKeepsPriorUnknown(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(strconv.FormatBool(shared), func(t *testing.T) {
			fresh(t)
			budget.Forget()
			t.Cleanup(budget.Forget)
			now := pinLimitClock(t)
			keys, secrets := newCaller(t, "Synthetic fallback audio")
			key := keys[0]
			key.Limit = &access.Limit{Period: "day", Tokens: 100000, Models: []string{"first/voice"}}
			if shared {
				key.Limit.Models = append(key.Limit.Models, "second/voice")
			}
			setLimit(t, key.ID, key.Limit)
			var first, second atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/first/") {
					first.Add(1)
					w.WriteHeader(503)
					io.WriteString(w, `{"error":{"message":"synthetic unavailable"}}`)
					return
				}
				second.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, scopedAudioDone)
			}))
			t.Cleanup(up.Close)
			for _, id := range []string{"first", "second"} {
				if err := provider.Save(provider.Provider{ID: id, Name: id, Chat: up.URL + "/" + id + "/v1", Key: "synthetic-key", Models: []string{"voice"}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := provider.SaveGroup(provider.Group{ID: "sound", Name: "sound", Members: []string{"first/voice", "second/voice"}, Routing: "order"}); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			New().Handler().ServeHTTP(w, scopedAudioRequest(t, "/v1/audio/speech", "group/sound", secrets[0]))
			if first.Load() != 1 {
				t.Fatalf("first sent %d times", first.Load())
			}
			if shared {
				if w.Code != 503 || second.Load() != 0 || w.Header().Get("X-Should-Retry") != "false" || !strings.Contains(w.Body.String(), "couldn't read the key's usage") {
					t.Fatalf("shared unknown bypassed: %d second=%d body=%s", w.Code, second.Load(), w.Body.String())
				}
			} else if w.Code != 200 || second.Load() != 1 || w.Body.String() != scopedAudioDone {
				t.Fatalf("unrelated model lost fallback: %d second=%d body=%s", w.Code, second.Load(), w.Body.String())
			}
			budget.Forget()
			states := budget.All(key, now)
			if len(states) != 1 || !states[0].Unavailable || states[0].Pending != 1 || states[0].InFlight != 0 {
				t.Fatalf("fallback erased earlier debt: %+v", states)
			}
		})
	}
}

func TestScopedAudioDownstreamCutKeepsUnknown(t *testing.T) {
	fresh(t)
	budget.Forget()
	t.Cleanup(budget.Forget)
	now := pinLimitClock(t)
	keys, secrets := newCaller(t, "Synthetic cut audio")
	key := keys[0]
	key.Limit = &access.Limit{Period: "day", Tokens: 100000, Models: []string{"sound/voice"}}
	setLimit(t, key.ID, key.Limit)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, scopedAudioDone)
	}))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "sound", Name: "Synthetic sound", Chat: up.URL + "/v1", Key: "synthetic-key", Models: []string{"voice"}}); err != nil {
		t.Fatal(err)
	}
	New().Handler().ServeHTTP(audioTailWriteError{httptest.NewRecorder()}, scopedAudioRequest(t, "/v1/audio/speech", "sound/voice", secrets[0]))
	budget.Forget()
	states := budget.All(key, now)
	if len(states) != 1 || !states[0].Unavailable || states[0].Pending != 1 || states[0].InFlight != 0 || states[0].Tokens < 21 {
		t.Fatalf("downstream cut became settled or erased received counters: %+v", states)
	}
}

type audioMarkRefusal struct {
	scopedTransportLease
	ended *gwbudget.AttemptEnd
}

func (audioMarkRefusal) MarkSent() error { return gwbudget.ErrUsageUnrecorded }
func (l audioMarkRefusal) Finish(out gwbudget.AttemptOutcome) error {
	*l.ended = out.Kind
	return nil
}

func TestScopedAudioMarkFailureNeverSends(t *testing.T) {
	fresh(t)
	var sends atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
	}))
	t.Cleanup(up.Close)
	p := provider.Provider{ID: "sound", Name: "Synthetic sound", Chat: up.URL + "/v1", Key: "synthetic-key"}
	end := gwbudget.Recorded
	l := audioMarkRefusal{ended: &end}
	who := access.Identity{KeyID: "synthetic", Limit: &access.Limit{Period: "day", Tokens: 100000, Models: []string{"sound/voice"}}}
	ctx := access.WithIdentity(t.Context(), who)
	ctx = context.WithValue(ctx, scopedSendKey{}, &budgetSend{lease: l})
	_, _, _, code, err := New().retrieveOpen(ctx, p, up.URL+"/v1/audio/speech", "application/json", "", []byte(`{"model":"voice","input":"hello"}`))
	if code != 503 || err != gwbudget.ErrUsageUnrecorded || sends.Load() != 0 {
		t.Fatalf("mark refusal dispatched: %d %v sends=%d", code, err, sends.Load())
	}
	abandonBudgetSend(ctx)
	if end != gwbudget.NeverSent {
		t.Fatalf("unsent marked %v", end)
	}
}
