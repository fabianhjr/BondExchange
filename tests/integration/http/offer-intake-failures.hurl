# Accepting a conversion quote is the only way a USD submission becomes an
# offer, so every way of misusing one is part of the contract. `{{quote_id}}` is
# a valid, unexpired, unused DEMO2027 quote issued to `demo-seller`.
#
# These run against DEMO2027 so the DEMO2026 book described by the other
# scenarios is untouched.

# A USD submission without a quote has no agreed rate, so there are no terms to
# publish.
POST {{base_url}}/sale-offers
Authorization: Bearer {{usd_without_quote_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000b2
Content-Type: application/json
{
  "bond_series": "DEMO2027",
  "price": "10.00",
  "currency_code": "USD"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "an accepted USD-to-MXN conversion quote is required"

# An MXN submission is already canonical. Attaching a quote would claim a
# conversion that did not happen.
POST {{base_url}}/sale-offers
Authorization: Bearer {{mxn_with_quote_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000b3
Content-Type: application/json
{
  "bond_series": "DEMO2027",
  "price": "10.00",
  "currency_code": "MXN",
  "conversion_quote_id": "{{quote_id}}"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "conversion quote is missing, expired, mismatched, or already used"

# Quote identities are UUIDv7 like every other resource, and are validated
# before any lookup.
POST {{base_url}}/sale-offers
Authorization: Bearer {{invalid_quote_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000b4
Content-Type: application/json
{
  "bond_series": "DEMO2027",
  "price": "10.00",
  "currency_code": "USD",
  "conversion_quote_id": "not-a-uuid"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "conversion quote must be a canonical UUIDv7"

# The quote is bound to the exact terms it priced. Accepting it for a different
# USD amount would apply its pinned rate to an amount the seller never quoted.
POST {{base_url}}/sale-offers
Authorization: Bearer {{changed_price_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000b5
Content-Type: application/json
{
  "bond_series": "DEMO2027",
  "price": "11.00",
  "currency_code": "USD",
  "conversion_quote_id": "{{quote_id}}"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "conversion quote is missing, expired, mismatched, or already used"

# A quote belongs to the principal that requested it. Another authenticated
# trader cannot accept it, and learns nothing about whether it exists.
POST {{base_url}}/sale-offers
Authorization: Bearer {{other_principal_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000b6
Content-Type: application/json
{
  "bond_series": "DEMO2027",
  "price": "10.00",
  "currency_code": "USD",
  "conversion_quote_id": "{{quote_id}}"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "conversion quote is missing, expired, mismatched, or already used"

# The seller accepts their own quote on the quoted terms. 10.00 USD x 17 = 170.
POST {{base_url}}/sale-offers
Authorization: Bearer {{accept_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000b7
Content-Type: application/json
{
  "bond_series": "DEMO2027",
  "price": "10.00",
  "currency_code": "USD",
  "conversion_quote_id": "{{quote_id}}"
}
HTTP 201
[Asserts]
jsonpath "$.offer.price" == "170"
jsonpath "$.offer.currency_code" == "MXN"

# A quote is single-use. A new operation cannot accept it a second time, so one
# priced conversion can never back two offers.
POST {{base_url}}/sale-offers
Authorization: Bearer {{reuse_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000b8
Content-Type: application/json
{
  "bond_series": "DEMO2027",
  "price": "10.00",
  "currency_code": "USD",
  "conversion_quote_id": "{{quote_id}}"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "conversion quote is missing, expired, mismatched, or already used"

# Reusing a spent nonce for different terms is a conflict, not a retry. The
# stored request digest is what distinguishes the two.
POST {{base_url}}/sale-offers
Authorization: Bearer {{conflict_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000b7
Content-Type: application/json
{
  "bond_series": "DEMO2027",
  "price": "12.00",
  "currency_code": "MXN"
}
HTTP 409
[Asserts]
jsonpath "$.error" == "idempotency key was already used for another operation"
