# Repository frictions

This is the living register of verified, unresolved limitations and rough
edges in the repository. It is not a feature wish list: each entry points to
current implementation or documentation and states what would make the item
complete. Priorities describe urgency if the service is taken beyond its
disposable demo: P1 blocks a credible production use, P2 is a material
correctness, security, operability, or scaling concern, and P3 primarily
affects maintainability or contributor experience.

The repository was reviewed on 2026-09-06. `devenv test` and the default
configurable integration load task passed for this review, so there is no known
failing quality gate; the items below are gaps that the current gates either
accept or do not cover.

F-025 through F-030 were added by a source review on 2026-09-06 that read the
implementation without re-running the gates. Each cites the code it describes;
none has been reproduced by an executed test, which is itself part of what
those entries report.

## Product and domain

### F-001 — Buying stops at an immutable reservation (P1)

- **Evidence:** `Buy` appends one purchase fact per sale offer. The root
  README, database documentation, security profile, and TLA+ model explicitly
  leave payment, custody, ownership transfer, finality, cancellation, expiry,
  and settlement outside the system.
- **Impact:** A successful `Buy` response can be mistaken for a completed
  securities transaction even though no downstream lifecycle exists. An offer
  also cannot return to the active book.
- **Complete when:** The intended lifecycle and terminology are decided in an
  ADR, represented by append-only facts, reflected in the API and TLA+ model,
  and covered by migration, concurrency, and recovery tests.

### F-002 — Market-integrity rules are undecided (P1)

- **Evidence:** [ADR-0030](docs/adr/0030-prevent-same-identity-self-trading.md)
  now prohibits a principal from buying an offer attributed to the same
  internal UUID. Go, PostgreSQL, TLA+, principal-specific discovery, negative
  tests, durable rejection records, and a bounded metric enforce that rule.
  The model still intentionally has no balances, holdings, eligibility,
  beneficial-owner relationships, partial fills, matching engine, price bands,
  or price/time priority.
  [ADR-0034](docs/adr/0034-make-the-principal-the-sole-identity.md) makes the
  principal the sole identity, so `principals` is now the only identity a rule
  could group: there is no owner table, and expressing one legal person behind
  several principals would require a new many-to-one relationship and
  authoritative affiliation data the service does not have.
- **Impact:** Same-identity self-trading is controlled, but affiliated-account,
  unfunded, economically invalid, or manipulative activity can still look like
  a valid reservation because the remaining applicable rules are undecided.
  Two principals under one beneficial owner remain indistinguishable from two
  unrelated counterparties in every artifact.
- **Complete when:** Product policy identifies which rules belong in this
  service, architecture decisions record the boundary, and every adopted
  domain rule is synchronized across Go, PostgreSQL, TLA+, and documentation.

## Data and administration

### F-003 — Provisioning and security administration require direct SQL (P1)

- **Evidence:** [`README.md`](README.md) says principals and bonds must be
  provisioned separately. Principals, suspensions, role grants, and revocations
  likewise have schema but no supported command or API. The `rbac.read`,
  `rbac.grant`, `rbac.revoke`, and `audit.read` permissions are seeded but no
  application operation consumes them.
- **Impact:** Operators need privileged, bespoke database access for normal
  onboarding and access changes. That makes validation, authorization,
  idempotency, audit attribution, and least-privilege procedures easy to apply
  inconsistently.
- **Complete when:** A reviewed, authenticated provisioning workflow exists
  for each supported administrative action, or an external owner and an exact
  database procedure are documented and tested.

### F-004 — Database constraints are looser than domain validation (P1)

- **Evidence:** Identifier, nonce, and currency-code constraints now agree with
  the Go domain: `sale_offers.currency_code` requires `^[A-Z]{3}$`, canonical
  offer terms are MXN, and submission provenance is MXN or USD. One divergence
  remains. `price` is `numeric(14,4)`, and PostgreSQL rounds an input with more
  than four fractional digits at cast time, before any `CHECK` observes it, so
  no column constraint can reject what `exchange.ParsePrice` refuses. Closing it
  requires changing the monetary domain's base type on append-only columns. This
  matters while F-003 keeps direct SQL a sanctioned writer and the affected
  facts are append-only.
- **Impact:** A privileged or future alternate writer can still append a sale
  offer whose price was silently rounded to a value the seller did not submit.
  Every other value class is now rejected at storage as well as at the boundary.
