-- migrate:up

-- Refuse contraction if a late legacy writer added evidence after the archive
-- expansion or if mutable coordination still has an active owner.
DO $$
DECLARE
  missing_archive_count bigint;
  active_lease_count bigint;
BEGIN
  SELECT count(*) INTO missing_archive_count
  FROM (
    SELECT 'user'::text AS entity_type, uuid_id AS entity_uuid, 'id'::text AS attribute_name, id AS legacy_value
    FROM bond_exchange.users WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'principal', uuid_id, 'id', id FROM bond_exchange.principals WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'sale_offer', uuid_id, 'id', id FROM bond_exchange.sale_offers WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'principal_suspension', uuid_id, 'id', id FROM bond_exchange.principal_suspensions WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'principal_reinstatement', uuid_id, 'id', id FROM bond_exchange.principal_reinstatements WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'role_permission_grant', uuid_id, 'id', id FROM bond_exchange.role_permission_grants WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'role_permission_revocation', uuid_id, 'id', id FROM bond_exchange.role_permission_revocations WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'principal_role_grant', uuid_id, 'id', id FROM bond_exchange.principal_role_grants WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'principal_role_revocation', uuid_id, 'id', id FROM bond_exchange.principal_role_revocations WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'operation_claim', uuid_id, 'id', id FROM bond_exchange.operation_claims WHERE id <> uuid_id::text
    UNION ALL
    SELECT 'operation_claim', uuid_id, 'idempotency_key', idempotency_key
    FROM bond_exchange.operation_claims WHERE idempotency_nonce IS NULL
    UNION ALL
    SELECT 'sie_exchange_rate_import', uuid_id, 'sequence', id::text
    FROM bond_exchange.sie_exchange_rate_imports
    EXCEPT
    SELECT entity_type, entity_uuid, attribute_name, legacy_value
    FROM bond_exchange.legacy_identifier_archive
  ) AS missing_archive;
  IF missing_archive_count <> 0 THEN
    RAISE EXCEPTION '% non-derivable legacy values are missing from the archive', missing_archive_count;
  END IF;

  SELECT
    (SELECT count(*) FROM bond_exchange.integration_event_deliveries WHERE lease_until > clock_timestamp())
    +
    (SELECT count(*) FROM bond_exchange.sie_exchange_rate_fetch_coordination WHERE lease_until > clock_timestamp())
  INTO active_lease_count;
  IF active_lease_count <> 0 THEN
    RAISE EXCEPTION 'UUID contraction requires a quiescent lease window; % active leases remain', active_lease_count;
  END IF;
END;
$$;

DROP VIEW bond_exchange.active_offers;
DROP VIEW bond_exchange.effective_principal_permissions;
DROP VIEW bond_exchange.current_sie_exchange_rates;

-- Make the transitional view independent of columns being removed so the
-- UUID-aware application deployed before this migration remains compatible.
CREATE OR REPLACE VIEW bond_exchange.active_offers_v2 AS
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

DROP TRIGGER users_sync_identifiers ON bond_exchange.users;
DROP TRIGGER sale_offers_sync_identifiers ON bond_exchange.sale_offers;
DROP TRIGGER purchases_sync_identifiers ON bond_exchange.purchases;
DROP TRIGGER principals_sync_identifiers ON bond_exchange.principals;
DROP TRIGGER principal_suspensions_sync_identifiers ON bond_exchange.principal_suspensions;
DROP TRIGGER principal_reinstatements_sync_identifiers ON bond_exchange.principal_reinstatements;
DROP TRIGGER role_permission_grants_sync_identifiers ON bond_exchange.role_permission_grants;
DROP TRIGGER role_permission_revocations_sync_identifiers ON bond_exchange.role_permission_revocations;
DROP TRIGGER principal_role_grants_sync_identifiers ON bond_exchange.principal_role_grants;
DROP TRIGGER principal_role_revocations_sync_identifiers ON bond_exchange.principal_role_revocations;
DROP TRIGGER operation_claims_sync_identifiers ON bond_exchange.operation_claims;
DROP TRIGGER operation_results_sync_identifiers ON bond_exchange.operation_results;
DROP TRIGGER integration_events_sync_identifiers ON bond_exchange.integration_events;
DROP TRIGGER integration_event_deliveries_sync_identifiers ON bond_exchange.integration_event_deliveries;
DROP TRIGGER sie_exchange_rate_observations_sync_identifiers ON bond_exchange.sie_exchange_rate_observations;
DROP TRIGGER sie_exchange_rate_fetch_sync_lease_nonce ON bond_exchange.sie_exchange_rate_fetch_coordination;

