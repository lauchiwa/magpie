import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { compare, validateShard } from "./compare.mjs";
import { completedSummary } from "./reds.mjs";

const expected = { side: "fork", engine: "chromium", shard: 1 };
const commit = "2".repeat(40);
function passing() {
  return { report: { version: 1, summary: { tests: 2, passed: 2, failed: 0, cancelled: 0, skipped: 0, todo: 0 }, failures: 0 }, exit: 0, reds: [], oom: "", identity: { ...expected, commit } };
}
function failing() {
  const run = passing();
  run.report.summary.passed = 1;
  run.report.summary.failed = 1;
  run.report.failures = 1;
  run.exit = 1;
  run.reds = ["a.test.cjs\tnamed failure"];
  return run;
}

test("passing and named failing executions are structurally complete", () => {
  assert.deepEqual(validateShard(passing(), expected), []);
  assert.deepEqual(validateShard(failing(), expected), []);
  assert.equal(completedSummary({ file: "a.test.cjs", counts: {} }), null);
  assert.deepEqual(completedSummary({ counts: { tests: 2 }, duration_ms: 3 }), { tests: 2, duration: 3 });
});

test("crashes, missing summaries, cancellation, OOM and unidentified results cannot pass", () => {
  const mutations = [
    r => { r.exit = 137; },
    r => { r.exit = 1; },
    r => { r.report.summary = null; },
    r => { r.report.summary.tests = 0; },
    r => { r.report.summary.cancelled = 1; r.report.summary.passed = 1; },
    r => { r.report.summary.skipped = 2; r.report.summary.passed = 0; },
    r => { r.oom = "terminated a browser"; },
    r => { r.identity.commit = "main"; },
    r => { r.identity.shard = 2; },
    r => { r.report.failures = 1; },
    r => { r.report.summary.failed = 1; r.report.summary.passed = 1; r.exit = 1; },
  ];
  for (const mutate of mutations) {
    const run = passing();
    mutate(run);
    assert.notEqual(validateShard(run, expected).length, 0, mutate.toString());
  }
});

test("comparison requires all 16 shards and rejects fork-only failures without a base", t => {
  const parent = path.resolve(process.env.GUI_SCENARIO_ROOT || tmpdir());
  const root = mkdtempSync(path.join(parent, "gui-report-"));
  t.after(() => {
    assert.equal(path.dirname(path.resolve(root)), parent);
    assert.ok(path.basename(root).startsWith("gui-report-"));
    rmSync(root, { recursive: true });
  });
  function fill(side, make) {
    for (const engine of ["chromium", "webkit"]) {
      for (let shard = 1; shard <= 8; shard++) {
        const dir = path.join(root, `reds-${side}-${engine}-${shard}`);
        mkdirSync(dir, { recursive: true });
        const run = make();
        run.identity = { side, engine, shard, commit };
        for (const [name, text] of Object.entries({ "report.json": JSON.stringify(run.report), "identity.json": JSON.stringify(run.identity), "reds.txt": run.reds.join("\n"), "exit.txt": `exit ${run.exit}`, "oom.txt": run.oom })) writeFileSync(path.join(dir, name), text);
      }
    }
  }
  assert.equal(compare(root, ["fork"]).ok, false);
  fill("fork", passing);
  assert.equal(compare(root, ["fork"]).ok, true);
  fill("fork", failing);
  assert.equal(compare(root, ["fork"]).ok, false);
  fill("upstream", failing);
  assert.equal(compare(root, ["fork", "upstream"]).ok, true);
  writeFileSync(path.join(root, "reds-fork-webkit-8", "exit.txt"), "exit 137");
  assert.equal(compare(root, ["fork", "upstream"]).ok, false);
});
