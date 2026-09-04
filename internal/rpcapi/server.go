package rpcapi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/internal/authn"
	"github.com/fabianhjr/BondExchange/internal/exchange"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type HealthChecker interface {
	Ping(ctx context.Context) error
	Authorize(ctx context.Context, access exchange.AccessContext, permission string) error
}

type Application interface {
	Buy(
		ctx context.Context,
		access exchange.AccessContext,
		idempotencyKey string,
		offer string,
	) (exchange.Purchase, error)
	CreateSaleOffer(
		ctx context.Context,
		access exchange.AccessContext,
		idempotencyKey string,
		id string,
		bond string,
		price string,
		currency string,
	) (exchange.SaleOffer, error)
	StreamActiveOffers(
		ctx context.Context,
		access exchange.AccessContext,
		bond string,
		yield func(exchange.SaleOffer) error,
	) error
	ActiveBondSeries(ctx context.Context, access exchange.AccessContext) ([]exchange.BondSeries, error)
}

func (server *Server) CreateSaleOffer(
	ctx context.Context,
	request *bondexchangev1.CreateSaleOfferRequest,
) (*bondexchangev1.CreateSaleOfferResponse, error) {
	authenticated, err := server.authenticate(ctx, exchange.OperationCreateSaleOffer, request, true)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationCreateSaleOffer, nil, err)
		return nil, transportError(err)
	}
	offer, err := server.application.CreateSaleOffer(
		ctx,
		authenticated.AccessContext,
		authenticated.IdempotencyKey,
		request.GetId(),
		request.GetBondSeries(),
		request.GetPrice(),
		request.GetCurrencyCode(),
	)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationCreateSaleOffer, &authenticated.AccessContext, err)
		return nil, transportError(err)
	}
	logSecurityOperation(ctx, exchange.OperationCreateSaleOffer, &authenticated.AccessContext, nil)
	return &bondexchangev1.CreateSaleOfferResponse{Offer: saleOfferToProto(offer)}, nil
}

type Server struct {
	bondexchangev1.UnimplementedBondExchangeServiceServer
	application   Application
	health        HealthChecker
	authenticator authn.Authenticator
}

func NewServer(application Application, health HealthChecker, authenticator authn.Authenticator) *Server {
	return &Server{application: application, health: health, authenticator: authenticator}
}

func (server *Server) Buy(ctx context.Context, request *bondexchangev1.BuyRequest) (*bondexchangev1.BuyResponse, error) {
	authenticated, err := server.authenticate(ctx, exchange.OperationBuy, request, true)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationBuy, nil, err)
		return nil, transportError(err)
	}
	purchase, err := server.application.Buy(
		ctx,
		authenticated.AccessContext,
		authenticated.IdempotencyKey,
		request.GetSaleOfferId(),
	)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationBuy, &authenticated.AccessContext, err)
		return nil, transportError(err)
	}
	logSecurityOperation(ctx, exchange.OperationBuy, &authenticated.AccessContext, nil)
	return purchaseToProto(purchase), nil
}

func (server *Server) ListActiveOffers(
	request *bondexchangev1.ListActiveOffersRequest,
	stream bondexchangev1.BondExchangeService_ListActiveOffersServer,
) error {
	ctx := stream.Context()
	authenticated, err := server.authenticate(ctx, exchange.OperationListActiveOffers, request, false)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationListActiveOffers, nil, err)
		return transportError(err)
	}
	var count uint64
	err = server.application.StreamActiveOffers(ctx, authenticated.AccessContext, request.GetBond(), func(offer exchange.SaleOffer) error {
		if err := stream.Send(&bondexchangev1.ListActiveOffersResponse{
			Event: &bondexchangev1.ListActiveOffersResponse_Offer{Offer: saleOfferToProto(offer)},
		}); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationListActiveOffers, &authenticated.AccessContext, err, "offer_count", count)
		return transportError(err)
	}
	err = stream.Send(&bondexchangev1.ListActiveOffersResponse{
		Event: &bondexchangev1.ListActiveOffersResponse_Complete{
			Complete: &bondexchangev1.ListActiveOffersComplete{OfferCount: count},
		},
	})
	logSecurityOperation(ctx, exchange.OperationListActiveOffers, &authenticated.AccessContext, err, "offer_count", count)
	return err
}

func (server *Server) ListActiveBondSeries(
	ctx context.Context,
	_ *bondexchangev1.ListActiveBondSeriesRequest,
) (*bondexchangev1.ListActiveBondSeriesResponse, error) {
	authenticated, err := server.authenticate(ctx, exchange.OperationListBondSeries, &bondexchangev1.ListActiveBondSeriesRequest{}, false)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationListBondSeries, nil, err)
		return nil, transportError(err)
	}
	series, err := server.application.ActiveBondSeries(ctx, authenticated.AccessContext)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationListBondSeries, &authenticated.AccessContext, err)
		return nil, transportError(err)
	}
	logSecurityOperation(ctx, exchange.OperationListBondSeries, &authenticated.AccessContext, nil, "series_count", len(series))
	response := &bondexchangev1.ListActiveBondSeriesResponse{
		BondSeries: make([]string, 0, len(series)),
	}
	for _, bondSeries := range series {
		response.BondSeries = append(response.BondSeries, string(bondSeries))
	}
	return response, nil
}

