package agent

// fork: NoWSL, for other packages' tests, has magpie find no WSL on any
// system, as off Windows: on a Windows machine with a distro running, a
// test's start-up otherwise ran the real wsl.exe, probed the distro and
// wrote wsl.json into the test's home. The returned func puts it back.
func NoWSL() func() {
	on := wslOn
	wslOn = false
	return func() { wslOn = on }
}
