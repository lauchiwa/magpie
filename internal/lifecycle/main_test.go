package lifecycle

import (
	"github.com/yetone/magpie/internal/testenv"
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
