// Package serverruntime holds the composition decisions that the server
// command used to make inline: environment parsing, verification-key loading,
// connection-pool and transport limits, listener creation, and shutdown.
//
// These are the paths that fail during deployment rather than during a
// request, so they belong in a package the coverage and mutation gates
// measure. The command package keeps only wiring.
package serverruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
)

// Environment variables the server reads.
const (
	DatabaseURLVariable         = "DATABASE_URL"
	RESTAddressVariable         = "BOND_EXCHANGE_ADDRESS"
	GRPCAddressVariable         = "BOND_EXCHANGE_GRPC_ADDRESS"
	AssertionIssuerVariable     = "BOND_EXCHANGE_ASSERTION_ISSUER"
	AssertionAudienceVariable   = "BOND_EXCHANGE_ASSERTION_AUDIENCE"
	AssertionKeySetPathVariable = "BOND_EXCHANGE_ASSERTION_JWKS_FILE"
	BanxicoSIETokenVariable     = "BANXICO_SIE_TOKEN" //nolint:gosec // This names the variable that carries the token; it is not a credential.
)

// Default listener addresses. Both bind to loopback: exposing the service is a
// deployment decision that this repository deliberately does not make.
const (
	DefaultRESTAddress = "127.0.0.1:8080"
	DefaultGRPCAddress = "127.0.0.1:9090"
)

// Resource ceilings. They are local limits on this process, not a rate-limit
// or capacity control; those remain deployment concerns.
const (
	MaxKeySetBytes        = 1024 * 1024
	MaxPoolConnections    = 20
	MinPoolConnections    = 2
	MaxConnectionLifetime = 30 * time.Minute
	MaxConnectionIdleTime = 5 * time.Minute
	PoolHealthCheckPeriod = 30 * time.Second
	MaxMessageBytes       = 64 * 1024
	MaxConcurrentStreams  = 128
	ReadTimeout           = 10 * time.Second
	ReadHeaderTimeout     = 5 * time.Second
	IdleTimeout           = 60 * time.Second
	ShutdownTimeout       = 10 * time.Second
)

// Config is the validated deployment configuration of one server process.
type Config struct {
	DatabaseURL         string
	RESTAddress         string
	GRPCAddress         string
	AssertionIssuer     string
	AssertionAudience   string
	AssertionKeySetPath string

	// BanxicoSIEToken is a provider credential. It is read here so that a
	// missing credential fails startup with the other configuration rather
	// than inside an adapter, and it must never be logged or persisted; the
	// SIE client validates its format and is its only consumer.
	BanxicoSIEToken string
}

// Load reads configuration through lookup, which is os.Getenv when nil. It
// fails closed: an incomplete environment never yields a partially configured
// server.
func Load(lookup func(string) string) (Config, error) {
	if lookup == nil {
		lookup = os.Getenv
	}
	config := Config{
		DatabaseURL:         lookup(DatabaseURLVariable),
		RESTAddress:         lookup(RESTAddressVariable),
		GRPCAddress:         lookup(GRPCAddressVariable),
		AssertionIssuer:     lookup(AssertionIssuerVariable),
		AssertionAudience:   lookup(AssertionAudienceVariable),
		AssertionKeySetPath: lookup(AssertionKeySetPathVariable),
		BanxicoSIEToken:     lookup(BanxicoSIETokenVariable),
	}
	if config.RESTAddress == "" {
		config.RESTAddress = DefaultRESTAddress
	}
	if config.GRPCAddress == "" {
		config.GRPCAddress = DefaultGRPCAddress
	}

	required := []struct {
		variable string
		value    string
	}{
		{DatabaseURLVariable, config.DatabaseURL},
		{AssertionIssuerVariable, config.AssertionIssuer},
		{AssertionAudienceVariable, config.AssertionAudience},
		{AssertionKeySetPathVariable, config.AssertionKeySetPath},
		{BanxicoSIETokenVariable, config.BanxicoSIEToken},
	}
	missing := make([]string, 0, len(required))
	for _, candidate := range required {
		if candidate.value == "" {
			missing = append(missing, candidate.variable)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	if config.RESTAddress == config.GRPCAddress {
		return Config{}, fmt.Errorf(
			"%s and %s must differ, both are %q",
			RESTAddressVariable,
			GRPCAddressVariable,
			config.RESTAddress,
		)
	}
	return config, nil
}

// ReadKeySet loads the assertion verification keys. It reads a bounded amount
// and requires at least one key, so a truncated or empty file fails startup
// rather than producing a server that rejects every assertion.
func ReadKeySet(path string) (jose.JSONWebKeySet, error) {
	var keySet jose.JSONWebKeySet
	file, err := os.Open(path) //nolint:gosec // The operator explicitly configures the JWKS file to read.
	if err != nil {
		return keySet, fmt.Errorf("open assertion verification keys: %w", err)
	}
	// The file is opened read-only, so a close failure cannot lose data and
	// must not mask the parse result.
	defer func() { _ = file.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(file, MaxKeySetBytes+1))
	if err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("read assertion verification keys: %w", err)
	}
	if len(encoded) > MaxKeySetBytes {
		return jose.JSONWebKeySet{}, fmt.Errorf(
			"assertion verification key file exceeds %d bytes",
			MaxKeySetBytes,
		)
	}
	if err := json.Unmarshal(encoded, &keySet); err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("decode assertion verification keys: %w", err)
	}
	if len(keySet.Keys) == 0 {
		return jose.JSONWebKeySet{}, errors.New("assertion verification key set contains no keys")
	}
	return keySet, nil
}

