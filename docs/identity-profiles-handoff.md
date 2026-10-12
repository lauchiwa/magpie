# Identity Profiles GUI Handoff

## Scope

This handoff covers the final GUI-side validation work for outbound identity profiles. The implementation is already present in the preceding commits:

- `a6b4a4ad` — shared gateway control client
- `2db4cc4f` — GUI identity API
- `d9796783` — Providers-page identity profiles panel
- `59d8978b` — provider editor identity binding summary

This change adds the missing real-control and timeout coverage, and records that coverage in the subsystem reference.

## Changes in this handoff

### `internal/gui/identity.go`

`identityControlTimeout` is now a variable with the production default of 10 seconds. GUI identity reads and mutations already derive a context from this timeout; making it adjustable lets the test exercise an unresponsive local gateway without waiting ten seconds.

The timeout is intentionally applied to both the armed-window read and arm/disarm mutations. A gateway that accepts a connection but never responds must not hold the browser API request forever.

### `internal/gui/identity_test.go`

Added a local `httptest` gateway helper that routes GUI identity operations through the real shared gateway control client instead of replacing the client with a stub. The tests cover:

- arm, armed-state read and disarm through the actual control paths;
- query parameters sent to the gateway;
- preservation of the gateway-provided client and `expiresAt` details;
- gateway refusal propagation without changing local profile state;
- bounded behavior when the gateway does not answer;
- cleanup ordering, so a deliberately held fake handler is released before `httptest.Server.Close`.

The bounded test uses a shortened timeout and an independent patience window, leaving room on both sides as required by the project test standards.

### `docs/subsystems/identity-profiles.md`

The verification section now documents the real-control tests and their safety/timeout guarantees.

## Verification completed

All Go commands below used a temporary `HOME`, isolated XDG directories, and the repository Go caches pinned from `go env`.

- `go test ./internal/gui -run 'TestIdentityControl' -count=1 -v` — passed.
- `go test ./internal/gui -count=1` — passed.
- Identity-focused gateway tests — passed:
  - `TestIdentityControlArmAndDisarm`
  - `TestIdentityControlArmedDetailsAndLegacy`
  - `TestIdentityControlErrors`
  - `TestIdentityControlTimeout`
  - `TestIdentityArmedRejectsInvalidExpiry`
- `gofmt -l internal/gui/identity.go internal/gui/identity_test.go internal/gateway/control.go` — no output.
- `git diff --check` — passed.
- `BROWSER=webkit node --test internal/gui/tests/identity-profiles.test.cjs` — 11 passed.
- `BROWSER=chromium node --test internal/gui/tests/identity-profiles.test.cjs` — 11 passed.

The browser tests covered English, Chinese, Traditional Chinese, Japanese and German translation states, as well as the provider editor identity summary.

## Verification not completed

- Full `go test ./internal/gateway` was not completed within the attempted timeout. The output showed long-running Claude subscription tests rather than a failure in the identity tests. The identity-focused gateway subset passes independently.
- Full repository acceptance commands were not run: `go vet ./...`, cross-OS vet/build commands, `go test -tags nogui ./...`, and the complete `make test-ui` suite.
- CI Test/Docker success for the final commit is not available yet.
- No real third-party account or live gateway was used; all control and upstream checks use local fixtures as required for this change.

Do not describe the full gateway suite or CI as green until those checks have completed on the final pushed head.

## Follow-up

1. Run the repository acceptance commands on the pushed commit, retaining the temporary-home setup for Go tests.
2. If the full gateway suite times out again, capture the last test name and investigate that baseline independently; do not attribute it to the identity GUI change without a reproducer.
3. Run the complete UI suite in Chromium and WebKit if release-level verification is required.
4. Wait for CI Test/Docker success before tagging or releasing.
