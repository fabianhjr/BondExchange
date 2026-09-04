-- migrate:up

-- PostgreSQL 18 supplies uuidv7(), uuidv4(), and uuid_extract_version().
-- Legacy keys remain unique and writable so the previously deployed
-- application can run throughout this expand-and-cutover migration.

ALTER TABLE bond_exchange.users
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7();
ALTER TABLE bond_exchange.bonds
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7();
ALTER TABLE bond_exchange.sale_offers
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN seller_uuid uuid,
  ADD COLUMN bond_uuid uuid;
ALTER TABLE bond_exchange.purchases
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN sale_offer_uuid uuid,
  ADD COLUMN buyer_uuid uuid;

ALTER TABLE bond_exchange.principals
  ADD COLUMN uuid_id uuid;
ALTER TABLE bond_exchange.principal_suspensions
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN principal_uuid uuid,
  ADD COLUMN suspended_by_uuid uuid;
ALTER TABLE bond_exchange.principal_reinstatements
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN suspension_uuid uuid,
  ADD COLUMN reinstated_by_uuid uuid;
ALTER TABLE bond_exchange.roles
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7();
ALTER TABLE bond_exchange.permissions
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7();
ALTER TABLE bond_exchange.role_permission_grants
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN role_uuid uuid,
  ADD COLUMN permission_uuid uuid,
  ADD COLUMN granted_by_uuid uuid;
ALTER TABLE bond_exchange.role_permission_revocations
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN grant_uuid uuid,
  ADD COLUMN revoked_by_uuid uuid;
ALTER TABLE bond_exchange.principal_role_grants
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN principal_uuid uuid,
  ADD COLUMN role_uuid uuid,
  ADD COLUMN granted_by_uuid uuid;
ALTER TABLE bond_exchange.principal_role_revocations
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN grant_uuid uuid,
  ADD COLUMN revoked_by_uuid uuid;
ALTER TABLE bond_exchange.operation_claims
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN principal_uuid uuid,
  ADD COLUMN idempotency_nonce uuid;
ALTER TABLE bond_exchange.operation_results
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN claim_uuid uuid,
  ADD COLUMN resource_uuid uuid;

ALTER TABLE bond_exchange.integration_events
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN source_uuid uuid;
ALTER TABLE bond_exchange.integration_event_deliveries
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN event_uuid uuid,
  ADD COLUMN lease_nonce uuid;

ALTER TABLE bond_exchange.sie_exchange_rate_imports
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7();
ALTER TABLE bond_exchange.sie_exchange_rate_observations
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN import_uuid uuid;
ALTER TABLE bond_exchange.sie_exchange_rate_fetch_coordination
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN lease_nonce uuid;
ALTER TABLE bond_exchange.sie_provider_state
  ADD COLUMN uuid_id uuid NOT NULL DEFAULT uuidv7();

-- Backfill relationship metadata under the migration owner. These updates add
-- identifiers without changing any established domain or security fact.
ALTER TABLE bond_exchange.sale_offers DISABLE TRIGGER sale_offers_are_append_only;
UPDATE bond_exchange.sale_offers AS offer
SET seller_uuid = seller.uuid_id,
    bond_uuid = bond.uuid_id
FROM bond_exchange.users AS seller,
     bond_exchange.bonds AS bond
WHERE seller.id = offer.seller_id
  AND bond.series = offer.bond_series;
ALTER TABLE bond_exchange.sale_offers ENABLE TRIGGER sale_offers_are_append_only;

ALTER TABLE bond_exchange.purchases DISABLE TRIGGER purchases_are_append_only;
UPDATE bond_exchange.purchases AS purchase
SET sale_offer_uuid = offer.uuid_id,
    buyer_uuid = buyer.uuid_id
FROM bond_exchange.sale_offers AS offer,
     bond_exchange.users AS buyer
WHERE offer.id = purchase.sale_offer_id
  AND buyer.id = purchase.buyer_id;
ALTER TABLE bond_exchange.purchases ENABLE TRIGGER purchases_are_append_only;

ALTER TABLE bond_exchange.principals DISABLE TRIGGER principals_are_append_only;
UPDATE bond_exchange.principals AS principal
SET uuid_id = app_user.uuid_id
FROM bond_exchange.users AS app_user
WHERE app_user.id = principal.id;
ALTER TABLE bond_exchange.principals ENABLE TRIGGER principals_are_append_only;

ALTER TABLE bond_exchange.principal_suspensions DISABLE TRIGGER principal_suspensions_are_append_only;
UPDATE bond_exchange.principal_suspensions AS suspension
SET principal_uuid = principal.uuid_id,
    suspended_by_uuid = (
      SELECT actor.uuid_id
      FROM bond_exchange.principals AS actor
      WHERE actor.id = suspension.suspended_by
    )
FROM bond_exchange.principals AS principal
WHERE principal.id = suspension.principal_id;
ALTER TABLE bond_exchange.principal_suspensions ENABLE TRIGGER principal_suspensions_are_append_only;

ALTER TABLE bond_exchange.principal_reinstatements DISABLE TRIGGER principal_reinstatements_are_append_only;
UPDATE bond_exchange.principal_reinstatements AS reinstatement
SET suspension_uuid = suspension.uuid_id,
    reinstated_by_uuid = (
      SELECT actor.uuid_id
      FROM bond_exchange.principals AS actor
      WHERE actor.id = reinstatement.reinstated_by
    )
FROM bond_exchange.principal_suspensions AS suspension
WHERE suspension.id = reinstatement.suspension_id;
ALTER TABLE bond_exchange.principal_reinstatements ENABLE TRIGGER principal_reinstatements_are_append_only;

