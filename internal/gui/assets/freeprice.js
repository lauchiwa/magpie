// fork: the free badge's word when the user's own price made a model free
// (provider.FreeBy, sent as a model's priced: "model", "provider" or
// "paid"). app.js only calls in: freeBadge and the provider editor's
// model chips take their title from pricedSays, and the chips, Free only
// and the model picker skip a model priced "paid", whose name may still
// say free. Loaded before app.js; nothing here runs at load but the words.
// Kept out of i18n.js and app.js so an upstream sync meets one file of ours.
magpie.strings({
  zh: {
    "free: you priced it at 0": "免费：你给它设的价格是 0",
    "free: you priced every model of this provider at 0": "免费：你给这个供应商的所有模型设的价格是 0",
  },
  "zh-TW": {
    "free: you priced it at 0": "免費：你給它設的價格是 0",
    "free: you priced every model of this provider at 0": "免費：你給這個供應商的所有模型設的價格是 0",
  },
  ja: {
    "free: you priced it at 0": "無料：価格を 0 に設定しています",
    "free: you priced every model of this provider at 0": "無料：このプロバイダーの全モデルの価格を 0 に設定しています",
  },
  de: {
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
