# ADR-0009: Bind federated authorization to idempotent operations

- Status: Accepted
- Date: 2026-09-03

## Context

REST and gRPC are internal interfaces used by both people and automated
clients. Caller-supplied buyer and seller IDs cannot establish identity, and a
generic bearer token could be replayed for a different sensitive operation.
Concurrent mutation retries also need a durable result independent of the
stateless server instance that receives them.

The service needs simple authorization and auditable history without making a
deployment-specific identity provider, service mesh, or log backend part of
the domain package.

## Decision

Require every operation to carry a short-lived signed JWT assertion from one
configured federated issuer. Bind it to the audience, one operation, the
SHA-256 digest of the deterministic protobuf request, and, for mutations, one
transport UUIDv4 idempotency nonce. Require a UUIDv4 JWT `jti`. Accept only configured public JWKs and explicit
EdDSA or ES256 algorithms. Resolve `(issuer, subject)` to an internal principal
instead of accepting a user ID in a request. Record whether the principal is a
human or automated client.

Implement PostgreSQL RBAC with append-only principals, roles, permissions,
grants, revocations, suspensions, and reinstatements. Derive current
permissions through a view and authorize every operation, including health.
RBAC administration remains a separately authorized provisioning workflow;
this API does not yet expose administration endpoints.

Make `Buy` and `CreateSaleOffer` durably idempotent in a scope composed of
principal, client ID, operation, and UUIDv4 idempotency nonce. Store the request and
assertion digests with the operation claim and its result. An exact retry may
present a freshly issued assertion but must keep the request and nonce. Reusing
the scope for another request is a conflict.

Keep all domain, RBAC, and operation facts append-only. Do not return buyer or
seller identity in API responses. Emit structured security-event logs with
audit identifiers and OpenTelemetry trace/span correlation, but never emit
assertions, authorization headers, request bodies, signing keys, or federated
subjects.

Treat a successful `Buy` as a binding order or reservation. Settlement,
payment, custody, transfer, cancellation, and expiry semantics are pending and
are not introduced by this decision.

## Consequences

- Human and automated clients share one narrow, operation-bound resource
  server contract while their upstream authentication assurance may differ.
- A captured assertion cannot authorize a different operation or request; its
  short lifetime still makes verifier key rotation and issuer security
  important.
- Identity and authorization changes are auditable additions rather than
  destructive updates. Effective-access queries and audit storage grow with
  history.
- Stateless instances can replay successful mutation results from PostgreSQL.
  Clients must retain idempotency nonces and request bytes for safe retries.
- The API compatibility break removes `buyer_id` and `seller_id`; their field
  numbers and names remain reserved in Proto3.
- External identity lifecycle, TLS, secret delivery, mesh policy, telemetry
  export, immutable log storage, and retention remain explicit pending
  deployment decisions.

## Alternatives considered

### Trust identity headers from a proxy

This is simple but makes correctness depend on every route stripping
attacker-supplied headers. A verifiable operation assertion keeps the trust
boundary explicit across both transports.

### Accept ordinary audience-bound access tokens

An ordinary token can authorize a class of actions but is reusable across
requests until it expires. Binding an authorization detail to the operation,
request digest, and mutation key limits that replay surface.

### Keep mutable role memberships

Updates and deletes are easier to query but discard who granted or revoked
access and when. Append-only facts match the repository's audit-first model.

### Store idempotency state in memory

That fails across restarts and multiple stateless instances. PostgreSQL is
already the shared concurrency and durability authority.
