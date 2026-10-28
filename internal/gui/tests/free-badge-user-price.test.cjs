// Run with Node's test runner and Playwright on the module path; see README.md.
// fork: the green FREE goes by the user's own price (provider.FreeBy,
// sent as a model's free and priced). A relay's model the user priced at
// nothing has it, its title saying they priced it at 0; every model of a
// provider priced at nothing has it, its title saying so for the
// provider. A model the user gave a price has none, though its name says
// free, and Free only leaves it out. With no price of the user's, the
// vendor's free model and one named free are as before. In the provider
// editor's chips, Free only and an agent's model picker; nothing scrolls,
// no page error. In every language, Chromium and WebKit.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const fetched = new Date(Date.now() - 3600e3).toISOString();
const relay = (id, name, models, chosen) => ({
  id, name, icon: "generic", host: id + ".example.com", chat: `https://${id}.example.com/v1`, responses: "", anthropic: "", catalog: "",
  models, chosen, fetched, agents: [], fallback: [], headers: {},
  key: { set: true, masked: "sk-…one" }, keyList: [], balanceToken: { takes: false, set: false }, proxy: "",
});
// as /api/providers sends them: free and priced from provider.FreeBy
const m = (id, more = {}) => ({ id, name: id, on: false, ...more });
const rv = relay("rv", "RV Relay", [
  m("zeroed", { free: true, priced: "model" }), // the user priced it at nothing
  m("qwen/qwen3.8-27b:free", { priced: "paid" }), // named free, the user gave it a price
  m("vpriced", { priced: "paid" }), // the vendor's free one, the user gave it a price
  m("vendor-free-one", { free: true }), // the vendor's free one, no price of the user's
  m("glm-5:free"), // named free, no price of the user's
  m("plain"),
], ["plain"]);
const all = relay("allzero", "All Zero", [m("a1", { free: true, priced: "provider" }), m("a2", { free: true, priced: "provider" })], ["a1"]);
const opt = (provider, id, more = {}) => ({ value: `magpie/${provider}/${id}`, ref: `${provider}/${id}`, label: id, note: "RV · via magpie", group: provider, ...more });
const options = [
  opt("rv", "zeroed", { free: true, priced: "model" }),
  opt("allzero", "a1", { free: true, priced: "provider" }),
  opt("rv", "qwen/qwen3.8-27b:free", { priced: "paid" }),
  opt("rv", "vendor-free-one", { free: true }),
  opt("rv", "plain"),
];
const agents = [{ id: "claude", name: "Claude Code", path: "/test/claude", wired: true, fields: [{ key: "model", label: "model", value: "magpie/rv/plain", options }] }];

