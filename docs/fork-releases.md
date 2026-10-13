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

## A mis-tagged release

A tag with the wrong base does not need deleting. A correctly based tag
outranks it, because the numeric part decides when it differs:

```text
v0.1.1181-rc.9 < v0.1.1182-rc.0 < v0.1.1182-rc.1
```

Leave the stale tag unused and publish the correct one. Deleting a pushed tag
or a published release is destructive and can confuse an install that already
saw it.

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
