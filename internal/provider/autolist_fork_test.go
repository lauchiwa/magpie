package provider

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"
)

// fork: the scheduled list fetch reads providers.json itself (ownOf, not
// All, #3185), and marks what it read as All does, so the picks it drops
// after the fetch go only to the provider it fetched for: one deleted and
// added again under its id while the list was asked is another, and keeps
// its picks (Expect(p) in RefetchTaking).
func TestAScheduledFetchDropsPicksOnlyOfTheProviderItRead(t *testing.T) {
	h := readyHome(t, nil)
	r := newRelayList(t, "gpt-5", "gone-model")
	relay := Provider{ID: "relay", Name: "Relay", Chat: r.srv.URL + "/v1", Key: "sk-relay", Models: []string{"gpt-5", "gone-model"}, RefreshEvery: "6h"}
	h.saveRows(relay)
	t.Cleanup(func() {
		listsTried.Lock()
		clear(listsTried.m)
		listsTried.Unlock()
	})
	now := time.Now()
	if got := refreshDueLists(context.Background(), now); !slices.Equal(got, []string{"relay"}) {
		t.Fatalf("asked %v, want [relay]", got)
	}
	// the next fetch no longer lists gone-model, and the relay is deleted
	// and added again while it is asked
	r.set("gpt-5")
	list := r.srv.Config.Handler
	var swapped bool
	r.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !swapped {
			swapped = true
			if err := Delete("relay"); err != nil {
				t.Error(err)
			}
			h.saveRows(relay)
		}
		list.ServeHTTP(w, req)
	})
	if got := refreshDueLists(context.Background(), now.Add(6*time.Hour+time.Minute)); !slices.Equal(got, []string{"relay"}) || !swapped {
		t.Fatalf("asked %v (swapped %v), want [relay]", got, swapped)
	}
	if p, _ := rawRow(t, "relay"); !slices.Equal(p.Models, []string{"gpt-5", "gone-model"}) {
		t.Fatalf("the old relay's fetch dropped the new relay's picks: %v", p.Models)
	}
}
