# Every rejection below is produced by the running server, not by a stub. The
# assertion contract is the service's primary control, so these are the shapes
# a client sees when it gets that contract wrong.
#
# Authentication and authorization failures are deliberately generic: they name
# no principal, key, claim, or reason, so a caller cannot enumerate identities
# or probe which part of an assertion was wrong.

# A request without an assertion is rejected before anything else happens.
GET {{base_url}}/active-bond-series
HTTP 401
[Asserts]
header "Content-Type" == "application/json; charset=utf-8"
header "Cache-Control" == "no-store"
header "X-Content-Type-Options" == "nosniff"
jsonpath "$.error" == "authentication required"

# A bearer value that is not a JWT is rejected identically.
GET {{base_url}}/active-bond-series
Authorization: Bearer not-a-json-web-token
HTTP 401
[Asserts]
jsonpath "$.error" == "authentication required"

# The scheme must be Bearer. A different scheme carries no operation binding.
GET {{base_url}}/active-bond-series
Authorization: Basic ZGVtby1idXllcjpzZWNyZXQ=
HTTP 401
[Asserts]
jsonpath "$.error" == "authentication required"

# A well-formed assertion signed by a key the server does not trust fails
# signature verification even though its key ID resolves.
GET {{base_url}}/active-bond-series
Authorization: Bearer {{foreign_key_token}}
HTTP 401
[Asserts]
jsonpath "$.error" == "authentication required"

# An assertion authorizes exactly one operation. A listing assertion cannot be
# replayed against a mutation.
POST {{base_url}}/buys
Authorization: Bearer {{wrong_operation_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000a1
Content-Type: application/json
{
  "sale_offer_id": "01991a20-0000-7000-8000-000000000101"
}
HTTP 401
[Asserts]
jsonpath "$.error" == "authentication required"

# An assertion is bound to the SHA-256 digest of one canonical request. The
# assertion below was issued for DEMO2027, so it cannot list DEMO2026.
GET {{base_url}}/active-offers?bond=DEMO2026
Authorization: Bearer {{tampered_request_token}}
HTTP 401
[Asserts]
jsonpath "$.error" == "authentication required"

# A suspended principal does not resolve, so it fails as unauthenticated rather
# than as forbidden. Suspension is an appended fact; the trader grant is intact.
GET {{base_url}}/active-bond-series
Authorization: Bearer {{suspended_token}}
HTTP 401
[Asserts]
jsonpath "$.error" == "authentication required"

# A principal that resolves but holds no role is authenticated and forbidden.
GET {{base_url}}/active-bond-series
Authorization: Bearer {{unauthorized_token}}
HTTP 403
[Asserts]
jsonpath "$.error" == "operation not permitted"

# The same principal is forbidden on a mutation, after admission and before any
# domain work.
POST {{base_url}}/buys
Authorization: Bearer {{unauthorized_buy_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000a2
Content-Type: application/json
{
  "sale_offer_id": "01991a20-0000-7000-8000-000000000101"
}
HTTP 403
[Asserts]
jsonpath "$.error" == "operation not permitted"

# A mutation must carry its nonce in the transport header as well as in the
# assertion. Omitting the header is rejected.
POST {{base_url}}/buys
Authorization: Bearer {{missing_nonce_token}}
Content-Type: application/json
{
  "sale_offer_id": "01991a20-0000-7000-8000-000000000101"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "idempotency key must be a canonical UUIDv4 nonce"

# The nonce must be a canonical UUIDv4. UUIDv7 is reserved for resource
# identities and is not accepted as a mutation nonce.
POST {{base_url}}/buys
Authorization: Bearer {{missing_nonce_token}}
Idempotency-Key: 01991a20-0000-7000-8000-000000000101
Content-Type: application/json
{
  "sale_offer_id": "01991a20-0000-7000-8000-000000000101"
}
HTTP 400
[Asserts]
jsonpath "$.error" == "idempotency key must be a canonical UUIDv4 nonce"

# Read operations must not carry a nonce at all; there is no durable result to
# replay, so accepting one would imply a guarantee the service does not make.
GET {{base_url}}/active-bond-series
Authorization: Bearer {{series_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000a3
HTTP 400
[Asserts]
jsonpath "$.error" == "idempotency key must be a canonical UUIDv4 nonce: not allowed for read operations"

# The JSON envelope is validated before the request reaches the domain.
POST {{base_url}}/buys
Authorization: Bearer {{missing_nonce_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000a4
Content-Type: text/plain
```
{"sale_offer_id":"01991a20-0000-7000-8000-000000000101"}
```
HTTP 415
[Asserts]
jsonpath "$.error" == "Content-Type must be application/json"

# A duplicated object key is ambiguous, so the document is refused rather than
# resolved to whichever value the parser reaches last.
POST {{base_url}}/buys
Authorization: Bearer {{missing_nonce_token}}
Idempotency-Key: 00000000-0000-4000-8000-0000000000a5
Content-Type: application/json
```
{"sale_offer_id":"01991a20-0000-7000-8000-000000000101","sale_offer_id":"01991a20-0000-7000-8000-000000000102"}
```
HTTP 400
[Asserts]
jsonpath "$.error" == "invalid JSON request"
