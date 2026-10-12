// Run with Node's test runner and Playwright on the module path; see README.md.
// The Providers page's identity profiles panel: a candidate is marked as
// needing review and can only be confirmed, a confirmed snapshot binds to an
// applicable provider, armed windows show their client and expiry, an older
// gateway's id-only status shows no invented countdown, providers that cannot
// present a profile say why, removal asks first and says it unbinds, and an
// API error is shown without changing what is listed. No credential, session,
// request or window id ever reaches the page. In English and Chinese, Chromium
// and WebKit, including a narrow window.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");

const relay = {
  id: "relay", name: "Relay", icon: "generic", chat: "https://relay.example/v1", responses: "", anthropic: "", catalog: "",
  host: "relay.example", models: [{ id: "m1", name: "M1", on: true }], agents: [], fallback: [], headers: {},
  key: { set: true, masked: "sk-…1234" }, keyList: [], balanceToken: { takes: false, set: false }, proxy: "", ready: true,
};
const second = { ...relay, id: "relay-two", name: "Relay Two", host: "two.example" };

// what a real captured Codex profile holds, and what it must never hold
const SECRET_UA = "codex-tui/0.162.1 (Mac OS 15.7.9; x86_64) Orca/1.4.224 (codex-tui; 0.162.1)";
const candidate = {
  id: "identity_aaaaaaaaaaaaaaaa", client: "codex_cli", source: "request_capture", state: "candidate",
  fingerprint: { userAgent: SECRET_UA, version: "0.162.1", headers: { Originator: "codex-tui" } },
  createdAt: "2026-10-12T01:00:00Z", observedAt: "2026-10-12T01:00:00Z", bindings: [],
};
const confirmed = {
  ...candidate, id: "identity_bbbbbbbbbbbbbbbb", state: "confirmed",
  confirmedAt: "2026-10-12T01:05:00Z", bindings: [],
};

function identityState(over = {}) {
  return {
    profiles: [], bindings: {},
    providers: [
      { id: "relay", name: "Relay", applicable: true },
      { id: "relay-two", name: "Relay Two", applicable: true },
    ],
    armed: [], gateway: true, window: "5m0s", ...over,
  };
}

// serve answers the page. identity is a function so each request sees the
// state the test has moved to; posts are recorded for the assertions.
function serve(lang, identity, posts, failPost) {
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/providers") return json({ providers: [relay, second], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/identity") return json(identity());
    if (url.pathname.startsWith("/api/identity/")) {
      const action = url.pathname.slice("/api/identity/".length);
      posts.push({ action, body: route.request().postDataJSON() });
      if (failPost && failPost.action === action) return route.fulfill({ status: 400, json: { error: failPost.error } });
      return json(identity(action, route.request().postDataJSON()));
    }
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
}

const words = {
  en: {
    panel: "Identity profiles", review: "Needs review", confirmedHead: "Confirmed profiles",
    confirm: "Confirm profile", bind: "Bind profile", capture: "Capture identity",
    stop: "Stop capture", gateway: "Start the gateway before capturing an identity profile.",
    empty: "No identity profiles yet", cannot: "Providers that cannot use profiles",
    remove: "Remove profile", unbinds: "This also unbinds", unbind: "Unbind Relay",
  },
  zh: {
    panel: "身份档案", review: "待确认", confirmedHead: "已确认的档案",
    confirm: "确认档案", bind: "绑定档案", capture: "捕获身份",
    stop: "停止捕获", gateway: "请先启动 gateway，再捕获身份档案。",
    empty: "还没有身份档案", cannot: "不能使用身份档案的供应商",
    remove: "删除档案", unbinds: "这会同时解绑", unbind: "解绑 Relay",
  },
};

