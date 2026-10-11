package identity

import (
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

// The store is written beside providers.json, so the tests run in a home
// of their own: never the runner's ~/.config/magpie.
func TestMain(m *testing.M) { testenv.Main(m) }
