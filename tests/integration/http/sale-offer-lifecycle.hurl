# A seller's discovery view omits series whose only active offers are their own.
GET {{base_url}}/active-bond-series
Authorization: Bearer {{seller_series_token}}
HTTP 200
[Asserts]
jsonpath "$.bond_series" count == 0

# A seller's offer book likewise omits their own offers.
GET {{base_url}}/active-offers?bond=DEMO2026
Authorization: Bearer {{seller_offers_token}}
HTTP 200
Content-Type: application/json-seq; charset=utf-8
[Asserts]
body contains "\"complete\":{}"

# Retaining an offer ID cannot bypass the self-trade control.
POST {{base_url}}/buys
Authorization: Bearer {{self_buy_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000004
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}",
  "quantity": 3
}
HTTP 400
[Asserts]
jsonpath "$.error" == "a buyer cannot reserve their own sale offer"

# The rejected market-integrity decision is durable and replayable.
POST {{base_url}}/buys
Authorization: Bearer {{self_buy_retry_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000004
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}",
  "quantity": 3
}
HTTP 400
[Asserts]
jsonpath "$.error" == "a buyer cannot reserve their own sale offer"

# Another principal discovers the bond series that have tradable offers.
GET {{base_url}}/active-bond-series
Authorization: Bearer {{series_token}}
HTTP 200
[Asserts]
jsonpath "$.bond_series" count == 2
jsonpath "$.bond_series[0]" == "DEMO2026"
jsonpath "$.bond_series[1]" == "DEMO2027"

# The buyer's listing is an RFC 7464 JSON Text Sequence. It includes the two seeded
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

# A partial Buy creates a binding reservation and an immutable residual, not
# settlement or custody transfer.
POST {{base_url}}/buys
Authorization: Bearer {{buy_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000002
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}",
  "quantity": 3
}
HTTP 201
[Captures]
bought_at: jsonpath "$.bought_at"
[Asserts]
jsonpath "$.offer.id" == "{{offer_id}}"
jsonpath "$.offer.price" == "1651.125"
jsonpath "$.offer.quantity" == 7
jsonpath "$.quantity" == 3
jsonpath "$.buyer_id" not exists
jsonpath "$.bought_at" matches /^\d{4}-\d{2}-\d{2}T/

# An exact retry with a fresh assertion replays the durable result.
POST {{base_url}}/buys
Authorization: Bearer {{buy_retry_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000002
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}",
  "quantity": 3
}
HTTP 201
[Asserts]
jsonpath "$.offer.id" == "{{offer_id}}"
jsonpath "$.quantity" == 3
jsonpath "$.bought_at" == "{{bought_at}}"

# A new operation sent to the original ID follows its residual and reserves the
# four units left there.
POST {{base_url}}/buys
Authorization: Bearer {{second_buy_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000003
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}",
  "quantity": 4
}
HTTP 201
[Asserts]
jsonpath "$.offer.id" matches /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
jsonpath "$.offer.id" != "{{offer_id}}"
jsonpath "$.offer.quantity" == 4
jsonpath "$.quantity" == 4

# Once the residual is fully reserved, another request through the ancestor is
# unavailable.
POST {{base_url}}/buys
Authorization: Bearer {{third_buy_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000005
Content-Type: application/json
{
  "sale_offer_id": "{{offer_id}}",
  "quantity": 3
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
