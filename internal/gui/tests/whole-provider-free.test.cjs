// Run with Node's test runner and Playwright on the module path; see README.md.
// fork: the editors' billing control (billing.js), List price / Free /
// Free quota, in the main area after the models. Free prices every model
// at nothing (`<id>/*` = 0): Save sends model prefs "*" with a price of
// nothing and countFree off; List price takes it off (OwnPrice); Free
// quota takes it off and sends countFree; chosen and chosen back, nothing;
// Escape and Discard send nothing. It opens on the mode the server says
// (billingMode). A `<id>/*` priced otherwise, or one that doesn't read, is
// never written over: Free can't be pressed, the hint says the price or
// the part and the command that takes it off, and the other two still
// save countFree. A new provider's Free waits until it is added. A model's
// own paid price keeps it off the free badge. Keyboard: Tab passes over a
// Free that can't be chosen, Enter picks, Enter on Save saves. Nothing
// scrolls on a click, no page error. In every language, Chromium and
// WebKit, wide and narrow.
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
  billingMode: "list", ...more,
});
const freeModels = [{ id: "model-a", name: "Model A", on: true, free: true, priced: "provider" }, { id: "model-p", name: "Model P", on: true, priced: "paid" }];
const providers = {
  providers: [
    relay("rv", "Plain Relay"),
    relay("zr", "Zero Relay", { whole: { free: true }, billingMode: "free", models: freeModels }),
    relay("bo", "Both Relay", { whole: { free: true }, billingMode: "free", countFree: true }),
    relay("qr", "Quota Relay", { billingMode: "quota", countFree: true }),
    relay("cl", "Priced Relay", { whole: { price: { input: 0.5, output: 2, cache_read: 0.05, cache_write: 0.625 } }, billingLocked: true }),
    relay("bk", "Broken Relay", { whole: { bad: "output" }, billingLocked: true }),
    {
      id: "codex", name: "Codex", icon: "codex-color", chat: "", responses: "", anthropic: "", catalog: "",
      models: [{ id: "gpt-6", name: "GPT-6", on: true }], agents: [], fallback: [], headers: {}, keyList: [],
      account: { agent: "codex", agentName: "Codex", user: "me@example.com", plan: "PLUS", logins: [{ user: "me@example.com", plan: "PLUS", active: true, on: true }] },
      proxy: "", maxConcurrency: null, billingMode: "list",
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
  en: { label: "Billing", list: "times Price rate", free: "Price rate no longer applies", bad: "doesn't read (output)", added: "once it is added" },
  zh: { label: "计费", list: "乘以价格倍率", free: "价格倍率不再起作用", bad: "写坏了（output）", added: "添加之后" },
  "zh-TW": { label: "計費", list: "乘以價格倍率", free: "價格倍率不再起作用", bad: "寫壞了（output）", added: "新增之後" },
  ja: { label: "課金", list: "価格倍率を掛けて", free: "価格倍率は適用されません", bad: "読み取れません（output）", added: "追加した後" },
  de: { label: "Abrechnung", list: "mal Preisfaktor", free: "der Preisfaktor gilt nicht mehr", bad: "nicht lesbar (output)", added: "sobald er hinzugefügt ist" },
};
const ZERO = { "*": { price: { input: 0, output: 0, cache_read: 0, cache_write: 0 } } };
const OWN = { "*": { ownPrice: true } };

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of Object.keys(said)) {
    for (const width of [900, 440]) {
      const w = said[lang];
      test(`${engine} ${lang} ${width}: the billing control writes the legacy fields`, async (t) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        const page = await (await browser.newContext({ viewport: { width, height: 760 }, reducedMotion: "reduce" })).newPage();
        t.after(async () => {
          if (process.env.ARTIFACT_DIR) {
            await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
            await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-${width}-billing-control.png`) });
          }
          await browser.close();
        });
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        const errors = [];
        page.on("pageerror", (e) => errors.push(e.message));
        const posts = [];
        await page.route("**/*", serve(lang, posts));
        await page.goto("http://magpie.test/?view=providers");
        const ui = await page.evaluate(() => ({ save: t("Save"), list: t("List price"), free: t("Free"), quota: t("Free quota") }));

        const editor = page.locator("#modal:not([hidden]) .editor");
        const seg = editor.locator(".segs.billing-mode");
        const open = async (name) => {
          await page.locator(".row.provider", { hasText: name }).click();
          await editor.locator(".ehead b", { hasText: name }).waitFor();
          await seg.waitFor();
        };
        const close = async () => {
          await page.keyboard.press("Escape");
          await page.locator("#modal").waitFor({ state: "hidden" });
        };
        const row = () => seg.evaluate((s) => {
          const wrap = s.parentElement, label = wrap.previousElementSibling, r = s.getBoundingClientRect();
          return {
            label: label.textContent, says: wrap.querySelector(".billing-says")?.textContent || "", why: wrap.querySelector(".billing-why")?.textContent || "",
            on: s.querySelector(".opt.on")?.textContent, disabled: [...s.querySelectorAll(".opt")].map((b) => b.disabled),
            advanced: !!s.closest("details.advanced"), fits: r.width > 60 && r.left >= 0 && r.right <= innerWidth && document.documentElement.scrollWidth <= innerWidth,
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

        // none saved: List price; Free saves a price of nothing for "*"
        await open("Plain Relay");
        let r = await row();
        assert.equal(r.label, w.label);
        assert(r.says.includes(w.list), `the hint says the list price: ${r.says}`);
        assert.equal(r.why, "");
        assert.equal(r.advanced, false, "in the main area");
        assert(r.fits, "fits the editor, no sideways scroll");
        assert.equal(r.on, ui.list);
        assert.deepEqual(r.disabled, [false, false, false]);
        await pick(ui.free).scrollIntoViewIfNeeded();
        const before = await scrolled();
        await pick(ui.free).click();
        assert.equal(await scrolled(), before, "the page moved");
        assert((await row()).says.includes(w.free), "the hint follows the choice");
        let saved = await save();
        assert.equal(saved.action, "save");
        assert.equal(saved.body.id, "rv");
        assert.deepEqual(saved.body.modelPrefs, ZERO);
        assert.equal(saved.body.countFree, false);

        // chosen and chosen back: nothing staged
        await open("Plain Relay");
        await pick(ui.free).click();
        await pick(ui.list).click();
        saved = await save();
        assert.equal(saved.body.modelPrefs, undefined, "chosen back, nothing is sent");
        assert.equal(saved.body.countFree, false);

        // chosen, then Escape and Discard: nothing saved
        await open("Plain Relay");
        await pick(ui.quota).click();
        const n = posts.length;
        await page.keyboard.press("Escape");
        await page.locator("dialog.action-confirm[open] button").last().click();
        await page.locator("#modal").waitFor({ state: "hidden" });
        assert.equal(posts.length, n, "Escape saved");

        // saved free: List price takes it off; a model's own paid price
        // keeps it off the free badge
        await open("Zero Relay");
        r = await row();
        assert.equal(r.on, ui.free);
        assert.equal(await editor.locator(".mchip", { hasText: "Model A" }).locator(".badge.free").count(), 1, "free by the provider's price");
        assert.equal(await editor.locator(".mchip", { hasText: "Model P" }).locator(".badge.free").count(), 0, "a model's own paid price stays paid");
        await pick(ui.list).click();
        saved = await save();
        assert.equal(saved.body.id, "zr");
        assert.deepEqual(saved.body.modelPrefs, OWN);
        assert.equal(saved.body.countFree, false);

        // saved free with countFree too (the wildcard wins): Free quota
        // takes the price off and keeps countFree
        await open("Both Relay");
        assert.equal((await row()).on, ui.free);
        await pick(ui.quota).click();
        saved = await save();
        assert.deepEqual(saved.body.modelPrefs, OWN);
        assert.equal(saved.body.countFree, true);
        // and pressing the Free it opened on changes nothing
        await open("Both Relay");
        await pick(ui.free).click();
        saved = await save();
        assert.equal(saved.body.modelPrefs, undefined);
        assert.equal(saved.body.countFree, true, "opened and saved, it stays as it was");

        // saved Free quota: Free clears countFree and prices it at nothing
        await open("Quota Relay");
        assert.equal((await row()).on, ui.quota);
        await pick(ui.free).click();
        saved = await save();
        assert.deepEqual(saved.body.modelPrefs, ZERO);
        assert.equal(saved.body.countFree, false);

        // priced from the command line: Free can't be pressed and nothing
        // is written over; Free quota still saves countFree
        await open("Priced Relay");
        r = await row();
        assert.equal(r.on, ui.list);
        assert.deepEqual(r.disabled, [false, true, false]);
        assert(r.why.includes("0.5,2,0.05,0.625"), `the price is said: ${r.why}`);
        assert(r.why.includes("magpie model price \"cl/*\" --reset"), `the command is said: ${r.why}`);
        await pick(ui.free).click({ force: true });
        assert.equal((await row()).on, ui.list, "a press changes nothing");
        await pick(ui.quota).click();
        saved = await save();
        assert.equal(saved.body.modelPrefs, undefined, "nothing written over");
        assert.equal(saved.body.countFree, true);

        // keyboard: Tab from List price passes over the Free it can't
        // choose; Enter picks, and Enter on Save saves. macOS WebKit
        // doesn't Tab to buttons, so there the option is focused directly.
        await open("Priced Relay");
        if (engine === "chromium") {
          await pick(ui.list).focus();
          await page.keyboard.press("Tab");
        } else await pick(ui.quota).focus();
        assert.equal(await pick(ui.quota).evaluate((b) => b === document.activeElement), true, "focus is on Free quota");
        await page.keyboard.press("Enter");
        assert.equal((await row()).on, ui.quota);
        const k = posts.length;
        await editor.locator(".bar").getByRole("button", { name: ui.save, exact: true }).focus();
        await page.keyboard.press("Enter");
        for (let i = 0; i < 100 && posts.length === k; i++) await page.waitForTimeout(30);
        assert.equal(posts.length, k + 1, "Enter on Save saved");
        assert.equal(posts.at(-1).body.countFree, true);
        assert.equal(posts.at(-1).body.modelPrefs, undefined);

        // one that doesn't read: the part said, left alone
        await open("Broken Relay");
        r = await row();
        assert.deepEqual(r.disabled, [false, true, false]);
        assert(r.why.includes(w.bad), `the part is said: ${r.why}`);
        assert(r.why.includes("magpie model price \"bk/*\" --reset"));
        await close();

        // a signed-in account: the same control, Free by its model prefs
        await open("Codex");
        r = await row();
        assert.equal(r.advanced, false);
        assert.deepEqual(r.disabled, [false, false, false]);
        await pick(ui.free).click();
        saved = await save();
        assert.equal(saved.body.id, "codex");
        assert.deepEqual(saved.body.modelPrefs, ZERO);
        assert.equal(saved.body.countFree, false);

        // a new provider: Free waits until it is added (opened as the Add
        // sheet's Custom does: a fresh draft, not the last one)
        await page.evaluate(() => { editing = { custom: true }; draft = null; renderProviders(); });
        await editor.locator(".ehead b", { hasText: await page.evaluate(() => t("Custom provider")) }).waitFor();
        await seg.waitFor();
        r = await row();
        assert.equal(r.on, ui.list);
        assert.deepEqual(r.disabled, [false, true, false]);
        assert(r.why.includes(w.added), `the hint says when: ${r.why}`);

        assert.deepEqual(errors, []);
      });
    }
  }
}
