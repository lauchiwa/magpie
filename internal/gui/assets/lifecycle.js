// fork: what the page says when a provider write is refused by the
// lifecycle checks (internal/gui/lifecycle_fork.go's lifecycleFail).
// A save, delete, switch, bind or unbind carries the provider's rev as the
// page read it; the answer's code says why it was refused:
//   stale     — changed, removed or remade elsewhere since: nothing written
//   pending   — its last change is still being finished: not to be made again
//   follow_up — the change is saved, and a step after it failed (reason)
// and committed "1" with its step whenever the change itself is saved.
// Kept out of i18n.js and app.js so an upstream sync meets one file of ours.
magpie.strings({
  zh: {
    "This provider was changed, removed or added again elsewhere since this page read it, so nothing was written. Open it again to see it as it is now.": "这个供应商在本页读到它之后，已在别处被修改、删除或重新添加，所以什么都没有写入。请重新打开它，查看它现在的样子。",
    "The last change to this provider is still being finished: it is completed with the next provider change or when magpie starts. Don't make it again.": "这个供应商的上一次改动还在收尾：会在下一次改动供应商或 magpie 启动时完成。请不要再改一次。",
    "Saved, except for {step}: {why}. {next}": "已保存，但{step}没有完成：{why}。{next}",
    "it was changed elsewhere in between": "这期间它在别处被改过",
    "a change before it is still being finished": "前一次改动还在收尾",
    "Open the provider again and save that part again.": "请重新打开这个供应商，把这一部分再保存一次。",
    "Don't make the change again: it is saved.": "不要再改一次：改动已经保存。",
    "renaming the provider": "重命名",
    "adding its other keys": "添加其余密钥",
    "its model names and levels": "模型名称与推理档位",
    "its output limits": "输出上限",
    "its compaction thresholds": "压缩阈值",
    "moving its model settings to the new name": "把模型设置移到新名称下",
    "moving agents to the new name": "把 Agent 移到新名称下",
    "moving or forgetting its sign-in": "移动或清除它的登录信息",
  },
  "zh-TW": {
    "This provider was changed, removed or added again elsewhere since this page read it, so nothing was written. Open it again to see it as it is now.": "這個供應商在本頁讀到它之後，已在別處被修改、刪除或重新新增，所以什麼都沒有寫入。請重新開啟它，查看它現在的樣子。",
    "The last change to this provider is still being finished: it is completed with the next provider change or when magpie starts. Don't make it again.": "這個供應商的上一次變更還在收尾：會在下一次變更供應商或 magpie 啟動時完成。請不要再改一次。",
    "Saved, except for {step}: {why}. {next}": "已儲存，但{step}沒有完成：{why}。{next}",
    "it was changed elsewhere in between": "這期間它在別處被改過",
    "a change before it is still being finished": "前一次變更還在收尾",
    "Open the provider again and save that part again.": "請重新開啟這個供應商，把這一部分再儲存一次。",
    "Don't make the change again: it is saved.": "不要再改一次：變更已經儲存。",
    "renaming the provider": "重新命名",
    "adding its other keys": "新增其餘金鑰",
    "its model names and levels": "模型名稱與推理檔位",
    "its output limits": "輸出上限",
    "its compaction thresholds": "壓縮閾值",
    "moving its model settings to the new name": "把模型設定移到新名稱下",
    "moving agents to the new name": "把 Agent 移到新名稱下",
    "moving or forgetting its sign-in": "移動或清除它的登入資訊",
  },
  ja: {
    "This provider was changed, removed or added again elsewhere since this page read it, so nothing was written. Open it again to see it as it is now.": "このページが読み込んだ後に、このプロバイダーは別の場所で変更・削除・再追加されたため、何も書き込んでいません。開き直して現在の状態を確認してください。",
    "The last change to this provider is still being finished: it is completed with the next provider change or when magpie starts. Don't make it again.": "このプロバイダーへの前回の変更はまだ仕上げの途中です：次にプロバイダーを変更したときか magpie の起動時に完了します。もう一度変更しないでください。",
    "Saved, except for {step}: {why}. {next}": "保存しましたが、{step}は完了していません：{why}。{next}",
    "it was changed elsewhere in between": "その間に別の場所で変更されました",
    "a change before it is still being finished": "前の変更がまだ仕上げの途中です",
    "Open the provider again and save that part again.": "プロバイダーを開き直して、その部分をもう一度保存してください。",
    "Don't make the change again: it is saved.": "変更をやり直さないでください：保存済みです。",
    "renaming the provider": "名前の変更",
    "adding its other keys": "残りのキーの追加",
    "its model names and levels": "モデル名と推論レベル",
    "its output limits": "出力の上限",
    "its compaction thresholds": "コンパクションのしきい値",
    "moving its model settings to the new name": "モデル設定の新しい名前への移動",
    "moving agents to the new name": "エージェントの新しい名前への移動",
    "moving or forgetting its sign-in": "サインイン情報の移動または削除",
  },
  de: {
    "This provider was changed, removed or added again elsewhere since this page read it, so nothing was written. Open it again to see it as it is now.": "Dieser Anbieter wurde seit dem Laden dieser Seite an anderer Stelle geändert, entfernt oder neu hinzugefügt, daher wurde nichts geschrieben. Öffnen Sie ihn erneut, um seinen aktuellen Stand zu sehen.",
    "The last change to this provider is still being finished: it is completed with the next provider change or when magpie starts. Don't make it again.": "Die letzte Änderung an diesem Anbieter wird noch abgeschlossen: Das geschieht bei der nächsten Anbieteränderung oder beim Start von magpie. Nehmen Sie sie nicht erneut vor.",
    "Saved, except for {step}: {why}. {next}": "Gespeichert, außer {step}: {why}. {next}",
    "it was changed elsewhere in between": "er wurde zwischendurch an anderer Stelle geändert",
    "a change before it is still being finished": "eine vorherige Änderung wird noch abgeschlossen",
    "Open the provider again and save that part again.": "Öffnen Sie den Anbieter erneut und speichern Sie diesen Teil noch einmal.",
    "Don't make the change again: it is saved.": "Nehmen Sie die Änderung nicht erneut vor: Sie ist gespeichert.",
    "renaming the provider": "dem Umbenennen",
    "adding its other keys": "dem Hinzufügen der übrigen Schlüssel",
    "its model names and levels": "den Modellnamen und Stufen",
    "its output limits": "den Ausgabelimits",
    "its compaction thresholds": "den Kompaktierungsschwellen",
    "moving its model settings to the new name": "dem Übertragen der Modelleinstellungen auf den neuen Namen",
    "moving agents to the new name": "dem Übertragen der Agenten auf den neuen Namen",
    "moving or forgetting its sign-in": "dem Übertragen oder Entfernen seiner Anmeldung",
  },
});

