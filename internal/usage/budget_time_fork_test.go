package usage

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func scopedTestHold(at time.Time) string {
	return fmt.Sprintf("s2:0123456789abcdef0123456789abcdef:%d", at.UnixNano())
}

func TestBudgetClockIsNotDisplayClock(t *testing.T) {
	cut := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	c := &rowChunk{}
	for range logBlockRows {
		c.add(Row{Record: Record{Time: cut.Add(-time.Second), Hold: scopedTestHold(cut.Add(time.Second)), Input: 3}}, "msg", 1, false)
	}
	packed := c.freeze()
	disk, err := readDiskEntry(diskEntry(nil, packed))
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []*rowChunk{c, packed, disk, packed.writable(), c.within(cut.Add(-time.Minute), cut)} {
		n := 0
		err := visitBudgetBlocks([]*rowChunk{chunk}, cut, func(r Record) {
			n++
			at, err := BudgetTime(r)
			if err != nil || !at.Equal(cut.Add(time.Second)) || !r.Time.Before(cut) || r.Input != 3 {
				t.Fatalf("clock/row changed: %+v, %v", r, err)
			}
		})
		if err != nil || n != logBlockRows {
			t.Fatalf("late display rows skipped: %d, %v", n, err)
		}
	}
}

func TestMalformedBudgetClockCannotSkipAWindow(t *testing.T) {
	at := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	c := &rowChunk{}
	c.add(Row{Record: Record{Time: at.Add(-time.Hour), Hold: "s2:broken"}}, "", 0, false)
	packed, err := readDiskEntry(diskEntry(nil, c))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = visitBudgetBlocks([]*rowChunk{packed}, at, func(r Record) {
		n++
		if _, err := BudgetTime(r); err == nil {
			t.Fatal("malformed clock became known")
		}
	})
	if err != nil || n != 1 {
		t.Fatalf("malformed hold skipped: %d %v", n, err)
	}
}

func TestBudgetIteratorColdDiskAndRebuild(t *testing.T) {
	pageHome(t)
	cut := time.Now().Truncate(time.Hour)
	for i := range logBlockRows {
		if err := AppendErr(Record{Time: cut.Add(-time.Second), Hold: scopedTestHold(cut.Add(time.Duration(i + 1))), CallerKeyID: "synthetic", Input: 1}); err != nil {
			t.Fatal(err)
		}
	}
	s := readLogSnapshot()
	if err := logDiskSave(s); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []bool{false, true} {
		if rebuild {
			if err := os.WriteFile(logDiskPath(), []byte("obsolete derived cache"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		logIndex.Lock()
		logIndex.snapshot = nil
		logIndex.Unlock()
		n := 0
		if err := VisitBudgetRead(cut, func(Record) { n++ }); err != nil || n != logBlockRows {
			t.Fatalf("cold/rebuild=%v: %d rows, %v", rebuild, n, err)
		}
	}
	after, err := os.ReadFile(Path())
	if err != nil || string(before) != string(after) {
		t.Fatal("derived cache rebuild changed history", err)
	}
}

func TestBudgetClockRejectsNoncanonicalEnvelope(t *testing.T) {
	at := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	for _, hold := range []string{
		"s2:0123456789abcdef0123456789abcdef:0",
		"s2:0123456789abcdef0123456789abcdef:-1",
		"s2:0123456789abcdef0123456789abcdef:+1",
		"s2:0123456789abcdef0123456789abcdef:01",
		"s2:0123456789abcdef0123456789abcdef:9223372036854775808",
		"s2:0123456789ABCDEF0123456789ABCDEF:1",
		"s2:0123456789abcdef0123456789abcdeg:1",
		"s2:0123456789abcdef0123456789abcdef:1:extra",
	} {
		t.Run(hold, func(t *testing.T) {
			c := &rowChunk{}
			c.add(Row{Record: Record{Time: at.Add(-time.Hour), Hold: hold}}, "", 0, false)
			disk, err := readDiskEntry(diskEntry(nil, c.freeze()))
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			if err := visitBudgetBlocks([]*rowChunk{disk}, at, func(r Record) {
				n++
				if _, err := BudgetTime(r); err == nil {
					t.Fatal("noncanonical envelope accepted")
				}
			}); err != nil || n != 1 {
				t.Fatalf("malformed archived envelope skipped: %d %v", n, err)
			}
		})
	}
	for _, hold := range []string{"", "legacy-hold"} {
		got, err := BudgetTime(Record{Time: at, Hold: hold})
		if err != nil || !got.Equal(at) {
			t.Fatalf("legacy clock changed: %v %v", got, err)
		}
	}
}

func TestBudgetIteratorPrunesOnlyKnownOldBlocks(t *testing.T) {
	at := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	// A deliberately undecodable old archive must never be opened; an
	// overlapping or malformed-clock archive must fail, not report no usage.
	for _, mode := range []string{"old", "overlap", "invalid", "missing-bound"} {
		t.Run(mode, func(t *testing.T) {
			c := &rowChunk{Count: 1, Archive: []byte("bad archive"), BudgetLatest: at.Add(-time.Second)}
			switch mode {
			case "overlap":
				c.BudgetLatest = at
			case "invalid":
				c.BudgetInvalid = true
			case "missing-bound":
				c.BudgetLatest = time.Time{}
			}
			n := 0
			err := visitBudgetBlocks([]*rowChunk{c}, at, func(Record) { n++ })
			if n != 0 || (err == nil) != (mode == "old") {
				t.Fatalf("mode=%s visited=%d err=%v", mode, n, err)
			}
		})
	}
}

func TestBudgetIteratorRejectsIncompleteDecodedBlock(t *testing.T) {
	at := time.Now()
	c := &rowChunk{}
	c.add(Row{Record: Record{Time: at, Hold: scopedTestHold(at)}}, "", 0, false)
	c = c.freeze()
	c.Count++
	if err := visitBudgetBlocks([]*rowChunk{c}, at, func(Record) { t.Fatal("partial block exposed") }); err == nil {
		t.Fatal("incomplete block reported complete")
	}
}
