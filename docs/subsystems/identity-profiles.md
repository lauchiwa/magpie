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
- `internal/gui/identity.go` serves the page's side. It reads and writes the
  same store and asks the same gateway control paths the CLI asks, so neither
  surface holds a window or an applicability rule of its own. Every change
  answers with the whole state, so the page never stitches one together from a
  guess while another refresh is in flight. Only the closed projection
  (`Fingerprint.Header`) is sent to the page, so no raw observed header can
  ride along.

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

`Sign` reads the store for every plain provider's request, bound or not, so
an `identities.json` that can't be read would stop them all. One a crash left
all zero or cut short is read from its last good generation
(`internal/lastgood`): the file as it was before its last write, which loses
that one change, and the GUI's recovery notice says so. Its next change keeps
the bad file aside as `identities.json.bad-<time>`. With no good generation
either, the store stays unknown and every plain provider's request fails with
the file named, rather than reading it as "nothing bound". An empty file is
no profiles.

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

## Provider lifecycle state

A binding is keyed by provider id, and an id can be reused. Optional
`lifecycle.json` (`internal/lifecycle`) gives each stored provider row a stable
instance, so a request signed for a value read before a rename, delete or
re-create uses that same provider's binding and never another provider's.
The write side is in
[`lifecycle_commit.go`](../../internal/provider/lifecycle_commit.go), the
signing side in
[`lifecycle_fork.go`](../../internal/provider/lifecycle_fork.go), and start-up
and reconcile in
[`lifecycle_ready.go`](../../internal/provider/lifecycle_ready.go).

- **Created at start-up only.** `provider.ReadyLifecycle` takes the lifecycle
  lock (waiting at most 3s), finishes a journal a dead writer left, then:
  - with no metadata, creates it from `providers.json` (new epoch, each id a
    new instance at rev 1);
  - with corrupt metadata, keeps it as `lifecycle.json.bad-<time>` and creates
    new metadata, so values read before become stale once;
  - with metadata from a newer magpie or one it can't read, leaves the file as
    it is and returns the error;
  - with no config folder, a folder it can't write, or the lock held
    elsewhere, stays legacy and returns no error; in a folder it can't
    write, a change made outside magpie is left unadopted (signing projects
    it, as below) and no error is returned either
    (`TestReadyLifecycleInAFolderItCantWrite`).

  It creates no metadata while `providers.json` is missing or is being read
  from its `.bak`. Ordinary reads and writes never create metadata, so a home
  without it is legacy and matches bindings by current id.
