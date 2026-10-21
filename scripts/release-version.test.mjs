// node --test scripts/release-version.test.mjs
//
// The fork's version rule: X.Y.Z is the upstream version synced from, and
// -rc.N counts this fork's releases on top of it. These check the two
// properties the updater depends on: the order is monotonic, and a bare
// upstream version is never produced.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";
import { nextTag, parseBase, parseForkTag, rcNumbersFor } from "./release-version.mjs";

test("the first release of an upstream version is rc.0", () => {
  assert.equal(nextTag("0.1.1182", []), "v0.1.1182-rc.0");
  assert.equal(nextTag("v0.1.1182", ["v0.1.1181-rc.4"]), "v0.1.1182-rc.0");
});

test("each release counts up from the highest rc already tagged", () => {
  const tags = ["v0.1.1182-rc.0"];
  assert.equal(nextTag("0.1.1182", tags), "v0.1.1182-rc.1");
  tags.push("v0.1.1182-rc.1");
  assert.equal(nextTag("0.1.1182", tags), "v0.1.1182-rc.2");
});

test("the counter passes nine, because it is compared as a number", () => {
  const tags = Array.from({ length: 10 }, (_, i) => `v0.1.1182-rc.${i}`);
  assert.equal(nextTag("0.1.1182", tags), "v0.1.1182-rc.10");
  assert.deepEqual(rcNumbersFor("0.1.1182", [...tags].reverse()).slice(-3), [7, 8, 9]);
  // the gap a deleted tag leaves is not filled: the next one is still higher
  assert.equal(nextTag("0.1.1182", ["v0.1.1182-rc.0", "v0.1.1182-rc.7"]), "v0.1.1182-rc.8");
});

test("a new upstream version starts again at rc.0", () => {
  const tags = ["v0.1.1182-rc.0", "v0.1.1182-rc.1", "v0.1.1182-rc.10"];
  assert.equal(nextTag("0.1.1183", tags), "v0.1.1183-rc.0");
});

test("only this base's tags are counted", () => {
  const tags = [
    "v0.1.1181-rc.9",
    "v0.1.1182-rc.0",
    "v0.2.0-rc.5",
    "v0.1.1182",
    "v0.1.11820-rc.3",
    "nightly",
    "",
  ];
  assert.deepEqual(rcNumbersFor("0.1.1182", tags), [0]);
  assert.equal(nextTag("0.1.1182", tags), "v0.1.1182-rc.1");
});

test("a bare upstream version is refused, and says why", () => {
  assert.throws(() => parseForkTag("v0.1.1182"), /bare upstream version/);
  assert.throws(() => parseForkTag("0.1.1182"), /bare upstream version/);
  for (const tag of ["v0.1.1182-rc", "v0.1.1182-rc.1a", "v0.1.1182-beta.1", "v0.1-rc.0", "rc.0"]) {
    assert.throws(() => parseForkTag(tag), /not a fork release tag|bare upstream/, tag);
  }
});

test("an upstream version with leading zeros or missing parts is refused", () => {
  for (const base of ["0.1.01", "0.1", "0.1.1182.1", "v", "", undefined, "latest"]) {
    assert.throws(() => parseBase(base), /upstream version must be X\.Y\.Z/, String(base));
  }
  assert.equal(parseBase(" v0.1.1182 "), "0.1.1182");
});

test("check accepts a tag this fork made and reads its parts", () => {
  assert.deepEqual(parseForkTag("v0.1.1182-rc.10"), {
    tag: "v0.1.1182-rc.10",
    version: "0.1.1182-rc.10",
    base: "0.1.1182",
    rc: 10,
  });
});

// The order these tags must sort in is checked against the updater's own
// comparison in internal/update (TestForkReleaseTagsIncrease), which is the
// code that actually picks a version.

test("run as a script it prints the tag, whatever form its path takes", () => {
  // On Windows an absolute path is "D:\…", which an entry check built on
  // new URL(argv[1], "file:") reads as a URL and skips main, printing nothing.
  const script = fileURLToPath(new URL("./release-version.mjs", import.meta.url));
  const root = dirname(dirname(script));
  const env = { ...process.env };
  delete env.GITHUB_OUTPUT;
  for (const path of [script, "scripts/release-version.mjs", "./scripts/release-version.mjs"]) {
    const out = execFileSync(process.execPath, [path, "check", "v0.1.1182-rc.3"], {
      cwd: root,
      env,
      encoding: "utf8",
    });
    assert.equal(out, "v0.1.1182-rc.3\n", path);
  }
});
