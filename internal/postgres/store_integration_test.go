package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

const testDatabaseEnvironment = "BOND_EXCHANGE_TEST_DATABASE_URL"

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
			purchase, err := store.Buy(ctx, buyer, offer)
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
	if purchase.BuyerID == "" || purchase.BoughtAt.IsZero() {
		t.Fatalf("purchase = %#v", purchase)
	}
	if !purchase.Offer.Price.Equal(decimal.RequireFromString("100.25")) {
		t.Fatalf("purchase price = %s, want 100.25", purchase.Offer.Price)
	}

	var purchaseCount, offerCount, activeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bond_exchange.purchases WHERE sale_offer_id = $1`, offer).Scan(&purchaseCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bond_exchange.sale_offers WHERE id = $1`, offer).Scan(&offerCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bond_exchange.active_offers WHERE id = $1`, offer).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if purchaseCount != 1 || offerCount != 1 || activeCount != 0 {
		t.Fatalf("purchase, offer, active counts = %d, %d, %d", purchaseCount, offerCount, activeCount)
	}
}

func TestBuyRejectsMissingDomainObjects(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	if _, err := store.Buy(ctx, exchange.UserID(uniqueID(t, "missing-buyer")), exchange.OfferID(uniqueID(t, "missing-offer"))); !errors.Is(err, exchange.ErrBuyerNotFound) {
		t.Fatalf("missing buyer error = %v", err)
	}
	buyer := insertUser(t, pool, "buyer")
	if _, err := store.Buy(ctx, buyer, exchange.OfferID(uniqueID(t, "missing-offer"))); !errors.Is(err, exchange.ErrOfferUnavailable) {
		t.Fatalf("missing offer error = %v", err)
	}
}

func TestCreateSaleOffer(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "seller")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		ID:         exchange.OfferID(uniqueID(t, "offer")),
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("99.125"),
		Currency:   "USD",
	}

	created, err := store.CreateSaleOffer(context.Background(), offer)
	if err != nil {
		t.Fatalf("CreateSaleOffer() error = %v", err)
	}
	if created.ID != offer.ID || created.SellerID != seller || created.BondSeries != bond ||
		!created.Price.Equal(offer.Price) || created.Currency != "USD" {
		t.Fatalf("created offer = %#v, want %#v", created, offer)
	}
	activeOffers, err := store.ActiveOffers(context.Background(), bond)
	if err != nil || len(activeOffers) != 1 || activeOffers[0].ID != offer.ID {
		t.Fatalf("ActiveOffers() after creation = %#v, %v", activeOffers, err)
	}
	activeSeries, err := store.ActiveBondSeries(context.Background())
	if err != nil || countSeries(activeSeries, bond) != 1 {
		t.Fatalf("ActiveBondSeries() after creation = %#v, %v", activeSeries, err)
	}
	if _, err := store.CreateSaleOffer(context.Background(), offer); !errors.Is(err, exchange.ErrOfferAlreadyExists) {
		t.Fatalf("duplicate CreateSaleOffer() error = %v", err)
	}

	missingSeller := offer
	missingSeller.ID = exchange.OfferID(uniqueID(t, "offer"))
	missingSeller.SellerID = exchange.UserID(uniqueID(t, "missing-seller"))
	if _, err := store.CreateSaleOffer(context.Background(), missingSeller); !errors.Is(err, exchange.ErrSellerNotFound) {
		t.Fatalf("missing seller error = %v", err)
	}

	missingBond := offer
	missingBond.ID = exchange.OfferID(uniqueID(t, "offer"))
	missingBond.BondSeries = exchange.BondSeries("B" + strings.ToUpper(uniqueHex(t, 8)))
	if _, err := store.CreateSaleOffer(context.Background(), missingBond); !errors.Is(err, exchange.ErrBondNotFound) {
		t.Fatalf("missing bond error = %v", err)
	}
}

func TestConcurrentCreateSaleOfferRecordsOneOffer(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	offer := exchange.SaleOffer{
		ID:         exchange.OfferID(uniqueID(t, "offer")),
		SellerID:   insertUser(t, pool, "seller"),
		BondSeries: insertBond(t, pool),
		Price:      decimal.RequireFromString("100.25"),
		Currency:   "USD",
	}

	const competitors = 8
	results := make(chan exchange.SaleOffer, competitors)
	errorsChannel := make(chan error, competitors)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for range competitors {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			created, err := store.CreateSaleOffer(context.Background(), offer)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- created
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	if len(results) != 1 {
		t.Fatalf("successful creates = %d, want 1", len(results))
	}
	for err := range errorsChannel {
		if !errors.Is(err, exchange.ErrOfferAlreadyExists) {
			t.Fatalf("losing CreateSaleOffer() error = %v", err)
		}
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
	if _, err := store.Buy(context.Background(), buyer, first); err != nil {
		t.Fatalf("buy first offer: %v", err)
	}
	if _, err := store.Buy(context.Background(), buyer, inactiveOffer); err != nil {
		t.Fatalf("buy inactive-bond offer: %v", err)
	}

	offers, err := store.ActiveOffers(context.Background(), bond)
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

	series, err := store.ActiveBondSeries(context.Background())
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
		`UPDATE bond_exchange.users SET id = id WHERE id = $1`,
		`DELETE FROM bond_exchange.users WHERE id = $1`,
	} {
		_, err := pool.Exec(context.Background(), statement, user)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "55000" {
			t.Fatalf("statement %q error = %v", statement, err)
		}
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
			   (id, seller_id, bond_series, price, currency_code)
			 VALUES ($1, $2, $3, $4::numeric, 'USD')`,
			exchange.OfferID(uniqueID(t, "invalid-price")),
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

func TestStoreReturnsConnectionErrors(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	pool.Close()
	ctx := context.Background()

	if err := store.Ping(ctx); err == nil {
		t.Fatal("Ping() succeeded with closed pool")
	}
	if _, err := store.Buy(ctx, "buyer", "offer"); err == nil {
		t.Fatal("Buy() succeeded with closed pool")
	}
	if _, err := store.CreateSaleOffer(ctx, exchange.SaleOffer{}); err == nil {
		t.Fatal("CreateSaleOffer() succeeded with closed pool")
	}
	if _, err := store.ActiveOffers(ctx, "BND"); err == nil {
		t.Fatal("ActiveOffers() succeeded with closed pool")
	}
	if _, err := store.ActiveBondSeries(ctx); err == nil {
		t.Fatal("ActiveBondSeries() succeeded with closed pool")
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
	databaseURL := os.Getenv(testDatabaseEnvironment)
	if databaseURL == "" {
		t.Skipf("%s is not set", testDatabaseEnvironment)
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func insertUser(t *testing.T, pool *pgxpool.Pool, prefix string) exchange.UserID {
	t.Helper()
	id := exchange.UserID(uniqueID(t, prefix))
	if _, err := pool.Exec(context.Background(), `INSERT INTO bond_exchange.users (id) VALUES ($1)`, id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
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
	id := exchange.OfferID(uniqueID(t, "offer"))
	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO bond_exchange.sale_offers
		   (id, seller_id, bond_series, price, currency_code)
		 VALUES ($1, $2, $3, 100.25, 'USD')`,
		id,
		seller,
		bond,
	); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	return id
}

func uniqueID(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + uniqueHex(t, 12)
}

func uniqueHex(t *testing.T, byteCount int) string {
	t.Helper()
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	return hex.EncodeToString(value)
}
