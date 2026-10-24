# Fork releases

This repository is a fork of upstream magpie. It builds and publishes its own
releases, on its own Releases page. Upstream's repositories are never written
to: the release workflow checks out this repository at the tag and uploads the
assets to the repository it runs in (`${{ github.repository }}`).

Upstream's own release workflow hands the version to a separate release
repository, which builds upstream's source and publishes there. That path is
gone here, and `scripts/release-workflow.test.mjs` fails if it comes back.

## The version

```text
vX.Y.Z-rc.N
```

- `X.Y.Z` is the upstream magpie version this fork synced from, copied
  verbatim.
- `N` counts this fork's releases on top of that upstream version, from `0`.
  `rc.0` means "synced, no fork changes released yet".

```text
synced upstream 0.1.1182     v0.1.1182-rc.0
released a fork change       v0.1.1182-rc.1
again                        v0.1.1182-rc.2
tenth                        v0.1.1182-rc.10
upstream ships 0.1.1183      v0.1.1183-rc.0
```

A bare `vX.Y.Z` is never published from this fork. SemVer ranks a pre-release
below the release it leads up to (§11), and the updater follows that
(`internal/update`: `Newer`, `comparePre`). A bare `0.1.1182` would therefore
outrank every later `0.1.1182-rc.N` and pin installs to it for good. Skipping
the bare version means the ceiling never exists and the order stays monotonic;
`TestForkReleaseTagsIncrease` and `TestBareUpstreamVersionOutranksTheForksReleases`
in [`internal/update/fork_version_test.go`](../internal/update/fork_version_test.go)
check both halves of that against the updater's own comparison.

The counter stays purely numeric: SemVer compares all-digit identifiers as
numbers and everything else as text, so `rc.10 > rc.2` holds while an
`rc.10a` would sort below `rc.2`.

Every release is marked as a pre-release on GitHub, because every version this
fork publishes carries `-rc.N`.

## Releasing

Find the upstream version this fork is synced from, then let the script pick
the next free tag. It reads this repository's tags, so it never reuses one:

```sh
git fetch --tags
node scripts/release-version.mjs next 0.1.1182   # prints v0.1.1182-rc.N
```

Tag that commit and push the tag. The push starts the Release workflow, which
checks the tag, builds macOS (both architectures), the Windows app, the Linux
app (both architectures) and the terminal-only builds, launches the Windows and
Linux apps to see that each serves the gateway, and publishes the assets with
their `SHA256SUMS` to this repository.

Before tagging, the repository's own standards still apply: a CI Test run on
that commit must have finished green, and the tag must be on `main`
([code standards](code-standards.md)). The "16 assets in the upstream release
repository" line there describes upstream's release, not this fork's.

A tag of any other shape starts nothing: the workflow listens for `v*-rc.*`
only, and `release-version.mjs check` refuses anything that is not
`vX.Y.Z-rc.N`, naming the bare-version case in the error.

`workflow_dispatch` builds an existing tag again, with a *Publish* switch for
a dry run that builds without releasing.

### The notes

