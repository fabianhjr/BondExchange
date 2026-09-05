-- migrate:up

CREATE TABLE bond_exchange.sale_offer_conversion_quotes (
  uuid_id uuid PRIMARY KEY DEFAULT uuidv7(),
  principal_uuid uuid NOT NULL REFERENCES bond_exchange.principals (uuid_id),
  bond_uuid uuid NOT NULL REFERENCES bond_exchange.bonds (uuid_id),
  submitted_price bond_exchange.monetary_amount NOT NULL,
  submitted_currency_code text NOT NULL,
  canonical_mxn_price bond_exchange.monetary_amount NOT NULL,
  rate_observation_uuid uuid NOT NULL
    REFERENCES bond_exchange.sie_exchange_rate_observations (uuid_id),
  rounding_policy text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  expires_at timestamptz NOT NULL,
  CONSTRAINT sale_offer_conversion_quotes_uuid_v7
    CHECK (uuid_extract_version(uuid_id) = 7),
  CONSTRAINT sale_offer_conversion_quotes_submitted_price_positive
    CHECK (submitted_price > 0),
  CONSTRAINT sale_offer_conversion_quotes_submitted_currency_usd
    CHECK (submitted_currency_code = 'USD'),
  CONSTRAINT sale_offer_conversion_quotes_canonical_price_positive
    CHECK (canonical_mxn_price > 0),
  CONSTRAINT sale_offer_conversion_quotes_rounding_policy_known
    CHECK (rounding_policy = 'half_even_4'),
  CONSTRAINT sale_offer_conversion_quotes_expiry_after_creation
    CHECK (expires_at > created_at)
);

CREATE INDEX sale_offer_conversion_quotes_principal_expiry_idx
  ON bond_exchange.sale_offer_conversion_quotes (principal_uuid, expires_at, uuid_id);

CREATE TABLE bond_exchange.sale_offer_canonical_terms (
  uuid_id uuid PRIMARY KEY DEFAULT uuidv7(),
  sale_offer_uuid uuid NOT NULL UNIQUE
    REFERENCES bond_exchange.sale_offers (uuid_id),
  price bond_exchange.monetary_amount NOT NULL,
  currency_code text NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT sale_offer_canonical_terms_uuid_v7
    CHECK (uuid_extract_version(uuid_id) = 7),
  CONSTRAINT sale_offer_canonical_terms_price_positive CHECK (price > 0),
  CONSTRAINT sale_offer_canonical_terms_currency_mxn CHECK (currency_code = 'MXN')
);

CREATE TABLE bond_exchange.sale_offer_submissions (
  uuid_id uuid PRIMARY KEY DEFAULT uuidv7(),
  sale_offer_uuid uuid NOT NULL UNIQUE
    REFERENCES bond_exchange.sale_offers (uuid_id),
  submitted_price bond_exchange.monetary_amount NOT NULL,
  submitted_currency_code text NOT NULL,
  conversion_quote_uuid uuid UNIQUE
    REFERENCES bond_exchange.sale_offer_conversion_quotes (uuid_id),
  submitted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT sale_offer_submissions_uuid_v7
    CHECK (uuid_extract_version(uuid_id) = 7),
  CONSTRAINT sale_offer_submissions_price_positive CHECK (submitted_price > 0),
  CONSTRAINT sale_offer_submissions_currency_supported
    CHECK (submitted_currency_code IN ('MXN', 'USD')),
  CONSTRAINT sale_offer_submissions_conversion_shape CHECK (
    (submitted_currency_code = 'MXN' AND conversion_quote_uuid IS NULL)
    OR
    (submitted_currency_code = 'USD' AND conversion_quote_uuid IS NOT NULL)
  )
);

CREATE TRIGGER sale_offer_conversion_quotes_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.sale_offer_conversion_quotes
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();
CREATE TRIGGER sale_offer_canonical_terms_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.sale_offer_canonical_terms
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();
CREATE TRIGGER sale_offer_submissions_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.sale_offer_submissions
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

REVOKE UPDATE, DELETE, TRUNCATE
  ON bond_exchange.sale_offer_conversion_quotes,
     bond_exchange.sale_offer_canonical_terms,
     bond_exchange.sale_offer_submissions
  FROM PUBLIC;

-- Existing MXN facts have an unambiguous identity conversion. Non-MXN facts
-- intentionally remain without canonical terms until their seller accepts an
-- explicit conversion; the application fails closed for those legacy rows.
INSERT INTO bond_exchange.sale_offer_canonical_terms
  (sale_offer_uuid, price, currency_code)
SELECT uuid_id, price, 'MXN'
FROM bond_exchange.sale_offers
WHERE currency_code = 'MXN';

INSERT INTO bond_exchange.sale_offer_submissions
  (sale_offer_uuid, submitted_price, submitted_currency_code)
SELECT uuid_id, price, 'MXN'
FROM bond_exchange.sale_offers
WHERE currency_code = 'MXN';

INSERT INTO bond_exchange.permissions (code, description)
VALUES ('offers.quote', 'Create an expiring USD-to-MXN sale-offer conversion quote.');

INSERT INTO bond_exchange.role_permission_grants
  (role_uuid, permission_uuid, reason)
SELECT role.uuid_id, permission.uuid_id,
       'Allow traders to preview and accept canonical MXN offer terms.'
FROM bond_exchange.roles AS role
CROSS JOIN bond_exchange.permissions AS permission
WHERE role.code = 'trader' AND permission.code = 'offers.quote';

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
      SELECT CASE WHEN EXISTS (
        SELECT 1 FROM bond_exchange.sale_offer_canonical_terms
        WHERE sale_offer_uuid = source_identifier
      ) THEN 2 ELSE 1 END INTO event_version;
    WHEN 'purchases.buy' THEN
      source_table := 'purchases';
      SELECT purchase.uuid_id,
             CASE WHEN canonical.sale_offer_uuid IS NULL THEN 1 ELSE 2 END
      INTO STRICT source_identifier, event_version
      FROM bond_exchange.purchases AS purchase
      LEFT JOIN bond_exchange.sale_offer_canonical_terms AS canonical
        ON canonical.sale_offer_uuid = purchase.sale_offer_uuid
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
    'canonical MXN terms and submission provenance are append-only facts; roll forward';
END;
$$;
