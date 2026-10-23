// fork: the Providers page's Identity profiles fold and the provider
// editor's Identity field (docs/subsystems/identity-profiles.md, "The page").
// app.js only calls in: identityRead/identityTake in loadProvidersNow,
// renderIdentityProfiles in renderProviders, identityField in drawEditor.
// Loaded before app.js, so nothing here runs app.js's code at load; the
// fold's button is wired on the first draw.
// Kept out of i18n.js and app.js so an upstream sync meets one file of ours.
magpie.strings({
  zh: {
    // identity profiles (provider editor)
    "Identity": "身份",
    "This provider cannot present an identity profile.": "该供应商不能呈现身份档案。",
    "Only a provider reached with your own key and base URL can present a frozen client identity.": "只有用你自己的密钥和 base URL 访问的供应商才能呈现冻结的客户端身份。",
    "No identity profile: requests go out as magpie.": "没有身份档案：请求以 magpie 的身份发出。",
    "Unbind": "解绑",
    "Manage identity profiles": "管理身份档案",
    "Every request, probe and model list for this provider presents this frozen client identity.": "该供应商的每个请求、探测和模型列表都会呈现这个冻结的客户端身份。",
    "Capture and confirm a profile in Identity profiles on the Providers page, then bind it here.": "先在 Providers 页的身份档案里捕获并确认一个档案，然后在这里绑定。",
    "Expires in {time}": "{time} 后过期",
    "Unbind {provider}": "解绑 {provider}",
    // identity profiles (Providers page)
    "{profiles} profiles · {armed} capturing": "{profiles} 个档案 · {armed} 个正在捕获",
    "Identity profiles could not be loaded.": "无法加载身份档案。",
    "A confirmed profile lets a plain API provider present the frozen Codex CLI or Claude Code identity. It never changes a signed-in account or plugin.": "确认后的档案可让纯 API 供应商呈现冻结的 Codex CLI 或 Claude Code 身份。它不会改变已登录的账号或插件。",
    "Start the gateway before capturing an identity profile.": "请先启动 gateway，再捕获身份档案。",
    "Capture status could not be read: {error}": "无法读取捕获状态：{error}",
    "Capturing one request": "正在捕获一个请求",
    "Client unavailable from this gateway": "此 gateway 未提供客户端信息",
    "Expires {time}": "{time} 过期",
    "Stop capture": "停止捕获",
    "Provider to capture": "要捕获的供应商",
    "Choose provider": "选择供应商",
    "Client identity": "客户端身份",
    "Capture identity": "捕获身份",
    "No identity profiles yet. Arm a capture, then send one request through that provider.": "还没有身份档案。先开启捕获，然后通过该供应商发送一个请求。",
    "Needs review": "待确认",
    "Confirmed profiles": "已确认的档案",
    "Codex CLI identity": "Codex CLI 身份",
    "Claude Code identity": "Claude Code 身份",
    "Created {time}": "创建于 {time}",
    "Presented by: {providers}": "呈现于：{providers}",
    "Confirm profile": "确认档案",
    "Provider to bind": "要绑定的供应商",
    "Bind profile": "绑定档案",
    "Providers that cannot use profiles ({n})": "不能使用身份档案的供应商（{n}）",
    "{provider}: {reason}": "{provider}：{reason}",
    "Remove profile": "删除档案",
    "Show identity profiles": "显示身份档案",
    "Fold identity profiles away": "折叠身份档案",
    "Captured from a request": "从请求捕获",
    "Imported from headers": "从请求头导入",
    "Built-in profile": "内置档案",
    "Candidate": "候选",
    "Confirmed": "已确认",
    "Capture armed on the gateway": "已在 gateway 上开启捕获",
    "Capture stopped": "已停止捕获",
    "Identity profile confirmed": "身份档案已确认",
    "Identity profile bound": "身份档案已绑定",
    "Identity profile unbound": "身份档案已解绑",
    "Identity profile removed": "身份档案已删除",
    "Identity profiles updated": "身份档案已更新",
    "This also unbinds {n} providers using this profile. Continue?": "这会同时解绑使用该档案的 {n} 个供应商。是否继续？",
    "This removes the profile. Continue?": "这会删除该档案。是否继续？",
    "Identity profiles": "身份档案",
  },
  "zh-TW": {
    // identity profiles (provider editor)
    "Identity": "身分",
    "This provider cannot present an identity profile.": "該供應商不能呈現身分檔案。",
    "Only a provider reached with your own key and base URL can present a frozen client identity.": "只有用你自己的金鑰和 base URL 存取的供應商才能呈現凍結的用戶端身分。",
    "No identity profile: requests go out as magpie.": "沒有身分檔案：請求以 magpie 的身分送出。",
    "Unbind": "解除繫結",
    "Manage identity profiles": "管理身分檔案",
    "Every request, probe and model list for this provider presents this frozen client identity.": "該供應商的每個請求、探測和模型列表都會呈現這個凍結的用戶端身分。",
    "Capture and confirm a profile in Identity profiles on the Providers page, then bind it here.": "先在 Providers 頁的身分檔案裡擷取並確認一個檔案，然後在這裡繫結。",
    "Expires in {time}": "{time} 後過期",
    "Unbind {provider}": "解除繫結 {provider}",
    // identity profiles (Providers page)
    "{profiles} profiles · {armed} capturing": "{profiles} 個檔案 · {armed} 個正在擷取",
    "Identity profiles could not be loaded.": "無法載入身分檔案。",
    "A confirmed profile lets a plain API provider present the frozen Codex CLI or Claude Code identity. It never changes a signed-in account or plugin.": "確認後的檔案可讓純 API 供應商呈現凍結的 Codex CLI 或 Claude Code 身分。它不會改變已登入的帳號或外掛。",
    "Start the gateway before capturing an identity profile.": "請先啟動 gateway，再擷取身分檔案。",
    "Capture status could not be read: {error}": "無法讀取擷取狀態：{error}",
    "Capturing one request": "正在擷取一個請求",
    "Client unavailable from this gateway": "此 gateway 未提供用戶端資訊",
    "Expires {time}": "{time} 過期",
    "Stop capture": "停止擷取",
    "Provider to capture": "要擷取的供應商",
    "Choose provider": "選擇供應商",
    "Client identity": "用戶端身分",
    "Capture identity": "擷取身分",
    "No identity profiles yet. Arm a capture, then send one request through that provider.": "還沒有身分檔案。先開啟擷取，然後透過該供應商送出一個請求。",
    "Needs review": "待確認",
    "Confirmed profiles": "已確認的檔案",
    "Codex CLI identity": "Codex CLI 身分",
    "Claude Code identity": "Claude Code 身分",
    "Created {time}": "建立於 {time}",
    "Presented by: {providers}": "呈現於：{providers}",
    "Confirm profile": "確認檔案",
    "Provider to bind": "要繫結的供應商",
    "Bind profile": "繫結檔案",
    "Providers that cannot use profiles ({n})": "不能使用身分檔案的供應商（{n}）",
    "{provider}: {reason}": "{provider}：{reason}",
    "Remove profile": "刪除檔案",
    "Show identity profiles": "顯示身分檔案",
    "Fold identity profiles away": "收合身分檔案",
    "Captured from a request": "從請求擷取",
    "Imported from headers": "從請求標頭匯入",
    "Built-in profile": "內建檔案",
    "Candidate": "候選",
    "Confirmed": "已確認",
    "Capture armed on the gateway": "已在 gateway 上開啟擷取",
    "Capture stopped": "已停止擷取",
    "Identity profile confirmed": "身分檔案已確認",
    "Identity profile bound": "身分檔案已繫結",
    "Identity profile unbound": "身分檔案已解除繫結",
    "Identity profile removed": "身分檔案已刪除",
    "Identity profiles updated": "身分檔案已更新",
    "This also unbinds {n} providers using this profile. Continue?": "這會同時解除繫結使用該檔案的 {n} 個供應商。是否繼續？",
    "This removes the profile. Continue?": "這會刪除該檔案。是否繼續？",
    "Identity profiles": "身分檔案",
  },
  ja: {
    // identity profiles (provider editor)
    "Identity": "アイデンティティ",
    "This provider cannot present an identity profile.": "このプロバイダーはアイデンティティプロファイルを提示できません。",
    "Only a provider reached with your own key and base URL can present a frozen client identity.": "自分のキーとベース URL で接続するプロバイダーだけが、固定したクライアントのアイデンティティを提示できます。",
    "No identity profile: requests go out as magpie.": "アイデンティティプロファイルなし: リクエストは magpie として送信されます。",
    "Unbind": "バインドを解除",
    "Manage identity profiles": "アイデンティティプロファイルを管理",
    "Every request, probe and model list for this provider presents this frozen client identity.": "このプロバイダーのすべてのリクエスト、プローブ、モデル一覧がこの固定したクライアントのアイデンティティを提示します。",
    "Capture and confirm a profile in Identity profiles on the Providers page, then bind it here.": "まず Providers ページのアイデンティティプロファイルでプロファイルを取得して確認し、ここでバインドしてください。",
    "Expires in {time}": "{time} 後に期限切れ",
    "Unbind {provider}": "{provider} のバインドを解除",
    // identity profiles (Providers page)
    "{profiles} profiles · {armed} capturing": "プロファイル {profiles} 件 · キャプチャ中 {armed} 件",
    "Identity profiles could not be loaded.": "アイデンティティプロファイルを読み込めませんでした。",
    "A confirmed profile lets a plain API provider present the frozen Codex CLI or Claude Code identity. It never changes a signed-in account or plugin.": "確認済みのプロファイルは、通常の API プロバイダーが固定した Codex CLI または Claude Code のアイデンティティを提示できるようにします。サインイン済みのアカウントやプラグインは変更しません。",
    "Start the gateway before capturing an identity profile.": "アイデンティティプロファイルを取得する前にゲートウェイを起動してください。",
    "Capture status could not be read: {error}": "キャプチャの状態を読み取れませんでした: {error}",
    "Capturing one request": "リクエストを 1 件キャプチャ中",
    "Client unavailable from this gateway": "このゲートウェイからはクライアント情報を取得できません",
    "Expires {time}": "{time} に期限切れ",
    "Stop capture": "キャプチャを停止",
    "Provider to capture": "キャプチャ対象のプロバイダー",
    "Choose provider": "プロバイダーを選択",
    "Client identity": "クライアントのアイデンティティ",
    "Capture identity": "アイデンティティをキャプチャ",
    "No identity profiles yet. Arm a capture, then send one request through that provider.": "アイデンティティプロファイルはまだありません。キャプチャを有効にし、そのプロバイダー経由でリクエストを 1 件送信してください。",
    "Needs review": "確認が必要",
    "Confirmed profiles": "確認済みのプロファイル",
    "Codex CLI identity": "Codex CLI のアイデンティティ",
    "Claude Code identity": "Claude Code のアイデンティティ",
    "Created {time}": "作成 {time}",
    "Presented by: {providers}": "提示するプロバイダー: {providers}",
    "Confirm profile": "プロファイルを確認",
    "Provider to bind": "バインド先のプロバイダー",
    "Bind profile": "プロファイルをバインド",
    "Providers that cannot use profiles ({n})": "プロファイルを使えないプロバイダー ({n})",
    "{provider}: {reason}": "{provider}: {reason}",
    "Remove profile": "プロファイルを削除",
    "Show identity profiles": "アイデンティティプロファイルを表示",
    "Fold identity profiles away": "アイデンティティプロファイルを折りたたむ",
    "Captured from a request": "リクエストから取得",
    "Imported from headers": "ヘッダーからインポート",
    "Built-in profile": "組み込みプロファイル",
    "Candidate": "候補",
    "Confirmed": "確認済み",
    "Capture armed on the gateway": "ゲートウェイでキャプチャを有効にしました",
    "Capture stopped": "キャプチャを停止しました",
    "Identity profile confirmed": "アイデンティティプロファイルを確認しました",
    "Identity profile bound": "アイデンティティプロファイルをバインドしました",
    "Identity profile unbound": "アイデンティティプロファイルのバインドを解除しました",
    "Identity profile removed": "アイデンティティプロファイルを削除しました",
    "Identity profiles updated": "アイデンティティプロファイルを更新しました",
    "This also unbinds {n} providers using this profile. Continue?": "このプロファイルを使う {n} 件のプロバイダーのバインドも解除されます。続けますか?",
    "This removes the profile. Continue?": "このプロファイルを削除します。続けますか?",
    "Identity profiles": "アイデンティティプロファイル",
  },
  de: {
    // identity profiles (provider editor)
    "Identity": "Identität",
    "This provider cannot present an identity profile.": "Dieser Anbieter kann kein Identitätsprofil zeigen.",
    "Only a provider reached with your own key and base URL can present a frozen client identity.": "Nur ein Anbieter, der mit Ihrem eigenen Schlüssel und Ihrer Basis-URL erreicht wird, kann eine eingefrorene Client-Identität zeigen.",
    "No identity profile: requests go out as magpie.": "Kein Identitätsprofil: Anfragen gehen als magpie hinaus.",
    "Unbind": "Lösen",
    "Manage identity profiles": "Identitätsprofile verwalten",
    "Every request, probe and model list for this provider presents this frozen client identity.": "Jede Anfrage, Prüfung und Modellliste dieses Anbieters zeigt diese eingefrorene Client-Identität.",
    "Capture and confirm a profile in Identity profiles on the Providers page, then bind it here.": "Erfassen und bestätigen Sie zuerst ein Profil unter Identitätsprofile auf der Seite Providers, dann binden Sie es hier.",
    "Expires in {time}": "Läuft in {time} ab",
    "Unbind {provider}": "{provider} lösen",
    // identity profiles (Providers page)
    "{profiles} profiles · {armed} capturing": "{profiles} Profile · {armed} werden erfasst",
    "Identity profiles could not be loaded.": "Identitätsprofile konnten nicht geladen werden.",
    "A confirmed profile lets a plain API provider present the frozen Codex CLI or Claude Code identity. It never changes a signed-in account or plugin.": "Ein bestätigtes Profil lässt einen reinen API-Anbieter die eingefrorene Identität von Codex CLI oder Claude Code zeigen. Angemeldete Konten und Plugins bleiben unberührt.",
    "Start the gateway before capturing an identity profile.": "Starten Sie das Gateway, bevor Sie ein Identitätsprofil erfassen.",
    "Capture status could not be read: {error}": "Erfassungsstatus konnte nicht gelesen werden: {error}",
    "Capturing one request": "Erfasst eine Anfrage",
    "Client unavailable from this gateway": "Dieses Gateway nennt den Client nicht",
    "Expires {time}": "Läuft {time} ab",
    "Stop capture": "Erfassung beenden",
    "Provider to capture": "Anbieter zum Erfassen",
    "Choose provider": "Anbieter wählen",
    "Client identity": "Client-Identität",
    "Capture identity": "Identität erfassen",
    "No identity profiles yet. Arm a capture, then send one request through that provider.": "Noch keine Identitätsprofile. Aktivieren Sie eine Erfassung und senden Sie dann eine Anfrage über diesen Anbieter.",
    "Needs review": "Zu prüfen",
    "Confirmed profiles": "Bestätigte Profile",
    "Codex CLI identity": "Codex-CLI-Identität",
    "Claude Code identity": "Claude-Code-Identität",
    "Created {time}": "Erstellt {time}",
    "Presented by: {providers}": "Gezeigt von: {providers}",
    "Confirm profile": "Profil bestätigen",
    "Provider to bind": "Anbieter zum Binden",
    "Bind profile": "Profil binden",
    "Providers that cannot use profiles ({n})": "Anbieter ohne Profilunterstützung ({n})",
    "{provider}: {reason}": "{provider}: {reason}",
    "Remove profile": "Profil entfernen",
    "Show identity profiles": "Identitätsprofile anzeigen",
    "Fold identity profiles away": "Identitätsprofile einklappen",
    "Captured from a request": "Aus einer Anfrage erfasst",
    "Imported from headers": "Aus Headern importiert",
    "Built-in profile": "Eingebautes Profil",
    "Candidate": "Kandidat",
    "Confirmed": "Bestätigt",
    "Capture armed on the gateway": "Erfassung am Gateway aktiviert",
    "Capture stopped": "Erfassung beendet",
    "Identity profile confirmed": "Identitätsprofil bestätigt",
    "Identity profile bound": "Identitätsprofil gebunden",
    "Identity profile unbound": "Identitätsprofil gelöst",
    "Identity profile removed": "Identitätsprofil entfernt",
    "Identity profiles updated": "Identitätsprofile aktualisiert",
    "This also unbinds {n} providers using this profile. Continue?": "Damit werden auch {n} Anbieter gelöst, die dieses Profil nutzen. Fortfahren?",
    "This removes the profile. Continue?": "Das Profil wird entfernt. Fortfahren?",
    "Identity profiles": "Identitätsprofile",
  },
});

