package rpcapi

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"testing"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/internal/authn"
	"github.com/fabianhjr/BondExchange/internal/eventing"
	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type applicationStub struct {
	purchase       exchange.Purchase
	buyErr         error
	created        exchange.SaleOffer
	createErr      error
	offers         []exchange.SaleOffer
	listErr        error
	series         []exchange.BondSeries
	seriesErr      error
	buyOffer       string
	createID       string
	createBond     string
	createPrice    string
	createCurrency string
	bond           string
	publishSummary eventing.Summary
	publishErr     error
	destinationID  string
}

func (application *applicationStub) Buy(
	_ context.Context,
	_ exchange.AccessContext,
	_ string,
	offer string,
) (exchange.Purchase, error) {
	application.buyOffer = offer
	return application.purchase, application.buyErr
}

func (application *applicationStub) CreateSaleOffer(
	_ context.Context,
	_ exchange.AccessContext,
	_ string,
	id string,
	bond string,
	price string,
	currency string,
) (exchange.SaleOffer, error) {
	application.createID = id
	application.createBond = bond
	application.createPrice = price
	application.createCurrency = currency
	return application.created, application.createErr
}

func (application *applicationStub) StreamActiveOffers(
	_ context.Context,
	_ exchange.AccessContext,
	bond string,
	yield func(exchange.SaleOffer) error,
) error {
	application.bond = bond
	if application.listErr != nil {
		return application.listErr
	}
	for _, offer := range application.offers {
		if err := yield(offer); err != nil {
			return err
		}
	}
	return nil
}

func (application *applicationStub) ActiveBondSeries(context.Context, exchange.AccessContext) ([]exchange.BondSeries, error) {
	return application.series, application.seriesErr
}

func (application *applicationStub) PublishPendingEvents(
	_ context.Context,
	_ exchange.AccessContext,
	destinationID string,
) (eventing.Summary, error) {
	application.destinationID = destinationID
	return application.publishSummary, application.publishErr
}

type healthStub struct {
	err          error
	authorizeErr error
}

func (health healthStub) Ping(context.Context) error { return health.err }
func (health healthStub) Authorize(context.Context, exchange.AccessContext, string) error {
	return health.authorizeErr
}

type authenticatorStub struct{ err error }

func (stub authenticatorStub) Authenticate(_ context.Context, operation string, _ []byte, idempotent bool) (authn.Result, error) {
	if stub.err != nil {
		return authn.Result{}, stub.err
	}
	result := authn.Result{AccessContext: exchange.AccessContext{
		Principal: exchange.Principal{ID: "principal-1", ClientID: "client-1"},
		Operation: operation,
	}}
	if idempotent {
		result.IdempotencyKey = "idempotency-key-1"
	}
	return result, nil
}

type listStreamStub struct {
	grpc.ServerStream
	//nolint:containedctx // The gRPC stream test double must return its configured request context.
	ctx     context.Context
	sendErr error
}

func (stream *listStreamStub) Context() context.Context {
	return stream.ctx
}

func (stream *listStreamStub) Send(*bondexchangev1.ListActiveOffersResponse) error {
	return stream.sendErr
}

func testServer(application Application, health HealthChecker) *Server {
	return NewServer(application, health, authenticatorStub{})
}

