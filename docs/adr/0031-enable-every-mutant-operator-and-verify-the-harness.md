# ADR-0031: Enable every mutant operator, split the mutation cadence, and verify the harness

- Status: Accepted
- Date: 2026-09-06

## Context

[ADR-0013](0013-require-95-percent-test-quality-gates.md) raised the mutation
efficacy gate to 95 percent and kept the existing measurement boundaries.
Neither that record nor `application/.gremlins.yaml` justified the set of
mutant operators the gate ran. Six of the eleven operators Gremlins 0.6.0
supports were disabled: `invert-assignments`, `invert-bitwise`,
`invert-bwassign`, `invert-logical`, `invert-loopctrl`, and
`remove-self-assignments`. Enabling them raises the analyzed mutants for this
module from 678 to 1027, and the newly enabled operators reach expression forms
the previous set never touched, including boolean operator composition in
authorization and validation predicates, compound assignment in accumulation
and retry arithmetic, and loop control in streaming and scanning paths.

Enabling them exposed that the gate had not been running tests at all.
Gremlins decides a mutant's verdict from the exit status of the test command:
non-zero is KILLED, zero is LIVED. Version 0.6.0 builds the `unleash.test-cpu`
setting as the single argument `-cpu N` rather than as two arguments, so
`go test` reads it as a test binary flag, drops `./...` from package selection,
and exits 1 with `no Go files in <workdir>` before compiling anything. The
repository set `test-cpu: 1`, so every mutant was scored KILLED and every
recorded run reported a 100 percent efficacy that no test had produced. A whole
run finished in under four seconds. Inserting a mutation site that no assertion
can observe confirmed it: the surviving mutant was reported as killed.

Once tests actually ran, the cost became the constraint. Gremlins runs the
whole suite once per mutant in `integration` mode, one mutant at a time, so a
whole-module run takes about three hours on a 24-core developer machine. The
continuous integration job allowed 45 minutes. Raising `workers` is not
available: the integration suite shares one PostgreSQL cluster, and four
concurrent suites exhaust its connection limit.

Fixing `test-cpu` exposed a second cause of the same symptom, this time in the
test suite rather than in Gremlins. `integration` mode runs the whole suite
once per mutant against one PostgreSQL database, so a test that passes on an
empty database and fails on the rows an earlier run left behind fails for every
mutant after the first. Two tests did.
`TestMutationIdempotencyReplaysResultAndRejectsKeyReuse` counted operation
claims for a nonce derived from a fixed label, matching on the nonce alone, so
it counted every earlier run's claims as well.
`TestManualIntegrationEventRecoveryRetriesFailedDelivery` asserted that a
destination had no failed or remaining deliveries after recovery, which
describes every event in the database rather than the one it created. Both
passed the existing gates, which give each task a fresh database and run the
suite once.

Two further properties of the harness affect what the number means. Gremlins
computes each mutant's time budget as `timeout-coefficient` times the elapsed
time of the coverage run, which does not include recompiling the mutated
package and, in a clean checkout, is itself the first compile; and it computes
efficacy as `KILLED / (KILLED + LIVED)`, so a timed-out mutant leaves the
denominator entirely. With the inherited coefficient of 3, a run timed out on
317 of 1027 mutants across eleven files and would have scored efficacy on the
remainder without saying so.

## Decision

Enable every mutant operator Gremlins 0.6.0 supports, run them at two
cadences, and keep the configuration honest about the harness underneath.

**Score changed lines on every change.** `go:mutation` runs Gremlins in diff
mode against the branch's merge base, so a pull request is measured on the
lines it introduced. The threshold stays at 95 percent.

**Score the whole module weekly.** `go:mutation-full` runs every covered
mutant in the module at a 90 percent threshold, from
`.github/workflows/scheduled-mutation.yml`, and only when `application/` or
`db/` changed in the preceding week. Diff mode never re-tests a line an earlier
change introduced, so without this a mutant that a later refactor stops killing
would stay invisible. The lower threshold reflects that this run scores the
whole accumulated module rather than the work in front of a reviewer. The task
stays out of the `devenv test` graph; the scheduled workflow is its only
automatic caller.

