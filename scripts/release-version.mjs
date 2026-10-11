// Fork release versions: this repository publishes its own builds, and each
// one says which upstream version it was synced from.
//
//   X.Y.Z-rc.N
//
// X.Y.Z is the upstream magpie version this fork synced from, copied
// verbatim. N counts this fork's releases on top of that upstream version,
// from 0: rc.0 is "synced, no fork changes released yet".
//
//   synced upstream 0.1.1182   v0.1.1182-rc.0
//   released a fork change     v0.1.1182-rc.1
//   again                      v0.1.1182-rc.2
//   tenth                      v0.1.1182-rc.10
//   upstream ships 0.1.1183    v0.1.1183-rc.0
//
// A bare X.Y.Z is never published from this fork. SemVer ranks a
// pre-release below the release it leads up to (§11), which the updater
// follows (internal/update: Newer, comparePre). Publishing a bare 0.1.1182
// would therefore outrank every later 0.1.1182-rc.N and pin installs to
// that ceiling for good. Skipping the bare version means the ceiling never
// exists, and the order stays monotonic:
//
//   0.1.1182-rc.0 < rc.1 < rc.2 < rc.10 < 0.1.1183-rc.0
//
// The counter stays purely numeric, because SemVer compares all-digit
// identifiers as numbers and everything else as text: rc.10 > rc.2 holds,
// while an "rc.10a" would sort below rc.2.
//
// Usage:
//   node scripts/release-version.mjs next <upstream-version>
//   node scripts/release-version.mjs check <tag>
//
// next prints the next free tag for that upstream version, reading this
// repository's existing tags. check validates a tag that already exists.
// Both write version, base and rc to GITHUB_OUTPUT when Actions set it.

import { execFileSync } from "node:child_process";
import { appendFileSync } from "node:fs";

// numeric is a SemVer numeric identifier: no leading zeros, so "01" is
// refused rather than taken as 1 and sorted as text later.
const numeric = String.raw`(?:0|[1-9][0-9]*)`;
const baseRE = new RegExp(`^${numeric}\\.${numeric}\\.${numeric}$`);
const tagRE = new RegExp(`^v(${numeric}\\.${numeric}\\.${numeric})-rc\\.(${numeric})$`);

/** The upstream version a fork release is based on, "v" allowed and dropped. */
export function parseBase(value) {
  const base = String(value ?? "")
    .trim()
    .replace(/^v/, "");
  if (!baseRE.test(base)) {
    throw new Error(
      `upstream version must be X.Y.Z with no leading zeros, not ${JSON.stringify(value)}`,
    );
  }
  return base;
}

/**
 * One of this fork's release tags: vX.Y.Z-rc.N.
 * A bare vX.Y.Z is refused here too, so neither `check` nor the workflow can
 * publish the ceiling that would outrank every later rc.
 */
export function parseForkTag(value) {
  const tag = String(value ?? "").trim();
  const m = tagRE.exec(tag);
  if (!m) {
    const bare = /^v?(\d+\.\d+\.\d+)$/.exec(tag);
    if (bare) {
      throw new Error(
        `${tag} is a bare upstream version. This fork publishes ${tag.replace(/^v?/, "v")}-rc.N, ` +
          "because a bare version outranks every -rc.N after it and would pin installs to it",
      );
    }
    throw new Error(`${JSON.stringify(value)} is not a fork release tag (vX.Y.Z-rc.N)`);
  }
  return { tag, version: tag.slice(1), base: m[1], rc: Number(m[2]) };
}

/** The rc numbers already tagged for base, ignoring every other tag. */
export function rcNumbersFor(base, tags) {
  const want = parseBase(base);
  const out = [];
  for (const tag of tags) {
    const m = tagRE.exec(String(tag).trim());
    if (m && m[1] === want) {
      out.push(Number(m[2]));
    }
  }
  return out.sort((a, b) => a - b);
}

/**
 * The next free tag for base: one past the highest rc already tagged for it,
 * rc.0 when none is. A new upstream version starts again at rc.0, which is
 * still an increase, because the numeric part decides when it differs.
 */
export function nextTag(base, tags) {
  const want = parseBase(base);
  const used = rcNumbersFor(want, tags);
  const next = used.length === 0 ? 0 : used[used.length - 1] + 1;
  return `v${want}-rc.${next}`;
}

function tagsFromGit() {
  const out = execFileSync("git", ["tag", "--list"], { encoding: "utf8" });
  return out.split("\n").filter(Boolean);
}

function report(parsed) {
  process.stdout.write(`${parsed.tag}\n`);
  if (process.env.GITHUB_OUTPUT) {
    appendFileSync(
      process.env.GITHUB_OUTPUT,
      `tag=${parsed.tag}\nversion=${parsed.version}\nbase=${parsed.base}\nrc=${parsed.rc}\n`,
    );
  }
}

function main(argv) {
  const [command, value] = argv;
  if (command === "next") {
    report(parseForkTag(nextTag(value, tagsFromGit())));
    return;
  }
  if (command === "check") {
    report(parseForkTag(value));
    return;
  }
  throw new Error("usage: release-version.mjs <next|check> <version-or-tag>");
}

if (process.argv[1] && import.meta.url === new URL(process.argv[1], "file:").href) {
  try {
    main(process.argv.slice(2));
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
