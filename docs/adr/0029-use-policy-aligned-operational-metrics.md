# ADR-0029: Use policy-aligned operational metrics

- Status: Accepted
- Date: 2026-09-06

## Context

ADR-0025 established application-owned OpenTelemetry instrumentation, but the
first custom histograms relied on SDK defaults intended for millisecond-valued
measurements while recording seconds. HTTP spans carried bounded routes but the
standard HTTP metrics did not. Authentication, request admission, observation
validation, idempotency, and pre-delivery event stages also lacked the bounded
metrics needed to distinguish important failure paths without querying sampled
traces or protected security logs.

Provider fetch count mixed two populations: one duration measurement described
a provider request while the counter increment described every work unit in its
batch. Integration-event backlog and database lock state, conversely, are
shared PostgreSQL facts that cannot be represented truthfully by independently
polling stateless application replicas.

## Decision

Every application histogram owns explicit boundaries chosen for its unit and
policy range. Latency histograms use seconds with sub-second boundaries;
observation age reaches the seven-day intake ceiling; streamed-offer counts use
count-oriented boundaries. Contract tests verify the boundaries as well as
metric names, units, descriptions, aggregation types, values, and bounded
attributes.

The sanitized REST route is attached to both the server span and standard HTTP
metrics. Recovered application panics complete the application operation once
with a fixed internal-error class before transport recovery returns a generic
error.

Use separate counters for exchange-rate provider requests and the work units
batched into those requests. Record shared cooldown decisions as skipped
requests, not provider attempts. Classify provider and observation results with
fixed enumerations.

Add bounded application metrics for authentication result and latency,
authenticated request-admission latency, domain operation and retry reason,
idempotency decisions, and integration-event processing stages. Identifiers,
claims, financial values, raw paths, SQL arguments, and configured destination
names remain excluded from metric attributes.

Do not emit per-replica gauges for shared event backlog, oldest pending event,
table growth, or PostgreSQL lock state. A future production deployment must
collect those values once through a deployment-owned PostgreSQL receiver or
exporter, as required by ADR-0025.

This decision changes technical observability only. It does not change domain
behavior or invariants, so the TLA+ specification is unaffected.

## Consequences

- Latency percentiles and policy-boundary alerts can use meaningful buckets.
- HTTP failures before RPC dispatch remain attributable to a bounded route.
- Metrics distinguish authentication, admission, provider, observation,
  idempotency, retry, and event-processing failure classes without protected
  identifiers.
- Fetch request rates and batch volume have independent, internally consistent
  denominators.
- A transaction retry can repeat an idempotency claim decision; dashboards must
  compare idempotency outcomes with operation traffic rather than treating the
  metric as an append-only database-fact count.
- Application instrumentation still cannot establish production monitoring.
  Shared-state collection, collector self-monitoring, alert routing, retention,
  access control, and response ownership remain deployment work.

## Alternatives considered

### Configure all histogram boundaries in the backend

This would make the exported contract dependent on an unspecified backend and
leave local OTLP verification unable to prove useful aggregation. Instrument-
owned boundaries travel with every supported collector and backend.

### Label metrics with principal, assertion, event, or destination identifiers

Those labels would improve drill-down but create confidentiality and
cardinality risks. Protected logs and sampled traces retain the approved
correlation surfaces; metrics use fixed operational classes.

### Poll shared PostgreSQL state from every application replica

This would duplicate observations, add database load, and make gauges depend on
replica count. One deployment-owned receiver is the truthful collection
boundary for shared state.
