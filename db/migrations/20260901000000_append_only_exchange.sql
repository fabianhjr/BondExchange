-- migrate:up

CREATE SCHEMA bond_exchange;

CREATE TABLE bond_exchange.users (
  id text PRIMARY KEY,
  CONSTRAINT users_id_not_empty CHECK (id <> '')
);

CREATE TABLE bond_exchange.bonds (
  series text PRIMARY KEY,
  CONSTRAINT bonds_series_canonical
    CHECK (series ~ '^[A-Z0-9]{3,40}$')
);

CREATE TABLE bond_exchange.sale_offers (
  id text PRIMARY KEY,
  seller_id text NOT NULL REFERENCES bond_exchange.users (id),
  bond_series text NOT NULL REFERENCES bond_exchange.bonds (series),
  price bigint NOT NULL CONSTRAINT sale_offers_price_positive CHECK (price > 0),
  currency_code text NOT NULL,
  CONSTRAINT sale_offers_currency_code_not_empty CHECK (currency_code <> '')
);

CREATE INDEX sale_offers_bond_series_id_idx
  ON bond_exchange.sale_offers (bond_series, id);

CREATE TABLE bond_exchange.purchases (
  sale_offer_id text PRIMARY KEY REFERENCES bond_exchange.sale_offers (id),
  buyer_id text NOT NULL REFERENCES bond_exchange.users (id),
  bought_at timestamptz NOT NULL DEFAULT transaction_timestamp()
);

CREATE INDEX purchases_buyer_id_sale_offer_id_idx
  ON bond_exchange.purchases (buyer_id, sale_offer_id);

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

CREATE FUNCTION bond_exchange.reject_domain_fact_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'domain fact tables are append only'
    USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER users_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.users
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

CREATE TRIGGER bonds_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.bonds
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

CREATE TRIGGER sale_offers_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.sale_offers
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

CREATE TRIGGER purchases_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.purchases
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

REVOKE UPDATE, DELETE, TRUNCATE
  ON bond_exchange.users,
     bond_exchange.bonds,
     bond_exchange.sale_offers,
     bond_exchange.purchases
  FROM PUBLIC;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'the initial append-only schema has no lossless down migration; roll forward';
END;
$$;
