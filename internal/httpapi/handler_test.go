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
	"github.com/fabianhjr/BondExchange/internal/rpcapi"
	"github.com/shopspring/decimal"
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
	buyer          string
	buyOffer       string
	createID       string
	createSeller   string
	createBond     string
	createPrice    string
	createCurrency string
	bond           string
}

func (application *applicationStub) Buy(
	_ context.Context,
	buyer string,
	offer string,
) (exchange.Purchase, error) {
	application.buyer = buyer
	application.buyOffer = offer
	return application.purchase, application.buyErr
}

func (application *applicationStub) CreateSaleOffer(
	_ context.Context,
	id string,
	seller string,
	bond string,
	price string,
	currency string,
) (exchange.SaleOffer, error) {
	application.createID = id
	application.createSeller = seller
	application.createBond = bond
	application.createPrice = price
	application.createCurrency = currency
	return application.created, application.createErr
}

func (application *applicationStub) ActiveOffers(
	_ context.Context,
	bond string,
) ([]exchange.SaleOffer, error) {
	application.bond = bond
	return application.offers, application.listErr
}

func (application *applicationStub) ActiveBondSeries(context.Context) ([]exchange.BondSeries, error) {
	return application.series, application.seriesErr
}

type healthStub struct{ err error }

func (health healthStub) Ping(context.Context) error { return health.err }

func TestBuyHandler(t *testing.T) {
	t.Parallel()

	purchase := exchange.Purchase{
		Offer: exchange.SaleOffer{
			ID:         "offer-1",
			SellerID:   "seller-1",
			BondSeries: "BND1",
			Price:      decimal.RequireFromString("100.25"),
			Currency:   "USD",
		},
		BuyerID:  "buyer-1",
		BoughtAt: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
	}
	application := &applicationStub{purchase: purchase}
	handler := newHandler(t, application, healthStub{})

	response := performRequest(handler, http.MethodPost, "/buys", `{"buyer_id":"buyer-1","sale_offer_id":"offer-1"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if application.buyer != "buyer-1" || application.buyOffer != "offer-1" {
		t.Fatalf("application received buyer %q and offer %q", application.buyer, application.buyOffer)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q", contentType)
	}
	for _, expected := range []string{
		`"id":"offer-1"`,
		`"seller_id":"seller-1"`,
		`"bond_series":"BND1"`,
		`"price":"100.25"`,
		`"currency_code":"USD"`,
		`"buyer_id":"buyer-1"`,
		`"bought_at":"2026-09-01T00:00:00Z"`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("body %s does not contain %s", response.Body.String(), expected)
		}
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
			if test.status >= http.StatusBadRequest && !strings.Contains(response.Body.String(), `"error":`) {
				t.Fatalf("error body = %s", response.Body.String())
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
	handler := newHandler(t, application, healthStub{})
	response := performRequest(handler, http.MethodGet, "/active-offers?bond=bnd", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if application.bond != "bnd" {
		t.Fatalf("application received bond %q", application.bond)
	}
	if !strings.Contains(response.Body.String(), `"id":"offer-2"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"price":"100.25"`) {
		t.Fatalf("body = %s", response.Body.String())
	}

	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "invalid bond", err: exchange.ErrInvalidBondSeries, status: http.StatusBadRequest},
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

func TestCreateSaleOfferHandler(t *testing.T) {
	t.Parallel()

	application := &applicationStub{created: exchange.SaleOffer{
		ID:         "offer-2",
		SellerID:   "seller-1",
		BondSeries: "BND1",
		Price:      decimal.RequireFromString("99.75"),
		Currency:   "USD",
	}}
	handler := newHandler(t, application, healthStub{})
	response := performRequest(
		handler,
		http.MethodPost,
		"/sale-offers",
		`{"id":"offer-2","seller_id":"seller-1","bond_series":"bnd1","price":"99.75","currency_code":"USD"}`,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if application.createID != "offer-2" || application.createSeller != "seller-1" ||
		application.createBond != "bnd1" || application.createPrice != "99.75" ||
		application.createCurrency != "USD" {
		t.Fatalf("application create input = %#v", application)
	}
	for _, expected := range []string{`"offer":{`, `"id":"offer-2"`, `"price":"99.75"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("body %s does not contain %s", response.Body.String(), expected)
		}
	}

	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "invalid", err: exchange.ErrInvalidPrice, status: http.StatusBadRequest},
		{name: "missing seller", err: exchange.ErrSellerNotFound, status: http.StatusNotFound},
		{name: "missing bond", err: exchange.ErrBondNotFound, status: http.StatusNotFound},
		{name: "duplicate", err: exchange.ErrOfferAlreadyExists, status: http.StatusConflict},
		{name: "internal", err: errors.New("boom"), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			application.createErr = test.err
			response := performRequest(handler, http.MethodPost, "/sale-offers", `{}`)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestActiveBondSeriesHandler(t *testing.T) {
	t.Parallel()

	application := &applicationStub{series: []exchange.BondSeries{"BND1", "GOV1"}}
	handler := newHandler(t, application, healthStub{})
	response := performRequest(handler, http.MethodGet, "/active-bond-series", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{`"bond_series":`, `"BND1"`, `"GOV1"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("body %s does not contain %s", response.Body.String(), expected)
		}
	}

	application.seriesErr = errors.New("boom")
	response = performRequest(handler, http.MethodGet, "/active-bond-series", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("error status = %d", response.Code)
	}
}

func TestHealthHandler(t *testing.T) {
	t.Parallel()

	response := performRequest(newHandler(t, &applicationStub{}, healthStub{}), http.MethodGet, "/healthz", "")
	if response.Code != http.StatusOK {
		t.Fatalf("healthy status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("healthy body = %s", response.Body.String())
	}
	response = performRequest(
		newHandler(t, &applicationStub{}, healthStub{err: errors.New("down")}),
		http.MethodGet,
		"/healthz",
		"",
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy status = %d", response.Code)
	}
}

func newHandler(t *testing.T, application *applicationStub, health healthStub) http.Handler {
	t.Helper()
	handler, err := NewHandler(rpcapi.NewServer(application, health))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func performRequest(handler http.Handler, method string, target string, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
