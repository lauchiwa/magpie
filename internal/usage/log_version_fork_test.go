package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogVersionDoesNotCallAnUnreadLogEmpty(t *testing.T) {
	pageHome(t)
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, known := LogVersion(); known {
		t.Fatal("a directory at the log path was a known empty log")
	}
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	if _, known := LogVersion(); !known {
		t.Fatal("a missing log after recovery is not known empty")
	}
	if err := AppendErr(Record{Time: time.Now(), Provider: "relay", Model: "m", Status: 200}); err != nil {
		t.Fatal(err)
	}
	if _, known := LogVersion(); !known {
		t.Fatal("a completely read log after recovery is unknown")
	}
}
