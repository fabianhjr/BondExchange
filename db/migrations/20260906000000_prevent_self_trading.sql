-- migrate:up

-- This control is activated only on a history that already satisfies it.
-- Domain facts are append-only, so a violation must never be rewritten away.
DO $$
DECLARE
  self_trade_count bigint;
BEGIN
  SELECT count(*) INTO self_trade_count
  FROM bond_exchange.purchases AS purchase
  JOIN bond_exchange.sale_offers AS offer
    ON offer.uuid_id = purchase.sale_offer_uuid
  WHERE purchase.buyer_uuid = offer.seller_uuid;

  IF self_trade_count <> 0 THEN
    RAISE EXCEPTION 'cannot activate self-trade prevention: % historical self-purchases exist', self_trade_count;
  END IF;
END;
$$;

CREATE FUNCTION bond_exchange.reject_self_purchase()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  offer_seller uuid;
BEGIN
  SELECT offer.seller_uuid INTO offer_seller
  FROM bond_exchange.sale_offers AS offer
  WHERE offer.uuid_id = NEW.sale_offer_uuid;

  IF FOUND AND NEW.buyer_uuid = offer_seller THEN
    RAISE EXCEPTION 'a buyer cannot reserve their own sale offer'
      USING ERRCODE = '23514',
            CONSTRAINT = 'purchases_buyer_not_seller';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER purchases_reject_self_trade
  BEFORE INSERT ON bond_exchange.purchases
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.reject_self_purchase();

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION 'self-trade prevention is active; use a corrective forward migration';
END;
$$;
