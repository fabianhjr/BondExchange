# Partial-fill implementation plan

## Required behavior

A sale offer carries a positive 32-bit `quantity`. `Buy` may carry a positive
quantity or omit it to buy all remaining quantity, preserving the old API
behavior. When two buyers concurrently request quantities smaller than the
original offer, both operations succeed if their combined quantity fits. The
database serializes the first purchase, creates an immutable residual offer,
and retries the conflicting transaction against that residual lineage.

For an offer of 10, concurrent buys of 3 and 4 therefore produce two purchase
facts and one active residual of 3. If the second request were 8, one request
would succeed and the other would receive `insufficient_quantity`, because the
combined demand exceeds the offer.

## Domain representation

- Add a positive `Quantity` value to sale offers and purchases.
- Add an optional requested quantity to create and buy commands.
- Default an omitted create quantity to one and an omitted buy quantity to all
  quantity remaining at execution time.
- Treat every residual as a new immutable sale-offer fact with the same seller,
  bond, raw terms, and canonical MXN terms as its parent.
- Link a residual to the partial purchase that created it. A purchase consumes
  exactly one immutable offer segment, so the existing one-purchase-per-offer
  constraint remains the concurrency authority.

## PostgreSQL transaction

1. Starting at the caller's offer ID, recursively follow purchase-to-residual
   links to the active leaf.
2. Insert the requested purchase against that leaf with
   `ON CONFLICT (sale_offer_uuid) DO NOTHING`.
3. If the purchase is smaller than the leaf, insert one residual offer and its
   canonical terms in the same transaction.
4. Record the operation result against the offer segment actually consumed.
5. If another transaction advanced the lineage, roll back the serializable
   snapshot and retry from the original ID so the new residual becomes visible.
6. Return `insufficient_quantity` only after observing an active residual whose
   quantity is smaller than the request; return `offer_unavailable` when the
   lineage is fully consumed.

Deferred database constraints require every partial purchase to have exactly
one residual, every full purchase to have none, and every residual to conserve
quantity and inherit its parent's terms. Previously deployed writers remain
compatible: historical offers are one unit, and an omitted purchase quantity
is filled from the referenced offer by a compatibility trigger.

## API and events

- Add optional `quantity` fields to create and buy requests.
- Return the executed quantity on offers and purchases.
- Map non-positive values to `InvalidArgument`/HTTP 400 and insufficient
  remaining quantity to `FailedPrecondition`/HTTP 400.
- Keep event schema versions 1 and 2 readable. Emit schema version 3 when
  quantity or residual information is needed; version 3 purchase events include
  the purchased quantity and, for a partial fill, the residual ID and quantity.

## Formal model

- Add quantities to offers, purchases, and pending buys.
- Split buy resolution into full commit, partial commit, advance-to-residual,
  insufficient-quantity rejection, unavailable rejection, and self-trade
  rejection.
- Verify one purchase per immutable segment, one residual per partial purchase,
  positive bounded purchases, term inheritance, quantity conservation, and
  append-only lineage facts.
- Use a small `BondExchangePartialFills.cfg` instance containing a two-unit
  offer and two one-unit buyers so TLC reaches the contended residual path.
  Keep the broader marketplace, authorization, and liveness instances focused
  on whole-offer behavior to avoid multiplying unrelated state spaces.

## Verification and documentation

- Unit-test parsing, defaults, transport mapping, and event version 3.
- Integration-test two concurrent partial buys sent to the same original offer;
  require two successes, distinct consumed segment IDs, conserved total
  quantity, and the correct active residual.
- Retain the existing whole-offer contention test, where omitted quantities
  still yield exactly one winner.
- Update the API, database, formal-model, application, integration-test, root,
  security, guarantee, friction, and FMEA documentation.
- Record the residual-lineage architecture in an ADR.
- Run `api:check`, `db:migrate`, `go:test`, `spec:check`, the focused integration
  gates, and finally `devenv test`.
