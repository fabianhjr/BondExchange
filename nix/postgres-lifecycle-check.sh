# shellcheck shell=bash
set -euo pipefail

check_root="$(mktemp -d "${TMPDIR:-/tmp}/bond-exchange-lifecycle-check.XXXXXX")"
trap 'rm -rf -- "$check_root"' EXIT

assert_removed() {
  local marker="$1"
  local runtime_root
  runtime_root="$(cat "$marker")"
  if [[ -e "$runtime_root" ]]; then
    echo "PostgreSQL runtime directory still exists: $runtime_root" >&2
    exit 1
  fi
}

success_marker="$check_root/success-runtime"
# The child shell must expand its PostgreSQL environment, not this parent shell.
# shellcheck disable=SC2016
BOND_EXCHANGE_RUNTIME_ROOT_FILE="$success_marker" \
  bond-exchange-with-postgres bash -c '
    test "$(psql -Atqc "SELECT to_regclass('"'"'bond_exchange.sale_offers'"'"') IS NOT NULL")" = t
  '
assert_removed "$success_marker"

failure_marker="$check_root/failure-runtime"
set +e
# The child shell must expand its PostgreSQL environment, not this parent shell.
# shellcheck disable=SC2016
BOND_EXCHANGE_RUNTIME_ROOT_FILE="$failure_marker" \
  bond-exchange-with-postgres bash -c '
    exit 23
  '
failure_status=$?
set -e
if (( failure_status != 23 )); then
  echo "expected failing child status 23, got $failure_status" >&2
  exit 1
fi
assert_removed "$failure_marker"

parallel_one_marker="$check_root/parallel-one-runtime"
parallel_two_marker="$check_root/parallel-two-runtime"
# The child shell must expand its PostgreSQL environment, not this parent shell.
# shellcheck disable=SC2016
BOND_EXCHANGE_RUNTIME_ROOT_FILE="$parallel_one_marker" \
  bond-exchange-with-postgres bash -c '
    psql -Atqc "SELECT 1" >/dev/null
    sleep 1
  ' &
parallel_one_pid=$!
# The child shell must expand its PostgreSQL environment, not this parent shell.
# shellcheck disable=SC2016
BOND_EXCHANGE_RUNTIME_ROOT_FILE="$parallel_two_marker" \
  bond-exchange-with-postgres bash -c '
    psql -Atqc "SELECT 1" >/dev/null
    sleep 1
  ' &
parallel_two_pid=$!
wait "$parallel_one_pid"
wait "$parallel_two_pid"

assert_removed "$parallel_one_marker"
assert_removed "$parallel_two_marker"
if [[ "$(cat "$parallel_one_marker")" == "$(cat "$parallel_two_marker")" ]]; then
  echo "parallel PostgreSQL harnesses shared a runtime directory" >&2
  exit 1
fi

echo "PostgreSQL lifecycle checks passed"
