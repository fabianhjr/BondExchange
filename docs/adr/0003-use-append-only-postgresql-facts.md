# ADR-0003: Use append-only PostgreSQL facts for offers and purchases

- Status: Accepted
- Date: 2026-09-01

ADR-0017 amends the physical identity mapping to UUIDv7 primary keys and a
separate unique purchase `sale_offer_uuid`. The append-only and PostgreSQL
concurrency decisions remain accepted.

## Context

The Go server must be safe when several instances handle attempts to buy the
same sale offer concurrently. A lock held by one server process cannot
coordinate the others, so concurrency must be resolved by shared durable
state.

The domain distinguishes active sale offers from binding order/reservation
facts. Keeping the facts that an offer existed and was reserved is useful for audit and
diagnosis, while destructive mutation would discard that history.

The TLA+ specification remains an implementation-independent description of
active offers, purchase facts, and their transitions. This record owns
the mapping between that abstract state and its PostgreSQL representation.

## Decision

Use PostgreSQL as the single logical concurrency authority for stateless Go
server instances. PostgreSQL may itself use a highly available deployment,
but all writers must observe the same uniqueness constraints.

Represent domain history with immutable facts:

- users, bonds, and sale offers are inserted and not changed in place;
- buying appends a purchase that relates one existing offer to one existing
  buyer; and
- a unique constraint on the purchase's offer ID permits at most one purchase
  for each offer, including when different server instances race.

The runtime database identity receives only the `SELECT` and `INSERT`
privileges required by the application. The schema also rejects updates,
deletes, and truncation of domain-fact tables as defense in depth. Schema
migrations and exceptional recovery use a separately controlled owner.

Expose active offers through a non-materialized view (`active_offers_v2` in
the UUID schema). It
contains sale offers for which no purchase exists. The refinement mapping is:

```text
TLA+ saleOffers = rows visible through active_offers_v2
TLA+ purchases  = purchase facts joined to their immutable sale offers
```

The abstract `Buy(buyer, offerId)` transition corresponds to atomically
inserting one purchase fact. It does not require the formal specification to
describe tables, SQL, transactions, views, indexes, or deployment topology.

Use indexes that support enforced uniqueness and demonstrated access paths.
Primary keys cover identity lookup, the unique purchase-offer key supports
both concurrency and the active-offer anti-join, and a buyer/offer index
supports purchase history. Additional indexes require a concrete query and
query-plan evidence.

## Consequences

- Any number of stateless server instances can compete for an offer, while
  the database records at most one winning buyer.
- Sale-offer and purchase history remains available for audit and debugging.
- The active-offer view is transactionally current and requires no refresh
  process.
- Runtime code does not use `UPDATE`, `DELETE`, or `TRUNCATE` for domain facts.
- Corrections, cancellation, and other reversals require new domain facts and
  corresponding specification changes rather than in-place edits.
- Data grows over time and needs an explicit retention or archival decision
  if its operational cost becomes significant.
- Listing active offers uses an anti-join. Its performance must be measured as
  the proportion of purchased offers grows; a materialized or mutable
  projection would require a later decision with different consistency and
  mutation trade-offs.
- Buyer identity makes the binding order/reservation attributable.
  Request-level authentication and idempotency are defined by ADR-0009 and the
  append-only security migration.

## Alternatives considered

### Delete the purchased offer

An atomic `DELETE ... RETURNING` also prevents double sales and maps directly
to removing an offer from the active set. It was rejected because it discards
the original offer fact and provides no durable purchase or buyer relation.

### Update an offer status

A conditional update from active to sold is straightforward to query. It was
rejected because it mutates historical facts and makes corrections less
explicit.

### Process-local locking

A mutex can serialize goroutines in one process but cannot coordinate
separate server instances. It is insufficient for a multi-instance service.

### Event store or Kafka command stream

Expected stream revisions or partitioned command processing can serialize
buys while retaining history. They were rejected for the current scope
because they introduce projections, replay, asynchronous processing, and
additional operations beyond what the small domain requires.

## Reconsideration triggers

Revisit this decision when active-offer query plans no longer meet measured
requirements, retention obligations require destructive archival, a workflow
must atomically change several aggregates, or asynchronous event processing
becomes a system requirement.
