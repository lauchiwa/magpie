# Error Handling Guidelines

## Error model

Use standard Go errors. Add context with `fmt.Errorf("...: %w", err)` when callers need to identify the failed operation while retaining `errors.Is`/`errors.As` behavior. Use sentinel errors sparingly for stable conditions and typed errors when callers (especially the GUI) need structured fields or behavior. Examples include `NoModelsError` and `RuntimeUnavailableError` in `internal/agent`, and `SignedInError`/`ShareError` in `internal/provider`. The lifecycle write API returns `*StaleError` (`errors.Is(err, ErrStale)`: reread, then retry), `*ConflictError`, and `*FollowUpError{Committed: true, Step}`. A FollowUpError means the main write already landed: redo only `Step`, never the whole operation. A sync or restore conflict is a `*ConflictError` that may be wrapped (`bringing in the providers: …`), so classify it with `errors.As`. A sync collects again and decides again once. A user-started restore returns the conflict to the user instead of retrying. A rename whose journal is pending returns its original error, and the next Recover finishes it. The caller must not replay that rename. It is `errors.Is(err, ErrPending)` and never `ErrStale`. A caller outside `provider` tells the three apart only with `errors.Is(ErrPending)`, `errors.Is(ErrStale)` and `errors.As(&*FollowUpError)`, in that order. To redo the step it calls `FollowUpError.Redo()`, which does `Step` alone. A step after the commit that belongs to another package says so with `provider.FollowUp(step, err, redo)`, for example `agent.StepAgents`. When two steps fail they join into one FollowUpError whose Redo does both.

Error messages should describe the actual failure and, when useful, what the user can do next. GUI validation copy commonly starts with a capital letter (`internal/access/access.go`); internal and CLI errors often use lowercase `subject: detail`. Vendor failures should identify the vendor or endpoint. Preserve distinctions between failure cases rather than collapsing them into a generic error.

```go
return fmt.Errorf("cannot update settings.json; the key is unchanged: %w", err)
```

Examples: `internal/access/access.go`, `internal/agent/agent.go`, `internal/catalog/live.go`, `internal/provider/logins.go`.

## Propagation and handling

Return errors to the layer that can make the right decision. Do not log an error and then discard it unless the operation is explicitly best-effort; in that case log the failure and state what Magpie kept or did instead. Use `errors.Is`/`errors.As` to classify wrapped errors; do not compare error strings. Use `errors.Join` when cleanup and primary-operation failures both matter (`internal/gateway/archive.go`).

Use `panic` only for impossible programmer states or invalid embedded build-time data, not for request, filesystem, or vendor failures. `recover` is narrowly applied at unreliable parser or platform boundaries; ordinary failures use returned errors.

## CLI

Command handlers return errors to `run`/`main` instead of printing and exiting deep in a helper. `main.go` prints `magpie: <message>` to stderr and exits nonzero. Use `exitError` in `quota_wait.go` when a command needs a distinct exit code. Follow adjacent CLI output conventions for progress and results. Provider and identity writes go through `lifecycleSaid` in `Main` (`internal/cli/lifecycle_cli.go`). A stale refusal says to run the command again. A pending one says not to. Nothing retries on its own. A committed FollowUpError is said, and its Redo runs once. If the redo fails again the command exits nonzero, says the main write is in, and never replays it.

## Gateway HTTP API

Use `writeError` in `internal/gateway/gateway.go` so the response matches the caller's protocol (OpenAI, Anthropic, or Gemini). Choose status according to the precise condition: malformed request (400), missing/invalid gateway key (401), forbidden key/model (403), unknown model (404), limit/refusal (429), upstream failure (usually 502), paused/unavailable service (503). Preserve an upstream status/body when the existing integration contract calls for it. Once streaming has begun, use the stream error path (`streamError`/`failTo`) rather than attempting to replace already-sent headers.

Error messages are user-visible to clients and often copied into usage records. Never include credentials or full secrets. `refusedKey` in `internal/gateway/lan.go` is a useful example: it distinguishes a missing key from a wrong one while only showing a sufficiently long key's final four characters.

## GUI JSON API

Use `writeJSON` for successful values and `fail` in `internal/gui/api.go` for errors. The current `fail` format is `{"error": ...}` with typed errors mapped to a stable `code` and relevant fields, allowing `app.js` to localize the message. If a new error needs special UI treatment, add a typed error mapping in `fail` and handle its code in the frontend; do not parse English error text to infer behavior. Provider and identity writes add their lifecycle code in `fail` through `lifecycleFail` (`internal/gui/lifecycle_fork.go`), checked in this order: `errors.Is(ErrPending)` → `"pending"` (finished by the next write or start; offer no retry), `errors.Is(ErrStale)` → `"stale"` (re-read, keep the draft), `errors.As(*FollowUpError)` → `"follow_up"`. A committed FollowUpError also sets `step` and `committed:"1"`. All stay 400, and other codes in `fail` are unchanged.

## Common mistakes

- Do not swallow an error from a state-changing operation and report success.
- Do not turn distinct vendor/status/content errors into one broad fallback or retry condition.
- Do not expose full API keys, tokens, authorization headers, or secret-bearing request bodies in errors.
- Do not use `http.Error` for protocol API errors where a protocol-shaped JSON response is required.
- Do not panic/recover as ordinary error control flow.
- Keep references to functions/symbols, not source line numbers.
