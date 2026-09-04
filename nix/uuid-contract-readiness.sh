# shellcheck shell=bash
set -euo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "DATABASE_URL must identify the PostgreSQL 18 database to inspect" >&2
  exit 64
fi

psql "$DATABASE_URL" --no-psqlrc --set ON_ERROR_STOP=1 <<'SQL'
DO $$
DECLARE
  drift_count bigint;
BEGIN
  IF current_setting('server_version_num')::integer NOT BETWEEN 180000 AND 189999 THEN
    RAISE EXCEPTION 'UUID contraction requires PostgreSQL 18';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'bond_exchange' AND table_name = 'users' AND column_name = 'id'
  ) THEN
    RAISE NOTICE 'legacy identifier graph is already contracted';
    RETURN;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.sale_offers AS offer
  JOIN bond_exchange.users AS seller ON seller.uuid_id = offer.seller_uuid
  JOIN bond_exchange.bonds AS bond ON bond.uuid_id = offer.bond_uuid
  WHERE offer.seller_id IS DISTINCT FROM seller.id
     OR offer.bond_series IS DISTINCT FROM bond.series;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'sale offer identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.purchases AS purchase
  JOIN bond_exchange.sale_offers AS offer ON offer.uuid_id = purchase.sale_offer_uuid
  JOIN bond_exchange.users AS buyer ON buyer.uuid_id = purchase.buyer_uuid
  WHERE purchase.sale_offer_id IS DISTINCT FROM offer.id
     OR purchase.buyer_id IS DISTINCT FROM buyer.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'purchase identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.principals AS principal
  JOIN bond_exchange.users AS app_user ON app_user.uuid_id = principal.uuid_id
  WHERE principal.id IS DISTINCT FROM app_user.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'principal identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.principal_suspensions AS suspension
  JOIN bond_exchange.principals AS principal ON principal.uuid_id = suspension.principal_uuid
  LEFT JOIN bond_exchange.principals AS actor ON actor.uuid_id = suspension.suspended_by_uuid
  WHERE suspension.principal_id IS DISTINCT FROM principal.id
     OR suspension.suspended_by IS DISTINCT FROM actor.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'principal suspension identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.principal_reinstatements AS reinstatement
  JOIN bond_exchange.principal_suspensions AS suspension ON suspension.uuid_id = reinstatement.suspension_uuid
  LEFT JOIN bond_exchange.principals AS actor ON actor.uuid_id = reinstatement.reinstated_by_uuid
  WHERE reinstatement.suspension_id IS DISTINCT FROM suspension.id
     OR reinstatement.reinstated_by IS DISTINCT FROM actor.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'principal reinstatement identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.role_permission_grants AS permission_grant
  JOIN bond_exchange.roles AS role ON role.uuid_id = permission_grant.role_uuid
  JOIN bond_exchange.permissions AS permission ON permission.uuid_id = permission_grant.permission_uuid
  LEFT JOIN bond_exchange.principals AS actor ON actor.uuid_id = permission_grant.granted_by_uuid
  WHERE permission_grant.role_id IS DISTINCT FROM role.id
     OR permission_grant.permission_id IS DISTINCT FROM permission.id
     OR permission_grant.granted_by IS DISTINCT FROM actor.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'role permission grant identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.role_permission_revocations AS revocation
  JOIN bond_exchange.role_permission_grants AS permission_grant ON permission_grant.uuid_id = revocation.grant_uuid
  LEFT JOIN bond_exchange.principals AS actor ON actor.uuid_id = revocation.revoked_by_uuid
  WHERE revocation.grant_id IS DISTINCT FROM permission_grant.id
     OR revocation.revoked_by IS DISTINCT FROM actor.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'role permission revocation identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.principal_role_grants AS role_grant
  JOIN bond_exchange.principals AS principal ON principal.uuid_id = role_grant.principal_uuid
  JOIN bond_exchange.roles AS role ON role.uuid_id = role_grant.role_uuid
  LEFT JOIN bond_exchange.principals AS actor ON actor.uuid_id = role_grant.granted_by_uuid
  WHERE role_grant.principal_id IS DISTINCT FROM principal.id
     OR role_grant.role_id IS DISTINCT FROM role.id
     OR role_grant.granted_by IS DISTINCT FROM actor.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'principal role grant identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.principal_role_revocations AS revocation
  JOIN bond_exchange.principal_role_grants AS role_grant ON role_grant.uuid_id = revocation.grant_uuid
  LEFT JOIN bond_exchange.principals AS actor ON actor.uuid_id = revocation.revoked_by_uuid
  WHERE revocation.grant_id IS DISTINCT FROM role_grant.id
     OR revocation.revoked_by IS DISTINCT FROM actor.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'principal role revocation identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.operation_claims AS claim
  JOIN bond_exchange.principals AS principal ON principal.uuid_id = claim.principal_uuid
  WHERE claim.principal_id IS DISTINCT FROM principal.id
     OR (claim.idempotency_nonce IS NOT NULL
         AND claim.idempotency_key IS DISTINCT FROM claim.idempotency_nonce::text);
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'operation claim identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.operation_results AS result
  JOIN bond_exchange.operation_claims AS claim ON claim.uuid_id = result.claim_uuid
  LEFT JOIN bond_exchange.sale_offers AS offer ON offer.uuid_id = result.resource_uuid
  WHERE result.claim_id IS DISTINCT FROM claim.id
     OR result.resource_id IS DISTINCT FROM offer.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'operation result identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.integration_events AS event
  WHERE event.id IS DISTINCT FROM CASE event.table_name
    WHEN 'sale_offers' THEN (SELECT offer.id FROM bond_exchange.sale_offers AS offer WHERE offer.uuid_id = event.source_uuid)
    WHEN 'purchases' THEN (
      SELECT offer.id
      FROM bond_exchange.purchases AS purchase
      JOIN bond_exchange.sale_offers AS offer ON offer.uuid_id = purchase.sale_offer_uuid
      WHERE purchase.uuid_id = event.source_uuid
    )
  END;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'integration event identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.integration_event_deliveries AS delivery
  JOIN bond_exchange.integration_events AS event ON event.uuid_id = delivery.event_uuid
  WHERE delivery.table_name IS DISTINCT FROM event.table_name
     OR delivery.id IS DISTINCT FROM event.id
     OR (delivery.lease_nonce IS NOT NULL
         AND delivery.lease_token IS DISTINCT FROM delivery.lease_nonce::text);
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'event delivery identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.sie_exchange_rate_observations AS observation
  JOIN bond_exchange.sie_exchange_rate_imports AS import ON import.uuid_id = observation.import_uuid
  WHERE observation.import_id IS DISTINCT FROM import.id;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'SIE import identifier drift: % rows', drift_count;
  END IF;

  SELECT count(*) INTO drift_count
  FROM bond_exchange.sie_exchange_rate_fetch_coordination
  WHERE lease_nonce IS NOT NULL
    AND lease_token IS DISTINCT FROM lease_nonce::text;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'SIE lease identifier drift: % rows', drift_count;
  END IF;

  SELECT
    (SELECT count(*) FROM bond_exchange.integration_event_deliveries WHERE lease_until > clock_timestamp())
    +
    (SELECT count(*) FROM bond_exchange.sie_exchange_rate_fetch_coordination WHERE lease_until > clock_timestamp())
  INTO drift_count;
  IF drift_count <> 0 THEN
    RAISE EXCEPTION 'UUID contraction requires a quiescent lease window; % active leases remain', drift_count;
  END IF;
END;
$$;

SELECT
  (SELECT count(*) FROM bond_exchange.operation_claims WHERE idempotency_nonce IS NULL)
    AS historical_non_uuid_idempotency_keys,
  (SELECT count(*) FROM bond_exchange.users) AS user_aliases_to_archive,
  (SELECT count(*) FROM bond_exchange.sale_offers) AS offer_aliases_to_archive,
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_imports) AS import_sequences_to_archive;
SQL

echo "UUID contraction data checks passed."
echo "Before contracting production, also prove that pre-UUID binaries and direct writers are retired,"
echo "their credentials are revoked, query logs contain no legacy access, and backup restore is rehearsed."
