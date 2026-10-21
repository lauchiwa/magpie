package davsync

// Provider lifecycle in sync (R1 G3b-2). What a sync, a restore or an undo
// brings in was decided from providers.json as collect read it: the Mirror
// is checked against that read (provider.OnBase), so a rename, delete or
// recreate made here meanwhile is never undone by that decision, nor its
// binding handed to what the server holds. A sync refused so is run once
// more, over the file as it is now, as one refused by the server is.
// Providers are the first part brought in, so nothing but a missing icon,
// and the copies kept in the sync folder, was written before the refusal.
// A restore or undo is said refused.

import (
	"errors"

	"github.com/yetone/magpie/internal/backup"
	"github.com/yetone/magpie/internal/provider"
)

// afterCollect, set by a test, runs between a collect and the bring of
// what was decided from it: a write here meanwhile.
var afterCollect func()

// onCollected checks a write of a decision made from local against
// providers.json as local was collected.
func onCollected(local backup.Bundle) provider.WriteOption {
	if afterCollect != nil {
		afterCollect()
	}
	return provider.OnBase(local.Base())
}

// changedHere is a sync refused because providers.json changed here since
// it was collected.
func changedHere(err error) bool {
	var conflict *provider.ConflictError
	return errors.As(err, &conflict)
}
