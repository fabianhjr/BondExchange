package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/eventing"
	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/offerintake"
	"github.com/fabianhjr/BondExchange/application/internal/ratelimit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type recordingPublisher struct {
	events []eventing.Envelope
	err    error
}

func (publisher *recordingPublisher) Publish(_ context.Context, event eventing.Envelope) error {
	publisher.events = append(publisher.events, event)
	return publisher.err
}

type rowScannerFunc func(...any) error

func (scan rowScannerFunc) Scan(destinations ...any) error {
	return scan(destinations...)
}

const (
	testDatabaseEnvironment          = "BOND_EXCHANGE_TEST_DATABASE_URL"
	continuousIntegrationEnvironment = "CI"
)

func TestConcurrentBuyRecordsOneBuyer(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	seller := insertUser(t, pool, "seller")
	bond := insertBond(t, pool)
	offer := insertOffer(t, pool, seller, bond)

	const competitors = 16
	buyers := make([]exchange.UserID, competitors)
	for index := range buyers {
		buyers[index] = insertUser(t, pool, "buyer")
	}

	results := make(chan exchange.Purchase, competitors)
	errorsChannel := make(chan error, competitors)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for _, buyer := range buyers {
		waitGroup.Add(1)
		go func(buyer exchange.UserID) {
			defer waitGroup.Done()
			<-start
			purchase, err := store.Buy(ctx, mutation(buyer, exchange.OperationBuy, "buy-request-key-"+string(buyer), string(offer)), offer)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- purchase
		}(buyer)
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	if len(results) != 1 {
		t.Fatalf("successful buys = %d, want 1", len(results))
	}
	for err := range errorsChannel {
		if !errors.Is(err, exchange.ErrOfferUnavailable) {
			t.Fatalf("losing Buy() error = %v", err)
		}
	}
	purchase := <-results
	if purchase.Offer.ID != offer || purchase.Offer.SellerID != seller || purchase.Offer.BondSeries != bond {
		t.Fatalf("purchase offer = %#v", purchase.Offer)
	}
	if _, err := exchange.ParseOfferID(string(purchase.ID)); err != nil {
		t.Fatalf("purchase ID is not UUIDv7: %q", purchase.ID)
	}
	if purchase.BuyerID == "" || purchase.BoughtAt.IsZero() {
		t.Fatalf("purchase = %#v", purchase)
	}
	if !purchase.Offer.Price.Equal(decimal.RequireFromString("100.25")) {
		t.Fatalf("purchase price = %s, want 100.25", purchase.Offer.Price)
	}

	var purchaseCount, offerCount, activeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bond_exchange.purchases WHERE sale_offer_uuid = $1`, offer).Scan(&purchaseCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bond_exchange.sale_offers WHERE uuid_id = $1`, offer).Scan(&offerCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bond_exchange.active_offers WHERE id = $1`, offer).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if purchaseCount != 1 || offerCount != 1 || activeCount != 0 {
		t.Fatalf("purchase, offer, active counts = %d, %d, %d", purchaseCount, offerCount, activeCount)
	}
}

func TestPrincipalRateLimitIsAtomicAcrossInstances(t *testing.T) {
	pool := openTestPool(t)
	secondPool := openTestPool(t)
	stores := []*Store{NewStore(pool), NewStore(secondPool)}
	principal := insertUser(t, pool, "rate-limited")

	const attempts = ratelimit.RequestsPerMinute + 40
	start := make(chan struct{})
	errs := make(chan error, attempts)
	var waitGroup sync.WaitGroup
	for index := range attempts {
		waitGroup.Add(1)
		go func(store *Store) {
			defer waitGroup.Done()
			<-start
			errs <- store.AdmitRequest(context.Background(), principal)
		}(stores[index%len(stores)])
	}
	close(start)
	waitGroup.Wait()
	close(errs)

	var admitted, rejected int
	for err := range errs {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, ratelimit.ErrExceeded):
			rejected++
			delay, ok := ratelimit.RetryAfter(err)
			if !ok || delay < time.Second || delay > time.Minute {
				t.Errorf("retry delay = %v, %t", delay, ok)
			}
		default:
			t.Errorf("unexpected admission error: %v", err)
		}
	}
	if admitted != ratelimit.RequestsPerMinute || rejected != attempts-ratelimit.RequestsPerMinute {
		t.Fatalf("admitted, rejected = %d, %d; want %d, %d", admitted, rejected, ratelimit.RequestsPerMinute, attempts-ratelimit.RequestsPerMinute)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `
SELECT request_count
FROM bond_exchange.principal_rate_limits
WHERE principal_uuid = $1`, principal).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != ratelimit.RequestsPerMinute {
		t.Fatalf("persisted request count = %d", count)
	}

	otherPrincipal := insertUser(t, pool, "other-rate-limit")
	if err := stores[0].AdmitRequest(context.Background(), otherPrincipal); err != nil {
		t.Fatalf("independent principal admission failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
UPDATE bond_exchange.principal_rate_limits
SET window_started_at = date_trunc('minute', statement_timestamp()) - interval '1 minute',
    updated_at = statement_timestamp()
WHERE principal_uuid = $1`, principal); err != nil {
		t.Fatal(err)
	}
	if err := stores[1].AdmitRequest(context.Background(), principal); err != nil {
		t.Fatalf("new-window admission failed: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
SELECT request_count
FROM bond_exchange.principal_rate_limits
WHERE principal_uuid = $1`, principal).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("reset request count = %d, want 1", count)
	}
}

func TestBuyRejectsMissingOffer(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	buyer := insertUser(t, pool, "buyer")
	missing := exchange.OfferID(uniqueUUIDv7(t))
	if _, err := store.Buy(ctx, mutation(buyer, exchange.OperationBuy, uniqueID(t, "buy"), string(missing)), missing); !errors.Is(err, exchange.ErrOfferUnavailable) {
		t.Fatalf("missing offer error = %v", err)
	}
}

func TestCreateSaleOffer(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "seller")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("99.125"),
		Currency:   "MXN",
	}

	operation := mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "create"), string(bond)+offer.Price.String())
	created, err := store.CreateSaleOffer(context.Background(), operation, offer)
	if err != nil {
		t.Fatalf("CreateSaleOffer() error = %v", err)
	}
	if _, err := exchange.ParseOfferID(string(created.ID)); err != nil {
		t.Fatalf("created offer ID is not UUIDv7: %q", created.ID)
	}
	if created.SellerID != seller || created.BondSeries != bond ||
		!created.Price.Equal(offer.Price) || created.Currency != "MXN" {
		t.Fatalf("created offer = %#v, want %#v", created, offer)
	}
	activeOffers, err := collectOffers(store, access(seller, exchange.OperationListActiveOffers), bond)
	if err != nil || len(activeOffers) != 1 || activeOffers[0].ID != created.ID {
		t.Fatalf("ActiveOffers() after creation = %#v, %v", activeOffers, err)
	}
	activeSeries, err := store.ActiveBondSeries(context.Background(), access(seller, exchange.OperationListBondSeries))
	if err != nil || countSeries(activeSeries, bond) != 1 {
		t.Fatalf("ActiveBondSeries() after creation = %#v, %v", activeSeries, err)
	}
	replayed, err := store.CreateSaleOffer(context.Background(), operation, offer)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("replayed CreateSaleOffer() = %#v, %v; want ID %s", replayed, err, created.ID)
	}

	missingBond := offer
	missingBond.BondSeries = exchange.BondSeries("B" + strings.ToUpper(uniqueHex(t, 8)))
	if _, err := store.CreateSaleOffer(context.Background(), mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "create"), string(missingBond.BondSeries)), missingBond); !errors.Is(err, exchange.ErrBondNotFound) {
		t.Fatalf("missing bond error = %v", err)
	}
}

func TestUSDSubmissionPersistsCanonicalMXNAndImmutableProvenance(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	seller := insertUser(t, pool, "usd-seller")
	bond := insertBond(t, pool)
	observedOn := time.Now().UTC().Truncate(24 * time.Hour)

	var importID, observationID string
	if err := pool.QueryRow(ctx, `
INSERT INTO bond_exchange.sie_exchange_rate_imports
  (request_kind, series_ids, response_body, response_sha256)
VALUES ('latest', ARRAY['SF43718'], '{"bmx":{"series":[]}}'::jsonb, $1)
RETURNING uuid_id`, make([]byte, sha256.Size)).Scan(&importID); err != nil {
		t.Fatalf("insert rate import: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO bond_exchange.sie_exchange_rate_observations
  (import_uuid, series_id, base_currency, quote_currency, observed_on, value)
VALUES ($1, 'SF43718', 'USD', 'MXN', $2, 17.1234)
RETURNING uuid_id`, importID, observedOn).Scan(&observationID); err != nil {
		t.Fatalf("insert rate observation: %v", err)
	}
	unusedQuoteOperation := mutation(seller, exchange.OperationQuoteSaleOffer, uniqueID(t, "unused-quote"), "unused")
	if replayed, found, err := store.ReplayConversionQuote(ctx, unusedQuoteOperation); err != nil || found || replayed.ID != "" {
		t.Fatalf("unused ReplayConversionQuote() = %#v, %t, %v", replayed, found, err)
	}

	quoteOperation := mutation(seller, exchange.OperationQuoteSaleOffer, uniqueID(t, "quote"), "DEMO USD 97.125")
	quote, err := store.CreateConversionQuote(ctx, quoteOperation, offerintake.QuoteDraft{
		BondSeries:     bond,
		SubmittedPrice: decimal.RequireFromString("97.125"),
		MXNPrice:       decimal.RequireFromString("1663.1102"),
		RateRevisionID: observationID,
		RateObservedOn: observedOn,
		ExpiresAt:      time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateConversionQuote() error = %v", err)
	}
	if _, err := offerintake.ParseQuoteID(string(quote.ID)); err != nil {
		t.Fatalf("quote ID = %q: %v", quote.ID, err)
	}
	if !quote.Rate.Equal(decimal.RequireFromString("17.1234")) {
		t.Fatalf("quote rate = %s", quote.Rate)
	}
	quoteRetry, err := store.CreateConversionQuote(ctx, quoteOperation, offerintake.QuoteDraft{})
	if err != nil || quoteRetry.ID != quote.ID {
		t.Fatalf("idempotent CreateConversionQuote() = %#v, %v", quoteRetry, err)
	}
	replayed, found, err := store.ReplayConversionQuote(ctx, quoteOperation)
	if err != nil || !found || replayed.ID != quote.ID {
		t.Fatalf("ReplayConversionQuote() = %#v, %t, %v", replayed, found, err)
	}
	quoteConflict := quoteOperation
	quoteConflict.RequestDigest = sha256.Sum256([]byte("changed quote request"))
	if _, _, err := store.ReplayConversionQuote(ctx, quoteConflict); !errors.Is(err, exchange.ErrIdempotencyConflict) {
		t.Fatalf("quote replay conflict error = %v", err)
	}
	if _, err := store.CreateConversionQuote(ctx, quoteConflict, offerintake.QuoteDraft{}); !errors.Is(err, exchange.ErrIdempotencyConflict) {
		t.Fatalf("quote creation conflict error = %v", err)
	}

	noResourceOperation := mutation(seller, exchange.OperationQuoteSaleOffer, uniqueID(t, "quote-no-resource"), "no resource")
	noResourceClaim := insertSyntheticOperationResult(t, pool, noResourceOperation, nil)
	if noResourceClaim == "" {
		t.Fatal("synthetic no-resource claim was not created")
	}
	if _, _, err := store.ReplayConversionQuote(ctx, noResourceOperation); err == nil || !strings.Contains(err.Error(), "no resource") {
		t.Fatalf("no-resource quote replay error = %v", err)
	}

	missingQuoteResource := uniqueUUIDv7(t)
	missingQuoteOperation := mutation(seller, exchange.OperationQuoteSaleOffer, uniqueID(t, "quote-missing-resource"), "missing resource")
	insertSyntheticOperationResult(t, pool, missingQuoteOperation, &missingQuoteResource)
	if _, _, err := store.ReplayConversionQuote(ctx, missingQuoteOperation); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing-resource quote replay error = %v", err)
	}
	if _, err := store.CreateConversionQuote(ctx, missingQuoteOperation, offerintake.QuoteDraft{}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing-resource quote creation replay error = %v", err)
	}

	submission := offerintake.Submission{
		BondSeries: bond,
		Price:      decimal.RequireFromString("97.125"), Currency: offerintake.USD, QuoteID: quote.ID,
	}
	createOperation := mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "usd-create"), "accepted quote "+string(quote.ID))
	created, err := store.CreateSaleOfferFromSubmission(ctx, createOperation, submission)
	if err != nil {
		t.Fatalf("CreateSaleOfferFromSubmission() error = %v", err)
	}
	if created.Currency != exchange.MXN || !created.Price.Equal(decimal.RequireFromString("1663.1102")) {
		t.Fatalf("canonical offer = %#v", created)
	}
	createRetry, err := store.CreateSaleOfferFromSubmission(ctx, createOperation, offerintake.Submission{})
	if err != nil || createRetry.ID != created.ID {
		t.Fatalf("idempotent CreateSaleOfferFromSubmission() = %#v, %v", createRetry, err)
	}
	createConflict := createOperation
	createConflict.RequestDigest = sha256.Sum256([]byte("changed accepted quote request"))
	if _, err := store.CreateSaleOfferFromSubmission(ctx, createConflict, offerintake.Submission{}); !errors.Is(err, exchange.ErrIdempotencyConflict) {
		t.Fatalf("submission conflict error = %v", err)
	}

	var rawPrice, rawCurrency, canonicalPrice, canonicalCurrency, submittedPrice, submittedCurrency, storedQuoteID string
	var eventVersion int
	if err := pool.QueryRow(ctx, `
SELECT
  offer.price::text,
  offer.currency_code,
  canonical.price::text,
  canonical.currency_code,
  submission.submitted_price::text,
  submission.submitted_currency_code,
  submission.conversion_quote_uuid,
  event.schema_version
FROM bond_exchange.sale_offers AS offer
JOIN bond_exchange.sale_offer_canonical_terms AS canonical
  ON canonical.sale_offer_uuid = offer.uuid_id
JOIN bond_exchange.sale_offer_submissions AS submission
  ON submission.sale_offer_uuid = offer.uuid_id
JOIN bond_exchange.integration_events AS event
  ON event.table_name = 'sale_offers' AND event.source_uuid = offer.uuid_id
WHERE offer.uuid_id = $1`, created.ID).Scan(
		&rawPrice, &rawCurrency, &canonicalPrice, &canonicalCurrency,
		&submittedPrice, &submittedCurrency, &storedQuoteID, &eventVersion,
	); err != nil {
		t.Fatalf("read canonical terms and provenance: %v", err)
	}
	if rawPrice != "1663.1102" || rawCurrency != "MXN" || canonicalPrice != rawPrice || canonicalCurrency != "MXN" ||
		submittedPrice != "97.1250" || submittedCurrency != "USD" || storedQuoteID != string(quote.ID) || eventVersion != 2 {
		t.Fatalf("stored terms = raw %s %s, canonical %s %s, submission %s %s quote %s, event v%d",
			rawPrice, rawCurrency, canonicalPrice, canonicalCurrency, submittedPrice, submittedCurrency, storedQuoteID, eventVersion)
	}

	reuseOperation := mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "quote-reuse"), "reuse "+string(quote.ID))
	_, err = store.CreateSaleOfferFromSubmission(ctx, reuseOperation, submission)
	if !errors.Is(err, offerintake.ErrConversionQuoteUnavailable) {
		t.Fatalf("reused quote error = %v", err)
	}
	if _, err := store.CreateSaleOfferFromSubmission(ctx, reuseOperation, offerintake.Submission{}); !errors.Is(err, offerintake.ErrConversionQuoteUnavailable) {
		t.Fatalf("replayed quote reuse error = %v", err)
	}

	direct, err := store.CreateSaleOfferFromSubmission(
		ctx,
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "mxn-create"), "MXN 101.25"),
		offerintake.Submission{BondSeries: bond, Price: decimal.RequireFromString("101.25"), Currency: exchange.MXN},
	)
	if err != nil || direct.Currency != exchange.MXN || !direct.Price.Equal(decimal.RequireFromString("101.25")) {
		t.Fatalf("direct MXN submission = %#v, %v", direct, err)
	}

	missingBond := exchange.BondSeries("B" + strings.ToUpper(uniqueHex(t, 8)))
	_, err = store.CreateSaleOfferFromSubmission(
		ctx,
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "missing-submission-bond"), string(missingBond)),
		offerintake.Submission{BondSeries: missingBond, Price: decimal.NewFromInt(1), Currency: exchange.MXN},
	)
	if !errors.Is(err, exchange.ErrBondNotFound) {
		t.Fatalf("missing submission bond error = %v", err)
	}
	_, err = store.CreateSaleOfferFromSubmission(
		ctx,
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "unsupported-submission"), "EUR"),
		offerintake.Submission{BondSeries: bond, Price: decimal.NewFromInt(1), Currency: "EUR"},
	)
	if !errors.Is(err, offerintake.ErrUnsupportedSubmissionCurrency) {
		t.Fatalf("unsupported repository submission error = %v", err)
	}
	_, err = store.CreateConversionQuote(
		ctx,
		mutation(seller, exchange.OperationQuoteSaleOffer, uniqueID(t, "missing-quote-bond"), string(missingBond)),
		offerintake.QuoteDraft{
			BondSeries: missingBond, SubmittedPrice: decimal.NewFromInt(1), MXNPrice: decimal.NewFromInt(17),
			RateRevisionID: observationID, RateObservedOn: observedOn, ExpiresAt: time.Now().Add(time.Minute),
		},
	)
	if !errors.Is(err, exchange.ErrBondNotFound) {
		t.Fatalf("missing quote bond error = %v", err)
	}
	_, err = store.CreateConversionQuote(
		ctx,
		mutation(seller, exchange.OperationQuoteSaleOffer, uniqueID(t, "missing-rate"), "missing rate"),
		offerintake.QuoteDraft{
			BondSeries: bond, SubmittedPrice: decimal.NewFromInt(1), MXNPrice: decimal.NewFromInt(17),
			RateRevisionID: uniqueUUIDv7(t), RateObservedOn: observedOn, ExpiresAt: time.Now().Add(time.Minute),
		},
	)
	if !errors.Is(err, offerintake.ErrExchangeRateUnavailable) {
		t.Fatalf("missing quote rate error = %v", err)
	}

	_, err = store.CreateConversionQuote(
		ctx,
		mutation(seller, exchange.OperationQuoteSaleOffer, uniqueID(t, "expired-draft"), "expired draft"),
		offerintake.QuoteDraft{
			BondSeries: bond, SubmittedPrice: decimal.NewFromInt(1), MXNPrice: decimal.NewFromInt(17),
			RateRevisionID: observationID, RateObservedOn: observedOn, ExpiresAt: time.Now().Add(-time.Minute),
		},
	)
	if err == nil {
		t.Fatal("CreateConversionQuote() accepted an already expired draft")
	}
	_, err = store.CreateSaleOfferFromSubmission(
		ctx,
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "invalid-quote-id"), "invalid quote ID"),
		offerintake.Submission{BondSeries: bond, Price: decimal.NewFromInt(1), Currency: offerintake.USD, QuoteID: "invalid"},
	)
	if err == nil {
		t.Fatal("CreateSaleOfferFromSubmission() accepted an invalid quote ID")
	}
	_, err = store.CreateSaleOfferFromSubmission(
		ctx,
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "zero-mxn"), "zero MXN"),
		offerintake.Submission{BondSeries: bond, Price: decimal.Zero, Currency: exchange.MXN},
	)
	if err == nil {
		t.Fatal("CreateSaleOfferFromSubmission() accepted a zero MXN amount")
	}

	unauthorized := insertUnprivilegedUser(t, pool)
	unauthorizedQuote := mutation(unauthorized, exchange.OperationQuoteSaleOffer, uniqueID(t, "unauthorized-quote"), "unauthorized")
	if _, _, err := store.ReplayConversionQuote(ctx, unauthorizedQuote); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("unauthorized quote replay error = %v", err)
	}
	if _, err := store.CreateConversionQuote(ctx, unauthorizedQuote, offerintake.QuoteDraft{}); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("unauthorized quote creation error = %v", err)
	}
	if _, err := store.CreateSaleOfferFromSubmission(
		ctx,
		mutation(unauthorized, exchange.OperationCreateSaleOffer, uniqueID(t, "unauthorized-create"), "unauthorized"),
		offerintake.Submission{},
	); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("unauthorized submission error = %v", err)
	}
}

