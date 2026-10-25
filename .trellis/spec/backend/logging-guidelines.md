# Logging Guidelines

## Logger and format

The project uses only the standard library `log` package. It writes to stderr with the default timestamp flags; there is no structured logger, log file, or formal level system. Keep the established lowercase subsystem-prefixed style:

```go
log.Printf("catalog: models not refreshed: %v; keeping the cached catalog", err)
log.Printf("refused %s %s from %s: %s", r.Method, r.URL.Path, r.RemoteAddr, msg)
```

Representative code is in `internal/catalog/fresh.go`, `internal/provider/logins.go`, `internal/gateway/lan.go`, and `internal/gateway/cors.go`.

## What to log

Log failures of background or best-effort work when Magpie continues with a safe fallback, state transitions a user may need to understand, and refused or security-relevant requests. Include the subsystem, operation, consequence, and wrapped error. `MAGPIE_DEBUG` gates verbose gateway request diagnostics; do not add ad-hoc always-on request-body logging.

The gateway's user-visible request history is separate from process logs: `Server.record` keeps recent calls in memory and `internal/usage` appends bounded records to `usage.jsonl`. Keep bodies out of ordinary usage records; messages are normalized and bounded by existing helpers.

## Secrets and privacy

Never log API keys, OAuth tokens, passwords, authorization headers, cookies, or complete vendor request/response bodies. Identify accounts by their user/email label, never by a credential. Use `provider.Mask` for displayable provider keys (first/last four characters or the existing fully masked form). Gateway key errors follow `refusedKey` in `internal/gateway/lan.go`, which reveals at most the last four characters and only for sufficiently long keys.

`internal/redact` protects values in upstream request/response transformations; it is not a license to put the unmasked values in logs. Config previews use the existing secret masking behavior in `internal/agent/preview.go`.

## Testing logs

Tests that capture logs must redirect `log.SetOutput` temporarily and restore it with cleanup. See `internal/gateway/caller_regression_test.go` and `internal/provider/antigravity_tier_test.go`. Assertions should check the meaningful event and redaction, not timestamps.

## Anti-patterns

- Do not introduce `slog`, third-party logging, or a new log sink for a local feature without an explicit architecture decision.
- Do not log at every successful request or dump request bodies by default.
- Do not use logs as the only user-facing error path when the caller needs a returned CLI/HTTP/GUI error.
- Do not claim that `internal/redact` redacts process logs.
