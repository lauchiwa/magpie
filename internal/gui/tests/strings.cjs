// The window's words in each language, as the page has them: i18n.js's,
// and each plugin's (assets/plugins/<id>/i18n.js, added by the kernel's
// magpie.strings), and the fork's own files (FORK). files says which
// file said which; a key two files say is in twice.
const fs = require("node:fs");
const path = require("node:path");

const assets = path.resolve(__dirname, "../assets");
// fork: the core files of ours that say their words through magpie.strings
const FORK = ["lifecycle.js", "identity.js"];

function strings() {
  const src = fs.readFileSync(path.join(assets, "i18n.js"), "utf8");
  const I18N = new Function(src.slice(0, src.indexOf("\n};") + 3) + "; return I18N;")();
  const files = [{ file: "i18n.js", src, dict: structuredClone(I18N) }];
  const twice = [];
  const dir = path.join(assets, "plugins");
  const own = [...FORK, ...fs.readdirSync(dir).sort().map((id) => path.join("plugins", id, "i18n.js"))];
  for (const name of own) {
    const file = path.join(assets, name);
    if (!fs.existsSync(file)) continue;
    const src = fs.readFileSync(file, "utf8");
    let dict;
    new Function("magpie", src)({ strings: (d) => { dict = d; } });
    files.push({ file: path.relative(assets, file), src, dict });
    for (const [lang, words] of Object.entries(dict)) {
      const to = (I18N[lang] ||= {});
      for (const [k, v] of Object.entries(words)) {
        if (k in to) twice.push(`${lang} ${JSON.stringify(k)} in ${path.relative(assets, file)}`);
        else to[k] = v;
      }
    }
  }
  return { I18N, files, twice };
}

module.exports = { strings, FORK };
