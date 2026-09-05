package serverruntime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func completeEnvironment() map[string]string {
	return map[string]string{
		DatabaseURLVariable:         "postgresql:///bond_exchange",
		AssertionIssuerVariable:     "https://issuer.test",
		AssertionAudienceVariable:   "bond-exchange",
		AssertionKeySetPathVariable: "/run/secrets/issuer.jwks",
		BanxicoSIETokenVariable:     strings.Repeat("t", 64),
	}
}

func lookupFrom(environment map[string]string) func(string) string {
	return func(name string) string { return environment[name] }
}

func TestLoadAppliesDefaultAddresses(t *testing.T) {
	config, err := Load(lookupFrom(completeEnvironment()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.BanxicoSIEToken != strings.Repeat("t", 64) {
		t.Errorf("BanxicoSIEToken was not read from the environment")
	}
	if config.RESTAddress != DefaultRESTAddress {
		t.Errorf("RESTAddress = %q, want %q", config.RESTAddress, DefaultRESTAddress)
	}
	if config.GRPCAddress != DefaultGRPCAddress {
		t.Errorf("GRPCAddress = %q, want %q", config.GRPCAddress, DefaultGRPCAddress)
	}
	if config.DatabaseURL != "postgresql:///bond_exchange" {
		t.Errorf("DatabaseURL = %q", config.DatabaseURL)
	}
	if config.AssertionIssuer != "https://issuer.test" {
		t.Errorf("AssertionIssuer = %q", config.AssertionIssuer)
	}
	if config.AssertionAudience != "bond-exchange" {
		t.Errorf("AssertionAudience = %q", config.AssertionAudience)
	}
	if config.AssertionKeySetPath != "/run/secrets/issuer.jwks" {
		t.Errorf("AssertionKeySetPath = %q", config.AssertionKeySetPath)
	}
}

func TestLoadKeepsSuppliedAddresses(t *testing.T) {
	environment := completeEnvironment()
	environment[RESTAddressVariable] = "0.0.0.0:18080"
	environment[GRPCAddressVariable] = "0.0.0.0:19090"
	config, err := Load(lookupFrom(environment))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.RESTAddress != "0.0.0.0:18080" {
		t.Errorf("RESTAddress = %q, want the supplied address", config.RESTAddress)
	}
	if config.GRPCAddress != "0.0.0.0:19090" {
		t.Errorf("GRPCAddress = %q, want the supplied address", config.GRPCAddress)
	}
}

func TestLoadRejectsIncompleteEnvironment(t *testing.T) {
	for _, missing := range []string{
		DatabaseURLVariable,
		AssertionIssuerVariable,
		AssertionAudienceVariable,
		AssertionKeySetPathVariable,
		BanxicoSIETokenVariable,
	} {
		environment := completeEnvironment()
		delete(environment, missing)
		config, err := Load(lookupFrom(environment))
		if err == nil {
			t.Fatalf("Load() without %s succeeded", missing)
		}
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("Load() error = %v, want it to name %s", err, missing)
		}
		if config != (Config{}) {
			t.Errorf("Load() returned %+v on failure, want the zero value", config)
		}
	}
}

func TestLoadNamesEveryMissingVariable(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{}))
	if err == nil {
		t.Fatal("Load() with an empty environment succeeded")
	}
	for _, variable := range []string{
		DatabaseURLVariable,
		AssertionIssuerVariable,
		AssertionAudienceVariable,
		AssertionKeySetPathVariable,
		BanxicoSIETokenVariable,
	} {
		if !strings.Contains(err.Error(), variable) {
			t.Errorf("Load() error = %v, want it to name %s", err, variable)
		}
	}
}

func TestLoadRejectsIdenticalAddresses(t *testing.T) {
	environment := completeEnvironment()
	environment[RESTAddressVariable] = "127.0.0.1:8080"
	environment[GRPCAddressVariable] = "127.0.0.1:8080"
	if _, err := Load(lookupFrom(environment)); err == nil {
		t.Fatal("Load() accepted identical REST and gRPC addresses")
	}
}

