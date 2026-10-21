package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/filelock"
)

// A channel is the process mutex so waiting for it is cancellable too.
var localLock = make(chan struct{}, 1)

const lockWait = 10 * time.Second
const lockPoll = 5 * time.Millisecond

// Lock requires both process and OS ownership. It never proceeds unlocked.
// Unlock is idempotent and never removes the persistent lock file. Take this
// lock before identity's mutex; do not call network or catalog callbacks under it.
func Lock(ctx context.Context) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	select {
	case localLock <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
	}
	release := func() { <-localLock }
	dir := appdir.Config()
	if err := os.MkdirAll(dir, 0700); err != nil {
		release()
		return nil, fmt.Errorf("lifecycle lock directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "lifecycle.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		release()
		return nil, fmt.Errorf("lifecycle lock: %w", err)
	}
	fail := func(err error) (func(), error) { f.Close(); release(); return nil, err }
	tick := time.NewTicker(lockPoll)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fail(fmt.Errorf("%w: %w", ErrBusy, err))
		}
		held, err := filelock.TryLock(f)
		if err != nil {
			return fail(fmt.Errorf("lifecycle lock: %w", err))
		}
		if held {
			var once sync.Once
			return func() { once.Do(func() { filelock.Unlock(f); f.Close(); release() }) }, nil
		}
		select {
		case <-ctx.Done():
			return fail(fmt.Errorf("%w: %w", ErrBusy, ctx.Err()))
		case <-tick.C:
		}
	}
}