func TestGRPCServer(t *testing.T) {
	t.Parallel()

	boughtAt := time.Date(2026, time.September, 1, 1, 2, 3, 0, time.UTC)
	application := &applicationStub{
		purchase: exchange.Purchase{
			Offer: exchange.SaleOffer{
				ID:         "offer-1",
				SellerID:   "seller-1",
				BondSeries: "BND1",
				Price:      decimal.RequireFromString("100.25"),
				Currency:   "USD",
			},
			BuyerID:  "buyer-1",
			BoughtAt: boughtAt,
		},
		created: exchange.SaleOffer{
			ID:         "offer-2",
			SellerID:   "seller-1",
			BondSeries: "BND1",
			Price:      decimal.RequireFromString("99.75"),
			Currency:   "USD",
		},
		offers: []exchange.SaleOffer{{ID: "offer-2"}},
		series: []exchange.BondSeries{"BND1", "GOV1"},
	}
	client := newClient(t, testServer(application, healthStub{}))

	purchase, err := client.Buy(context.Background(), &bondexchangev1.BuyRequest{
		SaleOfferId: "offer-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if application.buyOffer != "offer-1" {
		t.Fatalf("application received offer %q", application.buyOffer)
	}
	if purchase.GetOffer().GetPrice() != "100.25" {
		t.Fatalf("purchase = %v", purchase)
	}
	if !purchase.GetBoughtAt().AsTime().Equal(boughtAt) {
		t.Fatalf("bought_at = %s", purchase.GetBoughtAt().AsTime())
	}

	created, err := client.CreateSaleOffer(context.Background(), &bondexchangev1.CreateSaleOfferRequest{
		Id:           "offer-2",
		BondSeries:   "bnd1",
		Price:        "99.75",
		CurrencyCode: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.GetOffer().GetId() != "offer-2" || created.GetOffer().GetPrice() != "99.75" {
		t.Fatalf("created offer = %v", created)
	}
	if application.createID != "offer-2" || application.createBond != "bnd1" || application.createPrice != "99.75" ||
		application.createCurrency != "USD" {
		t.Fatalf("create input = %#v", application)
	}

	offers, err := client.ListActiveOffers(context.Background(), &bondexchangev1.ListActiveOffersRequest{Bond: "bnd"})
	if err != nil {
		t.Fatal(err)
	}
	firstOffer, err := offers.Recv()
	if err != nil || firstOffer.GetOffer().GetId() != "offer-2" {
		t.Fatalf("first stream event = %v, %v", firstOffer, err)
	}
	complete, err := offers.Recv()
	if err != nil || complete.GetComplete().GetOfferCount() != 1 {
		t.Fatalf("complete stream event = %v, %v", complete, err)
	}
	if _, err := offers.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("stream terminal error = %v", err)
	}
	if application.bond != "bnd" {
		t.Fatalf("application received bond %q", application.bond)
	}

	series, err := client.ListActiveBondSeries(context.Background(), &bondexchangev1.ListActiveBondSeriesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(series.GetBondSeries(), []string{"BND1", "GOV1"}) {
		t.Fatalf("bond series = %#v", series.GetBondSeries())
	}

	health, err := client.CheckHealth(context.Background(), &bondexchangev1.CheckHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if health.GetStatus() != "ok" {
		t.Fatalf("health status = %q", health.GetStatus())
	}

	application.publishSummary = eventing.Summary{Attempted: 3, Delivered: 2, Failed: 1, Remaining: 1}
	published, err := client.PublishPendingEvents(context.Background(), &bondexchangev1.PublishPendingEventsRequest{
		DestinationId: "security",
	})
	if err != nil {
		t.Fatal(err)
	}
	if application.destinationID != "security" || published.GetAttempted() != 3 || published.GetDelivered() != 2 ||
		published.GetFailed() != 1 || published.GetRemaining() != 1 {
		t.Fatalf("publish pending = %v, destination = %q", published, application.destinationID)
	}
}

func TestGRPCErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "canceled", err: context.Canceled, code: codes.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
		{name: "unauthenticated", err: exchange.ErrUnauthenticated, code: codes.Unauthenticated},
		{name: "permission", err: exchange.ErrPermissionDenied, code: codes.PermissionDenied},
		{name: "invalid", err: exchange.ErrInvalidUserID, code: codes.InvalidArgument},
		{name: "missing offer", err: exchange.ErrOfferUnavailable, code: codes.NotFound},
		{name: "identity inconsistency", err: exchange.ErrBuyerNotFound, code: codes.Internal},
		{name: "idempotency conflict", err: exchange.ErrIdempotencyConflict, code: codes.AlreadyExists},
		{name: "internal", err: errors.New("boom"), code: codes.Internal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newClient(t, testServer(&applicationStub{buyErr: test.err}, healthStub{}))
			_, err := client.Buy(context.Background(), &bondexchangev1.BuyRequest{})
			if status.Code(err) != test.code {
				t.Fatalf("code = %s, want %s", status.Code(err), test.code)
			}
		})
	}

	client := newClient(t, testServer(&applicationStub{listErr: exchange.ErrInvalidBondSeries}, healthStub{}))
	stream, err := client.ListActiveOffers(context.Background(), &bondexchangev1.ListActiveOffersRequest{})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("list code = %s", status.Code(err))
	}

	for _, test := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "invalid", err: exchange.ErrInvalidPrice, code: codes.InvalidArgument},
		{name: "missing seller", err: exchange.ErrSellerNotFound, code: codes.Internal},
		{name: "missing bond", err: exchange.ErrBondNotFound, code: codes.NotFound},
		{name: "duplicate", err: exchange.ErrOfferAlreadyExists, code: codes.AlreadyExists},
		{name: "internal", err: errors.New("boom"), code: codes.Internal},
	} {
		t.Run("create "+test.name, func(t *testing.T) {
			client := newClient(t, testServer(&applicationStub{createErr: test.err}, healthStub{}))
			_, err := client.CreateSaleOffer(context.Background(), &bondexchangev1.CreateSaleOfferRequest{})
			if status.Code(err) != test.code {
				t.Fatalf("code = %s, want %s", status.Code(err), test.code)
			}
		})
	}

	client = newClient(t, testServer(&applicationStub{seriesErr: errors.New("boom")}, healthStub{}))
	_, err = client.ListActiveBondSeries(context.Background(), &bondexchangev1.ListActiveBondSeriesRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("series code = %s", status.Code(err))
	}

	client = newClient(t, testServer(&applicationStub{}, healthStub{err: errors.New("down")}))
	_, err = client.CheckHealth(context.Background(), &bondexchangev1.CheckHealthRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("health code = %s", status.Code(err))
	}

	for _, test := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "unknown destination", err: eventing.ErrUnknownDestination, code: codes.InvalidArgument},
		{name: "no publishers", err: eventing.ErrNoPublishers, code: codes.FailedPrecondition},
		{name: "internal", err: errors.New("boom"), code: codes.Internal},
	} {
		t.Run("publish "+test.name, func(t *testing.T) {
			client := newClient(t, testServer(&applicationStub{publishErr: test.err}, healthStub{}))
			_, err := client.PublishPendingEvents(context.Background(), &bondexchangev1.PublishPendingEventsRequest{})
			if status.Code(err) != test.code {
				t.Fatalf("code = %s, want %s", status.Code(err), test.code)
			}
		})
	}
}

