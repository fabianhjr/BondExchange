# ADR-0025: Own application OpenTelemetry instrumentation

- Status: Accepted
- Date: 2026-09-05

## Context

The service previously depended on a deployment-supplied OpenTelemetry Go
automatic-instrumentation agent. Application code only copied an existing span
identifier into security logs. The repository did not pin or run that agent,
configure a collector, export metrics, flush telemetry during shutdown, or
verify telemetry end to end. Automatic library probes also could not describe
the in-process REST gateway, custom JSON-sequence stream, native pgx usage, rate
cache, idempotency, or integration-event workflow with domain-safe semantics.

Observability is also a security boundary. Request bodies, authorization
headers, Banxico credentials, financial values, audit identifiers, and dynamic
resource IDs must not become general trace attributes or metric labels. A
deployment still has to decide where protected telemetry is sent, sampled,
stored, and accessed.

## Decision

The application owns OpenTelemetry API instrumentation and SDK lifecycle. The
composition root initializes OTLP/HTTP trace and metric exporters only when
standard `OTEL_*` configuration enables them, installs W3C Trace Context and
Baggage propagation, and flushes providers during bounded shutdown. Export
failures are diagnostic failures and do not fail business requests.

Use standard instrumentation at technical boundaries: `otelhttp` for inbound
REST and the outbound Banxico client, `otelgrpc` for the native gRPC server, and
`otelpgx` for pgx queries and pool statistics. The Banxico transport creates a
child span but uses an empty propagator because that public provider is outside
the service trace trust domain. Application helpers add bounded operation,
authentication, admission, stream, rate-cache/provider/observation,
event-delivery/stage, idempotency, and transaction-retry signals.
[ADR-0029](0029-use-policy-aligned-operational-metrics.md) defines their
operational taxonomy and histogram policy.
The pure exchange domain remains independent of OpenTelemetry.

Continue writing structured JSON logs to stdout. A handler adds trace ID, span
ID, and sampled state to every context-aware record. Security logs retain their
existing protected audit fields, but those fields are not copied to traces or
metrics.

The OpenTelemetry Collector owns routing, authentication, transformation,
tail sampling, backend selection, retention, and access policy. The repository
pins a loopback-only collector for the disposable demo and contract validation;
it does not define a production backend. Do not attach automatic HTTP or gRPC
instrumentation to a natively instrumented process unless a deployment test
proves that duplicate spans are filtered.

## Consequences

- REST, gRPC, PostgreSQL, SIE, runtime, and application workflow telemetry is
  deterministic and can be tested without privileged eBPF attachment.
- A Go or dependency upgrade is checked against compiled instrumentation and
  telemetry contract tests instead of an unpinned runtime agent.
- OTLP destinations remain environment-driven; no collector address or
  credential is compiled into the binary.
- Metrics and traces can be dropped during collector failure while core
  operations remain available. Collector self-monitoring and deployment alerts
  are therefore required before production readiness.
- Native instrumentation adds dependencies and runtime overhead. Load tests
  must establish an accepted budget before a production rollout.
- Shared PostgreSQL facts such as oldest pending event and retained-table growth
  require one deployment-owned database receiver or exporter; stateless
  application replicas do not poll them independently just to emit metrics.

## Alternatives considered

### Keep automatic instrumentation as the only mechanism

This avoids SDK code in the application, but it leaves domain workflows and
native pgx state opaque, depends on deployment privileges and compatible probe
versions, and cannot be verified by the normal Go test suite.

### Expose a Prometheus endpoint

This is familiar for metrics, but it creates another listener and authorization
boundary. OTLP push reuses one collector contract for traces and metrics and
keeps the existing public API unchanged.

### Export OpenTelemetry logs directly from the application

The existing JSON security log is already an explicit audit interface. Keeping
stdout collection separate avoids migrating that interface onto the evolving
Go log SDK while still providing trace correlation.
