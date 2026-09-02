package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/jackc/pgx/v5"
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

const activeOffersQuery = `
SELECT id, seller_id, bond_series, price, currency_code
FROM bond_exchange.active_offers
WHERE ($1::text = '' OR bond_series = $1)
  AND id > $2
ORDER BY id
LIMIT $3`

type Store struct {
	pool *pgxpool.Pool
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
		price        int64
		currencyCode string
		buyerIDValue string
		boughtAt     time.Time
	)
	err := store.pool.QueryRow(ctx, buyQuery, string(offerID), string(buyerID)).Scan(
		&offerIDValue,
		&sellerID,
		&bondSeries,
		&price,
		&currencyCode,
		&buyerIDValue,
		&boughtAt,
	)
	if err == nil {
		return exchange.Purchase{
			Offer: exchange.SaleOffer{
				ID:         exchange.OfferID(offerIDValue),
				SellerID:   exchange.UserID(sellerID),
				BondSeries: exchange.BondSeries(bondSeries),
				Price:      exchange.Price(price),
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
	query exchange.ActiveOfferQuery,
) ([]exchange.SaleOffer, error) {
	bondSeries := ""
	if query.BondSeries != nil {
		bondSeries = string(*query.BondSeries)
	}
	rows, err := store.pool.Query(
		ctx,
		activeOffersQuery,
		bondSeries,
		string(query.After),
		query.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	offers := make([]exchange.SaleOffer, 0)
	for rows.Next() {
		var offer exchange.SaleOffer
		if err := rows.Scan(
			&offer.ID,
			&offer.SellerID,
			&offer.BondSeries,
			&offer.Price,
			&offer.Currency,
		); err != nil {
			return nil, err
		}
		offers = append(offers, offer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return offers, nil
}