func (server *Server) CheckHealth(
	ctx context.Context,
	_ *bondexchangev1.CheckHealthRequest,
) (*bondexchangev1.CheckHealthResponse, error) {
	authenticated, err := server.authenticate(ctx, exchange.OperationCheckHealth, &bondexchangev1.CheckHealthRequest{}, false)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationCheckHealth, nil, err)
		return nil, transportError(err)
	}
	if err := server.health.Authorize(ctx, authenticated.AccessContext, exchange.PermissionCheckHealth); err != nil {
		logSecurityOperation(ctx, exchange.OperationCheckHealth, &authenticated.AccessContext, err)
		return nil, transportError(err)
	}
	if err := server.health.Ping(ctx); err != nil {
		logSecurityOperation(ctx, exchange.OperationCheckHealth, &authenticated.AccessContext, err)
		return nil, status.Error(codes.Unavailable, "database unavailable")
	}
	logSecurityOperation(ctx, exchange.OperationCheckHealth, &authenticated.AccessContext, nil)
	return &bondexchangev1.CheckHealthResponse{Status: "ok"}, nil
}

func logSecurityOperation(
	ctx context.Context,
	operation string,
	access *exchange.AccessContext,
	err error,
	extra ...any,
) {
	attributes := []any{
		"event", "security.operation",
		"authentication", "federated_operation_assertion",
		"operation", operation,
	}
	if err == nil {
		attributes = append(attributes, "outcome", "succeeded")
	} else {
		attributes = append(attributes,
			"outcome", "rejected",
			"error_code", status.Code(transportError(err)).String(),
			"error_type", fmt.Sprintf("%T", err),
		)
	}
	if access != nil {
		attributes = append(attributes,
			"principal_id", access.Principal.ID,
			"client_id", access.Principal.ClientID,
			"client_class", access.Principal.ClientClass,
			"assertion_id", access.AssertionID,
			"request_sha256", hex.EncodeToString(access.RequestDigest[:]),
		)
	}
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		attributes = append(attributes, "trace_id", span.TraceID().String(), "span_id", span.SpanID().String())
	}
	attributes = append(attributes, extra...)
	if err == nil {
		slog.InfoContext(ctx, "operation completed", attributes...)
		return
	}
	slog.WarnContext(ctx, "operation rejected", attributes...)
}

func purchaseToProto(purchase exchange.Purchase) *bondexchangev1.BuyResponse {
	return &bondexchangev1.BuyResponse{
		Offer:    saleOfferToProto(purchase.Offer),
		BoughtAt: timestamppb.New(purchase.BoughtAt),
	}
}

func saleOfferToProto(offer exchange.SaleOffer) *bondexchangev1.SaleOffer {
	return &bondexchangev1.SaleOffer{
		Id:           string(offer.ID),
		BondSeries:   string(offer.BondSeries),
		Price:        offer.Price.String(),
		CurrencyCode: string(offer.Currency),
	}
}

func (server *Server) authenticate(
	ctx context.Context,
	operation string,
	request proto.Message,
	idempotent bool,
) (authn.Result, error) {
	if server.authenticator == nil {
		return authn.Result{}, exchange.ErrUnauthenticated
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return authn.Result{}, exchange.ErrInvalidOperation
	}
	return server.authenticator.Authenticate(ctx, operation, canonical, idempotent)
}

func transportError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	case errors.Is(err, exchange.ErrUnauthenticated):
		return status.Error(codes.Unauthenticated, "authentication required")
	case errors.Is(err, exchange.ErrPermissionDenied):
		return status.Error(codes.PermissionDenied, "operation not permitted")
	case errors.Is(err, exchange.ErrInvalidUserID),
		errors.Is(err, exchange.ErrInvalidOfferID),
		errors.Is(err, exchange.ErrInvalidBondSeries),
		errors.Is(err, exchange.ErrInvalidPrice),
		errors.Is(err, exchange.ErrInvalidCurrencyCode),
		errors.Is(err, exchange.ErrInvalidIdempotencyKey),
		errors.Is(err, exchange.ErrInvalidOperation):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, exchange.ErrOfferUnavailable),
		errors.Is(err, exchange.ErrBondNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, exchange.ErrBuyerNotFound),
		errors.Is(err, exchange.ErrSellerNotFound):
		return status.Error(codes.Internal, "internal identity consistency error")
	case errors.Is(err, exchange.ErrOfferAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, exchange.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}
