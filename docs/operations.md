# Operations

How to run the server outside the disposable demo. For the local demo itself,
see [`demo.md`](demo.md).

This repository has no deployment manifests and no production deployment. The
controls below are the contracts an eventual deployment must satisfy; the
unspecified deployment boundary is tracked as
[F-011](../FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1).

- [Configuration](#configuration)
- [Running against an external database](#running-against-an-external-database)
- [Assertion verification keys](#assertion-verification-keys)
- [Health probes](#health-probes)
- [Database privileges](#database-privileges)
- [Transport security](#transport-security)
- [Telemetry export](#telemetry-export)

## Configuration

`internal/serverruntime` parses the whole environment at startup and fails with
every missing variable named at once.

| Variable | Required | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | Yes | PostgreSQL 18 connection string. The application never migrates during startup. |
| `BOND_EXCHANGE_ASSERTION_ISSUER` | Yes | Expected assertion issuer. |
| `BOND_EXCHANGE_ASSERTION_AUDIENCE` | Yes | Expected assertion audience. |
| `BOND_EXCHANGE_ASSERTION_JWKS_FILE` | Yes | Path to the public verification key set. |
| `BANXICO_SIE_TOKEN` | Yes | 64-character Banxico SIE token; USD quotation is part of the composed API. |
| `BOND_EXCHANGE_ADDRESS` | No | REST listener address; defaults to `127.0.0.1:8080`. |
| `BOND_EXCHANGE_GRPC_ADDRESS` | No | gRPC listener address; defaults to `127.0.0.1:9090`. |
| `OTEL_*` | No | Standard OpenTelemetry configuration. See [Telemetry export](#telemetry-export). |

The SIE token stays owned by the SIE adapter, which is its only consumer and
validates its format. It is never logged or persisted.

## Running against an external database

Apply migrations separately with `dbmate up`, then start only the server. Before
applying migration `20260906120000`, take and verify a restorable backup: that
migration permanently removes the expired `legacy_identifier_archive`, and the
backup is its only recovery path under ADR-0033.

```console
DATABASE_URL=postgresql://user:password@localhost/bond_exchange \
BOND_EXCHANGE_ASSERTION_ISSUER=https://issuer.example \
BOND_EXCHANGE_ASSERTION_AUDIENCE=bond-exchange \
BOND_EXCHANGE_ASSERTION_JWKS_FILE=/run/secrets/issuer.jwks \
BANXICO_SIE_TOKEN=replace-with-64-character-token \
  devenv shell go -C application run ./cmd/server
```

Principals and bonds must already exist. The API has no method for creating
them, so they are provisioned separately before publishing or buying sale
offers. A principal is one insert: `bond_exchange.principals` generates its own
UUIDv7, and it is the only identity a sale offer or purchase can name. See
[`db/README.md`](../db/README.md) for the schema and migration gates that
must pass before a rolling release.

## Assertion verification keys

The JWKS must contain public EdDSA or ES256 signature keys with unique `kid`
values and `use: "sig"`. The key set is read once at startup, so rotation is a
sequence of restarts:

1. Publish the incoming key alongside the retiring one and restart.
2. Move signers to the incoming key.
3. Remove the retiring key and restart again.

Size the overlap for the longest assertion lifetime the issuer grants plus the
signer rollout. Emergency revocation is the last step executed immediately.
[ADR-0024](adr/0024-define-probe-and-key-rotation-contracts.md) records the
contract.

## Health probes

`CheckHealth` is a readiness signal: it requires an assertion, `health.read`,
and a database ping, so an orchestrator must remove a failing instance from
service rather than restart it. Use a TCP connection to either listener as the
liveness signal. [ADR-0024](adr/0024-define-probe-and-key-rotation-contracts.md)
records both contracts.

## Database privileges

A production exchange runtime role should receive only the required `SELECT` and
`INSERT` privileges. A future configured publisher also needs narrowly scoped
`UPDATE` access to integration-event delivery state. Schema ownership belongs to
a separate migration role.

## Transport security

Both listeners are plaintext. Production deployments should provide transport
security at the workload or ingress boundary. Invalid assertions never establish
a principal, so unauthenticated traffic remains subject to deployment-owned
ingress controls rather than the per-principal admission limit.

## Telemetry export

An externally started server does not contact a default collector unless an OTLP
exporter or endpoint is explicitly configured. For example:

```console
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=https://collector.example
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

Export failure does not fail a business request, and shutdown gives providers
five seconds to flush. Structured JSON logs stay on stdout and correlate every
context-aware record with its active trace. See
[`observability.md`](observability.md) for the signal and data-handling
contract.
