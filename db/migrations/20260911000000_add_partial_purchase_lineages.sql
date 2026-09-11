-- migrate:up

ALTER TABLE bond_exchange.sale_offers
  ADD COLUMN quantity integer NOT NULL DEFAULT 1,
  ADD COLUMN partial_purchase_provenance uuid,
  ADD CONSTRAINT sale_offers_quantity_positive
    CHECK (quantity > 0) NOT VALID,
  ADD CONSTRAINT sale_offers_partial_purchase_provenance_unique
    UNIQUE (partial_purchase_provenance),
  ADD CONSTRAINT sale_offers_partial_purchase_provenance_fkey
    FOREIGN KEY (partial_purchase_provenance)
    REFERENCES bond_exchange.purchases (uuid_id) NOT VALID;

ALTER TABLE bond_exchange.purchases
  ADD COLUMN quantity integer NOT NULL DEFAULT 1;

-- Historical purchases consumed historical one-unit offers. Remove the
-- physical default after the metadata-only backfill so the compatibility
-- trigger can resolve an omitted value to a future offer's full quantity.
ALTER TABLE bond_exchange.purchases
  ALTER COLUMN quantity DROP DEFAULT;

CREATE FUNCTION bond_exchange.enforce_purchase_quantity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  offered_quantity integer;
BEGIN
  SELECT quantity INTO offered_quantity
  FROM bond_exchange.sale_offers
  WHERE uuid_id = NEW.sale_offer_uuid;

  IF FOUND AND NEW.quantity IS NULL THEN
    -- A previously deployed writer omits quantity. Preserve its whole-offer
    -- behavior even when it encounters a newly published multi-unit offer.
    NEW.quantity := offered_quantity;
  END IF;

  IF NEW.quantity IS NULL OR NEW.quantity <= 0 THEN
    RAISE EXCEPTION 'purchase quantity must be positive'
      USING ERRCODE = '23514', CONSTRAINT = 'purchases_quantity_positive';
  END IF;
  IF FOUND AND NEW.quantity > offered_quantity THEN
    RAISE EXCEPTION 'purchase quantity exceeds the offer quantity'
      USING ERRCODE = '23514', CONSTRAINT = 'purchases_quantity_within_offer';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER purchases_enforce_quantity
  BEFORE INSERT ON bond_exchange.purchases
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.enforce_purchase_quantity();

ALTER TABLE bond_exchange.purchases
  ADD CONSTRAINT purchases_quantity_positive
    CHECK (quantity > 0) NOT VALID;

CREATE FUNCTION bond_exchange.check_purchase_remainder()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  offered_quantity integer;
  remainder_count integer;
BEGIN
  SELECT quantity INTO STRICT offered_quantity
  FROM bond_exchange.sale_offers
  WHERE uuid_id = NEW.sale_offer_uuid;

  SELECT count(*) INTO remainder_count
  FROM bond_exchange.sale_offers
  WHERE partial_purchase_provenance = NEW.uuid_id;

  IF NEW.quantity < offered_quantity AND remainder_count <> 1 THEN
    RAISE EXCEPTION 'a partial purchase must have exactly one residual offer'
      USING ERRCODE = '23514', CONSTRAINT = 'purchases_partial_remainder_required';
  END IF;
  IF NEW.quantity = offered_quantity AND remainder_count <> 0 THEN
    RAISE EXCEPTION 'a full purchase cannot have a residual offer'
      USING ERRCODE = '23514', CONSTRAINT = 'purchases_full_fill_has_no_remainder';
  END IF;
  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER purchases_check_remainder
  AFTER INSERT ON bond_exchange.purchases
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.check_purchase_remainder();

CREATE FUNCTION bond_exchange.check_residual_offer()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  purchase_quantity integer;
  parent_quantity integer;
  parent_seller uuid;
  parent_bond uuid;
  parent_price bond_exchange.monetary_amount;
  parent_currency text;
  parent_canonical_price bond_exchange.monetary_amount;
  parent_canonical_currency text;
  residual_canonical_price bond_exchange.monetary_amount;
  residual_canonical_currency text;
