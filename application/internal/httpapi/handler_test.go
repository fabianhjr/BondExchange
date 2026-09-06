package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/application/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/application/internal/authn"
	"github.com/fabianhjr/BondExchange/application/internal/eventing"
	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/offerintake"
	"github.com/fabianhjr/BondExchange/application/internal/ratelimit"
	"github.com/fabianhjr/BondExchange/application/internal/rpcapi"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

type applicationStub struct {
	purchase       exchange.Purchase
	buyErr         error
	created        exchange.SaleOffer
	createErr      error
	quote          offerintake.Quote
	quoteErr       error
	offers         []exchange.SaleOffer
	listErr        error
	listErrAfter   bool
	series         []exchange.BondSeries
	seriesErr      error
	buyOffer       string
	createBond     string
	createPrice    string
	createCurrency string
	createQuoteID  string
	bond           string
	panicBuy       bool
	publishSummary eventing.Summary
	publishErr     error
	destinationID  string
}

func TestHandlerCreatesStableHTTPRouteSpans(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	otel.SetTracerProvider(provider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
		_ = provider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	})
	handler := newHandler(t, &applicationStub{}, healthStub{})

	for index, path := range []string{"/healthz", "/users/01991a20-0000-7000-8000-000000000999"} {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer super-secret-assertion")
		request.Header.Set("User-Agent", "super-secret-agent")
		request.Host = "super-secret-host.example"
		if index == 0 {
			request.Header.Set("Traceparent", "00-01000000000000000000000000000000-0200000000000000-01")
		} else {
			request.URL.RawQuery = "token=super-secret-query"
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
	}
	var names []string
	var healthTraceID string
	routes := make(map[string]string)
	for _, span := range spanRecorder.Ended() {
		if span.SpanKind() == trace.SpanKindServer {
			names = append(names, span.Name())
			for _, item := range span.Attributes() {
				value := item.Value.String()
				if strings.Contains(value, "super-secret") || strings.Contains(value, "01991a20") {
					t.Fatalf("HTTP span leaked sensitive or dynamic input in %q=%q", item.Key, value)
				}
				if item.Key == "http.route" {
					routes[span.Name()] = value
				}
			}
			if span.Name() == "GET /healthz" {
				healthTraceID = span.SpanContext().TraceID().String()
			}
		}
	}
	if len(names) != 2 || names[0] != "GET /healthz" || names[1] != "GET unmatched" {
		t.Fatalf("HTTP server span names = %v", names)
	}
	if healthTraceID != "01000000000000000000000000000000" {
		t.Fatalf("propagated health trace ID = %q", healthTraceID)
	}
	if routes["GET /healthz"] != "/healthz" || routes["GET unmatched"] != "unmatched" {
		t.Fatalf("HTTP route attributes = %v", routes)
	}
	if routeTemplate("/buys") != "/buys" || routeTemplate("/buys/secret-id") != "unmatched" {
		t.Fatal("route templates are not bounded")
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	metricRoutes := make(map[string]bool)
	for _, scope := range collected.ScopeMetrics {
		for _, item := range scope.Metrics {
			if item.Name != "http.server.request.duration" {
				continue
			}
			histogram, ok := item.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("HTTP duration aggregation = %T", item.Data)
			}
			for _, point := range histogram.DataPoints {
				for _, value := range point.Attributes.ToSlice() {
					if strings.Contains(value.Value.String(), "super-secret") || strings.Contains(value.Value.String(), "01991a20") {
						t.Fatalf("HTTP metric leaked sensitive or dynamic input in %q=%q", value.Key, value.Value.String())
					}
					if value.Key == "http.route" {
						metricRoutes[value.Value.AsString()] = true
					}
				}
			}
		}
	}
	if !metricRoutes["/healthz"] || !metricRoutes["unmatched"] || len(metricRoutes) != 2 {
		t.Fatalf("HTTP metric routes = %v", metricRoutes)
	}
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
	bond string,
	price string,
	currency string,
	quoteID string,
) (exchange.SaleOffer, error) {
	application.createBond = bond
	application.createPrice = price
	application.createCurrency = currency
	application.createQuoteID = quoteID
	return application.created, application.createErr
}

