// fork: the provider editors' billing control, List price / Free / Free
// quota, in the main area of both editors after the models. It shows the
// mode the provider has today (billingMode, which the server derives from
// CountFree and `<id>/*` with provider.BillingPolicy) and stages the same
// two fields the editor's Save already sends: Free is `<id>/*` priced at
// nothing (model prefs under "*"), Free quota is countFree, List price is
// neither. Cancel drops what is staged. A `<id>/*` the user priced
// otherwise from the command line (billingLocked) is never written over:
// Free can't be chosen, the hint says how to clear it, and the other two
// stage no price (wasFree is false, so they only drop a staged one). A provider not
// saved yet can't be made Free either: its Save sends no model prefs.
// app.js only calls in. Loaded before app.js; nothing here runs at load
// but the words. Kept out of i18n.js and app.js so an upstream sync meets
// one file of ours.
magpie.strings({
  zh: {
    "Billing": "计费",
    "List price": "按列表价",
    "Free quota": "免费额度",
    "Costs are counted at the list price times Price rate; a price you set for a model stays as set.": "费用按列表价乘以价格倍率计算；你给单个模型设的价格保持不变。",
    "Every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.": "这个供应商的所有模型按 0 计价，价格倍率不再起作用；你给单个模型设的价格仍然优先。模型价格框里的灰色数字仍是列表价。",
    "Free can be chosen once it is added.": "添加之后才能选「免费」。",
    "You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset": "你已用命令行给这个供应商的所有模型设了价格 {price}（输入,输出,缓存读,缓存写，每百万 token 美元）。要在这里改，先运行：magpie model price \"{id}/*\" --reset",
    "Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset": "你给这个供应商所有模型设的价格写坏了（{part}）。要在这里改，先运行：magpie model price \"{id}/*\" --reset",
  },
  "zh-TW": {
    "Billing": "計費",
    "List price": "按列表價",
    "Free quota": "免費額度",
    "Costs are counted at the list price times Price rate; a price you set for a model stays as set.": "費用按列表價乘以價格倍率計算；你給單個模型設的價格保持不變。",
    "Every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.": "這個供應商的所有模型按 0 計價，價格倍率不再起作用；你給單個模型設的價格仍然優先。模型價格框裡的灰色數字仍是列表價。",
    "Free can be chosen once it is added.": "新增之後才能選「免費」。",
    "You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset": "你已用命令列給這個供應商的所有模型設了價格 {price}（輸入,輸出,快取讀,快取寫，每百萬 token 美元）。要在這裡改，先執行：magpie model price \"{id}/*\" --reset",
    "Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset": "你給這個供應商所有模型設的價格寫壞了（{part}）。要在這裡改，先執行：magpie model price \"{id}/*\" --reset",
  },
  ja: {
    "Billing": "課金",
    "List price": "定価",
    "Free quota": "無料枠",
    "Costs are counted at the list price times Price rate; a price you set for a model stays as set.": "コストは定価に価格倍率を掛けて計算します。モデルごとに設定した価格はそのままです。",
    "Every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.": "このプロバイダーの全モデルを 0 として計算し、価格倍率は適用されません。モデルごとに設定した価格が引き続き優先されます。モデルの価格欄の灰色の数字は定価のままです。",
    "Free can be chosen once it is added.": "「無料」は追加した後に選べます。",
    "You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset": "コマンドラインでこのプロバイダーの全モデルに価格 {price}（入力,出力,キャッシュ読み取り,キャッシュ書き込み、100 万トークンあたりのドル）を設定済みです。ここで変更するには、先に次を実行してください：magpie model price \"{id}/*\" --reset",
    "Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset": "このプロバイダーの全モデルに設定した価格が読み取れません（{part}）。ここで変更するには、先に次を実行してください：magpie model price \"{id}/*\" --reset",
  },
  de: {
    "Billing": "Abrechnung",
    "List price": "Listenpreis",
    "Free quota": "Gratiskontingent",
    "Costs are counted at the list price times Price rate; a price you set for a model stays as set.": "Kosten werden zum Listenpreis mal Preisfaktor gezählt; ein Preis, den du für ein Modell gesetzt hast, bleibt wie gesetzt.",
    "Every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.": "Alle Modelle werden mit 0 berechnet, der Preisfaktor gilt nicht mehr; ein Preis, den du für ein Modell gesetzt hast, geht weiter vor. Die grauen Zahlen in den Preisfeldern eines Modells bleiben sein Listenpreis.",
    "Free can be chosen once it is added.": "„Kostenlos“ lässt sich wählen, sobald er hinzugefügt ist.",
    "You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset": "Du hast allen Modellen dieses Anbieters per Kommandozeile den Preis {price} gegeben (Eingabe,Ausgabe,Cache-Lesen,Cache-Schreiben, $ pro 1M Token). Um ihn hier zu ändern, führe zuerst aus: magpie model price \"{id}/*\" --reset",
    "Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset": "Dein Preis für alle Modelle dieses Anbieters ist nicht lesbar ({part}). Um ihn hier zu ändern, führe zuerst aus: magpie model price \"{id}/*\" --reset",
  },
});

// billingField: the control and its hint, which says what the choice does
// today. p is the saved provider, none for one being added (a copy too).
function billingField(p) {
  const w = p?.whole || {}, locked = !!p?.billingLocked;
  const wasFree = p?.billingMode === "free"; // `<id>/*` is nothing as saved
  const freeNow = () => { const x = draft.modelPrefs?.["*"]; return x ? !!x.price : wasFree; };
  const now = () => (freeNow() ? "free" : draft.countFree ? "quota" : "list");
  const says = (mode) => mode === "free"
    ? t("Every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.")
    : mode === "quota"
      ? t("For a free quota, like a daily allowance: Free first uses its models before paid ones, and Cheapest first counts them as costing nothing. Prices and the costs Usage counts stay as listed.")
      : t("Costs are counted at the list price times Price rate; a price you set for a model stays as set.");
  const pr = w.price, why = !p ? t("Free can be chosen once it is added.")
    : w.bad ? t("Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset", { part: w.bad, id: p.id })
      : pr ? t("You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset", { price: [pr.input, pr.output, pr.cache_read, pr.cache_write].join(","), id: p.id })
        : "";
  const set = (mode) => {
    if (mode === now()) return;
    draft.countFree = mode === "quota";
    if (!p) return;
    const prefs = draft.modelPrefs = draft.modelPrefs || {};
    if ((mode === "free") === wasFree) delete prefs["*"];
    else prefs["*"] = mode === "free" ? { price: { input: 0, output: 0, cache_read: 0, cache_write: 0 } } : { ownPrice: true };
    if (!Object.keys(prefs).length) delete draft.modelPrefs;
  };
  const box = segs([["list", t("List price")], ["free", t("Free")], ["quota", t("Free quota")]], now(), (mode) => {
    set(mode);
    hint.textContent = says(mode);
  });
  box.classList.add("billing-mode");
  box.setAttribute("role", "group");
  box.setAttribute("aria-label", t("Billing"));
  if (!p || locked) {
    const free = box.querySelectorAll(".opt")[1];
    free.disabled = true;
    free.style.opacity = ".5";
    free.title = why;
  }
  const row = field(t("Billing"), box, says(now()));
  const hint = row[1].querySelector(".hint");
  hint.classList.add("billing-says");
  box.setAttribute("aria-describedby", hint.id);
  if (why) {
    const note = el("div", "hint billing-why", why);
    note.id = hint.id + "-why";
    row[1].append(note);
    box.setAttribute("aria-describedby", hint.id + " " + note.id);
  }
  return row;
}
