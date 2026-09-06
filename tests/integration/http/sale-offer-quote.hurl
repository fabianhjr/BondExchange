# A USD submission is never stored as USD. The seller first asks for a quote,
# which pins one Banxico SF43718 FIX observation and converts the submitted USD
# amount to the canonical MXN terms the marketplace will hold.
#
# The quote is an offer of terms, not an offer to the market: nothing is
# published until the seller accepts it in `sale-offer-create.hurl`.
POST {{base_url}}/sale-offer-quotes
Authorization: Bearer {{quote_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000009
Content-Type: application/json
{
  "bond_series": "demo2026",
  "price": "97.125",
  "currency_code": "USD"
}
HTTP 201
[Captures]
quote_id: jsonpath "$.quote_id"
[Asserts]
# PostgreSQL generates the quote identity, as it does for every resource.
jsonpath "$.quote_id" matches /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
# Bond series are canonicalized to uppercase at the service boundary.
jsonpath "$.bond_series" == "DEMO2026"
# The submission is echoed back so the seller can confirm what they offered.
jsonpath "$.submitted_price" == "97.125"
jsonpath "$.submitted_currency_code" == "USD"
# 97.125 USD x 17 MXN/USD, banker's-rounded to four fractional digits.
jsonpath "$.mxn_price" == "1651.125"
jsonpath "$.currency_code" == "MXN"
# The exact rate and its series are pinned into the quote, so acceptance cannot
# silently use a newer observation.
jsonpath "$.rate" == "17"
jsonpath "$.rate_series" == "SF43718"
jsonpath "$.rate_observed_on" matches /^\d{4}-\d{2}-\d{2}T/
# A quote expires, which is what makes accepting it a decision rather than an
# open option on the exchange rate.
jsonpath "$.expires_at" matches /^\d{4}-\d{2}-\d{2}T/
# Seller identity comes from the assertion and is never echoed back.
jsonpath "$.seller_id" not exists

# An exact retry replays the stored quote instead of pricing a second one. The
# quote ID and the pinned rate are unchanged, so a retrying client cannot drift
# onto a different exchange rate.
POST {{base_url}}/sale-offer-quotes
Authorization: Bearer {{quote_retry_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000009
Content-Type: application/json
{
  "bond_series": "demo2026",
  "price": "97.125",
  "currency_code": "USD"
}
HTTP 201
[Asserts]
jsonpath "$.quote_id" == "{{quote_id}}"
jsonpath "$.mxn_price" == "1651.125"
jsonpath "$.rate" == "17"
