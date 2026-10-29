// Run with Node's test runner and Playwright on the module path; see README.md.
// fork: a saved key provider's editor has, under Advanced after Price
// rate, an Off/On that prices every model of it at nothing (`<id>/*` = 0,
// read back as the provider's whole, provider.WholeOf). Turned on, Save
// sends model prefs "*" with a price of nothing; turned off, OwnPrice;
// turned and turned back, nothing; Escape and Discard send nothing. A
// `<id>/*` priced otherwise, or one that doesn't read, is shown and left
// alone: both buttons can't be pressed, and the hint says the price or
// the part and the command that takes it off. A new provider's editor
// and a signed-in account's have no such row. Nothing scrolls on a click,
// no page error. In every language, Chromium and WebKit.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const relay = (id, name, more = {}) => ({
  id, name, icon: "generic", chat: `https://${id}.example.com/v1`, responses: "", anthropic: "", catalog: "",
  models: [{ id: "model-a", name: "Model A", on: true }], agents: [], fallback: [], headers: {},
  key: { set: true, masked: "sk-…one" }, keyList: [], balanceToken: { takes: false, set: false }, proxy: "", maxConcurrency: null,
  ...more,
});
const providers = {
  providers: [
    relay("rv", "Plain Relay"),
    relay("zr", "Zero Relay", { whole: { free: true } }),
    relay("cl", "Priced Relay", { whole: { price: { input: 0.5, output: 2, cache_read: 0.05, cache_write: 0.625 } } }),
    relay("bk", "Broken Relay", { whole: { bad: "output" } }),
    {
      id: "codex", name: "Codex", icon: "codex-color", chat: "", responses: "", anthropic: "", catalog: "",
      models: [{ id: "gpt-6", name: "GPT-6", on: true }], agents: [], fallback: [], headers: {}, keyList: [],
      account: { agent: "codex", agentName: "Codex", user: "me@example.com", plan: "PLUS", logins: [{ user: "me@example.com", plan: "PLUS", active: true, on: true }] },
      proxy: "", maxConcurrency: null,
    },
  ],
  presets: [], excluded: [], gateway: { running: true, window: true },
};

