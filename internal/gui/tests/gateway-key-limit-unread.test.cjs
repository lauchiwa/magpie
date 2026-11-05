// fork: Billing D4. A gateway key whose usage this computer couldn't read
// (usage.jsonl unreadable) is refused until it can be. Its row says so:
// the badge "Usage unread", its line why, and never "0 of" a cap. In
// every language, both engines, wide and narrow.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { fixture } = require("./fixtures/caller-keys.cjs");

const src = fs.readFileSync(path.resolve(__dirname, "../assets/i18n.js"), "utf8");
const I18N = new Function(src.slice(0, src.indexOf("\n};") + 3) + "; return I18N;")();

const hour = 36e5, day = 864e5;
// budget.All's statuses for an unread window: counts and what is left 0,
// unavailable; the week beside it was read and is under its cap
const limits = () => ({
  laptop: {
    limit: { period: "day", tokens: 1000, cost: 5 },
    moreLimits: [{ period: "week", cost: 50 }],
    usedAll: [
      { period: "day", start: new Date(Date.now() - 2 * hour).toISOString(), reset: new Date(Date.now() + 22 * hour).toISOString(), calls: 0,
        tokens: 0, cost: 0, tokenLimit: 1000, costLimit: 5, tokensLeft: 0, costLeft: 0, spent: false, unavailable: true },
      { period: "week", start: new Date(Date.now() - 4 * day).toISOString(), reset: new Date(Date.now() + 3 * day).toISOString(), calls: 12,
        tokens: 90000, cost: 2.5, costLimit: 50, tokensLeft: 0, costLeft: 47.5, spent: false },
    ],
  },
});

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [1100, 420]) {
      test(`${engine} ${lang} ${width}: a key whose usage couldn't be read`, async (t) => {
        const tr = (k, vars = {}) => {
          const s = lang === "en" ? k : I18N[lang][k];
          assert.equal(typeof s, "string", `${lang} has ${k}`);
          return s.replace(/\{(\w+)\}/g, (_, v) => String(vars[v]));
        };
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        t.after(() => browser.close());
        const page = await (await browser.newContext({ viewport: { width, height: 760 }, reducedMotion: "reduce" })).newPage();
        page.setDefaultTimeout(6000);
        page.setDefaultNavigationTimeout(60000);
        const events = [], errors = [];
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", fixture(lang, "light", events, { lan: true, limits: limits() }));
        await page.goto("http://magpie.test/?view=gateway");
        await page.locator("#gatewayKeys .acc[data-key]").last().waitFor();
        const row = page.locator('#gatewayKeys .acc[data-key="laptop"]');

        // the badge is of the unread limit, which holds the key, not the
        // week with the most of a cap used
        const badge = row.locator(".amodels.limit");
        const text = await badge.textContent();
        assert(text.includes(tr("Usage unread")) && text.includes(tr("per day")) && text.includes(tr("+1 limit")), "badge says " + text);
        assert(!text.includes("0 / 1"), "badge says 0 used: " + text);
        assert.equal(await row.locator(".amodels.limit.spent").count(), 1, "the badge is marked refused");
        const lines = row.locator(".key-limit");
        assert.equal(await lines.count(), 2);
        const unread = await lines.nth(0).textContent();
        assert(unread.includes(tr("Couldn't read this key's usage · refused until it can")), "line says " + unread);
        assert(!/\b0 of\b|\$0\.00/.test(unread), "line says 0 used: " + unread);
        assert.equal(await lines.nth(0).evaluate((e) => e.classList.contains("spent")), true);
        assert.equal(await lines.nth(0).getAttribute("title"), tr("magpie couldn't read its usage log, so it can't tell what this key used; it refuses the key's requests until the log can be read"));
        // the week was read: its line is as before
        assert((await lines.nth(1).textContent()).includes("$2.50"));
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false, "the rows scroll sideways");
        assert.deepEqual(errors, []);
      });
    }
  }
}
