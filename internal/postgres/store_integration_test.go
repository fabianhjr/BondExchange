package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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

func TestActiveOffersSupportsFilteringAndPagination(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "seller")
	bond := insertBond(t, pool)
	first := insertOffer(t, pool, seller, bond)
	second := insertOffer(t, pool, seller, bond)
	if first > second {
		first, second = second, first
	}

	offers, err := store.ActiveOffers(context.Background(), exchange.ActiveOfferQuery{
		BondSeries: &bond,
		After:      first,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ActiveOffers() error = %v", err)
	}
	foundSecond := false
	for _, offer := range offers {
		if offer.ID == second {
			foundSecond = true
		}
		if offer.BondSeries != bond || offer.ID <= first {
			t.Fatalf("unexpected offer %#v", offer)
		}
	}
	if !foundSecond {
		t.Fatalf("offer %q not returned in %#v", second, offers)
	}

	allOffers, err := store.ActiveOffers(context.Background(), exchange.ActiveOfferQuery{Limit: 100})
	if err != nil || len(allOffers) == 0 {
		t.Fatalf("unfiltered ActiveOffers() = %#v, %v", allOffers, err)
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
	if _, err := store.ActiveOffers(ctx, exchange.ActiveOfferQuery{Limit: 1}); err == nil {
		t.Fatal("ActiveOffers() succeeded with closed pool")
	}
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
		 VALUES ($1, $2, $3, 100, 'USD')`,
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
