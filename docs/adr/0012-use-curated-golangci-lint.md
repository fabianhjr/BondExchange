# ADR-0012: Use curated golangci-lint static analysis

- Status: Accepted
- Date: 2026-09-04

## Context

The Go quality gate previously checked `gofmt` and invoked `go vet` directly.
That detects important compiler-supported problems, but it does not consistently
check error handling, resource closure, context propagation, security-sensitive
constructs, logging calls, or the repository's dependency direction. Running
uncoordinated standalone analyzers would also give contributors and CI different
tool versions and suppression behavior.

## Decision

Use golangci-lint from the nixpkgs revision pinned by `devenv.lock`, and run it
through `devenv tasks run go:check` after the existing `gofmt` verification.
The version-2 configuration in `.golangci.yml` uses `linters.default: standard`
and adds an explicit, reviewed set of correctness, security, context,
resource-lifecycle, API, logging, test, and maintenance analyzers. The standard
set intentionally follows golangci-lint: when the pinned tool is deliberately
updated, any newly standard analyzer becomes part of the reviewed update.

Use `depguard` to enforce that `internal/exchange` does not import internal
adapters or command composition. Generated Go files are recognized only by the
strict Go generated-code marker. Production and test code otherwise share the
same gate, and CI analyzes the complete repository rather than only changed
lines.

Every suppression must name the applicable linter and explain why the construct
is safe. Prefer correcting a finding. Use a narrow suppression only when the
analyzer cannot represent an intentional boundary, and do not use baseline
files or broad directory exclusions to accept existing findings.

## Consequences

- `go vet` remains active through golangci-lint's standard set without running
  the analyzer twice.
- Contributors and CI use the same pinned binary and checked-in policy.
- Tool updates may add a standard analyzer and therefore require source or
  configuration changes as part of the lock-file update.
- Static analysis adds defense in depth but does not replace race, integration,
  coverage, mutation, vulnerability, or model checks.
- Some safe boundary code may need a local, explained suppression.

## Alternatives considered

### Keep only `gofmt` and `go vet`

This has a smaller configuration surface but leaves recurring classes of error,
resource, context, and security mistakes outside the ordinary quality gate.

### Enable every golangci-lint analyzer

This was rejected because many analyzers impose conflicting or highly
opinionated styles, target unused frameworks, or change behavior as analyzers
are added. A reviewed addition to the standard set is acceptable because it is
coupled to an intentional pinned-tool update.

### Enable `revive` immediately

The current code produces a large comment-policy migration rather than
correctness findings. That policy can be adopted separately if maintaining
GoDoc for every exported internal symbol becomes a project goal.
