# Failure mode and effects analysis

This system-level failure mode and effects analysis (FMEA) covers the Bond
Exchange domain, API, authentication, request admission and authorization, PostgreSQL persistence,
integration-event delivery, Banxico SIE ingestion, runtime and telemetry
composition, and verification workflow. It evaluates the repository as
implemented on 2026-09-06. It does not assert that the disposable demo is
production-ready or that deployment-owned controls exist.

The FMEA is a prioritization aid, not a quantitative prediction, security
certification, incident log, or acceptance of a high risk. The
[friction register](../FRICTIONS.md) remains the source of truth for verified
unresolved gaps and their completion conditions, and the
[guarantee register](guarantees.md) remains the source of truth for the
properties that currently hold and the evidence behind them; failure modes cite
guarantees as prevention controls rather than restating them. The
[ASVS profile](security/ASVS.md) tracks requirement-level security evidence,
ADRs record durable architecture decisions, and the TLA+ model verifies the
selected domain behavior. Links between them are intentional: update all
affected records together rather than allowing this analysis to become a
parallel source of contradictory requirements.

## Scoring method

Each score describes residual risk after the listed, currently verified
controls. Scores are ordinal; the risk-priority number (RPN) is
`severity × occurrence × detection` and is useful only for ordering work in
this analysis.

| Score | Severity (S) | Occurrence (O) | Detection (D) |
| --- | --- | --- | --- |
| 1 | No material user or system effect. | Prevented by construction in all reviewed paths. | The control prevents the effect or detection is effectively certain before it occurs. |
| 2–3 | Local, reversible degradation. | Unlikely; multiple independent controls must fail. | Automated checks are likely to detect the cause before the effect. |
| 4–6 | Material degraded service, incorrect data, or manual recovery. | Credible under an ordinary fault or foreseeable misuse. | Detection exists but can be late, indirect, or dependent on a caller or operator. |
| 7–8 | Prolonged outage, significant integrity/confidentiality impact, or incorrect business outcome. | Expected under a known gap or recurring operating condition. | Detection is weak, manual, or unable to identify all affected records or users. |
| 9–10 | Catastrophic transaction-integrity, security, legal, or safety consequence. | Present by default or nearly inevitable when the triggering condition exists. | No verified detection, or detection normally occurs only after the full effect. |

RPN 200 or greater is **high**, 100–199 is **medium**, and below 100 is
**monitored**. Any mode with severity 9 or 10 requires explicit review even
when prevention makes its RPN low. Scores must not be reduced without evidence;
production observations may replace the occurrence assumptions when their
scope and measurement window are recorded.

The repository does not define named system owners. Actions therefore name an
accountable area. Assigning people and target dates is required before a
production-readiness decision.

## Risk summary

| ID | Failure mode | S | O | D | RPN | Priority | Accountable area | Status |
| --- | --- | ---: | ---: | ---: | ---: | --- | --- | --- |
| FM-001 | A binding reservation is treated as a settled securities transaction. | 10 | 7 | 8 | 560 | High | Product/domain | Open |
| FM-002 | Unsupported market-integrity behavior is accepted as valid trading. | 9 | 5 | 9 | 405 | High | Product/domain | Partially controlled; policy open |
| FM-003 | More than one buyer acquires the same offer. | 9 | 2 | 2 | 36 | Monitored; severity review | Domain/data | Controlled |
| FM-004 | An unauthorized, altered, or replayed operation changes domain state. | 10 | 2 | 3 | 60 | Monitored; severity review | Security/data | Controlled |
| FM-005 | Direct or alternate writers append facts outside domain constraints. | 8 | 4 | 6 | 192 | Medium | Data/security | Open |
| FM-006 | Unbounded immutable history exhausts storage or degrades queries. | 8 | 6 | 7 | 336 | High | Data/operations | Open |
| FM-007 | Read workloads exhaust database or transport capacity. | 8 | 5 | 6 | 240 | High | API/operations | Open |
| FM-008 | A deployment exposes traffic, credentials, or privileged infrastructure. | 10 | 8 | 8 | 640 | High | Platform/security | Production blocker |
| FM-009 | Verification-key or issuer rotation causes an outage or stale trust. | 8 | 3 | 4 | 96 | Monitored | Identity/operations | Controlled by procedure |
| FM-010 | A committed integration event is never delivered. | 7 | 10 | 9 | 630 | High | Integration/operations | Present by design |
| FM-011 | An integration event is delivered more than once and applied twice. | 7 | 4 | 8 | 224 | High | Integration/consumer | Open external control |
| FM-012 | A stale, future, over-age, corrected, or incorrectly directed FIX rate becomes accepted offer terms. | 9 | 2 | 3 | 54 | Monitored; severity review | Rates/intake | Controlled |
| FM-013 | The reusable Banxico SIE token is disclosed. | 8 | 3 | 6 | 144 | Medium | Platform/security | Deployment action open |
| FM-014 | A high-impact defect or vulnerability remains undetected. | 9 | 5 | 6 | 270 | High | Engineering/security | Open |
| FM-015 | Dependency failure is misclassified as process failure, causing restart churn. | 7 | 3 | 4 | 84 | Monitored | Platform/operations | Controlled by contract |
| FM-016 | A migration loses facts or breaks the previously deployed application. | 10 | 2 | 4 | 80 | Monitored; severity review | Data/release | Controlled by workflow |
| FM-017 | SIE or its cache is unavailable when a seller requests a USD quote. | 5 | 5 | 3 | 75 | Monitored | Rates/operations | Controlled degradation |
| FM-018 | Decimal conversion or rounding creates the wrong MXN terms. | 8 | 2 | 2 | 32 | Monitored | Intake/data | Controlled |
| FM-019 | A quote is accepted by the wrong seller, for changed terms, after expiry, or more than once. | 8 | 2 | 2 | 32 | Monitored | Intake/data | Controlled |
| FM-020 | Legacy non-MXN offers are hidden by the new binary or still served by an old binary during rollout. | 8 | 5 | 4 | 160 | Medium | Release/product | Open disposition |
| FM-021 | Telemetry is silently lost, unsafe, misleading, or unavailable during an incident. | 8 | 5 | 7 | 280 | High | Platform/operations | Open deployment control |
| FM-022 | Per-principal request admission is bypassed, falsely rejects traffic, or creates database contention. | 7 | 3 | 5 | 105 | Medium | API/data/operations | Application-controlled; monitoring open |

