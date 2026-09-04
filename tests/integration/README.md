# HTTP integration and load tests

These tests start the real REST and gRPC server against a newly migrated,
seeded, disposable PostgreSQL database. They exercise only sale-offer creation,
active-offer and active-series listings, and buying; the existing demo smoke
test retains the remaining health, gRPC-discovery, and event-recovery checks.

Run the executable request/response example with:

```console
devenv tasks run integration:test
```

[`http/sale-offer-lifecycle.hurl`](http/sale-offer-lifecycle.hurl) is intended
to be read as API documentation. It discovers and lists seeded offers, creates
an offer, observes it in the active book, buys it, demonstrates an idempotent
retry, and shows that a new buy can no longer acquire it. The REST offer list is
an RFC 7464 JSON Text Sequence, so the runner also parses every frame with
`jq --seq` and verifies ordering and the terminal count.

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
The load runner creates unique authenticated targets for these scenarios:

| Scenario | Required result |
| --- | --- |
| Create | Every unique sale offer returns `201`. |
| List offers | Every request consumes the populated JSON-seq book and returns `200`. |
| List series | Every discovery request returns `200`. |
| Buy | Every generated offer is bought once with `201`. |
| Contended buy | Exactly one request returns `201`; every other request returns `404`. |

Binary Vegeta results and JSON/text summaries are written to
`.artifacts/integration-load/`. These local measurements help compare changes
on the same machine, but they are not production service objectives and no
absolute latency threshold is enforced. The target stream, assertions, and
ephemeral private key are never written to the artifact directory.

`Buy` records a binding reservation only. These tests do not demonstrate
payment, custody, ownership transfer, or settlement.
