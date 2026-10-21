// node --test scripts/release-notes.test.mjs
//
// The notes list this fork's commits since its previous release and leave
// upstream's out. Each test builds a small repository of its own: upstream
// tags, a fork that syncs them through merges, and fork tags on top. No test
// fetches anything or touches this repository's tags.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { compareTags, previousTag, renderNotes, upstreamRefFor } from "./release-notes.mjs";

const script = fileURLToPath(new URL("./release-notes.mjs", import.meta.url));

// A repository in a folder of its own, removed when the test ends. Its git
// runs with no global or system config, so the runner's user, signing and
// hooks don't reach it, and every commit has a fixed author and date.
function repo(t, name = "notes") {
  const dir = mkdtempSync(join(tmpdir(), `magpie ${name}-`));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  let tick = 0;
  const env = {
    ...process.env,
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: join(dir, ".no-global-config"),
    GIT_AUTHOR_NAME: "t",
    GIT_AUTHOR_EMAIL: "t@example.invalid",
    GIT_COMMITTER_NAME: "t",
    GIT_COMMITTER_EMAIL: "t@example.invalid",
  };
  const git = (...args) => {
    tick++;
    const date = `2026-01-01T00:${String(Math.floor(tick / 60)).padStart(2, "0")}:${String(tick % 60).padStart(2, "0")}Z`;
    return execFileSync("git", args, {
      cwd: dir,
      encoding: "utf8",
      env: { ...env, GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date },
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  };
  git("init", "-q", "-b", "upstream");
  git("config", "commit.gpgsign", "false");
  git("config", "tag.gpgsign", "false");
  const commit = (subject) => {
    writeFileSync(join(dir, "f"), `${subject}\n`);
    git("add", "f");
    git("commit", "-q", "-m", subject);
    return git("rev-parse", "HEAD");
  };
  const merge = (branch, subject) => {
    git("merge", "-q", "--no-ff", "-X", "theirs", "-m", subject, branch);
    return git("rev-parse", "HEAD");
  };
  return { dir, git, commit, merge };
}

// upstream: u1 (v0.1.1) → u2a → u2b (v0.1.2)
// fork:     sync v0.1.1, f1, tag v0.1.1-rc.0; f2 through a PR merge, tag
//           v0.1.1-rc.1; sync v0.1.2, f3, tag v0.1.2-rc.0
function synced(t, name) {
  const r = repo(t, name);
  r.commit("u1: upstream one");
  r.git("tag", "v0.1.1");
  r.git("checkout", "-q", "-b", "main");
  r.commit("f1: fork one");
  r.git("tag", "v0.1.1-rc.0");
  r.git("checkout", "-q", "-b", "pr");
  r.commit("f2: fork two");
  r.git("checkout", "-q", "main");
  r.merge("pr", "Merge pull request #1 from fork/pr");
  r.git("tag", "v0.1.1-rc.1");
  r.git("checkout", "-q", "upstream");
  r.commit("u2a: upstream two a");
  r.commit("u2b: upstream two b");
  r.git("tag", "v0.1.2");
  r.git("checkout", "-q", "main");
  r.merge("v0.1.2", "sync: upstream v0.1.2");
  r.commit("f3: fork three");
  r.git("tag", "v0.1.2-rc.0");
  return r;
}

const subjects = (notes) =>
  [...notes.matchAll(/^- (.*) \(\[[0-9a-f]{8}\]\(https:\/\/github\.com\/[^)]+\/commit\/[0-9a-f]{40}\)\)$/gm)].map(
    (m) => m[1],
  );

test("a sync lists the fork's commits and the sync's merge, not upstream's", (t) => {
  const r = synced(t, "sync");
  const notes = renderNotes({ tag: "v0.1.2-rc.0", repo: "fork/magpie", upstreamRef: "v0.1.2", cwd: r.dir });
  assert.deepEqual(subjects(notes), ["f3: fork three", "sync: upstream v0.1.2"]);
  assert.doesNotMatch(notes, /u2a|u2b|u1:/);
  assert.match(notes, /This fork's own commits since v0\.1\.1-rc\.1:/);
  assert.match(
    notes,
    /Upstream magpie v0\.1\.1 → \[v0\.1\.2\]\(https:\/\/github\.com\/yetone\/magpie\/releases\/tag\/v0\.1\.2\): \[what changed upstream\]\(https:\/\/github\.com\/yetone\/magpie\/compare\/v0\.1\.1\.\.\.v0\.1\.2\)/,
  );
  // the links point at the repository the release is published to
  assert.match(notes, /https:\/\/github\.com\/fork\/magpie\/commit\//);
  assert.match(notes, /Built from `fork\/magpie` at `v0\.1\.2-rc\.0`/);
});

test("a fork PR's merge is listed with the commits it brought", (t) => {
  const r = synced(t, "pr");
  const notes = renderNotes({ tag: "v0.1.1-rc.1", repo: "fork/magpie", upstreamRef: "v0.1.1", cwd: r.dir });
  assert.deepEqual(subjects(notes), ["Merge pull request #1 from fork/pr", "f2: fork two"]);
  assert.match(notes, /Upstream is unchanged since v0\.1\.1-rc\.0: still magpie \[v0\.1\.1\]/);
});

test("the first release says so and lists every fork commit on its base", (t) => {
  const r = synced(t, "first");
  const notes = renderNotes({ tag: "v0.1.1-rc.0", repo: "fork/magpie", upstreamRef: "v0.1.1", cwd: r.dir });
  assert.deepEqual(subjects(notes), ["f1: fork one"]);
  assert.match(notes, /The first release of this fork, on upstream magpie \[v0\.1\.1\]/);
  assert.match(notes, /^This fork's own commits:$/m);
  assert.doesNotMatch(notes, /compare/);
});

test("a tag on the same commit as the last release lists none", (t) => {
  const r = synced(t, "none");
  r.git("tag", "v0.1.2-rc.1", "v0.1.2-rc.0");
  const notes = renderNotes({ tag: "v0.1.2-rc.1", repo: "fork/magpie", upstreamRef: "v0.1.2", cwd: r.dir });
  assert.deepEqual(subjects(notes), []);
  assert.match(notes, /since v0\.1\.2-rc\.0:\n\n- None\.\n/);
});

test("the previous release is the highest by number, rc.10 above rc.2", (t) => {
  const r = synced(t, "rc10");
  for (let n = 1; n <= 10; n++) {
    r.commit(`f3.${n}`);
    r.git("tag", `v0.1.2-rc.${n}`);
  }
  r.commit("f4: after rc.10");
  r.git("tag", "v0.1.2-rc.11");
  const notes = renderNotes({ tag: "v0.1.2-rc.11", repo: "fork/magpie", upstreamRef: "v0.1.2", cwd: r.dir });
  assert.match(notes, /since v0\.1\.2-rc\.10:/);
  assert.deepEqual(subjects(notes), ["f4: after rc.10"]);
  assert.equal(previousTag("v0.1.2-rc.11", ["v0.1.2-rc.2", "v0.1.2-rc.10", "v0.1.2-rc.9"]), "v0.1.2-rc.10");
  assert.equal(compareTags("v0.1.10-rc.0", "v0.1.9-rc.99"), 1);
  assert.equal(compareTags("v0.1.9-rc.2", "v0.1.9-rc.10"), -1);
});

test("a tag on a branch the release never merged is not its previous release", (t) => {
  const r = synced(t, "branch");
  r.git("checkout", "-q", "-b", "side", "v0.1.1-rc.1");
  r.commit("s1: side work");
  // higher than v0.1.1-rc.1 and lower than v0.1.2-rc.0, but not in its history
  r.git("tag", "v0.1.1-rc.5");
  r.git("checkout", "-q", "main");
  const notes = renderNotes({ tag: "v0.1.2-rc.0", repo: "fork/magpie", upstreamRef: "v0.1.2", cwd: r.dir });
  assert.match(notes, /since v0\.1\.1-rc\.1:/);
  assert.doesNotMatch(notes, /s1: side work/);
});

test("a previous release with the wrong base is named by its label", (t) => {
  const r = synced(t, "mislabel");
  // like v0.1.1185-rc.0: tagged as 0.1.9 on a commit that never had it
  r.git("tag", "v0.1.9-rc.0", "v0.1.1-rc.0");
  r.git("checkout", "-q", "upstream");
  r.commit("u10: upstream ten");
  r.git("tag", "v0.1.10");
  r.git("checkout", "-q", "main");
  r.merge("v0.1.10", "sync: upstream v0.1.10");
  r.git("tag", "v0.1.10-rc.0");
  const notes = renderNotes({ tag: "v0.1.10-rc.0", repo: "fork/magpie", upstreamRef: "v0.1.10", cwd: r.dir });
  assert.match(notes, /since v0\.1\.9-rc\.0:/);
  assert.match(notes, /compare\/v0\.1\.9\.\.\.v0\.1\.10/);
  assert.doesNotMatch(notes, /u10|u2a|u2b/);
});

test("without upstream's base, or with a tag that doesn't contain it, nothing is written", (t) => {
  const r = synced(t, "missing");
  assert.throws(
    () => renderNotes({ tag: "v0.1.2-rc.0", repo: "fork/magpie", cwd: r.dir }),
    /upstream v0\.1\.2 is not at refs\/magpie-notes\/upstream\/v0\.1\.2; fetch/,
  );
  // v0.1.1-rc.1 was tagged before the fork synced v0.1.2
  assert.throws(
    () => renderNotes({ tag: "v0.1.1-rc.1", repo: "fork/magpie", upstreamRef: "v0.1.2", cwd: r.dir }),
    /v0\.1\.1-rc\.1 does not contain upstream v0\.1\.1 \(v0\.1\.2\)/,
  );
  assert.throws(
    () => renderNotes({ tag: "v0.1.3-rc.0", repo: "fork/magpie", upstreamRef: "v0.1.2", cwd: r.dir }),
    /v0\.1\.3-rc\.0 is not a tag/,
  );
});

test("the default upstream ref is the workflow's own namespace", (t) => {
  const r = synced(t, "default");
  r.git("update-ref", upstreamRefFor("0.1.2"), "v0.1.2");
  const notes = renderNotes({ tag: "v0.1.2-rc.0", repo: "fork/magpie", cwd: r.dir });
  assert.deepEqual(subjects(notes), ["f3: fork three", "sync: upstream v0.1.2"]);
});

test("a bad tag, repository or ref is refused before git runs", () => {
  const cwd = tmpdir();
  assert.throws(() => renderNotes({ tag: "v0.1.2", repo: "fork/magpie", cwd }), /bare upstream version/);
  for (const repo of ["fork", "fork/magpie/x", "fork/ma gpie", "fork/magpie)", undefined]) {
    assert.throws(() => renderNotes({ tag: "v0.1.2-rc.0", repo, cwd }), /owner\/name/, String(repo));
  }
  assert.throws(
    () => renderNotes({ tag: "v0.1.2-rc.0", repo: "fork/magpie", upstreamRef: "--output=x", cwd }),
    /is not a ref/,
  );
});

test("run as a script it prints the notes, from a folder with a space in it", (t) => {
  const r = synced(t, "cli");
  const env = { ...process.env };
  delete env.GITHUB_OUTPUT;
  const out = execFileSync(process.execPath, [script, "render", "v0.1.2-rc.0", "fork/magpie", "v0.1.2"], {
    cwd: r.dir,
    env,
    encoding: "utf8",
  });
  assert.deepEqual(subjects(out), ["f3: fork three", "sync: upstream v0.1.2"]);
  assert.match(out, /^### Changes\n/);
  // a failure says why on stderr, exits non-zero and prints no notes
  let failed;
  try {
    execFileSync(process.execPath, [script, "render", "v0.1.2-rc.0", "fork/magpie"], {
      cwd: r.dir,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
  } catch (error) {
    failed = error;
  }
  assert.ok(failed, "render without upstream's ref must fail");
  assert.equal(failed.status, 1);
  assert.equal(failed.stdout, "");
  assert.match(failed.stderr, /fetch upstream's tag there first/);
});

test("imported, it runs nothing", () => {
  // a main() run on import would see the importer's argv and fail with the
  // usage line on stderr
  const out = execFileSync(
    process.execPath,
    ["--input-type=module", "-e", `await import(${JSON.stringify(new URL("./release-notes.mjs", import.meta.url).href)})`],
    { cwd: tmpdir(), encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
  );
  assert.equal(out, "");
});
