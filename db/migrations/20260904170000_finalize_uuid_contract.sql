-- migrate:up

ALTER TABLE bond_exchange.roles RENAME COLUMN id TO code;
ALTER TABLE bond_exchange.roles
  RENAME CONSTRAINT roles_id_canonical TO roles_code_canonical;
ALTER TABLE bond_exchange.permissions RENAME COLUMN id TO code;
ALTER TABLE bond_exchange.permissions
  RENAME CONSTRAINT permissions_id_canonical TO permissions_code_canonical;

CREATE OR REPLACE VIEW bond_exchange.active_offers AS
SELECT
  sale_offer.uuid_id AS id,
  sale_offer.seller_uuid AS seller_id,
  bond.series AS bond_series,
  sale_offer.price,
  sale_offer.currency_code
FROM bond_exchange.sale_offers AS sale_offer
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = sale_offer.bond_uuid
WHERE NOT EXISTS (
  SELECT 1 FROM bond_exchange.purchases AS purchase
  WHERE purchase.sale_offer_uuid = sale_offer.uuid_id
);

CREATE OR REPLACE VIEW bond_exchange.effective_principal_permissions AS
SELECT DISTINCT
  principal_role_grant.principal_uuid AS principal_id,
  role_permission_grant.permission_uuid AS permission_id,
  permission.code AS permission_code
FROM bond_exchange.principal_role_grants AS principal_role_grant
JOIN bond_exchange.role_permission_grants AS role_permission_grant
  ON role_permission_grant.role_uuid = principal_role_grant.role_uuid
JOIN bond_exchange.permissions AS permission
  ON permission.uuid_id = role_permission_grant.permission_uuid
WHERE NOT EXISTS (
  SELECT 1 FROM bond_exchange.principal_role_revocations AS revocation
  WHERE revocation.grant_uuid = principal_role_grant.uuid_id
)
AND NOT EXISTS (
  SELECT 1 FROM bond_exchange.role_permission_revocations AS revocation
  WHERE revocation.grant_uuid = role_permission_grant.uuid_id
)
AND NOT EXISTS (
  SELECT 1 FROM bond_exchange.principal_suspensions AS suspension
  WHERE suspension.principal_uuid = principal_role_grant.principal_uuid
    AND NOT EXISTS (
      SELECT 1 FROM bond_exchange.principal_reinstatements AS reinstatement
      WHERE reinstatement.suspension_uuid = suspension.uuid_id
    )
);

CREATE OR REPLACE VIEW bond_exchange.current_sie_exchange_rates AS
SELECT DISTINCT ON (series_id, base_currency, quote_currency, observed_on)
  series_id,
  base_currency,
  quote_currency,
  observed_on,
  value,
  recorded_at,
  uuid_id AS revision_id,
  revision_sequence
FROM bond_exchange.sie_exchange_rate_observations
ORDER BY series_id, base_currency, quote_currency, observed_on, revision_sequence DESC;

DROP VIEW bond_exchange.active_offers_v2;
DROP VIEW bond_exchange.effective_principal_permissions_v2;
DROP VIEW bond_exchange.current_sie_exchange_rates_v2;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION 'canonical UUID views are deployed; use a corrective forward migration';
END;
$$;
