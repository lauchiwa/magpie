# Database and Persistence Guidelines

## Scope

Magpie does not have an application-owned database, ORM, or migration directory. `modernc.org/sqlite` is used to inspect or carefully update databases owned by agents and integrations (Codex, Kiro, Claude Desktop/import sources, Hermes, OpenCode, Cursor). Treat those files as external state with version-dependent schemas.

Examples: `internal/agent/cindy.go`, `internal/provider/importapps.go`, `internal/provider/kiro.go`, and `internal/sessions/{codex_provider,cursor,hermes,opencode}.go`.

## SQLite access

Use `database/sql` with the `modernc.org/sqlite` driver. Prefer read-only connections for indexing/importing:

```go
u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&_pragma=busy_timeout(1000)"}
db, err := sql.Open("sqlite", u.String())
```

This pattern appears in `internal/agent/cindy.go` and `internal/provider/importapps.go`. Use `immutable=1` only when the source is known to be a stable copy (`internal/sessions/cursor.go`). Configure a bounded `_pragma=busy_timeout(...)` for live agent databases; do not hang indefinitely on a lock.

Use raw SQL with parameter placeholders (`?`), `QueryRow`/`Query`, `Exec`, and explicit `BeginTx` snapshots where a consistent read matters (`internal/sessions/hermes.go`). Always close `*sql.DB`, rows, and transactions. Check schema capabilities before touching a vendor database: `moveCodexThread` in `internal/sessions/codex_provider.go` checks `pragma_table_info` before updating `threads`.

## Updates to external databases

An update must be narrowly scoped, parameterized, and preserve unknown schema versions. `moveCodexThread` updates only the matching `id` and leaves a database without the expected table/columns alone. Do not assume a vendor's schema is stable or create a migration for a vendor-owned database.

Tests may create minimal in-memory or temporary SQLite schemas matching the real integration. Keep schemas close to the tests that need them (`internal/sessions/*_test.go`, `internal/provider/importapps_test.go`) and use realistic column names/types.

## Application state is files, not SQL

Magpie-owned settings, accounts, migrations, keys, caches, and usage are JSON/JSONL/TOML/YAML files under paths resolved by `internal/appdir` and `internal/settings`. Use the existing readers (`filememo`, `lastgood`, `edit`) rather than adding a new persistence abstraction.

Writes that can affect user state go through `internal/steady.WriteFile`: create a sibling temp file, write and `Sync`, apply the requested permission, close, then rename atomically. `internal/provider/logins.go` uses this for private account files with `0700` directories and `0600` files. Preserve a valid previous generation with `lastgood.Keep` and recover with `lastgood.Fallback` where the owning package already uses that pattern.

## Scenario: consistent multi-file reads

### 1. Scope / trigger
Use when combining providers, identity profiles and bindings across concurrent processes. R1 G1 review found that stable primary/backup bytes can hide two supported writes (unbind then bind); byte equality is not a generation protocol.

### 2. Signatures
Existing owners: `lastgood.Read(path, valid)`, `lastgood.Fallback(path, bad, valid)`, `lastgood.Keep(path, valid, perm)`; provider `writePrivate` delegates link resolution to `edit.Target`. Lifecycle (R1 G1r): `lifecycle.Begin(mode) (Mark, error)` and `Mark.Validate() error` bracket one closed capture; `Begin` classifies `lifecycle.json` as valid/absent/corrupt/unsupported/io (`*MetaError`, `errors.Is/As`). Read-only mode never creates the lock, recovers or writes (`*PendingError` wraps `ErrBusy`). There is no arbitrary-callback `View`.

### 3. Contracts
A coherent read needs either a common reader/writer lock over snapshot capture, or a monotonic generation advanced by every writer that changes the cross-file mapping, with journal checks around reads. R1 uses the latter for healthy metadata: provider commits advance gen; an identity-only write does not, so a capture must take exactly one atomic owner read of identities.json and derive every projection from it. With absent metadata, `Validate` cannot see a completed legacy multi-file commit, so composite views take `Lock` after `Begin` (never call `Begin` while holding `Lock`). Capture local state before publishing caches; never put All/Accounts or network work under a lock they can reenter. Backup selection stays with the owning decoder. Providers writes preserve symlinks and logical backup locations.

