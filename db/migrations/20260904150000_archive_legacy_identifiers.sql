-- migrate:up

CREATE TABLE bond_exchange.legacy_identifier_archive (
  uuid_id uuid PRIMARY KEY DEFAULT uuidv7(),
  entity_type text NOT NULL,
  entity_uuid uuid NOT NULL,
  attribute_name text NOT NULL,
  legacy_value text NOT NULL,
  archived_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT legacy_identifier_archive_uuid_v7
    CHECK (uuid_extract_version(uuid_id) = 7),
  CONSTRAINT legacy_identifier_archive_entity_type_known
    CHECK (entity_type IN (
      'user',
      'principal',
      'sale_offer',
      'principal_suspension',
      'principal_reinstatement',
      'role_permission_grant',
      'role_permission_revocation',
      'principal_role_grant',
      'principal_role_revocation',
      'operation_claim',
      'sie_exchange_rate_import'
    )),
  CONSTRAINT legacy_identifier_archive_attribute_known
    CHECK (attribute_name IN ('id', 'idempotency_key', 'sequence')),
  CONSTRAINT legacy_identifier_archive_value_not_empty
    CHECK (legacy_value <> ''),
  CONSTRAINT legacy_identifier_archive_entity_attribute_unique
    UNIQUE (entity_type, entity_uuid, attribute_name),
  CONSTRAINT legacy_identifier_archive_value_unique
    UNIQUE (entity_type, attribute_name, legacy_value)
);

CREATE TRIGGER legacy_identifier_archive_is_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.legacy_identifier_archive
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

REVOKE UPDATE, DELETE, TRUNCATE
  ON bond_exchange.legacy_identifier_archive
  FROM PUBLIC;

INSERT INTO bond_exchange.legacy_identifier_archive
  (entity_type, entity_uuid, attribute_name, legacy_value)
SELECT 'user', uuid_id, 'id', id
FROM bond_exchange.users
WHERE id <> uuid_id::text
UNION ALL
SELECT 'principal', uuid_id, 'id', id
FROM bond_exchange.principals
WHERE id <> uuid_id::text
UNION ALL
SELECT 'sale_offer', uuid_id, 'id', id
FROM bond_exchange.sale_offers
WHERE id <> uuid_id::text
UNION ALL
SELECT 'principal_suspension', uuid_id, 'id', id
FROM bond_exchange.principal_suspensions
WHERE id <> uuid_id::text
UNION ALL
SELECT 'principal_reinstatement', uuid_id, 'id', id
FROM bond_exchange.principal_reinstatements
WHERE id <> uuid_id::text
UNION ALL
SELECT 'role_permission_grant', uuid_id, 'id', id
FROM bond_exchange.role_permission_grants
WHERE id <> uuid_id::text
UNION ALL
SELECT 'role_permission_revocation', uuid_id, 'id', id
FROM bond_exchange.role_permission_revocations
WHERE id <> uuid_id::text
UNION ALL
SELECT 'principal_role_grant', uuid_id, 'id', id
FROM bond_exchange.principal_role_grants
WHERE id <> uuid_id::text
UNION ALL
SELECT 'principal_role_revocation', uuid_id, 'id', id
FROM bond_exchange.principal_role_revocations
WHERE id <> uuid_id::text
UNION ALL
SELECT 'operation_claim', uuid_id, 'id', id
FROM bond_exchange.operation_claims
WHERE id <> uuid_id::text
UNION ALL
SELECT 'operation_claim', uuid_id, 'idempotency_key', idempotency_key
FROM bond_exchange.operation_claims
WHERE idempotency_nonce IS NULL
UNION ALL
SELECT 'sie_exchange_rate_import', uuid_id, 'sequence', id::text
FROM bond_exchange.sie_exchange_rate_imports;

-- The archive is an integrity boundary, not a live compatibility lookup.
DO $$
DECLARE
  expected_count bigint;
  archived_count bigint;
BEGIN
  SELECT
    (SELECT count(*) FROM bond_exchange.users WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.principals WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.sale_offers WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.principal_suspensions WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.principal_reinstatements WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.role_permission_grants WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.role_permission_revocations WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.principal_role_grants WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.principal_role_revocations WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.operation_claims WHERE id <> uuid_id::text)
    + (SELECT count(*) FROM bond_exchange.operation_claims WHERE idempotency_nonce IS NULL)
    + (SELECT count(*) FROM bond_exchange.sie_exchange_rate_imports)
  INTO expected_count;

  SELECT count(*) INTO archived_count
  FROM bond_exchange.legacy_identifier_archive;
  IF archived_count <> expected_count THEN
    RAISE EXCEPTION 'legacy archive contains % rows, expected %', archived_count, expected_count;
  END IF;
END;
$$;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION 'legacy identifiers are immutable audit evidence; use a corrective forward migration';
END;
$$;
