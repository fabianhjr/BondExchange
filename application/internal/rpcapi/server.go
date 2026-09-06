package rpcapi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/application/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/application/internal/authn"
	"github.com/fabianhjr/BondExchange/application/internal/eventing"
	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/offerintake"
	"github.com/fabianhjr/BondExchange/application/internal/ratelimit"
	"github.com/fabianhjr/BondExchange/application/internal/telemetry"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
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
		bond string,
		price string,
		currency string,
		quoteID string,
	) (exchange.SaleOffer, error)
	QuoteSaleOffer(
		ctx context.Context,
		access exchange.AccessContext,
		idempotencyKey string,
		bond string,
		price string,
		currency string,
	) (offerintake.Quote, error)
	StreamActiveOffers(
		ctx context.Context,
		access exchange.AccessContext,
		bond string,
		yield func(exchange.SaleOffer) error,
	) error
	ActiveBondSeries(ctx context.Context, access exchange.AccessContext) ([]exchange.BondSeries, error)
	PublishPendingEvents(ctx context.Context, access exchange.AccessContext, destinationID string) (eventing.Summary, error)
}

func (server *Server) QuoteSaleOffer(
	ctx context.Context,
	request *bondexchangev1.QuoteSaleOfferRequest,
) (*bondexchangev1.QuoteSaleOfferResponse, error) {
	ctx = telemetry.BeginOperation(ctx, exchange.OperationQuoteSaleOffer)
	defer telemetry.CompleteOperationOnPanic(ctx, exchange.OperationQuoteSaleOffer, nil)
	authenticated, err := server.authenticate(ctx, exchange.OperationQuoteSaleOffer, request, true)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationQuoteSaleOffer, authenticatedAccess(authenticated), err)
		return nil, transportError(err)
	}
	quote, err := server.application.QuoteSaleOffer(
		ctx,
		authenticated.AccessContext,
		authenticated.IdempotencyKey,
		request.GetBondSeries(),
		request.GetPrice(),
		request.GetCurrencyCode(),
	)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationQuoteSaleOffer, &authenticated.AccessContext, err)
		return nil, transportError(err)
	}
	logSecurityOperation(ctx, exchange.OperationQuoteSaleOffer, &authenticated.AccessContext, nil)
	return &bondexchangev1.QuoteSaleOfferResponse{
		QuoteId:               string(quote.ID),
		BondSeries:            string(quote.BondSeries),
		SubmittedPrice:        quote.SubmittedPrice.String(),
		SubmittedCurrencyCode: string(offerintake.USD),
		MxnPrice:              quote.MXNPrice.String(),
		CurrencyCode:          string(exchange.MXN),
		Rate:                  quote.Rate.String(),
		RateSeries:            offerintake.FIXSeriesID,
		RateObservedOn:        timestamppb.New(quote.RateObservedOn),
		ExpiresAt:             timestamppb.New(quote.ExpiresAt),
	}, nil
}

func (server *Server) CreateSaleOffer(
	ctx context.Context,
	request *bondexchangev1.CreateSaleOfferRequest,
) (*bondexchangev1.CreateSaleOfferResponse, error) {
	ctx = telemetry.BeginOperation(ctx, exchange.OperationCreateSaleOffer)
	defer telemetry.CompleteOperationOnPanic(ctx, exchange.OperationCreateSaleOffer, nil)
	authenticated, err := server.authenticate(ctx, exchange.OperationCreateSaleOffer, request, true)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationCreateSaleOffer, authenticatedAccess(authenticated), err)
		return nil, transportError(err)
	}
	offer, err := server.application.CreateSaleOffer(
		ctx,
		authenticated.AccessContext,
		authenticated.IdempotencyKey,
		request.GetBondSeries(),
		request.GetPrice(),
		request.GetCurrencyCode(),
		request.GetConversionQuoteId(),
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
	rateLimiter   ratelimit.Limiter
}

func NewServer(
	application Application,
	health HealthChecker,
	authenticator authn.Authenticator,
	rateLimiter ratelimit.Limiter,
) *Server {
	return &Server{application: application, health: health, authenticator: authenticator, rateLimiter: rateLimiter}
}

