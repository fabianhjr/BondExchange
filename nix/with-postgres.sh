# shellcheck shell=bash
set -euo pipefail

if (( $# == 0 )); then
  echo "usage: bond-exchange-with-postgres COMMAND [ARGUMENT ...]" >&2
  exit 64
fi

runtime_base="${TMPDIR:-/tmp}"
database_name="${BOND_EXCHANGE_DATABASE_NAME:-bond_exchange_test}"
if [[ ! "$database_name" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  echo "invalid PostgreSQL database name: $database_name" >&2
  exit 64
fi

runtime_root="$(mktemp -d "${runtime_base%/}/bond-exchange-postgres.XXXXXX")"
data_dir="$runtime_root/data"
socket_dir="$runtime_root/socket"
log_file="$runtime_root/postgres.log"
postgres_started=0
child_pid=""

# Invoked from the EXIT trap through cleanup.
# shellcheck disable=SC2329
remove_runtime_root() {
  case "$runtime_root" in
    "${runtime_base%/}"/bond-exchange-postgres.*)
      rm -rf -- "$runtime_root"
      ;;
    *)
      echo "refusing to remove unexpected PostgreSQL runtime path: $runtime_root" >&2
      return 1
      ;;
  esac
}

# Invoked from the EXIT trap through cleanup.
# shellcheck disable=SC2329
stop_child() {
  if [[ -z "$child_pid" ]] || ! kill -0 "$child_pid" 2>/dev/null; then
    return
  fi

  kill -TERM -- "-$child_pid" 2>/dev/null || kill -TERM "$child_pid" 2>/dev/null || true
  for _ in {1..50}; do
    if ! kill -0 "$child_pid" 2>/dev/null; then
      wait "$child_pid" 2>/dev/null || true
      return
    fi
    sleep 0.1
  done
  kill -KILL -- "-$child_pid" 2>/dev/null || kill -KILL "$child_pid" 2>/dev/null || true
  wait "$child_pid" 2>/dev/null || true
}

# Installed as a signal trap below.
# shellcheck disable=SC2329
cleanup() {
  local status=$?
  trap - EXIT INT TERM HUP
  stop_child
  if (( postgres_started == 1 )); then
    if pg_ctl -D "$data_dir" status >/dev/null 2>&1; then
      if pg_ctl -D "$data_dir" -m fast -w stop >/dev/null 2>&1; then
        postgres_started=0
      else
        echo "failed to stop temporary PostgreSQL; runtime retained at $runtime_root" >&2
        cat "$log_file" >&2 || true
        status=1
      fi
    else
      postgres_started=0
    fi
  fi
  if (( postgres_started == 0 )); then
    remove_runtime_root || status=1
  fi
  exit "$status"
}

# Installed as a signal trap below.
# shellcheck disable=SC2329
forward_signal() {
  local signal="$1"
  if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
    kill -"$signal" -- "-$child_pid" 2>/dev/null || kill -"$signal" "$child_pid" 2>/dev/null || true
  fi
}

trap cleanup EXIT
trap 'forward_signal INT' INT
trap 'forward_signal TERM' TERM
trap 'forward_signal HUP' HUP

mkdir -p "$socket_dir"
chmod 700 "$runtime_root" "$socket_dir"
initdb \
  -D "$data_dir" \
  --auth=trust \
  --encoding=UTF8 \
  --no-locale \
  --username=postgres \
  >"$log_file" 2>&1

{
  echo "listen_addresses = ''"
  echo "unix_socket_directories = '$socket_dir'"
} >>"$data_dir/postgresql.conf"

pg_ctl -D "$data_dir" -l "$log_file" -w start >/dev/null
postgres_started=1

export PGDATA="$data_dir"
export PGHOST="$socket_dir"
export PGPORT=5432
export PGUSER=postgres
export PGDATABASE="$database_name"
export BOND_EXCHANGE_RUNTIME_ROOT="$runtime_root"
export DATABASE_URL="postgresql:///$database_name?host=$socket_dir"
export BOND_EXCHANGE_TEST_DATABASE_URL="$DATABASE_URL"
export DBMATE_MIGRATIONS_DIR="${DEVENV_ROOT:-$PWD}/db/migrations"
export DBMATE_NO_DUMP_SCHEMA=true
export DBMATE_STRICT=true

if [[ -n "${BOND_EXCHANGE_RUNTIME_ROOT_FILE:-}" ]]; then
  printf '%s\n' "$runtime_root" >"$BOND_EXCHANGE_RUNTIME_ROOT_FILE"
fi

createdb "$database_name"
dbmate --wait up

setsid "$@" &
child_pid=$!
child_status=0
set +e
while kill -0 "$child_pid" 2>/dev/null; do
  wait "$child_pid"
  child_status=$?
done
wait "$child_pid" 2>/dev/null
wait_status=$?
if (( wait_status != 127 )); then
  child_status=$wait_status
fi
set -e
child_pid=""
exit "$child_status"