BEGIN
  IF NEW.partial_purchase_provenance IS NULL THEN
    RETURN NULL;
  END IF;

  SELECT purchase.quantity, parent.quantity, parent.seller_uuid,
         parent.bond_uuid, parent.price, parent.currency_code,
         parent_terms.price, parent_terms.currency_code
  INTO STRICT purchase_quantity, parent_quantity, parent_seller,
       parent_bond, parent_price, parent_currency,
       parent_canonical_price, parent_canonical_currency
  FROM bond_exchange.purchases AS purchase
  JOIN bond_exchange.sale_offers AS parent
    ON parent.uuid_id = purchase.sale_offer_uuid
  JOIN bond_exchange.sale_offer_canonical_terms AS parent_terms
    ON parent_terms.sale_offer_uuid = parent.uuid_id
  WHERE purchase.uuid_id = NEW.partial_purchase_provenance;

  SELECT price, currency_code
  INTO STRICT residual_canonical_price, residual_canonical_currency
  FROM bond_exchange.sale_offer_canonical_terms
  WHERE sale_offer_uuid = NEW.uuid_id;

  IF purchase_quantity >= parent_quantity
     OR NEW.quantity <> parent_quantity - purchase_quantity
     OR NEW.seller_uuid IS DISTINCT FROM parent_seller
     OR NEW.bond_uuid IS DISTINCT FROM parent_bond
     OR NEW.price IS DISTINCT FROM parent_price
     OR NEW.currency_code IS DISTINCT FROM parent_currency
     OR residual_canonical_price IS DISTINCT FROM parent_canonical_price
     OR residual_canonical_currency IS DISTINCT FROM parent_canonical_currency THEN
    RAISE EXCEPTION 'residual offer does not conserve and inherit its parent terms'
      USING ERRCODE = '23514', CONSTRAINT = 'sale_offers_residual_matches_purchase';
  END IF;
  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER sale_offers_check_residual
  AFTER INSERT ON bond_exchange.sale_offers
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.check_residual_offer();

ALTER TABLE bond_exchange.sale_offers
  VALIDATE CONSTRAINT sale_offers_quantity_positive;
ALTER TABLE bond_exchange.sale_offers
  VALIDATE CONSTRAINT sale_offers_partial_purchase_provenance_fkey;
ALTER TABLE bond_exchange.purchases
  VALIDATE CONSTRAINT purchases_quantity_positive;

CREATE OR REPLACE FUNCTION bond_exchange.record_integration_event_on_completion()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
  completed_operation text;
  source_table text;
  source_identifier uuid;
  event_version smallint;
BEGIN
  IF NEW.outcome <> 'succeeded' THEN RETURN NEW; END IF;
  SELECT operation INTO STRICT completed_operation
  FROM bond_exchange.operation_claims WHERE uuid_id = NEW.claim_uuid;
  CASE completed_operation
    WHEN 'offers.create' THEN
      source_table := 'sale_offers';
      source_identifier := NEW.resource_uuid;
      SELECT CASE
        WHEN canonical.sale_offer_uuid IS NULL THEN 1
        WHEN offer.quantity = 1 THEN 2
        ELSE 3
      END
      INTO STRICT event_version
      FROM bond_exchange.sale_offers AS offer
      LEFT JOIN bond_exchange.sale_offer_canonical_terms AS canonical
        ON canonical.sale_offer_uuid = offer.uuid_id
      WHERE offer.uuid_id = source_identifier;
    WHEN 'purchases.buy' THEN
      source_table := 'purchases';
      SELECT purchase.uuid_id,
             CASE
               WHEN canonical.sale_offer_uuid IS NULL THEN 1
               WHEN purchase.quantity = 1 AND residual.uuid_id IS NULL THEN 2
               ELSE 3
             END
      INTO STRICT source_identifier, event_version
      FROM bond_exchange.purchases AS purchase
      LEFT JOIN bond_exchange.sale_offer_canonical_terms AS canonical
        ON canonical.sale_offer_uuid = purchase.sale_offer_uuid
      LEFT JOIN bond_exchange.sale_offers AS residual
        ON residual.partial_purchase_provenance = purchase.uuid_id
      WHERE purchase.sale_offer_uuid = NEW.resource_uuid;
    ELSE RETURN NEW;
  END CASE;
  INSERT INTO bond_exchange.integration_events
    (table_name, source_uuid, schema_version, completed_at)
  VALUES
    (source_table, source_identifier, event_version, NEW.completed_at);
  RETURN NEW;
END;
$$;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'partial-purchase quantities and residual provenance are append-only facts; roll forward';
END;
$$;
