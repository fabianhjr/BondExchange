# Go application

This directory is the Go module for the Bond Exchange service. It contains:

- `cmd/server`, the production composition root;
- `cmd/demo-auth`, the development-only assertion issuer;
- `internal/`, the domain, application services, and transport and persistence
  adapters; and
- `gen/go/`, the checked-in Go bindings generated from the Proto3 contract in
  `../api/`.

Run ordinary Go commands from this directory, for example:

```console
go test ./...
go run ./cmd/server
```

Prefer the repository-root devenv tasks for complete checks because they also
provision PostgreSQL, validate generated artifacts, and run the other project
quality gates. Generated bindings must be changed through the root
`api:generate` task, not edited directly.