func TestConcurrentCreateSaleOfferGeneratesDistinctIDs(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	offer := exchange.SaleOffer{
		SellerID:   insertUser(t, pool, "seller"),
		BondSeries: insertBond(t, pool),
		Price:      decimal.RequireFromString("100.25"),
		Currency:   "MXN",
	}

	const competitors = 8
	results := make(chan exchange.SaleOffer, competitors)
	errorsChannel := make(chan error, competitors)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for index := range competitors {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			created, err := store.CreateSaleOffer(context.Background(), mutation(offer.SellerID, exchange.OperationCreateSaleOffer, fmt.Sprintf("create-attempt-%04d", index), fmt.Sprintf("request-%d", index)), offer)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- created
		}(index)
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	if len(results) != competitors {
		t.Fatalf("successful creates = %d, want %d", len(results), competitors)
	}
	for err := range errorsChannel {
		t.Fatalf("CreateSaleOffer() error = %v", err)
	}
	seen := make(map[exchange.OfferID]struct{}, competitors)
	for created := range results {
		if _, err := exchange.ParseOfferID(string(created.ID)); err != nil {
			t.Fatalf("generated ID %q: %v", created.ID, err)
		}
		seen[created.ID] = struct{}{}
	}
	if len(seen) != competitors {
		t.Fatalf("distinct generated IDs = %d, want %d", len(seen), competitors)
	}
}

