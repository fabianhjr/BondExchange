package postgres

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/offerintake"
	"github.com/fabianhjr/BondExchange/application/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

func (store *Store) ReplayConversionQuote(
	ctx context.Context,
	operation exchange.MutationContext,
) (offerintake.Quote, bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return offerintake.Quote{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorize(ctx, tx, operation.AccessContext, exchange.PermissionQuoteSaleOffer); err != nil {
		return offerintake.Quote{}, false, err
	}
	var resourceID *string
	var requestDigest []byte
	err = tx.QueryRow(ctx, `
SELECT result.resource_uuid, claim.request_digest
FROM bond_exchange.operation_claims AS claim
JOIN bond_exchange.operation_results AS result ON result.claim_uuid = claim.uuid_id
WHERE claim.principal_uuid = $1
  AND claim.client_id = $2
  AND claim.operation = $3
  AND claim.idempotency_nonce = $4
  AND result.outcome = 'succeeded'`,
		operation.Principal.ID,
		operation.Principal.ClientID,
		operation.Operation,
		operation.IdempotencyKey,
	).Scan(&resourceID, &requestDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return offerintake.Quote{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return offerintake.Quote{}, false, err
	}
	if len(requestDigest) != sha256.Size || !equalDigest(requestDigest, operation.RequestDigest) {
		return offerintake.Quote{}, false, exchange.ErrIdempotencyConflict
	}
	if resourceID == nil {
		return offerintake.Quote{}, false, errors.New("successful quote operation has no resource")
	}
	quote, err := scanConversionQuote(tx.QueryRow(ctx, conversionQuoteByIDQuery, *resourceID))
	if err != nil {
		return offerintake.Quote{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return offerintake.Quote{}, false, err
	}
	return quote, true, nil
}

const conversionQuoteByIDQuery = `
SELECT
  quote.uuid_id,
  quote.principal_uuid,
  bond.series,
  quote.submitted_price::text,
  quote.canonical_mxn_price::text,
  observation.value::text,
  quote.rate_observation_uuid,
  observation.observed_on,
  quote.expires_at
FROM bond_exchange.sale_offer_conversion_quotes AS quote
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = quote.bond_uuid
JOIN bond_exchange.sie_exchange_rate_observations AS observation
  ON observation.uuid_id = quote.rate_observation_uuid
WHERE quote.uuid_id = $1`

func (store *Store) CreateConversionQuote(
	ctx context.Context,
	operation exchange.MutationContext,
	draft offerintake.QuoteDraft,
) (offerintake.Quote, error) {
	return retryTransaction(ctx, func() (offerintake.Quote, error) {
		tx, err := store.beginAuthorizedMutation(ctx, operation, exchange.PermissionQuoteSaleOffer)
		if err != nil {
			return offerintake.Quote{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		claimID, claimed, err := claimOperation(ctx, tx, operation)
		if err != nil {
			return offerintake.Quote{}, err
		}
		if !claimed {
			telemetry.RecordIdempotency(ctx, exchange.OperationQuoteSaleOffer, "replayed")
			if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				return offerintake.Quote{}, err
			}
			resourceID, err := store.replayResource(ctx, operation)
			if err != nil {
				return offerintake.Quote{}, err
			}
			return scanConversionQuote(store.pool.QueryRow(ctx, conversionQuoteByIDQuery, resourceID))
		}
		var quoteID string
		err = tx.QueryRow(ctx, `
INSERT INTO bond_exchange.sale_offer_conversion_quotes
  (principal_uuid, bond_uuid, submitted_price, submitted_currency_code,
   canonical_mxn_price, rate_observation_uuid, rounding_policy, expires_at)
SELECT $1, bond.uuid_id, $3::numeric, 'USD', $4::numeric,
       observation.uuid_id, $7, $8
FROM bond_exchange.bonds AS bond
JOIN bond_exchange.sie_exchange_rate_observations AS observation
  ON observation.uuid_id = $5
 AND observation.series_id = 'SF43718'
 AND observation.base_currency = 'USD'
 AND observation.quote_currency = 'MXN'
 AND observation.observed_on = $6
WHERE bond.series = $2
RETURNING uuid_id`,
			operation.Principal.ID,
			draft.BondSeries,
			draft.SubmittedPrice.String(),
			draft.MXNPrice.String(),
			draft.RateRevisionID,
			draft.RateObservedOn,
			offerintake.RoundingPolicy,
			draft.ExpiresAt,
		).Scan(&quoteID)
		if errors.Is(err, pgx.ErrNoRows) {
			var bondExists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bond_exchange.bonds WHERE series = $1)`, draft.BondSeries).Scan(&bondExists); err != nil {
				return offerintake.Quote{}, err
			}
			if !bondExists {
				if err := recordOperationRejection(ctx, tx, claimID, "bond_not_found"); err != nil {
					return offerintake.Quote{}, err
				}
				if err := tx.Commit(ctx); err != nil {
					return offerintake.Quote{}, err
				}
				return offerintake.Quote{}, exchange.ErrBondNotFound
			}
			return offerintake.Quote{}, offerintake.ErrExchangeRateUnavailable
		}
		if err != nil {
			return offerintake.Quote{}, err
		}
		if err := recordOperationSuccess(ctx, tx, claimID, quoteID); err != nil {
			return offerintake.Quote{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return offerintake.Quote{}, err
		}
		return scanConversionQuote(store.pool.QueryRow(ctx, conversionQuoteByIDQuery, quoteID))
	})
}

func (store *Store) CreateSaleOfferFromSubmission(
	ctx context.Context,
	operation exchange.MutationContext,
	submission offerintake.Submission,
) (exchange.SaleOffer, error) {
	return retryTransaction(ctx, func() (exchange.SaleOffer, error) {
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
			telemetry.RecordIdempotency(ctx, exchange.OperationCreateSaleOffer, "replayed")
			if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				return exchange.SaleOffer{}, err
			}
			return store.replayCreatedOffer(ctx, operation)
		}

		canonicalPrice := submission.Price
		var quoteID any
		if submission.Currency == offerintake.USD {
			quoteID = string(submission.QuoteID)
			var canonicalText string
			err := tx.QueryRow(ctx, `
SELECT quote.canonical_mxn_price::text
FROM bond_exchange.sale_offer_conversion_quotes AS quote
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = quote.bond_uuid
WHERE quote.uuid_id = $1
  AND quote.principal_uuid = $2
  AND bond.series = $3
  AND quote.submitted_price = $4::numeric
  AND quote.submitted_currency_code = 'USD'
  AND quote.expires_at > statement_timestamp()
  AND NOT EXISTS (
    SELECT 1 FROM bond_exchange.sale_offer_submissions AS used
    WHERE used.conversion_quote_uuid = quote.uuid_id
  )`, submission.QuoteID, operation.Principal.ID, submission.BondSeries, submission.Price.String()).Scan(&canonicalText)
			if errors.Is(err, pgx.ErrNoRows) {
				if err := recordOperationRejection(ctx, tx, claimID, "conversion_quote_unavailable"); err != nil {
					return exchange.SaleOffer{}, err
				}
				if err := tx.Commit(ctx); err != nil {
					return exchange.SaleOffer{}, err
				}
				return exchange.SaleOffer{}, offerintake.ErrConversionQuoteUnavailable
			}
			if err != nil {
				return exchange.SaleOffer{}, err
			}
			canonicalPrice, err = exchange.ParsePrice(canonicalText)
			if err != nil {
				return exchange.SaleOffer{}, err
			}
		} else if submission.Currency != exchange.MXN || submission.QuoteID != "" {
			return exchange.SaleOffer{}, offerintake.ErrUnsupportedSubmissionCurrency
		}

		var created exchange.SaleOffer
		var priceText string
		err = tx.QueryRow(ctx, `
WITH inserted_offer AS (
  INSERT INTO bond_exchange.sale_offers
    (seller_uuid, bond_uuid, price, currency_code)
  SELECT $1, bond.uuid_id, $3::numeric, 'MXN'
  FROM bond_exchange.bonds AS bond
  WHERE bond.series = $2
  RETURNING uuid_id, seller_uuid, bond_uuid
), inserted_terms AS (
  INSERT INTO bond_exchange.sale_offer_canonical_terms
    (sale_offer_uuid, price, currency_code)
  SELECT uuid_id, $3::numeric, 'MXN' FROM inserted_offer
  RETURNING sale_offer_uuid, price, currency_code
), inserted_submission AS (
  INSERT INTO bond_exchange.sale_offer_submissions
    (sale_offer_uuid, submitted_price, submitted_currency_code, conversion_quote_uuid)
  SELECT uuid_id, $4::numeric, $5, $6 FROM inserted_offer
  RETURNING sale_offer_uuid
)
SELECT offer.uuid_id, offer.seller_uuid, bond.series,
       terms.price::text, terms.currency_code
FROM inserted_offer AS offer
JOIN inserted_terms AS terms ON terms.sale_offer_uuid = offer.uuid_id
JOIN inserted_submission AS source ON source.sale_offer_uuid = offer.uuid_id
JOIN bond_exchange.bonds AS bond ON bond.uuid_id = offer.bond_uuid`,
			operation.Principal.ID,
			submission.BondSeries,
			canonicalPrice.String(),
			submission.Price.String(),
			submission.Currency,
			quoteID,
		).Scan(&created.ID, &created.SellerID, &created.BondSeries, &priceText, &created.Currency)
		if errors.Is(err, pgx.ErrNoRows) {
			if err := recordOperationRejection(ctx, tx, claimID, "bond_not_found"); err != nil {
				return exchange.SaleOffer{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return exchange.SaleOffer{}, err
			}
			return exchange.SaleOffer{}, exchange.ErrBondNotFound
		}
		if err != nil {
			return exchange.SaleOffer{}, classifyCreateSaleOfferError(err)
		}
		created.Price, err = exchange.ParsePrice(priceText)
		if err != nil {
			return exchange.SaleOffer{}, err
		}
		if err := recordOperationSuccess(ctx, tx, claimID, string(created.ID)); err != nil {
			return exchange.SaleOffer{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return exchange.SaleOffer{}, err
		}
		return created, nil
	})
}

type conversionQuoteScanner interface {
	Scan(...any) error
}

func scanConversionQuote(row conversionQuoteScanner) (offerintake.Quote, error) {
	var quote offerintake.Quote
	var submittedPrice, mxnPrice, rate string
	if err := row.Scan(
		&quote.ID,
		&quote.SellerID,
		&quote.BondSeries,
		&submittedPrice,
		&mxnPrice,
		&rate,
		&quote.RateRevisionID,
		&quote.RateObservedOn,
		&quote.ExpiresAt,
	); err != nil {
		return offerintake.Quote{}, err
	}
	var err error
	quote.SubmittedPrice, err = exchange.ParsePrice(submittedPrice)
	if err != nil {
		return offerintake.Quote{}, err
	}
	quote.MXNPrice, err = exchange.ParsePrice(mxnPrice)
	if err != nil {
		return offerintake.Quote{}, err
	}
	quote.Rate, err = exchange.ParsePrice(rate)
	if err != nil {
		return offerintake.Quote{}, err
	}
	return quote, nil
}
