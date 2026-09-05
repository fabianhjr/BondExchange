# ADR-0014: Persist and coordinate Banxico SIE exchange rates

- Status: Accepted
- Date: 2026-09-04

ADR-0017 changed durable import and observation identities to UUIDv7. ADR-0018
retains the observation's original bigint as `revision_sequence` and removes
the redundant bigint import identity. The revision-ordering decision below
remains accepted.

## Context

Exchange-rate consumers need current and historical observations from Banco de
México's SIE REST API. The API requires a reusable token and imposes separate
short-window and daily request limits. Repeating the same request for every
caller would waste quota, make availability depend directly on SIE, and allow
multiple stateless server instances to create a request stampede.

Historical observations are expected to be stable, but corrections remain
possible. Replacing a value in place would hide the fact that this service once
observed another value. A successful query can also return no observation for
a weekend or holiday, so stored rows alone do not prove that a period was
queried.

## Decision

Define a provider-neutral exchange-rate application module and a separate SIE
HTTP adapter. Callers map canonical SIE series IDs explicitly to base and quote
currencies. Values remain exact decimals from HTTP parsing through PostgreSQL;
human-readable provider titles never determine quote direction.

Support on-demand latest and date-range requests. Normalize historical ranges
into calendar-month work units per series and explicit currency mapping, and
batch no more than 20 series with the same period into an upstream request. A
successfully queried closed month is permanently covered, including when it
contains no observations. The current partial month and latest observations
use a 15-minute default freshness period. An explicit revalidation operation
can query covered history again.

Persist every successful, bounded, sanitized JSON response as an append-only
import with its request scope and digest. Persist normalized observations as
append-only rows. The importing transaction briefly serializes each
series/currency/date coordinate and omits a value equal to its current
revision; a changed value, including a return to an older value, appends a new
revision. A view derives the current revision by its serialized observation
revision sequence
while retaining the complete observed history, avoiding transaction-start
timestamps as a concurrency order.

Coordinate fetches with mutable PostgreSQL rows keyed by the normalized work
unit, including its explicit currency mapping. A short lease is committed
before the network call, which occurs without an open database transaction.
Lease ownership guards completion and failure updates. Latest callers may use
stale stored data while another instance refreshes; cold and historical
callers wait within their context. Persist the provider-wide reset deadline
reported by SIE so rate limiting applies across instances and series.

Use the fixed Banxico HTTPS origin, a five-second default HTTP timeout, a 1 MiB
response limit, same-origin redirects, and the `Bmx-Token` request header. The
token is never placed in URLs, persistent data, recordings, errors, or logs.

Keep ordinary tests offline. A repository-owned recorder can explicitly make
one latest and one fixed historical request when a contributor supplies
`BANXICO_SIE_TOKEN`. It writes only after replacing the token with
`<REDACTED>`, selecting safe response headers, and verifying that the body does
not contain the credential. A documentation-derived example fixture remains
separately labeled and must not be presented as a live capture.

Do not expose rates through the current protobuf API or use them to reprice an
offer in this decision. Therefore the transport contract and TLA+ domain model
do not change.

## Consequences

- Closed history remains available after cache expiry, restart, or an SIE
  outage, and empty successful periods are distinguishable from missing work.
- Corrections are visible without destructive mutation, at the cost of durable
  import and revision growth.
- Identical and overlapping requests share work across processes. Exactly one
  upstream request cannot be guaranteed if a process stops after receiving a
  response but before committing it; a later lease owner must retry.
- Stale latest data is explicit in the returned model rather than silently
  presented as fresh.
- The SIE token, its delivery, rotation, and production egress policy remain
  deployment responsibilities.
- The module has no scheduled refresh worker. Data changes only when a caller
  requests it or explicitly revalidates a range.

## Alternatives considered

### Use an in-process cache and singleflight

This would reduce duplicate work in one process but would not coordinate
multiple stateless instances or survive restarts.

### Store only the latest value for each series

This minimizes storage but loses historical availability, response provenance,
and correction history.

### Treat historical values as immutable by natural key

Rejecting a changed value would either make a provider correction unavailable
or require destructive repair. Append-only revisions retain both facts.

### Hold a PostgreSQL advisory lock during the HTTP request

This serializes callers but occupies a pooled database connection across an
untrusted network delay. Committed leases provide bounded recovery without
holding a transaction or connection.

### Refresh rates on a background schedule

No current exchange operation consumes rates and no freshness service-level
objective exists. On-demand fetching keeps the first implementation bounded;
scheduled ownership, monitoring, and retry policy require a later decision.