### 4. Validation / error matrix
| State | Required result |
| --- | --- |
| Valid primary, unusable unused backup | Primary read still succeeds; a later write may separately fail to keep a backup. |
| Primary absent / I/O error / invalid content | Preserve the owning reader’s distinct missing, error and fallback rules. |
| Before/after bytes equal following two protocol writes | Not sufficient evidence of a consistent combined snapshot. |
| Bad metadata / unsupported version / unreadable metadata | Distinguish policy categories; never silently downgrade an already-managed object. |
| Windows replacement momentarily absent | Use steady bounded retry; do not infer an empty store from an initial Lstat failure. |

### 5. Good / base / bad cases
Base: owner reads valid primary and ignores bak. Good: all values derive from one protected snapshot. Bad: inspect all backups on every read, or accept mixed values because A→B→A restored both primary and bak.

### 6. Required tests
Use controlled subprocess pauses across the tail rechecks, two identity writes during capture, valid-primary/unusable-bak fixtures, metadata policy distinctions and linked target replacement/recovery. Assert actual output/errors, not just callback count. Evidence: `internal/lifecycle` tests (single-read ABA plus a two-read counterexample, unused directory `.bak`, metadata classes, read-only zero side effects, linked providers target recheck) and `keep_test.go:TestCommitKeepMatchesLastgoodKeep`, which compares lifecycle's backup step against `lastgood.Keep`.

### 7. Wrong vs correct
Wrong: `before == after` therefore a callback spanning several file reads was coherent. Correct: bracket one capture with `Begin`/`Validate`, read each owner file once, and project from that snapshot; under absent metadata hold `Lock` for composite views.

## Scenario: provider commit guards and held reads

### 1. Scope / trigger
Changes to `internal/provider/lifecycle_commit.go`, its callees, or the provider/filememo read caches. A journal write bypasses `writePrivate`; a direct-call AST scan cannot prove that its helpers never reenter provider discovery.

### 2. Signatures
`commit(f file, in intent) error` delegates its locked section to `commitHeld(f, b, in, unlock) error`. `filememo.Hold() func()` holds parses; `filememo.Forget()` invalidates them. `notUnderCommit(name string)` calls a nil-by-default atomic test hook; stack walking and violation records live only in `lifecycle_guard_test.go`.

### 3. Contracts
- Invalidate filememo before releasing the commit lock, on success and partial failure, without relying on `catalog.Touched`/`catalog.Changed`. The locked section uses `defer unlock(); defer filememo.Forget()` (LIFO).
- Only `commitLocked`'s direct raw `read()` belongs under this lock. Discovery, signing, combined identity views and cached provider/catalog entry points do not. A guard checks the calling goroutine's stack, not a process-wide count that would flag unrelated readers.
- Check every participating `providers.json.bak` / `identities.json.bak` with `usableBak` before the first `lifecycle.Prepare`. A non-regular backup is refused with its path and `move it aside and try again`. This check is journal-only: the legacy fast path (`writeLegacy`, no binding change) keeps the original `lastgood.Keep` handling, which replaces a linked `.bak` (`TestLegacyStoreKeepsAnUnusualBackupAsBefore`).
- Legacy writes without a binding change preserve the old Keep-before-write sequence and exact bytes; Recovery A and disk schemas stay unchanged.

### 4. Validation / error matrix
| Condition | Required result |
| --- | --- |
| Held, settled parse; successful or partly failed journal write | At unlock, the next filememo read matches disk. |
| Commit helper indirectly calls All/Accounts/read entry | Runtime guard test fails before reentry can recover its own journal. |
| Other goroutine reads during a commit | No false guard violation; normal coordination still applies. |
| Participating backup is a directory, symlink or dangling symlink | Actionable error before Prepare; primary, backup, evidence and link target unchanged. |
| Valid primary, unused bad backup | Reading still succeeds; no broader backup validation on reads. |

