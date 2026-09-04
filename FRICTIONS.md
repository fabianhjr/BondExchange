# Repository frictions

This is the living register of verified, unresolved limitations and rough
edges in the repository. It is not a feature wish list: each entry points to
current implementation or documentation and states what would make the item
complete. Priorities describe urgency if the service is taken beyond its
disposable demo: P1 blocks a credible production use, P2 is a material
correctness, security, operability, or scaling concern, and P3 primarily
affects maintainability or contributor experience.

The repository was reviewed on 2026-09-03. `devenv test` passed at that point,
so there is no known failing quality gate; the items below are gaps that the
current gates either accept or do not cover.

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

- **Evidence:** [`spec/tla/README.md`](spec/tla/README.md) explicitly permits a
  seller to buy their own offer. The model also intentionally has no balances,
  holdings, partial fills, matching engine, or price/time priority.
- **Impact:** The current behavior is enough to demonstrate exclusive buying,
  but it is not enough to state or enforce realistic trading rules.
- **Complete when:** Product policy identifies which rules belong in this
  service, architecture decisions record the boundary, and every adopted
  domain rule is synchronized across Go, PostgreSQL, TLA+, and documentation.

## Data and administration

### F-003 — Provisioning and security administration require direct SQL (P1)

- **Evidence:** [`README.md`](README.md) says users and bonds must be
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

- **Evidence:** The initial migration only requires a nonempty user ID,
  sale-offer IDs have no length or character constraint, and currency codes
  only need to be nonempty. Go requires visible-ASCII IDs of 1–128 bytes and
  exactly three uppercase ASCII letters for currency. This matters especially
  while F-003 requires direct SQL provisioning, and the affected tables are
  append-only.
- **Impact:** A privileged or future alternate writer can create immutable
  facts that the Go domain rejects or cannot reliably process.
- **Complete when:** A backward-compatible forward migration makes storage
  constraints and every sanctioned writer agree with the documented domain,
  including a safe plan for any pre-existing nonconforming facts.

### F-005 — Append-only operational data has no lifecycle policy (P1)

- **Evidence:** Operation claims/results, RBAC facts, and domain facts grow
  without deletion. [`docs/security/ASVS.md`](docs/security/ASVS.md) leaves
  capacity, retention, protected backups, immutable log storage, and erasure
  semantics to future deployment and legal decisions.
- **Impact:** Storage and query costs grow indefinitely, while retention and
  data-subject obligations cannot yet be evaluated against a concrete policy.
- **Complete when:** Capacity and retention targets, archival or partitioning,
  backup handling, monitoring, and any corrective/erasure semantics are
  decided and verified without weakening the append-only audit model.

## API and runtime

### F-006 — Read APIs have unbounded resource use (P1)

- **Evidence:** Active-offer listing intentionally streams every match while
  holding a repeatable-read transaction, result rows, snapshot, and pooled
  connection. Active bond-series discovery instead collects every series into
  one unary response. The gRPC server caps each outbound message at 64 KiB, so
  a sufficiently large series set can fail on native gRPC even though the
  in-process REST path can return it.
- **Impact:** Slow or numerous readers can exhaust the fixed 20-connection
  pool, and large datasets can cause transport divergence or failed discovery.
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

### F-008 — Verification-key and issuer changes require a restart (P2)

- **Evidence:** `cmd/server` reads one issuer, one audience, and one local JWKS
  file during startup. The authenticator retains that in-memory key set and has
  no refresh mechanism.
- **Impact:** Key rotation and emergency revocation depend on coordinated
  process replacement, and serving multiple trusted issuers requires separate
  deployments or code changes.
- **Complete when:** The deployment contract explicitly accepts restart-based
  rotation with a tested overlap procedure, or bounded, failure-safe key and
  issuer refresh is implemented and tested.

### F-009 — The reflection permission is not an enforcement point (P2)

- **Evidence:** The database grants operators `reflection.use`, but enabling
  `BOND_EXCHANGE_ENABLE_REFLECTION=true` registers the standard gRPC reflection
  service for the entire listener. Only panic interceptors wrap that service;
  the application authenticator and RBAC check are not applied to reflection.
- **Impact:** The flag is an environment-wide exposure switch, not the
  per-principal authorization suggested by the permission model.
- **Complete when:** Reflection is protected by an authenticated authorization
  layer, or the unused permission is removed and documentation clearly assigns
  all access control to a separately protected diagnostic listener or network.