func TestLoadDefaultsToProcessEnvironment(t *testing.T) {
	t.Setenv(DatabaseURLVariable, "postgresql:///from-process")
	t.Setenv(AssertionIssuerVariable, "https://issuer.test")
	t.Setenv(AssertionAudienceVariable, "bond-exchange")
	t.Setenv(AssertionKeySetPathVariable, "/run/secrets/issuer.jwks")
	t.Setenv(BanxicoSIETokenVariable, strings.Repeat("t", 64))
	t.Setenv(RESTAddressVariable, "")
	t.Setenv(GRPCAddressVariable, "")
	config, err := Load(nil)
	if err != nil {
		t.Fatalf("Load(nil) error = %v", err)
	}
	if config.DatabaseURL != "postgresql:///from-process" {
		t.Errorf("DatabaseURL = %q, want the process environment value", config.DatabaseURL)
	}
}

const validKeySet = `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"one","use":"sig","alg":"EdDSA",` +
	`"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}]}`

func writeKeySet(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "issuer.jwks")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestReadKeySetAcceptsPublicKeys(t *testing.T) {
	keySet, err := ReadKeySet(writeKeySet(t, validKeySet))
	if err != nil {
		t.Fatalf("ReadKeySet() error = %v", err)
	}
	if len(keySet.Keys) != 1 {
		t.Fatalf("len(Keys) = %d, want 1", len(keySet.Keys))
	}
	if keySet.Keys[0].KeyID != "one" {
		t.Errorf("KeyID = %q, want %q", keySet.Keys[0].KeyID, "one")
	}
}

func TestReadKeySetRejectsMissingFile(t *testing.T) {
	if _, err := ReadKeySet(filepath.Join(t.TempDir(), "absent.jwks")); err == nil {
		t.Fatal("ReadKeySet() accepted a missing file")
	}
}

func TestReadKeySetRejectsUnreadableFile(t *testing.T) {
	// Opening a directory succeeds while reading it fails, which exercises the
	// read failure separately from the open failure.
	if _, err := ReadKeySet(t.TempDir()); err == nil {
		t.Fatal("ReadKeySet() accepted a directory")
	}
}

func TestReadKeySetRejectsMalformedJSON(t *testing.T) {
	if _, err := ReadKeySet(writeKeySet(t, "{not json")); err == nil {
		t.Fatal("ReadKeySet() accepted malformed JSON")
	}
}

func TestReadKeySetRejectsEmptyKeySet(t *testing.T) {
	if _, err := ReadKeySet(writeKeySet(t, `{"keys":[]}`)); err == nil {
		t.Fatal("ReadKeySet() accepted a key set with no keys")
	}
}

func TestReadKeySetSizeBoundary(t *testing.T) {
	// The padding keeps the document valid JSON while reaching an exact size,
	// so the accepted and rejected cases differ by exactly one byte.
	prefix := `{"keys":[],"padding":"`
	suffix := `"}`
	atLimit := prefix + strings.Repeat("p", MaxKeySetBytes-len(prefix)-len(suffix)) + suffix
	if len(atLimit) != MaxKeySetBytes {
		t.Fatalf("len(atLimit) = %d, want %d", len(atLimit), MaxKeySetBytes)
	}

	// At the limit the file is read and rejected for having no keys, which
	// proves the size check passed rather than short-circuited.
	_, err := ReadKeySet(writeKeySet(t, atLimit))
	if err == nil || !strings.Contains(err.Error(), "no keys") {
		t.Fatalf("ReadKeySet() at the size limit error = %v, want a no-keys failure", err)
	}

	overLimit := prefix + strings.Repeat("p", MaxKeySetBytes-len(prefix)-len(suffix)+1) + suffix
	if len(overLimit) != MaxKeySetBytes+1 {
		t.Fatalf("len(overLimit) = %d, want %d", len(overLimit), MaxKeySetBytes+1)
	}
	_, err = ReadKeySet(writeKeySet(t, overLimit))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadKeySet() past the size limit error = %v, want a size failure", err)
	}
}

