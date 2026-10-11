package update

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yetone/magpie/internal/proc"
)

// This fork has no paid Apple certificate, so its macOS app is ad-hoc signed
// (`codesign --sign -`), as the Release workflow does it. The updater accepts
// a new app only when it is signed by the same team as the app installed
// (sameSigner), so this checks that an ad-hoc build replaces an ad-hoc build:
// both have no team, the signature is intact, and the update goes in. Were
// this to stop holding, every macOS update of this fork would be refused.
func TestAnAdHocSignedAppUpdatesAnAdHocSignedApp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("codesign and ad-hoc signing are a Mac's")
	}
	dir := t.TempDir()
	installed := adHocApp(t, filepath.Join(dir, "installed"), "old")
	downloaded := adHocApp(t, filepath.Join(dir, "downloaded"), "new")

	if err := sameSigner(context.Background(), installed, downloaded); err != nil {
		t.Fatalf("an ad-hoc build won't update an ad-hoc build: %v", err)
	}
	// Neither has a team, which is what makes them each other's equal.
	if team := team(context.Background(), installed); team != "" {
		t.Errorf("an ad-hoc app reports the team %q", team)
	}
}

// A build signed by somebody else is still refused: ad-hoc signing doesn't
// turn the check off, it only makes both sides teamless.
func TestADifferentlySignedAppIsStillRefused(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("codesign and ad-hoc signing are a Mac's")
	}
	dir := t.TempDir()
	installed := adHocApp(t, filepath.Join(dir, "installed"), "old")
	broken := adHocApp(t, filepath.Join(dir, "broken"), "new")
	// Change a byte after signing: the signature no longer matches.
	exe := filepath.Join(broken, "Contents", "MacOS", "magpie")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sameSigner(context.Background(), installed, broken); err == nil {
		t.Fatal("an app changed after signing was accepted")
	}
}

// adHocApp builds a minimal magpie.app under dir and signs it the way the
// Release workflow does.
func adHocApp(t *testing.T, dir, what string) string {
	t.Helper()
	app := filepath.Join(dir, "magpie.app")
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>magpie</string>
<key>CFBundleIdentifier</key><string>ai.usemagpie.magpie</string>
<key>CFBundleName</key><string>magpie</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macos, "magpie"), []byte("#!/bin/sh\necho "+what+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// exactly what the workflow runs
	if b, err := proc.CommandContext(context.Background(), "codesign", "--force", "--deep", "--sign", "-", app).CombinedOutput(); err != nil {
		t.Fatalf("codesign: %v: %s", err, b)
	}
	return app
}