- **Complete when:** The monetary representation rejects rather than rounds an
  over-precise value for every sanctioned writer, with a backward-compatible
  forward migration and a safe plan for pre-existing rounded facts, or an ADR
  records the rounding as an accepted permanent constraint. The equivalence is
  pinned by `TestStorageConstraintsMatchDomainValidation`, which fails if the
  divergence closes without this entry being updated.

### F-018 — Legacy non-MXN offers have no seller disposition workflow (P1)

- **Evidence:** The canonical-MXN expand migration preserves pre-existing
  non-MXN offer facts and backfills only unambiguous MXN rows. New list and buy
  queries require canonical terms and therefore hide those rows, while the
  compatibility view remains readable by the previously deployed binary. No
  authenticated operation lets a seller accept a conversion, relist, or retire
  a legacy row.
- **Impact:** A mixed-version rollout can still serve non-MXN offers, and
  completing rollout can strand earlier offers without a seller-visible
  resolution. Directly updating or guessing their price would violate the
  append-only model and seller consent.
- **Complete when:** An inventory and seller-notification procedure exists, an
  authorized append-only accept/relist or retirement workflow dispositions
  every active non-MXN row, and release evidence shows old binaries and
  compatibility-view readers are drained before the MXN-only control is
  declared active.

### F-005 — Append-only and event-delivery data has no lifecycle policy (P1)

- **Evidence:** Operation claims/results, RBAC facts, domain facts, Banxico SIE
  response imports and exchange-rate revisions, minimal integration-event
  references, and per-destination delivery records grow without deletion.
  ADR-0033 removed expired pre-UUID identifier evidence, but it did not define
  a lifecycle for these retained records.
  [`docs/security/ASVS.md`](docs/security/ASVS.md) leaves
  capacity, retention, protected backups, immutable log storage, and erasure
  semantics to future deployment and legal decisions.
- **Impact:** Storage and query costs grow indefinitely, while retention and
  data-subject obligations cannot yet be evaluated against a concrete policy.
- **Complete when:** Capacity and retention targets, archival or partitioning,
  backup handling, monitoring, and any corrective/erasure semantics are
  decided and verified without weakening the append-only audit model or the
  exchange-rate correction history.

## API and runtime

### F-006 — Read APIs have unbounded resource use (P1)

- **Evidence:** Active-offer listing intentionally streams every match while
  holding a repeatable-read transaction, result rows, snapshot, and pooled
  connection. Active bond-series discovery instead collects every series into
  one unary response. The gRPC server caps each outbound message at 64 KiB, so
  a sufficiently large series set can fail on native gRPC even though the
  in-process REST path can return it. A small generated REST workload now
  exercises populated offer books and concurrent reads, but it defines no
  production cardinality, latency, or saturation threshold and does not model
  slow readers.
- **Impact:** Slow or numerous readers can exhaust the fixed 20-connection
  pool, and large datasets can cause transport divergence or failed discovery.
- **Current mitigation:** PostgreSQL admits at most 100 requests per
  authenticated principal in each fixed UTC minute across instances. This
  limits request starts but not stream duration, concurrent streams, result
  cardinality, or the cost of one admitted read.
- **Complete when:** Bounded pagination/snapshots or explicit, measured limits
  exist for both reads; REST and gRPC share equivalent limits and semantics;
  and load, cancellation, and concurrency tests cover the chosen design.

### F-007 — Swagger does not describe the REST stream on `/active-offers` (P2)

- **Evidence:** The generated Swagger document advertises global
  `application/json` and a single `ListActiveOffersResponse`. The custom REST
  adapter actually returns an RFC 7464 `application/json-seq` stream containing
  many offer events and a terminal count, and it may append an error event
  after HTTP 200 has started.
- **Impact:** Generated REST clients and contract tools cannot infer the actual
  media type, framing, multiplicity, or post-header error behavior.
- **Complete when:** A checked-in machine-readable contract accurately
  describes the streaming representation, or the REST endpoint adopts a
  representation the generated contract can express and verify.

### F-011 — The production deployment boundary is unspecified (P1)

- **Evidence:** The server provides plaintext loopback listeners and no
  production package. It now owns tested OTLP trace/metric instrumentation and
  devenv runs a loopback development collector. PostgreSQL now coordinates a
  tested 100-request fixed UTC-minute limit for authenticated principals, but
  the ASVS profile still has `pending-deployment` and
  `pending-external-identity` dispositions for TLS/workload identity, ingress
  trust, unauthenticated and connection-level limits, broader anti-automation,
  secret delivery, database protection, a protected telemetry backend and
  alerts, identity assurance, and related controls.
- **Impact:** Passing application tests does not establish a secure or operable
  production system, and several controls cannot be verified until the hosting
  architecture exists.
