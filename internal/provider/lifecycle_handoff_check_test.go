package provider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/lifecycle"
)

// A rename whose commit fails before its journal is written (the config
// folder can't take the journal) is not pending: nothing will finish it,
// so its fetched list stays under the old id, and no journal is left. Only
// a journal on disk makes a failed rename move its list (journalPending).
func TestARenameFailingBeforeItsJournalMovesNoList(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "managed"}[managed], func(t *testing.T) {
			renameHome(t, managed)
			live := catalog.LivePath("relay")
			if err := os.MkdirAll(filepath.Dir(live), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(live, []byte(`{"models":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			denyWrites(t)
			before := configSnapshot(t)
			err := Rename("relay", "relay2")
			if err == nil {
				t.Fatal("a rename in a folder that can't take its journal was made")
			}
			if pendingJournal(err) {
				t.Fatalf("a rename with no journal written is pending: %v", err)
			}
			if _, jerr := os.Stat(lifecycle.JournalPath()); !errors.Is(jerr, os.ErrNotExist) {
				t.Fatalf("a journal after the failed rename (%v)", jerr)
			}
			sameSnapshot(t, "a rename that couldn't write its journal", before, configSnapshot(t))
			if _, err := os.Stat(live); err != nil {
				t.Fatalf("a rename with no journal moved the fetched list: %v", err)
			}
			if _, err := os.Stat(catalog.LivePath("relay2")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a rename with no journal put a fetched list under relay2 (%v)", err)
			}
		})
	}
}
