// Fork release notes: what this release changed, without upstream's history.
//
// A fork release vX.Y.Z-rc.N is upstream magpie X.Y.Z plus this fork's own
// commits. The notes list those commits since the previous fork release, and
// point at upstream's compare page for the upstream part, rather than
// printing every upstream commit a sync brought in: `git log PREV..TAG
// --no-merges` listed 862 commits for v0.1.1305-rc.0, about 75 of them the
// fork's.
//
// A commit is the fork's when upstream's tag for this release's base can't
// reach it. That is decided by ancestry alone, never by author, message or
// path, and merges are kept: a fork PR's merge and a sync's merge are the
// fork's own commits, and a sync's merge may hold its conflict resolution.
//
// Usage:
//   node scripts/release-notes.mjs render <tag> <owner/name> [upstream-ref]
//
// It reads the repository in the current directory and writes the whole
// notes to stdout. upstream-ref is the commit upstream tagged vX.Y.Z,
// refs/magpie-notes/upstream/vX.Y.Z by default: the release workflow fetches
// it there, read-only, so neither the fork's tags nor its checkout change.
// The ref must exist and the tag must contain it, or nothing is written: a
// note that printed upstream's whole history as the fork's would be wrong.

import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { parseForkTag } from "./release-version.mjs";

const upstreamRepo = "yetone/magpie";
const repoRE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

/** The default ref the workflow fetches upstream's base tag into. */
export function upstreamRefFor(base) {
  return `refs/magpie-notes/upstream/v${base}`;
}

/** How two fork tags order, as numbers: base's three parts, then rc. */
export function compareTags(a, b) {
  const pa = parseForkTag(a);
  const pb = parseForkTag(b);
  const na = [...pa.base.split("."), pa.rc].map(BigInt);
  const nb = [...pb.base.split("."), pb.rc].map(BigInt);
  for (let i = 0; i < na.length; i++) {
    if (na[i] !== nb[i]) return na[i] < nb[i] ? -1 : 1;
  }
  return 0;
}

/**
 * The previous fork release among tags: the highest one below tag. tags are
 * the ones tag's commit contains, so a tag on a branch the release never
 * merged is not a candidate. Anything that isn't a fork tag is skipped.
 */
export function previousTag(tag, tags) {
  let best = null;
  for (const t of tags) {
    try {
      parseForkTag(t);
    } catch {
      continue;
    }
    if (compareTags(t, tag) >= 0) continue;
    if (best === null || compareTags(t, best) > 0) best = t;
  }
  return best;
}

function git(cwd, args) {
  return execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
}

function commitOf(cwd, ref) {
  try {
    return git(cwd, ["rev-parse", "--verify", "--quiet", "--end-of-options", `${ref}^{commit}`]).trim();
  } catch {
    return null;
  }
}

function contains(cwd, ancestor, ref) {
  try {
    git(cwd, ["merge-base", "--is-ancestor", ancestor, ref]);
    return true;
  } catch (error) {
    if (error.status === 1) return false;
    throw error;
  }
}

/**
 * This fork's commits in TAG, after PREV when there is one, that upstream's
 * base can't reach: hash and subject, newest first, a merged branch's
 * commits beside its merge.
 */
function forkCommits(cwd, tag, prev, upstream) {
  const range = prev ? [`${prev}..${tag}`] : [tag];
  const out = git(cwd, ["log", "--topo-order", "--format=%H%x09%s", ...range, "--not", upstream, "--"]);
  return out
    .split("\n")
    .filter(Boolean)
    .map((line) => {
      const tab = line.indexOf("\t");
      return { hash: line.slice(0, tab), subject: line.slice(tab + 1) };
    });
}

