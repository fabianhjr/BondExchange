# ADR-0026: Verify fact provenance and append-only history in TLA+

ADR-0030 later derives a buyer-specific tradable subset of the active book and
adds the `NoSelfPurchases` invariant without adding marketplace state.

- Status: Accepted
- Date: 2026-09-05

## Context

The TLA+ model held the active sale book as a variable, removed an offer from
it when the offer was bought, and admitted any well-formed set of offers as the
initial state. Its invariants were checked and passing, but most of them
restated the guard of the only action that could break them:

- `AllSaleOffersAreMXN` restated a literal in `CreateSaleOffer`.
- `UniqueSaleOfferIds` restated the `offerId \notin KnownSaleOfferIds` guard.
- `UniqueOperationScopes` restated the `ScopeIsUnused` guard.
- `OnlyAuthorizedOperationsComplete` restated the `IsAuthorized` guard against
  a constant set.

Because offers could exist in the initial state with no operation result, the
converse of the authorization invariant — that no domain fact exists without an
authorized operation — was not merely unchecked, it was false. The model also
had no property corresponding to ADR-0003. Nothing prevented an action from
retracting a purchase or changing a published offer's price in place, even
though append-only facts are the repository's central storage rule, and
`docs/FMEA.md` credited the model as a detection control.

## Decision

The active sale book is no longer a fact. Published offers, purchases,
operation claims, and operation results are append-only sets, and the active
book is `ActiveOffers`, a view over published offers and purchases that mirrors
the service's derived active-offer read.

The market starts empty. Every offer, purchase, claim, and result is produced
by a modeled operation.

Purchases reference the offer by ID rather than embedding a copy of it, so an
offer record has exactly one representation, matching `purchases.sale_offer_uuid`.

These changes make the following properties expressible, and each was confirmed
to fail against a deliberately defective specification before being adopted:

- `EveryFactHasASucceededOperation` — the published offers and purchased offer
  IDs are exactly the resources named by succeeded results, with matching
  counts. This is the modeled form of the database rule that a successful
  operation result must reference an existing source fact.
- `OfferSellersMatchOperationPrincipals` and
  `PurchaseBuyersMatchOperationPrincipals` — a fact is attributed to the
  principal that claimed the operation producing it.
- `FactsAreAppendOnly`, `OfferTermsAreImmutable`,
  `PurchasedOffersStayPurchased`, and `SaleOfferIdsAreNeverReused` — the
  append-only rule, stated over steps rather than states.

## Consequences

The specification is split into three modules: actions, properties, and the
top-level composition. Properties are grouped by what they protect, so a
reviewer can see which failure each one addresses.

Provenance invariants force append-only behavior for offers and purchases on
their own, because they tie fact counts to operation counts. `OfferTermsAreImmutable`
and `FactsAreAppendOnly` remain load-bearing beyond that: an offer repriced in
place under the same ID preserves every count and identity set, and only those
two properties reject it. They are also the properties that a future
cancellation, expiry, or settlement action must not break, which is the
lifecycle work F-001 anticipates.

Starting from an empty market removes "any well-formed initial book" from the
model. That state was never reachable in the service, which begins with an
empty schema and produces every offer through an operation.

The mapping from model names to database objects is no longer one-to-one, and
the specification README now states it in full. One conflation narrows what a
checked property means and is accepted here rather than left implicit. An
offer's terms map to `sale_offer_canonical_terms`, so `AllPublishedOffersAreMXN`
covers the terms that reads and buys use and not every `sale_offers` row, which
is what F-018 is about.

This decision originally recorded a second conflation: the model's identity set
covered both `principals` and `users`, so it could not represent the mismatch
behind the `buyer_not_found` and `seller_not_found` rejections.
[ADR-0034](0034-make-the-principal-the-sole-identity.md) removed `users`, so
the set — now named `Principals` — maps to one table, that mismatch is no
longer representable in the schema either, and `buyer_not_found` has since been
retired.

## Alternatives considered

**Keep the active book as a variable and add a consistency invariant.** This
would check that the materialized book equals the derivation. The service has
no materialized book, so the invariant would verify a structure that does not
exist while leaving the append-only rule unchecked.

**Keep the arbitrary initial state and give initial offers synthetic operation
results.** This preserves an unreachable configuration and weakens the
provenance property to accommodate it, in exchange for initial states that the
behavior can reach anyway.

**Leave append-only to the database.** The trigger and revoked privileges do
enforce it in PostgreSQL. That places the repository's central storage rule
outside the artifact whose stated purpose is to verify domain behavior, and it
does not constrain a future domain action that has not been written yet.