function serve(lang, posts) {
  const providers = { providers: [rv, all], presets: [], excluded: [], gateway: { running: true, window: true } };
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (req.method() === "POST" && url.pathname.startsWith("/api/")) posts.push({ path: url.pathname, body: req.postDataJSON() || {} });
    if (url.pathname === "/api/state") return json({ agents, profiles: [], settings: { lang, theme: "light" } });
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/agents/cli") return json({ agents: {}, pending: false });
    if (url.pathname === "/api/providers") return json(providers);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

// the two new words, as each language says them
const said = {
  en: ["free: you priced it at 0", "free: you priced every model of this provider at 0"],
  zh: ["免费：你给它设的价格是 0", "免费：你给这个供应商的所有模型设的价格是 0"],
  "zh-TW": ["免費：你給它設的價格是 0", "免費：你給這個供應商的所有模型設的價格是 0"],
  ja: ["無料：価格を 0 に設定しています", "無料：このプロバイダーの全モデルの価格を 0 に設定しています"],
  de: ["kostenlos: du hast den Preis auf 0 gesetzt", "kostenlos: du hast alle Modelle dieses Anbieters auf 0 gesetzt"],
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of Object.keys(said)) {
    const [model, provider] = said[lang];
    test(`${engine} ${lang}: the free badge goes by the user's own price`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await (await browser.newContext({ viewport: { width: 900, height: 600 }, reducedMotion: "reduce" })).newPage();
      t.after(async () => {
        if (process.env.ARTIFACT_DIR) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-free-badge-user-price.png`) });
        }
        await browser.close();
      });
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      const posts = [];
      await page.route("**/*", serve(lang, posts));
      await page.goto("http://magpie.test/?view=providers");
      const w = await page.evaluate(() => ({
        plan: t("free: it doesn't use the plan's credits"), name: t("free: so its name says"),
        freeOnly: t("Free only"), save: t("Save"),
      }));

      const editor = page.locator("#modal:not([hidden]) .editor");
      const open = async (name) => {
        await page.locator(".row.provider", { hasText: name }).click();
        await editor.locator(".ehead b", { hasText: name }).waitFor();
        await editor.locator(".mchips .mchip").first().waitFor();
      };
      // each chip: its id, whether it has the badge, and its title's word
      // after the id (its free word, when it has one)
      const chips = () => editor.locator(".mchips .mchip").evaluateAll((cs) => Object.fromEntries(cs.map((c) => {
        const id = c.querySelector("span").textContent;
        return [id, [!!c.querySelector(".badge.free"), (c.title || "").split("\n")[0].replace(id + " · ", "")]];
      })));

      // every model of a provider priced at nothing
      await open("All Zero");
      const a = await chips();
      assert.deepEqual([a.a1, a.a2], [[true, provider], [true, provider]]);
      await page.keyboard.press("Escape");
      await page.locator("#modal").waitFor({ state: "hidden" });

      // one model priced at nothing, two the user gave a price, the
      // vendor's and the name's own free beside them
      await open("RV Relay");
      const r = await chips();
      assert.deepEqual(r.zeroed, [true, model]);
      assert.equal(r["qwen/qwen3.8-27b:free"][0], false, "a model the user gave a price has no badge, though named free");
      assert.equal(r.vpriced[0], false);
      assert.deepEqual(r["vendor-free-one"], [true, w.plan]);
      assert.deepEqual(r["glm-5:free"], [true, w.name]);
      assert.equal(r.plain[0], false);
      const border = await page.evaluate(() => [...document.querySelectorAll(".mchip, .mchip *")].map((e) => getComputedStyle(e).borderLeftWidth).filter((b) => parseFloat(b) > 1));
      assert.deepEqual(border, [], "no border stripes");

      // Free only leaves out the ones the user gave a price, and moves nothing
      const scrolled = () => page.evaluate(() => [window.scrollY, ...[...document.querySelectorAll("*")].filter((e) => e.scrollTop > 0).map((e) => `${e.className}:${e.scrollTop}`)].join(" "));
      const freeOnly = editor.locator(".mbulk").getByRole("button", { name: w.freeOnly, exact: true });
      await freeOnly.scrollIntoViewIfNeeded();
      const before = await scrolled();
      await freeOnly.click();
      assert.equal(await scrolled(), before, "the page moved");
      const picks = await editor.locator(".mchips .mchip.on").evaluateAll((cs) => cs.map((c) => c.querySelector("span").textContent).sort());
      assert.deepEqual(picks, ["glm-5:free", "vendor-free-one", "zeroed"]);
      await editor.locator(".bar").getByRole("button", { name: w.save, exact: true }).click();
      for (let i = 0; i < 100 && !posts.some((p) => p.path === "/api/provider/save"); i++) await page.waitForTimeout(30);
      const saved = posts.find((p) => p.path === "/api/provider/save");
      assert(saved, "nothing saved");
      assert.deepEqual([...saved.body.models].sort(), ["glm-5:free", "vendor-free-one", "zeroed"]);

      // an agent's model picker
      await page.goto("http://magpie.test/?view=agents");
      await page.locator('.row.agent[data-id="claude"] > .field.ag-start[data-key="model"]').click();
      const list = page.locator("#pop:not([hidden]) #list");
      await list.locator("li").filter({ hasText: "zeroed" }).first().waitFor();
      const rows = Object.fromEntries(await list.locator("li[data-i]").evaluateAll((ls) => ls.map((l) => {
        const f = l.querySelector(".badge.free");
        return [l.querySelector(".v").textContent, f ? f.title : null];
      })));
      assert.equal(rows.zeroed, model);
      assert.equal(rows.a1, provider);
      assert.equal(rows["qwen/qwen3.8-27b:free"], null, "a model the user gave a price has no badge in the picker, though named free");
      assert.equal(rows["vendor-free-one"], w.plan);
      assert.equal(rows.plain, null);

      assert.deepEqual(errors, []);
    });
  }
}
