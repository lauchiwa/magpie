# Sync and backup

`internal/backup` collects magpie's portable setup into a passphrase-sealed
bundle. `internal/davsync` keeps that bundle in a WebDAV folder, an S3 bucket
or a GitHub repository. It merges changes between computers; it does not
clone a working tree or synchronize arbitrary files.

## Sources of truth

- [`Config`, `Configure`, `Status` and `syncOnce`](../../internal/davsync/davsync.go)
  keep the active server in `sync.json`, and the last local/remote hashes,
  versions, notices and undo file in `sync-state.json`. These files live beside
  magpie's settings. Secrets are local, never in the status response.
- The [`remote`](../../internal/davsync/dav.go) interface reads a version and
  conditionally replaces it. WebDAV and S3 use ETags; GitHub uses the blob SHA
  required by its Contents API. [`github.go`](../../internal/davsync/github.go)
  interprets `github://owner/repo[/prefix]`, with `Config.Branch` selecting a
  branch or using the repository's default.
- [`backup.go`](../../internal/backup/backup.go) owns bundle contents and
  sealing. Sync divides the setup into providers, settings, profiles, agents
  and library. Provider keys, agent models, library and usage have their own
  inclusion switches.
- [`usage.go`](../../internal/davsync/usage.go) shares separately sealed usage
  and quota files through a remote's `files` interface.
- [`restore.go`](../../internal/davsync/restore.go) replaces local parts from
  the remote bundle, first keeping the local setup for Undo.
- [`backup.go`](../../internal/gui/backup.go), the Settings form in
  [`app.js`](../../internal/gui/assets/app.js) and
  [`webdav_cli.go`](../../internal/cli/webdav_cli.go) reach the same sync operations.

## What stays on each computer

The settings part carries the user's preferences, but not what is only about
one computer. [`Settings.KeepOwn`](../../internal/settings/settings.go) puts
those back from this computer's settings whenever a sync, a join, a Restore,
an Undo or a backup file brings settings in, and `hashes` blanks them, so
changing one is no change to push. They are: the window's size and whether it
was maximised, the fonts, the proxy, the gateway's port, sharing on the local
network (`LAN`) and the addresses let in to it (`AllowIPs`), the private
hosts magpie may download from (`FetchPrivate`), the Dock, gateway
mode, keeping awake, lightweight windows, WSL lookups, account discovery, when
allowances are read, recording gateway conversations, the Mac terminal a
session resumes in (`SessionTerminal`) and what the menu bar shows. Launch at
login is the system's own setting (`internal/autostart`) and the listen
address is `MAGPIE_ADDR`, so neither is in a bundle.

Sharing turned on at one computer never turns it on or off at another
(Lullaby on Discord). A backup restored onto a new computer leaves sharing as
that computer has it, off on a fresh one: sharing opens the gateway to the
network, and the user turns it on there by hand, as the port and proxy are
set there. The default sharing key is different: it is in the gateway-key
store, which rides the settings part with `LANKey` and `LANKeyID`, so every
computer holds the same one, and sharing turned on at a second computer takes
it up rather than making another (`TestLANSharingStaysOnItsComputer`).

## Runtime and state

The gateway runs sync every three minutes by default. `SetAuto` can select a
longer interval or manual sync only. Saving in Settings syncs immediately.
Each operation holds the sync lock, so configuration changes cannot race a
sync's state write.

A first computer creates a remote backup. Another computer joining takes
the remote setup and keeps displaced local parts in its sync folder.
Subsequent syncs compare each part with the hashes from the previous sync:
local-only changes go up, remote-only changes come down, and a part changed
on both sides keeps the newer version while retaining the replaced copy.
A conditional write refused because another computer committed in between
is retried through this same merge.

What a sync brings in was decided from the local setup as `collect` read it.
A provider write made here before the bring (a rename, delete, re-create or
edit) must not be undone by that decision. So `backup.Collect` keeps
`providers.json`'s base (`Bundle.Base()`, never sealed), and `bring` passes it
to `provider.Mirror` as `provider.OnBase`
([`lifecycle_fork.go`](../../internal/davsync/lifecycle_fork.go),
[`lifecycle_sync.go`](../../internal/provider/lifecycle_sync.go)). The base
covers the file's bytes, and under `lifecycle.json` its epoch and gen, so a
provider deleted and re-added with the same bytes still counts as a change. If
either changed, the Mirror is refused with `*provider.ConflictError` and
nothing is written. `syncNow` then runs the sync once more, as it does for
`errChanged`, over the file as it is now. Providers are the first part brought
in, so the only earlier writes are missing icons filled in and the copies kept
in the sync folder before a merge that takes one side. The second run decides
by the usual rules: providers changed on both sides keep the newer, with the
other kept in the sync folder, and on a first sync the server's come in. So
the check keeps a sync from undoing a write it never saw; it does not make a
change made here win. A rename made here that the server's newer providers,
or a first sync, replace is gone from `providers.json`, and this computer's
setup is kept as `<time>-this-computer.magpie-backup` in the sync folder. A
second refusal is the sync's error, and the next round tries again. A
Restore or Undo from the server that is refused this way returns the error
(`bringing in the providers: …; nothing was changed — read it again and
retry`). An opened bundle carries
no base, so its Mirror is checked only against its own read, as before.

