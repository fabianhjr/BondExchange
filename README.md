# Bond Exchange

A small bond-sale service backed by an executable TLA+ specification. A user
can buy one existing sale offer; the service does not publish buy offers.

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

The HTTP service deliberately has no endpoint for creating users, bonds, or
sale offers yet. Those facts must be provisioned separately before exercising
the buy and active-offer endpoints.

## Run locally

Start PostgreSQL in one shell. In another, apply pending migrations and run
the server:

```console
devenv up
devenv shell dbmate up
devenv shell go run ./cmd/server
```

The server listens on `:8080` by default. Set `BOND_EXCHANGE_ADDRESS` to change
the address and `DATABASE_URL` to change the PostgreSQL connection string. A
production runtime role should receive only the required `SELECT` and `INSERT`
privileges; schema ownership belongs to a separate migration role.

Available endpoints are:

- `POST /buys` with
  `{"buyer_id":"user-2","sale_offer_id":"offer-1"}`;
- `GET /active-offers`, with optional `bond`, `after`, and `limit` query
  parameters; and
- `GET /healthz`.

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
devenv tasks run spec:check
devenv tasks run db:migrate
devenv tasks run go:test
devenv tasks run go:coverage
devenv tasks run go:mutation
```

Coverage must be at least 90%, and mutation-test efficacy must be at least 80%.
Both gates measure the implementation under `internal/`; the thin server
entrypoint under `cmd/` is compiled and vetted but excluded from those scores.
Reports are written to `.artifacts/`. `devenv test` runs formatting, static
analysis, pending dbmate migrations, race-enabled tests, coverage, mutation
testing, and the TLC model check. Coverage and mutation also run as separately
visible CI gates.

See the [formal model](spec/tla/README.md), [database design](db/README.md), and
[architecture decisions](docs/adr/README.md) for the boundaries and rationale.
