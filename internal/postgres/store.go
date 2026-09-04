package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const buyQuery = `
WITH inserted_purchase AS (
  INSERT INTO bond_exchange.purchases (sale_offer_id, buyer_id)
  SELECT sale_offer.id, buyer.id
  FROM bond_exchange.sale_offers AS sale_offer
  CROSS JOIN bond_exchange.users AS buyer
  WHERE sale_offer.id = $1 AND buyer.id = $2
  ON CONFLICT (sale_offer_id) DO NOTHING
  RETURNING sale_offer_id, buyer_id, bought_at
)
SELECT
  sale_offer.id,
  sale_offer.seller_id,
  sale_offer.bond_series,
  sale_offer.price,
  sale_offer.currency_code,
  inserted_purchase.buyer_id,
  inserted_purchase.bought_at
FROM inserted_purchase
JOIN bond_exchange.sale_offers AS sale_offer
  ON sale_offer.id = inserted_purchase.sale_offer_id`

const classifyFailedBuyQuery = `
SELECT
  EXISTS (SELECT 1 FROM bond_exchange.users WHERE id = $1),
  EXISTS (SELECT 1 FROM bond_exchange.sale_offers WHERE id = $2)`

const purchaseByOfferQuery = `
SELECT
  sale_offer.id,
  sale_offer.seller_id,
  sale_offer.bond_series,
  sale_offer.price,
  sale_offer.currency_code,
  purchase.buyer_id,
  purchase.bought_at
FROM bond_exchange.purchases AS purchase
JOIN bond_exchange.sale_offers AS sale_offer
  ON sale_offer.id = purchase.sale_offer_id
WHERE purchase.sale_offer_id = $1`

const createSaleOfferQuery = `
INSERT INTO bond_exchange.sale_offers
  (id, seller_id, bond_series, price, currency_code)
VALUES ($1, $2, $3, $4::numeric, $5)
RETURNING id, seller_id, bond_series, price, currency_code`

const activeOffersQuery = `
SELECT id, seller_id, bond_series, price, currency_code
FROM bond_exchange.active_offers
WHERE bond_series = $1
ORDER BY id`

