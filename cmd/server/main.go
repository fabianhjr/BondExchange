package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/internal/authn"
	"github.com/fabianhjr/BondExchange/internal/eventing"
	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/fabianhjr/BondExchange/internal/httpapi"
	postgresstore "github.com/fabianhjr/BondExchange/internal/postgres"
	"github.com/fabianhjr/BondExchange/internal/rpcapi"
	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true})))
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	restAddress := os.Getenv("BOND_EXCHANGE_ADDRESS")
	if restAddress == "" {
		restAddress = "127.0.0.1:8080"
	}
	grpcAddress := os.Getenv("BOND_EXCHANGE_GRPC_ADDRESS")
	if grpcAddress == "" {
		grpcAddress = "127.0.0.1:9090"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = 20
	poolConfig.MinConns = 2
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()

	store := postgresstore.NewStore(pool)
	if err := store.Ping(ctx); err != nil {
		return err
	}
	exchangeService := exchange.NewService(store)
	dispatcher, err := eventing.NewDispatcher(store, nil, 5*time.Second)
	if err != nil {
		return err
	}
	service := eventing.NewApplication(exchangeService, store, dispatcher)
	jwtAuthenticator, err := loadAuthenticator(store)
	if err != nil {
		return err
	}
	apiServer := rpcapi.NewServer(service, store, jwtAuthenticator)
	httpHandler, err := httpapi.NewHandler(apiServer)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              restAddress,
		Handler:           httpHandler,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(64*1024),
		grpc.MaxSendMsgSize(64*1024),
		grpc.MaxConcurrentStreams(128),
		grpc.UnaryInterceptor(recoverUnaryPanic),
		grpc.StreamInterceptor(recoverStreamPanic),
	)
	bondexchangev1.RegisterBondExchangeServiceServer(grpcServer, apiServer)

	listenConfig := net.ListenConfig{}
	httpListener, err := listenConfig.Listen(ctx, "tcp", restAddress)
	if err != nil {
		return fmt.Errorf("listen for REST: %w", err)
	}
	defer closeListener(httpListener, "REST")
	grpcListener, err := listenConfig.Listen(ctx, "tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	defer closeListener(grpcListener, "gRPC")

	serverErrors := make(chan error, 2)
	go func() {
		err := httpServer.Serve(httpListener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErrors <- err
	}()
	go func() {
		err := grpcServer.Serve(grpcListener)
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

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := shutdownServers(shutdownContext, httpServer, grpcServer)
	if serveErr != nil {
		return serveErr
	}
	return shutdownErr
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

func loadAuthenticator(resolver authn.PrincipalResolver) (*authn.JWTAuthenticator, error) {
	issuer := os.Getenv("BOND_EXCHANGE_ASSERTION_ISSUER")
	audience := os.Getenv("BOND_EXCHANGE_ASSERTION_AUDIENCE")
	keySetPath := os.Getenv("BOND_EXCHANGE_ASSERTION_JWKS_FILE")
	if issuer == "" || audience == "" || keySetPath == "" {
		return nil, errors.New("BOND_EXCHANGE_ASSERTION_ISSUER, BOND_EXCHANGE_ASSERTION_AUDIENCE, and BOND_EXCHANGE_ASSERTION_JWKS_FILE are required")
	}
	file, err := os.Open(keySetPath) //nolint:gosec // The operator explicitly configures the JWKS file to read.
	if err != nil {
		return nil, fmt.Errorf("open assertion verification keys: %w", err)
	}
	defer closeReadFile(file, "assertion verification key")
	encoded, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("read assertion verification keys: %w", err)
	}
	if len(encoded) > 1024*1024 {
		return nil, errors.New("assertion verification key file exceeds 1048576 bytes")
	}
	var keySet jose.JSONWebKeySet
	if err := json.Unmarshal(encoded, &keySet); err != nil {
		return nil, fmt.Errorf("decode assertion verification keys: %w", err)
	}
	return authn.NewJWTAuthenticator(authn.JWTConfig{
		Issuer:   issuer,
		Audience: audience,
		Keys:     keySet,
	}, resolver)
}

func closeListener(listener net.Listener, name string) {
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		slog.Warn("failed to close listener", "transport", name, "error", err)
	}
}

func closeReadFile(file *os.File, purpose string) {
	if err := file.Close(); err != nil {
		slog.Warn("failed to close read-only file", "purpose", purpose, "error", err)
	}
}

func shutdownServers(ctx context.Context, httpServer *http.Server, grpcServer *grpc.Server) error {
	httpErr := httpServer.Shutdown(ctx)
	grpcStopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcStopped)
	}()
	select {
	case <-grpcStopped:
	case <-ctx.Done():
		grpcServer.Stop()
		<-grpcStopped
	}
	return httpErr
}
