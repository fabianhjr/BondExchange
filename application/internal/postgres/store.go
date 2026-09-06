package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/offerintake"
	"github.com/fabianhjr/BondExchange/application/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const buyQuery = `
WITH inserted_purchase AS (
  INSERT INTO bond_exchange.purchases (sale_offer_uuid, buyer_uuid)
  SELECT sale_offer.uuid_id, buyer.uuid_id
  FROM bond_exchange.sale_offers AS sale_offer
  JOIN bond_exchange.sale_offer_canonical_terms AS canonical
    ON canonical.sale_offer_uuid = sale_offer.uuid_id
  CROSS JOIN bond_exchange.users AS buyer
  WHERE sale_offer.uuid_id = $1
    AND buyer.uuid_id = $2
    AND sale_offer.seller_uuid <> buyer.uuid_id
  ON CONFLICT (sale_offer_uuid) DO NOTHING
  RETURNING uuid_id, sale_offer_uuid, buyer_uuid, bought_at
)
SELECT
  inserted_purchase.uuid_id,
  sale_offer.uuid_id,
  sale_offer.seller_uuid,
  bond.series,
  canonical.price,
  canonical.currency_code,
  inserted_purchase.buyer_uuid,
  inserted_purchase.bought_at
FROM inserted_purchase
JOIN bond_exchange.sale_offers AS sale_offer
  ON sale_offer.uuid_id = inserted_purchase.sale_offer_uuid
JOIN bond_exchange.sale_offer_canonical_terms AS canonical
  ON canonical.sale_offer_uuid = sale_offer.uuid_id
JOIN bond_exchange.bonds AS bond
  ON bond.uuid_id = sale_offer.bond_uuid`

const classifyFailedBuyQuery = `
SELECT
  EXISTS (SELECT 1 FROM bond_exchange.users WHERE uuid_id = $1),
  EXISTS (SELECT 1 FROM bond_exchange.sale_offers WHERE uuid_id = $2),
  EXISTS (
    SELECT 1
    FROM bond_exchange.sale_offers AS offer
    JOIN bond_exchange.sale_offer_canonical_terms AS canonical
      ON canonical.sale_offer_uuid = offer.uuid_id
    WHERE offer.uuid_id = $2
      AND offer.seller_uuid = $1
      AND NOT EXISTS (
        SELECT 1 FROM bond_exchange.purchases AS purchase
        WHERE purchase.sale_offer_uuid = offer.uuid_id
      )
  )`

const purchaseByOfferQuery = `
SELECT
  purchase.uuid_id,
  sale_offer.uuid_id,
  sale_offer.seller_uuid,
  bond.series,
  canonical.price,
  canonical.currency_code,
  purchase.buyer_uuid,
  purchase.bought_at
FROM bond_exchange.purchases AS purchase
JOIN bond_exchange.sale_offers AS sale_offer
  ON sale_offer.uuid_id = purchase.sale_offer_uuid
JOIN bond_exchange.sale_offer_canonical_terms AS canonical
  ON canonical.sale_offer_uuid = sale_offer.uuid_id
JOIN bond_exchange.bonds AS bond
  ON bond.uuid_id = sale_offer.bond_uuid
WHERE purchase.sale_offer_uuid = $1`

const createSaleOfferQuery = `
WITH inserted_offer AS (
  INSERT INTO bond_exchange.sale_offers
    (seller_uuid, bond_uuid, price, currency_code)
  SELECT $1, bond.uuid_id, $3::numeric, $4
  FROM bond_exchange.bonds AS bond
  WHERE bond.series = $2
  RETURNING uuid_id, seller_uuid, bond_uuid, price, currency_code
), inserted_terms AS (
  INSERT INTO bond_exchange.sale_offer_canonical_terms
    (sale_offer_uuid, price, currency_code)
  SELECT uuid_id, price, currency_code
  FROM inserted_offer
  RETURNING sale_offer_uuid
), inserted_submission AS (
  INSERT INTO bond_exchange.sale_offer_submissions
    (sale_offer_uuid, submitted_price, submitted_currency_code)
  SELECT uuid_id, price, currency_code
  FROM inserted_offer
  RETURNING sale_offer_uuid
)
SELECT inserted_offer.uuid_id, inserted_offer.seller_uuid, bond.series,
       inserted_offer.price, inserted_offer.currency_code