**Pin `unleash.test-cpu` to 0** so Gremlins omits the flag it cannot build
correctly. `workers: 1` already serializes mutation runs, which is what the
setting was there to achieve.

**Raise `unleash.timeout-coefficient` to 20**, so that a mutant times out
because it does not terminate rather than because the mutated package had to be
rebuilt. One mutant in `internal/rpcapi` legitimately does not terminate:
removing the self-assignment in `index += 2` produces an infinite loop.

**Enforce the threshold in `nix/go-mutation.sh` rather than in Gremlins.**
Gremlins holds one threshold and these gates need two. A change that touches no
mutable covered line produces zero tested mutants, which Gremlins reports as 0
percent efficacy and fails; that result is empty, not a regression, and the
wrapper passes it with an explicit message.

**Require the suite to survive a second run before scoring any mutant.** The
wrapper runs the unmutated suite twice against the gate's database and stops
with a message naming the cause if the second run fails. `go:mutation-harness`
cannot catch this, because its canary module has no shared state to accumulate.
The two tests above are made independent of rows an earlier run left behind:
the first derives its nonce from a per-run label, and the second asserts that
its own failed delivery was retried and delivered rather than that the whole
destination is clean.

**Bound the run as a whole, not only each mutant.** `unleash.timeout-coefficient`
bounds one mutant against a multiple of the coverage run, and nothing bounds
the run. The wrapper wraps Gremlins in `timeout`, at 20 minutes for a diff run
and 240 minutes for a whole-module run, both overridable through
`BOND_EXCHANGE_MUTATION_TIMEOUT`. The PostgreSQL harness kills the process
group afterwards, so no `go test` child outlives the bound.

**Count a timed-out mutant as killed, and bound how many may time out.**
Gremlins drops a timed-out mutant from both sides of its ratio, so a run can
score a smaller population than it analyzed without saying so. A mutant that
stops the suite from terminating has been detected, so the wrapper counts it as
killed — `internal/exchangerates` alone has a retry loop with no bound, and
mutating the conditions that end it makes the suite hang rather than fail.
Counting timeouts as kills would let a far-too-small per-mutant budget
manufacture a perfect score, so the wrapper also fails the run when timed-out
mutants exceed 30 percent of the tested population
(`BOND_EXCHANGE_MUTATION_TIMEOUT_SHARE`). That is a check on the measurement,
not on the code, and is deliberately loose.

**Set `diff.relative` through the environment for diff runs.** Gremlins gathers
the diff with `git diff --merge-base <ref>` and matches the result against file
names relative to the Go module root. This module is not at the repository
root, so git's repository-relative paths match nothing and every mutant is
skipped. The setting is passed in `GIT_CONFIG_*` variables so that no
contributor's repository configuration is modified.

**Add a `go:mutation-harness` gate that both mutation tasks depend on.** It
runs the repository's own `application/.gremlins.yaml`, through the same
wrapper the gates execute, in both modes, against a throwaway repository whose
layout mirrors this one and whose outcome is known in advance: one mutation
site that no test asserts, which must survive, and one that a test asserts,
which must be killed. A configuration that cannot produce both verdicts cannot
measure efficacy, so neither mutation gate runs.

Record the reason for each setting in `application/.gremlins.yaml` and
`nix/go-mutation.sh`, next to the value it constrains.

## Consequences

- This resolves F-030, which asked that the operator set either be enabled or
  recorded with its reasoning; the register no longer carries that entry.
- Efficacy is computed from mutants that were compiled and tested, against a
  suite that fails for the mutation rather than for the previous mutant's rows.
  The number the gates report is evidence rather than an artifact of an
  argument-quoting defect or of accumulated test data.
- The suite is now repeatable against one database. That property was never
  exercised before, because every task that runs it provisions its own cluster
  and runs it once.
