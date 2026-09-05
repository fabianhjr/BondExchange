package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/eventing"
	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/jackc/pgx/v5"
)

func (store *Store) LoadEvent(ctx context.Context, ref eventing.SourceRef) (eventing.Envelope, error) {
	var envelopeID string
	var version uint16
	var completedAt time.Time
	err := store.pool.QueryRow(ctx, `
SELECT uuid_id, schema_version, completed_at
FROM bond_exchange.integration_events
WHERE table_name = $1 AND source_uuid = $2`, ref.TableName, ref.ID).Scan(&envelopeID, &version, &completedAt)
	if err != nil {
		return eventing.Envelope{}, err
	}
	if version != 1 && version != 2 {
		return eventing.Envelope{}, eventing.ErrUnsupportedEvent
	}

	envelope := eventing.Envelope{
		ID:            envelopeID,
		Source:        ref,
		SchemaVersion: version,
		CompletedAt:   completedAt,
	}
	switch ref.TableName {
	case eventing.TableSaleOffers:
		var payload eventing.SaleOfferCreated
		var priceText string
		query := `
SELECT offer.uuid_id, bond.series, offer.price::text, offer.currency_code
FROM bond_exchange.sale_offers AS offer
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = offer.bond_uuid
WHERE offer.uuid_id = $1`
		if version == 2 {
			query = `
SELECT offer.uuid_id, bond.series, canonical.price::text, canonical.currency_code
FROM bond_exchange.sale_offers AS offer
JOIN bond_exchange.sale_offer_canonical_terms AS canonical
  ON canonical.sale_offer_uuid = offer.uuid_id
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = offer.bond_uuid
WHERE offer.uuid_id = $1`
		}
		err = store.pool.QueryRow(ctx, query, ref.ID).Scan(
			&payload.ID,
			&payload.BondSeries,
			&priceText,
			&payload.CurrencyCode,
		)
		if err == nil {
			price, parseErr := exchange.ParsePrice(priceText)
			if parseErr != nil {
				return eventing.Envelope{}, parseErr
			}
			payload.Price = price.String()
		}
		envelope.Type = eventing.TypeSaleOfferCreated
		envelope.Data = payload
	case eventing.TablePurchases:
		var payload eventing.PurchaseRecorded
		var priceText string
		query := `
SELECT
  purchase.sale_offer_uuid,
  bond.series,
  sale_offer.price::text,
  sale_offer.currency_code,
  purchase.bought_at
FROM bond_exchange.purchases AS purchase
JOIN bond_exchange.sale_offers AS sale_offer
  ON sale_offer.uuid_id = purchase.sale_offer_uuid
JOIN bond_exchange.bonds AS bond
  ON bond.uuid_id = sale_offer.bond_uuid
WHERE purchase.uuid_id = $1`
		if version == 2 {
			query = `
SELECT
  purchase.sale_offer_uuid,
  bond.series,
  canonical.price::text,
  canonical.currency_code,
  purchase.bought_at
FROM bond_exchange.purchases AS purchase
JOIN bond_exchange.sale_offers AS sale_offer
  ON sale_offer.uuid_id = purchase.sale_offer_uuid
JOIN bond_exchange.sale_offer_canonical_terms AS canonical
  ON canonical.sale_offer_uuid = sale_offer.uuid_id
JOIN bond_exchange.bonds AS bond
  ON bond.uuid_id = sale_offer.bond_uuid
WHERE purchase.uuid_id = $1`
		}
		err = store.pool.QueryRow(ctx, query, ref.ID).Scan(
			&payload.SaleOfferID,
			&payload.BondSeries,
			&priceText,
			&payload.CurrencyCode,
			&payload.BoughtAt,
		)
		if err == nil {
			price, parseErr := exchange.ParsePrice(priceText)
			if parseErr != nil {
				return eventing.Envelope{}, parseErr
			}
			payload.Price = price.String()
		}
		envelope.Type = eventing.TypePurchaseRecorded
		envelope.Data = payload
	default:
		return eventing.Envelope{}, eventing.ErrUnsupportedEvent
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return eventing.Envelope{}, fmt.Errorf("%w: source fact is missing", eventing.ErrUnsupportedEvent)
	}
	return envelope, err
}