// each step a save, rename or delete names (lifecycle_fork.go, provider's
// StepSettings and StepSignIn, agent.StepAgents), as the page says it
const LIFECYCLE_STEPS = {
  rename: "renaming the provider", keys: "adding its other keys", "model-prefs": "its model names and levels",
  outputs: "its output limits", compacts: "its compaction thresholds",
  settings: "moving its model settings to the new name", agents: "moving agents to the new name", "sign-in": "moving or forgetting its sign-in",
};
// the steps a second Save makes again: the editor's form still holds them.
// The others were done again once by magpie already (a rename's, a
// delete's), and a second Save wouldn't reach them.
const LIFECYCLE_REDONE = new Set(["rename", "keys", "model-prefs", "outputs", "compacts"]);

// lifecycleError is api()'s: err takes the answer's code, step and
// committed, and its message is said in the reader's language. An answer
// without one of these codes is left as it is.
function lifecycleError(err, data) {
  if (data?.committed === "1") {
    err.committed = true;
    err.step = data.step || "";
  }
  if (!["stale", "pending", "follow_up"].includes(data?.code)) return;
  err.code = data.code;
  if (!err.committed) {
    err.message = data.code === "pending"
      ? t("The last change to this provider is still being finished: it is completed with the next provider change or when magpie starts. Don't make it again.")
      : t("This provider was changed, removed or added again elsewhere since this page read it, so nothing was written. Open it again to see it as it is now.");
    return;
  }
  const steps = err.step.split(", ").filter(Boolean);
  const step = steps.map((s) => (LIFECYCLE_STEPS[s] ? t(LIFECYCLE_STEPS[s]) : s)).join(", ");
  const why = data.code === "stale" ? t("it was changed elsewhere in between")
    : data.code === "pending" ? t("a change before it is still being finished")
      : data.reason || data.error || "";
  const next = data.code !== "pending" && steps.length && steps.every((s) => LIFECYCLE_REDONE.has(s))
    ? t("Open the provider again and save that part again.")
    : t("Don't make the change again: it is saved.");
  err.message = t("Saved, except for {step}: {why}. {next}", { step, why: why.replace(/[.。]$/, ""), next });
}
