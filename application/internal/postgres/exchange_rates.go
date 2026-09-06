package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchangerates"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func (store *Store) States(
	ctx context.Context,
	units []exchangerates.WorkUnit,
) (map[string]exchangerates.WorkState, error) {
	states := make(map[string]exchangerates.WorkState, len(units))
	for _, unit := range units {
		var state exchangerates.WorkState
		var leaseUntil, completedAt *time.Time
		err := store.pool.QueryRow(ctx, `
SELECT
  COALESCE(CASE
    WHEN request_kind = 'latest' THEN
      completed_at IS NOT NULL AND fresh_until > statement_timestamp()
    ELSE
      covered_until >= $2::date
      AND ($3 OR fresh_until > statement_timestamp())
  END, FALSE),
  lease_until,
  completed_at
FROM bond_exchange.sie_exchange_rate_fetch_coordination
WHERE work_key = $1`, unit.Key, unit.FetchTo, unit.Permanent).Scan(&state.Ready, &leaseUntil, &completedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			states[unit.Key] = exchangerates.WorkState{}
			continue
		}
		if err != nil {
			return nil, err
		}
		if leaseUntil != nil {
			state.LeaseUntil = *leaseUntil
		}
		if completedAt != nil {
			state.CompletedAt = *completedAt
		}
		states[unit.Key] = state
	}
	return states, nil
}

