# ADR-0015: Place the Go module under `application/`

- Status: Accepted
- Date: 2026-09-04

## Context

The repository contains a service implementation alongside its API schema,
database migrations, formal specification, security analysis, documentation,
and development orchestration. Keeping the Go module, commands, internal
packages, generated Go bindings, and Go tool configuration at the repository
root made those application concerns visually indistinguishable from the
system-level concerns.

A Go module stored in a repository subdirectory also needs that subdirectory
in its module path so normal module discovery and versioning identify its
location correctly.

## Decision

Use `application/` as the Go module boundary. Move `cmd/`, `internal/`,
`gen/go/`, `go.mod`, `go.sum`, `.golangci.yml`, and `.gremlins.yaml` beneath
it. Change the module path to
`github.com/fabianhjr/BondExchange/application`, including internal imports and
the generated protobuf package path.

Keep the Proto3 source and non-Go artifacts under `api/`; Buf writes generated
Go bindings into `application/gen/go/`. Keep database migrations, the TLA+
specification, documentation, Nix/devenv configuration, CI workflows, and
shared `.artifacts/` output at the repository root. Root devenv tasks remain
the supported cross-cutting command interface and enter the nested module when
they run Go tools.

This is a repository-layout and module-identity change only. It does not alter
domain behavior, data flows, runtime boundaries, security controls, failure
modes, or TLA+ invariants.

## Consequences

- The repository root presents application, API, data, specification, and
  documentation concerns as separate top-level areas.
- Go commands run directly from `application/`, while root-level contributors
  can continue to use the unchanged devenv task names.
- External users of the generated Go bindings must import the new package path.
- API generation, scripts, CI path filters, security evidence, and
  documentation must refer to the nested application paths.
- Future Go-specific files belong under `application/` unless they orchestrate
  repository-wide behavior.

## Alternatives considered

### Move only `cmd/` and `internal/`

This leaves the module files, generated Go source, and Go tool policy split
across two directory levels, so the application does not have a coherent
boundary.

### Keep the original module path

This avoids import changes locally, but the declared module identity would no
longer describe its repository subdirectory and would make normal remote module
discovery and version tagging ambiguous.

### Move all supporting subsystems under `application/`

The API contract, database migrations, formal specification, and system-level
documentation are independently meaningful boundaries. Nesting them inside the
Go module would imply ownership and coupling that the architecture deliberately
avoids.
