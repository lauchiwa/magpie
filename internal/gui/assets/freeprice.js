// fork: what the user's own prices make free, in the page. The free
// badge's word (provider.FreeBy, sent as a model's priced: "model",
// "provider" or "paid"): freeBadge and the provider editor's model chips
// take their title from pricedSays, and the chips, Free only and the
// model picker skip a model priced "paid", whose name may still say free.
// The key provider editor's whole-provider toggle (wholeFreeField, drawn
// from the provider's whole, provider.WholeOf). app.js only calls in.
// Loaded before app.js; nothing here runs at load but the words. Kept out
// of i18n.js and app.js so an upstream sync meets one file of ours.
magpie.strings({
  zh: {
    "Whole provider free": "整个供应商免费",
    "On: every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.": "打开后这个供应商的所有模型按 0 计价，价格倍率不再起作用；你给单个模型设的价格仍然优先。模型价格框里的灰色数字仍是列表价。",
    "You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset": "你已用命令行给这个供应商的所有模型设了价格 {price}（输入,输出,缓存读,缓存写，每百万 token 美元）。要在这里改，先运行：magpie model price \"{id}/*\" --reset",
    "Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset": "你给这个供应商所有模型设的价格写坏了（{part}）。要在这里改，先运行：magpie model price \"{id}/*\" --reset",
    "free: you priced it at 0": "免费：你给它设的价格是 0",
    "free: you priced every model of this provider at 0": "免费：你给这个供应商的所有模型设的价格是 0",
  },
  "zh-TW": {
    "Whole provider free": "整個供應商免費",
    "On: every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.": "開啟後這個供應商的所有模型按 0 計價，價格倍率不再起作用；你給單個模型設的價格仍然優先。模型價格框裡的灰色數字仍是列表價。",
    "You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset": "你已用命令列給這個供應商的所有模型設了價格 {price}（輸入,輸出,快取讀,快取寫，每百萬 token 美元）。要在這裡改，先執行：magpie model price \"{id}/*\" --reset",
    "Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset": "你給這個供應商所有模型設的價格寫壞了（{part}）。要在這裡改，先執行：magpie model price \"{id}/*\" --reset",
    "free: you priced it at 0": "免費：你給它設的價格是 0",
    "free: you priced every model of this provider at 0": "免費：你給這個供應商的所有模型設的價格是 0",
  },
  ja: {
    "Whole provider free": "プロバイダー全体を無料に",
    "On: every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.": "オンにすると、このプロバイダーの全モデルを 0 として計算し、価格倍率は適用されません。モデルごとに設定した価格が引き続き優先されます。モデルの価格欄の灰色の数字は定価のままです。",
    "You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset": "コマンドラインでこのプロバイダーの全モデルに価格 {price}（入力,出力,キャッシュ読み取り,キャッシュ書き込み、100 万トークンあたりのドル）を設定済みです。ここで変更するには、先に次を実行してください：magpie model price \"{id}/*\" --reset",
    "Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset": "このプロバイダーの全モデルに設定した価格が読み取れません（{part}）。ここで変更するには、先に次を実行してください：magpie model price \"{id}/*\" --reset",
    "free: you priced it at 0": "無料：価格を 0 に設定しています",
    "free: you priced every model of this provider at 0": "無料：このプロバイダーの全モデルの価格を 0 に設定しています",
  },
  de: {
    "Whole provider free": "Ganzer Anbieter kostenlos",
    "On: every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.": "An: Alle Modelle werden mit 0 berechnet, der Preisfaktor gilt nicht mehr; ein Preis, den du für ein Modell gesetzt hast, geht weiter vor. Die grauen Zahlen in den Preisfeldern eines Modells bleiben sein Listenpreis.",
    "You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset": "Du hast allen Modellen dieses Anbieters per Kommandozeile den Preis {price} gegeben (Eingabe,Ausgabe,Cache-Lesen,Cache-Schreiben, $ pro 1M Token). Um ihn hier zu ändern, führe zuerst aus: magpie model price \"{id}/*\" --reset",
    "Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset": "Dein Preis für alle Modelle dieses Anbieters ist nicht lesbar ({part}). Um ihn hier zu ändern, führe zuerst aus: magpie model price \"{id}/*\" --reset",
    "free: you priced it at 0": "kostenlos: du hast den Preis auf 0 gesetzt",
    "free: you priced every model of this provider at 0": "kostenlos: du hast alle Modelle dieses Anbieters auf 0 gesetzt",
  },
});

// pricedSays: why a model the user priced at nothing is free, "" when no
// price of theirs made it so (the vendor's word or its name stands)
function pricedSays(priced) {
  return priced === "model" ? t("free: you priced it at 0")
    : priced === "provider" ? t("free: you priced every model of this provider at 0") : "";
}

// wholeFreeField: the key provider editor's Off/On that prices every model
// of a saved provider at nothing (`<id>/*` = 0, what the badge, Free first
// and resting read), in Advanced after Price rate. It is staged in the
// draft's model prefs under "*", so the editor's Save writes it as it
// writes a model's price and Cancel drops it: a price of nothing turns it
// on, OwnPrice takes it off, and back where it was saved nothing is
// staged. A `<id>/*` priced otherwise or that doesn't read is the user's,
// set from the command line: shown, never written over. None for a
// provider not saved yet, whose Save sends no model prefs.
function wholeFreeField(p) {
  if (!p) return [];
  const w = p.whole || {}, was = !!w.free, locked = !!(w.price || w.bad);
  const now = () => { const x = draft.modelPrefs?.["*"]; return x ? !!x.price : was; };
  const set = (on) => {
    const prefs = draft.modelPrefs = draft.modelPrefs || {};
    if (on === was) delete prefs["*"];
    else prefs["*"] = on ? { price: { input: 0, output: 0, cache_read: 0, cache_write: 0 } } : { ownPrice: true };
    if (!Object.keys(prefs).length) delete draft.modelPrefs;
  };
  const box = segs([["off", t("Off")], ["on", t("On")]], !locked && now() ? "on" : "off", (v) => set(v === "on"));
  box.classList.add("whole-free");
  if (locked) {
    for (const b of box.querySelectorAll(".opt")) b.disabled = true;
    box.style.opacity = ".6";
  }
  const pr = w.price, hint = w.bad
    ? t("Your price for every model of it doesn't read ({part}). To change it here, first run: magpie model price \"{id}/*\" --reset", { part: w.bad, id: p.id })
    : pr
      ? t("You priced every model of it at {price} (input,output,cache read,cache write, $ per 1M tokens) with the command line. To change it here, first run: magpie model price \"{id}/*\" --reset", { price: [pr.input, pr.output, pr.cache_read, pr.cache_write].join(","), id: p.id })
      : t("On: every model of it is counted at 0, and Price rate no longer applies; a price you set for a model still comes first. The grey numbers in a model's price boxes are still its list price.");
  return field(t("Whole provider free"), box, hint);
}