// Identity profiles are their own policy store, not provider headers or
// provider-save data. A candidate's only action is confirm; a confirmed
// snapshot can be bound to one or more applicable providers.
let identityState = null; // profiles, bindings and capture windows, separate from providers.json
let identityLoadError = "";
let identityCountdownTimer = null;
let identityFolded = true;
try { identityFolded = localStorage.getItem("magpie.identityProfilesOpen") !== "1"; } catch {}

// identityRead is asked together with the provider list: the panel is part
// of that page, and a second round trip would hold the list back. A failed
// read leaves the list alone and is said in the panel, not thrown.
function identityRead() {
  return api("identity").then((s) => ({ s }), (e) => ({ e }));
}

// identityTake keeps what identityRead answered, once the list's read is in
function identityTake(identity) {
  identityState = identity.s ?? null;
  identityLoadError = identity.e ? identity.e.message : "";
}

function renderIdentityProfiles() {
  const head = $("#identityHead"), panel = $("#identityPanel"), button = $("#foldIdentity");
  if (!head || !panel || !button) return;
  if (!button.onclick) {
    button.prepend(svg(CHEV_R, 11, 1.6));
    button.onclick = () => {
      identityFolded = !identityFolded;
      try { localStorage.setItem("magpie.identityProfilesOpen", identityFolded ? "0" : "1"); } catch {}
      renderIdentityProfiles();
    };
  }
  // A body this magpie can't read is neither "no profiles" nor "none bound":
  // the panel claims nothing and isn't offered at all. Only a read that
  // failed outright is said, so the reader isn't told profiles are gone
  // while the gateway merely couldn't be asked.
  const s = identityState && Array.isArray(identityState.profiles) && Array.isArray(identityState.providers) ? identityState : null;
  const profiles = s?.profiles || [];
  const armed = Array.isArray(s?.armed) ? s.armed : [];
  head.hidden = !s && !identityLoadError;
  panel.hidden = (!s && !identityLoadError) || identityFolded;
  button.setAttribute("aria-expanded", String(!identityFolded));
  button.title = t(identityFolded ? "Show identity profiles" : "Fold identity profiles away");
  $("#identityCount").textContent = profiles.length || armed.length ? t("{profiles} profiles · {armed} capturing", { profiles: profiles.length, armed: armed.length }) : "";
  panel.replaceChildren();
  if (!s) {
    panel.append(el("p", "hint", identityLoadError || t("Identity profiles could not be loaded.")));
    return;
  }
  const top = el("div", "identity-top");
  top.append(el("p", "hint", t("A confirmed profile lets a plain API provider present the frozen Codex CLI or Claude Code identity. It never changes a signed-in account or plugin.")));
  if (!s.gateway) {
    top.append(el("p", "identity-note", t("Start the gateway before capturing an identity profile.")));
  }
  panel.append(top);

  if (s.armedWhy) panel.append(el("p", "identity-note", t("Capture status could not be read: {error}", { error: s.armedWhy })));
  if (armed.length) {
    const windows = el("div", "identity-windows");
    windows.append(el("h3", "", t("Capturing one request")));
    for (const a of armed) {
      const row = el("div", "identity-armed");
      row.append(el("span", "identity-dot", "●"));
      const words = el("span", "identity-armed-words");
      words.append(el("b", "", identityProviderName(a.provider)), el("span", "sub", a.client || t("Client unavailable from this gateway")));
      if (a.expiresAt && !isNaN(new Date(a.expiresAt))) {
        const expiry = el("span", "note identity-expiry");
        expiry.dataset.expiresAt = a.expiresAt;
        words.append(expiry);
      }
      row.append(words);
      const disarm = el("button", "text", t("Stop capture"));
      disarm.type = "button";
      disarm.disabled = !s.gateway;
      disarm.onclick = () => identityAction("disarm", { provider: a.provider });
      row.append(disarm);
      windows.append(row);
    }
    panel.append(windows);
  }

  const arm = el("div", "identity-arm");
  const providerButton = el("button", "field identity-picker");
  providerButton.type = "button";
  providerButton.setAttribute("aria-label", t("Provider to capture"));
  let selectedProvider = "";
  const applicable = s.providers.filter((p) => p.applicable);
  const setProvider = (p) => { selectedProvider = p?.id || ""; providerButton.replaceChildren(el("span", "v" + (p ? "" : " empty"), p ? p.name : t("Choose provider")), el("span", "chev", "⌄")); };
  setProvider(applicable[0]);
  providerButton.onclick = () => openProtoMenu(providerButton, applicable.map((p) => ({ v: p.id, name: p.name, note: p.id })), selectedProvider, (id) => setProvider(applicable.find((p) => p.id === id)), "Provider to capture", "", "left");
  const clientButton = el("button", "field identity-picker");
  clientButton.type = "button";
  clientButton.setAttribute("aria-label", t("Client identity"));
  let selectedClient = "codex_cli";
  const clients = [{ v: "codex_cli", name: "Codex CLI", note: "codex_cli" }, { v: "claude_code", name: "Claude Code", note: "claude_code" }];
  const setClient = (c) => { selectedClient = c.v; clientButton.replaceChildren(el("span", "v", t(c.name)), el("span", "chev", "⌄")); };
  setClient(clients[0]);
  clientButton.onclick = () => openProtoMenu(clientButton, clients, selectedClient, (id) => setClient(clients.find((c) => c.v === id)), "Client identity", "", "left");
  const armButton = el("button", "text primary", t("Capture identity"));
  armButton.type = "button";
  armButton.disabled = !s.gateway || !selectedProvider;
  armButton.onclick = () => identityAction("arm", { provider: selectedProvider, client: selectedClient });
  arm.append(providerButton, clientButton, armButton);
  panel.append(arm);
  updateIdentityCountdowns();
  clearInterval(identityCountdownTimer);
  identityCountdownTimer = armed.some((a) => a.expiresAt) ? setInterval(updateIdentityCountdowns, 1000) : null;

  if (!profiles.length) {
    panel.append(el("p", "identity-empty", t("No identity profiles yet. Arm a capture, then send one request through that provider.")));
  }
  const candidates = profiles.filter((p) => p.state === "candidate");
  const confirmed = profiles.filter((p) => p.state === "confirmed");
  if (candidates.length) {
    panel.append(el("h3", "identity-section-title", t("Needs review")));
    for (const p of candidates) panel.append(identityProfileCard(p, s));
  }
  if (confirmed.length) {
    panel.append(el("h3", "identity-section-title", t("Confirmed profiles")));
    for (const p of confirmed) panel.append(identityProfileCard(p, s));
  }
}

