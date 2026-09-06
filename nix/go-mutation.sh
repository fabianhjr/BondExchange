# shellcheck shell=bash
set -euo pipefail

# Two mutation gates share this script.
#
# `go:mutation` runs in diff mode. Gremlins mutates only the lines a branch
# changed, which keeps the gate inside a pull request's time budget. Every
# other mutant is reported as SKIPPED and counts toward nothing.
#
# `go:mutation-full` runs the whole module on a schedule. Diff mode never
# re-tests a line that an earlier change already introduced, so a mutant that
# a later refactor stops killing would otherwise stay invisible.
#
# Gremlins gathers the diff by running `git diff --merge-base <ref>` from the
# directory it was invoked in, and matches the result against file names
# relative to the Go module root. This module is not at the repository root, so
# git's repository-relative paths would match nothing and every mutant would be
# skipped. `diff.relative` makes git emit module-relative paths and drop
# changes outside the module. It is set through the environment so that no
# contributor's repository configuration is modified.
#
# The efficacy threshold is enforced here rather than by Gremlins, for three
# reasons. Gremlins holds one threshold and these gates need two. In diff mode
# a change that touches no mutable Go line produces zero tested mutants, which
# Gremlins reports as 0 percent efficacy and fails, when the honest answer is
# that there was nothing to score. And Gremlins excludes a timed-out mutant
# from both sides of its ratio; see the accounting below.

project_root="${DEVENV_ROOT:-$PWD}"
mode="${BOND_EXCHANGE_MUTATION_MODE:-diff}"
diff_ref="${BOND_EXCHANGE_MUTATION_DIFF_REF:-main}"

case "$mode" in
  diff)
    threshold="${BOND_EXCHANGE_MUTATION_THRESHOLD:-95}"
    report="$project_root/.artifacts/mutation-report.json"
    # A diff run scores the lines one branch touched and finishes in seconds
    # to minutes. Anything approaching this bound is a hang, not a big change.
    run_timeout="${BOND_EXCHANGE_MUTATION_TIMEOUT:-20m}"
    ;;
  full)
    threshold="${BOND_EXCHANGE_MUTATION_THRESHOLD:-90}"
    report="$project_root/.artifacts/mutation-report-full.json"
    run_timeout="${BOND_EXCHANGE_MUTATION_TIMEOUT:-240m}"
    ;;
  *)
    echo "BOND_EXCHANGE_MUTATION_MODE must be 'diff' or 'full', got '$mode'" >&2
    exit 64
    ;;
esac

# Share of tested mutants that may time out before the run is considered
# unreliable rather than merely slow. A whole-module run measured 22 percent,
# because `internal/postgres` and `internal/exchangerates` retry in loops whose
# exit conditions a mutation can remove. See the accounting below.
timeout_share="${BOND_EXCHANGE_MUTATION_TIMEOUT_SHARE:-30}"

mkdir -p "$project_root/.artifacts"
# A run that dies before writing a report must not be scored against the
# previous run's numbers.
rm -f "$report"
cd "$project_root/application"

# The thresholds are disabled on the command line as well as in the
# configuration file, so that re-adding one there cannot make Gremlins fail a
# run this script would pass, or pass one it would fail.
arguments=(--output "$report" --threshold-efficacy 0 --threshold-mcover 0)

if [[ "$mode" == "diff" ]]; then
  if ! git rev-parse --verify --quiet "$diff_ref^{commit}" > /dev/null; then
    {
      echo "The mutation diff reference '$diff_ref' does not resolve to a commit."
      echo "Set BOND_EXCHANGE_MUTATION_DIFF_REF to the branch or commit this"
      echo "change is measured against, and make sure it has been fetched."
    } >&2
    exit 1
  fi
  export GIT_CONFIG_COUNT=1
  export GIT_CONFIG_KEY_0=diff.relative
  export GIT_CONFIG_VALUE_0=true
  # Every unchanged mutant in the module is reported as SKIPPED, which is a
  # thousand lines of noise around the handful of verdicts that matter.
  arguments+=(--diff "$diff_ref" --output-statuses lctkvr)
  echo "Mutating lines changed since the merge base with $diff_ref"
else
  echo "Mutating every covered line in the module"
fi

# Gremlins runs the whole suite once per mutant against one database, and reads
# a non-zero exit as KILLED. A test that passes on an empty database and fails
# on the rows a previous run left behind therefore kills every mutant after the
# first, whatever the mutation was, and the run reports a perfect score. Prove
# the unmutated suite survives a second run before trusting any verdict from
# the first.
#
# This also settles the per-mutant time budget, which Gremlins derives from the
# elapsed time of its own coverage run. In a clean checkout that run is the
# first compile, so the budget would be a multiple of a cold build rather than
# of a test run, and a mutant that hangs would cost minutes instead of seconds.
# The compile happens here instead, and the work is not extra: the coverage run
# would have paid for it.
echo "Checking that the test suite is repeatable against one database"
if ! go test -cover -count=1 ./... > "$project_root/.artifacts/mutation-baseline-1.log" 2>&1; then
  {
    echo "The test suite does not pass before any mutation is applied."
    cat "$project_root/.artifacts/mutation-baseline-1.log"
  } >&2
  exit 1
