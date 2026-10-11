// node --test scripts/release-workflow.test.mjs
//
// This fork publishes its own releases. The release workflow is checked here
// rather than read, because the way it used to work — handing the version to
// another repository, which built upstream's source and published there — is
// one line away, and a release that went out from the wrong repository or
// under the wrong version can't be taken back.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const workflow = readFileSync(new URL("../.github/workflows/release.yml", import.meta.url), "utf8");

test("the release is built from this repository, not the upstream one", () => {
  // No step hands the release to another repository, and nothing names the
  // upstream source or the upstream release repository.
  assert.doesNotMatch(workflow, /repository_dispatch/);
  assert.doesNotMatch(workflow, /repository-dispatch/);
  assert.doesNotMatch(workflow, /magpie-releases/);
  assert.doesNotMatch(workflow, /repository:\s*yetone\//);
  // Every checkout takes this repository's tag, so none passes `repository:`.
  assert.doesNotMatch(workflow, /^\s+repository:/m);
});

test("the assets go to the repository the workflow runs in", () => {
  assert.match(workflow, /gh release create "\$TAG" --repo "\$GITHUB_REPOSITORY"/);
  assert.doesNotMatch(workflow, /--repo (?!"\$GITHUB_REPOSITORY")\S/);
});

test("only this fork's own tags start a release", () => {
  // vX.Y.Z-rc.N alone: a bare v* tag, which would outrank every rc after it,
  // starts nothing.
  assert.match(workflow, /tags:\n\s+- 'v\*-rc\.\*'/);
  assert.doesNotMatch(workflow, /- 'v\*'\s*$/m);
});

test("the tag is checked before anything is built", () => {
  assert.match(workflow, /node scripts\/release-version\.mjs check "\$TAG"/);
  // Every job that builds or publishes waits for that check.
  for (const job of ["mac", "cli", "linux"]) {
    const needs = new RegExp(`^  ${job}:\\n(?:.*\\n)*?    needs: .*validate`, "m");
    assert.match(workflow, needs, job);
  }
  assert.match(workflow, /^  publish:\n(?:.*\n)*?    needs: \[validate, /m);
});

test("a release is published as a pre-release", () => {
  // Every version this fork publishes carries -rc.N, which is a pre-release.
  assert.match(workflow, /--prerelease/);
  assert.doesNotMatch(workflow, /--latest/);
});

test("the tag must already exist, and an existing release is never overwritten", () => {
  assert.match(workflow, /--verify-tag/);
  assert.match(workflow, /is already released on/);
});

test("the publish job is the only one that may write", () => {
  // The workflow is read-only by default, and only publish adds the write.
  assert.match(workflow, /^permissions:\n  contents: read$/m);
  const writes = workflow.match(/contents: write/g) || [];
  assert.equal(writes.length, 1);
});

test("every uploaded asset is checked against what was built", () => {
  assert.match(workflow, /was not uploaded/);
});
