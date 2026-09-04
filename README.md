# Bond Exchange

A small bond-sale service backed by an executable TLA+ specification. A user
can publish and buy sale offers; the service does not publish buy offers.

## Prerequisites

Install [Nix](https://nixos.org/download/) and devenv:

```console
nix profile add nixpkgs#devenv
```

`devenv.lock` pins Go, PostgreSQL, dbmate, TLA+, Gremlins, and the other
development tools. They do not need to be installed globally.

## Architecture

The Go server is stateless, so multiple instances can serve requests
concurrently. They share PostgreSQL as the concurrency and durable-idempotency
authority. Users, bonds, sale offers, binding orders/reservations, principals,
RBAC changes, and operation results are append-only facts. A unique purchase
key guarantees that only one buyer wins a race for an offer. A database view
derives the currently active offers.

Successful offer creation and buying also append minimal integration-event
references in the same transaction. A reference contains only the immutable
source table and row ID, its event-schema version, and completion time; payloads
are reconstructed from the source fact in memory. After commit the application
makes one bounded delivery attempt to each configured destination. Delivery is
at least once and consumers must deduplicate by `(table_name, id)`. There is no
automatic retry worker or startup drain; an authorized operator can explicitly
retry pending events. This repository currently configures no concrete event
publisher, so no event leaves the service.

Both internal transports require a short-lived federated JWT assertion bound
to one operation and the deterministic protobuf request digest. Domain
mutations and manual event recovery also require an idempotency key. PostgreSQL
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
use a persistent or externally managed
database instead, supply its `DATABASE_URL` explicitly and start only the
server:

```console
DATABASE_URL=postgresql://user:password@localhost/bond_exchange \
BOND_EXCHANGE_ASSERTION_ISSUER=https://issuer.example \
BOND_EXCHANGE_ASSERTION_AUDIENCE=bond-exchange \
BOND_EXCHANGE_ASSERTION_JWKS_FILE=/run/secrets/issuer.jwks \
  devenv shell go run ./cmd/server
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

- `POST /buys` with `{"sale_offer_id":"offer-1"}`;
- `POST /sale-offers` with
  `{"id":"offer-2","bond_series":"BND2026","price":"99.75","currency_code":"USD"}`;
- `GET /active-offers?bond=BND2026`, which streams every active offer and a
  terminal count as `application/json-seq`;
- `GET /active-bond-series`, which returns every bond series having an active
  offer; and
- `GET /healthz`; and
- `POST /event-publications:publish-pending` with an optional
  `{"destination_id":"..."}`, which explicitly attempts pending integration
  events and returns aggregate counts. It returns an error while no publisher
  is configured.

The matching native gRPC methods are:

- `bondexchange.v1.BondExchangeService/Buy`;
- `bondexchange.v1.BondExchangeService/CreateSaleOffer`;
- `bondexchange.v1.BondExchangeService/ListActiveOffers`;
- `bondexchange.v1.BondExchangeService/ListActiveBondSeries`; and
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
IDEMPOTENCY_KEY=demo-buy-key-0000001
REQUEST='{"sale_offer_id":"demo-offer-1"}'
TOKEN="$(go run ./cmd/demo-auth token "$KEY" demo-buyer purchases.buy "$IDEMPOTENCY_KEY" "$REQUEST")"
curl --header "Authorization: Bearer $TOKEN" \
  --header "Idempotency-Key: $IDEMPOTENCY_KEY" \
  --header 'Content-Type: application/json' \
  --data "$REQUEST" http://127.0.0.1:8080/buys
```

Read operations also need an assertion but omit the idempotency header and use
`-` as the helper's idempotency argument. Assertions expire after two minutes
in the demo and after at most five minutes at the server boundary.

Bond-series input is canonicalized to uppercase at the service boundary. Its
canonical stored form is 3–40 uppercase ASCII letters or digits.

Monetary prices are exact decimal values and are returned as JSON strings, for
example `"price":"100.25"`. Consumers must parse them as decimals rather than
binary floating-point numbers. A price must be greater than zero and fit ten
integer and four fractional digits (maximum `9999999999.9999`); its currency
remains explicit in `currency_code`.

## Verification

Run focused checks with devenv tasks:

```console
devenv tasks run api:check
devenv tasks run spec:check
devenv tasks run db:migrate
devenv tasks run postgres:lifecycle-check
devenv tasks run demo:smoke
devenv tasks run go:test
devenv tasks run go:coverage
devenv tasks run go:mutation
devenv tasks run security:check
```

After editing the Proto3 source, regenerate every checked-in API artifact with
`devenv tasks run api:generate`. `api:check` lints the contract and fails when
the generated Go, Swagger, or descriptor output was stale. Remote schema
dependencies are content-addressed in `api/buf.lock`; update that lock intentionally with
`devenv tasks run api:update-deps`.

Coverage must be at least 90%, and mutation-test efficacy must be at least 80%.
Both gates measure the implementation under `internal/`; the thin server
entrypoint under `cmd/` is compiled and vetted but excluded from those scores.
Every database-dependent task creates its own migrated PostgreSQL cluster on a
private Unix socket and removes it on success, failure, or interruption. This
makes repeated and parallel task invocations independent and avoids requiring a
manually managed database. A raw `go test` outside these tasks may skip the
PostgreSQL integration package when `BOND_EXCHANGE_TEST_DATABASE_URL` is not
set.

Reports are written to `.artifacts/`. `devenv test` runs Nix and shell checks,
API artifact verification, PostgreSQL lifecycle and demo smoke checks,
race-enabled tests, coverage, mutation testing, and the TLC model check.
Coverage and mutation also run as separately visible CI gates.

See the [formal model](spec/tla/README.md), [database design](db/README.md), and
[architecture decisions](docs/adr/README.md) for the boundaries and rationale.
The [ASVS 5.0 Level 3 application profile](docs/security/ASVS.md) records
requirement-level evidence and the deployment and identity controls that remain
pending rather than assumed. The [repository friction register](FRICTIONS.md)
collects verified product, implementation, operations, and contributor rough
edges that remain unresolved.
