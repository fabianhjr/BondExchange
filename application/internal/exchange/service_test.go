package exchange

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

type storeStub struct {
	buyBuyer     UserID
	buyOffer     OfferID
	buyValue     Purchase
	buyError     error
	createdInput SaleOffer
	createdValue SaleOffer
	createError  error
	bond         BondSeries
	offers       []SaleOffer
	queryErr     error
	series       []BondSeries
	seriesError  error
	operation    AccessContext
}

const (
	testUserID  = "019535d9-3df7-79fb-b466-fa907fa17f9e"
	testOfferID = "019535d9-3df7-79fb-b466-fa907fa17f9f"
	testNonce   = "41db1265-8bc1-4ab3-992f-885799a4af1d"
)

func (store *storeStub) Buy(_ context.Context, operation MutationContext, offer OfferID) (Purchase, error) {
	store.operation = operation.AccessContext
	store.buyBuyer = operation.Principal.ID
	store.buyOffer = offer
	return store.buyValue, store.buyError
}

func (store *storeStub) CreateSaleOffer(
	_ context.Context,
	operation MutationContext,
	offer SaleOffer,
) (SaleOffer, error) {
	store.operation = operation.AccessContext
	store.createdInput = offer
	return store.createdValue, store.createError
}

func (store *storeStub) StreamActiveOffers(
	_ context.Context,
	access AccessContext,
	bond BondSeries,
	yield func(SaleOffer) error,
) error {
	store.operation = access
	store.bond = bond
	if store.queryErr != nil {
		return store.queryErr
	}
	for _, offer := range store.offers {
		if err := yield(offer); err != nil {
			return err
		}
	}
	return nil
}

func (store *storeStub) ActiveBondSeries(_ context.Context, access AccessContext) ([]BondSeries, error) {
	store.operation = access
	return store.series, store.seriesError
}

func testAccess(operation string) AccessContext {
	return AccessContext{
		Principal: Principal{ID: testUserID, ClientID: "test-client", ClientClass: ClientClassHuman},
		Operation: operation,
	}
}

func TestServiceBuy(t *testing.T) {
	t.Parallel()

	expected := Purchase{BuyerID: testUserID, BoughtAt: time.Unix(1, 0)}
	store := &storeStub{buyValue: expected}
	actual, err := NewService(store).Buy(context.Background(), testAccess(OperationBuy), testNonce, testOfferID)
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Buy() = %#v, %v", actual, err)
	}
	if store.buyBuyer != testUserID || store.buyOffer != testOfferID {
		t.Fatalf("store received buyer %q and offer %q", store.buyBuyer, store.buyOffer)
	}

	store.buyError = ErrOfferUnavailable
	if _, err := NewService(store).Buy(context.Background(), testAccess(OperationBuy), testNonce, testOfferID); !errors.Is(err, ErrOfferUnavailable) {
		t.Fatalf("Buy() store error = %v", err)
	}
	if _, err := NewService(store).Buy(context.Background(), testAccess(OperationBuy), testNonce, ""); !errors.Is(err, ErrInvalidOfferID) {
		t.Fatalf("Buy() offer error = %v", err)
	}
}

