# ADR-0008: Use isolated PostgreSQL lifecycles for tests and demos

- Status: Accepted
- Date: 2026-09-03

## Context

Database-dependent devenv tasks originally depended on one managed PostgreSQL
service under `.devenv/state`. Separate test, coverage, and mutation invocations
reused that process and data directory. If the task supervisor ended without a
complete PostgreSQL shutdown, later invocations encountered stale
`postmaster.pid`, socket-lock, or shared-memory state and required manual
cleanup.

The local run workflow also required three manual steps: start PostgreSQL,
apply migrations, and start the server. It did not provision the users and
bonds needed to exercise the public API.

## Decision

Package a `bond-exchange-with-postgres` runtime harness with Nix. The harness
uses the pinned PostgreSQL and dbmate packages to create a uniquely named
temporary data directory and Unix socket, disables TCP, creates the requested
database, applies the complete migration history, exports the application and
test connection variables, and runs one child command. It owns both processes,
forwards termination signals, preserves the child's exit status, and stops and
removes its PostgreSQL cluster when the child succeeds, fails, or is
interrupted.

Run every database-dependent devenv gate inside its own harness invocation.
Do not use a shared devenv PostgreSQL service as a task dependency. Verify the
harness with explicit success, failure, teardown, and parallel-isolation
checks.

Expose `devenv up` as a complete disposable demo process. It uses the same
harness, applies migrations, loads a deterministic seed that is separate from
the migration history, builds the server, and starts both REST and gRPC
listeners. The demo database is discarded at shutdown. Persistent development
and deployed environments continue to supply `DATABASE_URL` and run dbmate
separately; the application still never migrates at startup.

## Consequences

- Test, coverage, and mutation runs start with an empty database and cannot
  inherit facts or locks from another run.
- Private Unix sockets allow database-backed tasks to run concurrently without
  claiming a shared TCP port.
- A failed or interrupted task has one owner responsible for both child and
  database teardown. An uncatchable process kill can leave a uniquely named
  temporary directory, but it cannot block the next invocation through a
  shared PID or socket path.
- `devenv up` provides a repeatable demonstration dataset in one command and
  loses all demo changes when stopped.
- Starting a fresh cluster and applying migrations adds a small fixed cost to
  each database-dependent gate. Isolation and deterministic state are preferred
  over reusing one faster mutable cluster.
- Demo fixtures are development data, not schema or production bootstrap data.
  They remain outside `db/migrations` and are exercised by a smoke test.

## Alternatives considered

### Keep the shared devenv PostgreSQL service

Process Compose is appropriate for an explicitly long-running local service,
but using that service as an implicit dependency of finite task invocations
made ownership and teardown ambiguous. Adding stale-lock deletion would treat a
symptom and risk interfering with another live process.

### Run every check as a pure Nix derivation with `postgresqlTestHook`

The nixpkgs hook provides the desired setup and teardown semantics during Nix
build check phases. Moving the complete Go, coverage, and mutation workflows
into derivations would require packaging their source and dependency caches and
would make the existing focused devenv tasks less direct. The runtime harness
follows the same lifecycle while retaining the established commands.

### Use containers for PostgreSQL

Containers would provide isolation but add a second dependency and lifecycle
system when Nix already pins PostgreSQL and all client tools. The Nix harness
keeps local and CI behavior on the same toolchain.
