package offerintake

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/exchangerates"
	"github.com/shopspring/decimal"
)

const (
	testSellerID = exchange.UserID("01991a20-0000-7000-8000-000000000001")
	testQuoteID  = QuoteID("01991a20-0000-7000-8000-000000000099")
	testNonce    = "41db1265-8bc1-4ab3-992f-885799a4af1d"
)

type exchangeStub struct {
	purchase exchange.Purchase
	offers   []exchange.SaleOffer
	series   []exchange.BondSeries
	err      error
}

func (stub exchangeStub) Buy(context.Context, exchange.AccessContext, string, string) (exchange.Purchase, error) {
	return stub.purchase, stub.err
}

func (stub exchangeStub) StreamActiveOffers(_ context.Context, _ exchange.AccessContext, _ string, yield func(exchange.SaleOffer) error) error {
	if stub.err != nil {
		return stub.err
	}
	for _, offer := range stub.offers {
		if err := yield(offer); err != nil {
			return err
		}
	}
	return nil
}

func (stub exchangeStub) ActiveBondSeries(context.Context, exchange.AccessContext) ([]exchange.BondSeries, error) {
	return stub.series, stub.err
}

type ratesStub struct {
	observations []exchangerates.Observation
	err          error
	requested    []exchangerates.Series
}

func (stub *ratesStub) Latest(_ context.Context, requested []exchangerates.Series) ([]exchangerates.Observation, error) {
	stub.requested = requested
	return stub.observations, stub.err
}

type repositoryStub struct {
	replayed       Quote
	replayFound    bool
	replayErr      error
	draft          QuoteDraft
	createdQuote   Quote
	quoteErr       error
	submission     Submission
	createdOffer   exchange.SaleOffer
	submissionErr  error
	quoteCreations int
}

func (stub *repositoryStub) ReplayConversionQuote(context.Context, exchange.MutationContext) (Quote, bool, error) {
	return stub.replayed, stub.replayFound, stub.replayErr
}

func (stub *repositoryStub) CreateConversionQuote(_ context.Context, _ exchange.MutationContext, draft QuoteDraft) (Quote, error) {
	stub.draft = draft
	stub.quoteCreations++
	return stub.createdQuote, stub.quoteErr
}

func (stub *repositoryStub) CreateSaleOfferFromSubmission(_ context.Context, _ exchange.MutationContext, submission Submission) (exchange.SaleOffer, error) {
	stub.submission = submission
	return stub.createdOffer, stub.submissionErr
}

func quoteAccess() exchange.AccessContext {
	return exchange.AccessContext{
		Principal: exchange.Principal{ID: testSellerID, ClientID: "test-client"},
		Operation: exchange.OperationQuoteSaleOffer,
	}
}

func createAccess() exchange.AccessContext {
	access := quoteAccess()
	access.Operation = exchange.OperationCreateSaleOffer
	return access
}

func TestQuoteSaleOfferPinsFIXRevisionAndRoundsCanonicalMXN(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC)
	observedOn := time.Date(2026, time.September, 4, 0, 0, 0, 0, time.UTC)
	rates := &ratesStub{observations: []exchangerates.Observation{{
		RevisionID: "01991a20-0000-7000-8000-000000000010",
		SeriesID:   FIXSeriesID,
		Base:       "USD",
		Quote:      "MXN",
		Date:       observedOn,
		Value:      decimal.RequireFromString("17.1234"),
	}}}
	repository := &repositoryStub{createdQuote: Quote{ID: testQuoteID}}
	service, err := NewService(exchangeStub{}, rates, repository, Config{
		QuoteTTL:          3 * time.Minute,
		MaxObservationAge: 4 * 24 * time.Hour,
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	quote, err := service.QuoteSaleOffer(context.Background(), quoteAccess(), testNonce, "demo2026", "97.125", "USD")
	if err != nil || quote.ID != testQuoteID {
		t.Fatalf("QuoteSaleOffer() = %#v, %v", quote, err)
	}
	wantSeries := []exchangerates.Series{{ID: FIXSeriesID, Base: "USD", Quote: "MXN"}}
	if !reflect.DeepEqual(rates.requested, wantSeries) {
		t.Fatalf("requested series = %#v, want %#v", rates.requested, wantSeries)
	}
	if repository.draft.BondSeries != "DEMO2026" ||
		!repository.draft.SubmittedPrice.Equal(decimal.RequireFromString("97.125")) ||
		!repository.draft.MXNPrice.Equal(decimal.RequireFromString("1663.1102")) ||
		repository.draft.RateRevisionID != rates.observations[0].RevisionID ||
		!repository.draft.RateObservedOn.Equal(observedOn) ||
		!repository.draft.ExpiresAt.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("persisted draft = %#v", repository.draft)
	}
}

