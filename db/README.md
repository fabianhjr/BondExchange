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
Append-only Banxico SIE imports and exchange-rate observations, plus mutable
fetch coordination, are added by
[`migrations/20260904120000_add_sie_exchange_rates.sql`](migrations/20260904120000_add_sie_exchange_rates.sql).
The PostgreSQL 18 UUID identity and nonce cutover is
[`migrations/20260904130000_use_uuid_keys_and_nonces.sql`](migrations/20260904130000_use_uuid_keys_and_nonces.sql).
UUID-only writer compatibility is prepared by
[`migrations/20260904140000_prepare_uuid_only_writers.sql`](migrations/20260904140000_prepare_uuid_only_writers.sql),
non-derivable history is preserved by
[`migrations/20260904150000_archive_legacy_identifiers.sql`](migrations/20260904150000_archive_legacy_identifiers.sql),
and the operational legacy graph is removed by
[`migrations/20260904160000_contract_legacy_identifier_graph.sql`](migrations/20260904160000_contract_legacy_identifier_graph.sql).
Canonical view names and business-code column names are finalized by
[`migrations/20260904170000_finalize_uuid_contract.sql`](migrations/20260904170000_finalize_uuid_contract.sql).

PostgreSQL 18 is required. Every application table has a `uuid_id uuid`
primary key generated with `uuidv7()` and constrained with
`uuid_extract_version(uuid_id) = 7`. Natural identifiers remain unique lookup
attributes. The original rolling migration retained legacy keys, foreign keys,
views, and synchronization triggers. The contract migration removes that live
graph after verifying archive coverage and a quiescent lease window. The
current Go adapter uses only UUID relationships and the canonical unversioned
views. The transitional `_v2` aliases have been removed.

Non-derivable values needed for audit and reconciliation are copied into the
append-only `legacy_identifier_archive` before contraction. It is keyed by
UUIDv7 and maps an entity UUID to its historical caller-selected identifier,
pre-UUID idempotency value, or SIE import sequence. It is not a supported
runtime alias lookup and receives no mutable application access.

The `bond_exchange.monetary_amount` domain is based on PostgreSQL
`numeric(14,4)`: ten integer digits and four fractional digits, with a maximum
positive value of `9999999999.9999`. Its constraint excludes NaN and infinite
values, while the sale-offer column constraint also rejects zero and negative
prices. PostgreSQL rounds direct inputs having more than four fractional
digits, so provisioning code must validate the scale before insertion when
rounding is not intended. The Go adapter converts the database's exact decimal
text to `decimal.Decimal`; it never passes monetary values through binary
floating point.

A purchase records the buyer's binding order or reservation and has its own
UUIDv7 primary key. Settlement, payment, custody, ownership
transfer, expiry, and cancellation are not represented and remain pending.
The unique `sale_offer_uuid` constraint gives concurrent inserts for the same offer one winner even when they
come from different server instances. The losing requests are reported as an
unavailable offer, while the original offer row remains as history.

The current adapter reads the non-materialized
`bond_exchange.active_offers` view, which excludes every offer
that has a matching purchase. The API requires a bond series and returns all
active offers for it in ID order; the
`sale_offers_bond_uuid_uuid_id_idx` index supports the relationship join. A separate
distinct query derives the sorted list of bond series represented in the view.
The unique offer constraint supports the anti-join and enforces single-sale
concurrency. Purchase history by buyer is supported by
`purchases_buyer_uuid_sale_offer_uuid_idx`. Add further indexes only for observed
query plans.

The API creates sale offers with `INSERT ... RETURNING`; PostgreSQL generates
the UUIDv7 resource identity. Clients do not supply an ID. UUID foreign keys
require the authenticated seller and bond series to have been provisioned
already. Idempotency, rather than a caller-selected resource key, serializes
exact retries.

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
client ID, operation, and a canonical UUIDv4 nonce. The claim keeps SHA-256 request and
assertion digests, and `operation_results` stores the immutable outcome. An
exact retry can use a fresh assertion and recover the original resource; using
the same scope for another request is rejected. These tables intentionally
retain audit history and have no destructive down migration.

