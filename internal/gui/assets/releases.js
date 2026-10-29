// fork: Settings › About's Releases row, under upstream's Website row.
// usemagpie.ai downloads upstream's builds, which replace this fork's and
// then update from upstream; this fork's builds and their notes are on its
// own Releases page (update.Downloads, sent as settings' releases). app.js
// only calls in. Loaded before app.js; nothing here runs at load but the
// words. Kept out of i18n.js and app.js so an upstream sync meets one file
// of ours.
magpie.strings({
  zh: {
    "Releases": "版本发布",
    "this build's downloads and what's new in each version": "这个版本的下载和每个版本的更新内容",
  },
  "zh-TW": {
    "Releases": "版本發佈",
    "this build's downloads and what's new in each version": "這個版本的下載和每個版本的更新內容",
  },
  ja: {
    "Releases": "リリース",
    "this build's downloads and what's new in each version": "このビルドのダウンロードと各バージョンの変更点",
  },
  de: {
    "Releases": "Releases",
    "this build's downloads and what's new in each version": "Downloads dieses Builds und was in jeder Version neu ist",
  },
});

// releasesRow: About's row for the fork's Releases page, as row() takes
// it (name, sub, value, button); none when settings name no page
function releasesRow(s) {
  if (!s.releases) return null;
  const open = el("button", "text", t("Open"));
  open.onclick = () => api("open", { url: s.releases }).catch(() => {});
  return [t("Releases"), t("this build's downloads and what's new in each version"), s.releases.replace(/^https:\/\//, ""), open];
}
