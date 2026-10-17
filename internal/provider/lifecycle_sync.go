package provider

// Sync and restore over provider lifecycle (R1 G3b-2). A sync decides what
// to bring in from providers.json as collected: a write made here after
// that (a rename, a delete, one deleted and added again) must not be undone
// by that decision, nor its binding handed to what the server holds. So the
// collect keeps the file's base (StoredBase), and the Mirror that follows
// is refused (*ConflictError) when the file is no longer that, or when
// lifecycle.json moved on since (a provider deleted and added again with
// the same bytes is another instance under lifecycle.json). A restore is
// "make it the backup" as the file is now: it merges into a fresh read, and
// reads and merges again once when another write got in between.
//
// Neither carries lifecycle.json or identities.json: a provider kept by id
// keeps its instance and binding, one renamed elsewhere comes as one
// deleted and one added (its binding doesn't follow), and one deleted
// leaves a tombstone, as any write does (lifecycle_commit.go).

import (
	"errors"

	"github.com/yetone/magpie/internal/lifecycle"
)

// FileBase is providers.json as one read found it: what a later write
// checks the file against (OnBase). The zero FileBase checks nothing.
type FileBase struct {
	b    fileBase
	meta metaBase
}

// metaBase is lifecycle.json as a read found it: absent, or its epoch and
// gen. One that couldn't be read is known false: any write on it is a
// conflict. The zero value is "not read", and checks nothing.
type metaBase struct {
	read, known, exists bool
	epoch               string
	gen                 uint64
}

// since is a conflict when lifecycle.json, as a commit reads it under the
// lock (meta), is not what b found: every commit advances gen, so one
// made since b was read, or a new epoch, changed which instance holds an
// id even where providers.json's bytes are the same.
func (b metaBase) since(meta *lifecycle.Meta) error {
	switch {
	case !b.read:
		return nil
	case !b.known:
		return &ConflictError{Reason: "lifecycle.json couldn't be read when this change was made"}
	case b.exists != (meta != nil) || meta != nil && (meta.Epoch != b.epoch || meta.Gen != b.gen):
		return &ConflictError{Reason: "a provider was changed, deleted or added again since it was read"}
	}
	return nil
}

// StoredBase is Stored with the base the providers were read at:
// providers.json, then lifecycle.json. A commit between the two reads
// leaves one of them other than that commit wrote, so a write on the base
// is refused then too.
func StoredBase() ([]Provider, FileBase, error) {
	f, err := read()
	if err != nil {
		return f.Providers, FileBase{}, err
	}
	mb := metaBase{read: true}
	if meta, err := lifecycle.ReadMeta(); err == nil {
		mb.known, mb.exists = true, meta != nil
		if meta != nil {
			mb.epoch, mb.gen = meta.Epoch, meta.Gen
		}
	}
	return f.Providers, FileBase{f.base, mb}, nil
}

// OnBase has a write check providers.json against b instead of against
// its own read: a change to providers.json or lifecycle.json since b was
// read is a *ConflictError, and nothing is written. The zero b leaves the write's own check.
func OnBase(b FileBase) WriteOption {
	return func(o *writeOpts) {
		if b.b.read {
			o.base, o.in.meta = b.b, b.meta
		}
	}
}

// Restore puts providers from a backup in. Each replaces the one here with
// its id; one that came without keys keeps the keys already here, and
// one without an access key or balance token keeps those (KeepSecrets). It
// returns how many were new and how many replaced one here. A write that
// got in between its read and its commit has it read and merge again,
// once.
func Restore(ps []Provider) (added, replaced int, err error) {
	added, replaced, err = restoreOnce(ps)
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		added, replaced, err = restoreOnce(ps)
	}
	return added, replaced, err
}