func TestServiceCreateSaleOffer(t *testing.T) {
	t.Parallel()

	expected := SaleOffer{ID: testOfferID, SellerID: testUserID, BondSeries: "BND", Price: decimal.RequireFromString("100.25"), Currency: "MXN"}
	store := &storeStub{createdValue: expected}
	created, err := NewService(store).CreateSaleOffer(
		context.Background(),
		testAccess(OperationCreateSaleOffer),
		testNonce,
		"bnd",
		"100.25",
		"MXN",
	)
	if err != nil || !reflect.DeepEqual(created, expected) {
		t.Fatalf("CreateSaleOffer() = %#v, %v", created, err)
	}
	if store.createdInput.ID != "" ||
		store.createdInput.SellerID != testUserID ||
		store.createdInput.BondSeries != "BND" ||
		!store.createdInput.Price.Equal(decimal.RequireFromString("100.25")) ||
		store.createdInput.Currency != "MXN" {
		t.Fatalf("store received %#v", store.createdInput)
	}

	store.createError = ErrOfferAlreadyExists
	if _, err := NewService(store).CreateSaleOffer(context.Background(), testAccess(OperationCreateSaleOffer), testNonce, "BND", "1", "MXN"); !errors.Is(err, ErrOfferAlreadyExists) {
		t.Fatalf("CreateSaleOffer() store error = %v", err)
	}

	for _, test := range []struct {
		name     string
		bond     string
		price    string
		currency string
		want     error
	}{
		{name: "bond", bond: "!", price: "1", currency: "MXN", want: ErrInvalidBondSeries},
		{name: "price", bond: "BND", price: "0", currency: "MXN", want: ErrInvalidPrice},
		{name: "usd", bond: "BND", price: "1", currency: "USD", want: ErrInvalidCurrencyCode},
		{name: "currency", bond: "BND", price: "1", want: ErrInvalidCurrencyCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewService(&storeStub{}).CreateSaleOffer(
				context.Background(),
				testAccess(OperationCreateSaleOffer),
				testNonce,
				test.bond,
				test.price,
				test.currency,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("CreateSaleOffer() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServiceActiveOffers(t *testing.T) {
	t.Parallel()

	store := &storeStub{offers: []SaleOffer{{ID: testOfferID}}}
	service := NewService(store)
	var offers []SaleOffer
	err := service.StreamActiveOffers(context.Background(), testAccess(OperationListActiveOffers), "bnd", func(offer SaleOffer) error {
		offers = append(offers, offer)
		return nil
	})
	if err != nil || !reflect.DeepEqual(offers, store.offers) {
		t.Fatalf("StreamActiveOffers() = %#v, %v", offers, err)
	}
	if store.bond != "BND" {
		t.Fatalf("store received bond %q", store.bond)
	}

	for _, bond := range []string{"", "!"} {
		if err := service.StreamActiveOffers(context.Background(), testAccess(OperationListActiveOffers), bond, func(SaleOffer) error { return nil }); !errors.Is(err, ErrInvalidBondSeries) {
			t.Fatalf("ActiveOffers(%q) error = %v", bond, err)
		}
	}

	store.queryErr = context.Canceled
	if err := service.StreamActiveOffers(context.Background(), testAccess(OperationListActiveOffers), "BND", func(SaleOffer) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActiveOffers(store error) = %v", err)
	}
}

func TestServiceActiveBondSeries(t *testing.T) {
	t.Parallel()

	expected := []BondSeries{"BND", "GOV"}
	store := &storeStub{series: expected}
	actual, err := NewService(store).ActiveBondSeries(context.Background(), testAccess(OperationListBondSeries))
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("ActiveBondSeries() = %#v, %v", actual, err)
	}
	store.seriesError = context.Canceled
	if _, err := NewService(store).ActiveBondSeries(context.Background(), testAccess(OperationListBondSeries)); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActiveBondSeries() error = %v", err)
	}
}

func TestServiceRejectsInvalidOperationContextAndIdempotency(t *testing.T) {
	t.Parallel()
	service := NewService(&storeStub{})
	invalid := testAccess("other.operation")
	if _, err := service.Buy(context.Background(), invalid, testNonce, testOfferID); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("Buy() operation error = %v", err)
	}
	if _, err := service.Buy(context.Background(), testAccess(OperationBuy), "short", testOfferID); !errors.Is(err, ErrInvalidIdempotencyKey) {
		t.Fatalf("Buy() idempotency error = %v", err)
	}
	if _, err := service.CreateSaleOffer(context.Background(), invalid, testNonce, "BND", "1", "USD"); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("CreateSaleOffer() operation error = %v", err)
	}
	if _, err := service.CreateSaleOffer(context.Background(), testAccess(OperationCreateSaleOffer), "short", "BND", "1", "USD"); !errors.Is(err, ErrInvalidIdempotencyKey) {
		t.Fatalf("CreateSaleOffer() idempotency error = %v", err)
	}
	if err := service.StreamActiveOffers(context.Background(), invalid, "BND", func(SaleOffer) error { return nil }); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("StreamActiveOffers() operation error = %v", err)
	}
	if err := service.StreamActiveOffers(context.Background(), testAccess(OperationListActiveOffers), "BND", nil); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("StreamActiveOffers() nil callback error = %v", err)
	}
	if _, err := service.ActiveBondSeries(context.Background(), invalid); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("ActiveBondSeries() operation error = %v", err)
	}
}
