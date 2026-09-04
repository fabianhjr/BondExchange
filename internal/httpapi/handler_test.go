package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/internal/authn"
	"github.com/fabianhjr/BondExchange/internal/eventing"
	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/fabianhjr/BondExchange/internal/rpcapi"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/metadata"
)

type applicationStub struct {
	purchase       exchange.Purchase
	buyErr         error
	created        exchange.SaleOffer
	createErr      error
	offers         []exchange.SaleOffer
	listErr        error
	listErrAfter   bool
	series         []exchange.BondSeries
	seriesErr      error
	buyOffer       string
	createID       string
	createBond     string
	createPrice    string
	createCurrency string
	bond           string
	panicBuy       bool
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
	if application.panicBuy {
		panic("test panic")
	}
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
	if application.listErr != nil && !application.listErrAfter {
		return application.listErr
	}
	for _, offer := range application.offers {
		if err := yield(offer); err != nil {
			return err
		}
	}
	return application.listErr
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

type healthStub struct{ err error }

func (health healthStub) Ping(context.Context) error                                      { return health.err }
func (health healthStub) Authorize(context.Context, exchange.AccessContext, string) error { return nil }

type authenticatorStub struct{}

func (authenticatorStub) Authenticate(_ context.Context, operation string, _ []byte, idempotent bool) (authn.Result, error) {
	result := authn.Result{AccessContext: exchange.AccessContext{
		Principal: exchange.Principal{ID: "principal-1", ClientID: "client-1"},
		Operation: operation,
	}}
	if idempotent {
		result.IdempotencyKey = "idempotency-key-1"
	}
	return result, nil
}

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

	response := performRequest(handler, http.MethodPost, "/buys", `{"sale_offer_id":"offer-1"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if application.buyOffer != "offer-1" {
		t.Fatalf("application received offer %q", application.buyOffer)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	for _, expected := range []string{
		`"id":"offer-1"`,
		`"bond_series":"BND1"`,
		`"price":"100.25"`,
		`"currency_code":"USD"`,
		`"bought_at":"2026-09-01T00:00:00Z"`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("body %s does not contain %s", response.Body.String(), expected)
		}
	}
	if strings.Contains(response.Body.String(), "buyer_id") || strings.Contains(response.Body.String(), "seller_id") {
		t.Fatalf("response exposed user identity: %s", response.Body.String())
	}

	tests := []struct {
		name   string
		body   string
		err    error
		status int
	}{
		{name: "malformed", body: `{`, status: http.StatusBadRequest},
		{name: "identity field rejected", body: `{"buyer_id":"buyer","sale_offer_id":"offer"}`, status: http.StatusBadRequest},
		{name: "unknown field", body: `{"sale_offer_id":"offer","extra":true}`, status: http.StatusBadRequest},
		{name: "duplicate field", body: `{"sale_offer_id":"offer-a","sale_offer_id":"offer-b"}`, status: http.StatusBadRequest},
		{name: "nested duplicate field", body: `{"sale_offer_id":"offer","extra":{"a":1,"a":2}}`, status: http.StatusBadRequest},
		{name: "two objects", body: `{"sale_offer_id":"offer"} {}`, status: http.StatusBadRequest},
		{name: "invalid buyer", body: `{}`, err: exchange.ErrInvalidUserID, status: http.StatusBadRequest},
		{name: "missing buyer", body: `{}`, err: exchange.ErrBuyerNotFound, status: http.StatusInternalServerError},
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
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json-seq") {
		t.Fatalf("stream Content-Type = %q", contentType)
	}
	if !strings.Contains(response.Body.String(), `"complete":{"offer_count":"1"}`) {
		t.Fatalf("stream completion missing from %q", response.Body.String())
	}
	response = performRequest(handler, http.MethodGet, "/active-offers?bond=A&bond=B", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("polluted query status = %d", response.Code)
	}
	response = performRequest(handler, http.MethodPost, "/active-offers?bond=BND", `{}`)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported stream method status = %d", response.Code)
	}
	response = performRequest(handler, http.MethodGet, "/active-offers", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing query status = %d", response.Code)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/active-offers?bond=BND", strings.NewReader(`{}`))
	request.ContentLength = -1
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("GET body status = %d", response.Code)
	}

	application.listErr = errors.New("stream failed")
	application.listErrAfter = true
	response = performRequest(handler, http.MethodGet, "/active-offers?bond=bnd", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"error":"internal server error"`) {
		t.Fatalf("midstream error response = %d, %q", response.Code, response.Body.String())
	}
	application.listErrAfter = false

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
			response := performRequest(handler, http.MethodGet, "/active-offers?bond=bnd", "")
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
		`{"id":"offer-2","bond_series":"bnd1","price":"99.75","currency_code":"USD"}`,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if application.createID != "offer-2" || application.createBond != "bnd1" || application.createPrice != "99.75" ||
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
		{name: "missing seller", err: exchange.ErrSellerNotFound, status: http.StatusInternalServerError},
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

func TestPublishPendingEventsHandler(t *testing.T) {
	t.Parallel()
	application := &applicationStub{publishSummary: eventing.Summary{
		Attempted: 4,
		Delivered: 3,
		Failed:    1,
		Remaining: 1,
	}}
	handler := newHandler(t, application, healthStub{})
	response := performRequest(
		handler,
		http.MethodPost,
		"/event-publications:publish-pending",
		`{"destination_id":"security"}`,
	)
	if response.Code != http.StatusOK || application.destinationID != "security" {
		t.Fatalf("response = %d %s, destination = %q", response.Code, response.Body.String(), application.destinationID)
	}
	for _, expected := range []string{`"attempted":"4"`, `"delivered":"3"`, `"failed":"1"`, `"remaining":"1"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("body %s does not contain %s", response.Body.String(), expected)
		}
	}

	application.publishErr = eventing.ErrNoPublishers
	response = performRequest(handler, http.MethodPost, "/event-publications:publish-pending", `{}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("no-publisher status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestJSONStructureValidation(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{
		`{}`,
		`{"object":{"key":"value"},"array":[1,true,null,{"nested":[]}]}`,
	} {
		if err := validateSingleJSONObject([]byte(valid)); err != nil {
			t.Fatalf("valid JSON %s error = %v", valid, err)
		}
	}
	for _, invalid := range []string{
		`[]`,
		`{"a":1} {"b":2}`,
		`{"a":[1,2}`,
		`{"a":{"b":1,"b":2}}`,
	} {
		if err := validateSingleJSONObject([]byte(invalid)); err == nil {
			t.Fatalf("invalid JSON %s was accepted", invalid)
		}
	}
}

func TestRESTStreamInterfaceAndPanicRecovery(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	stream := &activeOffersRESTStream{context: context.Background(), response: recorder}
	if err := stream.SetHeader(metadata.Pairs("key", "value")); err != nil {
		t.Fatal(err)
	}
	if err := stream.SendHeader(metadata.Pairs("key", "value")); err != nil {
		t.Fatal(err)
	}
	stream.SetTrailer(metadata.Pairs("key", "value"))
	if stream.Context() == nil {
		t.Fatal("stream context is nil")
	}
	message := &bondexchangev1.ListActiveOffersResponse{
		Event: &bondexchangev1.ListActiveOffersResponse_Complete{Complete: &bondexchangev1.ListActiveOffersComplete{}},
	}
	if err := stream.SendMsg(message); err != nil {
		t.Fatal(err)
	}
	if err := stream.SendMsg(&bondexchangev1.BuyRequest{}); err == nil {
		t.Fatal("SendMsg accepted wrong message type")
	}
	if err := stream.RecvMsg(nil); !errors.Is(err, io.EOF) {
		t.Fatalf("RecvMsg() = %v", err)
	}

	handler := newHandler(t, &applicationStub{panicBuy: true}, healthStub{})
	response := performRequest(handler, http.MethodPost, "/buys", `{"sale_offer_id":"offer-1"}`)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "internal server error") {
		t.Fatalf("panic response = %d, %s", response.Code, response.Body.String())
	}
}

func TestRequestBodyLimitsAndMediaType(t *testing.T) {
	t.Parallel()
	handler := newHandler(t, &applicationStub{}, healthStub{})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/buys", strings.NewReader(`{"sale_offer_id":"offer"}`))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("media type status = %d", response.Code)
	}
	request = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/buys", strings.NewReader(`{"sale_offer_id":"offer"}`))
	request.Header.Set("Content-Type", "application/jsonp")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("JSONP media type status = %d", response.Code)
	}

	request = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/buys", strings.NewReader(`{"value":"`+strings.Repeat("x", 70*1024)+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large request status = %d", response.Code)
	}
}

func newHandler(t *testing.T, application *applicationStub, health healthStub) http.Handler {
	t.Helper()
	handler, err := NewHandler(rpcapi.NewServer(application, health, authenticatorStub{}))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func performRequest(handler http.Handler, method string, target string, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer test")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "idempotency-key-1")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
