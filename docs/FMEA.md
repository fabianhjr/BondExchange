# Failure mode and effects analysis

This system-level failure mode and effects analysis (FMEA) covers the Bond
Exchange domain, API, authentication and authorization, PostgreSQL persistence,
integration-event delivery, Banxico SIE ingestion, runtime composition, and
verification workflow. It evaluates the repository as implemented on
2026-09-04. It does not assert that the disposable demo is production-ready or
that deployment-owned controls exist.

The FMEA is a prioritization aid, not a quantitative prediction, security
certification, incident log, or acceptance of a high risk. The
[friction register](../FRICTIONS.md) remains the source of truth for verified
unresolved gaps and their completion conditions. The
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
| FM-002 | Unsupported market-integrity behavior is accepted as valid trading. | 9 | 5 | 9 | 405 | High | Product/domain | Open |
| FM-003 | More than one buyer acquires the same offer. | 9 | 2 | 2 | 36 | Monitored; severity review | Domain/data | Controlled |
| FM-004 | An unauthorized, altered, or replayed operation changes domain state. | 10 | 2 | 3 | 60 | Monitored; severity review | Security/data | Controlled |
| FM-005 | Direct or alternate writers append facts outside domain constraints. | 8 | 6 | 8 | 384 | High | Data/security | Open |
| FM-006 | Unbounded immutable history exhausts storage or degrades queries. | 8 | 6 | 7 | 336 | High | Data/operations | Open |
| FM-007 | Read workloads exhaust database or transport capacity. | 8 | 6 | 7 | 336 | High | API/operations | Open |
| FM-008 | A deployment exposes traffic, credentials, or privileged infrastructure. | 10 | 8 | 8 | 640 | High | Platform/security | Production blocker |
| FM-009 | Verification-key or issuer rotation causes an outage or stale trust. | 8 | 5 | 5 | 200 | High | Identity/operations | Open |
| FM-010 | A committed integration event is never delivered. | 7 | 10 | 9 | 630 | High | Integration/operations | Present by design |
| FM-011 | An integration event is delivered more than once and applied twice. | 7 | 4 | 8 | 224 | High | Integration/consumer | Open external control |
| FM-012 | A stale, unavailable, or incorrectly mapped SIE rate is consumed as current. | 6 | 4 | 4 | 96 | Monitored | Rates/caller | Controlled within current scope |
| FM-013 | The reusable Banxico SIE token is disclosed. | 8 | 3 | 6 | 144 | Medium | Platform/security | Deployment action open |
| FM-014 | A high-impact defect or vulnerability remains undetected. | 9 | 5 | 8 | 360 | High | Engineering/security | Open |
| FM-015 | Dependency failure is misclassified as process failure, causing restart churn. | 7 | 5 | 4 | 140 | Medium | Platform/operations | Open |
| FM-016 | A migration loses facts or breaks the previously deployed application. | 10 | 2 | 4 | 80 | Monitored; severity review | Data/release | Controlled by workflow |

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
  [database behavior](../db/README.md), and
  [formal domain](../spec/tla/README.md).

### FM-002 — Unsupported market-integrity behavior

- **Function and failure:** The service accepts behavior that a real market
  policy could prohibit, including a seller buying their own offer, because no
  applicable rule exists in the current domain.
- **Effects:** Manipulative, unfunded, or otherwise invalid activity can look
  like a valid reservation, undermining business and audit conclusions.
- **Causes:** Balances, holdings, eligibility, self-trade prevention, partial
  fills, matching, and price/time priority are intentionally out of scope.
- **Current controls and detection:** The limited behavior is explicit in the
  formal model and documentation. No control detects activity against a policy
  that has not been defined.
- **Action:** Product and domain owners must decide which integrity rules this
  service owns and add invariants, enforcement, negative tests, and monitoring
  for every adopted rule.
