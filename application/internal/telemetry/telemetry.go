package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const scopeName = "github.com/fabianhjr/BondExchange/application"

type Config struct {
	ServiceName    string
	ServiceVersion string
}

type Shutdown func(context.Context) error

type recorder struct {
	tracer              trace.Tracer
	operations          metric.Int64Counter
	operationDuration   metric.Float64Histogram
	rateFetches         metric.Int64Counter
	rateFetchDuration   metric.Float64Histogram
	rateCacheResults    metric.Int64Counter
	observationAge      metric.Float64Histogram
	eventDeliveries     metric.Int64Counter
	eventDuration       metric.Float64Histogram
	eventPublisherCount metric.Int64Gauge
	databaseRetries     metric.Int64Counter
	idempotencyResults  metric.Int64Counter
	streamedOfferCount  metric.Int64Histogram
}

type operationState struct {
	started time.Time
	span    trace.Span
}

type operationStateKey struct{}

var (
	active              atomic.Pointer[recorder]
	startRuntimeMetrics = otelruntime.Start
)

func Setup(ctx context.Context, config Config) (Shutdown, error) {
	cleanupContext := context.WithoutCancel(ctx)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Warn("OpenTelemetry pipeline error", "event", "telemetry.export", "error_type", fmt.Sprintf("%T", err))
	}))

	var tracesEnabled, metricsEnabled bool
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		var err error
		tracesEnabled, err = signalEnabled("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
		if err != nil {
			return nil, err
		}
		metricsEnabled, err = signalEnabled("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
		if err != nil {
			return nil, err
		}
	}

	var serviceResource *resource.Resource
	if tracesEnabled || metricsEnabled {
		if config.ServiceName == "" {
			config.ServiceName = "bond-exchange"
		}
		resourceOptions := []resource.Option{
			resource.WithAttributes(attribute.String("service.name", config.ServiceName)),
			resource.WithFromEnv(),
			resource.WithTelemetrySDK(),
			resource.WithProcess(),
			resource.WithOS(),
			resource.WithContainer(),
		}
		if config.ServiceVersion != "" {
			resourceOptions = append(resourceOptions, resource.WithAttributes(attribute.String("service.version", config.ServiceVersion)))
		}
		createdResource, err := resource.New(ctx, resourceOptions...)
		if err != nil {
			return nil, fmt.Errorf("create OpenTelemetry resource: %w", err)
		}
		serviceResource = createdResource
	}

	var tracerProvider *sdktrace.TracerProvider
	if tracesEnabled {
		if endpointErr := validateHTTPExporter("TRACES"); endpointErr != nil {
			return nil, endpointErr
		}
		sampler, samplerErr := samplerFromEnv()
		if samplerErr != nil {
			return nil, samplerErr
		}
		exporter, exportErr := otlptracehttp.New(ctx)
		if exportErr != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", exportErr)
		}
		tracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(serviceResource),
			sdktrace.WithSampler(sampler),
		)
		otel.SetTracerProvider(tracerProvider)
	}

	var meterProvider *sdkmetric.MeterProvider
	if metricsEnabled {
		if endpointErr := validateHTTPExporter("METRICS"); endpointErr != nil {
			if tracerProvider != nil {
				_ = tracerProvider.Shutdown(cleanupContext)
			}
			return nil, endpointErr
		}
		interval, intervalErr := millisecondDurationFromEnv("OTEL_METRIC_EXPORT_INTERVAL", time.Minute)
		if intervalErr != nil {
			if tracerProvider != nil {
				_ = tracerProvider.Shutdown(cleanupContext)
			}
			return nil, intervalErr
		}
		timeout, timeoutErr := millisecondDurationFromEnv("OTEL_METRIC_EXPORT_TIMEOUT", 30*time.Second)
		if timeoutErr != nil {
			if tracerProvider != nil {
				_ = tracerProvider.Shutdown(cleanupContext)
			}
			return nil, timeoutErr
		}
		exporter, exportErr := otlpmetrichttp.New(ctx)
		if exportErr != nil {
			if tracerProvider != nil {
				_ = tracerProvider.Shutdown(cleanupContext)
			}
			return nil, fmt.Errorf("create OTLP metric exporter: %w", exportErr)
		}
		meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval), sdkmetric.WithTimeout(timeout))),
			sdkmetric.WithResource(serviceResource),
		)
		otel.SetMeterProvider(meterProvider)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	created := newRecorder(otel.GetTracerProvider(), otel.GetMeterProvider())
	previous := active.Swap(created)
	if metricsEnabled {
		if err := startRuntimeMetrics(otelruntime.WithMeterProvider(meterProvider)); err != nil {
			active.CompareAndSwap(created, previous)
			shutdownErr := shutdownProviders(cleanupContext, meterProvider, tracerProvider)
			return nil, errors.Join(fmt.Errorf("start Go runtime metrics: %w", err), shutdownErr)
		}
	}

	return func(shutdownContext context.Context) error {
		active.CompareAndSwap(created, previous)
		return shutdownProviders(shutdownContext, meterProvider, tracerProvider)
	}, nil
}

