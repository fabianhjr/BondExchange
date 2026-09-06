# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
base_url="$BOND_EXCHANGE_TEST_BASE_URL"
private_key="$BOND_EXCHANGE_TEST_PRIVATE_JWK"
demo_auth="$BOND_EXCHANGE_TEST_DEMO_AUTH"
test_root="$BOND_EXCHANGE_TEST_RUNTIME_ROOT"
scenarios="$project_root/tests/integration/http"

issue_token() {
  "$demo_auth" token "$private_key" "$1" "$2" "$3" "$4"
}

series_token="$(issue_token demo-buyer bond-series.list - '{}')"
offers_token="$(issue_token demo-buyer offers.list - '{"bond":"DEMO2026"}')"

# The seller prices a USD submission against the pinned SF43718 observation.
# Hurl owns the exchange and its assertions; the runner only needs the
# generated quote ID so it can bind the acceptance assertion to it.
quote_key='00000000-0000-4000-8000-000000000009'
quote_request='{"bond_series":"demo2026","price":"97.125","currency_code":"USD"}'
quote_token="$(issue_token demo-seller offers.quote "$quote_key" "$quote_request")"
quote_retry_token="$(issue_token demo-seller offers.quote "$quote_key" "$quote_request")"

hurl --jobs 1 --output "$test_root/quote.json" \
  --variable "base_url=$base_url" \
  --secret "quote_token=$quote_token" \
  --secret "quote_retry_token=$quote_retry_token" \
  "$scenarios/sale-offer-quote.hurl"
