# ADR-0033: Retire legacy identifier evidence and transition tooling

- Status: Accepted
- Date: 2026-09-06

## Context

ADR-0017 replaced caller-selected text and numeric primary keys with UUIDv7
identities and replaced unconstrained idempotency and lease values with UUIDv4
nonces. ADR-0018 then contracted the compatibility graph while preserving
non-derivable pre-UUID values in `legacy_identifier_archive` for a bounded
audit and reconciliation period.

That retention period has elapsed. The affected owners have accepted permanent
loss of the archived caller-selected identifiers, deterministic claim IDs,
non-UUID idempotency values, and SIE import sequences. The archive is not read
by the application and is not a supported alias lookup. All deployed writers
and readers that depended on the pre-UUID graph have been retired.

The one-time readiness executable and historical archival fixture now verify a
transition that no supported deployment may enter. The repository task wraps
the readiness executable in a harness that first applies the complete schema,
so it exits immediately after observing that contraction already occurred.

## Decision

Delete `legacy_identifier_archive` with a new corrective forward dbmate
migration. The deletion is an explicit, narrowly scoped exception to the
repository's lossless-forward-migration rule because the evidence has reached
the end of its accepted retention period. Require a verified pre-migration
backup as the only recovery path; the migration has no destructive or
fabricated down path.

Remove the UUID contract readiness executable, the historical archival test,
their Nix packages and devenv tasks, and documentation that presents either as
a current operator or contributor workflow. Continue running the complete
migration history against a fresh PostgreSQL 18 database and retain schema
tests that reject the legacy columns, synchronization machinery, transitional
views, and archive table.

Applied migrations remain immutable. Proto field reservations remain part of
the API compatibility contract. Rename application-local variables that still
use lease-token terminology to lease-nonce terminology and use canonical UUID
fixtures in adapter and eventing tests so current code and tests describe the
retained contract.

## Consequences

- Pre-UUID identifier evidence is permanently unavailable after the migration;
  recovery requires restoring the verified pre-migration backup.
- Fresh databases briefly create and populate the archive while replaying
  immutable history, then drop it in the retirement migration.
- The live schema, development environment, and task graph no longer expose
  completed UUID-transition artifacts.
- Removing the historical fixture means CI no longer proves that the retired
  archive once captured representative pre-UUID values. It continues to prove
  that the entire migration chain applies and that the final schema contains
  only the UUID identity graph.
- The broader lifecycle gap for retained domain, authorization, operation,
  integration-event, and exchange-rate history remains unresolved in F-005.
- Domain behavior and TLA+ invariants do not change.

## Alternatives considered

### Keep the archive indefinitely

This would preserve historical reconciliation evidence but retain data beyond
its accepted retention period and keep a table that no supported workflow
uses.

### Export the archive before dropping it

Moving the evidence would retain the same lifecycle and access obligations in
another system. The responsible owners explicitly accepted deletion instead.

### Rewrite the archival and contract migrations

This would make fresh installations smaller but would invalidate applied
dbmate history. Applied migrations remain immutable, so retirement is a new
forward migration.