- **Changes made outside magpie.** An older magpie, an editor, or a
  `providers.json` read from its `.bak` leaves the file's digest different
  from the metadata. `ReadyLifecycle` adopts the change under the lock, and so
  does the next provider write; adoption writes `lifecycle.json` only. An id
  that is still present keeps its instance, and its rev goes up only if its row
  changed (the metadata records each row's digest). An unbound id that was
  added gets a new instance; an unbound id that was removed gets a tombstone.
  A bound id that was removed, and a new id that an orphan binding already
  names, are marked `unreconciled`.
- **Signing while state is undecided.** Signing never writes. Before anything
  adopts the change, signing applies the same rule to what it read. Only an
  unreconciled id that is still bound fails, with `ErrUnknown`; every other
  provider signs as before. A bound provider whose row was only edited
  outside magpie keeps its profile.
- **`magpie identity reconcile`.** With no arguments it prints each id's
  instance and the undecided bindings, and writes nothing: no lock file, no
  recovery and no copy. `--yes` takes the lock, then:
  - keeps a journal it can't finish as
    `lifecycle-journal.json.reconciled-<time>` and removes it;
  - keeps the old metadata as `lifecycle.json.reconciled-<time>`;
  - adopts the change and clears the undecided marks.

  If any binding is undecided, `--yes` also needs `--keep` (leave those
  bindings) or `--drop` (unbind them); with neither it writes nothing. Unknown
  arguments and `--help` write nothing. It won't settle a `providers.json` that
  is missing or being read from its `.bak`.
- **Legacy signing may write once.** Signing a provider with no lifecycle
  marker (legacy) is lock-free unless a journal is pending. A pending journal
  means a writer died part way through, and that request takes the lock and
  finishes the journal (`lifecycle.Begin` in writable mode). This is the one
  case where a request writes, and it happens once per abandoned journal.
- **Not covered.**
  - An id deleted and re-added outside magpie with the same bytes in between
    can't be detected.
  - Recovery from an identity store `.bak` can still restore the latest unbind
    or delete (recovery A). The instance mapping adds no other way back:
    `TestRecoveryAAddsNoOtherWayBack`.

## The page

The GUI does the same four steps on the Providers page, under an **Identity
profiles** fold below the provider list (`#identityHead`, `#identityPanel` in
`index.html`; `renderIdentityProfiles` in `app.js`). It is not a top-level
page: a profile is a property of a provider, and the Providers page is where a
provider is already managed. The fold starts folded and is remembered per
viewer.

| Path | What it does |
| --- | --- |
| `GET /api/identity` | profiles, bindings, per-provider applicability, armed windows, whether a gateway runs, and the capture window's length |
| `POST /api/identity/arm` | asks the running gateway for a one-shot window |
| `POST /api/identity/disarm` | cancels one |
| `POST /api/identity/confirm` | makes a candidate usable |
| `POST /api/identity/bind`, `/unbind` | starts and stops presentation |
| `POST /api/identity/remove` | removes a profile and every binding to it |

What the panel will not do:

- It never claims a state it does not have. A body it cannot read is neither
  "no profiles" nor "none bound": the fold is not offered, and a read that
  failed is said. An `armedWhy` from a gateway that could not be asked lists
  the profiles and says the windows are unknown, rather than showing none.
- It invents no countdown. An older gateway answers `IdentityArmedPath` with
  provider ids alone, and such a window is shown armed with its client
  unavailable and no expiry; only an `expiresAt` the gateway gave runs down.
- It offers no action that would not be sent. Capture is disabled with no
  gateway, a candidate offers confirm and remove but never bind, and a
  provider `IdentityApplies` rejects is listed with its reason instead of
  being bindable. The API checks applicability again on every bind, so the
  page's own list is not the guard.
- It shows no credential, session, thread, request or window id, and no raw
  observed request. Removal asks first and says how many providers it unbinds.

A provider's editor has an **Identity** field of its own (`identityField` in
`app.js`), between Headers and Proxy: what this provider presents, an Unbind,
and a way to the panel. It is a summary, not a second editor. The binding is
not part of the provider's draft or its save: it is read from the identity
state and changed through `/api/identity/{bind,unbind}` alone, so an unbind
leaves an unsaved draft open and untouched, and a provider save carries no
profile field. A provider `IdentityApplies` rejects shows that reason and
offers no action.

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
  candidate lifecycle, fail-closed store, copy isolation and one-shot capture,
  and a zeroed store read from its last good generation, one with none that
  fails closed, and an empty one. `TestAZeroedIdentityStoreLeavesProvidersSigning`
  (`internal/provider`) signs an unbound provider over a zeroed real store.
- `internal/gateway/identity_test.go` asserts that a capture armed over the
  control path is held by the gateway and projects the request it then
  forwards, that disarm revokes it, that another machine is refused with and
  without a gateway key while `/v1/magpie/concurrency` still answers that key,
  and that a provider no profile applies to and an unknown client are refused.
- `internal/provider/identity_test.go` asserts the actual headers received by
  an `httptest` upstream for signing, model lists and endpoint tests; it also
  covers user-header precedence, broken bindings, accounts and unbound
  providers.
- `internal/gui/identity_test.go` covers the empty state, confirming a
  candidate, binding and unbinding, removal cascading to bindings, the
  sensitive fields left out, a provider no profile applies to, a gateway that
  is down, an older gateway without window details, a failed armed read and
  fail-closed behaviour. Its real-control tests send arm, disarm and armed
  reads through `gateway.ArmIdentity`, `DisarmIdentity` and
  `ArmedIdentities` to a local fake gateway, checking the returned client and
  expiry, refused actions and bounded calls that receive no gateway response.
- `internal/gui/tests/identity-profiles.test.cjs` drives the page in Chromium
  and WebKit: the empty state, a candidate that only confirms, a confirmed
  profile bound and unbound, the countdown running down, a removal that says
  what it unbinds and sends nothing on Cancel, no gateway, an older gateway's
  id-only window, a provider that cannot present a profile, a refused action
  shown without changing the list, and no leaked header. English and Chinese
  for the behaviour, all five languages for the strings, and 420px wide for
  the layout. The same file covers the editor's Identity field: the profile it
  names, an unbind that leaves an unsaved draft open and saves no provider,
  the way to the panel, and an inapplicable provider's reason with no action.
- [`lifecycle_ready_test.go`](../../internal/provider/lifecycle_ready_test.go)
  covers `ReadyLifecycle` in each metadata state, an adoption that marks only
  undecided bindings, a write adopting an outside change or a `.bak` read,
  recovery A combined with renames, deletes and tombstones, and reconcile's
  preview, `--keep`, `--drop` and refusals.
  [`lifecycle_process_test.go`](../../internal/provider/lifecycle_process_test.go)
  signs values held by this process after another process renames, deletes,
  re-creates and binds. `TestIdentityReconcileCmd` (`internal/cli`) checks
  that the preview, unknown flags and `--help` write nothing.
- Run affected tests with an isolated home and pinned Go caches, as described
  in [Provider and plugin ownership](provider-plugins.md#verification).
