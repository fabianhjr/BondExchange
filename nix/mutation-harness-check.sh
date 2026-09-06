# shellcheck shell=bash
set -euo pipefail

# Gremlins scores a mutant by the exit status of the test command it runs. Any
# defect that makes that command fail before it reaches a test — a malformed
# argument, a broken module copy, an unusable toolchain — is therefore scored
# as KILLED, and the run reports a vacuous 100 percent efficacy instead of
# failing. Gremlins 0.6.0 does exactly that when `unleash.test-cpu` is set,
# because it passes "-cpu N" as one argument; `go test` then reads it as a test
# binary flag, drops "./..." from package selection, and exits 1 with "no Go
# files".
#
# Diff mode fails in the opposite direction. Gremlins runs `git diff
# --merge-base <ref>` from the directory it was invoked in and matches the
# result against file names relative to the Go module root. Because this
# module lives under `application/`, git's repository-relative paths match
# nothing unless `diff.relative` is set, and every mutant is skipped. A gate
# that tests nothing reports no failures.
#
# This check runs the repository's own `application/.gremlins.yaml`, in both
# modes and through the same wrapper the gates use, against a throwaway
# repository whose layout mirrors this one and whose outcome is known: one
# mutation site that no test asserts, which must survive, and one that a test
# asserts, which must be killed. A harness that cannot produce both verdicts
# cannot measure efficacy, so the mutation gates must not run.

project_root="${DEVENV_ROOT:-$PWD}"
configuration="$project_root/application/.gremlins.yaml"

# The devenv task passes the same wrapper the mutation gates execute, so this
# check covers the wrapper as it ships rather than a copy of its source.
gate=("${BOND_EXCHANGE_MUTATION_GATE:-}")
if [[ -z "${gate[0]}" ]]; then
  gate=(bash "$project_root/nix/go-mutation.sh")
fi

for required in "$configuration" "${gate[-1]}"; do
  if [[ ! -f "$required" && ! -x "$required" ]]; then
    echo "missing $required" >&2
    exit 1
  fi
done

canary_root="$(mktemp -d "${TMPDIR:-/tmp}/bond-exchange-mutation-canary.XXXXXX")"
trap 'rm -rf -- "$canary_root"' EXIT INT TERM HUP

# The module sits below the repository root exactly as `application/` does, so
# that a diff-mode path mismatch reproduces here instead of only in the gate.
module="$canary_root/application"
mkdir -p "$module/internal/mutationcanary"
cp "$configuration" "$module/.gremlins.yaml"

cat > "$module/go.mod" <<'EOF'
module bondexchange.invalid/mutationcanary

go 1.21
EOF

cat > "$module/internal/mutationcanary/canary_test.go" <<'EOF'
package mutationcanary

import "testing"

func TestSurvivesIsExecutedWithoutAssertion(t *testing.T) {
	_ = Survives(2, 3)
}

func TestKilledIsAsserted(t *testing.T) {
	if got := Killed(2, 3); got != 6 {
		t.Fatalf("Killed(2, 3) = %d, want 6", got)
	}
}
EOF

# Gremlins derives a changed range from a diff fragment's leading context and
# added-line count, which assumes the added lines are contiguous. The two
# versions below therefore differ in exactly two lines, kept far enough apart
# that git reports them as separate fragments.
write_canary() {
	cat > "$module/internal/mutationcanary/canary.go" <<EOF
// Package mutationcanary provides two mutation sites with known verdicts.
package mutationcanary

// Survives is executed by a test that ignores its result, so mutating the
// operator must leave the test suite passing and the mutant LIVED.
func Survives(a, b int) int {
	return $1
}

// The functions are separated so that a change to each return statement
// produces its own diff fragment rather than one fragment spanning both.
//
//
//
//
// Killed is executed by a test that asserts its result, so mutating the
// operator must fail the test suite and the mutant must be KILLED.
func Killed(a, b int) int {
	return $2
}
EOF
}

# The baseline carries no mutable operator, so the only mutation sites a diff
# can report are the two the check introduces below.
write_canary "a" "b"

git -C "$canary_root" init --quiet
git -C "$canary_root" -c user.name=canary -c user.email=canary@invalid \
  -c commit.gpgsign=false add --all
git -C "$canary_root" -c user.name=canary -c user.email=canary@invalid \
  -c commit.gpgsign=false commit --quiet --message "canary baseline"

write_canary "a + b" "a * b"

failures=0

# The gate is invoked exactly as the devenv tasks invoke it, so the check
# covers the wrapper's own diff handling and not only Gremlins.
run_gate() {
  local mode="$1" label="$2"
  local log="$canary_root/$label.log"

  if ! env \
    DEVENV_ROOT="$canary_root" \
    BOND_EXCHANGE_MUTATION_MODE="$mode" \
    BOND_EXCHANGE_MUTATION_DIFF_REF=HEAD \
    BOND_EXCHANGE_MUTATION_THRESHOLD=0 \
    "${gate[@]}" > "$log" 2>&1; then
    {
      echo "The $label mutation gate failed against the canary repository."
      cat "$log"
    } >&2
    failures=$((failures + 1))

    return
  fi

  local report="$canary_root/.artifacts/mutation-report.json"
  if [[ "$mode" == full ]]; then
    report="$canary_root/.artifacts/mutation-report-full.json"
  fi

  local missing=()
  grep -q '"status":"LIVED"' "$report" || missing+=(LIVED)
  grep -q '"status":"KILLED"' "$report" || missing+=(KILLED)

  if ((${#missing[@]} > 0)); then
    {
      echo "The $label mutation gate cannot distinguish surviving from killed mutants."
      echo "Missing verdict(s): ${missing[*]}"
      echo
      echo "Every efficacy score this configuration produces is meaningless"
      echo "until the harness reproduces both verdicts on the canary module."
      echo "A missing LIVED verdict usually means the test command fails before"
      echo "it runs a test, so Gremlins scores every mutant as KILLED. Both"
      echo "verdicts missing in diff mode usually means the diff paths did not"
      echo "match the module-relative file names, so every mutant was skipped."
      echo
      cat "$log"
      cat "$report"
    } >&2
    failures=$((failures + 1))
  fi
}

run_gate full full
run_gate diff diff

if ((failures > 0)); then
  echo "$failures mutation harness problem(s) found" >&2
  exit 1
fi

echo "Mutation harness check passed: $configuration reports both LIVED and KILLED in full and diff mode"
