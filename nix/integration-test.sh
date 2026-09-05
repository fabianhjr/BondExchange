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
quote_key='00000000-0000-4000-8000-000000000009'
quote_request='{"bond_series":"demo2026","price":"97.125","currency_code":"USD"}'
quote_token="$(issue_token demo-seller offers.quote "$quote_key" "$quote_request")"
quote_status="$(curl --silent --output "$test_root/quote.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --header "Authorization: Bearer $quote_token" \
  --header "Idempotency-Key: $quote_key" \
  --data "$quote_request" \
  "$base_url/sale-offer-quotes")"
if [[ "$quote_status" != 201 ]]; then
  echo "quote sale offer returned HTTP $quote_status" >&2
  exit 1
fi
quote_id="$(jq --raw-output '.quote_id' "$test_root/quote.json")"
jq --exit-status '
  .submitted_price == "97.125"
  and .submitted_currency_code == "USD"
  and .mxn_price == "1651.125"
  and .currency_code == "MXN"
  and .rate == "17"
  and .rate_series == "SF43718"
' "$test_root/quote.json" >/dev/null
create_key='00000000-0000-4000-8000-000000000001'
create_request="{\"bond_series\":\"demo2026\",\"price\":\"97.125\",\"currency_code\":\"USD\",\"conversion_quote_id\":\"$quote_id\"}"
create_token="$(issue_token demo-seller offers.create "$create_key" "$create_request")"

hurl --jobs 1 --output "$test_root/create.json" \
  --variable "base_url=$base_url" \
  --variable "quote_id=$quote_id" \
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

rate_limit_token="$(issue_token demo-rate-limited bond-series.list - '{}')"
psql "$BOND_EXCHANGE_TEST_DATABASE_URL" --no-psqlrc --set ON_ERROR_STOP=1 \
  --set principal_uuid='01991a20-0000-7000-8000-000000000003' <<'SQL'
INSERT INTO bond_exchange.principal_rate_limits
  (principal_uuid, window_started_at, request_count)
VALUES (
  :'principal_uuid',
  date_trunc('minute', statement_timestamp()),
  100
)
ON CONFLICT (principal_uuid) DO UPDATE
SET window_started_at = EXCLUDED.window_started_at,
    request_count = EXCLUDED.request_count,
    updated_at = statement_timestamp();
SQL
rate_limit_status="$(curl --silent --dump-header "$test_root/rate-limit.headers" \
  --output "$test_root/rate-limit.json" --write-out '%{http_code}' \
  --header "Authorization: Bearer $rate_limit_token" \
  "$base_url/active-bond-series")"
if [[ "$rate_limit_status" != 429 ]]; then
  echo "rate-limited request returned HTTP $rate_limit_status" >&2
  exit 1
fi
if ! grep -Eiq '^Retry-After: ([1-9]|[1-5][0-9]|60)[[:space:]]*$' "$test_root/rate-limit.headers"; then
  echo "rate-limited response did not carry a bounded Retry-After header" >&2
  exit 1
fi
jq --exit-status '.error == "request rate limit exceeded"' "$test_root/rate-limit.json" >/dev/null

psql "$BOND_EXCHANGE_TEST_DATABASE_URL" --no-psqlrc --set ON_ERROR_STOP=1 \
  --set principal_uuid='01991a20-0000-7000-8000-000000000003' <<'SQL'
UPDATE bond_exchange.principal_rate_limits
SET window_started_at = date_trunc('minute', statement_timestamp()) - interval '1 minute',
    updated_at = statement_timestamp()
WHERE principal_uuid = :'principal_uuid';
SQL
curl --fail --silent \
  --header "Authorization: Bearer $rate_limit_token" \
  "$base_url/active-bond-series" >"$test_root/rate-limit-reset.json"
jq --exit-status '.bond_series | type == "array"' "$test_root/rate-limit-reset.json" >/dev/null

echo "HTTP integration test passed"
