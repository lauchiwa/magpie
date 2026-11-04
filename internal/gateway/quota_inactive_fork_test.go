package gateway

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/provider"
)

func dormantQuotaFile(t *testing.T, state provider.QuotaState) string {
	t.Helper()
	path := filepath.Join(appdir.Config(), "quota.json")
	now := time.Now().UTC() // the legacy request path reads the real clock
	switch state {
	case provider.QuotaNone:
		return path
	case provider.QuotaUnknown:
		if err := os.WriteFile(path, []byte(`{"version":1,`), 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		if err := provider.NewQuotaStore(path).Exhaust("freev", "quota", now.Add(time.Hour), now); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// The store is not bound to the gateway yet. The production-reference
// guard is the activation gate; this exercises the ordinary path beside it.
func TestQuotaInactiveKeepsLegacyRouting(t *testing.T) {
	for name, state := range map[string]provider.QuotaState{"none": provider.QuotaNone, "exhausted": provider.QuotaExhausted, "unknown": provider.QuotaUnknown} {
		t.Run(name, func(t *testing.T) {
			_, asked := freeByPrice(t, nil)
			path := dormantQuotaFile(t, state)
			before, readErr := os.ReadFile(path)
			_, lockErr := os.Stat(path + ".lock")
			s := New()
			code, body := postAs(t, s, "", `{"model":"group/g","messages":[{"role":"user","content":"x"}]}`)
			if code != http.StatusOK || !slices.Equal(asked(), []string{"freev"}) {
				t.Fatalf("legacy free-first changed: code=%d body=%s calls=%v", code, body, asked())
			}
			after, err := os.ReadFile(path)
			if state == provider.QuotaNone {
				if !os.IsNotExist(readErr) || !os.IsNotExist(err) {
					t.Fatalf("startup/request created quota state: before=%v after=%v", readErr, err)
				}
			} else if readErr != nil || err != nil || string(before) != string(after) {
				t.Fatalf("legacy request changed dormant state: before=%v after=%v", readErr, err)
			}
			if _, err := os.Stat(path + ".lock"); os.IsNotExist(lockErr) && !os.IsNotExist(err) {
				t.Fatalf("startup/request took the quota lock: %v", err)
			}
			if aside, _ := filepath.Glob(path + ".bad-*"); len(aside) != 0 {
				t.Fatalf("startup/request moved quota state aside: %v", aside)
			}
		})
	}
}

func TestQuotaInactiveKeepsLegacyCreditFallback(t *testing.T) {
	broke := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":{"message":"insufficient balance","type":"insufficient_quota"}}`)
	})
	_, asked := freeByPrice(t, broke)
	path := dormantQuotaFile(t, provider.QuotaExhausted)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New()
	code, body := postAs(t, s, "", `{"model":"group/g","messages":[{"role":"user","content":"x"}]}`)
	if code != http.StatusOK || !slices.Equal(asked(), []string{"freev", "paidv"}) {
		t.Fatalf("legacy credit fallback changed: code=%d body=%s calls=%v", code, body, asked())
	}
	restingUntil.Lock()
	rested := false
	for id, rest := range restingUntil.note {
		if strings.HasPrefix(id, "freev") && rest.Why == failCredit {
			rested = true
		}
	}
	restingUntil.Unlock()
	if !rested {
		t.Fatal("dormant quota state prevented the live credit rest")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("credit refusal mutated dormant state: %v", err)
	}
}
