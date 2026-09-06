package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestRecorderEmitsBoundedSpansAndMetrics(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	created := newRecorder(tracerProvider, meterProvider)
	previous := active.Swap(created)
	t.Cleanup(func() {
		active.Store(previous)
		_ = tracerProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	})

	ctx := BeginOperation(context.Background(), "offers.list")
	CompleteOperation(ctx, "offers.list", "succeeded", "", 3)
	ctx = BeginOperation(context.Background(), "purchases.buy")
	CompleteOperation(ctx, "purchases.buy", "rejected", "Unavailable", -1)
	_, failedSpan := Start(context.Background(), "dependency.call", attribute.String("bounded", "value"))
	End(failedSpan, "dependency_error")
	RecordAuthentication(ctx, "purchases.buy", "succeeded", "complete", 10*time.Millisecond)
	RecordRateFetch(ctx, "latest", "succeeded", 2, 25*time.Millisecond)
	RecordRateFetchSkip(ctx, "latest", "rate_limited")
	RecordRateCache(ctx, "hit")
	RecordObservationAge(ctx, 24*time.Hour)
	RecordObservationValidation(ctx, "accepted")
	RecordEventDelivery(ctx, "failed", "publisher_timeout", time.Second)
	RecordEventStage(ctx, "publish", "error")
	RecordEventPublisherCount(ctx, 2)
	RecordDatabaseRetry(ctx, "purchases.buy", "serialization_failure")
	RecordIdempotency(ctx, "purchases.buy", "replayed")
	RecordRateLimit(ctx, "purchases.buy", "rejected", 5*time.Millisecond)

	ended := spanRecorder.Ended()
	if len(ended) != 3 {
		t.Fatalf("ended spans = %d, want 3", len(ended))
	}
	if ended[1].Status().Code != codes.Error || ended[1].Status().Description != "Unavailable" {
		t.Fatalf("operation error status = %#v", ended[1].Status())
	}
	if ended[2].Status().Code != codes.Error || ended[2].Name() != "dependency.call" {
		t.Fatalf("dependency span = %q %#v", ended[2].Name(), ended[2].Status())
	}
	for _, span := range ended {
		for _, item := range span.Attributes() {
			value := item.Value.String()
			if strings.Contains(value, "00000000-0000-4000") || strings.Contains(value, "secret") {
				t.Fatalf("sensitive or unbounded attribute %q=%q", item.Key, value)
			}
		}
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"bondexchange.authentication.count",
		"bondexchange.authentication.duration",
		"bondexchange.database.transaction.retry",
		"bondexchange.event.delivery.count",
		"bondexchange.event.delivery.duration",
		"bondexchange.event.publisher.configured",
		"bondexchange.event.stage.count",
		"bondexchange.idempotency.result",
		"bondexchange.operation.count",
		"bondexchange.operation.duration",
		"bondexchange.rate.cache.result",
		"bondexchange.rate.fetch.attempt.count",
		"bondexchange.rate.fetch.duration",
		"bondexchange.rate.fetch.skip.count",
		"bondexchange.rate.fetch.work_unit.count",
		"bondexchange.rate.observation.age",
		"bondexchange.rate.observation.validation.count",
		"bondexchange.request.rate_limit.count",
		"bondexchange.request.rate_limit.duration",
		"bondexchange.stream.offer.count",
	}
	type metadata struct {
		description string
		unit        string
	}
	wantMetadata := map[string]metadata{
		"bondexchange.authentication.count":              {"Federated operation authentication results", "{authentication}"},
		"bondexchange.authentication.duration":           {"Federated operation authentication duration", "s"},
		"bondexchange.database.transaction.retry":        {"Retryable PostgreSQL transaction failures", "{retry}"},
		"bondexchange.event.delivery.count":              {"Integration-event delivery attempts", "{attempt}"},
		"bondexchange.event.delivery.duration":           {"Integration-event delivery attempt duration", "s"},
		"bondexchange.event.publisher.configured":        {"Configured integration-event publishers", "{publisher}"},
		"bondexchange.event.stage.count":                 {"Integration-event processing stage results", "{result}"},
		"bondexchange.idempotency.result":                {"Durable mutation idempotency outcomes", "{result}"},
		"bondexchange.operation.count":                   {"Completed application operations", "{operation}"},
		"bondexchange.operation.duration":                {"Application operation duration", "s"},
		"bondexchange.rate.cache.result":                 {"Exchange-rate cache resolution outcomes", "{result}"},
		"bondexchange.rate.fetch.attempt.count":          {"Exchange-rate provider requests", "{attempt}"},
		"bondexchange.rate.fetch.duration":               {"Exchange-rate provider request duration", "s"},
		"bondexchange.rate.fetch.skip.count":             {"Exchange-rate provider requests skipped before transport", "{skip}"},
		"bondexchange.rate.fetch.work_unit.count":        {"Exchange-rate work units included in provider requests", "{work_unit}"},
		"bondexchange.rate.observation.age":              {"Age of an exchange-rate observation accepted by intake", "s"},
		"bondexchange.rate.observation.validation.count": {"Exchange-rate observation validation results", "{validation}"},
		"bondexchange.request.rate_limit.count":          {"Authenticated request rate-limit decisions", "{decision}"},
		"bondexchange.request.rate_limit.duration":       {"Authenticated request admission duration", "s"},
		"bondexchange.stream.offer.count":                {"Offers written by a completed active-offer stream", "{offer}"},
	}
	durationBounds := []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 10}
	shortDurationBounds := []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
	wantBounds := map[string][]float64{
		"bondexchange.authentication.duration":     shortDurationBounds,
		"bondexchange.operation.duration":          durationBounds,
		"bondexchange.rate.fetch.duration":         durationBounds,
		"bondexchange.rate.observation.age":        {0, 3600, 21600, 43200, 86400, 172800, 259200, 345600, 432000, 518400, 604800},
		"bondexchange.event.delivery.duration":     durationBounds,
		"bondexchange.request.rate_limit.duration": shortDurationBounds,
		"bondexchange.stream.offer.count":          {0, 1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000},
	}
	wantSums := map[string]int64{
		"bondexchange.authentication.count":              1,
		"bondexchange.database.transaction.retry":        1,
		"bondexchange.event.delivery.count":              1,
		"bondexchange.event.stage.count":                 1,
		"bondexchange.idempotency.result":                1,
		"bondexchange.operation.count":                   2,
		"bondexchange.rate.cache.result":                 1,
		"bondexchange.rate.fetch.attempt.count":          1,
		"bondexchange.rate.fetch.skip.count":             1,
		"bondexchange.rate.fetch.work_unit.count":        2,
		"bondexchange.rate.observation.validation.count": 1,
		"bondexchange.request.rate_limit.count":          1,
	}
	wantHistogramCount := map[string]uint64{
		"bondexchange.authentication.duration":     1,
		"bondexchange.operation.duration":          2,
		"bondexchange.rate.fetch.duration":         1,
		"bondexchange.rate.observation.age":        1,
		"bondexchange.event.delivery.duration":     1,
		"bondexchange.request.rate_limit.duration": 1,
		"bondexchange.stream.offer.count":          1,
	}
	wantHistogramSum := map[string]float64{
		"bondexchange.authentication.duration":     0.01,
		"bondexchange.rate.fetch.duration":         0.025,
		"bondexchange.rate.observation.age":        86400,
		"bondexchange.event.delivery.duration":     1,
		"bondexchange.request.rate_limit.duration": 0.005,
		"bondexchange.stream.offer.count":          3,
	}
	var got []string
	allowedAttribute := map[attribute.Key]bool{
		"bondexchange.operation": true,
		"outcome":                true,
		"error.type":             true,
		"fetch.kind":             true,
		"stage":                  true,
		"reason":                 true,
	}
	assertBounded := func(set attribute.Set) {
		t.Helper()
		for _, item := range set.ToSlice() {
			if !allowedAttribute[item.Key] {
				t.Fatalf("unapproved metric attribute %q", item.Key)
			}
			if strings.Contains(item.Value.String(), "00000000-0000-4000") {
				t.Fatalf("identifier leaked through metric attribute %q", item.Key)
			}
		}
	}
	for _, scope := range collected.ScopeMetrics {
		for _, item := range scope.Metrics {
			got = append(got, item.Name)
			if metadata := wantMetadata[item.Name]; item.Description != metadata.description || item.Unit != metadata.unit {
				t.Fatalf("metric %s metadata = %q %q, want %q %q", item.Name, item.Description, item.Unit, metadata.description, metadata.unit)
			}
			switch data := item.Data.(type) {
			case metricdata.Sum[int64]:
				var sum int64
				for _, point := range data.DataPoints {
					assertBounded(point.Attributes)
					sum += point.Value
				}
				if sum != wantSums[item.Name] {
					t.Fatalf("metric %s sum = %d, want %d", item.Name, sum, wantSums[item.Name])
				}
			case metricdata.Histogram[int64]:
				var count uint64
				var sum int64
				for _, point := range data.DataPoints {
					assertBounded(point.Attributes)
					if !slices.Equal(point.Bounds, wantBounds[item.Name]) {
						t.Fatalf("metric %s bounds = %v, want %v", item.Name, point.Bounds, wantBounds[item.Name])
					}
					count += point.Count
					sum += point.Sum
				}
				if count != wantHistogramCount[item.Name] || math.Abs(float64(sum)-wantHistogramSum[item.Name]) > 1e-9 {
					t.Fatalf("metric %s count/sum = %d/%d", item.Name, count, sum)
				}
			case metricdata.Histogram[float64]:
				var count uint64
				var sum float64
				for _, point := range data.DataPoints {
					assertBounded(point.Attributes)
					if !slices.Equal(point.Bounds, wantBounds[item.Name]) {
						t.Fatalf("metric %s bounds = %v, want %v", item.Name, point.Bounds, wantBounds[item.Name])
					}
					count += point.Count
					sum += point.Sum
				}
				if count != wantHistogramCount[item.Name] {
					t.Fatalf("metric %s count = %d, want %d", item.Name, count, wantHistogramCount[item.Name])
				}
				if expected, exists := wantHistogramSum[item.Name]; exists && math.Abs(sum-expected) > 1e-9 {
					t.Fatalf("metric %s sum = %f, want %f", item.Name, sum, expected)
				}
			case metricdata.Gauge[int64]:
				var values []int64
				for _, point := range data.DataPoints {
					assertBounded(point.Attributes)
					values = append(values, point.Value)
				}
				if !slices.Equal(values, []int64{2}) {
					t.Fatalf("metric %s values = %v, want [2]", item.Name, values)
				}
			default:
				t.Fatalf("unexpected metric aggregation %T", item.Data)
			}
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("metric names = %v, want %v", got, want)
	}
}

