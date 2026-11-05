// Run with Node's test runner and Playwright on the module path; see README.md.
// "Count its models as free" (jiabubu on Discord: ModelScope's free daily
// quota) is the user's word that a provider costs them nothing, so Free
// first spends it first. fork: it is the editor's billing control's Free
// quota now (billing.js), in the main area after the models, not under
// Advanced: it opens as saved, leaves Advanced folded, and Save posts
// countFree: for a provider of keys and for a signed-in account. In every
// language, in both engines, wide and narrow.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const base = {
  icon: "generic", chat: "", responses: "", anthropic: "", catalog: "",
  models: [{ id: "qwen3", name: "Qwen3", on: true }], agents: [], fallback: [], headers: {},
  key: { set: true, masked: "ms-…1234" }, keyList: [], balanceToken: { takes: false, set: false }, proxy: "", searches: false, unredacted: false,
};
const scope = { ...base, id: "modelscope", name: "ModelScope", preset: "modelscope", chat: "https://api-inference.modelscope.cn/v1" };
const counted = { ...base, id: "free-relay", name: "Free Relay", chat: "https://relay.example.com/v1", countFree: true, billingMode: "quota" };
const codex = {
  id: "codex", name: "Codex", icon: "codex-color", chat: "", responses: "", anthropic: "", catalog: "",
  models: [{ id: "gpt-6", name: "GPT-6", on: true }], agents: [], fallback: [], headers: {}, keyList: [],
  account: { agent: "codex", agentName: "Codex", user: "me@example.com", plan: "PLUS", logins: [{ user: "me@example.com", plan: "PLUS", active: true, on: true }] },
  proxy: "", billingMode: "list",
};
scope.billingMode = "list";

function serve(lang, posts) {
  const providers = { providers: [scope, counted, codex], presets: [], excluded: [], gateway: { running: true, window: true } };
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

const QUOTA = "Free quota";
const STRINGS = [
  "Billing", "List price", QUOTA,
  "For a free quota, like a daily allowance: Free first uses its models before paid ones, and Cheapest first counts them as costing nothing. Prices and the costs Usage counts stay as listed.",
];

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [900, 440]) {
      const open = async (t, name) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        let page;
        t.after(async () => {
          if (process.env.ARTIFACT_DIR && page) {
            await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
            await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-${width}-${name.replace(/\W+/g, "-")}-count-free.png`), fullPage: true });
          }
          await browser.close();
        });
        page = await (await browser.newContext({ viewport: { width, height: 760 }, reducedMotion: "reduce" })).newPage();
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        const errors = [];
        page.on("pageerror", (e) => errors.push(e.message));
        const posts = [];
        await page.route("**/*", serve(lang, posts));
        await page.goto("http://magpie.test/?view=providers");
        await page.locator(".row.provider", { hasText: name }).click();
        await page.locator(".editor .bar").waitFor();
        const tick = await page.evaluate((k) => t(k), QUOTA);
        return { page, errors, posts, tick };
      };
      const adv = (page) => page.locator(".editor details.advanced");
      const seg = (page) => page.locator(".editor .segs.billing-mode");
      const box = (page, tick) => seg(page).getByRole("button", { name: tick, exact: true });
      const pressed = async (page, tick) => (await box(page, tick).getAttribute("aria-pressed")) === "true";
      const save = async (page, posts) => {
        const n = posts.length;
        const label = await page.evaluate(() => t("Save"));
        await page.locator(".editor .bar").getByRole("button", { name: label, exact: true }).click();
        for (let i = 0; i < 50 && posts.length === n; i++) await page.waitForTimeout(50);
        return posts.at(-1);
      };
      const fits = async (page, tick) => {
        const r = await seg(page).boundingBox();
        assert.ok(r && r.width > 60 && r.x >= 0 && r.x + r.width <= width, `the control fits: ${JSON.stringify(r)}`);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, "no sideways scroll");
        assert.equal(await box(page, tick).textContent(), tick);
        // in the main area, after the models: never inside Advanced
        assert.equal(await seg(page).evaluate((s) => !s.closest("details.advanced")), true, "not under Advanced");
        const after = await seg(page).evaluate((s) => {
          const models = [...document.querySelectorAll(".editor .mchip")].at(-1);
          return !!models && !!(models.compareDocumentPosition(s) & Node.DOCUMENT_POSITION_FOLLOWING);
        });
        assert.equal(after, true, "after the models");
      };

      test(`${engine} ${lang} ${width}: ModelScope is counted free and saved so`, async (t) => {
        const { page, errors, posts, tick } = await open(t, "ModelScope");
        assert.equal(await adv(page).evaluate((d) => d.open), false, "nothing set opens Advanced");
        assert.equal(await pressed(page, tick), false, "opens on List price");
        await box(page, tick).scrollIntoViewIfNeeded();
        await fits(page, tick);
        await box(page, tick).click();
        const saved = await save(page, posts);
        assert.equal(saved.action, "save");
        assert.equal(saved.body.id, "modelscope");
        assert.equal(saved.body.countFree, true);
        assert.equal(saved.body.modelPrefs, undefined, "Free quota writes no price");
        if (lang !== "en") {
          const missing = await page.evaluate(([l, ks]) => ks.filter((k) => !I18N[l][k]), [lang, STRINGS]);
          assert.deepEqual(missing, [], `every string has its ${lang}`);
        }
        assert.deepEqual(errors, []);
      });

      test(`${engine} ${lang} ${width}: one saved counted free opens on Free quota, and is saved at List price`, async (t) => {
        const { page, errors, posts, tick } = await open(t, "Free Relay");
        assert.equal(await adv(page).evaluate((d) => d.open), false, "the control isn't in Advanced, so it doesn't open it");
        assert.equal(await pressed(page, tick), true);
        const list = await page.evaluate(() => t("List price"));
        await box(page, list).scrollIntoViewIfNeeded();
        await box(page, list).click();
        const saved = await save(page, posts);
        assert.equal(saved.body.id, "free-relay");
        assert.equal(saved.body.countFree, false);
        assert.equal(saved.body.modelPrefs, undefined, "nothing was free to take off");
        assert.deepEqual(errors, []);
      });

      test(`${engine} ${lang} ${width}: a signed-in account is counted free too`, async (t) => {
        const { page, errors, posts, tick } = await open(t, "Codex");
        assert.equal(await pressed(page, tick), false);
        await box(page, tick).scrollIntoViewIfNeeded();
        await fits(page, tick);
        await box(page, tick).click();
        const saved = await save(page, posts);
        assert.equal(saved.action, "save");
        assert.equal(saved.body.id, "codex");
        assert.equal(saved.body.countFree, true);
        assert.deepEqual(errors, []);
      });
    }
  }
}
