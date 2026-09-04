# Transport API

`proto/bondexchange/v1/bond_exchange.proto` is the source of truth for the
server's internal RPC messages, gRPC methods, REST routes, success responses, and
documented REST errors. It uses `google.api.http` annotations to generate the
REST gateway and gRPC-Gateway OpenAPI annotations to make the Swagger response
codes and schemas match the running service.

Generated artifacts are committed so building and consuming the repository do
not require generation:

- Go protobuf, gRPC, and REST gateway bindings are under `../gen/go/`;
- the Swagger 2.0 document is under `openapi/`; and
- `buf.lock` pins the Google API and gRPC-Gateway schema dependencies by
  content digest.

Do not edit generated artifacts directly. Change the Proto3 contract and run:

```console
devenv tasks run api:generate
devenv tasks run api:check
```

The Nix/devenv environment pins Buf, protoc, all Go code-generation plugins,
and `grpcurl`. `api:check` also runs before the Go checks and the complete
`devenv test` gate.

The generated REST gateway is registered directly against the in-process gRPC
service implementation. REST requests therefore do not make a loopback network
call, while external gRPC clients reach the same implementation on the gRPC
listener. The adapter maps domain errors to canonical gRPC codes; the REST
gateway maps those codes to HTTP statuses while retaining the existing
`{"error":"..."}` JSON error shape.

Every method requires one `Authorization: Bearer <assertion>` metadata value.
Mutations additionally require exactly one `Idempotency-Key`. Identity fields
are not part of request messages: the operation-bound assertion resolves the
principal used as buyer or seller. Assertion content and validation are
documented in [`../docs/security/ASVS.md`](../docs/security/ASVS.md).

The API publishes sale offers with `POST /sale-offers`, lists every active
offer for one required bond series with `GET /active-offers?bond=...`, and
discovers all series currently having active offers with
`GET /active-bond-series`. Active-offer listing is deliberately unbounded but
server-streamed. gRPC uses its native stream; a strict custom REST adapter uses
RFC 7464 JSON Text Sequences because direct in-process gRPC-Gateway
registration does not implement server streams. Both emit one offer per event
and a terminal count. Removed pagination and identity field numbers and names
are reserved against reuse.
