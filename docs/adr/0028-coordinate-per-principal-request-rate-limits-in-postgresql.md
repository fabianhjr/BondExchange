# ADR-0028: Coordinate per-principal request rate limits in PostgreSQL

## Status

Accepted

## Context

Every REST and gRPC operation resolves a federated assertion to one internal
principal, and multiple stateless server instances may serve that principal at
the same time. A process-local limiter would therefore grant a separate
allowance on every instance and lose its state during replacement. The
repository has no production mesh, sidecar, or separate coordination service,
while PostgreSQL is already the shared authority used by all instances.

The service needs to admit no more than 100 authenticated requests for one
principal in one database-clock minute. The policy must cover reads,
mutations, health checks, manual event publication, authorization failures,
and exact idempotent retries without making user-controlled identity claims or
network addresses into the key.

## Decision

After an assertion is cryptographically validated and its `(issuer, subject)`
is resolved, but before authorization or application work, admit the request
against the internal principal UUID. All client IDs and operations for that
principal share one fixed UTC-minute window. A server stream consumes one
admission when it begins. Requests that cannot establish an authenticated
principal do not reach this control and remain a deployment-ingress concern.

PostgreSQL stores one mutable operational row per principal. An atomic upsert
uses `statement_timestamp()` to reset a stale window or increment a current
one only while its count is below 100. Rejected attempts do not increase the
counter. Database time avoids disagreement between application instances, and
the unique principal key serializes concurrent decisions across processes.
This table is bounded coordination state, not an append-only domain,
authorization, operation, or audit fact.

Return gRPC `ResourceExhausted` with `google.rpc.RetryInfo` when the allowance
is exhausted. REST returns HTTP 429 with an integer `Retry-After` header.
Failure to read or update the shared state fails closed as gRPC `Unavailable`
or HTTP 503. Emit bounded decision telemetry without principal identifiers;
the protected security log may retain the internal principal audit ID.

## Consequences

- REST and gRPC share one policy and multiple instances cannot multiply a
  principal's allowance.
- An authenticated request consumes capacity even if later authorization,
  validation, or business logic rejects it. Exact idempotent retries also
  consume capacity.
- The fixed-window definition is simple and deterministic, but permits up to
  200 admitted requests close to a UTC-minute boundary. It is not a rolling
  60-second or concurrency limit.
- Every authenticated request adds a PostgreSQL write or conflict check. A hot
  principal serializes on one small row, and database unavailability prevents
  authenticated admission.
- Invalid assertions and traffic rejected before authentication are not
  limited per principal. Production ingress anti-automation, connection, and
  unauthenticated-traffic controls remain necessary.
- Request admission does not change exchange-domain transitions or invariants,
  so the TLA+ specification remains unchanged.

## Alternatives considered

### Keep rate limiting in a mesh or sidecar

This can reject traffic before application work and may remain useful for
unauthenticated or network-level controls. No such deployment boundary is
currently owned or verified, and it would need a trustworthy mapping to the
same authenticated principal to provide this policy.

### Use process-local token buckets

They avoid a database write, but each replica grants its own capacity and
replacement resets it. That conflicts with stateless horizontal serving and
does not implement one per-principal allowance.

### Store every request timestamp for a rolling window

This prevents the fixed-window boundary burst, but adds more retained or
mutable state and more complex pruning and concurrency behavior. Adopt it in a
new decision if the product requires a strict rolling 60-second window.