func TestActiveOffersAndBondSeries(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "seller")
	bond := insertBond(t, pool)
	first := insertOffer(t, pool, seller, bond)
	second := insertOffer(t, pool, seller, bond)
	third := insertOffer(t, pool, seller, bond)
	otherBond := insertBond(t, pool)
	insertOffer(t, pool, seller, otherBond)
	inactiveBond := insertBond(t, pool)
	inactiveOffer := insertOffer(t, pool, seller, inactiveBond)
	buyer := insertUser(t, pool, "buyer")
	if _, err := store.Buy(context.Background(), mutation(buyer, exchange.OperationBuy, uniqueID(t, "buy"), string(first)), first); err != nil {
		t.Fatalf("buy first offer: %v", err)
	}
	if _, err := store.Buy(context.Background(), mutation(buyer, exchange.OperationBuy, uniqueID(t, "buy"), string(inactiveOffer)), inactiveOffer); err != nil {
		t.Fatalf("buy inactive-bond offer: %v", err)
	}

	offers, err := collectOffers(store, access(buyer, exchange.OperationListActiveOffers), bond)
	if err != nil {
		t.Fatalf("ActiveOffers() error = %v", err)
	}
	expectedOfferIDs := []exchange.OfferID{second, third}
	slices.Sort(expectedOfferIDs)
	if len(offers) != len(expectedOfferIDs) {
		t.Fatalf("ActiveOffers() = %#v, want IDs %#v", offers, expectedOfferIDs)
	}
	for index, offer := range offers {
		if offer.BondSeries != bond || offer.ID != expectedOfferIDs[index] {
			t.Fatalf("unexpected offer %#v", offer)
		}
	}

	series, err := store.ActiveBondSeries(context.Background(), access(buyer, exchange.OperationListBondSeries))
	if err != nil {
		t.Fatalf("ActiveBondSeries() error = %v", err)
	}
	if !slices.IsSorted(series) {
		t.Fatalf("active bond series are not sorted: %#v", series)
	}
	if countSeries(series, bond) != 1 || countSeries(series, otherBond) != 1 {
		t.Fatalf("active bond series %#v do not include %q and %q exactly once", series, bond, otherBond)
	}
	if countSeries(series, inactiveBond) != 0 {
		t.Fatalf("inactive bond series %q returned in %#v", inactiveBond, series)
	}
}

