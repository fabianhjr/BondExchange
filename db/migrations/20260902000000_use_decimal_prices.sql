-- migrate:up

CREATE DOMAIN bond_exchange.monetary_amount AS numeric(14, 4)
  CONSTRAINT monetary_amount_finite
  CHECK (VALUE > '-Infinity'::numeric AND VALUE < 'Infinity'::numeric);

DROP VIEW bond_exchange.active_offers;

ALTER TABLE bond_exchange.sale_offers
  DROP CONSTRAINT sale_offers_price_positive;

ALTER TABLE bond_exchange.sale_offers
  ALTER COLUMN price TYPE bond_exchange.monetary_amount
    USING price::bond_exchange.monetary_amount,
  ADD CONSTRAINT sale_offers_price_positive
    CHECK (price > 0);

CREATE VIEW bond_exchange.active_offers AS
SELECT
  sale_offer.id,
  sale_offer.seller_id,
  sale_offer.bond_series,
  sale_offer.price,
  sale_offer.currency_code
FROM bond_exchange.sale_offers AS sale_offer
WHERE NOT EXISTS (
  SELECT 1
  FROM bond_exchange.purchases AS purchase
  WHERE purchase.sale_offer_id = sale_offer.id
);

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'monetary amounts cannot be converted to bigint losslessly; roll forward';
END;
$$;
