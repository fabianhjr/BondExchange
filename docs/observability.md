# Observability

The server owns OpenTelemetry tracing and metrics instrumentation and exports
both signals over OTLP/HTTP when standard `OTEL_*` configuration enables them.
JSON logs remain on stdout and include `trace_id`, `span_id`, and
`trace_sampled` whenever their context contains a valid span. There is no
public `/metrics` endpoint.

`devenv up` starts a loopback OpenTelemetry Collector alongside the disposable
demo. The collector accepts OTLP/HTTP on `127.0.0.1:4318` and writes a basic
debug representation to its process log. This configuration is development
evidence, not a production telemetry backend.

For an externally started server, a minimal configuration is:

```console
OTEL_SERVICE_NAME=bond-exchange \
OTEL_TRACES_EXPORTER=otlp \
OTEL_METRICS_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_ENDPOINT=https://collector.example \
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
OTEL_TRACES_SAMPLER=parentbased_traceidratio \
OTEL_TRACES_SAMPLER_ARG=0.1 \
OTEL_METRIC_EXPORT_INTERVAL=60000 \
  devenv shell go -C application run ./cmd/server
```

`OTEL_TRACES_EXPORTER` and `OTEL_METRICS_EXPORTER` accept `otlp` or `none`.
With neither an OTLP endpoint nor an explicit `otlp` exporter, the application
uses no-op providers. `OTEL_SDK_DISABLED=true` disables both signals. Exporter
TLS and authentication use the standard OTLP environment variables. The
application supports `http/protobuf` and rejects an explicitly configured
different protocol or a non-HTTP(S) endpoint at startup. Supported samplers are
`always_on`, `always_off`, `traceidratio`, and their `parentbased_*` variants.
Export interval and timeout values are positive integer milliseconds.

## Signal contract

Standard HTTP, RPC, database, and Go runtime instruments retain their current
OpenTelemetry semantic-convention names. The application owns these additional
metrics:

| Metric | Bounded attributes | Meaning |
| --- | --- | --- |
| `bondexchange.operation.count` / `.duration` | operation, outcome, safe error type | Completed authenticated API operations, including recovered panics. |
| `bondexchange.stream.offer.count` | operation, outcome, safe error type | Offers emitted by one completed or failed active-offer stream. |
| `bondexchange.authentication.count` / `.duration` | operation, outcome, stage | Federated assertion results and latency. Stages are metadata, assertion, principal resolution, and complete. |
| `bondexchange.idempotency.result` | operation, outcome | Mutation claim, replay, conflict, and idempotency-storage-error decisions. Transaction retries can repeat a claim decision. |
| `bondexchange.request.rate_limit.count` / `.duration` | operation, outcome | Authenticated admission decisions and coordination latency: allowed, rejected, or error. |
| `bondexchange.market_integrity.rejection.count` | control | Market-integrity rejections; currently the bounded `self_trade` control. |
| `bondexchange.database.transaction.retry` | operation, reason | Retryable serialization failures and deadlocks. |
| `bondexchange.rate.cache.result` | outcome | Cache hits, misses, and lease contention. |
| `bondexchange.rate.fetch.attempt.count` / `.duration` | fetch kind, outcome | Actual provider request count and latency. |
| `bondexchange.rate.fetch.work_unit.count` | fetch kind, outcome | Series/month work units included in actual provider requests. |
| `bondexchange.rate.fetch.skip.count` | fetch kind, outcome | Provider requests prevented before transport, currently by a shared cooldown. |
| `bondexchange.rate.observation.age` | none | Age of a FIX observation accepted by offer intake. |
| `bondexchange.rate.observation.validation.count` | outcome | Accepted, stale, future, over-age, or structurally invalid observations. |
| `bondexchange.event.publisher.configured` | none | Number of publishers composed into one process. |
| `bondexchange.event.delivery.count` / `.duration` | outcome, safe error type | Claimed delivery attempts. |
| `bondexchange.event.stage.count` | stage, outcome | Scan, pending-count, claim, load, publish, and delivery-state transition results, including pre-claim errors and skipped claims. |

Trace topology is transport server span → application operation →
authentication, PostgreSQL request admission, rate-provider, intake, and event-delivery children
as applicable. REST route names use a fixed allowlist in both spans and HTTP
metrics; unmatched paths are reported as `unmatched`. Native gRPC uses fully
qualified method names.
Banxico client spans do not inject `traceparent` or baggage to the provider.

Custom histograms own boundaries appropriate to the recorded unit. Operation,
provider, and delivery latency cover 5 ms through 10 s; authentication and
request admission cover 1 ms through 5 s. Accepted observation age covers zero
through seven days, and streamed-offer counts cover zero through 10,000. Values
outside those ranges remain in the histogram underflow or overflow bucket. Do
not derive average provider latency by dividing its duration sum by work-unit
count: durations are per request, while work units describe batch volume.

## Data handling and cardinality

Never put authorization values, assertions, the Banxico token, request or
response bodies, SQL arguments, prices, principal/client/assertion IDs,
idempotency keys, request digests, event IDs, offer IDs, quote IDs, raw inbound
paths, or arbitrary destination input into metric attributes or general trace
attributes. Configured destination names may appear only on delivery spans.
The REST instrumentation sees normalized methods, allowlisted routes, an empty
query, and a fixed host; the adapter restores the original request only after
propagation and span creation. This prevents unmatched paths, query values,
host headers, and user-agent values from becoming standard span attributes.
Protected security logs retain audit identifiers under the access and retention
rules in the ASVS profile.

Security logs are never sampled. Trace sampling affects whether a correlated
trace is retained, so a production collector should use a reviewed policy for
errors and high latency. Metrics remain aggregated independently of trace
sampling.

## Operations and verification

Candidate dashboards and alerts map directly to the FMEA:

- same-identity self-trade rejection volume for FM-002, without principal,
  offer, bond, or price labels;
- route-specific HTTP/gRPC latency, authentication and rate-limit decisions,
  admission latency, stream duration, and pgx pool
  wait/utilization for FM-007 and FM-022;
- append-only growth, long snapshots, and storage capacity from one
  deployment-owned PostgreSQL receiver for FM-006;
- SIE authentication/rate limiting, accepted observation age, rejected stale
  observations, lease contention,
  and quote rejection for FM-012 and FM-017;
- configured publisher count, delivery failure/timeout, backlog size, and oldest
  pending age for FM-010 and FM-011; and
- collector refusal, queue saturation, dropped telemetry, and export failure for
  FM-008 and FM-021.

Alert thresholds, owners, protected backend, retention, and shared-database
receiver remain deployment decisions. Do not lower FMEA detection scores until
those alerts have routing and response evidence.

Run `devenv tasks run observability:check` to validate the collector
configuration and deterministic tests for OTLP export, shutdown flush,
propagation, log correlation, metric metadata, values and histogram boundaries,
bounded standard and application route/label contracts, panic completion, and
Banxico non-propagation. The gate belongs to `dev:ci`; CI retains its JSON report.
`devenv tasks run integration:load` remains the tool for establishing an
accepted telemetry overhead budget before production rollout.