function updateIdentityCountdowns() {
  for (const node of document.querySelectorAll(".identity-expiry[data-expires-at]")) {
    const ms = Math.max(0, new Date(node.dataset.expiresAt).getTime() - Date.now());
    const seconds = Math.ceil(ms / 1000);
    node.textContent = t("Expires in {time}", { time: `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}` });
  }
}

function identityProviderName(id) {
  return identityState?.providers?.find((p) => p.id === id)?.name || id;
}

function identityProfileCard(profile, state) {
  const card = el("article", "identity-card");
  const title = el("div", "identity-card-title");
  title.append(el("div", "identity-profile-name", profile.client === "codex_cli" ? t("Codex CLI identity") : t("Claude Code identity")), el("span", "identity-state", t(profile.state === "candidate" ? "Candidate" : "Confirmed")));
  title.append(el("span", "identity-source", t(profile.source === "request_capture" ? "Captured from a request" : profile.source === "request_import" ? "Imported from headers" : "Built-in profile")));
  card.append(title);
  const fields = el("dl", "identity-fingerprint");
  const entries = [[t("User-Agent"), profile.fingerprint?.userAgent], [t("Version"), profile.fingerprint?.version], ...Object.entries(profile.fingerprint?.headers || {})];
  for (const [name, value] of entries) {
    if (!value) continue;
    fields.append(el("dt", "", name), el("dd", "", value));
  }
  card.append(fields);
  const meta = el("div", "identity-meta");
  const made = profile.createdAt ? new Date(profile.createdAt) : null;
  if (made && !isNaN(made)) meta.append(el("span", "", t("Created {time}", { time: made.toLocaleString(intlLang()) })));
  if (profile.bindings?.length) meta.append(el("span", "", t("Presented by: {providers}", { providers: profile.bindings.map(identityProviderName).join(", ") })));
  card.append(meta);

  if (profile.state === "candidate") {
    const actions = el("div", "identity-actions");
    const confirm = el("button", "text primary", t("Confirm profile"));
    confirm.type = "button";
    confirm.onclick = () => identityAction("confirm", { profile: profile.id });
    const remove = el("button", "text", t("Remove"));
    remove.type = "button";
    remove.onclick = () => removeIdentityProfile(profile);
    actions.append(confirm, remove);
    card.append(actions);
    return card;
  }

  const boundProviders = (profile.bindings || []).map((id) => state.providers.find((p) => p.id === id) || { id, name: id });
  if (boundProviders.length) {
    const bound = el("div", "identity-bound");
    bound.append(el("span", "sub", t("Presented by: {providers}", { providers: boundProviders.map((p) => p.name).join(", ") })));
    for (const p of boundProviders) {
      const unbind = el("button", "text", t("Unbind {provider}", { provider: p.name }));
      unbind.type = "button";
      unbind.onclick = () => identityAction("unbind", { provider: p.id, rev: p.rev });
      bound.append(unbind);
    }
    card.append(bound);
  }
  const free = state.providers.filter((p) => p.applicable && !(state.bindings || {})[p.id]);
  if (free.length) {
    const bindRow = el("div", "identity-actions");
    const picker = el("button", "field identity-picker");
    picker.type = "button";
    picker.setAttribute("aria-label", t("Provider to bind"));
    let id = "";
    const set = (p) => { id = p?.id || ""; picker.replaceChildren(el("span", "v" + (p ? "" : " empty"), p ? p.name : t("Choose provider")), el("span", "chev", "⌄")); };
    set(free[0]);
    picker.onclick = () => openProtoMenu(picker, free.map((p) => ({ v: p.id, name: p.name, note: p.id })), id, (v) => set(free.find((p) => p.id === v)), "Provider to bind", "", "left");
    const bind = el("button", "text primary", t("Bind profile"));
    bind.type = "button";
    bind.disabled = !id;
    bind.onclick = () => identityAction("bind", { provider: id, profile: profile.id, rev: free.find((p) => p.id === id)?.rev });
    bindRow.append(picker, bind);
    card.append(bindRow);
  }
  const unavailable = state.providers.filter((p) => !p.applicable);
  if (unavailable.length) {
    const why = el("details", "identity-inapplicable");
    why.append(el("summary", "", t("Providers that cannot use profiles ({n})", { n: unavailable.length })));
    const list = el("ul");
    for (const p of unavailable) list.append(el("li", "", t("{provider}: {reason}", { provider: p.name, reason: p.why || "" })));
    why.append(list);
    card.append(why);
  }
  const remove = el("button", "text", t("Remove profile"));
  remove.type = "button";
  remove.onclick = () => removeIdentityProfile(profile);
  card.append(remove);
  return card;
}

