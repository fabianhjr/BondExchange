# Bond Exchange

A small bond-sale service backed by an executable TLA+ specification. A user can
publish and buy sale offers; the service does not publish buy offers.

`Buy` records a binding reservation. Settlement, payment, custody, and ownership
transfer are not implemented. This is a reviewable demo of a domain, not a
production trading venue. The [guarantee register](docs/guarantees.md) states
what the service does promise and how each promise is verified; the [friction
register](FRICTIONS.md) and the [failure mode and effects
analysis](docs/FMEA.md) state what it excludes.

## Quick start

Install [Nix](https://nixos.org/download/) and devenv:

```console
nix profile add nixpkgs#devenv
```

Clone with submodules, or initialize them afterwards:

```console
git clone --recurse-submodules https://github.com/fabianhjr/BondExchange.git
git submodule update --init --depth 1 third_party/asvs
```

Start a complete disposable demo environment:

```console
devenv up
```

`devenv.lock` pins Go, PostgreSQL, dbmate, TLA+, golangci-lint, Gremlins, and
the other development tools; they do not need to be installed globally.
`third_party/asvs` pins the OWASP ASVS source that the security profile is
verified against, and `devenv test` and `security:check` fail without it.

The demo brings up a temporary PostgreSQL cluster, both server transports, and a
loopback OpenTelemetry Collector. [`docs/demo.md`](docs/demo.md) walks through
the seeded data and an authenticated request.

## Where to look

| If you want to | Read |
| --- | --- |
| Try the service locally | [`docs/demo.md`](docs/demo.md) |
| Call the REST or gRPC API | [`api/README.md`](api/README.md) |
| Deploy or configure a real instance | [`docs/operations.md`](docs/operations.md) |
| Understand the schema and migrations | [`db/README.md`](db/README.md) |
| Work on the Go module | [`application/README.md`](application/README.md) |
| Understand the domain formally | [`spec/tla/README.md`](spec/tla/README.md) |
| Understand USD-to-MXN conversion | [`docs/exchange-rates.md`](docs/exchange-rates.md) |
| Configure traces, metrics, and logs | [`docs/observability.md`](docs/observability.md) |
| Run or extend the HTTP tests | [`tests/integration/README.md`](tests/integration/README.md) |
| Know what the service guarantees | [`docs/guarantees.md`](docs/guarantees.md) |
| Know why something is the way it is | [`docs/adr/README.md`](docs/adr/README.md) |
| Know what is missing or risky | [`FRICTIONS.md`](FRICTIONS.md), [`docs/FMEA.md`](docs/FMEA.md) |
| Report a vulnerability | [`SECURITY.md`](SECURITY.md) |
| Review the security posture | [`docs/security/ASVS.md`](docs/security/ASVS.md) |

## Architecture at a glance

The Go module and its commands, internal packages, generated bindings, and Go
tool configuration live under [`application/`](application/). Repository-wide
API, database, specification, documentation, and development orchestration stay
at the top level.

- **Servers are stateless.** Multiple instances serve requests concurrently and
  share PostgreSQL as the concurrency and durable-idempotency authority.
- **Domain facts are append-only.** Principals, bonds, sale offers, binding
  orders, RBAC changes, and operation results are inserted, never mutated.
  A unique purchase constraint on the offer UUID guarantees one winner per
  offer race ([ADR-0003](docs/adr/0003-use-append-only-postgresql-facts.md)).
- **PostgreSQL generates identity.** Every table has a database-generated UUIDv7
  primary key; UUIDv4 is reserved for request and lease nonces
  ([ADR-0017](docs/adr/0017-use-postgresql-18-uuidv7-identities-and-uuidv4-nonces.md)).
- **Every request is assertion-bound.** Both transports require a short-lived
  federated JWT bound to one operation and the deterministic protobuf request
  digest; mutations also require a canonical UUIDv4 idempotency nonce.
  PostgreSQL resolves the identity and derives permissions from append-only RBAC
  facts. Buyer and seller identifiers come only from that authenticated
  principal and are omitted from API responses
  ([ADR-0009](docs/adr/0009-bind-federated-authorization-to-idempotent-operations.md)).
- **Admission is coordinated in the database.** After authentication and before
  authorization, PostgreSQL admits at most 100 requests per internal principal
  per database-clock UTC minute, shared across every operation and client ID
  ([ADR-0028](docs/adr/0028-coordinate-per-principal-request-rate-limits-in-postgresql.md)).
- **The marketplace is MXN-only.** USD submissions are converted at intake
  against a pinned Banxico SIE observation; USD survives only as provenance
  ([ADR-0019](docs/adr/0019-canonicalize-sale-offers-to-mxn-at-intake.md)).
- **Same-identity self-trading is prohibited.** Discovery omits the
  authenticated principal's own offers, while Go, PostgreSQL, and TLA+ prevent
  that principal from reserving one directly
  ([ADR-0030](docs/adr/0030-prevent-same-identity-self-trading.md)).
- **Proto3 is the transport contract.**
  [`bond_exchange.proto`](api/proto/bondexchange/v1/bond_exchange.proto)
  generates the Go bindings, the gRPC server, the in-process REST gateway, the
  checked-in Swagger document, and a versioned descriptor set. Transport types
  do not enter the domain or PostgreSQL packages
  ([ADR-0006](docs/adr/0006-use-protobuf-for-rest-and-grpc-api.md)).
- **Integration events are minimal and at-least-once.** Successful mutations
  append an event reference in the same transaction; payloads are reconstructed
  from the source fact. There is no retry worker, and this repository configures
  no publisher, so no event leaves the service
  ([ADR-0011](docs/adr/0011-use-minimal-transactional-event-references.md)).
- **The application owns its telemetry.** OpenTelemetry tracing and metrics
  cover the REST, gRPC, PostgreSQL, Banxico SIE, runtime, and workflow
  boundaries, exporting over OTLP only when standard `OTEL_*` configuration
  enables a signal ([ADR-0025](docs/adr/0025-own-application-opentelemetry-instrumentation.md)).

The [guarantee register](docs/guarantees.md) restates these as promises — each
with the adverse condition it survives, what a caller observes, where it stops,
and the named constraints, properties, and tasks that back it.

The API deliberately has no method for creating principals or bonds yet. Those
facts must be provisioned separately before publishing or buying sale offers.

## Verification

`devenv test` runs the complete gate. Run focused checks while iterating:

| Task | Proves |
| --- | --- |
| `api:check` | Proto3 lints and the generated Go, Swagger, and descriptor artifacts are current. |
| `docs:check` | Documentation links, anchors, indexes, register identifiers, and guarantee citations resolve. |
| `spec:check` | Every TLC instance model-checks and every action is reachable. |
| `db:migrate` | The full migration history applies to a fresh database. |
| `db:canonical-mxn-readiness` | Every active offer has consistent accepted MXN terms. |
| `postgres:lifecycle-check` | Temporary PostgreSQL clusters isolate and clean up. |
| `demo:smoke` | The disposable demo starts and serves. |
| `integration:test` | Documented REST interactions behave as written. |
| `integration:load-smoke` | A small generated workload stays correct under concurrency. |
| `observability:check` | The collector and OpenTelemetry signal contracts hold. |
| `go:check` | `gofmt` and the curated golangci-lint set pass. |
| `go:test` | Go tests pass with the race detector and real PostgreSQL. |
| `go:coverage` | Statement coverage is at least 95%. |
| `go:mutation` | Mutation-test efficacy is at least 95%. |
| `security:check` | ASVS evidence matches the pinned source; Go modules and vulnerabilities are scanned. |

Run one with `devenv tasks run <task>`.

`spec:check` model-checks three TLC instances — marketplace contention,
revocable authorization, and liveness — and fails when any action was never
enabled, because an unreachable action makes every property that depends on it
vacuously true. See the [formal model](spec/tla/README.md) for the properties
each instance checks.

`docs:check` also requires each migration to appear in the database README and
each architecture decision record to appear in its index, and rejects a
reference to a friction, failure-mode, or guarantee identifier that its register
does not define. It further resolves every constraint, trigger, Go identifier,
Proto3 declaration, TLA+ property, and task name that the
[guarantee register](docs/guarantees.md) cites as evidence, so a guarantee
cannot outlive what enforces it
([ADR-0032](docs/adr/0032-publish-a-verified-guarantee-register.md)).
`security:check` verifies the ASVS profile against the pinned
`third_party/asvs` source rather than a contributor-local copy.

Coverage and mutation efficacy both measure `application/internal/`; the thin
entrypoint under `application/cmd/` is compiled and statically analyzed but
excluded from those scores. Every database-dependent task creates its own
migrated PostgreSQL cluster on a private Unix socket and removes it afterwards,
so repeated and parallel invocations stay independent. A raw `go test` from
`application/` outside these tasks may skip the PostgreSQL integration package
when `BOND_EXCHANGE_TEST_DATABASE_URL` is unset; those tests fail instead of
skipping whenever `CI` is set, so a gate cannot report success without having
exercised persistence.

After editing the Proto3 source, regenerate every checked-in API artifact with
`devenv tasks run api:generate`. Remote schema dependencies are
content-addressed in `api/buf.lock`; update that lock intentionally with
`devenv tasks run api:update-deps`.

`sie:record` is intentionally omitted from the verification graph because it
requires a real credential and external network access. Reports and the
golangci-lint cache are written to `.artifacts/`.

`devenv test` reaches every gate through exactly two aggregate tasks: `dev:ci`,
which runs everything except mutation testing, and `go:mutation`. Continuous
integration runs the same two tasks as separately visible jobs, so the local and
CI gates cover the same work. `dev:check` fails when any other task attaches
itself to the test gate, because such a task would run locally and never in CI
([ADR-0020](docs/adr/0020-keep-local-and-ci-gates-equivalent.md)).
