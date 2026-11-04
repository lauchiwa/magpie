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

## Dormant accounting component (fork)

The explicit-input library in
[`billing_types_fork.go`](../../internal/usage/billing_types_fork.go),
[`billing_fork.go`](../../internal/usage/billing_fork.go) and
[`billing_codec_fork.go`](../../internal/usage/billing_codec_fork.go) is not
connected to the active paths above. It does not change `Record`, `OwnCost`,
`Append`, `Pricer`, live budgets, UI, CLI, observers, sync or backup. It has no
startup initialization, file writer, current-state lookup, timer or network
request. There is no activation flag and no model probe.

The component's ownership boundary is:

1. `CaptureBilling` validates and copies explicit attempt-time policy,
   identity, price and quota inputs into a private context. Caller inputs
   must not be mutated concurrently while capture runs.
2. `Seal` combines that held context with matching completion usage and
   evidence. Later changes to the original input do not change the result.
   Repeated construction is not a durable commit or an exactly-once guarantee.
3. `Amounts` and the legacy projection preserve monetary dimensions and
   their knownness separately. Totals fold the same projections without
   treating missing values as zero.
4. `EncodeBilling` and `DecodeBilling` exchange a strict versioned envelope.
   These are byte transformations, not proof that bytes were durably saved.

The caller supplies an opaque attempt identity and the complete authority,
provider incarnation, principal incarnation and evidence generation. A
display name, key fingerprint or the route ID shared by retries is not that
identity. The library validates supplied relationships, not their real-world
authenticity. A production identity and evidence owner is still required.

### Monetary meaning

The envelope distinguishes estimated spend, applicable reported USD,
reference value, signed paired difference and vendor credits. Reports never
become estimates or independently verified invoices. Missing usage is not a
known zero-token call, and a zero-token report can still report nonzero USD.

An exact model override precedes the inherited policy. A quota model's free
classification is not proof that an attempt was included. Included and paid
outcomes require separate attempt-bound evidence. Actual paid evidence is
retained even when permission was absent; recording it does not authorize
dispatch. Conflicting report and zero-charge evidence remain visible, with
unknown selected charge rather than a silent zero.

Reference inputs identify a provider's published pay-as-you-go price, or an
explicitly matched maker reference when that provider reference is absent.
User free settings, discounts and markups are not a published reference.
Published zero remains zero; unavailable references stay unknown. The pure
library accepts those normalized inputs; it does not discover prices or
certify a provider's claim.

New envelopes retain their original rates, resolved time/rate factors,
effective tariff, selected tier/components and amounts. Historical decoding
checks that chain under the stored arithmetic version, not today's tariff
or holiday calendar. Hour-cache fallback and strict tier thresholds are
part of that version. Overflow and inconsistent inputs are errors. Supplied
timestamps must remain JSON-representable after UTC normalization as well as
in their original offset; capture must not produce an unencodable envelope.

Difference is reference minus selected charge only for comparable known
pairs. A negative difference is a premium, not zero savings. Summing total
reference and total spend from different populations cannot establish total
savings. Totals retain source and unknown coverage; exact internal sums are
rounded only when projected. `BillingAmounts.Values` keeps dimension, origin,
source, unit and authority with known/unknown counts, rather than one total
that silently joins different populations. `BillingTotals.Merge` merges the
exact accumulators; adding rounded projections cannot provide that guarantee.
Subscription fees are not silently allocated,
so the difference does not claim net cash savings after a subscription fee.

Legacy projections accept an explicitly supplied current-price estimate and
reported facts. They never infer an old billing mode or quota from today's
provider. They do not create historical savings. Invalid or unsupported new
envelopes cannot fall back to the legacy path.

### Quota evidence and historical limits

[`CloneQuotaSnapshot`](../../internal/provider/quota_snapshot_fork.go)
checks and copies a supplied materialized scope view, complete declared
coverage and its saved evaluation instant. It shares provider-owned rules
with [dormant quota state](providers-accounts.md#dormant-quota-state-fork).
All applicable windows must be represented, including unknown ones; one
exhausted window cannot be selected away. Expiry does not establish recovery.

This checks consistency relative to supplied coverage. It cannot certify
coverage authenticity, recover omitted update timestamps, replay a cleared
blocker or reconstruct arbitrary old views from later data. No store fields
are invented to claim those guarantees. No-record and unavailable context
remain explicit rather than fabricated snapshot windows. Availability
before an attempt does not prove that the vendor charged it to free quota.

### Serialization and errors

The wire format uses explicit fields, required zero values and distinct
omitted optional evidence. It rejects ambiguous JSON, unknown fields in a
supported schema, nulls, missing fields and contradictory monetary facts.
An unsupported version is distinct from corruption; neither returns a usable
partial envelope. Payload limits and error categories belong to the owner.

[`strictjson`](../../internal/strictjson/strictjson_fork.go) shares only
syntax/member/shape mechanics with the quota store. Quota keeps its existing
version/error precedence and gains no billing-specific size limit. Billing's
method-free price and quota wire types avoid permissive catalogue decoding.
Strict validation is not cryptographic authentication.

## Integration gates

None of the following is implemented merely by adding the dormant library:

- Capture before every actual attempt, including charged failures and
  retries; bind live provider/account incarnations and trusted price/evidence
  sources without hidden current-state reads.
- Durable intents, acknowledged settlement, incomplete-history errors and
  pending-liability recovery. A post-dispatch write failure cannot undo a
  request, justify a blind retry or release uncertainty as zero.
- Coherent caller/issuer admission, reservations, settlement and restart.
  Legacy current-price estimates must revalue together while frozen new-call
  amounts remain fixed. Revaluation must not discard active holds. Unknown
  accounting for an affected monetary-limited key must not permit spending
  against a fabricated zero; optional logging failures alone must not stop
  otherwise unlimited requests.
- Rows, summaries, packed blocks, CSV, heatmap and session projections with
  source/knownness and cache-version compatibility. Filters must not change
  the authoritative budget population.
- OTLP and watched-attempt paths, plus the middleware Usage hook's queued
  records and cached coster. No old current-price cache may rewrite frozen
  facts. Allowance-window list-value estimates must not become spend by a
  blanket replacement of cost lookups.
- Mixed-version sync/backup and UI/CLI controls that preserve or explicitly
  reject supported evidence instead of silently dropping it. No history
  backfill from a local installation date.

These require their own reviewed runtime changes and whole-path verification
before activation or release. Component tests do not prove live durability,
vendor recovery, strict invoice caps or large-history performance.

## Verification

Run the provider quota and billing tests, the Usage billing tests and the
strict-JSON tests under the repository's isolated test environment. Mutation
checks must fail by assertion for changed rates plus amounts, omitted
coverage, reset-as-recovery, shared input mutation, unknown-as-zero, unpaired
savings, permissive decoding and a production consumer.

The inactivity guards cover the closed set of domain files, including
private wrappers, method values and methods on live receiver types. The
Usage library may share quota data and its pure checked-copy helper, not
access the quota store. Existing runtime inactivity tests remain necessary.
Run race checks on supported runners; report actual results and unrun
checks separately from this contract.
