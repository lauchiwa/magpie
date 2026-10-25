# Architecture and Directory Structure

## Project shape

Magpie is a single Go module and binary (`github.com/yetone/magpie`, Go `1.26.8`). The binary combines a hand-written CLI, TUI, desktop Wails application, browser UI, local LLM gateway, provider/account integrations, and plugin host. There is no `cmd/` tree or service framework.

The root package `main` is the composition root. `main.go` performs environment checks, installs cross-package hooks, runs migrations, then dispatches to command functions. Root command files use the `<command>_cli.go` convention (`accounts_cli.go`, `providers_cli.go`, `web_cli.go`, `usage_cli.go`). Keep feature implementation in `internal/`; do not introduce a new root package for reusable logic.

## Layout

```text
.
├── *.go                         # root CLI command handlers and platform shims
├── internal/
│   ├── appdir, agentenv, steady # filesystem/env primitives
│   ├── settings, catalog        # local settings and model catalog
│   ├── provider, access         # providers, accounts, subscriptions, gateway keys
│   ├── gateway                  # HTTP model gateway and routing/middleware
│   ├── agent                    # agent discovery, config wiring, model selection
│   ├── sessions, usage          # session indexing and usage records
│   ├── plugin, middleware       # Bun/OpenCode plugins and gateway middleware
│   ├── gui, tui                 # Wails/browser UI and terminal UI
│   └── testenv                  # hermetic test environment
├── internal/gui/assets/         # embedded, unbundled vanilla JS/CSS/HTML
├── internal/gui/tests/          # Node test + Playwright browser tests
├── docs/subsystems/             # source-backed subsystem contracts
├── site/                        # Cloudflare Worker and installer tests
├── scripts/                     # Node release/version scripts
├── build/                       # release assets, dev handover, race sharding
├── Dockerfile                   # nogui container image
└── .github/workflows/           # test, release, Docker and UI-preview CI
```

## Package boundaries and direction

The observed dependency direction is:

`agentenv/steady/redact` → `appdir/filememo/edit/lastgood` → `settings/catalog/proc/netproxy` → `plugin` → `provider` → `access/usage/sessions` → `gateway` → `agent` → `library/profile/backup/davsync` → `tui/gui` → `main`.

This is a convention, not a formal framework rule. Preserve it when adding code:

- `internal/provider` owns vendor adapters, keys, subscription sign-in/refresh/usage, routing groups, and migration state. It must not import `gateway` or `agent`.
- `internal/gateway` owns inbound HTTP protocols, request translation, provider selection/fallback, streaming, caller-key limits, and gateway middleware. It imports provider-facing packages, not GUI.
- `internal/agent` owns agent config discovery, field/model wiring, and connection state. It may consume gateway/provider APIs.
- `internal/gui` is the top-level UI/API layer; only root `main` imports it. Avoid making lower layers import GUI.
- Avoid import cycles by assigning hooks in `main.run`, for example `catalog.Changed`, `gateway.StandIn`, `gateway.WhileServing`, and `gateway.ArchiveBucket`.

Important package responsibilities are documented in `docs/subsystems/README.md` and its linked references. Read the relevant reference before changing gateway, provider/plugin, agent wiring, library, identity, or GUI-shell behavior.

## HTTP route organization

Gateway routes are registered in `internal/gateway/gateway.go` by `(*Server).Handler()` using `http.NewServeMux()` and Go 1.22 method patterns. Wire-specific handlers live in files such as `anthropic.go`, `responses.go`, `gemini.go`, and `chat.go`; provider/vendor behavior gets its own file. Register both `/v1` and compatibility paths when existing API conventions require it. Keep the middleware chain around the mux (`counted`, CORS, caller identity, key limits), not inside individual handlers.

GUI/browser JSON routes are registered by `gui.Handler` in `internal/gui/api.go`. Add a feature-specific `fooRoutes(mux)` in `internal/gui/foo.go`, call it from `Handler` or an owning route group, and add the matching Go and browser tests. Use `writeJSON` and `fail` rather than inventing response formats.

## File and naming conventions

- Keep packages flat and split files by vendor, wire protocol, or behavior: `internal/provider/kiro_{accounts,signin,usage}.go`, `internal/gateway/codex_*.go`, `internal/provider/migrate_<vendor>.go`.
- Platform/build variants use suffixes and explicit build constraints: `_windows.go`, `_unix.go`, `_darwin.go`, `_linux.go`, `_other.go`, `_gtk3.go`, `_gtk4.go`, and `_on.go`/`_off.go` pairs (`gui_on.go`/`gui_off.go`, `internal/gui/dev_on.go`/`dev_off.go`).
- Tests are many focused files named `<feature>_<behavior>_test.go`, not one package mega-test. Shared fixtures belong in package `testdata/`.
- Use package doc comments and document exported identifiers. Comments explain purpose/behavior and may preserve an issue number; do not use stale line-number references.

## GUI asset structure

`internal/gui/assets` is served from an embedded `embed.FS` in normal builds and from disk in `dev` builds. It is vanilla HTML/CSS/JavaScript with no bundler. `app.js` owns the main state/render flow; page modules include `routing.js`, `library.js`, `plugins.js`, `sessions.js`, `context.js`, and `caller-keys.js`; translations are in `i18n.js`; CSS is split by page. Add visible strings through `t()` and `i18n.js`, never hard-code user-facing text.

`internal/gui/tests` uses Node `node:test` and Playwright against the real assets with mocked API responses. Tests commonly run Chromium and WebKit and are named `<page-or-feature>-<behavior>.test.cjs`.

## Examples

- Root composition: `main.go`, `help_cli.go`, `gui_on.go`, `gui_off.go`
- Gateway routes/middleware: `internal/gateway/gateway.go`, `internal/gateway/lan.go`
- GUI route composition: `internal/gui/api.go`, `internal/gui/providers.go`, `internal/gui/identity.go`
- Durable state primitives: `internal/steady/steady.go`, `internal/settings/settings.go`
- Package-safe test setup: `internal/testenv/testenv.go`, `internal/provider/main_test.go`
