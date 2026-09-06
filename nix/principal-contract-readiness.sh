# shellcheck shell=bash
set -euo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "DATABASE_URL must identify the PostgreSQL 18 database to inspect" >&2
  exit 64
fi

psql "$DATABASE_URL" --no-psqlrc --set ON_ERROR_STOP=1 <<'SQL'
DO $$
DECLARE
  unlinked_count bigint;
  seller_count bigint;
  buyer_count bigint;
  validated_key_count bigint;
  principal_default text;
BEGIN
  -- Every seller and buyer must already be a principal. This holds before the
  -- contract migration, because the previous migration validated the
  -- principal-referencing foreign keys, and after it, because those keys are
  -- then the only ones the columns carry. Checking it in both states keeps the
  -- gate meaningful wherever it runs.
  SELECT count(*) INTO seller_count
  FROM bond_exchange.sale_offers AS offer
  WHERE NOT EXISTS (
    SELECT 1 FROM bond_exchange.principals AS principal
    WHERE principal.uuid_id = offer.seller_uuid
  );
  IF seller_count <> 0 THEN
    RAISE EXCEPTION '% sale offer(s) are attributed to an identity that is not a principal', seller_count;
  END IF;

  SELECT count(*) INTO buyer_count
  FROM bond_exchange.purchases AS purchase
  WHERE NOT EXISTS (
    SELECT 1 FROM bond_exchange.principals AS principal
    WHERE principal.uuid_id = purchase.buyer_uuid
  );
  IF buyer_count <> 0 THEN
    RAISE EXCEPTION '% purchase(s) are attributed to an identity that is not a principal', buyer_count;
  END IF;

  SELECT column_default INTO principal_default
  FROM information_schema.columns
  WHERE table_schema = 'bond_exchange'
    AND table_name = 'principals'
    AND column_name = 'uuid_id';
  IF principal_default IS DISTINCT FROM 'uuidv7()' THEN
    RAISE EXCEPTION 'principals.uuid_id must generate its own identity; default is %',
      coalesce(principal_default, 'absent');
  END IF;

  IF to_regclass('bond_exchange.users') IS NULL THEN
    RAISE NOTICE 'the user identity table is already contracted; principals is the sole identity table';
    RETURN;
  END IF;

  SELECT count(*) INTO validated_key_count
  FROM pg_constraint
  WHERE contype = 'f'
    AND convalidated
    AND conname IN ('sale_offers_seller_principal_fkey', 'purchases_buyer_principal_fkey');
  IF validated_key_count <> 2 THEN
    RAISE EXCEPTION
      'contraction requires both principal foreign keys to be present and validated; % of 2 found',
      validated_key_count;
  END IF;

  -- An identity allocated in `users` but never linked to a principal is the
  -- only value the table holds that contraction would discard, and the contract
  -- migration refuses rather than discarding it. Fail here too, so the block is
  -- reported before the migration is attempted.
  SELECT count(*) INTO unlinked_count
  FROM bond_exchange.users AS app_user
  WHERE NOT EXISTS (
    SELECT 1 FROM bond_exchange.principals AS principal
    WHERE principal.uuid_id = app_user.uuid_id
  );
  IF unlinked_count <> 0 THEN
    RAISE EXCEPTION
      '% allocated identit(ies) have no principal; append one for each before contracting',
      unlinked_count;
  END IF;
  RAISE NOTICE 'the user identity table is ready to contract';
END;
$$;
SQL

echo "Principal identity data checks passed."
echo "Before contracting production, also prove that every application instance and direct writer"
echo "naming bond_exchange.users is retired and its credentials are revoked."
