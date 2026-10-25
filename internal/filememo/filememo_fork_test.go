package filememo

import (
	"testing"
	"testing/synctest"
)

// The fork's provider lifecycle tests commit inside a synctest bubble, to
// keep a slow fsync from running out Hold's second. Their Forget must not
// leave a channel made in the bubble for the next write outside it to
// close: Go stops the whole test binary with "close of synctest channel
// from outside bubble" (lifecycle_hold_test.go, lifecycle_guard_test.go).
func TestForgetInsideABubbleLeavesNoBubbleChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) { Forget() })
	w := Written()
	Forget()
	select {
	case <-w:
	default:
		t.Fatal("Written's channel wasn't closed by the next Forget")
	}
	// waiters between two writes share one channel, and the next write
	// closes it for all of them
	a, b := Written(), Written()
	if a != b {
		t.Fatal("two waiters between writes were given different channels")
	}
	Forget()
	for _, c := range []<-chan struct{}{a, b} {
		select {
		case <-c:
		default:
			t.Fatal("a waiter's channel wasn't closed by the next Forget")
		}
	}
}
