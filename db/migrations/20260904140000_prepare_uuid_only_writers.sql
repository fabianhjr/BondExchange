-- migrate:up

-- Let the UUID-only release omit every compatibility column while the
-- pre-contract schema still keeps those columns valid for older writers.
CREATE FUNCTION bond_exchange.sync_user_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.id IS NULL THEN NEW.id := NEW.uuid_id::text; END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER users_sync_identifiers
  BEFORE INSERT ON bond_exchange.users
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_user_identifiers();

CREATE OR REPLACE FUNCTION bond_exchange.sync_principal_suspension_identifiers()
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
  IF NEW.suspended_by_uuid IS NULL AND NEW.suspended_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.suspended_by_uuid FROM bond_exchange.principals WHERE id = NEW.suspended_by;
  ELSIF NEW.suspended_by IS NULL AND NEW.suspended_by_uuid IS NOT NULL THEN
    SELECT id INTO STRICT NEW.suspended_by FROM bond_exchange.principals WHERE uuid_id = NEW.suspended_by_uuid;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION bond_exchange.sync_principal_reinstatement_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.id IS NULL THEN NEW.id := NEW.uuid_id::text; END IF;
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

CREATE OR REPLACE FUNCTION bond_exchange.sync_role_permission_grant_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.id IS NULL THEN NEW.id := NEW.uuid_id::text; END IF;
  IF NEW.role_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.role_uuid FROM bond_exchange.roles WHERE id = NEW.role_id;
  ELSIF NEW.role_id IS NULL THEN
    SELECT id INTO STRICT NEW.role_id FROM bond_exchange.roles WHERE uuid_id = NEW.role_uuid;
  END IF;
  IF NEW.permission_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.permission_uuid FROM bond_exchange.permissions WHERE id = NEW.permission_id;
  ELSIF NEW.permission_id IS NULL THEN
    SELECT id INTO STRICT NEW.permission_id FROM bond_exchange.permissions WHERE uuid_id = NEW.permission_uuid;
  END IF;
  IF NEW.granted_by_uuid IS NULL AND NEW.granted_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.granted_by_uuid FROM bond_exchange.principals WHERE id = NEW.granted_by;
  ELSIF NEW.granted_by IS NULL AND NEW.granted_by_uuid IS NOT NULL THEN
    SELECT id INTO STRICT NEW.granted_by FROM bond_exchange.principals WHERE uuid_id = NEW.granted_by_uuid;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION bond_exchange.sync_role_permission_revocation_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.id IS NULL THEN NEW.id := NEW.uuid_id::text; END IF;
  IF NEW.grant_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.grant_uuid FROM bond_exchange.role_permission_grants WHERE id = NEW.grant_id;
  ELSIF NEW.grant_id IS NULL THEN
    SELECT id INTO STRICT NEW.grant_id FROM bond_exchange.role_permission_grants WHERE uuid_id = NEW.grant_uuid;
  END IF;
  IF NEW.revoked_by_uuid IS NULL AND NEW.revoked_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.revoked_by_uuid FROM bond_exchange.principals WHERE id = NEW.revoked_by;
  ELSIF NEW.revoked_by IS NULL AND NEW.revoked_by_uuid IS NOT NULL THEN
    SELECT id INTO STRICT NEW.revoked_by FROM bond_exchange.principals WHERE uuid_id = NEW.revoked_by_uuid;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION bond_exchange.sync_principal_role_grant_identifiers()
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
  IF NEW.role_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.role_uuid FROM bond_exchange.roles WHERE id = NEW.role_id;
  ELSIF NEW.role_id IS NULL THEN
    SELECT id INTO STRICT NEW.role_id FROM bond_exchange.roles WHERE uuid_id = NEW.role_uuid;
  END IF;
  IF NEW.granted_by_uuid IS NULL AND NEW.granted_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.granted_by_uuid FROM bond_exchange.principals WHERE id = NEW.granted_by;
  ELSIF NEW.granted_by IS NULL AND NEW.granted_by_uuid IS NOT NULL THEN
    SELECT id INTO STRICT NEW.granted_by FROM bond_exchange.principals WHERE uuid_id = NEW.granted_by_uuid;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION bond_exchange.sync_principal_role_revocation_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.id IS NULL THEN NEW.id := NEW.uuid_id::text; END IF;
  IF NEW.grant_uuid IS NULL THEN
    SELECT uuid_id INTO STRICT NEW.grant_uuid FROM bond_exchange.principal_role_grants WHERE id = NEW.grant_id;
  ELSIF NEW.grant_id IS NULL THEN
    SELECT id INTO STRICT NEW.grant_id FROM bond_exchange.principal_role_grants WHERE uuid_id = NEW.grant_uuid;
  END IF;
  IF NEW.revoked_by_uuid IS NULL AND NEW.revoked_by IS NOT NULL THEN
    SELECT uuid_id INTO STRICT NEW.revoked_by_uuid FROM bond_exchange.principals WHERE id = NEW.revoked_by;
  ELSIF NEW.revoked_by IS NULL AND NEW.revoked_by_uuid IS NOT NULL THEN
    SELECT id INTO STRICT NEW.revoked_by FROM bond_exchange.principals WHERE uuid_id = NEW.revoked_by_uuid;
  END IF;
  RETURN NEW;
END;
$$;

CREATE FUNCTION bond_exchange.sync_integration_event_identifiers()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.id IS NULL THEN
    CASE NEW.table_name
      WHEN 'sale_offers' THEN
        SELECT id INTO STRICT NEW.id FROM bond_exchange.sale_offers WHERE uuid_id = NEW.source_uuid;
      WHEN 'purchases' THEN
        SELECT offer.id INTO STRICT NEW.id
        FROM bond_exchange.purchases AS purchase
        JOIN bond_exchange.sale_offers AS offer ON offer.uuid_id = purchase.sale_offer_uuid
        WHERE purchase.uuid_id = NEW.source_uuid;
    END CASE;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER integration_events_sync_identifiers
  BEFORE INSERT ON bond_exchange.integration_events
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.sync_integration_event_identifiers();

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
    (table_name, source_uuid, schema_version, completed_at)
  VALUES
    (source_table, source_identifier, 1, NEW.completed_at);
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION bond_exchange.sync_event_delivery_identifiers()
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
    NEW.lease_token := NEW.lease_nonce::text;
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

CREATE OR REPLACE FUNCTION bond_exchange.sync_fetch_lease_nonce()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  IF TG_OP = 'INSERT' OR NEW.lease_nonce IS DISTINCT FROM OLD.lease_nonce THEN
    NEW.lease_token := NEW.lease_nonce::text;
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

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION 'UUID-only writes may already exist; use a corrective forward migration';
END;
$$;
