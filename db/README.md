# Database

PostgreSQL stores users, bonds, sale offers, and purchases as append-only
facts. The initial migration is
[`migrations/20260901000000_append_only_exchange.sql`](migrations/20260901000000_append_only_exchange.sql).

A purchase contains the buyer and uses its sale-offer ID as the primary key.
Concurrent inserts for the same offer therefore have one winner even when they
come from different server instances. The losing requests are reported as an
unavailable offer, while the original offer row remains as history.

The non-materialized `bond_exchange.active_offers` view excludes every offer
that has a matching purchase. Its optional bond filter and keyset pagination
are supported by the sale-offer primary key and the
`sale_offers_bond_series_id_idx` index. The purchase primary key supports the
anti-join and enforces single-sale concurrency. Purchase history by buyer is
supported by `purchases_buyer_id_sale_offer_id_idx`. Add further indexes only
for observed query plans.

Dbmate records applied versions in `schema_migrations` and applies pending
migrations transactionally in strict version order. Migrations are the schema
source of truth, so automatic schema dumps are disabled.

With `devenv up` running, apply pending migrations from the development shell:

```console
dbmate up
```

The `db:migrate` devenv task starts PostgreSQL when necessary and performs the
same operation for tests and CI:

```console
devenv tasks run db:migrate
```

Create later migrations with `dbmate new <description>`. Do not edit an
already-applied migration; add a new timestamp-versioned migration instead.

Statement-level triggers reject `UPDATE`, `DELETE`, and `TRUNCATE` on domain
fact tables as defense in depth. Production runtime roles should not own the
schema and should be granted only the `SELECT` and `INSERT` privileges their
queries require. Migrations and exceptional recovery use a separately
controlled owner.
