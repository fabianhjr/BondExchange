# ADR-0007: Publish and discover sale offers through the API

- Status: Accepted
- Date: 2026-09-03

ADR-0017 amends creation identity: PostgreSQL now generates the sale-offer
UUIDv7, and the caller no longer supplies an offer ID. The remaining publishing
and discovery decisions stay accepted.

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

Require a bond series for `ListActiveOffers` and stream every active offer for
that series in sale-offer ID order from one repeatable-read database snapshot.
Remove the `after` and `limit` fields from the protobuf request and reserve
their field numbers and names. The stream is intentionally unbounded in count,
uses transport backpressure, and ends with an explicit offer-count event. REST
uses JSON Text Sequences and gRPC uses server streaming.

Add `ListActiveBondSeries`, exposed as `GET /active-bond-series`, to return each
bond series represented by at least one active offer exactly once and in
lexicographic order. Derive both reads from the UUID-backed `active_offers` view.

Add `CreateSaleOffer`, exposed as `POST /sale-offers`. A caller supplies the
bond series, exact decimal price string, and currency code; the
seller is the authenticated principal and cannot be assigned in the request.
The application validates and canonicalizes those values, then PostgreSQL
generates a UUIDv7 and inserts the offer as an append-only fact. Sellers and
bonds continue to be provisioned outside this API. UUID foreign keys establish
whether its seller and bond exist, while the UUIDv4 mutation nonce handles
exact retries.

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
- Heavily offered bonds do not require one in-memory response, but slow clients
  retain a database snapshot and connection. Pagination or a maximum count
  requires a later explicit contract change rather than implicit truncation.
- Offer creation preserves stateless servers and append-only facts. Clients
  retain the returned UUIDv7 rather than selecting a resource identity.
- The UUID migration provides the database default, constraints, UUID view,
  relationship graph, and `(bond_series, uuid_id)` index for this behavior.

## Alternatives considered

### Keep the bond filter optional

This would retain compatibility but continue mixing series discovery with
offer retrieval and could return an unbounded cross-market result. It was not
selected because the dedicated discovery operation makes the two intents
explicit.

### Keep offer-ID pagination

Pagination would bound individual requests but require explicit cross-page
snapshot semantics to represent one complete active book. Streaming was
selected to preserve a single database snapshot and incremental delivery. If
offer books become too slow to consume, a future contract can add snapshot or
cursor semantics with explicit consistency guarantees.

### Return bond records instead of series identifiers

The current bond domain contains only its canonical series. Returning strings
keeps discovery aligned with that model. A richer bond resource can replace or
supplement this response if the domain gains bond metadata.
