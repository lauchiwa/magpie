// fork: what the user's own prices make free, in the page. The free
// badge's word (provider.FreeBy, sent as a model's priced: "model",
// "provider" or "paid"): freeBadge and the provider editor's model chips
// take their title from pricedSays, and the chips, Free only and the
// model picker skip a model priced "paid", whose name may still say free.
// A routing group's Free first and Cheapest first hints end with what its
// member order decides (groupOrderSays); the Routing page only calls in.
// Loaded before app.js; nothing here runs at load but the words. Kept out
// of i18n.js and app.js so an upstream sync meets one file of ours.
magpie.strings({
  zh: {
    "Paid members keep their listed order too: the order of the members is your preference among those the routing ranks the same.": "付费成员也按列出的顺序：成员的顺序就是你的偏好，在路由排得一样的成员之间起作用。",
    "The order of the members is your preference among those that cost the same.": "成员的顺序就是你的偏好，在价格相同的成员之间起作用。",
    "free: you priced it at 0": "免费：你给它设的价格是 0",
    "free: you priced every model of this provider at 0": "免费：你给这个供应商的所有模型设的价格是 0",
  },
  "zh-TW": {
    "Paid members keep their listed order too: the order of the members is your preference among those the routing ranks the same.": "付費成員也按列出的順序：成員的順序就是你的偏好，在路由排得一樣的成員之間起作用。",
    "The order of the members is your preference among those that cost the same.": "成員的順序就是你的偏好，在價格相同的成員之間起作用。",
    "free: you priced it at 0": "免費：你給它設的價格是 0",
    "free: you priced every model of this provider at 0": "免費：你給這個供應商的所有模型設的價格是 0",
  },
  ja: {
    "Paid members keep their listed order too: the order of the members is your preference among those the routing ranks the same.": "有料のメンバーも設定順のままです。メンバーの順序が、ルーティングで同じ順位になるもの同士でのあなたの優先順位です。",
    "The order of the members is your preference among those that cost the same.": "メンバーの順序が、同じ価格のもの同士でのあなたの優先順位です。",
    "free: you priced it at 0": "無料：価格を 0 に設定しています",
    "free: you priced every model of this provider at 0": "無料：このプロバイダーの全モデルの価格を 0 に設定しています",
  },
  de: {
    "Paid members keep their listed order too: the order of the members is your preference among those the routing ranks the same.": "Auch bezahlte Mitglieder behalten ihre Reihenfolge: Die Reihenfolge der Mitglieder ist deine Vorliebe unter denen, die das Routing gleich einstuft.",
    "The order of the members is your preference among those that cost the same.": "Die Reihenfolge der Mitglieder ist deine Vorliebe unter denen, die gleich viel kosten.",
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

// groupOrderSays: what a group's member order decides under its routing,
// added to the Routing page's hint for it (routing/client.js gHint and the
// row's menu); "" where the hint says it already. The order is the user's
// preference among members the routing ranks the same (routes.FreeFirst,
// routes.Cheapest's sameCost).
function groupOrderSays(routing) {
  return routing === "free" ? t("Paid members keep their listed order too: the order of the members is your preference among those the routing ranks the same.")
    : routing === "cheap" ? t("The order of the members is your preference among those that cost the same.") : "";
}