FROM inserted_offer
JOIN inserted_terms ON inserted_terms.sale_offer_uuid = inserted_offer.uuid_id
JOIN inserted_submission ON inserted_submission.sale_offer_uuid = inserted_offer.uuid_id
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = inserted_offer.bond_uuid`

const activeOffersQuery = `
SELECT
  offer.uuid_id,
  offer.seller_uuid,
  bond.series,
  canonical.price,
  canonical.currency_code
FROM bond_exchange.sale_offers AS offer
JOIN bond_exchange.sale_offer_canonical_terms AS canonical
  ON canonical.sale_offer_uuid = offer.uuid_id
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = offer.bond_uuid
WHERE bond.series = $1
  AND offer.seller_uuid <> $2
  AND NOT EXISTS (
    SELECT 1 FROM bond_exchange.purchases AS purchase
    WHERE purchase.sale_offer_uuid = offer.uuid_id
  )
ORDER BY offer.uuid_id`

const activeBondSeriesQuery = `
SELECT DISTINCT bond.series
FROM bond_exchange.sale_offers AS offer
JOIN bond_exchange.sale_offer_canonical_terms AS canonical
  ON canonical.sale_offer_uuid = offer.uuid_id
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = offer.bond_uuid
WHERE NOT EXISTS (
  SELECT 1 FROM bond_exchange.purchases AS purchase
  WHERE purchase.sale_offer_uuid = offer.uuid_id
)
  AND offer.seller_uuid <> $1
ORDER BY bond.series`

type Store struct {
	pool *pgxpool.Pool
}

func (store *Store) ResolvePrincipal(
	ctx context.Context,
	issuer string,
	subject string,
) (exchange.Principal, error) {
	var principal exchange.Principal
	err := store.pool.QueryRow(ctx, `
