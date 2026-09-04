# ASVS 5.0.0 Level 3 application profile

This repository targets the application-controlled portions of OWASP ASVS
5.0.0 Level 3. The source baseline is the English release at
`/home/fabian/Downloads/ASVS-5.0.0_release/5.0/en`; the ordered Markdown-source
checksum used for this profile is
`114f24ef5ddac9105fd72804ca2bf652152c8f5d22e770c1a92cc66453b0a518`.
That local path is an assessment input, not a build dependency.

The requirement-level profile is
[`asvs-5.0.0-l3.tsv`](asvs-5.0.0-l3.tsv). It contains all 345 requirements in
the supplied release and one of these dispositions:

- `verified`: application-owned behavior has implementation, test, or design
  evidence in this repository;
- `not-applicable`: the named functionality is absent from the application;
- `pending-deployment`: the control depends on a deployment, transport,
  storage, or operations decision; or
- `pending-external-identity`: the control belongs to the federated identity
  and authorization server rather than this resource service.

This profile is evidence for assessment; it is not an ASVS certification.
Changing scope, adding an endpoint, accepting another data type, or changing
an identity/token boundary requires reviewing every affected disposition.

## Security architecture

REST and gRPC are internal interfaces over the same stateless application
adapter. Every operation requires one short-lived signed JWT assertion from a
configured federated issuer. The assertion contains one authorization detail
of type `urn:bond-exchange:operation:v1`, exactly one action, the SHA-256 digest
of the deterministic protobuf request, and, for a mutation, the same
idempotency key carried in transport metadata. The service accepts EdDSA and
ES256 by default, selects a configured public JWK by `kid`, and validates the
issuer, audience, subject, identifier, issued-at time, expiry, and maximum
five-minute assertion lifetime. It never accepts identity from the request
body.

The federated `(issuer, subject)` resolves to an append-only PostgreSQL
principal classified as `human` or `automated`. Effective permissions are
derived from append-only role grants and revocations. Authorization and a
mutation execute in one database transaction. Operation claims are scoped by
principal, client, operation, and idempotency key; a successful exact retry
returns the original result, while reuse for another request is rejected.

Sale-offer and purchase responses deliberately omit seller and buyer
identifiers. The database retains those immutable identifiers for restricted
audit. This prioritizes auditability over erasure while avoiding disclosure
through market-data responses. Assertions, authorization headers, request
bodies, issuer subjects, and signing keys must never be logged.

`Buy` records a binding order or reservation against an offer. Payment,
custody, ownership transfer, finality, cancellation, expiry, and all other
settlement semantics remain explicitly pending; this change does not implement
or model them.

Active-offer listing remains unbounded in count but is streamed with
backpressure from one repeatable-read PostgreSQL snapshot. Native gRPC sends
one event per offer and a terminal count event. REST uses RFC 7464 JSON Text
Sequences (`application/json-seq`), using the same event schema. There is no
implicit truncation or pagination.

Input controls include typed protobuf decoding, unknown-field rejection,
duplicate-key rejection at every JSON object depth, a single top-level JSON
object, exact query cardinality, canonical identifiers, exact decimals,
three-letter uppercase currency codes, 64 KiB message/body limits, HTTP
read/header/idle timeouts, a refreshed 30-second streaming write deadline,
bounded PostgreSQL connections, and no runtime gRPC reflection. A checked-in
descriptor set supports offline tooling without exposing service discovery.
Unexpected errors and panics produce generic responses.

Successful offer creation and buying atomically record only an integration
event's immutable source-table name, source ID, schema version, and completion
time. Versioned loaders reconstruct minimized payloads in memory and do not
include buyer, seller, assertion, issuer-subject, request, or credential data.
Publication occurs after commit, is at least once, and uses per-destination
leases. Publisher failures and panics are contained and cannot change a
committed domain result. Consumers must deduplicate by `(table_name, id)`.

There is no automatic event recovery process. `PublishPendingEvents` requires
an operation-bound assertion, `events.publish`, and an idempotency key; it
attempts pending deliveries only when explicitly invoked and returns aggregate
counts rather than event data. The repository configures no concrete publisher.
Destination authentication, transport security, secrets, allowlists, payload
classification, and operational invocation remain deployment decisions.

Security events are JSON logs with source-code location and the operation,
outcome, safe error class, principal audit ID, client ID/class, assertion ID,
request digest, and stream count where applicable. When an OpenTelemetry span
is present, logs include its trace and span IDs. This is compatible with the
OpenTelemetry Go automatic-instrumentation agent; the application does not
embed an exporter or collector address. Malformed input, authentication and
authorization decisions, domain outcomes, unexpected errors, and recovered
panics are logged without request or credential contents.

## Architecture decisions and tradeoffs

