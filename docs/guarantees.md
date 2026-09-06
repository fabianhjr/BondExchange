# System guarantees

This is the plain-language register of what the Bond Exchange service promises,
what it deliberately does not promise, and the evidence behind each claim. It is
written for two readers at once: someone calling the API who needs to know what
a response means, and someone reviewing the system who needs to know where each
promise is enforced and what proves it.

A guarantee is listed here only when it is enforced in code or schema and
verified by a task in the test gate. Prose alone does not qualify.

- [How to read an entry](#how-to-read-an-entry)
- [Marketplace integrity](#marketplace-integrity)
- [History and provenance](#history-and-provenance)
- [Identity, authorization, and admission](#identity-authorization-and-admission)
- [Operations](#operations)
- [What the system does not guarantee](#what-the-system-does-not-guarantee)
- [How this register is verified](#how-this-register-is-verified)

## How to read an entry

Every entry has a stable `G-` identifier that other documents cite. Identifiers
are never reused. Each entry states:

- **Promise** — what holds, in one sentence, without reference to mechanism.
- **Even when** — the adverse condition the promise survives. A guarantee that
  only holds when nothing goes wrong is not a guarantee.
- **You will see** — the observable API behavior, for a caller.
- **Not promised** — the boundary. Every entry has one, and it links to the
  [friction register](../FRICTIONS.md) or the [FMEA](FMEA.md) where the gap is
  tracked.
- **Enforced by** — the named Go identifiers, PostgreSQL objects, Proto3
  declarations, and TLA+ properties that implement and prove it, plus the
  devenv tasks that verify it.

Every name in an **Enforced by** block is checked to exist by `docs:check`, so
an entry cannot outlive the code it describes. See
[how this register is verified](#how-this-register-is-verified) and
[ADR-0032](adr/0032-publish-a-verified-guarantee-register.md).

Status codes below are the REST ones. The gRPC code for each is given in
parentheses; the in-process gateway derives one from the other.

## Marketplace integrity

### G-001 — At most one buyer acquires a sale offer

**Promise.** One sale offer produces at most one binding reservation, for one
buyer, permanently.

**Even when.** Any number of authorized buyers submit `Buy` for the same offer
in the same instant, against different stateless server instances, with no
coordination between them.

**You will see.** Exactly one caller receives `201 Created` (`OK`) with the
purchase. Every other caller receives `404 Not Found` (`NOT_FOUND`) and
`offer_unavailable` is recorded as the durable outcome of that caller's
operation, so retrying the same idempotency nonce returns the same rejection
rather than racing again.

**Not promised.** A reservation is not settlement, payment, custody, or
ownership transfer, and the offer never returns to the active book
([F-001](../FRICTIONS.md#f-001--buying-stops-at-an-immutable-reservation-p1),
[FM-001](FMEA.md#fm-001--reservation-mistaken-for-settlement)). Which of the
racing buyers wins is unspecified; there is no price/time priority
([F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1)).

**Enforced by.**

- PostgreSQL: `purchases_sale_offer_uuid_unique`
- Go: `buyQuery`, `ErrOfferUnavailable`, `retryTransaction`
- TLA+: `AtMostOnePurchasePerOffer`, `ActiveAndPurchasedOffersAreDisjoint`, `EveryPurchasedOfferWasPublished`
- Verified by: `spec:check`, `go:test`, `integration:load-smoke`

The unique constraint on the offer UUID, not a lock in the server, is what
decides the race; `integration:load-smoke` sends a burst of concurrent buys for
one offer and fails unless exactly one succeeds and every other request is
refused. See
[ADR-0003](adr/0003-use-append-only-postgresql-facts.md) and
[FM-003](FMEA.md#fm-003--double-acquisition-of-one-offer).

### G-002 — Nobody buys their own sale offer

**Promise.** A principal cannot reserve an offer that the same principal
published.

**Even when.** The seller already holds the offer ID from publishing it and
submits a buy directly, bypassing the discovery view that hides it, or races
their own publication.

**You will see.** Discovery omits the caller's own offers entirely, from both
the offer book and the bond-series list. A direct `Buy` of one's own offer
returns `400 Bad Request` (`FAILED_PRECONDITION`) with `self_trade_prohibited`
recorded durably, so the retry of that nonce replays the same rejection.

**Not promised.** This covers one internal identity only. Two principals with
the same beneficial owner, affiliated accounts, and coordinated wash trading
are not detected, because the applicable market-integrity policy is undecided
([F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1),
[FM-002](FMEA.md#fm-002--unsupported-market-integrity-behavior)).

**Enforced by.**

- PostgreSQL: `purchases_reject_self_trade`, `reject_self_purchase`
- Go: `ErrSelfTradeProhibited`, `classifyBuyError`, `classifyFailedBuyQuery`, `RecordSelfTradeRejection`
- TLA+: `NoSelfPurchases`
- Verified by: `spec:check`, `go:test`, `integration:test`

Three independent layers hold this: the buy statement will not insert a
self-purchase, a `BEFORE INSERT` trigger rejects one from any writer, and the
domain model proves no committed purchase names its own seller. See
[ADR-0030](adr/0030-prevent-same-identity-self-trading.md).

### G-003 — Every offer you can see or buy is priced in MXN

**Promise.** The price and currency on every offer returned by discovery or
resolved by a buy are accepted canonical MXN terms.

**Even when.** The offer was submitted in USD, or predates canonical terms
entirely.

**You will see.** `currency_code` is always `MXN`. A USD submission must first
obtain a quote and have the seller accept it; if no fresh Banxico SIE
observation supports a quote the request is refused with
`conversion_quote_unavailable` rather than being priced on a stale rate.

**Not promised.** Historical non-MXN rows retained from before canonicalization
have no canonical terms. They are excluded from every read and buy path rather
than converted, and they have no seller disposition workflow
([F-018](../FRICTIONS.md#f-018--legacy-non-mxn-offers-have-no-seller-disposition-workflow-p1),
[FM-020](FMEA.md#fm-020--legacy-non-mxn-offer-rollout-ambiguity)).

**Enforced by.**

- PostgreSQL: `sale_offer_canonical_terms`, `sale_offer_canonical_terms_currency_mxn`
- Go: `PrepareSaleOffer`, `ErrInvalidCurrencyCode`, `ErrConversionQuoteUnavailable`
- TLA+: `AllPublishedOffersAreMXN`
- Verified by: `spec:check`, `db:canonical-mxn-readiness`, `go:test`, `integration:test`

The buy and discovery statements join canonical terms, so an offer without them
is unreachable rather than mispriced. See
[ADR-0019](adr/0019-canonicalize-sale-offers-to-mxn-at-intake.md) and
[the exchange-rate guide](exchange-rates.md).

### G-004 — Published offer terms never change, and offer IDs are never reused

**Promise.** Once published, an offer's seller, bond, price, and currency are
fixed under its ID forever, and that ID never names a different offer.

**Even when.** The seller resubmits, a correction is needed, or an operator has
direct database access through the application role.

**You will see.** The offer you read is the offer you buy: the purchase
response echoes the same terms that discovery returned. There is no update or
delete endpoint, and none is planned for these facts.

**Not promised.** There is no cancellation, expiry, amendment, or repricing
workflow at all; a correction would require new facts and a decided lifecycle
([F-001](../FRICTIONS.md#f-001--buying-stops-at-an-immutable-reservation-p1)).

**Enforced by.**

- PostgreSQL: `sale_offers_are_append_only`, `sale_offer_canonical_terms_are_append_only`
- Go: `createSaleOfferQuery`, `ErrOfferAlreadyExists`
- TLA+: `OfferTermsAreImmutable`, `SaleOfferIdsAreNeverReused`, `UniqueSaleOfferIds`, `PurchasedOffersStayPurchased`
- Verified by: `spec:check`, `go:test`, `db:migrate`

## History and provenance

### G-005 — Domain facts are append-only; corrections are new facts

**Promise.** Principals, bonds, sale offers, purchases, RBAC changes, and
operation results are inserted and never updated or deleted.

**Even when.** The write is attempted directly in SQL by the application role
rather than through the API, or by a future migration.

**You will see.** A fact you have read never changes underneath you, and no
value you were shown is ever rewritten. Nothing that was true becomes silently
untrue: a reversal, if one is ever added, will be a new fact that references the
original.

**Not promised.** Append-only history is unbounded and has no retention,
archival, or partitioning policy, so storage and query cost grow without limit
([F-005](../FRICTIONS.md#f-005--append-only-and-event-delivery-data-has-no-lifecycle-policy-p1),
[FM-006](FMEA.md#fm-006--storage-or-query-exhaustion-from-retained-history)).
Mutable operational coordination — rate-limit windows, delivery leases, and
rate-fetch coordination — is deliberately outside this rule and is separated
into its own tables.

**Enforced by.**

- PostgreSQL: `reject_domain_fact_mutation`, `principals_are_append_only`, `bonds_are_append_only`, `sale_offers_are_append_only`, `purchases_are_append_only`
- TLA+: `FactsAreAppendOnly`, `AuthorizationFactsAreAppendOnly`
- Verified by: `spec:check`, `go:test`, `db:migrate`

Statement-level triggers reject `UPDATE`, `DELETE`, and `TRUNCATE` on every
fact table, so the rule binds any writer holding the application role, not only
this service. See
[ADR-0003](adr/0003-use-append-only-postgresql-facts.md) and
[FM-005](FMEA.md#fm-005--invalid-immutable-facts-from-an-alternate-writer).

### G-006 — Every fact comes from one authorized, recorded operation

**Promise.** Each offer and each purchase corresponds to exactly one succeeded
operation result, is attributed to the principal that claimed that operation,
and no succeeded operation exists without its fact.

**Even when.** A request is retried, a transaction is rolled back, or a
connection drops mid-write.

**You will see.** Whatever you can read back, you can trace to the operation
that created it. A crashed or rolled-back request leaves no partial fact: the
claim, the fact, and the result commit together or not at all.

**Not promised.** The recorded outcome does not prove the request reached a
downstream consumer; see [G-014](#g-014--integration-event-delivery-is-not-guaranteed).

**Enforced by.**

- PostgreSQL: `operation_results_shape`, `operation_results_outcome_known`, `operation_claims_operation_canonical`
- Go: `recordOperationSuccess`, `recordOperationRejection`, `beginAuthorizedMutation`
- TLA+: `EveryFactHasASucceededOperation`, `OfferSellersMatchOperationPrincipals`, `PurchaseBuyersMatchOperationPrincipals`, `EveryResultHasAClaim`, `ResultsReferenceKnownResources`, `ResultShapeIsWellFormed`
- Verified by: `spec:check`, `go:test`

## Identity, authorization, and admission

### G-007 — Every request carries a short-lived assertion bound to one operation and one body

**Promise.** A federated JWT authorizes exactly one operation, against exactly
one request body, for a bounded lifetime.

**Even when.** A valid assertion is captured in transit and replayed against a
different endpoint, a different payload, or a different idempotency nonce.

**You will see.** `401 Unauthorized` (`UNAUTHENTICATED`) whenever the issuer,
audience, algorithm, key ID, lifetime, single authorization-detail action,
SHA-256 request digest, or bound idempotency key does not match the request
being made. Every failure is reported identically, without disclosing which
check failed.

**Not promised.** The digest construction is defined by this implementation and
has no published interoperable specification, so an independent client cannot
be written from a spec today
([F-025](../FRICTIONS.md#f-025--the-request-digest-binding-has-no-interoperable-specification-p1)).
The TLA+ model treats digests as opaque values and verifies only that a
recorded digest never changes, not that it binds a request
([F-024](../FRICTIONS.md#f-024--the-model-omits-authorization-timing-reads-and-rejection-paths-p2)).

**Enforced by.**

- PostgreSQL: `operation_claims_request_digest_sha256`, `operation_claims_assertion_digest_sha256`
- Go: `validateClaims`, `AuthorizationType`, `errInvalidAssertion`, `ErrUnauthenticated`
- Verified by: `go:test`, `integration:test`, `integration:load-smoke`, `security:check`

See [ADR-0009](adr/0009-bind-federated-authorization-to-idempotent-operations.md)
and [FM-004](FMEA.md#fm-004--unauthorized-altered-or-replayed-mutation).

### G-008 — Retrying a mutation repeats the outcome, never the effect

**Promise.** Replaying a mutation with the same idempotency nonce returns the
original outcome and appends no second fact. A nonce reused for a different
request body is refused rather than silently accepted.

**Even when.** The original response was lost, the client times out and retries,
or two copies of the retry arrive concurrently.

**You will see.** The original response again — including the original
rejection, replayed verbatim with the same error. A conflicting body under a
used nonce returns `409 Conflict` (`ALREADY_EXISTS`). Nonces must be canonical
UUIDv4 and are scoped per principal, client, and operation.

**Not promised.** Retries are not deduplicated across different nonces: a client
that generates a fresh nonce for a retry is submitting a new operation.

**Enforced by.**

- PostgreSQL: `operation_claims_nonce_scope_unique`
- Go: `claimOperation`, `replayResource`, `ErrIdempotencyConflict`, `IsValidIdempotencyKey`
- TLA+: `UniqueClaimScopes`, `AtMostOneResultPerClaim`, `ClaimDigestsAreImmutable`, `RetriesAreTotal`
- Verified by: `spec:check`, `go:test`, `integration:test`

The claim is an `INSERT ... ON CONFLICT DO NOTHING` inside the mutation's own
serializable transaction, so the database — not the process — decides which
concurrent copy of a retry is the original.

### G-009 — Buyer and seller identity comes only from the authenticated principal

**Promise.** The seller of a created offer and the buyer of a purchase are
taken from the authenticated principal, never from the request, and are never
returned in a response.

**Even when.** The caller sends a seller or buyer identifier in the payload.

**You will see.** No `seller_id` or `buyer_id` field exists in any request or
response message; those field names are reserved in the Proto3 contract so they
cannot be reintroduced by accident. You cannot publish or buy on behalf of
another identity, and you cannot learn the counterparty's identifier.

**Not promised.** The principal is the identity, not the person behind it. Two
principals under one beneficial owner are indistinguishable from two unrelated
counterparties, because the service holds no affiliation data
([F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1)).

**Enforced by.**

- Go: `PrepareSaleOffer`, `ResolvePrincipal`, `AccessContext`
- Proto3: `SaleOffer`, `seller_id`, `buyer_id`
- Verified by: `api:check`, `go:test`, `integration:test`

### G-010 — Authorization is decided inside the transaction that appends the fact

**Promise.** Every mutation checks the caller's effective permissions in the
same serializable transaction that writes the fact, against append-only RBAC
facts. A revocation or suspension takes effect on the next operation.

**Even when.** The revocation lands between the caller's authentication and
their write, or a suspended principal still holds an unexpired assertion.

**You will see.** `403 Forbidden` (`PERMISSION_DENIED`) as soon as the grant
chain no longer supports the operation. Restoring access requires a new grant
fact, never the retraction of the revocation.

**Not promised.** Read operations — the offer book and bond-series discovery —
have no modeled action, so nothing in the formal model verifies that a revoked
or suspended principal cannot read
([F-024](../FRICTIONS.md#f-024--the-model-omits-authorization-timing-reads-and-rejection-paths-p2)).
There is also no API for administering principals, roles, or grants; all of it
is direct SQL
([F-003](../FRICTIONS.md#f-003--provisioning-and-security-administration-require-direct-sql-p1)).

**Enforced by.**

- PostgreSQL: `effective_principal_permissions`
- Go: `authorize`, `beginAuthorizedMutation`, `ErrPermissionDenied`
- TLA+: `NewClaimsAreAuthorizedWhenClaimed`, `EffectivePermissionsMatchAuthorizationFacts`, `SuspendedPrincipalsHaveNoPermissions`, `RevocationsReferenceGrants`
- Verified by: `spec:check`, `go:test`, `integration:test`

### G-011 — At most 100 requests per principal per minute

**Promise.** An authenticated principal is admitted at most 100 requests per
UTC minute of the database clock, counted across every operation, client ID,
and server instance.

**Even when.** The caller spreads traffic across many server instances or many
client IDs, and instances disagree about the wall clock.

**You will see.** `429 Too Many Requests` (`RESOURCE_EXHAUSTED`) with a
`RetryInfo` delay naming the seconds remaining in the current window.
Admission happens after authentication and before authorization, so an
unauthenticated request is never counted against a principal.

**Not promised.** The limit is fixed in code, not configurable, and is not a
defense against distributed or unauthenticated flooding, which belongs to a
deployment boundary this repository does not define
([F-006](../FRICTIONS.md#f-006--read-apis-have-unbounded-resource-use-p1),
[F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1),
[FM-007](FMEA.md#fm-007--read-driven-resource-exhaustion)). If the coordinating
query fails the request is refused with `503 Service Unavailable`
(`UNAVAILABLE`) rather than admitted
([FM-022](FMEA.md#fm-022--incorrect-or-contended-per-principal-request-admission)).

**Enforced by.**

- PostgreSQL: `principal_rate_limits`
- Go: `admitRequestQuery`, `RequestsPerMinute`, `ExceededError`, `ErrUnavailable`
- Verified by: `go:test`, `integration:test`, `integration:load-smoke`

The window boundary comes from `statement_timestamp()` in the database, so all
instances share one clock. See
[ADR-0028](adr/0028-coordinate-per-principal-request-rate-limits-in-postgresql.md).

### G-016 — Every seller and buyer is an authenticated principal

**Promise.** The service has one identity table. Every sale offer and every
purchase is attributed to a principal — an identity that can authenticate —
and no other kind of identity exists for a fact to be attributed to.

**Even when.** The fact is appended by direct SQL rather than through the API,
or by a future alternate writer.

**You will see.** No response can carry a seller or buyer that could not have
authenticated, and no operation can be rejected because the identity that
authenticated is not the identity its facts are attributed to. That failure
mode is removed rather than handled.

**Not promised.** The register says nothing about *who* the principal is.
Provisioning a principal remains a direct-SQL activity with no reviewed
workflow
([F-003](../FRICTIONS.md#f-003--provisioning-and-security-administration-require-direct-sql-p1)),
and the service holds no data relating distinct principals to a common
beneficial owner
([F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1)).

**Enforced by.**

- PostgreSQL: `sale_offers_seller_principal_fkey`, `purchases_buyer_principal_fkey`, `principals_are_append_only`
- Go: `PrincipalID`, `ResolvePrincipal`, `classifyFailedBuyQuery`
- TLA+: `OfferSellersMatchOperationPrincipals`, `PurchaseBuyersMatchOperationPrincipals`
- Verified by: `db:migrate`, `go:test`, `spec:check`

The foreign keys were added `NOT VALID` and validated by a separate migration,
so the retained history was proven to conform rather than assumed to. The
`bond_exchange.users` table this replaced held no attribute other than its own
identifier; see
[ADR-0034](adr/0034-make-the-principal-the-sole-identity.md).
`TestPrincipalIsTheSoleIdentityTable` re-checks the contracted shape on every
run, so the transitional readiness gate that guarded the cutover was removed
with the rest of its tooling.

## Operations

### G-012 — Servers are stateless and PostgreSQL is the sole concurrency authority

**Promise.** No correctness property depends on process-local state. Any
instance can serve any request, and instances can be added, removed, or
restarted freely.

**Even when.** Instances run concurrently with no shared memory, no sticky
sessions, and no leader election.

**You will see.** Consistent behavior regardless of which instance answers, and
no warm-up window in which guarantees are weaker.

**Not promised.** Availability, autoscaling, and the deployment boundary itself
are outside this repository
([F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1),
[FM-008](FMEA.md#fm-008--unsafe-production-deployment-boundary)). No operation
has a server-side deadline
([F-027](../FRICTIONS.md#f-027--no-operation-has-a-server-side-deadline-p2)).

**Enforced by.**

- Go: `beginAuthorizedMutation`, `retryTransaction`, `isRetryableTransactionError`
- Verified by: `integration:load-smoke`, `postgres:lifecycle-check`, `go:test`

Mutations run at `Serializable` isolation and serialization failures are
retried, so contention is resolved by the database rather than avoided by
locking in the process.

### G-013 — Migrations roll forward and stay compatible with the deployed application

**Promise.** Applying a migration never breaks the application version already
running, and never discards a domain fact.

**Even when.** A deployment is rolling and both versions serve traffic at once,
or a change must be reverted — which is done with a corrective forward
migration, not by discarding facts.

**You will see.** Schema changes arrive as timestamped dbmate migrations in
expand, backfill, and contract steps. The service never migrates at startup.

**Not promised.** Compatibility is enforced by review and by readiness gates for
specific cutovers, not by an automatic check of arbitrary migration pairs.
Explicitly accepted retention decisions may remove non-domain evidence after a
verified backup; that recovery procedure is not exercised automatically
([FM-016](FMEA.md#fm-016--lossy-or-incompatible-database-migration)).

**Enforced by.**

- Verified by: `db:migrate`, `db:canonical-mxn-readiness`, `go:test`, `docs:check`

`db:migrate` applies the full history to a fresh database, PostgreSQL integration
tests verify the retained UUID schema and reject retired legacy artifacts, and
`docs:check` fails when a migration is missing from the
[schema history](../db/README.md#schema-history). See
[ADR-0004](adr/0004-use-dbmate-for-database-migrations.md) and
[ADR-0018](adr/0018-contract-the-legacy-identifier-graph.md). ADR-0033 records
the accepted retirement of the pre-UUID evidence and its backup-only recovery
path.

### G-014 — Integration event delivery is not guaranteed

This entry records a deliberate non-guarantee that is easy to mistake for one,
because the mechanism looks like an outbox.

**Promise.** A successful mutation appends an integration-event reference in the
same transaction as the fact, so the record of what happened is never lost.

**Even when.** No publisher is configured — the reference is still written.

**You will see.** Nothing. This repository configures no publisher, so no event
leaves the service. Delivery, when a publisher exists, is at-least-once and
consumers must deduplicate.

**Not promised.** There is no retry worker and no automatic recovery; an
undelivered event stays undelivered until an operator invokes the manual
recovery operation
([F-017](../FRICTIONS.md#f-017--integration-event-recovery-is-manual-and-has-no-destination-p2),
[FM-010](FMEA.md#fm-010--committed-event-remains-undelivered),
[FM-011](FMEA.md#fm-011--duplicate-integration-event-effect)).

**Enforced by.**

- PostgreSQL: `record_integration_event_on_completion`, `integration_events_are_append_only`
- Go: `RecordEventDelivery`, `ErrNoPublishers`
- Verified by: `go:test`, `observability:check`

See [ADR-0011](adr/0011-use-minimal-transactional-event-references.md).

### G-015 — Telemetry leaves the process only when it is configured to

**Promise.** No trace or metric leaves the process unless standard `OTEL_*`
configuration enables that signal, and the attributes the application emits are
a fixed, bounded set.

**Even when.** The service runs in an environment with no collector, or with
partial OpenTelemetry configuration — an unsupported exporter or protocol is
rejected at startup rather than silently ignored.

**You will see.** Nothing exported by default. When a signal is enabled, metric
and span attributes are drawn from a closed set of operation names, outcomes,
stages, and error classes. Principal, assertion, event, destination, and
request identifiers are not among them, so cardinality stays bounded and no
request content is carried out of the process.

**Not promised.** This constrains what the application emits, not what a
deployment's collector, exporter, or the SDK's own resource attributes add
downstream, and it says nothing about the confidentiality of the transport a
deployment configures
([F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1),
[FM-021](FMEA.md#fm-021--telemetry-is-lost-unsafe-or-misleading)).

**Enforced by.**

- Go: `signalEnabled`, `validateHTTPExporter`, `newRecorder`, `NewLogHandler`
- Verified by: `observability:check`, `go:test`

See [ADR-0025](adr/0025-own-application-opentelemetry-instrumentation.md),
[ADR-0029](adr/0029-use-policy-aligned-operational-metrics.md), and the
[observability guide](observability.md#data-handling-and-cardinality).

## What the system does not guarantee

The boundaries above are per-guarantee. These are the system-wide ones. Each is
a deliberate scope decision, tracked where it can be acted on.

| Not guaranteed | Where it is tracked |
| --- | --- |
| Settlement, payment, custody, or ownership transfer. `Buy` records a reservation and stops. | [F-001](../FRICTIONS.md#f-001--buying-stops-at-an-immutable-reservation-p1), [FM-001](FMEA.md#fm-001--reservation-mistaken-for-settlement) |
| Cancellation, expiry, amendment, or any return of an offer to the active book. | [F-001](../FRICTIONS.md#f-001--buying-stops-at-an-immutable-reservation-p1) |
| Buy offers, a matching engine, partial fills, balances, holdings, eligibility, price bands, or price/time priority. | [F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1), [FM-002](FMEA.md#fm-002--unsupported-market-integrity-behavior) |
| Detection of affiliated-account, wash, or otherwise manipulative trading. Only same-identity self-trading is prevented. | [F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1) |
| Creation of principals or bonds through the API, or any administration of principals, roles, and grants. | [F-003](../FRICTIONS.md#f-003--provisioning-and-security-administration-require-direct-sql-p1) |
| Any relationship between distinct principals: the principal is the identity, not the person behind it. | [F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1) |
| Storage constraints as strict as the Go domain validation for every column. | [F-004](../FRICTIONS.md#f-004--database-constraints-are-looser-than-domain-validation-p1) |
| Bounded resource use on read APIs, or any server-side operation deadline. | [F-006](../FRICTIONS.md#f-006--read-apis-have-unbounded-resource-use-p1), [F-027](../FRICTIONS.md#f-027--no-operation-has-a-server-side-deadline-p2) |
| Delivery of integration events. See [G-014](#g-014--integration-event-delivery-is-not-guaranteed). | [F-017](../FRICTIONS.md#f-017--integration-event-recovery-is-manual-and-has-no-destination-p2), [FM-010](FMEA.md#fm-010--committed-event-remains-undelivered) |
| Retention, archival, or bounded growth of append-only history. | [F-005](../FRICTIONS.md#f-005--append-only-and-event-delivery-data-has-no-lifecycle-policy-p1), [FM-006](FMEA.md#fm-006--storage-or-query-exhaustion-from-retained-history) |
| A production deployment boundary: TLS termination, network policy, secret management, or backups. | [F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1), [FM-008](FMEA.md#fm-008--unsafe-production-deployment-boundary) |
| An interoperable specification of the request-digest binding for third-party clients. | [F-025](../FRICTIONS.md#f-025--the-request-digest-binding-has-no-interoperable-specification-p1) |
| Formal verification of read-path authorization, authorization timing, or most rejection paths. | [F-024](../FRICTIONS.md#f-024--the-model-omits-authorization-timing-reads-and-rejection-paths-p2) |

The [TLA+ model's own boundaries](../spec/tla/README.md#what-a-passing-check-does-not-establish)
state precisely what a green `spec:check` does and does not establish.

## How this register is verified

`docs:check` parses every **Enforced by** block and fails when a cited name
cannot be found where its kind says it lives:

| Kind | Checked against |
| --- | --- |
| `PostgreSQL:` | `db/migrations/`, forward sections only. The name must be defined, and its most recent definition must come after any drop of it. |
| `Go:` | Non-generated, non-test Go source under `application/`. |
| `Proto3:` | `api/proto/`. |
| `TLA+:` | Defined in `spec/tla/BondExchangeProperties.tla` **and** referenced by at least one TLC configuration. A property no instance checks is not evidence. |
| `Verified by:` | Task names defined in `devenv.nix`. |

The same check rejects an entry that is missing any of the five sections above
or that names no verifying task, so a guarantee cannot be added without a
boundary or without evidence.

`G-` identifiers are checked the same way `F-` and `FM-` identifiers are: a
duplicate definition fails, and a reference anywhere in the documentation to an
identifier this register does not define fails.

This makes the register cheap to trust and expensive to let rot. Renaming a
constraint, deleting a TLA+ property, or dropping a task from the gate breaks
the build until the guarantee that cited it is corrected or withdrawn.

```console
devenv tasks run docs:check
```
