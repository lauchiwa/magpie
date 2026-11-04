package gateway

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
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

// relays serves model m on each of ids, each priced $3/$15 by the user,
// and saves group G of their m in that order under routing. A relay in
// free is priced at nothing instead; one in broke answers 402 out of
// balance.
// It returns the server and who was asked, in order.
func relays(t *testing.T, routing string, ids, free, broke []string) (*Server, func() []string) {
	t.Helper()
	fresh(t)
	var mu sync.Mutex
	var asked []string
	members := make([]string, 0, len(ids))
	for _, id := range ids {
		out := slices.Contains(broke, id)
		serveOn(t, id, "sk-"+id, []string{"m"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			asked = append(asked, id)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if out {
				w.WriteHeader(http.StatusPaymentRequired)
				io.WriteString(w, `{"error":{"message":"insufficient balance","type":"insufficient_quota"}}`)
				return
			}
			io.WriteString(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1000,"completion_tokens":100}}`)
		}))
		liveModels(t, id, `{"models":[{"id":"m"}]}`)
		price := &catalog.Price{Input: 3, Output: 15}
		if slices.Contains(free, id) {
			price = &catalog.Price{}
		}
		if err := provider.SetModelPrice(id+"/*", price); err != nil {
			t.Fatal(err)
		}
		members = append(members, id+"/m")
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: members, Routing: routing}); err != nil {
		t.Fatal(err)
	}
	return New(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}
}

// paidServed checks that the one that answered, id, is billed as paid:
// not free, at its price, and recorded as the call's provider.
func paidServed(t *testing.T, id string) {
	t.Helper()
	p, err := provider.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	if p.FreeModel("m") {
		t.Errorf("%s/m counts as free", id)
	}
	if pr, ok := p.PriceAt(settings.Load(), "m", time.Now()); !ok || pr.Input != 3 || pr.Output != 15 {
		t.Errorf("%s/m is priced %+v (known %v), want its $3/$15", id, pr, ok)
	}
	if r := lastUsage(t); r.Provider != id || r.Status != 200 {
		t.Errorf("the answer is recorded as %q (status %d), want %s", r.Provider, r.Status, id)
	}
}

// Member order is the preference among the paid members under Free
// first (B7): the free one goes first wherever it is listed, and when it
// is out of balance the paid member listed first answers, priced as it
// was. Reordering the paid members changes which one; nothing else does.
func TestFreeFirstPaidMembersKeepTheirListedOrder(t *testing.T) {
	for _, c := range []struct {
		ids  []string
		want []string
	}{
		{[]string{"paidb", "freev", "paida"}, []string{"freev", "paidb"}},
		{[]string{"paida", "freev", "paidb"}, []string{"freev", "paida"}},
	} {
		t.Run(strings.Join(c.ids, ","), func(t *testing.T) {
			s, asked := relays(t, provider.FreeFirst, c.ids, []string{"freev"}, []string{"freev"})
			code, body := postAs(t, s, "", `{"model":"group/g","messages":[{"role":"user","content":"x"}]}`)
			if code != 200 {
				t.Fatalf("code %d: %s", code, body)
			}
			if got := asked(); !slices.Equal(got, c.want) {
				t.Fatalf("asked %v, want %v", got, c.want)
			}
			paidServed(t, c.want[1])
		})
	}
}

// Under In order the paid member listed first leads, and when it is out
// of balance the next one answers, priced as it was: a preference never
// keeps a request on a member that can't pay for it.
func TestInOrderPassesTheListedFirstWhenItIsOutOfBalance(t *testing.T) {
	s, asked := relays(t, provider.Ordered, []string{"paida", "paidb"}, nil, []string{"paida"})
	code, body := postAs(t, s, "", `{"model":"group/g","messages":[{"role":"user","content":"x"}]}`)
	if code != 200 {
		t.Fatalf("code %d: %s", code, body)
	}
	if got := asked(); !slices.Equal(got, []string{"paida", "paidb"}) {
		t.Fatalf("asked %v, want paida then paidb", got)
	}
	paidServed(t, "paidb")
	restingUntil.Lock()
	defer restingUntil.Unlock()
	rested := false
	for id, r := range restingUntil.note {
		if strings.HasPrefix(id, "paida") {
			rested = true
			if r.Why != failCredit {
				t.Errorf("%s rests for %s, want %s", id, r.Why, failCredit)
			}
		}
	}
	if !rested {
		t.Errorf("paida should rest out of credit: %v", restingUntil.note)
	}
}