// Every language the app knows draws the panel: a key with no translation
// would fall back to English and show beside its neighbours.
const titles = {
  en: { panel: "Identity profiles", review: "Needs review", capture: "Capture identity" },
  zh: { panel: "身份档案", review: "待确认", capture: "捕获身份" },
  "zh-TW": { panel: "身分檔案", review: "待確認", capture: "擷取身分" },
  ja: { panel: "アイデンティティプロファイル", review: "確認が必要", capture: "アイデンティティをキャプチャ" },
  de: { panel: "Identitätsprofile", review: "Zu prüfen", capture: "Identität erfassen" },
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    const w = words[lang];
    test(`${engine} ${lang}: identity profiles are reviewed, bound and removed from the Providers page`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await (await browser.newContext({ viewport: { width: 980, height: 700 }, reducedMotion: "reduce" })).newPage();
      t.after(async () => {
        if (process.env.ARTIFACT_DIR) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-identity-profiles.png`) });
        }
        await browser.close();
      });
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));

      // the panel is folded away until opened, and says nothing is there yet
      let current = identityState();
      const posts = [];
      // the page keeps the state a change answers with, so the fake gateway
      // moves its own state on each action, as the real API does
      const advance = (action, body) => {
        if (action === "arm") current = { ...current, armed: [{ provider: body.provider, client: body.client, expiresAt: new Date(Date.now() + 5 * 60e3).toISOString() }] };
        if (action === "disarm") current = { ...current, armed: [] };
        if (action === "confirm") current = { ...current, profiles: current.profiles.map((p) => (p.id === body.profile ? { ...p, state: "confirmed", confirmedAt: "2026-10-12T01:05:00Z" } : p)) };
        if (action === "bind") current = { ...current, bindings: { ...current.bindings, [body.provider]: body.profile }, profiles: current.profiles.map((p) => (p.id === body.profile ? { ...p, bindings: [...new Set([...p.bindings, body.provider])] } : p)) };
        if (action === "unbind") current = { ...current, bindings: Object.fromEntries(Object.entries(current.bindings).filter(([id]) => id !== body.provider)), profiles: current.profiles.map((p) => ({ ...p, bindings: p.bindings.filter((id) => id !== body.provider) })) };
        if (action === "remove") current = { ...current, profiles: current.profiles.filter((p) => p.id !== body.profile), bindings: {} };
        return current;
      };
      await page.route("**/*", serve(lang, (action, body) => (action ? advance(action, body) : current), posts));
      await page.goto("http://magpie.test/?view=providers");
      await page.locator('#providers .row.provider[data-id="relay"]').waitFor();
      const head = page.locator("#identityHead"), panel = page.locator("#identityPanel");
      assert(await head.isVisible(), "the identity fold is not shown");
      assert(await panel.evaluate((p) => p.hidden), "the panel is open before it was asked for");
      assert((await head.textContent()).includes(w.panel), await head.textContent());
      await page.locator("#foldIdentity").click();
      await panel.locator(".identity-arm").waitFor();
      assert((await panel.textContent()).includes(w.empty), await panel.textContent());

      // arming goes to the gateway, and the window shows its client and expiry
      await panel.getByRole("button", { name: w.capture }).click();
      await panel.locator(".identity-armed").waitFor();
      assert.deepEqual(posts.map((p) => p.action), ["arm"]);
      assert.deepEqual(posts[0].body, { provider: "relay", client: "codex_cli" });
      const armedText = await panel.locator(".identity-armed").textContent();
      assert(armedText.includes("Relay"), armedText);
      assert(armedText.includes("codex_cli"), armedText);
      // the countdown runs down from the gateway's own expiry
      const firstTick = await panel.locator(".identity-expiry").textContent();
      assert(/\d:\d\d/.test(firstTick), `no countdown: ${firstTick}`);
      await page.waitForTimeout(2200);
      const laterTick = await panel.locator(".identity-expiry").textContent();
      assert(laterTick !== firstTick, `the countdown stood still at ${firstTick}`);

      // the captured candidate needs review: confirm is its only step, and
      // nothing from the request beyond the closed projection is shown
      current = { ...current, profiles: [candidate], armed: [] };
      await page.reload();
      await page.locator("#identityHead").waitFor();
      await panel.locator(".identity-card").waitFor();
      let text = await panel.textContent();
      assert(text.includes(w.review), text);
      assert(text.includes(SECRET_UA), "the user agent the profile presents is not shown");
      assert(text.includes("codex-tui"), text);
      for (const leak of ["Authorization", "Bearer", "X-Codex-Window-Id", "X-Session-Id", "X-Request-Id"]) {
        assert(!text.includes(leak), `the panel shows ${leak}`);
      }
      assert.equal(await panel.getByRole("button", { name: w.bind }).count(), 0, "a candidate offers Bind");
      await panel.getByRole("button", { name: w.confirm }).click();
      assert.equal(posts.at(-1).action, "confirm");
      assert.deepEqual(posts.at(-1).body, { profile: candidate.id });

      // a confirmed profile binds to an applicable provider
      await panel.getByRole("button", { name: w.bind }).waitFor();
      text = await panel.textContent();
      assert(text.includes(w.confirmedHead), text);
      await panel.getByRole("button", { name: w.bind }).click();
      assert.equal(posts.at(-1).action, "bind");
      assert.deepEqual(posts.at(-1).body, { provider: "relay", profile: candidate.id });

      // a bound profile names the provider presenting it, and unbinds it
      await panel.locator(".identity-bound").waitFor();
      text = await panel.textContent();
      assert(text.includes("Relay"), text);
      await panel.getByRole("button", { name: w.unbind }).first().click();
      assert.equal(posts.at(-1).action, "unbind");
      assert.deepEqual(posts.at(-1).body, { provider: "relay" });

      // bound again, so removal has something to unbind
      await panel.getByRole("button", { name: w.bind }).click();
      await panel.locator(".identity-bound").waitFor();

      // removing asks first and says it unbinds; Cancel sends nothing
      const before = posts.length;
      await panel.getByRole("button", { name: w.remove }).click();
      const dialog = page.locator("dialog.action-confirm");
      await dialog.waitFor();
      assert((await dialog.textContent()).includes(w.unbinds), await dialog.textContent());
      await dialog.getByRole("button").first().click();
      await page.waitForTimeout(150);
      assert.equal(posts.length, before, "Cancel sent a removal");
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: the panel degrades safely without a gateway, on an older one and on an error`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await (await browser.newContext({ viewport: { width: 420, height: 760 }, reducedMotion: "reduce" })).newPage();
      t.after(async () => {
        if (process.env.ARTIFACT_DIR) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-identity-degraded.png`) });
        }
        await browser.close();
      });
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));

      // no gateway: capture is refused up front, and said so
      let current = identityState({ gateway: false, profiles: [confirmed] });
      const posts = [];
      await page.route("**/*", serve(lang, () => current, posts));
      await page.goto("http://magpie.test/?view=providers");
      await page.locator("#identityHead").waitFor();
      await page.locator("#foldIdentity").click();
      const panel = page.locator("#identityPanel");
      await panel.locator(".identity-card").waitFor();
      let text = await panel.textContent();
      assert(text.includes(w.gateway), text);
      assert(await panel.getByRole("button", { name: w.capture }).isDisabled(), "capture is offered with no gateway");

      // a narrow window keeps the panel inside the page
      const overflow = await page.evaluate(() => {
        const p = document.querySelector("#identityPanel");
        return p.scrollWidth - p.clientWidth;
      });
      assert(overflow <= 1, `the panel overflows by ${overflow}px at 420px wide`);

      // an older gateway answers with provider ids alone: shown armed, with
      // no invented countdown
      current = identityState({ armed: [{ provider: "relay" }] });
      await page.reload();
      await page.locator("#identityHead").waitFor();
      await panel.locator(".identity-armed").waitFor();
      text = await panel.locator(".identity-armed").textContent();
      assert(text.includes("Relay"), text);
      assert(!/\d{1,2}:\d{2}/.test(text), `an expiry was invented: ${text}`);

      // a provider no profile reaches says why, and is not offered to bind
      current = identityState({
        profiles: [confirmed],
        providers: [
          { id: "relay", name: "Relay", applicable: true },
          { id: "remote", name: "Remote", applicable: false, why: "this provider is a magpie service, not an ordinary upstream API provider" },
        ],
      });
      await page.reload();
      await page.locator("#identityHead").waitFor();
      await panel.locator(".identity-card").waitFor();
      text = await panel.textContent();
      assert(text.includes(w.cannot), text);
      assert(text.includes("magpie service"), text);

      // an action the gateway refuses is said, and what is listed stays
      await page.unroute("**/*");
      await page.route("**/*", serve(lang, () => current, posts, { action: "bind", error: "Remote: this provider is a magpie service, not an ordinary upstream API provider" }));
      await page.reload();
      await page.locator("#identityHead").waitFor();
      await panel.locator(".identity-card").waitFor();
      // the app puts back a scroll no reader asked for, so the button is
      // brought into sight with a wheel, as the other page tests do
      const bind = panel.getByRole("button", { name: w.bind });
      for (let i = 0; i < 12 && !(await bind.evaluate((b) => { const r = b.getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight; })); i++) {
        await page.mouse.move(210, 400);
        await page.mouse.wheel(0, 200);
        await page.waitForTimeout(40);
      }
      await bind.click();
      const status = page.locator("#status");
      await status.waitFor();
      assert((await status.textContent()).includes("magpie service"), await status.textContent());
      assert.equal(await panel.locator(".identity-card").count(), 1, "the list changed on a refused action");
      assert.deepEqual(errors, []);
    });
  }
}

// Every language the app knows: the panel's own strings are translated, so
// none of them falls back to English beside its neighbours.
for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    test(`${engine} ${lang}: the identity panel is translated`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await (await browser.newContext({ viewport: { width: 980, height: 700 }, reducedMotion: "reduce" })).newPage();
      t.after(async () => { await browser.close(); });
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      const current = identityState({ profiles: [candidate] });
      await page.route("**/*", serve(lang, () => current, []));
      await page.goto("http://magpie.test/?view=providers");
      await page.locator("#identityHead").waitFor();
      await page.locator("#foldIdentity").click();
      const panel = page.locator("#identityPanel");
      await panel.locator(".identity-card").waitFor();
      const head = await page.locator("#identityHead").textContent();
      const text = await panel.textContent();
      const want = titles[lang];
      assert(head.includes(want.panel), `${lang} fold: ${head}`);
      assert(text.includes(want.review), `${lang} review: ${text.slice(0, 200)}`);
      assert(text.includes(want.capture), `${lang} capture: ${text.slice(0, 200)}`);
      if (lang !== "en") {
        for (const english of ["Needs review", "Capture identity", "Confirm profile"]) {
          assert(!text.includes(english), `${lang} still shows the English "${english}"`);
        }
      }
      assert.deepEqual(errors, []);
    });
  }
}

// The provider editor's Identity field is a summary and a way to the panel,
// never an editor of a profile: the binding is its own store, so unbinding
// from here leaves an unsaved draft alone and a provider save carries no
// profile field.
for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    const w = { en: { manage: "Manage identity profiles", unbind: "Unbind", none: "No identity profile", cannot: "cannot present an identity profile" },
      zh: { manage: "管理身份档案", unbind: "解绑", none: "没有身份档案", cannot: "不能呈现身份档案" } }[lang];
    test(`${engine} ${lang}: the provider editor says which identity a provider presents`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await (await browser.newContext({ viewport: { width: 1100, height: 820 }, reducedMotion: "reduce" })).newPage();
      t.after(async () => {
        if (process.env.ARTIFACT_DIR) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-identity-editor.png`) });
        }
        await browser.close();
      });
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));

      // bound: the editor names the profile and offers Unbind
      let current = identityState({
        profiles: [{ ...confirmed, bindings: ["relay"] }],
        bindings: { relay: confirmed.id },
        providers: [
          { id: "relay", name: "Relay", applicable: true, profileId: confirmed.id },
          { id: "relay-two", name: "Relay Two", applicable: true },
        ],
      });
      const posts = [];
      const saves = [];
      await page.route("**/*", async (route) => {
        const url = new URL(route.request().url());
        if (url.pathname.startsWith("/api/provider/")) saves.push({ path: url.pathname, body: route.request().postDataJSON() });
        return serve(lang, (action, body) => {
          if (action === "unbind") current = { ...current, bindings: {}, profiles: current.profiles.map((p) => ({ ...p, bindings: [] })), providers: current.providers.map((p) => ({ ...p, profileId: undefined })) };
          return current;
        }, posts)(route);
      });
      await page.goto("http://magpie.test/?view=providers");
      await page.locator('#providers .row.provider[data-id="relay"]').click();
      const editor = page.locator(".editor");
      await editor.waitFor();
      const field = editor.locator(".identity-field");
      await field.waitFor();
      let text = await field.textContent();
      assert(text.includes("0.162.1"), text);
      assert(text.includes(SECRET_UA), "the editor doesn't say what identity goes out");
      for (const leak of ["Authorization", "Bearer", "X-Codex-Window-Id"]) assert(!text.includes(leak), `the editor shows ${leak}`);

      // an unsaved edit survives an unbind: the binding is not part of the
      // provider's draft, and the editor stays open on it
      const nameBox = editor.locator("input").first();
      await nameBox.fill("Relay edited");
      await field.getByRole("button", { name: w.unbind, exact: true }).click();
      await page.waitForTimeout(300);
      assert.equal(posts.at(-1).action, "unbind");
      assert.deepEqual(posts.at(-1).body, { provider: "relay" });
      assert.deepEqual(saves, [], "unbinding saved the provider");
      assert(await editor.isVisible(), "the editor closed on an unbind");
      assert.equal(await nameBox.inputValue(), "Relay edited", "the unsaved draft was lost");
      text = await field.textContent();
      assert(text.includes(w.none), text);

      // Manage takes the reader to the panel on the page under the editor.
      // The draft is still dirty, so it asks first, as leaving the editor
      // any other way does; discarding opens the panel.
      await field.getByRole("button", { name: w.manage }).click();
      const ask = page.locator("dialog.action-confirm");
      await ask.waitFor();
      await ask.getByRole("button").last().click();
      await page.locator("#identityPanel .identity-arm").waitFor();
      // the editor folds back into its row: gone once that has run
      await page.locator(".editor").waitFor({ state: "detached" });

      // a provider no profile would be sent on says why, and offers nothing
      current = identityState({
        profiles: [confirmed],
        providers: [{ id: "relay", name: "Relay", applicable: false, why: "this provider is a signed-in account, whose agent owns the outbound identity" }],
      });
      await page.goto("http://magpie.test/?view=providers");
      await page.locator('#providers .row.provider[data-id="relay"]').click();
      await page.locator(".editor .identity-field").waitFor();
      text = await page.locator(".editor .identity-field").textContent();
      assert(text.includes("signed-in account") || text.includes(w.cannot), text);
      assert.equal(await page.locator(".editor .identity-field button").count(), 0, "an inapplicable provider offers an identity action");
      assert.deepEqual(errors, []);
    });
  }
}
