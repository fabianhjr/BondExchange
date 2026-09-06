# ADR-0013: Require 95 percent test quality gates

- Status: Accepted
- Date: 2026-09-04

## Context

The repository required 90 percent aggregate statement coverage for packages
under `application/internal/` and 80 percent mutation-test efficacy. Both
thresholds passed, but the remaining uncovered code included authentication,
event-publication, HTTP failure, transport, and PostgreSQL error paths whose
behavior is important to security and recovery. The mutation suite already
demonstrated efficacy above a stricter threshold.

## Decision

Require at least 95 percent aggregate statement coverage for
`application/internal/...` and at least 95 percent mutation-test efficacy. Keep
the existing measurement boundaries: coverage uses the PostgreSQL-backed
aggregate profile, and Gremlins mutates implementation under
`application/internal/` while excluding generated and command packages.

Meet the coverage threshold with focused behavior and failure-path tests. Do
not omit packages, remove statements from the coverage profile, or add
production test hooks solely to make the percentage pass. Gremlins'
`mutant-coverage` threshold remains disabled; mutation efficacy is the existing
mutation gate being tightened by this decision.

## Consequences

- Authentication, delivery recovery, HTTP write failures, gRPC stream errors,
  and PostgreSQL failure paths have stronger regression coverage.
- A change that adds untested internal behavior or leaves more than five percent
  of viable tested mutants alive fails the contributor and CI task graph.
- Command composition remains outside the percentage calculations. That
  exclusion was later made deliberate rather than tolerated: the composition
  logic that carries failure modes — environment parsing, verification-key
  loading, pool and transport limits, listener binding and partial-startup
  release, and graceful and forced shutdown — moved into
  `internal/serverruntime`, where these gates measure it. What stays in
  `application/cmd/server` is wiring whose failure the demo smoke and
  integration scenarios already surface, so command packages are intentionally
  excluded from the percentages instead of being an untracked gap. This resolved
  F-016, which the register no longer carries.
- A 95 percent aggregate result does not imply that every function or branch is
  covered, so review remains responsible for risk-based test selection.
- The mutation half of this decision was later found to be unmeasured.
  [ADR-0031](0031-enable-every-mutant-operator-and-verify-the-harness.md)
  records that the configured `test-cpu` setting made Gremlins score every
  mutant as killed without running a test, enables the operators this record
  left unexamined, and splits the 95 percent threshold onto the lines a change
  touches, with a weekly whole-module run at 90 percent. The coverage half is
  unaffected.

## Alternatives considered

### Keep the previous thresholds

This would avoid adding tests, but would continue accepting several reachable
security and recovery branches without direct exercise.

### Require 100 percent

Perfect statement and mutation scores would incentivize intrusive test hooks or
low-value tests for defensive branches that depend on failures inside trusted
runtime and database-driver primitives. The selected threshold preserves room
for those branches while materially tightening the gate.

### Enable a mutant-coverage threshold too

Gremlins separately reports whether mutation sites are exercised at all. That
was not an existing enforced gate and has different semantics from mutation
efficacy, so introducing it requires a separate decision based on stable report
history.