func TestNoRecorderIsANoopAndOperationWithoutStateStillRecords(t *testing.T) {
	previous := active.Swap(nil)
	t.Cleanup(func() { active.Store(previous) })
	ctx, span := Start(context.Background(), "noop")
	End(span, "")
	CompleteOperation(ctx, "health.read", "succeeded", "", -1)
	RecordOperation(ctx, "health.read", "succeeded", "", 0, -1)
	RecordRateFetch(ctx, "latest", "succeeded", 1, 0)
	RecordRateFetchSkip(ctx, "latest", "rate_limited")
	RecordRateCache(ctx, "hit")
	RecordObservationAge(ctx, 0)
	RecordObservationValidation(ctx, "accepted")
	RecordEventDelivery(ctx, "delivered", "", 0)
	RecordEventStage(ctx, "publish", "succeeded")
	RecordEventPublisherCount(ctx, 0)
	RecordDatabaseRetry(ctx, "purchases.buy", "deadlock")
	RecordIdempotency(ctx, "purchases.buy", "replayed")
	RecordAuthentication(ctx, "purchases.buy", "succeeded", "complete", 0)
	RecordRateLimit(ctx, "purchases.buy", "allowed", 0)
}

func TestOperationPanicCompletesExactlyOnce(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	created := newRecorder(tracerProvider, meterProvider)
	previous := active.Swap(created)
	t.Cleanup(func() {
		active.Store(previous)
		_ = tracerProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	})

	ctx := BeginOperation(context.Background(), "offers.list")
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		defer CompleteOperationOnPanic(ctx, "offers.list", func() int64 { return 2 })
		panic("operation panic")
	}()
	if recovered != "operation panic" {
		t.Fatalf("recovered panic = %#v", recovered)
	}
	CompleteOperation(ctx, "offers.list", "succeeded", "", 3)

	ended := spanRecorder.Ended()
	if len(ended) != 1 || ended[0].Status().Code != codes.Error {
		t.Fatalf("panic spans = %#v", ended)
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, item := range scope.Metrics {
			switch item.Name {
			case "bondexchange.operation.count":
				data, ok := item.Data.(metricdata.Sum[int64])
				if !ok {
					t.Fatalf("panic operation aggregation = %T", item.Data)
				}
				if len(data.DataPoints) != 1 || data.DataPoints[0].Value != 1 {
					t.Fatalf("panic operation count = %#v", data.DataPoints)
				}
			case "bondexchange.stream.offer.count":
				data, ok := item.Data.(metricdata.Histogram[int64])
				if !ok {
					t.Fatalf("panic stream aggregation = %T", item.Data)
				}
				if len(data.DataPoints) != 1 || data.DataPoints[0].Count != 1 || data.DataPoints[0].Sum != 2 {
					t.Fatalf("panic stream count = %#v", data.DataPoints)
				}
			}
		}
	}
}

