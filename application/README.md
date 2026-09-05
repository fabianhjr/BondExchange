# Go application

This directory is the Go module for the Bond Exchange service. It contains:

- `cmd/server`, the production composition root;
- `cmd/demo-auth`, the development-only assertion issuer;
- `cmd/load-targets`, the development-only authenticated Vegeta target
  generator;
- `cmd/internal/demoauth`, shared assertion support for those development
  commands;
- `internal/exchange`, the MXN-only marketplace core;
- `internal/offerintake`, the outer MXN/USD submission and accepted FIX-quote
  workflow;
- `internal/serverruntime`, the composition decisions the server command used
  to make inline: environment parsing, verification-key loading, pool and
  transport limits, listener binding, and shutdown;
- the remaining `internal/` packages, including provider-neutral rates, SIE,
  transport, eventing, and PostgreSQL adapters; and
- `gen/go/`, the checked-in Go bindings generated from the Proto3 contract in
  `../api/`.

Run ordinary Go commands from this directory, for example:

```console
go test ./...
go run ./cmd/server
```

A bare `go test` is convenient while iterating, but it skips the PostgreSQL
integration tests in `internal/postgres` unless
`BOND_EXCHANGE_TEST_DATABASE_URL` points at a migrated database. Use
`devenv tasks run go:test` from the repository root to provision that database
automatically. The skip becomes a failure whenever `CI` is set, so an automated
gate cannot pass without persistence coverage.

Prefer the repository-root devenv tasks for complete checks because they also
provision PostgreSQL, validate generated artifacts, and run the other project
quality gates. Generated bindings must be changed through the root
`api:generate` task, not edited directly.

`cmd/server` requires `BANXICO_SIE_TOKEN` because USD quotation is part of the
composed API. The token remains owned by the SIE adapter, which is its only
consumer and validates its format; `internal/serverruntime` reads it alongside
the rest of the environment so that a missing credential fails startup with
every other missing variable named at once, and it is never logged or
persisted. The marketplace core does not import the intake or exchange-rate
packages and accepts only MXN.