### 5. Good / base / bad cases
Base: legacy Keep then write, no metadata bootstrap. Good: explicit invalidation before unlock and callbacks afterwards. Bad: a later callback or the one-second Hold expiry hides a missing invalidation.

### 6. Required tests
`lifecycle_legacy_test.go` compares every intermediate directory snapshot against the pre-lifecycle store (bytes, modes, backup/evidence counts). `lifecycle_guard_test.go` covers indirect and warm-cache entries, independent readers, and restores all hooks with `t.Cleanup`. `lifecycle_hold_test.go` uses `testing/synctest`, old mtimes and a sentinel to prove Hold is active, then checks bytes at unlock, before callbacks. `TestAnUnusableBackupRefusesTheWrite` covers both owners, legacy/managed and all three non-regular backup kinds. Break write order/bytes, indirect reads, invalidation and precheck order separately: each must yield a behavioral FAIL, not a build error or timeout.

### 7. Wrong vs correct
Wrong: assert only the final primary, scan only direct calls, or read after callbacks/real-time expiry. Correct: compare each persisted generation, exercise runtime transitive calls (including held hits), and freeze time while asserting immediately at unlock. See the follow-up evidence in `tasks/10-14-identity-lifecycle/research/g2b2-implementation.md` under `.trellis/`.

## Scenario: lifecycle bootstrap and adoption

### 1. Scope / trigger
Changes to `internal/provider/lifecycle_ready.go`, `commitManaged`'s digest handling, `lifecycle.Meta` fields, or `magpie identity reconcile`.

### 2. Signatures
`provider.ReadyLifecycle() error` is the only bootstrap entry (G3b-2 calls it once from `cli/main.go:run()`). `lifecycle.Meta` gains per-row digests and `Unreconciled` (omitempty, version unchanged because no release has written v1; the next field after a release needs a version bump). `magpie identity reconcile [--yes [--keep|--drop]]`.

### 3. Contracts
- Ordinary reads and writes never bootstrap: a test HOME stays legacy. Missing providers.json, or one read from its `.bak`, gets no meta.
- Corrupt meta: kept aside, then a new epoch (old managed values become `ErrStale`). Unsupported or unreadable meta: untouched, error returned.
- An unwritable folder (permission or read-only, and no journal written yet) stays legacy or pending with no error, so start-up still works. A failure after the journal is written goes to Recover.
- A digest mismatch is adopted under the lock, meta only: changed rows keep their instance and get rev+1; added or removed ids that touch no binding get a new instance or a tombstone; ones that do are `Unreconciled`. Signing returns `ErrUnknown` only for an unreconciled id that is still bound. The request path never writes.
- `reconcile` previews with zero writes (no lock file, no recovery, no evidence). `--yes` keeps the old meta and any unfinished journal aside first, and requires `--keep` or `--drop` while undecided bindings exist. `--help` and unknown flags write nothing.

### 4. Required tests
`lifecycle_ready_test.go` (each meta state, unwritable folder via icacls/chmod), `lifecycle_process_test.go` (R1.10: parent holds old Provider values while a child process renames, deletes, recreates and binds; assertions on the headers httptest received), the Recovery A combination, and `internal/cli/identity_reconcile_cli_test.go`. Signing must leave `lifecycle.json` byte-identical. Evidence: `.trellis/tasks/10-14-identity-lifecycle/research/g3a-implementation.md` and `g3a-check.md`.

## Scenario: checked provider writes

### 1. Scope / trigger
Code in `internal/provider/lifecycle_api.go` that writes a provider or a binding on behalf of a value read earlier: the Checked API, `Expect`, the learners, AddCopy, and Rename. Since G3b-2, `run()` calls `ReadyLifecycle` at start-up, so a real user's HOME gets `lifecycle.json`. Test HOMEs stay legacy, which means zero-marker, unless the test calls `ReadyLifecycle` itself.

