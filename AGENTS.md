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
  READMEs, `FRICTIONS.md`, `docs/FMEA.md`, the security profile, and ADRs as
  documentation.
- Include both implementation impact and documentation impact when planning a
  change. If one side needs no edit, still verify that it remains accurate.
- Record architecture-level decisions or changes in `docs/adr/`.

## Maintain the friction register

- Treat `FRICTIONS.md` as the living register of verified, unresolved product,
  implementation, security, operations, and contributor-experience rough edges.
- Review `FRICTIONS.md` whenever work is planned or completed. Update it in the
  same change when that work resolves, mitigates, invalidates, reprioritizes, or
  discovers a friction; include friction impact in plans alongside implementation
  and documentation impact.
- Keep entries concrete and evidence-based. Each entry should identify the
  affected area, practical impact, and the condition that would resolve it.
  Do not use the register as an unbounded feature wish list.
- Remove resolved entries instead of retaining a completed-work log. Preserve
  architecture rationale in ADRs and history, and do not reuse an old friction
  identifier for an unrelated issue.
- Keep `FRICTIONS.md` consistent with READMEs, ADRs, the security profile, the
  TLA+ specification, migrations, configuration, and CI. If an apparent
  friction is an intentional long-term constraint, document that decision in
  the appropriate durable document and adjust the register accordingly.

## Maintain the failure mode and effects analysis

- Treat `docs/FMEA.md` as the living, system-level analysis of how the service
  can fail, the effects of each failure, current prevention and detection
  controls, residual risk, and required follow-up.
- Review the FMEA whenever work is planned or completed. Update it in the same
  change when functionality, data flows, dependencies, deployment assumptions,
  controls, tests, operating procedures, or known incidents change a failure
  mode, effect, cause, score, action, or status. Include FMEA impact in plans
  alongside implementation, documentation, and friction impact.
- Score residual risk after current verified controls using the rubric in the
  FMEA. Do not lower occurrence or detection scores without implementation,
  test, monitoring, or operational evidence. A low risk-priority number does
  not waive explicit review of a high-severity effect.
- Keep FMEA actions synchronized with `FRICTIONS.md`, the ASVS profile, ADRs,
  READMEs, the TLA+ specification, migrations, configuration, and CI. The FMEA
  analyzes failure paths; `FRICTIONS.md` remains the source of truth for
  verified unresolved rough edges and their completion conditions.
- Give new failure modes stable, sequential identifiers. Do not reuse an
  identifier. Retain controlled failure modes so their safety mechanisms stay
  visible; remove one only when the affected function or boundary no longer
  exists, and update all references in the same change.

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
- Preserve the Go dependency direction: `application/cmd/server` performs
  composition; HTTP and PostgreSQL adapters depend on
  `application/internal/exchange`, never the reverse.
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
| Repository root | Project guidance, contributor documentation, and the devenv configuration. |
| `api/` | Proto3 API source, generated Swagger contract, and versioned descriptor set. |
| `application/` | Go module containing the stateless server, demo-only assertion issuer, generated Go transport bindings, domain logic, authentication, Banxico SIE exchange-rate ingestion and caching, integration-event delivery orchestration, REST/gRPC adapters, PostgreSQL adapter, and Go quality configuration. |
| `db/` | Versioned database schema, disposable demo fixtures, and persistence documentation. |
| `nix/` | Nix-packaged PostgreSQL lifecycle, demo, and development verification helpers. |
| `spec/tla/` | TLA+ domain and behavior specifications, TLC model configuration, and model documentation. |
| `docs/FMEA.md` | System-level failure mode, effects, controls, residual-risk, and follow-up analysis. |
| `docs/adr/` | Architecture decision records and their index. |
| `docs/security/` | ASVS application profile, requirement dispositions, and continuous-compliance evidence. |
| `tests/integration/` | Executable REST interaction documentation and generated load-test scenarios. |
| `.github/workflows/` | Continuous integration workflows. |
