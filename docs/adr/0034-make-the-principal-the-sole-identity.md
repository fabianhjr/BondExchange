# ADR-0034: Make the principal the sole identity

- Status: Accepted; transition completed and its tooling removed 2026-09-07
- Date: 2026-09-06

## Context

The schema carried two identity tables. `bond_exchange.users` was created as
the internal identity that sale offers and purchases are attributed to, and
`bond_exchange.principals` was added later as the federated identity that
authenticates. After the UUID contraction
([ADR-0018](0018-contract-the-legacy-identifier-graph.md)), `users` retained a
single column: its own `uuid_id`. It held no attribute, no name, and no
relationship other than being referenced.

The relationship between the two was already one-to-one and enforced:
`principals.uuid_id` was simultaneously that table's primary key and a foreign
key to `users.uuid_id`. A principal could not exist without a user row, and
because the API derives the seller and buyer only from the authenticated
principal ([G-009](../guarantees.md)), a user row without a principal could
never appear in any domain fact. The only state the second table could
represent was an allocated identity that can never authenticate, sell, or buy.

That shape paid for two tables and delivered none of the benefit a separate
owner table would provide. Expressing beneficial ownership — one legal person
behind several credentials — requires a many-to-one relationship, which a
primary key that is also the foreign key cannot represent. The registers,
meanwhile, described a state the schema had already made unreachable: F-023,
[G-009](../guarantees.md), and `spec/tla/README.md` each asserted that the two
tables had no foreign key between them, and derived the reachability of
`buyer_not_found` and `seller_not_found` from that. The assertion was true
before the UUID expand migration restored the key on `uuid_id` and was not
rechecked afterwards.

Every consumer already treated the two as one concept. The Go domain had a
single `UserID` type used for the principal, the seller, and the buyer; the
TLA+ model had a single `Users` set; and the demo seed, the load-test fixture,
and the integration-test helpers always wrote both rows together.

## Decision

`bond_exchange.principals` is the sole identity table. It generates its own
UUIDv7, `sale_offers.seller_uuid` and `purchases.buyer_uuid` reference it
directly, and `bond_exchange.users` is removed.

The change is applied as expand, validate, and contract migrations. The expand
migration gives `principals.uuid_id` a `uuidv7()` default and adds the
principal-referencing foreign keys `NOT VALID`, so it neither scans history nor
breaks the previously deployed application, which still reads and writes
through both relationships. The validate migration proves the retained history
conforms; because sale offers and purchases are append-only it cannot repair a
nonconforming row, so it reports the counts and stops. The contract migration
drops the user-referencing keys and the table.

Contraction discards nothing. An identity allocated in `users` that no
principal covers is the only value the table held that is not derivable
elsewhere, so the contract migration refuses rather than dropping it, reporting
the count and stopping. `users` is append-only, so the resolution available to
an operator is to append the missing principal fact and link the identity; the
migration does not do that on their behalf. In every state this repository
produces — the demo seed, the load-test fixture, the integration-test helpers —
no such identity exists, so the check is a guard rather than a step.

An earlier draft of this decision archived those identities into
`legacy_identifier_archive` instead. That table is being retired by
[ADR-0033](0033-retire-legacy-identifier-evidence-and-transition-tooling.md)
because its retention period elapsed, and adding a new dependent to a table
already accepted for removal would have reintroduced exactly the evidence
lifecycle that decision closes.

The contract migration must not be applied while any writer still names
`bond_exchange.users`. The `db:principal-contract-readiness` gate reported the
database side of that condition; retirement of the previous application
binaries and of direct-SQL writers had to be shown by release evidence, as for
the UUID and canonical-MXN contractions.

The Go domain type is renamed `exchange.PrincipalID`. `ParseUserID` and
`ErrInvalidUserID` are removed: no non-test caller produced or consumed them,
so the gRPC arm mapping that error to `InvalidArgument` was unreachable. The
buy classifier no longer distinguishes a buyer that is not a principal, because
the validated foreign key makes that state unrepresentable; `ErrBuyerNotFound`
and its durable code were retained at first so that `operation_results` rows
written before this change still replayed.

