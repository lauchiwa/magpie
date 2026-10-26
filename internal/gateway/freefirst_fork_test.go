package gateway

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
)

// freeByPrice is the case the user's own price is for: two relays serve
// the same model, neither lists it free, and the user priced every model
// of one at nothing (`magpie model price 'freev/*' 0,0,0,0`). The paid one
// is listed first, so only the price can put the free one ahead.
func freeByPrice(t *testing.T, free http.Handler) (*Server, func() []string) {
	t.Helper()
	fresh(t)
	var mu sync.Mutex
	var asked []string
	note := func(id string, h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			asked = append(asked, id)
			mu.Unlock()
			h.ServeHTTP(w, r)
		})
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	})
	if free == nil {
		free = ok
	}
	serveOn(t, "freev", "sk-free", []string{"m"}, note("freev", free))
	serveOn(t, "paidv", "sk-paid", []string{"m"}, note("paidv", ok))
	// neither vendor's list says free, and both price m
	liveModels(t, "freev", `{"models":[{"id":"m","cost":{"input":3,"output":15}}]}`)
	liveModels(t, "paidv", `{"models":[{"id":"m","cost":{"input":3,"output":15}}]}`)
	if err := provider.SetModelPrice("freev/*", &catalog.Price{}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{
		Name: "G", Members: []string{"paidv/m", "freev/m"}, Routing: provider.FreeFirst,
	}); err != nil {
		t.Fatal(err)
	}
	return New(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}
}

// A member the user priced at nothing is spent first under Free first,
// and its trace row says Free.
func TestFreeFirstSpendsWhatTheUserPricedAtNothing(t *testing.T) {
	s, asked := freeByPrice(t, nil)
	code, body := postAs(t, s, "", `{"model":"group/g","messages":[{"role":"user","content":"x"}]}`)
	if code != 200 {
		t.Fatalf("code %d: %s", code, body)
	}
	if got := asked(); !slices.Equal(got, []string{"freev"}) {
		t.Fatalf("asked %v, want only freev", got)
	}
	last := s.trace.routes[len(s.trace.routes)-1]
	if len(last.Order) < 2 || last.Order[0].Provider != "freev" || !last.Order[0].Free {
		t.Fatalf("the trace should put freev first, as Free: %+v", last.Order)
	}
	if w := last.Order[len(last.Order)-1]; w.Provider != "paidv" || w.Free {
		t.Fatalf("paidv should be last and not Free: %+v", last.Order)
	}
}

// When the free one is out of balance, the same request is answered by
// the paid one: free first, then what costs.
func TestFreeFirstFallsToThePaidOneWhenTheFreeOneIsSpent(t *testing.T) {
	broke := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":{"message":"insufficient balance","type":"insufficient_quota"}}`)
	})
	s, asked := freeByPrice(t, broke)
	code, body := postAs(t, s, "", `{"model":"group/g","messages":[{"role":"user","content":"x"}]}`)
	if code != 200 {
		t.Fatalf("code %d: %s", code, body)
	}
	if got := asked(); !slices.Equal(got, []string{"freev", "paidv"}) {
		t.Fatalf("asked %v, want freev then paidv", got)
	}
	restingUntil.Lock()
	defer restingUntil.Unlock()
	rested := false
	for id, r := range restingUntil.note {
		if strings.HasPrefix(id, "freev") {
			rested = true
			if r.Why != failCredit {
				t.Errorf("%s rests for %s, want %s", id, r.Why, failCredit)
			}
		}
	}
	if !rested {
		t.Errorf("freev should rest out of credit: %v", restingUntil.note)
	}
}
