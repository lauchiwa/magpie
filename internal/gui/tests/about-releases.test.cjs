const assert = require("node:assert/strict");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { fixture } = require("./fixtures/caller-keys.cjs");
const { openPart } = require("./reader.cjs");

// fork: Settings › About links this fork's Releases page (settings'
// releases, update.Downloads) under upstream's Website row: usemagpie.ai
// downloads upstream's builds, which replace this one. In every language
// and at the tray panel's width; with no page named there is no row.
const words = {
  en: "Releases", zh: "版本发布", "zh-TW": "版本發佈", ja: "リリース", de: "Releases",
};
const page_ = "https://github.com/lauchiwa/magpie/releases";

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of Object.keys(words)) {
    for (const width of [1100, 420]) {
      test(`${engine} ${lang} ${width}: About links the fork's releases`, async (t) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        t.after(() => browser.close());
        const page = await (await browser.newContext({ viewport: { width, height: 800 }, reducedMotion: "reduce" })).newPage();
        page.setDefaultTimeout(6000);
        const errors = [];
        page.on("pageerror", (e) => errors.push(e.message));
        // the settings as the Go side sends them, with the fork's page
        const base = fixture(lang, "light", []);
        await page.route("**/*", (route) => new URL(route.request().url()).pathname !== "/api/settings" ? base(route)
          : base({ request: () => route.request(), fulfill: (r) => route.fulfill(r.json ? { ...r, json: { ...r.json, releases: page_ } } : r) }));
        await page.goto("http://magpie.test/");
        await page.locator("#prefs").click();
        await openPart(page, "about");
        const row = page.locator("#about .row", { has: page.locator(".who .name", { hasText: new RegExp(`^${words[lang]}$`) }) });
        await row.waitFor();
        assert.equal(await row.locator("code").textContent(), "github.com/lauchiwa/magpie/releases");
        // right under the Website row
        const prev = await row.evaluate((r) => r.previousElementSibling.querySelector("code")?.textContent);
        assert.equal(prev, "usemagpie.ai");
        const opened = page.waitForRequest((r) => r.url().includes("/api/open"));
        await row.locator("button.text").click();
        assert.equal(JSON.parse((await opened).postData()).url, page_);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, "About wider than the window");
        assert.deepEqual(errors, []);
      });
    }
  }
  test(`${engine}: no Releases row without a page`, async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    const page = await (await browser.newContext({ viewport: { width: 1100, height: 800 }, reducedMotion: "reduce" })).newPage();
    page.setDefaultTimeout(6000);
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.route("**/*", fixture("en", "light", []));
    await page.goto("http://magpie.test/");
    await page.locator("#prefs").click();
    await openPart(page, "about");
    await page.locator("#about .row", { hasText: "usemagpie.ai" }).waitFor();
    assert.equal(await page.locator("#about .row .who .name", { hasText: /^Releases$/ }).count(), 0);
    assert.deepEqual(errors, []);
  });
}
