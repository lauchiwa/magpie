package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
)

// new-api's refusals when a key's wallet or token can't pay for a request,
// as its ToOpenAIError writes them (HTTP 403): the messages are its
// i18n/locales quota.user_insufficient and quota.pre_consume_insufficient,
// and service/quota.go's PreConsumeTokenQuota error.
var newAPIBroke = map[string]string{
	"user quota, en":     `{"error":{"message":"Insufficient user quota, remaining quota: ＄0.000000","type":"new_api_error","param":"","code":"insufficient_user_quota"}}`,
	"user quota, zh-CN":  `{"error":{"message":"用户额度不足, 剩余额度: ＄0.000000","type":"new_api_error","param":"","code":"insufficient_user_quota"}}`,
	"pre-consume, en":    `{"error":{"message":"Not enough quota to pre-consume for this request: ＄0.002000 remaining, ＄0.010000 required","type":"new_api_error","param":"","code":"insufficient_user_quota"}}`,
	"pre-consume, zh-CN": `{"error":{"message":"预扣费额度失败, 用户剩余额度: ＄0.002000, 需要预扣费额度: ＄0.010000","type":"new_api_error","param":"","code":"insufficient_user_quota"}}`,
	"token quota":        `{"error":{"message":"token quota is not enough, token remain quota: ＄0.002000, need quota: ＄0.010000","type":"new_api_error","param":"","code":"pre_consume_token_quota_failed"}}`,
}

// Each of them is a key out of credit or quota, the two failures that
// rest a paid model alone beside a free one (PaidBesideFree in failure's
// caller): the English ones say quota; the Chinese ones' 剩余额度 holds
// 余额, which creditWords reads as an account short of money.
func TestNewAPIBrokeIsOutOfCreditOrQuota(t *testing.T) {
	want := map[string]string{
		"user quota, en":     failQuota,
		"user quota, zh-CN":  failCredit,
		"pre-consume, en":    failQuota,
		"pre-consume, zh-CN": failCredit,
		"token quota":        failQuota,
	}
	for name, body := range newAPIBroke {
		if got := failure(http.StatusForbidden, []byte(body)); got != want[name] {
			t.Errorf("%s: failure(403) = %q, want %q", name, got, want[name])
		}
	}
}

// A relay key the user priced one model of at nothing (`magpie model
// price rv/free-m 0,0,0,0`), its other model paid, and neither marked
// free by the relay's list. The wallet runs out: paid-m is refused, and
// free-m, which the relay still serves for nothing, keeps its key. The
// keys are asked in order, so free-m goes to the first unless it rests.
func TestOutOfBalanceRestsThePaidModelBesideOneTheUserPricedFree(t *testing.T) {
	for _, name := range []string{"user quota, en", "user quota, zh-CN", "token quota"} {
		t.Run(name, func(t *testing.T) {
			fresh(t)
			restingUntil.Lock()
			restingUntil.note = map[string]Rest{}
			restingUntil.Unlock()
			var mu sync.Mutex
			var answered []string // the key each free-m request came with
			relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					Model string `json:"model"`
				}
				b, _ := io.ReadAll(r.Body)
				json.Unmarshal(b, &q)
				w.Header().Set("Content-Type", "application/json")
				if q.Model == "paid-m" {
					w.WriteHeader(http.StatusForbidden)
					io.WriteString(w, newAPIBroke[name])
					return
				}
				mu.Lock()
				answered = append(answered, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
				mu.Unlock()
				io.WriteString(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
			})
			serveOn(t, "rv", "sk-one", []string{"paid-m", "free-m"}, relay, "sk-two")
			p, err := provider.Find("rv")
			if err != nil {
				t.Fatal(err)
			}
			p.Routing = provider.Ordered
			if err := provider.Save(*p); err != nil {
				t.Fatal(err)
			}
			liveModels(t, "rv", `{"models":[{"id":"paid-m","cost":{"input":3,"output":15}},{"id":"free-m","cost":{"input":1,"output":2}}]}`)
			if err := provider.SetModelPrice("rv/free-m", &catalog.Price{}); err != nil {
				t.Fatal(err)
			}
			s := New()

			if code, body := postAs(t, s, "", `{"model":"rv/paid-m","messages":[{"role":"user","content":"x"}]}`); code == 200 {
				t.Fatalf("paid-m should be refused: %s", body)
			}
			restingUntil.Lock()
			rests := len(restingUntil.note)
			for id, r := range restingUntil.note {
				if !strings.HasSuffix(id, "/paid-m") {
					t.Errorf("%s rests (%s): only paid-m should, beside free-m the user priced at nothing", id, r.Why)
				}
			}
			restingUntil.Unlock()
			if rests == 0 {
				t.Fatal("paid-m should rest")
			}

			if code, body := postAs(t, s, "", `{"model":"rv/free-m","messages":[{"role":"user","content":"x"}]}`); code != 200 {
				t.Fatalf("free-m: code %d: %s", code, body)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(answered) != 1 || answered[0] != "sk-one" {
				t.Fatalf("free-m answered on %v, want the first key, which only rests for paid-m", answered)
			}
		})
	}
}