func TestDomainFactTablesRejectMutation(t *testing.T) {
	pool := openTestPool(t)
	user := insertUser(t, pool, "immutable")

	for _, statement := range []string{
		`UPDATE bond_exchange.users SET uuid_id = uuid_id WHERE uuid_id = $1`,
		`DELETE FROM bond_exchange.users WHERE uuid_id = $1`,
	} {
		_, err := pool.Exec(context.Background(), statement, user)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "55000" {
			t.Fatalf("statement %q error = %v", statement, err)
		}
	}

	offer := insertOffer(t, pool, user, insertBond(t, pool))
	var eventUUID string
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO bond_exchange.integration_events (table_name, source_uuid, schema_version, completed_at)
		VALUES ('sale_offers', $1, 1, transaction_timestamp())`, offer); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT uuid_id FROM bond_exchange.integration_events WHERE table_name = 'sale_offers' AND source_uuid = $1`, offer).Scan(&eventUUID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE bond_exchange.integration_events SET schema_version = schema_version WHERE uuid_id = $1`,
		`DELETE FROM bond_exchange.integration_events WHERE uuid_id = $1`,
	} {
		_, err := pool.Exec(context.Background(), statement, eventUUID)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "55000" {
			t.Fatalf("statement %q error = %v", statement, err)
		}
	}
}

func TestMutationIdempotencyReplaysResultAndRejectsKeyReuse(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "seller")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		SellerID: seller, BondSeries: bond,
		Price: decimal.RequireFromString("101.25"), Currency: "MXN",
	}
	operation := mutation(seller, exchange.OperationCreateSaleOffer, "stable-idempotency-key", "request-a")

	first, err := store.CreateSaleOffer(context.Background(), operation, offer)
	if err != nil {
		t.Fatalf("first CreateSaleOffer() error = %v", err)
	}
	retry := operation
	retry.AssertionDigest = sha256.Sum256([]byte("fresh federated assertion"))
	second, err := store.CreateSaleOffer(context.Background(), retry, offer)
	if err != nil || second.ID != first.ID || !second.Price.Equal(first.Price) {
		t.Fatalf("idempotent retry = %#v, %v; want %#v", second, err, first)
	}
	conflict := retry
	conflict.RequestDigest = sha256.Sum256([]byte("request-b"))
	if _, err := store.CreateSaleOffer(context.Background(), conflict, offer); !errors.Is(err, exchange.ErrIdempotencyConflict) {
		t.Fatalf("key reuse error = %v", err)
	}

	var claims, results, offers int
	if err := pool.QueryRow(context.Background(), `
SELECT
  (SELECT count(*) FROM bond_exchange.operation_claims WHERE idempotency_nonce = $1),
  (SELECT count(*) FROM bond_exchange.operation_results AS result JOIN bond_exchange.operation_claims AS claim ON claim.uuid_id = result.claim_uuid WHERE claim.idempotency_nonce = $1),
  (SELECT count(*) FROM bond_exchange.sale_offers WHERE uuid_id = $2)`, operation.IdempotencyKey, first.ID).Scan(&claims, &results, &offers); err != nil {
		t.Fatal(err)
	}
	if claims != 1 || results != 1 || offers != 1 {
		t.Fatalf("claims, results, offers = %d, %d, %d", claims, results, offers)
	}
}

func TestSuccessfulMutationsRecordMinimalIntegrationEventReferences(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "event-seller")
	buyer := insertUser(t, pool, "event-buyer")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("101.25"),
		Currency:   "MXN",
	}
	createOperation := mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "event-create"), "event offer")
	created, err := store.CreateSaleOffer(context.Background(), createOperation, offer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSaleOffer(context.Background(), createOperation, offer); err != nil {
		t.Fatalf("idempotent create replay: %v", err)
	}
	purchase, err := store.Buy(
		context.Background(),
		mutation(buyer, exchange.OperationBuy, uniqueID(t, "event-buy"), string(created.ID)),
		created.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	var eventCount, payloadColumns int
	if err := pool.QueryRow(context.Background(), `
SELECT count(*)
FROM bond_exchange.integration_events
WHERE (table_name = 'sale_offers' AND source_uuid = $1)
   OR (table_name = 'purchases' AND source_uuid = $2)`, created.ID, purchase.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema = 'bond_exchange'
  AND table_name = 'integration_events'
  AND column_name IN ('payload', 'data', 'event_id')`).Scan(&payloadColumns); err != nil {
		t.Fatal(err)
	}
	if eventCount != 2 || payloadColumns != 0 {
		t.Fatalf("event references = %d, copied payload columns = %d", eventCount, payloadColumns)
	}

	createdEvent, err := store.LoadEvent(context.Background(), eventing.SourceRef{
		TableName: eventing.TableSaleOffers,
		ID:        string(created.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	createdPayload, ok := createdEvent.Data.(eventing.SaleOfferCreated)
	if !ok || createdEvent.Type != eventing.TypeSaleOfferCreated || createdPayload.ID != string(created.ID) ||
		createdPayload.Price != "101.25" {
		t.Fatalf("created event = %#v", createdEvent)
	}
	if _, err := exchange.ParseOfferID(createdEvent.ID); err != nil {
		t.Fatalf("created event ID is not UUIDv7: %q", createdEvent.ID)
	}
	purchaseEvent, err := store.LoadEvent(context.Background(), eventing.SourceRef{
		TableName: eventing.TablePurchases,
		ID:        string(purchase.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	purchasePayload, ok := purchaseEvent.Data.(eventing.PurchaseRecorded)
	if !ok || purchaseEvent.Type != eventing.TypePurchaseRecorded || purchasePayload.SaleOfferID != string(created.ID) ||
		purchasePayload.BoughtAt.IsZero() {
		t.Fatalf("purchase event = %#v", purchaseEvent)
	}
	if _, err := exchange.ParseOfferID(purchaseEvent.ID); err != nil || purchaseEvent.ID == createdEvent.ID {
		t.Fatalf("purchase event ID = %q, want a distinct UUIDv7", purchaseEvent.ID)
	}
}

func TestIntegrationEventDeliveryIsLeasedAndDeduplicated(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "publisher")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("99.5"),
		Currency:   "MXN",
	}
	created, err := store.CreateSaleOffer(
		context.Background(),
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "publish-create"), "published offer"),
		offer,
	)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	dispatcher, err := eventing.NewDispatcher(store, []eventing.Destination{{ID: "test", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ref := eventing.SourceRef{TableName: eventing.TableSaleOffers, ID: string(created.ID)}
	dispatcher.Publish(context.Background(), ref)
	dispatcher.Publish(context.Background(), ref)
	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d", len(publisher.events))
	}
	var delivered bool
	if err := pool.QueryRow(context.Background(), `
SELECT delivered_at IS NOT NULL
FROM bond_exchange.integration_event_deliveries
WHERE destination_id = 'test'
  AND event_uuid = (
    SELECT uuid_id FROM bond_exchange.integration_events
    WHERE table_name = $1 AND source_uuid = $2
  )`, ref.TableName, ref.ID).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("event was not marked delivered")
	}
}

func TestIntegrationEventLeaseCoordinatesOverlappingAttempts(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "lease-publisher")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("97.25"),
		Currency:   "MXN",
	}
	created, err := store.CreateSaleOffer(
		context.Background(),
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "lease-create"), "leased offer"),
		offer,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ref := eventing.SourceRef{TableName: eventing.TableSaleOffers, ID: string(created.ID)}
	firstLease := testNonce("first-lease")
	secondLease := testNonce("second-lease")
	wrongLease := testNonce("wrong-lease")
	earlyLease := testNonce("early-lease")
	recoveryLease := testNonce("recovery-lease")
	attempt, claimed, err := store.ClaimEvent(ctx, "lease-test", ref, firstLease, time.Minute, false)
	if err != nil || !claimed || attempt != 1 {
		t.Fatalf("first claim = %d, %t, %v", attempt, claimed, err)
	}
	if _, claimed, err := store.ClaimEvent(ctx, "lease-test", ref, secondLease, time.Minute, true); err != nil || claimed {
		t.Fatalf("overlapping claim = %t, %v", claimed, err)
	}
	if err := store.MarkEventFailed(ctx, "lease-test", ref, wrongLease, "publisher_error", time.Hour); err == nil {
		t.Fatal("failure with wrong lease token succeeded")
	}
	if err := store.MarkEventFailed(ctx, "lease-test", ref, firstLease, "publisher_error", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimEvent(ctx, "lease-test", ref, earlyLease, time.Minute, false); err != nil || claimed {
		t.Fatalf("claim before retry time = %t, %v", claimed, err)
	}
	_, claimed, err = store.ClaimEvent(ctx, "lease-test", ref, recoveryLease, time.Minute, true)
	if err != nil || !claimed {
		t.Fatalf("forced recovery claim = %t, %v", claimed, err)
	}
	if err := store.MarkEventDelivered(ctx, "lease-test", ref, recoveryLease); err != nil {
		t.Fatal(err)
	}
}

func TestManualIntegrationEventRecoveryRetriesFailedDelivery(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "recovery-publisher")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("98.75"),
		Currency:   "MXN",
	}
	created, err := store.CreateSaleOffer(
		context.Background(),
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "recovery-create"), "recovery offer"),
		offer,
	)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{err: errors.New("publisher unavailable")}
	dispatcher, err := eventing.NewDispatcher(store, []eventing.Destination{{ID: "test", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ref := eventing.SourceRef{TableName: eventing.TableSaleOffers, ID: string(created.ID)}
	dispatcher.Publish(context.Background(), ref)

	var attempts int
	var errorClass string
	if err := pool.QueryRow(context.Background(), `
SELECT attempt_count, last_error_class
FROM bond_exchange.integration_event_deliveries
WHERE destination_id = 'test'
  AND event_uuid = (
    SELECT uuid_id FROM bond_exchange.integration_events
    WHERE table_name = $1 AND source_uuid = $2
  )`, ref.TableName, ref.ID).Scan(&attempts, &errorClass); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || errorClass != "publisher_error" {
		t.Fatalf("failed delivery = %d attempts, %q", attempts, errorClass)
	}

	publisher.err = nil
	summary, err := dispatcher.PublishPending(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Attempted == 0 || summary.Delivered != summary.Attempted || summary.Failed != 0 || summary.Remaining != 0 {
		t.Fatalf("recovery summary = %#v", summary)
	}
}

func TestLoadEventRejectsUnsupportedVersionAndMissingReference(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "versioned-event")
	bond := insertBond(t, pool)
	offer := insertOffer(t, pool, seller, bond)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO bond_exchange.integration_events (table_name, source_uuid, schema_version, completed_at)
		VALUES ('sale_offers', $1, 3, transaction_timestamp())`, offer); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadEvent(context.Background(), eventing.SourceRef{
		TableName: eventing.TableSaleOffers,
		ID:        string(offer),
	}); !errors.Is(err, eventing.ErrUnsupportedEvent) {
		t.Fatalf("unsupported version error = %v", err)
	}
	missingSource := uniqueUUIDv7(t)
	if _, err := store.LoadEvent(context.Background(), eventing.SourceRef{
		TableName: eventing.TableSaleOffers,
		ID:        missingSource,
	}); err == nil {
		t.Fatal("missing event reference loaded")
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO bond_exchange.integration_events (table_name, source_uuid, schema_version, completed_at)
		VALUES ('sale_offers', $1, 1, transaction_timestamp())`, missingSource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadEvent(context.Background(), eventing.SourceRef{
		TableName: eventing.TableSaleOffers,
		ID:        missingSource,
	}); !errors.Is(err, eventing.ErrUnsupportedEvent) {
		t.Fatalf("missing source error = %v", err)
	}
	if err := store.MarkEventDelivered(context.Background(), "sink", eventing.SourceRef{
		TableName: eventing.TableSaleOffers,
		ID:        missingSource,
	}, testNonce("missing-lease")); err == nil {
		t.Fatal("missing delivery lease was accepted")
	}
}

func TestBuyIdempotencyReplaysOriginalBinding(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "seller")
	buyer := insertUser(t, pool, "buyer")
	bond := insertBond(t, pool)
	offer := insertOffer(t, pool, seller, bond)
	operation := mutation(buyer, exchange.OperationBuy, "stable-buy-key-0001", string(offer))

	first, err := store.Buy(context.Background(), operation, offer)
	if err != nil {
		t.Fatalf("first Buy() error = %v", err)
	}
	retry := operation
	retry.AssertionDigest = sha256.Sum256([]byte("fresh assertion"))
	second, err := store.Buy(context.Background(), retry, offer)
	if err != nil || second.BuyerID != first.BuyerID || second.Offer.ID != first.Offer.ID || !second.BoughtAt.Equal(first.BoughtAt) {
		t.Fatalf("retried Buy() = %#v, %v; want %#v", second, err, first)
	}
	conflict := retry
	conflict.RequestDigest = sha256.Sum256([]byte("different request"))
	if _, err := store.Buy(context.Background(), conflict, offer); !errors.Is(err, exchange.ErrIdempotencyConflict) {
		t.Fatalf("conflicting Buy() error = %v", err)
	}
}

func TestAppendOnlyRBACRevocationTakesEffect(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	principal := insertUser(t, pool, "principal")
	ctx := context.Background()
	if err := store.Authorize(ctx, access(principal, exchange.OperationBuy), exchange.PermissionBuy); err != nil {
		t.Fatalf("initial Authorize() error = %v", err)
	}
	var grantID string
	if err := pool.QueryRow(ctx, `
		SELECT principal_role_grant.uuid_id
		FROM bond_exchange.principal_role_grants AS principal_role_grant
		JOIN bond_exchange.roles AS role
		  ON role.uuid_id = principal_role_grant.role_uuid
		WHERE principal_role_grant.principal_uuid = $1 AND role.code = 'trader'`, principal).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO bond_exchange.principal_role_revocations (grant_uuid, revoked_by_uuid, reason)
VALUES ($1, $2, 'integration test')`, grantID, principal); err != nil {
		t.Fatal(err)
	}
	if err := store.Authorize(ctx, access(principal, exchange.OperationBuy), exchange.PermissionBuy); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("Authorize() after revocation error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bond_exchange.principal_role_grants SET reason = reason WHERE uuid_id = $1`, grantID); err == nil {
		t.Fatal("append-only role grant accepted UPDATE")
	}
	if _, err := store.Buy(ctx, mutation(principal, exchange.OperationBuy, "revoked-buy-key-001", "offer"), "offer"); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("Buy() after revocation error = %v", err)
	}
	if _, err := store.CreateSaleOffer(ctx, mutation(principal, exchange.OperationCreateSaleOffer, "revoked-create-key-1", "offer"), exchange.SaleOffer{}); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("CreateSaleOffer() after revocation error = %v", err)
	}
}

