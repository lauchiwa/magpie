package gui

import (
	"encoding/json"
	"errors"

	"github.com/yetone/magpie/internal/provider"
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
