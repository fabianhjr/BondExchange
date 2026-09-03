# ADR-0005: Use shopspring/decimal for monetary amounts

- Status: Accepted
- Date: 2026-09-02

## Context

Sale offers contain a price and currency. The initial Go implementation used
an `int64`-based `Price`, and PostgreSQL stored the corresponding value as
`bigint`. That representation is exact but leaves its unit implicit, prevents
fractional major-currency values, and places a fixed bound on magnitude.

Binary floating-point types are unsuitable because ordinary decimal monetary
values cannot always be represented exactly. Conversions through `float32` or
`float64` could therefore silently change a price at an API, calculation, or
persistence boundary.

The project needs one exact representation across its Go domain model and a
compatible exact PostgreSQL representation. Display scale, currency-specific
fraction digits, rounding, fees, and arithmetic policy are separate concerns
that the current purchase-only behavior does not yet define.

## Decision

Use [`github.com/shopspring/decimal`](https://github.com/shopspring/decimal) at
version 1.4.0 and represent monetary amounts directly with
`decimal.Decimal`. `SaleOffer.Price` uses that type rather than a project
wrapper or an integer alias.

Construct monetary values from decimal strings or exact integer inputs. Do not
construct them from binary floating-point input. Domain boundaries reject
malformed, zero, negative, out-of-range prices, and values that would require
rounding to fit four fractional digits. Compare monetary values with the
library's `Equal`, `Cmp`, and sign methods rather than Go's `==`, because
numerically equal decimals can have different internal exponents.

Serialize prices in JSON as decimal strings, using the library's default
behavior, so clients do not implicitly decode them through IEEE 754 numbers.
The serialized value is a monetary value, not a display-format contract;
currency-specific trailing zeros are not guaranteed. Do not change the
library's process-global JSON or arithmetic settings. Any future operation
that can require rounding or division must define its precision and rounding
mode explicitly at the call site and record a domain-level policy when one is
introduced.

Define `bond_exchange.monetary_amount` as a PostgreSQL domain over
`numeric(14,4)`. Its 14-digit precision and four-digit scale allow ten digits
to the left of the decimal point and values through `9999999999.9999`. A
domain constraint excludes NaN and positive and negative infinity; columns
separately apply `NOT NULL` and domain-specific sign constraints. Sale-offer
prices must be greater than zero.

PostgreSQL rounds inputs with more than four fractional digits when coercing
them to `numeric(14,4)`. Application and provisioning boundaries must reject
values that would change under that coercion unless a future domain rule
explicitly authorizes rounding. The PostgreSQL adapter transfers stored prices
as exact decimal text and parses that text into `decimal.Decimal`, avoiding an
intermediate floating-point value.

Migrate the existing `bigint` column to `monetary_amount` with an explicit
cast. Existing values within the new range convert without rounding; the
migration aborts rather than discard or alter an out-of-range value. Existing
integer prices remain readable by the previous application during deployment.
There is currently no application write path for sale offers; external
provisioning must not introduce fractional prices until every old application
instance has been replaced. Because later decimal values cannot necessarily
be represented by `bigint`, the migration intentionally has no destructive
down path.

Increasing precision or scale later is a schema migration, not a configuration
change: PostgreSQL cannot alter a domain's underlying `numeric` typemod. The
migration must create a replacement domain, convert every dependent column,
recreate dependent views, and retire the old domain only after all consumers
have moved. Although `numeric` stores only the digits present and therefore
does not reserve the declared maximum for every row, converting populated
columns requires schema locks and may rewrite their tables. Before widening,
measure the lock and rewrite behavior on production-sized data and use an
expand/backfill/contract rollout when an in-place conversion cannot meet the
availability budget. Increasing scale also needs an explicit rounding and API
compatibility decision.

The TLA+ model continues to use finite positive natural numbers as abstract
representatives of prices. Decimal encoding and precision do not alter the
buy transition or its invariants and remain outside that domain model.

## Consequences

- Decimal monetary values remain exact through domain, JSON, and persistence
  boundaries without relying on an implicit minor unit.
- Prices may have four fractional digits and up to ten integer digits rather
  than being limited to integers.
- API consumers must treat `price` as a JSON string and use a decimal-capable
  parser.
- Callers must use decimal methods for comparison and arithmetic; the type is
  more allocation-heavy and less familiar than primitive integers.
- The combination of amount and `currency_code` remains the monetary value;
  `decimal.Decimal` alone does not prevent arithmetic across currencies.
- This decision does not establish currency-specific display scales, business
  rounding modes, exchange-rate, settlement, or accounting rules.
- The shared domain centralizes storage constraints, but widening its fixed
  precision or scale requires a coordinated migration of every dependent
  column.

## Alternatives considered

### Integer minor units

Keeping `int64` and defining it as a minor-unit count is exact and efficient.
It was not selected because the scale varies by currency or instrument, must
be carried separately, and the fixed range can constrain large values. It
would also require conversion rules before the current domain has defined a
currency-scale policy.

### Binary floating point

`float64` is simple and fast but cannot exactly represent many base-10 values.
It was rejected because invisible rounding at monetary boundaries is an
unacceptable default.

### `math/big.Rat`

Rational numbers are exact but can retain non-terminating values and do not
naturally match decimal input, JSON, or PostgreSQL `numeric`. They would still
need a separate decimal rounding and serialization policy.

### A project-specific money type

A wrapper could combine amount, currency, validation, and serialization. It
was deferred because the current aggregate already carries currency beside
the price and has no monetary arithmetic. Introduce a richer value object if
future behavior needs to make cross-currency operations impossible by type.
