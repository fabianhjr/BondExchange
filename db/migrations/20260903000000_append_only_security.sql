-- migrate:up

CREATE TABLE bond_exchange.principals (
  id text PRIMARY KEY REFERENCES bond_exchange.users (id),
  issuer text NOT NULL,
  subject text NOT NULL,
  client_class text NOT NULL,
  linked_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT principals_issuer_not_empty CHECK (issuer <> ''),
  CONSTRAINT principals_subject_not_empty CHECK (subject <> ''),
  CONSTRAINT principals_issuer_size CHECK (octet_length(issuer) <= 1024),
  CONSTRAINT principals_subject_size CHECK (octet_length(subject) <= 256),
  CONSTRAINT principals_client_class_known
    CHECK (client_class IN ('human', 'automated')),
  CONSTRAINT principals_federated_identity_unique UNIQUE (issuer, subject)
);

CREATE TABLE bond_exchange.principal_suspensions (
  id text PRIMARY KEY,
  principal_id text NOT NULL REFERENCES bond_exchange.principals (id),
  suspended_by text REFERENCES bond_exchange.principals (id),
  reason text NOT NULL,
  suspended_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT principal_suspensions_id_not_empty CHECK (id <> ''),
  CONSTRAINT principal_suspensions_reason_not_empty CHECK (reason <> '')
);

CREATE TABLE bond_exchange.principal_reinstatements (
  id text PRIMARY KEY,
  suspension_id text NOT NULL UNIQUE
    REFERENCES bond_exchange.principal_suspensions (id),
  reinstated_by text REFERENCES bond_exchange.principals (id),
  reason text NOT NULL,
  reinstated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT principal_reinstatements_id_not_empty CHECK (id <> ''),
  CONSTRAINT principal_reinstatements_reason_not_empty CHECK (reason <> '')
);

CREATE TABLE bond_exchange.roles (
  id text PRIMARY KEY,
  description text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT roles_id_canonical CHECK (id ~ '^[a-z][a-z0-9.-]{0,63}$'),
  CONSTRAINT roles_description_not_empty CHECK (description <> '')
);

CREATE TABLE bond_exchange.permissions (
  id text PRIMARY KEY,
  description text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT permissions_id_canonical
    CHECK (id ~ '^[a-z][a-z0-9.-]{0,63}$'),
  CONSTRAINT permissions_description_not_empty CHECK (description <> '')
);

CREATE TABLE bond_exchange.role_permission_grants (
  id text PRIMARY KEY,
  role_id text NOT NULL REFERENCES bond_exchange.roles (id),
  permission_id text NOT NULL REFERENCES bond_exchange.permissions (id),
  granted_by text REFERENCES bond_exchange.principals (id),
  reason text NOT NULL,
  granted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT role_permission_grants_id_not_empty CHECK (id <> ''),
  CONSTRAINT role_permission_grants_reason_not_empty CHECK (reason <> '')
);

CREATE TABLE bond_exchange.role_permission_revocations (
  id text PRIMARY KEY,
  grant_id text NOT NULL UNIQUE
    REFERENCES bond_exchange.role_permission_grants (id),
  revoked_by text REFERENCES bond_exchange.principals (id),
  reason text NOT NULL,
  revoked_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT role_permission_revocations_id_not_empty CHECK (id <> ''),
  CONSTRAINT role_permission_revocations_reason_not_empty CHECK (reason <> '')
);

CREATE TABLE bond_exchange.principal_role_grants (
  id text PRIMARY KEY,
  principal_id text NOT NULL REFERENCES bond_exchange.principals (id),
  role_id text NOT NULL REFERENCES bond_exchange.roles (id),
  granted_by text REFERENCES bond_exchange.principals (id),
  reason text NOT NULL,
  granted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT principal_role_grants_id_not_empty CHECK (id <> ''),
  CONSTRAINT principal_role_grants_reason_not_empty CHECK (reason <> '')
);

CREATE INDEX principal_role_grants_principal_role_idx
  ON bond_exchange.principal_role_grants (principal_id, role_id);

CREATE TABLE bond_exchange.principal_role_revocations (
  id text PRIMARY KEY,
  grant_id text NOT NULL UNIQUE
    REFERENCES bond_exchange.principal_role_grants (id),
  revoked_by text REFERENCES bond_exchange.principals (id),
  reason text NOT NULL,
  revoked_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT principal_role_revocations_id_not_empty CHECK (id <> ''),
  CONSTRAINT principal_role_revocations_reason_not_empty CHECK (reason <> '')
);

CREATE VIEW bond_exchange.effective_principal_permissions AS
SELECT DISTINCT
  principal_role_grant.principal_id,
  role_permission_grant.permission_id
FROM bond_exchange.principal_role_grants AS principal_role_grant
JOIN bond_exchange.role_permission_grants AS role_permission_grant
  ON role_permission_grant.role_id = principal_role_grant.role_id
WHERE NOT EXISTS (
  SELECT 1
  FROM bond_exchange.principal_role_revocations AS principal_role_revocation
  WHERE principal_role_revocation.grant_id = principal_role_grant.id
)
AND NOT EXISTS (
  SELECT 1
  FROM bond_exchange.role_permission_revocations AS role_permission_revocation
  WHERE role_permission_revocation.grant_id = role_permission_grant.id
)
AND NOT EXISTS (
  SELECT 1
  FROM bond_exchange.principal_suspensions AS suspension
  WHERE suspension.principal_id = principal_role_grant.principal_id
    AND NOT EXISTS (
      SELECT 1
      FROM bond_exchange.principal_reinstatements AS reinstatement
      WHERE reinstatement.suspension_id = suspension.id
    )
);

