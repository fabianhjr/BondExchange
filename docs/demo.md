# Local demo

The demo is a complete, disposable environment: a temporary PostgreSQL cluster,
both server transports, and a loopback OpenTelemetry Collector. It starts from
the same known state every time and leaves nothing behind.

- [Start it](#start-it)
- [What you get](#what-you-get)
- [Making an authenticated request](#making-an-authenticated-request)
- [Value formats](#value-formats)
- [Calling gRPC](#calling-grpc)

## Start it

```console
devenv up
```

Devenv creates a temporary PostgreSQL cluster, applies every dbmate migration,
loads the fixtures in `db/demo/seed.sql`, starts both server transports, and
starts a loopback OpenTelemetry Collector that prints a basic trace and metric
representation to its process log. Stopping `devenv up` also stops PostgreSQL
and removes the demo database.

## What you get

The REST server listens on `127.0.0.1:8080` and the gRPC server listens on
`127.0.0.1:9090`. Set `BOND_EXCHANGE_ADDRESS` and `BOND_EXCHANGE_GRPC_ADDRESS`
to change the respective listener addresses.

The seed provides:

- users `demo-seller`, `demo-buyer`, and the dedicated `demo-rate-limited`
  verification principal;
- bonds `DEMO2026` and `DEMO2027`; and
- three active offers.

Every REST route and gRPC method is listed in
[`api/README.md`](../api/README.md#endpoints).

## Making an authenticated request

The demo generates an ephemeral signing key and prints its path. Use the
development-only `demo-auth` helper to create an assertion whose request JSON
exactly matches the request being sent:

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
`-` as the helper's idempotency argument.

Assertions expire after two minutes in the demo and after at most five minutes
at the server boundary. Mutation clients generate a fresh random UUIDv4 for a
new operation and retain that nonce with the exact request for retries.
PostgreSQL generates resource UUIDv7 values; create clients must use the
returned ID rather than supplying one.

The offer book is a tradable view for the authenticated principal. The demo
seller therefore does not see the three offers attributed to `demo-seller`, and
an attempt to buy one directly is rejected even when its UUID is known. The
separate `demo-buyer` principal sees and can reserve those offers.

## Value formats

Bond-series input is canonicalized to uppercase at the service boundary. Its
canonical stored form is 3–40 uppercase ASCII letters or digits.

Monetary prices are exact decimal values and are returned as JSON strings, for
example `"price":"100.25"`. Consumers must parse them as decimals rather than
binary floating-point numbers. A price must be greater than zero and fit ten
integer and four fractional digits (maximum `9999999999.9999`).

Every served offer has `currency_code: "MXN"`. A USD submission is multiplied by
its pinned FIX observation and rounded once to four places using half-to-even.
See [`exchange-rates.md`](exchange-rates.md) for the quote workflow.

## Calling gRPC

The server does not register gRPC reflection. Tools such as `grpcurl` must use
the versioned descriptor set explicitly, for example with
`-protoset api/descriptors/bondexchange.protoset`. This keeps schema discovery
offline and prevents a runtime introspection endpoint from bypassing the
application authorization model.
