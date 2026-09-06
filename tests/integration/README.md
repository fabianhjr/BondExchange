# HTTP integration and load tests

These tests start the real REST and gRPC server against a newly migrated,
seeded, disposable PostgreSQL database. They exercise USD quotation and
sale-offer creation,
active-offer and active-series listings, and buying; the existing demo smoke
test retains the remaining health, gRPC-discovery, gRPC-transport, and
event-recovery checks.

Run the executable request/response example with:

```console
devenv tasks run integration:test
```

## Scenarios

The four Hurl files are intended to be read as API documentation. The runner
arranges fixtures and binds assertions; every documented request and response
lives in a scenario file.

| Scenario | Shows |
| --- | --- |
| [`sale-offer-quote.hurl`](http/sale-offer-quote.hurl) | Pricing a USD submission against a pinned `SF43718` FIX observation, and replaying that quote instead of repricing it. |
| [`sale-offer-create.hurl`](http/sale-offer-create.hurl) | Accepting the quote, returning only canonical MXN terms, and receiving the PostgreSQL-generated offer UUIDv7. |
| [`sale-offer-lifecycle.hurl`](http/sale-offer-lifecycle.hurl) | Discovery, listing, buying, idempotent retry, self-trade rejection, and removal from the active book. |
| [`authentication-failures.hurl`](http/authentication-failures.hurl) | Every way an assertion, nonce, or JSON envelope can be refused. |
| [`offer-intake-failures.hurl`](http/offer-intake-failures.hurl) | Every way a conversion quote can be misused, and an idempotency conflict. |

The lifecycle scenario discovers and lists seeded offers, observes the created
offer in the active book from the buyer's tradable view, verifies the seller
cannot discover or reserve its own offer, replays that durable rejection, buys
from the separate buyer, demonstrates a successful idempotent retry, and shows
that a new buy can no longer acquire it. The REST offer list is
an RFC 7464 JSON Text Sequence, so the runner also parses every frame with
`jq --seq` and verifies ordering and the terminal count.
The runner also preloads the dedicated `demo-rate-limited` principal's
current window, verifies HTTP `429`, a bounded integer `Retry-After`, and the
generic error body, then moves the operational window back and verifies the
next request is admitted.

## Rejection paths

`authentication-failures.hurl` proves that the composed server enforces the
assertion contract, not only that the packages do. It covers a missing header,
a malformed bearer value, a non-Bearer scheme, an assertion signed by a second
ephemeral issuer, an assertion issued for a different operation, an assertion
bound to a different canonical request, a suspended principal, a principal that
holds no role, a missing nonce, a UUIDv7 used as a nonce, a nonce sent on a
read, an unsupported media type, and a duplicated JSON object key.

Two of those depend on seeded fixtures that exist only for this purpose.
`demo-unauthorized` resolves and is refused with `403`, while `demo-suspended`
retains its trader grant but does not resolve at all and is refused with `401`.
The difference is deliberate: suspension is an appended fact that removes every
permission through `effective_principal_permissions`, and the API reports it
generically so a caller cannot enumerate identities.

`offer-intake-failures.hurl` runs last and against `DEMO2027`, so the
`DEMO2026` book the other scenarios describe stays exactly as they assert it.

Run the short correctness-gated generated workload with:

```console
devenv tasks run integration:load-smoke
```

Run the configurable workload with:

```console
BOND_EXCHANGE_LOAD_COUNT=1000 \
BOND_EXCHANGE_LOAD_RATE=100 \
BOND_EXCHANGE_LOAD_WORKERS=40 \
  devenv tasks run integration:load
```

`BOND_EXCHANGE_LOAD_COUNT` must be divisible by
`BOND_EXCHANGE_LOAD_RATE`; their quotient is the attack duration in seconds and
must not exceed 90 seconds so the demo assertions remain valid.
The manual task defaults to 1,000 operations per main phase at 50 requests per
second with at most 20 workers. The CI smoke profile uses 120 operations per
main phase at 12 requests per second with at most 12 workers.
The load runner provisions disposable sellers and buyers, rotates targets
through enough principals that each remains within its shared allowance, and
creates unique authenticated targets for these scenarios:

| Scenario | Required result |
| --- | --- |
| Create | Every independently idempotent MXN request returns `201` with a distinct UUIDv7. |
| List offers | Every request consumes the caller's populated tradable JSON-seq book and returns `200`. |
| List series | Every discovery request returns `200` with the caller's tradable series. |
| Buy | The runner discovers every generated UUIDv7 and buys each offer once with `201`. |
| Contended buy | Exactly one request returns `201`; every other request returns `404`. |

Binary Vegeta results and JSON/text summaries are written to
`.artifacts/integration-load/`. These local measurements help compare changes
on the same machine, but they are not production service objectives and no
absolute latency threshold is enforced. The target stream, assertions, and
ephemeral private key are never written to the artifact directory.

`Buy` records a binding reservation only. These tests do not demonstrate
payment, custody, ownership transfer, or settlement.
