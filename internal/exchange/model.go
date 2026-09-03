package exchange

import (
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	MinBondSeriesLength     = 3
	MaxBondSeriesLength     = 40
	MonetaryAmountPrecision = 14
	MonetaryAmountScale     = 4
)

var maxMonetaryAmount = decimal.New(99_999_999_999_999, -MonetaryAmountScale)

var (
	ErrInvalidUserID           = errors.New("user ID must not be empty")
	ErrInvalidOfferID          = errors.New("sale-offer ID must not be empty")
	ErrInvalidBondSeries       = errors.New("bond series must be 3-40 uppercase ASCII alphanumeric characters")
	ErrInvalidPrice            = errors.New("price must be a positive decimal with at most 10 integer and 4 fractional digits")
	ErrInvalidCurrencyCode     = errors.New("currency code must not be empty")
	ErrInvalidActiveOfferLimit = errors.New("active-offer limit must be between 1 and 100")
	ErrBuyerNotFound           = errors.New("buyer does not exist")
	ErrOfferUnavailable        = errors.New("sale offer does not exist or has already been bought")
)

type UserID string

func ParseUserID(value string) (UserID, error) {
	if value == "" {
		return "", ErrInvalidUserID
	}
	return UserID(value), nil
}

type OfferID string

func ParseOfferID(value string) (OfferID, error) {
	if value == "" {
		return "", ErrInvalidOfferID
	}
	return OfferID(value), nil
}

type BondSeries string

func ParseBondSeries(value string) (BondSeries, error) {
	canonical := strings.ToUpper(value)
	if !IsCanonicalBondSeries(canonical) {
		return "", ErrInvalidBondSeries
	}
	return BondSeries(canonical), nil
}

func IsCanonicalBondSeries(value string) bool {
	if len(value) < MinBondSeriesLength || len(value) > MaxBondSeriesLength {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') {
			return false
		}
	}
	return true
}

func ParsePrice(value string) (decimal.Decimal, error) {
	price, err := decimal.NewFromString(value)
	if err != nil ||
		!price.IsPositive() ||
		!price.Equal(price.Round(MonetaryAmountScale)) ||
		price.GreaterThan(maxMonetaryAmount) {
		return decimal.Zero, ErrInvalidPrice
	}
	return price, nil
}

type CurrencyCode string

func ParseCurrencyCode(value string) (CurrencyCode, error) {
	if value == "" {
		return "", ErrInvalidCurrencyCode
	}
	return CurrencyCode(value), nil
}

type SaleOffer struct {
	ID         OfferID         `json:"id"`
	SellerID   UserID          `json:"seller_id"`
	BondSeries BondSeries      `json:"bond_series"`
	Price      decimal.Decimal `json:"price"`
	Currency   CurrencyCode    `json:"currency_code"`
}

type Purchase struct {
	Offer    SaleOffer `json:"offer"`
	BuyerID  UserID    `json:"buyer_id"`
	BoughtAt time.Time `json:"bought_at"`
}

type ActiveOfferQuery struct {
	BondSeries *BondSeries
	After      OfferID
	Limit      int
}