`backup.Restore` ("make it the backup") merges into a fresh read of
`providers.json`. If another write lands between that read and the commit, it
reads and merges once more; a second conflict is its error. Neither sync nor
restore carries `lifecycle.json` or `identities.json`. Their effect on
instances and bindings is the commit's: see
[identity profiles](identity-profiles.md#provider-lifecycle-state). The
`.bak` files still keep the generation before each write.

When a remote file is empty, damaged or another app's format, sync stops and
describes it through `ServerFile`. Settings and the backend's `upload` command
can replace it with this computer's setup, keeping the old file first.
HTML and XML are not offered for replacement. GitHub diagnostics name the
repository and `magpie github upload`, rather than WebDAV's recovery path.

A local file that is there but can't be read is unknown, never empty or the
defaults. `backup.Collect` stops on an unreadable providers.json (with its groups
and order), settings.json (`settings.LoadChecked`: unreadable, or
not parsing with no good `.bak`) or library.json, and on a gateway key
store that fails to export. A backup, an export and a sync all end with that
error, nothing sealed or pushed; the sync's status shows it. A missing or
empty settings.json is still none (`TestCollectStopsOnUnreadableSettings`,
`TestSyncStopsOnUnreadableProviders`, `TestSyncStopsOnUnreadableSettings`,
#2269).

Switching services keeps all inactive kinds in `Config.Servers`. `Other`
remains the most recently left server for compatibility with older saved
configurations and clients. Only the active server is used. Empty credential
fields reuse a saved credential only for its matching account; GitHub
restricts reuse to the same owner/repo. Branch or prefix changes within that
repository can keep the token. The branch is part of the sync state key.

## GitHub constraints and errors

- The repository must exist and be private: `prepare` refuses one whose
  repository read doesn't say `"private": true`, since a public history hands
  every sealed version to anyone to guess the passphrase against
  (`TestGitHubPublicRepositoryRefused`). In an empty repository, the first encrypted
  backup initializes its default branch through the Contents API. A selected
  branch other than the default must already exist. The token needs repository
  Contents read and write.
- Backups are `<prefix>/magpie/magpie.magpie-backup`; optional usage files
  live beside them under `usage/`. Changes create commits, and unchanged
  setup does not create another backup commit.
- Tokens are separate from the library's GitHub token, sent only to
  `https://api.github.com`, and must differ from the encryption passphrase.
  Repository history retains older encrypted versions.
- Repository and branch access is checked before a file's 404 can mean
  "no backup yet." Failed reads and malformed metadata fail sync.
- Only the ref endpoint's explicit `409: Git Repository is empty.` allows
  initialization, after repository access and the default branch are checked.
  The initial write omits `branch` so GitHub creates its default branch; other
  conflicts and missing branches are not treated as an empty repository.
- Large files are downloaded by blob SHA, preserving the version that was
  read. Sync files are limited to 64 MiB. Usage listing fails at the Contents
  API's 1,000-file limit rather than treating a truncated list as complete.
- A stale SHA or a racing initial creation returns `errChanged`. Other 422
  responses stay actionable errors. Authentication, permission and branch
  failures remain distinct from missing files. Rate limits use sync backoff.
- Request archival remains an S3-only feature; its objects are separate from
  the encrypted setup and usage files.

## Verification

```sh
go test -tags nogui ./internal/davsync
go test -tags nogui ./internal/cli -run 'TestGitHubCmd|TestWebdavCmd|TestS3Cmd'
node --test internal/gui/tests/github-sync.test.cjs internal/gui/tests/s3-sync.test.cjs internal/gui/tests/sync-other-kind.test.cjs
```

`TestASyncDoesNotUndoARenameMadeMeanwhile` and
`TestARestoreRefusesOverARenameMadeMeanwhile` (`internal/davsync`) cover a
rename made between collect and bring. `TestACollectedBundleCarriesItsBase`
(`internal/backup`) covers a collected bundle's base and an opened bundle's
missing one. `TestAMirrorOnAnOlderBaseIsRefused`,
`TestAManagedMirrorKeepsInstancesByID` and `TestARestoreMergesAgainOnce`
(`internal/provider`) cover the provider side.

`TestGitHubEmptyRepository` covers the first encrypted backup initializing an
empty repository and an unchanged second sync. `TestGitHubEmptyRepositoryFailures`
covers missing branches, unrelated ref conflicts and another computer winning
the initial write. `TestGitHubNotBackup` covers backend-specific recovery
advice when the repository holds an empty or foreign file.

Go tests use `testenv` homes and HTTP fixtures. Browser tests use isolated
API fixtures in Chromium and WebKit. A live GitHub write requires a test
repository and token; fixture tests do not establish that a particular
account's permissions or repository rules allow commits.
