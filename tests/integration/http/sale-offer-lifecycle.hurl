# Discover the bond series that currently have active offers.
GET {{base_url}}/active-bond-series
Authorization: Bearer {{series_token}}
HTTP 200
[Asserts]
jsonpath "$.bond_series" count == 2
jsonpath "$.bond_series[0]" == "DEMO2026"
jsonpath "$.bond_series[1]" == "DEMO2027"

# A listing is an RFC 7464 JSON Text Sequence: offer frames, then a count.
GET {{base_url}}/active-offers?bond=DEMO2026
Authorization: Bearer {{offers_token}}
HTTP 200
Content-Type: application/json-seq; charset=utf-8
[Asserts]
body contains "demo-offer-1"
body contains "demo-offer-2"
body contains "\"offer_count\":\"2\""

# The authenticated principal is the seller; it is not accepted in the body or returned.
POST {{base_url}}/sale-offers
Authorization: Bearer {{create_token}}
Idempotency-Key: integration-create-0001
Content-Type: application/json
{
  "id": "integration-offer-1",
  "bond_series": "demo2026",
  "price": "97.125",
  "currency_code": "USD"
}
HTTP 201
[Asserts]
jsonpath "$.offer.id" == "integration-offer-1"
jsonpath "$.offer.bond_series" == "DEMO2026"
jsonpath "$.offer.price" == "97.125"
jsonpath "$.offer.currency_code" == "USD"
jsonpath "$.offer.seller_id" not exists

# The new offer becomes visible and changes the terminal count.
GET {{base_url}}/active-offers?bond=DEMO2026
Authorization: Bearer {{offers_token}}
HTTP 200
[Asserts]
body contains "integration-offer-1"
body contains "\"offer_count\":\"3\""

# Buy creates a binding reservation, not settlement or custody transfer.
POST {{base_url}}/buys
Authorization: Bearer {{buy_token}}
Idempotency-Key: integration-buy-000001
Content-Type: application/json
{
  "sale_offer_id": "integration-offer-1"
}
HTTP 201
[Captures]
bought_at: jsonpath "$.bought_at"
[Asserts]
jsonpath "$.offer.id" == "integration-offer-1"
jsonpath "$.offer.price" == "97.125"
jsonpath "$.buyer_id" not exists
jsonpath "$.bought_at" matches /^\d{4}-\d{2}-\d{2}T/

# An exact retry with a fresh assertion replays the durable result.
POST {{base_url}}/buys
Authorization: Bearer {{buy_retry_token}}
Idempotency-Key: integration-buy-000001
Content-Type: application/json
{
  "sale_offer_id": "integration-offer-1"
}
HTTP 201
[Asserts]
jsonpath "$.offer.id" == "integration-offer-1"
jsonpath "$.bought_at" == "{{bought_at}}"

# A new operation cannot buy the already reserved offer.
POST {{base_url}}/buys
Authorization: Bearer {{second_buy_token}}
Idempotency-Key: integration-buy-000002
Content-Type: application/json
{
  "sale_offer_id": "integration-offer-1"
}
HTTP 404
[Asserts]
jsonpath "$.error" == "sale offer does not exist or has already been bought"

# Buying removes the offer from the active book.
GET {{base_url}}/active-offers?bond=DEMO2026
Authorization: Bearer {{offers_token}}
HTTP 200
[Asserts]
body contains "\"offer_count\":\"2\""
