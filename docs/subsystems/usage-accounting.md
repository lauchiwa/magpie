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

[`budget`](../../internal/budget/budget.go) holds caller-key and issuer
reservations, accumulates eligible local calls and reconstructs windows from
the ledger. Its limits are estimates, not a guarantee about a vendor's final
invoice. Display filters and Usage clears do not erase budget spending.
`Append` currently has no durable success result: observers run before its
best-effort write. This is not an atomic billing settlement protocol.

A window the budget can't read is unknown, never 0 used (fork, Billing D4).
When `usage.jsonl` fails to read with anything but "no log" (a stat or
open error, a path that isn't a file, a read error before EOF), the usage
index's `logSnapshot.err` is set (`log_index.go`, `log_read_fork.go`), and
`budget.read` returns the error and keeps nothing, so the next request
reads again. `All` gives each limit in that window an `Unavailable` status
with its counts and what is left 0, `Binding` picks it over any spent one,
and `reserve` refuses every limited key, token caps included, with a
`Refusal` that unwraps to `gwbudget.ErrUsageUnread`. The gateway answers
that 503 `gateway_key_usage_unread`, X-Should-Retry false, and nothing is
sent. `/v1/magpie/limit` and `/v1/magpie/me` carry `unavailable`; the CLI
(`magpie gateway key limit`, `list`), the Gateway keys page, the key
holder's `/me` page and a Remote magpie's key card say the usage couldn't
be read instead of "0 of". A remote's unavailable answer is a failed read
there: the card keeps the last numbers it read, dated. Not covered yet: a
failed write of a record (its call counts only in the sums this process
holds, and is gone from the window once it is read again from the file),
and reservations held across a restart.
`TestKeyLimitRefusesWhileUsageUnread`, `TestKeyLimitRefusesWhileUsageUnreadable`,
`TestUnreadUsageIsNoZero`, `TestGatewayKeyLimitUnreadCLI`,
`TestRemoteMagpieKeyUsageUnread`, `gateway-key-limit-unread.test.cjs`,
`usage-remote-key-unread.test.cjs` and `key-page.test.cjs`'s unread case
fail without it.

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