CREATE TABLE bond_exchange.operation_claims (
  id text PRIMARY KEY,
  principal_id text NOT NULL REFERENCES bond_exchange.principals (id),
  client_id text NOT NULL,
  operation text NOT NULL,
  idempotency_key text NOT NULL,
  request_digest bytea NOT NULL,
  assertion_digest bytea NOT NULL,
  claimed_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT operation_claims_id_not_empty CHECK (id <> ''),
  CONSTRAINT operation_claims_client_id_not_empty CHECK (client_id <> ''),
  CONSTRAINT operation_claims_client_id_size CHECK (octet_length(client_id) <= 256),
  CONSTRAINT operation_claims_operation_canonical
    CHECK (operation ~ '^[a-z][a-z0-9.-]{0,63}$'),
  CONSTRAINT operation_claims_idempotency_key_size
    CHECK (octet_length(idempotency_key) BETWEEN 16 AND 128),
  CONSTRAINT operation_claims_request_digest_sha256
    CHECK (octet_length(request_digest) = 32),
  CONSTRAINT operation_claims_assertion_digest_sha256
    CHECK (octet_length(assertion_digest) = 32),
  CONSTRAINT operation_claims_idempotency_scope_unique
    UNIQUE (principal_id, client_id, operation, idempotency_key)
);

CREATE TABLE bond_exchange.operation_results (
  claim_id text PRIMARY KEY REFERENCES bond_exchange.operation_claims (id),
  outcome text NOT NULL,
  resource_id text,
  safe_error_code text,
  completed_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT operation_results_outcome_known
    CHECK (outcome IN ('succeeded', 'rejected')),
  CONSTRAINT operation_results_shape
    CHECK (
      (outcome = 'succeeded' AND resource_id IS NOT NULL AND safe_error_code IS NULL)
      OR
      (outcome = 'rejected' AND resource_id IS NULL AND safe_error_code IS NOT NULL)
    )
);

INSERT INTO bond_exchange.permissions (id, description)
VALUES
  ('offers.create', 'Create a sale offer for the authenticated principal.'),
  ('offers.list', 'Stream the complete active offer book for one bond series.'),
  ('purchases.buy', 'Place a binding order or reservation for an active sale offer.'),
  ('rbac.grant', 'Append role and permission grants.'),
  ('rbac.read', 'Read role and permission facts.'),
  ('rbac.revoke', 'Append role and permission revocations.'),
  ('audit.read', 'Read restricted security and operation audit facts.'),
  ('health.read', 'Read service readiness information.'),
  ('reflection.use', 'Use gRPC reflection for internal diagnostics.');

INSERT INTO bond_exchange.roles (id, description)
VALUES
  ('market-reader', 'Read active market data.'),
  ('trader', 'Create offers and place binding orders or reservations.'),
  ('security-administrator', 'Administer the append-only RBAC model.'),
  ('auditor', 'Inspect RBAC and security audit facts.'),
  ('operator', 'Inspect service health and use diagnostics.');

INSERT INTO bond_exchange.role_permission_grants
  (id, role_id, permission_id, reason)
VALUES
  ('bootstrap-market-reader-offers-list', 'market-reader', 'offers.list', 'Initial reviewed role definition.'),
  ('bootstrap-trader-offers-create', 'trader', 'offers.create', 'Initial reviewed role definition.'),
  ('bootstrap-trader-offers-list', 'trader', 'offers.list', 'Initial reviewed role definition.'),
  ('bootstrap-trader-purchases-buy', 'trader', 'purchases.buy', 'Initial reviewed role definition.'),
  ('bootstrap-security-admin-rbac-read', 'security-administrator', 'rbac.read', 'Initial reviewed role definition.'),
  ('bootstrap-security-admin-rbac-grant', 'security-administrator', 'rbac.grant', 'Initial reviewed role definition.'),
  ('bootstrap-security-admin-rbac-revoke', 'security-administrator', 'rbac.revoke', 'Initial reviewed role definition.'),
  ('bootstrap-auditor-rbac-read', 'auditor', 'rbac.read', 'Initial reviewed role definition.'),
  ('bootstrap-auditor-audit-read', 'auditor', 'audit.read', 'Initial reviewed role definition.'),
  ('bootstrap-operator-health-read', 'operator', 'health.read', 'Initial reviewed role definition.'),
  ('bootstrap-operator-reflection-use', 'operator', 'reflection.use', 'Initial reviewed role definition.');

DO $$
DECLARE
  table_name text;
BEGIN
  FOREACH table_name IN ARRAY ARRAY[
    'principals',
    'principal_suspensions',
    'principal_reinstatements',
    'roles',
    'permissions',
    'role_permission_grants',
    'role_permission_revocations',
    'principal_role_grants',
    'principal_role_revocations',
    'operation_claims',
    'operation_results'
  ]
  LOOP
    EXECUTE format(
      'CREATE TRIGGER %I_are_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.%I FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation()',
      table_name,
      table_name
    );
    EXECUTE format(
      'REVOKE UPDATE, DELETE, TRUNCATE ON bond_exchange.%I FROM PUBLIC',
      table_name
    );
  END LOOP;
END;
$$;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'append-only security and operation facts have no lossless down migration; roll forward';
END;
$$;
