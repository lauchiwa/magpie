package provider

import (
	"runtime"
	"testing"
)

// environSum follows the environment: a variable set, changed or unset
// stamps another sum, and the same environment the same one.
func TestEnvironSumFollowsTheEnvironment(t *testing.T) {
	before := environSum()
	if environSum() != before {
		t.Fatal("the same environment summed twice differs")
	}
	t.Setenv("MAGPIE_ENVIRON_SUM_PROBE", "a")
	a := environSum()
	if a == before {
		t.Fatal("a variable set left the sum as it was")
	}
	t.Setenv("MAGPIE_ENVIRON_SUM_PROBE", "b")
	if environSum() == a {
		t.Fatal("a variable changed left the sum as it was")
	}
	t.Setenv("MAGPIE_ENVIRON_SUM_PROBE", "a")
	if environSum() != a {
		t.Fatal("the same environment again sums to another")
	}
}

// On Windows os.Environ makes every variable's string again on each call,
// and every kept catalog's hit stamps the environment.
func TestEnvironSumAllocatesLittle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("os.Environ copies one slice elsewhere")
	}
	if a := testing.AllocsPerRun(20, func() { environSum() }); a > 5 {
		t.Fatalf("environSum made %.0f allocations", a)
	}
}
