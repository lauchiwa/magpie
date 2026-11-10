import { readFileSync, readdirSync, writeFileSync, appendFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

export function validateShard({ report, exit, reds, oom, identity }, expected) {
  const errors = [];
  const s = report?.summary;
  const counts = ["tests", "passed", "failed", "cancelled", "skipped", "todo"];
  if (report?.version !== 1 || !s || counts.some(k => !Number.isInteger(s[k]) || s[k] < 0)) {
    errors.push("missing or invalid final test summary");
  } else {
    if (s.tests === 0 || s.passed + s.failed === 0) errors.push("no executed tests");
    if (s.cancelled) errors.push("cancelled tests");
    if (s.tests !== s.passed + s.failed + s.cancelled + s.skipped + s.todo) errors.push("inconsistent counts");
    if (exit !== (s.failed ? 1 : 0)) errors.push("exit status disagrees with test summary");
    if (s.failed && reds.length === 0) errors.push("failed run has no named failures");
    if (!s.failed && reds.length) errors.push("passing run has named failures");
  }
  if (exit !== 0 && exit !== 1) errors.push("missing or abnormal process exit");
  if (!Number.isInteger(report?.failures) || report.failures !== reds.length) errors.push("failure list is incomplete");
  if (reds.some(line => !/^[^\t?]+\.test\.cjs\t\S/.test(line))) errors.push("unattributed failure");
  if (oom.trim()) errors.push("earlyoom terminated a process");
  if (!identity || identity.side !== expected.side || identity.engine !== expected.engine || identity.shard !== expected.shard || !/^[0-9a-f]{40}$/.test(identity.commit || "")) {
    errors.push("missing or mismatched checkout identity");
  }
  return errors;
}

export function compare(root, sides) {
  const errors = [], failures = new Map(), commits = new Map();
  const expectedDirs = new Set();
  for (const side of sides) {
    const named = new Set(), shas = new Set();
    for (const engine of ["chromium", "webkit"]) {
      for (let shard = 1; shard <= 8; shard++) {
        const name = `reds-${side}-${engine}-${shard}`;
        expectedDirs.add(name);
        try {
          const dir = path.join(root, name);
          const text = file => readFileSync(path.join(dir, file), "utf8");
          const reds = text("reds.txt").split(/\r?\n/).filter(Boolean);
          const rawExit = text("exit.txt").trim();
          const exit = /^exit \d+$/.test(rawExit) ? Number(rawExit.slice(5)) : null;
          const report = JSON.parse(text("report.json"));
          const identity = JSON.parse(text("identity.json"));
          for (const error of validateShard({ report, identity, exit, reds, oom: text("oom.txt") }, { side, engine, shard })) errors.push(`${name}: ${error}`);
          shas.add(identity.commit);
          for (const line of reds) named.add(`${engine}\t${line}`);
        } catch {
          errors.push(`${name}: missing or unreadable artifact`);
        }
      }
    }
    if (shas.size !== 1) errors.push(`${side}: shards did not test one commit`);
    failures.set(side, named);
    commits.set(side, [...shas]);
  }
  for (const name of readdirSync(root)) {
    if (name.startsWith("reds-") && !expectedDirs.has(name)) errors.push(`${name}: unexpected shard`);
  }
  const fork = failures.get("fork") || new Set();
  const upstream = failures.get("upstream") || new Set();
  const added = [...fork].filter(x => !upstream.has(x)).sort();
  return { errors, failures, commits, added, ok: errors.length === 0 && added.length === 0 };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const sides = process.env.BASE ? ["fork", "upstream"] : ["fork"];
  let result;
  try { result = compare(process.argv[2] || "reds", sides); }
  catch { result = { errors: ["artifact directory is missing"], failures: new Map(), commits: new Map(), added: [], ok: false }; }
  const lines = ["## GUI suite", ""];
  for (const [side, reds] of result.failures) {
    lines.push(`${side}: ${reds.size} named failures; checkout ${(result.commits.get(side) || []).join(", ")}.`);
    writeFileSync(`${side}.txt`, [...reds].sort().join("\n") + (reds.size ? "\n" : ""));
  }
  lines.push(`Fork-only failures: ${result.added.length}.`);
  if (result.errors.length) lines.push("", "Incomplete or invalid execution:", ...result.errors.map(x => `- ${x}`));
  if (result.added.length) lines.push("", "```text", ...result.added.slice(0, 200), "```");
  const summary = lines.join("\n") + "\n";
  writeFileSync("new.txt", result.added.join("\n"));
  console.log(summary);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary);
  process.exitCode = result.ok ? 0 : 1;
}