// PoolConfig applies this process's connection ceilings to a parsed database
// URL.
func PoolConfig(databaseURL string) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = MaxPoolConnections
	poolConfig.MinConns = MinPoolConnections
	poolConfig.MaxConnLifetime = MaxConnectionLifetime
	poolConfig.MaxConnIdleTime = MaxConnectionIdleTime
	poolConfig.HealthCheckPeriod = PoolHealthCheckPeriod
	return poolConfig, nil
}

// HTTPServer builds the REST server with this process's timeouts.
func HTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadTimeout:       ReadTimeout,
		ReadHeaderTimeout: ReadHeaderTimeout,
		IdleTimeout:       IdleTimeout,
	}
}

// GRPCServerOptions returns the native gRPC server limits and interceptors.
func GRPCServerOptions(
	unary grpc.UnaryServerInterceptor,
	stream grpc.StreamServerInterceptor,
) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(MaxMessageBytes),
		grpc.MaxSendMsgSize(MaxMessageBytes),
		grpc.MaxConcurrentStreams(MaxConcurrentStreams),
		grpc.UnaryInterceptor(unary),
		grpc.StreamInterceptor(stream),
	}
}

// Listeners holds the two bound listeners of one server process.
type Listeners struct {
	REST net.Listener
	GRPC net.Listener
}

// Close releases both listeners, reporting the first failure.
func (listeners Listeners) Close() error {
	restErr := closeListener(listeners.REST)
	grpcErr := closeListener(listeners.GRPC)
	if restErr != nil {
		return restErr
	}
	return grpcErr
}

// Listen binds both listeners. If the second bind fails, the first is released
// before returning, so a partial startup leaves no port held.
func Listen(ctx context.Context, config Config) (Listeners, error) {
	var listenConfig net.ListenConfig
	restListener, err := listenConfig.Listen(ctx, "tcp", config.RESTAddress)
	if err != nil {
		return Listeners{}, fmt.Errorf("listen for REST: %w", err)
	}
	grpcListener, err := listenConfig.Listen(ctx, "tcp", config.GRPCAddress)
	if err != nil {
		// Join rather than branch: a failure to release the REST listener is
		// worth reporting but must not hide why startup failed, and one
		// statement keeps this path exercised by the bind-failure test.
		return Listeners{}, errors.Join(
			fmt.Errorf("listen for gRPC: %w", err),
			closeListener(restListener),
		)
	}
	return Listeners{REST: restListener, GRPC: grpcListener}, nil
}

func closeListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// GRPCServer is the part of *grpc.Server that shutdown needs, so that
// forced-stop behavior can be exercised without a live server.
type GRPCServer interface {
	GracefulStop()
	Stop()
}

// ShutdownServers drains both transports. The gRPC server is forced to stop
// when the context expires first, so a stuck stream cannot hold the process
// open past the shutdown budget.
func ShutdownServers(ctx context.Context, httpServer *http.Server, grpcServer GRPCServer) error {
	httpErr := httpServer.Shutdown(ctx)
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
		grpcServer.Stop()
		<-stopped
	}
	return httpErr
}
