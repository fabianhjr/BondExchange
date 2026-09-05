package offerintake

import (
	"errors"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const USD exchange.CurrencyCode = "USD"

const (
	FIXSeriesID    = "SF43718"
	RoundingPolicy = "half_even_4"
)

var (
	ErrUnsupportedSubmissionCurrency = errors.New("sale offers may be submitted only in MXN or USD")
	ErrConversionQuoteRequired       = errors.New("an accepted USD-to-MXN conversion quote is required")
	ErrInvalidConversionQuote        = errors.New("conversion quote must be a canonical UUIDv7")
	ErrConversionQuoteUnavailable    = errors.New("conversion quote is missing, expired, mismatched, or already used")
	ErrExchangeRateUnavailable       = errors.New("an acceptable Banxico FIX exchange rate is unavailable")
)

type QuoteID string

func ParseQuoteID(value string) (QuoteID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != uuid.Version(7) || parsed.String() != value {
		return "", ErrInvalidConversionQuote
	}
	return QuoteID(value), nil
}

type Quote struct {
	ID             QuoteID
	SellerID       exchange.UserID
	BondSeries     exchange.BondSeries
	SubmittedPrice decimal.Decimal
	MXNPrice       decimal.Decimal
	Rate           decimal.Decimal
	RateRevisionID string
	RateObservedOn time.Time
	ExpiresAt      time.Time
}

type QuoteDraft struct {
	BondSeries     exchange.BondSeries
	SubmittedPrice decimal.Decimal
	MXNPrice       decimal.Decimal
	RateRevisionID string
	RateObservedOn time.Time
	ExpiresAt      time.Time
}

type Submission struct {
	BondSeries exchange.BondSeries
	Price      decimal.Decimal
	Currency   exchange.CurrencyCode
	QuoteID    QuoteID
}
