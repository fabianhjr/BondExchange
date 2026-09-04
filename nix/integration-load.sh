# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
base_url="$BOND_EXCHANGE_TEST_BASE_URL"
private_key="$BOND_EXCHANGE_TEST_PRIVATE_JWK"
load_targets="$BOND_EXCHANGE_TEST_LOAD_TARGETS"
demo_auth="$BOND_EXCHANGE_TEST_DEMO_AUTH"
count="${BOND_EXCHANGE_LOAD_COUNT:-1000}"
rate="${BOND_EXCHANGE_LOAD_RATE:-50}"
workers="${BOND_EXCHANGE_LOAD_WORKERS:-20}"
artifact_root="$project_root/.artifacts/integration-load"
mkdir -p "$artifact_root"

if [[ ! "$count" =~ ^[1-9][0-9]*$ || ! "$rate" =~ ^[1-9][0-9]*$ || ! "$workers" =~ ^[1-9][0-9]*$ ]]; then
  echo "BOND_EXCHANGE_LOAD_COUNT, BOND_EXCHANGE_LOAD_RATE, and BOND_EXCHANGE_LOAD_WORKERS must be positive integers" >&2
  exit 64
fi
if (( count % rate != 0 )); then
  echo "BOND_EXCHANGE_LOAD_COUNT must be divisible by BOND_EXCHANGE_LOAD_RATE" >&2
  exit 64
fi
duration="$((count / rate))s"
if (( count / rate > 90 )); then
  echo "each load phase must finish within 90 seconds so its request-bound assertions remain valid" >&2
  exit 64
fi

report_attack() {
  local name="$1"
  vegeta report -type=json "$artifact_root/$name.bin" >"$artifact_root/$name.json"
  vegeta report "$artifact_root/$name.bin" >"$artifact_root/$name.txt"
  cat "$artifact_root/$name.txt"
}

run_attack() {
  local scenario="$1"
  local request_count="$2"
  local prefix="$3"
  local name="$4"
  local attack_duration="$duration"
  local attack_rate="$rate"
  local generated_count=$((request_count + rate + workers))
  if (( request_count == 1 )); then
    attack_duration="1s"
    attack_rate=1
    generated_count=2
  fi
  set +o pipefail
  "$load_targets" "$private_key" "$base_url" "$scenario" "$generated_count" "$prefix" \
    | vegeta attack \
      -format=json \
      -lazy \
      -duration="$attack_duration" \
      -rate="$attack_rate/s" \
      -workers="$workers" \
      -max-workers="$workers" \
      -timeout=10s \
      -output="$artifact_root/$name.bin"
  attack_status="${PIPESTATUS[1]}"
  set -o pipefail
  if (( attack_status != 0 )); then
    return "$attack_status"
  fi
  report_attack "$name"
}

require_status() {
  local name="$1"
  local request_count="$2"
  local status="$3"
  jq --exit-status \
    --argjson request_count "$request_count" \
    --arg status "$status" '
      .requests == $request_count
      and ((.status_codes[$status] // 0) == $request_count)
      and (.errors | length == 0)
    ' "$artifact_root/$name.json" >/dev/null
}

echo "Creating $count distinct offers at $rate requests/s with at most $workers workers"
run_attack create "$count" load-offer create
require_status create "$count" 201

echo "Listing the populated DEMO2026 offer book"
run_attack list-offers "$count" list-offers list-offers
require_status list-offers "$count" 200

echo "Listing active bond series"
run_attack list-series "$count" list-series list-series
require_status list-series "$count" 200

echo "Buying each generated offer exactly once"
run_attack buy "$count" load-offer buy
require_status buy "$count" 201

echo "Creating one offer for a contended buy"
run_attack create 1 contended-offer contended-setup
require_status contended-setup 1 201

echo "Sending $count independent buy operations for one offer"
run_attack contended-buy "$count" contended-offer contended-buy
jq --exit-status \
  --argjson request_count "$count" '
    .requests == $request_count
    and ((.status_codes["201"] // 0) == 1)
    and ((.status_codes["404"] // 0) == ($request_count - 1))
  ' "$artifact_root/contended-buy.json" >/dev/null

offers_token="$($demo_auth token "$private_key" demo-buyer offers.list - '{"bond":"DEMO2026"}')"
curl --fail --silent \
  --header "Authorization: Bearer $offers_token" \
  "$base_url/active-offers?bond=DEMO2026" \
  >"$BOND_EXCHANGE_TEST_RUNTIME_ROOT/load-final-offers.json-seq"
jq --seq --slurp --exit-status '
  ([.[] | select(.offer) | .offer.id] == ["demo-offer-1", "demo-offer-2"])
  and (.[-1].complete.offer_count == "2")
' "$BOND_EXCHANGE_TEST_RUNTIME_ROOT/load-final-offers.json-seq" >/dev/null

echo "Integration load test passed; reports are in $artifact_root"