fi
if ! go test -cover -count=1 ./... > "$project_root/.artifacts/mutation-baseline-2.log" 2>&1; then
  {
    echo "The test suite passes once and fails when run again against the same"
    echo "database. Gremlins runs it once per mutant and reads any failure as a"
    echo "killed mutant, so every verdict after the first would be manufactured"
    echo "by this failure rather than by the mutation."
    echo
    echo "Make the failing test independent of rows an earlier run left behind."
    cat "$project_root/.artifacts/mutation-baseline-2.log"
  } >&2
  exit 1
fi

# The coefficient bounds one mutant; nothing in Gremlins bounds the run. A
# mutant that hangs a test costs its whole budget, and this module has many
# that do, so `timeout` bounds the run as a whole. The harness that provides
# PostgreSQL kills the process group afterwards, so no `go test` child outlives
# it.
echo "Bounding the run at $run_timeout"
status=0
timeout --kill-after=60s "$run_timeout" gremlins unleash "${arguments[@]}" || status=$?

if ((status == 124 || status == 137)); then
  {
    echo "The mutation run did not finish within $run_timeout and was stopped."
    echo "Raise BOND_EXCHANGE_MUTATION_TIMEOUT only after checking that the run"
    echo "is slow rather than stuck: a mutant that hangs a test the coverage"
    echo "run never reached is not bounded by unleash.timeout-coefficient."
  } >&2
  exit 1
fi

if ((status != 0)); then
  echo "gremlins exited with status $status" >&2
  exit "$status"
fi

if [[ ! -f "$report" ]]; then
  echo "gremlins wrote no report to $report" >&2
  exit 1
fi

# The report is a single-line JSON object with integer summary fields, and one
# entry per mutant carrying its status. Every reader below tolerates a missing
# match, because the script must report what it could not read rather than
# exiting on a failed grep.
count() {
  grep -o "\"$1\":[0-9]*" "$report" | head -n 1 | cut -d: -f2 || true
}

# The whole report is one line, so occurrences must be counted rather than
# lines matched.
occurrences() {
  grep -o "$1" "$report" | wc -l || true
}

killed="$(count mutants_killed)"
lived="$(count mutants_lived)"
timed_out="$(occurrences '"status":"TIMED OUT"')"
[[ -n "$timed_out" ]] || timed_out=0

if [[ -z "$killed" || -z "$lived" ]]; then
  echo "could not read mutant counts from $report" >&2
  exit 1
fi

# A mutant that stops the suite from terminating has been detected as surely as
# one that fails an assertion, so it is counted as killed. Gremlins instead
# drops it from both sides of its ratio, which lets a run quietly score a
# smaller population than it analyzed. Counting it here closes that hole, but
# it opens another: if the per-mutant budget were far too small, every mutant
# would "time out" and the score would be a perfect fiction. The share check
# below bounds that. It is a measurement-quality check, not a quality gate on
# the code, so it is deliberately loose.
detected=$((killed + timed_out))
tested=$((detected + lived))

if ((tested == 0)); then
  if [[ "$mode" == "diff" ]]; then
    echo "No mutable covered line changed since the merge base with $diff_ref; nothing to score."
    exit 0
  fi
  echo "A full mutation run produced no tested mutants, which cannot be right." >&2
  exit 1
fi

if ((timed_out > 0)); then
  printf '%d of %d tested mutants timed out and are counted as killed.\n' \
    "$timed_out" "$tested"
fi

if ((timed_out * 100 > timeout_share * tested)); then
  {
    printf 'Timed-out mutants are %d of %d tested, above the %d%% share this gate trusts.\n' \
      "$timed_out" "$tested" "$timeout_share"
    echo "Each mutant is allowed unleash.timeout-coefficient times the elapsed"
    echo "time of the coverage run, which is measured with a warm build cache"
    echo "and excludes recompiling the mutated package. Too many timeouts means"
    echo "that budget no longer separates a non-terminating mutant from a slow"
    echo "one, so the efficacy above is not a measurement of the test suite."
  } >&2
  exit 1
fi

# Integer arithmetic keeps the comparison exact: the run passes when
# detected * 100 >= threshold * tested.
if ((detected * 100 < threshold * tested)); then
  printf 'Mutation efficacy %d/%d is below the %d%% threshold for the %s gate.\n' \
    "$detected" "$tested" "$threshold" "$mode" >&2
  echo "Surviving mutants are listed above and in $report." >&2
  exit 1
fi

printf 'Mutation efficacy %d/%d meets the %d%% threshold for the %s gate.\n' \
  "$detected" "$tested" "$threshold" "$mode"
