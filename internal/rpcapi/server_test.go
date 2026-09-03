package rpcapi

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type applicationStub struct {
	purchase exchange.Purchase
	buyErr   error
	offers   []exchange.SaleOffer
	listErr  error
	buyer    string
	offer    string
	bond     string
	after    string
	limit    int
}

func (application *applicationStub) Buy(
	_ context.Context,
	buyer string,
	offer string,
) (exchange.Purchase, error) {
	application.buyer = buyer
	application.offer = offer
	return application.purchase, application.buyErr
}

func (application *applicationStub) ActiveOffers(
	_ context.Context,
	bond string,
	after string,
	limit int,
) ([]exchange.SaleOffer, error) {
	application.bond = bond
	application.after = after
	application.limit = limit
	return application.offers, application.listErr
}

type healthStub struct{ err error }

func (health healthStub) Ping(context.Context) error { return health.err }

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
		offers: []exchange.SaleOffer{{ID: "offer-2"}},
	}
	client := newClient(t, NewServer(application, healthStub{}))

	purchase, err := client.Buy(context.Background(), &bondexchangev1.BuyRequest{
		BuyerId:     "buyer-1",
		SaleOfferId: "offer-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if application.buyer != "buyer-1" || application.offer != "offer-1" {
		t.Fatalf("application received buyer %q and offer %q", application.buyer, application.offer)
	}
	if purchase.GetOffer().GetPrice() != "100.25" || purchase.GetBuyerId() != "buyer-1" {
		t.Fatalf("purchase = %v", purchase)
	}
	if !purchase.GetBoughtAt().AsTime().Equal(boughtAt) {
		t.Fatalf("bought_at = %s", purchase.GetBoughtAt().AsTime())
	}

	offers, err := client.ListActiveOffers(context.Background(), &bondexchangev1.ListActiveOffersRequest{
		Bond:  "bnd",
		After: "offer-1",
		Limit: 25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers.GetOffers()) != 1 || offers.GetOffers()[0].GetId() != "offer-2" {
		t.Fatalf("offers = %v", offers)
	}
	if application.bond != "bnd" || application.after != "offer-1" || application.limit != 25 {
		t.Fatalf("application received bond %q, after %q, limit %d", application.bond, application.after, application.limit)
	}

	health, err := client.CheckHealth(context.Background(), &bondexchangev1.CheckHealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if health.GetStatus() != "ok" {
		t.Fatalf("health status = %q", health.GetStatus())
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
		{name: "invalid", err: exchange.ErrInvalidUserID, code: codes.InvalidArgument},
		{name: "not found", err: exchange.ErrBuyerNotFound, code: codes.NotFound},
		{name: "internal", err: errors.New("boom"), code: codes.Internal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newClient(t, NewServer(&applicationStub{buyErr: test.err}, healthStub{}))
			_, err := client.Buy(context.Background(), &bondexchangev1.BuyRequest{})
			if status.Code(err) != test.code {
				t.Fatalf("code = %s, want %s", status.Code(err), test.code)
			}
		})
	}

	client := newClient(t, NewServer(&applicationStub{listErr: exchange.ErrInvalidActiveOfferLimit}, healthStub{}))
	_, err := client.ListActiveOffers(context.Background(), &bondexchangev1.ListActiveOffersRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("list code = %s", status.Code(err))
	}

	client = newClient(t, NewServer(&applicationStub{}, healthStub{err: errors.New("down")}))
	_, err = client.CheckHealth(context.Background(), &bondexchangev1.CheckHealthRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("health code = %s", status.Code(err))
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