ALTER TABLE bond_exchange.role_permission_grants DISABLE TRIGGER role_permission_grants_are_append_only;
UPDATE bond_exchange.role_permission_grants AS permission_grant
SET role_uuid = role.uuid_id,
    permission_uuid = permission.uuid_id,
    granted_by_uuid = (
      SELECT actor.uuid_id
      FROM bond_exchange.principals AS actor
      WHERE actor.id = permission_grant.granted_by
    )
FROM bond_exchange.roles AS role,
     bond_exchange.permissions AS permission
WHERE role.id = permission_grant.role_id
  AND permission.id = permission_grant.permission_id;
ALTER TABLE bond_exchange.role_permission_grants ENABLE TRIGGER role_permission_grants_are_append_only;

ALTER TABLE bond_exchange.role_permission_revocations DISABLE TRIGGER role_permission_revocations_are_append_only;
UPDATE bond_exchange.role_permission_revocations AS revocation
SET grant_uuid = permission_grant.uuid_id,
    revoked_by_uuid = (
      SELECT actor.uuid_id
      FROM bond_exchange.principals AS actor
      WHERE actor.id = revocation.revoked_by
    )
FROM bond_exchange.role_permission_grants AS permission_grant
WHERE permission_grant.id = revocation.grant_id;
ALTER TABLE bond_exchange.role_permission_revocations ENABLE TRIGGER role_permission_revocations_are_append_only;

ALTER TABLE bond_exchange.principal_role_grants DISABLE TRIGGER principal_role_grants_are_append_only;
UPDATE bond_exchange.principal_role_grants AS role_grant
SET principal_uuid = principal.uuid_id,
    role_uuid = role.uuid_id,
    granted_by_uuid = (
      SELECT actor.uuid_id
      FROM bond_exchange.principals AS actor
      WHERE actor.id = role_grant.granted_by
    )
FROM bond_exchange.principals AS principal,
     bond_exchange.roles AS role
WHERE principal.id = role_grant.principal_id
  AND role.id = role_grant.role_id;
ALTER TABLE bond_exchange.principal_role_grants ENABLE TRIGGER principal_role_grants_are_append_only;

ALTER TABLE bond_exchange.principal_role_revocations DISABLE TRIGGER principal_role_revocations_are_append_only;
UPDATE bond_exchange.principal_role_revocations AS revocation
SET grant_uuid = role_grant.uuid_id,
    revoked_by_uuid = (
      SELECT actor.uuid_id
      FROM bond_exchange.principals AS actor
      WHERE actor.id = revocation.revoked_by
    )
FROM bond_exchange.principal_role_grants AS role_grant
WHERE role_grant.id = revocation.grant_id;
ALTER TABLE bond_exchange.principal_role_revocations ENABLE TRIGGER principal_role_revocations_are_append_only;

ALTER TABLE bond_exchange.operation_claims DISABLE TRIGGER operation_claims_are_append_only;
UPDATE bond_exchange.operation_claims AS claim
SET principal_uuid = principal.uuid_id,
    idempotency_nonce = CASE
      WHEN claim.idempotency_key ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
        THEN claim.idempotency_key::uuid
      ELSE NULL
    END
FROM bond_exchange.principals AS principal
WHERE principal.id = claim.principal_id;
ALTER TABLE bond_exchange.operation_claims ENABLE TRIGGER operation_claims_are_append_only;

ALTER TABLE bond_exchange.operation_results DISABLE TRIGGER operation_results_are_append_only;
UPDATE bond_exchange.operation_results AS result
SET claim_uuid = claim.uuid_id,
    resource_uuid = (
      SELECT offer.uuid_id
      FROM bond_exchange.sale_offers AS offer
      WHERE offer.id = result.resource_id
    )
FROM bond_exchange.operation_claims AS claim
WHERE claim.id = result.claim_id;
ALTER TABLE bond_exchange.operation_results ENABLE TRIGGER operation_results_are_append_only;

ALTER TABLE bond_exchange.integration_events DISABLE TRIGGER integration_events_are_append_only;
UPDATE bond_exchange.integration_events AS event
SET source_uuid = CASE event.table_name
  WHEN 'sale_offers' THEN (
    SELECT offer.uuid_id FROM bond_exchange.sale_offers AS offer
    WHERE offer.id = event.id
  )
  WHEN 'purchases' THEN (
    SELECT purchase.uuid_id FROM bond_exchange.purchases AS purchase
    WHERE purchase.sale_offer_id = event.id
  )
END;
ALTER TABLE bond_exchange.integration_events ENABLE TRIGGER integration_events_are_append_only;

UPDATE bond_exchange.integration_event_deliveries AS delivery
SET event_uuid = event.uuid_id,
    lease_nonce = CASE
      WHEN delivery.lease_token ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
        THEN delivery.lease_token::uuid
      ELSE NULL
    END
FROM bond_exchange.integration_events AS event
WHERE event.table_name = delivery.table_name
  AND event.id = delivery.id;

ALTER TABLE bond_exchange.sie_exchange_rate_observations DISABLE TRIGGER sie_exchange_rate_observations_are_append_only;
UPDATE bond_exchange.sie_exchange_rate_observations AS observation
SET import_uuid = import.uuid_id
FROM bond_exchange.sie_exchange_rate_imports AS import
WHERE import.id = observation.import_id;
ALTER TABLE bond_exchange.sie_exchange_rate_observations ENABLE TRIGGER sie_exchange_rate_observations_are_append_only;

UPDATE bond_exchange.sie_exchange_rate_fetch_coordination
SET lease_nonce = CASE
  WHEN lease_token ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    THEN lease_token::uuid
  ELSE NULL
