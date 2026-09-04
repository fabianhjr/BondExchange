# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
base_url="$BOND_EXCHANGE_TEST_BASE_URL"
private_key="$BOND_EXCHANGE_TEST_PRIVATE_JWK"
demo_auth="$BOND_EXCHANGE_TEST_DEMO_AUTH"
test_root="$BOND_EXCHANGE_TEST_RUNTIME_ROOT"

issue_token() {
  "$demo_auth" token "$private_key" "$1" "$2" "$3" "$4"
}

series_token="$(issue_token demo-buyer bond-series.list - '{}')"
offers_token="$(issue_token demo-buyer offers.list - '{"bond":"DEMO2026"}')"
create_key='00000000-0000-4000-8000-000000000001'
create_request='{"bond_series":"demo2026","price":"97.125","currency_code":"USD"}'
create_token="$(issue_token demo-seller offers.create "$create_key" "$create_request")"

hurl --jobs 1 --output "$test_root/create.json" \
  --variable "base_url=$base_url" \
  --secret "create_token=$create_token" \
  "$project_root/tests/integration/http/sale-offer-create.hurl"
offer_id="$(jq --raw-output '.offer.id' "$test_root/create.json")"
if [[ ! "$offer_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]; then
  echo "create did not return a canonical UUIDv7 offer ID" >&2
  exit 1
fi

buy_request="{\"sale_offer_id\":\"$offer_id\"}"
buy_key='00000000-0000-4000-8000-000000000002'
second_buy_key='00000000-0000-4000-8000-000000000003'
buy_token="$(issue_token demo-buyer purchases.buy "$buy_key" "$buy_request")"
buy_retry_token="$(issue_token demo-buyer purchases.buy "$buy_key" "$buy_request")"
second_buy_token="$(issue_token demo-buyer purchases.buy "$second_buy_key" "$buy_request")"

hurl --test --jobs 1 --no-output \
  --variable "base_url=$base_url" \
  --secret "series_token=$series_token" \
  --secret "offers_token=$offers_token" \
  --variable "offer_id=$offer_id" \
  --secret "buy_token=$buy_token" \
  --secret "buy_retry_token=$buy_retry_token" \
  --secret "second_buy_token=$second_buy_token" \
  "$project_root/tests/integration/http/sale-offer-lifecycle.hurl"

curl --fail --silent \
  --header "Authorization: Bearer $offers_token" \
  "$base_url/active-offers?bond=DEMO2026" \
  >"$test_root/final-offers.json-seq"
jq --seq --slurp --exit-status '
  ([.[] | select(.offer) | .offer.id] == ["01991a20-0000-7000-8000-000000000101", "01991a20-0000-7000-8000-000000000102"])
  and (.[-1].complete.offer_count == "2")
' "$test_root/final-offers.json-seq" >/dev/null

echo "HTTP integration test passed"