func (application *applicationStub) QuoteSaleOffer(
	context.Context,
	exchange.AccessContext,
	string,
	string,
	string,
	string,
) (offerintake.Quote, error) {
	return application.quote, application.quoteErr
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

type rateLimiterStub struct{ err error }

func (stub rateLimiterStub) AdmitRequest(context.Context, exchange.UserID) error { return stub.err }

func TestRateLimitResponsesCarryRetryAfter(t *testing.T) {
	t.Parallel()

	limiter := rateLimiterStub{err: &ratelimit.ExceededError{RetryAfter: 1500 * time.Millisecond}}
	for _, test := range []struct {
		name   string
		method string
		target string
	}{
		{name: "gateway unary", method: http.MethodGet, target: "/active-bond-series"},
		{name: "custom stream", method: http.MethodGet, target: "/active-offers?bond=BND"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newHandlerWithLimiter(t, &applicationStub{}, healthStub{}, limiter)
			response := performRequest(handler, test.method, test.target, "")
			if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "2" {
				t.Fatalf("rate-limit response = %d, Retry-After %q, body %s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
			}
		})
	}
}

type failingResponseWriter struct {
	header http.Header
}

func (writer *failingResponseWriter) Header() http.Header {
	return writer.header
}

func (*failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func (*failingResponseWriter) WriteHeader(int) {}

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
		Currency:   "MXN",
	}}
	handler := newHandler(t, application, healthStub{})
	response := performRequest(
		handler,
		http.MethodPost,
		"/sale-offers",
		`{"bond_series":"bnd1","price":"99.75","currency_code":"USD"}`,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if application.createBond != "bnd1" || application.createPrice != "99.75" ||
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

func TestQuoteSaleOfferHandler(t *testing.T) {
	t.Parallel()

	application := &applicationStub{quote: offerintake.Quote{
		ID:             "01991a20-0000-7000-8000-000000000099",
		BondSeries:     "BND1",
		SubmittedPrice: decimal.RequireFromString("99.75"),
		MXNPrice:       decimal.RequireFromString("1700.7375"),
		Rate:           decimal.RequireFromString("17.05"),
		RateObservedOn: time.Date(2026, time.September, 4, 0, 0, 0, 0, time.UTC),
		ExpiresAt:      time.Date(2026, time.September, 4, 0, 5, 0, 0, time.UTC),
	}}
	handler := newHandler(t, application, healthStub{})
	response := performRequest(
		handler,
		http.MethodPost,
		"/sale-offer-quotes",
		`{"bond_series":"bnd1","price":"99.75","currency_code":"USD"}`,
	)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"currency_code":"MXN"`) ||
		!strings.Contains(response.Body.String(), `"rate_series":"SF43718"`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	application.quoteErr = offerintake.ErrExchangeRateUnavailable
	response = performRequest(
		handler,
		http.MethodPost,
		"/sale-offer-quotes",
		`{"bond_series":"BND1","price":"99.75","currency_code":"USD"}`,
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable-rate status = %d, body = %s", response.Code, response.Body.String())
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
		`{`,
		`{,}`,
		`{"a":`,
		`{"a":1} {"b":2}`,
		`{"a":[1,2}`,
		`{"a":{"b":1,"b":2}}`,
	} {
		if err := validateSingleJSONObject([]byte(invalid)); err == nil {
			t.Fatalf("invalid JSON %s was accepted", invalid)
		}
	}
	if err := validateJSONValue(json.NewDecoder(strings.NewReader("")), json.Delim(']')); err == nil {
		t.Fatal("unexpected closing delimiter was accepted")
	}
	for _, encoded := range []string{"invalid", "[invalid"} {
		if err := validateJSONValue(json.NewDecoder(strings.NewReader(encoded)), json.Delim('[')); err == nil {
			t.Fatalf("malformed array contents %q were accepted", encoded)
		}
	}
}

func TestHTTPWriteAndEmptyBodyPaths(t *testing.T) {
	t.Parallel()
	failed := &failingResponseWriter{header: http.Header{}}
	stream := &activeOffersRESTStream{context: context.Background(), response: failed}
	message := &bondexchangev1.ListActiveOffersResponse{
		Event: &bondexchangev1.ListActiveOffersResponse_Complete{Complete: &bondexchangev1.ListActiveOffersComplete{}},
	}
	if err := stream.writeJSONSequence(message); err == nil {
		t.Fatal("stream write failure was ignored")
	}
	writeRESTError(failed, http.StatusInternalServerError, "failed")

	called := false
	handler := requireSingleJSONDocument(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called = true
		response.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(" \n\t"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("empty body path = called %t, status %d", called, response.Code)
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
	return newHandlerWithLimiter(t, application, health, rateLimiterStub{})
}

func newHandlerWithLimiter(t *testing.T, application *applicationStub, health healthStub, limiter rateLimiterStub) http.Handler {
	t.Helper()
	handler, err := NewHandler(rpcapi.NewServer(application, health, authenticatorStub{}, limiter))
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
