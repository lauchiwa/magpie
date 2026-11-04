// Run with Node's test runner and Playwright on the module path; see README.md.
// fork: a routing group's Free first and Cheapest first hints end with what
// its member order decides (freeprice.js groupOrderSays): under Free first
// the paid members keep their listed order too, under Cheapest first those
// that cost the same do, and that order is the user's preference. The
// group editor's routing hint says it, and so does the row's menu of
// routings, on the routing's own title; Smart's says nothing of it. In both
// engines, in English, Chinese, Traditional Chinese, Japanese and German,
// and at a phone's width.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const models = [
  { id: "a/sol", name: "sol", providerName: "A", icon: "generic" },
  { id: "b/flash", name: "flash", providerName: "B", icon: "generic" },
];
const info = [{ id: "a/sol", ready: true }, { id: "b/flash", ready: true }];
const base = { members: ["a/sol", "b/flash"], ready: true, memberInfo: info, offers: [], shared: [] };
const groups = { models, pools: [], groups: [
  { ...base, id: "gfree", name: "Freebies", routing: "free" },
  { ...base, id: "gcheap", name: "Thrifty", routing: "cheap" },
] };

// the sentence each language adds: [Free first's, Cheapest first's]
const says = {
  en: ["Paid members keep their listed order too: the order of the members is your preference among those the routing ranks the same.",
    "The order of the members is your preference among those that cost the same."],
  zh: ["付费成员也按列出的顺序：成员的顺序就是你的偏好，在路由排得一样的成员之间起作用。", "成员的顺序就是你的偏好，在价格相同的成员之间起作用。"],
  "zh-TW": ["付費成員也按列出的順序：成員的順序就是你的偏好，在路由排得一樣的成員之間起作用。", "成員的順序就是你的偏好，在價格相同的成員之間起作用。"],
  ja: ["有料のメンバーも設定順のままです。メンバーの順序が、ルーティングで同じ順位になるもの同士でのあなたの優先順位です。", "メンバーの順序が、同じ価格のもの同士でのあなたの優先順位です。"],
  de: ["Auch bezahlte Mitglieder behalten ihre Reihenfolge: Die Reihenfolge der Mitglieder ist deine Vorliebe unter denen, die das Routing gleich einstuft.",
    "Die Reihenfolge der Mitglieder ist deine Vorliebe unter denen, die gleich viel kosten."],
};
// GROUP_ROUTE_OPTS' places: Smart, Free first, Cheapest first
const SMART = 0, FREE = 6, CHEAP = 8;
const KIND = '.segs[data-kind="|order|drain|rotate|usage|pace|free|fast|cheap|weight|manual"]';

function serve(lang, web) {
  const state = { agents: [{ id: "codex", name: "Codex", path: "/test/codex", fields: [] }], profiles: [], settings: { lang, theme: "light" } };
  return async (r) => {
    const url = new URL(r.request().url());
    const json = (data) => r.fulfill({ json: data });
    if (url.pathname === "/boot.js") return r.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:${web}};` });
    if (url.pathname === "/wails/runtime.js") return r.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/gateway/trace") {
      if (url.searchParams.get("wait")) return new Promise(() => {}); // nothing more comes
      return json({ mine: true, now: new Date().toISOString(), seq: 1, totals: { requests: 0, rerouted: 0, errors: 0 }, routes: [] });
    }
    if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [], routes: [] });
    if (url.pathname === "/api/groups" || url.pathname.startsWith("/api/groups/")) return json(groups);
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true, url: "http://127.0.0.1:3999" } });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await r.fulfill({ body: await fs.readFile(file), contentType });
  };
}

async function open(t, engine, { lang, web = false, width = 1100 }) {
  const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
  t.after(() => browser.close());
  const page = await (await browser.newContext({ viewport: { width, height: 800 } })).newPage();
  page.setDefaultTimeout(5000);
  page.setDefaultNavigationTimeout(60000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/*", serve(lang, web));
  await page.goto("http://magpie.test/?view=routing");
  await page.locator('.rt-group[data-id="gcheap"]').waitFor({ timeout: 30000 }); // the page's first load
  return { page, errors };
}

// the routing hint under the group editor's routings, once the routing at
// place i is picked (none: as the group was saved)
async function hintAfter(page, i) {
  const box = page.locator(".rt-gedit " + KIND);
  if (i !== undefined) await box.locator(".opt").nth(i).click();
  return box.evaluate((b) => b.nextElementSibling.textContent);
}

// a hint with the sentence added: the routing's own words, a space, then it
const endsWith = (hint, said) => assert.ok(hint.endsWith(" " + said) && hint.length > said.length + 20, JSON.stringify(hint));
const saysNone = (hint, lang) => { for (const s of says[lang]) assert.ok(!hint.includes(s), JSON.stringify(hint)); };

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of Object.keys(says)) {
    const [free, cheap] = says[lang];
    test(`${engine} ${lang}: Free first's and Cheapest first's hints say what the member order decides`, async (t) => {
      const { page, errors } = await open(t, engine, { lang });

      // the row's menu: each routing's title, Free first's and Cheapest
      // first's with the sentence, Smart's without
      await page.locator('.rt-group[data-id="gfree"] button.rt-groute').click();
      const items = page.locator(".proto-menu .pm-item");
      await items.first().waitFor();
      assert.equal(await items.count(), 11);
      endsWith(await items.nth(FREE).getAttribute("title"), free);
      endsWith(await items.nth(CHEAP).getAttribute("title"), cheap);
      saysNone(await items.nth(SMART).getAttribute("title"), lang);
      await page.keyboard.press("Escape");
      assert.equal(await page.locator(".proto-menu").count(), 0);

      // the editor of a group saved as Free first, then its routing
      // switched to Cheapest first and to Smart
      await page.locator('.rt-group[data-id="gfree"]').click();
      await page.locator(".rt-gedit " + KIND).waitFor();
      endsWith(await hintAfter(page), free);
      endsWith(await hintAfter(page, CHEAP), cheap);
      saysNone(await hintAfter(page, SMART), lang);
      endsWith(await hintAfter(page, FREE), free);
      assert.deepEqual(errors, []);
    });
  }

  test(`${engine}: at 390px (web) the hint says it within the window`, async (t) => {
    const { page, errors } = await open(t, engine, { lang: "en", web: true, width: 390 });
    await page.locator('.rt-group[data-id="gcheap"]').click();
    await page.locator(".rt-gedit " + KIND).waitFor();
    endsWith(await hintAfter(page), says.en[1]);
    const hint = page.locator(".rt-gedit " + KIND + " + .hint");
    await hint.scrollIntoViewIfNeeded();
    const box = await hint.boundingBox();
    assert.ok(box && box.height > 0 && box.x >= 0 && box.x + box.width <= 390, JSON.stringify(box));
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, "no sideways scroll");
    assert.deepEqual(errors, []);
  });
}
