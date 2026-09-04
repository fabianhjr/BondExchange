# Database

PostgreSQL stores users, bonds, sale offers, and purchases as append-only
facts. The initial migration is
[`migrations/20260901000000_append_only_exchange.sql`](migrations/20260901000000_append_only_exchange.sql).
The decimal-price migration is
[`migrations/20260902000000_use_decimal_prices.sql`](migrations/20260902000000_use_decimal_prices.sql).

The `bond_exchange.monetary_amount` domain is based on PostgreSQL
`numeric(14,4)`: ten integer digits and four fractional digits, with a maximum
positive value of `9999999999.9999`. Its constraint excludes NaN and infinite
values, while the sale-offer column constraint also rejects zero and negative
prices. PostgreSQL rounds direct inputs having more than four fractional
digits, so provisioning code must validate the scale before insertion when
rounding is not intended. The Go adapter converts the database's exact decimal
text to `decimal.Decimal`; it never passes monetary values through binary
floating point.

A purchase contains the buyer and uses its sale-offer ID as the primary key.
Concurrent inserts for the same offer therefore have one winner even when they
come from different server instances. The losing requests are reported as an
unavailable offer, while the original offer row remains as history.

The non-materialized `bond_exchange.active_offers` view excludes every offer
that has a matching purchase. The API requires a bond series and returns all
active offers for it in ID order; the
`sale_offers_bond_series_id_idx` index supports that lookup. A separate
distinct query derives the sorted list of bond series represented in the view.
The purchase primary key supports the anti-join and enforces single-sale
concurrency. Purchase history by buyer is supported by
`purchases_buyer_id_sale_offer_id_idx`. Add further indexes only for observed
query plans.

The API creates sale offers with `INSERT ... RETURNING`. The sale-offer primary
key serializes concurrent attempts to publish the same ID, while foreign keys
require the seller and bond series to have been provisioned already. Creation
does not require a schema migration because the existing append-only table
already contains the complete sale-offer fact.

Dbmate records applied versions in `schema_migrations` and applies pending
migrations transactionally in strict version order. Migrations are the schema
source of truth, so automatic schema dumps are disabled.

Database-dependent devenv tasks do not share a long-lived PostgreSQL process.
The Nix-packaged `bond-exchange-with-postgres` harness creates a fresh cluster
and private Unix socket for each task, applies pending migrations, exports the
test connection variables, runs the task, and stops and removes the cluster.
Validate the full migration history in isolation with:

```console
devenv tasks run db:migrate
```

The lifecycle check covers successful, failing, repeated, and parallel harness
invocations:

```console
devenv tasks run postgres:lifecycle-check
```

`devenv up` uses the same disposable lifecycle for the local demo. After
migration it loads `demo/seed.sql`, which is deliberately separate from the
production schema history. Demo facts are discarded when the process stops;
the seed is never applied to an external database. To migrate a persistent or
production-like database, set `DATABASE_URL` explicitly and run `dbmate up`.

Create later migrations with `dbmate new <description>`. Do not edit an
already-applied migration; add a new timestamp-versioned migration instead.

Forward migrations must be lossless and compatible with the previously
deployed application. Use separate expand, backfill, and contract migrations:
introduce compatible structures first, preserve source data during backfill,
and contract only redundant compatibility structures after old application
versions can no longer run. Preserve all unique data and prefer a corrective
forward migration when a lossless rollback is not possible.

Statement-level triggers reject `UPDATE`, `DELETE`, and `TRUNCATE` on domain
fact tables as defense in depth. Production runtime roles should not own the
schema and should be granted only the `SELECT` and `INSERT` privileges their
queries require. Migrations and exceptional recovery use a separately
controlled owner.
