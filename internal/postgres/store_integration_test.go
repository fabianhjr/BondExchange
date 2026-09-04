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

	"github.com/fabianhjr/BondExchange/internal/eventing"
	"github.com/fabianhjr/BondExchange/internal/exchange"
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

func TestBuyRejectsMissingOffer(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	buyer := insertUser(t, pool, "buyer")
	missing := exchange.OfferID(uniqueID(t, "missing-offer"))
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
		ID:         exchange.OfferID(uniqueID(t, "offer")),
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("99.125"),
		Currency:   "USD",
	}

	created, err := store.CreateSaleOffer(context.Background(), mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "create"), string(offer.ID)), offer)
	if err != nil {
		t.Fatalf("CreateSaleOffer() error = %v", err)
	}
	if created.ID != offer.ID || created.SellerID != seller || created.BondSeries != bond ||
		!created.Price.Equal(offer.Price) || created.Currency != "USD" {
		t.Fatalf("created offer = %#v, want %#v", created, offer)
	}
	activeOffers, err := collectOffers(store, access(seller, exchange.OperationListActiveOffers), bond)
	if err != nil || len(activeOffers) != 1 || activeOffers[0].ID != offer.ID {
		t.Fatalf("ActiveOffers() after creation = %#v, %v", activeOffers, err)
	}
	activeSeries, err := store.ActiveBondSeries(context.Background(), access(seller, exchange.OperationListBondSeries))
	if err != nil || countSeries(activeSeries, bond) != 1 {
		t.Fatalf("ActiveBondSeries() after creation = %#v, %v", activeSeries, err)
	}
	duplicateOperation := mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "create"), string(offer.ID))
	if _, err := store.CreateSaleOffer(context.Background(), duplicateOperation, offer); !errors.Is(err, exchange.ErrOfferAlreadyExists) {
		t.Fatalf("duplicate CreateSaleOffer() error = %v", err)
	}
	if _, err := store.CreateSaleOffer(context.Background(), duplicateOperation, offer); !errors.Is(err, exchange.ErrOfferAlreadyExists) {
		t.Fatalf("replayed duplicate CreateSaleOffer() error = %v", err)
	}

	missingSeller := offer
	missingSeller.ID = exchange.OfferID(uniqueID(t, "offer"))
	missingSeller.SellerID = exchange.UserID(uniqueID(t, "missing-seller"))
	if _, err := store.CreateSaleOffer(context.Background(), mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "create"), string(missingSeller.ID)), missingSeller); !errors.Is(err, exchange.ErrSellerNotFound) {
		t.Fatalf("missing seller error = %v", err)
	}

	missingBond := offer
	missingBond.ID = exchange.OfferID(uniqueID(t, "offer"))
	missingBond.BondSeries = exchange.BondSeries("B" + strings.ToUpper(uniqueHex(t, 8)))
	if _, err := store.CreateSaleOffer(context.Background(), mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "create"), string(missingBond.ID)), missingBond); !errors.Is(err, exchange.ErrBondNotFound) {
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
	for index := range competitors {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			created, err := store.CreateSaleOffer(context.Background(), mutation(offer.SellerID, exchange.OperationCreateSaleOffer, fmt.Sprintf("create-attempt-%04d", index), string(offer.ID)), offer)
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
		`UPDATE bond_exchange.users SET id = id WHERE id = $1`,
		`DELETE FROM bond_exchange.users WHERE id = $1`,
	} {
		_, err := pool.Exec(context.Background(), statement, user)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "55000" {
			t.Fatalf("statement %q error = %v", statement, err)
		}
	}

	offer := insertOffer(t, pool, user, insertBond(t, pool))
	if _, err := pool.Exec(context.Background(), `
INSERT INTO bond_exchange.integration_events (table_name, id, schema_version, completed_at)
VALUES ('sale_offers', $1, 1, transaction_timestamp())`, offer); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE bond_exchange.integration_events SET schema_version = schema_version WHERE table_name = 'sale_offers' AND id = $1`,
		`DELETE FROM bond_exchange.integration_events WHERE table_name = 'sale_offers' AND id = $1`,
	} {
		_, err := pool.Exec(context.Background(), statement, offer)
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
		ID: exchange.OfferID(uniqueID(t, "offer")), SellerID: seller, BondSeries: bond,
		Price: decimal.RequireFromString("101.25"), Currency: "USD",
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
  (SELECT count(*) FROM bond_exchange.operation_claims WHERE idempotency_key = $1),
  (SELECT count(*) FROM bond_exchange.operation_results AS result JOIN bond_exchange.operation_claims AS claim ON claim.id = result.claim_id WHERE claim.idempotency_key = $1),
  (SELECT count(*) FROM bond_exchange.sale_offers WHERE id = $2)`, operation.IdempotencyKey, offer.ID).Scan(&claims, &results, &offers); err != nil {
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
		ID:         exchange.OfferID(uniqueID(t, "event-offer")),
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("101.25"),
		Currency:   "USD",
	}
	createOperation := mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "event-create"), string(offer.ID))
	if _, err := store.CreateSaleOffer(context.Background(), createOperation, offer); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSaleOffer(context.Background(), createOperation, offer); err != nil {
		t.Fatalf("idempotent create replay: %v", err)
	}
	if _, err := store.Buy(
		context.Background(),
		mutation(buyer, exchange.OperationBuy, uniqueID(t, "event-buy"), string(offer.ID)),
		offer.ID,
	); err != nil {
		t.Fatal(err)
	}

	var eventCount, payloadColumns int
	if err := pool.QueryRow(context.Background(), `
SELECT count(*)
FROM bond_exchange.integration_events
WHERE id = $1`, offer.ID).Scan(&eventCount); err != nil {
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
		ID:        string(offer.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	createdPayload, ok := createdEvent.Data.(eventing.SaleOfferCreated)
	if !ok || createdEvent.Type != eventing.TypeSaleOfferCreated || createdPayload.ID != string(offer.ID) ||
		createdPayload.Price != "101.25" {
		t.Fatalf("created event = %#v", createdEvent)
	}
	purchaseEvent, err := store.LoadEvent(context.Background(), eventing.SourceRef{
		TableName: eventing.TablePurchases,
		ID:        string(offer.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	purchasePayload, ok := purchaseEvent.Data.(eventing.PurchaseRecorded)
	if !ok || purchaseEvent.Type != eventing.TypePurchaseRecorded || purchasePayload.SaleOfferID != string(offer.ID) ||
		purchasePayload.BoughtAt.IsZero() {
		t.Fatalf("purchase event = %#v", purchaseEvent)
	}
}

func TestIntegrationEventDeliveryIsLeasedAndDeduplicated(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "publisher")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		ID:         exchange.OfferID(uniqueID(t, "published-offer")),
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("99.5"),
		Currency:   "USD",
	}
	if _, err := store.CreateSaleOffer(
		context.Background(),
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "publish-create"), string(offer.ID)),
		offer,
	); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	dispatcher, err := eventing.NewDispatcher(store, []eventing.Destination{{ID: "test", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ref := eventing.SourceRef{TableName: eventing.TableSaleOffers, ID: string(offer.ID)}
	dispatcher.Publish(context.Background(), ref)
	dispatcher.Publish(context.Background(), ref)
	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d", len(publisher.events))
	}
	var delivered bool
	if err := pool.QueryRow(context.Background(), `
SELECT delivered_at IS NOT NULL
FROM bond_exchange.integration_event_deliveries
WHERE destination_id = 'test' AND table_name = $1 AND id = $2`, ref.TableName, ref.ID).Scan(&delivered); err != nil {
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
		ID:         exchange.OfferID(uniqueID(t, "leased-offer")),
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("97.25"),
		Currency:   "USD",
	}
	if _, err := store.CreateSaleOffer(
		context.Background(),
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "lease-create"), string(offer.ID)),
		offer,
	); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ref := eventing.SourceRef{TableName: eventing.TableSaleOffers, ID: string(offer.ID)}
	attempt, claimed, err := store.ClaimEvent(ctx, "lease-test", ref, "first-lease", time.Minute, false)
	if err != nil || !claimed || attempt != 1 {
		t.Fatalf("first claim = %d, %t, %v", attempt, claimed, err)
	}
	if _, claimed, err := store.ClaimEvent(ctx, "lease-test", ref, "second-lease", time.Minute, true); err != nil || claimed {
		t.Fatalf("overlapping claim = %t, %v", claimed, err)
	}
	if err := store.MarkEventFailed(ctx, "lease-test", ref, "wrong-lease", "publisher_error", time.Hour); err == nil {
		t.Fatal("failure with wrong lease token succeeded")
	}
	if err := store.MarkEventFailed(ctx, "lease-test", ref, "first-lease", "publisher_error", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimEvent(ctx, "lease-test", ref, "early-lease", time.Minute, false); err != nil || claimed {
		t.Fatalf("claim before retry time = %t, %v", claimed, err)
	}
	_, claimed, err = store.ClaimEvent(ctx, "lease-test", ref, "recovery-lease", time.Minute, true)
	if err != nil || !claimed {
		t.Fatalf("forced recovery claim = %t, %v", claimed, err)
	}
	if err := store.MarkEventDelivered(ctx, "lease-test", ref, "recovery-lease"); err != nil {
		t.Fatal(err)
	}
}

func TestManualIntegrationEventRecoveryRetriesFailedDelivery(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	seller := insertUser(t, pool, "recovery-publisher")
	bond := insertBond(t, pool)
	offer := exchange.SaleOffer{
		ID:         exchange.OfferID(uniqueID(t, "recovery-offer")),
		SellerID:   seller,
		BondSeries: bond,
		Price:      decimal.RequireFromString("98.75"),
		Currency:   "USD",
	}
	if _, err := store.CreateSaleOffer(
		context.Background(),
		mutation(seller, exchange.OperationCreateSaleOffer, uniqueID(t, "recovery-create"), string(offer.ID)),
		offer,
	); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{err: errors.New("publisher unavailable")}
	dispatcher, err := eventing.NewDispatcher(store, []eventing.Destination{{ID: "test", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ref := eventing.SourceRef{TableName: eventing.TableSaleOffers, ID: string(offer.ID)}
	dispatcher.Publish(context.Background(), ref)

	var attempts int
	var errorClass string
	if err := pool.QueryRow(context.Background(), `
SELECT attempt_count, last_error_class
FROM bond_exchange.integration_event_deliveries
WHERE destination_id = 'test' AND table_name = $1 AND id = $2`, ref.TableName, ref.ID).Scan(&attempts, &errorClass); err != nil {
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
INSERT INTO bond_exchange.integration_events (table_name, id, schema_version, completed_at)
VALUES ('sale_offers', $1, 2, transaction_timestamp())`, offer); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadEvent(context.Background(), eventing.SourceRef{
		TableName: eventing.TableSaleOffers,
		ID:        string(offer),
	}); !errors.Is(err, eventing.ErrUnsupportedEvent) {
		t.Fatalf("unsupported version error = %v", err)
	}
	if _, err := store.LoadEvent(context.Background(), eventing.SourceRef{
		TableName: eventing.TableSaleOffers,
		ID:        "missing",
	}); err == nil {
		t.Fatal("missing event reference loaded")
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
SELECT id FROM bond_exchange.principal_role_grants
WHERE principal_id = $1 AND role_id = 'trader'`, principal).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO bond_exchange.principal_role_revocations (id, grant_id, revoked_by, reason)
VALUES ($1, $2, $3, 'integration test')`, uniqueID(t, "revoke"), grantID, principal); err != nil {
		t.Fatal(err)
	}
	if err := store.Authorize(ctx, access(principal, exchange.OperationBuy), exchange.PermissionBuy); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("Authorize() after revocation error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bond_exchange.principal_role_grants SET reason = reason WHERE id = $1`, grantID); err == nil {
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
INSERT INTO bond_exchange.principal_suspensions (id, principal_id, suspended_by, reason)
VALUES ($1, $2, $2, 'Integration test suspension.')`, uniqueID(t, "suspension"), id); err != nil {
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
	if _, err := store.Buy(ctx, mutation("buyer", exchange.OperationBuy, "idempotency-key-1", "offer"), "offer"); err == nil {
		t.Fatal("Buy() succeeded with closed pool")
	}
	if _, err := store.CreateSaleOffer(ctx, mutation("seller", exchange.OperationCreateSaleOffer, "idempotency-key-1", "offer"), exchange.SaleOffer{}); err == nil {
		t.Fatal("CreateSaleOffer() succeeded with closed pool")
	}
	if err := store.StreamActiveOffers(ctx, access("reader", exchange.OperationListActiveOffers), "BND", func(exchange.SaleOffer) error { return nil }); err == nil {
		t.Fatal("StreamActiveOffers() succeeded with closed pool")
	}
	if _, err := store.ActiveBondSeries(ctx, access("reader", exchange.OperationListBondSeries)); err == nil {
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
	if _, err := pool.Exec(context.Background(), `
INSERT INTO bond_exchange.principals (id, issuer, subject, client_class)
VALUES ($1, 'https://issuer.test', $1, 'automated')`, id); err != nil {
		t.Fatalf("insert principal: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO bond_exchange.principal_role_grants (id, principal_id, role_id, granted_by, reason)
VALUES ($1, $2, 'trader', $2, 'Integration test access.')`, uniqueID(t, "grant"), id); err != nil {
		t.Fatalf("insert principal role grant: %v", err)
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
	value := access(user, operation)
	value.RequestDigest = sha256.Sum256([]byte(request))
	value.AssertionDigest = sha256.Sum256([]byte("fresh assertion: " + idempotencyKey))
	value.AssertionID = idempotencyKey
	return exchange.MutationContext{AccessContext: value, IdempotencyKey: idempotencyKey}
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