const activeBondSeriesQuery = `
SELECT DISTINCT bond_series
FROM bond_exchange.active_offers
ORDER BY bond_series`

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
SELECT id, client_class
FROM bond_exchange.principals AS principal
WHERE issuer = $1
  AND subject = $2
  AND NOT EXISTS (
    SELECT 1
    FROM bond_exchange.principal_suspensions AS suspension
    WHERE suspension.principal_id = principal.id
      AND NOT EXISTS (
        SELECT 1
        FROM bond_exchange.principal_reinstatements AS reinstatement
        WHERE reinstatement.suspension_id = suspension.id
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
	return retryTransaction(ctx, func() (exchange.SaleOffer, error) {
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
		string(offer.ID),
		string(offer.SellerID),
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
	price, err := exchange.ParsePrice(priceText)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	created.Price = price
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
	return retryTransaction(ctx, func() (exchange.Purchase, error) {
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
		offerIDValue string
		sellerID     string
		bondSeries   string
		priceText    string
		currencyCode string
		buyerIDValue string
		boughtAt     time.Time
	)
	err = tx.QueryRow(ctx, buyQuery, string(offerID), string(operation.Principal.ID)).Scan(
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
		return exchange.Purchase{}, err
	}

	var buyerExists, offerExists bool
	if classifyErr := tx.QueryRow(
		ctx,
		classifyFailedBuyQuery,
		string(operation.Principal.ID),
		string(offerID),
	).Scan(&buyerExists, &offerExists); classifyErr != nil {
		return exchange.Purchase{}, classifyErr
	}
	if !buyerExists {
		err = exchange.ErrBuyerNotFound
	} else {
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
	rows, err := tx.Query(ctx, activeBondSeriesQuery)
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
  WHERE principal_id = $1 AND permission_id = $2
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
	claimID := operationClaimID(operation)
	var inserted string
	err := tx.QueryRow(ctx, `
INSERT INTO bond_exchange.operation_claims
  (id, principal_id, client_id, operation, idempotency_key, request_digest, assertion_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (principal_id, client_id, operation, idempotency_key) DO NOTHING
RETURNING id`,
		claimID,
		operation.Principal.ID,
		operation.Principal.ClientID,
		operation.Operation,
		operation.IdempotencyKey,
		operation.RequestDigest[:],
		operation.AssertionDigest[:],
	).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimID, false, nil
	}
	if err != nil {
		return "", false, err
	}
	return inserted, true, nil
}

func operationClaimID(operation exchange.MutationContext) string {
	digest := sha256.New()
	for _, part := range []string{
		string(operation.Principal.ID),
		operation.Principal.ClientID,
		operation.Operation,
		operation.IdempotencyKey,
	} {
		_, _ = digest.Write([]byte(part))
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func recordOperationSuccess(ctx context.Context, tx pgx.Tx, claimID string, resourceID string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO bond_exchange.operation_results
  (claim_id, outcome, resource_id)
VALUES ($1, 'succeeded', $2)`, claimID, resourceID)
	return err
}

func recordOperationRejection(ctx context.Context, tx pgx.Tx, claimID string, safeErrorCode string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO bond_exchange.operation_results
  (claim_id, outcome, safe_error_code)
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
SELECT id, seller_id, bond_series, price, currency_code
FROM bond_exchange.sale_offers
WHERE id = $1`, resourceID).Scan(
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
SELECT result.resource_id, result.outcome, COALESCE(result.safe_error_code, ''), claim.request_digest
FROM bond_exchange.operation_claims AS claim
JOIN bond_exchange.operation_results AS result ON result.claim_id = claim.id
WHERE claim.principal_id = $1
  AND claim.client_id = $2
  AND claim.operation = $3
  AND claim.idempotency_key = $4
	`,
		operation.Principal.ID,
		operation.Principal.ClientID,
		operation.Operation,
		operation.IdempotencyKey,
	).Scan(&resourceID, &outcome, &safeErrorCode, &requestDigest)
	if err != nil {
		return "", err
	}
	if !equalDigest(requestDigest, operation.RequestDigest) {
		return "", exchange.ErrIdempotencyConflict
	}
	if outcome == "rejected" {
		return "", operationErrorFromCode(safeErrorCode)
	}
	if outcome != "succeeded" || resourceID == nil {
		return "", errors.New("invalid stored operation result")
	}
	return *resourceID, nil
}

func safeOperationErrorCode(err error) (string, bool) {
	switch {
	case errors.Is(err, exchange.ErrBuyerNotFound):
		return "buyer_not_found", true
	case errors.Is(err, exchange.ErrOfferUnavailable):
		return "offer_unavailable", true
	case errors.Is(err, exchange.ErrOfferAlreadyExists):
		return "offer_already_exists", true
	case errors.Is(err, exchange.ErrSellerNotFound):
		return "seller_not_found", true
	case errors.Is(err, exchange.ErrBondNotFound):
		return "bond_not_found", true
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
	case "offer_already_exists":
		return exchange.ErrOfferAlreadyExists
	case "seller_not_found":
		return exchange.ErrSellerNotFound
	case "bond_not_found":
		return exchange.ErrBondNotFound
	default:
		return errors.New("invalid stored operation error code")
	}
}

func isRetryableTransactionError(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && (databaseError.Code == "40001" || databaseError.Code == "40P01")
}

func retryTransaction[T any](ctx context.Context, operation func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		value, err := operation()
		if !isRetryableTransactionError(err) {
			return value, err
		}
		lastErr = err
		if err := waitBeforeTransactionRetry(ctx, attempt); err != nil {
			return zero, err
		}
	}
	return zero, lastErr
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
	case "sale_offers_seller_id_fkey":
		return exchange.ErrSellerNotFound
	case "sale_offers_bond_series_fkey":
		return exchange.ErrBondNotFound
	default:
		return err
	}
}
