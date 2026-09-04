-- migrate:up

INSERT INTO bond_exchange.role_permission_revocations
  (id, grant_id, reason)
VALUES
  (
    'retire-bootstrap-operator-reflection-use',
    'bootstrap-operator-reflection-use',
    'Runtime gRPC reflection was removed; use the versioned descriptor set instead.'
  );

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'RBAC revocations are append-only security facts and have no lossless down migration; roll forward';
END;
$$;