func TestCorrelatingHandlerAddsOnlyTraceMetadata(t *testing.T) {
	var output bytes.Buffer
	base := slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelWarn})
	handler := NewLogHandler(base)
	if handler.Enabled(context.Background(), slog.LevelInfo) || !handler.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("handler did not preserve the wrapped level")
	}
	logger := slog.New(handler.WithGroup("component").WithAttrs([]slog.Attr{slog.String("name", "test")}))
	var traceID trace.TraceID
	traceID[0] = 1
	var spanID trace.SpanID
	spanID[0] = 2
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))
	logger.ErrorContext(ctx, "failed", "error_type", "safe")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	component, ok := record["component"].(map[string]any)
	if !ok || component["trace_id"] != traceID.String() || component["span_id"] != spanID.String() || component["trace_sampled"] != true {
		t.Fatalf("correlated record = %#v", record)
	}
	if strings.Contains(output.String(), "authorization") {
		t.Fatalf("unexpected credential field in %s", output.String())
	}

	output.Reset()
	slog.New(NewLogHandler(slog.NewJSONHandler(&output, nil))).InfoContext(context.Background(), "plain")
	if strings.Contains(output.String(), "trace_id") {
		t.Fatalf("invalid context gained trace metadata: %s", output.String())
	}
}

