# ADR-0001: Use devenv for project dependencies and tasks

- Status: Accepted
- Date: 2026-09-01

## Context

The Bond Exchange repository starts with an executable TLA+ specification.
Contributors and CI need the same Java runtime, TLA+ tools, and model-checking
command. The repository is expected to gain more implementation languages and
services as the design develops, so its environment should remain composable
without requiring globally installed project dependencies.

## Decision

Use devenv as the contributor-facing interface to the Nix ecosystem.

Project inputs are pinned in `devenv.lock`. Packages and tasks are declared in
`devenv.nix`, while `devenv.yaml` identifies the upstream inputs. Contributors
enter the environment with `devenv shell`, invoke individual checks with
`devenv tasks run`, and run the complete verification suite with `devenv test`.
CI runs that same `devenv test` command.

The TLA+ package is sourced from the pinned nixpkgs input. Its wrapper is
overridden to use the project's JDK 21 runtime rather than nixpkgs' legacy Java
8 default.

## Consequences

- Dependencies and their transitive inputs are reproducible through Nix.
- Local development and CI share one task definition.
- Future languages, services, and task dependencies can be added without
  introducing a second environment manager.
- Contributors must install Nix and the devenv CLI.
- The project carries devenv-specific configuration in addition to Nix input
  lock data.
- Updating dependencies is an explicit change performed with `devenv update`
  and reviewed through the resulting lock-file diff.

## Alternatives considered

### Plain Nix flakes

Plain flakes have fewer layers and expose native `nix develop` and
`nix flake check` workflows. They were not selected because devenv provides a
clear task lifecycle and leaves room for future services while still using Nix
for dependency resolution.

### Devbox

Devbox offers an approachable JSON configuration. It was not selected because
custom package overrides and task lifecycle integration are more direct in
devenv's Nix module configuration.

### Legacy `shell.nix`

A `shell.nix` development shell is simple, but it does not provide the same
standard input lock and task interface expected from a new repository.

