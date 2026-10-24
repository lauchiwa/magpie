// Run with Node's test runner and Playwright on the module path; see README.md.
// fork: a provider write carries the rev the page read it at (lifecycle.js,
// lifecycle_fork.go). Save (an account's too), Remove, the on/off switch,
// Bind and Unbind (the panel's and the editor's) send it; a Duplicate is new
// and sends none. A picker's picks left for the Save go first with that rev,
// and the Save carries the one their answer gave. A stale answer keeps the editor and
// the draft as typed, sends nothing again and rereads the list; a write that
// was saved but for a step (follow_up, committed) or is finished by the
// journal (pending) closes the editor and says so as a warning, with the
// step and its cause, in the reader's language. In en, zh, zh-TW, ja and de,
// Chromium and WebKit, at 980 and 420 wide.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");

const relay = {
  id: "relay", name: "Relay", icon: "generic", chat: "https://relay.example/v1", responses: "", anthropic: "", catalog: "",
  host: "relay.example", models: [{ id: "m1", name: "M1", on: true }], agents: [], fallback: [], headers: {},
  key: { set: true, masked: "sk-…1234" }, keyList: [], balanceToken: { takes: false, set: false }, proxy: "", ready: true, rev: "r1",
};
// a subscription: its editor is the account one, with its own Save
const sub = {
  id: "workbuddy-ai", name: "WorkBuddy AI", icon: "generic", chat: "", responses: "", anthropic: "", catalog: "",
  models: [{ id: "glm-5", name: "GLM-5", on: true }], agents: [], fallback: [], headers: {}, keyList: [], routing: "", affinity: "", ready: true, rev: "s1",
  account: { agent: "workbuddy-ai", agentName: "WorkBuddy AI", user: "one@example.test", plan: "Pro", logins: [{ user: "one@example.test", plan: "Pro", active: true, on: true }] },
};
const profile = {
  id: "identity_bbbbbbbbbbbbbbbb", client: "codex_cli", source: "request_capture", state: "confirmed",
  fingerprint: { userAgent: "codex-tui/0.162.1", version: "0.162.1", headers: {} },
  createdAt: "2026-10-12T01:00:00Z", observedAt: "2026-10-12T01:00:00Z", confirmedAt: "2026-10-12T01:05:00Z", bindings: [],
};

