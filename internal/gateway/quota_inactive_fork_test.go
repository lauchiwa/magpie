package gateway

import (
	"context"
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
	path := filepath.Join(appdir.Config(), "quota-state.json")
	if state == provider.QuotaStateUnknown {
		return path
	}
	store := provider.NewQuotaStore(path)
	epoch, err := store.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The legacy request path uses the real clock; keep this evidence fresh there too.
	now := time.Now().UTC()
	fact := provider.QuotaFact{
		Window: "included", Kind: provider.QuotaConfirmedExhaustion,
		EvidenceID: "evidence-1", Cycle: "cycle-1",
	}
	if state == provider.QuotaStateAvailable {
		fact.Kind = provider.QuotaReportedAvailable
		fact.FreshUntil = now.Add(time.Hour)
		fact.Capacity = &provider.QuotaCapacity{Semantics: "included", Unit: "requests", Remaining: 5, Limit: 10}
	}
	subject := provider.QuotaSubject{Authority: "fixture", Provider: "freev", Principal: "key-instance-1", Generation: "scope-1"}
	_, err = store.Apply(context.Background(), provider.QuotaObservation{
		Subject: subject, ID: "observation-1", Expected: provider.QuotaVersion{StoreEpoch: epoch},
		Source: "fixture-source", ObservedAt: now,
		Coverage: []provider.QuotaCoverage{{Window: "included", Scopes: []string{"m"}}},
		Facts:    []provider.QuotaFact{fact},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.Read(subject, "m", now)
	if err != nil || view.State != state {
		t.Fatalf("dormant fixture = %+v, %v; want %s", view, err, state)
	}
	return path
}

// The store is not bound to gateway identities yet. The production-reference
// guard is the activation gate; this exercises the ordinary path beside it.
func TestQuotaInactiveKeepsLegacyRouting(t *testing.T) {
	for _, state := range []provider.QuotaState{provider.QuotaStateUnknown, provider.QuotaStateExhausted, provider.QuotaStateAvailable} {
		t.Run(string(state), func(t *testing.T) {
			_, asked := freeByPrice(t, nil)
			path := dormantQuotaFile(t, state)
			before, readErr := os.ReadFile(path)
			s := New()
			code, body := postAs(t, s, "", `{"model":"group/g","messages":[{"role":"user","content":"x"}]}`)
			if code != http.StatusOK || !slices.Equal(asked(), []string{"freev"}) {
				t.Fatalf("legacy free-first changed: code=%d body=%s calls=%v", code, body, asked())
			}
			after, err := os.ReadFile(path)
			if state == provider.QuotaStateUnknown {
				if !os.IsNotExist(readErr) || !os.IsNotExist(err) {
					t.Fatalf("startup/request created quota state: before=%v after=%v", readErr, err)
				}
				if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
					t.Fatalf("startup/request created quota lock: %v", err)
				}
			} else if readErr != nil || err != nil || string(before) != string(after) {
				t.Fatalf("legacy request changed dormant state: before=%v after=%v", readErr, err)
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
	path := dormantQuotaFile(t, provider.QuotaStateAvailable)
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
		t.Fatal("dormant availability prevented the live credit rest")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("credit refusal mutated dormant state: %v", err)
	}
}