func TestPoolConfigAppliesLimits(t *testing.T) {
	poolConfig, err := PoolConfig("postgresql:///bond_exchange")
	if err != nil {
		t.Fatalf("PoolConfig() error = %v", err)
	}
	if poolConfig.MaxConns != MaxPoolConnections {
		t.Errorf("MaxConns = %d, want %d", poolConfig.MaxConns, MaxPoolConnections)
	}
	if poolConfig.MinConns != MinPoolConnections {
		t.Errorf("MinConns = %d, want %d", poolConfig.MinConns, MinPoolConnections)
	}
	if poolConfig.MaxConnLifetime != MaxConnectionLifetime {
		t.Errorf("MaxConnLifetime = %v, want %v", poolConfig.MaxConnLifetime, MaxConnectionLifetime)
	}
	if poolConfig.MaxConnIdleTime != MaxConnectionIdleTime {
		t.Errorf("MaxConnIdleTime = %v, want %v", poolConfig.MaxConnIdleTime, MaxConnectionIdleTime)
	}
	if poolConfig.HealthCheckPeriod != PoolHealthCheckPeriod {
		t.Errorf("HealthCheckPeriod = %v, want %v", poolConfig.HealthCheckPeriod, PoolHealthCheckPeriod)
	}
}

func TestPoolConfigRejectsMalformedURL(t *testing.T) {
	if _, err := PoolConfig("://not a url"); err == nil {
		t.Fatal("PoolConfig() accepted a malformed database URL")
	}
}

func TestHTTPServerAppliesTimeouts(t *testing.T) {
	handler := http.NewServeMux()
	server := HTTPServer("127.0.0.1:0", handler)
	if server.Addr != "127.0.0.1:0" {
		t.Errorf("Addr = %q", server.Addr)
	}
	if server.Handler != handler {
		t.Error("Handler was not the supplied handler")
	}
	if server.ReadTimeout != ReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", server.ReadTimeout, ReadTimeout)
	}
	if server.ReadHeaderTimeout != ReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", server.ReadHeaderTimeout, ReadHeaderTimeout)
	}
	if server.IdleTimeout != IdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", server.IdleTimeout, IdleTimeout)
	}
}

func TestGRPCServerOptionsCoverEveryLimit(t *testing.T) {
	options := GRPCServerOptions(nil, nil)
	if len(options) != 5 {
		t.Fatalf("len(options) = %d, want 5", len(options))
	}
	for index, option := range options {
		if option == nil {
			t.Errorf("option %d is nil", index)
		}
	}
}

// listenLoopback binds a listener the way the production path does, through
// net.ListenConfig, so the tests exercise the same bind semantics.
func listenLoopback(t *testing.T, address string) net.Listener {
	t.Helper()
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", address)
	if err != nil {
		t.Fatalf("Listen(%q) error = %v", address, err)
	}
	return listener
}

func availableAddress(t *testing.T) string {
	t.Helper()
	listener := listenLoopback(t, "127.0.0.1:0")
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return address
}

