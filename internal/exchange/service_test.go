package exchange

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type storeStub struct {
	buyBuyer UserID
	buyOffer OfferID
	buyValue Purchase
	buyError error
	query    ActiveOfferQuery
	offers   []SaleOffer
	queryErr error
}

func (store *storeStub) Buy(_ context.Context, buyer UserID, offer OfferID) (Purchase, error) {
	store.buyBuyer = buyer
	store.buyOffer = offer
	return store.buyValue, store.buyError
}

func (store *storeStub) ActiveOffers(_ context.Context, query ActiveOfferQuery) ([]SaleOffer, error) {
	store.query = query
	return store.offers, store.queryErr
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

func TestServiceActiveOffers(t *testing.T) {
	t.Parallel()

	store := &storeStub{offers: []SaleOffer{{ID: "offer-2"}}}
	service := NewService(store)
	offers, err := service.ActiveOffers(context.Background(), "bnd", "offer-1", 10)
	if err != nil || !reflect.DeepEqual(offers, store.offers) {
		t.Fatalf("ActiveOffers() = %#v, %v", offers, err)
	}
	if store.query.Limit != 10 || store.query.After != "offer-1" || store.query.BondSeries == nil || *store.query.BondSeries != "BND" {
		t.Fatalf("query = %#v", store.query)
	}

	if _, err := service.ActiveOffers(context.Background(), "", "", 0); err != nil {
		t.Fatalf("ActiveOffers(defaults) error = %v", err)
	}
	if store.query.Limit != DefaultActiveOfferLimit || store.query.BondSeries != nil {
		t.Fatalf("default query = %#v", store.query)
	}

	for _, limit := range []int{-1, MaxActiveOfferLimit + 1} {
		if _, err := service.ActiveOffers(context.Background(), "", "", limit); !errors.Is(err, ErrInvalidActiveOfferLimit) {
			t.Fatalf("ActiveOffers(limit %d) error = %v", limit, err)
		}
	}
	if _, err := service.ActiveOffers(context.Background(), "!", "", 1); !errors.Is(err, ErrInvalidBondSeries) {
		t.Fatalf("ActiveOffers(invalid bond) error = %v", err)
	}
	if _, err := service.ActiveOffers(context.Background(), "", "", 1); err != nil {
		t.Fatalf("ActiveOffers(valid) error = %v", err)
	}

	store.queryErr = context.Canceled
	if _, err := service.ActiveOffers(context.Background(), "", "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActiveOffers(store error) = %v", err)
	}
}
