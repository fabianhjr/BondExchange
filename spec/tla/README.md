# Bond Exchange TLA+ specification

This directory contains a deliberately small marketplace model:

- `BondExchangeActions.tla` defines the domain, active sale offers, binding
  order/reservation records, authorized idempotent operations, validation,
  publishing action, and buying action.
- `BondExchange.tla` defines the possible initial sale books and composes the
  publishing and buying actions into the temporal specification.
- `BondExchange.cfg` provides a finite instance for exhaustive TLC checking.

## Domain

- A user is an element of the finite `Users` set.
- A bond is identified by an uppercase alphanumeric series string whose length
  is between 3 and 40 characters, inclusive.
- A sale offer has a unique ID, a seller, a bond, a positive price, and one
  currency code.
- A purchase fact relates the reserved offer to the user who placed the
  binding order. It does not assert settlement or ownership transfer.
- An operation result records its principal, client, operation, idempotency
  key, abstract request digest, and resulting offer ID.

Users, offers, operation keys, and purchases are abstract values here. The
implementation's UUIDv7 identity representation, UUIDv4 nonce syntax, and
PostgreSQL 18 generation mechanics do not change domain behavior and remain
outside the model.

The later removal of the legacy identifier graph and promotion of
`active_offers` from its transitional view name likewise changes only the
persistence refinement. It does not change an action or invariant in the TLA+
modules.

`Prices` uses positive natural numbers in the finite TLC instance as abstract
representatives of positive exact monetary values. Decimal precision,
PostgreSQL encoding, and JSON serialization do not affect buying behavior or
the checked invariants, so they remain outside the domain model.

The initial state represents any well-formed set of sale offers, including an
empty market, within the configured bounds. Offer IDs must be unique within
that set.

Case-insensitive lookup and conversion of input to the uppercase canonical
representation are responsibilities of the system boundary. The TLA+ model
does not implement that conversion; it assumes it receives normalized bond
identifiers and verifies that their stored representation is uppercase.

## Behavior

`CreateSaleOffer(seller, client, key, requestDigest, bond, offerId, price,
currency)` publishes a new active sale offer. It requires authorization, a new
idempotency scope, valid domain values, and an ID that has never appeared in
either the active book or purchase history. Creation appends the offer and its
operation result and does not change purchase facts. `offerId` represents the
fresh identity allocated by the service; it is not modeled as caller-selected.

`Buy(buyer, client, key, requestDigest, offerId)` is enabled only when the
principal/client is authorized, the idempotency scope is new, and exactly one
active sale offer has the requested ID. The action atomically removes that
offer from the active sale book and records a purchase fact and operation
result. `RetryCompletedOperation` permits only the same request digest for an
existing scope and leaves all domain state unchanged. Reusing a scope for a
different digest has no enabled transition.

Bond-specific offer listing and active-series discovery are derived reads and
do not change model state. HTTP parameters, SQL queries, ordering, and input
canonicalization remain implementation-boundary concerns.

Outbound integration-event references and delivery attempts are likewise an
implementation projection. They do not change sale offers, purchases, or
operation idempotency, so publisher interfaces, delivery leases, retries, and
the manual recovery endpoint are intentionally outside the TLA+ state. A
successful mutation's source fact remains the modeled behavior.

The model does not prohibit a seller from buying their own offer.

There are intentionally no buy offers, matching engine, balances, holdings,
partial fills, order publication, ownership transfer, cancellation, expiry, or
settlement process in this model. Settlement semantics remain pending.

## Verification

TLC checks through interleaved creation, buying, and retry that every reachable
state is well formed, offer IDs and operation scopes remain unique, an offer
cannot be both active and purchased, and every completed operation was
authorized:

```console
devenv tasks run spec:check
```

`devenv test` includes the same check. The specification CI workflow invokes
the focused `spec:check` task directly.

When the domain grows, introduce only behavior required by an explicit system
decision and add its properties to both the TLA+ module and TLC configuration.
