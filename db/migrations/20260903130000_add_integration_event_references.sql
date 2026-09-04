-- migrate:up

CREATE TABLE bond_exchange.integration_events (
  table_name text NOT NULL,
  id text NOT NULL,
  schema_version smallint NOT NULL,
  completed_at timestamptz NOT NULL,
  CONSTRAINT integration_events_primary_key PRIMARY KEY (table_name, id),
  CONSTRAINT integration_events_table_known
    CHECK (table_name IN ('sale_offers', 'purchases')),
  CONSTRAINT integration_events_schema_version_positive
    CHECK (schema_version > 0)
);

CREATE TABLE bond_exchange.integration_event_deliveries (
  destination_id text NOT NULL,
  table_name text NOT NULL,
  id text NOT NULL,
  attempt_count integer NOT NULL DEFAULT 0,
  lease_token text,
  lease_until timestamptz,
  next_attempt_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  delivered_at timestamptz,
  last_error_class text,
  CONSTRAINT integration_event_deliveries_primary_key
    PRIMARY KEY (destination_id, table_name, id),
  CONSTRAINT integration_event_deliveries_event_foreign_key
    FOREIGN KEY (table_name, id)
    REFERENCES bond_exchange.integration_events (table_name, id),
  CONSTRAINT integration_event_deliveries_destination_not_empty
    CHECK (destination_id <> ''),
  CONSTRAINT integration_event_deliveries_attempt_count_nonnegative
    CHECK (attempt_count >= 0),
  CONSTRAINT integration_event_deliveries_lease_shape
    CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);

CREATE INDEX integration_event_deliveries_pending_idx
  ON bond_exchange.integration_event_deliveries
    (destination_id, delivered_at, lease_until, next_attempt_at);

CREATE TRIGGER integration_events_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.integration_events
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

REVOKE UPDATE, DELETE, TRUNCATE
  ON bond_exchange.integration_events
  FROM PUBLIC;

CREATE FUNCTION bond_exchange.record_integration_event_on_completion()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
  completed_operation text;
  source_table text;
BEGIN
  IF NEW.outcome <> 'succeeded' THEN
    RETURN NEW;
  END IF;

  SELECT operation
  INTO STRICT completed_operation
  FROM bond_exchange.operation_claims
  WHERE id = NEW.claim_id;

  CASE completed_operation
    WHEN 'offers.create' THEN
      source_table := 'sale_offers';
      IF NOT EXISTS (
        SELECT 1 FROM bond_exchange.sale_offers WHERE id = NEW.resource_id
      ) THEN
        RAISE EXCEPTION 'successful offer operation references a missing source fact'
          USING ERRCODE = '23503';
      END IF;
    WHEN 'purchases.buy' THEN
      source_table := 'purchases';
      IF NOT EXISTS (
        SELECT 1 FROM bond_exchange.purchases WHERE sale_offer_id = NEW.resource_id
      ) THEN
        RAISE EXCEPTION 'successful purchase operation references a missing source fact'
          USING ERRCODE = '23503';
      END IF;
    ELSE RETURN NEW;
  END CASE;

  INSERT INTO bond_exchange.integration_events
    (table_name, id, schema_version, completed_at)
  VALUES
    (source_table, NEW.resource_id, 1, NEW.completed_at);

  RETURN NEW;
END;
$$;

REVOKE ALL
  ON FUNCTION bond_exchange.record_integration_event_on_completion()
  FROM PUBLIC;

CREATE TRIGGER operation_success_records_integration_event
  AFTER INSERT ON bond_exchange.operation_results
  FOR EACH ROW EXECUTE FUNCTION bond_exchange.record_integration_event_on_completion();

INSERT INTO bond_exchange.permissions (id, description)
VALUES ('events.publish', 'Manually publish every pending integration event.');

INSERT INTO bond_exchange.role_permission_grants
  (id, role_id, permission_id, reason)
VALUES
  ('bootstrap-operator-events-publish', 'operator', 'events.publish',
   'Allow operators to trigger recovery of pending integration-event deliveries.');

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'integration event references and delivery history have no lossless down migration; roll forward';
END;
$$;
