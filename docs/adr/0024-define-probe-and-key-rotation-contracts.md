# ADR-0024: Define health-probe and verification-key rotation contracts

- Status: Accepted
- Date: 2026-09-05

## Context

Two deployment behaviors were implemented but undecided, so an operator had to
guess and could reasonably guess wrong.

`CheckHealth` requires a short-lived operation-bound assertion, principal
resolution, the `health.read` permission, and a successful database ping. An
orchestrator that wires it to a liveness probe must mint application credentials
continuously, and it restarts a healthy process whenever the identity provider
or the database is unavailable — converting a dependency incident into restart
churn at exactly the moment recovery matters (FM-015).

Verification keys are read once at startup from one local JWKS file, with one
issuer and one audience, and never refreshed. Whether that is an accepted
constraint with a procedure, or a defect awaiting bounded refresh, was never
decided, so no rotation procedure existed to rehearse and key rotation was an
untested operation on the authentication path (FM-009).

## Decision

### `CheckHealth` is readiness, never liveness

`CheckHealth` reports whether this instance can serve authenticated traffic. It
is a readiness signal. An orchestrator must remove a failing instance from
service and must not restart it on that basis.

Liveness is process liveness. A successful TCP connection to the REST or gRPC
listener is the liveness signal, and it requires no credential, no database, and
no application endpoint. A process that has bound its listeners and is not
wedged answers it; one that cannot is genuinely dead and should be replaced.

This deliberately adds no unauthenticated HTTP endpoint. An unauthenticated
`/livez` would be a new anonymous surface on both transports for information a
TCP connect already provides.

The two dependency failures stay distinguishable at the API: a database failure
returns `Unavailable`, an authorization failure returns `PermissionDenied`, and
both are covered by tests in `internal/rpcapi`.

### Verification-key rotation is restart-based with a published overlap

Rotation is a sequence of deployments rather than a refresh:

1. publish the incoming public key alongside the retiring one in the JWKS file
   and restart, so the service accepts assertions signed by either;
2. move signers to the incoming key, with the overlap window sized to the
   longest assertion lifetime the issuer grants — at most five minutes at this
   boundary — plus the time to complete the signer rollout;
3. remove the retiring key from the JWKS file and restart.

Emergency revocation is step 3 executed immediately, accepting that assertions
signed by the revoked key fail from that moment.

Every key in the set needs a unique `kid`. Startup already refuses a set with a
duplicate identifier, which is what makes an overlap window safe: two keys under
one identifier would leave the accepted signer ambiguous.

`TestVerificationKeyRotationOverlap` executes the three steps as three
authenticator configurations and asserts which signer each accepts;
`TestVerificationKeySetRejectsDuplicateKeyIDs` pins the startup refusal.

## Consequences

- An orchestrator has a stated contract: TCP connect for liveness, authenticated
  `CheckHealth` for readiness. FM-015's cause is addressed by a decision rather
  than by code, so its occurrence drops while its severity does not.
- Rotation has a rehearsable, tested procedure. F-008 is resolved by its first
  branch — an explicit deployment contract with a tested overlap — rather than
  by implementing refresh.
- Rotation still requires a restart, so it is coupled to deployment tooling and
  cannot be driven by the identity provider alone. Emergency revocation is as
  fast as a restart, not instantaneous.
- Serving more than one issuer still requires separate deployments. Nothing here
  changes that; it remains a scope decision rather than an accepted defect.
- The tests exercise the authenticator, not a live process replacement. They
  prove which signers each key set accepts; they do not prove that a particular
  orchestrator performs the restarts correctly.

## Alternatives considered

### Add an unauthenticated liveness endpoint

Conventional, and it would let a probe distinguish "process up" from "port open"
— a wedged process holding a bound socket would still pass a TCP check. It adds
an anonymous endpoint on both transports and a Proto contract change for
information the socket already approximates. If a wedged-but-listening failure
is ever observed, this becomes the right answer and this ADR should be revisited.

### Make `CheckHealth` unauthenticated

This would let one probe serve both purposes, but it would publish database
reachability to any caller that can reach the port and remove the only current
demonstration that the health path enforces the same authorization model as
every other operation.

### Implement bounded JWKS refresh

Refreshing from the issuer would decouple rotation from deployment and make
emergency revocation faster. It also adds a network dependency to the
authentication path, needs its own failure policy for a refresh that fails or
returns an empty set, and introduces a cache whose staleness becomes a new
security property to reason about. Restart-based rotation with a tested overlap
is the smaller commitment for a service that is not yet deployable at all
(F-011); refresh remains the better answer once a deployment exists.

### Leave both undecided

The status quo. Both behaviors are already implemented, so leaving them
undecided does not avoid a commitment — it just leaves the commitment
undocumented and the operator's reasonable misuse of `CheckHealth` unguarded.