func (server *Server) Buy(ctx context.Context, request *bondexchangev1.BuyRequest) (*bondexchangev1.BuyResponse, error) {
	ctx = telemetry.BeginOperation(ctx, exchange.OperationBuy)
	defer telemetry.CompleteOperationOnPanic(ctx, exchange.OperationBuy, nil)
	authenticated, err := server.authenticate(ctx, exchange.OperationBuy, request, true)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationBuy, authenticatedAccess(authenticated), err)
		return nil, transportError(err)
	}
	purchase, err := server.application.Buy(
		ctx,
		authenticated.AccessContext,
		authenticated.IdempotencyKey,
		request.GetSaleOfferId(),
	)
	if err != nil {
		if errors.Is(err, exchange.ErrSelfTradeProhibited) {
			telemetry.RecordSelfTradeRejection(ctx)
		}
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
	ctx = telemetry.BeginOperation(ctx, exchange.OperationListActiveOffers)
	var count uint64
	defer telemetry.CompleteOperationOnPanic(ctx, exchange.OperationListActiveOffers, func() int64 {
		return int64(count) //nolint:gosec // The count is bounded by successfully emitted Go values.
	})
	authenticated, err := server.authenticate(ctx, exchange.OperationListActiveOffers, request, false)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationListActiveOffers, authenticatedAccess(authenticated), err)
		return transportError(err)
	}
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
	ctx = telemetry.BeginOperation(ctx, exchange.OperationListBondSeries)
	defer telemetry.CompleteOperationOnPanic(ctx, exchange.OperationListBondSeries, nil)
	authenticated, err := server.authenticate(ctx, exchange.OperationListBondSeries, &bondexchangev1.ListActiveBondSeriesRequest{}, false)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationListBondSeries, authenticatedAccess(authenticated), err)
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
	ctx = telemetry.BeginOperation(ctx, exchange.OperationCheckHealth)
	defer telemetry.CompleteOperationOnPanic(ctx, exchange.OperationCheckHealth, nil)
	authenticated, err := server.authenticate(ctx, exchange.OperationCheckHealth, &bondexchangev1.CheckHealthRequest{}, false)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationCheckHealth, authenticatedAccess(authenticated), err)
		return nil, transportError(err)
	}
	if err := server.health.Authorize(ctx, authenticated.AccessContext, exchange.PermissionCheckHealth); err != nil {
		logSecurityOperation(ctx, exchange.OperationCheckHealth, &authenticated.AccessContext, err)
		return nil, transportError(err)
	}
	if err := server.health.Ping(ctx); err != nil {
		unavailable := status.Error(codes.Unavailable, "database unavailable")
		logSecurityOperation(ctx, exchange.OperationCheckHealth, &authenticated.AccessContext, unavailable)
		return nil, unavailable
	}
	logSecurityOperation(ctx, exchange.OperationCheckHealth, &authenticated.AccessContext, nil)
	return &bondexchangev1.CheckHealthResponse{Status: "ok"}, nil
}

