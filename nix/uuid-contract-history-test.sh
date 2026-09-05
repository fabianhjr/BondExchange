# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
history_database="bond_exchange_uuid_history"
history_url="postgresql:///$history_database?host=$PGHOST"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/bond-exchange-uuid-history.XXXXXX")"
migration_root="$test_root/migrations"

cleanup() {
  local status=$?
  trap - EXIT
  dropdb --if-exists "$history_database" >/dev/null 2>&1 || status=1
  case "$test_root" in
    "${TMPDIR:-/tmp}"/bond-exchange-uuid-history.*) rm -rf -- "$test_root" ;;
    *) status=1 ;;
  esac
  exit "$status"
}
trap cleanup EXIT

createdb "$history_database"
mkdir -p "$migration_root"

for migration in \
  20260901000000_append_only_exchange.sql \
  20260902000000_use_decimal_prices.sql \
  20260903000000_append_only_security.sql \
  20260903120000_retire_reflection_permission.sql \
  20260903130000_add_integration_event_references.sql \
  20260904120000_add_sie_exchange_rates.sql \
  20260904130000_use_uuid_keys_and_nonces.sql \
  20260904140000_prepare_uuid_only_writers.sql
do
  ln -s "$project_root/db/migrations/$migration" "$migration_root/$migration"
done

DATABASE_URL="$history_url" DBMATE_MIGRATIONS_DIR="$migration_root" dbmate up

psql "$history_url" --no-psqlrc --set ON_ERROR_STOP=1 <<'SQL'
INSERT INTO bond_exchange.users (id) VALUES ('historical-user');
INSERT INTO bond_exchange.principals (id, issuer, subject, client_class)
VALUES ('historical-user', 'https://history.invalid', 'historical-user', 'automated');
INSERT INTO bond_exchange.principal_role_grants
  (id, principal_id, role_id, granted_by, reason)
VALUES ('historical-role-grant', 'historical-user', 'trader', 'historical-user', 'Migration fixture.');
INSERT INTO bond_exchange.bonds (series) VALUES ('HISTORY2026');
INSERT INTO bond_exchange.sale_offers
  (id, seller_id, bond_series, price, currency_code)
VALUES ('historical-offer', 'historical-user', 'HISTORY2026', 100.25, 'USD');
INSERT INTO bond_exchange.operation_claims
  (id, principal_id, client_id, operation, idempotency_key, request_digest, assertion_digest)
VALUES (
  repeat('a', 64),
  'historical-user',
  'historical-client',
  'offers.create',
  'historical-nonce',
  decode(repeat('00', 32), 'hex'),
  decode(repeat('11', 32), 'hex')
);
INSERT INTO bond_exchange.sie_exchange_rate_imports
  (request_kind, series_ids, response_body, response_sha256)
VALUES ('latest', ARRAY['SF43718'], '{}'::jsonb, decode(repeat('22', 32), 'hex'));
SQL

archive_migration="20260904150000_archive_legacy_identifiers.sql"
ln -s "$project_root/db/migrations/$archive_migration" "$migration_root/$archive_migration"
DATABASE_URL="$history_url" DBMATE_MIGRATIONS_DIR="$migration_root" dbmate up

psql "$history_url" --no-psqlrc --set ON_ERROR_STOP=1 <<'SQL'
DO $$
DECLARE
  archive_count bigint;
  value_count bigint;
BEGIN
  SELECT count(*) INTO archive_count
  FROM bond_exchange.legacy_identifier_archive;
  IF archive_count < 7 THEN
    RAISE EXCEPTION 'legacy archive contains only % rows, expected at least 7 fixture rows', archive_count;
  END IF;

  SELECT count(*) INTO value_count
  FROM bond_exchange.legacy_identifier_archive
  WHERE (entity_type, attribute_name, legacy_value) IN (
    ('user', 'id', 'historical-user'),
    ('principal', 'id', 'historical-user'),
    ('principal_role_grant', 'id', 'historical-role-grant'),
    ('sale_offer', 'id', 'historical-offer'),
    ('operation_claim', 'id', repeat('a', 64)),
    ('operation_claim', 'idempotency_key', 'historical-nonce'),
    ('sie_exchange_rate_import', 'sequence', '1')
  );
  IF value_count <> 7 THEN
    RAISE EXCEPTION 'archive fixture retained % expected values, expected 7', value_count;
  END IF;
END;
$$;
SQL
