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

quote_key="00000000-0000-4000-8000-000000000010"
quote_request='{"bond_series":"DEMO2026","price":"97.125","currency_code":"USD"}'
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
jq --exit-status '.mxn_price == "1651.125" and .currency_code == "MXN" and .rate_series == "SF43718"' \
  "$test_root/quote.json" >/dev/null

create_key="00000000-0000-4000-8000-000000000011"
create_request="{\"bond_series\":\"DEMO2026\",\"price\":\"97.125\",\"currency_code\":\"USD\",\"conversion_quote_id\":\"$quote_id\"}"
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
jq --exit-status '.offer.price == "1651.125" and .offer.currency_code == "MXN"' "$test_root/create.json" >/dev/null
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

# Everything above reached the application through the in-process REST gateway.
# The checks below drive the native gRPC listener so the second transport is
# exercised end to end rather than assumed equivalent.
grpc_call() {
  local token="$1" method="$2" body="$3"
  shift 3
  grpcurl -plaintext \
    -protoset "$project_root/api/descriptors/bondexchange.protoset" \
    -H "Authorization: Bearer $token" \
    "$@" \
    -d "$body" \
    "$grpc_address" \
    "bondexchange.v1.BondExchangeService/$method"
}

grpc_series_token="$(issue_token demo-buyer bond-series.list - '{}')"
grpc_call "$grpc_series_token" ListActiveBondSeries '{}' >"$test_root/grpc-series.json"
jq -e '.bondSeries == ["DEMO2026", "DEMO2027"]' "$test_root/grpc-series.json" >/dev/null

# ListActiveOffers is a server stream. grpcurl prints one JSON document per
# message, so the terminal completion frame is the last document.
grpc_offers_token="$(issue_token demo-buyer offers.list - '{"bond":"DEMO2027"}')"
grpc_call "$grpc_offers_token" ListActiveOffers '{"bond":"DEMO2027"}' \
  >"$test_root/grpc-offers.json"
jq -se '
  ([.[] | select(.offer) | .offer.id] == ["01991a20-0000-7000-8000-000000000103"])
  and (.[-1].complete.offerCount == "1")
' "$test_root/grpc-offers.json" >/dev/null

# A mutation over gRPC uses the same assertion, nonce, and durable idempotency
# scope as the REST path. DEMO2027 is untouched by the REST journey above.
grpc_buy_key="00000000-0000-4000-8000-000000000014"
grpc_buy_request='{"sale_offer_id":"01991a20-0000-7000-8000-000000000103"}'
grpc_buy_token="$(issue_token demo-buyer purchases.buy "$grpc_buy_key" "$grpc_buy_request")"
grpc_call "$grpc_buy_token" Buy "$grpc_buy_request" \
  -H "Idempotency-Key: $grpc_buy_key" >"$test_root/grpc-buy.json"
jq -e '.offer.id == "01991a20-0000-7000-8000-000000000103" and .offer.currencyCode == "MXN"' \
  "$test_root/grpc-buy.json" >/dev/null

# The reservation is visible to the other transport, which is the point of
# sharing PostgreSQL rather than process state.
curl --fail --silent \
  --header "Authorization: Bearer $(issue_token demo-buyer bond-series.list - '{}')" \
  "$base_url/active-bond-series" >"$test_root/series-after-grpc-buy.json"
jq -e '.bond_series == ["DEMO2026"]' "$test_root/series-after-grpc-buy.json" >/dev/null

# Request admission is shared across transports. ADR-0028 requires gRPC to
# report ResourceExhausted with a google.rpc.RetryInfo detail, which is the
# native counterpart of the REST Retry-After header.
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
grpc_limited_token="$(issue_token demo-rate-limited bond-series.list - '{}')"
if grpc_call "$grpc_limited_token" ListActiveBondSeries '{}' \
  >"$test_root/grpc-rate-limit.out" 2>"$test_root/grpc-rate-limit.err"; then
  echo "rate-limited gRPC request unexpectedly succeeded" >&2
  exit 1
fi
if ! grep -q 'Code: ResourceExhausted' "$test_root/grpc-rate-limit.err"; then
  echo "rate-limited gRPC request did not report ResourceExhausted" >&2
  cat "$test_root/grpc-rate-limit.err" >&2
  exit 1
fi
if ! grep -q 'google.rpc.RetryInfo' "$test_root/grpc-rate-limit.err"; then
  echo "rate-limited gRPC request did not carry a RetryInfo detail" >&2
  cat "$test_root/grpc-rate-limit.err" >&2
  exit 1
fi

# An unauthorized principal is rejected on gRPC exactly as it is on REST, and
# the message names neither the principal nor the missing permission.
grpc_denied_token="$(issue_token demo-unauthorized bond-series.list - '{}')"
if grpc_call "$grpc_denied_token" ListActiveBondSeries '{}' \
  >"$test_root/grpc-denied.out" 2>"$test_root/grpc-denied.err"; then
  echo "unauthorized gRPC request unexpectedly succeeded" >&2
  exit 1
fi
if ! grep -q 'Code: PermissionDenied' "$test_root/grpc-denied.err"; then
  echo "unauthorized gRPC request did not report PermissionDenied" >&2
  cat "$test_root/grpc-denied.err" >&2
  exit 1
fi
if ! grep -q 'Message: operation not permitted' "$test_root/grpc-denied.err"; then
  echo "unauthorized gRPC request did not use the generic authorization message" >&2
  cat "$test_root/grpc-denied.err" >&2
  exit 1
fi

# An assertion bound to a different request is refused on gRPC as well, so the
# request digest is enforced by the adapter rather than by the REST gateway.
grpc_tampered_token="$(issue_token demo-buyer offers.list - '{"bond":"DEMO2026"}')"
if grpc_call "$grpc_tampered_token" ListActiveOffers '{"bond":"DEMO2027"}' \
  >"$test_root/grpc-tampered.out" 2>"$test_root/grpc-tampered.err"; then
  echo "gRPC request with a mismatched assertion digest unexpectedly succeeded" >&2
  exit 1
fi
if ! grep -q 'Code: Unauthenticated' "$test_root/grpc-tampered.err"; then
  echo "mismatched gRPC assertion digest did not report Unauthenticated" >&2
  cat "$test_root/grpc-tampered.err" >&2
  exit 1
fi

echo "Demo smoke check passed"