func TestResolveFederatedPrincipal(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	id := insertUser(t, pool, "federated")
	principal, err := store.ResolvePrincipal(context.Background(), "https://issuer.test", string(id))
	if err != nil || principal.ID != id || principal.ClientClass != exchange.ClientClassAutomated {
		t.Fatalf("ResolvePrincipal() = %#v, %v", principal, err)
	}
}

func TestSuspendedPrincipalCannotResolveOrAuthorize(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	id := insertUser(t, pool, "suspended")
	if _, err := pool.Exec(context.Background(), `
INSERT INTO bond_exchange.principal_suspensions (principal_uuid, suspended_by_uuid, reason)
VALUES ($1, $1, 'Integration test suspension.')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolvePrincipal(context.Background(), "https://issuer.test", string(id)); err == nil {
		t.Fatal("ResolvePrincipal() returned a suspended principal")
	}
	if err := store.Authorize(context.Background(), access(id, exchange.OperationBuy), exchange.PermissionBuy); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("Authorize() error = %v", err)
	}
}

func TestStreamStopsOnConsumerError(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "seller")
	bond := insertBond(t, pool)
	insertOffer(t, pool, seller, bond)
	want := errors.New("consumer stopped")
	err := store.StreamActiveOffers(context.Background(), access(seller, exchange.OperationListActiveOffers), bond, func(exchange.SaleOffer) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("StreamActiveOffers() error = %v", err)
	}
}

func TestOperationErrorMappingsAndCanceledRetry(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{exchange.ErrBuyerNotFound, "buyer_not_found"},
		{exchange.ErrOfferUnavailable, "offer_unavailable"},
		{exchange.ErrOfferAlreadyExists, "offer_already_exists"},
		{exchange.ErrSellerNotFound, "seller_not_found"},
		{exchange.ErrBondNotFound, "bond_not_found"},
		{offerintake.ErrConversionQuoteUnavailable, "conversion_quote_unavailable"},
	} {
		code, ok := safeOperationErrorCode(test.err)
		if !ok || code != test.code || !errors.Is(operationErrorFromCode(code), test.err) {
			t.Fatalf("mapping %v = %q, %t", test.err, code, ok)
		}
	}
	if _, ok := safeOperationErrorCode(errors.New("unknown")); ok {
		t.Fatal("unknown error received a safe code")
	}
	if operationErrorFromCode("unknown") == nil {
		t.Fatal("unknown stored code did not fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBeforeTransactionRetry(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitBeforeTransactionRetry() error = %v", err)
	}
	original := errors.New("original")
	if classifyCreateSaleOfferError(original) != original { //nolint:errorlint // This test requires exact passthrough identity.
		t.Fatal("non-database error was reclassified")
	}
	databaseError := &pgconn.PgError{ConstraintName: "other_constraint"}
	if classifyCreateSaleOfferError(databaseError) != databaseError { //nolint:errorlint // This test requires exact passthrough identity.
		t.Fatal("unrecognized database error was reclassified")
	}
	for _, test := range []struct {
		constraint string
		want       error
	}{
		{constraint: "sale_offers_pkey", want: exchange.ErrOfferAlreadyExists},
		{constraint: "sale_offers_seller_uuid_fkey", want: exchange.ErrSellerNotFound},
		{constraint: "sale_offers_bond_uuid_fkey", want: exchange.ErrBondNotFound},
	} {
		if got := classifyCreateSaleOfferError(&pgconn.PgError{ConstraintName: test.constraint}); !errors.Is(got, test.want) {
			t.Fatalf("constraint %q = %v, want %v", test.constraint, got, test.want)
		}
	}
	if equalDigest([]byte("short"), [sha256.Size]byte{}) {
		t.Fatal("short digest was accepted")
	}
	wantScanError := errors.New("scan failed")
	if _, err := scanPurchase(rowScannerFunc(func(...any) error { return wantScanError })); !errors.Is(err, wantScanError) {
		t.Fatalf("scanPurchase() scan error = %v", err)
	}
	if _, err := scanPurchase(rowScannerFunc(func(destinations ...any) error {
		*destinations[4].(*string) = "invalid" //nolint:forcetypeassert // scanPurchase controls this destination's concrete type.
		return nil
	})); !errors.Is(err, exchange.ErrInvalidPrice) {
		t.Fatalf("scanPurchase() price error = %v", err)
	}
	if _, err := scanConversionQuote(rowScannerFunc(func(...any) error { return wantScanError })); !errors.Is(err, wantScanError) {
		t.Fatalf("scanConversionQuote() scan error = %v", err)
	}
	for _, test := range []struct {
		name      string
		submitted string
		mxn       string
		rate      string
	}{
		{name: "submitted", submitted: "invalid", mxn: "1", rate: "1"},
		{name: "mxn", submitted: "1", mxn: "invalid", rate: "1"},
		{name: "rate", submitted: "1", mxn: "1", rate: "invalid"},
	} {
		t.Run("quote "+test.name, func(t *testing.T) {
			_, err := scanConversionQuote(rowScannerFunc(func(destinations ...any) error {
				*destinations[3].(*string) = test.submitted //nolint:forcetypeassert // scanConversionQuote controls destinations.
				*destinations[4].(*string) = test.mxn       //nolint:forcetypeassert // scanConversionQuote controls destinations.
				*destinations[5].(*string) = test.rate      //nolint:forcetypeassert // scanConversionQuote controls destinations.
				return nil
			}))
			if !errors.Is(err, exchange.ErrInvalidPrice) {
				t.Fatalf("scanConversionQuote() error = %v", err)
			}
		})
	}
	canceledContext, cancelRetry := context.WithCancel(context.Background())
	cancelRetry()
	if _, err := retryTransaction(canceledContext, func() (string, error) {
		return "", &pgconn.PgError{Code: "40001"}
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled retryTransaction() error = %v", err)
	}
	attempts := 0
	value, err := retryTransaction(context.Background(), func() (string, error) {
		attempts++
		if attempts == 1 {
			return "", &pgconn.PgError{Code: "40001"}
		}
		return "completed", nil
	})
	if err != nil || value != "completed" || attempts != 2 {
		t.Fatalf("retryTransaction() = %q, %v after %d attempts", value, err, attempts)
	}
	attempts = 0
	_, err = retryTransaction(context.Background(), func() (string, error) {
		attempts++
		return "", &pgconn.PgError{Code: "40001"}
	})
	if err == nil || attempts != 8 {
		t.Fatalf("exhausted retryTransaction() error = %v after %d attempts", err, attempts)
	}
}

func TestSaleOffersRejectInvalidDecimalPrices(t *testing.T) {
	pool := openTestPool(t)
	seller := insertUser(t, pool, "seller")
	bond := insertBond(t, pool)

	for _, price := range []string{
		"0",
		"-0.01",
		"10000000000",
		"NaN",
		"Infinity",
		"-Infinity",
	} {
		_, err := pool.Exec(
			context.Background(),
			`INSERT INTO bond_exchange.sale_offers
			   (seller_uuid, bond_uuid, price, currency_code)
			 SELECT $1, uuid_id, $3::numeric, 'USD'
			 FROM bond_exchange.bonds WHERE series = $2`,
			seller,
			bond,
			price,
		)
		if err == nil {
			t.Fatalf("price %q was accepted", price)
		}
	}
}

func TestMonetaryAmountDomainHasFixedPrecisionAndScale(t *testing.T) {
	pool := openTestPool(t)

	var dataType, columnDomain string
	var precision, scale int
	if err := pool.QueryRow(context.Background(), `
SELECT data_type, numeric_precision, numeric_scale
FROM information_schema.domains
WHERE domain_schema = 'bond_exchange' AND domain_name = 'monetary_amount'`).Scan(
		&dataType,
		&precision,
		&scale,
	); err != nil {
		t.Fatalf("query monetary_amount domain: %v", err)
	}
	if dataType != "numeric" || precision != 14 || scale != 4 {
		t.Fatalf("monetary_amount = %s(%d,%d), want numeric(14,4)", dataType, precision, scale)
	}

	if err := pool.QueryRow(context.Background(), `
SELECT domain_name
FROM information_schema.columns
WHERE table_schema = 'bond_exchange'
  AND table_name = 'sale_offers'
  AND column_name = 'price'`).Scan(&columnDomain); err != nil {
		t.Fatalf("query sale_offers.price domain: %v", err)
	}
	if columnDomain != "monetary_amount" {
		t.Fatalf("sale_offers.price domain = %q, want monetary_amount", columnDomain)
	}
}

func TestPostgreSQL18AndUUIDv7PrimaryKeys(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	var serverVersion int
	if err := pool.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil {
		t.Fatal(err)
	}
	if serverVersion < 180000 || serverVersion >= 190000 {
		t.Fatalf("server_version_num = %d, want PostgreSQL 18", serverVersion)
	}

	expected := map[string]bool{
		"bonds": false, "integration_event_deliveries": false, "integration_events": false,
		"legacy_identifier_archive": false,
		"operation_claims":          false, "operation_results": false, "permissions": false,
		"principal_rate_limits": false, "principal_reinstatements": false, "principal_role_grants": false,
		"principal_role_revocations": false, "principal_suspensions": false,
		"principals": false, "purchases": false, "role_permission_grants": false,
		"role_permission_revocations": false, "roles": false, "sale_offers": false,
		"sale_offer_canonical_terms": false, "sale_offer_conversion_quotes": false,
		"sale_offer_submissions":               false,
		"sie_exchange_rate_fetch_coordination": false, "sie_exchange_rate_imports": false,
		"sie_exchange_rate_observations": false, "sie_provider_state": false, "users": false,
	}
	rows, err := pool.Query(ctx, `
		SELECT table_name, column_name, data_type
		FROM information_schema.table_constraints AS constraint_info
		JOIN information_schema.key_column_usage AS key_info
		  USING (constraint_catalog, constraint_schema, constraint_name, table_catalog, table_schema, table_name)
		JOIN information_schema.columns AS column_info
		  USING (table_catalog, table_schema, table_name, column_name)
		WHERE constraint_info.constraint_schema = 'bond_exchange'
		  AND constraint_info.constraint_type = 'PRIMARY KEY'
		ORDER BY table_name, key_info.ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, &dataType); err != nil {
			t.Fatal(err)
		}
		if _, ok := expected[table]; !ok {
			t.Fatalf("unexpected primary-key table %q", table)
		}
		if expected[table] {
			t.Fatalf("table %q has a composite primary key", table)
		}
		if column != "uuid_id" || dataType != "uuid" {
			t.Fatalf("%s primary key = %s %s, want uuid_id uuid", table, column, dataType)
		}
		expected[table] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for table, found := range expected {
		if !found {
			t.Errorf("table %q has no reviewed UUID primary key", table)
		}
	}
}

