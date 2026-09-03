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
