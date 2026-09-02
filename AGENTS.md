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

## Project map

| Area | Responsibility |
| --- | --- |
| Repository root | Project guidance, contributor documentation, and the devenv/Nix development environment. |
| `cmd/` and `internal/` | Stateless Go server, domain logic, HTTP transport, and PostgreSQL adapter. |
| `db/` | Versioned database schema and persistence documentation. |
| `spec/tla/` | TLA+ domain and behavior specifications, TLC model configuration, and model documentation. |
| `docs/adr/` | Architecture decision records and their index. |
| `.github/workflows/` | Continuous integration workflows. |
