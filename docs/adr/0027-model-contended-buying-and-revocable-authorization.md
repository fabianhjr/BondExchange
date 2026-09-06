# ADR-0027: Model contended buying and revocable authorization

- Status: Accepted
- Date: 2026-09-05

This ADR builds on ADR-0026, which made every domain fact append-only and gave
every fact provenance.

## Context

Two properties that `docs/FMEA.md` credits to the model could not fail.

`Buy` removed the offer from the sale book and appended the purchase in one
atomic step, so "an offer is purchased at most once" held by construction of
the action. FM-003 is about concurrent requests contending for one offer; the
model had no state in which two requests contend.

`IsAuthorized` tested membership in the constant sets `AuthorizedBuyers` and
`AuthorizedOfferSellers`, and every action conjoined it. The service evaluates
`bond_exchange.effective_principal_permissions`, a view over append-only role
grants, role revocations, principal suspensions, and reinstatements. A constant
cannot be revoked, so nothing verified that a revocation takes effect.

The model also had no notion of a failed operation. The service records a
`rejected` outcome with a safe error code and replays it, and reusing a scope
with a different request digest returns a conflict. In the model, reusing a
scope with a different digest had no enabled transition, so "impossible" and
"correctly rejected" were indistinguishable.

There were no liveness properties and no fairness.

## Decision

**Buying is claimed and then resolved.** `ClaimBuy` appends the claim and
records the order as in flight, naming any offer ID. `CommitBuy` is enabled
only while the offer is still in the active book and appends the purchase.
`RejectBuy` is enabled otherwise and appends a rejected result carrying
`offer_unavailable`. Two authorized buyers can therefore hold simultaneous
claims against one offer, and `AtMostOnePurchasePerOffer` becomes a property of
the resolution rule.

This models concurrent contention, not the mechanism that resolves it.
Transaction isolation, the unique constraint, conflict handling, and retry
remain in the persistence adapter, per the architectural guardrail that keeps
SQL out of the model.

Publishing stays atomic. No other request contends for a fresh offer identity.

**Authorization is a set of append-only facts.** Role-permission grants,
principal-role grants, the revocations that name individual grants, and the
suspensions and reinstatements that name individual principals are variables,
and `EffectivePermissions` is a view over them, written in the shape of the
service's join. Grants carry a generation, so access revoked by an append-only
revocation is restored by a new grant rather than by retracting a fact.

**Retries are total.** `ReplayCompletedOperation`, `RetryInFlightOperation`,
and `RejectConflictingRetry` are separate actions, and `RetriesAreTotal`
requires that every retry of a claimed scope has an enabled handler.

**Weak fairness on buy resolution.** `EveryInFlightBuyIsResolved` and
`EveryClaimIsEventuallyResolved` require that a claimed order is always
resolved into a committed purchase or a recorded rejection. Nothing requires an
offer to be published or an order to be claimed, so liveness asserts only that
the service finishes work it has already accepted.

**Non-vacuity is checked directly.** `BuyIsClaimableForAnActiveOffer` and
`RetriesAreTotal` fail if a guard becomes strong enough to make an intended
transition unreachable, and `spec:check` fails when TLC reports that any action
was never enabled.

## Consequences

The marketplace and authorization state spaces multiply. One instance
containing both is either intractable or too small to reach the interesting
interleavings, so `spec:check` checks three instances — marketplace,
authorization, and liveness — each with constants sized for the failures it
must be able to distinguish. Every action is reachable in the instance that
checks it, and the coverage gate enforces that.

Each property was confirmed to fail against a deliberately defective
specification before being adopted. Weakening the exclusivity guard, dropping a
result record, misattributing a buyer, repricing an offer in place, ignoring a
suspension in the permission view, claiming without authorization,
over-constraining `ClaimBuy`, leaving a claimed order unresolvable, reusing an
idempotency scope, and removing a retry handler are each rejected by the
property intended to catch them.

`AuthorizedBuyers` and `AuthorizedOfferSellers` are gone. Authorization now
starts from the reviewed bootstrap that the security migration seeds: one
trader role carrying both mutation permissions, granted to every user.

Checking the authorization instance takes roughly a minute, which is the
dominant cost of `spec:check`. That is the price of a property about revocation
that can actually fail.

The model gains revocation semantics but not the timing of an authorization
check. `ClaimBuy` evaluates permission and appends the claim in one step, which
matches the service and leaves no reachable state between them, so
`NewClaimsAreAuthorizedWhenClaimed` cannot fail for a check performed before the
mutation transaction — a cause FM-004 names. Read operations and five of the six
recorded rejection outcomes are likewise unrepresented. The specification README
states these boundaries under "What a passing check does not establish", and
F-024 tracks closing them or reassigning the credit to the tests that cover
them.

## Alternatives considered

**Keep buying atomic and rely on the database tests.** Integration tests and a
generated workload already exercise concurrency, and the unique constraint is
the cross-instance authority. They demonstrate the implementation; they cannot
state what the domain requires of any implementation, and the FMEA credits the
model as a separate control.

**Model the serializable transaction.** Modeling isolation levels, conflict
codes, and retry would put SQL mechanics into the specification. The domain
requirement is that exactly one contending order wins and the others are
recorded as rejected, which the claim-and-resolve split states without naming a
mechanism.

**Re-check authorization at commit rather than at claim.** The service
authorizes, claims, and commits inside one transaction, so a revocation
interleaved after authorization cannot affect that operation. Re-checking at
commit would assert a guarantee the service does not make and does not need.

**Keep authorization constant and add only a suspension flag.** This would
check suspension while leaving role and permission revocation — the paths that
affect several principals at once — unverified.