func (store *Store) Claim(
	ctx context.Context,
	units []exchangerates.WorkUnit,
	leaseNonce string,
	leaseDuration time.Duration,
	force bool,
) ([]exchangerates.WorkUnit, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed := make([]exchangerates.WorkUnit, 0, len(units))
	for _, unit := range units {
		var from, to any
		if unit.Kind == exchangerates.FetchRange {
			from = unit.From
			to = unit.To
		}
		_, err := tx.Exec(ctx, `
INSERT INTO bond_exchange.sie_exchange_rate_fetch_coordination
  (work_key, series_id, base_currency, quote_currency, request_kind, period_start, period_end)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (work_key) DO NOTHING`,
			unit.Key,
			unit.Series.ID,
			unit.Series.Base,
			unit.Series.Quote,
			unit.Kind,
			from,
			to,
		)
		if err != nil {
			return nil, err
		}
		var key string
		err = tx.QueryRow(ctx, `
UPDATE bond_exchange.sie_exchange_rate_fetch_coordination
SET
  lease_nonce = $2,
  lease_until = transaction_timestamp() + ($3 * interval '1 microsecond')
WHERE work_key = $1
  AND series_id = $4
  AND base_currency = $5
  AND quote_currency = $6
  AND request_kind = $7
  AND (lease_until IS NULL OR lease_until <= transaction_timestamp())
  AND next_attempt_at <= transaction_timestamp()
  AND ($10 OR (
    (request_kind = 'latest'
      AND (completed_at IS NULL OR fresh_until <= transaction_timestamp()))
    OR
    (request_kind = 'range'
      AND (
        covered_until IS NULL
        OR covered_until < $8::date
        OR (NOT $9 AND fresh_until <= transaction_timestamp())
      ))
  ))
RETURNING work_key`,
			unit.Key,
			leaseNonce,
			leaseDuration.Microseconds(),
			unit.Series.ID,
			unit.Series.Base,
			unit.Series.Quote,
			unit.Kind,
			unit.FetchTo,
			unit.Permanent,
			force,
		).Scan(&key)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, unit)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (store *Store) Complete(
	ctx context.Context,
	leaseNonce string,
	units []exchangerates.WorkUnit,
	request exchangerates.FetchRequest,
	result exchangerates.FetchResult,
	freshFor time.Duration,
) error {
	if len(units) == 0 || !json.Valid(result.Response) {
		return exchangerates.ErrInvalidObservation
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, unit := range units {
		var owned bool
		if err := tx.QueryRow(ctx, `
SELECT lease_nonce = $2
FROM bond_exchange.sie_exchange_rate_fetch_coordination
WHERE work_key = $1`, unit.Key, leaseNonce).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return errors.New("SIE exchange-rate fetch lease was lost")
		}
	}
	seriesIDs := make([]string, 0, len(request.SeriesIDs))
	for _, seriesID := range request.SeriesIDs {
		seriesIDs = append(seriesIDs, string(seriesID))
	}
	digest := sha256.Sum256(result.Response)
	var periodStart, periodEnd any
	if request.Kind == exchangerates.FetchRange {
		periodStart = request.From
		periodEnd = request.To
	}
	var importID string
	err = tx.QueryRow(ctx, `
INSERT INTO bond_exchange.sie_exchange_rate_imports
  (request_kind, series_ids, period_start, period_end, response_body, response_sha256)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)
RETURNING uuid_id`, request.Kind, seriesIDs, periodStart, periodEnd, result.Response, digest[:]).Scan(&importID)
	if err != nil {
		return err
	}
	seriesByID := make(map[exchangerates.SeriesID]exchangerates.Series, len(units))
	for _, unit := range units {
		seriesByID[unit.Series.ID] = unit.Series
	}
	for _, observation := range result.Observations {
		series, ok := seriesByID[observation.SeriesID]
		if !ok || !observation.Value.IsPositive() || observation.Date.IsZero() {
			return exchangerates.ErrInvalidObservation
		}
		if request.Kind == exchangerates.FetchRange && (observation.Date.Before(request.From) || observation.Date.After(request.To)) {
			return exchangerates.ErrInvalidObservation
		}
		lockKey := string(series.ID) + ":" + series.Base + ":" + series.Quote + ":" + observation.Date.Format(time.DateOnly)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
INSERT INTO bond_exchange.sie_exchange_rate_observations
  (import_uuid, series_id, base_currency, quote_currency, observed_on, value)
SELECT $1, $2, $3, $4, $5, $6::numeric
WHERE NOT EXISTS (
  SELECT 1
  FROM bond_exchange.current_sie_exchange_rates
  WHERE series_id = $2
    AND base_currency = $3
    AND quote_currency = $4
    AND observed_on = $5
    AND value = $6::numeric
)`, importID, series.ID, series.Base, series.Quote, observation.Date, observation.Value.String())
		if err != nil {
			return err
		}
	}
	for _, unit := range units {
		var coveredUntil any
		var freshMicros int64
		if unit.Kind == exchangerates.FetchRange {
			coveredUntil = request.To
		}
		if unit.Kind == exchangerates.FetchLatest || !unit.Permanent {
			freshMicros = freshFor.Microseconds()
		}
		command, err := tx.Exec(ctx, `
UPDATE bond_exchange.sie_exchange_rate_fetch_coordination
SET
  covered_until = CASE
    WHEN $3::date IS NULL THEN covered_until
    WHEN covered_until IS NULL OR covered_until < $3::date THEN $3::date
    ELSE covered_until
  END,
  completed_at = transaction_timestamp(),
  fresh_until = CASE
    WHEN $4::bigint > 0
      THEN transaction_timestamp() + ($4 * interval '1 microsecond')
    ELSE NULL
  END,
  lease_nonce = NULL,
  lease_until = NULL,
  next_attempt_at = transaction_timestamp(),
  last_error_class = NULL
WHERE work_key = $1 AND lease_nonce = $2`, unit.Key, leaseNonce, coveredUntil, freshMicros)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return errors.New("SIE exchange-rate fetch lease was lost")
		}
	}
	return tx.Commit(ctx)
}

func (store *Store) Fail(
	ctx context.Context,
	leaseNonce string,
	units []exchangerates.WorkUnit,
	errorClass string,
	retryAfter time.Duration,
) error {
	for _, unit := range units {
		command, err := store.pool.Exec(ctx, `
UPDATE bond_exchange.sie_exchange_rate_fetch_coordination
SET
  lease_nonce = NULL,
  lease_until = NULL,
  next_attempt_at = transaction_timestamp() + ($3 * interval '1 microsecond'),
  last_error_class = $4
WHERE work_key = $1 AND lease_nonce = $2`, unit.Key, leaseNonce, retryAfter.Microseconds(), errorClass)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return errors.New("SIE exchange-rate fetch lease was lost")
		}
	}
	return nil
}

func (store *Store) LatestObservations(
	ctx context.Context,
	series []exchangerates.Series,
) ([]exchangerates.Observation, error) {
	result := make([]exchangerates.Observation, 0, len(series))
	for _, item := range series {
		observation, err := scanRate(store.pool.QueryRow(ctx, `
SELECT revision_id, series_id, base_currency, quote_currency, observed_on, value::text, recorded_at
FROM bond_exchange.current_sie_exchange_rates
WHERE series_id = $1 AND base_currency = $2 AND quote_currency = $3
ORDER BY observed_on DESC
LIMIT 1`, item.ID, item.Base, item.Quote))
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, observation)
	}
	return result, nil
}

func (store *Store) RangeObservations(
	ctx context.Context,
	series []exchangerates.Series,
	from time.Time,
	to time.Time,
) ([]exchangerates.Observation, error) {
	seriesIDs := make([]string, 0, len(series))
	bases := make([]string, 0, len(series))
	quotes := make([]string, 0, len(series))
	for _, item := range series {
		seriesIDs = append(seriesIDs, string(item.ID))
		bases = append(bases, item.Base)
		quotes = append(quotes, item.Quote)
	}
	result := make([]exchangerates.Observation, 0)
	rows, err := store.pool.Query(ctx, `
WITH requested AS (
  SELECT *
  FROM unnest($1::text[], $2::text[], $3::text[])
    AS item(series_id, base_currency, quote_currency)
)
SELECT revision_id, series_id, base_currency, quote_currency, observed_on, value::text, recorded_at
FROM bond_exchange.current_sie_exchange_rates AS rate
JOIN requested AS item USING (series_id, base_currency, quote_currency)
WHERE observed_on BETWEEN $4 AND $5
ORDER BY series_id, observed_on`, seriesIDs, bases, quotes, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		observation, err := scanRate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, observation)
	}
	return result, rows.Err()
}

type rateScanner interface {
	Scan(...any) error
}

func scanRate(row rateScanner) (exchangerates.Observation, error) {
	var observation exchangerates.Observation
	var value string
	if err := row.Scan(
		&observation.RevisionID,
		&observation.SeriesID,
		&observation.Base,
		&observation.Quote,
		&observation.Date,
		&value,
		&observation.RecordedAt,
	); err != nil {
		return exchangerates.Observation{}, err
	}
	parsed, err := exchangeRateDecimal(value)
	if err != nil {
		return exchangerates.Observation{}, err
	}
	observation.Value = parsed
	return observation, nil
}

func exchangeRateDecimal(value string) (decimal.Decimal, error) {
	parsed, err := decimal.NewFromString(value)
	if err != nil || !parsed.IsPositive() {
		return decimal.Zero, exchangerates.ErrInvalidObservation
	}
	return parsed, nil
}

func (store *Store) ProviderBlockedUntil(ctx context.Context) (time.Time, error) {
	var blockedUntil *time.Time
	if err := store.pool.QueryRow(ctx, `
SELECT blocked_until
FROM bond_exchange.sie_provider_state
WHERE provider_id = 'banxico-sie'`).Scan(&blockedUntil); err != nil {
		return time.Time{}, err
	}
	if blockedUntil == nil {
		return time.Time{}, nil
	}
	return *blockedUntil, nil
}

func (store *Store) BlockProviderUntil(ctx context.Context, blockedUntil time.Time) error {
	_, err := store.pool.Exec(ctx, `
UPDATE bond_exchange.sie_provider_state
SET
  blocked_until = CASE
    WHEN blocked_until IS NULL OR blocked_until < $1 THEN $1
    ELSE blocked_until
  END,
  updated_at = transaction_timestamp()
WHERE provider_id = 'banxico-sie'`, blockedUntil)
	return err
}