- **Complete when:** Deployment and external-identity responsibilities have
  named owners and testable contracts, architecture choices are recorded in
  ADRs, a production artifact is built, and affected ASVS dispositions have
  deployable evidence.

### F-025 — The request-digest binding has no interoperable specification (P1)

- **Evidence:** `internal/rpcapi/server.go` and `cmd/internal/demoauth/demoauth.go`
  each compute the binding as the SHA-256 of
  `proto.MarshalOptions{Deterministic: true}.Marshal(request)`. Upstream
  protobuf-go documents that deterministic marshaling is not canonical form and
  is not stable across builds or library versions.
  [`README.md`](README.md), [`docs/security/ASVS.md`](docs/security/ASVS.md),
  and ADR-0009 all cite the "deterministic protobuf request digest" as a
  control, but [`api/README.md`](api/README.md) never specifies the byte
  encoding an issuer must reproduce. The only issuer today is the demo helper,
  built from this module against the same generated bindings.
- **Impact:** The federated-issuer premise cannot be implemented by a third
  party, because no specification exists to build against. A protobuf-go
  upgrade could also change the digest input for every operation at once, and
  no gate compares the encoding against a fixed expectation. This is the
  request/assertion binding drift named as a cause under FM-004.
- **Complete when:** A versioned canonical encoding for `request_sha256` is
  specified in the API contract, a second independent implementation verifies
  it in continuous integration, and ADR-0009 records its compatibility policy —
  or the digest is taken over an encoding that already has a published
  canonical form.

### F-026 — Cold exchange-rate resolution polls PostgreSQL without a bound (P2)

- **Evidence:** `internal/exchangerates/service.go` polls every
  `Config.PollEvery`, defaulting to 25 milliseconds, with no attempt or
  deadline cap in `Latest`, `resolve`, or `resolveForced`. `QuoteSaleOffer`
  reaches it through `internal/offerintake/service.go`. Each iteration issues a
  `States` read and a `Claim` write transaction;
  `internal/postgres/exchange_rates.go` requires
  `next_attempt_at <= transaction_timestamp()` to claim, and `Fail` pushes that
  forward by `RetryAfter`, one minute by default, or by a provider-supplied
  retry time. A stored observation short-circuits the loop, because `Latest`
  returns it marked stale and offer intake rejects a stale observation, so only
  a genuinely cold cache reaches the poll.
- **Impact:** After a cold start or a provider outage, every concurrent quote
  request polls at roughly forty iterations per second for the whole backoff
  window, adding write transactions to the database at the moment upstream
  recovery matters. With no server-side deadline (F-027) the loop ends only
  when the client disconnects. Relates to FM-012 and FM-017.
- **Complete when:** Resolution returns promptly when a work unit is in
  backoff, or caps its attempts and backs the poll off, and a test covers
  concurrent callers against a cold cache whose leaseholder has failed.

### F-027 — No operation has a server-side deadline (P2)

- **Evidence:** `internal/serverruntime/serverruntime.go` sets `ReadTimeout`,
  `ReadHeaderTimeout`, `IdleTimeout`, and `ShutdownTimeout`, but no
  `WriteTimeout` — deliberately, for the RFC 7464 stream — and no
  handler-level context deadline. The gRPC server admits 128 concurrent streams
  against a 20-connection pool. Per-principal admission bounds request starts
  in each fixed UTC minute; ADR-0028 states that it is not a concurrency or
  duration limit.
- **Impact:** A slow reader, a stalled upstream Banxico SIE call, or the F-026
  poll runs until its client disconnects while holding a pooled connection.
  F-006 records this for the read APIs; the same gap applies to mutations and
  to the USD intake path, which is the service's most dependency-heavy
  operation. Relates to FM-007.
- **Complete when:** Every operation carries a bounded server-side deadline
  appropriate to its transport, exhaustion maps to a documented status on both
  REST and gRPC, and tests cover the deadline for a stream, a mutation, and an
  upstream dependency call.

### F-029 — A quote-reuse race is reported as an internal error (P3)

- **Evidence:** `sale_offer_submissions.conversion_quote_uuid` is `UNIQUE`, so
  single use of a conversion quote is enforced at storage and not only by the
  `NOT EXISTS` check in `internal/postgres/offer_intake.go`.
  `classifyCreateSaleOfferError` in `internal/postgres/store.go` maps only
  `sale_offers_pkey`, `sale_offers_seller_principal_fkey`, and
  `sale_offers_bond_uuid_fkey`, so the quote-reuse violation falls through to
  the default branch and becomes an internal error. Serializable isolation
  usually converts the race into a retryable serialization failure first, so
  the path is rare and no domain fact is written. The `sale_offers_pkey` case
  is separately unreachable now that PostgreSQL generates the key.
