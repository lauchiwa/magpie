package sessions

import "time"

// fork: heldStamp is time.Now for held's hold and listing times, but each
// one later than the one before. Windows' clock moves in ticks of up to
// ~15ms, and two time.Now calls in one tick are equal: a hold begun just
// after a listing was read then took that listing as its own (its at not
// Before since), and TestOneListing was red there. Called under held.
var lastHeldStamp time.Time

func heldStamp() time.Time {
	t := time.Now()
	if !t.After(lastHeldStamp) {
		t = lastHeldStamp.Add(time.Nanosecond)
	}
	lastHeldStamp = t
	return t
}
