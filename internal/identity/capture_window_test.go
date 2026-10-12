package identity

import (
	"testing"
	"time"
)

func TestCaptureWindowIsExplicitOneShotAndMemoryOnly(t *testing.T) {
	ownStore := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", ownStore)
	now := time.Now().UTC()
	if err := Arm("relay", ClientCodex, now); err != nil {
		t.Fatal(err)
	}
	if !CaptureArmed("relay", now.Add(time.Second)) {
		t.Fatal("armed capture is not visible")
	}
	armed := ArmedCaptures(now.Add(time.Second))
	if len(armed) != 1 || armed[0].Provider != "relay" || armed[0].Client != ClientCodex || !armed[0].Expires.Equal(now.Add(CaptureWindow)) {
		t.Fatalf("armed snapshot = %+v", armed)
	}
	p, consumed, err := Observe("relay", ClientCodex, codexHeader(), now.Add(2*time.Second))
	if err != nil || !consumed {
		t.Fatalf("observe = %v, %v", p, err)
	}
	if p.Source != SourceCapture || p.ObservedAt == nil || p.ConfirmedAt != nil {
		t.Fatalf("capture = %+v, want an unconfirmed observed candidate", p)
	}
	if CaptureArmed("relay", now.Add(3*time.Second)) {
		t.Fatal("one-shot capture remained armed after observation")
	}
	stored, err := Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Fingerprint.UserAgent != p.Fingerprint.UserAgent {
		t.Fatalf("stored candidate = %+v", stored)
	}
}

func TestCaptureWindowDoesNotConsumeOnWrongClientAndExpires(t *testing.T) {
	now := time.Now().UTC()
	if err := Arm("relay", ClientCodex, now); err != nil {
		t.Fatal(err)
	}
	if _, consumed, err := Observe("relay", ClientClaudeCode, claudeHeader(), now.Add(time.Second)); err != nil || consumed {
		t.Fatalf("wrong client consumed capture: %v, %v", consumed, err)
	}
	if !CaptureArmed("relay", now.Add(2*time.Second)) {
		t.Fatal("wrong client disarmed capture")
	}
	if _, consumed, err := Observe("relay", ClientCodex, codexHeader(), now.Add(CaptureWindow+time.Second)); err != nil || consumed {
		t.Fatalf("expired capture was consumed: %v, %v", consumed, err)
	}
	if CaptureArmed("relay", now.Add(CaptureWindow+2*time.Second)) {
		t.Fatal("expired capture remained armed")
	}
}