| ID | Implemented application decision | Principal risk or tradeoff |
| --- | --- | --- |
| AD-1 | `Buy` means a binding order/reservation; settlement is pending and unchanged. | The current name `purchase` can be over-read as settled ownership, so API and domain documentation must retain the qualification. |
| AD-2 | Principals explicitly distinguish human and automated clients. | One authorization model serves both classes; external assurance and factor policy can differ by class. |
| AD-3/4 | A federated assertion is bound to one operation, canonical request, audience, and idempotency key. | Clients must obtain a fresh assertion whenever the request or operation changes; retries may use a fresh assertion only with the original key and request. |
| AD-5 | PostgreSQL-backed RBAC uses immutable grants, revocations, suspensions, and reinstatements. | Effective access is a derived view; administration requires append-only provisioning until an admin API is designed. |
| AD-6 | REST and gRPC are internal and both enforce the same application controls. | Internal status does not justify bypassing authentication; network reachability is still a pending deployment control. |
| AD-7 | Complete offer books use streaming semantics and a stable database snapshot. | Slow readers retain a database connection and snapshot; per-principal concurrency limits remain pending. |
| AD-8 | Both mutations are idempotent using a durable operation scope and request digest. | Operation records grow append-only and need deployment-owned capacity/retention monitoring. |
| AD-9 | Application resource ceilings are local; mesh/sidecar rate limiting and service identity are pending. | A mesh can centralize policy but creates another parser and authorization boundary that must be assessed end to end. |
| AD-10 | Domain, RBAC, and operation facts are append-only; response minimization protects user identity. | Audit records cannot satisfy erasure semantics without a future legal and architectural decision. |
| AD-11 | Logs are structured JSON and enrich automatic OpenTelemetry context. | Automatic instrumentation and the collector are runtime concerns; telemetry data must be classified and protected. |
| AD-12 | Expected domain failures stay detailed unless detail would enable identity or credential enumeration. | Authentication and authorization failures are generic; unexpected failures never expose database, token, or stack details. |
| AD-13 | Runtime gRPC reflection is absent; clients use a versioned descriptor set. | Operators lose live discovery and must select an artifact matching the deployed API. |
| AD-14 | Integration events persist only immutable source references and use immediate best-effort delivery with explicit manual recovery. | Source loaders must remain compatible for the retention period, delivery is at least once, and pending events can remain indefinitely without operator action. |

## Pending non-code and deployment decisions

The following items are intentionally not guessed or implemented here. They
remain open even though the application fails closed and binds only to
loopback by default:

- TLS versions, certificates, HTTPS behavior, workload/service identity,
  ingress trust, trusted forwarding headers, and REST/gRPC network exposure;
- whether a mesh/sidecar owns mTLS, service-to-service authorization,
  per-principal rate limits, connection limits, and anti-automation controls;
- identity-provider assurance, human MFA, automated workload credentials,
  account recovery, factor lifecycle, and authorization-server policy;
- secret injection and rotation for `DATABASE_URL` and verification keys;
- PostgreSQL encryption, backup protection, runtime/migration roles, high
  availability, capacity, and retention;
- OpenTelemetry agent/collector selection, authenticated export, sampling,
  clock synchronization, immutable log storage, access, alerting, and
  retention;
- integration-event destination selection, publisher credentials and network
  policy, duplicate handling, payload approval, pending-event alerting, and the
  operating procedure for explicit recovery; and
- production packaging, SBOM publication/attestation, vulnerability-response
  ownership, and independent ASVS verification.

## Continuous compliance

`devenv tasks run security:check` validates the complete profile, generates a
Go module inventory, runs `govulncheck`, and exercises the security-focused Go
tests. The Go quality gate also runs pinned, curated source analysis including
`gosec`, dangerous-Unicode checks, context propagation, error handling, and
resource-lifecycle checks. These tasks are part of `devenv test` and the Go
quality workflow. API generation, race tests, PostgreSQL integration, coverage,
mutation, and TLC checks remain independent evidence layers.

For every security-relevant change:

1. update the Proto contract, domain, adapter, migration, model, and ADRs that
   the behavior affects;
2. update this profile and requirement evidence in the same change;
3. add a negative test for the control and a boundary/concurrency test where
   applicable;
4. regenerate API artifacts and the module inventory; and
5. run focused checks, then `devenv test` before handoff.

Dependency vulnerabilities use these maximum remediation targets from first
confirmation: 24 hours for known exploitation or critical impact, seven days
for high, 30 days for medium, and 90 days for low. Unsupported components are
high severity at minimum. Exceptions require an ADR with compensating controls
and an expiry date. Dependencies come only from the locked Go module graph,
Buf lock, and Nix/devenv lock. No currently selected component is classified
as risky. Dangerous boundaries are the JSON/JWT parsers, protobuf gateway,
cryptographic verification, SQL adapter, and demo-only private-key parser;
they are isolated behind size limits, strict decoding, prepared parameters,
public-key-only server configuration, and focused negative tests.
