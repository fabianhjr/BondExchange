# ADR-0036: Represent partial fills as residual-offer lineages

- Status: Accepted
- Date: 2026-09-11

## Context

The original marketplace treated every offer as one indivisible reservation.
PostgreSQL's unique purchase key made concurrent buys of one offer mutually
exclusive. Adding a mutable remaining-quantity column would permit partial
fills, but it would violate the append-only domain-fact rule and make the terms
observed under one offer ID change over time.

A contended partial fill also needs different semantics from a whole-offer
race. If an offer has ten units and independent buyers concurrently request
three and four, both should succeed. The unique constraint still has to prevent
both transactions from purchasing the same units.

## Decision

Sale offers and purchases carry positive 32-bit quantities. A partial purchase
consumes one immutable offer segment and atomically appends exactly one residual
sale offer for `offered quantity - purchased quantity`. The residual inherits
the seller, bond, raw price/currency, and canonical MXN terms and names its
creating purchase through `partial_purchase_provenance`.

The original offer ID is a stable lineage entry point. A buy recursively follows
purchase-to-residual facts to the active leaf. PostgreSQL's existing unique
constraint on `purchases.sale_offer_uuid` serializes each leaf. When a concurrent
transaction discovers that another purchase advanced the lineage, it rolls back
its serializable snapshot and retries from the original ID. It succeeds against
the residual when sufficient quantity remains.

An omitted create quantity means one. An omitted buy quantity means all quantity
remaining at execution time, preserving the previous wire behavior. An explicit
request larger than the active residual is durably rejected as
`insufficient_quantity`; a fully consumed lineage remains `offer_unavailable`.

Deferred database constraint triggers enforce residual cardinality, quantity
conservation, and inherited terms for every writer. Event schema version 3 adds
quantity and residual details while readers retain versions 1 and 2.

## Consequences

- Two compatible partial buys that race on one original ID can both succeed,
  while no immutable segment can be purchased twice.
- Offer and purchase facts remain append-only; a purchased segment never
  becomes active again.
- One logical offer can produce a chain of offer and purchase rows. Reads expose
  only its active residual, while a buy may keep using any ancestor ID.
- High contention can cause serializable retries, and many small fills grow the
  immutable history linearly.
- A successful buy response identifies the segment actually consumed, which
  may differ from the ancestor ID in the request.
- This adds reservation quantity only. It adds no balances, holdings, funding,
  matching, allocation priority, ownership, or settlement semantics.

## Alternatives considered

**Update remaining quantity in place.** Rejected because it mutates a domain
fact, weakens historical reconstruction, and conflicts with the repository's
append-only architecture.

**Keep one offer row and append allocations without a residual fact.** Rejected
because determining the active remainder would require aggregation on every
contention decision and the existing one-purchase-per-offer constraint could no
longer be the cross-instance serialization authority.

**Reject every loser of the first segment race.** Rejected because it fails the
required behavior when independently requested quantities can both fit.

**Use process-local locks.** Rejected because server instances are stateless and
must coordinate through PostgreSQL.
