# shellcheck shell=bash
set -euo pipefail

smoke_root="$(mktemp -d "${TMPDIR:-/tmp}/bond-exchange-demo-smoke.XXXXXX")"
demo_log="$smoke_root/demo.log"
runtime_marker="$smoke_root/postgres-runtime"
rest_address="127.0.0.1:18080"
grpc_address="127.0.0.1:19090"
demo_pid=""

# Installed as a signal trap below.
# shellcheck disable=SC2329
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "$demo_pid" ]] && kill -0 "$demo_pid" 2>/dev/null; then
    kill -TERM "$demo_pid" 2>/dev/null || true
    wait "$demo_pid" 2>/dev/null || true
  fi
  if (( status != 0 )); then
    cat "$demo_log" >&2 || true
  fi
  rm -rf -- "$smoke_root"
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

healthy=0
for _ in {1..150}; do
  if curl --fail --silent "http://$rest_address/healthz" >"$smoke_root/health.json"; then
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
jq -e '.status == "ok"' "$smoke_root/health.json" >/dev/null

grpcurl -plaintext \
  -d '{}' \
  "$grpc_address" \
  bondexchange.v1.BondExchangeService/CheckHealth \
  >"$smoke_root/grpc-health.json"
jq -e '.status == "ok"' "$smoke_root/grpc-health.json" >/dev/null

curl --fail --silent \
  "http://$rest_address/active-bond-series" \
  >"$smoke_root/series.json"
jq -e '.bond_series == ["DEMO2026", "DEMO2027"]' "$smoke_root/series.json" >/dev/null

curl --fail --silent \
  "http://$rest_address/active-offers?bond=DEMO2026" \
  >"$smoke_root/offers.json"
jq -e '.offers | length == 2' "$smoke_root/offers.json" >/dev/null

create_status="$(curl --silent --output "$smoke_root/create.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --data '{"id":"demo-smoke-offer","seller_id":"demo-seller","bond_series":"DEMO2026","price":"97.125","currency_code":"USD"}' \
  "http://$rest_address/sale-offers")"
if [[ "$create_status" != 201 ]]; then
  echo "create sale offer returned HTTP $create_status" >&2
  exit 1
fi
jq -e '.offer.id == "demo-smoke-offer"' "$smoke_root/create.json" >/dev/null

buy_status="$(curl --silent --output "$smoke_root/buy.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --data '{"buyer_id":"demo-buyer","sale_offer_id":"demo-smoke-offer"}' \
  "http://$rest_address/buys")"
if [[ "$buy_status" != 201 ]]; then
  echo "buy returned HTTP $buy_status" >&2
  exit 1
fi
jq -e '.offer.id == "demo-smoke-offer"' "$smoke_root/buy.json" >/dev/null

kill -TERM "$demo_pid"
set +e
wait "$demo_pid"
shutdown_status=$?
set -e
demo_pid=""
if (( shutdown_status != 0 && shutdown_status != 143 )); then
  echo "demo exited with unexpected status $shutdown_status during shutdown" >&2
  exit 1
fi

if curl --fail --silent "http://$rest_address/healthz" >/dev/null 2>&1; then
  echo "demo REST listener remained available after shutdown" >&2
  exit 1
fi
asserted_runtime_root="$(cat "$runtime_marker")"
if [[ -e "$asserted_runtime_root" ]]; then
  echo "demo PostgreSQL runtime remained after shutdown: $asserted_runtime_root" >&2
  exit 1
fi

echo "Demo smoke check passed"
