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