func TestObservationValidationOutcomesAreBounded(t *testing.T) {
	today := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	valid := exchangerates.Observation{
		RevisionID: "01991a20-0000-7000-8000-000000000010",
		SeriesID:   FIXSeriesID,
		Base:       "USD",
		Quote:      "MXN",
		Date:       today.AddDate(0, 0, -1),
		Value:      decimal.NewFromInt(19),
	}
	for _, test := range []struct {
		name   string
		mutate func(*exchangerates.Observation)
		want   string
	}{
		{name: "accepted", mutate: func(*exchangerates.Observation) {}, want: "accepted"},
		{name: "invalid", mutate: func(value *exchangerates.Observation) { value.SeriesID = "SF1" }, want: "invalid"},
		{name: "stale", mutate: func(value *exchangerates.Observation) { value.Stale = true }, want: "stale"},
		{name: "future", mutate: func(value *exchangerates.Observation) { value.Date = today.AddDate(0, 0, 1) }, want: "future"},
		{name: "over age", mutate: func(value *exchangerates.Observation) { value.Date = today.AddDate(0, 0, -8) }, want: "over_age"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := valid
			test.mutate(&observation)
			if got := observationValidationOutcome(observation, today, 7*24*time.Hour); got != test.want {
				t.Fatalf("observationValidationOutcome() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestQuoteSaleOfferFailsClosedWithoutAcceptableRate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC)
	acceptable := exchangerates.Observation{
		RevisionID: "revision",
		SeriesID:   FIXSeriesID,
		Base:       "USD",
		Quote:      "MXN",
		Date:       time.Date(2026, time.September, 4, 0, 0, 0, 0, time.UTC),
		Value:      decimal.NewFromInt(17),
	}
	for _, test := range []struct {
		name   string
		mutate func(*exchangerates.Observation)
	}{
		{name: "wrong series", mutate: func(value *exchangerates.Observation) { value.SeriesID = "SF1" }},
		{name: "wrong base", mutate: func(value *exchangerates.Observation) { value.Base = "EUR" }},
		{name: "wrong quote", mutate: func(value *exchangerates.Observation) { value.Quote = "USD" }},
		{name: "nonpositive", mutate: func(value *exchangerates.Observation) { value.Value = decimal.Zero }},
		{name: "stale", mutate: func(value *exchangerates.Observation) { value.Stale = true }},
		{name: "old", mutate: func(value *exchangerates.Observation) { value.Date = value.Date.AddDate(0, 0, -8) }},
		{name: "future", mutate: func(value *exchangerates.Observation) { value.Date = value.Date.AddDate(0, 0, 1) }},
		{name: "unpersisted", mutate: func(value *exchangerates.Observation) { value.RevisionID = "" }},
		{name: "undated", mutate: func(value *exchangerates.Observation) { value.Date = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := acceptable
			test.mutate(&observation)
			repository := &repositoryStub{}
			service, err := NewService(exchangeStub{}, &ratesStub{observations: []exchangerates.Observation{observation}}, repository, Config{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.QuoteSaleOffer(context.Background(), quoteAccess(), testNonce, "DEMO2026", "1", "USD")
			if !errors.Is(err, ErrExchangeRateUnavailable) || repository.quoteCreations != 0 {
				t.Fatalf("QuoteSaleOffer() error = %v, quote creations = %d", err, repository.quoteCreations)
			}
		})
	}
}

func TestQuoteSaleOfferRejectsCanonicalAmountOutsideCorePrecision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 18, 0, 0, 0, time.UTC)
	service, err := NewService(exchangeStub{}, &ratesStub{observations: []exchangerates.Observation{{
		RevisionID: "01991a20-0000-7000-8000-000000000010",
		SeriesID:   FIXSeriesID,
		Base:       "USD",
		Quote:      "MXN",
		Date:       time.Date(2026, time.September, 4, 0, 0, 0, 0, time.UTC),
		Value:      decimal.RequireFromString("99999999999999999999"),
	}}}, &repositoryStub{}, Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.QuoteSaleOffer(context.Background(), quoteAccess(), testNonce, "DEMO2026", "9999999999.9999", "USD")
	if !errors.Is(err, exchange.ErrInvalidPrice) {
		t.Fatalf("oversized canonical amount error = %v", err)
	}
}

func TestQuoteSaleOfferReplaysWithoutRefreshingRate(t *testing.T) {
	t.Parallel()
	want := Quote{ID: testQuoteID}
	repository := &repositoryStub{replayed: want, replayFound: true}
	rates := &ratesStub{err: errors.New("must not be called")}
	service, err := NewService(exchangeStub{}, rates, repository, Config{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.QuoteSaleOffer(context.Background(), quoteAccess(), testNonce, "DEMO2026", "1", "USD")
	if err != nil || got.ID != want.ID || rates.requested != nil {
		t.Fatalf("QuoteSaleOffer() = %#v, %v; rate request = %#v", got, err, rates.requested)
	}
}

func TestOfferIntakeRejectsInvalidQuoteInputs(t *testing.T) {
	t.Parallel()
	service, err := NewService(exchangeStub{}, &ratesStub{}, &repositoryStub{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		access   exchange.AccessContext
		key      string
		bond     string
		price    string
		currency string
		want     error
	}{
		{name: "operation", access: createAccess(), key: testNonce, bond: "BND", price: "1", currency: "USD", want: exchange.ErrInvalidOperation},
		{name: "nonce", access: quoteAccess(), key: "bad", bond: "BND", price: "1", currency: "USD", want: exchange.ErrInvalidIdempotencyKey},
		{name: "bond", access: quoteAccess(), key: testNonce, bond: "!", price: "1", currency: "USD", want: exchange.ErrInvalidBondSeries},
		{name: "price", access: quoteAccess(), key: testNonce, bond: "BND", price: "0", currency: "USD", want: exchange.ErrInvalidPrice},
		{name: "currency shape", access: quoteAccess(), key: testNonce, bond: "BND", price: "1", currency: "usd", want: ErrUnsupportedSubmissionCurrency},
		{name: "currency policy", access: quoteAccess(), key: testNonce, bond: "BND", price: "1", currency: "MXN", want: ErrUnsupportedSubmissionCurrency},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.QuoteSaleOffer(context.Background(), test.access, test.key, test.bond, test.price, test.currency)
			if !errors.Is(err, test.want) {
				t.Fatalf("QuoteSaleOffer() error = %v, want %v", err, test.want)
			}
		})
	}

	replayErr := errors.New("replay failed")
	service, _ = NewService(exchangeStub{}, &ratesStub{}, &repositoryStub{replayErr: replayErr}, Config{})
	if _, err := service.QuoteSaleOffer(context.Background(), quoteAccess(), testNonce, "BND", "1", "USD"); !errors.Is(err, replayErr) {
		t.Fatalf("replay error = %v", err)
	}
	service, _ = NewService(exchangeStub{}, &ratesStub{err: errors.New("SIE down")}, &repositoryStub{}, Config{})
	if _, err := service.QuoteSaleOffer(context.Background(), quoteAccess(), testNonce, "BND", "1", "USD"); !errors.Is(err, ErrExchangeRateUnavailable) {
		t.Fatalf("rate error = %v", err)
	}
}

func TestCreateSaleOfferSeparatesCanonicalAndSubmissionTerms(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{createdOffer: exchange.SaleOffer{Currency: exchange.MXN}}
	service, err := NewService(exchangeStub{}, &ratesStub{}, repository, Config{})
	if err != nil {
		t.Fatal(err)
	}

	created, err := service.CreateSaleOffer(context.Background(), createAccess(), testNonce, "demo2026", "97.125", "USD", string(testQuoteID))
	if err != nil || created.Currency != exchange.MXN {
		t.Fatalf("CreateSaleOffer() = %#v, %v", created, err)
	}
	if repository.submission.BondSeries != "DEMO2026" ||
		!repository.submission.Price.Equal(decimal.RequireFromString("97.125")) || repository.submission.Currency != USD ||
		repository.submission.QuoteID != testQuoteID {
		t.Fatalf("submission = %#v", repository.submission)
	}

	if _, err := service.CreateSaleOffer(context.Background(), createAccess(), testNonce, "DEMO2026", "1", "USD", ""); !errors.Is(err, ErrConversionQuoteRequired) {
		t.Fatalf("missing quote error = %v", err)
	}
	if _, err := service.CreateSaleOffer(context.Background(), createAccess(), testNonce, "DEMO2026", "1", "EUR", ""); !errors.Is(err, ErrUnsupportedSubmissionCurrency) {
		t.Fatalf("unsupported currency error = %v", err)
	}
	if _, err := service.CreateSaleOffer(context.Background(), createAccess(), testNonce, "DEMO2026", "1", "MXN", string(testQuoteID)); !errors.Is(err, ErrConversionQuoteUnavailable) {
		t.Fatalf("MXN quote error = %v", err)
	}
	if _, err := service.CreateSaleOffer(context.Background(), createAccess(), testNonce, "DEMO2026", "1", "USD", "not-a-uuid"); !errors.Is(err, ErrInvalidConversionQuote) {
		t.Fatalf("invalid quote ID error = %v", err)
	}
	if _, err := ParseQuoteID(testNonce); !errors.Is(err, ErrInvalidConversionQuote) {
		t.Fatalf("UUIDv4 quote ID error = %v", err)
	}
}

func TestOfferIntakeRejectsInvalidCreateInputsAndDelegatesCoreOperations(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	coreErr := errors.New("core failed")
	core := exchangeStub{
		purchase: exchange.Purchase{ID: "purchase"},
		offers:   []exchange.SaleOffer{{ID: "offer"}},
		series:   []exchange.BondSeries{"BND"},
	}
	service, err := NewService(core, &ratesStub{}, repository, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		access   exchange.AccessContext
		key      string
		bond     string
		price    string
		currency string
		want     error
	}{
		{name: "operation", access: quoteAccess(), key: testNonce, bond: "BND", price: "1", currency: "MXN", want: exchange.ErrInvalidOperation},
		{name: "nonce", access: createAccess(), key: "bad", bond: "BND", price: "1", currency: "MXN", want: exchange.ErrInvalidIdempotencyKey},
		{name: "bond", access: createAccess(), key: testNonce, bond: "!", price: "1", currency: "MXN", want: exchange.ErrInvalidBondSeries},
		{name: "price", access: createAccess(), key: testNonce, bond: "BND", price: "0", currency: "MXN", want: exchange.ErrInvalidPrice},
		{name: "currency", access: createAccess(), key: testNonce, bond: "BND", price: "1", currency: "mxn", want: exchange.ErrInvalidCurrencyCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.CreateSaleOffer(context.Background(), test.access, test.key, test.bond, test.price, test.currency, "")
			if !errors.Is(err, test.want) {
				t.Fatalf("CreateSaleOffer() error = %v, want %v", err, test.want)
			}
		})
	}

	purchase, err := service.Buy(context.Background(), exchange.AccessContext{}, "key", "offer")
	if err != nil || purchase.ID != "purchase" {
		t.Fatalf("Buy() = %#v, %v", purchase, err)
	}
	var offers []exchange.SaleOffer
	if err := service.StreamActiveOffers(context.Background(), exchange.AccessContext{}, "BND", func(offer exchange.SaleOffer) error {
		offers = append(offers, offer)
		return nil
	}); err != nil || len(offers) != 1 {
		t.Fatalf("StreamActiveOffers() = %#v, %v", offers, err)
	}
	series, err := service.ActiveBondSeries(context.Background(), exchange.AccessContext{})
	if err != nil || !reflect.DeepEqual(series, []exchange.BondSeries{"BND"}) {
		t.Fatalf("ActiveBondSeries() = %#v, %v", series, err)
	}

	service, _ = NewService(exchangeStub{err: coreErr}, &ratesStub{}, repository, Config{})
	if _, err := service.Buy(context.Background(), exchange.AccessContext{}, "key", "offer"); !errors.Is(err, coreErr) {
		t.Fatalf("Buy() error = %v", err)
	}
	if err := service.StreamActiveOffers(context.Background(), exchange.AccessContext{}, "BND", func(exchange.SaleOffer) error { return nil }); !errors.Is(err, coreErr) {
		t.Fatalf("StreamActiveOffers() error = %v", err)
	}
	if _, err := service.ActiveBondSeries(context.Background(), exchange.AccessContext{}); !errors.Is(err, coreErr) {
		t.Fatalf("ActiveBondSeries() error = %v", err)
	}

	if _, err := NewService(nil, &ratesStub{}, repository, Config{}); err == nil {
		t.Fatal("NewService() accepted nil core")
	}
}
