# ADR-0020: Keep the local test gate and continuous integration equivalent

- Status: Accepted
- Date: 2026-09-04

ADR-0033 later removed `db:uuid-contract-history` after the accepted retention
period for its subject data elapsed. References to that gate below describe the
state when this decision was accepted; the aggregate-equivalence rule remains
in force.

## Context

`devenv test` runs every task that names `devenv:enterTest` in its `before`
list. The Go quality workflow did not run `devenv test`; it invoked a
hand-maintained subset of tasks — `dev:smoke`, `security:check`, `go:coverage`,
and `go:mutation` — chosen so that coverage and mutation remain separately
visible jobs with their own timeouts.

Nothing kept the two lists equal. `db:uuid-contract-history`, which verifies
that the legacy-identifier contraction archived every non-derivable value, was
attached to `devenv:enterTest` but was reachable from none of the four CI
invocations. It had never run in continuous integration. That control is cited
by FM-016 in the failure mode and effects analysis, so the analysis credited a
detection capability that the shared branch did not have.

The same class of defect appeared inside a task. `security:check` invoked
`go test ./internal/postgres` directly rather than through the disposable
PostgreSQL harness, so every test in that package skipped for want of
`BOND_EXCHANGE_TEST_DATABASE_URL` and the task reported success. The ASVS
profile cited that invocation as security evidence.

Both defects are the same failure: a gate that is present, passing, and
verifying nothing, discoverable only by reading the task graph.

## Decision

Route `devenv test` through exactly two aggregate tasks and make continuous
integration run those same two tasks:

- `dev:ci` runs every gate except mutation testing;
- `go:mutation` remains separate so its longer runtime and report stay a
  distinct, separately visible job.

No other task may name `devenv:enterTest` in its `before` list. `dev:check`
enforces this: `nix/task-graph-check.sh` fails when the set of tasks attached to
`devenv:enterTest` is not exactly the aggregates, when an aggregate is not
declared, or when the Go quality workflow does not invoke one. New gates join an
aggregate's `after` list instead of attaching themselves to the test entry
point.

Make a gate fail rather than pass vacuously when its dependencies are absent.
`security:check` runs inside the PostgreSQL harness and exits non-zero if
`BOND_EXCHANGE_TEST_DATABASE_URL` is unset, and the PostgreSQL integration tests
fail instead of skipping whenever `CI` is set.

## Consequences

- `devenv test` and the Go quality workflow cover the same gates by
  construction, and the equivalence is checked rather than reviewed.
- `db:uuid-contract-history` runs in continuous integration, so FM-016's
  archival control now has shared-branch evidence.
- `security:check` exercises the PostgreSQL integration tests it names, so the
  ASVS continuous-compliance claim is accurate.
- The aggregate job's wall time grows because it now also runs migration
  archival and provisions a database for `security:check`; its timeout moved
  from 20 to 30 minutes. Its name no longer claims to be a coverage gate alone:
  the workflow's jobs are `quality-gates` and `mutation`, matching the
  aggregates they run.
- Continuous integration reports one aggregated gate step instead of three
  named steps. Task-level progress remains visible in the devenv output, and the
  mutation job stays independent.
- A contributor adding a gate must choose an aggregate. That is a deliberate
  friction: the choice is exactly the one that was previously made implicitly
  and wrongly.

## Alternatives considered

### Run `devenv test` directly in one CI job

This is the simplest equivalence, but it merges mutation testing into the same
job. Mutation has a materially longer runtime and its own report artifact, and
collapsing it would remove a separately visible gate and force a single long
timeout on every run.

### Add the missing task to the existing CI invocation list

Adding `db:uuid-contract-history` to the workflow would fix the instance
without fixing the class. The next task attached to `devenv:enterTest` would be
invisible again, which is how this defect arose.

### Derive the CI task list from `devenv.nix` at workflow runtime

Generating the list would also guarantee equivalence, but it requires evaluating
the devenv configuration to enumerate tasks before the job can plan its steps,
and it would still not prevent a task from running vacuously. Two named
aggregates and a text-level guard achieve the same invariant with no generation
step.

### Delete the redundant `go test` invocation from `security:check`

The security tests are also run by `go:test`, so removing the line would end the
false claim. Keeping the invocation and making it fail without a database is
stronger: it preserves `security:check` as a self-contained gate that a reviewer
can run alone, and it converts a silent skip into a loud failure.
