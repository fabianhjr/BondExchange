-- migrate:up

-- Prove that the retained history also satisfies the principal-referencing
-- foreign keys introduced by the previous migration: every seller and every
-- buyer already is an authenticated principal, not merely an allocated user
-- identity.
--
-- Sale offers and purchases are append-only domain facts, so a seller or buyer
-- that no principal covers cannot be corrected by an UPDATE here. Report it
-- precisely and stop instead: the operator must decide, under review, how to
-- represent the correction as new facts before this migration can proceed.

DO $$
DECLARE
  unlinked_sellers bigint;
  unlinked_buyers bigint;
BEGIN
  SELECT count(*) INTO unlinked_sellers
  FROM bond_exchange.sale_offers AS offer
  WHERE NOT EXISTS (
    SELECT 1
    FROM bond_exchange.principals AS principal
    WHERE principal.uuid_id = offer.seller_uuid
  );

  SELECT count(*) INTO unlinked_buyers
  FROM bond_exchange.purchases AS purchase
  WHERE NOT EXISTS (
    SELECT 1
    FROM bond_exchange.principals AS principal
    WHERE principal.uuid_id = purchase.buyer_uuid
  );

  IF unlinked_sellers > 0 OR unlinked_buyers > 0 THEN
    RAISE EXCEPTION
      'cannot validate principal identity: % sale offer(s) and % purchase(s) are attributed to an identity that is not a principal',
      unlinked_sellers, unlinked_buyers
      USING HINT =
        'Sale offers and purchases are append-only. Append the missing '
        'principal facts for the affected identities, or record the correction '
        'as new facts in a reviewed corrective forward migration, before '
        'applying this validation.';
  END IF;
END;
$$;

ALTER TABLE bond_exchange.sale_offers
  VALIDATE CONSTRAINT sale_offers_seller_principal_fkey;

ALTER TABLE bond_exchange.purchases
  VALIDATE CONSTRAINT purchases_buyer_principal_fkey;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'principal identity validation is deployed; use a corrective forward migration';
END;
$$;
