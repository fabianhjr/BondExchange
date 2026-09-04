# ADR-0006: Use Proto3 for the REST and gRPC API

- Status: Accepted
- Date: 2026-09-03

## Context

The server initially exposed a handwritten REST adapter. It now needs a native
gRPC interface and a generated OpenAPI/Swagger artifact without allowing the
REST routes, gRPC messages, implementation interfaces, and documentation to
evolve as independent contracts.

The domain and persistence layers must remain transport-independent. The API
change does not alter buying behavior, active-offer filtering, database
serialization, or the TLA+ abstraction.

## Decision

Use a versioned Proto3 package, `bondexchange.v1`, as the source of truth for
the server transport contract. Define REST routes with `google.api.http`
annotations and REST response schemas with gRPC-Gateway OpenAPI annotations.

Use Buf with locked remote schema dependencies and Nix-pinned local generation
plugins. Generate and commit:

- Go protobuf messages;
- gRPC client and server bindings;
- an in-process gRPC-Gateway REST handler; and
- a Swagger 2.0 JSON artifact; and
- a `FileDescriptorSet` for offline gRPC tooling.

Lint the Proto3 source and verify generated-artifact freshness as part of the
devenv test graph and Go CI. Contract changes begin in Proto3; generated files
are not edited directly.

Implement the generated service in an internal adapter that depends on
`internal/exchange`. Register the same adapter with a native gRPC server and
directly with the generated REST gateway, avoiding a loopback call from REST to
gRPC. Keep the existing REST paths, snake-case JSON names, success statuses,
and error envelope. Map domain errors to canonical gRPC status codes before the
gateway maps them to HTTP statuses.

Run the internal REST and gRPC interfaces on separate configurable listeners.
Keep `BOND_EXCHANGE_ADDRESS` for REST and add
`BOND_EXCHANGE_GRPC_ADDRESS`; their defaults are `127.0.0.1:8080` and
`127.0.0.1:9090` respectively.
Do not register runtime gRPC reflection. Generate and check in a descriptor set
for clients that need dynamic tooling, as refined by ADR-0010.
Both transports require the same application-layer federated assertion and
authorization checks. Transport encryption remains a pending
deployment-boundary decision for the current server.

## Consequences

- REST, gRPC, generated Go interfaces, and Swagger derive from one reviewed
  contract.
- REST clients retain their routes, status codes, exact decimal strings, and
  JSON error shape; identity fields are intentionally removed and reserved.
- gRPC clients receive typed protobuf messages and canonical status codes.
- Generated code, Swagger, and the descriptor set increase the repository size
  and must be regenerated whenever the Proto3 contract changes.
- The server opens two internal ports and deployments must route and secure
  each interface that they make reachable.
- Proto3 compatibility rules and stable field numbers constrain later API
  evolution.
- The Proto3 schema describes transport encoding only. Domain behavior remains
  in `internal/exchange`, and the TLA+ model remains transport-agnostic.

## Alternatives considered

### Handwritten REST beside generated gRPC

This would minimize gateway dependencies but duplicate route, message, status,
and documentation decisions. It was rejected because drift would remain the
default failure mode.

### REST gateway through a loopback gRPC connection

This is the common reverse-proxy deployment shape and permits independently
deployed gateway and gRPC processes. It was not selected because both
interfaces currently live in one stateless process; an in-process registration
keeps identical semantics without an unnecessary network hop. The generated
gateway can still be registered against a gRPC client if deployment boundaries
change later.

### Multiplex REST and gRPC on one listener

HTTP/2 and h2c routing can serve both protocols on one port. Separate listeners
were selected because they make ingress policy, health checks, plaintext local
development, and graceful lifecycle management explicit. Multiplexing can be
reconsidered if the deployment platform requires one exposed port.

### Generate OpenAPI from handwritten HTTP code

Code-first tooling could retain the existing adapter, but the gRPC definition
would still be a second contract. Generating both gateway and Swagger from
Proto3 makes transport compatibility reviewable in one place.
