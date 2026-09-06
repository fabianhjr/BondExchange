package exchange

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
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
	ErrInvalidOfferID      = errors.New("sale-offer ID must be a canonical UUIDv7")
	ErrInvalidBondSeries   = errors.New("bond series must be 3-40 uppercase ASCII alphanumeric characters")
	ErrInvalidPrice        = errors.New("price must be a positive decimal with at most 10 integer and 4 fractional digits")
	ErrInvalidCurrencyCode = errors.New("currency code must contain exactly three uppercase ASCII letters")
	ErrBuyerNotFound       = errors.New("buyer is not a known principal")
	ErrSellerNotFound      = errors.New("seller is not a known principal")
	ErrBondNotFound        = errors.New("bond series does not exist")
	ErrOfferAlreadyExists  = errors.New("sale-offer ID already exists")
	ErrOfferUnavailable    = errors.New("sale offer does not exist or has already been bought")
	ErrSelfTradeProhibited = errors.New("a buyer cannot reserve their own sale offer")
)

// PrincipalID identifies one authenticated principal. It is also the identity
// that sale offers and purchases are attributed to: the service has a single
// identity table, and the seller or buyer of a fact is always the principal
// that appended it, never a value a caller supplies. See ADR-0034.
type PrincipalID string

type OfferID string

func ParseOfferID(value string) (OfferID, error) {
	if !isCanonicalUUIDVersion(value, uuid.Version(7)) {
		return "", ErrInvalidOfferID
	}
	return OfferID(value), nil
}

type PurchaseID string

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

const MXN CurrencyCode = "MXN"

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

func isCanonicalUUIDVersion(value string, version uuid.Version) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.Version() == version && parsed.String() == value
}

type SaleOffer struct {
	ID         OfferID         `json:"id"`
	SellerID   PrincipalID     `json:"seller_id"`
	BondSeries BondSeries      `json:"bond_series"`
	Price      decimal.Decimal `json:"price"`
	Currency   CurrencyCode    `json:"currency_code"`
}

type Purchase struct {
	ID       PurchaseID  `json:"id"`
	Offer    SaleOffer   `json:"offer"`
	BuyerID  PrincipalID `json:"buyer_id"`
	BoughtAt time.Time   `json:"bought_at"`
}
