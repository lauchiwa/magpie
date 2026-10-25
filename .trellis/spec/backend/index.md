# Backend and System Development Guidelines

Magpie is a Go desktop/CLI application and local HTTP gateway, not a conventional web-service backend. These guides record the repository's current architecture and development practices; use them with the source-backed subsystem references in `docs/subsystems/`.

| Guide | Description |
| --- | --- |
| [Directory Structure](./directory-structure.md) | Package boundaries, route registration, file layout, GUI assets |
| [Database and Persistence](./database-guidelines.md) | External SQLite integration and Magpie-owned file state |
| [Error Handling](./error-handling.md) | Go errors, CLI/API response behavior, failure classification |
| [Logging](./logging-guidelines.md) | Standard logger, useful events, secret-safety |
| [Quality](./quality-guidelines.md) | Code style, hermetic tests, CI and verification expectations |

## Dependency and version source of truth

The authoritative Go toolchain version is the `go` directive in `go.mod` (currently `1.26.8`); direct and indirect Go module versions are pinned there and in `go.sum`. Do not copy a dependency-version list into this guide because it will drift—inspect those files when changing dependencies. The project also uses Bun for plugin runtime/tests, Node.js for site/release tooling, and Playwright for GUI tests; see `Makefile`, `Dockerfile`, `internal/plugin`, and `.github/workflows/test.yml` for the current versions and usage.

## Reverse proxy note

No Nginx configuration is maintained in this repository. The documented proxy contract is Magpie's gateway behavior, not a prescribed Nginx deployment template: see [reference.md](../../../docs/reference.md) (proxy/tunnel section), `internal/gateway/lan.go` (`proxied`, `forwardHeaders`, `MAGPIE_TRUST_PROXY`), and [quality-guidelines.md](./quality-guidelines.md). Do not invent proxy buffering, websocket, or header directives as project standards without a tested, documented deployment requirement.
