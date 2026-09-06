# Bond Exchange TLA+ specification

This directory contains a deliberately small marketplace model:

- `BondExchangeActions.tla` defines the domain, the append-only facts, the
  derived active sale book, the effective-authorization view, and every
  transition action.
- `BondExchangeProperties.tla` defines the state invariants, action
  properties, and liveness properties that TLC checks.
- `BondExchange.tla` composes the actions into the checked specifications.
- `BondExchange.cfg`, `BondExchangeAuthorization.cfg`, and
  `BondExchangeLiveness.cfg` provide finite instances for exhaustive TLC
  checking.

## Domain

- A principal is an element of the finite `Principals` set. It is the only
  identity in the model: it authenticates, sells, and buys.
- A bond is identified by an uppercase alphanumeric series string whose length
  is between 3 and 40 characters, inclusive.
- A sale offer has a unique ID, a seller, a bond, a positive price, and
  MXN-denominated terms.
- A purchase fact relates a reserved offer ID to the principal who placed the
  binding order. It does not assert settlement or ownership transfer.
- An operation claim records a principal, client, operation, idempotency key,
  and abstract request digest. An operation result records the outcome of one
  claim: a succeeded result names the resulting offer ID, and a rejected
  result names a safe error code.
- Authorization is a set of append-only facts: role-permission grants,
  principal-role grants, the revocations that name individual grants, and the
  suspensions and reinstatements that name individual principals.

Principals, offers, operation keys, and purchases are abstract values here. The
implementation's UUIDv7 identity representation, UUIDv4 nonce syntax, and
PostgreSQL 18 generation mechanics do not change domain behavior and remain
outside the model.

`Prices` uses positive natural numbers in the finite TLC instances as abstract
representatives of positive exact monetary values. Decimal precision,
PostgreSQL encoding, and JSON serialization do not affect buying behavior or
the checked properties, so they remain outside the domain model.

Case-insensitive lookup and conversion of input to the uppercase canonical
representation are responsibilities of the system boundary. The TLA+ model
does not implement that conversion; it assumes it receives normalized bond
identifiers and verifies that their stored representation is uppercase.

### Facts and derived reads

Every domain fact set is append-only. The active sale book is not a fact: it
is `ActiveOffers`, a view over published offers and purchases, matching the
service's derived active-offer read. Buying therefore never removes a record;
it appends a purchase that removes the offer from the derived book.

The only non-monotonic variable is `inFlightBuys`, which holds binding orders
that have been claimed and not yet resolved. It represents a request in
progress, not a persisted row: the service claims the scope, appends the
purchase, and records the result inside one transaction, so a claim without a
result is never committed.

`EffectivePermissions` is likewise a view, written in the shape of the
service's effective-permission join.

### Refinement mapping

A model name is not always the database object a reader would expect. The
mapping is:

| Model | Implementation |
| --- | --- |
| `publishedOffers` | `sale_offers` joined to `sale_offer_canonical_terms` |
| `offer.price`, `offer.currency` | `sale_offer_canonical_terms.price` and `.currency_code` |
| `ActiveOffers` | the global derived book: canonical terms, no purchase |
| `TradableOffers(buyer)` | the adapter's principal-specific read: active offers whose seller differs from the principal |
| `purchases` | `purchases`, referencing its offer by `sale_offer_uuid` |
| `claims` | `operation_claims`, keyed by the scope's unique constraint |
| `results` | `operation_results`, including the succeeded/rejected shape |
| `inFlightBuys` | a mutation request between claim and commit, not a row |
| authorization facts | the RBAC grant, revocation, suspension, and reinstatement tables |
| `EffectivePermissions` | `effective_principal_permissions` |
| `Principals` | `principals` |

One conflation is deliberate and narrows what a checked property means:

