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

seller_principals="$(((count + 100) / 100))"
buyer_principals="$(((4 * count + 99) / 100))"
psql "$BOND_EXCHANGE_TEST_DATABASE_URL" --no-psqlrc --set ON_ERROR_STOP=1 \
  --set seller_count="$seller_principals" --set buyer_count="$buyer_principals" <<'SQL'
BEGIN;
CREATE TEMP TABLE load_principals (
  uuid_id uuid PRIMARY KEY,
  subject text NOT NULL UNIQUE
) ON COMMIT DROP;
INSERT INTO load_principals (uuid_id, subject)
SELECT uuidv7(), 'load-seller-' || index
FROM generate_series(1, :seller_count) AS index
UNION ALL
SELECT uuidv7(), 'load-buyer-' || index
FROM generate_series(1, :buyer_count) AS index;
INSERT INTO bond_exchange.users (uuid_id)
SELECT uuid_id FROM load_principals;
INSERT INTO bond_exchange.principals (uuid_id, issuer, subject, client_class)
SELECT uuid_id, 'https://demo-issuer.invalid', subject, 'automated'
FROM load_principals;
INSERT INTO bond_exchange.principal_role_grants
  (principal_uuid, role_uuid, reason)
SELECT principal.uuid_id, role.uuid_id, 'Disposable load-test access.'
FROM load_principals AS principal
CROSS JOIN bond_exchange.roles AS role
WHERE role.code = 'trader';
COMMIT;
SQL

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
  local principal_count="$buyer_principals"
  if [[ "$scenario" == create ]]; then
    principal_count="$seller_principals"
  fi
  local attack_duration="$duration"
  local attack_rate="$rate"
  local generated_count="$request_count"
  if (( request_count == 1 )); then
    attack_duration="1s"
    attack_rate=1
    generated_count=1
  fi
  set +o pipefail
  "$load_targets" "$private_key" "$base_url" "$scenario" "$generated_count" "$prefix" "$principal_count" \
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

offers_token="$($demo_auth token "$private_key" demo-buyer offers.list - '{"bond":"DEMO2026"}')"
curl --fail --silent \
  --header "Authorization: Bearer $offers_token" \
  "$base_url/active-offers?bond=DEMO2026" \
  >"$artifact_root/created-offers.json-seq"
jq --seq --raw-output 'select(.offer.price == "101.25") | .offer.id' \
  "$artifact_root/created-offers.json-seq" >"$artifact_root/created-offer-ids.txt"
if [[ "$(wc -l <"$artifact_root/created-offer-ids.txt")" -ne "$count" ]]; then
  echo "could not discover all generated UUIDv7 offer IDs" >&2
  exit 1
fi

echo "Listing the populated DEMO2026 offer book"
run_attack list-offers "$count" list-offers list-offers
require_status list-offers "$count" 200

echo "Listing active bond series"
run_attack list-series "$count" list-series list-series
require_status list-series "$count" 200

echo "Buying each generated offer exactly once"
run_attack buy "$count" "@$artifact_root/created-offer-ids.txt" buy
require_status buy "$count" 201

echo "Creating one offer for a contended buy"
run_attack create 1 contended-offer contended-setup
require_status contended-setup 1 201

curl --fail --silent \
  --header "Authorization: Bearer $offers_token" \
  "$base_url/active-offers?bond=DEMO2026" \
  >"$artifact_root/contended-offer.json-seq"
jq --seq --raw-output 'select(.offer.price == "101.25") | .offer.id' \
  "$artifact_root/contended-offer.json-seq" >"$artifact_root/contended-offer-id.txt"
if [[ "$(wc -l <"$artifact_root/contended-offer-id.txt")" -ne 1 ]]; then
  echo "could not discover the contended UUIDv7 offer ID" >&2
  exit 1
fi

echo "Sending $count independent buy operations for one offer"
run_attack contended-buy "$count" "@$artifact_root/contended-offer-id.txt" contended-buy
jq --exit-status \
  --argjson request_count "$count" '
    .requests == $request_count
    and ((.status_codes["201"] // 0) == 1)
    and ((.status_codes["404"] // 0) == ($request_count - 1))
  ' "$artifact_root/contended-buy.json" >/dev/null

curl --fail --silent \
  --header "Authorization: Bearer $offers_token" \
  "$base_url/active-offers?bond=DEMO2026" \
  >"$BOND_EXCHANGE_TEST_RUNTIME_ROOT/load-final-offers.json-seq"
jq --seq --slurp --exit-status '
  ([.[] | select(.offer) | .offer.id] == ["01991a20-0000-7000-8000-000000000101", "01991a20-0000-7000-8000-000000000102"])
  and (.[-1].complete.offer_count == "2")
' "$BOND_EXCHANGE_TEST_RUNTIME_ROOT/load-final-offers.json-seq" >/dev/null

echo "Integration load test passed; reports are in $artifact_root"