SELECT uuid_id, client_class
FROM bond_exchange.principals AS principal
WHERE issuer = $1
  AND subject = $2
  AND NOT EXISTS (
    SELECT 1
    FROM bond_exchange.principal_suspensions AS suspension
    WHERE suspension.principal_uuid = principal.uuid_id
      AND NOT EXISTS (
        SELECT 1
        FROM bond_exchange.principal_reinstatements AS reinstatement
        WHERE reinstatement.suspension_uuid = suspension.uuid_id
      )
  )`, issuer, subject).Scan(&principal.ID, &principal.ClientClass)
	if err != nil {
		return exchange.Principal{}, err
	}
	return principal, nil
}

func (store *Store) CreateSaleOffer(
	ctx context.Context,
	operation exchange.MutationContext,
	offer exchange.SaleOffer,
) (exchange.SaleOffer, error) {
	return retryTransaction(ctx, exchange.OperationCreateSaleOffer, func() (exchange.SaleOffer, error) {
		return store.createSaleOfferOnce(ctx, operation, offer)
	})
}

func (store *Store) createSaleOfferOnce(
	ctx context.Context,
	operation exchange.MutationContext,
	offer exchange.SaleOffer,
) (exchange.SaleOffer, error) {
	tx, err := store.beginAuthorizedMutation(ctx, operation, exchange.PermissionCreateSaleOffer)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimID, claimed, err := claimOperation(ctx, tx, operation)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	if !claimed {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return exchange.SaleOffer{}, err
		}
		return store.replayCreatedOffer(ctx, operation)
	}

	var (
		created   exchange.SaleOffer
		priceText string
	)
	work, err := tx.Begin(ctx)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	err = work.QueryRow(
		ctx,
		createSaleOfferQuery,
		string(operation.Principal.ID),
		string(offer.BondSeries),
		offer.Price.String(),
		string(offer.Currency),
	).Scan(
		&created.ID,
		&created.SellerID,
		&created.BondSeries,
		&priceText,
		&created.Currency,
	)
	if err != nil {
		_ = work.Rollback(ctx)
		classified := classifyCreateSaleOfferError(err)
		if errors.Is(err, pgx.ErrNoRows) {
			classified = exchange.ErrBondNotFound
		}
		if code, ok := safeOperationErrorCode(classified); ok {
			if err := recordOperationRejection(ctx, tx, claimID, code); err != nil {
				return exchange.SaleOffer{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return exchange.SaleOffer{}, err
			}
		}
		return exchange.SaleOffer{}, classified
	}
	if err := work.Commit(ctx); err != nil {
		return exchange.SaleOffer{}, err
	}
	parsedPrice, err := exchange.ParsePrice(priceText)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	created.Price = parsedPrice
	if err := recordOperationSuccess(ctx, tx, claimID, string(created.ID)); err != nil {
		return exchange.SaleOffer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return exchange.SaleOffer{}, err
	}
	return created, nil
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (store *Store) Ping(ctx context.Context) error {
	return store.pool.Ping(ctx)
}

func (store *Store) Authorize(
	ctx context.Context,
	access exchange.AccessContext,
	permission string,
) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorize(ctx, tx, access, permission); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) Buy(
	ctx context.Context,
	operation exchange.MutationContext,
	offerID exchange.OfferID,
) (exchange.Purchase, error) {
	return retryTransaction(ctx, exchange.OperationBuy, func() (exchange.Purchase, error) {
		return store.buyOnce(ctx, operation, offerID)
	})
}

func (store *Store) buyOnce(
	ctx context.Context,
	operation exchange.MutationContext,
	offerID exchange.OfferID,
) (exchange.Purchase, error) {
	tx, err := store.beginAuthorizedMutation(ctx, operation, exchange.PermissionBuy)
	if err != nil {
		return exchange.Purchase{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimID, claimed, err := claimOperation(ctx, tx, operation)
	if err != nil {
		return exchange.Purchase{}, err
	}
	if !claimed {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return exchange.Purchase{}, err
		}
		return store.replayPurchase(ctx, operation)
	}

	var (
		purchaseID   string
		offerIDValue string
		sellerID     string
		bondSeries   string
		priceText    string
		currencyCode string
		buyerIDValue string
		boughtAt     time.Time
	)
	err = tx.QueryRow(ctx, buyQuery, string(offerID), string(operation.Principal.ID)).Scan(
		&purchaseID,
		&offerIDValue,
		&sellerID,
		&bondSeries,
		&priceText,
		&currencyCode,
		&buyerIDValue,
		&boughtAt,
	)
	if err == nil {
		price, parseErr := exchange.ParsePrice(priceText)
		if parseErr != nil {
			return exchange.Purchase{}, parseErr
		}
		purchase := exchange.Purchase{
			ID: exchange.PurchaseID(purchaseID),
			Offer: exchange.SaleOffer{
				ID:         exchange.OfferID(offerIDValue),
				SellerID:   exchange.UserID(sellerID),
				BondSeries: exchange.BondSeries(bondSeries),
				Price:      price,
				Currency:   exchange.CurrencyCode(currencyCode),
			},
			BuyerID:  exchange.UserID(buyerIDValue),
			BoughtAt: boughtAt,
		}
		if err := recordOperationSuccess(ctx, tx, claimID, string(offerID)); err != nil {
			return exchange.Purchase{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return exchange.Purchase{}, err
		}
		return purchase, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return exchange.Purchase{}, classifyBuyError(err)
	}

	var buyerExists, offerExists, selfTrade bool
	if classifyErr := tx.QueryRow(
		ctx,
		classifyFailedBuyQuery,
		string(operation.Principal.ID),
		string(offerID),
	).Scan(&buyerExists, &offerExists, &selfTrade); classifyErr != nil {
		return exchange.Purchase{}, classifyErr
	}
	switch {
	case !buyerExists:
		err = exchange.ErrBuyerNotFound
	case selfTrade:
		err = exchange.ErrSelfTradeProhibited
	default:
		err = exchange.ErrOfferUnavailable
	}
	code, _ := safeOperationErrorCode(err)
	if recordErr := recordOperationRejection(ctx, tx, claimID, code); recordErr != nil {
		return exchange.Purchase{}, recordErr
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return exchange.Purchase{}, commitErr
	}
	return exchange.Purchase{}, err
}

func (store *Store) StreamActiveOffers(
	ctx context.Context,
	access exchange.AccessContext,
	bondSeries exchange.BondSeries,
	yield func(exchange.SaleOffer) error,
) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorize(ctx, tx, access, exchange.PermissionListActiveOffers); err != nil {
		return err
	}
	rows, err := tx.Query(
		ctx,
		activeOffersQuery,
		string(bondSeries),
		string(access.Principal.ID),
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			offer     exchange.SaleOffer
			priceText string
		)
		if err := rows.Scan(
			&offer.ID,
			&offer.SellerID,
			&offer.BondSeries,
			&priceText,
			&offer.Currency,
		); err != nil {
			return err
		}
		price, err := exchange.ParsePrice(priceText)
		if err != nil {
			return err
		}
		offer.Price = price
		if err := yield(offer); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Store) ActiveBondSeries(
	ctx context.Context,
	access exchange.AccessContext,
) ([]exchange.BondSeries, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorize(ctx, tx, access, exchange.PermissionListBondSeries); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, activeBondSeriesQuery, string(access.Principal.ID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	series := make([]exchange.BondSeries, 0)
	for rows.Next() {
		var bondSeries exchange.BondSeries
		if err := rows.Scan(&bondSeries); err != nil {
			return nil, err
		}
		series = append(series, bondSeries)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return series, nil
}

func (store *Store) beginAuthorizedMutation(
	ctx context.Context,
	operation exchange.MutationContext,
	permission string,
) (pgx.Tx, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	if err := authorize(ctx, tx, operation.AccessContext, permission); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func authorize(ctx context.Context, tx pgx.Tx, access exchange.AccessContext, permission string) error {
	var allowed bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM bond_exchange.effective_principal_permissions
  WHERE principal_id = $1 AND permission_code = $2
)`, access.Principal.ID, permission).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return exchange.ErrPermissionDenied
	}
	return nil
}

