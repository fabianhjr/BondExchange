# ADR-0030: Prevent same-identity self-trading

- Status: Accepted
- Date: 2026-09-06

## Context

The marketplace permits every authorized principal to claim and buy every
active sale offer, including an offer attributed to that same internal
identity. That behavior is explicit in the TLA+ model and is one concrete part
of F-002's wider, undecided market-integrity policy.

The service has no balances, holdings, quantities, beneficial-owner or account
grouping, matching engine, price reference, market clock, or settlement state.
Adding controls that depend on any of those facts would expand the product and
formal model beyond their current purpose. Buyer and seller UUIDs already exist
in every purchase decision, however, so equality of those identities can be
prohibited without introducing new marketplace state.

## Decision

An authenticated principal must not reserve a sale offer whose seller UUID is
the same as the principal UUID used as the buyer. PostgreSQL is the final
authority for this rule: a trigger rejects any purchase insert whose buyer is
the referenced offer's seller, including inserts from alternate writers.

The application rejects a direct attempt with the durable safe error code
`self_trade_prohibited`. Exact idempotent retries replay that rejection. gRPC
reports `FailedPrecondition`; REST uses its existing HTTP 400 response shape.
No identity is accepted from the request and no identity is returned in the
error.

Discovery is a tradable view for the authenticated principal. Active-offer
listing omits that principal's own offers, and active-series discovery omits a
series when its only active offers belong to that principal. The database's
unparameterized `active_offers` view remains the global append-only projection;
the PostgreSQL adapter applies the principal-specific restriction.

The TLA+ model adds no variable or fact shape. It derives tradable offers from
the existing active offers, restricts successful buy resolution, represents a
self-trade rejection, and checks that no purchase has the same buyer and offer
seller.

The control assumes the existing internal UUID is the relevant identity. It
does not detect common beneficial ownership, affiliated principals, collusion,
or circular activity. Those controls require authoritative identity or account
relationship data and remain outside this decision.

## Consequences

- A seller cannot reserve its own offer through REST, gRPC, the Go store, or a
  direct PostgreSQL insert.
- Offer and series discovery become principal-specific without changing their
  wire representations.
- Rejected attempts are retained with the operation claim and can be counted
  through a bounded market-integrity metric without exposing identifiers or
  financial terms.
- Existing non-self purchases, exclusive contention, immutable facts, and the
  meaning of `Buy` as a binding reservation are unchanged.
- F-002 remains open because this rule addresses only same-identity
  self-trading. F-023 remains material because the schema does not enforce the
  principal-to-user relationship or represent beneficial ownership.

## Alternatives considered

### Add balances, holdings, quantities, or exposure limits

These controls require new authoritative facts and transaction semantics. They
would expand the product, persistence model, API, and TLA+ state rather than
restrict existing behavior.

### Add price bands

A meaningful price band needs a reviewed reference price, applicability rules,
and an owner for stale or unavailable reference data. The monetary type's
numeric maximum is a representation bound, not a market-integrity policy.

### Enforce the rule only in Go

That would leave direct SQL and future alternate writers able to append an
irreversible self-purchase fact. PostgreSQL must enforce the invariant shared by
all stateless server instances and writers.

### Return an unavailable-offer error

This would preserve the current public classification but make intentional
self-trade attempts indistinguishable from contention or missing offers in the
durable operation record and operational signals.
