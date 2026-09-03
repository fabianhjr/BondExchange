package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/shopspring/decimal"
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

func TestBuyHandler(t *testing.T) {
	t.Parallel()

	purchase := exchange.Purchase{
		Offer:    exchange.SaleOffer{ID: "offer-1"},
		BuyerID:  "buyer-1",
		BoughtAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
	}
	application := &applicationStub{purchase: purchase}
	handler := NewHandler(application, healthStub{})

	response := performRequest(handler, http.MethodPost, "/buys", `{"buyer_id":"buyer-1","sale_offer_id":"offer-1"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if application.buyer != "buyer-1" || application.offer != "offer-1" {
		t.Fatalf("application received buyer %q and offer %q", application.buyer, application.offer)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q", contentType)
	}

	tests := []struct {
		name   string
		body   string
		err    error
		status int
	}{
		{name: "malformed", body: `{`, status: http.StatusBadRequest},
		{name: "unknown field", body: `{"buyer_id":"buyer","sale_offer_id":"offer","extra":true}`, status: http.StatusBadRequest},
		{name: "two objects", body: `{"buyer_id":"buyer","sale_offer_id":"offer"} {}`, status: http.StatusBadRequest},
		{name: "invalid buyer", body: `{}`, err: exchange.ErrInvalidUserID, status: http.StatusBadRequest},
		{name: "missing buyer", body: `{}`, err: exchange.ErrBuyerNotFound, status: http.StatusNotFound},
		{name: "unavailable", body: `{}`, err: exchange.ErrOfferUnavailable, status: http.StatusNotFound},
		{name: "internal", body: `{}`, err: errors.New("boom"), status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application.buyErr = test.err
			response := performRequest(handler, http.MethodPost, "/buys", test.body)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestActiveOffersHandler(t *testing.T) {
	t.Parallel()

	application := &applicationStub{offers: []exchange.SaleOffer{{
		ID:    "offer-2",
		Price: decimal.RequireFromString("100.25"),
	}}}
	handler := NewHandler(application, healthStub{})
	response := performRequest(handler, http.MethodGet, "/active-offers?bond=bnd&after=offer-1&limit=25", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if application.bond != "bnd" || application.after != "offer-1" || application.limit != 25 {
		t.Fatalf("application received bond %q, after %q, limit %d", application.bond, application.after, application.limit)
	}
	if !strings.Contains(response.Body.String(), `"id":"offer-2"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"price":"100.25"`) {
		t.Fatalf("body = %s", response.Body.String())
	}

	response = performRequest(handler, http.MethodGet, "/active-offers?limit=nope", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d", response.Code)
	}

	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "invalid bond", err: exchange.ErrInvalidBondSeries, status: http.StatusBadRequest},
		{name: "invalid limit", err: exchange.ErrInvalidActiveOfferLimit, status: http.StatusBadRequest},
		{name: "internal", err: errors.New("boom"), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			application.listErr = test.err
			response := performRequest(handler, http.MethodGet, "/active-offers", "")
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestHealthHandler(t *testing.T) {
	t.Parallel()

	response := performRequest(NewHandler(&applicationStub{}, healthStub{}), http.MethodGet, "/healthz", "")
	if response.Code != http.StatusOK {
		t.Fatalf("healthy status = %d", response.Code)
	}
	response = performRequest(
		NewHandler(&applicationStub{}, healthStub{err: errors.New("down")}),
		http.MethodGet,
		"/healthz",
		"",
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy status = %d", response.Code)
	}
}

func performRequest(handler http.Handler, method string, target string, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
