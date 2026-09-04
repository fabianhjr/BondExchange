# PostgreSQL generates the UUIDv7 identity; the authenticated principal is the seller.
POST {{base_url}}/sale-offers
Authorization: Bearer {{create_token}}
Idempotency-Key: 00000000-0000-4000-8000-000000000001
Content-Type: application/json
{
  "bond_series": "demo2026",
  "price": "97.125",
  "currency_code": "USD"
}
HTTP 201
[Asserts]
jsonpath "$.offer.id" matches /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
jsonpath "$.offer.bond_series" == "DEMO2026"
jsonpath "$.offer.price" == "97.125"
jsonpath "$.offer.currency_code" == "USD"
jsonpath "$.offer.seller_id" not exists
