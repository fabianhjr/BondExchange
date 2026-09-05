# ADR-0018: Contract the legacy identifier graph

- Status: Accepted
- Date: 2026-09-04

The transition described below is complete: the application uses the
canonical views and the `_v2` aliases have been removed.

## Context

ADR-0017 moved every application table to a UUIDv7 primary key and every
current relationship to UUID foreign keys. Its expand migration retained the
original text and numeric keys, a parallel foreign-key graph, synchronization
triggers, and versioned views so old and UUID-aware releases could overlap.
That compatibility graph adds write work and ambiguity after the rollout.

Some old values are not redundant. Caller-selected identifiers, deterministic
operation-claim IDs, non-UUID idempotency values, and SIE import sequences can
be audit or reconciliation evidence. Removing their operational columns
without preserving those values would violate the lossless-migration policy.

## Decision

Contract the graph through separately deployable prepare, archive, contract,
and final-view migrations. Each migration must work with the application
release deployed immediately before it. Pre-UUID binaries and direct writers
must be retired, their credentials revoked, the relationship graph proven free
of drift, leases quiesced, and backup restoration rehearsed before contraction.

Preserve non-derivable legacy values in the append-only
`legacy_identifier_archive`. The archive has a UUIDv7 primary key and records
the owning entity UUID, value type, and original value. It is audit evidence,
not a supported runtime alias lookup, and application writers receive no
mutation privilege. Redundant relationship columns are not copied because the
UUID graph and archived entity aliases reconstruct them.

Remove the legacy relationships, constraints, indexes, compatibility triggers,
and compatibility columns in a corrective-forward contract migration. Keep
business identifiers—bond series, role and permission codes, client IDs,
integration destination IDs, event source types, SIE work keys, and provider
IDs—as unique attributes. Retain the SIE observation sequence as revision
ordering, not identity. Promote UUID-backed views to their canonical names,
retain `_v2` aliases for one application transition, and remove those aliases
only after the canonical-view application is fully deployed.

Do not provide destructive down migrations. Before the final alias-view
removal, application rollback targets the UUID-only preparation release. A
schema defect is repaired with a corrective forward migration; backup restore
is disaster recovery rather than routine release rollback.

## Consequences

- Operational writes and joins have one identifier graph and no synchronization
  trigger overhead.
- Distinct historical values remain available under restricted, append-only
  audit storage.
- The original pre-UUID application cannot run after contraction; retiring it
  is an explicit release gate.
- Contract migrations require exclusive table locks and production-sized
  rehearsal even when most column removal is catalog-only.
- The archive remains subject to the unresolved retention, capacity, access,
  and erasure policy tracked by F-005.
- Domain behavior and TLA+ invariants do not change.

## Alternatives considered

### Delete all old values

This produces the smallest schema but loses unique audit and reconciliation
evidence and violates the repository's forward-migration policy.

### Keep the compatibility graph indefinitely

This avoids a contract event but permanently retains redundant writes,
indexes, failure paths, and ambiguity for alternate writers.

### Keep legacy columns without constraints or triggers

This reduces write work but leaves unclear, stale aliases on operational facts.
An isolated immutable archive makes their historical-only status explicit.
