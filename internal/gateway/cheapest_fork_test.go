package gateway

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
)

// Member order is the preference among members that cost the same under
// Cheapest first (B7): of two at $3/$15 the one listed first goes first,
// and swapping them swaps the order; one at $1/$5 goes first from any
// place in the list.
func TestCheapestFirstSameCostKeepsTheListedOrder(t *testing.T) {
	cost := map[string]catalog.Price{"samea": {Input: 3, Output: 15}, "sameb": {Input: 3, Output: 15}, "cheap": {Input: 1, Output: 5}}
	for _, c := range []struct {
		ids  []string
		want []string
	}{
		{[]string{"samea", "sameb"}, []string{"samea", "sameb"}},
		{[]string{"sameb", "samea"}, []string{"sameb", "samea"}},
		{[]string{"cheap", "samea", "sameb"}, []string{"cheap", "samea", "sameb"}},
		{[]string{"samea", "cheap", "sameb"}, []string{"cheap", "samea", "sameb"}},
		{[]string{"sameb", "samea", "cheap"}, []string{"cheap", "sameb", "samea"}},
	} {
		t.Run(strings.Join(c.ids, ","), func(t *testing.T) {
			fresh(t)
			members := make([]string, 0, len(c.ids))
			for _, id := range c.ids {
				serveOn(t, id, "sk-"+id, []string{"m"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
				}))
				price := cost[id]
				if err := provider.SetModelPrice(id+"/*", &price); err != nil {
					t.Fatal(err)
				}
				members = append(members, id+"/m")
			}
			if err := provider.SaveGroup(provider.Group{Name: "G", Members: members, Routing: provider.Cheapest}); err != nil {
				t.Fatal(err)
			}
			s := New()
			code, body := postAs(t, s, "", `{"model":"group/g","messages":[{"role":"user","content":"x"}]}`)
			if code != 200 {
				t.Fatalf("code %d: %s", code, body)
			}
			var got []string
			for _, w := range s.trace.routes[len(s.trace.routes)-1].Order {
				got = append(got, w.Provider)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("weighed %v, want %v", got, c.want)
			}
		})
	}
}
