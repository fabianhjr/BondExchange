package exchange

import "context"

type Store interface {
	Buy(ctx context.Context, buyerID UserID, offerID OfferID) (Purchase, error)
	CreateSaleOffer(ctx context.Context, offer SaleOffer) (SaleOffer, error)
	ActiveOffers(ctx context.Context, bondSeries BondSeries) ([]SaleOffer, error)
	ActiveBondSeries(ctx context.Context) ([]BondSeries, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (service *Service) Buy(ctx context.Context, buyer string, offer string) (Purchase, error) {
	buyerID, err := ParseUserID(buyer)
	if err != nil {
		return Purchase{}, err
	}
	offerID, err := ParseOfferID(offer)
	if err != nil {
		return Purchase{}, err
	}
	return service.store.Buy(ctx, buyerID, offerID)
}

func (service *Service) CreateSaleOffer(
	ctx context.Context,
	id string,
	seller string,
	bond string,
	price string,
	currency string,
) (SaleOffer, error) {
	offerID, err := ParseOfferID(id)
	if err != nil {
		return SaleOffer{}, err
	}
	sellerID, err := ParseUserID(seller)
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
	return service.store.CreateSaleOffer(ctx, SaleOffer{
		ID:         offerID,
		SellerID:   sellerID,
		BondSeries: bondSeries,
		Price:      offerPrice,
		Currency:   currencyCode,
	})
}

func (service *Service) ActiveOffers(
	ctx context.Context,
	bond string,
) ([]SaleOffer, error) {
	series, err := ParseBondSeries(bond)
	if err != nil {
		return nil, err
	}
	return service.store.ActiveOffers(ctx, series)
}

func (service *Service) ActiveBondSeries(ctx context.Context) ([]BondSeries, error) {
	return service.store.ActiveBondSeries(ctx)
}
