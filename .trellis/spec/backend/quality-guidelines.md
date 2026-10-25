# Quality and Verification Guidelines

## Change discipline

Follow `docs/code-standards.md`: keep a change limited to its goal, enumerate the cases that change and neighboring cases that must not, trace the full data flow and side effects, and grep for sibling implementations that need the same fix. Read `LESSONS.md` before code changes. For documented subsystems, read `docs/subsystems/README.md` and the owning reference; update that reference when an observable responsibility, interface, state transition, runtime path, compatibility rule, or failure behavior changes.

## Go style

- Run `gofmt`; use `any`, not `interface{}`.
- Use `appdir.Getenv`/`LookupEnv` for environment variables that name directories, so relative/unusable paths are not accepted. `agentenv.Vars` is the canonical list of agent environment variables cleared by test sandboxes; register non-path entries in `agentenv.NotPaths`.
- Keep comments concise and purpose-focused. Describe behavior and rationale where non-obvious; name functions instead of line numbers.
- Follow the package and layering conventions in [directory-structure.md](./directory-structure.md). Reuse existing helpers before creating another abstraction.

## Tests must prove behavior

Every fix should have a regression test that exercises realistic input and fails on the old behavior for the intended reason. Assert observable behavior, including empty, missing, default, boundary, sibling, and negative cases relevant to the change. Tests should use `t.TempDir`, `t.Setenv`, `t.Cleanup`, fixtures, and local `httptest` servers; do not depend on vendor internet, live credentials, or a developer's agent configuration.

Most packages use `internal/testenv` from `TestMain` to isolate HOME/XDG/APPDATA, clear agent variables and proxy settings, provide inert executables, and block external networking. A package with custom `TestMain` must preserve equivalent protections, including `testenv.Marker`. Use `testenv.SetHome` rather than HOME-only setup for cross-platform tests. Stop or await every server, goroutine, process, and asynchronous operation before a test ends.

For concurrency-sensitive Go tests, use the race detector; diagnose a suspected flake with `go test -race -count=20 -run TestName`. Timing tests need margin on both sides and should reproduce delays by injecting them, not by rerunning until lucky.

Examples: `internal/provider/planquota_test.go` (table-driven parsing and local server), `internal/gateway/plugin_decide_test.go` (gateway behavior with local fake upstream), `internal/plugin/host_flow_test.go` (streaming flow), and `internal/testenv/testenv.go`.

## GUI tests

For `internal/gui/assets` changes, add/adjust Playwright tests in `internal/gui/tests`. Run the relevant tests in Chromium and WebKit; run `make test-ui` for changes to shared APIs/selectors/styles/layout or where regression scope is broad. Exercise supported locales: English, Chinese, Traditional Chinese (`zh-TW`), Japanese, and German, with placeholders preserved. Follow GUI rules in `docs/code-standards.md`: user-facing strings through `t()` and `i18n.js`, app menu instead of native `<select>`, clicks must not scroll the page, and do not use colored left-border stripes.

## Required verification (report exact results)

For Go changes, select checks based on risk and the project acceptance standard in `docs/code-standards.md`. The full listed gate is:

```sh
gofmt -l <changed-go-files>                 # no output
go vet ./...
GOOS=linux go vet -tags nogui ./...
GOOS=windows go vet ./...
go build
GOOS=linux go build -tags nogui
GOOS=windows go build
go test -tags nogui ./...
```

Go tests must run in a temporary HOME with `GOPATH`, `GOMODCACHE`, and `GOCACHE` pinned to values obtained before swapping HOME. Never read/write `~/.config/magpie` or real agent homes. See `docs/subsystems/provider-plugins.md#verification` for the sandbox recipe. Use `set -o pipefail` for piped test commands. Cross-OS builds do not compile test files; vet/compile checks remain necessary.

CI is defined by `.github/workflows/test.yml`: Linux/macOS execute Go tests; Windows compiles the suite and runs selected native checks. Linux runs GTK3-tagged checks, production plugin-flow coverage, and race-sharded gateway tests. CI does not run the Playwright suite, so run it locally when applicable. For provider/subscription/agent work, test with the real upstream/config in a sandbox where possible; disclose when a real check was not possible. A GOOS build is not runtime verification.

## Review checklist

- Does the regression test fail when the old behavior is restored?
- Are the reporter's real bytes/input and failure mode represented?
- Were all sibling callers, vendors, routes, protocols, platforms and locales checked?
- Is the whole operation traced from input through stored state, side effects and output?
- Do tests avoid live network, user state and leaked goroutines/processes?
- Are all required and unrun checks reported explicitly?
- Do docs and subsystem references match the source and link to valid files?
- Are secrets excluded from logs and errors?

## Avoid

Do not accept a test that only compiles, passes only alone while failing in the full suite, depends on external network, or fails for an unrelated setup reason. A failure on `main` is still a defect to resolve or file before shipping; do not label it a baseline. Never overwrite user-owned data because a read/parse/hash failed. Do not claim platform/runtime behavior from a cross-build alone.
