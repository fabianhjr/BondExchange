package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/fabianhjr/BondExchange/internal/httpapi"
	postgresstore "github.com/fabianhjr/BondExchange/internal/postgres"
	"github.com/fabianhjr/BondExchange/internal/rpcapi"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	restAddress := os.Getenv("BOND_EXCHANGE_ADDRESS")
	if restAddress == "" {
		restAddress = ":8080"
	}
	grpcAddress := os.Getenv("BOND_EXCHANGE_GRPC_ADDRESS")
	if grpcAddress == "" {
		grpcAddress = ":9090"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	store := postgresstore.NewStore(pool)
	if err := store.Ping(ctx); err != nil {
		return err
	}
	service := exchange.NewService(store)
	apiServer := rpcapi.NewServer(service, store)
	httpHandler, err := httpapi.NewHandler(apiServer)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              restAddress,
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	grpcServer := grpc.NewServer()
	bondexchangev1.RegisterBondExchangeServiceServer(grpcServer, apiServer)
	reflection.Register(grpcServer)

	httpListener, err := net.Listen("tcp", restAddress)
	if err != nil {
		return fmt.Errorf("listen for REST: %w", err)
	}
	defer httpListener.Close()
	grpcListener, err := net.Listen("tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	defer grpcListener.Close()

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