The TLA+ constant `Users` is renamed `Principals`. The model's meaning does not
change — one identity set was always what it described — and TLC reports the
same state counts for every configured instance.

### Transition completed

The rolling window has since closed, and everything that existed only to cross
it is removed:

- `classifyCreateSaleOfferError` no longer accepts the old
  `sale_offers_seller_uuid_fkey` name alongside the principal-referencing one.
  Only one of those constraints can exist in a contracted schema.
- `db:principal-contract-readiness`, its script, and its `dev:ci` entry are
  removed. Its pre-contraction checks describe a state that no longer occurs,
  and `TestPrincipalIsTheSoleIdentityTable` already re-checks the contracted
  shape — the table's absence, both validated foreign keys, the generated
  default, and the rejection of a non-principal seller — on every run. This
  follows the precedent
  [ADR-0033](0033-retire-legacy-identifier-evidence-and-transition-tooling.md)
  set for the UUID transition's gates.
- `ErrBuyerNotFound` and the `buyer_not_found` rejection code are removed.
  Migration `20260907000000` first proves that no retained `operation_results`
  row records the code and then forbids it, so removing the decode cannot
  change how any stored rejection replays. That order matters: an exact retry
  replays a stored rejection verbatim, so deleting the decode while such a row
  existed would have quietly downgraded a recorded rejection to an
  unrecognized-code error. The migration refuses instead, leaving an operator
  whose history still carries the code on a binary that understands it.

`ErrSellerNotFound` stays. It is still produced when an insert violates
`sale_offers_seller_principal_fkey`, which is defense in depth against an
alternate writer rather than a reachable path for API traffic.

This decision does not introduce beneficial ownership. It removes a table that
could not express it. Should affiliation between distinct principals become a
requirement, it is added as a nullable owner column on `principals` and an
owner table alongside it, which is an additive expand migration rather than a
restoration of `users`.

## Consequences

- One identity table, one Go type, and one TLA+ set describe one concept.
- Every seller and buyer is an authenticated principal, enforced by validated
  foreign keys rather than by convention ([G-016](../guarantees.md)).
- Provisioning a principal is one insert instead of two, and the identity is
  generated by the database rather than supplied by the caller.
- `users_are_append_only` is withdrawn as guarantee evidence; the identity
  facts it covered are now covered by `principals_are_append_only`.
- F-023 is withdrawn. Its second concern — that distinct principals may share a
  beneficial owner — is not resolved by this decision, and F-002 already tracks
  it, so no replacement identifier was minted.
- A deployment that applied the contract migration before retiring the previous
  binaries broke those instances at their next buy, because `buyQuery` named
  `bond_exchange.users`. That ordering hazard is spent once the transition
  completes, and the gate that guarded it is removed with it.
- The service records six durable rejection codes rather than seven. A database
  constraint, not a convention, keeps the retired one from reappearing.

## Alternatives considered

### Keep both tables and make the relationship many-to-one

Renaming `users` to an owner table and giving `principals` a separate owner
column would deliver what F-023 and ASVS AD-19 ask for. It also changes what
same-identity self-trade prevention compares
([ADR-0030](0030-prevent-same-identity-self-trading.md)), requires the TLA+
model to carry two identity sets while a combined authorization and contention
instance is already intractable (F-022), and adds authoritative affiliation
data the product does not have. Nothing today needs multi-principal owners, and
this decision leaves that path open as an additive change.

### Correct the documentation and leave the schema

The three false statements about the missing foreign key are worth correcting
on their own, and doing only that carries no deployment risk. It leaves a table
with no attributes, a foreign key that exists only to constrain a key to
itself, and error paths that defend an unreachable state.

### Keep `users` and move the federated columns into it

This collapses to one table too, but keeps the name of the concept the service
does not model — an owner distinct from a credential — and would require
renaming every `principal_*` table, view, column, permission, and metric that
already uses the accurate word.
