//go:build unix

package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPackedHoldMappedAndSpilled(t *testing.T) {
	pageHome(t)
	at := time.Date(2026, 11, 8, 0, 0, 1, 123, time.FixedZone("held", 3600))
	c := &rowChunk{}
	c.add(Row{Record: Record{Time: at.Add(-2 * time.Second), Hold: scopedTestHold(at), RouteID: 1 << 42, Input: 13, ImageFiles: 3, UpBytes: 17, ImageFilesSaved: -4}}, "message", 1<<45, false)
	packed := c.pack()
	path := filepath.Join(t.TempDir(), "hold-archive.bin")
	if err := os.WriteFile(path, packed.Archive, 0o600); err != nil {
		t.Fatal(err)
	}
	m, ok := mapDiskFile(path)
	if !ok {
		t.Fatal("archive not mapped")
	}
	mapped := *packed
	mapped.Archive, mapped.mapped = m.data, m
	spilled := *packed
	sp := newArchiveSpill()
	if !sp.put(&spilled, 0) {
		t.Fatal("archive not spilled")
	}
	sp.hand(&spilled)
	sp.done(func(int) *rowChunk { t.Fatal("spill unexpectedly rebuilt"); return packed })
	if spilled.mapped == nil || !spilled.mapped.spill {
		t.Fatal("spill is not mapped")
	}
	for _, chunk := range []*rowChunk{&mapped, &spilled} {
		back := chunk.unpack()
		if back == nil || len(back.Rows) != 1 {
			t.Fatal("archive unreadable")
		}
		r := back.row(0)
		if r.Hold != scopedTestHold(at) || r.RouteID != 1<<42 || back.Rows[0].Order != 1<<45 || r.Input != 13 || r.UpBytes != 17 || r.ImageFilesSaved != -4 || r.ImageFiles != 3 {
			t.Fatalf("lost packed fields: %+v", r)
		}
		if !r.Time.Equal(at.Add(-2*time.Second)) || r.Time.Format(time.RFC3339Nano) != at.Add(-2*time.Second).Format(time.RFC3339Nano) {
			t.Fatal("lost display time/zone")
		}
		n := 0
		if err := visitBudgetBlocks([]*rowChunk{chunk}, at, func(Record) { n++ }); err != nil || n != 1 {
			t.Fatalf("late mapped row skipped: %d %v", n, err)
		}
	}
}