- **Offer terms.** `sale_offers.currency_code` is constrained only to
  `^[A-Z]{3}$`, while `sale_offer_canonical_terms.currency_code` requires MXN.
  Pre-existing non-MXN offers have no canonical terms and are excluded by the
  read and buy queries, which is
  [F-018](../../FRICTIONS.md#f-018--legacy-non-mxn-offers-have-no-seller-disposition-workflow-p1).
  `AllPublishedOffersAreMXN` therefore verifies the canonical terms that reads
  and buys use, not every `sale_offers` row.

  Note that `ActiveOffers` does not correspond to the database view named
  `bond_exchange.active_offers`. That view still selects `sale_offers.price` and
  `.currency_code` and does not require canonical terms; it is retained for an
  expand-first rolling deployment and the current adapter does not read it. The
  model corresponds to the adapter's query, so it says nothing about what the
  compatibility view returns while legacy rows remain.

`Principals` is one set and now maps to one table.
[ADR-0034](../../docs/adr/0034-make-the-principal-the-sole-identity.md) removed
`bond_exchange.users`, so an identity that sells or buys but cannot
authenticate is no longer representable in the schema either, and the model is
not narrower than the implementation on that point. What the single set still
does not represent is two principals under one beneficial owner:
`NoSelfPurchases` proves that one identity cannot buy its own offer, not that
distinct principals are unaffiliated
([F-002](../../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1)).

## Behavior

The market starts empty. Every offer, purchase, claim, and result is produced
by a modeled operation, so every fact has provenance and the specification can
require that no fact exists without an authorized operation. The initial
authorization state is the reviewed bootstrap: one trader role carrying both
mutation permissions, granted to every principal.

`CreateSaleOffer` publishes a new active MXN sale offer atomically. It requires
authorization, a new idempotency scope, and an ID that has never appeared
before, and it appends the offer together with its claim and succeeded result.
`offerId` represents the fresh identity allocated by the service; it is not
modeled as caller-selected, and no other request contends for it.

Buying is split into a claim and a resolution:

- `ClaimBuy` requires authorization and a new idempotency scope, then appends
  the claim and records the order as in flight. It may name any offer ID,
  including one that is unpublished or already sold.
- `CommitBuy` is enabled only when the named offer is still active and its
  seller differs from the buyer. It appends the purchase and a succeeded
  result.
- `RejectBuy` is enabled otherwise. It appends a rejected result carrying
  `offer_unavailable` and appends no purchase.
- `RejectSelfBuy` handles an active offer attributed to the buyer. It appends a
  rejected result carrying `self_trade_prohibited` and no purchase.

The split is what gives the exclusivity property its content. Two authorized
buyers can hold simultaneous claims against one active offer, so at most one
purchase per offer is a property of the resolution rule rather than a
consequence of an atomic action. This models concurrent contention for one
offer, not the mechanism that resolves it: transaction isolation, unique
constraints, conflict handling, and retry belong to the persistence adapter.

`ReplayCompletedOperation`, `RetryInFlightOperation`, and
`RejectConflictingRetry` are the three ways a retry of a claimed scope is
handled. All three leave every fact unchanged. They are separate actions so
that TLC coverage shows each path is reachable and `RetriesAreTotal` can
require that no retry is left unhandled.

`GrantRolePermission`, `RevokeRolePermission`, `GrantPrincipalRole`,
`RevokePrincipalRole`, `SuspendPrincipal`, and `ReinstatePrincipal` append
authorization facts. A revocation names the grant it revokes and a
reinstatement names the suspension it lifts, so restoring access requires a
new grant generation rather than the retraction of a fact. Granting and
suspending are enabled only when the corresponding access state is not
already in force.

Bond-specific offer listing and active-series discovery are derived reads and
do not change model state. Both use `TradableOffers` so a principal's own
offers are absent. HTTP parameters, SQL queries, ordering, and input
canonicalization remain implementation-boundary concerns.

Outbound integration-event references and delivery attempts are likewise an
implementation projection. They do not change sale offers, purchases, or
operation idempotency, so publisher interfaces, delivery leases, retries, and
the manual recovery endpoint are intentionally outside the TLA+ state.

USD submission provenance, Banxico FIX observations, quote expiry, decimal
conversion, and seller acceptance are application-boundary controls. They are
not marketplace state: the boundary must produce accepted MXN terms before it
can invoke the modeled core action. `AllPublishedOffersAreMXN` verifies the
resulting domain invariant without pulling HTTP, provider, or persistence
mechanics into the model.

The model prohibits a principal from buying an offer attributed to that same
principal.
It has no beneficial-owner or affiliated-principal relationship.

There are intentionally no buy offers, matching engine, balances, holdings,
partial fills, order publication, ownership transfer, cancellation, expiry, or
settlement process in this model. Settlement semantics remain pending.

## Checked properties

The [guarantee register](../../docs/guarantees.md) states in plain language
which system promises these properties support, alongside the PostgreSQL and Go
controls that enforce the same promises outside this model. Each property named
below is cited there, and `docs:check` fails when a guarantee cites a property
that this module no longer defines or that no TLC configuration checks.

### Marketplace exclusivity

- `AtMostOnePurchasePerOffer` — one offer is acquired at most once, even when
  several authorized buyers hold simultaneous claims against it.
- `ActiveAndPurchasedOffersAreDisjoint` and `EveryPurchasedOfferWasPublished` —
  the derived book and the purchase history stay consistent.
- `UniqueSaleOfferIds`.
- `AllPublishedOffersAreMXN` — every offer the model publishes carries MXN
  terms. This corresponds to canonical terms, not to every `sale_offers` row;
  see the refinement mapping above.
- `NoSelfPurchases` — every committed purchase has a buyer different from the
  referenced offer's seller.

### Provenance

- `EveryFactHasASucceededOperation` — the published offers and the purchased
  offer IDs are exactly the resources named by succeeded create and buy
  results, with matching counts. No fact exists without an authorized,
  idempotent operation, and no succeeded operation exists without its fact.
- `OfferSellersMatchOperationPrincipals` and
  `PurchaseBuyersMatchOperationPrincipals` — a fact is attributed to the
  principal that claimed the operation producing it.
- `EveryResultHasAClaim` and `ResultsReferenceKnownResources`.

### Idempotent operation handling

- `UniqueClaimScopes` and `AtMostOneResultPerClaim` — one scope is claimed at
  most once and resolves to at most one outcome.
- `ResultShapeIsWellFormed` — a succeeded result names a resource and no error
  code; a rejected result names an error code and no resource.
- `InFlightBuysAreClaimedAndUnresolved`.
- `ClaimDigestsAreImmutable` — a scope's recorded request digest never changes.

### Append-only history

- `FactsAreAppendOnly` and `AuthorizationFactsAreAppendOnly` — no fact is
  retracted.
- `OfferTermsAreImmutable` — a published offer's terms never change under its
  ID. Corrections require new facts.
- `PurchasedOffersStayPurchased` and `SaleOfferIdsAreNeverReused`.

### Authorization

- `EffectivePermissionsMatchAuthorizationFacts` — the effective-permission view
  agrees with an independently written predicate over the raw grant,
  revocation, suspension, and reinstatement facts.
- `SuspendedPrincipalsHaveNoPermissions` and `RevocationsReferenceGrants`.
- `NewClaimsAreAuthorizedWhenClaimed` — every newly appended claim was
  authorized under the authorization state in force before the step, so a
  revocation takes effect on the next claim.

### Non-vacuity

An invariant that restates the guard of the only action that can break it
verifies nothing. These properties fail if a guard becomes strong enough to
make an intended transition unreachable:

- `BuyIsClaimableForAnActiveOffer` — an authorized buyer with an unused scope
  can always claim an active offer.
- `RetriesAreTotal` — every retry of a claimed scope has an enabled handler.

`spec:check` additionally fails when TLC reports that any action was never
enabled in the instance that checks it.

### Liveness

- `EveryInFlightBuyIsResolved` and `EveryClaimIsEventuallyResolved` — under
  weak fairness on buy resolution, a claimed binding order is always resolved
  into a committed purchase or a recorded rejection. Nothing requires an offer
  to be published or an order to be claimed, so liveness here asserts only
  that the service finishes work it has already accepted.

  These describe the lifecycle of a request, not recoverable state. Because the
  service claims, commits, and records a result in one transaction, an
  unresolved claim is never durable, and a crashed request is resolved by
  transaction rollback rather than by a recovery step. The properties are what a
  future design that persisted an unresolved claim would have to keep.

## What a passing check does not establish

The failure-mode analysis credits this model as a detection control. These are
the boundaries of that credit, so a reviewer does not read more into a green
`spec:check` than it supports.

**Authorization is checked only where the model checks it.** `ClaimBuy`
evaluates authorization and appends the claim in one step, so
`NewClaimsAreAuthorizedWhenClaimed` holds by construction of that step. The
model cannot express a check performed at one moment and a claim committed at a
later one, which is the cause FM-004 names as "RBAC evaluated outside the
mutation transaction". The service performs authorization, claim, and commit in
one transaction; only the Go and PostgreSQL tests verify that it still does.

**Read operations are absent.** Active-offer listing and bond-series discovery
require the `offers.list` permission, but they change no state and have no
modeled action, so nothing here verifies that a revoked or suspended principal
cannot read the book.

**Rejection paths are largely absent.** `offer_unavailable` is the only
recorded rejection the model produces. The service also records
`seller_not_found`, `bond_not_found`, `offer_already_exists`, and
`conversion_quote_unavailable` as durable rejected results and replays them.
Publishing cannot fail in the model, so `ResultShapeIsWellFormed` and the
provenance properties exercise two rejection codes out of the six a current
operation can produce: `offer_unavailable` and `self_trade_prohibited`. A
seventh, `buyer_not_found`, is replayed but no longer produced; see
[ADR-0034](../../docs/adr/0034-make-the-principal-the-sole-identity.md).

**Assertion binding is absent.** `operation_claims.assertion_digest` and the
issuer, audience, `jti`, and audience-binding checks have no counterpart.
`RequestDigests` is an abstract set with no structure, so the model verifies
that a scope's digest never changes, not that a digest binds a request.

**There is no clock.** Quote expiry, delivery leases, retry scheduling, and
assertion lifetime are all time-based and entirely outside the model.

**One known divergence.** `RejectConflictingRetry` is enabled whenever a claim
with a different digest exists, whereas `replayResource` joins claims to
results and would report no rows before comparing digests if a claim were
unresolved. The state is not durably reachable, so the two cannot disagree in
practice, but the model is the more permissive of the two.

`FRICTIONS.md` records the coverage gaps that remain open:
[F-022](../../FRICTIONS.md#f-022--marketplace-and-authorization-behavior-are-model-checked-separately-p3)
and
[F-024](../../FRICTIONS.md#f-024--the-model-omits-authorization-timing-reads-and-rejection-paths-p2).

## Verification

```console
devenv tasks run spec:check
```

`devenv test` includes the same check, and the specification CI workflow
invokes the focused `spec:check` task directly.

The task checks three finite instances. Marketplace and authorization state
spaces multiply, and each concern needs different constants to stay
falsifiable, so one combined instance would be either intractable or too small
to reach the interesting interleavings:

| Instance | Specification | Purpose |
| --- | --- | --- |
| `BondExchange.cfg` | `MarketplaceSpec` | Contention, same-identity self-trade prevention, provenance, append-only history, and idempotent retries under a fixed authorization state. Two principals make both self and non-self resolution reachable; two offer identities and two prices make term immutability and identity reuse falsifiable; two request digests reach the conflicting-retry path. |
| `BondExchangeAuthorization.cfg` | `AuthorizationSpec` | Grants, revocations, suspensions, and reinstatements interleaved with marketplace operations. Two grant generations let access be revoked and then restored by a new fact. The market is kept minimal. |
| `BondExchangeLiveness.cfg` | `FairSpec` | Resolution of claimed binding orders under weak fairness. Every dimension is at the minimum that still lets two buyers contend. |

The task runs TLC with coverage reporting and fails when any action was never
enabled. A self-looping action such as an idempotent retry contributes no
distinct state, so the gate uses the total number of generated states rather
than the distinct count.

When the domain grows, introduce only behavior required by an explicit system
decision and add its properties to the properties module and to the TLC
configuration that can falsify them. A property added to a configuration whose
constants cannot distinguish the failure it describes is not verification.
