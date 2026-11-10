package budget

import (
	"errors"
	"io/fs"
	"os"

	"github.com/yetone/magpie/internal/usage"
)

// Revisions invalidate warm sums after another process appends. They follow
// the shared ledger index and journal's append-only file stamp, not a timer.
type budgetRevision struct {
	ledger uint64
	holds  os.FileInfo
}

func currentRevision() (budgetRevision, error) {
	v, ok := usage.LogVersion()
	if !ok {
		return budgetRevision{}, errPendingUsage
	}
	info, err := os.Stat(holdsPath())
	if errors.Is(err, fs.ErrNotExist) {
		info, err = nil, nil
	}
	if err == nil && info != nil && !info.Mode().IsRegular() {
		err = errPendingUsage
	}
	return budgetRevision{v, info}, err
}

func (v budgetRevision) same(other budgetRevision) bool {
	if v.ledger != other.ledger {
		return false
	}
	if v.holds == nil || other.holds == nil {
		return v.holds == nil && other.holds == nil
	}
	return os.SameFile(v.holds, other.holds) && v.holds.Size() == other.holds.Size() && v.holds.ModTime().Equal(other.holds.ModTime())
}