quote_id="$(jq --raw-output '.quote_id' "$test_root/quote.json")"
if [[ ! "$quote_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]; then
  echo "quote did not return a canonical UUIDv7 quote ID" >&2
  exit 1
fi

create_key='00000000-0000-4000-8000-000000000001'
create_request="{\"bond_series\":\"demo2026\",\"price\":\"97.125\",\"currency_code\":\"USD\",\"conversion_quote_id\":\"$quote_id\"}"
create_token="$(issue_token demo-seller offers.create "$create_key" "$create_request")"

hurl --jobs 1 --output "$test_root/create.json" \
  --variable "base_url=$base_url" \
  --variable "quote_id=$quote_id" \
  --secret "create_token=$create_token" \
  "$scenarios/sale-offer-create.hurl"
offer_id="$(jq --raw-output '.offer.id' "$test_root/create.json")"
if [[ ! "$offer_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]; then
  echo "create did not return a canonical UUIDv7 offer ID" >&2
  exit 1
fi

buy_request="{\"sale_offer_id\":\"$offer_id\"}"
buy_key='00000000-0000-4000-8000-000000000002'
second_buy_key='00000000-0000-4000-8000-000000000003'
self_buy_key='00000000-0000-4000-8000-000000000004'
buy_token="$(issue_token demo-buyer purchases.buy "$buy_key" "$buy_request")"
buy_retry_token="$(issue_token demo-buyer purchases.buy "$buy_key" "$buy_request")"
second_buy_token="$(issue_token demo-buyer purchases.buy "$second_buy_key" "$buy_request")"
self_buy_token="$(issue_token demo-seller purchases.buy "$self_buy_key" "$buy_request")"
self_buy_retry_token="$(issue_token demo-seller purchases.buy "$self_buy_key" "$buy_request")"
seller_series_token="$(issue_token demo-seller bond-series.list - '{}')"
seller_offers_token="$(issue_token demo-seller offers.list - '{"bond":"DEMO2026"}')"

hurl --test --jobs 1 --no-output \
  --variable "base_url=$base_url" \
  --secret "series_token=$series_token" \
  --secret "offers_token=$offers_token" \
  --secret "seller_series_token=$seller_series_token" \
  --secret "seller_offers_token=$seller_offers_token" \
  --variable "offer_id=$offer_id" \
  --secret "buy_token=$buy_token" \
  --secret "buy_retry_token=$buy_retry_token" \
  --secret "second_buy_token=$second_buy_token" \
  --secret "self_buy_token=$self_buy_token" \
  --secret "self_buy_retry_token=$self_buy_retry_token" \
  "$scenarios/sale-offer-lifecycle.hurl"

curl --fail --silent \
  --header "Authorization: Bearer $offers_token" \
  "$base_url/active-offers?bond=DEMO2026" \
  >"$test_root/final-offers.json-seq"
jq --seq --slurp --exit-status '
  ([.[] | select(.offer) | .offer.id] == ["01991a20-0000-7000-8000-000000000101", "01991a20-0000-7000-8000-000000000102"])
  and (.[-1].complete.offer_count == "2")
' "$test_root/final-offers.json-seq" >/dev/null

# A second ephemeral issuer produces a structurally valid assertion whose
# signature the server cannot verify. Its key ID matches, so the failure is a
# signature failure rather than an unknown-key failure.
"$demo_auth" init "$test_root/foreign-auth"
foreign_key="$test_root/foreign-auth/private.jwk"
foreign_key_token="$("$demo_auth" token "$foreign_key" demo-buyer bond-series.list - '{}')"

wrong_operation_token="$(issue_token demo-buyer offers.list - '{"bond":"DEMO2026"}')"
tampered_request_token="$(issue_token demo-buyer offers.list - '{"bond":"DEMO2027"}')"
suspended_token="$(issue_token demo-suspended bond-series.list - '{}')"
unauthorized_token="$(issue_token demo-unauthorized bond-series.list - '{}')"
unauthorized_buy_request='{"sale_offer_id":"01991a20-0000-7000-8000-000000000101"}'
unauthorized_buy_token="$(issue_token demo-unauthorized purchases.buy \
  '00000000-0000-4000-8000-0000000000a2' "$unauthorized_buy_request")"
missing_nonce_token="$(issue_token demo-buyer purchases.buy \
  '00000000-0000-4000-8000-0000000000a1' "$unauthorized_buy_request")"

hurl --test --jobs 1 --no-output \
  --variable "base_url=$base_url" \
  --secret "series_token=$series_token" \
  --secret "foreign_key_token=$foreign_key_token" \
  --secret "wrong_operation_token=$wrong_operation_token" \
  --secret "tampered_request_token=$tampered_request_token" \
  --secret "suspended_token=$suspended_token" \
  --secret "unauthorized_token=$unauthorized_token" \
  --secret "unauthorized_buy_token=$unauthorized_buy_token" \
  --secret "missing_nonce_token=$missing_nonce_token" \
  "$scenarios/authentication-failures.hurl"

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

# Quote misuse runs last and against DEMO2027, so the DEMO2026 book asserted
# above is final. The runner only arranges the fixture quote; every documented
# exchange lives in the scenario file.
intake_quote_key='00000000-0000-4000-8000-0000000000b1'
intake_quote_request='{"bond_series":"DEMO2027","price":"10.00","currency_code":"USD"}'
intake_quote_token="$(issue_token demo-seller offers.quote "$intake_quote_key" "$intake_quote_request")"
intake_quote_status="$(curl --silent --output "$test_root/intake-quote.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --header "Authorization: Bearer $intake_quote_token" \
  --header "Idempotency-Key: $intake_quote_key" \
  --data "$intake_quote_request" \
  "$base_url/sale-offer-quotes")"
if [[ "$intake_quote_status" != 201 ]]; then
  echo "offer-intake fixture quote returned HTTP $intake_quote_status" >&2
  exit 1
fi
intake_quote_id="$(jq --raw-output '.quote_id' "$test_root/intake-quote.json")"

accepted_request="{\"bond_series\":\"DEMO2027\",\"price\":\"10.00\",\"currency_code\":\"USD\",\"conversion_quote_id\":\"$intake_quote_id\"}"
usd_without_quote_token="$(issue_token demo-seller offers.create \
  '00000000-0000-4000-8000-0000000000b2' '{"bond_series":"DEMO2027","price":"10.00","currency_code":"USD"}')"
mxn_with_quote_token="$(issue_token demo-seller offers.create \
  '00000000-0000-4000-8000-0000000000b3' \
  "{\"bond_series\":\"DEMO2027\",\"price\":\"10.00\",\"currency_code\":\"MXN\",\"conversion_quote_id\":\"$intake_quote_id\"}")"
invalid_quote_token="$(issue_token demo-seller offers.create \
  '00000000-0000-4000-8000-0000000000b4' \
  '{"bond_series":"DEMO2027","price":"10.00","currency_code":"USD","conversion_quote_id":"not-a-uuid"}')"
changed_price_token="$(issue_token demo-seller offers.create \
  '00000000-0000-4000-8000-0000000000b5' \
  "{\"bond_series\":\"DEMO2027\",\"price\":\"11.00\",\"currency_code\":\"USD\",\"conversion_quote_id\":\"$intake_quote_id\"}")"
other_principal_token="$(issue_token demo-buyer offers.create \
  '00000000-0000-4000-8000-0000000000b6' "$accepted_request")"
accept_token="$(issue_token demo-seller offers.create \
  '00000000-0000-4000-8000-0000000000b7' "$accepted_request")"
reuse_token="$(issue_token demo-seller offers.create \
  '00000000-0000-4000-8000-0000000000b8' "$accepted_request")"
conflict_token="$(issue_token demo-seller offers.create \
  '00000000-0000-4000-8000-0000000000b7' '{"bond_series":"DEMO2027","price":"12.00","currency_code":"MXN"}')"

hurl --test --jobs 1 --no-output \
  --variable "base_url=$base_url" \
  --variable "quote_id=$intake_quote_id" \
  --secret "usd_without_quote_token=$usd_without_quote_token" \
  --secret "mxn_with_quote_token=$mxn_with_quote_token" \
  --secret "invalid_quote_token=$invalid_quote_token" \
  --secret "changed_price_token=$changed_price_token" \
  --secret "other_principal_token=$other_principal_token" \
  --secret "accept_token=$accept_token" \
  --secret "reuse_token=$reuse_token" \
  --secret "conflict_token=$conflict_token" \
  "$scenarios/offer-intake-failures.hurl"

echo "HTTP integration test passed"
