# ADR-0011: Use minimal transactional references for integration events

- Status: Accepted
- Date: 2026-09-03

ADR-0017 amends the identity details below: events and purchases now have
independent UUIDv7 identities, source references use UUIDs, delivery uniqueness
is `(destination_id, event_uuid)`, and consumers deduplicate by event UUID. The
minimal payload, transactional recording, and explicit-recovery decisions
remain accepted.

## Context

Successful offer creation and buying may need to notify independently owned
systems. Possible destinations include webhooks, event systems, and SIEM or
SOAR integrations, but no destination has been selected. Calling an external
system inside the exchange transaction would couple database correctness to
network availability. Calling it only after commit would leave no recoverable
record if the process stopped first.

The source sale-offer and purchase rows are already immutable and uniquely
identified. Copying their complete data into an outbox would add another
long-lived representation without improving recovery.

## Decision

When a successful `operation_results` fact is inserted, a PostgreSQL trigger
atomically appends an integration-event reference. Its durable identity is an
independent UUIDv7. The source is the pair `(table_name, source_uuid)`:
`sale_offers` with the offer UUID for creation and `purchases` with the purchase
UUID for buying. The reference stores only those identifiers, a schema version,
and the operation completion time. It has no sequence, payload, authentication context, or copied domain
columns. The narrowly scoped trigger function uses its migration-owner rights
and a fixed system search path, allowing an older application writer to keep
working after the expand migration without receiving a new table privilege.

Load a versioned event from its immutable source fact immediately before a
delivery attempt. The table name is a checked logical identifier and selects
a fixed query; it is never interpolated into SQL. Version-specific loaders
remain available for as long as their references can be retried or replayed.
The first schemas expose the same minimized offer data as the API and do not
include buyer, seller, assertion, issuer-subject, request, or credential data.

After a mutation commits, the application makes one immediate, bounded
delivery attempt to every configured destination. Publisher errors and panics
leave the reference pending and do not change the already committed API
outcome. Each destination records mutable lease and delivery coordination
against `(destination_id, event_uuid)` and uses a UUIDv4 lease nonce. Delivery is at least once: a
destination may accept an event immediately before the process stops, leaving
the database unable to record the acknowledgement. Consumers must therefore
deduplicate using the event UUID.

Do not run a startup sweep, timer, scheduled retry, or background polling
worker. An authenticated `PublishPendingEvents` operation lets an authorized
operator explicitly attempt every visible pending event, either for one
destination or for all configured destinations. It returns aggregate counts,
continues past individual publisher failures, and leaves unvisited or failed
events pending. Per-event leases coordinate overlapping immediate and manual
attempts. The operation requires a UUIDv4 idempotency nonce bound into its assertion;
delivery records, rather than an operation result, prevent acknowledged events
from being intentionally sent again.

Define a transport-neutral publisher interface and test it only with fakes.
Do not add a webhook, broker, SIEM, SOAR, no-op, or other concrete publisher in
this decision. An empty registry is valid; in that configuration mutations
still record event references and the manual operation reports that no
publisher is configured.

## Consequences

- Event references commit atomically with successful mutations and exact
  idempotent operation retries do not append another reference.
- Old application instances also record references after the migration because
  production is driven by the operation-result trigger.
- Storage does not duplicate source facts, but source rows and their
  version-specific loaders are part of the event-retention contract.
- The polymorphic source relationship cannot use one PostgreSQL foreign key;
  the closed table-name constraint, trigger, fixed loaders, and tests enforce
  it.
- One integration event exists per append-only source fact, enforced by the
  unique `(table_name, source_uuid)` lookup key.
- There is no global ordering guarantee. Recovery scans by the composite key
  and a transaction that commits after the scan remains pending for a later
  invocation.
- Publication normally adds destination latency after commit. The framework
  bounds each call, but a concrete adapter must define its own safe timeout,
  retry, authentication, and duplicate-handling behavior.
- Pending events can remain indefinitely when a process stops or a publisher
  fails and no operator invokes recovery.

## Alternatives considered

### Persist a complete serialized event

This makes publication independent of source schemas but duplicates immutable
domain data and expands retention and disclosure obligations. It was rejected
because the source facts are already durable and cannot change.

### Use the source reference as the event identifier

This was the original choice. ADR-0017 later selected a UUIDv7 event identity
so consumers have one transport-neutral deduplication value while retaining a
unique source reference. A separate numeric sequence remains rejected because
checkpoint processing can skip transactions committing out of allocation
order.

### Publish synchronously inside the database transaction

This would make destination availability part of exchange correctness and
hold database resources across network calls. It was rejected.

### Automatically drain the outbox

A worker or startup sweep would improve eventual delivery but introduce an
always-running process and retry policy before any destination is selected.
Explicit recovery was selected for the current scope.
