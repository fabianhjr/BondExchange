# Bond Exchange TLA+ specification

This directory contains a deliberately small marketplace model:

- `BondExchangeActions.tla` defines the domain, sale-offer state, validation,
  and buying action.
- `BondExchange.tla` defines the possible initial sale books and composes the
  buying action into the temporal specification.
- `BondExchange.cfg` provides a finite instance for exhaustive TLC checking.

## Domain

- A user is an element of the finite `Users` set.
- A bond is identified by an uppercase alphanumeric series string whose length
  is between 3 and 40 characters, inclusive.
- A sale offer has a unique ID, a seller, a bond, a positive price, and one
  currency code.

The initial state represents any non-empty, well-formed set of sale offers
within the configured bounds. Offer IDs must be unique within that set.

Case-insensitive lookup and conversion of input to the uppercase canonical
representation are responsibilities of the system boundary. The TLA+ model
does not implement that conversion; it assumes it receives normalized bond
identifiers and verifies that their stored representation is uppercase.

## Behavior

`Buy(offerId)` is the only state-changing action. It is enabled only when
exactly one existing sale offer has the requested ID. The action atomically
removes that one offer. An empty sale book is a valid terminal state, so the
TLC configuration does not treat it as a deadlock error.

There are intentionally no buy offers, matching engine, balances, holdings,
partial fills, order publication, ownership transfer, or settlement process in
this model.

## Verification

TLC checks that every reachable sale-offer set is well formed and that offer
IDs remain unique:

```console
devenv tasks run spec:check
```

`devenv test` includes the same check and is the command run by CI.

When the domain grows, introduce only behavior required by an explicit system
decision and add its properties to both the TLA+ module and TLC configuration.