func TestListenBindsBothTransports(t *testing.T) {
	config := Config{RESTAddress: availableAddress(t), GRPCAddress: availableAddress(t)}
	listeners, err := Listen(context.Background(), config)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if listeners.REST == nil || listeners.GRPC == nil {
		t.Fatal("Listen() returned a nil listener")
	}
	if err := listeners.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	// Closing twice must stay quiet: shutdown paths may close defensively.
	if err := listeners.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestListenRejectsUnavailableRESTAddress(t *testing.T) {
	held := listenLoopback(t, "127.0.0.1:0")
	defer func() { _ = held.Close() }()

	config := Config{RESTAddress: held.Addr().String(), GRPCAddress: availableAddress(t)}
	if _, err := Listen(context.Background(), config); err == nil {
		t.Fatal("Listen() succeeded on an occupied REST address")
	} else if !strings.Contains(err.Error(), "REST") {
		t.Errorf("Listen() error = %v, want it to name the REST listener", err)
	}
}

func TestListenReleasesRESTListenerWhenGRPCBindFails(t *testing.T) {
	restAddress := availableAddress(t)
	held := listenLoopback(t, "127.0.0.1:0")
	defer func() { _ = held.Close() }()

	config := Config{RESTAddress: restAddress, GRPCAddress: held.Addr().String()}
	if _, err := Listen(context.Background(), config); err == nil {
		t.Fatal("Listen() succeeded on an occupied gRPC address")
	} else if !strings.Contains(err.Error(), "gRPC") {
		t.Errorf("Listen() error = %v, want it to name the gRPC listener", err)
	}

	// The partial startup must not keep the REST port bound.
	var listenConfig net.ListenConfig
	reclaimed, err := listenConfig.Listen(t.Context(), "tcp", restAddress)
	if err != nil {
		t.Fatalf("REST address %s remained bound after a partial startup: %v", restAddress, err)
	}
	if err := reclaimed.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestListenersCloseReportsRESTFailure(t *testing.T) {
	listeners := Listeners{REST: failingListener{}, GRPC: nil}
	if err := listeners.Close(); !errors.Is(err, errListenerClose) {
		t.Errorf("Close() error = %v, want %v", err, errListenerClose)
	}
}

func TestListenersCloseReportsGRPCFailure(t *testing.T) {
	listeners := Listeners{REST: nil, GRPC: failingListener{}}
	if err := listeners.Close(); !errors.Is(err, errListenerClose) {
		t.Errorf("Close() error = %v, want %v", err, errListenerClose)
	}
}

func TestListenersCloseIgnoresAlreadyClosed(t *testing.T) {
	listeners := Listeners{REST: closedListener{}, GRPC: closedListener{}}
	if err := listeners.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil for an already-closed listener", err)
	}
}

var errListenerClose = errors.New("close failed")

type failingListener struct{ net.Listener }

func (failingListener) Close() error { return errListenerClose }

type closedListener struct{ net.Listener }

func (closedListener) Close() error { return net.ErrClosed }

// recordingGRPCServer stands in for *grpc.Server. release is fixed at
// construction and only ever closed, so the field itself is never mutated and
// GracefulStop can read it without a lock.
type recordingGRPCServer struct {
	mutex       sync.Mutex
	forced      bool
	release     chan struct{}
	releaseOnce sync.Once
}

func (server *recordingGRPCServer) GracefulStop() {
	if server.release != nil {
		<-server.release
	}
}

func (server *recordingGRPCServer) Stop() {
	server.mutex.Lock()
	server.forced = true
	server.mutex.Unlock()
	if server.release != nil {
		server.releaseOnce.Do(func() { close(server.release) })
	}
}

func (server *recordingGRPCServer) wasForced() bool {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return server.forced
}

func TestShutdownServersStopsGracefully(t *testing.T) {
	grpcServer := &recordingGRPCServer{}
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := ShutdownServers(ctx, HTTPServer("127.0.0.1:0", http.NewServeMux()), grpcServer); err != nil {
		t.Fatalf("ShutdownServers() error = %v", err)
	}
	if grpcServer.wasForced() {
		t.Error("ShutdownServers() forced a stop while the context was still valid")
	}
}

func TestShutdownServersForcesStopWhenTheBudgetExpires(t *testing.T) {
	grpcServer := &recordingGRPCServer{release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := ShutdownServers(ctx, HTTPServer("127.0.0.1:0", http.NewServeMux()), grpcServer); err != nil &&
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ShutdownServers() error = %v", err)
	}
	if !grpcServer.wasForced() {
		t.Error("ShutdownServers() did not force a stop after the shutdown budget expired")
	}
}