- **Impact:** A client-caused precondition failure is reported to the caller
  and counted in `bondexchange.operation.count` as a server fault, which is the
  signal an operator would alert on. Relates to FM-019.
- **Complete when:** The constraint is classified as the same durable
  conversion-quote rejection the read path already records, the unreachable
  primary-key case is removed, and a test exercises the constraint directly
  rather than only the `NOT EXISTS` path.

## Verification and contributor workflow

### F-015 — The default contributor path can silently reduce test coverage (P3)

- **Evidence:** A raw `go test ./...` from `application/` still skips PostgreSQL
  integration tests when `BOND_EXCHANGE_TEST_DATABASE_URL` is absent, though the
  skip now names the task that provisions the database and becomes a failure
  when `CI` is set. Both scoped READMEs carry the caveat. The README bootstrap
  still installs an unpinned `nixpkgs#devenv` CLI before the repository's lock
  and task graph take effect.
- **Impact:** Familiar Go commands can still look green without persistence
  coverage on a contributor's machine, and onboarding can change when the
  external devenv package advances. Automated gates are no longer affected.
- **Complete when:** The ordinary contributor command fails loudly or always
  provisions required integration dependencies, and the bootstrap toolchain is
  pinned or has an explicitly tested compatibility range.

### F-017 — Integration-event recovery is manual and has no destination (P2)

- **Evidence:** Successful mutations record durable event references and make
  one immediate attempt per configured destination, but the repository ships
  no concrete publisher. There is deliberately no startup sweep, scheduled
  retry, or background worker; only an authorized operator can invoke
  `PublishPendingEvents`. The application now reports configured-publisher,
  scan/claim/load/publish/state-transition, and claimed-delivery outcome
  metrics, but no destination exists from which to derive or alert on a
  continuously measured shared backlog.
- **Impact:** No event leaves the service in the checked-in configuration, and
  a crash or publisher failure can leave events pending indefinitely unless an
  operator detects the backlog and explicitly retries it. At-least-once
  recovery can also produce duplicates after an ambiguous acknowledgement.
- **Complete when:** A reviewed destination adapter, authenticated transport,
  payload approval, consumer deduplication, backlog monitoring, and tested
  recovery runbook are deployed, or an ADR deliberately accepts database-only
  event retention and removes the outbound-delivery claim.

### F-020 — Verifying the ASVS baseline requires a large upstream checkout (P3)

- **Evidence:** [`docs/security/ASVS.md`](docs/security/ASVS.md) is verified
  against the `third_party/asvs` submodule, pinned to OWASP/ASVS
  `v5.0.0_release`. Only `5.0/en` is read, but the checkout is roughly 160 MB
  because the upstream repository retains every prior standard version and its
  images. `.gitmodules` marks the submodule shallow, which bounds history but
  not working-tree size. `security:check`, and therefore `devenv test` and the
  Go quality workflow, fail without it.
- **Impact:** Cloning, continuous integration, and a first `devenv test` each
  pay for content the assessment never reads, and a contributor who skips
  `git submodule update --init` sees a failing gate before writing any code.
- **Complete when:** The pinned requirement text is obtained without the
  unrelated history — through submodule sparse-checkout, an upstream
  machine-readable requirement artifact, or a reviewed vendored extract with an
  automated provenance check — or the cost is measured and accepted in an ADR.

### F-021 — The repository has no license (P2)

- **Evidence:** There is no `LICENSE` file at the repository root and no
  copyright statement in the READMEs, so the default is exclusive copyright.
  The repository now also references OWASP ASVS, which upstream licenses under
  Creative Commons Attribution-ShareAlike 4.0 International, as a submodule.
- **Impact:** No one can reuse, fork, or contribute to this repository with
  legal confidence, and the relationship between this work and the terms of the
  referenced standard is unstated. [`SECURITY.md`](SECURITY.md) and
  [`.github/CODEOWNERS`](.github/CODEOWNERS) name a responsible maintainer but
  cannot supply terms of use.
- **Complete when:** The owner selects a license, adds it at the repository
  root, states how it relates to the separately licensed ASVS source, and
  records the choice where contributors will find it.

### F-022 — Marketplace and authorization behavior are model-checked separately (P3)

