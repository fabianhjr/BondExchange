# ADR-0023: Align storage constraints with domain validation

- Status: Accepted
- Date: 2026-09-05

## Context

Domain facts are append-only. A row that the Go domain cannot interpret is
therefore permanent: no `UPDATE` can repair it, and no `DELETE` can remove it.
That makes any gap between what the application validates and what storage
accepts more serious here than in a system where a bad row can be corrected.

Such a gap existed. `exchange.ParseCurrencyCode` requires exactly three
uppercase ASCII letters, while `sale_offers.currency_code` required only a
non-empty string. Provisioning and security administration still require direct
SQL (F-003), so the writer most likely to produce a nonconforming fact is
exactly the one the application does not validate. The SIE tables had already
adopted `^[A-Z]{3}$` for their currency columns, so the schema disagreed with
itself as well.

Nothing detected the divergence. The schema tests asserted structure — UUID
primary keys, the monetary domain, contracted legacy columns — but never
compared a validation rule against its storage counterpart.

## Decision

Constrain `sale_offers.currency_code` with `CHECK (currency_code ~ '^[A-Z]{3}$')`,
matching the domain parser and the existing SIE columns.

Add the constraint and validate it in two migrations. The first adds it
`NOT VALID`, which constrains every subsequent insert immediately without
scanning existing rows, so it cannot fail on historical data and the previously
deployed application continues to work. The second validates the retained
history.

Make the validation fail loudly rather than repair. Sale offers are append-only,
so the validation migration counts nonconforming rows and raises an exception
naming that count, with a hint that the correction must be recorded as new facts
in a reviewed corrective forward migration. Silently accepting a fact the
application cannot read, or silently discarding one, are both worse outcomes
than a stopped migration.

Test the equivalence rather than the constraint.
`TestStorageConstraintsMatchDomainValidation` runs each candidate value through
both the Go parser and a direct SQL insert inside a rolled-back transaction, and
fails when the two verdicts differ in either direction. Candidates include the
regular-expression anchor trap `"USD\n"`, which a dialect whose `$` matches
before a trailing newline would wrongly accept.

Record the one remaining divergence as a test. The price column is
`numeric(14,4)` and PostgreSQL rounds a more precise input at cast time, before
any `CHECK` observes it, so no column constraint can reject it. A dedicated
sub-test asserts that the divergence still exists, so closing it — which would
require changing the monetary domain's base type — fails the suite and forces
the register and database README to be updated in the same change.

## Consequences

- A direct-SQL writer can no longer append a sale offer whose currency the
  application rejects. F-004 narrows to the price-scale case alone.
- The equivalence test generalizes: adding a value class to the table extends
  the guarantee, and a future constraint drift fails with a message naming the
  value and the direction of the disagreement.
- A deployment holding nonconforming rows cannot apply the validation migration
  until an operator dispositions them. That is intended, and it is why the
  forward protection is in a separate migration that always applies.
- Two migrations for one constraint is more ceremony than a single
  `ADD CONSTRAINT`. The separation is what keeps a nonconforming history from
  blocking protection of new writes.
- The price divergence remains documented rather than fixed, so the storage
  layer is still not a complete substitute for boundary validation.

## Alternatives considered

### Add the constraint and validate in one migration

Simpler, and correct on any conforming database. On a database with even one
nonconforming row the whole migration rolls back, so the forward protection is
lost precisely where it is most needed. Splitting costs one file and removes
that coupling.

### Repair nonconforming rows in the migration

An `UPDATE` normalizing the currency would let validation always succeed. It
would also mutate an immutable domain fact, which the append-only triggers and
ADR-0003 exist to prevent, and it would invent a value the seller never
submitted.

### Change the monetary domain so the price scale also agrees

This would close the last divergence, but it requires altering the base type of
a domain used by append-only columns, which is a materially riskier migration
than a `CHECK`. The sanctioned writer validates scale first, so the residual
risk is limited to direct SQL and is recorded in F-004 rather than acted on now.

### Rely on the boundary and document the gap

The application already validates. The reason this is not sufficient is F-003:
until a supported provisioning workflow exists, direct SQL is a sanctioned
writer, and it does not run the Go parser.
