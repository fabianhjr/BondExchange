-- migrate:up

-- Prove that the retained history also satisfies the canonical currency
-- constraint introduced by the previous migration.
--
-- Sale offers are append-only domain facts, so a nonconforming row cannot be
-- corrected by an UPDATE here. Report it precisely and stop instead: the
-- operator must decide, under review, how to represent the correction as new
-- facts before this migration can proceed. Failing loudly with a count is the
-- safe outcome; silently accepting or discarding a fact is not.

DO $$
DECLARE
  nonconforming bigint;
BEGIN
  SELECT count(*) INTO nonconforming
  FROM bond_exchange.sale_offers
  WHERE currency_code !~ '^[A-Z]{3}$';

  IF nonconforming > 0 THEN
    RAISE EXCEPTION
      'cannot validate sale_offers_currency_code_canonical: % sale offer(s) have a non-canonical currency code',
      nonconforming
      USING HINT =
        'Sale offers are append-only. Review the affected rows and record the '
        'correction as new facts in a reviewed corrective forward migration '
        'before applying this validation.';
  END IF;
END;
$$;

ALTER TABLE bond_exchange.sale_offers
  VALIDATE CONSTRAINT sale_offers_currency_code_canonical;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION 'currency-code validation is deployed; use a corrective forward migration';
END;
$$;
