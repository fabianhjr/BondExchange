# Database

PostgreSQL is the durable record and the concurrency authority. Domain facts
are append-only: principals, bonds, sale offers, purchases, RBAC changes, and
operation results are inserted and never updated or deleted. A small, clearly
separated set of tables holds mutable operational coordination instead.

- [Schema history](#schema-history)
- [Identity and keys](#identity-and-keys)
- [One identity table](#one-identity-table)
- [Monetary amounts](#monetary-amounts)
- [Marketplace facts](#marketplace-facts)
- [Canonical MXN terms and provenance](#canonical-mxn-terms-and-provenance)
- [Authentication and authorization](#authentication-and-authorization)
- [Request admission](#request-admission)
- [Idempotency](#idempotency)
- [Integration events](#integration-events)
- [Banxico SIE storage](#banxico-sie-storage)
- [Privileges and append-only enforcement](#privileges-and-append-only-enforcement)
- [Migration workflow](#migration-workflow)
- [Migration and readiness gates](#migration-and-readiness-gates)
- [Disposable lifecycles and the demo seed](#disposable-lifecycles-and-the-demo-seed)

## Schema history

Migrations are the schema source of truth and are applied in strict version
order. Every migration in `migrations/` appears below; `docs:check` fails when
one is missing from this table.

| Version | Migration | Introduces |
| --- | --- | --- |
| 20260901000000 | [`append_only_exchange.sql`](migrations/20260901000000_append_only_exchange.sql) | Users, bonds, sale offers, and purchases as append-only facts. |
| 20260902000000 | [`use_decimal_prices.sql`](migrations/20260902000000_use_decimal_prices.sql) | Exact decimal monetary prices. |
| 20260903000000 | [`append_only_security.sql`](migrations/20260903000000_append_only_security.sql) | Principals, roles, grants, revocations, and operation facts. |
| 20260903120000 | [`retire_reflection_permission.sql`](migrations/20260903120000_retire_reflection_permission.sql) | Revocation of the `reflection.use` grant. |
| 20260903130000 | [`add_integration_event_references.sql`](migrations/20260903130000_add_integration_event_references.sql) | Minimal integration-event references and delivery coordination. |
| 20260904120000 | [`add_sie_exchange_rates.sql`](migrations/20260904120000_add_sie_exchange_rates.sql) | Append-only SIE imports and observations, plus mutable fetch coordination. |
| 20260904130000 | [`use_uuid_keys_and_nonces.sql`](migrations/20260904130000_use_uuid_keys_and_nonces.sql) | The PostgreSQL 18 UUIDv7 identity and UUIDv4 nonce cutover (expand). |
| 20260904140000 | [`prepare_uuid_only_writers.sql`](migrations/20260904140000_prepare_uuid_only_writers.sql) | UUID-only writer compatibility. |
| 20260904150000 | [`archive_legacy_identifiers.sql`](migrations/20260904150000_archive_legacy_identifiers.sql) | Lossless archival of non-derivable pre-UUID history. |
| 20260904160000 | [`contract_legacy_identifier_graph.sql`](migrations/20260904160000_contract_legacy_identifier_graph.sql) | Removal of the live legacy identifier graph (contract). |
| 20260904170000 | [`finalize_uuid_contract.sql`](migrations/20260904170000_finalize_uuid_contract.sql) | Canonical view names and business-code column names. |
| 20260904180000 | [`add_canonical_mxn_offer_terms.sql`](migrations/20260904180000_add_canonical_mxn_offer_terms.sql) | Canonical MXN offer terms, conversion quotes, and submission provenance. |
| 20260905000000 | [`constrain_currency_codes.sql`](migrations/20260905000000_constrain_currency_codes.sql) | The canonical currency-code constraint, added `NOT VALID`. |
| 20260905010000 | [`validate_currency_codes.sql`](migrations/20260905010000_validate_currency_codes.sql) | Validation of that constraint against retained history. |
| 20260905020000 | [`add_principal_rate_limits.sql`](migrations/20260905020000_add_principal_rate_limits.sql) | Shared authenticated request admission. |
| 20260906000000 | [`prevent_self_trading.sql`](migrations/20260906000000_prevent_self_trading.sql) | Same-identity self-trade prevention. |
| 20260906010000 | [`expand_principal_identity.sql`](migrations/20260906010000_expand_principal_identity.sql) | Principal-referencing marketplace keys, added `NOT VALID` (expand). |
| 20260906020000 | [`validate_principal_identity.sql`](migrations/20260906020000_validate_principal_identity.sql) | Validation of those keys against retained history. |
| 20260906030000 | [`contract_user_identity.sql`](migrations/20260906030000_contract_user_identity.sql) | Removal of the separate user identity table (contract). |
| 20260906120000 | [`retire_legacy_identifier_archive.sql`](migrations/20260906120000_retire_legacy_identifier_archive.sql) | Accepted retirement of expired pre-UUID identifier evidence. |
| 20260907000000 | [`retire_buyer_not_found_code.sql`](migrations/20260907000000_retire_buyer_not_found_code.sql) | Retirement of the `buyer_not_found` rejection code. |

## Identity and keys

PostgreSQL 18 is required. Every application table has a `uuid_id uuid` primary
key generated with `uuidv7()` and constrained with
`uuid_extract_version(uuid_id) = 7`. Natural identifiers remain unique lookup
attributes. UUIDv4 is reserved for request and lease nonces.

The original rolling migration retained legacy keys, foreign keys, views, and
synchronization triggers. The contract migration removes that live graph after
verifying archive coverage and a quiescent lease window. The current Go adapter
uses only UUID relationships and the canonical unversioned views; the
transitional `_v2` aliases have been removed.

The migration history copied non-derivable values into an append-only archive
before contraction. ADR-0033 records that the accepted retention period later
elapsed, and the current schema removes that archive with recovery available
only from a verified pre-retirement backup.

## One identity table

`bond_exchange.principals` is the only identity. It generates its own UUIDv7,
and `sale_offers.seller_uuid` and `purchases.buyer_uuid` reference it directly,
so every domain fact is attributed to an identity that can authenticate.

The schema previously had a second table, `bond_exchange.users`. After the UUID
contraction it held one column — its own `uuid_id` — and `principals.uuid_id`
was both that table's primary key and a foreign key to it, so the relationship
was one-to-one and a principal could not exist without a user row. The only
state it could represent was an allocated identity that could never
authenticate, sell, or buy.
[ADR-0034](../docs/adr/0034-make-the-principal-the-sole-identity.md) removed it
in three migrations — an expand that added the principal-referencing keys
`NOT VALID` and gave `principals.uuid_id` its own default, a validation that
proved the retained history conformed, and a contract that dropped the
user-referencing keys and the table — and the transition has since completed.
Migration `20260907000000` retired `buyer_not_found`, the last rejection code
that only the two-table split could produce.

Expressing beneficial ownership — one legal person behind several principals —
is a separate concern that this shape never supported, because a primary key
that is also the foreign key cannot be many-to-one. It remains tracked by
[F-002](../FRICTIONS.md#f-002--market-integrity-rules-are-undecided-p1) and
would be added as a nullable owner column on `principals`.

## Monetary amounts

The `bond_exchange.monetary_amount` domain is based on PostgreSQL
`numeric(14,4)`: ten integer digits and four fractional digits, with a maximum
positive value of `9999999999.9999`. Its constraint excludes NaN and infinite
values, while the sale-offer column constraint also rejects zero and negative
prices. The Go adapter converts the database's exact decimal text to
`decimal.Decimal`; it never passes monetary values through binary floating
point.

PostgreSQL rounds direct inputs having more than four fractional digits, so
provisioning code must validate the scale before insertion when rounding is not
intended. That rounding is the one place where storage remains more permissive
than the Go domain: it happens when the value is cast into `numeric(14,4)`,
before any `CHECK` constraint observes it, so no column constraint can reject an
over-precise input. Closing it would require changing the monetary domain's base
type.

Every other sale-offer value class now agrees, and
`TestStorageConstraintsMatchDomainValidation` compares the two directly:
`sale_offers.currency_code` must match `^[A-Z]{3}$`, matching
`exchange.ParseCurrencyCode`. The test fails if the documented rounding
divergence is ever closed without updating this README and the register.

The currency constraint is added `NOT VALID` and validated by a separate
migration. Adding it constrains every subsequent insert immediately without
scanning history, so it cannot fail on existing rows and the previously deployed
application keeps working. The validation migration then proves the retained
history conforms; because sale offers are append-only it cannot repair a
nonconforming row, so it reports the count and stops, leaving the operator to
represent the correction as new facts under review.

## Marketplace facts

A purchase records the buyer's binding order or reservation and has its own
UUIDv7 primary key. Settlement, payment, custody, ownership transfer, expiry,
and cancellation are not represented and remain pending.

The unique `sale_offer_uuid` constraint gives concurrent inserts for the same
offer one winner even when they come from different server instances. The losing
requests are reported as an unavailable offer, while the original offer row
remains as history. That constraint, the self-trade trigger below, and the
append-only triggers are cited as evidence by the
[guarantee register](../docs/guarantees.md), which states the promises they
back and what those promises exclude.

The `purchases_reject_self_trade` trigger compares each new purchase's buyer to
the referenced offer's seller and raises the named
`purchases_buyer_not_seller` check violation when they match. The activating
migration verifies the retained history first; it does not rewrite append-only
facts. The Go adapter classifies the same rule before insertion and records
`self_trade_prohibited` as the durable idempotent result.

The current adapter joins `sale_offer_canonical_terms` directly and excludes
every offer that has a matching purchase or belongs to the authenticated
principal. The compatibility `bond_exchange.active_offers` view retains its
prior shape for an expand-first rolling deployment and remains a global,
unparameterized projection. The API requires a bond series and returns all
canonical offers tradable by that principal in ID order; the
`sale_offers_bond_uuid_uuid_id_idx` index supports the relationship join. A
separate distinct query derives the sorted list of bond series having at least
one offer tradable by that principal.

The unique offer constraint supports the anti-join and enforces single-sale
concurrency. Purchase history by buyer is supported by
`purchases_buyer_uuid_sale_offer_uuid_idx`. Add further indexes only for
observed query plans.

The API creates sale offers, canonical terms, and submission provenance in one
statement with `INSERT ... RETURNING`; PostgreSQL generates the UUIDv7 resource
identity. Clients do not supply an ID. UUID foreign keys require the
authenticated seller's principal and the bond series to have been provisioned
already.
Idempotency, rather than a caller-selected resource key, serializes exact
retries.

## Canonical MXN terms and provenance

`sale_offer_canonical_terms` is the one-to-one MXN authority read by the new
application. `sale_offer_submissions` preserves the original MXN or USD amount;
a USD row references exactly one `sale_offer_conversion_quotes` fact. That quote
pins a `SF43718` observation, accepted MXN amount, half-to-even rounding policy,
principal, bond, and expiry. All three tables are append-only. A quote is
single-use by a unique provenance reference, and create verifies its principal,
bond, exact USD amount, expiry, and non-use in the serializable transaction.

The expand migration backfills canonical identity terms only for existing MXN
offers. It preserves non-MXN facts without inventing seller consent or a
historical rate. The new adapter therefore hides and refuses to buy those rows;
the compatibility view remains available only so the previously deployed binary
continues to function during rollout. Retiring that binary is required before
claiming the MXN-only control is active. A future authorized workflow must
disposition legacy non-MXN rows without mutation.

## Authentication and authorization

A federated identity is resolved to a principal by the unique
`(issuer, subject)` pair in `principals`; the API never accepts the principal's
UUID as operation input. `client_class` distinguishes human and automated
principals.

Roles and permissions have append-only grant and revocation tables, and
`effective_principal_permissions` derives current access while excluding revoked
grants and suspended principals. A reinstatement references exactly one
suspension rather than modifying it.

Runtime gRPC reflection was removed after the initial security facts were
recorded. The original `reflection.use` permission and operator grant remain as
immutable history; the later migration appends their revocation, so the initial
operator grant is no longer effective. Operators use the versioned API
descriptor set instead.

## Request admission

`principal_rate_limits` is mutable operational coordination rather than a
domain, authorization, operation, or audit fact. It holds one UUIDv7-keyed row
per principal and a unique principal reference.

An atomic upsert uses the database clock to reset a stale UTC-minute window or
increment its count only through 100, so concurrent server instances share one
allowance. Rejections do not increment the row. This fixed-window control can
admit requests on both sides of a minute boundary and does not bound concurrent
or unauthenticated traffic.

## Idempotency

Mutation idempotency uses `operation_claims`, uniquely scoped by principal,
client ID, operation, and a canonical UUIDv4 nonce. The claim keeps SHA-256
request and assertion digests, and `operation_results` stores the immutable
outcome.

An exact retry can use a fresh assertion and recover the original resource;
using the same scope for another request is rejected. These tables intentionally
retain audit history and have no destructive down migration.

## Integration events

An `AFTER INSERT` trigger on successful operation results maps `offers.create`
to the sale-offer UUID and `purchases.buy` to the purchase UUID. It appends an
independent event UUIDv7, source type and UUID, schema version, and completion
time to `integration_events` in the mutation's transaction. Rejections and
unrelated operations append nothing.

The table has no sequence, serialized payload, or copied domain columns. Event
data is reconstructed through fixed, versioned queries against the immutable
source facts immediately before publication. The narrowly scoped trigger
function runs as its migration owner with a fixed system search path, so
applying the expand migration does not require a previously deployed exchange
writer to gain a new table privilege.

`integration_event_deliveries` is mutable operational coordination rather than a
domain-fact table. It has its own UUIDv7 key and a unique destination/event
pair. UUIDv4 lease nonces coordinate overlapping immediate and
operator-triggered attempts; delivery acknowledgement, attempt count,
next-attempt time, and a sanitized error class are the only retained delivery
data.

External calls occur after the claiming transaction commits. A crash after
destination acceptance but before acknowledgement can cause a duplicate, so
consumers deduplicate by the event UUID. Runtime `UPDATE` privilege is required
only on this delivery table; integration events and all domain facts remain
append-only.

The application attempts publication once after each successful mutation. It
does not scan pending events on startup or run a scheduled worker. An authorized
API operation performs an explicit recovery scan. With no configured concrete
publisher, references accumulate without delivery.

## Banxico SIE storage

Each successful Banxico SIE request appends one bounded
`sie_exchange_rate_imports` row containing its canonical series/date scope,
sanitized JSON response, SHA-256 digest, and database fetch time. Tokens and
request headers are never stored.

`sie_exchange_rate_observations` normalizes the response into exact positive
PostgreSQL numeric values and explicit series/base/quote/date coordinates. The
short importing transaction serializes each coordinate, makes a repeated current
value a no-op, and appends any change—including a return to an older value—as a
correction. The `current_sie_exchange_rates` view returns a UUIDv7 revision ID
but selects the greatest preserved bigint revision sequence without removing
earlier values; it does not rely on transaction-start timestamps to order
concurrent imports.

Historical range coverage uses one deterministic calendar-month work unit per
series and explicit base/quote mapping. A completed closed month remains covered
even when it contained no observations. The current partial month has a
freshness deadline and is fetched only through the current date. This avoids
interpreting weekends and holidays as missing imports while keeping closed
historical data available without SIE.

`sie_exchange_rate_fetch_coordination` is mutable operational data. Short UUIDv4
lease nonces ensure that at most one server instance actively fetches a mapped
series and period; the external call occurs after the claiming transaction
releases its connection. A crash after SIE responds but before the importing
transaction commits can result in a later duplicate request after lease expiry,
but the observation uniqueness constraint prevents duplicate normalized values.
`sie_provider_state` carries the token-wide reset deadline returned by SIE so
one rate-limit response suppresses requests for other series across all server
instances.

Imports and observations are intentional durable provenance and have no
destructive down migration. Fetch coordination and provider state require narrow
`SELECT`, `INSERT`, and `UPDATE` runtime privileges; immutable imports and
observations require only `SELECT` and `INSERT`.

See [`docs/exchange-rates.md`](../docs/exchange-rates.md) for the fetch workflow
that writes these tables.

## Privileges and append-only enforcement

Statement-level triggers reject `UPDATE`, `DELETE`, and `TRUNCATE` on domain
fact tables as defense in depth. Production runtime roles should not own the
schema. Exchange writers need only the `SELECT` and `INSERT` privileges their
queries require; a configured event publisher additionally needs narrowly scoped
`UPDATE` on `integration_event_deliveries`. Migrations and exceptional recovery
use a separately controlled owner.

## Migration workflow

Dbmate records applied versions in `schema_migrations` and applies pending
migrations transactionally in strict version order. Migrations are the schema
source of truth, so automatic schema dumps are disabled.

Create later migrations with `dbmate new <description>`. Do not edit an
already-applied migration; add a new timestamp-versioned migration instead.

Forward migrations must be lossless and compatible with the previously deployed
application. Use separate expand, backfill, and contract migrations: introduce
compatible structures first, preserve source data during backfill, and contract
only redundant compatibility structures after old application versions can no
longer run. Preserve all unique data and prefer a corrective forward migration
when a lossless rollback is not possible. An architecture decision may authorize
deletion only after an explicit retention and recovery decision; ADR-0033 is the
current narrow exception and requires a verified pre-migration backup.

Validate the full migration history in isolation with:

```console
devenv tasks run db:migrate
```

## Migration and readiness gates

The UUID migrations call PostgreSQL 18 native functions and therefore cannot run
on PostgreSQL 17. For an existing deployment, first take and verify a restorable
backup, rehearse the selected PostgreSQL major-upgrade procedure, review
extensions and collation changes, upgrade the cluster to 18, and verify
`server_version_num` before running the UUID expand migration. Supported choices
include deployment-owned `pg_upgrade`, logical replication, or dump/restore.

Do not start the UUID-aware application until the schema migration completes.
Before contraction, the compatibility graph permits the previous application to
run on PostgreSQL 18. After contraction, rollback targets the UUID-only
preparation release; a schema problem requires a corrective forward migration.
Do not downgrade the data directory or use a non-destructive down section as a
rollback mechanism.

The UUID compatibility graph and its one-time readiness and archival gates are
retired. ADR-0033 requires a verified backup before applying the archive-
retirement migration because that evidence cannot be reconstructed by a down
migration.

Before activating the MXN-only release against an existing database, run:

```console
DATABASE_URL=... devenv tasks run db:canonical-mxn-readiness
```

That gate rejects inconsistent terms/provenance/rate mappings, purchases without
canonical terms, and any still-active legacy offer without seller-accepted MXN
terms. It is part of `dev:ci`, so its behavior is exercised locally and in
continuous integration against a disposable migrated database.

Database checks cannot prove reader retirement. Release evidence must also show
that sanctioned canonical-MXN compatibility-view readers are drained.

## Disposable lifecycles and the demo seed

Database-dependent devenv tasks do not share a long-lived PostgreSQL process.
The Nix-packaged `bond-exchange-with-postgres` harness creates a fresh cluster
and private Unix socket for each task, applies pending migrations, exports the
test connection variables, runs the task, and stops and removes the cluster.

The lifecycle check covers successful, failing, repeated, and parallel harness
invocations:

```console
devenv tasks run postgres:lifecycle-check
```

`devenv up` uses the same disposable lifecycle for the local demo. After
migration it loads `demo/seed.sql`, which is deliberately separate from the
production schema history. Demo facts are discarded when the process stops; the
seed is never applied to an external database. To migrate a persistent or
production-like database, set `DATABASE_URL` explicitly and run `dbmate up`.

The seed provisions five principals, three of which exist so an authorization
outcome is reachable from the running server rather than only from the Go
integration tests:

| Principal | Roles | Reaches |
| --- | --- | --- |
| `demo-seller` | `trader` | Quoting, creating, and the self-trade rejection. |
| `demo-buyer` | `trader`, `operator` | Buying, listing, health, and event recovery. |
| `demo-rate-limited` | `trader` | Per-principal admission, by preloading its window. |
| `demo-unauthorized` | none | `PermissionDenied`: it resolves and holds no permission. |
| `demo-suspended` | `trader`, then suspended | `Unauthenticated`: an appended suspension removes every permission through `effective_principal_permissions`, and a suspended principal does not resolve at all. |

`demo-suspended` keeps its role grant on purpose. Suspension and revocation are
separate append-only facts, and the seed exercises the suspension branch of the
permission view rather than simulating it with a revocation.
