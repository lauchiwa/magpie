# Outbound identity profiles

An identity profile is a frozen, reusable snapshot of the client fingerprint a
provider's upstream recognizes. It is separate from `providers.json`: the
store is `identities.json` in the same config directory and binds a provider id
to a profile id. This separation is intentional. Provider records are changed
frequently and are synchronized with the upstream repository; identity state is
local policy and must not be merged into the provider schema.

## Responsibilities and boundaries

- `internal/identity` owns the profile contract, closed projection, validation,
  candidate/confirmed lifecycle, capture window and storage.
- `internal/provider/identity.go` is the only provider integration point for
  presenting a confirmed profile. It applies after the existing user
  `Headers`, so unbound provider precedence stays unchanged and the confirmed
  profile owns its closed identity fields.
- `internal/provider/models.go` adds the same profile to model-list requests.
  Connectivity tests already call `Provider.Sign`, so they use the same path.
- `internal/gateway/gateway.go` only observes headers when the gateway already
  recognized a Codex or Claude Code request. Observation is a one-shot,
  explicitly armed operation; the raw request is not stored.
- `internal/gateway/identity.go` arms, disarms and reports those windows for
  the gateway holding them. A window lives in the memory of the process that
  forwards the requests, so the CLI cannot hold one: `magpie identity arm`
  asks the running gateway over these paths. They answer this machine alone,
  with or without a gateway key, because arming is consent to keep the next
  matching request's identity fields and is granted where magpie runs, not by
  an agent holding a shared gateway's key.
- `internal/provider/identity_capture.go` maps the client ids this package
  recognizes a request by (`codex`, `claude-code`) to `identity.Client`
  (`codex_cli`, `claude_code`). The two vocabularies differ, so the mapping is
  explicit and an unknown client observes nothing.
- Signed-in accounts and plugin providers are excluded. Their own signers own
  the outbound identity, and a magpie-side binding would otherwise appear to
  succeed while never being sent.

A profile is not authentication. Recognition and projection validate only the
format of headers controlled by the caller. A profile contains no credential,
session id, thread id, request id, attestation, retry field or request beta.
The allowed fields are a closed set:

- Codex: `User-Agent`, derived `Version`, and `Originator`.
- Claude Code: `User-Agent`, `X-App`, and the documented install fields
  `X-Stainless-Lang`, `X-Stainless-Package-Version`, `X-Stainless-Os`,
  `X-Stainless-Arch`, `X-Stainless-Runtime`, and
  `X-Stainless-Runtime-Version`.

User-Agent, version and Codex originator are checked for internal consistency;
values are printable ASCII, trimmed, bounded and cannot name `magpie`. A
profile imported or captured from a request is a candidate. Only `confirm`
makes it usable, and binding requires a confirmed profile. A broken binding is
an error (fail-closed), not a silent fallback to magpie's own User-Agent.

## Lifecycle

1. `magpie identity arm <provider> <codex_cli|claude_code>` arms a five-minute,
   memory-only, one-shot capture on the running gateway
   (`IdentityArmPath`). The first matching request it forwards creates an
   unconfirmed candidate in `identities.json`. With no gateway running there is
   nothing to arm, and the command says so rather than reporting success.
2. `magpie identity ls` shows the candidate and its projected fields, and the
   windows armed on the gateway (`IdentityArmedPath`), which are otherwise
   invisible. The operator checks it, then runs
   `magpie identity confirm <profile-id>`.
3. `magpie identity bind <provider> <profile-id>` binds the confirmed snapshot.
   Forwarding, probes and model lists for that plain key+URL provider present
   it. The profile is sent on fallback attempts as each selected provider is
   signed.
4. `magpie identity unbind <provider>` stops presentation. `disarm`
   (`IdentityDisarmPath`) cancels an armed window. `rm` removes the profile and
   every binding to it.

A file import is available when the operator already has a request header dump:
`magpie identity import <client> <headers-file>`. It is always stored as an
unconfirmed `request_import` candidate; the file is projected through the same
closed allowlist and consistency checks.

## Compatibility and upstream synchronization

The implementation intentionally keeps identity data out of `Provider`,
`providers.json`, plugin contracts and GUI state. The integration with existing
code is limited to the stable `Sign` chokepoint, the model-list header funnel,
and the two existing client-observation calls. Existing providers remain
unchanged when unbound, existing user-header precedence remains unchanged for
unbound providers, and account/plugin requests do not read the identity store.
A confirmed profile owns its closed fields, so it cannot be mixed with an open
user header. This keeps future upstream merges focused on small, obvious
anchors rather than a long-lived fork of provider or gateway structures.

## Verification

- `internal/identity` tests cover the closed projection, consistency checks,
  candidate lifecycle, fail-closed store, copy isolation and one-shot capture.
- `internal/gateway/identity_test.go` asserts that a capture armed over the
  control path is held by the gateway and projects the request it then
  forwards, that disarm revokes it, that another machine is refused with and
  without a gateway key while `/v1/magpie/concurrency` still answers that key,
  and that a provider no profile applies to and an unknown client are refused.
- `internal/provider/identity_test.go` asserts the actual headers received by
  an `httptest` upstream for signing, model lists and endpoint tests; it also
  covers user-header precedence, broken bindings, accounts and unbound
  providers.
- Run affected tests with an isolated home and pinned Go caches, as described
  in [Provider and plugin ownership](provider-plugins.md#verification).
