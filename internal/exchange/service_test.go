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
}

func (store *storeStub) Buy(_ context.Context, buyer UserID, offer OfferID) (Purchase, error) {
	store.buyBuyer = buyer
	store.buyOffer = offer
	return store.buyValue, store.buyError
}

func (store *storeStub) CreateSaleOffer(_ context.Context, offer SaleOffer) (SaleOffer, error) {
	store.createdInput = offer
	return store.createdValue, store.createError
}

func (store *storeStub) ActiveOffers(_ context.Context, bond BondSeries) ([]SaleOffer, error) {
	store.bond = bond
	return store.offers, store.queryErr
}

func (store *storeStub) ActiveBondSeries(context.Context) ([]BondSeries, error) {
	return store.series, store.seriesError
}

func TestServiceBuy(t *testing.T) {
	t.Parallel()

	expected := Purchase{BuyerID: "buyer", BoughtAt: time.Unix(1, 0)}
	store := &storeStub{buyValue: expected}
	actual, err := NewService(store).Buy(context.Background(), "buyer", "offer")
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Buy() = %#v, %v", actual, err)
	}
	if store.buyBuyer != "buyer" || store.buyOffer != "offer" {
		t.Fatalf("store received buyer %q and offer %q", store.buyBuyer, store.buyOffer)
	}

	store.buyError = ErrOfferUnavailable
	if _, err := NewService(store).Buy(context.Background(), "buyer", "offer"); !errors.Is(err, ErrOfferUnavailable) {
		t.Fatalf("Buy() store error = %v", err)
	}
	if _, err := NewService(store).Buy(context.Background(), "", "offer"); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("Buy() buyer error = %v", err)
	}
	if _, err := NewService(store).Buy(context.Background(), "buyer", ""); !errors.Is(err, ErrInvalidOfferID) {
		t.Fatalf("Buy() offer error = %v", err)
	}
}

func TestServiceCreateSaleOffer(t *testing.T) {
	t.Parallel()

	expected := SaleOffer{ID: "offer-1", SellerID: "seller-1", BondSeries: "BND", Price: decimal.RequireFromString("100.25"), Currency: "USD"}
	store := &storeStub{createdValue: expected}
	created, err := NewService(store).CreateSaleOffer(
		context.Background(),
		"offer-1",
		"seller-1",
		"bnd",
		"100.25",
		"USD",
	)
	if err != nil || !reflect.DeepEqual(created, expected) {
		t.Fatalf("CreateSaleOffer() = %#v, %v", created, err)
	}
	if store.createdInput.ID != "offer-1" ||
		store.createdInput.SellerID != "seller-1" ||
		store.createdInput.BondSeries != "BND" ||
		!store.createdInput.Price.Equal(decimal.RequireFromString("100.25")) ||
		store.createdInput.Currency != "USD" {
		t.Fatalf("store received %#v", store.createdInput)
	}

	store.createError = ErrOfferAlreadyExists
	if _, err := NewService(store).CreateSaleOffer(context.Background(), "offer-1", "seller-1", "BND", "1", "USD"); !errors.Is(err, ErrOfferAlreadyExists) {
		t.Fatalf("CreateSaleOffer() store error = %v", err)
	}

	for _, test := range []struct {
		name     string
		id       string
		seller   string
		bond     string
		price    string
		currency string
		want     error
	}{
		{name: "offer ID", seller: "seller", bond: "BND", price: "1", currency: "USD", want: ErrInvalidOfferID},
		{name: "seller ID", id: "offer", bond: "BND", price: "1", currency: "USD", want: ErrInvalidUserID},
		{name: "bond", id: "offer", seller: "seller", bond: "!", price: "1", currency: "USD", want: ErrInvalidBondSeries},
		{name: "price", id: "offer", seller: "seller", bond: "BND", price: "0", currency: "USD", want: ErrInvalidPrice},
		{name: "currency", id: "offer", seller: "seller", bond: "BND", price: "1", want: ErrInvalidCurrencyCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewService(&storeStub{}).CreateSaleOffer(
				context.Background(),
				test.id,
				test.seller,
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

	store := &storeStub{offers: []SaleOffer{{ID: "offer-2"}}}
	service := NewService(store)
	offers, err := service.ActiveOffers(context.Background(), "bnd")
	if err != nil || !reflect.DeepEqual(offers, store.offers) {
		t.Fatalf("ActiveOffers() = %#v, %v", offers, err)
	}
	if store.bond != "BND" {
		t.Fatalf("store received bond %q", store.bond)
	}

	for _, bond := range []string{"", "!"} {
		if _, err := service.ActiveOffers(context.Background(), bond); !errors.Is(err, ErrInvalidBondSeries) {
			t.Fatalf("ActiveOffers(%q) error = %v", bond, err)
		}
	}

	store.queryErr = context.Canceled
	if _, err := service.ActiveOffers(context.Background(), "BND"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActiveOffers(store error) = %v", err)
	}
}

func TestServiceActiveBondSeries(t *testing.T) {
	t.Parallel()

	expected := []BondSeries{"BND", "GOV"}
	store := &storeStub{series: expected}
	actual, err := NewService(store).ActiveBondSeries(context.Background())
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("ActiveBondSeries() = %#v, %v", actual, err)
	}
	store.seriesError = context.Canceled
	if _, err := NewService(store).ActiveBondSeries(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActiveBondSeries() error = %v", err)
	}
}
