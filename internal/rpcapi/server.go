package rpcapi

import (
	"context"
	"errors"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/internal/exchange"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type HealthChecker interface {
	Ping(ctx context.Context) error
}

type Application interface {
	Buy(ctx context.Context, buyer string, offer string) (exchange.Purchase, error)
	ActiveOffers(ctx context.Context, bond string, after string, limit int) ([]exchange.SaleOffer, error)
}

type Server struct {
	bondexchangev1.UnimplementedBondExchangeServiceServer
	application Application
	health      HealthChecker
}

func NewServer(application Application, health HealthChecker) *Server {
	return &Server{application: application, health: health}
}

func (server *Server) Buy(ctx context.Context, request *bondexchangev1.BuyRequest) (*bondexchangev1.BuyResponse, error) {
	purchase, err := server.application.Buy(ctx, request.GetBuyerId(), request.GetSaleOfferId())
	if err != nil {
		return nil, transportError(err)
	}
	return purchaseToProto(purchase), nil
}

func (server *Server) ListActiveOffers(
	ctx context.Context,
	request *bondexchangev1.ListActiveOffersRequest,
) (*bondexchangev1.ListActiveOffersResponse, error) {
	offers, err := server.application.ActiveOffers(
		ctx,
		request.GetBond(),
		request.GetAfter(),
		int(request.GetLimit()),
	)
	if err != nil {
		return nil, transportError(err)
	}
	response := &bondexchangev1.ListActiveOffersResponse{
		Offers: make([]*bondexchangev1.SaleOffer, 0, len(offers)),
	}
	for _, offer := range offers {
		response.Offers = append(response.Offers, saleOfferToProto(offer))
	}
	return response, nil
}

func (server *Server) CheckHealth(
	ctx context.Context,
	_ *bondexchangev1.CheckHealthRequest,
) (*bondexchangev1.CheckHealthResponse, error) {
	if err := server.health.Ping(ctx); err != nil {
		return nil, status.Error(codes.Unavailable, "database unavailable")
	}
	return &bondexchangev1.CheckHealthResponse{Status: "ok"}, nil
}

func purchaseToProto(purchase exchange.Purchase) *bondexchangev1.BuyResponse {
	return &bondexchangev1.BuyResponse{
		Offer:    saleOfferToProto(purchase.Offer),
		BuyerId:  string(purchase.BuyerID),
		BoughtAt: timestamppb.New(purchase.BoughtAt),
	}
}

func saleOfferToProto(offer exchange.SaleOffer) *bondexchangev1.SaleOffer {
	return &bondexchangev1.SaleOffer{
		Id:           string(offer.ID),
		SellerId:     string(offer.SellerID),
		BondSeries:   string(offer.BondSeries),
		Price:        offer.Price.String(),
		CurrencyCode: string(offer.Currency),
	}
}

func transportError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	case errors.Is(err, exchange.ErrInvalidUserID),
		errors.Is(err, exchange.ErrInvalidOfferID),
		errors.Is(err, exchange.ErrInvalidBondSeries),
		errors.Is(err, exchange.ErrInvalidActiveOfferLimit):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, exchange.ErrBuyerNotFound),
		errors.Is(err, exchange.ErrOfferUnavailable):
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}
