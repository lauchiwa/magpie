// A node --test reporter that writes one line per failing test, as
// "file<TAB>test name", so two runs of the GUI suite can be compared by
// name (gui-suite.yml). A test that failed only because a subtest did is
// left out: the subtest's own line names it.
import path from "node:path";

export default async function* reds(source) {
  for await (const { type, data } of source) {
    if (type !== "test:fail" || data.todo || data.skip) continue;
    if (data.details?.error?.failureType === "subtestsFailed") continue;
    const file = data.file ? path.basename(data.file) : "?";
    yield `${file}\t${String(data.name).replace(/\s+/g, " ")}\n`;
  }
}
