-- migrate:up

-- The Go domain accepts exactly three uppercase ASCII letters for a sale-offer
-- currency, while storage accepted any non-empty text. A privileged or future
-- alternate writer could therefore append an immutable fact that the
-- application rejects or cannot interpret. The SIE tables already constrain
-- their currency columns with this pattern; this aligns the domain fact.
--
-- NOT VALID constrains every subsequent insert immediately without scanning
-- existing rows, so this migration cannot fail on historical data and the
-- previously deployed application keeps working. A separate migration
-- validates the existing rows, which isolates a nonconforming-history failure
-- from the forward protection.

ALTER TABLE bond_exchange.sale_offers
  ADD CONSTRAINT sale_offers_currency_code_canonical
  CHECK (currency_code ~ '^[A-Z]{3}$') NOT VALID;

-- migrate:down

ALTER TABLE bond_exchange.sale_offers
  DROP CONSTRAINT sale_offers_currency_code_canonical;
