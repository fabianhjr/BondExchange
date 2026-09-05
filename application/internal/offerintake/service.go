package offerintake

import (
	"context"
	"errors"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/exchangerates"
)

type Exchange interface {
	Buy(context.Context, exchange.AccessContext, string, string) (exchange.Purchase, error)
	StreamActiveOffers(context.Context, exchange.AccessContext, string, func(exchange.SaleOffer) error) error
	ActiveBondSeries(context.Context, exchange.AccessContext) ([]exchange.BondSeries, error)
}

type Rates interface {
	Latest(context.Context, []exchangerates.Series) ([]exchangerates.Observation, error)
}

type Repository interface {
	ReplayConversionQuote(context.Context, exchange.MutationContext) (Quote, bool, error)
	CreateConversionQuote(context.Context, exchange.MutationContext, QuoteDraft) (Quote, error)
	CreateSaleOfferFromSubmission(context.Context, exchange.MutationContext, Submission) (exchange.SaleOffer, error)
}

type Config struct {
	QuoteTTL          time.Duration
	MaxObservationAge time.Duration
	Now               func() time.Time
}

type Service struct {
	exchange   Exchange
	rates      Rates
	repository Repository
	config     Config
}

func NewService(exchangeService Exchange, rates Rates, repository Repository, config Config) (*Service, error) {
	if exchangeService == nil || rates == nil || repository == nil {
		return nil, errors.New("exchange, rate, and offer-intake dependencies are required")
	}
	if config.QuoteTTL <= 0 {
		config.QuoteTTL = 5 * time.Minute
	}
	if config.MaxObservationAge <= 0 {
		config.MaxObservationAge = 7 * 24 * time.Hour
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{exchange: exchangeService, rates: rates, repository: repository, config: config}, nil
}

func (service *Service) QuoteSaleOffer(
	ctx context.Context,
	access exchange.AccessContext,
	idempotencyKey string,
	bond string,
	price string,
	currency string,
) (Quote, error) {
	if access.Operation != exchange.OperationQuoteSaleOffer || access.Principal.ID == "" {
		return Quote{}, exchange.ErrInvalidOperation
	}
	if !exchange.IsValidIdempotencyKey(idempotencyKey) {
		return Quote{}, exchange.ErrInvalidIdempotencyKey
	}
	bondSeries, err := exchange.ParseBondSeries(bond)
	if err != nil {
		return Quote{}, err
	}
	submittedPrice, err := exchange.ParsePrice(price)
	if err != nil {
		return Quote{}, err
	}
	currencyCode, err := exchange.ParseCurrencyCode(currency)
	if err != nil || currencyCode != USD {
		return Quote{}, ErrUnsupportedSubmissionCurrency
	}
	operation := exchange.MutationContext{AccessContext: access, IdempotencyKey: idempotencyKey}
	if replayed, found, err := service.repository.ReplayConversionQuote(ctx, operation); err != nil || found {
		return replayed, err
	}
	observations, err := service.rates.Latest(ctx, []exchangerates.Series{{
		ID: FIXSeriesID, Base: string(USD), Quote: string(exchange.MXN),
	}})
	if err != nil || len(observations) != 1 {
		return Quote{}, ErrExchangeRateUnavailable
	}
	observation := observations[0]
	now := service.config.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if observation.SeriesID != FIXSeriesID || observation.Base != string(USD) || observation.Quote != string(exchange.MXN) ||
		!observation.Value.IsPositive() || observation.Stale || observation.RevisionID == "" || observation.Date.IsZero() ||
		observation.Date.After(today) || today.Sub(observation.Date) > service.config.MaxObservationAge {
		return Quote{}, ErrExchangeRateUnavailable
	}
	mxnPrice, err := exchange.ParsePrice(submittedPrice.Mul(observation.Value).RoundBank(exchange.MonetaryAmountScale).String())
	if err != nil {
		return Quote{}, err
	}
	return service.repository.CreateConversionQuote(ctx, operation, QuoteDraft{
		BondSeries:     bondSeries,
		SubmittedPrice: submittedPrice,
		MXNPrice:       mxnPrice,
		RateRevisionID: observation.RevisionID,
		RateObservedOn: observation.Date,
		ExpiresAt:      now.Add(service.config.QuoteTTL),
	})
}

func (service *Service) CreateSaleOffer(
	ctx context.Context,
	access exchange.AccessContext,
	idempotencyKey string,
	bond string,
	price string,
	currency string,
	quoteID string,
) (exchange.SaleOffer, error) {
	if access.Operation != exchange.OperationCreateSaleOffer || access.Principal.ID == "" {
		return exchange.SaleOffer{}, exchange.ErrInvalidOperation
	}
	if !exchange.IsValidIdempotencyKey(idempotencyKey) {
		return exchange.SaleOffer{}, exchange.ErrInvalidIdempotencyKey
	}
	bondSeries, err := exchange.ParseBondSeries(bond)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	submittedPrice, err := exchange.ParsePrice(price)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	currencyCode, err := exchange.ParseCurrencyCode(currency)
	if err != nil {
		return exchange.SaleOffer{}, err
	}
	var parsedQuoteID QuoteID
	switch currencyCode {
	case exchange.MXN:
		if quoteID != "" {
			return exchange.SaleOffer{}, ErrConversionQuoteUnavailable
		}
	case USD:
		if quoteID == "" {
			return exchange.SaleOffer{}, ErrConversionQuoteRequired
		}
		parsedQuoteID, err = ParseQuoteID(quoteID)
		if err != nil {
			return exchange.SaleOffer{}, err
		}
	default:
		return exchange.SaleOffer{}, ErrUnsupportedSubmissionCurrency
	}
	return service.repository.CreateSaleOfferFromSubmission(ctx, exchange.MutationContext{
		AccessContext: access, IdempotencyKey: idempotencyKey,
	}, Submission{
		BondSeries: bondSeries, Price: submittedPrice,
		Currency: currencyCode, QuoteID: parsedQuoteID,
	})
}

func (service *Service) Buy(ctx context.Context, access exchange.AccessContext, key, offer string) (exchange.Purchase, error) {
	return service.exchange.Buy(ctx, access, key, offer)
}

func (service *Service) StreamActiveOffers(ctx context.Context, access exchange.AccessContext, bond string, yield func(exchange.SaleOffer) error) error {
	return service.exchange.StreamActiveOffers(ctx, access, bond, yield)
}

func (service *Service) ActiveBondSeries(ctx context.Context, access exchange.AccessContext) ([]exchange.BondSeries, error) {
	return service.exchange.ActiveBondSeries(ctx, access)
}