async function removeIdentityProfile(profile) {
  const name = profile.client === "codex_cli" ? t("Codex CLI identity") : t("Claude Code identity");
  // t() fills {n} itself: a number put in before it would no longer match
  // the key, and the sentence would stay English (zh caught this)
  const n = profile.bindings?.length || 0;
  const consequence = n
    ? t("This also unbinds {n} providers using this profile. Continue?", { n })
    : t("This removes the profile. Continue?");
  if (!(await confirmAction(t("Remove {name}?", { name }), consequence, t("Remove")))) return;
  await identityAction("remove", { profile: profile.id });
}

async function identityAction(action, body) {
  try {
    const [nextIdentity, nextProviders] = await Promise.all([
      api("identity/" + action, body),
      api("providers"),
    ]);
    identityState = nextIdentity;
    providers = nextProviders;
    identityLoadError = "";
    renderProviders();
    status(t(({ arm: "Capture armed on the gateway", disarm: "Capture stopped", confirm: "Identity profile confirmed", bind: "Identity profile bound", unbind: "Identity profile unbound", remove: "Identity profile removed" })[action] || "Identity profiles updated"), "ok");
  } catch (e) {
    if (e.code === "stale") loadProviders().catch(() => {}); // the bindings as they are now
    status(e.message, "err");
  }
}

