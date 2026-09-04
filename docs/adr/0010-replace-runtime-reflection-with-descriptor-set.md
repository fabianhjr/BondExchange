# ADR-0010: Replace runtime gRPC reflection with a versioned descriptor set

- Status: Accepted
- Date: 2026-09-03

## Context

The server registered the standard gRPC reflection service when
`BOND_EXCHANGE_ENABLE_REFLECTION=true`. That service shared the application
listener but bypassed its operation authenticator and PostgreSQL RBAC checks,
even though the initial RBAC facts granted operators `reflection.use`.

Applying the existing operation-bound assertion model to reflection would
introduce an exception: reflection is a bidirectional stream containing many
different descriptor queries, while an application assertion is bound to one
operation and one deterministic request. A separate diagnostic listener would
still require independently designed and verified deployment access controls.
The versioned Proto3 contract is already available at build and review time.

## Decision

Do not register runtime gRPC reflection on any server listener. Remove the
environment switch rather than retaining a dormant exposure path.

Generate and commit a `google.protobuf.FileDescriptorSet` with the Go,
gRPC-Gateway, and Swagger artifacts. Make API freshness checks cover that
descriptor, and configure development and diagnostic clients such as
`grpcurl` to load it explicitly instead of querying the running service.

Preserve the initial `reflection.use` permission and role grant as immutable
security history. Append a role-permission revocation in a forward dbmate
migration so the obsolete bootstrap grant is no longer effective. Do not
rewrite the migration that introduced the facts.

This changes transport discovery and security only. It does not change domain
behavior, the Proto3 API, or the TLA+ model.

## Consequences

- Reaching the gRPC listener cannot reveal its service and message inventory
  through the standard reflection protocol.
- Operators and generic clients must obtain the descriptor set matching the
  deployed API version. Selecting that artifact becomes an explicit,
  auditable client action.
- Descriptor drift becomes a build-time concern, so `api:check` must regenerate
  and compare the checked-in artifact.
- The server loses the convenience of live service discovery, including for
  ad hoc tools that do not accept a descriptor source.
- The historical permission remains queryable in the append-only audit model
  but grants no effective access after its revocation.

## Alternatives considered

### Authorize reflection on the application listener

A stream interceptor could require a `reflection.use` assertion and an RBAC
check. Standard clients issue several reflection requests on one stream, so
the assertion would authorize a session rather than bind to every request.
That exception, continued introspection exposure, and repeated authorization
checks were not justified for a diagnostic convenience.

### Expose reflection on a separate diagnostic listener

A loopback, Unix-socket, mTLS, or network-policy boundary could isolate
reflection from application RPCs. This adds listener lifecycle and deployment
policy that the repository cannot verify end to end. An offline descriptor set
provides the required tooling without another runtime boundary.