func samplerFromEnv() (sdktrace.Sampler, error) {
	name := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER"))
	if name == "" {
		name = "parentbased_always_on"
	}
	ratio := func() (sdktrace.Sampler, error) {
		value := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG"))
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < 0 || parsed > 1 {
			return nil, errors.New("OTEL_TRACES_SAMPLER_ARG must be a number between zero and one")
		}
		return sdktrace.TraceIDRatioBased(parsed), nil
	}
	switch name {
	case "always_on":
		return sdktrace.AlwaysSample(), nil
	case "always_off":
		return sdktrace.NeverSample(), nil
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample()), nil
	case "traceidratio":
		return ratio()
	case "parentbased_traceidratio":
		configured, err := ratio()
		if err != nil {
			return nil, err
		}
		return sdktrace.ParentBased(configured), nil
	default:
		return nil, fmt.Errorf("unsupported OTEL_TRACES_SAMPLER %q", name)
	}
}

func millisecondDurationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || milliseconds <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer number of milliseconds", name)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func signalEnabled(exporterVariable string, endpointVariable string) (bool, error) {
	exporter := strings.TrimSpace(os.Getenv(exporterVariable))
	switch exporter {
	case "none":
		return false, nil
	case "", "otlp":
	default:
		return false, fmt.Errorf("%s supports only otlp or none", exporterVariable)
	}
	return exporter == "otlp" || strings.TrimSpace(os.Getenv(endpointVariable)) != "" || strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "", nil
}

func validateHTTPExporter(signal string) error {
	protocolVariable := "OTEL_EXPORTER_OTLP_" + signal + "_PROTOCOL"
	protocol := strings.TrimSpace(os.Getenv(protocolVariable))
	if protocol == "" {
		protocolVariable = "OTEL_EXPORTER_OTLP_PROTOCOL"
		protocol = strings.TrimSpace(os.Getenv(protocolVariable))
	}
	if protocol != "" && protocol != "http/protobuf" {
		return fmt.Errorf("%s supports only http/protobuf", protocolVariable)
	}

	endpointVariable := "OTEL_EXPORTER_OTLP_" + signal + "_ENDPOINT"
	endpoint := strings.TrimSpace(os.Getenv(endpointVariable))
	if endpoint == "" {
		endpointVariable = "OTEL_EXPORTER_OTLP_ENDPOINT"
		endpoint = strings.TrimSpace(os.Getenv(endpointVariable))
	}
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute HTTP or HTTPS URL", endpointVariable)
	}
	return nil
}

func shutdownProviders(ctx context.Context, meterProvider *sdkmetric.MeterProvider, tracerProvider *sdktrace.TracerProvider) error {
	var result error
	if meterProvider != nil {
		result = errors.Join(result, meterProvider.Shutdown(ctx))
	}
	if tracerProvider != nil {
		result = errors.Join(result, tracerProvider.Shutdown(ctx))
	}
	return result
}

