# Database

PostgreSQL stores users, bonds, sale offers, and purchases as append-only
facts. The initial migration is
[`migrations/20260901000000_append_only_exchange.sql`](migrations/20260901000000_append_only_exchange.sql).
The decimal-price migration is
[`migrations/20260902000000_use_decimal_prices.sql`](migrations/20260902000000_use_decimal_prices.sql).
The security-fact migration is
[`migrations/20260903000000_append_only_security.sql`](migrations/20260903000000_append_only_security.sql).
The reflection-permission retirement is
[`migrations/20260903120000_retire_reflection_permission.sql`](migrations/20260903120000_retire_reflection_permission.sql).
Minimal integration-event references and their delivery coordination are added
by
[`migrations/20260903130000_add_integration_event_references.sql`](migrations/20260903130000_add_integration_event_references.sql).

The `bond_exchange.monetary_amount` domain is based on PostgreSQL
`numeric(14,4)`: ten integer digits and four fractional digits, with a maximum
positive value of `9999999999.9999`. Its constraint excludes NaN and infinite
values, while the sale-offer column constraint also rejects zero and negative
prices. PostgreSQL rounds direct inputs having more than four fractional
digits, so provisioning code must validate the scale before insertion when
rounding is not intended. The Go adapter converts the database's exact decimal
text to `decimal.Decimal`; it never passes monetary values through binary
floating point.

A purchase records the buyer's binding order or reservation and uses its
sale-offer ID as the primary key. Settlement, payment, custody, ownership
transfer, expiry, and cancellation are not represented and remain pending.
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

Federated identities are linked to internal users by the unique
`(issuer, subject)` pair in `principals`; the API never accepts that user ID as
operation input. `client_class` distinguishes human and automated principals.
Roles and permissions have append-only grant and revocation tables, and
`effective_principal_permissions` derives current access while excluding
revoked grants and suspended principals. A reinstatement references exactly
one suspension rather than modifying it.

Runtime gRPC reflection was removed after the initial security facts were
recorded. The original `reflection.use` permission and operator grant remain as
immutable history; the later migration appends their revocation, so the
initial operator grant is no longer effective. Operators use the versioned API
descriptor set instead.

Mutation idempotency uses `operation_claims`, uniquely scoped by principal,
client ID, operation, and idempotency key. The claim keeps SHA-256 request and
assertion digests, and `operation_results` stores the immutable outcome. An
exact retry can use a fresh assertion and recover the original resource; using
the same scope for another request is rejected. These tables intentionally
retain audit history and have no destructive down migration.

An `AFTER INSERT` trigger on successful operation results maps `offers.create`
to `(sale_offers, offer_id)` and `purchases.buy` to
`(purchases, sale_offer_id)`. It appends that composite source reference, event
schema version, and completion time to `integration_events` in the mutation's
transaction. Rejections and unrelated operations append nothing. The table has
no event ID, sequence, serialized payload, or copied domain columns. Event data
is reconstructed through fixed, versioned queries against the immutable source
facts immediately before publication. The narrowly scoped trigger function runs
as its migration owner with a fixed system search path, so applying the expand
migration does not require a previously deployed exchange writer to gain a new
table privilege.

`integration_event_deliveries` is mutable operational coordination rather than
a domain-fact table. Its primary key adds a stable destination ID to the source
reference. Short leases coordinate overlapping immediate and operator-triggered
attempts; delivery acknowledgement, attempt count, next-attempt time, and a
sanitized error class are the only retained delivery data. External calls occur
after the claiming transaction commits. A crash after destination acceptance
but before acknowledgement can cause a duplicate, so consumers deduplicate by
the composite source reference. Runtime `UPDATE` privilege is required only on
this delivery table; integration events and all domain facts remain append-only.

The application attempts publication once after each successful mutation. It
does not scan pending events on startup or run a scheduled worker. An authorized
API operation performs an explicit recovery scan. With no configured concrete
publisher, references accumulate without delivery.

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
schema. Exchange writers need only the `SELECT` and `INSERT` privileges their
queries require; a configured event publisher additionally needs narrowly
scoped `UPDATE` on `integration_event_deliveries`. Migrations and exceptional
recovery use a separately controlled owner.
