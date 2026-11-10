// A node --test reporter that writes one line per failing test, as
// "file<TAB>test name", so two runs of the GUI suite can be compared by
// name (gui-suite.yml). A test that failed only because a subtest did is
// left out: the subtest's own line names it.
import path from "node:path";
import { writeFileSync } from "node:fs";

export function completedSummary(data) {
  if (data.file) return null;
  return { ...data.counts, duration: data.duration_ms };
}

export default async function* reds(source) {
  let summary = null;
  let failures = 0;
  for await (const { type, data } of source) {
    if (type === "test:summary") summary = completedSummary(data) || summary;
    if (type !== "test:fail" || data.todo || data.skip) continue;
    if (data.details?.error?.failureType === "subtestsFailed") continue;
    failures++;
    const file = data.file ? path.basename(data.file) : "?";
    yield `${file}\t${String(data.name).replace(/\s+/g, " ")}\n`;
  }
  if (process.env.GUI_REPORT_PATH) {
    writeFileSync(process.env.GUI_REPORT_PATH, JSON.stringify({ version: 1, summary, failures }) + "\n");
  }
}
