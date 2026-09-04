package exchange

import "context"

type Store interface {
	Buy(ctx context.Context, operation MutationContext, offerID OfferID) (Purchase, error)
	CreateSaleOffer(ctx context.Context, operation MutationContext, offer SaleOffer) (SaleOffer, error)
	StreamActiveOffers(
		ctx context.Context,
		access AccessContext,
		bondSeries BondSeries,
		yield func(SaleOffer) error,
	) error
	ActiveBondSeries(ctx context.Context, access AccessContext) ([]BondSeries, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (service *Service) Buy(ctx context.Context, access AccessContext, idempotencyKey string, offer string) (Purchase, error) {
	if access.Operation != OperationBuy || access.Principal.ID == "" {
		return Purchase{}, ErrInvalidOperation
	}
	if !IsValidIdempotencyKey(idempotencyKey) {
		return Purchase{}, ErrInvalidIdempotencyKey
	}
	offerID, err := ParseOfferID(offer)
	if err != nil {
		return Purchase{}, err
	}
	return service.store.Buy(ctx, MutationContext{AccessContext: access, IdempotencyKey: idempotencyKey}, offerID)
}

func (service *Service) CreateSaleOffer(
	ctx context.Context,
	access AccessContext,
	idempotencyKey string,
	id string,
	bond string,
	price string,
	currency string,
) (SaleOffer, error) {
	if access.Operation != OperationCreateSaleOffer || access.Principal.ID == "" {
		return SaleOffer{}, ErrInvalidOperation
	}
	if !IsValidIdempotencyKey(idempotencyKey) {
		return SaleOffer{}, ErrInvalidIdempotencyKey
	}
	offerID, err := ParseOfferID(id)
	if err != nil {
		return SaleOffer{}, err
	}
	bondSeries, err := ParseBondSeries(bond)
	if err != nil {
		return SaleOffer{}, err
	}
	offerPrice, err := ParsePrice(price)
	if err != nil {
		return SaleOffer{}, err
	}
	currencyCode, err := ParseCurrencyCode(currency)
	if err != nil {
		return SaleOffer{}, err
	}
	return service.store.CreateSaleOffer(ctx, MutationContext{
		AccessContext:  access,
		IdempotencyKey: idempotencyKey,
	}, SaleOffer{
		ID:         offerID,
		SellerID:   access.Principal.ID,
		BondSeries: bondSeries,
		Price:      offerPrice,
		Currency:   currencyCode,
	})
}

func (service *Service) StreamActiveOffers(
	ctx context.Context,
	access AccessContext,
	bond string,
	yield func(SaleOffer) error,
) error {
	if access.Operation != OperationListActiveOffers || access.Principal.ID == "" || yield == nil {
		return ErrInvalidOperation
	}
	series, err := ParseBondSeries(bond)
	if err != nil {
		return err
	}
	return service.store.StreamActiveOffers(ctx, access, series, yield)
}

func (service *Service) ActiveBondSeries(ctx context.Context, access AccessContext) ([]BondSeries, error) {
	if access.Operation != OperationListBondSeries || access.Principal.ID == "" {
		return nil, ErrInvalidOperation
	}
	return service.store.ActiveBondSeries(ctx, access)
}
