package exchange

import (
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	MinIdentifierLength     = 1
	MaxIdentifierLength     = 128
	MinBondSeriesLength     = 3
	MaxBondSeriesLength     = 40
	MonetaryAmountPrecision = 14
	MonetaryAmountScale     = 4
)

var maxMonetaryAmount = decimal.New(99_999_999_999_999, -MonetaryAmountScale)

var (
	ErrInvalidUserID       = errors.New("user ID must contain 1-128 visible ASCII characters")
	ErrInvalidOfferID      = errors.New("sale-offer ID must contain 1-128 visible ASCII characters")
	ErrInvalidBondSeries   = errors.New("bond series must be 3-40 uppercase ASCII alphanumeric characters")
	ErrInvalidPrice        = errors.New("price must be a positive decimal with at most 10 integer and 4 fractional digits")
	ErrInvalidCurrencyCode = errors.New("currency code must contain exactly three uppercase ASCII letters")
	ErrBuyerNotFound       = errors.New("buyer does not exist")
	ErrSellerNotFound      = errors.New("seller does not exist")
	ErrBondNotFound        = errors.New("bond series does not exist")
	ErrOfferAlreadyExists  = errors.New("sale-offer ID already exists")
	ErrOfferUnavailable    = errors.New("sale offer does not exist or has already been bought")
)

type UserID string

func ParseUserID(value string) (UserID, error) {
	if !isVisibleASCIIIdentifier(value) {
		return "", ErrInvalidUserID
	}
	return UserID(value), nil
}

type OfferID string

func ParseOfferID(value string) (OfferID, error) {
	if !isVisibleASCIIIdentifier(value) {
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
	if len(value) != 3 {
		return "", ErrInvalidCurrencyCode
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 'A' || value[index] > 'Z' {
			return "", ErrInvalidCurrencyCode
		}
	}
	return CurrencyCode(value), nil
}

func isVisibleASCIIIdentifier(value string) bool {
	if len(value) < MinIdentifierLength || len(value) > MaxIdentifierLength {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
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