/** The whole notes for tag, from the repository at cwd. */
export function renderNotes({ tag, repo, upstreamRef, cwd = process.cwd() }) {
  const cur = parseForkTag(tag);
  if (!repoRE.test(String(repo ?? ""))) {
    throw new Error(`repository must be owner/name, not ${JSON.stringify(repo)}`);
  }
  const ref = upstreamRef ?? upstreamRefFor(cur.base);
  if (String(ref).startsWith("-")) {
    throw new Error(`upstream ref ${JSON.stringify(ref)} is not a ref`);
  }
  if (!commitOf(cwd, `refs/tags/${cur.tag}`)) {
    throw new Error(`${cur.tag} is not a tag in this repository`);
  }
  const upstream = commitOf(cwd, ref);
  if (!upstream) {
    throw new Error(
      `upstream v${cur.base} is not at ${ref}; fetch upstream's tag there first, so the notes can tell upstream's commits from this fork's`,
    );
  }
  if (!contains(cwd, upstream, `refs/tags/${cur.tag}`)) {
    throw new Error(
      `${cur.tag} does not contain upstream v${cur.base} (${ref}), so it is not synced from it; nothing can tell this fork's commits from upstream's`,
    );
  }
  const reachable = git(cwd, ["tag", "--list", "v*-rc.*", "--merged", `refs/tags/${cur.tag}`])
    .split("\n")
    .map((t) => t.trim())
    .filter(Boolean);
  const prev = previousTag(cur.tag, reachable);
  const commits = forkCommits(cwd, `refs/tags/${cur.tag}`, prev && `refs/tags/${prev}`, upstream);

  const lines = ["### Changes", ""];
  const upstreamTag = `https://github.com/${upstreamRepo}/releases/tag/v${cur.base}`;
  if (!prev) {
    lines.push(`- The first release of this fork, on upstream magpie [v${cur.base}](${upstreamTag}).`);
  } else {
    const before = parseForkTag(prev).base;
    if (before === cur.base) {
      lines.push(`- Upstream is unchanged since ${prev}: still magpie [v${cur.base}](${upstreamTag}).`);
    } else {
      // before is what prev's tag says; a tag with the wrong base is left
      // as it is (docs/fork-releases.md), so this is its label, not a check
      lines.push(
        `- Upstream magpie v${before} → [v${cur.base}](${upstreamTag}): ` +
          `[what changed upstream](https://github.com/${upstreamRepo}/compare/v${before}...v${cur.base}).`,
      );
    }
  }
  lines.push("");
  lines.push(prev ? `This fork's own commits since ${prev}:` : "This fork's own commits:");
  lines.push("");
  if (commits.length === 0) {
    lines.push("- None.");
  } else {
    for (const c of commits) {
      lines.push(`- ${c.subject} ([${c.hash.slice(0, 8)}](https://github.com/${repo}/commit/${c.hash}))`);
    }
  }
  lines.push(
    "",
    "### This build",
    "",
    `- A build of this fork, synced from upstream magpie **${cur.base}**.`,
    "- Marked as a pre-release: every version this fork publishes carries `-rc.N`.",
    `- Built from \`${repo}\` at \`${cur.tag}\`.`,
    "",
    "### Install",
    "",
    "- **macOS (Apple Silicon):** `magpie-darwin-arm64.dmg`",
    "- **macOS (Intel):** `magpie-darwin-amd64.dmg`",
    "- **Windows:** `magpie-windows-amd64.exe` (ARM: `magpie-windows-arm64.exe`)",
    "- **Linux:** `magpie-linux-amd64` / `magpie-linux-arm64` (needs GTK 3 and WebKitGTK 4.1)",
    "- **Terminal only:** `magpie-cli-<os>-<arch>`",
    "",
    "### Unsigned builds",
    "",
    "These builds are not signed with a paid Apple or Windows certificate, so each system asks before running one the first time.",
    "",
    "- **macOS:** the app is ad-hoc signed and not notarised. Open it once with right-click (or Control-click) → **Open**, or run `xattr -dr com.apple.quarantine /Applications/magpie.app`. Later updates install without asking again.",
    '- **Windows:** SmartScreen shows "unrecognised app": **More info** → **Run anyway**.',
    "",
  );
  return lines.join("\n");
}

function main(argv) {
  const [command, tag, repo, upstreamRef] = argv;
  if (command === "render" && tag && repo) {
    process.stdout.write(renderNotes({ tag, repo, upstreamRef }));
    return;
  }
  throw new Error("usage: release-notes.mjs render <tag> <owner/name> [upstream-ref]");
}

// Run only as a script, not when imported by the tests (see release-version.mjs).
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    main(process.argv.slice(2));
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