func mustInstrument[T any](instrument T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("create static OpenTelemetry instrument: %v", err))
	}
	return instrument
}

func newRecorder(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) *recorder {
	meter := meterProvider.Meter(scopeName)
	operations := mustInstrument(meter.Int64Counter("bondexchange.operation.count", metric.WithDescription("Completed application operations"), metric.WithUnit("{operation}")))
	operationDuration := mustInstrument(meter.Float64Histogram("bondexchange.operation.duration", metric.WithDescription("Application operation duration"), metric.WithUnit("s")))
	rateFetches := mustInstrument(meter.Int64Counter("bondexchange.rate.fetch.count", metric.WithDescription("Exchange-rate provider fetches"), metric.WithUnit("{fetch}")))
	rateFetchDuration := mustInstrument(meter.Float64Histogram("bondexchange.rate.fetch.duration", metric.WithDescription("Exchange-rate provider fetch duration"), metric.WithUnit("s")))
	rateCacheResults := mustInstrument(meter.Int64Counter("bondexchange.rate.cache.result", metric.WithDescription("Exchange-rate cache resolution outcomes"), metric.WithUnit("{result}")))
	observationAge := mustInstrument(meter.Float64Histogram("bondexchange.rate.observation.age", metric.WithDescription("Age of an exchange-rate observation used by intake"), metric.WithUnit("s")))
	eventDeliveries := mustInstrument(meter.Int64Counter("bondexchange.event.delivery.count", metric.WithDescription("Integration-event delivery attempts"), metric.WithUnit("{attempt}")))
	eventDuration := mustInstrument(meter.Float64Histogram("bondexchange.event.delivery.duration", metric.WithDescription("Integration-event delivery attempt duration"), metric.WithUnit("s")))
	eventPublisherCount := mustInstrument(meter.Int64Gauge("bondexchange.event.publisher.configured", metric.WithDescription("Configured integration-event publishers"), metric.WithUnit("{publisher}")))
	databaseRetries := mustInstrument(meter.Int64Counter("bondexchange.database.transaction.retry", metric.WithDescription("Retryable PostgreSQL transaction failures"), metric.WithUnit("{retry}")))
	idempotencyResults := mustInstrument(meter.Int64Counter("bondexchange.idempotency.result", metric.WithDescription("Durable mutation idempotency outcomes"), metric.WithUnit("{result}")))
	streamedOfferCount := mustInstrument(meter.Int64Histogram("bondexchange.stream.offer.count", metric.WithDescription("Offers written by a completed active-offer stream"), metric.WithUnit("{offer}")))
	return &recorder{
		tracer: tracerProvider.Tracer(scopeName), operations: operations, operationDuration: operationDuration,
		rateFetches: rateFetches, rateFetchDuration: rateFetchDuration, rateCacheResults: rateCacheResults,
		observationAge: observationAge, eventDeliveries: eventDeliveries, eventDuration: eventDuration, eventPublisherCount: eventPublisherCount,
		databaseRetries: databaseRetries, idempotencyResults: idempotencyResults, streamedOfferCount: streamedOfferCount,
	}
}

func Start(ctx context.Context, name string, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	current := active.Load()
	if current == nil {
		return otel.Tracer(scopeName).Start(ctx, name, trace.WithAttributes(attributes...))
	}
	return current.tracer.Start(ctx, name, trace.WithAttributes(attributes...))
}

func End(span trace.Span, errorClass string) {
	if errorClass != "" {
		span.SetAttributes(attribute.String("error.type", errorClass))
		span.SetStatus(codes.Error, errorClass)
	}
	span.End()
}

func BeginOperation(ctx context.Context, operation string) context.Context {
	ctx, span := Start(ctx, "bondexchange.operation "+operation, attribute.String("bondexchange.operation", operation))
	return context.WithValue(ctx, operationStateKey{}, operationState{started: time.Now(), span: span})
}

