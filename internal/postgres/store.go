package postgres

import (
	"context"
	"errors"
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

func (store *Store) CreateSaleOffer(
	ctx context.Context,
	offer exchange.SaleOffer,
) (exchange.SaleOffer, error) {
	var (
		created   exchange.SaleOffer
		priceText string
	)
	err := store.pool.QueryRow(
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
		return exchange.SaleOffer{}, classifyCreateSaleOfferError(err)
	}
	price, err := exchange.ParsePrice(priceText)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	created.Price = price
	return created, nil
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (store *Store) Ping(ctx context.Context) error {
	return store.pool.Ping(ctx)
}

func (store *Store) Buy(
	ctx context.Context,
	buyerID exchange.UserID,
	offerID exchange.OfferID,
) (exchange.Purchase, error) {
	var (
		offerIDValue string
		sellerID     string
		bondSeries   string
		priceText    string
		currencyCode string
		buyerIDValue string
		boughtAt     time.Time
	)
	err := store.pool.QueryRow(ctx, buyQuery, string(offerID), string(buyerID)).Scan(
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
		return exchange.Purchase{
			Offer: exchange.SaleOffer{
				ID:         exchange.OfferID(offerIDValue),
				SellerID:   exchange.UserID(sellerID),
				BondSeries: exchange.BondSeries(bondSeries),
				Price:      price,
				Currency:   exchange.CurrencyCode(currencyCode),
			},
			BuyerID:  exchange.UserID(buyerIDValue),
			BoughtAt: boughtAt,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return exchange.Purchase{}, err
	}

	var buyerExists, offerExists bool
	if classifyErr := store.pool.QueryRow(
		ctx,
		classifyFailedBuyQuery,
		string(buyerID),
		string(offerID),
	).Scan(&buyerExists, &offerExists); classifyErr != nil {
		return exchange.Purchase{}, classifyErr
	}
	if !buyerExists {
		return exchange.Purchase{}, exchange.ErrBuyerNotFound
	}
	return exchange.Purchase{}, exchange.ErrOfferUnavailable
}

func (store *Store) ActiveOffers(
	ctx context.Context,
	bondSeries exchange.BondSeries,
) ([]exchange.SaleOffer, error) {
	rows, err := store.pool.Query(
		ctx,
		activeOffersQuery,
		string(bondSeries),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offers := make([]exchange.SaleOffer, 0)
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
			return nil, err
		}
		price, err := exchange.ParsePrice(priceText)
		if err != nil {
			return nil, err
		}
		offer.Price = price
		offers = append(offers, offer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return offers, nil
}

func (store *Store) ActiveBondSeries(ctx context.Context) ([]exchange.BondSeries, error) {
	rows, err := store.pool.Query(ctx, activeBondSeriesQuery)
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
	return series, nil
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
