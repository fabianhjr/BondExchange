-- migrate:up

-- `bond_exchange.users` has carried no attribute of its own since the UUID
-- contraction: it is a single `uuid_id` column, and `principals.uuid_id` is
-- both its own primary key and a foreign key to it. That relationship is
-- strictly one-to-one, so the table cannot express the beneficial ownership
-- its name suggests, while every consumer already treats the two as one
-- identity. `principals` becomes the sole identity table. See ADR-0034.
--
-- Expand first. `principals` starts generating its own identity, and the
-- marketplace facts gain principal-referencing foreign keys alongside the
-- user-referencing ones they already have. NOT VALID constrains every
-- subsequent insert immediately without scanning history, so this migration
-- cannot fail on existing rows, and the previously deployed application keeps
-- working while both relationships hold.

ALTER TABLE bond_exchange.principals
  ALTER COLUMN uuid_id SET DEFAULT uuidv7();

ALTER TABLE bond_exchange.sale_offers
  ADD CONSTRAINT sale_offers_seller_principal_fkey
  FOREIGN KEY (seller_uuid) REFERENCES bond_exchange.principals (uuid_id)
  NOT VALID;

ALTER TABLE bond_exchange.purchases
  ADD CONSTRAINT purchases_buyer_principal_fkey
  FOREIGN KEY (buyer_uuid) REFERENCES bond_exchange.principals (uuid_id)
  NOT VALID;

-- migrate:down

ALTER TABLE bond_exchange.purchases
  DROP CONSTRAINT purchases_buyer_principal_fkey;

ALTER TABLE bond_exchange.sale_offers
  DROP CONSTRAINT sale_offers_seller_principal_fkey;

ALTER TABLE bond_exchange.principals
  ALTER COLUMN uuid_id DROP DEFAULT;