func CompleteOperation(ctx context.Context, operation string, outcome string, errorCode string, streamedOffers int64) {
	state, found := ctx.Value(operationStateKey{}).(operationState)
	elapsed := time.Duration(0)
	if found {
		elapsed = time.Since(state.started)
		state.span.SetAttributes(attribute.String("outcome", outcome))
		if errorCode != "" {
			state.span.SetAttributes(attribute.String("error.type", errorCode))
		}
		if errorCode == "Internal" || errorCode == "Unavailable" || errorCode == "Unknown" || errorCode == "DeadlineExceeded" {
			state.span.SetStatus(codes.Error, errorCode)
		}
		state.span.End()
	}
	RecordOperation(ctx, operation, outcome, errorCode, elapsed, streamedOffers)
}

func RecordOperation(ctx context.Context, operation string, outcome string, errorCode string, elapsed time.Duration, streamedOffers int64) {
	current := active.Load()
	if current == nil {
		return
	}
	attributes := []attribute.KeyValue{attribute.String("bondexchange.operation", operation), attribute.String("outcome", outcome)}
	if errorCode != "" {
		attributes = append(attributes, attribute.String("error.type", errorCode))
	}
	options := metric.WithAttributes(attributes...)
	current.operations.Add(ctx, 1, options)
	current.operationDuration.Record(ctx, elapsed.Seconds(), options)
	if streamedOffers >= 0 {
		current.streamedOfferCount.Record(ctx, streamedOffers, options)
	}
}

func RecordRateFetch(ctx context.Context, kind string, outcome string, units int, elapsed time.Duration) {
	current := active.Load()
	if current == nil {
		return
	}
	options := metric.WithAttributes(attribute.String("fetch.kind", kind), attribute.String("outcome", outcome))
	current.rateFetches.Add(ctx, int64(units), options)
	current.rateFetchDuration.Record(ctx, elapsed.Seconds(), options)
}

func RecordRateCache(ctx context.Context, outcome string) {
	if current := active.Load(); current != nil {
		current.rateCacheResults.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	}
}

func RecordObservationAge(ctx context.Context, age time.Duration) {
	if current := active.Load(); current != nil {
		current.observationAge.Record(ctx, age.Seconds())
	}
}

func RecordEventDelivery(ctx context.Context, outcome string, errorClass string, elapsed time.Duration) {
	current := active.Load()
	if current == nil {
		return
	}
	attributes := []attribute.KeyValue{attribute.String("outcome", outcome)}
	if errorClass != "" {
		attributes = append(attributes, attribute.String("error.type", errorClass))
	}
	options := metric.WithAttributes(attributes...)
	current.eventDeliveries.Add(ctx, 1, options)
	current.eventDuration.Record(ctx, elapsed.Seconds(), options)
}

func RecordEventPublisherCount(ctx context.Context, count int) {
	if current := active.Load(); current != nil {
		current.eventPublisherCount.Record(ctx, int64(count))
	}
}

func RecordDatabaseRetry(ctx context.Context, operation string) {
	if current := active.Load(); current != nil {
		current.databaseRetries.Add(ctx, 1, metric.WithAttributes(attribute.String("db.operation.name", operation)))
	}
}

func RecordIdempotency(ctx context.Context, operation string, outcome string) {
	if current := active.Load(); current != nil {
		current.idempotencyResults.Add(ctx, 1, metric.WithAttributes(
			attribute.String("bondexchange.operation", operation),
			attribute.String("outcome", outcome),
		))
	}
}

type CorrelatingHandler struct {
	next slog.Handler
}

func NewLogHandler(next slog.Handler) *CorrelatingHandler {
	return &CorrelatingHandler{next: next}
}

func (handler *CorrelatingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *CorrelatingHandler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		record.AddAttrs(
			slog.String("trace_id", span.TraceID().String()),
			slog.String("span_id", span.SpanID().String()),
			slog.Bool("trace_sampled", span.IsSampled()),
		)
	}
	return handler.next.Handle(ctx, record)
}

func (handler *CorrelatingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	return &CorrelatingHandler{next: handler.next.WithAttrs(attributes)}
}

func (handler *CorrelatingHandler) WithGroup(name string) slog.Handler {
	return &CorrelatingHandler{next: handler.next.WithGroup(name)}
}