### F-010 — There is no orchestration-friendly liveness endpoint (P2)

- **Evidence:** `CheckHealth` requires a short-lived operation-bound assertion,
  principal resolution, `health.read`, and a successful database ping. There
  is no separate process-only liveness signal.
- **Impact:** A scheduler must continuously mint application credentials for a
  probe, and identity or database outages can cause healthy processes to be
  restarted instead of merely removed from service.
- **Complete when:** Deployment health semantics are decided and documented,
  with distinct liveness/readiness behavior where required and tests for each
  dependency failure.

### F-011 — The production deployment boundary is unspecified (P1)

- **Evidence:** The server provides plaintext loopback listeners and no
  production package. The ASVS profile still has `pending-deployment` and
  `pending-external-identity` dispositions for TLS/workload identity, ingress
  trust, rate limiting, secret delivery, database roles and protection,
  telemetry, identity assurance, and related controls.
- **Impact:** Passing application tests does not establish a secure or operable
  production system, and several controls cannot be verified until the hosting
  architecture exists.
- **Complete when:** Deployment and external-identity responsibilities have
  named owners and testable contracts, architecture choices are recorded in
  ADRs, a production artifact is built, and affected ASVS dispositions have
  deployable evidence.

## Verification and contributor workflow

### F-012 — Security checks are change-triggered rather than continuous (P2)

- **Evidence:** `govulncheck` runs in the Go quality workflow only for manual
  dispatches and pushes or pull requests matching its path filters. There is no
  scheduled scan, so a newly disclosed vulnerability can remain unseen while
  the repository is idle.
- **Impact:** The remediation windows in the security profile start at
  confirmation, but confirmation itself has no bounded cadence.
- **Complete when:** A scheduled dependency/security job runs at a documented
  cadence, reports failures to an owner, and retains or publishes the evidence
  needed by the response policy.

### F-013 — Governance-only changes bypass CI path filters (P3)

- **Evidence:** The workflow filters include `README.md`, ADRs, and security
  docs, but not `AGENTS.md` or this file. A change only to repository guidance
  or the friction register therefore starts neither workflow.
- **Impact:** Important process claims can change without even the inexpensive
  formatting, migration, API, or specification consistency checks running.
- **Complete when:** Governance documents trigger an appropriate documentation
  or repository-consistency check, without requiring unrelated expensive jobs
  unless their scope warrants them.

### F-014 — The ASVS assessment input is not reproducible from the repository (P2)

- **Evidence:** The security profile records an absolute path under one
  contributor's `Downloads` directory and a source checksum. The repository
  does not contain or fetch that source, and `asvs-profile-check.sh` verifies
  row shape, count, dispositions, and evidence-path existence but not the
  recorded source checksum or requirement text.
- **Impact:** Another contributor can validate the derived TSV structurally but
  cannot independently regenerate it or prove that its IDs and text match the
  claimed ASVS release.
- **Complete when:** A license-compliant, content-addressed retrieval or
  regeneration procedure is documented and automated, and CI verifies the
  checked-in profile against that pinned input.

### F-015 — The default contributor path can silently reduce test coverage (P3)

- **Evidence:** A raw `go test ./...` skips PostgreSQL integration tests when
  `BOND_EXCHANGE_TEST_DATABASE_URL` is absent. The recommended devenv tasks fix
  this, but the README bootstrap installs an unpinned `nixpkgs#devenv` CLI
  before the repository's lock and task graph take effect.
- **Impact:** Familiar Go commands can look green without persistence coverage,
  and onboarding can change when the external devenv package advances.
- **Complete when:** The ordinary contributor command fails loudly or always
  provisions required integration dependencies, and the bootstrap toolchain is
  pinned or has an explicitly tested compatibility range.

### F-016 — Server composition has limited direct test coverage (P3)

- **Evidence:** Package tests cover domain, authentication, adapters, and the
  PostgreSQL store, and the demo smoke test exercises happy paths and shutdown.
  `cmd/server` has no focused tests, while coverage and mutation scores
  intentionally measure only `internal/`.
- **Impact:** Environment parsing, listener composition, hard-coded pool and
  server limits, partial startup failures, and forced shutdown behavior can
  regress without a targeted failure identifying the boundary.
- **Complete when:** Composition is factored into testable units or covered by
  focused process tests for invalid configuration, listener failures, startup,
  and graceful/forced shutdown, with an explicit decision on whether command
  packages belong in quality metrics.
