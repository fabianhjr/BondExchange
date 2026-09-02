# ADR-0004: Use dbmate for database migrations

- Status: Accepted
- Date: 2026-09-01

## Context

The append-only PostgreSQL design still requires controlled schema evolution.
Applying plain SQL files directly does not record which migrations ran, detect
out-of-order additions, or provide one command shared by development, tests,
CI, and deployment.

The project already uses devenv to pin tools and compose PostgreSQL-dependent
tasks. The migration mechanism should preserve reviewable SQL without coupling
schema evolution to the Go application binary.

## Decision

Use dbmate as the database migration runner and provide it through the pinned
devenv environment.

Keep timestamp-versioned SQL files under `db/migrations/`, including dbmate's
`migrate:up` and `migrate:down` sections. Run migrations transactionally in
strict version order, and let dbmate track applied versions in its
`schema_migrations` table. Treat migrations as the schema source of truth and
disable automatic schema dumps.

The application does not apply migrations at startup. Development, CI, and
deployment run `dbmate up` as a separate step using an identity authorized for
schema changes. Tests depend on a devenv migration task before accessing
PostgreSQL.

## Consequences

- Repeated migration runs are safe because dbmate skips recorded versions.
- Out-of-order migration files fail instead of being silently applied.
- Schema changes remain plain SQL and independent of the Go process.
- Deployments need an explicit migration step and a privileged migration
  identity separate from the runtime application identity.
- Applied migration files must be immutable; corrections require a new
  migration.
- Rollbacks are explicit SQL and remain an operator decision, particularly for
  destructive changes to append-only facts.

## Alternatives considered

### Direct psql execution

This keeps tooling minimal but does not provide migration history or strict
ordering. It was replaced by dbmate.

### golang-migrate or Goose

Both support SQL migrations and version tracking. They were not selected
because dbmate provides the required standalone workflow with a small,
language-independent interface and no need to embed migration behavior in the
Go server.

### Atlas

Atlas supports declarative schema management, diffing, and more advanced
workflows. It was not selected because the current schema is small and
explicitly reviewed SQL migrations are sufficient.
