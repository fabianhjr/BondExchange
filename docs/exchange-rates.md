# Banxico SIE exchange rates

Sale offers are stored and served only in MXN. USD submission is an outer
intake concern that resolves to accepted MXN terms before the marketplace core
sees an offer. [ADR-0014](adr/0014-persist-and-coordinate-banxico-sie-exchange-rates.md)
and [ADR-0019](adr/0019-canonicalize-sale-offers-to-mxn-at-intake.md) record the
rationale.

- [Packages](#packages)
- [Offer intake](#offer-intake)
- [Fetching and caching](#fetching-and-caching)
- [Append-only history](#append-only-history)
- [Credentials](#credentials)
- [Recorded fixtures](#recorded-fixtures)

## Packages

| Package | Responsibility |
| --- | --- |
| `application/internal/exchangerates` | Provider-neutral types and the on-demand fetch workflow. |
| `application/internal/sie` | Fixed-origin HTTPS adapter for `https://www.banxico.org.mx`. |
| `application/internal/postgres/exchange_rates.go` | Durable observations, coverage, leases, and provider-wide cooldown state. |

A caller supplies an explicit mapping from each SIE series ID to its base and
quote currencies; titles returned by Banxico are not used to infer quote
direction.

## Offer intake

Offer intake is the only transaction consumer. A USD quote fixes the latest
non-stale `SF43718` revision — explicitly mapped as MXN per USD — for five
minutes, and rejects observations dated in the future or more than seven days
old.

Accepting a quote creates immutable MXN terms and retains the USD amount, rate
revision, observation date, rounding policy, and quote as provenance. A quote is
bound to one seller, bond, and USD amount and may be used once.

SIE or cache failure fails USD quotation closed. MXN-native offers and existing
marketplace operations remain available. The exchange core receives, stores,
lists, sells, and publishes only the accepted MXN terms; it has no SIE or
submission-currency dependency, and USD survives only as append-only submission
and conversion provenance.

## Fetching and caching

`Latest` treats data as fresh for 15 minutes by default. One server instance
claims an expired series/currency mapping in PostgreSQL before leaving the
transaction to call SIE. Another instance returns a stored stale value or waits
for a cold cache.

`Range` expands requests into calendar-month coverage units, batches up to 20
series sharing a period, and never refetches a successfully imported closed
month. Empty successful ranges still establish coverage for weekends and
holidays. `RevalidateRange` explicitly checks durable history again.

## Append-only history

Every successful upstream response is retained as an append-only import.
Normalized observations are also append-only, and a value equal to the current
revision is ignored. If a historical value changes, the new value becomes the
current revision while the prior value remains available as provenance. Cache
and lease coordination is mutable operational state rather than a domain fact.

The table-level detail is in
[`db/README.md`](../db/README.md#banxico-sie-storage).

## Credentials

Create the SIE client with a 64-character token obtained from Banxico. The
client sends it only in the `Bmx-Token` header and never persists or logs it.
The production origin is not configurable. Callers may inject an HTTP transport
for tests.

## Recorded fixtures

Offline parser tests replay the fixtures under
`application/internal/sie/testdata/recordings`. The checked-in `.example.json`
fixture is derived from Banxico's published documentation and is clearly labeled
as such.

To capture two real interactions — a latest FIX observation and a fixed
historical range — into a sanitized cassette, run:

```console
BANXICO_SIE_TOKEN=... devenv tasks run sie:record
```

Recording is explicit and is never part of CI, because it requires a real
credential and external network access. Its output is exercised offline by
`go:test`, coverage, and mutation checks.

The recorder rejects a response that echoes the supplied token, records the
request credential only as `<REDACTED>`, retains only selected response headers,
and writes the sanitized file atomically. Review the resulting `banxico_sie.json`
before committing it.