END;

ALTER TABLE bond_exchange.sale_offers
  ALTER COLUMN seller_uuid SET NOT NULL,
  ALTER COLUMN bond_uuid SET NOT NULL;
ALTER TABLE bond_exchange.purchases
  ALTER COLUMN sale_offer_uuid SET NOT NULL,
  ALTER COLUMN buyer_uuid SET NOT NULL;
ALTER TABLE bond_exchange.principals ALTER COLUMN uuid_id SET NOT NULL;
ALTER TABLE bond_exchange.principal_suspensions ALTER COLUMN principal_uuid SET NOT NULL;
ALTER TABLE bond_exchange.principal_reinstatements ALTER COLUMN suspension_uuid SET NOT NULL;
ALTER TABLE bond_exchange.role_permission_grants
  ALTER COLUMN role_uuid SET NOT NULL,
  ALTER COLUMN permission_uuid SET NOT NULL;
ALTER TABLE bond_exchange.role_permission_revocations ALTER COLUMN grant_uuid SET NOT NULL;
ALTER TABLE bond_exchange.principal_role_grants
  ALTER COLUMN principal_uuid SET NOT NULL,
  ALTER COLUMN role_uuid SET NOT NULL;
ALTER TABLE bond_exchange.principal_role_revocations ALTER COLUMN grant_uuid SET NOT NULL;
ALTER TABLE bond_exchange.operation_claims ALTER COLUMN principal_uuid SET NOT NULL;
ALTER TABLE bond_exchange.operation_results ALTER COLUMN claim_uuid SET NOT NULL;
ALTER TABLE bond_exchange.integration_events ALTER COLUMN source_uuid SET NOT NULL;
ALTER TABLE bond_exchange.integration_event_deliveries ALTER COLUMN event_uuid SET NOT NULL;
ALTER TABLE bond_exchange.sie_exchange_rate_observations ALTER COLUMN import_uuid SET NOT NULL;

-- Keep legacy keys unique before moving primary-key ownership to UUIDv7.
ALTER TABLE bond_exchange.users ADD CONSTRAINT users_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.bonds ADD CONSTRAINT bonds_legacy_series_unique UNIQUE (series);
ALTER TABLE bond_exchange.sale_offers ADD CONSTRAINT sale_offers_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.purchases ADD CONSTRAINT purchases_legacy_offer_unique UNIQUE (sale_offer_id);
ALTER TABLE bond_exchange.principals ADD CONSTRAINT principals_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.principal_suspensions ADD CONSTRAINT principal_suspensions_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.principal_reinstatements ADD CONSTRAINT principal_reinstatements_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.roles ADD CONSTRAINT roles_code_unique UNIQUE (id);
ALTER TABLE bond_exchange.permissions ADD CONSTRAINT permissions_code_unique UNIQUE (id);
ALTER TABLE bond_exchange.role_permission_grants ADD CONSTRAINT role_permission_grants_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.role_permission_revocations ADD CONSTRAINT role_permission_revocations_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.principal_role_grants ADD CONSTRAINT principal_role_grants_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.principal_role_revocations ADD CONSTRAINT principal_role_revocations_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.operation_claims ADD CONSTRAINT operation_claims_legacy_id_unique UNIQUE (id);
ALTER TABLE bond_exchange.operation_results ADD CONSTRAINT operation_results_legacy_claim_unique UNIQUE (claim_id);
ALTER TABLE bond_exchange.integration_events ADD CONSTRAINT integration_events_legacy_source_unique UNIQUE (table_name, id);
ALTER TABLE bond_exchange.integration_event_deliveries ADD CONSTRAINT integration_event_deliveries_legacy_scope_unique UNIQUE (destination_id, table_name, id);
ALTER TABLE bond_exchange.sie_exchange_rate_imports ADD CONSTRAINT sie_exchange_rate_imports_legacy_sequence_unique UNIQUE (id);
ALTER TABLE bond_exchange.sie_exchange_rate_observations ADD CONSTRAINT sie_exchange_rate_observations_revision_sequence_unique UNIQUE (id);
ALTER TABLE bond_exchange.sie_exchange_rate_fetch_coordination ADD CONSTRAINT sie_exchange_rate_fetch_work_key_unique UNIQUE (work_key);
ALTER TABLE bond_exchange.sie_provider_state ADD CONSTRAINT sie_provider_state_provider_unique UNIQUE (provider_id);

-- Remove legacy foreign keys temporarily so their referenced primary keys can
-- be replaced. Equivalent legacy foreign keys are restored below.
ALTER TABLE bond_exchange.sale_offers
  DROP CONSTRAINT sale_offers_seller_id_fkey,
  DROP CONSTRAINT sale_offers_bond_series_fkey;
ALTER TABLE bond_exchange.purchases
  DROP CONSTRAINT purchases_sale_offer_id_fkey,
  DROP CONSTRAINT purchases_buyer_id_fkey;
ALTER TABLE bond_exchange.principals DROP CONSTRAINT principals_id_fkey;
ALTER TABLE bond_exchange.principal_suspensions
  DROP CONSTRAINT principal_suspensions_principal_id_fkey,
  DROP CONSTRAINT principal_suspensions_suspended_by_fkey;
ALTER TABLE bond_exchange.principal_reinstatements
  DROP CONSTRAINT principal_reinstatements_suspension_id_fkey,
  DROP CONSTRAINT principal_reinstatements_reinstated_by_fkey;
ALTER TABLE bond_exchange.role_permission_grants
  DROP CONSTRAINT role_permission_grants_role_id_fkey,
  DROP CONSTRAINT role_permission_grants_permission_id_fkey,
  DROP CONSTRAINT role_permission_grants_granted_by_fkey;
