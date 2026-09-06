-- migrate:up

-- Contract `bond_exchange.users`. `principals` is now the sole identity table:
-- it generates its own UUIDv7, and the foreign keys validated by the previous
-- migration prove that every seller and buyer is an authenticated principal.
--
-- Apply this only after every application instance that names
-- `bond_exchange.users` is retired. `db:principal-contract-readiness` reports
-- the database side; writer retirement must be shown by release evidence.

-- An identity allocated in `users` but never linked to a principal is the only
-- value this table holds that dropping it would discard. Refuse rather than
-- discard it: `users` is append-only, so the lossless resolution is to append
-- the missing principal fact and link the identity, which this migration must
-- not do on the operator's behalf.
DO $$
DECLARE
  unlinked_count bigint;
BEGIN
  SELECT count(*) INTO unlinked_count
  FROM bond_exchange.users AS app_user
  WHERE NOT EXISTS (
    SELECT 1
    FROM bond_exchange.principals AS principal
    WHERE principal.uuid_id = app_user.uuid_id
  );

  IF unlinked_count > 0 THEN
    RAISE EXCEPTION
      'cannot contract the user identity table: % allocated identit(ies) have no principal',
      unlinked_count
      USING HINT =
        'Append a principal for each affected identity so it survives as a '
        'linked identity, or record the disposition of each one as new facts '
        'in a reviewed corrective forward migration, before contracting.';
  END IF;
END;
$$;

ALTER TABLE bond_exchange.sale_offers
  DROP CONSTRAINT sale_offers_seller_uuid_fkey;
ALTER TABLE bond_exchange.purchases
  DROP CONSTRAINT purchases_buyer_uuid_fkey;
ALTER TABLE bond_exchange.principals
  DROP CONSTRAINT principals_uuid_id_fkey;

-- DROP TABLE removes the append-only trigger and the table's privileges with
-- it. Nothing references `users` once the three foreign keys above are gone.
DROP TABLE bond_exchange.users;

-- `users_are_append_only` was the explicitly declared trigger the guarantee
-- register cited for identity facts. Its counterpart on `principals` was
-- created by a loop in the security migration, so the name appears nowhere in
-- the schema history and cannot be cited as evidence. Redeclare it explicitly,
-- unchanged in behavior, now that it carries that evidence alone.
DROP TRIGGER principals_are_append_only ON bond_exchange.principals;
CREATE TRIGGER principals_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.principals
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'the user identity table was archived and contracted; use a corrective forward migration';
END;
$$;