### 2. Signatures
- `SaveChecked(p, opts...) (Provider, error)`
- `DeleteChecked(p) error`
- `RenameChecked(p, to) (Provider, error)`
- `BindIdentity(p, profile) error`, `UnbindIdentity(p) error`, `UnbindOrphan(id) error`
- `Expect(p) WriteOption`. Upstream `Save`, `Delete`, `Rename`, `add` and `LearnAuthHeader` take `opts ...WriteOption`; their call sites without options are unchanged.
- `*FollowUpError{Committed bool, Step string, Err error}` unwraps to `Err`.
- On the identity side, `identity.PlanBind` and `PlanUnbind` only read under `mu`.

### 3. Contracts
- The marker decides the check:
  - managed: the instance must match. Whole-draft writes (Save, Rename) also compare the rev, or the row digest when the file and meta disagreed at read time.
  - unreconciled: the id must still be unknown and the row unchanged. The adoption happens in the same journal.
  - transient: Save goes by the current id. Delete, Rename, Bind, Unbind and learner writes return `errTransient`.
  - unknown: always `*StaleError`.
  - zero: by the current id, with no check.
- A marked value whose meta is now nil, or whose epoch differs, returns `*StaleError`. A zero marker never produces an expect, so legacy writes stay byte-identical to upstream.
- A managed Save whose `p.ID` differs from `issuedID` is refused. Changing an id is a Rename.
- The Checked result's marker and row come from the in-lock commit (`intent.out`), never from a later read. An account's result is its stored picks with a zero marker.
- Rename commits first. The fetched-list move, `catalog.Touched` and settings come after. A failure after the commit is `*FollowUpError{Committed:true}`: redo only `Step` and never replay the rename. A commit failure leaves the catalog files untouched.
- Under the lifecycle lock, Bind and Unbind call only `PlanBind`/`PlanUnbind` plus `commitPlan`. Never call the identity writers there: they take the lock themselves.
- Bind refuses to write when providers.json is missing or was read from its `.bak`. An explicit marked Bind or Unbind, and `UnbindOrphan`, clear that id's `Unreconciled`. A zero-marker `identity.Bind`/`Unbind` does not.
- `UnbindOrphan` refuses an id that has a row in the in-lock `read()`, in its raw or normalized form.
- Learners and AddCopy sources pass `Expect(p)`. A write whose originating instance no longer holds the id is dropped. A transient value never writes, with or without meta. AddCopy clears `Was`.
- Callers write the value they read, never a fresh `Find` by id. Since G4a:
  - CLI `set` is `SaveChecked` followed by `agent.RenameChecked` on the saved value.
  - `rm` is `DeleteChecked`.
  - `off`/`on` are `SetOff(id, off, Expect(p))`.
  - `identity bind`/`unbind` are `BindIdentity`/`UnbindIdentity`, and `unbind` falls back to `UnbindOrphan` for an id no provider has.
  - The TUI keeps the confirmed row (`confirmProv`) and saves its edits with `Expect` of the value the list read.
  - The account, queue, RPM, quota-rest, model-name/price setters and refetch still go by id. Adding a check there means giving the setter `opts`.
- The GUI writes the value the page read (`internal/gui/lifecycle_fork.go`):
  - `providerJSON.rev` is `p.Token()`, an opaque marker; `""` when the value has none. A request's `rev` comes back through `provider.FromToken(p, rev)`. No `rev` is the zero marker, so an old page writes by id as before. A `rev` that doesn't decode, or was issued for another id, is unknown and every checked write of it is stale, never a write by id.
  - Save is `SaveChecked` (or `AddChecked`/`AddCopyChecked`), then `renameSaved` → `agent.RenameChecked` on the committed value, then keys, model prefs and outputs on that one snapshot: `addKeysTo` (AddKeys with `Expect`), and `stepOn`/`provider.CheckCurrent(snap)` before each settings.json step, which `Expect` can't guard. `savedFind` refuses a refetch when the id holds another instance.
  - A step after the commit that fails because the instance moved, or is left to its journal, is a `*FollowUpError` naming the step (`afterSave`); only the rename's settings/agents step is redone, once, on the server. Nothing else retries; the page re-reads and keeps its draft.
  - delete, off/on, bind/unbind carry `rev` the same way. `identityState` reads one snapshot. A sync/restore conflict is stale and isn't retried.
  - Not guarded: the window between `CheckCurrent` and the settings write; `/api/keys/*` and the other by-id setters, as in the CLI/TUI.
