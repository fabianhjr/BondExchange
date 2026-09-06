# ASVS 5.0.0 Level 3 application profile

This repository targets the application-controlled portions of OWASP ASVS
5.0.0 Level 3. The assessment baseline is the upstream English source, pinned
as the `third_party/asvs` submodule of
[OWASP/ASVS](https://github.com/OWASP/ASVS) at tag `v5.0.0_release`, commit
`5cf9b032440be53ce345ab3c130fda46ba1ce7a2`. The requirement text lives in
`third_party/asvs/5.0/en` and is licensed by OWASP under Creative Commons
Attribution-ShareAlike 4.0 International; this repository references it rather
than copying it. Check it out with:

```console
git submodule update --init --depth 1 third_party/asvs
```

`devenv tasks run security:check` verifies that the submodule is present, that
it is at the recorded commit, and that this document records the same baseline.
Anyone with the repository can therefore reproduce the assessment input; no
contributor-local path is involved. Moving to another ASVS release requires
re-pinning the submodule and reviewing every disposition below.

The requirement-level profile is
[`asvs-5.0.0-l3.tsv`](asvs-5.0.0-l3.tsv). Its 345 requirement identifiers and
levels are compared against the pinned source on every run of the security
check, so the profile cannot silently drift from the standard it claims to
cover. Each requirement carries one of these dispositions:

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
of the deterministic protobuf request, and, for a mutation, the same canonical
UUIDv4 idempotency nonce carried in transport metadata. The JWT `jti` is also a
UUIDv4 nonce. The service accepts EdDSA and
ES256 by default, selects a configured public JWK by `kid`, and validates the
issuer, audience, subject, identifier, issued-at time, expiry, and maximum
five-minute assertion lifetime. It never accepts identity from the request
body.

The federated `(issuer, subject)` resolves to an append-only PostgreSQL
principal classified as `human` or `automated`. Effective permissions are
derived from append-only role grants and revocations. Authorization and a
mutation execute in one database transaction. Operation claims are scoped by
principal, client, operation, and UUIDv4 idempotency nonce; a successful exact retry
returns the original result, while reuse for another request is rejected.

Every successfully authenticated request is then admitted against one mutable
PostgreSQL row keyed by the internal principal UUID. REST and gRPC, all
operations, and all asserted client IDs share a fixed database-clock UTC-minute
allowance of 100; idempotent retries count and streams consume one admission at
start. Exhaustion returns a bounded retry delay. Coordination failure fails
closed, while requests that do not establish an authenticated principal remain
the responsibility of deployment ingress controls.

Sale-offer and purchase responses deliberately omit seller and buyer
identifiers. The database retains those immutable identifiers for restricted
audit. This prioritizes auditability over erasure while avoiding disclosure
through market-data responses. Assertions, authorization headers, request
bodies, issuer subjects, signing keys, and external-service credentials must
never be logged. The integration harness passes assertions to Hurl as redacted
secrets and pipes generated Vegeta targets directly to the load runner; result
artifacts contain response measurements but not request headers, target bodies,
or the ephemeral private key. The internal Banxico SIE client accepts a 64-character
provider token only at construction and sends it in the `Bmx-Token` header to
a fixed HTTPS origin. It does not place the token in a URL, database row,
recording, error, or log.

`Buy` records a binding order or reservation against an offer. Payment,
custody, ownership transfer, finality, cancellation, expiry, and all other
settlement semantics remain explicitly pending; this change does not implement
or model them.

The same internal UUID cannot be both the buyer and the referenced offer's
seller. Principal-specific discovery omits those offers, the application
records a durable `self_trade_prohibited` rejection for direct attempts, and a
PostgreSQL trigger rejects alternate-writer inserts. This does not infer common
beneficial ownership or relationships between distinct principals.

Active-offer listing remains unbounded in count but is streamed with
backpressure from one repeatable-read PostgreSQL snapshot. Native gRPC sends
one event per offer and a terminal count event. REST uses RFC 7464 JSON Text
Sequences (`application/json-seq`), using the same event schema. There is no
implicit truncation or pagination. A small generated workload exercises
populated REST listings and records response size and latency, but it is not a
production capacity claim. The authenticated fixed-window limit does not bound
stream duration, concurrent streams, result cardinality, or traffic rejected
before a principal is established.

Input controls include typed protobuf decoding, unknown-field rejection,
duplicate-key rejection at every JSON object depth, a single top-level JSON
object, exact query cardinality, canonical identifiers, exact decimals,
MXN/USD submission policy, UUIDv7 conversion-quote references, 64 KiB
message/body limits, HTTP
read/header/idle timeouts, a refreshed 30-second streaming write deadline,
bounded PostgreSQL connections, and no runtime gRPC reflection. A checked-in
descriptor set supports offline tooling without exposing service discovery.
Unexpected errors and panics produce generic responses. SIE responses are
limited to 1 MiB, require JSON on success, and are decoded into validated
series IDs, dates, and positive exact decimals. The HTTP client has a
five-second default deadline and rejects redirects outside Banxico's HTTPS
origin. PostgreSQL leases coordinate duplicate fetches without holding a
transaction across the network call. Token-wide rate-limit reset state is
shared across instances.

Successful SIE imports and normalized observations are append-only. Recordings
are offline fixtures with the token replaced by `<REDACTED>`; live capture is
an explicit developer task, rejects credential reflection in the response,
and is not invoked by CI.

USD sale-offer intake fixes `SF43718` as MXN per USD and rejects stale, future,
unpersisted, or over-seven-day observations. A five-minute append-only quote
pins its exact revision and half-to-even four-place result. Create verifies the
quote's principal, bond, amount, expiry, and single use transactionally, then
stores and serves only MXN core terms while retaining USD provenance. This is a
denomination and integrity control, not a guarantee that the business-date
rate policy is suitable for production; that ownership remains pending.

Successful offer creation and buying atomically record an integration event
UUIDv7 plus only its immutable source-table name, source UUID, schema version, and completion
time. Versioned loaders reconstruct minimized payloads in memory and do not
include buyer, seller, assertion, issuer-subject, request, or credential data.
Publication occurs after commit, is at least once, and uses per-destination
UUIDv4 lease nonces. Publisher failures and panics are contained and cannot
change a committed domain result. Consumers must deduplicate by event UUID.

There is no automatic event recovery process. `PublishPendingEvents` requires
an operation-bound assertion, `events.publish`, and a UUIDv4 idempotency nonce; it
attempts pending deliveries only when explicitly invoked and returns aggregate
counts rather than event data. The repository configures no concrete publisher.
Destination authentication, transport security, secrets, allowlists, payload
classification, and operational invocation remain deployment decisions.

Security events are JSON logs with source-code location and the operation,
outcome, safe error class, principal audit ID, client ID/class, assertion ID,
request digest, and stream count where applicable. Every context-aware log adds
its OpenTelemetry trace ID, span ID, and sampled state when a valid span is
present. The application owns OTLP trace and metric provider lifecycle but
enables export only through standard `OTEL_*` configuration; no destination or
credential is compiled into the binary. Malformed input, authentication and
authorization decisions, domain outcomes, unexpected errors, and recovered
panics are logged without request or credential contents. Metric labels and
general span attributes exclude audit IDs, financial terms, request contents,
credentials, and dynamic resource identifiers.

## Architecture decisions and tradeoffs

| ID | Implemented application decision | Principal risk or tradeoff |
| --- | --- | --- |
| AD-1 | `Buy` means a binding order/reservation; settlement is pending and unchanged. | The current name `purchase` can be over-read as settled ownership, so API and domain documentation must retain the qualification. |
| AD-2 | Principals explicitly distinguish human and automated clients. | One authorization model serves both classes; external assurance and factor policy can differ by class. |
| AD-3/4 | A federated assertion is bound to one operation, canonical request, audience, and UUIDv4 idempotency nonce. | Clients must obtain a fresh assertion whenever the request or operation changes; retries may use a fresh assertion only with the original nonce and request. |
| AD-5 | PostgreSQL-backed RBAC uses immutable grants, revocations, suspensions, and reinstatements. | Effective access is a derived view; administration requires append-only provisioning until an admin API is designed. |
| AD-6 | REST and gRPC are internal and both enforce the same application controls. | Internal status does not justify bypassing authentication; network reachability is still a pending deployment control. |
| AD-7 | Complete offer books use streaming semantics and a stable database snapshot. | Slow readers retain a database connection and snapshot; per-principal concurrency limits remain pending. |
| AD-8 | Both mutations are idempotent using a durable operation scope and request digest. | Operation records grow append-only and need deployment-owned capacity/retention monitoring. |
| AD-9 | Most application resource ceilings are local; mesh/sidecar ingress controls and service identity remain pending. | A mesh can centralize policy but creates another parser and authorization boundary that must be assessed end to end. |
| AD-10 | Domain, RBAC, and operation facts are append-only; response minimization protects user identity. | Audit records cannot satisfy erasure semantics without a future legal and architectural decision. |
| AD-11 | The application owns OpenTelemetry trace/metric instrumentation and OTLP lifecycle; JSON security logs correlate with active spans. | Collector routing, protected storage, access, alerting, retention, and production sampling remain deployment concerns. |
| AD-12 | Expected domain failures stay detailed unless detail would enable identity or credential enumeration. | Authentication and authorization failures are generic; unexpected failures never expose database, token, or stack details. |
| AD-13 | Runtime gRPC reflection is absent; clients use a versioned descriptor set. | Operators lose live discovery and must select an artifact matching the deployed API. |
| AD-14 | Integration events persist only immutable source references and use immediate best-effort delivery with explicit manual recovery. | Source loaders must remain compatible for the retention period, delivery is at least once, and pending events can remain indefinitely without operator action. |
| AD-15 | Banxico SIE responses and exact exchange-rate revisions are durable; PostgreSQL leases and cooldowns coordinate on-demand fetches. | Durable provenance grows over time, stale latest values are possible during refresh failures, and a crash before import commit can cause a repeated upstream request. |
| AD-16 | PostgreSQL 18 generates and enforces UUIDv7 table identities; UUIDv4 is reserved for idempotency, assertion, and lease nonces. ADR-0033 retired the pre-UUID evidence archive after its accepted retention period. | UUIDv7 reveals approximate creation time; retired evidence can be recovered only from a verified pre-retirement backup. |
| AD-17 | A separate intake layer turns an explicitly accepted `SF43718` USD quote into immutable MXN core terms and retains USD only as provenance. | USD intake depends on rate policy and availability; legacy non-MXN offers require seller disposition and old binaries must be drained before activation. |
| AD-18 | PostgreSQL coordinates a 100-request fixed UTC-minute allowance per authenticated principal across transports, operations, clients, and instances. | The fixed window permits a boundary burst, adds a shared-database write, and cannot control unauthenticated, concurrent, or connection-level traffic. |
| AD-19 | Same-identity self-trading is prohibited in discovery, execution, PostgreSQL, and TLA+. | Distinct principals can still share a beneficial owner because no authoritative affiliation data exists. |

## Pending non-code and deployment decisions

The following items are intentionally not guessed or implemented here. They
remain open even though the application fails closed and binds only to
loopback by default:

- TLS versions, certificates, HTTPS behavior, workload/service identity,
  ingress trust, trusted forwarding headers, and REST/gRPC network exposure;
- whether a mesh/sidecar owns mTLS, service-to-service authorization,
  unauthenticated and connection limits, complementary rolling/burst controls,
  and broader anti-automation controls;
- identity-provider assurance, human MFA, automated workload credentials,
  account recovery, factor lifecycle, and authorization-server policy;
- secret injection and rotation for `DATABASE_URL`, verification keys, and any
  runtime `BANXICO_SIE_TOKEN`;
- PostgreSQL encryption, backup protection, runtime/migration roles, high
  availability, capacity, and retention;
- OpenTelemetry collector/backend selection, authenticated export, production sampling,
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
tests. It runs inside the disposable migrated-PostgreSQL harness and refuses to
start without it, because the persistence tests it names would otherwise skip
and report success without verifying authorization, idempotency, or schema
constraints. `integration:test` verifies a freshly signed idempotent retry,
response identity minimization, per-principal REST `429`, `Retry-After`, and
next-window reset through the complete REST server, while
`integration:load-smoke` verifies authenticated status distributions under a
small generated multi-principal workload. The Go quality gate also runs pinned, curated source
analysis including `gosec`, dangerous-Unicode checks, context propagation,
error handling, and resource-lifecycle checks. These tasks are part of
`devenv test` and the Go quality workflow, which run the same gates through the
`dev:ci` and `go:mutation` aggregates; `dev:check` fails when a gate attaches
itself to the test entry point without joining one, so security evidence cannot
be produced locally while being absent from CI. API generation, race tests,
PostgreSQL integration, coverage, mutation, and TLC checks remain independent
evidence layers. `observability:check` separately validates the loopback
collector, OTLP export and flush, REST/gRPC propagation, bounded metric and
route attributes, trace/log correlation, and the Banxico propagation boundary;
its JSON report is retained by continuous integration.

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
as risky. Dangerous boundaries are the JSON/JWT and SIE response parsers,
outbound SIE HTTP client, protobuf gateway, cryptographic verification, SQL
adapter, and demo-only private-key parser;
they are isolated behind size limits, strict decoding, prepared parameters,
public-key-only server configuration, and focused negative tests.
