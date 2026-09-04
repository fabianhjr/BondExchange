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

The Go server is stateless, so multiple pods can serve requests concurrently.
They share PostgreSQL as the concurrency authority. Users, bonds, sale offers,
and purchases are append-only facts; a unique purchase key guarantees that
only one buyer wins a race for an offer. A database view derives the currently
active offers.

[`api/proto/bondexchange/v1/bond_exchange.proto`](api/proto/bondexchange/v1/bond_exchange.proto)
is the transport contract. Buf generates Go messages, gRPC server/client
bindings, the REST gateway, and the checked-in
[`Swagger 2.0 artifact`](api/openapi/bondexchange/v1/bond_exchange.swagger.json).
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

The REST server listens on `:8080` and the gRPC server listens on `:9090` by
default. Set `BOND_EXCHANGE_ADDRESS` and `BOND_EXCHANGE_GRPC_ADDRESS` to change
the respective listener addresses. To use a persistent or externally managed
database instead, supply its `DATABASE_URL` explicitly and start only the
server:

```console
DATABASE_URL=postgresql://user:password@localhost/bond_exchange \
  devenv shell go run ./cmd/server
```

Apply migrations to that external database separately with `dbmate up`; the
application never migrates during startup. Both listeners are plaintext;
production deployments should provide transport security at the workload or
ingress boundary. A production runtime role should receive only the required
`SELECT` and `INSERT` privileges; schema ownership belongs to a separate
migration role.

Available endpoints are:

- `POST /buys` with
  `{"buyer_id":"user-2","sale_offer_id":"offer-1"}`;
- `POST /sale-offers` with
  `{"id":"offer-2","seller_id":"user-1","bond_series":"BND2026","price":"99.75","currency_code":"USD"}`;
- `GET /active-offers?bond=BND2026`, which requires a bond series and returns
  every active offer for it;
- `GET /active-bond-series`, which returns every bond series having an active
  offer; and
- `GET /healthz`.

The matching native gRPC methods are:

- `bondexchange.v1.BondExchangeService/Buy`;
- `bondexchange.v1.BondExchangeService/CreateSaleOffer`;
- `bondexchange.v1.BondExchangeService/ListActiveOffers`;
- `bondexchange.v1.BondExchangeService/ListActiveBondSeries`; and
- `bondexchange.v1.BondExchangeService/CheckHealth`.

Server reflection is enabled, so the development shell's `grpcurl` can inspect
and invoke the service. For example:

```console
grpcurl -plaintext localhost:9090 list
grpcurl -plaintext \
  -d '{"buyer_id":"demo-buyer","sale_offer_id":"demo-offer-1"}' \
  localhost:9090 bondexchange.v1.BondExchangeService/Buy
```

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
```

After editing the Proto3 source, regenerate every checked-in API artifact with
`devenv tasks run api:generate`. `api:check` lints the contract and fails when
the generated Go or Swagger output was stale. Remote schema dependencies are
content-addressed in `api/buf.lock`; update that lock intentionally with
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
