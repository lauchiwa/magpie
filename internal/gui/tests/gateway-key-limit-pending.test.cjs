// fork: Billing D4, write failures. Requests a restart cut off (magpie
// quit, crashed or handed over mid-call) are in a gateway key's counts at
// their reservation, and its limit line says how many, with why in its
// title. In every language, both engines, wide and narrow.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { fixture } = require("./fixtures/caller-keys.cjs");

const src = fs.readFileSync(path.resolve(__dirname, "../assets/i18n.js"), "utf8");
const I18N = new Function(src.slice(0, src.indexOf("\n};") + 3) + "; return I18N;")();

const hour = 36e5;
const limits = (pending) => ({
  laptop: {
    limit: { period: "day", tokens: 1000 },
    usedAll: [
      { period: "day", start: new Date(Date.now() - 2 * hour).toISOString(), reset: new Date(Date.now() + 22 * hour).toISOString(), calls: 3,
        tokens: 400, cost: 0, tokenLimit: 1000, tokensLeft: 600, costLeft: 0, spent: false, pending },
    ],
  },
});

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [1100, 420]) {
      test(`${engine} ${lang} ${width}: a key holding requests a restart cut off`, async (t) => {
        const tr = (k, vars = {}) => {
          const s = lang === "en" ? k : I18N[lang][k];
          assert.equal(typeof s, "string", `${lang} has ${k}`);
          return s.replace(/\{(\w+)\}/g, (_, v) => String(vars[v]));
        };
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        t.after(() => browser.close());
        for (const [pending, says] of [[1, tr("1 pending at its reservation")], [2, tr("{n} pending at their reservation", { n: 2 })]]) {
          const page = await (await browser.newContext({ viewport: { width, height: 760 }, reducedMotion: "reduce" })).newPage();
          page.setDefaultTimeout(6000);
          page.setDefaultNavigationTimeout(60000);
          const events = [], errors = [];
          page.on("pageerror", (e) => errors.push(e.message));
          await page.route("**/*", fixture(lang, "light", events, { lan: true, limits: limits(pending) }));
          await page.goto("http://magpie.test/?view=gateway");
          await page.locator("#gatewayKeys .acc[data-key]").last().waitFor();
          const line = page.locator('#gatewayKeys .acc[data-key="laptop"] .key-limit');
          assert.equal(await line.count(), 1);
          const text = await line.textContent();
          assert(text.includes(says), `${pending} pending: line says ${text}`);
          assert((await line.getAttribute("title")).includes(tr("A request cut off before it ended (magpie quit, crashed or handed over) may have been billed, so it counts at what it reserved")));
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false, "the rows scroll sideways");
          assert.deepEqual(errors, []);
          await page.context().close();
        }
      });
    }
  }
}
