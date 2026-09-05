# Bond Exchange

A small bond-sale service backed by an executable TLA+ specification. A user
can publish and buy sale offers; the service does not publish buy offers.

## Prerequisites

Install [Nix](https://nixos.org/download/) and devenv:

```console
nix profile add nixpkgs#devenv
```

`devenv.lock` pins Go, PostgreSQL, dbmate, TLA+, golangci-lint, Gremlins, and
the other development tools. They do not need to be installed globally.

Clone with submodules, or initialize them afterwards:

```console
git clone --recurse-submodules https://github.com/fabianhjr/BondExchange.git
git submodule update --init --depth 1 third_party/asvs
```

`third_party/asvs` pins the OWASP ASVS source that the security profile is
verified against. `devenv test` and `security:check` fail without it.

## Architecture

The Go module and its commands, internal packages, generated bindings, and Go
tool configuration live under [`application/`](application/). Repository-wide
API, database, specification, documentation, and development orchestration stay
at the top level.

The Go server is stateless, so multiple instances can serve requests
concurrently. They share PostgreSQL as the concurrency and durable-idempotency
authority. Every table has a database-generated UUIDv7 primary key; UUIDv4 is
reserved for request and lease nonces. Users, bonds, sale offers, binding orders/reservations, principals,
RBAC changes, and operation results are append-only facts. A unique purchase
constraint on the offer UUID guarantees that only one buyer wins a race for an offer. A database view
derives the currently active offers.

Successful offer creation and buying also append minimal integration-event
references in the same transaction. An event has its own UUIDv7 and contains
only the immutable source table and source UUID, its event-schema version, and completion time; payloads
are reconstructed from the source fact in memory. After commit the application
makes one bounded delivery attempt to each configured destination. Delivery is
at least once and consumers must deduplicate by the event UUID. There is no
automatic retry worker or startup drain; an authorized operator can explicitly
retry pending events. This repository currently configures no concrete event
publisher, so no event leaves the service.

Both internal transports require a short-lived federated JWT assertion bound
to one operation and the deterministic protobuf request digest. Domain
mutations and manual event recovery also require a canonical UUIDv4 idempotency
nonce. PostgreSQL
resolves the federated identity and derives permissions from append-only RBAC
grants and revocations. Buyer and seller identifiers come only from that
authenticated principal and are omitted from API responses.

[`api/proto/bondexchange/v1/bond_exchange.proto`](api/proto/bondexchange/v1/bond_exchange.proto)
is the transport contract. Buf generates Go messages, gRPC server/client
bindings, the REST gateway, and the checked-in
[`Swagger 2.0 artifact`](api/openapi/bondexchange/v1/bond_exchange.swagger.json).
It also produces a versioned
[`FileDescriptorSet`](api/descriptors/bondexchange.protoset) for gRPC tooling.
One application adapter implements the generated service and calls the domain
service; both the native gRPC server and the in-process REST gateway use that
adapter. Transport types do not enter the domain or PostgreSQL packages.

The API deliberately has no method for creating users or bonds yet. Those
facts must be provisioned separately before publishing or buying sale offers.

The repository also contains an internal Banxico SIE exchange-rate module. It
fetches explicitly mapped currency series through the SIE latest-data and
historical-range endpoints, parses values as exact decimals, and persists both
the bounded source response and normalized observations in PostgreSQL. This
module is not exposed by the REST or gRPC service and does not reprice offers or
change purchase behavior.

## Run locally

Start a complete disposable demo environment with one command:

```console
devenv up
```

Devenv creates a temporary PostgreSQL cluster, applies every dbmate migration,
loads the fixtures in `db/demo/seed.sql`, and starts both server transports.
Stopping `devenv up` also stops PostgreSQL and removes the demo database. Each
new demo therefore starts from the same known state with users `demo-seller`
and `demo-buyer`, bonds `DEMO2026` and `DEMO2027`, and three active offers.

The REST server listens on `127.0.0.1:8080` and the gRPC server listens on
`127.0.0.1:9090` by default. Set `BOND_EXCHANGE_ADDRESS` and
`BOND_EXCHANGE_GRPC_ADDRESS` to change the respective listener addresses. To
use a persistent or externally managed PostgreSQL 18 database instead, supply
its `DATABASE_URL` explicitly and start only the
server:

```console
DATABASE_URL=postgresql://user:password@localhost/bond_exchange \
BOND_EXCHANGE_ASSERTION_ISSUER=https://issuer.example \
BOND_EXCHANGE_ASSERTION_AUDIENCE=bond-exchange \
BOND_EXCHANGE_ASSERTION_JWKS_FILE=/run/secrets/issuer.jwks \
  devenv shell go -C application run ./cmd/server
```

The JWKS must contain public EdDSA or ES256 signature keys with unique `kid`
values and `use: "sig"`. Apply migrations to that external database separately with `dbmate up`; the
application never migrates during startup. Both listeners are plaintext;
production deployments should provide transport security at the workload or
ingress boundary. A production exchange runtime role should receive only the
required `SELECT` and `INSERT` privileges. A future configured publisher also
needs narrowly scoped `UPDATE` access to integration-event delivery state;
schema ownership belongs to a separate migration role.

Available endpoints are:

- `POST /buys` with a UUIDv7, for example
  `{"sale_offer_id":"01991a20-0000-7000-8000-000000000101"}`;
- `POST /sale-offers` with
  `{"bond_series":"BND2026","price":"99.75","currency_code":"USD"}`;
- `GET /active-offers?bond=BND2026`, which streams every active offer and a
  terminal count as `application/json-seq`;
- `GET /active-bond-series`, which returns every bond series having an active
  offer;
- `GET /healthz`; and
- `POST /event-publications:publish-pending` with an optional
  `{"destination_id":"..."}`, which explicitly attempts pending integration
  events and returns aggregate counts. It returns an error while no publisher
  is configured.

The matching native gRPC methods are:

- `bondexchange.v1.BondExchangeService/Buy`;
- `bondexchange.v1.BondExchangeService/CreateSaleOffer`;
- `bondexchange.v1.BondExchangeService/ListActiveOffers`;
- `bondexchange.v1.BondExchangeService/ListActiveBondSeries`;
- `bondexchange.v1.BondExchangeService/CheckHealth`; and
- `bondexchange.v1.BondExchangeService/PublishPendingEvents`.

The server does not register gRPC reflection. Tools such as `grpcurl` must use
the versioned descriptor set explicitly, for example with
`-protoset api/descriptors/bondexchange.protoset`. This keeps schema discovery
offline and prevents a runtime introspection endpoint from bypassing the
application authorization model.

The disposable demo generates an ephemeral signing key and prints its path.
Use the development-only `demo-auth` helper to create an assertion whose
request JSON exactly matches the request being sent. For example:

```console
KEY=/path/printed/by/devenv/private.jwk
IDEMPOTENCY_KEY=00000000-0000-4000-8000-000000000021
REQUEST='{"sale_offer_id":"01991a20-0000-7000-8000-000000000101"}'
TOKEN="$(go -C application run ./cmd/demo-auth token "$KEY" demo-buyer purchases.buy "$IDEMPOTENCY_KEY" "$REQUEST")"
curl --header "Authorization: Bearer $TOKEN" \
  --header "Idempotency-Key: $IDEMPOTENCY_KEY" \
  --header 'Content-Type: application/json' \
  --data "$REQUEST" http://127.0.0.1:8080/buys
```

Read operations also need an assertion but omit the idempotency header and use
`-` as the helper's idempotency argument. Assertions expire after two minutes
in the demo and after at most five minutes at the server boundary. Mutation
clients generate a fresh random UUIDv4 for a new operation and retain that
nonce with the exact request for retries. PostgreSQL generates resource UUIDv7
values; create clients must use the returned ID rather than supplying one.

Bond-series input is canonicalized to uppercase at the service boundary. Its
canonical stored form is 3–40 uppercase ASCII letters or digits.

Monetary prices are exact decimal values and are returned as JSON strings, for
example `"price":"100.25"`. Consumers must parse them as decimals rather than
binary floating-point numbers. A price must be greater than zero and fit ten
integer and four fractional digits (maximum `9999999999.9999`); its currency
remains explicit in `currency_code`.

## Banxico SIE exchange rates

`application/internal/exchangerates` owns provider-neutral types and the
on-demand fetch workflow. `application/internal/sie` is the fixed-origin HTTPS
adapter for `https://www.banxico.org.mx`, and
`application/internal/postgres/exchange_rates.go` provides
durable observations, coverage, leases, and provider-wide cooldown state. A
caller supplies an explicit mapping from each SIE series ID to its base and
quote currencies; titles returned by Banxico are not used to infer quote
direction.

`Latest` treats data as fresh for 15 minutes by default. One server instance
claims an expired series/currency mapping in PostgreSQL before leaving the
transaction to call SIE. Another instance returns a stored stale value or
waits for a cold cache.
`Range` expands requests into calendar-month coverage units, batches up to 20
series sharing a period, and never refetches a successfully imported closed
month. Empty successful ranges still establish coverage for weekends and
holidays. `RevalidateRange` explicitly checks durable history again.

Every successful upstream response is retained as an append-only import.
Normalized observations are also append-only and a value equal to the current
revision is ignored. If a historical value changes, the new value becomes the
current revision while the prior value remains available as provenance. Cache
and lease coordination is mutable operational state rather than a domain fact.

Create the SIE client with a 64-character token obtained from Banxico. The
client sends it only in the `Bmx-Token` header and never persists or logs it.
The production origin is not configurable. Callers may inject an HTTP
transport for tests.

Offline parser tests replay the fixtures under
`application/internal/sie/testdata/recordings`. The checked-in `.example.json`
fixture is
derived from Banxico's published documentation and is clearly labeled as such.
To capture two real interactions—a latest FIX observation and a fixed
historical range—into a sanitized cassette, run:

```console
BANXICO_SIE_TOKEN=... devenv tasks run sie:record
```

Recording is explicit and is never part of CI. The recorder rejects a response
that echoes the supplied token, records the request credential only as
`<REDACTED>`, retains only selected response headers, and writes the sanitized
file atomically. Review the resulting `banxico_sie.json` before committing it.

## Verification

Run focused checks with devenv tasks:

```console
devenv tasks run api:check
devenv tasks run docs:check
devenv tasks run spec:check
devenv tasks run db:migrate
devenv tasks run db:uuid-contract-history
devenv tasks run postgres:lifecycle-check
devenv tasks run demo:smoke
devenv tasks run integration:test
devenv tasks run integration:load-smoke
devenv tasks run go:check
devenv tasks run go:test
devenv tasks run go:coverage
devenv tasks run go:mutation
devenv tasks run security:check
```

`integration:test` starts the complete disposable server and runs the readable
[`create`](tests/integration/http/sale-offer-create.hurl) and
[`post-create lifecycle`](tests/integration/http/sale-offer-lifecycle.hurl)
scenarios.
It covers active-series and active-offer listings, sale-offer creation, buying,
idempotent retry, and removal from the active book. `integration:load-smoke`
uses generated request-bound assertions to exercise distinct creates and buys,
both listings, and a contended buy with exactly one winner.

For a larger local run, set the request count, rate per second, and maximum
workers. The count must be divisible by the rate:

```console
BOND_EXCHANGE_LOAD_COUNT=1000 \
BOND_EXCHANGE_LOAD_RATE=100 \
BOND_EXCHANGE_LOAD_WORKERS=40 \
  devenv tasks run integration:load
```

Each resulting load phase is limited to 90 seconds so its short-lived demo
assertions remain valid. The manual task defaults to 1,000 operations per main
phase; the correctness-gated smoke profile uses 120.

Vegeta reports are written under `.artifacts/integration-load/`. They are
repeatable local baselines rather than production service objectives; the
default gate checks response correctness and status distributions, not an
absolute latency threshold. See the
[`integration test guide`](tests/integration/README.md) for the scenarios and
artifact format.

`sie:record` is intentionally omitted from the normal verification graph
because it requires a real credential and external network access. Its output
is exercised offline by `go:test`, coverage, and mutation checks.

After editing the Proto3 source, regenerate every checked-in API artifact with
`devenv tasks run api:generate`. `api:check` lints the contract and fails when
the generated Go, Swagger, or descriptor output was stale. Remote schema
dependencies are content-addressed in `api/buf.lock`; update that lock intentionally with
`devenv tasks run api:update-deps`.

`docs:check` resolves every relative documentation link and heading anchor,
requires each migration to appear in the database README and each architecture
decision record to appear in its index, and rejects a reference to a friction
or failure-mode identifier that its register does not define. `security:check`
additionally verifies the ASVS profile against the pinned `third_party/asvs`
source rather than against a contributor-local copy.

`go:check` verifies `gofmt` and runs the pinned golangci-lint standard set plus
the curated correctness, security, context, resource-lifecycle, logging, test,
and dependency-direction checks in `application/.golangci.yml`. Suppressions
must identify one or more specific linters and explain why the flagged
construct is safe.
Coverage and mutation-test efficacy must each be at least 95%.
Both gates measure the implementation under `application/internal/`; the thin
server entrypoint under `application/cmd/` is compiled and statically analyzed
but excluded from those scores.
Every database-dependent task creates its own migrated PostgreSQL cluster on a
private Unix socket and removes it on success, failure, or interruption. This
makes repeated and parallel task invocations independent and avoids requiring a
manually managed database. A raw `go test` from `application/` outside these
tasks may skip the PostgreSQL integration package when
`BOND_EXCHANGE_TEST_DATABASE_URL` is not set. Those tests fail instead of
skipping whenever `CI` is set, so a quality gate cannot report success without
having exercised persistence.

Reports and the golangci-lint cache are written to `.artifacts/`. `devenv test`
runs Nix and shell checks, Go formatting and static analysis, API artifact
verification, migration archival, PostgreSQL lifecycle and demo smoke checks,
readable HTTP integration tests, a small generated load check, race-enabled
tests, coverage, mutation testing, and the TLC model check.

`devenv test` reaches those gates through exactly two aggregate tasks:
`dev:ci`, which runs everything except mutation testing, and `go:mutation`.
Continuous integration runs the same two tasks as separately visible jobs, so
the local and CI gates cover the same work. `dev:check` fails when any other
task attaches itself to the test gate, because such a task would run locally
and never run in CI.

See the [formal model](spec/tla/README.md), [database design](db/README.md), and
[architecture decisions](docs/adr/README.md) for the boundaries and rationale.
The [failure mode and effects analysis](docs/FMEA.md) ranks system-level
failure paths, records their current prevention and detection controls, and
links residual risks to required follow-up.
The [ASVS 5.0 Level 3 application profile](docs/security/ASVS.md) records
requirement-level evidence and the deployment and identity controls that remain
pending rather than assumed. The [repository friction register](FRICTIONS.md)
collects verified product, implementation, operations, and contributor rough
edges that remain unresolved. The [security policy](SECURITY.md) describes how
to report a vulnerability, who responds, and the scheduled scan that bounds
confirmation time.
