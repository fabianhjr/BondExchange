package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/exaring/otelpgx"
	bondexchangev1 "github.com/fabianhjr/BondExchange/application/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/application/internal/authn"
	"github.com/fabianhjr/BondExchange/application/internal/eventing"
	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/fabianhjr/BondExchange/application/internal/exchangerates"
	"github.com/fabianhjr/BondExchange/application/internal/httpapi"
	"github.com/fabianhjr/BondExchange/application/internal/offerintake"
	postgresstore "github.com/fabianhjr/BondExchange/application/internal/postgres"
	"github.com/fabianhjr/BondExchange/application/internal/rpcapi"
	"github.com/fabianhjr/BondExchange/application/internal/serverruntime"
	"github.com/fabianhjr/BondExchange/application/internal/sie"
	"github.com/fabianhjr/BondExchange/application/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	slog.SetDefault(slog.New(telemetry.NewLogHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true}))))
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	config, err := serverruntime.Load(nil)
	if err != nil {
		return err
	}
	keySet, err := serverruntime.ReadKeySet(config.AssertionKeySetPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTelemetry, err := telemetry.Setup(ctx, telemetry.Config{})
	if err != nil {
		return err
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, shutdownTelemetry(shutdownContext))
	}()
	poolConfig, err := serverruntime.PoolConfig(config.DatabaseURL)
	if err != nil {
		return err
	}
	poolConfig.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := otelpgx.RecordStats(pool); err != nil {
		return fmt.Errorf("record PostgreSQL pool metrics: %w", err)
	}

	store := postgresstore.NewStore(pool)
	if err := store.Ping(ctx); err != nil {
		return err
	}
	exchangeService := exchange.NewService(store)
	sieClient, err := sie.NewClient(sie.Config{Token: config.BanxicoSIEToken})
	if err != nil {
		return err
	}
	rateService, err := exchangerates.NewService(sieClient, store, exchangerates.Config{})
	if err != nil {
		return err
	}
	intakeService, err := offerintake.NewService(exchangeService, rateService, store, offerintake.Config{})
	if err != nil {
		return err
	}
	dispatcher, err := eventing.NewDispatcher(store, nil, 5*time.Second)
	if err != nil {
		return err
	}
	service := eventing.NewApplication(intakeService, store, dispatcher)
	jwtAuthenticator, err := authn.NewJWTAuthenticator(authn.JWTConfig{
		Issuer:   config.AssertionIssuer,
		Audience: config.AssertionAudience,
		Keys:     keySet,
	}, store)
	if err != nil {
		return err
	}
	apiServer := rpcapi.NewServer(service, store, jwtAuthenticator)
	httpHandler, err := httpapi.NewHandler(apiServer)
	if err != nil {
		return err
	}
	httpServer := serverruntime.HTTPServer(config.RESTAddress, httpHandler)
	grpcServer := newGRPCServer(apiServer)

	listeners, err := serverruntime.Listen(ctx, config)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := listeners.Close(); closeErr != nil {
			slog.Warn("failed to close listeners", "error", closeErr)
		}
	}()

	serverErrors := make(chan error, 2)
	go func() {
		err := httpServer.Serve(listeners.REST)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErrors <- err
	}()
	go func() {
		err := grpcServer.Serve(listeners.GRPC)
		if errors.Is(err, grpc.ErrServerStopped) {
			err = nil
		}
		serverErrors <- err
	}()

	var serveErr error
	select {
	case serveErr = <-serverErrors:
	case <-ctx.Done():
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), serverruntime.ShutdownTimeout)
	defer cancel()
	shutdownErr := serverruntime.ShutdownServers(shutdownContext, httpServer, grpcServer)
	if serveErr != nil {
		return serveErr
	}
	return shutdownErr
}

func newGRPCServer(apiServer bondexchangev1.BondExchangeServiceServer) *grpc.Server {
	options := serverruntime.GRPCServerOptions(recoverUnaryPanic, recoverStreamPanic)
	options = append(options, grpc.StatsHandler(otelgrpc.NewServerHandler()))
	server := grpc.NewServer(options...)
	bondexchangev1.RegisterBondExchangeServiceServer(server, apiServer)
	return server
}

func recoverUnaryPanic(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (response any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(ctx, "recovered gRPC panic", "method", info.FullMethod)
			response = nil
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, request)
}

func recoverStreamPanic(
	service any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(stream.Context(), "recovered gRPC stream panic", "method", info.FullMethod)
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(service, stream)
}