- **Traceability:** [F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1)
  and [formal-model scope](../spec/tla/README.md#behavior).

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
  requires exactly one success when independent buys contend for one offer.
  TLC checks that an offer cannot be both active and purchased or purchased
  twice.
- **Action:** Preserve the database constraint as the cross-instance authority.
  Re-score and update PostgreSQL tests and TLA+ invariants whenever purchase,
  cancellation, partial-fill, or settlement behavior changes.
- **Traceability:** [ADR-0003](adr/0003-use-append-only-postgresql-facts.md),
  [database behavior](../db/README.md), and
  [TLA+ verification](../spec/tla/README.md#verification).

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
  signed assertion and verifies identity minimization; structured security logs
  record safe decision metadata.
- **Action:** Preserve these controls and add a negative test for every new
  operation or authentication input. Reassess deployment identity assurance,
  telemetry, and key lifecycle under FM-008 and FM-009.
- **Traceability:** [ADR-0009](adr/0009-bind-federated-authorization-to-idempotent-operations.md)
  and [ASVS security architecture](security/ASVS.md#security-architecture).

### FM-005 — Invalid immutable facts from an alternate writer

- **Function and failure:** Direct SQL provisioning or a future writer inserts
  identifiers or currency values that Go rejects, permanently adding a fact
  the application cannot safely interpret.
- **Effects:** Reads or mutations fail, inconsistent records persist, and
  correction is difficult because domain-fact tables reject destructive
  changes.
- **Causes:** Provisioning and security administration currently require direct
  SQL, while several database constraints are looser than service validation.
- **Current controls and detection:** Go validates API inputs, foreign keys and
  PostgreSQL UUID-version checks reject invalid current identifiers and nonces,
  and append-only triggers prevent silent rewriting. Legacy text aliases and
  currency constraints remain looser, there is no supported provisioning path
  or complete storage-level constraint equivalence, and no pre-insert detection
  for privileged SQL.
- **Action:** Define and test a supported administration workflow and add a
  backward-compatible migration that aligns storage constraints with every
  sanctioned writer after safely classifying existing facts.
- **Traceability:** [F-003](../FRICTIONS.md#f-003--provisioning-and-security-administration-require-direct-sql-p1)
  and [F-004](../FRICTIONS.md#f-004--database-constraints-are-looser-than-domain-validation-p1).

### FM-006 — Storage or query exhaustion from retained history

- **Function and failure:** Append-only domain, authorization, operation,
  integration-event, SIE import, and rate-revision data grows until storage,
  backups, migrations, or queries become unavailable or miss service targets.
- **Effects:** Mutations fail, reads and recovery slow down, audit availability
  is lost, and legal retention or erasure obligations cannot be demonstrated.
- **Causes:** The repository defines no capacity, retention, partitioning,
  archival, protected-backup, or erasure policy.
- **Current controls and detection:** Purpose-built indexes exist for current
  queries and the schema preserves provenance. No repository-owned capacity
  thresholds, alerts, or lifecycle mechanism detect or prevent exhaustion.
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
  There is no per-principal concurrency or rate limit.
- **Current controls and detection:** Streaming applies backpressure and closes
  rows and transactions on cancellation; server input/message sizes, HTTP
  timeouts, gRPC concurrent streams, and the pool are bounded. Those ceilings
  limit individual resources but do not guarantee fair or complete service. A
  generated REST workload records latency, errors, and status distributions for
  populated offer books, but has no production threshold and does not exercise
  slow readers or both transports.
- **Action:** Choose bounded pagination/snapshot semantics or measured hard
  limits shared by both transports, then add slow-reader and cross-transport
  cancellation tests, production-scale load thresholds, and pool/snapshot
  saturation alerts.
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
  unspecified hosting and external-identity architecture.
- **Current controls and detection:** The default binds to loopback, application
  authentication always runs, inputs are bounded, errors are minimized, and
  the README states required boundaries. These controls neither build nor
  verify a production environment.
- **Action:** Keep production use blocked until owners define and test TLS,
  workload identity, ingress trust, rate limits, secret delivery/rotation,
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
  there is no refresh, overlap protocol, or multi-issuer trust configuration.
- **Current controls and detection:** Startup fails closed for missing or
  malformed configuration and validates public signing keys, unique key IDs,
  algorithms, and use. Health checks exercise authenticated access but do not
  prove that all client keys overlap correctly.
- **Action:** Either document and test coordinated restart-based rotation,
  including overlap and emergency revocation, or implement bounded fail-safe
  refresh and multi-issuer behavior with negative and outage tests.
- **Traceability:** [F-008](../FRICTIONS.md#f-008--verification-key-and-issuer-changes-require-a-restart-p2)
  and [authentication configuration](../README.md#run-locally).

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
  operation support bounded retries. No current destination or monitoring
  detects the resulting backlog.
- **Action:** Deploy a reviewed destination and authenticated transport,
  backlog monitoring, payload approval, recovery ownership and runbook, and
  tested retry behavior; alternatively accept database-only retention in an
  ADR and remove outbound-delivery expectations.
- **Traceability:** [F-017](../FRICTIONS.md#f-017--integration-event-recovery-is-manual-and-has-no-destination-p2)
  and [ADR-0011](adr/0011-use-minimal-transactional-event-references.md).

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
- **Action:** Make idempotent consumer handling by event UUID an
  acceptance criterion for every destination; exercise acknowledgement-loss
  and replay tests and monitor duplicate outcomes before enabling it.
- **Traceability:** [F-017](../FRICTIONS.md#f-017--integration-event-recovery-is-manual-and-has-no-destination-p2)
  and [ADR-0011](adr/0011-use-minimal-transactional-event-references.md).

### FM-012 — Stale, unavailable, or misdirected exchange rate

- **Function and failure:** A caller treats a stale observation as fresh, maps
  a SIE series to the wrong base/quote direction, or cannot obtain a rate during
  a cold-cache provider/database failure.
- **Effects:** A future consumer can calculate an incorrect value or fail its
  own operation. No offer or reservation is currently repriced because the
  module is not exposed by the API or used by exchange behavior.
- **Causes:** SIE and PostgreSQL can be unavailable; latest values intentionally
  permit stale fallback during refresh; quote direction is caller-supplied
  because provider titles are not authoritative.
- **Current controls and detection:** Callers supply explicit canonical
  mappings, results expose stale status, exact decimals preserve values,
  bounded imports and append-only revisions preserve provenance, closed ranges
  remain cached, and PostgreSQL leases/cooldowns coordinate requests. Strict
  parsing rejects unexpected or invalid responses.
- **Action:** Require every future consumer to define maximum age, missing-data
  and direction-validation policy before using a rate. Add consumer-level
  alerts and failure tests; reassess severity before rates affect transactions.
- **Traceability:** [ADR-0014](adr/0014-persist-and-coordinate-banxico-sie-exchange-rates.md)
  and [rate behavior](../README.md#banxico-sie-exchange-rates).

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
  writes atomically; ordinary tests use offline fixtures. Production secret
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
- **Causes:** Security scanning is change-triggered rather than scheduled; raw
  Go tests can skip PostgreSQL; server composition has limited direct
  failure-path tests; and current coverage metrics exclude `application/cmd/`.
- **Current controls and detection:** `devenv test` composes formatting, static
  analysis, API generation checks, migration and lifecycle checks, demo smoke,
  readable full-server REST scenarios, a generated load check, race tests, at
  least 95% internal-package statement coverage and mutation efficacy,
  `govulncheck`, ASVS evidence checks, and TLC. Governance, FMEA, and integration
  test paths trigger that workflow, but no idle-repository vulnerability cadence
  or complete composition coverage exists.
- **Action:** Schedule owned security scans, ensure the default test path cannot
  silently skip integration coverage, and test server configuration, partial
  startup, and forced shutdown. Keep remediation evidence and alert ownership.
- **Traceability:** [F-012](../FRICTIONS.md#f-012--security-checks-are-change-triggered-rather-than-continuous-p2),
  [F-015](../FRICTIONS.md#f-015--the-default-contributor-path-can-silently-reduce-test-coverage-p3),
  and [F-016](../FRICTIONS.md#f-016--server-composition-has-limited-direct-test-coverage-p3).

### FM-015 — Dependency failure causes restart churn

- **Function and failure:** An orchestrator uses the authenticated, database-
  dependent health operation as liveness and repeatedly restarts an otherwise
  functioning process during identity or PostgreSQL failure.
- **Effects:** A dependency incident is amplified into process churn, slower
  recovery, avoidable load, and loss of diagnostic continuity.
- **Causes:** The only health operation checks authorization and database
  readiness; there is no distinct process-only liveness contract.
- **Current controls and detection:** Startup pings PostgreSQL and the demo smoke
  test verifies health and shutdown. The authenticated health response detects
  readiness loss but cannot distinguish it for an orchestrator without an
  external probe policy.
- **Action:** Define deployment health semantics and test separate liveness and
  readiness behavior, dependency failures, thresholds, and restart policy.
- **Traceability:** [F-010](../FRICTIONS.md#f-010--there-is-no-orchestration-friendly-liveness-endpoint-p2)
  and [runtime endpoints](../README.md#run-locally).

### FM-016 — Lossy or incompatible database migration

- **Function and failure:** A forward migration loses immutable facts or makes
  the previously deployed application fail during a rolling release.
- **Effects:** Irrecoverable transaction/audit loss, service outage, or mixed-
  version corruption across instances.
- **Causes:** Rewriting an applied migration, destructive contraction, new
  required privileges or columns without expansion, unsafe backfill, or use of
  a destructive down migration. The PostgreSQL 18 UUID transition additionally
  depends on a correctly sequenced major-version upgrade and a synchronized
  legacy/UUID relationship graph during its compatibility period.
- **Current controls and detection:** Repository guidance requires timestamped
  dbmate migrations, lossless backward-compatible expand/backfill/contract
  changes, corrective roll-forward, and separately owned migrations. Fresh
  isolated PostgreSQL 18 database and lifecycle checks exercise the full
  history; schema tests verify the server major version and all 21 UUID primary
  keys, while compatibility triggers preserve prior-writer inserts and
  append-only triggers prevent ordinary fact mutation. These checks do not
  simulate every production dataset, PostgreSQL major upgrade, direct writer,
  or mixed-version rollout.
- **Action:** Preserve the guardrails; for every schema change, test the prior
  application against the expanded schema and representative existing data,
  document rollout/rollback-forward steps, verify compatibility-graph drift,
  and require PostgreSQL upgrade plus backup/restore evidence before production
  execution.
- **Traceability:** [ADR-0004](adr/0004-use-dbmate-for-database-migrations.md),
  [ADR-0017](adr/0017-use-postgresql-18-uuidv7-identities-and-uuidv4-nonces.md),
  [F-018](../FRICTIONS.md#f-018--the-uuid-migration-retains-a-dual-identifier-graph-p2),
  [database migration policy](../db/README.md), and
  [repository guardrails](../AGENTS.md#architectural-guardrails).

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
4. synchronize required actions and statuses with `FRICTIONS.md`, ASVS, ADRs,
   READMEs, TLA+, migrations, configuration, and CI as applicable; and
5. update the assessed date in the opening scope statement and record the
   focused checks used to validate changed controls in the change handoff.

Add new modes with the next `FM-NNN` identifier. Keep a controlled mode in the
analysis so later changes do not accidentally remove its control. Remove a mode
only when its function or boundary has been removed, and never reuse its ID for
another risk. Before production readiness, every high-priority open action must
have a named owner, target date, measurable completion evidence, and an explicit
decision to mitigate, avoid, transfer, or accept the remaining risk.