function serve(lang, posts) {
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/providers") return json(providers);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname.startsWith("/api/provider/") && route.request().method() === "POST") {
      posts.push({ action: url.pathname.slice("/api/provider/".length), body: route.request().postDataJSON() });
      return json(providers);
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

// the words as each language says them, the price label included
const said = {
  en: { label: "Whole provider free", hint: "Price rate no longer applies", bad: "doesn't read (output)" },
  zh: { label: "整个供应商免费", hint: "价格倍率不再起作用", bad: "写坏了（output）" },
  "zh-TW": { label: "整個供應商免費", hint: "價格倍率不再起作用", bad: "寫壞了（output）" },
  ja: { label: "プロバイダー全体を無料に", hint: "価格倍率は適用されません", bad: "読み取れません（output）" },
  de: { label: "Ganzer Anbieter kostenlos", hint: "der Preisfaktor gilt nicht mehr", bad: "nicht lesbar (output)" },
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of Object.keys(said)) {
    const w = said[lang];
    test(`${engine} ${lang}: a whole provider is priced at nothing from its editor`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await (await browser.newContext({ viewport: { width: 900, height: 760 }, reducedMotion: "reduce" })).newPage();
      t.after(async () => {
        if (process.env.ARTIFACT_DIR) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-whole-provider-free.png`) });
        }
        await browser.close();
      });
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      const posts = [];
      await page.route("**/*", serve(lang, posts));
      await page.goto("http://magpie.test/?view=providers");
      const ui = await page.evaluate(() => ({ save: t("Save"), off: t("Off"), on: t("On"), rate: t("Price rate") }));

      const editor = page.locator("#modal:not([hidden]) .editor");
      const seg = editor.locator(".segs.whole-free");
      const open = async (name) => {
        await page.locator(".row.provider", { hasText: name }).click();
        await editor.locator(".ehead b", { hasText: name }).waitFor();
        const adv = editor.locator("details.advanced");
        await adv.waitFor({ state: "attached" });
        if (!(await adv.evaluate((d) => d.open))) await adv.locator(":scope > summary").click();
        await editor.locator("input.price-rate").waitFor();
      };
      const row = () => seg.evaluate((s) => {
        const wrap = s.parentElement, label = wrap.previousElementSibling;
        return {
          label: label.textContent, hint: wrap.querySelector(".hint")?.textContent || "",
          on: s.querySelector(".opt.on")?.textContent, disabled: [...s.querySelectorAll(".opt")].map((b) => b.disabled),
          afterRate: !!label.previousElementSibling?.querySelector("input.price-rate"),
        };
      });
      const pick = (name) => seg.getByRole("button", { name, exact: true });
      const save = async () => {
        const n = posts.length;
        await editor.locator(".bar").getByRole("button", { name: ui.save, exact: true }).click();
        for (let i = 0; i < 100 && posts.length === n; i++) await page.waitForTimeout(30);
        assert.equal(posts.length, n + 1, "nothing saved");
        return posts.at(-1);
      };
      // where each box is scrolled to; .holds (the box under the pointer
      // keeps the wheel, app.js) comes and goes with hovering, not scrolling
      const scrolled = () => page.evaluate(() => [window.scrollY, ...[...document.querySelectorAll("*")].filter((e) => e.scrollTop > 0).map((e) => `${[...e.classList].filter((c) => c !== "holds").join(" ")}:${e.scrollTop}`)].join(" "));

      // none saved: Off, and On saves a price of nothing for "*"
      await open("Plain Relay");
      let r = await row();
      assert.equal(r.label, w.label);
      assert(r.hint.includes(w.hint), `the hint says Price rate stops: ${r.hint}`);
      assert(r.afterRate, "after Price rate");
      assert.equal(r.on, ui.off);
      assert.deepEqual(r.disabled, [false, false]);
      await pick(ui.on).scrollIntoViewIfNeeded();
      const before = await scrolled();
      await pick(ui.on).click();
      assert.equal(await scrolled(), before, "the page moved");
      let saved = await save();
      assert.equal(saved.action, "save");
      assert.equal(saved.body.id, "rv");
      assert.deepEqual(saved.body.modelPrefs, { "*": { price: { input: 0, output: 0, cache_read: 0, cache_write: 0 } } });

      // turned on and back: nothing staged, so no model prefs
      await open("Plain Relay");
      await pick(ui.on).click();
      await pick(ui.off).click();
      saved = await save();
      assert.equal(saved.body.modelPrefs, undefined, "turned back, nothing is sent");

      // turned on, then Escape and Discard: nothing saved
      await open("Plain Relay");
      await pick(ui.on).click();
      const n = posts.length;
      await page.keyboard.press("Escape");
      await page.locator("dialog.action-confirm[open] button").last().click();
      await page.locator("#modal").waitFor({ state: "hidden" });
      assert.equal(posts.length, n, "Escape saved");

      // saved free: On, and Off takes it off
      await open("Zero Relay");
      r = await row();
      assert.equal(r.on, ui.on);
      assert.deepEqual(r.disabled, [false, false]);
      await pick(ui.off).click();
      saved = await save();
      assert.equal(saved.body.id, "zr");
      assert.deepEqual(saved.body.modelPrefs, { "*": { ownPrice: true } });

      // priced from the command line: shown and left alone
      await open("Priced Relay");
      r = await row();
      assert.equal(r.on, ui.off);
      assert.deepEqual(r.disabled, [true, true]);
      assert(r.hint.includes("0.5,2,0.05,0.625"), `the price is said: ${r.hint}`);
      assert(r.hint.includes("magpie model price \"cl/*\" --reset"), `the command is said: ${r.hint}`);
      await pick(ui.on).click({ force: true });
      assert.equal((await row()).on, ui.off, "a press changes nothing");
      saved = await save();
      assert.equal(saved.body.modelPrefs, undefined, "nothing written over");

      // one that doesn't read: the part said, left alone
      await open("Broken Relay");
      r = await row();
      assert.deepEqual(r.disabled, [true, true]);
      assert(r.hint.includes(w.bad), `the part is said: ${r.hint}`);
      assert(r.hint.includes("magpie model price \"bk/*\" --reset"));
      await page.keyboard.press("Escape");
      await page.locator("#modal").waitFor({ state: "hidden" });

      // a signed-in account's editor and a new provider's have no such row
      await open("Codex");
      assert.equal(await seg.count(), 0, "an account's editor has it");
      await page.keyboard.press("Escape");
      await page.locator("#modal").waitFor({ state: "hidden" });
      await page.evaluate(() => openModal(renderEditor(null)));
      await editor.waitFor();
      const adv = editor.locator("details.advanced");
      if (await adv.count()) await adv.evaluate((d) => { d.open = true; });
      assert.equal(await seg.count(), 0, "a new provider's editor has it");

      assert.deepEqual(errors, []);
    });
  }
}
