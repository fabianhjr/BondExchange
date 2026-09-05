# shellcheck shell=bash
set -euo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "DATABASE_URL must identify the PostgreSQL 18 database to inspect" >&2
  exit 64
fi

psql "$DATABASE_URL" --no-psqlrc --set ON_ERROR_STOP=1 <<'SQL'
DO $$
DECLARE
  invalid_count bigint;
BEGIN
  SELECT count(*) INTO invalid_count
  FROM bond_exchange.sale_offer_canonical_terms
  WHERE currency_code <> 'MXN';
  IF invalid_count <> 0 THEN
    RAISE EXCEPTION 'canonical terms contain % non-MXN rows', invalid_count;
  END IF;

  SELECT count(*) INTO invalid_count
  FROM bond_exchange.sale_offer_canonical_terms AS canonical
  JOIN bond_exchange.sale_offers AS offer ON offer.uuid_id = canonical.sale_offer_uuid
  LEFT JOIN bond_exchange.sale_offer_submissions AS submission
    ON submission.sale_offer_uuid = offer.uuid_id
  WHERE offer.currency_code <> 'MXN'
     OR offer.price IS DISTINCT FROM canonical.price
     OR submission.sale_offer_uuid IS NULL;
  IF invalid_count <> 0 THEN
    RAISE EXCEPTION 'canonical offer/provenance inconsistency: % rows', invalid_count;
  END IF;

  SELECT count(*) INTO invalid_count
  FROM bond_exchange.sale_offer_submissions AS submission
  LEFT JOIN bond_exchange.sale_offer_conversion_quotes AS quote
    ON quote.uuid_id = submission.conversion_quote_uuid
  WHERE (submission.submitted_currency_code = 'MXN' AND quote.uuid_id IS NOT NULL)
     OR (submission.submitted_currency_code = 'USD' AND (
       quote.uuid_id IS NULL
       OR quote.principal_uuid <> (
         SELECT offer.seller_uuid FROM bond_exchange.sale_offers AS offer
         WHERE offer.uuid_id = submission.sale_offer_uuid
       )
       OR quote.bond_uuid <> (
         SELECT offer.bond_uuid FROM bond_exchange.sale_offers AS offer
         WHERE offer.uuid_id = submission.sale_offer_uuid
       )
       OR quote.submitted_price IS DISTINCT FROM submission.submitted_price
     ));
  IF invalid_count <> 0 THEN
    RAISE EXCEPTION 'submission/quote provenance inconsistency: % rows', invalid_count;
  END IF;

  SELECT count(*) INTO invalid_count
  FROM bond_exchange.sale_offer_conversion_quotes AS quote
  JOIN bond_exchange.sie_exchange_rate_observations AS observation
    ON observation.uuid_id = quote.rate_observation_uuid
  WHERE observation.series_id <> 'SF43718'
     OR observation.base_currency <> 'USD'
     OR observation.quote_currency <> 'MXN'
     OR quote.rounding_policy <> 'half_even_4';
  IF invalid_count <> 0 THEN
    RAISE EXCEPTION 'quote/rate mapping inconsistency: % rows', invalid_count;
  END IF;

  SELECT count(*) INTO invalid_count
  FROM bond_exchange.sale_offers AS offer
  WHERE NOT EXISTS (
    SELECT 1 FROM bond_exchange.purchases AS purchase
    WHERE purchase.sale_offer_uuid = offer.uuid_id
  )
  AND NOT EXISTS (
    SELECT 1 FROM bond_exchange.sale_offer_canonical_terms AS canonical
    WHERE canonical.sale_offer_uuid = offer.uuid_id
  );
  IF invalid_count <> 0 THEN
    RAISE EXCEPTION '% active legacy offers lack seller-accepted canonical MXN terms', invalid_count;
  END IF;

  SELECT count(*) INTO invalid_count
  FROM bond_exchange.purchases AS purchase
  WHERE NOT EXISTS (
    SELECT 1 FROM bond_exchange.sale_offer_canonical_terms AS canonical
    WHERE canonical.sale_offer_uuid = purchase.sale_offer_uuid
  );
  IF invalid_count <> 0 THEN
    RAISE EXCEPTION '% historical purchases lack canonical MXN terms', invalid_count;
  END IF;
END;
$$;
SQL

echo "Canonical MXN data checks passed."
echo "Before activation, also prove that old binaries and compatibility-view readers are drained."