func TestLegacyIdentifierGraphIsContracted(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	var forbiddenColumns int
	if err := pool.QueryRow(ctx, `
WITH forbidden(table_name, column_name) AS (VALUES
  ('users', 'id'),
  ('sale_offers', 'id'), ('sale_offers', 'seller_id'), ('sale_offers', 'bond_series'),
  ('purchases', 'sale_offer_id'), ('purchases', 'buyer_id'),
  ('principals', 'id'),
  ('principal_suspensions', 'id'), ('principal_suspensions', 'principal_id'), ('principal_suspensions', 'suspended_by'),
  ('principal_reinstatements', 'id'), ('principal_reinstatements', 'suspension_id'), ('principal_reinstatements', 'reinstated_by'),
  ('role_permission_grants', 'id'), ('role_permission_grants', 'role_id'), ('role_permission_grants', 'permission_id'), ('role_permission_grants', 'granted_by'),
  ('role_permission_revocations', 'id'), ('role_permission_revocations', 'grant_id'), ('role_permission_revocations', 'revoked_by'),
  ('principal_role_grants', 'id'), ('principal_role_grants', 'principal_id'), ('principal_role_grants', 'role_id'), ('principal_role_grants', 'granted_by'),
  ('principal_role_revocations', 'id'), ('principal_role_revocations', 'grant_id'), ('principal_role_revocations', 'revoked_by'),
  ('operation_claims', 'id'), ('operation_claims', 'principal_id'), ('operation_claims', 'idempotency_key'),
  ('operation_results', 'claim_id'), ('operation_results', 'resource_id'),
  ('integration_events', 'id'),
  ('integration_event_deliveries', 'table_name'), ('integration_event_deliveries', 'id'), ('integration_event_deliveries', 'lease_token'),
  ('sie_exchange_rate_imports', 'id'),
  ('sie_exchange_rate_observations', 'id'), ('sie_exchange_rate_observations', 'import_id'),
  ('sie_exchange_rate_fetch_coordination', 'lease_token')
)
SELECT count(*)
FROM information_schema.columns AS column_info
JOIN forbidden USING (table_name, column_name)
WHERE column_info.table_schema = 'bond_exchange'`).Scan(&forbiddenColumns); err != nil {
		t.Fatal(err)
	}
	if forbiddenColumns != 0 {
		t.Fatalf("contracted schema retains %d forbidden columns", forbiddenColumns)
	}

	var compatibilityTriggers, compatibilityFunctions int
	if err := pool.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM information_schema.triggers
   WHERE trigger_schema = 'bond_exchange' AND trigger_name LIKE '%sync%'),
  (SELECT count(*) FROM information_schema.routines
   WHERE routine_schema = 'bond_exchange' AND routine_name LIKE 'sync_%')`).Scan(
		&compatibilityTriggers,
		&compatibilityFunctions,
	); err != nil {
		t.Fatal(err)
	}
	if compatibilityTriggers != 0 || compatibilityFunctions != 0 {
		t.Fatalf("compatibility machinery remains: %d triggers, %d functions", compatibilityTriggers, compatibilityFunctions)
	}

	var canonicalViews, transitionalViews int
	if err := pool.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE table_name IN ('active_offers', 'effective_principal_permissions', 'current_sie_exchange_rates')),
  count(*) FILTER (WHERE table_name IN ('active_offers_v2', 'effective_principal_permissions_v2', 'current_sie_exchange_rates_v2'))
FROM information_schema.views
WHERE table_schema = 'bond_exchange'`).Scan(&canonicalViews, &transitionalViews); err != nil {
		t.Fatal(err)
	}
	if canonicalViews != 3 || transitionalViews != 0 {
		t.Fatalf("view contract = %d canonical, %d transitional; want 3 and 0", canonicalViews, transitionalViews)
	}

	var businessCodeColumns int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema = 'bond_exchange'
  AND (table_name, column_name) IN (('roles', 'code'), ('permissions', 'code'))`).Scan(&businessCodeColumns); err != nil {
		t.Fatal(err)
	}
	if businessCodeColumns != 2 {
		t.Fatalf("business code columns = %d, want 2", businessCodeColumns)
	}
}

func TestStoreReturnsConnectionErrors(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	pool.Close()
	ctx := context.Background()

	if err := store.Ping(ctx); err == nil {
		t.Fatal("Ping() succeeded with closed pool")
	}
	if err := store.Authorize(ctx, access("reader", exchange.OperationListActiveOffers), exchange.PermissionListActiveOffers); err == nil {
		t.Fatal("Authorize() succeeded with closed pool")
	}
	if err := store.AdmitRequest(ctx, "reader"); !errors.Is(err, ratelimit.ErrUnavailable) {
		t.Fatalf("AdmitRequest() error = %v", err)
	}
	if _, err := store.Buy(ctx, mutation("buyer", exchange.OperationBuy, "idempotency-key-1", "offer"), "offer"); err == nil {
		t.Fatal("Buy() succeeded with closed pool")
	}
	if _, err := store.CreateSaleOffer(ctx, mutation("seller", exchange.OperationCreateSaleOffer, "idempotency-key-1", "offer"), exchange.SaleOffer{}); err == nil {
		t.Fatal("CreateSaleOffer() succeeded with closed pool")
	}
	quoteOperation := mutation("seller", exchange.OperationQuoteSaleOffer, "idempotency-key-2", "quote")
	if _, _, err := store.ReplayConversionQuote(ctx, quoteOperation); err == nil {
		t.Fatal("ReplayConversionQuote() succeeded with closed pool")
	}
	if _, err := store.CreateConversionQuote(ctx, quoteOperation, offerintake.QuoteDraft{}); err == nil {
		t.Fatal("CreateConversionQuote() succeeded with closed pool")
	}
	if _, err := store.CreateSaleOfferFromSubmission(ctx, mutation("seller", exchange.OperationCreateSaleOffer, "idempotency-key-3", "submission"), offerintake.Submission{}); err == nil {
		t.Fatal("CreateSaleOfferFromSubmission() succeeded with closed pool")
	}
	if err := store.StreamActiveOffers(ctx, access("reader", exchange.OperationListActiveOffers), "BND", func(exchange.SaleOffer) error { return nil }); err == nil {
		t.Fatal("StreamActiveOffers() succeeded with closed pool")
	}
	if _, err := store.ActiveBondSeries(ctx, access("reader", exchange.OperationListBondSeries)); err == nil {
		t.Fatal("ActiveBondSeries() succeeded with closed pool")
	}
	ref := eventing.SourceRef{TableName: eventing.TableSaleOffers, ID: "offer"}
	if _, err := store.LoadEvent(ctx, ref); err == nil {
		t.Fatal("LoadEvent() succeeded with closed pool")
	}
	if _, _, err := store.ClaimEvent(ctx, "sink", ref, "lease", time.Second, false); err == nil {
		t.Fatal("ClaimEvent() succeeded with closed pool")
	}
	if err := store.MarkEventDelivered(ctx, "sink", ref, "lease"); err == nil {
		t.Fatal("MarkEventDelivered() succeeded with closed pool")
	}
	if err := store.MarkEventFailed(ctx, "sink", ref, "lease", "publisher_error", time.Second); err == nil {
		t.Fatal("MarkEventFailed() succeeded with closed pool")
	}
	if _, err := store.PendingEvents(ctx, "sink", eventing.SourceRef{}, 100); err == nil {
		t.Fatal("PendingEvents() succeeded with closed pool")
	}
	if _, err := store.CountPendingEvents(ctx, "sink"); err == nil {
		t.Fatal("CountPendingEvents() succeeded with closed pool")
	}
}

// TestStreamActiveOffersReleasesConnectionsOnEveryExit covers the resource path
// in FM-007. Active-offer listing is deliberately unbounded and holds a
// repeatable-read snapshot, open rows, and a pooled connection for as long as
// the reader takes. A slow, failing, or abandoned reader must therefore release
// all three on every exit, or a handful of them exhausts the fixed pool.
func TestStreamActiveOffersReleasesConnectionsOnEveryExit(t *testing.T) {
	const poolSize = 2
	pool := openBoundedTestPool(t, poolSize)
	store := NewStore(pool)
	ctx := context.Background()
	seller := insertUser(t, pool, "")
	bond := insertBond(t, pool)
	for range 3 {
		insertOffer(t, pool, seller, bond)
	}
	reader := access(seller, exchange.OperationListActiveOffers)

	yieldFailure := errors.New("reader stopped early")
	exits := []struct {
		name string
		run  func() error
	}{
		{
			name: "reader consumes every offer",
			run: func() error {
				return store.StreamActiveOffers(ctx, reader, bond, func(exchange.SaleOffer) error { return nil })
			},
		},
		{
			name: "reader abandons the stream after one offer",
			run: func() error {
				seen := 0
				err := store.StreamActiveOffers(ctx, reader, bond, func(exchange.SaleOffer) error {
					seen++
					return yieldFailure
				})
				if seen != 1 {
					t.Errorf("yield called %d times after refusing the first offer, want 1", seen)
				}
				if !errors.Is(err, yieldFailure) {
					t.Errorf("StreamActiveOffers() error = %v, want %v", err, yieldFailure)
				}
				return nil
			},
		},
		{
			name: "caller cancels mid-stream",
			run: func() error {
				streamContext, cancel := context.WithCancel(ctx)
				defer cancel()
				seen := 0
				err := store.StreamActiveOffers(
					streamContext,
					reader,
					bond,
					func(exchange.SaleOffer) error {
						seen++
						cancel()
						return nil
					},
				)
				if err == nil {
					t.Error("StreamActiveOffers() succeeded after the caller cancelled mid-stream")
				}
				if seen == 0 {
					t.Error("StreamActiveOffers() cancelled before yielding any offer")
				}
				return nil
			},
		},
	}

	for _, exit := range exits {
		t.Run(exit.name, func(t *testing.T) {
			// Repeat past the pool size: a connection leaked on this path would
			// block instead of returning, so the exhaustion is observable here
			// rather than in production.
			for attempt := range poolSize + 2 {
				if err := exit.run(); err != nil {
					t.Fatalf("attempt %d: %v", attempt, err)
				}
			}
			requirePoolDrains(t, pool)
		})
	}
}

// TestConcurrentSlowReadersDoNotExhaustThePool pins the behavior that AD-7 in
// the security profile accepts: readers queue for a bounded pool rather than
// failing, and a reader that finishes returns its connection promptly.
func TestConcurrentSlowReadersDoNotExhaustThePool(t *testing.T) {
	const poolSize = 2
	pool := openBoundedTestPool(t, poolSize)
	store := NewStore(pool)
	ctx := context.Background()
	seller := insertUser(t, pool, "")
	bond := insertBond(t, pool)
	for range 2 {
		insertOffer(t, pool, seller, bond)
	}
	reader := access(seller, exchange.OperationListActiveOffers)

	release := make(chan struct{})
	var readers sync.WaitGroup
	errs := make(chan error, poolSize*2)
	for range poolSize * 2 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			errs <- store.StreamActiveOffers(ctx, reader, bond, func(exchange.SaleOffer) error {
				<-release
				return nil
			})
		}()
	}

	// Every reader is either holding a connection or waiting for one. None may
	// fail while the pool is merely saturated.
	time.Sleep(200 * time.Millisecond)
	close(release)
	readers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("slow reader failed while the pool was saturated: %v", err)
		}
	}
	requirePoolDrains(t, pool)
}

// requirePoolDrains waits for every pooled connection to be released.
//
// A connection cancelled mid-query cannot be reused, because the server may
// still be sending rows, so pgx destroys it rather than returning it and the
// pool drains asynchronously. Polling distinguishes that from a genuine leak,
// which never drains. A burst of cancellations therefore churns connections
// instead of merely returning them, which is a cost worth knowing about.
func requirePoolDrains(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		acquired := pool.Stat().AcquiredConns()
		if acquired == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("pool still holds %d acquired connection(s); a stream did not release it", acquired)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStorageConstraintsMatchDomainValidation pins the equivalence that F-004
// tracks. Domain facts are append-only, so a value the Go domain rejects must
// also be impossible to append through direct SQL: an alternate or privileged
// writer would otherwise create an immutable row the application cannot
// interpret, and no correction could remove it.
func TestStorageConstraintsMatchDomainValidation(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	seller := insertUser(t, pool, "")
	bond := insertBond(t, pool)

	insertCurrency := func(candidate string) error {
		return probeInsert(ctx, t, pool, `