func TestSignalConfiguration(t *testing.T) {
	for _, variable := range []string{"OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		t.Setenv(variable, "")
	}
	enabled, err := signalEnabled("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if err != nil || enabled {
		t.Fatalf("default signal = %v, %v", enabled, err)
	}
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	enabled, err = signalEnabled("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if err != nil || !enabled {
		t.Fatalf("explicit OTLP signal = %v, %v", enabled, err)
	}
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	enabled, err = signalEnabled("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if err != nil || enabled {
		t.Fatalf("disabled signal = %v, %v", enabled, err)
	}
	t.Setenv("OTEL_TRACES_EXPORTER", "console")
	if _, err := signalEnabled("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); err == nil {
		t.Fatal("unsupported exporter was accepted")
	}
}

func TestSamplerAndMetricIntervalConfiguration(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER", "")
	if _, err := samplerFromEnv(); err != nil {
		t.Fatalf("default sampler: %v", err)
	}
	for _, name := range []string{"always_on", "always_off", "parentbased_always_on", "parentbased_always_off"} {
		t.Setenv("OTEL_TRACES_SAMPLER", name)
		if _, err := samplerFromEnv(); err != nil {
			t.Fatalf("sampler %q: %v", name, err)
		}
	}
	for _, name := range []string{"traceidratio", "parentbased_traceidratio"} {
		t.Setenv("OTEL_TRACES_SAMPLER", name)
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.25")
		if _, err := samplerFromEnv(); err != nil {
			t.Fatalf("sampler %q: %v", name, err)
		}
	}
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "2")
	if _, err := samplerFromEnv(); err == nil {
		t.Fatal("invalid ratio was accepted")
	}
	t.Setenv("OTEL_TRACES_SAMPLER", "custom")
	if _, err := samplerFromEnv(); err == nil {
		t.Fatal("unsupported sampler was accepted")
	}

	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "")
	if got, err := millisecondDurationFromEnv("OTEL_METRIC_EXPORT_INTERVAL", time.Minute); err != nil || got != time.Minute {
		t.Fatalf("default interval = %s, %v", got, err)
	}
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "2500")
	if got, err := millisecondDurationFromEnv("OTEL_METRIC_EXPORT_INTERVAL", time.Minute); err != nil || got != 2500*time.Millisecond {
		t.Fatalf("configured interval = %s, %v", got, err)
	}
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "zero")
	if _, err := millisecondDurationFromEnv("OTEL_METRIC_EXPORT_INTERVAL", time.Minute); err == nil {
		t.Fatal("invalid interval was accepted")
	}
}

