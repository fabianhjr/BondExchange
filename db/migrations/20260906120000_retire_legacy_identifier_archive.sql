-- migrate:up

-- ADR-0033 records the accepted end of the retention period for the
-- pre-UUID identifier evidence. The runtime has never read this table, and
-- every deployed database must have a verified pre-migration backup because
-- this deletion is intentionally irreversible.
DROP TABLE bond_exchange.legacy_identifier_archive;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION 'retired legacy identifier evidence can only be recovered from a pre-migration backup';
END;
$$;