INSERT INTO bond_exchange.sale_offers (seller_uuid, bond_uuid, price, currency_code)
SELECT $1, uuid_id, 100.25, $3
FROM bond_exchange.bonds WHERE series = $2`, seller, bond, candidate)
	}
	insertPrice := func(candidate string) error {
		return probeInsert(ctx, t, pool, `
INSERT INTO bond_exchange.sale_offers (seller_uuid, bond_uuid, price, currency_code)
SELECT $1, uuid_id, $3::numeric, 'USD'
FROM bond_exchange.bonds WHERE series = $2`, seller, bond, candidate)
	}

	t.Run("currency code", func(t *testing.T) {
		// "USD\n" is the anchor trap: a regular-expression dialect whose "$"
		// matches before a trailing newline would accept it while Go does not.
		for _, candidate := range []string{
			"USD", "EUR", "MXN",
			"", "US", "USDD", "usd", "Usd", "U5D", "U$D",
			"US ", " US", "US\n", "USD\n", "ＵＳＤ",
		} {
			_, domainErr := exchange.ParseCurrencyCode(candidate)
			requireSameVerdict(t, "currency code", candidate, domainErr == nil, insertCurrency(candidate))
		}
	})

	t.Run("bond series", func(t *testing.T) {
		// Storage holds the canonical form, so the domain side of the
		// comparison is the canonical predicate rather than the parser, which
		// also uppercases its input at the service boundary.
		for _, candidate := range []string{
			"BND", "BOND2026", strings.Repeat("A", exchange.MaxBondSeriesLength),
			"", "AB", strings.Repeat("A", exchange.MaxBondSeriesLength+1),
			"bnd", "BND-1", "BND 1", "BND\n", "BÑD",
		} {
			requireSameVerdict(
				t,
				"bond series",
				candidate,
				exchange.IsCanonicalBondSeries(candidate),
				probeInsert(ctx, t, pool, `INSERT INTO bond_exchange.bonds (series) VALUES ($1)`, candidate),
			)
		}
	})

	t.Run("price", func(t *testing.T) {
		for _, candidate := range []string{
			"100.25", "0.0001", "9999999999.9999",
			"0", "-1", "-0.0001", "10000000000.0000", "NaN", "Infinity", "abc", "",
		} {
			_, domainErr := exchange.ParsePrice(candidate)
			requireSameVerdict(t, "price", candidate, domainErr == nil, insertPrice(candidate))
		}
	})

	// A documented, deliberate divergence rather than an oversight: the price
	// column is numeric(14,4), and PostgreSQL rounds a more precise input at
	// cast time, before any CHECK constraint observes the value. No column
	// constraint can reject it. The Go boundary validates scale first, so the
	// sanctioned writer never relies on this. Removing the divergence requires
	// changing the monetary domain's base type, which F-004 still tracks; this
	// test fails if that happens, so the register and database README must be
	// updated in the same change.
	t.Run("documented price scale divergence", func(t *testing.T) {
		const overPrecise = "1.00005"
		if _, err := exchange.ParsePrice(overPrecise); err == nil {
			t.Fatalf("ParsePrice(%q) accepted an over-precise value; update F-004 and db/README.md", overPrecise)
		}
		if err := insertPrice(overPrecise); err != nil {
			t.Fatalf(
				"storage now rejects the over-precise price %q (%v); the divergence is closed, so update F-004 and db/README.md",
				overPrecise,
				err,
			)
		}
	})
}

// probeInsert reports whether storage accepts a candidate value. The insert
// always rolls back, so probing cannot collide with unique constraints or
// leave rows behind for other tests.
func probeInsert(ctx context.Context, t *testing.T, pool *pgxpool.Pool, statement string, args ...any) error {
	t.Helper()
	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	_, execErr := transaction.Exec(ctx, statement, args...)
	return execErr
}

func requireSameVerdict(t *testing.T, class, candidate string, domainAccepts bool, storageErr error) {
	t.Helper()
	switch {
	case domainAccepts && storageErr != nil:
		t.Errorf("%s %q: the domain accepts it but storage rejects it: %v", class, candidate, storageErr)
	case !domainAccepts && storageErr == nil:
		t.Errorf(
			"%s %q: storage accepts it but the domain rejects it; an alternate writer could append an uninterpretable fact",
			class,
			candidate,
		)
	}
}

func countSeries(series []exchange.BondSeries, target exchange.BondSeries) int {
	count := 0
	for _, candidate := range series {
		if candidate == target {
			count++
		}
	}
	return count
}

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testDatabaseURL(t))
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// openBoundedTestPool bounds the connection pool so that a leaked connection
// blocks a later acquisition instead of hiding behind the default ceiling.
func openBoundedTestPool(t *testing.T, maxConnections int32) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(testDatabaseURL(t))
	if err != nil {
		t.Fatalf("pgxpool.ParseConfig() error = %v", err)
	}
	config.MaxConns = maxConnections
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	databaseURL := os.Getenv(testDatabaseEnvironment)
	if databaseURL == "" {
		// Skipping keeps a bare `go test ./...` usable while iterating, but a
		// quality gate that silently drops persistence coverage would report
		// success without verifying anything. Fail loudly wherever the
		// disposable PostgreSQL harness is expected to have run.
		if os.Getenv(continuousIntegrationEnvironment) != "" {
			t.Fatalf(
				"%s is required when %s is set; run PostgreSQL tests through a devenv task",
				testDatabaseEnvironment,
				continuousIntegrationEnvironment,
			)
		}
		t.Skipf(
			"%s is not set; run `devenv tasks run go:test` to include PostgreSQL integration tests",
			testDatabaseEnvironment,
		)
	}
	return databaseURL
}

func insertUser(t *testing.T, pool *pgxpool.Pool, _ string) exchange.UserID {
	t.Helper()
	var id exchange.UserID
	if err := pool.QueryRow(context.Background(), `