DROP FUNCTION bond_exchange.sync_user_identifiers();
DROP FUNCTION bond_exchange.sync_sale_offer_identifiers();
DROP FUNCTION bond_exchange.sync_purchase_identifiers();
DROP FUNCTION bond_exchange.sync_principal_identifiers();
DROP FUNCTION bond_exchange.sync_principal_suspension_identifiers();
DROP FUNCTION bond_exchange.sync_principal_reinstatement_identifiers();
DROP FUNCTION bond_exchange.sync_role_permission_grant_identifiers();
DROP FUNCTION bond_exchange.sync_role_permission_revocation_identifiers();
DROP FUNCTION bond_exchange.sync_principal_role_grant_identifiers();
DROP FUNCTION bond_exchange.sync_principal_role_revocation_identifiers();
DROP FUNCTION bond_exchange.sync_operation_claim_identifiers();
DROP FUNCTION bond_exchange.sync_operation_result_identifiers();
DROP FUNCTION bond_exchange.sync_integration_event_identifiers();
DROP FUNCTION bond_exchange.sync_event_delivery_identifiers();
DROP FUNCTION bond_exchange.sync_rate_observation_identifiers();
DROP FUNCTION bond_exchange.sync_fetch_lease_nonce();

-- Remove child-side compatibility relationships before parent aliases.
ALTER TABLE bond_exchange.integration_event_deliveries
  DROP COLUMN table_name,
  DROP COLUMN id,
  DROP COLUMN lease_token;
ALTER TABLE bond_exchange.operation_results
  DROP COLUMN claim_id,
  DROP COLUMN resource_id;
ALTER TABLE bond_exchange.principal_role_revocations
  DROP COLUMN id,
  DROP COLUMN grant_id,
  DROP COLUMN revoked_by;
ALTER TABLE bond_exchange.role_permission_revocations
  DROP COLUMN id,
  DROP COLUMN grant_id,
  DROP COLUMN revoked_by;
ALTER TABLE bond_exchange.principal_reinstatements
  DROP COLUMN id,
  DROP COLUMN suspension_id,
  DROP COLUMN reinstated_by;
ALTER TABLE bond_exchange.principal_role_grants
  DROP COLUMN id,
  DROP COLUMN principal_id,
  DROP COLUMN role_id,
  DROP COLUMN granted_by;
ALTER TABLE bond_exchange.role_permission_grants
  DROP COLUMN id,
  DROP COLUMN role_id,
  DROP COLUMN permission_id,
  DROP COLUMN granted_by;
ALTER TABLE bond_exchange.principal_suspensions
  DROP COLUMN id,
  DROP COLUMN principal_id,
  DROP COLUMN suspended_by;
ALTER TABLE bond_exchange.purchases
  DROP COLUMN sale_offer_id,
  DROP COLUMN buyer_id;
ALTER TABLE bond_exchange.integration_events DROP COLUMN id;
ALTER TABLE bond_exchange.operation_claims
  DROP COLUMN id,
  DROP COLUMN principal_id,
  DROP COLUMN idempotency_key;
ALTER TABLE bond_exchange.principals DROP COLUMN id;
ALTER TABLE bond_exchange.sale_offers
  DROP COLUMN id,
  DROP COLUMN seller_id,
  DROP COLUMN bond_series;
ALTER TABLE bond_exchange.users DROP COLUMN id;
ALTER TABLE bond_exchange.sie_exchange_rate_observations DROP COLUMN import_id;
ALTER TABLE bond_exchange.sie_exchange_rate_imports DROP COLUMN id;
ALTER TABLE bond_exchange.sie_exchange_rate_fetch_coordination DROP COLUMN lease_token;

ALTER TABLE bond_exchange.sie_exchange_rate_observations
  RENAME COLUMN id TO revision_sequence;
ALTER TABLE bond_exchange.bonds
  RENAME CONSTRAINT bonds_legacy_series_unique TO bonds_series_unique;

CREATE INDEX sale_offers_bond_uuid_uuid_id_idx
  ON bond_exchange.sale_offers (bond_uuid, uuid_id);

CREATE VIEW bond_exchange.active_offers AS
SELECT * FROM bond_exchange.active_offers_v2;

CREATE VIEW bond_exchange.effective_principal_permissions AS
SELECT * FROM bond_exchange.effective_principal_permissions_v2;

CREATE VIEW bond_exchange.current_sie_exchange_rates AS
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

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION 'legacy identifier columns were archived and contracted; use a corrective forward migration';
END;
$$;
