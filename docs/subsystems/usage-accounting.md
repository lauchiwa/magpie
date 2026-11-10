# Usage accounting

## Active accounting

The active Usage ledger records requests in `usage.jsonl` through
[`usage.Append`](../../internal/usage/usage.go). Gateway rows hold usage and
source facts, not a frozen billing-policy snapshot. `OwnCost` is a reported
USD amount; `Credits` is a separate vendor-plan unit, not dollars.

[`Pricer` and `NewCoster`](../../internal/usage/ledger.go) derive estimates
from current tariffs and the record's timestamp. Price edits can revalue old
records. A known zero tariff is distinct from an unknown price. The ledger,
packed request blocks, summaries, heatmap, sessions, CSV and observers have
separate projection paths; changing only a visible row does not change them
all. [Gateway sessions](gateway-sessions.md) describes session ownership and
deduplication.

A reliably different served model is valued at that model's applicable user
price, then its list price; the requested model's price, even zero, cannot
substitute for an unknown served tariff. `priceModels` preserves the existing
wire, listed-name and vendor aliases and is shared with the dormant biller's
published reference. Reported `OwnCost` still wins for active spend, never for
published reference. This does not activate a persisted bill or quota policy.

[`budget`](../../internal/budget/budget.go) holds caller-key and issuer
reservations, accumulates eligible local calls and reconstructs windows from
the ledger. Its limits are estimates, not a guarantee about a vendor's final
invoice. Display filters and Usage clears do not erase budget spending.
`usage.Append` swallows a failed write, and observers run before it;
`usage.AppendErr` (fork) is the same write with its error. This is not an
atomic billing settlement protocol.