// serve answers the page. refuse maps a POST's path to the 400 body it gets
// (the server's fail()); reads counts the provider list's GETs.
function serve(lang, world) {
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  const list = () => ({ providers: world.providers, presets: [], excluded: [], gateway: { running: true, window: true } });
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data, status = 200) => route.fulfill({ status, json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/providers") { world.reads++; return json(list()); }
    if (url.pathname === "/api/identity") { world.idReads++; return json(world.identity); }
    if (route.request().method() === "POST" && url.pathname.startsWith("/api/")) {
      const action = url.pathname.slice("/api/".length);
      world.posts.push({ action, body: route.request().postDataJSON() });
      if (world.refuse[action]) return json(world.refuse[action], 400);
      world.wrote?.[action]?.(); // what the write changed, before the list it answers
      return json(action.startsWith("identity/") ? world.identity : list());
    }
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
}

// what the reader is told, by hand in each language: the stale refusal, a
// saved write with a failed step it can redo (output limits), and one it
// must not make again (the sign-in, after a Remove); then pending
const words = {
  en: { save: "Save", remove: "Remove", dup: "Duplicate", add: "Add", bind: "Bind profile", unbind: "Unbind Relay", fieldUnbind: "Unbind",
    stale: "changed, removed or added again elsewhere", outputs: "Saved, except for its output limits: disk full. Open the provider again and save that part again.",
    signIn: "Saved, except for moving or forgetting its sign-in: access denied. Don't make the change again: it is saved.", pending: "Don't make it again." },
  zh: { save: "保存", remove: "移除", dup: "复制", add: "添加", bind: "绑定档案", unbind: "解绑 Relay", fieldUnbind: "解绑",
    stale: "已在别处被修改、删除或重新添加", outputs: "已保存，但输出上限没有完成：disk full。请重新打开这个供应商，把这一部分再保存一次。",
    signIn: "已保存，但移动或清除它的登录信息没有完成：access denied。不要再改一次：改动已经保存。", pending: "请不要再改一次。" },
  "zh-TW": { stale: "已在別處被修改、刪除或重新新增", outputs: "已儲存，但輸出上限沒有完成：disk full。請重新開啟這個供應商，把這一部分再儲存一次。", pending: "請不要再改一次。" },
  ja: { stale: "別の場所で変更・削除・再追加された", outputs: "保存しましたが、出力の上限は完了していません：disk full。プロバイダーを開き直して、その部分をもう一度保存してください。", pending: "もう一度変更しないでください。" },
  de: { stale: "an anderer Stelle geändert, entfernt oder neu hinzugefügt", outputs: "Gespeichert, außer den Ausgabelimits: disk full. Öffnen Sie den Anbieter erneut und speichern Sie diesen Teil noch einmal.", pending: "Nehmen Sie sie nicht erneut vor." },
};

const stale = { error: "relay was changed since it was read", code: "stale" };
const saved = (step, reason) => ({ error: `saved, but ${step}: ${reason}`, code: "follow_up", committed: "1", step, reason });
const pending = { error: "the change is written to the lifecycle journal", code: "pending" };

async function launch(t, engine, lang, width, setup) {
  const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
  const page = await (await browser.newContext({ viewport: { width, height: 760 }, reducedMotion: "reduce" })).newPage();
  t.after(async () => {
    if (process.env.ARTIFACT_DIR) {
      await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
      await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-${width}-lifecycle-rev.png`) });
    }
    await browser.close();
  });
  page.setDefaultTimeout(5000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const world = { providers: [relay], identity: null, refuse: {}, posts: [], reads: 0, idReads: 0 };
  setup?.(world);
  await page.route("**/*", serve(lang, world));
  await page.goto("http://magpie.test/?view=providers");
  await page.locator('#providers .row.provider[data-id="relay"]').waitFor();
  return { page, errors, world };
}

// posted waits for the next POST after n
async function posted(page, world, n) {
  for (let i = 0; i < 60 && world.posts.length === n; i++) await page.waitForTimeout(50);
  assert.equal(world.posts.length, n + 1, "one request, and only one");
  return world.posts.at(-1);
}

// statusSays waits for the footer to say text, as kind
async function statusSays(page, text, kind) {
  const s = page.locator("#status");
  for (let i = 0; i < 60 && !(await s.innerText()).includes(text); i++) await page.waitForTimeout(50);
  assert((await s.innerText()).includes(text), `status: ${await s.innerText()} (want ${text})`);
  if (kind) assert((await s.getAttribute("class")).split(" ").includes(kind), await s.getAttribute("class"));
}

// fits: nothing on the page is wider than the window
async function fits(page) {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  assert(over <= 1, `the page is ${over}px wider than the window`);
}
const openEditor = async (page) => {
  await page.locator('#providers .row.provider[data-id="relay"]').click();
  await page.locator(".editor .bar").waitFor();
};
const barButton = (page, name) => page.locator(".editor .bar").getByRole("button", { name, exact: true });
// press scrolls the way a reader does: a scroll by code within 4s of a
// click is put back where that click held the page (#1554), so at 420 wide
// the identity panel's buttons stayed below the window in WebKit
const press = async (page, b) => {
  await b.waitFor();
  await page.mouse.move(200, 400);
  for (let i = 0; i < 12 && !(await b.evaluate((e) => { const r = e.getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight; })); i++) await page.mouse.wheel(0, 200);
  await b.click();
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const width of [980, 420]) {
    for (const lang of ["en", "zh"]) {
      const w = words[lang];
      test(`${engine} ${lang} ${width}: provider writes carry the rev, and a refusal says what happened`, async (t) => {
        const { page, errors, world } = await launch(t, engine, lang, width);

        // the switch: refused as stale, it goes back and the list is read again
        world.refuse = { "provider/off": stale };
        let n = world.posts.length, reads = world.reads;
        const sw = page.locator('#providers .row.provider[data-id="relay"] .pswitch');
        await sw.click();
        assert.deepEqual(await posted(page, world, n), { action: "provider/off", body: { id: "relay", rev: "r1" } });
        await statusSays(page, w.stale, "err");
        assert(await sw.evaluate((s) => s.classList.contains("on")), "the switch stayed off");
        for (let i = 0; i < 40 && world.reads === reads; i++) await page.waitForTimeout(50);
        assert(world.reads > reads, "the list was not read again");
        await fits(page);

        // Save, stale: the editor and the draft stay, nothing is sent again
        await openEditor(page);
        const name = page.locator(".editor input").first();
        assert.equal(await name.inputValue(), "Relay");
        await name.fill("Relay X");
        world.refuse = { "provider/save": stale };
        n = world.posts.length; reads = world.reads;
        await barButton(page, w.save).click();
        let p = await posted(page, world, n);
        assert.equal(p.action, "provider/save");
        assert.equal(p.body.rev, "r1");
        assert.equal(p.body.name, "Relay X");
        await page.locator(".editor .editor-error").waitFor();
        assert((await page.locator(".editor .editor-error").innerText()).includes(w.stale));
        assert.equal(await name.inputValue(), "Relay X", "the draft was lost");
        await page.waitForTimeout(400);
        assert.equal(world.posts.length, n + 1, "a stale save was sent again");
        assert(world.reads > reads, "the list was not read again for the next open");
        await fits(page);

        // Save again: saved but for its output limits, which a Save redoes
        world.refuse = { "provider/save": saved("outputs", "disk full") };
        n = world.posts.length;
        await barButton(page, w.save).click();
        assert.equal((await posted(page, world, n)).body.rev, "r1");
        await page.locator(".editor").waitFor({ state: "detached" });
        await statusSays(page, w.outputs, "warn");
        await fits(page);

        // Remove: saved but for its sign-in, which is not to be made again
        await openEditor(page);
        world.refuse = { "provider/delete": saved("sign-in", "access denied") };
        n = world.posts.length;
        await barButton(page, w.remove).click();
        const ask = page.locator("dialog.action-confirm");
        await ask.waitFor();
        await ask.getByRole("button").last().click();
        assert.deepEqual(await posted(page, world, n), { action: "provider/delete", body: { id: "relay", rev: "r1" } });
        await page.locator(".editor").waitFor({ state: "detached" });
        await statusSays(page, w.signIn, "warn");

        // pending: finished by the journal, so the editor closes as saved
        await openEditor(page);
        world.refuse = { "provider/save": pending };
        n = world.posts.length;
        await barButton(page, w.save).click();
        await posted(page, world, n);
        await page.locator(".editor").waitFor({ state: "detached" });
        await statusSays(page, w.pending, "warn");

        // a Duplicate is a new provider: no rev
        world.refuse = {};
        await openEditor(page);
        await barButton(page, w.dup).click();
        await barButton(page, w.add).waitFor();
        n = world.posts.length;
        await barButton(page, w.add).click();
        p = await posted(page, world, n);
        assert.equal(p.body.new, true);
        assert.equal(p.body.copyOf, "relay");
        assert(!("rev" in p.body), `a copy sent rev ${p.body.rev}`);
        // the Add has finished (its state read too) before the page reloads
        await page.locator(".editor").waitFor({ state: "detached" });
        await page.waitForLoadState("networkidle");

        // Bind and Unbind carry the rev the identity panel read
        world.identity = { profiles: [profile], bindings: {}, providers: [{ id: "relay", name: "Relay", applicable: true, rev: "r1" }], armed: [], gateway: true, window: "5m0s" };
        await page.reload();
        await page.locator("#foldIdentity").click();
        const panel = page.locator("#identityPanel");
        world.refuse = { "identity/bind": stale };
        // the bindings are read again: the identity read, as the list's
        // own read goes with every Bind
        n = world.posts.length; reads = world.idReads;
        await press(page, panel.getByRole("button", { name: w.bind }));
        assert.deepEqual(await posted(page, world, n), { action: "identity/bind", body: { provider: "relay", profile: profile.id, rev: "r1" } });
        await statusSays(page, w.stale, "err");
        for (let i = 0; i < 40 && world.idReads === reads; i++) await page.waitForTimeout(50);
        assert(world.idReads > reads, "the bindings were not read again");
        world.refuse = {};
        world.identity = { ...world.identity, bindings: { relay: profile.id }, profiles: [{ ...profile, bindings: ["relay"] }] };
        n = world.posts.length;
        await press(page, panel.getByRole("button", { name: w.bind }));
        await posted(page, world, n);
        await panel.locator(".identity-bound").waitFor();
        n = world.posts.length;
        await press(page, panel.getByRole("button", { name: w.unbind }).first());
        assert.deepEqual(await posted(page, world, n), { action: "identity/unbind", body: { provider: "relay", rev: "r1" } });
        await fits(page);
        assert.deepEqual(errors, []);
      });
    }
  }

  // an account's Save sends its rev, and the editor's Unbind the one the
  // identity read gave (r2 here, the list's is r1)
  for (const [lang, width] of [["en", 980], ["zh", 420]]) {
    const w = words[lang];
    test(`${engine} ${lang} ${width}: an account's Save and the editor's Unbind carry the rev`, async (t) => {
      const { page, errors, world } = await launch(t, engine, lang, width, (world) => {
        world.providers = [relay, sub];
        world.identity = { profiles: [{ ...profile, bindings: ["relay"] }], bindings: { relay: profile.id }, providers: [{ id: "relay", name: "Relay", applicable: true, profileId: profile.id, rev: "r2" }], armed: [], gateway: true, window: "5m0s" };
      });
      await page.locator('#providers .row.provider[data-id="workbuddy-ai"]').click();
      await page.locator(".editor .accts").first().waitFor();
      let n = world.posts.length;
      await barButton(page, w.save).click();
      const p = await posted(page, world, n);
      assert.equal(p.action, "provider/save");
      assert.equal(p.body.rev, "s1");
      await page.locator(".editor").waitFor({ state: "detached" });
      await openEditor(page);
      n = world.posts.length;
      await press(page, page.locator(".editor .identity-field").getByRole("button", { name: w.fieldUnbind, exact: true }));
      assert.deepEqual(await posted(page, world, n), { action: "identity/unbind", body: { provider: "relay", rev: "r2" } });
      await fits(page);
      assert.deepEqual(errors, []);
    });
  }

  // an editor's Save with a picker's picks unsaved (#2862): the picks go
  // first, with the rev the editor read (r1), and write the provider, whose
  // rev their answer gives (r2); the Save after them carries r2, so its own
  // picks don't make it stale. A key's picker and an account's alike.
  const keyed = { ...relay, keyList: [{ id: "aaaaaaaaaa", name: "Team", masked: "sk-…1234", on: true, active: true }, { id: "bbbbbbbbbb", name: "", masked: "sk-…5678", on: true }] };
  const subs = { ...sub, account: { ...sub.account, logins: [...sub.account.logins, { user: "two@example.test", plan: "Pro", on: true }] } };
  for (const [lang, width] of [["en", 980], ["zh", 420]]) {
    for (const [p, ref, model] of [[keyed, "bbbbbbbbbb", "M1"], [subs, "two@example.test", "GLM-5"]]) {
      test(`${engine} ${lang} ${width}: ${p.id}'s Save after its own picks sends the rev they left`, async (t) => {
        const others = p.id === relay.id ? [] : [relay];
        const { page, errors, world } = await launch(t, engine, lang, width, (world) => {
          world.providers = [...others, p];
          world.wrote = { "provider/accountmodels": () => { world.providers = [...others, { ...p, rev: p.rev + "+", accountModels: { [ref]: [p.models[0].id] } }]; } };
        });
        await page.locator(`#providers .row.provider[data-id="${p.id}"]`).click();
        const acc = page.locator(`.editor .accts .acc[data-account-id="${ref}"]`);
        await acc.hover();
        await acc.locator(".amodels").click();
        await acc.locator(".acct-models .mchip", { hasText: model }).click();
        const n = world.posts.length;
        await page.locator(".editor > .bar button.primary").click();
        await page.locator(".editor").waitFor({ state: "detached" });
        const [picks, save] = world.posts.slice(n);
        assert.deepEqual(picks, { action: "provider/accountmodels", body: { id: p.id, account: ref, allow: [p.models[0].id], rev: p.rev } });
        assert.equal(save.action, "provider/save");
        assert.equal(save.body.rev, p.rev + "+", "the Save carried the rev the editor read, not the one its picks left");
        assert.equal(world.posts.length, n + 2);
        await fits(page);
        assert.deepEqual(errors, []);
      });
    }
  }

  // the other languages say each answer in their own words, at 420 wide
  for (const lang of ["zh-TW", "ja", "de"]) {
    const w = words[lang];
    test(`${engine} ${lang} 420: stale, saved but for a step, and pending are said in ${lang}`, async (t) => {
      const { page, errors, world } = await launch(t, engine, lang, 420);
      world.refuse = { "provider/off": stale };
      await page.locator('#providers .row.provider[data-id="relay"] .pswitch').click();
      await statusSays(page, w.stale, "err");
      await fits(page);
      for (const [answer, said] of [[saved("outputs", "disk full"), w.outputs], [pending, w.pending]]) {
        await openEditor(page);
        world.refuse = { "provider/save": answer };
        const n = world.posts.length;
        await page.locator(".editor .bar button.primary").click();
        await posted(page, world, n);
        await page.locator(".editor").waitFor({ state: "detached" });
        await statusSays(page, said, "warn");
        await fits(page);
      }
      assert.deepEqual(errors, []);
    });
  }
}