ALTER TABLE bond_exchange.role_permission_revocations
  DROP CONSTRAINT role_permission_revocations_grant_id_fkey,
  DROP CONSTRAINT role_permission_revocations_revoked_by_fkey;
ALTER TABLE bond_exchange.principal_role_grants
  DROP CONSTRAINT principal_role_grants_principal_id_fkey,
  DROP CONSTRAINT principal_role_grants_role_id_fkey,
  DROP CONSTRAINT principal_role_grants_granted_by_fkey;
ALTER TABLE bond_exchange.principal_role_revocations
  DROP CONSTRAINT principal_role_revocations_grant_id_fkey,
  DROP CONSTRAINT principal_role_revocations_revoked_by_fkey;
ALTER TABLE bond_exchange.operation_claims DROP CONSTRAINT operation_claims_principal_id_fkey;
ALTER TABLE bond_exchange.operation_results DROP CONSTRAINT operation_results_claim_id_fkey;
ALTER TABLE bond_exchange.integration_event_deliveries DROP CONSTRAINT integration_event_deliveries_event_foreign_key;
ALTER TABLE bond_exchange.sie_exchange_rate_observations DROP CONSTRAINT sie_exchange_rate_observations_import_id_fkey;

ALTER TABLE bond_exchange.users DROP CONSTRAINT users_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.bonds DROP CONSTRAINT bonds_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.sale_offers DROP CONSTRAINT sale_offers_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.purchases DROP CONSTRAINT purchases_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.principals DROP CONSTRAINT principals_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.principal_suspensions DROP CONSTRAINT principal_suspensions_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.principal_reinstatements DROP CONSTRAINT principal_reinstatements_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.roles DROP CONSTRAINT roles_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.permissions DROP CONSTRAINT permissions_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.role_permission_grants DROP CONSTRAINT role_permission_grants_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.role_permission_revocations DROP CONSTRAINT role_permission_revocations_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.principal_role_grants DROP CONSTRAINT principal_role_grants_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.principal_role_revocations DROP CONSTRAINT principal_role_revocations_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.operation_claims DROP CONSTRAINT operation_claims_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.operation_results DROP CONSTRAINT operation_results_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.integration_events DROP CONSTRAINT integration_events_primary_key, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.integration_event_deliveries DROP CONSTRAINT integration_event_deliveries_primary_key, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.sie_exchange_rate_imports DROP CONSTRAINT sie_exchange_rate_imports_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.sie_exchange_rate_observations DROP CONSTRAINT sie_exchange_rate_observations_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.sie_exchange_rate_fetch_coordination DROP CONSTRAINT sie_exchange_rate_fetch_coordination_pkey, ADD PRIMARY KEY (uuid_id);
ALTER TABLE bond_exchange.sie_provider_state DROP CONSTRAINT sie_provider_state_pkey, ADD PRIMARY KEY (uuid_id);

-- Preserve the legacy relationship graph for rolling compatibility.
ALTER TABLE bond_exchange.sale_offers
  ADD FOREIGN KEY (seller_id) REFERENCES bond_exchange.users (id),
  ADD FOREIGN KEY (bond_series) REFERENCES bond_exchange.bonds (series);
ALTER TABLE bond_exchange.purchases
  ADD FOREIGN KEY (sale_offer_id) REFERENCES bond_exchange.sale_offers (id),
  ADD FOREIGN KEY (buyer_id) REFERENCES bond_exchange.users (id);
ALTER TABLE bond_exchange.principals ADD FOREIGN KEY (id) REFERENCES bond_exchange.users (id);
ALTER TABLE bond_exchange.principal_suspensions
  ADD FOREIGN KEY (principal_id) REFERENCES bond_exchange.principals (id),
  ADD FOREIGN KEY (suspended_by) REFERENCES bond_exchange.principals (id);
ALTER TABLE bond_exchange.principal_reinstatements
  ADD FOREIGN KEY (suspension_id) REFERENCES bond_exchange.principal_suspensions (id),
  ADD FOREIGN KEY (reinstated_by) REFERENCES bond_exchange.principals (id);
ALTER TABLE bond_exchange.role_permission_grants
  ADD FOREIGN KEY (role_id) REFERENCES bond_exchange.roles (id),
  ADD FOREIGN KEY (permission_id) REFERENCES bond_exchange.permissions (id),
  ADD FOREIGN KEY (granted_by) REFERENCES bond_exchange.principals (id);
ALTER TABLE bond_exchange.role_permission_revocations
  ADD FOREIGN KEY (grant_id) REFERENCES bond_exchange.role_permission_grants (id),
  ADD FOREIGN KEY (revoked_by) REFERENCES bond_exchange.principals (id);
ALTER TABLE bond_exchange.principal_role_grants
  ADD FOREIGN KEY (principal_id) REFERENCES bond_exchange.principals (id),
  ADD FOREIGN KEY (role_id) REFERENCES bond_exchange.roles (id),
  ADD FOREIGN KEY (granted_by) REFERENCES bond_exchange.principals (id);
ALTER TABLE bond_exchange.principal_role_revocations
  ADD FOREIGN KEY (grant_id) REFERENCES bond_exchange.principal_role_grants (id),
  ADD FOREIGN KEY (revoked_by) REFERENCES bond_exchange.principals (id);
ALTER TABLE bond_exchange.operation_claims ADD FOREIGN KEY (principal_id) REFERENCES bond_exchange.principals (id);
ALTER TABLE bond_exchange.operation_results ADD FOREIGN KEY (claim_id) REFERENCES bond_exchange.operation_claims (id);
ALTER TABLE bond_exchange.integration_event_deliveries
  ADD FOREIGN KEY (table_name, id) REFERENCES bond_exchange.integration_events (table_name, id);