- Boolean operator composition, compound and self assignment, bitwise
  operations, and loop control are covered. A test suite that asserts an `&&`
  predicate only through one of its operands now fails the gate.
- A pull request is scored in seconds to minutes on the lines it changed
  instead of hours on the whole module, and the continuous integration job
  keeps its 45-minute ceiling.
- A mutant on an untouched line is scored weekly rather than on every change,
  so a regression there is found within a week rather than immediately. The
  scheduled run opens or updates one tracking issue on failure.
- The whole-module gate does not pass today. With the harness repaired, a run
  scored well below its 90 percent threshold, with survivors concentrated in
  `internal/eventing/dispatcher.go` and `internal/authn/authenticator.go`. The
  threshold is kept at the value this decision intends rather than lowered to
  what the suite currently reaches, so the first scheduled run reports the gap
  as a tracking issue instead of a green check.
  [F-032](../../FRICTIONS.md#f-032--the-module-does-not-meet-the-whole-module-mutation-threshold-p2)
  carries it.
- Neither mutation gate can silently degrade into a task that reports success
  without exercising its subject. Both failure shapes seen here — every mutant
  killed without a test running, and every mutant skipped because diff paths
  did not match — are detected by `go:mutation-harness` in about a second,
  before the long run starts.
- A run that hangs fails with a message naming the bound it exceeded, instead
  of being killed by the continuous integration job timeout with no report.
  Both job timeouts sit above the wrapper's own bound so that the wrapper
  reports first.
- Upgrading Gremlins requires re-checking `test-cpu`. If a later version fixes
  the argument construction, the setting can be restored to 1 only if
  `go:mutation-harness` still reports both verdicts.

## Alternatives considered

### Keep the previously enabled operator subset

This would leave the gate cheaper, but nothing recorded why those six
operators were excluded, and they cover expression forms the domain uses.
Retaining an unexplained subset would keep crediting the gate for coverage it
does not provide.

### Run the whole module on every change

This is the strongest measurement and was the intent of the previous
configuration, but it takes about three hours per run serially and cannot be
parallelized while the integration suite shares one PostgreSQL cluster. It
would either exceed the job budget or force `integration` mode off, which would
stop crediting mutants that only another package's tests kill.

### Lower the thresholds to what the suite currently reaches

Setting each threshold just under the measured result would make both gates
green immediately and still catch a regression. It would also record the
current state as the intended one, in a decision whose subject is a gate that
had been reporting a number nobody had measured. The thresholds state what the
suite should reach; the register states what it reaches.

### Keep one threshold for both cadences

A single number is simpler. The two runs measure different populations: the
diff gate scores the change under review, where a surviving mutant has an
obvious owner, and the scheduled run scores every accumulated line, where one
does not. Holding the second to the first's threshold would make a weekly
failure the normal state.

### Set `GOMAXPROCS=1` for the mutation task instead of `test-cpu: 0`

This would preserve the original intent of `test-cpu: 1` through an
environment variable that Gremlins passes through to `go test`. It also
constrains Gremlins itself and the coverage run, and it encodes a workaround
in the task rather than in the configuration the workaround is about.
`workers: 1` already provides the serialization the setting was for.

### Fail the run when any mutant times out

This would make the exclusion visible, but several mutants in this module do
not terminate by construction: `internal/exchangerates` retries in a loop with
no bound, and removing the self-assignment in `index += 2` in
`internal/rpcapi` spins forever. The rule would need an exception list that
grows with the code. Counting a timeout as a kill and bounding the share that
may time out gives the same protection without one, and
[F-031](../../FRICTIONS.md#f-031--a-timed-out-mutant-is-credited-as-killed-without-evidence-p3)
records what that trade leaves open.

### Verify the harness by asserting a plausible efficacy range

Rejecting a suspiciously perfect score would have caught the `test-cpu`
defect, but it would also reject a genuinely perfect one, it would not say what
went wrong, and it would not have caught the diff-mode path mismatch, which
reports no score at all. Reproducing a known LIVED and a known KILLED verdict
tests the property that actually matters: that the harness can tell the two
apart.
