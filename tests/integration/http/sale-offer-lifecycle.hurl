# Discover the bond series that currently have active offers.
GET {{base_url}}/active-bond-series
Authorization: Bearer {{series_token}}
HTTP 200
[Asserts]
jsonpath "$.bond_series" count == 2
jsonpath "$.bond_series[0]" == "DEMO2026"
jsonpath "$.bond_series[1]" == "DEMO2027"

# A listing is an RFC 7464 JSON Text Sequence. It includes the two seeded
# offers, the PostgreSQL-generated offer captured by the create scenario, and
# then a terminal count.
GET {{base_url}}/active-offers?bond=DEMO2026
Authorization: Bearer {{offers_token}}
HTTP 200
Content-Type: application/json-seq; charset=utf-8
[Asserts]
body contains "01991a20-0000-7000-8000-000000000101"
body contains "01991a20-0000-7000-8000-000000000102"
body contains "{{offer_id}}"
body contains "\"offer_count\":\"3\""

# Buy creates a binding reservation, not settlement or custody transfer.
POST {{base_url}}/buys
Authorization: Bearer {{buy_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000002
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}"
}
HTTP 201
[Captures]
bought_at: jsonpath "$.bought_at"
[Asserts]
jsonpath "$.offer.id" == "{{offer_id}}"
jsonpath "$.offer.price" == "1651.125"
jsonpath "$.buyer_id" not exists
jsonpath "$.bought_at" matches /^\d{4}-\d{2}-\d{2}T/

# An exact retry with a fresh assertion replays the durable result.
POST {{base_url}}/buys
Authorization: Bearer {{buy_retry_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000002
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}"
}
HTTP 201
[Asserts]
jsonpath "$.offer.id" == "{{offer_id}}"
jsonpath "$.bought_at" == "{{bought_at}}"

# A new operation cannot buy the already reserved offer.
POST {{base_url}}/buys
Authorization: Bearer {{second_buy_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000003
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}"
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
