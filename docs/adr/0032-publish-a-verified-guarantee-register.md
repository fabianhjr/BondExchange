# ADR-0032: Publish a verified guarantee register

- Status: Accepted
- Date: 2026-09-06

## Context

The properties this system actually holds — one purchase per offer under
contention, append-only facts, assertion-bound idempotent operations,
same-identity self-trade prevention, MXN-only offer terms — were recorded only
as a side effect of the documents that own their mechanisms. The root README
lists them as architecture bullets, `db/README.md` describes them as
constraints, `spec/tla/README.md` names them as TLA+ properties for a reader
who can read TLA+, and thirty ADRs each explain one decision at the moment it
was made. A reader who wants to know what the service promises has to
reconstruct it from all four.

The registers that already exist are deliberately negative. `FRICTIONS.md`
records verified unresolved gaps and `docs/FMEA.md` analyzes failure paths.
Neither states what currently holds, so the repository documents its weaknesses
more legibly than its strengths, and a reviewer cannot tell whether an absent
claim is an oversight or an intentional boundary.

A positive register carries a specific risk the negative ones do not. A
friction is removed when it is fixed, and a stale friction is visible as work
that is already done. A stale guarantee is invisible: it keeps asserting a
property after the constraint, property, or gate that enforced it was renamed
or removed, and it is credited as evidence in exactly the reviews where being
wrong matters most. Prose that claims verification without being verified is
worse than no document, for the same reason a quality gate that skips its own
subject is worse than an absent gate.

## Decision

Add `docs/guarantees.md` as the register of what the system guarantees, and
make `docs:check` verify its claims rather than only its links.

Each entry carries a stable, never-reused `G-` identifier and states the
promise, the adverse condition the promise survives, what a caller observes,
where the promise stops, and the artifacts that enforce and verify it. The
boundary section is mandatory: every entry links to the friction or failure
mode that tracks what it does not cover. A guarantee is admitted only when it is
enforced in code or schema and verified by a task in the test gate.

Enforcement citations are machine-checked by kind. A PostgreSQL name must be
defined by a forward migration and not dropped by a later one; a Go identifier
must exist in hand-written application source, excluding generated bindings and
tests; a Proto3 name must exist in the API source; a TLA+ property must be
defined in the properties module and referenced by at least one TLC
configuration; a verification task must be defined in `devenv.nix`. `G-`
identifiers are checked the way `F-` and `FM-` identifiers already are, with
architecture decision records exempt from reference checking for the same
reason.

Only forward migration sections are read. A down migration may drop an object
the deployed schema still has, and this repository corrects by rolling forward.

The register describes the system as implemented. It does not introduce
behavior, and it is not a specification of intent: a property the service does
not yet hold belongs in `FRICTIONS.md` or an ADR, never here.

## Consequences

- A reviewer, an operator, and an API consumer can read the system's promises
  and their boundaries in one place, at the level of "at most one buyer
  acquires an offer" rather than at the level of a constraint name.
- Renaming a constraint, deleting a TLA+ property, or removing a task from the
  gate fails `docs:check` until the guarantee citing it is corrected or
  withdrawn. The register cannot silently outlive its evidence.
- Requiring a TLA+ citation to appear in a TLC configuration makes the same
  distinction `spec:check` already makes: a property no instance checks is not
  evidence.
- The FMEA can cite guarantees as prevention controls by identifier instead of
  restating them, and the three registers stay complementary: guarantees state
  what holds, frictions state what is missing, the FMEA states how it fails.
- The citation check proves that a named artifact exists, not that it enforces
  the sentence above it. That link is still review, as it is for every other
  document here.
- Adding a guarantee is now more expensive than writing a paragraph. That is
  intended; the cost is what the register's credibility rests on.

## Alternatives considered

### Expand the root README

The README is the entry point and already carries a quick start, a navigation
table, an architecture summary, and the verification matrix. Adding fourteen
entries with boundaries and citations would bury all of it, and README prose
has no checked relationship to the code.

### Put the overview in the TLA+ README

That document is the model's own record and is addressed to a reader working
with the specification. Most guarantees here are enforced partly or entirely
outside the model — assertion binding, rate limiting, migration compatibility —
and the model's boundaries section exists precisely to stop readers crediting
it with more than it checks.

### Keep the register as prose with no identifiers or gate

Cheapest to write and the most likely to become false. Identifiers exist so the
FMEA and ADRs can reference a guarantee without restating it; the gate exists
because this register's whole value is that its claims are current.

### Generate the register from the code

Constraint names, property names, and task names can be extracted, but the
promise, the adverse condition, and the boundary cannot. A generated document
would list mechanisms, which the existing documents already do, and omit the
part a reader needs.