ALTER TABLE bond_exchange.sie_exchange_rate_observations
  ADD FOREIGN KEY (import_id) REFERENCES bond_exchange.sie_exchange_rate_imports (id);

-- UUID relationships are authoritative for the new application.
ALTER TABLE bond_exchange.sale_offers
  ADD FOREIGN KEY (seller_uuid) REFERENCES bond_exchange.users (uuid_id),
  ADD FOREIGN KEY (bond_uuid) REFERENCES bond_exchange.bonds (uuid_id);
ALTER TABLE bond_exchange.purchases
  ADD CONSTRAINT purchases_sale_offer_uuid_unique UNIQUE (sale_offer_uuid),
  ADD FOREIGN KEY (sale_offer_uuid) REFERENCES bond_exchange.sale_offers (uuid_id),
  ADD FOREIGN KEY (buyer_uuid) REFERENCES bond_exchange.users (uuid_id);
ALTER TABLE bond_exchange.principals ADD FOREIGN KEY (uuid_id) REFERENCES bond_exchange.users (uuid_id);
ALTER TABLE bond_exchange.principal_suspensions
  ADD FOREIGN KEY (principal_uuid) REFERENCES bond_exchange.principals (uuid_id),
  ADD FOREIGN KEY (suspended_by_uuid) REFERENCES bond_exchange.principals (uuid_id);
ALTER TABLE bond_exchange.principal_reinstatements
  ADD CONSTRAINT principal_reinstatements_suspension_uuid_unique UNIQUE (suspension_uuid),
  ADD FOREIGN KEY (suspension_uuid) REFERENCES bond_exchange.principal_suspensions (uuid_id),
  ADD FOREIGN KEY (reinstated_by_uuid) REFERENCES bond_exchange.principals (uuid_id);
ALTER TABLE bond_exchange.role_permission_grants
  ADD FOREIGN KEY (role_uuid) REFERENCES bond_exchange.roles (uuid_id),
  ADD FOREIGN KEY (permission_uuid) REFERENCES bond_exchange.permissions (uuid_id),
  ADD FOREIGN KEY (granted_by_uuid) REFERENCES bond_exchange.principals (uuid_id);
ALTER TABLE bond_exchange.role_permission_revocations
  ADD CONSTRAINT role_permission_revocations_grant_uuid_unique UNIQUE (grant_uuid),
  ADD FOREIGN KEY (grant_uuid) REFERENCES bond_exchange.role_permission_grants (uuid_id),
  ADD FOREIGN KEY (revoked_by_uuid) REFERENCES bond_exchange.principals (uuid_id);
ALTER TABLE bond_exchange.principal_role_grants
  ADD FOREIGN KEY (principal_uuid) REFERENCES bond_exchange.principals (uuid_id),
  ADD FOREIGN KEY (role_uuid) REFERENCES bond_exchange.roles (uuid_id),
  ADD FOREIGN KEY (granted_by_uuid) REFERENCES bond_exchange.principals (uuid_id);
ALTER TABLE bond_exchange.principal_role_revocations
  ADD CONSTRAINT principal_role_revocations_grant_uuid_unique UNIQUE (grant_uuid),
  ADD FOREIGN KEY (grant_uuid) REFERENCES bond_exchange.principal_role_grants (uuid_id),
  ADD FOREIGN KEY (revoked_by_uuid) REFERENCES bond_exchange.principals (uuid_id);
ALTER TABLE bond_exchange.operation_claims
  ADD FOREIGN KEY (principal_uuid) REFERENCES bond_exchange.principals (uuid_id),
  ADD CONSTRAINT operation_claims_nonce_scope_unique
    UNIQUE (principal_uuid, client_id, operation, idempotency_nonce);
ALTER TABLE bond_exchange.operation_results
  ADD CONSTRAINT operation_results_claim_uuid_unique UNIQUE (claim_uuid),
  ADD FOREIGN KEY (claim_uuid) REFERENCES bond_exchange.operation_claims (uuid_id);
ALTER TABLE bond_exchange.integration_events
  ADD CONSTRAINT integration_events_source_uuid_unique UNIQUE (table_name, source_uuid);
ALTER TABLE bond_exchange.integration_event_deliveries
  ADD CONSTRAINT integration_event_deliveries_event_destination_unique UNIQUE (destination_id, event_uuid),
  ADD FOREIGN KEY (event_uuid) REFERENCES bond_exchange.integration_events (uuid_id);
ALTER TABLE bond_exchange.sie_exchange_rate_observations
  ADD FOREIGN KEY (import_uuid) REFERENCES bond_exchange.sie_exchange_rate_imports (uuid_id),
  ADD CONSTRAINT sie_exchange_rate_observations_uuid_revision_unique
    UNIQUE (import_uuid, series_id, base_currency, quote_currency, observed_on);

CREATE INDEX sale_offers_bond_series_uuid_id_idx
  ON bond_exchange.sale_offers (bond_series, uuid_id);
CREATE INDEX purchases_buyer_uuid_sale_offer_uuid_idx
  ON bond_exchange.purchases (buyer_uuid, sale_offer_uuid);
CREATE INDEX principal_role_grants_principal_uuid_role_uuid_idx
  ON bond_exchange.principal_role_grants (principal_uuid, role_uuid);
CREATE INDEX role_permission_grants_role_uuid_permission_uuid_idx
  ON bond_exchange.role_permission_grants (role_uuid, permission_uuid);

-- Enforce UUID versions independently of their generation defaults.
DO $$
DECLARE
  target regclass;
  constraint_name text;
