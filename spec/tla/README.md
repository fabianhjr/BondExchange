# Bond Exchange TLA+ specification

This directory contains a deliberately small marketplace model:

- `BondExchangeActions.tla` defines the domain, active sale offers, completed
  purchases, validation, and buying action.
- `BondExchange.tla` defines the possible initial sale books and composes the
  buying action into the temporal specification.
- `BondExchange.cfg` provides a finite instance for exhaustive TLC checking.

## Domain

- A user is an element of the finite `Users` set.
- A bond is identified by an uppercase alphanumeric series string whose length
  is between 3 and 40 characters, inclusive.
- A sale offer has a unique ID, a seller, a bond, a positive price, and one
  currency code.
- A completed purchase relates the purchased offer to the user who bought it.

`Prices` uses positive natural numbers in the finite TLC instance as abstract
representatives of positive exact monetary values. Decimal precision,
PostgreSQL encoding, and JSON serialization do not affect buying behavior or
the checked invariants, so they remain outside the domain model.

The initial state represents any non-empty, well-formed set of sale offers
within the configured bounds. Offer IDs must be unique within that set.

Case-insensitive lookup and conversion of input to the uppercase canonical
representation are responsibilities of the system boundary. The TLA+ model
does not implement that conversion; it assumes it receives normalized bond
identifiers and verifies that their stored representation is uppercase.

## Behavior

`Buy(buyer, offerId)` is the only state-changing action. It is enabled only
when the buyer is a user and exactly one active sale offer has the requested
ID. The action atomically removes that offer from the active sale book and
records a completed purchase containing the unchanged offer and its buyer. An
empty active sale book is a valid terminal state, so the TLC configuration
does not treat it as a deadlock error.

The model does not prohibit a seller from buying their own offer.

There are intentionally no buy offers, matching engine, balances, holdings,
partial fills, order publication, ownership transfer, or settlement process in
this model.

## Verification

TLC checks that every reachable active sale-offer and completed-purchase set
is well formed, that offer IDs remain unique, and that an offer cannot be both
active and purchased:

```console
devenv tasks run spec:check
```

`devenv test` includes the same check. The specification CI workflow invokes
the focused `spec:check` task directly.

When the domain grows, introduce only behavior required by an explicit system
decision and add its properties to both the TLA+ module and TLC configuration.