A window the budget can't read is unknown, never 0 used (fork, Billing D4).
When `usage.jsonl` fails to read with anything but "no log" (a stat or
open error, a path that isn't a file, a read error before EOF), the usage
index's `logSnapshot.err` is set (`log_index.go`, `log_read_fork.go`), and
`LogVersion` does not expose a known version for that failed read (including
a nil file-info result), so Sessions cannot reuse an unchanged empty result.
Then
`budget.read` returns the error and keeps nothing, so the next request
reads again. `All` gives each affected limit an `Unavailable` status with
zero available budget; known partial charges and pending estimates can
remain visible but are not a known total. `Binding` picks it over a spent one,
and `reserve` refuses every limited key, token caps included, with a
`Refusal` that unwraps to `gwbudget.ErrUsageUnread`. The gateway answers
that 503 `gateway_key_usage_unread`, X-Should-Retry false, and nothing is
sent. `/v1/magpie/limit` and `/v1/magpie/me` carry `unavailable`; the CLI
(`magpie gateway key limit`, `list`), the Gateway keys page, the key
holder's `/me` page and a Remote magpie's key card say the usage couldn't
be read instead of "0 of". A remote's unavailable answer is a failed read
there: the card keeps the last numbers it read, dated.
`TestKeyLimitRefusesWhileUsageUnread`, `TestKeyLimitRefusesWhileUsageUnreadable`,
`TestUnreadUsageIsNoZero`, `TestGatewayKeyLimitUnreadCLI`,
`TestRemoteMagpieKeyUsageUnread`, `gateway-key-limit-unread.test.cjs`,
`usage-remote-key-unread.test.cjs` and `key-page.test.cjs`'s unread case
fail without it.

A limited key's reservation and a call it couldn't record outlive the
process (fork, Billing D4, [`journal_fork.go`](../../internal/budget/journal_fork.go)).
`usage-holds.jsonl`, beside `usage.jsonl`, is written only for a limited
key's requests. `reserve` appends an `open` line (the key, each window's
start, the tokens and cost reserved there) before the request is sent; a
journal that can't be written refuses the request with a `Refusal` that
unwraps to `gwbudget.ErrUsageUnrecorded`, which the gateway answers 503
`gateway_key_usage_unrecorded`, nothing sent. The release writes `done`.
`keyLimited` reserves under an id of its own (`gwbudget.HoldLimiter`,
`reserveHeld` in `key_hold_fork.go`) and `appendUsage` writes it on the
call's records as `hold`. A call `usage.jsonl` refuses (`AppendErr`) is
kept whole as a `rec` line; if both writers fail, its `done` isn't written.
`budget.read` counts, besides `usage.jsonl`'s calls, the `rec` lines, and
every `open` with no `done`, no record naming its hold and not in flight
in this process, at its reservation: a request cut off by a crash, a kill
or a handover (`Status.Pending`). The journal is written without fsync,
so it outlives the process, not a power cut. Coherent legacy open/done pairs
may be compacted; unresolved, conflicting and version-2 evidence is retained,
not expired by age. Limits: a legacy cut-off request counts
at its estimate (body/4 plus the window's mean output), which can be
less than the vendor billed; the other gateway of a handover counts the
first's requests in flight at their estimate until their records are
read again; the MCP tool and web_fetch reservations name no hold, so one
cut off after its record was written also counts its estimate. The CLI,
the Gateway keys page and the key holder's page say how many are
pending. `TestCutOffRequestCountsAtItsReservation`,
`TestHeldRequestCountsOnce`, `TestIssuedRequestHeldInBoth`,
`TestUnwrittenCallStillCounts`, `TestUnrecordedReservationIsRefused`,
`TestKeyLimitCountsACutOffRequest`,
`TestKeyLimitRefusesWhileReservationsUnwritable`,
`TestGatewayKeyLimitPendingCLI`, `gateway-key-limit-pending.test.cjs`
and `key-page.test.cjs`'s cut-off case fail without it.

## Scoped admission integration (fork)

The in-progress sync candidate keeps request-level admission when neither
caller nor issuer has scoped limits. With scopes, `gwbudget.ScopedLimiter`
reserves each selected provider/model invocation in all applicable caller
and issuer windows atomically before sending. `attempt_fork.go` writes a v2
open set including principal, model aliases, scope definition and window
bounds. Whole and scoped windows sharing a start each hold the invocation
once, not once per redundant limit. A selected model with no applicable
cap never becomes dependent on an optional usage write.

The lease distinguishes never-sent, recorded, known-nonbillable and unknown.
There is currently no generic nonbillable proof adapter: HTTP errors, missing
tokens and transport failures after sending are unknown. A sent-unknown lease
keeps affected windows unavailable both live and after restart, even when a
pending estimate is small. A bare primary row is not completion evidence.
Replay requires the exact open set and a matching complete receipt; primary
occurrences retain their multiplicity and only proven journal mirrors are
deduplicated. Partial diagnostics retain known charges but cannot settle.
Orphan primary rows and orphan or contradictory diagnostic envelopes also
keep their eligible known charges, while affected totals remain unknown.
A malformed terminal cannot settle by carrying a row of its own; a v2 Hold
with a missing/downgraded version cannot be compacted as a legacy done pair.
The shared usage writer and its observers run outside the budget lock.

`BudgetTime` reads scoped admission time from the opaque Hold; visible
request Time remains unchanged. `VisitBudgetRead` uses a separate maximum
budget-time bound on each packed block, including disk metadata, so a
fallback admitted after midnight is not lost behind its earlier display
time. Malformed envelopes are visited rather than silently skipped. Legacy
rows retain their previous since-Time semantics. Warm sums also validate
the shared ledger version and the journal's file stamp after external writes.

The shared retrieval sender has scoped adapters for embeddings/rerank and
the speech/transcription/translation aliases. `audioBudgetReply` requires
both nonnegative input and output token counters (explicit zeros count),
with any stated total consistent with them. A complete bounded JSON reply
or speech's final complete `speech.audio.done` SSE frame supplies evidence;
binary audio, plain transcripts, duration-only usage and missing counters
do not. `copyAnswerTail` forwards and flushes without holding the reply,
retaining only the existing bounded tail. A stream cut preserves available
diagnostic counters but cannot settle. Counterless audio may answer once,
then its affected windows stay unknown: this is not vendor metering support.
Requests without scoped limits retain their previous integer-count path.

Each fallback acquires its own lease; an unknown prior attempt does not lend
its reservation to the next. An independent model outside that scope can
still be admitted, but a whole-key window or shared scope affected by the
unknown cannot. Hidden transport replay and redirects are not performed
inside a scoped invocation. Other counted scoped adapters (including chat,
native, Codex, drawing, video and model subcalls) remain integration blockers
and fail before dispatch, not a delivered replacement for upstream routing.
Journal v2 compaction is conservative and still needs bounded-growth
validation. These are candidate implementation contracts, not claims of
tested or completed upstream sync. D4 visibility, unreliable-price admission
and dormant Billing activation are not added here.

The budget tests include separate writer/reader test binaries with inherited
isolated test homes: a writer exits before lease cleanup, and the reader
uses only persisted evidence. This differs from the older `Forget`-based
same-process simulations. It covers process termination, not power loss or
simultaneous multi-process admission, and is evidence only when executed.

## Packed row contract (fork)

`packedRow` is exactly 160 bytes on supported 64-bit targets, asserted twice
in `row_shape_test.go`. Its six varied string IDs keep Hold separate from the
message ID and shared shapes; zones remain pointer-free IDs. All upstream
counters and full 64-bit Order/RouteID remain. Existing saturation bounds
still apply; values outside them are not arbitrarily lossless.

Compared with upstream's 152 bytes, direct row arrays cost 8 more bytes per
row (5.26%), or 6,000,000 bytes (about 5.72 MiB) at 750,000 rows. This is not
an RSS or compressed-size measurement. `rowChunk.add`, unpacking and
`within` use `unsafe.Sizeof` in byte accounting; `rowBuffer`, its four kept
slots and all row slice capacities inherit the new size. Other allocation
budgets remain unchanged. Disk codecs encode fields/varints, not raw structs;
the package-source fingerprint invalidates derived caches, which rebuild
without changing original JSONL history. Heap/archive/disk/mapping/spill,
shape-sharing, zone and full-Order tests cover this contract when executed.

## Dormant accounting component (fork)

[`billing_fork.go`](../../internal/usage/billing_fork.go) bills one call
under its provider's billing mode, beside its list price. Nothing in
production calls it: it does not change `Record`, `OwnCost`, `Append`,
`Pricer`, budgets, the UI, the CLI, observers, sync or backup, and it has no
file, timer or network request of its own.

`NewBiller` reads the ledger's prices once, through `NewCoster`, and returns
a function of a record, the `provider.BillingDecision` `ResolveBilling` made
for it, and the provider's quota state when the call was made (`""`,
`"exhausted"` or `"unknown"`). It returns a `CallBilling`:

