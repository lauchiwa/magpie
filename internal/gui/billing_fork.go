package gui

import (
	"encoding/json"
	"errors"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// inactiveBilling refuses even null or empty values: storage support must
// not present a successful setter before routing and accounting use it.
func inactiveBilling(billing, exhausted json.RawMessage) error {
	if len(billing) != 0 || len(exhausted) != 0 {
		return errors.New("billing and quotaExhausted controls are not active yet")
	}
	return nil
}

// keepDormantBilling carries metadata the existing editor cannot send.
// Copies inherit it in provider.AddCopy instead.
func keepDormantBilling(in, old *provider.Provider) {
	if old != nil {
		in.Billing, in.QuotaExhausted = old.Billing, old.QuotaExhausted
	}
}

// billingOf is the editor's billing control (billing.js): the mode p has
// today, as BillingPolicy derives it from CountFree and `<id>/*` alone,
// and locked when its `<id>/*` is the user's own price, set from the
// command line, which the control never writes over. An explicit Billing
// in the file (sync, a hand edit) is left out: nothing reads it yet, and
// the control shows what runtime does. It is sent as billingMode, not
// billing, the explicit field's name, which the editor can't save yet.
func billingOf(s settings.Settings, p provider.Provider, whole *provider.Whole) (provider.BillingMode, bool) {
	p.Billing, p.QuotaExhausted = "", ""
	b, err := p.BillingPolicy(s)
	if err != nil {
		return "", false
	}
	return b.Mode, whole != nil && !whole.Free
}