func TestSetupExportsOTLPAndShutsDown(t *testing.T) {
	type receivedRequest struct {
		path string
		body []byte
	}
	var mutex sync.Mutex
	var requests []receivedRequest
	sink := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		mutex.Lock()
		requests = append(requests, receivedRequest{path: request.URL.Path, body: body})
		mutex.Unlock()
		response.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", sink.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=test")

	shutdown, err := Setup(context.Background(), Config{ServiceName: "bond-exchange-test", ServiceVersion: "test-version"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := BeginOperation(context.Background(), "health.read")
	CompleteOperation(ctx, "health.read", "succeeded", "", -1)
	RecordRateCache(ctx, "hit")
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}

	mutex.Lock()
	defer mutex.Unlock()
	var paths []string
	for _, request := range requests {
		paths = append(paths, request.path)
		if bytes.Contains(request.body, []byte("authorization")) || bytes.Contains(request.body, []byte("secret")) {
			t.Fatalf("exported sensitive telemetry to %s", request.path)
		}
	}
	if !slices.Contains(paths, "/v1/traces") || !slices.Contains(paths, "/v1/metrics") {
		t.Fatalf("OTLP requests = %v", paths)
	}
}

func TestSetupCanBeDisabledAndRejectsUnsupportedExporter(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_TRACES_EXPORTER", "unsupported-while-disabled")
	t.Setenv("OTEL_METRICS_EXPORTER", "unsupported-while-disabled")
	shutdown, err := Setup(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "zipkin")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	if _, err := Setup(context.Background(), Config{}); err == nil {
		t.Fatal("unsupported trace exporter was accepted")
	}
	if err := shutdownProviders(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	_ = newRecorder(sdktrace.NewTracerProvider(), noop.NewMeterProvider())
}

func TestSetupRejectsInvalidSignalConfiguration(t *testing.T) {
	configure := func(t *testing.T) {
		t.Helper()
		for _, variable := range []string{
			"OTEL_EXPORTER_OTLP_ENDPOINT",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
			"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL",
			"OTEL_EXPORTER_OTLP_PROTOCOL",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
			"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
			"OTEL_METRIC_EXPORT_INTERVAL",
			"OTEL_METRIC_EXPORT_TIMEOUT",
			"OTEL_METRICS_EXPORTER",
			"OTEL_RESOURCE_ATTRIBUTES",
			"OTEL_SDK_DISABLED",
			"OTEL_TRACES_EXPORTER",
			"OTEL_TRACES_SAMPLER",
			"OTEL_TRACES_SAMPLER_ARG",
		} {
			t.Setenv(variable, "")
		}
		t.Setenv("OTEL_SDK_DISABLED", "false")
	}
	tests := []struct {
		name string
		set  func(*testing.T)
	}{
		{
			name: "resource attributes",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
				t.Setenv("OTEL_METRICS_EXPORTER", "none")
				t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "missing-value")
			},
		},
		{
			name: "metrics exporter",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "none")
				t.Setenv("OTEL_METRICS_EXPORTER", "prometheus")
			},
		},
		{
			name: "sampler",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
				t.Setenv("OTEL_METRICS_EXPORTER", "none")
				t.Setenv("OTEL_TRACES_SAMPLER", "custom")
			},
		},
		{
			name: "trace endpoint",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
				t.Setenv("OTEL_METRICS_EXPORTER", "none")
				t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "://invalid")
			},
		},
		{
			name: "metric interval",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "none")
				t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
				t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "invalid")
			},
		},
		{
			name: "metric timeout",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "none")
				t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
				t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "0")
			},
		},
		{
			name: "metric endpoint",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "none")
				t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
				t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "://invalid")
			},
		},
		{
			name: "trace protocol",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
				t.Setenv("OTEL_METRICS_EXPORTER", "none")
				t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "grpc")
			},
		},
		{
			name: "metric protocol",
			set: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
				t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
				t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
				t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configure(t)
			test.set(t)
			if _, err := Setup(context.Background(), Config{}); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestSetupReportsPipelineAndRuntimeErrors(t *testing.T) {
	for _, variable := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_METRICS_EXPORTER",
		"OTEL_SDK_DISABLED",
		"OTEL_TRACES_EXPORTER",
	} {
		t.Setenv(variable, "")
	}
	t.Setenv("OTEL_SDK_DISABLED", "true")
	shutdown, err := Setup(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	otel.Handle(errors.New("pipeline unavailable"))
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://127.0.0.1:1")
	previousStart := startRuntimeMetrics
	startRuntimeMetrics = func(...otelruntime.Option) error {
		return errors.New("runtime instruments unavailable")
	}
	t.Cleanup(func() { startRuntimeMetrics = previousStart })
	if _, err := Setup(context.Background(), Config{}); err == nil || !strings.Contains(err.Error(), "start Go runtime metrics") {
		t.Fatalf("runtime setup error = %v", err)
	}
}

func TestMustInstrumentPanicsOnStaticDefinitionError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("instrument construction error did not panic")
		}
	}()
	_ = mustInstrument(0, errors.New("invalid static instrument"))
}
