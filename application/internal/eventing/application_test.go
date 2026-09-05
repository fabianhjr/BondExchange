package eventing

import (
	"context"
	"errors"
	"testing"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/offerintake"
)

type exchangeFake struct {
	purchase exchange.Purchase
	offer    exchange.SaleOffer
	err      error
}

func (fake *exchangeFake) Buy(context.Context, exchange.AccessContext, string, string) (exchange.Purchase, error) {
	return fake.purchase, fake.err
}

func (fake *exchangeFake) CreateSaleOffer(context.Context, exchange.AccessContext, string, string, string, string, string) (exchange.SaleOffer, error) {
	return fake.offer, fake.err
}

func (fake *exchangeFake) QuoteSaleOffer(context.Context, exchange.AccessContext, string, string, string, string) (offerintake.Quote, error) {
	return offerintake.Quote{}, fake.err
}

func (*exchangeFake) StreamActiveOffers(_ context.Context, _ exchange.AccessContext, _ string, yield func(exchange.SaleOffer) error) error {
	return yield(exchange.SaleOffer{ID: "offer"})
}

func (*exchangeFake) ActiveBondSeries(context.Context, exchange.AccessContext) ([]exchange.BondSeries, error) {
	return []exchange.BondSeries{"BND"}, nil
}

type authorizerFake struct{ err error }

func (fake authorizerFake) Authorize(context.Context, exchange.AccessContext, string) error {
	return fake.err
}

func TestApplicationPublishesSuccessfulMutations(t *testing.T) {
	t.Parallel()
	store := newStoreFake(
		testEvent(TablePurchases, "offer-1"),
		testEvent(TableSaleOffers, "offer-2"),
	)
	publisher := &publisherFake{}
	dispatcher, _ := NewDispatcher(store, []Destination{{ID: "sink", Publisher: publisher}}, 0)
	application := NewApplication(&exchangeFake{
		purchase: exchange.Purchase{ID: "offer-1", Offer: exchange.SaleOffer{ID: "offer-1"}},
		offer:    exchange.SaleOffer{ID: "offer-2"},
	}, authorizerFake{}, dispatcher)
	if _, err := application.QuoteSaleOffer(context.Background(), exchange.AccessContext{}, "key", "BND", "1", "USD"); err != nil {
		t.Fatal(err)
	}

	if _, err := application.Buy(context.Background(), exchange.AccessContext{}, "key", "offer-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := application.CreateSaleOffer(context.Background(), exchange.AccessContext{}, "key", "BND", "1", "MXN", ""); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 2 {
		t.Fatalf("published events = %d", len(publisher.events))
	}
	var offers []exchange.SaleOffer
	if err := application.StreamActiveOffers(context.Background(), exchange.AccessContext{}, "BND", func(offer exchange.SaleOffer) error {
		offers = append(offers, offer)
		return nil
	}); err != nil || len(offers) != 1 {
		t.Fatalf("stream = %#v, %v", offers, err)
	}
	if series, err := application.ActiveBondSeries(context.Background(), exchange.AccessContext{}); err != nil || len(series) != 1 {
		t.Fatalf("series = %#v, %v", series, err)
	}
}

func TestApplicationDoesNotPublishFailedMutation(t *testing.T) {
	t.Parallel()
	want := errors.New("rejected")
	store := newStoreFake()
	publisher := &publisherFake{}
	dispatcher, _ := NewDispatcher(store, []Destination{{ID: "sink", Publisher: publisher}}, 0)
	application := NewApplication(&exchangeFake{err: want}, authorizerFake{}, dispatcher)
	if _, err := application.Buy(context.Background(), exchange.AccessContext{}, "key", "offer"); !errors.Is(err, want) {
		t.Fatalf("Buy() error = %v", err)
	}
	if _, err := application.CreateSaleOffer(context.Background(), exchange.AccessContext{}, "key", "BND", "1", "MXN", ""); !errors.Is(err, want) {
		t.Fatalf("CreateSaleOffer() error = %v", err)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("published events = %d", len(publisher.events))
	}
}

func TestApplicationAuthorizesManualDrain(t *testing.T) {
	t.Parallel()
	event := testEvent(TableSaleOffers, "offer")
	store := newStoreFake(event)
	dispatcher, _ := NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{}}}, 0)
	application := NewApplication(&exchangeFake{}, authorizerFake{}, dispatcher)
	access := exchange.AccessContext{
		Principal: exchange.Principal{ID: "operator"},
		Operation: exchange.OperationPublishPendingEvents,
	}
	summary, err := application.PublishPendingEvents(context.Background(), access, "sink")
	if err != nil || summary.Delivered != 1 {
		t.Fatalf("PublishPendingEvents() = %#v, %v", summary, err)
	}

	invalid := access
	invalid.Operation = exchange.OperationBuy
	if _, err := application.PublishPendingEvents(context.Background(), invalid, "sink"); !errors.Is(err, exchange.ErrInvalidOperation) {
		t.Fatalf("invalid operation error = %v", err)
	}
	denied := NewApplication(&exchangeFake{}, authorizerFake{err: exchange.ErrPermissionDenied}, dispatcher)
	if _, err := denied.PublishPendingEvents(context.Background(), access, "sink"); !errors.Is(err, exchange.ErrPermissionDenied) {
		t.Fatalf("authorization error = %v", err)
	}
}