// identityField is the editor's view of the outbound identity profile this
// provider presents, and nothing more: a summary and a way to the panel that
// manages them. It is deliberately not an editor of one.
//
// The profile never joins the provider's save: a binding lives in
// identities.json, keyed by provider id, and is changed through
// /api/identity/{bind,unbind} alone. So the field reads identityState, not
// draft, and a Save (or a discarded draft) leaves the binding as it was.
function identityField(p) {
  // a provider not saved yet has no id to bind to, and the API read may have
  // failed: nothing is claimed either way
  const s = identityState && Array.isArray(identityState.providers) ? identityState : null;
  if (!p?.id || !s) return [];
  const mine = s.providers.find((x) => x.id === p.id);
  if (!mine) return [];
  const box = el("div", "stack identity-field");
  // a provider no profile would ever be sent on says so, with the one
  // reason provider.IdentityApplies gives, and offers nothing to bind
  if (!mine.applicable) {
    box.append(el("div", "identity-field-none", mine.why || t("This provider cannot present an identity profile.")));
    return field(t("Identity"), box, t("Only a provider reached with your own key and base URL can present a frozen client identity."));
  }
  const bound = mine.profileId ? s.profiles.find((x) => x.id === mine.profileId) : null;
  if (bound) {
    const who = el("div", "identity-field-on");
    who.append(el("b", "", bound.client === "codex_cli" ? t("Codex CLI identity") : t("Claude Code identity")));
    if (bound.fingerprint?.version) who.append(el("span", "sub", bound.fingerprint.version));
    box.append(who);
    if (bound.fingerprint?.userAgent) box.append(el("div", "identity-field-ua", bound.fingerprint.userAgent));
    const unbind = el("button", "text", t("Unbind"));
    unbind.type = "button";
    // the binding is its own store: unbinding leaves the open draft alone,
    // and a refused one changes nothing but the status line
    unbind.onclick = () => identityAction("unbind", { provider: p.id, rev: mine.rev });
    box.append(unbind);
  } else {
    box.append(el("div", "identity-field-none", t("No identity profile: requests go out as magpie.")));
  }
  const manage = el("button", "text", t("Manage identity profiles"));
  manage.type = "button";
  manage.onclick = async (e) => {
    // the panel is on the page under this editor: open the fold, close the
    // editor and take the reader to it. The scroll is the reader's own
    // click, so the click guard lets it stand (scrollOnPurpose).
    const go = scrollOnPurpose(e, 1200);
    if (identityFolded) $("#foldIdentity").click();
    if (!(await cancelEdit())) return;
    if (go) $("#identityPanel").scrollIntoView({ block: "nearest" });
  };
  box.append(manage);
  return field(t("Identity"), box, bound
    ? t("Every request, probe and model list for this provider presents this frozen client identity.")
    : t("Capture and confirm a profile in Identity profiles on the Providers page, then bind it here."));
}