`scripts/release-notes.mjs render <tag> <owner/name>` writes the notes. They
list this fork's own commits since the previous release, and link upstream's
compare page for the upstream part instead of listing it. A commit is the
fork's when upstream's `vX.Y.Z` (the tag's base) can't reach it. That is
decided by ancestry only, never by author or message, so a fork PR's merge
and a sync's merge are listed with the commits they brought. For
`v0.1.1305-rc.0` that is 76 commits; `git log PREV..TAG --no-merges`, which
the workflow used before, listed 862.

The previous release is the highest `vX.Y.Z-rc.N` below the tag, compared as
numbers, among the tags the tag's commit contains. A tag on a branch that was
never merged is not a candidate. The upstream span the notes name is the
previous tag's base as labelled, so a mis-tagged release (below) is named by
its label and not checked.

The publish job fetches upstream's base tag read-only, without this
repository's token, into `refs/magpie-notes/upstream/vX.Y.Z`. This
repository's tags and checkout are left as they are. Without that ref, or
when the tag doesn't contain it, the script fails and nothing is published.
`v0.1.1185-rc.0` is such a tag: it was published before this script, and its
notes are not written again.

To preview the notes locally, pass a ref that holds upstream's tag:

```sh
node scripts/release-notes.mjs render v0.1.1307-rc.0 lauchiwa/magpie v0.1.1307
```

## A mis-tagged release

A tag with the wrong base does not need deleting. A correctly based tag
outranks it, because the numeric part decides when it differs:

```text
v0.1.1181-rc.9 < v0.1.1182-rc.0 < v0.1.1182-rc.1
```

Leave the stale tag unused and publish the correct one. Deleting a pushed tag
or a published release is destructive and can confuse an install that already
saw it.

## Syncing upstream

A sync merges an upstream tag into a `sync-vX.Y.Z` branch and reaches `main`
through a PR. The fork keeps its logic in files upstream doesn't have
(`internal/lifecycle/*`, `*_fork.go`, `internal/provider/lifecycle_*.go`,
`internal/provider/identity.go`, `internal/identity/*`,
`internal/gui/identity.go`, `internal/cli/identity_cli.go`,
`internal/cli/lifecycle_cli.go`, `internal/gui/assets/lifecycle.js`,
`internal/gui/assets/identity.js`, `internal/gui/assets/identity.css`,
`internal/gui/core_budget_fork_test.go`).
Upstream files carry only short hooks: field declarations, delegating calls,
variadic options and single request-body fields, many marked `fork:`. After
the merge, `git diff <previous upstream tag> HEAD` over the upstream files
lists them, and `git grep -n "fork:"` finds the marked ones. A conflict there
is resolved by keeping upstream's change and putting the hook back.

The sync PR records each item below as checked, with what was run:

- **The whole suite.** `go test -tags nogui ./...` and the GUI suite in
  Chromium and WebKit ([code standards](code-standards.md)).
  `TestNoProductionStoreOfALiteralFile` fails on a new production
  `store(file{…})`.
- **Provider writes** (`internal/provider`): `store` is still
  `commit(f, intent{})` in `provider.go`. `Save`, `AddCopy`, `Delete`,
  `Rename` (`rename.go`), `Mirror` (`backup.go`), `AddKeys` (`keys.go`) and
  `SetAccountModels` (`account_models.go`) still take `opts ...WriteOption`, and `AddChecked`/`AddCopyChecked`
  (`lifecycle_token.go`) still reach `add`/`AddCopy`. The learners (`LearnAuthHeader`, `LearnZhipuCoding`,
  `fixV1`'s write in `models.go`, `dropGonePicks`) still pass `Expect(p)`. A new write path upstream adds goes through `store` or
  `storeWith`, never around it. A change to `Rename`, `Was` or `Delete`
  semantics is re-read against `lifecycle_commit.go`'s diff of rows.
- **Who signs.** A new caller of `Provider.Sign` or `identityListHeaders`,
  or a new way a `Provider` is handed out (beyond `All`, `Find` and the
  held reads), is checked for its lifecycle marker. `notUnderCommit` stays
  in `provider.go`'s `load`, `read` and `All`, `account.go`'s `Accounts`
  and `hold.go`'s `heldEntries`.
- **Start-up.** `cli/main.go`'s `run` still calls `readyAtStart(args)`
  right after `settings.Migrate()`, and a new early return before it is
  something that writes no provider.
- **Sync and backup.** `backup.Collect` still keeps `StoredBase`'s base
  (`backup/backup.go`); `davsync.go`'s `bring` still passes
  `onCollected(local)` at each of its calls, and `syncNow` still retries on
  `changedHere`.
- **Command line and TUI.** `cli/providers_cli.go` still uses `saveFound`,
  `deleteFound` and `SetOff(…, Expect(*p))`. `tui/pages.go` still deletes
  `confirmProv` and saves line edits with `Expect`.
- **GUI, Go side.** `gui/providers.go`'s save chain (`AddChecked` or
  `SaveChecked`, `renameSaved`, `addKeysTo`, `stepOn`, `savedFind`), its
  `DeleteChecked`, `providerJSON.Rev`, its `accountmodels` passing
  `accountModelsAsRead(req.Rev)`; `gui/api.go`'s `fail()` still calls
  `lifecycleFail`.
- **GUI, page.** `index.html` still loads `lifecycle.js` and `identity.js`
  before `app.js`, `identity.css` right after `app.css`, and keeps the
  `#identityHead`/`#identityPanel` nodes. In `app.js`, `loadProvidersNow`
  still asks `identityRead()` beside the list and hands it to
  `identityTake`, `renderProviders` calls `renderIdentityProfiles()`, and
  `drawEditor` appends `identityField(p)`.
  In `app.js`: `api()` still calls `lifecycleError(err, data)`; `draftOf`
  still carries `rev`, and both editors' Save bodies, both Removes,
  `switchProvider` and the identity bind/unbind still send it;
  `duplicateProvider` still clears it; `providerAction`'s catch still
  closes on `committed`/`pending` and rereads on `stale`.
  `saveAccountModelsDraft(picked, body)` still sends the picks through
  `accountModelsAsRead`, with the Save body's `rev`, and the Save then
  carries the `rev` their answer gave.
  `provider-lifecycle-rev.test.cjs` covers each of these.
  `TestCoreScriptBudget` skips the fork's scripts (`forkScript`), so its
  budgets and comments are upstream's as they come; `TestForkScriptBudget`
  weighs `forkScripts` instead. The hooks inside `app.js` are weighed on
  top of upstream's two budgets as `forkHookBudget`, so a sync never raises
  upstream's numbers; a hook that needs more raises that one.
- **Plugin host.** `plugin/host.go`'s `headRead` still calls
  `afterHeadMiss` (`host_fork.go`) after a pop that found no head, and
  pops again when it finds the answer, before calling the reply "no
  response"; `TestHeadPushedBeforeTheAnswerIsRead` fails without it.
- **GUI strings.** `tests/strings.cjs` still reads each `FORK` file as one
  with its own words, and `tests/i18n-keys.test.cjs` still skips them in
  "a plugin's words are its own" (they are always loaded, never off like a
  plugin). `TestForkScriptBudget` fails when `FORK` and `forkScripts` differ.

What R1 doesn't guarantee stays listed in
[providers and accounts](subsystems/providers-accounts.md#coordinated-provider-writes):
the id-keyed setters, and the moment between `CheckCurrent` and the
`settings.json` write after it.

## What still points at upstream

The download page and the install script are upstream's: `usemagpie.ai` and
`site/` describe upstream's releases, and `install.sh` downloads them. This
fork's builds are installed from its own Releases page.

Upstream's site also serves the JSON update feed its own builds ask
(`usemagpie.ai/api/latest`). This fork does not use it; see below.

A message that sends someone to download a build by hand (a new version
Windows wouldn't start, `magpie update` from a build from source or from an
app it can't replace) names this fork's Releases page (`update.Downloads`),
not `usemagpie.ai`: a build from there is upstream's, which replaces this
fork's and then updates from upstream.

A release's notes link upstream's release and compare pages for the upstream
version it was synced from: that part of the build is upstream's.

The container hint still names `ghcr.io/yetone/magpie:latest`, upstream's
image. This fork's Docker workflow publishes `ghcr.io/lauchiwa/magpie`, but
only as `edge` and per version: `latest` is tagged for a bare version, which
this fork never publishes, so there is no fork tag for the hint to name yet.

## Signing

Nothing is signed with a paid certificate. There is no Apple Developer
membership behind this fork, so:

- **macOS**: the app is ad-hoc signed (`codesign --force --deep --sign -`),
  which is free and needs no secrets, and the dmg is not notarised. The
  hardened runtime is left off: it is only meaningful with a Developer ID.
- **Windows and Linux**: not signed, as upstream's aren't either.

Each system asks before running an unsigned build the first time, and the
release notes say how to let it through (right-click → Open on macOS, *More
info* → *Run anyway* for SmartScreen).

Ad-hoc signing does not weaken the updater's own check. A macOS update is
accepted only if the downloaded app's signature is intact and made by the
same team as the app installed (`internal/update`: `sameSigner`). An ad-hoc
build reports no team, so an ad-hoc build updates an ad-hoc build, while an
app changed after signing is still refused — both checked on a real
`codesign` in
[`internal/update/adhoc_darwin_test.go`](../internal/update/adhoc_darwin_test.go).

If a Developer ID is ever bought, signing and notarisation go back into the
`mac` job; the updater needs no change, though the first notarised release
cannot update an ad-hoc install (the teams differ), so that one is installed
by hand.

## Updating

An installed build updates itself from this fork's own releases.

Upstream's updater asks a JSON feed that a Cloudflare worker builds from
upstream's release repository. This fork has no such feed, so it asks GitHub
for the releases of its own repository and builds the same value itself:
the newest version, its notes, and every file with the SHA-256 from the
release's `SHA256SUMS` ([`internal/update/fork.go`](../internal/update/fork.go)).
Everything after that is upstream's code unchanged, so a file that doesn't
match its checksum is still never installed.

Two details of this fork's releases shape that code:

- Every version is `vX.Y.Z-rc.N`, which GitHub counts as a pre-release.
  GitHub's "latest release" endpoint leaves pre-releases out and so reports
  nothing here at all. The releases are listed instead and the newest is
  picked with `Newer`, the same comparison used everywhere else.
- The repository has to stay public: an install downloads from it with no
  credentials. GitHub also limits unauthenticated callers by address, which
  the error message says plainly when it happens.
- The release list and `SHA256SUMS` are asked of GitHub alone, never of a
  fallback mirror (`MAGPIE_MIRRORS`, `source.DoOfficial`). They say what the
  files must hash to, and an ad-hoc build has no team for `sameSigner` to
  catch a swapped app, so the hash is the only check a mirror cannot pass
  by serving both. The files themselves may still come through a mirror;
  `internal/update/fork_mirror_test.go` checks both halves.

The repository is `update.ForkRepo`. Three variables override the defaults:

| Variable | What it does |
| --- | --- |
| `MAGPIE_RELEASE_REPO` | take releases from another GitHub repository |
| `MAGPIE_UPDATE_FEED` | ask a JSON feed instead of GitHub |
| `MAGPIE_NOTES_FEED` | ask a JSON feed for past releases' notes |

`MAGPIE_RELEASE_API` points at another API host, for a test.

After a fork, change `ForkRepo` in `internal/update/fork.go`.
`TestRepoDefaultsToTheFork` fails if it is left pointing at upstream.
