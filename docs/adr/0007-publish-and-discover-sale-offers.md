# ADR-0007: Publish and discover sale offers through the API

- Status: Accepted
- Date: 2026-09-03

## Context

The initial API could buy offers and list active offers with an optional bond
filter and offer-ID pagination. Sale offers had to be provisioned outside the
service. Clients now need to publish sale offers and discover which bond series
currently have liquidity before requesting the offers for one series.

Unfiltered offer listing combines discovery with potentially unrelated offer
data, while pagination can hide offers from a requested series. The desired
contract instead separates series discovery from complete, bond-specific
listing.

## Decision

Require a bond series for `ListActiveOffers` and return every active offer for
that series in sale-offer ID order. Remove the `after` and `limit` fields from
the protobuf request and reserve their field numbers and names. The response is
intentionally unpaginated.

Add `ListActiveBondSeries`, exposed as `GET /active-bond-series`, to return each
bond series represented by at least one active offer exactly once and in
lexicographic order. Derive both reads from the existing `active_offers` view.

Add `CreateSaleOffer`, exposed as `POST /sale-offers`. A caller supplies the
offer ID, seller ID, bond series, exact decimal price string, and currency code.
The application validates and canonicalizes those values, then inserts the
offer as an append-only fact. Sellers and bonds continue to be provisioned
outside this API. The database primary key resolves concurrent creation of the
same ID, and foreign keys establish whether its seller and bond exist.

Model sale-offer creation as a new TLA+ state transition. An offer ID can never
be reused after creation, including after purchase. Keep listing and series
discovery as derived reads rather than transport or SQL behavior in the model.

## Consequences

- Clients first discover active bond series and then request the complete offer
  book for a chosen series.
- Calling the existing active-offer endpoint without `bond` now fails with an
  invalid-argument response. Existing uses of `after` and `limit` must stop.
- A bond series with no active offers is absent from discovery, including after
  its last active offer is purchased.
- Creating the first active offer for a series makes that series discoverable.
- Responses for a heavily offered bond may be large because listing is
  deliberately unpaginated. Pagination requires a later explicit contract
  change rather than implicit truncation.
- Offer creation preserves stateless servers and append-only facts. Duplicate
  IDs are reported as conflicts without process-local locking.
- No database schema migration is required; the existing table, constraints,
  view, and `(bond_series, id)` index support the behavior.

## Alternatives considered

### Keep the bond filter optional

This would retain compatibility but continue mixing series discovery with
offer retrieval and could return an unbounded cross-market result. It was not
selected because the dedicated discovery operation makes the two intents
explicit.

### Keep offer-ID pagination

Pagination would bound responses but would prevent the operation from returning
the complete active book requested for a bond series. It was removed. If offer
books become too large, a future contract can add snapshot or cursor semantics
with explicit consistency guarantees.

### Return bond records instead of series identifiers

The current bond domain contains only its canonical series. Returning strings
keeps discovery aligned with that model. A richer bond resource can replace or
supplement this response if the domain gains bond metadata.