## Detailed analysis

### FM-001 — Reservation mistaken for settlement

- **Function and failure:** `Buy` records one immutable binding order or
  reservation, but a caller or downstream process treats success as payment,
  ownership transfer, custody, or final settlement.
- **Effects:** Parties can act on ownership or finality that the system never
  established; no cancellation, expiry, reversal, or settlement recovery path
  exists.
- **Causes:** The operation and persistence table use purchase terminology
  while the intended post-reservation lifecycle and legal meaning are
  undecided.
- **Current controls and detection:** README, database, security, API, and TLA+
  documentation qualify the result as a reservation. There is no runtime
  control that can detect an external semantic over-read.
- **Action:** Decide the lifecycle and terminology in an ADR; then synchronize
  append-only facts, API, Go, PostgreSQL, TLA+, recovery behavior, and tests.
  Until then, treat production transaction claims as blocked.
- **Traceability:** [F-001](../FRICTIONS.md#f-001--buying-stops-at-an-immutable-reservation-p1),
  [database behavior](../db/README.md),
  [formal domain](../spec/tla/README.md), and the reservation boundary stated by
  [G-001](guarantees.md#g-001--at-most-one-buyer-acquires-a-sale-offer).

### FM-002 — Unsupported market-integrity behavior

- **Function and failure:** The service accepts behavior that a real market
  policy could prohibit because many applicable rules remain outside the
  current domain.
- **Effects:** Manipulative, unfunded, or otherwise invalid activity can look
  like a valid reservation, undermining business and audit conclusions.
- **Causes:** Balances, holdings, eligibility, beneficial-owner relationships,
  partial fills, matching, price bands, and price/time priority are
  intentionally out of scope.
- **Current controls and detection:** Same-identity self-trading is prohibited
  by principal-specific discovery, Go transaction logic, a PostgreSQL insert
  trigger, and the TLA+ `NoSelfPurchases` invariant. Negative store, transport,
  direct-SQL, and HTTP tests cover the control; rejected operations retain
  `self_trade_prohibited`, and a bounded metric counts attempts. The occurrence
  and detection scores remain unchanged because other integrity rules and
  production alert ownership are still undecided.
- **Action:** Product and domain owners must decide which additional integrity
  rules this service owns and add invariants, enforcement, negative tests, and
  monitoring for every adopted rule. Define authoritative beneficial-owner or
  affiliation data before claiming cross-principal self-trade prevention.
- **Traceability:** [F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1),
  [ADR-0030](adr/0030-prevent-same-identity-self-trading.md),
  [G-002](guarantees.md#g-002--nobody-buys-their-own-sale-offer), and
  [formal-model scope](../spec/tla/README.md#behavior).

### FM-003 — Double acquisition of one offer

- **Function and failure:** Concurrent requests create more than one purchase
  fact for one sale offer.
- **Effects:** Conflicting buyers appear to hold the same exclusive
  reservation, causing a severe integrity failure and ambiguous recovery.
- **Causes:** Cross-instance races, retry races, or loss of database uniqueness
  and transactional checks.
- **Current controls and detection:** Each purchase has a UUIDv7 primary key,
  while `purchases.sale_offer_uuid` is unique; each authorization decision and insert occurs in one PostgreSQL
  transaction; the losing request reports the offer unavailable. Integration
  tests exercise store-level concurrency, and a generated full-server workload
  requires exactly one success when independent buys contend for one offer. The
  TLA+ model claims a binding order before resolving it, so two authorized
  buyers can hold simultaneous claims against one offer; `AtMostOnePurchasePerOffer`
  is therefore a property of the resolution rule rather than of an atomic
  action, and it was confirmed to fail against a specification whose exclusivity
  guard was weakened.
- **Action:** Preserve the database constraint as the cross-instance authority.
  Re-score and update PostgreSQL tests and TLA+ properties whenever purchase,
  cancellation, partial-fill, or settlement behavior changes. Scores are
  unchanged: the detection score of 2 already assumed the model could
  distinguish double acquisition, and the claim-and-resolve split makes that
  assumption true rather than improving on it.
- **Traceability:** [ADR-0003](adr/0003-use-append-only-postgresql-facts.md),
  [ADR-0027](adr/0027-model-contended-buying-and-revocable-authorization.md),
  [database behavior](../db/README.md),
  [TLA+ verification](../spec/tla/README.md#verification), and
  [G-001](guarantees.md#g-001--at-most-one-buyer-acquires-a-sale-offer).

### FM-004 — Unauthorized, altered, or replayed mutation

- **Function and failure:** A request changes state for the wrong principal,
  permission, operation, payload, client, or idempotency scope, or an exact
  retry creates another fact.
- **Effects:** Unauthorized reservations or offers, duplicate facts, loss of
  non-repudiation, and disclosure through error or audit paths.
- **Causes:** Signature or claims-validation defects, identity accepted from
  input, RBAC evaluated outside the mutation transaction, request/assertion
  binding drift, or idempotency uniqueness loss.
- **Current controls and detection:** Short-lived EdDSA/ES256 assertions bind
  issuer, audience, principal, client, operation, deterministic request digest,
  and a canonical UUIDv4 mutation nonce; assertion `jti` values are also
  UUIDv4. PostgreSQL resolves the principal and checks append-only RBAC in the
  mutation transaction. A unique UUID-backed operation scope and stored digest
  make an exact retry return the prior result and reject changed input. Focused
  negative, integration, race, coverage, mutation, and TLC checks exercise the
  boundary. The full-server REST journey also retries a buy with a freshly
  signed assertion and verifies identity minimization. A readable REST scenario
  (`tests/integration/http/authentication-failures.hurl`) drives the composed
  server through a missing, malformed, foreign-signed, wrong-operation, and
  digest-mismatched assertion, a suspended and an unauthorized principal, and
  every nonce and JSON-envelope rejection, so the composition is verified to
  install the control rather than only the packages that implement it. The demo
  smoke check repeats the digest-mismatch, authorization, and admission
  rejections over native gRPC. Structured security logs
  record safe decision metadata and correlate it with application-owned REST,
  gRPC, authentication, and database spans. Bounded operation, authentication,
  and idempotency-decision metrics exclude audit and request identifiers and
  distinguish claim, replay, conflict, and storage-error paths. In the TLA+
  model, authorization is a view over
  append-only grant, revocation, suspension, and reinstatement facts rather than
  a constant set, so `NewClaimsAreAuthorizedWhenClaimed` requires a revocation
  to take effect on the next claim, `EffectivePermissionsMatchAuthorizationFacts`
  cross-checks the view against the raw facts, and
  `EveryFactHasASucceededOperation` requires that no domain fact exists without
  an authorized, idempotent operation. The model covers mutations only: it
  evaluates authorization in the same step that claims the scope, so it cannot
  detect a check moved outside the mutation transaction, it represents no read
  operation, and it produces only the `offer_unavailable` rejection. Those three
  causes are detected by the Go and PostgreSQL tests alone
  ([F-024](../FRICTIONS.md#f-024--the-model-omits-authorization-timing-reads-and-rejection-paths-p2)).
- **Action:** Preserve these controls and add a negative test for every new
  operation or authentication input. Reassess deployment identity assurance,
  telemetry, and key lifecycle under FM-008 and FM-009. Scores are unchanged;
  the detection score of 3 rests on the transaction-scoped RBAC check and the
  negative tests, with the model contributing revocation semantics rather than
  the timing of the check.
- **Traceability:** [ADR-0009](adr/0009-bind-federated-authorization-to-idempotent-operations.md),
  [ADR-0027](adr/0027-model-contended-buying-and-revocable-authorization.md),
  [ASVS security architecture](security/ASVS.md#security-architecture), and the
  assertion-binding, idempotency, and in-transaction authorization promises of
  [G-007](guarantees.md#g-007--every-request-carries-a-short-lived-assertion-bound-to-one-operation-and-one-body),
  [G-008](guarantees.md#g-008--retrying-a-mutation-repeats-the-outcome-never-the-effect), and
  [G-010](guarantees.md#g-010--authorization-is-decided-inside-the-transaction-that-appends-the-fact).

### FM-005 — Invalid immutable facts from an alternate writer

- **Function and failure:** Direct SQL provisioning or a future writer inserts
  identifiers or currency values that Go rejects, permanently adding a fact
  the application cannot safely interpret.
- **Effects:** Reads or mutations fail, inconsistent records persist, and
  correction is difficult because domain-fact tables reject destructive
  changes.
- **Causes:** Provisioning and security administration currently require direct
  SQL. Storage now rejects the value classes the domain rejects, except that
  `numeric(14,4)` rounds an over-precise price at cast time before any `CHECK`
  observes it.
- **Current controls and detection:** Go validates API inputs, foreign keys and
  PostgreSQL UUID-version checks reject invalid current identifiers and nonces,
  and append-only triggers prevent silent rewriting. Operational legacy aliases
  have been contracted. `sale_offers.currency_code` now requires `^[A-Z]{3}$`,
  canonical terms require MXN, and submission provenance requires MXN or USD.
  A purchase trigger rejects a buyer UUID equal to the referenced offer's
  seller UUID, and a direct-SQL integration test pins that control.
  `TestStorageConstraintsMatchDomainValidation` compares each Go validation rule
  against a direct SQL insert and fails on a disagreement in either direction,
  so constraint drift is detected rather than discovered. The constraint was
  added `NOT VALID` and validated separately, and the validation migration
  refuses to proceed while nonconforming history exists. The TLA+ model requires
  that the published offers and purchased offer IDs are exactly the resources
  named by succeeded operation results, which is the domain-level statement of
  the rule that a successful operation must reference an existing source fact.
  There is still no supported provisioning path and no pre-insert detection for
  privileged SQL, and the model constrains the application's own writers rather
  than an alternate one.
  No constraint relates a principal to the user its facts are attributed to, so
  provisioning can create a principal that authenticates and passes
  authorization but cannot own an offer or a purchase.
- **Action:** Define and test a supported administration workflow, decide
  whether the monetary representation should reject rather than round an
  over-precise price, and state the principal-to-user relationship as an
  enforced constraint. Occurrence drops from 6 to 4 and detection from 8 to 6 on
  the implemented constraints and the equivalence test; severity is unchanged
  because a nonconforming fact remains permanent.
- **Traceability:** [F-003](../FRICTIONS.md#f-003--provisioning-and-security-administration-require-direct-sql-p1),
  [F-004](../FRICTIONS.md#f-004--database-constraints-are-looser-than-domain-validation-p1),
  [F-023](../FRICTIONS.md#f-023--the-model-conflates-principal-and-user-identity-p2),
  [ADR-0023](adr/0023-align-storage-constraints-with-domain-validation.md),
  [ADR-0030](adr/0030-prevent-same-identity-self-trading.md), and the
  writer-independent controls behind
  [G-005](guarantees.md#g-005--domain-facts-are-append-only-corrections-are-new-facts).

### FM-006 — Storage or query exhaustion from retained history

- **Function and failure:** Append-only domain, authorization, operation,
  integration-event, SIE import, and rate-revision data grows until storage,
  backups, migrations, or queries become unavailable or miss service targets.
- **Effects:** Mutations fail, reads and recovery slow down, audit availability
  is lost, and legal retention or erasure obligations cannot be demonstrated.
- **Causes:** The repository defines no capacity, retention, partitioning,
  protected-backup, or erasure policy for the retained facts. ADR-0033 retired
  expired pre-UUID identifier evidence, but that bounded decision does not
  establish a lifecycle for the remaining records.
- **Current controls and detection:** Purpose-built indexes exist for current
  queries and the schema preserves provenance. Native pgx pool metrics and
  query spans expose per-instance pressure when OTLP is configured. No
  repository-owned shared-database capacity thresholds, alerts, or lifecycle
  mechanism detect or prevent exhaustion.
- **Action:** Establish measured growth and service objectives, capacity alerts,
  protected backup/restore tests, and a reviewed retention or archival design
  that preserves required append-only facts and correction history.
- **Traceability:** [F-005](../FRICTIONS.md#f-005--append-only-and-event-delivery-data-has-no-lifecycle-policy-p1)
  and [ASVS pending decisions](security/ASVS.md#pending-non-code-and-deployment-decisions).

### FM-007 — Read-driven resource exhaustion

- **Function and failure:** Slow or high-cardinality reads occupy all 20 pooled
  database connections, retain repeatable-read snapshots, exceed gRPC message
  size, or create REST/gRPC behavior divergence.
- **Effects:** Reads and mutations time out or become unavailable; long-lived
  snapshots impede database maintenance; clients receive partial streams or a
  transport-specific failure.
- **Causes:** Active-offer listing streams every match without a count bound,
  while active-series discovery collects all results into one unary response.
  There is no per-principal concurrency or result-cardinality limit. The
  authenticated request-rate limit does not bound the duration or resource
  cost of an admitted read.
- **Current controls and detection:** Streaming applies backpressure and closes
  rows and transactions on cancellation; server input/message sizes, HTTP
  timeouts, gRPC concurrent streams, and the pool are bounded. PostgreSQL also
  admits at most 100 requests per authenticated principal in each fixed UTC
  minute across instances. Those ceilings limit individual resources but do
  not guarantee fair or complete service. A
  generated REST workload records latency, errors, and status distributions for
  populated offer books, but has no production threshold. Standard HTTP/gRPC
  metrics with bounded routes, pgx pool metrics, policy-aligned latency
  histograms, and a bounded emitted-offer histogram expose request and stream
  pressure when OTLP is configured.
  `TestStreamActiveOffersReleasesConnectionsOnEveryExit` proves that a stream
  releases its connection, snapshot, and rows on normal completion, on an
  abandoning reader, and on mid-stream cancellation, repeating each exit past
  the pool size against a deliberately bounded pool so a leak blocks rather than
  hides. `TestConcurrentSlowReadersDoNotExhaustThePool` runs twice as many slow
  readers as connections and requires that saturation makes them queue rather
  than fail. Both are bounded-pool tests, not production capacity claims, and
  neither exercises the gRPC transport.
- **Action:** Choose bounded pagination/snapshot semantics or measured hard
  limits shared by both transports, then add cross-transport cancellation tests,
  production-scale load thresholds, and pool/snapshot saturation alerts. Note
  that a connection cancelled mid-query is destroyed rather than returned, so a
  burst of cancellations churns the pool; that cost is unmeasured. Occurrence
  drops from 6 to 5 and detection from 7 to 6 on the release and saturation
  tests; severity is unchanged because no bound on result cardinality exists.
- **Traceability:** [F-006](../FRICTIONS.md#f-006--read-apis-have-unbounded-resource-use-p1)
  and [F-007](../FRICTIONS.md#f-007--swagger-does-not-describe-the-rest-stream-on-active-offers-p2).

### FM-008 — Unsafe production deployment boundary

- **Function and failure:** A production-like deployment exposes plaintext
  listeners, trusts an incorrect ingress or workload identity, over-privileges
  database roles, mishandles secrets, or lacks telemetry and rate controls.
- **Effects:** Credential or transaction disclosure, unauthorized access,
  tampering, denial of service, loss of audit evidence, or compromise of the
  database and external provider account.
- **Causes:** The repository intentionally ships only loopback plaintext
  listeners and no production package; multiple ASVS controls depend on an
  unspecified hosting and external-identity architecture. Application request
  admission starts only after a valid principal is established.
- **Current controls and detection:** The default binds to loopback, application
  authentication always runs, inputs are bounded, errors are minimized, and
  the README states required boundaries. Application-owned OTLP traces and
  metrics, correlated JSON logs, a loopback collector, and contract/privacy
  tests cover the development boundary. These controls neither build nor
  verify protected production telemetry or another production boundary.
- **Action:** Keep production use blocked until owners define and test TLS,
  workload identity, ingress trust, unauthenticated and connection-level
  limits, secret delivery/rotation,
  least-privilege database roles, backups, availability, and protected
  telemetry. Record material decisions in ADRs and replace affected pending
  ASVS dispositions with deployable evidence.
- **Traceability:** [F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1)
  and [ASVS pending decisions](security/ASVS.md#pending-non-code-and-deployment-decisions).

### FM-009 — Verification-key or issuer rotation failure

- **Function and failure:** A verification-key or issuer change rejects valid
  clients during rotation or continues trusting a key that must be revoked.
- **Effects:** All authenticated operations can become unavailable, or a
  compromised signer can remain effective until every process is replaced.
- **Causes:** One issuer, audience, and local JWKS are loaded only at startup;
  there is no refresh or multi-issuer trust configuration, so rotation depends
  on correctly sequenced process replacement.
- **Current controls and detection:** Startup fails closed for missing or
  malformed configuration and validates public signing keys, unique key IDs,
  algorithms, and use; a key set with duplicate key IDs is refused, which is
  what makes an overlap window unambiguous. ADR-0024 publishes the three-step
  rotation procedure and its overlap sizing, and
  `TestVerificationKeyRotationOverlap` executes those steps as three key sets,
  asserting which signer each accepts. `TestVerificationKeySetRejectsDuplicateKeyIDs`
  pins the startup refusal. Configuration parsing and key-set loading are
  covered by `internal/serverruntime` tests rather than only by a running
  process.
- **Action:** Rehearse the procedure against the real deployment tooling once a
  deployment exists, and revisit bounded refresh if emergency revocation must be
  faster than a restart. Occurrence drops from 5 to 3 and detection from 5 to 4
  on the published, tested procedure; severity is unchanged because a mistaken
  rotation still affects every authenticated operation.
- **Traceability:** [ADR-0024](adr/0024-define-probe-and-key-rotation-contracts.md)
  and [key rotation](operations.md#assertion-verification-keys).

### FM-010 — Committed event remains undelivered

- **Function and failure:** A successful offer or reservation commits its event
  reference, but no destination receives the event.
- **Effects:** Downstream audit, notification, or integration state remains
  absent or stale indefinitely despite a successful API result.
- **Causes:** The checked-in runtime configures no publisher. With a future
  publisher, a crash or publisher fault leaves work pending until an authorized
  operator explicitly invokes recovery; there is no startup drain, worker,
  schedule, backlog alert, or runbook.
- **Current controls and detection:** A database trigger atomically records a
  minimal reference with the successful operation result. Per-destination
  delivery state, leases, error classes, and an authenticated manual recovery
  operation support bounded retries. Metrics expose that zero publishers are
  configured and classify scan, claim, load, publish, delivery-state, and
  claimed-attempt outcomes, including failures before a claim, but no current
  destination or continuously monitored shared backlog detects an undelivered
  event.
- **Action:** Deploy a reviewed destination and authenticated transport,
  backlog monitoring, payload approval, recovery ownership and runbook, and
  tested retry behavior; alternatively accept database-only retention in an
  ADR and remove outbound-delivery expectations.
- **Traceability:** [F-017](../FRICTIONS.md#f-017--integration-event-recovery-is-manual-and-has-no-destination-p2),
  [ADR-0011](adr/0011-use-minimal-transactional-event-references.md), and
  [G-014](guarantees.md#g-014--integration-event-delivery-is-not-guaranteed),
  which records the absence of a delivery guarantee where a reader is most
  likely to assume one.

### FM-011 — Duplicate integration-event effect

- **Function and failure:** A destination accepts an event, the service loses
  the acknowledgement, and recovery publishes it again; the consumer applies
  the duplicate as new work.
- **Effects:** Duplicate notifications, audit records, reservations, or other
  destination-specific side effects occur.
- **Causes:** Delivery is intentionally at least once, and consumer
  deduplication is outside this repository and unverified.
- **Current controls and detection:** The immutable event UUIDv7 is a stable
  deduplication key, independently of its source reference. UUIDv4 delivery
  leases prevent concurrent
  intentional sends, but cannot resolve an ambiguous external acknowledgement.
  Delivery spans and bounded stage/outcome/error-class metrics are ready for a
  future publisher but have no consumer-side evidence.
- **Action:** Make idempotent consumer handling by event UUID an
  acceptance criterion for every destination; exercise acknowledgement-loss
  and replay tests and monitor duplicate outcomes before enabling it.
- **Traceability:** [F-017](../FRICTIONS.md#f-017--integration-event-recovery-is-manual-and-has-no-destination-p2)
  and [ADR-0011](adr/0011-use-minimal-transactional-event-references.md).

### FM-012 — Stale, unavailable, or misdirected exchange rate

- **Function and failure:** Offer intake accepts a stale, future, over-age,
  corrected, or incorrectly directed observation as the rate for USD-to-MXN
  terms.
- **Effects:** The seller accepts and a buyer can reserve an economically
  incorrect MXN offer. The core terms do not change afterward, so the error is
  durable rather than corrected by a later read.
- **Causes:** SIE and PostgreSQL can be unavailable; latest values intentionally
  permit explicit stale fallback; a provider correction can append after a
  quote; and business-calendar policy is not represented by the generic rate
  cache.
- **Current controls and detection:** Intake requests only `SF43718` with a
  fixed USD/MXN mapping, refuses the rate service's stale flag, pins an exact
  append-only revision, rejects zero/future dates and observations older than
  seven days, and persists the rate and resulting quote before acceptance.
  Strict SIE parsing, exact decimal persistence, focused rejection tests, the
  full-server USD journey, and immutable provenance make the selected inputs
  detectable after the fact. Separate provider-request and work-unit metrics,
  provider latency and safe outcome classes, cache/skip outcomes, bounded
  observation-validation results, and accepted observation age are emitted
  without financial terms or dynamic identifiers. A later correction does not
  mutate an accepted quote.
- **Action:** Before production, assign rate-policy ownership, confirm the
  seven-day ceiling against bank holidays and applicable trading rules, alert
  on stale/over-age rejection and corrections affecting unexpired quotes, and
  define whether an administered business-date rate must replace on-demand
  latest FIX.
- **Traceability:** [ADR-0014](adr/0014-persist-and-coordinate-banxico-sie-exchange-rates.md)
  [ADR-0019](adr/0019-canonicalize-sale-offers-to-mxn-at-intake.md), and
  [rate behavior](exchange-rates.md).

### FM-013 — Banxico SIE token disclosure

- **Function and failure:** The reusable provider credential appears in a URL,
  log, error, database row, fixture, artifact, or exposed runtime secret.
- **Effects:** Unauthorized quota consumption, loss of provider availability,
  incident response and rotation, and possible compromise of the provider
  account boundary.
- **Causes:** Unsafe secret injection, outbound request or error changes,
  recording mistakes, provider reflection, or overly broad access to runtime
  configuration and artifacts.
- **Current controls and detection:** The client accepts a 64-character token
  at construction, sends it only in `Bmx-Token` to a fixed HTTPS origin, and
  excludes it from persistence and errors. Live recording is explicit, rejects
  reflected credentials, redacts request authentication, limits headers, and
  writes atomically; ordinary tests use offline fixtures. The instrumented HTTP
  transport is tested not to propagate trace or baggage headers to Banxico, and
  telemetry contract tests reject credential-bearing output. Production secret
  delivery, access, and rotation are not defined.
- **Action:** Define a least-privilege secret store, injection and rotation
  procedure, egress policy, log/artifact scanning, response plan, and owner as
  part of FM-008; retain credential non-disclosure tests for client changes.
- **Traceability:** [ADR-0014](adr/0014-persist-and-coordinate-banxico-sie-exchange-rates.md)
  and [ASVS pending decisions](security/ASVS.md#pending-non-code-and-deployment-decisions).

### FM-014 — Undetected high-impact defect or vulnerability

- **Function and failure:** A security or correctness defect reaches the main
  branch or remains present after a dependency vulnerability is disclosed.
- **Effects:** Any domain, confidentiality, integrity, or availability failure
  can persist without a bounded discovery or remediation start time.
- **Causes:** A raw Go test invocation can skip PostgreSQL; server composition
  has limited direct failure-path tests; and current coverage metrics exclude
  `application/cmd/`. Scheduled scanning now bounds disclosure-to-confirmation
  time, but a scan cannot detect a defect class it does not analyze.
- **Current controls and detection:** `devenv test` composes formatting, static
  analysis, API generation checks, migration, archival and lifecycle checks,
  demo smoke, readable full-server REST scenarios, a generated load check, race
  tests, at least 95% internal-package statement coverage and mutation efficacy,
  `govulncheck`, ASVS evidence checks, and TLC. It reaches those gates only
  through the `dev:ci` and `go:mutation` aggregates, which the Go quality
  workflow runs as separate jobs; `dev:check` fails when a gate attaches itself
  to the test entry point without joining an aggregate, so a gate cannot run
  locally while being absent from CI. The PostgreSQL integration tests fail
  rather than skip when `CI` is set, and `security:check` refuses to run outside
  the migrated-database harness. A scheduled workflow runs `security:check`
  daily and on demand, retains the module inventory for 90 days, and opens or
  updates a tracking issue on failure, so confirmation of a disclosed Go
  dependency vulnerability is bounded to one day rather than to the next
  matching change. `docs:check` verifies that documentation links, anchors,
  indexes, and register identifiers still resolve, and the ASVS profile is
  compared against its pinned upstream source, so security evidence cannot decay
  silently. Environment parsing, verification-key loading, pool and transport
  limits, listener binding including partial-startup release, and graceful and
  forced shutdown now live in `internal/serverruntime`, so the coverage and
  mutation gates measure them; `application/cmd/server` retains only wiring. A
  dedicated observability gate validates collector configuration, OTLP
  export/flush, propagation, bounded labels and routes, and credential
  exclusion; a focused command test covers instrumented gRPC composition.
- **Action:** Keep remediation evidence with the response ownership recorded in
  `SECURITY.md` and `.github/CODEOWNERS`. Detection improves from 8 to 6 on the
  implemented scan cadence and reporting path; severity and occurrence are
  unchanged because scanning cannot find a defect class it does not analyze, and
  because the composition wiring that remains in the command package is still
  measured by the demo smoke, integration scenarios, and a focused gRPC test.
  Re-score again once the schedule has operational history, and note that GitHub
  suspends scheduled workflows in an idle repository.
- **Traceability:** [F-015](../FRICTIONS.md#f-015--the-default-contributor-path-can-silently-reduce-test-coverage-p3),
  [ADR-0013](adr/0013-require-95-percent-test-quality-gates.md),
  and [ADR-0021](adr/0021-schedule-security-scanning-and-name-a-response-owner.md).

### FM-015 — Dependency failure causes restart churn

- **Function and failure:** An orchestrator uses the authenticated, database-
  dependent health operation as liveness and repeatedly restarts an otherwise
  functioning process during identity or PostgreSQL failure.
- **Effects:** A dependency incident is amplified into process churn, slower
  recovery, avoidable load, and loss of diagnostic continuity.
- **Causes:** The only health operation checks authorization and database
  readiness. Without a stated contract an orchestrator can reasonably wire it to
  a liveness probe.
- **Current controls and detection:** Startup pings PostgreSQL and the demo smoke
  test verifies health and shutdown. ADR-0024 states the contract: `CheckHealth`
  is readiness and must never drive a restart, while liveness is a TCP
  connection to either listener, which needs no credential and no database. The
  two dependency failures stay distinguishable at the API and are covered by
  `internal/rpcapi` tests: a database failure returns `Unavailable` and an
  authorization failure returns `PermissionDenied`. HTTP/gRPC and operation
  telemetry can correlate readiness latency and failures when a collector
  exists.
- **Action:** Apply the contract in whatever deployment manifests are eventually
  written, and revisit if a wedged-but-listening process is ever observed, since
  a TCP check cannot detect one. Occurrence drops from 5 to 3 on the published
  contract; detection is unchanged because the repository cannot observe how an
  orchestrator is configured.
- **Traceability:** [ADR-0024](adr/0024-define-probe-and-key-rotation-contracts.md)
  and [health probes](operations.md#health-probes).

### FM-016 — Lossy or incompatible database migration

- **Function and failure:** A forward migration loses immutable facts or makes
  the previously deployed application fail during a rolling release.
- **Effects:** Irrecoverable transaction/audit loss, service outage, or mixed-
  version corruption across instances.
- **Causes:** Rewriting an applied migration, unapproved destructive
  contraction, new required privileges or columns without expansion, unsafe
  backfill, or use of a destructive down migration. A retention-authorized
  deletion can still destroy the wrong data or run without its required
  recovery evidence.
- **Current controls and detection:** Repository guidance requires timestamped
  dbmate migrations, lossless backward-compatible expand/backfill/contract
  changes, corrective roll-forward, and separately owned migrations. Fresh
  isolated PostgreSQL 18 database and lifecycle checks exercise the full
  history; schema tests verify the server major version and all 24 retained
  UUID primary keys and reject the retired archive, reviewed legacy columns,
  synchronization machinery, and transitional views. ADR-0033 requires an
  accepted retention decision and verified backup for its narrow deletion.
  Append-only triggers prevent ordinary fact mutation. These checks do not
  simulate every production dataset, PostgreSQL major upgrade, backup restore,
  direct writer, or mixed-version rollout. The 10/2/4 score remains unchanged:
  retiring the historical fixture narrows evidence for a property that is no
  longer required, while the full-history and final-schema checks retain the
  same pre-release detection level for the supported schema.
- **Action:** Preserve the guardrails; for every schema change, test the prior
  application against the expanded schema and representative existing data and
  document rollout/rollback-forward steps. For an explicitly retention-
  authorized deletion, verify its exact target and backup recovery evidence
  before production execution.
- **Traceability:** [ADR-0004](adr/0004-use-dbmate-for-database-migrations.md),
  [ADR-0017](adr/0017-use-postgresql-18-uuidv7-identities-and-uuidv4-nonces.md),
  [ADR-0018](adr/0018-contract-the-legacy-identifier-graph.md),
  [ADR-0033](adr/0033-retire-legacy-identifier-evidence-and-transition-tooling.md),
  [database migration policy](../db/README.md),
  [repository guardrails](../AGENTS.md#architectural-guardrails), and
  [G-013](guarantees.md#g-013--migrations-roll-forward-and-stay-compatible-with-the-deployed-application).

### FM-017 — USD quotation is unavailable

- **Function and failure:** A seller cannot obtain a USD-to-MXN quote while
  SIE, PostgreSQL rate state, the token, or outbound network is unavailable, or
  while the only stored observation is stale or over age.
- **Effects:** New USD submissions stop. Sellers must retry later or submit
  governed MXN terms; existing MXN offers, listings, and reservations continue.
- **Causes:** Provider outage or rate limit, invalid/rotated credential, network
  denial, cold cache, lease contention, persistence failure, or deliberate
  rejection by the observation-age policy.
- **Current controls and detection:** The quote endpoint returns a specific
  unavailable status and never creates an offer without an accepted rate.
  Provider leases, stale fallback metadata, cooldowns, timeouts, durable cache,
  and exact quote replay bound upstream work. Rate request, work-unit, skip,
  cache, safe provider-outcome, and observation-validation metrics plus provider
  spans and accepted-observation age improve diagnosis when exported.
  The dependency is composed only
  into intake, so the core has no rate call on create-MXN, list, or buy paths.
- **Action:** Define an availability objective and alerts for quote rejection,
  cache age, token authentication, rate limits, and lease recovery. If USD
  intake needs higher availability, consider a scheduled administered-rate
  feed or asynchronous pending submissions as evaluated in ADR-0019.
- **Traceability:** [ADR-0019](adr/0019-canonicalize-sale-offers-to-mxn-at-intake.md)
  and [F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1).

### FM-018 — Incorrect conversion arithmetic or rounding

- **Function and failure:** USD is divided instead of multiplied, binary
  floating point changes the value, rounding occurs more than once, or a
  different scale/tie policy is applied between quote, storage, and response.
- **Effects:** The immutable MXN offer differs from the terms the seller should
  have accepted, producing financial and reconciliation errors.
- **Causes:** Direction confusion, numeric type conversion, policy drift, or an
  alternate writer that bypasses intake.
- **Current controls and detection:** The mapping is fixed as MXN per USD;
  `shopspring/decimal` and PostgreSQL numeric avoid binary floating point;
  intake multiplies once and applies half-to-even at scale four; the quote
  stores both amounts, revision, and named rounding policy. Unit tests verify a
  nontrivial exact conversion, while PostgreSQL and full-server tests compare
  the accepted and served MXN amount.
- **Action:** Preserve golden boundary/tie cases and reconcile a sample of
  production quotes against an independently governed calculation. Alternate
  writers remain governed by FM-005.
- **Traceability:** [ADR-0005](adr/0005-use-shopspring-decimal-for-monetary-amounts.md)
  and [ADR-0019](adr/0019-canonicalize-sale-offers-to-mxn-at-intake.md).

### FM-019 — Invalid quote acceptance

- **Function and failure:** A quote is accepted by another principal, for a
  different bond or USD amount, after expiry, more than once, or with a changed
  request under the same idempotency scope.
- **Effects:** A seller receives terms they did not accept, or one accepted
  conversion produces multiple active offers.
- **Causes:** Trusting client-carried quote contents, checking outside the
  create transaction, missing uniqueness, clock/expiry mistakes, or weak
  idempotency binding.
- **Current controls and detection:** Clients carry only the UUID. A
  serializable PostgreSQL transaction verifies the persisted quote's principal,
  bond, exact submitted amount, USD currency, database-time expiry, and absence
  of a provenance use. The provenance quote reference is unique. Operation
  claims bind the deterministic request digest and exact retries recover one
  result. Unit and integration tests cover requirements, UUID shape, replay,
  provenance, and quote reuse. A readable REST scenario
  (`tests/integration/http/offer-intake-failures.hurl`) additionally drives the
  composed server through a missing quote, a quote on an MXN submission, a
  malformed quote identity, a changed amount, another principal's quote, a
  reused quote, and a conflicting reuse of a spent nonce.
- **Action:** Add clock-boundary cases if quote TTL becomes configurable at
  runtime; monitor rejected acceptance without logging financial request
  contents. Scores are unchanged: the end-to-end scenario confirms the composed
  boundary but adds no control the serializable acceptance check did not
  already provide.
- **Traceability:** [ADR-0009](adr/0009-bind-federated-authorization-to-idempotent-operations.md)
  and [ADR-0019](adr/0019-canonicalize-sale-offers-to-mxn-at-intake.md).

### FM-020 — Legacy non-MXN offer rollout ambiguity

- **Function and failure:** The expanded database contains historical non-MXN
  offers. The new application cannot infer seller consent or a historical
  conversion and hides them, while a previously deployed application can still
  serve them through the compatibility view during a rolling release.
- **Effects:** Sellers can lose visibility or liquidity for existing offers;
  mixed-version traffic can violate the MXN-only serving claim; operators may
  be tempted to destructively reprice immutable facts.
- **Causes:** Earlier behavior allowed arbitrary currencies, and a safe
  conversion needs a seller decision that no migration can invent.
- **Current controls and detection:** The migration preserves every source fact,
  backfills only unambiguous MXN rows, and does not break the old view. New list
  and buy queries require one canonical MXN fact and therefore fail closed.
  Canonical and provenance tables are append-only. A read-only readiness gate
  rejects active or purchased rows without canonical terms and validates
  provenance mappings. It runs through the `dev:ci` aggregate locally and in
  continuous integration; process-drain evidence and seller disposition are
  still external.
- **Action:** Inventory active rows without canonical terms, notify their
  sellers, implement an authorized accept/relist or retirement fact, and drain
  old binaries before declaring the control active. Release evidence must show
  that no instance or sanctioned reader serves the compatibility view.
- **Traceability:** [ADR-0019](adr/0019-canonicalize-sale-offers-to-mxn-at-intake.md),
  [F-018](../FRICTIONS.md#f-018--legacy-non-mxn-offers-have-no-seller-disposition-workflow-p1),
  and [database behavior](../db/README.md).

### FM-021 — Telemetry is lost, unsafe, or misleading

- **Function and failure:** Traces, metrics, or correlated logs are silently
  dropped, duplicated, mislabeled, exposed to an unauthorized destination, or
  unavailable when an incident needs them.
- **Effects:** Operators miss or misdiagnose transaction, dependency, capacity,
  and security failures; unsafe attributes can disclose protected operational
  or audit data; misleading telemetry can trigger harmful remediation.
- **Causes:** Collector or exporter outage, invalid credentials or TLS,
  incompatible instrumentation, duplicate automatic and native probes,
  excessive cardinality, unreviewed sampling, queue exhaustion, clock skew, or
  missing alert and retention ownership.
- **Current controls and detection:** The application owns one native OTLP
  pipeline with bounded queues and shutdown flush, uses stable route and metric
  dimensions plus unit-appropriate histogram boundaries, keeps Banxico outside
  the propagation domain, and logs safe
  exporter error types without failing business traffic. Deterministic tests
  inspect spans, metric metadata, values, boundaries and labels, recovered-panic
  completion, log correlation, OTLP/HTTP export, secret exclusion, and both
  REST and gRPC propagation and metric dimensions. Devenv pins and validates a
  loopback collector. There is no protected production backend, collector
  self-alert, delivery SLO, clock evidence, or tested retention and access
  policy, so scores are not reduced.
- **Action:** Assign a platform owner; deploy authenticated encrypted export and
  protected storage; define sampling, retention, access, and overhead budgets;
  alert on collector refusals, queue saturation, drops, and signal absence; and
  exercise collector outage, recovery, and incident-query runbooks before
  production use.
- **Traceability:** [ADR-0025](adr/0025-own-application-opentelemetry-instrumentation.md),
  [observability contract](observability.md), and
  [F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1).

### FM-022 — Incorrect or contended per-principal request admission

- **Function and failure:** Authenticated request admission allows more than
  100 requests for one principal in a database-clock UTC minute, rejects a
  request that remains within the allowance, or serializes enough work on its
  coordination row to degrade database service.
- **Effects:** One authenticated principal can consume disproportionate
  capacity; legitimate reads, mutations, probes, or recovery operations can be
  denied; or admission contention can delay otherwise independent traffic.
- **Causes:** A non-atomic counter, process-local state, an incorrect principal
  key, database clock or window-boundary assumptions, SQL/schema drift, pool or
  database failure, or a hot principal repeatedly contending on one row. The
  deliberate fixed-window design can admit traffic immediately before and
  after a boundary and does not limit unauthenticated traffic or concurrency.
- **Current controls and detection:** JWT validation and principal resolution
  precede one shared RPC-adapter check. A unique PostgreSQL principal key and
  atomic database-time upsert coordinate stateless instances; a constraint
  caps stored counts at 100; rejected attempts do not increment the counter;
  and coordination failure fails closed. PostgreSQL integration tests race 140
  attempts through separate pools and require exactly 100 admissions, prove
  independent principals and next-window reset, and transport tests verify
  application work is skipped plus gRPC/REST retry contracts. The REST
  integration test preloads a dedicated principal's window and verifies the
  `429` and bounded `Retry-After` end to end; the demo smoke check verifies the
  gRPC `ResourceExhausted` and `google.rpc.RetryInfo` counterpart. The generated
  workload adds `denied` and `invalid-assertion` phases that record the cost of
  requests refused after and before admission respectively. A bounded
  decision counter, admission-duration histogram, and protected security log
  expose rejection, error, and coordination-latency outcomes, but no production
  backend, threshold, representative contention measurement, or contention SLO
  exists.
- **Action:** Measure admission-query and row-lock latency under representative
  multi-principal and hot-principal workloads; alert on coordination errors,
  unexpected rejection rates, pool saturation, and lock waits; retain
  deployment ingress controls; and adopt a rolling-window design in a new ADR
  if the documented boundary burst is unacceptable.
- **Traceability:** [ADR-0028](adr/0028-coordinate-per-principal-request-rate-limits-in-postgresql.md),
  [F-006](../FRICTIONS.md#f-006--read-apis-have-unbounded-resource-use-p1),
  [F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1),
  [observability contract](observability.md), and
  [G-011](guarantees.md#g-011--at-most-100-requests-per-principal-per-minute).

## Maintenance and review procedure

Review this document as part of every planned change and after incidents,
control failures, new production measurements, or dependency/deployment
changes. At minimum:

1. identify affected functions, boundaries, failure modes, and linked
   frictions before implementation;
2. update effects and causes, then list only controls that have implementation,
   test, monitoring, or documented operational evidence;
3. re-score residual severity, occurrence, and detection after those controls,
   calculate the RPN, and preserve explicit review for severity 9 or 10;
4. synchronize required actions and statuses with `FRICTIONS.md`,
   [`guarantees.md`](guarantees.md), ASVS, ADRs, READMEs, TLA+, migrations,
   configuration, and CI as applicable — a control strong enough to cite here
   as prevention usually belongs in the guarantee register, and a weakened
   control must be withdrawn from it; and
5. update the assessed date in the opening scope statement and record the
   focused checks used to validate changed controls in the change handoff.

Add new modes with the next `FM-NNN` identifier. Keep a controlled mode in the
analysis so later changes do not accidentally remove its control. Remove a mode
only when its function or boundary has been removed, and never reuse its ID for
another risk. Before production readiness, every high-priority open action must
have a named owner, target date, measurable completion evidence, and an explicit
decision to mitigate, avoid, transfer, or accept the remaining risk.