func (server *Server) PublishPendingEvents(
	ctx context.Context,
	request *bondexchangev1.PublishPendingEventsRequest,
) (*bondexchangev1.PublishPendingEventsResponse, error) {
	ctx = telemetry.BeginOperation(ctx, exchange.OperationPublishPendingEvents)
	defer telemetry.CompleteOperationOnPanic(ctx, exchange.OperationPublishPendingEvents, nil)
	authenticated, err := server.authenticate(ctx, exchange.OperationPublishPendingEvents, request, true)
	if err != nil {
		logSecurityOperation(ctx, exchange.OperationPublishPendingEvents, authenticatedAccess(authenticated), err)
		return nil, transportError(err)
	}
	summary, err := server.application.PublishPendingEvents(
		ctx,
		authenticated.AccessContext,
		request.GetDestinationId(),
	)
	logSecurityOperation(
		ctx,
		exchange.OperationPublishPendingEvents,
		&authenticated.AccessContext,
		err,
		"attempted", summary.Attempted,
		"delivered", summary.Delivered,
		"failed", summary.Failed,
		"remaining", summary.Remaining,
	)
	if err != nil {
		return nil, transportError(err)
	}
	return &bondexchangev1.PublishPendingEventsResponse{
		Attempted: summary.Attempted,
		Delivered: summary.Delivered,
		Failed:    summary.Failed,
		Remaining: summary.Remaining,
	}, nil
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
	if retryAfter, ok := ratelimit.RetryAfter(err); ok {
		seconds := int64(retryAfter / time.Second)
		if retryAfter%time.Second != 0 {
			seconds++
		}
		attributes = append(attributes, "retry_after_seconds", seconds)
	}
	streamedOffers := int64(-1)
	for index := 0; index+1 < len(extra); index += 2 {
		if extra[index] == "offer_count" {
			switch count := extra[index+1].(type) {
			case uint64:
				streamedOffers = int64(count) //nolint:gosec // The count is bounded by successfully emitted Go values.
			case int:
				streamedOffers = int64(count)
			}
		}
	}
	outcome := "succeeded"
	errorCode := ""
	if err != nil {
		outcome = "rejected"
		errorCode = status.Code(transportError(err)).String()
	}
	telemetry.CompleteOperation(ctx, operation, outcome, errorCode, streamedOffers)
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
	authenticated, err := server.authenticator.Authenticate(ctx, operation, canonical, idempotent)
	if err != nil {
		return authn.Result{}, err
	}
	started := time.Now()
	if server.rateLimiter == nil {
		telemetry.RecordRateLimit(ctx, operation, "error", time.Since(started))
		return authenticated, ratelimit.ErrUnavailable
	}
	if err := server.rateLimiter.AdmitRequest(ctx, authenticated.AccessContext.Principal.ID); err != nil {
		outcome := "error"
		if errors.Is(err, ratelimit.ErrExceeded) {
			outcome = "rejected"
		}
		telemetry.RecordRateLimit(ctx, operation, outcome, time.Since(started))
		return authenticated, err
	}
	telemetry.RecordRateLimit(ctx, operation, "allowed", time.Since(started))
	return authenticated, nil
}

func authenticatedAccess(authenticated authn.Result) *exchange.AccessContext {
	if authenticated.AccessContext.Principal.ID == "" {
		return nil
	}
	return &authenticated.AccessContext
}

func transportError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	case errors.Is(err, exchange.ErrUnauthenticated):
		return status.Error(codes.Unauthenticated, "authentication required")
	case errors.Is(err, exchange.ErrPermissionDenied):
		return status.Error(codes.PermissionDenied, "operation not permitted")
	case errors.Is(err, ratelimit.ErrExceeded):
		limited := status.New(codes.ResourceExhausted, ratelimit.ErrExceeded.Error())
		retryAfter, ok := ratelimit.RetryAfter(err)
		if !ok {
			return limited.Err()
		}
		withRetry, detailErr := limited.WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(retryAfter)})
		if detailErr != nil {
			return limited.Err()
		}
		return withRetry.Err()
	case errors.Is(err, ratelimit.ErrUnavailable):
		return status.Error(codes.Unavailable, "request admission unavailable")
	case errors.Is(err, exchange.ErrInvalidOfferID),
		errors.Is(err, exchange.ErrInvalidBondSeries),
		errors.Is(err, exchange.ErrInvalidPrice),
		errors.Is(err, exchange.ErrInvalidCurrencyCode),
		errors.Is(err, exchange.ErrInvalidIdempotencyKey),
		errors.Is(err, exchange.ErrInvalidOperation),
		errors.Is(err, offerintake.ErrUnsupportedSubmissionCurrency),
		errors.Is(err, offerintake.ErrInvalidConversionQuote):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, offerintake.ErrConversionQuoteRequired),
		errors.Is(err, offerintake.ErrConversionQuoteUnavailable),
		errors.Is(err, exchange.ErrSelfTradeProhibited):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, offerintake.ErrExchangeRateUnavailable):
		return status.Error(codes.Unavailable, err.Error())
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
	case errors.Is(err, eventing.ErrUnknownDestination):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, eventing.ErrNoPublishers):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}
