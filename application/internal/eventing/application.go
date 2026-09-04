package eventing

import (
	"context"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
)

type Exchange interface {
	Buy(context.Context, exchange.AccessContext, string, string) (exchange.Purchase, error)
	CreateSaleOffer(context.Context, exchange.AccessContext, string, string, string, string, string) (exchange.SaleOffer, error)
	StreamActiveOffers(context.Context, exchange.AccessContext, string, func(exchange.SaleOffer) error) error
	ActiveBondSeries(context.Context, exchange.AccessContext) ([]exchange.BondSeries, error)
}

type Authorizer interface {
	Authorize(context.Context, exchange.AccessContext, string) error
}

type Application struct {
	exchange   Exchange
	authorizer Authorizer
	dispatcher *Dispatcher
}

func NewApplication(exchangeService Exchange, authorizer Authorizer, dispatcher *Dispatcher) *Application {
	return &Application{exchange: exchangeService, authorizer: authorizer, dispatcher: dispatcher}
}

func (application *Application) Buy(
	ctx context.Context,
	access exchange.AccessContext,
	idempotencyKey string,
	offer string,
) (exchange.Purchase, error) {
	purchase, err := application.exchange.Buy(ctx, access, idempotencyKey, offer)
	if err == nil {
		application.dispatcher.Publish(ctx, SourceRef{TableName: TablePurchases, ID: string(purchase.Offer.ID)})
	}
	return purchase, err
}

func (application *Application) CreateSaleOffer(
	ctx context.Context,
	access exchange.AccessContext,
	idempotencyKey string,
	id string,
	bond string,
	price string,
	currency string,
) (exchange.SaleOffer, error) {
	offer, err := application.exchange.CreateSaleOffer(ctx, access, idempotencyKey, id, bond, price, currency)
	if err == nil {
		application.dispatcher.Publish(ctx, SourceRef{TableName: TableSaleOffers, ID: string(offer.ID)})
	}
	return offer, err
}

func (application *Application) StreamActiveOffers(
	ctx context.Context,
	access exchange.AccessContext,
	bond string,
	yield func(exchange.SaleOffer) error,
) error {
	return application.exchange.StreamActiveOffers(ctx, access, bond, yield)
}

func (application *Application) ActiveBondSeries(
	ctx context.Context,
	access exchange.AccessContext,
) ([]exchange.BondSeries, error) {
	return application.exchange.ActiveBondSeries(ctx, access)
}

func (application *Application) PublishPendingEvents(
	ctx context.Context,
	access exchange.AccessContext,
	destinationID string,
) (Summary, error) {
	if access.Operation != exchange.OperationPublishPendingEvents || access.Principal.ID == "" {
		return Summary{}, exchange.ErrInvalidOperation
	}
	if err := application.authorizer.Authorize(ctx, access, exchange.PermissionPublishEvents); err != nil {
		return Summary{}, err
	}
	return application.dispatcher.PublishPending(ctx, destinationID)
}
