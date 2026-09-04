-- migrate:up

CREATE TABLE bond_exchange.sie_exchange_rate_imports (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  request_kind text NOT NULL,
  series_ids text[] NOT NULL,
  period_start date,
  period_end date,
  response_body jsonb NOT NULL,
  response_sha256 bytea NOT NULL,
  fetched_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT sie_exchange_rate_imports_kind_known
    CHECK (request_kind IN ('latest', 'range')),
  CONSTRAINT sie_exchange_rate_imports_series_count
    CHECK (cardinality(series_ids) BETWEEN 1 AND 20),
  CONSTRAINT sie_exchange_rate_imports_series_canonical
    CHECK (array_to_string(series_ids, ',') ~ '^[A-Z]{2}[0-9]{1,20}(,[A-Z]{2}[0-9]{1,20})*$'),
  CONSTRAINT sie_exchange_rate_imports_period_shape
    CHECK (
      (request_kind = 'latest' AND period_start IS NULL AND period_end IS NULL)
      OR
      (request_kind = 'range' AND period_start IS NOT NULL AND period_end >= period_start)
    ),
  CONSTRAINT sie_exchange_rate_imports_response_sha256
    CHECK (octet_length(response_sha256) = 32),
  CONSTRAINT sie_exchange_rate_imports_response_size
    CHECK (octet_length(response_body::text) <= 1048576)
);

CREATE TABLE bond_exchange.sie_exchange_rate_observations (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  import_id bigint NOT NULL
    REFERENCES bond_exchange.sie_exchange_rate_imports (id),
  series_id text NOT NULL,
  base_currency text NOT NULL,
  quote_currency text NOT NULL,
  observed_on date NOT NULL,
  value numeric NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT sie_exchange_rate_observations_series_canonical
    CHECK (series_id ~ '^[A-Z]{2}[0-9]{1,20}$'),
  CONSTRAINT sie_exchange_rate_observations_base_currency
    CHECK (base_currency ~ '^[A-Z]{3}$'),
  CONSTRAINT sie_exchange_rate_observations_quote_currency
    CHECK (quote_currency ~ '^[A-Z]{3}$'),
  CONSTRAINT sie_exchange_rate_observations_distinct_currencies
    CHECK (base_currency <> quote_currency),
  CONSTRAINT sie_exchange_rate_observations_value_positive
    CHECK (value <> 'NaN'::numeric AND value > 0),
  CONSTRAINT sie_exchange_rate_observations_revision_unique
    UNIQUE (import_id, series_id, base_currency, quote_currency, observed_on)
);

CREATE INDEX sie_exchange_rate_observations_lookup_idx
  ON bond_exchange.sie_exchange_rate_observations
    (series_id, base_currency, quote_currency, observed_on, id DESC);

CREATE VIEW bond_exchange.current_sie_exchange_rates AS
SELECT DISTINCT ON (series_id, base_currency, quote_currency, observed_on)
  series_id,
  base_currency,
  quote_currency,
  observed_on,
  value,
  recorded_at,
  id AS revision_id
FROM bond_exchange.sie_exchange_rate_observations
ORDER BY
  series_id,
  base_currency,
  quote_currency,
  observed_on,
  id DESC;

CREATE TABLE bond_exchange.sie_exchange_rate_fetch_coordination (
  work_key text PRIMARY KEY,
  series_id text NOT NULL,
  base_currency text NOT NULL,
  quote_currency text NOT NULL,
  request_kind text NOT NULL,
  period_start date,
  period_end date,
  covered_until date,
  completed_at timestamptz,
  fresh_until timestamptz,
  lease_token text,
  lease_until timestamptz,
  next_attempt_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  last_error_class text,
  CONSTRAINT sie_exchange_rate_fetch_work_key_size
    CHECK (octet_length(work_key) BETWEEN 1 AND 128),
  CONSTRAINT sie_exchange_rate_fetch_series_canonical
    CHECK (series_id ~ '^[A-Z]{2}[0-9]{1,20}$'),
  CONSTRAINT sie_exchange_rate_fetch_base_currency
    CHECK (base_currency ~ '^[A-Z]{3}$'),
  CONSTRAINT sie_exchange_rate_fetch_quote_currency
    CHECK (quote_currency ~ '^[A-Z]{3}$'),
  CONSTRAINT sie_exchange_rate_fetch_distinct_currencies
    CHECK (base_currency <> quote_currency),
  CONSTRAINT sie_exchange_rate_fetch_kind_known
    CHECK (request_kind IN ('latest', 'range')),
  CONSTRAINT sie_exchange_rate_fetch_period_shape
    CHECK (
      (request_kind = 'latest'
        AND period_start IS NULL AND period_end IS NULL AND covered_until IS NULL)
      OR
      (request_kind = 'range'
        AND period_start IS NOT NULL
        AND period_end >= period_start
        AND (covered_until IS NULL OR covered_until BETWEEN period_start AND period_end))
    ),
  CONSTRAINT sie_exchange_rate_fetch_lease_shape
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
  CONSTRAINT sie_exchange_rate_fetch_completion_shape
    CHECK (completed_at IS NOT NULL OR fresh_until IS NULL)
);

CREATE INDEX sie_exchange_rate_fetch_retry_idx
  ON bond_exchange.sie_exchange_rate_fetch_coordination
    (next_attempt_at, lease_until);

CREATE TABLE bond_exchange.sie_provider_state (
  provider_id text PRIMARY KEY,
  blocked_until timestamptz,
  updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
  CONSTRAINT sie_provider_state_known
    CHECK (provider_id = 'banxico-sie')
);

INSERT INTO bond_exchange.sie_provider_state (provider_id)
VALUES ('banxico-sie');

CREATE TRIGGER sie_exchange_rate_imports_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.sie_exchange_rate_imports
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

CREATE TRIGGER sie_exchange_rate_observations_are_append_only
  BEFORE UPDATE OR DELETE OR TRUNCATE ON bond_exchange.sie_exchange_rate_observations
  FOR EACH STATEMENT EXECUTE FUNCTION bond_exchange.reject_domain_fact_mutation();

REVOKE UPDATE, DELETE, TRUNCATE
  ON bond_exchange.sie_exchange_rate_imports,
     bond_exchange.sie_exchange_rate_observations
  FROM PUBLIC;

-- migrate:down

DO $$
BEGIN
  RAISE EXCEPTION
    'SIE imports and exchange-rate observations are durable facts; roll forward';
END;
$$;
