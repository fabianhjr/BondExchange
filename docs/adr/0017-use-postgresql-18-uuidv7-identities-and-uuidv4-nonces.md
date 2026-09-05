# ADR-0017: Use PostgreSQL 18, UUIDv7 identities, and UUIDv4 nonces

- Status: Accepted; compatibility period completed by ADR-0018
- Date: 2026-09-04

## Context

The original schema used caller-selected text identifiers, composite natural
keys, and a bigint sequence as primary keys. Idempotency keys, assertion IDs,
and coordination lease tokens were unconstrained text. These choices mixed
resource identity with request uniqueness, made alternate writers able to
persist identifiers the application could not parse, and gave high-volume
B-tree indexes no common locality or representation.

The service needs durable identifiers that are globally unique, efficient for
PostgreSQL indexes, and independent of business names. It also needs random,
single-use values for replay and lease boundaries. PostgreSQL 18 provides
native `uuidv7()`, `uuidv4()`, and `uuid_extract_version()` functions, allowing
generation and version enforcement without an extension.

## Decision

Run the repository and its verification harnesses on PostgreSQL 18. A deployed
database must be upgraded to PostgreSQL 18 before applying the UUID migration.
The application never performs the major-version upgrade or schema migration
at startup.

Give every `bond_exchange` table one `uuid` primary key named `uuid_id` and
constrain it to UUID version 7. PostgreSQL generates new persistent identities
with `uuidv7()`. Relationships used by the current application reference these
UUID keys. Business identifiers such as bond series, permission codes, role
codes, work keys, provider keys, and destination IDs remain unique attributes,
not primary keys. The SIE observation bigint remains a revision-order value,
not an identity. A purchase has its own UUIDv7 identity while a separate unique
constraint on its sale-offer UUID preserves one winner per offer.

PostgreSQL generates sale-offer IDs. `CreateSaleOffer` no longer accepts a
caller-selected ID, and the API reserves the removed Proto field number and
name. User and offer IDs crossing the current API boundary must be canonical
lowercase UUIDv7 strings.

Use canonical UUIDv4 values for nonces: transport idempotency keys, JWT `jti`
assertion IDs, integration-event delivery leases, and SIE fetch leases. The Go
application generates these values with a cryptographically secure UUIDv4
generator. PostgreSQL stores them as `uuid` and verifies their version.
Persistent event records and their immutable source facts have independent
UUIDv7 identities; consumers deduplicate by the event UUID.

Perform the schema transition as an expand/backfill/cutover migration. Preserve
legacy keys as unique aliases, preserve their foreign-key graph, expose
versioned UUID views, and use compatibility triggers so the previously
deployed writer can still insert rows after migration. Historical non-UUID
idempotency and lease values remain readable with a null UUID counterpart;
the current application only creates and queries UUIDv4 values. Remove the
legacy graph only in a later corrective-forward contract migration after old
application versions and sanctioned direct writers have been retired and the
backfill has been verified.

## Consequences

- Persistent keys have one database-native type and insertion-local UUIDv7
  ordering without sacrificing cross-system uniqueness.
- Random UUIDv4 nonces are visibly distinct from durable identities and cannot
  accidentally encode creation time.
- PostgreSQL 18 is now a hard migration and runtime prerequisite. Production
  upgrades require deployment-owned backup, restore rehearsal, extension and
  collation review, and a supported `pg_upgrade`, logical-replication, or
  dump/restore procedure.
- Sale-offer creation is a coordinated API compatibility break. Clients must
  stop sending `id`, retain the returned UUIDv7, and generate a fresh UUIDv4
  for each new mutation while retaining it for exact retries.
- The compatibility period temporarily stores two relationship graphs and
  relies on synchronization triggers. It consumes additional storage and
  requires explicit drift checks before the later contract step.
- UUIDv7 exposes approximate creation time. These IDs are identifiers, not
  secrets or authorization evidence, and response minimization continues to
  protect buyer and seller identities.
- The TLA+ domain behavior is unchanged: identity representation, SQL keys,
  nonce syntax, and PostgreSQL deployment are outside the model. The unique
  purchase-per-offer invariant remains the database implementation of the
  modeled exclusive buy.

## Alternatives considered

### Generate UUIDv7 only in Go

Application generation would work for ordinary writes but would make every
sanctioned database writer reproduce the same policy. Database defaults and
constraints keep the invariant at the shared persistence boundary.

### Use UUIDv4 for every value

UUIDv4 is appropriate for unpredictable nonces, but random persistent keys
have poorer B-tree insertion locality and do not communicate the distinction
between an identity and a replay token.

### Keep natural or caller-selected primary keys

Natural attributes remain useful unique lookup keys, but changing business
meaning and inconsistent textual validation should not define relationship
identity. Caller-selected resource IDs also conflate create semantics with
idempotency, which is already represented by operation claims.

### Install a UUID extension on PostgreSQL 17

An extension or custom function could generate UUIDv7, but it would add a
deployment dependency and duplicate functionality available natively in
PostgreSQL 18. The repository instead verifies the selected major version.
