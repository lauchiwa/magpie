package identity

import (
	"os"
	"testing"
)

// PlanBind and PlanUnbind are Bind's and Unbind's change, checked the same
// way, with nothing written: the bytes and who was bound before, for a
// provider commit to write in its journal.
func TestPlanBindWritesNothing(t *testing.T) {
	ownStore(t)
	p := confirmed(t)
	unconfirmed := newCandidate(t, ClientCodex, codexHeader())
	if err := Add(unconfirmed); err != nil {
		t.Fatal(err)
	}
	before := dirListing(t)
	raw0, _ := os.ReadFile(Path())
	after, changed, bound, err := PlanBind("relay", p.ID)
	if err != nil || !changed || len(bound) != 0 {
		t.Fatalf("PlanBind: changed %v, bound %v, %v", changed, bound, err)
	}
	f, err := decode(after)
	if err != nil || f.Bound["relay"] != p.ID {
		t.Fatalf("planned store: %+v %v", f.Bound, err)
	}
	if raw1, _ := os.ReadFile(Path()); string(raw0) != string(raw1) || dirListing(t) != before {
		t.Fatal("PlanBind wrote the store")
	}
	for name, id := range map[string]string{"no profile": "nope", "unconfirmed": unconfirmed.ID} {
		if _, _, _, err := PlanBind("relay", id); err == nil {
			t.Errorf("PlanBind of %s was planned", name)
		}
	}
	if _, _, _, err := PlanBind("", p.ID); err == nil {
		t.Error("PlanBind of no provider was planned")
	}
	if err := Bind("relay", p.ID); err != nil {
		t.Fatal(err)
	}
	if after, changed, bound, err := PlanBind("relay", p.ID); err != nil || changed || after != nil || !bound["relay"] {
		t.Fatalf("PlanBind already bound: %v %v %v %v", after != nil, changed, bound, err)
	}
	after, changed, bound, err = PlanUnbind("relay")
	if err != nil || !changed || !bound["relay"] {
		t.Fatalf("PlanUnbind: %v %v %v", changed, bound, err)
	}
	if f, err := decode(after); err != nil || f.Bound["relay"] != "" || len(f.Profiles) != 2 {
		t.Fatalf("planned unbind: %+v %v", f, err)
	}
	if _, changed, _, err := PlanUnbind("other"); err != nil || changed {
		t.Fatalf("PlanUnbind of nothing bound: %v %v", changed, err)
	}
	// a store cut short is planned from its last good generation, as Bind's
	// change reads it
	if err := Bind("other", p.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(Path())
	if err := os.WriteFile(Path(), make([]byte, len(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, changed, bound, err := PlanUnbind("relay"); err != nil || !changed || !bound["relay"] || bound["other"] {
		t.Fatalf("PlanUnbind over a zeroed store: %v %v %v", changed, bound, err)
	}
}