An `AFTER INSERT` trigger on successful operation results maps `offers.create`
to the sale-offer UUID and `purchases.buy` to the purchase UUID. It appends an
independent event UUIDv7, source type and UUID, schema version, and completion
time to `integration_events` in the mutation's transaction. Rejections and
unrelated operations append nothing. The table has no sequence, serialized
payload, or copied domain columns. Event data
is reconstructed through fixed, versioned queries against the immutable source
facts immediately before publication. The narrowly scoped trigger function runs
as its migration owner with a fixed system search path, so applying the expand
migration does not require a previously deployed exchange writer to gain a new
table privilege.

`integration_event_deliveries` is mutable operational coordination rather than
a domain-fact table. It has its own UUIDv7 key and a unique destination/event
pair. UUIDv4 lease nonces coordinate overlapping immediate and operator-triggered
attempts; delivery acknowledgement, attempt count, next-attempt time, and a
sanitized error class are the only retained delivery data. External calls occur
after the claiming transaction commits. A crash after destination acceptance
but before acknowledgement can cause a duplicate, so consumers deduplicate by
the event UUID. Runtime `UPDATE` privilege is required only on
this delivery table; integration events and all domain facts remain append-only.

The application attempts publication once after each successful mutation. It
does not scan pending events on startup or run a scheduled worker. An authorized
API operation performs an explicit recovery scan. With no configured concrete
publisher, references accumulate without delivery.

Each successful Banxico SIE request appends one bounded
`sie_exchange_rate_imports` row containing its canonical series/date scope,
sanitized JSON response, SHA-256 digest, and database fetch time. Tokens and
request headers are never stored. `sie_exchange_rate_observations` normalizes
the response into exact positive PostgreSQL numeric values and explicit
series/base/quote/date coordinates. The short importing transaction serializes
each coordinate, makes a repeated current value a no-op, and appends any
change—including a return to an older value—as a correction. The
`current_sie_exchange_rates` view returns a UUIDv7 revision ID but selects
the greatest preserved bigint revision sequence without removing earlier
values; it does not rely on transaction-start
timestamps to order concurrent imports.

Historical range coverage uses one deterministic calendar-month work unit per
series and explicit base/quote mapping. A completed closed month remains
covered even when it contained no observations. The current partial month has
a freshness deadline and is fetched only through the current date. This avoids
interpreting weekends and holidays as missing imports while keeping closed
historical data available without SIE.

`sie_exchange_rate_fetch_coordination` is mutable operational data. Short
UUIDv4 lease nonces ensure that at most one server instance actively fetches a mapped series
and period; the external call occurs after the claiming transaction releases
its connection. A crash after SIE responds but before the importing transaction
commits can result in a later duplicate request after lease expiry, but the
observation uniqueness constraint prevents duplicate normalized values.
`sie_provider_state` carries the token-wide reset deadline returned by SIE so
one rate-limit response suppresses requests for other series across all server
instances.

Imports and observations are intentional durable provenance and have no
destructive down migration. Fetch coordination and provider state require
narrow `SELECT`, `INSERT`, and `UPDATE` runtime privileges; immutable imports
and observations require only `SELECT` and `INSERT`.

The UUID migrations call PostgreSQL 18 native functions and therefore cannot
run on PostgreSQL 17. For an existing deployment, first take and verify a
restorable backup, rehearse the selected PostgreSQL major-upgrade procedure,
review extensions and collation changes, upgrade the cluster to 18, and verify
`server_version_num` before running the UUID expand migration. Supported choices include
deployment-owned `pg_upgrade`, logical replication, or dump/restore. Do not
start the UUID-aware application until the schema migration completes. Before
contraction, the compatibility graph permits the previous application to run
on PostgreSQL 18. After contraction, rollback targets the UUID-only preparation
release; a schema problem requires a corrective forward migration. Do not
downgrade the data directory or use a non-destructive down section as a rollback
mechanism.

Before removing the rolling-compatibility graph, run:

```console
devenv tasks run db:uuid-contract-readiness
```

Against production, run `bond-exchange-uuid-contract-readiness` with an
explicit `DATABASE_URL` during a quiescent lease window. The gate rejects any
text/UUID relationship drift or active event-delivery/SIE lease and reports
the historical aliases that require lossless archival. Database checks cannot
prove writer retirement: release evidence must also show that pre-UUID
binaries and direct-SQL writers are retired, their credentials are revoked,
query logs no longer use compatibility columns or views, and backup restore
has been rehearsed.

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
