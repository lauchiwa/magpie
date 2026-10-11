package gui

import "testing"

// Joining the Dock while active is activated again by way of the Dock, so
// magpie leads Command-Tab's order (#1381); anything else is as before.
func TestDockStep(t *testing.T) {
	cases := []struct {
		regular, on, active, front bool
		want                       dockMove
	}{
		// already where it should be: nothing, active or not
		{true, true, true, true, dockMove{}},
		{false, false, true, true, dockMove{}},
		// the panel (active) opens the window: joins, activated again
		{false, true, true, true, dockMove{policy: true, again: true, front: true}},
		// the tray's Open or a second magpie (not active): joins, activated
		{false, true, false, true, dockMove{policy: true, front: true}},
		// joins for another reason, not to the front: not activated
		{false, true, true, false, dockMove{policy: true}},
		// the window closed: leaves, stays where it is
		{true, false, true, false, dockMove{policy: true}},
		// Settings takes it out with the window open: brought back to the front
		{true, false, true, true, dockMove{policy: true, front: true}},
	}
	for _, c := range cases {
		if got := dockStep(c.regular, c.on, c.active, c.front); got != c.want {
			t.Errorf("dockStep(regular=%v, on=%v, active=%v, front=%v) = %+v, want %+v", c.regular, c.on, c.active, c.front, got, c.want)
		}
	}
}
