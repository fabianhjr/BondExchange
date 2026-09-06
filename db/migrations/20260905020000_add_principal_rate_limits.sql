-- migrate:up

-- Request windows are mutable, bounded operational coordination. They are not
-- domain or audit facts: PostgreSQL updates one row per principal so every
-- stateless server instance observes the same authenticated allowance.
CREATE TABLE bond_exchange.principal_rate_limits (
  uuid_id uuid PRIMARY KEY DEFAULT uuidv7(),
  principal_uuid uuid NOT NULL UNIQUE
    REFERENCES bond_exchange.principals (uuid_id),
  window_started_at timestamptz NOT NULL,
  request_count integer NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
  CONSTRAINT principal_rate_limits_uuid_v7
    CHECK (uuid_extract_version(uuid_id) = 7),
  CONSTRAINT principal_rate_limits_window_aligned
    CHECK (window_started_at = date_trunc('minute', window_started_at)),
  CONSTRAINT principal_rate_limits_request_count
    CHECK (request_count BETWEEN 1 AND 100),
  CONSTRAINT principal_rate_limits_updated_after_window
    CHECK (updated_at >= window_started_at)
);

-- migrate:down

-- This table contains only disposable admission coordination, so removing it
-- does not discard domain, authorization, or audit facts.
DROP TABLE bond_exchange.principal_rate_limits;
