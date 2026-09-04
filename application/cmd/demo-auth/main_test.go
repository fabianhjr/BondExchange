package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/authn"
	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/go-jose/go-jose/v4"
	"google.golang.org/grpc/metadata"
)

type resolverStub struct{}

func (resolverStub) ResolvePrincipal(context.Context, string, string) (exchange.Principal, error) {
	return exchange.Principal{ID: "demo-buyer", ClientClass: exchange.ClientClassHuman}, nil
}

func TestDemoAssertionIsAcceptedByProductionAuthenticator(t *testing.T) {
	directory := t.TempDir()
	if err := initialize(directory); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	token, err := issueToken(filepath.Join(directory, "private.jwk"), "demo-buyer", exchange.OperationCheckHealth, "-", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filepath.Join(directory, "public.jwks")) //nolint:gosec // The path is inside t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	var keys jose.JSONWebKeySet
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatal(err)
	}
	authenticator, err := authn.NewJWTAuthenticator(authn.JWTConfig{
		Issuer: demoIssuer, Audience: demoAudience, Keys: keys, Now: func() time.Time { return now },
	}, resolverStub{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(authn.AuthorizationMetadata, "Bearer "+token))
	if _, err := authenticator.Authenticate(ctx, exchange.OperationCheckHealth, nil, false); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

func TestDemoAuthSupportsPendingEventRecovery(t *testing.T) {
	directory := t.TempDir()
	if err := initialize(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := issueToken(
		filepath.Join(directory, "private.jwk"),
		"demo-buyer",
		exchange.OperationPublishPendingEvents,
		"event-recovery-key-0001",
		`{"destination_id":"security"}`,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("issueToken() error = %v", err)
	}
}
