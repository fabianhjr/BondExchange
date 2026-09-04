# shellcheck shell=bash
set -euo pipefail

test_root="$BOND_EXCHANGE_TEST_RUNTIME_ROOT"
base_url="$BOND_EXCHANGE_TEST_BASE_URL"
grpc_address="$BOND_EXCHANGE_TEST_GRPC_ADDRESS"
private_key="$BOND_EXCHANGE_TEST_PRIVATE_JWK"
demo_auth="$BOND_EXCHANGE_TEST_DEMO_AUTH"
project_root="${DEVENV_ROOT:-$PWD}"

issue_token() {
  "$demo_auth" token "$private_key" "$1" "$2" "$3" "$4"
}

health_token="$(issue_token demo-buyer health.read - '{}')"
if grpcurl -plaintext "$grpc_address" list >"$test_root/reflection.txt" 2>&1; then
  echo "demo server unexpectedly exposes gRPC reflection" >&2
  exit 1
fi

grpcurl -plaintext \
  -protoset "$project_root/api/descriptors/bondexchange.protoset" \
  -H "Authorization: Bearer $health_token" \
  -d '{}' \
  "$grpc_address" \
  bondexchange.v1.BondExchangeService/CheckHealth \
  >"$test_root/grpc-health.json"
jq -e '.status == "ok"' "$test_root/grpc-health.json" >/dev/null

series_token="$(issue_token demo-buyer bond-series.list - '{}')"
curl --fail --silent \
  --header "Authorization: Bearer $series_token" \
  "$base_url/active-bond-series" \
  >"$test_root/series.json"
jq -e '.bond_series == ["DEMO2026", "DEMO2027"]' "$test_root/series.json" >/dev/null

offers_token="$(issue_token demo-buyer offers.list - '{"bond":"DEMO2026"}')"
curl --fail --silent \
  --header "Authorization: Bearer $offers_token" \
  "$base_url/active-offers?bond=DEMO2026" \
  | tr '\036' '\n' >"$test_root/offers.json"
jq -se '([.[] | select(.offer)] | length == 2) and (.[-1].complete.offer_count == "2")' "$test_root/offers.json" >/dev/null

create_key="00000000-0000-4000-8000-000000000011"
create_request='{"bond_series":"DEMO2026","price":"97.125","currency_code":"USD"}'
create_token="$(issue_token demo-seller offers.create "$create_key" "$create_request")"
create_status="$(curl --silent --output "$test_root/create.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --header "Authorization: Bearer $create_token" \
  --header "Idempotency-Key: $create_key" \
  --data "$create_request" \
  "$base_url/sale-offers")"
if [[ "$create_status" != 201 ]]; then
  echo "create sale offer returned HTTP $create_status" >&2
  exit 1
fi
offer_id="$(jq --raw-output '.offer.id' "$test_root/create.json")"
if [[ ! "$offer_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]; then
  echo "create did not return a canonical UUIDv7 offer ID" >&2
  exit 1
fi

buy_key="00000000-0000-4000-8000-000000000012"
buy_request="{\"sale_offer_id\":\"$offer_id\"}"
buy_token="$(issue_token demo-buyer purchases.buy "$buy_key" "$buy_request")"
buy_status="$(curl --silent --output "$test_root/buy.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --header "Authorization: Bearer $buy_token" \
  --header "Idempotency-Key: $buy_key" \
  --data "$buy_request" \
  "$base_url/buys")"
if [[ "$buy_status" != 201 ]]; then
  echo "buy returned HTTP $buy_status" >&2
  exit 1
fi
jq --exit-status --arg offer_id "$offer_id" '.offer.id == $offer_id' "$test_root/buy.json" >/dev/null

publish_key="00000000-0000-4000-8000-000000000013"
publish_request='{}'
publish_token="$(issue_token demo-buyer events.publish-pending "$publish_key" "$publish_request")"
publish_status="$(curl --silent --output "$test_root/publish.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --header "Authorization: Bearer $publish_token" \
  --header "Idempotency-Key: $publish_key" \
  --data "$publish_request" \
  "$base_url/event-publications:publish-pending")"
if [[ "$publish_status" != 400 ]]; then
  echo "manual event publication without a configured publisher returned HTTP $publish_status" >&2
  exit 1
fi
jq -e '.error == "no event publishers are configured"' "$test_root/publish.json" >/dev/null

echo "Demo smoke check passed"