- `Charge` is what the call costs the user. It is the ledger's cost for a
  call the decision doesn't make free, `0` for a free or within-quota call,
  and the ledger's cost again, with `Paid` set, for a call past a quota the
  provider charges for then (`QuotaPaid`). A call to such a provider in an
  unknown quota state has no charge: it may have been charged or not. A
  model with its own price set (source `model`) is charged that price
  whatever the quota, and one the vendor serves free
  (`AllowanceIndependent`) stays free past the quota.
- `List` is the call at its list price: the price the provider publishes
  (`provider.PublishedPrice`, `ListSource` `"provider"`), else its maker's
  (`provider.MakerPrice`, `"maker"`), at the call's hour when the provider
  bills peak hours double (`PeakOf`). A price the user set, a price rate and
  a rate window never change it; they change the charge. A provider gone
  since lists at its maker's price, and a call made before its provider was
  deleted is listed as that provider's, as the ledger prices it.
- A nil amount is unknown, never zero. JSON writes a charge of `0` and
  leaves an unknown one out.

Because the charge comes from `NewCoster`, it is the ledger's cost of the
same record by construction, peak hours and holidays included. The decision
and the quota state come from the caller; the biller reads no quota store
and infers no mode from today's provider for an old call.

`BillingTotals` sums `CallBilling`s. `Charge` and `List` are each summed
over the calls they are known for, with counts of the known and the
unknown. `Saved` is list minus charge over the calls both are known for
(`Paired`), so it is negative where a provider charged more than the list,
and it is no net saving after a subscription fee.

## Integration gates

None of the following is done by adding the dormant biller:

- Computing a decision and a quota state at call time, for every attempt,
  retries and charged failures included, without reading today's provider
  for an old call.
- Storing the result with the call. Historical calls have no billing record
  and must not be given one from today's settings.
- Budgets, rows, summaries, packed blocks, CSV, the heatmap, sessions, OTLP
  and the middleware Usage hook. Each has its own projection path, and none
  of them reads `CallBilling` yet.
- UI and CLI controls, and sync and backup across versions.

Each needs its own reviewed runtime change and a test of the whole path
before activation or a release.

## Verification

Run `go test ./internal/usage/ -run 'TestBill|TestCallBilling|TestDormantBill'`
and the provider billing and price tests in the repository's isolated test
environment. Break checks must turn a test red for: a list from `ListPrice`
(maker's price labelled the provider's), a list without peak hours, a list
from a reported cost, a paid-past-quota call left free, an unknown quota
state charged nothing, the served model's list skipped, a deleted
provider's call listed as its successor's, an allowance-independent model
charged past the quota, a free call left out of the savings, and a
production file naming the biller (`TestDormantBillerHasNoProductionConsumer`).
`TestDormantBillingDoesNotRepriceHistory` checks that setting a mode doesn't
change what the ledger shows for an old call.