func TestGRPCNilAuthenticatorStreamFailuresAndTraceLogging(t *testing.T) {
	t.Parallel()
	server := NewServer(&applicationStub{}, healthStub{}, nil)
	if _, err := server.Buy(context.Background(), &bondexchangev1.BuyRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("nil authenticator error = %v", err)
	}

	want := errors.New("send failed")
	server = testServer(&applicationStub{offers: []exchange.SaleOffer{{ID: "offer"}}}, healthStub{})
	if err := server.ListActiveOffers(&bondexchangev1.ListActiveOffersRequest{}, &listStreamStub{ctx: context.Background(), sendErr: want}); status.Code(err) != codes.Internal {
		t.Fatalf("offer send error = %v", err)
	}
	server = testServer(&applicationStub{}, healthStub{})
	if err := server.ListActiveOffers(&bondexchangev1.ListActiveOffersRequest{}, &listStreamStub{ctx: context.Background(), sendErr: want}); status.Code(err) != codes.Unknown {
		t.Fatalf("completion send error = %v", err)
	}

	var traceID trace.TraceID
	traceID[0] = 1
	var spanID trace.SpanID
	spanID[0] = 1
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))
	logSecurityOperation(ctx, exchange.OperationCheckHealth, nil, nil)
}

func TestGRPCAuthenticationFailureIsGeneric(t *testing.T) {
	t.Parallel()
	client := newClient(t, NewServer(&applicationStub{}, healthStub{}, authenticatorStub{err: exchange.ErrUnauthenticated}))
	assertUnauthenticated := func(err error) {
		t.Helper()
		if status.Code(err) != codes.Unauthenticated || status.Convert(err).Message() != "authentication required" {
			t.Fatalf("authentication error = %v", err)
		}
	}
	_, err := client.Buy(context.Background(), &bondexchangev1.BuyRequest{SaleOfferId: "offer-1"})
	assertUnauthenticated(err)
	_, err = client.CreateSaleOffer(context.Background(), &bondexchangev1.CreateSaleOfferRequest{})
	assertUnauthenticated(err)
	stream, err := client.ListActiveOffers(context.Background(), &bondexchangev1.ListActiveOffersRequest{Bond: "BND"})
	if err == nil {
		_, err = stream.Recv()
	}
	assertUnauthenticated(err)
	_, err = client.ListActiveBondSeries(context.Background(), &bondexchangev1.ListActiveBondSeriesRequest{})
	assertUnauthenticated(err)
	_, err = client.CheckHealth(context.Background(), &bondexchangev1.CheckHealthRequest{})
	assertUnauthenticated(err)
	_, err = client.PublishPendingEvents(context.Background(), &bondexchangev1.PublishPendingEventsRequest{})
	assertUnauthenticated(err)
}

func TestGRPCHealthAuthorizationFailure(t *testing.T) {
	t.Parallel()
	client := newClient(t, testServer(&applicationStub{}, healthStub{authorizeErr: exchange.ErrPermissionDenied}))
	_, err := client.CheckHealth(context.Background(), &bondexchangev1.CheckHealthRequest{})
	if status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != "operation not permitted" {
		t.Fatalf("authorization error = %v", err)
	}
}

func newClient(t *testing.T, apiServer bondexchangev1.BondExchangeServiceServer) bondexchangev1.BondExchangeServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	bondexchangev1.RegisterBondExchangeServiceServer(grpcServer, apiServer)
	go func() {
		if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			panic(err)
		}
	}()
	t.Cleanup(grpcServer.Stop)
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return bondexchangev1.NewBondExchangeServiceClient(connection)
}