INSERT INTO bond_exchange.users DEFAULT VALUES
RETURNING uuid_id`).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO bond_exchange.principals (uuid_id, issuer, subject, client_class)
VALUES ($1, 'https://issuer.test', $2, 'automated')`, id, id); err != nil {
		t.Fatalf("insert principal: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO bond_exchange.principal_role_grants (principal_uuid, role_uuid, granted_by_uuid, reason)
SELECT $1, uuid_id, $1, 'Integration test access.'
FROM bond_exchange.roles WHERE code = 'trader'`, id); err != nil {
		t.Fatalf("insert principal role grant: %v", err)
	}
	return id
}

func insertUnprivilegedUser(t *testing.T, pool *pgxpool.Pool) exchange.UserID {
	t.Helper()
	var id exchange.UserID
	if err := pool.QueryRow(context.Background(), `INSERT INTO bond_exchange.users DEFAULT VALUES RETURNING uuid_id`).Scan(&id); err != nil {
		t.Fatalf("insert unprivileged user: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO bond_exchange.principals (uuid_id, issuer, subject, client_class)
VALUES ($1, 'https://issuer.test', $2, 'automated')`, id, id); err != nil {
		t.Fatalf("insert unprivileged principal: %v", err)
	}
	return id
}

func access(user exchange.UserID, operation string) exchange.AccessContext {
	return exchange.AccessContext{
		Principal: exchange.Principal{ID: user, ClientID: "integration-client", ClientClass: exchange.ClientClassAutomated},
		Operation: operation,
	}
}

func mutation(user exchange.UserID, operation, idempotencyKey, request string) exchange.MutationContext {
	idempotencyKey = testNonce(idempotencyKey)
	value := access(user, operation)
	value.RequestDigest = sha256.Sum256([]byte(request))
	value.AssertionDigest = sha256.Sum256([]byte("fresh assertion: " + idempotencyKey))
	value.AssertionID = idempotencyKey
	return exchange.MutationContext{AccessContext: value, IdempotencyKey: idempotencyKey}
}

func insertSyntheticOperationResult(
	t *testing.T,
	pool *pgxpool.Pool,
	operation exchange.MutationContext,
	resourceID *string,
) string {
	t.Helper()
	var claimID string
	if err := pool.QueryRow(context.Background(), `
INSERT INTO bond_exchange.operation_claims
  (principal_uuid, client_id, operation, idempotency_nonce, request_digest, assertion_digest)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING uuid_id`,
		operation.Principal.ID,
		operation.Principal.ClientID,
		operation.Operation,
		operation.IdempotencyKey,
		operation.RequestDigest[:],
		operation.AssertionDigest[:],
	).Scan(&claimID); err != nil {
		t.Fatalf("insert synthetic operation claim: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO bond_exchange.operation_results (claim_uuid, outcome, resource_uuid)
VALUES ($1, 'succeeded', $2)`, claimID, resourceID); err != nil {
		t.Fatalf("insert synthetic operation result: %v", err)
	}
	return claimID
}

func collectOffers(store *Store, access exchange.AccessContext, bond exchange.BondSeries) ([]exchange.SaleOffer, error) {
	offers := make([]exchange.SaleOffer, 0)
	err := store.StreamActiveOffers(context.Background(), access, bond, func(offer exchange.SaleOffer) error {
		offers = append(offers, offer)
		return nil
	})
	return offers, err
}

func insertBond(t *testing.T, pool *pgxpool.Pool) exchange.BondSeries {
	t.Helper()
	series := exchange.BondSeries("B" + strings.ToUpper(uniqueHex(t, 8)))
	if _, err := pool.Exec(context.Background(), `INSERT INTO bond_exchange.bonds (series) VALUES ($1)`, series); err != nil {
		t.Fatalf("insert bond: %v", err)
	}
	return series
}

func insertOffer(
	t *testing.T,
	pool *pgxpool.Pool,
	seller exchange.UserID,
	bond exchange.BondSeries,
) exchange.OfferID {
	t.Helper()
	var id exchange.OfferID
	if err := pool.QueryRow(
		context.Background(),
		`WITH inserted_offer AS (
		   INSERT INTO bond_exchange.sale_offers
		     (seller_uuid, bond_uuid, price, currency_code)
		   SELECT $1, uuid_id, 100.25, 'MXN'
		   FROM bond_exchange.bonds WHERE series = $2
		   RETURNING uuid_id
		 ), inserted_terms AS (
		   INSERT INTO bond_exchange.sale_offer_canonical_terms
		     (sale_offer_uuid, price, currency_code)
		   SELECT uuid_id, 100.25, 'MXN' FROM inserted_offer
		 ), inserted_submission AS (
		   INSERT INTO bond_exchange.sale_offer_submissions
		     (sale_offer_uuid, submitted_price, submitted_currency_code)
		   SELECT uuid_id, 100.25, 'MXN' FROM inserted_offer
		 )
		 SELECT uuid_id FROM inserted_offer`,
		seller,
		bond,
	).Scan(&id); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	return id
}

func uniqueID(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + uniqueHex(t, 12)
}

func uniqueUUIDv7(t *testing.T) string {
	t.Helper()
	value, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7() error = %v", err)
	}
	return value.String()
}

func testNonce(label string) string {
	digest := sha256.Sum256([]byte(label))
	digest[6] = (digest[6] & 0x0f) | 0x40
	digest[8] = (digest[8] & 0x3f) | 0x80
	return uuid.UUID(digest[:16]).String()
}

func uniqueHex(t *testing.T, byteCount int) string {
	t.Helper()
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	return hex.EncodeToString(value)
}
