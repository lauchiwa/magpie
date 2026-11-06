// Run with Node's test runner and Playwright on the module path; see README.md.
// fork: Billing D4. A remote magpie that couldn't read a gateway key's
// usage refuses the key and answers its limit as unavailable. This
// computer's card for that key, with nothing read before, says so in the
// reader's language (errRemoteKeyUnread, remote_key_limits.go), never
// "0 of" a cap. No backend, the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const src = require("node:fs").readFileSync(path.resolve(__dirname, "../assets/i18n.js"), "utf8");
const I18N = new Function(src.slice(0, src.indexOf("\n};") + 3) + "; return I18N;")();
const assets = path.resolve(__dirname, "../assets");
const WHY = "That computer's magpie couldn't read this key's usage — it refuses the key until it can";

const quotas = [{ provider: "office/~keys", name: "Office", icon: "magpie", from: "Office", kind: "plan", user: "laptop", windows: [],
  error: "remote magpie couldn't read this key's usage; it refuses the key until it can" }];

function serve(lang) {
  const settings = { theme: "light", lang, tray: "panel", quotaLeft: false, currency: "usd" };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (body) => route.fulfill({ json: body });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings });
    if (url.pathname === "/api/settings") return json(settings);
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/usage/quotas" || url.pathname === "/api/usage/quotas/refresh") return json(quotas);
    if (url.pathname === "/api/usage/quotas/history") return json([]);
    if (url.pathname === "/api/usage") return json({ calls: 0, cost: 0, days: [], agents: [], models: [], keys: [] });
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    test(`${engine} ${lang}: a remote key whose usage couldn't be read`, async (t) => {
      const want = lang === "en" ? WHY : I18N[lang][WHY];
      assert.equal(typeof want, "string", `${lang} has the words`);
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const errors = [];
      const page = await (await browser.newContext({ viewport: { width: 400, height: 800 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      page.setDefaultNavigationTimeout(60000);
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", serve(lang));
      await page.goto("http://magpie.test/?view=usage");
      const card = page.locator('.subscription-card[data-provider="office/~keys"]');
      await card.waitFor();
      assert.equal((await card.locator(".subscription-error").textContent()).trim(), want);
      assert.equal(await card.locator(".quota-track").count(), 0, "an unread key has no bar");
      const over = await page.evaluate(() => document.scrollingElement.scrollWidth - innerWidth);
      assert.ok(over <= 0, `page scrolls sideways by ${over}px`);
      assert.deepEqual(errors, []);
    });
  }
}
