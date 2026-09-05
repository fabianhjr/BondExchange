# ADR-0019: Canonicalize sale offers to MXN at intake

- Status: Accepted
- Date: 2026-09-04

This ADR amends ADR-0014: the persisted SIE service now has one transactional
consumer, but remains separated from the exchange core.

## Context

Every sale offer served to buyers must be denominated in Mexican pesos. Sellers
may think and submit in US dollars, and that original amount must remain
auditable, but a changing rate must not make an active offer or completed
reservation change value after publication. Banxico SIE series `SF43718` is the
approved FIX mapping and is interpreted explicitly as MXN per USD.

Conversion creates financial terms on the seller's behalf. Silently applying a
rate during `CreateSaleOffer`, or converting each read at a new rate, would not
give the seller a stable amount to accept. The exchange core should not depend
on a provider, network client, cache, or submission currency.

## Decision

The exchange core accepts and persists only positive MXN sale-offer terms. A
separate `offerintake` application service owns the optional USD workflow and
depends inward on the exchange types plus the provider-neutral exchange-rate
service. The server composition root supplies the existing SIE adapter and
PostgreSQL repository; the core never imports either rate package.

A USD seller performs two idempotent operations:

1. `QuoteSaleOffer` obtains the current persisted `SF43718` observation mapped
   as USD/MXN, rejects stale, future, missing, unpersisted, or more-than-seven-
   day-old observations, multiplies exact decimals, and rounds once to the
   monetary scale using half-to-even.
2. `CreateSaleOffer` names that quote and repeats the exact seller, bond, USD
   amount, and currency. PostgreSQL atomically verifies ownership, match,
   expiry, and non-use, then appends the MXN offer, canonical terms, USD
   submission provenance, and successful operation result.

Quotes expire after five minutes by default. A quote pins one immutable rate
revision and may create at most one offer. MXN submissions require no quote and
record identity provenance. Unsupported currencies fail before persistence.
All create, list, buy, response, and version-2 event paths use canonical terms;
they never dynamically convert. USD provenance is not served as an offer.

The expand migration preserves the original `sale_offers` columns and
compatibility view so the previously deployed application can still run. It
backfills unambiguous existing MXN rows. Existing non-MXN rows remain immutable
but receive no guessed conversion; the new application hides them from active
reads and refuses to buy them until a separately authorized seller migration or
retirement workflow exists.

## Consequences

- Buyers and downstream version-2 event consumers see one immutable MXN price.
- The exact submitted USD amount, accepted quote, rate revision, observation
  date, rounding policy, and resulting MXN value remain append-only facts.
- SIE failure disables new USD quotes but does not disable MXN offer creation,
  listing, buying, or already issued offers.
- Quote acceptance adds one API round trip and requires clients to obtain a new
  operation-bound assertion after learning the quote UUID.
- A seven-day observation-age ceiling tolerates long bank-holiday intervals but
  is a policy value, not proof that a market is open. Production ownership and
  monitoring remain required.
- Mixed-version rollout remains data-compatible, but old instances can still
  expose their legacy multi-currency behavior until they are drained. The
  control's activation therefore requires release evidence that the old binary
  is no longer serving traffic.
- Pre-existing non-MXN offers are preserved and fail closed. Their disposition
  is an explicit operational gap, not an automatic repricing decision.

## Alternatives considered

### Convert on every read

This keeps USD as the source of truth and always displays a recent MXN value,
but makes active terms vary over time, makes purchase and event values depend
on read timing, and increases SIE availability coupling. It was rejected.

### Store USD in the core and materialize an MXN projection

A projection could serve stable terms while retaining the original row shape,
but it leaves two plausible core prices and makes alternate readers easy to get
wrong. Separating canonical terms from provenance makes the authority explicit.

### Convert once during create without a quote

This is operationally simpler, but the seller cannot inspect and explicitly
accept the actual rate, rounding, and resulting MXN terms. It was rejected for
the human submission journey.

### Reject every non-MXN submission

This is the strongest and simplest denomination control. It remains preferable
where clients can convert under their own governed process, but it does not meet
the required USD-submission journey or preserve server-side conversion
provenance.

### Use a centrally approved daily rate table or manual pricing service

An administered business-date rate with maker-checker approval could provide
stronger holiday, market-calendar, correction, and incident controls than
on-demand latest FIX. It also introduces operational ownership and a second
approval system. This should replace the current policy if regulation or
settlement rules require a specific business-date rate.

### Create a pending USD submission and convert asynchronously

This supports manual approval, scheduled rates, and extended outages, but adds
a pending/accepted/rejected lifecycle, cancellation semantics, and new domain
facts. It is a better design if quote acceptance must survive beyond minutes;
it is unnecessary for the current synchronous journey.