- **Evidence:** [`spec/tla/README.md`](spec/tla/README.md) documents three TLC
  instances. `BondExchange.cfg` explores contention between buyers with
  authorization fixed, and `BondExchangeAuthorization.cfg` explores grants,
  revocations, and suspensions over a single-offer market. A first attempt at
  one combined instance exceeded four million distinct states at depth six
  without terminating, so the concerns were separated to keep `spec:check`
  tractable. `MaxGrantGeneration` also bounds re-granting at two generations.
- **Impact:** No checked behavior interleaves a contended binding order with a
  concurrent revocation or suspension. The service authorizes, claims, and
  commits inside one transaction, so that interleaving is not expected to
  produce a fact the model would reject, but the expectation is reasoned rather
  than verified. A defect that appears only when both concerns interact would
  not be found by TLC.
- **Complete when:** A combined instance is tractable — through a sound symmetry
  set, a state constraint, or a smaller abstraction of authorization — and
  covers contention and revocation together, or an ADR records that the
  separation is sufficient and states why the interaction cannot produce a
  distinct failure.

### F-024 — The model omits authorization timing, reads, and rejection paths (P2)

- **Evidence:** [`spec/tla/README.md`](spec/tla/README.md) records the
  boundaries under "What a passing check does not establish". `ClaimBuy`
  evaluates authorization and appends the claim in one step, so no reachable
  state separates the two, and `NewClaimsAreAuthorizedWhenClaimed` cannot fail
  for a check performed before the mutation. Active-offer listing and
  bond-series discovery require `offers.list` but have no modeled action.
  `offer_unavailable` and `self_trade_prohibited` are the two rejections the
  model produces, while the service also records `seller_not_found`,
  `bond_not_found`, `offer_already_exists`, and `conversion_quote_unavailable`.
- **Impact:** [`docs/FMEA.md`](docs/FMEA.md) credits the model as a detection
  control for FM-004, whose named causes include RBAC evaluated outside the
  mutation transaction. That cause is currently detected by Go and PostgreSQL
  tests alone. Revoked or suspended read access, and four of the six durable
  rejection outcomes, are likewise unverified by TLC.
- **Complete when:** Authorization is evaluated in a step the model can
  separate from the claim it authorizes, read operations that require a
  permission are represented, and every recorded rejection outcome is
  reachable — or an ADR records which of these belong to the test suite rather
  than the specification, and the FMEA credits the controls that actually cover
  them.

### F-028 — The USD intake path has no concurrency or load evidence (P3)

- **Evidence:** `tests/integration/http/sale-offer-quote.hurl` and
  `offer-intake-failures.hurl` now cover quotation, replay, and every documented
  misuse functionally, and the generated workload gained `denied` and
  `invalid-assertion` phases that measure rejected traffic at both depths. The
  workload in `application/cmd/load-targets/main.go` still emits only MXN
  creates, distinct buys, a contended buy, and both listings, so
  `nix/integration-load.sh` has no quote phase. `QuoteSaleOffer` is the most
  expensive operation in the service: principal resolution, admission, quote
  replay, rate resolution, and a serializable claim across three or four
  transactions plus one possible upstream request, against a 20-connection
  pool.
- **Impact:** The cost of the newest operation is unmeasured, so pool sizing,
  the cold-cache behavior in F-026, and contention on a single-use quote are
  unverified under concurrency. The correctness-gated load profile can stay
  green while the path it does not cover is the one most likely to saturate.
  Relates to FM-014 and FM-017.
- **Complete when:** The generated workload includes a USD quote-and-accept
  scenario, including contention on one quote, and its report is retained
  alongside the existing profiles.

### F-030 — The mutation gate's operator set is not recorded (P3)

- **Evidence:** `application/.gremlins.yaml` disables `invert-logical`,
  `invert-assignments`, `invert-bitwise`, `invert-bwassign`,
  `invert-loopctrl`, and `remove-self-assignments`. ADR-0013 records that the
  `mutant-coverage` threshold stays disabled and weighs enabling it as an
  alternative, but says nothing about which mutation operators run.
  `invert-logical` mutates `&&` and `||`, which is the shape of the guard
  chains in `internal/authn/authenticator.go` and of the nine-clause
  observation check in `internal/offerintake/service.go`.
- **Impact:** The 95 percent efficacy figure is measured over a reduced
  operator set, and the operator most relevant to this repository's
  authentication and rate-validation logic is not among those measured. The
  gate is credited as detection evidence under FM-014 without that
  qualification.
- **Complete when:** `invert-logical` is enabled and the threshold still holds,
  or ADR-0013 records the excluded operators and the reason with the same
  reasoning it already applies to `mutant-coverage`.
