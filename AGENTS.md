# Repository guidance

## Keep implementation and documentation synchronized

- Whenever an implementation change is planned or made, review all relevant
  documentation for impact. Update the documentation in the same change when
  behavior, constraints, workflows, architecture, or usage have changed.
- Whenever a documentation change is planned or made, review the corresponding
  implementation, specification, configuration, and CI workflow. Update them
  in the same change when the documentation would otherwise describe behavior
  that is not implemented or verified.
- Treat TLA+ modules, TLC configurations, Nix/devenv configuration, and GitHub
  Actions workflows as implementation for this rule. Treat root and scoped
  READMEs and ADRs as documentation.
- Include both implementation impact and documentation impact when planning a
  change. If one side needs no edit, still verify that it remains accurate.
- Record architecture-level decisions or changes in `docs/adr/`.

## Maintain the project map

- Keep the project map below as a brief overview of repository organization,
  not a file inventory.
- Update it when a subsystem or major directory is added, removed, moved, or
  given a materially different responsibility. Ordinary file changes do not
  require a map update.
- Omit generated files, local runtime state, and low-level implementation
  details.

## Architectural guardrails

- Keep TLA+ focused on domain behavior. Do not model HTTP, SQL, deployment, or
  input-canonicalization mechanics; update the specification when domain
  behavior or invariants change.
- Preserve the Go dependency direction: `cmd/server` performs composition;
  HTTP and PostgreSQL adapters depend on `internal/exchange`, never the reverse.
- Keep server instances stateless. Cross-instance buy serialization belongs to
  PostgreSQL's one-purchase-per-offer constraint, not process-local locking.
- Keep domain-fact tables append-only. Corrections or reversals require new
  facts and review of the specification and ADRs.
- Manage schema changes with dbmate. Add timestamped migrations and never
  rewrite an applied migration or migrate during application startup.
- Make forward migrations lossless and backward compatible: the previously
  deployed application must continue to work after each migration. Use
  expand/backfill/contract steps, preserve all unique data, and contract only
  redundant compatibility structures after old application versions can no
  longer run. Do not use down migrations to discard domain facts; prefer a
  corrective forward migration.
- Use devenv tasks for project commands. Run focused checks while iterating and
  `devenv test` before handing off cross-cutting changes; do not weaken quality
  gates without documenting the decision.

## Project map

| Area | Responsibility |
| --- | --- |
| Repository root | Project guidance, contributor documentation, and the devenv/Nix development environment. |
| `api/` and `gen/go/` | Proto3 API source, generated Swagger contract, and generated Go transport bindings. |
| `cmd/` and `internal/` | Stateless Go server, domain logic, REST/gRPC transport adapters, and PostgreSQL adapter. |
| `db/` | Versioned database schema and persistence documentation. |
| `spec/tla/` | TLA+ domain and behavior specifications, TLC model configuration, and model documentation. |
| `docs/adr/` | Architecture decision records and their index. |
| `.github/workflows/` | Continuous integration workflows. |
