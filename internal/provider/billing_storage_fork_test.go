package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/settings"
)

func billingStored(t *testing.T, id string, mode BillingMode, exhausted QuotaExhaustion) {
	t.Helper()
	p, ok := rawRow(t, id)
	if !ok || p.Billing != mode || p.QuotaExhausted != exhausted {
		t.Fatalf("stored %s: billing=%q quotaExhausted=%q found=%v", id, p.Billing, p.QuotaExhausted, ok)
	}
}

func TestBillingStorageRoundTrip(t *testing.T) {
	h := newLifeHome(t)
	p := h.row("relay")
	p.Billing, p.QuotaExhausted = BillingQuota, QuotaPaid
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	billingStored(t, "relay", BillingQuota, QuotaPaid)
	p = h.find("relay")
	p.Name = "edited"
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	copy := Provider{ID: "copy", Name: "copy", Chat: p.Chat}
	id, err := AddCopy(copy, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	billingStored(t, id, BillingQuota, QuotaPaid)
	copy.ID, copy.Name, copy.Billing, copy.QuotaExhausted = "explicit", "explicit", BillingList, QuotaStop
	id, err = AddCopy(copy, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	billingStored(t, id, BillingList, QuotaStop)
	for _, c := range []struct {
		id            string
		mode          BillingMode
		exhausted     QuotaExhaustion
		wantMode      BillingMode
		wantExhausted QuotaExhaustion
	}{
		{"mode-only", BillingFree, "", BillingFree, QuotaPaid},
		{"exhaustion-only", "", QuotaStop, BillingQuota, QuotaStop},
	} {
		id, err := AddCopy(Provider{ID: c.id, Chat: p.Chat, Billing: c.mode, QuotaExhausted: c.exhausted}, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		billingStored(t, id, c.wantMode, c.wantExhausted)
	}
	if err := Rename("relay", "renamed"); err != nil {
		t.Fatal(err)
	}
	billingStored(t, "renamed", BillingQuota, QuotaPaid)
	added := h.row("added")
	added.Key, added.Billing, added.QuotaExhausted = "another-key", BillingFree, QuotaStop
	id, err = Add(added)
	if err != nil {
		t.Fatal(err)
	}
	billingStored(t, id, BillingFree, QuotaStop)
	ps, err := Stored()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(ps)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []Provider
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := Mirror(nil, nil); err != nil {
		t.Fatal(err)
	}
	n, replaced, err := Restore(decoded)
	if err != nil || n != len(decoded) || replaced != 0 {
		t.Fatalf("restore: %d %d %v", n, replaced, err)
	}
	billingStored(t, "renamed", BillingQuota, QuotaPaid)
	for i := range decoded {
		decoded[i].Billing, decoded[i].QuotaExhausted = BillingList, QuotaStop
	}
	n, replaced, err = Restore(decoded)
	if err != nil || n != 0 || replaced != len(decoded) {
		t.Fatalf("replace: %d %d %v", n, replaced, err)
	}
	billingStored(t, "renamed", BillingList, QuotaStop)
	decoded[0].Billing, decoded[0].QuotaExhausted = BillingQuota, QuotaPaid
	if err := Mirror(decoded, nil); err != nil {
		t.Fatal(err)
	}
	billingStored(t, decoded[0].ID, BillingQuota, QuotaPaid)
}

func TestBillingSubscriptionSaveAndPicks(t *testing.T) {
	signIn(t)
	if err := Save(Provider{ID: "codex", Billing: BillingQuota, QuotaExhausted: QuotaPaid}); err != nil {
		t.Fatal(err)
	}
	billingStored(t, "codex", BillingQuota, QuotaPaid)
	p, err := Find("codex")
	if err != nil {
		t.Fatal(err)
	}
	if p.Account == nil || p.Billing != BillingQuota || p.QuotaExhausted != QuotaPaid {
		t.Fatalf("account reconstruction: account=%v billing=%q quotaExhausted=%q", p.Account != nil, p.Billing, p.QuotaExhausted)
	}
	p.Billing, p.QuotaExhausted = BillingList, QuotaStop
	if err := Save(*p); err != nil {
		t.Fatal(err)
	}
	billingStored(t, "codex", BillingList, QuotaStop)
	picks := withPicks(Provider{Billing: BillingFree, QuotaExhausted: QuotaPaid}, Provider{})
	if picks.Billing != "" || picks.QuotaExhausted != "" {
		t.Fatal("omitted picks synthesized metadata")
	}
}

func TestBillingInvalidWritesAreAtomic(t *testing.T) {
	for _, invalid := range []Provider{{Billing: "future"}, {QuotaExhausted: "continue"}} {
		for _, op := range []string{"save", "add", "copy", "restore", "mirror"} {
			t.Run(op+string(invalid.Billing)+string(invalid.QuotaExhausted), func(t *testing.T) {
				h := readyHome(t, nil, "relay")
				bad := h.row("invalid")
				bad.Billing, bad.QuotaExhausted = invalid.Billing, invalid.QuotaExhausted
				good := h.row("first")
				good.Billing = BillingFree
				before := configSnapshot(t)
				var err error
				switch op {
				case "save":
					err = Save(bad)
				case "add":
					_, err = Add(bad)
				case "copy":
					_, err = AddCopy(bad, "relay")
				case "restore":
					var added, replaced int
					added, replaced, err = Restore([]Provider{good, bad})
					if added != 0 || replaced != 0 {
						t.Fatalf("invalid batch reported committed rows: %d %d", added, replaced)
					}
				case "mirror":
					err = Mirror([]Provider{good, bad}, []Group{{ID: "new-group"}})
				}
				if err == nil {
					t.Fatal("accepted invalid metadata")
				}
				sameSnapshot(t, "invalid billing write", before, configSnapshot(t))
			})
		}
	}
}

func TestBillingStaleWritesRemainRefused(t *testing.T) {
	h := readyHome(t, nil, "relay")
	p := h.find("relay")
	p.Billing, p.QuotaExhausted = BillingQuota, QuotaPaid
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	p = h.find("relay")
	ps, base, err := StoredBase()
	if err != nil {
		t.Fatal(err)
	}
	if err := Rename("relay", "renamed"); err != nil {
		t.Fatal(err)
	}
	before := configSnapshot(t)
	if err := Save(p); !errors.Is(err, ErrStale) {
		t.Fatalf("stale save: %v", err)
	}
	if err := Mirror(ps, nil, OnBase(base)); !errors.Is(err, ErrStale) {
		t.Fatalf("stale mirror: %v", err)
	}
	if _, err := AddCopy(Provider{ID: "copy", Chat: p.Chat}, "renamed", Expect(p)); !errors.Is(err, ErrStale) {
		t.Fatalf("stale copy: %v", err)
	}
	sameSnapshot(t, "stale billing writes", before, configSnapshot(t))
	billingStored(t, "renamed", BillingQuota, QuotaPaid)
}

func TestBillingReadsPreserveUnknownAndOmittedMetadata(t *testing.T) {
	h := newLifeHome(t)
	p := h.row("relay")
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	if b := string(providersBytes(t)); strings.Contains(b, `"billing"`) || strings.Contains(b, `"quotaExhausted"`) {
		t.Fatal("save synthesized defaults")
	}
	p.Billing, p.QuotaExhausted = "future", "future"
	writeOutside(t, p)
	before := configSnapshot(t)
	ps, err := Stored()
	if err != nil || len(ps) != 1 || ps[0].Billing != "future" || ps[0].QuotaExhausted != "future" {
		t.Fatalf("unknown stored metadata: %+v %v", ps, err)
	}
	loaded, err := Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	d, err := loaded.ResolveBilling(settings.Settings{}, catalog.Model{Free: true})
	if err == nil || d != (BillingDecision{}) {
		t.Fatalf("unknown policy resolution: %+v, %v", d, err)
	}
	sameSnapshot(t, "read-only billing resolution", before, configSnapshot(t))
}
