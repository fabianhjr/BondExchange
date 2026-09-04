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
create_request='{"id":"integration-offer-1","bond_series":"demo2026","price":"97.125","currency_code":"USD"}'
create_token="$(issue_token demo-seller offers.create integration-create-0001 "$create_request")"
buy_request='{"sale_offer_id":"integration-offer-1"}'
buy_token="$(issue_token demo-buyer purchases.buy integration-buy-000001 "$buy_request")"
buy_retry_token="$(issue_token demo-buyer purchases.buy integration-buy-000001 "$buy_request")"
second_buy_token="$(issue_token demo-buyer purchases.buy integration-buy-000002 "$buy_request")"

hurl --test --jobs 1 --no-output \
  --variable "base_url=$base_url" \
  --secret "series_token=$series_token" \
  --secret "offers_token=$offers_token" \
  --secret "create_token=$create_token" \
  --secret "buy_token=$buy_token" \
  --secret "buy_retry_token=$buy_retry_token" \
  --secret "second_buy_token=$second_buy_token" \
  "$project_root/tests/integration/http/sale-offer-lifecycle.hurl"

curl --fail --silent \
  --header "Authorization: Bearer $offers_token" \
  "$base_url/active-offers?bond=DEMO2026" \
  >"$test_root/final-offers.json-seq"
jq --seq --slurp --exit-status '
  ([.[] | select(.offer) | .offer.id] == ["demo-offer-1", "demo-offer-2"])
  and (.[-1].complete.offer_count == "2")
' "$test_root/final-offers.json-seq" >/dev/null

echo "HTTP integration test passed"