BEGIN
  FOR target, constraint_name IN
    SELECT table_name::regclass, check_name
    FROM (VALUES
      ('bond_exchange.users', 'users_uuid_v7'),
      ('bond_exchange.bonds', 'bonds_uuid_v7'),
      ('bond_exchange.sale_offers', 'sale_offers_uuid_v7'),
      ('bond_exchange.purchases', 'purchases_uuid_v7'),
      ('bond_exchange.principals', 'principals_uuid_v7'),
      ('bond_exchange.principal_suspensions', 'principal_suspensions_uuid_v7'),
      ('bond_exchange.principal_reinstatements', 'principal_reinstatements_uuid_v7'),
      ('bond_exchange.roles', 'roles_uuid_v7'),
      ('bond_exchange.permissions', 'permissions_uuid_v7'),
      ('bond_exchange.role_permission_grants', 'role_permission_grants_uuid_v7'),
      ('bond_exchange.role_permission_revocations', 'role_permission_revocations_uuid_v7'),
      ('bond_exchange.principal_role_grants', 'principal_role_grants_uuid_v7'),
      ('bond_exchange.principal_role_revocations', 'principal_role_revocations_uuid_v7'),
      ('bond_exchange.operation_claims', 'operation_claims_uuid_v7'),
      ('bond_exchange.operation_results', 'operation_results_uuid_v7'),
      ('bond_exchange.integration_events', 'integration_events_uuid_v7'),
      ('bond_exchange.integration_event_deliveries', 'integration_event_deliveries_uuid_v7'),
      ('bond_exchange.sie_exchange_rate_imports', 'sie_exchange_rate_imports_uuid_v7'),
      ('bond_exchange.sie_exchange_rate_observations', 'sie_exchange_rate_observations_uuid_v7'),
      ('bond_exchange.sie_exchange_rate_fetch_coordination', 'sie_exchange_rate_fetch_coordination_uuid_v7'),
      ('bond_exchange.sie_provider_state', 'sie_provider_state_uuid_v7')
    ) AS checks(table_name, check_name)
  LOOP
    EXECUTE format(
      'ALTER TABLE %s ADD CONSTRAINT %I CHECK (uuid_extract_version(uuid_id) = 7)',
      target,
      constraint_name
    );
  END LOOP;
END;
$$;

ALTER TABLE bond_exchange.operation_claims
  ADD CONSTRAINT operation_claims_nonce_v4
    CHECK (idempotency_nonce IS NULL OR uuid_extract_version(idempotency_nonce) = 4),
  ADD CONSTRAINT operation_claims_nonce_legacy_match
    CHECK (idempotency_nonce IS NULL OR idempotency_key = idempotency_nonce::text);
ALTER TABLE bond_exchange.integration_event_deliveries
  ADD CONSTRAINT integration_event_deliveries_lease_nonce_v4
    CHECK (lease_nonce IS NULL OR uuid_extract_version(lease_nonce) = 4);
ALTER TABLE bond_exchange.sie_exchange_rate_fetch_coordination
  ADD CONSTRAINT sie_exchange_rate_fetch_lease_nonce_v4
    CHECK (lease_nonce IS NULL OR uuid_extract_version(lease_nonce) = 4);

CREATE FUNCTION bond_exchange.sync_sale_offer_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.id IS NULL THEN NEW.id := NEW.uuid_id::text; END IF;
  IF NEW.seller_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.seller_uuid FROM bond_exchange.users WHERE id = NEW.seller_id;
  ELSIF NEW.seller_id IS NULL THEN
    SELECT id INTO STRICT NEW.seller_id FROM bond_exchange.users WHERE uuid_id = NEW.seller_uuid;
  END IF;
  IF NEW.bond_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.bond_uuid FROM bond_exchange.bonds WHERE series = NEW.bond_series;
  ELSIF NEW.bond_series IS NULL THEN
    SELECT series INTO STRICT NEW.bond_series FROM bond_exchange.bonds WHERE uuid_id = NEW.bond_uuid;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER sale_offers_sync_identifiers
  BEFORE INSERT ON bond_exchange.sale_offers
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_sale_offer_identifiers();

CREATE FUNCTION bond_exchange.sync_purchase_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.sale_offer_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.sale_offer_uuid FROM bond_exchange.sale_offers WHERE id = NEW.sale_offer_id;
  ELSIF NEW.sale_offer_id IS NULL THEN
    SELECT id INTO STRICT NEW.sale_offer_id FROM bond_exchange.sale_offers WHERE uuid_id = NEW.sale_offer_uuid;
  END IF;
  IF NEW.buyer_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.buyer_uuid FROM bond_exchange.users WHERE id = NEW.buyer_id;
  ELSIF NEW.buyer_id IS NULL THEN
    SELECT id INTO STRICT NEW.buyer_id FROM bond_exchange.users WHERE uuid_id = NEW.buyer_uuid;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER purchases_sync_identifiers
  BEFORE INSERT ON bond_exchange.purchases
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_purchase_identifiers();

CREATE FUNCTION bond_exchange.sync_principal_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.uuid_id IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.uuid_id FROM bond_exchange.users WHERE id = NEW.id;
  ELSIF NEW.id IS NULL THEN
    SELECT id INTO STRICT NEW.id FROM bond_exchange.users WHERE uuid_id = NEW.uuid_id;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER principals_sync_identifiers
  BEFORE INSERT ON bond_exchange.principals
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_principal_identifiers();