func claimOperation(
	ctx context.Context,
	tx pgx.Tx,
	operation exchange.MutationContext,
) (string, bool, error) {
	var inserted string
	err := tx.QueryRow(ctx, `
INSERT INTO bond_exchange.operation_claims
  (principal_uuid, client_id, operation, idempotency_nonce, request_digest, assertion_digest)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (principal_uuid, client_id, operation, idempotency_nonce) DO NOTHING
RETURNING uuid_id`,
		operation.Principal.ID,
		operation.Principal.ClientID,
		operation.Operation,
		operation.IdempotencyKey,
		operation.RequestDigest[:],
		operation.AssertionDigest[:],
	).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		telemetry.RecordIdempotency(ctx, operation.Operation, "storage_error")
		return "", false, err
	}
	telemetry.RecordIdempotency(ctx, operation.Operation, "claimed")
	return inserted, true, nil
}

func recordOperationSuccess(ctx context.Context, tx pgx.Tx, claimID string, resourceID string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO bond_exchange.operation_results
  (claim_uuid, outcome, resource_uuid)
VALUES ($1, 'succeeded', $2)`, claimID, resourceID)
	return err
}

func recordOperationRejection(ctx context.Context, tx pgx.Tx, claimID string, safeErrorCode string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO bond_exchange.operation_results
  (claim_uuid, outcome, safe_error_code)
VALUES ($1, 'rejected', $2)`, claimID, safeErrorCode)
	return err
}

func (store *Store) replayPurchase(
	ctx context.Context,
	operation exchange.MutationContext,
) (exchange.Purchase, error) {
	resourceID, err := store.replayResource(ctx, operation)
	if err != nil {
		return exchange.Purchase{}, err
	}
	return scanPurchase(store.pool.QueryRow(ctx, purchaseByOfferQuery, resourceID))
}

func (store *Store) replayCreatedOffer(
	ctx context.Context,
	operation exchange.MutationContext,
) (exchange.SaleOffer, error) {
	resourceID, err := store.replayResource(ctx, operation)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	var offer exchange.SaleOffer
	var priceText string
	err = store.pool.QueryRow(ctx, `
SELECT offer.uuid_id, offer.seller_uuid, bond.series, canonical.price, canonical.currency_code
FROM bond_exchange.sale_offers AS offer
JOIN bond_exchange.sale_offer_canonical_terms AS canonical
  ON canonical.sale_offer_uuid = offer.uuid_id
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = offer.bond_uuid
WHERE offer.uuid_id = $1`, resourceID).Scan(
		&offer.ID,
		&offer.SellerID,
		&offer.BondSeries,
		&priceText,
		&offer.Currency,
	)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	offer.Price, err = exchange.ParsePrice(priceText)
	return offer, err
}

func (store *Store) replayResource(
	ctx context.Context,
	operation exchange.MutationContext,
) (string, error) {
	var resourceID *string
	var outcome, safeErrorCode string
	var requestDigest []byte
	err := store.pool.QueryRow(ctx, `
SELECT result.resource_uuid, result.outcome, COALESCE(result.safe_error_code, ''), claim.request_digest
FROM bond_exchange.operation_claims AS claim
JOIN bond_exchange.operation_results AS result ON result.claim_uuid = claim.uuid_id
WHERE claim.principal_uuid = $1
  AND claim.client_id = $2
  AND claim.operation = $3
  AND claim.idempotency_nonce = $4
	`,
		operation.Principal.ID,
		operation.Principal.ClientID,
		operation.Operation,
		operation.IdempotencyKey,
	).Scan(&resourceID, &outcome, &safeErrorCode, &requestDigest)
	if err != nil {
		telemetry.RecordIdempotency(ctx, operation.Operation, "storage_error")
		return "", err
	}
	if !equalDigest(requestDigest, operation.RequestDigest) {
		telemetry.RecordIdempotency(ctx, operation.Operation, "conflict")
		return "", exchange.ErrIdempotencyConflict
	}
	if outcome == "rejected" {
		telemetry.RecordIdempotency(ctx, operation.Operation, "replayed")
		return "", operationErrorFromCode(safeErrorCode)
	}
	if outcome != "succeeded" || resourceID == nil {
		telemetry.RecordIdempotency(ctx, operation.Operation, "storage_error")
		return "", errors.New("invalid stored operation result")
	}
	telemetry.RecordIdempotency(ctx, operation.Operation, "replayed")
	return *resourceID, nil
}