- `agent.RenameChecked` computes the agents' moves before the commit. It moves them only once the commit is in, including when the settings step failed. A refusal or `ErrPending` moves nothing. An upstream change to `RenameProvider`'s moves goes into `renameMoves`/`applyRenameMoves`, which both renames share.

### 4. Validation / error matrix
| Condition | Result |
| --- | --- |
| Instance gone, rev or digest moved, epoch changed, meta now nil | `*StaleError{ID}` (`errors.Is(err, ErrStale)`) |
| Rename target taken or source gone at commit | `*ConflictError` |
| Add loses `freeID` to another process | One retry outside the lock, then `*ConflictError` |
| Transient value deleted, renamed, bound, or learned | `errTransient`, nothing written |
| Settings save after a committed rename fails | `*FollowUpError{Committed:true, Step}` |
| Bind with providers.json missing or read from `.bak` | Refused, nothing written |

### 5. Good / base / bad cases
- Base: no `lifecycle.json`. Upstream tests pass unchanged, and the writes match e4cf6bf7.
- Good: an old value from `Find` is renamed by another process. `BindIdentity` follows the instance to the new id. `SaveChecked` returns `*StaleError`.
- Bad: a caller retries a `FollowUpError` by calling `Rename(from, to)` again.

### 6. Tests required
- `lifecycle_api_test.go` covers each table cell.
- `lifecycle_api_check_test.go` covers bindLocked's `identityRefused`, orphanLocked's `.bak` refusal, saveNew clearing `p.life`, and legacy `Expect(transient)`.
- `identity/planbind_test.go` checks that the plan functions write nothing.
- The AST and runtime guard lists in `lifecycle_guard_test.go` name every new lock-held function.
- Break each guard by hand (`-overlay`). Each must give a behavioral FAIL. The 30-row table is in `research/g3b1-implementation.md`, and the spot checks are in `g3b1-check.md`.

### 7. Wrong vs correct
- Wrong: build a `Provider{ID: id}` literal for a bound provider and Save or Bind it. The zero marker skips the instance check and the unreconciled check.
- Correct: pass the value from `All`/`Find`, or the returned Checked value, and handle `*StaleError` by rereading.

## Scenario: sync and restore over the lifecycle

### 1. Scope / trigger
Changes to `internal/provider/{backup,lifecycle_sync}.go` (Mirror, Restore, `FileBase`), `backup.Collect`/`Bundle.Base`, davsync's `bring`/`syncNow`/Restore/Undo, or the start-up hook `internal/cli/lifecycle_cli.go`.

### 2. Signatures
- `provider.StoredBase() ([]Provider, FileBase, error)`
- `provider.OnBase(FileBase) WriteOption`
- `Mirror(ps, gs, opts ...WriteOption)`
- `provider.Restore(ps)`
- `backup.Bundle.Base()`: an unexported field. It is never sealed, so an opened bundle's base is zero.
- davsync `bring(b, part, opts ...provider.WriteOption)`, `onCollected(local)` and `changedHere(err)`, all in `lifecycle_fork.go`.
- `readyAtStart(startArgs)`: one line in `startUp`, after `settings.Migrate()` and before `provider.Hold()`. `run` sets `startArgs = args` just before calling `startUp()`.