CREATE FUNCTION bond_exchange.sync_principal_suspension_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.principal_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.principal_uuid FROM bond_exchange.principals WHERE id = NEW.principal_id;
  ELSIF NEW.principal_id IS NULL THEN
    SELECT id INTO STRICT NEW.principal_id FROM bond_exchange.principals WHERE uuid_id = NEW.principal_uuid;
  END IF;
  IF NEW.suspended_by_uuid IS NULL AND NEW.suspended_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.suspended_by_uuid FROM bond_exchange.principals WHERE id = NEW.suspended_by;
  ELSIF NEW.suspended_by IS NULL AND NEW.suspended_by_uuid IS NOT NULL THEN
    SELECT id INTO STRICT NEW.suspended_by FROM bond_exchange.principals WHERE uuid_id = NEW.suspended_by_uuid;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER principal_suspensions_sync_identifiers
  BEFORE INSERT ON bond_exchange.principal_suspensions
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_principal_suspension_identifiers();

CREATE FUNCTION bond_exchange.sync_principal_reinstatement_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.suspension_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.suspension_uuid FROM bond_exchange.principal_suspensions WHERE id = NEW.suspension_id;
  ELSIF NEW.suspension_id IS NULL THEN
    SELECT id INTO STRICT NEW.suspension_id FROM bond_exchange.principal_suspensions WHERE uuid_id = NEW.suspension_uuid;
  END IF;
  IF NEW.reinstated_by_uuid IS NULL AND NEW.reinstated_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.reinstated_by_uuid FROM bond_exchange.principals WHERE id = NEW.reinstated_by;
  ELSIF NEW.reinstated_by IS NULL AND NEW.reinstated_by_uuid IS NOT NULL THEN
    SELECT id INTO STRICT NEW.reinstated_by FROM bond_exchange.principals WHERE uuid_id = NEW.reinstated_by_uuid;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER principal_reinstatements_sync_identifiers
  BEFORE INSERT ON bond_exchange.principal_reinstatements
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_principal_reinstatement_identifiers();

CREATE FUNCTION bond_exchange.sync_role_permission_grant_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.role_uuid IS NULL THEN SELECT uuid_id INTO STRICT NEW.role_uuid FROM bond_exchange.roles WHERE id = NEW.role_id; END IF;
  IF NEW.permission_uuid IS NULL THEN SELECT uuid_id INTO STRICT NEW.permission_uuid FROM bond_exchange.permissions WHERE id = NEW.permission_id; END IF;
  IF NEW.granted_by_uuid IS NULL AND NEW.granted_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.granted_by_uuid FROM bond_exchange.principals WHERE id = NEW.granted_by;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER role_permission_grants_sync_identifiers
  BEFORE INSERT ON bond_exchange.role_permission_grants
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_role_permission_grant_identifiers();

CREATE FUNCTION bond_exchange.sync_role_permission_revocation_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.grant_uuid IS NULL THEN SELECT uuid_id INTO STRICT NEW.grant_uuid FROM bond_exchange.role_permission_grants WHERE id = NEW.grant_id; END IF;
  IF NEW.revoked_by_uuid IS NULL AND NEW.revoked_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.revoked_by_uuid FROM bond_exchange.principals WHERE id = NEW.revoked_by;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER role_permission_revocations_sync_identifiers
  BEFORE INSERT ON bond_exchange.role_permission_revocations
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_role_permission_revocation_identifiers();

CREATE FUNCTION bond_exchange.sync_principal_role_grant_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.principal_uuid IS NULL THEN SELECT uuid_id INTO STRICT NEW.principal_uuid FROM bond_exchange.principals WHERE id = NEW.principal_id; END IF;
  IF NEW.role_uuid IS NULL THEN SELECT uuid_id INTO STRICT NEW.role_uuid FROM bond_exchange.roles WHERE id = NEW.role_id; END IF;
  IF NEW.granted_by_uuid IS NULL AND NEW.granted_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.granted_by_uuid FROM bond_exchange.principals WHERE id = NEW.granted_by;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER principal_role_grants_sync_identifiers
  BEFORE INSERT ON bond_exchange.principal_role_grants
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_principal_role_grant_identifiers();

CREATE FUNCTION bond_exchange.sync_principal_role_revocation_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.grant_uuid IS NULL THEN SELECT uuid_id INTO STRICT NEW.grant_uuid FROM bond_exchange.principal_role_grants WHERE id = NEW.grant_id; END IF;
  IF NEW.revoked_by_uuid IS NULL AND NEW.revoked_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.revoked_by_uuid FROM bond_exchange.principals WHERE id = NEW.revoked_by;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER principal_role_revocations_sync_identifiers
  BEFORE INSERT ON bond_exchange.principal_role_revocations
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_principal_role_revocation_identifiers();

CREATE FUNCTION bond_exchange.sync_operation_claim_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.id IS NULL THEN NEW.id := NEW.uuid_id::text; END IF;
  IF NEW.principal_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.principal_uuid FROM bond_exchange.principals WHERE id = NEW.principal_id;
  ELSIF NEW.principal_id IS NULL THEN
    SELECT id INTO STRICT NEW.principal_id FROM bond_exchange.principals WHERE uuid_id = NEW.principal_uuid;
  END IF;
  IF NEW.idempotency_nonce IS NULL AND
     NEW.idempotency_key ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' THEN
    NEW.idempotency_nonce := NEW.idempotency_key::uuid;
  ELSIF NEW.idempotency_key IS NULL AND NEW.idempotency_nonce IS NOT NULL THEN
    NEW.idempotency_key := NEW.idempotency_nonce::text;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER operation_claims_sync_identifiers
  BEFORE INSERT ON bond_exchange.operation_claims
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_operation_claim_identifiers();

