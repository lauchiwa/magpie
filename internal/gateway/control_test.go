package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/identity"
)

func TestIdentityControlArmAndDisarm(t *testing.T) {
	var calls []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Query().Get("provider") != "relay" {
			t.Errorf("provider = %q", r.URL.Query().Get("provider"))
		}
		if r.URL.Path == IdentityArmPath && r.URL.Query().Get("client") != string(identity.ClientCodex) {
			t.Errorf("client = %q", r.URL.Query().Get("client"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()
	baseURL := s.URL + "/"
	ctx := context.Background()
	if err := ArmIdentity(ctx, baseURL, "relay", identity.ClientCodex); err != nil {
		t.Fatal(err)
	}
	if err := DisarmIdentity(ctx, baseURL, "relay"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "POST "+IdentityArmPath || calls[1] != "POST "+IdentityDisarmPath {
		t.Fatalf("calls = %v", calls)
	}
}

func TestIdentityControlArmedDetailsAndLegacy(t *testing.T) {
	expires := time.Date(2026, 10, 12, 1, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		body       string
		wantClient identity.Client
		wantExpiry bool
	}{
		{
			name:       "new details",
			body:       `{"object":"list","data":["relay"],"details":[{"provider":"relay","client":"codex_cli","expiresAt":"` + expires.Format(time.RFC3339Nano) + `"}]}`,
			wantClient: identity.ClientCodex,
			wantExpiry: true,
		},
		{
			name: "old id list",
			body: `{"object":"list","data":["relay","legacy"]}`,
		},
		{
			name:       "details only",
			body:       `{"object":"list","details":[{"provider":"relay","client":"claude_code","expiresAt":"` + expires.Format(time.RFC3339Nano) + `"}]}`,
			wantClient: identity.ClientClaudeCode,
			wantExpiry: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != IdentityArmedPath {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			got, err := ArmedIdentities(context.Background(), s.URL)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if tc.name == "old id list" {
				want = 2
			}
			if len(got) != want {
				t.Fatalf("armed = %+v, want %d entries", got, want)
			}
			if got[0].Provider != "relay" || got[0].Client != tc.wantClient {
				t.Fatalf("first armed = %+v, want relay as %q", got[0], tc.wantClient)
			}
			if (got[0].Expires != nil) != tc.wantExpiry {
				t.Fatalf("expiry = %v, want present %v", got[0].Expires, tc.wantExpiry)
			}
			if tc.wantExpiry && !got[0].Expires.Equal(expires) {
				t.Errorf("expiry = %v, want %v", got[0].Expires, expires)
			}
			if tc.name == "old id list" && got[1].Provider != "legacy" {
				t.Errorf("legacy entry = %+v", got[1])
			}
		})
	}
}

func TestIdentityControlErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "old gateway route", status: http.StatusNotFound, body: `{"error":{"message":"not found"}}`, want: "404 Not Found: not found"},
		{name: "gateway error", status: http.StatusBadRequest, body: `{"error":{"message":"bad client"}}`, want: "400 Bad Request: bad client"},
		{name: "empty error", status: http.StatusForbidden, want: "403 Forbidden: no error message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			err := ArmIdentity(context.Background(), s.URL, "relay", identity.ClientCodex)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestIdentityControlTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := ArmIdentity(ctx, s.URL, "relay", identity.ClientCodex)
	if err == nil {
		t.Fatal("arm without a response succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
}

func TestIdentityArmedRejectsInvalidExpiry(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":    []string{"relay"},
			"details": []map[string]string{{"provider": "relay", "client": "codex_cli", "expiresAt": "tomorrow"}},
		})
	}))
	defer s.Close()
	if _, err := ArmedIdentities(context.Background(), s.URL); err == nil || !strings.Contains(err.Error(), "expiry") {
		t.Fatalf("error = %v, want invalid expiry error", err)
	}
}