### 3. Contracts
- A sync decides from what was collected. Mirror with `OnBase(collect's base)` is refused with `*ConflictError` when providers.json's bytes changed since the collect. It is also refused when lifecycle.json's presence, epoch or gen changed. That catches a delete followed by a recreate with the same bytes. An unreadable meta at collect is a conflict, never permission. A zero `FileBase` checks nothing, so upstream calls are unchanged.
- The check runs under the lock, before any part is written. Only icons, which are add-only, and the sync-folder copies are written before a refusal.
- `syncNow` reruns the whole round once on a conflict, and a second conflict fails that round. davsync Restore/Undo return the conflict wrapped in `bringing in the providers: …`. `provider.Restore` rereads and merges again once.
- Sync and restore never carry lifecycle.json or identities.json:
  - A row matched by ID keeps its instance and binding.
  - A rename made on another computer arrives as a delete plus an add, and the binding stays behind.
  - A deleted row leaves a tombstone.
- On a first sync, or when the remote is newer, the server's providers win, including over a local rename. The local copy stays in `-this-computer`.
- Start-up never fails because of `readyAtStart`: it only prints `magpie: lifecycle: …`. It is skipped for `identity(ies) reconcile` and under `MAGPIE_TEST_SANDBOX`, unless a test sets `readyInSandbox`. `--help` and unknown flags return before it.
- commitBinding adopts through the strict `boundLocked` and never through identities.json.bak. Each `commitPlan` caller names its own error prefix.
- A rename whose journal write lands but whose commit then fails returns the error wrapped in `journalPending`. In that case the fetched list moves anyway and `catalog.Touched` runs, but settings do not change. The next Recover rolls the rename forward.

### 4. Validation / error matrix
| Condition | Result |
| --- | --- |
| providers.json or lifecycle epoch/gen moved between collect and bring | `*ConflictError`, nothing written; sync reruns once |
| lifecycle.json unreadable at collect | `*ConflictError` |
| Restore races another write | One reread and merge, then `*ConflictError` |
| Adoption with identities.json blank or corrupt | Refused, the `.bak` is not used |
| Journal written, commit failed | Original error (`errors.Is` holds), fetched list moved, rename finished by the next Recover |

### 5. Good / base / bad cases
- Base: no lifecycle.json and a zero `FileBase`. Mirror, Restore and `bring(backup.Bundle{}, "providers")` behave as upstream.
- Good: the user renames a provider while a sync runs. The Mirror is refused and the rerun collects the rename.
- Bad: a sync reads providers.json again at bring time and mirrors the old decision over the rename.

### 6. Tests required
- `provider/lifecycle_sync_test.go` and `lifecycle_handoff_test.go`, including `lifecycle_handoff_check_test.go`'s journal-fails-first case.
- `backup/lifecycle_base_test.go`, `davsync/lifecycle_fork_test.go` (uses the `afterCollect` seam) and `cli/lifecycle_cli_test.go` (`TestHelpReadiesNothing` needs a stored provider to go red).
- Breakage evidence is in `research/g3b2-implementation.md` (15 rows) and `g3b2-check.md` (7 more).

### 7. Wrong vs correct
- Wrong: retry a Mirror conflict in a loop, or drop the base so the write goes through.
- Correct: collect again and decide again, at most once per round. Surface the second conflict.

## Naming and safety

External SQL names follow the source application's schema (`threads`, `model_provider`, `custom_providers`, etc.); do not rename columns to fit Magpie style. For Magpie JSON, preserve existing field names and stable IDs; migrations live in the owning package (`internal/provider/migrate*.go`, `internal/settings`). Never treat a missing, unreadable, or invalid file as an empty database/config if doing so could overwrite user state.

## Anti-patterns

- Do not introduce an ORM or application database without a separate design decision.
- Do not write to an agent database through a default read/write connection when read-only access is sufficient.
- Do not use string interpolation for SQL values.
- Do not overwrite a user-owned file after a failed parse, hash, or validation; preserve it and surface/log the failure.
- Do not invent a generic migration for an external vendor schema; capability-check and no-op when unsupported.