CREATE FUNCTION bond_exchange.sync_operation_result_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.claim_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.claim_uuid FROM bond_exchange.operation_claims WHERE id = NEW.claim_id;
  ELSIF NEW.claim_id IS NULL THEN
    SELECT id INTO STRICT NEW.claim_id FROM bond_exchange.operation_claims WHERE uuid_id = NEW.claim_uuid;
  END IF;
  IF NEW.resource_uuid IS NULL AND NEW.resource_id IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.resource_uuid FROM bond_exchange.sale_offers WHERE id = NEW.resource_id;
  ELSIF NEW.resource_id IS NULL AND NEW.resource_uuid IS NOT NULL THEN
    SELECT id INTO STRICT NEW.resource_id FROM bond_exchange.sale_offers WHERE uuid_id = NEW.resource_uuid;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER operation_results_sync_identifiers
  BEFORE INSERT ON bond_exchange.operation_results
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_operation_result_identifiers();

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
BEGIN
  IF NEW.outcome <> 'succeeded' THEN RETURN NEW; END IF;
  SELECT operation INTO STRICT completed_operation
  FROM bond_exchange.operation_claims WHERE uuid_id = NEW.claim_uuid;
  CASE completed_operation
    WHEN 'offers.create' THEN
      source_table := 'sale_offers';
      source_identifier := NEW.resource_uuid;
    WHEN 'purchases.buy' THEN
      source_table := 'purchases';
      SELECT uuid_id INTO STRICT source_identifier
      FROM bond_exchange.purchases WHERE sale_offer_uuid = NEW.resource_uuid;
    ELSE RETURN NEW;
  END CASE;
  INSERT INTO bond_exchange.integration_events
    (table_name, id, source_uuid, schema_version, completed_at)
  VALUES
    (source_table, NEW.resource_id, source_identifier, 1, NEW.completed_at);
  RETURN NEW;
END;
$$;

CREATE FUNCTION bond_exchange.sync_event_delivery_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.event_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.event_uuid
    FROM bond_exchange.integration_events
    WHERE table_name = NEW.table_name AND id = NEW.id;
  ELSIF NEW.table_name IS NULL OR NEW.id IS NULL THEN
    SELECT table_name, id INTO STRICT NEW.table_name, NEW.id
    FROM bond_exchange.integration_events WHERE uuid_id = NEW.event_uuid;
  END IF;
  IF TG_OP = 'INSERT' OR NEW.lease_nonce IS DISTINCT FROM OLD.lease_nonce THEN
    IF NEW.lease_nonce IS NOT NULL THEN NEW.lease_token := NEW.lease_nonce::text; END IF;
  ELSIF NEW.lease_token IS DISTINCT FROM OLD.lease_token THEN
    IF NEW.lease_token ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' THEN
      NEW.lease_nonce := NEW.lease_token::uuid;
    ELSE
      NEW.lease_nonce := NULL;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER integration_event_deliveries_sync_identifiers
  BEFORE INSERT OR UPDATE ON bond_exchange.integration_event_deliveries
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_event_delivery_identifiers();

CREATE FUNCTION bond_exchange.sync_rate_observation_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.import_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.import_uuid FROM bond_exchange.sie_exchange_rate_imports WHERE id = NEW.import_id;
  ELSIF NEW.import_id IS NULL THEN
    SELECT id INTO STRICT NEW.import_id FROM bond_exchange.sie_exchange_rate_imports WHERE uuid_id = NEW.import_uuid;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER sie_exchange_rate_observations_sync_identifiers
  BEFORE INSERT ON bond_exchange.sie_exchange_rate_observations
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_rate_observation_identifiers();

CREATE FUNCTION bond_exchange.sync_fetch_lease_nonce()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF TG_OP = 'INSERT' OR NEW.lease_nonce IS DISTINCT FROM OLD.lease_nonce THEN
    IF NEW.lease_nonce IS NOT NULL THEN NEW.lease_token := NEW.lease_nonce::text; END IF;
  ELSIF NEW.lease_token IS DISTINCT FROM OLD.lease_token THEN
    IF NEW.lease_token ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' THEN
      NEW.lease_nonce := NEW.lease_token::uuid;
    ELSE
      NEW.lease_nonce := NULL;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER sie_exchange_rate_fetch_sync_lease_nonce
  BEFORE INSERT OR UPDATE ON bond_exchange.sie_exchange_rate_fetch_coordination
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_fetch_lease_nonce();

CREATE VIEW bond_exchange.active_offers_v2 AS
SELECT
  sale_offer.uuid_id AS id,
  sale_offer.seller_uuid AS seller_id,
  sale_offer.bond_series,
  sale_offer.price,
  sale_offer.currency_code
FROM bond_exchange.sale_offers AS sale_offer
WHERE NOT EXISTS (
  SELECT 1 FROM bond_exchange.purchases AS purchase
  WHERE purchase.sale_offer_uuid = sale_offer.uuid_id
);

CREATE VIEW bond_exchange.effective_principal_permissions_v2 AS
SELECT DISTINCT
  principal_role_grant.principal_uuid AS principal_id,
  role_permission_grant.permission_uuid AS permission_id,
  permission.id AS permission_code
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

CREATE VIEW bond_exchange.current_sie_exchange_rates_v2 AS
SELECT DISTINCT ON (series_id, base_currency, quote_currency, observed_on)
  series_id,
  base_currency,
  quote_currency,
  observed_on,
  value,
  recorded_at,
  uuid_id AS revision_id,
  id AS revision_sequence
FROM bond_exchange.sie_exchange_rate_observations
ORDER BY series_id, base_currency, quote_currency, observed_on, id DESC;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'UUID identifiers were attached to append-only facts; use a corrective forward migration';
END;
$$;
