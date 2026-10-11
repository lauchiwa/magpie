package gui

// dockMove is what setDock does to the app on the Mac.
type dockMove struct {
	policy bool // join the Dock or leave it
	again  bool // activate magpie again by way of the Dock (activateAgain)
	front  bool // activate magpie
}

// dockStep: what setDock does for magpie in the Dock (regular) or not, to
// be in it (on) or not, magpie active or not, brought to the front or not.
// Nothing when it is where it should be. Leaving deactivates the app, so
// with front it is activated after. Joining while active, as it is when the
// quick panel opens the window or the Settings page puts it there, it is
// activated again by way of the Dock: an app activated out of the Dock that
// joins it stays last in Command-Tab's order, and one Command-Tab away
// couldn't come back to the window (#1381).
func dockStep(regular, on, active, front bool) dockMove {
	if regular == on {
		return dockMove{}
	}
	return dockMove{policy: true, again: on && front && active, front: front}
}