func safeOperationErrorCode(err error) (string, bool) {
	switch {
	case errors.Is(err, exchange.ErrBuyerNotFound):
		return "buyer_not_found", true
	case errors.Is(err, exchange.ErrOfferUnavailable):
		return "offer_unavailable", true
	case errors.Is(err, exchange.ErrSelfTradeProhibited):
		return "self_trade_prohibited", true
	case errors.Is(err, exchange.ErrOfferAlreadyExists):
		return "offer_already_exists", true
	case errors.Is(err, exchange.ErrSellerNotFound):
		return "seller_not_found", true
	case errors.Is(err, exchange.ErrBondNotFound):
		return "bond_not_found", true
	case errors.Is(err, offerintake.ErrConversionQuoteUnavailable):
		return "conversion_quote_unavailable", true
	default:
		return "", false
	}
}

func operationErrorFromCode(code string) error {
	switch code {
	case "buyer_not_found":
		return exchange.ErrBuyerNotFound
	case "offer_unavailable":
		return exchange.ErrOfferUnavailable
	case "self_trade_prohibited":
		return exchange.ErrSelfTradeProhibited
	case "offer_already_exists":
		return exchange.ErrOfferAlreadyExists
	case "seller_not_found":
		return exchange.ErrSellerNotFound
	case "bond_not_found":
		return exchange.ErrBondNotFound
	case "conversion_quote_unavailable":
		return offerintake.ErrConversionQuoteUnavailable
	default:
		return errors.New("invalid stored operation error code")
	}
}

func classifyBuyError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.ConstraintName == "purchases_buyer_not_seller" {
		return exchange.ErrSelfTradeProhibited
	}
	return err
}

func isRetryableTransactionError(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && (databaseError.Code == "40001" || databaseError.Code == "40P01")
}

func retryTransaction[T any](ctx context.Context, operationName string, operation func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		value, err := operation()
		if !isRetryableTransactionError(err) {
			return value, err
		}
		telemetry.RecordDatabaseRetry(ctx, operationName, retryReason(err))
		lastErr = err
		if err := waitBeforeTransactionRetry(ctx, attempt); err != nil {
			return zero, err
		}
	}
	return zero, lastErr
}

func retryReason(err error) string {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "40P01" {
		return "deadlock"
	}
	return "serialization_failure"
}

func waitBeforeTransactionRetry(ctx context.Context, attempt int) error {
	delay := time.Millisecond << min(attempt, 6)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func equalDigest(actual []byte, expected [sha256.Size]byte) bool {
	if len(actual) != sha256.Size {
		return false
	}
	var difference byte
	for index := range actual {
		difference |= actual[index] ^ expected[index]
	}
	return difference == 0
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPurchase(row rowScanner) (exchange.Purchase, error) {
	var purchase exchange.Purchase
	var priceText string
	err := row.Scan(
		&purchase.ID,
		&purchase.Offer.ID,
		&purchase.Offer.SellerID,
		&purchase.Offer.BondSeries,
		&priceText,
		&purchase.Offer.Currency,
		&purchase.BuyerID,
		&purchase.BoughtAt,
	)
	if err != nil {
		return exchange.Purchase{}, err
	}
	price, err := exchange.ParsePrice(priceText)
	if err != nil {
		return exchange.Purchase{}, fmt.Errorf("parse stored price: %w", err)
	}
	purchase.Offer.Price = price
	return purchase, nil
}

func classifyCreateSaleOfferError(err error) error {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return err
	}
	switch databaseError.ConstraintName {
	case "sale_offers_pkey":
		return exchange.ErrOfferAlreadyExists
	case "sale_offers_seller_uuid_fkey":
		return exchange.ErrSellerNotFound
	case "sale_offers_bond_uuid_fkey":
		return exchange.ErrBondNotFound
	default:
		return err
	}
}
