# shellcheck shell=bash
set -euo pipefail

if (( $# == 0 )); then
  echo "usage: bond-exchange-with-demo-server COMMAND [ARGUMENT ...]" >&2
  exit 64
fi

lock_file="${TMPDIR:-/tmp}/bond-exchange-demo-server-${UID}.lock"
exec 9>"$lock_file"
flock 9

test_root="$(mktemp -d "${TMPDIR:-/tmp}/bond-exchange-demo-test.XXXXXX")"
demo_log="$test_root/demo.log"
runtime_marker="$test_root/postgres-runtime"
rest_address="${BOND_EXCHANGE_TEST_REST_ADDRESS:-127.0.0.1:18080}"
grpc_address="${BOND_EXCHANGE_TEST_GRPC_ADDRESS:-127.0.0.1:19090}"
demo_pid=""
asserted_runtime_root=""

# Installed as a signal trap below.
# shellcheck disable=SC2329
cleanup() {
  local status=$?
  local shutdown_status=0
  trap - EXIT INT TERM
  if [[ -n "$demo_pid" ]] && kill -0 "$demo_pid" 2>/dev/null; then
    kill -TERM "$demo_pid" 2>/dev/null || true
    set +e
    wait "$demo_pid"
    shutdown_status=$?
    set -e
    if (( shutdown_status != 0 && shutdown_status != 143 )); then
      echo "demo exited with unexpected status $shutdown_status during shutdown" >&2
      status=1
    fi
  fi
  if [[ -n "$asserted_runtime_root" && -e "$asserted_runtime_root" ]]; then
    echo "demo PostgreSQL runtime remained after shutdown: $asserted_runtime_root" >&2
    status=1
  fi
  if (( status != 0 )); then
    cat "$demo_log" >&2 || true
  fi
  rm -rf -- "$test_root"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

BOND_EXCHANGE_ADDRESS="$rest_address" \
BOND_EXCHANGE_GRPC_ADDRESS="$grpc_address" \
BOND_EXCHANGE_RUNTIME_ROOT_FILE="$runtime_marker" \
  bond-exchange-demo >"$demo_log" 2>&1 &
demo_pid=$!

project_root="${DEVENV_ROOT:-$PWD}"
go -C "$project_root/application" build -o "$test_root/demo-auth" ./cmd/demo-auth
go -C "$project_root/application" build -o "$test_root/load-targets" ./cmd/load-targets

auth_ready=0
for _ in {1..150}; do
  if [[ -s "$runtime_marker" ]]; then
    asserted_runtime_root="$(<"$runtime_marker")"
    if [[ -f "$asserted_runtime_root/auth/private.jwk" ]]; then
      auth_ready=1
      break
    fi
  fi
  if ! kill -0 "$demo_pid" 2>/dev/null; then
    break
  fi
  sleep 0.2
done
if (( auth_ready != 1 )); then
  echo "demo assertion issuer did not become ready" >&2
  exit 1
fi

private_key="$asserted_runtime_root/auth/private.jwk"
health_token="$("$test_root/demo-auth" token "$private_key" demo-buyer health.read - '{}')"
healthy=0
for _ in {1..150}; do
  if curl --fail --silent \
    --header "Authorization: Bearer $health_token" \
    "http://$rest_address/healthz" >"$test_root/health.json"; then
    healthy=1
    break
  fi
  if ! kill -0 "$demo_pid" 2>/dev/null; then
    break
  fi
  sleep 0.2
done
if (( healthy != 1 )); then
  echo "demo server did not become healthy" >&2
  exit 1
fi
jq -e '.status == "ok"' "$test_root/health.json" >/dev/null

export BOND_EXCHANGE_TEST_BASE_URL="http://$rest_address"
export BOND_EXCHANGE_TEST_GRPC_ADDRESS="$grpc_address"
export BOND_EXCHANGE_TEST_PRIVATE_JWK="$private_key"
export BOND_EXCHANGE_TEST_DEMO_AUTH="$test_root/demo-auth"
export BOND_EXCHANGE_TEST_LOAD_TARGETS="$test_root/load-targets"
export BOND_EXCHANGE_TEST_RUNTIME_ROOT="$test_root"

"$@"
