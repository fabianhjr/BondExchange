package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/ratelimit"
)

const admitRequestQuery = `
WITH current_window AS (
  SELECT date_trunc('minute', statement_timestamp()) AS started_at
), admitted AS (
  INSERT INTO bond_exchange.principal_rate_limits
    (principal_uuid, window_started_at, request_count, updated_at)
  SELECT $1, started_at, 1, statement_timestamp()
  FROM current_window
  ON CONFLICT (principal_uuid) DO UPDATE
  SET window_started_at = EXCLUDED.window_started_at,
      request_count = CASE
        WHEN principal_rate_limits.window_started_at = EXCLUDED.window_started_at
          THEN principal_rate_limits.request_count + 1
        ELSE 1
      END,
      updated_at = EXCLUDED.updated_at
  WHERE principal_rate_limits.window_started_at <> EXCLUDED.window_started_at
     OR principal_rate_limits.request_count < $2
  RETURNING uuid_id
)
SELECT
  EXISTS (SELECT 1 FROM admitted),
  GREATEST(
    1,
    CEIL(EXTRACT(EPOCH FROM (
      (SELECT started_at FROM current_window) + interval '1 minute' - statement_timestamp()
    )))
  )::bigint`

func (store *Store) AdmitRequest(ctx context.Context, principalID exchange.PrincipalID) error {
	var (
		admitted     bool
		retrySeconds int64
	)
	err := store.pool.QueryRow(
		ctx,
		admitRequestQuery,
		string(principalID),
		ratelimit.RequestsPerMinute,
	).Scan(&admitted, &retrySeconds)
	if err != nil {
		return fmt.Errorf("%w: %w", ratelimit.ErrUnavailable, err)
	}
	if !admitted {
		return &ratelimit.ExceededError{RetryAfter: time.Duration(retrySeconds) * time.Second}
	}
	return nil
}