func (store *Store) ClaimEvent(
	ctx context.Context,
	destinationID string,
	ref eventing.SourceRef,
	leaseToken string,
	leaseDuration time.Duration,
	force bool,
) (int, bool, error) {
	var attempt int
	err := store.pool.QueryRow(ctx, `
INSERT INTO bond_exchange.integration_event_deliveries
  (destination_id, event_uuid, attempt_count, lease_nonce, lease_until)
SELECT $1, event.uuid_id, 1, $4, transaction_timestamp() + ($5 * interval '1 microsecond')
FROM bond_exchange.integration_events AS event
WHERE event.table_name = $2 AND event.source_uuid = $3
ON CONFLICT (destination_id, event_uuid) DO UPDATE
SET
  attempt_count = bond_exchange.integration_event_deliveries.attempt_count + 1,
  lease_nonce = EXCLUDED.lease_nonce,
  lease_until = EXCLUDED.lease_until
WHERE bond_exchange.integration_event_deliveries.delivered_at IS NULL
  AND (
    bond_exchange.integration_event_deliveries.lease_until IS NULL
    OR bond_exchange.integration_event_deliveries.lease_until <= transaction_timestamp()
  )
  AND (
    $6
    OR bond_exchange.integration_event_deliveries.next_attempt_at <= transaction_timestamp()
  )
RETURNING attempt_count`, destinationID, ref.TableName, ref.ID, leaseToken, leaseDuration.Microseconds(), force).Scan(&attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return attempt, err == nil, err
}

func (store *Store) MarkEventDelivered(
	ctx context.Context,
	destinationID string,
	ref eventing.SourceRef,
	leaseToken string,
) error {
	result, err := store.pool.Exec(ctx, `
UPDATE bond_exchange.integration_event_deliveries
SET
  delivered_at = transaction_timestamp(),
  lease_nonce = NULL,
  lease_until = NULL,
  last_error_class = NULL
WHERE destination_id = $1
  AND event_uuid = (
    SELECT uuid_id FROM bond_exchange.integration_events
    WHERE table_name = $2 AND source_uuid = $3
  )
  AND lease_nonce = $4
  AND delivered_at IS NULL`, destinationID, ref.TableName, ref.ID, leaseToken)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("integration event delivery lease was lost")
	}
	return nil
}

func (store *Store) MarkEventFailed(
	ctx context.Context,
	destinationID string,
	ref eventing.SourceRef,
	leaseToken string,
	errorClass string,
	retryAfter time.Duration,
) error {
	result, err := store.pool.Exec(ctx, `
UPDATE bond_exchange.integration_event_deliveries
SET
  lease_nonce = NULL,
  lease_until = NULL,
  next_attempt_at = transaction_timestamp() + ($5 * interval '1 microsecond'),
  last_error_class = $6
WHERE destination_id = $1
  AND event_uuid = (
    SELECT uuid_id FROM bond_exchange.integration_events
    WHERE table_name = $2 AND source_uuid = $3
  )
  AND lease_nonce = $4
  AND delivered_at IS NULL`, destinationID, ref.TableName, ref.ID, leaseToken, retryAfter.Microseconds(), errorClass)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("integration event delivery lease was lost")
	}
	return nil
}

func (store *Store) PendingEvents(
	ctx context.Context,
	destinationID string,
	after eventing.SourceRef,
	limit int,
) ([]eventing.SourceRef, error) {
	rows, err := store.pool.Query(ctx, `
SELECT event.table_name, event.source_uuid
FROM bond_exchange.integration_events AS event
LEFT JOIN bond_exchange.integration_event_deliveries AS delivery
  ON delivery.destination_id = $1
  AND delivery.event_uuid = event.uuid_id
WHERE delivery.delivered_at IS NULL
  AND (delivery.lease_until IS NULL OR delivery.lease_until <= transaction_timestamp())
  AND (event.table_name, event.source_uuid) > (
    $2,
    COALESCE(NULLIF($3, '')::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  )
ORDER BY event.table_name, event.source_uuid
LIMIT $4`, destinationID, after.TableName, after.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]eventing.SourceRef, 0, limit)
	for rows.Next() {
		var ref eventing.SourceRef
		if err := rows.Scan(&ref.TableName, &ref.ID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (store *Store) CountPendingEvents(ctx context.Context, destinationID string) (uint64, error) {
	var count uint64
	err := store.pool.QueryRow(ctx, `
SELECT count(*)
FROM bond_exchange.integration_events AS event
LEFT JOIN bond_exchange.integration_event_deliveries AS delivery
  ON delivery.destination_id = $1
  AND delivery.event_uuid = event.uuid_id
WHERE delivery.delivered_at IS NULL`, destinationID).Scan(&count)
	return count, err
}
