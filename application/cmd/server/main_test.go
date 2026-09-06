package main

import (
	"context"
	"net"
	"testing"

	bondexchangev1 "github.com/fabianhjr/BondExchange/application/gen/go/bondexchange/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

type healthServer struct {
	bondexchangev1.UnimplementedBondExchangeServiceServer
}

func (healthServer) CheckHealth(context.Context, *bondexchangev1.CheckHealthRequest) (*bondexchangev1.CheckHealthResponse, error) {
	return &bondexchangev1.CheckHealthResponse{Status: "ok"}, nil
}

func TestGRPCServerCreatesPropagatedServerSpan(t *testing.T) {
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

	listener := bufconn.Listen(1024 * 1024)
	server := newGRPCServer(healthServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"traceparent", "00-01000000000000000000000000000000-0200000000000000-01",
	))
	if _, err := bondexchangev1.NewBondExchangeServiceClient(connection).CheckHealth(ctx, &bondexchangev1.CheckHealthRequest{}); err != nil {
		t.Fatal(err)
	}

	serverSpanFound := false
	for _, span := range spanRecorder.Ended() {
		if span.SpanKind() == trace.SpanKindServer {
			if span.SpanContext().TraceID().String() != "01000000000000000000000000000000" {
				t.Fatalf("gRPC trace ID = %s", span.SpanContext().TraceID())
			}
			serverSpanFound = true
		}
	}
	if !serverSpanFound {
		t.Fatal("gRPC server span was not recorded")
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	metricFound := false
	for _, scope := range collected.ScopeMetrics {
		for _, item := range scope.Metrics {
			if item.Name != "rpc.server.call.duration" {
				continue
			}
			histogram, ok := item.Data.(metricdata.Histogram[float64])
			if !ok || len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Count != 1 {
				t.Fatalf("gRPC duration metric = %#v", item.Data)
			}
			attributes := histogram.DataPoints[0].Attributes
			method, methodFound := attributes.Value("rpc.method")
			statusCode, statusFound := attributes.Value("rpc.response.status_code")
			if !methodFound || method.AsString() != "bondexchange.v1.BondExchangeService/CheckHealth" ||
				!statusFound || statusCode.AsString() != "OK" {
				t.Fatalf("gRPC duration attributes = %v", attributes.ToSlice())
			}
			metricFound = true
		}
	}
	if !metricFound {
		t.Fatal("gRPC server duration metric was not recorded")
	}
}
