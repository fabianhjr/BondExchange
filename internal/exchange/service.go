package exchange

import "context"

const (
	DefaultActiveOfferLimit = 50
	MaxActiveOfferLimit     = 100
)

type Store interface {
	Buy(ctx context.Context, buyerID UserID, offerID OfferID) (Purchase, error)
	ActiveOffers(ctx context.Context, query ActiveOfferQuery) ([]SaleOffer, error)
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

func (service *Service) ActiveOffers(
	ctx context.Context,
	bond string,
	after string,
	limit int,
) ([]SaleOffer, error) {
	query := ActiveOfferQuery{Limit: limit}
	if query.Limit == 0 {
		query.Limit = DefaultActiveOfferLimit
	}
	if query.Limit < 1 || query.Limit > MaxActiveOfferLimit {
		return nil, ErrInvalidActiveOfferLimit
	}
	if bond != "" {
		series, err := ParseBondSeries(bond)
		if err != nil {
			return nil, err
		}
		query.BondSeries = &series
	}
	if after != "" {
		offerID, err := ParseOfferID(after)
		if err != nil {
			return nil, err
		}
		query.After = offerID
	}
	return service.store.ActiveOffers(ctx, query)
}
