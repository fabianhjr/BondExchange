# Architecture decision records

Architecture decision records (ADRs) document choices that materially shape
the system or its development workflow. They are kept even when superseded so
the reasoning remains available.

Each ADR uses the next four-digit number and one of these statuses:

- Proposed
- Accepted
- Superseded by ADR-NNNN
- Rejected

New ADRs should contain, at minimum, Context, Decision, Consequences, and
Alternatives considered sections.

## Records

- [ADR-0001: Use devenv for project dependencies and tasks](0001-use-devenv.md)
- [ADR-0002: Use TLA+ for behavioral system specification](0002-use-tla-plus.md)
- [ADR-0003: Use append-only PostgreSQL facts for offers and purchases](0003-use-append-only-postgresql-facts.md)
- [ADR-0004: Use dbmate for database migrations](0004-use-dbmate-for-database-migrations.md)
- [ADR-0005: Use shopspring/decimal for monetary amounts](0005-use-shopspring-decimal-for-monetary-amounts.md)
- [ADR-0006: Use Proto3 for the REST and gRPC API](0006-use-protobuf-for-rest-and-grpc-api.md)
- [ADR-0007: Publish and discover sale offers through the API](0007-publish-and-discover-sale-offers.md)
- [ADR-0008: Use isolated PostgreSQL lifecycles for tests and demos](0008-use-isolated-postgresql-lifecycles.md)
- [ADR-0009: Bind federated authorization to idempotent operations](0009-bind-federated-authorization-to-idempotent-operations.md)
- [ADR-0010: Replace runtime gRPC reflection with a versioned descriptor set](0010-replace-runtime-reflection-with-descriptor-set.md)
- [ADR-0011: Use minimal transactional references for integration events](0011-use-minimal-transactional-event-references.md)
- [ADR-0012: Use curated golangci-lint static analysis](0012-use-curated-golangci-lint.md)
- [ADR-0013: Require 95 percent test quality gates](0013-require-95-percent-test-quality-gates.md)
- [ADR-0014: Persist and coordinate Banxico SIE exchange rates](0014-persist-and-coordinate-banxico-sie-exchange-rates.md)
- [ADR-0015: Place the Go module under `application/`](0015-place-go-module-under-application.md)
- [ADR-0016: Use readable HTTP scenarios and generated load targets](0016-use-readable-http-scenarios-and-generated-load.md)
- [ADR-0017: Use PostgreSQL 18, UUIDv7 identities, and UUIDv4 nonces](0017-use-postgresql-18-uuidv7-identities-and-uuidv4-nonces.md)
- [ADR-0018: Contract the legacy identifier graph](0018-contract-the-legacy-identifier-graph.md)
- [ADR-0019: Canonicalize sale offers to MXN at intake](0019-canonicalize-sale-offers-to-mxn-at-intake.md)
- [ADR-0020: Keep the local test gate and continuous integration equivalent](0020-keep-local-and-ci-gates-equivalent.md)
- [ADR-0021: Schedule security scanning and name a response owner](0021-schedule-security-scanning-and-name-a-response-owner.md)
- [ADR-0022: Verify documentation against pinned sources](0022-verify-documentation-against-pinned-sources.md)
- [ADR-0023: Align storage constraints with domain validation](0023-align-storage-constraints-with-domain-validation.md)
- [ADR-0024: Define health-probe and verification-key rotation contracts](0024-define-probe-and-key-rotation-contracts.md)
- [ADR-0025: Own application OpenTelemetry instrumentation](0025-own-application-opentelemetry-instrumentation.md)
- [ADR-0026: Verify fact provenance and append-only history in TLA+](0026-verify-fact-provenance-and-append-only-history-in-tla.md)
- [ADR-0027: Model contended buying and revocable authorization](0027-model-contended-buying-and-revocable-authorization.md)
- [ADR-0028: Coordinate per-principal request rate limits in PostgreSQL](0028-coordinate-per-principal-request-rate-limits-in-postgresql.md)
- [ADR-0029: Use policy-aligned operational metrics](0029-use-policy-aligned-operational-metrics.md)
