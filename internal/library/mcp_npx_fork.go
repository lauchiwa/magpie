package library

// fork: NoNpxInstall, for other packages' tests, has an npx server's
// package never installed, as the library's own tests have it (npxInstall
// nil): `library mcp add … npx -y fs` in internal/cli's tests started a
// real npm install against the registry, which outlived its test and held
// the test's folder on Windows. The returned func puts it back.
func NoNpxInstall() func() {
	was := npxInstall
	npxInstall = nil
	return func() { npxInstall = was }
}

// NpxInstallOff is whether NoNpxInstall holds, for the test that checks a
// package's TestMain called it.
func NpxInstallOff() bool { return npxInstall == nil }
