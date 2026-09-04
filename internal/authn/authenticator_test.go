package authn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"google.golang.org/grpc/metadata"
)

type resolverStub struct {
	principal exchange.Principal
	err       error
}

func (resolver resolverStub) ResolvePrincipal(context.Context, string, string) (exchange.Principal, error) {
	return resolver.principal, resolver.err
}

func TestJWTAuthenticatorBindsPrincipalOperationRequestAndIdempotencyKey(t *testing.T) {
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	authenticator, privateKey := newTestAuthenticator(t, now)
	request := []byte("canonical protobuf request")
	key := "idempotency-key-123"
	token := signAssertion(t, privateKey, standardClaims(now), operationClaims{
		ClientID: "automated-client-1",
		AuthorizationDetails: []authorizationDetails{{
			Type:           AuthorizationType,
			Actions:        []string{exchange.OperationBuy},
			RequestSHA256:  requestDigest(request),
			IdempotencyKey: key,
		}},
	})
	ctx := incomingContext(token, key)

	result, err := authenticator.Authenticate(ctx, exchange.OperationBuy, request, true)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.AccessContext.Principal.ID != "principal-1" ||
		result.AccessContext.Principal.ClientID != "automated-client-1" ||
		result.AccessContext.Operation != exchange.OperationBuy ||
		result.IdempotencyKey != key {
		t.Fatalf("Authenticate() = %#v", result)
	}
	if result.AccessContext.RequestDigest != sha256.Sum256(request) || result.AccessContext.AssertionDigest == [sha256.Size]byte{} {
		t.Fatalf("operation digests were not captured: %#v", result.AccessContext)
	}
}

func TestJWTAuthenticatorRejectsAssertionReuseOutsideExactOperation(t *testing.T) {
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	authenticator, privateKey := newTestAuthenticator(t, now)
	request := []byte("request-a")
	key := "idempotency-key-123"
	token := signAssertion(t, privateKey, standardClaims(now), operationClaims{
		ClientID: "human-client-1",
		AuthorizationDetails: []authorizationDetails{{
			Type:           AuthorizationType,
			Actions:        []string{exchange.OperationBuy},
			RequestSHA256:  requestDigest(request),
			IdempotencyKey: key,
		}},
	})

	for _, test := range []struct {
		name      string
		operation string
		request   []byte
		key       string
	}{
		{name: "other operation", operation: exchange.OperationCreateSaleOffer, request: request, key: key},
		{name: "other request", operation: exchange.OperationBuy, request: []byte("request-b"), key: key},
		{name: "other idempotency key", operation: exchange.OperationBuy, request: request, key: "idempotency-key-456"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := authenticator.Authenticate(incomingContext(token, test.key), test.operation, test.request, true)
			if !errors.Is(err, exchange.ErrUnauthenticated) {
				t.Fatalf("Authenticate() error = %v", err)
			}
		})
	}
}

func TestJWTAuthenticatorRejectsInvalidEnvelopeAndClaims(t *testing.T) {
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	authenticator, privateKey := newTestAuthenticator(t, now)
	request := []byte("request")
	key := "idempotency-key-123"
	validOperation := operationClaims{
		ClientID: "client-1",
		AuthorizationDetails: []authorizationDetails{{
			Type: AuthorizationType, Actions: []string{exchange.OperationBuy},
			RequestSHA256: requestDigest(request), IdempotencyKey: key,
		}},
	}

	tests := []struct {
		name     string
		claims   jwt.Claims
		metadata metadata.MD
	}{
		{name: "expired", claims: jwt.Claims{Issuer: "https://issuer.test", Subject: "subject-1", Audience: jwt.Audience{"bond-exchange"}, ID: "assertion-1", IssuedAt: jwt.NewNumericDate(now.Add(-10 * time.Minute)), Expiry: jwt.NewNumericDate(now.Add(-5 * time.Minute))}},
		{name: "wrong audience", claims: func() jwt.Claims { value := standardClaims(now); value.Audience = jwt.Audience{"other"}; return value }()},
		{name: "missing bearer", claims: standardClaims(now), metadata: metadata.Pairs(IdempotencyMetadata, key)},
		{name: "multiple bearer", claims: standardClaims(now), metadata: metadata.MD{AuthorizationMetadata: {"Bearer placeholder", "Bearer duplicate"}, IdempotencyMetadata: {key}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := signAssertion(t, privateKey, test.claims, validOperation)
			values := test.metadata
			if values == nil {
				values = metadata.Pairs(AuthorizationMetadata, "Bearer "+token, IdempotencyMetadata, key)
			} else if authorization := values.Get(AuthorizationMetadata); len(authorization) > 0 && authorization[0] == "Bearer placeholder" {
				values.Set(AuthorizationMetadata, "Bearer "+token, "Bearer "+token)
			}
			_, err := authenticator.Authenticate(metadata.NewIncomingContext(context.Background(), values), exchange.OperationBuy, request, true)
			if !errors.Is(err, exchange.ErrUnauthenticated) {
				t.Fatalf("Authenticate() error = %v", err)
			}
		})
	}
}

func TestReadOperationRejectsIdempotencyMetadata(t *testing.T) {
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	authenticator, privateKey := newTestAuthenticator(t, now)
	request := []byte("read request")
	token := signAssertion(t, privateKey, standardClaims(now), operationClaims{
		ClientID: "client-1",
		AuthorizationDetails: []authorizationDetails{{
			Type: AuthorizationType, Actions: []string{exchange.OperationListBondSeries}, RequestSHA256: requestDigest(request),
		}},
	})
	_, err := authenticator.Authenticate(incomingContext(token, "unexpected-key-123"), exchange.OperationListBondSeries, request, false)
	if !errors.Is(err, exchange.ErrInvalidIdempotencyKey) {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

func TestReadOperationAuthenticationSucceedsWithoutIdempotencyMetadata(t *testing.T) {
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	authenticator, privateKey := newTestAuthenticator(t, now)
	request := []byte("read request")
	token := signAssertion(t, privateKey, standardClaims(now), operationClaims{
		ClientID: "client-1",
		AuthorizationDetails: []authorizationDetails{{
			Type: AuthorizationType, Actions: []string{exchange.OperationListBondSeries}, RequestSHA256: requestDigest(request),
		}},
	})
	if _, err := authenticator.Authenticate(incomingContext(token, ""), exchange.OperationListBondSeries, request, false); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

func TestJWTAuthenticatorRejectsMalformedClaims(t *testing.T) {
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	authenticator, privateKey := newTestAuthenticator(t, now)
	request := []byte("request")
	key := "idempotency-key-123"
	validDetail := authorizationDetails{
		Type: AuthorizationType, Actions: []string{exchange.OperationBuy},
		RequestSHA256: requestDigest(request), IdempotencyKey: key,
	}
	tests := []struct {
		name      string
		standard  jwt.Claims
		operation operationClaims
	}{
		{name: "missing subject", standard: func() jwt.Claims { value := standardClaims(now); value.Subject = ""; return value }(), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{validDetail}}},
		{name: "missing assertion ID", standard: func() jwt.Claims { value := standardClaims(now); value.ID = ""; return value }(), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{validDetail}}},
		{name: "missing issued at", standard: func() jwt.Claims { value := standardClaims(now); value.IssuedAt = nil; return value }(), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{validDetail}}},
		{name: "missing expiry", standard: func() jwt.Claims { value := standardClaims(now); value.Expiry = nil; return value }(), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{validDetail}}},
		{name: "future issued at", standard: func() jwt.Claims {
			value := standardClaims(now)
			value.IssuedAt = jwt.NewNumericDate(now.Add(time.Minute))
			value.Expiry = jwt.NewNumericDate(now.Add(2 * time.Minute))
			return value
		}(), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{validDetail}}},
		{name: "excessive lifetime", standard: func() jwt.Claims {
			value := standardClaims(now)
			value.Expiry = jwt.NewNumericDate(value.IssuedAt.Time().Add(6 * time.Minute))
			return value
		}(), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{validDetail}}},
		{name: "missing client", standard: standardClaims(now), operation: operationClaims{AuthorizationDetails: []authorizationDetails{validDetail}}},
		{name: "missing authorization detail", standard: standardClaims(now), operation: operationClaims{ClientID: "client"}},
		{name: "wrong detail type", standard: standardClaims(now), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{func() authorizationDetails { value := validDetail; value.Type = "other"; return value }()}}},
		{name: "multiple actions", standard: standardClaims(now), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{func() authorizationDetails {
			value := validDetail
			value.Actions = []string{exchange.OperationBuy, exchange.OperationCreateSaleOffer}
			return value
		}()}}},
		{name: "wrong digest", standard: standardClaims(now), operation: operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{func() authorizationDetails {
			value := validDetail
			value.RequestSHA256 = requestDigest([]byte("other"))
			return value
		}()}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := signAssertion(t, privateKey, test.standard, test.operation)
			if _, err := authenticator.Authenticate(incomingContext(token, key), exchange.OperationBuy, request, true); !errors.Is(err, exchange.ErrUnauthenticated) {
				t.Fatalf("Authenticate() error = %v", err)
			}
		})
	}
}

func TestJWTAuthenticatorConfigurationValidation(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	validKey := jose.JSONWebKey{Key: publicKey, KeyID: "key-1", Use: "sig", Algorithm: string(jose.EdDSA)}
	valid := JWTConfig{Issuer: "issuer", Audience: "audience", Keys: jose.JSONWebKeySet{Keys: []jose.JSONWebKey{validKey}}}
	tests := []struct {
		name     string
		config   JWTConfig
		resolver PrincipalResolver
	}{
		{name: "missing issuer", config: func() JWTConfig { value := valid; value.Issuer = ""; return value }(), resolver: resolverStub{}},
		{name: "missing resolver", config: valid},
		{name: "invalid clock skew", config: func() JWTConfig { value := valid; value.ClockSkew = 2 * time.Minute; return value }(), resolver: resolverStub{}},
		{name: "private verification key", config: func() JWTConfig { value := valid; value.Keys.Keys[0].Key = privateKey; return value }(), resolver: resolverStub{}},
		{name: "wrong key use", config: func() JWTConfig { value := valid; value.Keys.Keys[0].Use = "enc"; return value }(), resolver: resolverStub{}},
		{name: "unsupported key algorithm", config: func() JWTConfig { value := valid; value.Keys.Keys[0].Algorithm = string(jose.RS256); return value }(), resolver: resolverStub{}},
		{name: "duplicate key ID", config: func() JWTConfig { value := valid; value.Keys.Keys = append(value.Keys.Keys, validKey); return value }(), resolver: resolverStub{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewJWTAuthenticator(test.config, test.resolver); err == nil {
				t.Fatal("NewJWTAuthenticator() succeeded")
			}
		})
	}
}

func TestJWTAuthenticatorRejectsResolverFailureAndMalformedBearer(t *testing.T) {
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	authenticator, privateKey := newTestAuthenticator(t, now)
	request := []byte("request")
	key := "idempotency-key-123"
	operation := operationClaims{ClientID: "client", AuthorizationDetails: []authorizationDetails{{
		Type: AuthorizationType, Actions: []string{exchange.OperationBuy}, RequestSHA256: requestDigest(request), IdempotencyKey: key,
	}}}
	token := signAssertion(t, privateKey, standardClaims(now), operation)
	for _, header := range []string{"Basic " + token, "Bearer", "Bearer " + token + " extra", "not-a-jwt"} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(AuthorizationMetadata, header, IdempotencyMetadata, key))
		if _, err := authenticator.Authenticate(ctx, exchange.OperationBuy, request, true); !errors.Is(err, exchange.ErrUnauthenticated) {
			t.Fatalf("header %q error = %v", header, err)
		}
	}
	authenticator.resolver = resolverStub{err: errors.New("lookup failed")}
	if _, err := authenticator.Authenticate(incomingContext(token, key), exchange.OperationBuy, request, true); !errors.Is(err, exchange.ErrUnauthenticated) {
		t.Fatalf("resolver error = %v", err)
	}
}

func newTestAuthenticator(t *testing.T, now time.Time) (*JWTAuthenticator, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewJWTAuthenticator(JWTConfig{
		Issuer: "https://issuer.test", Audience: "bond-exchange", Now: func() time.Time { return now },
		Keys:       jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: publicKey, KeyID: "test-key", Use: "sig", Algorithm: string(jose.EdDSA)}}},
		Algorithms: []jose.SignatureAlgorithm{jose.EdDSA},
	}, resolverStub{principal: exchange.Principal{ID: "principal-1", ClientClass: exchange.ClientClassAutomated}})
	if err != nil {
		t.Fatal(err)
	}
	return authenticator, privateKey
}

func standardClaims(now time.Time) jwt.Claims {
	return jwt.Claims{
		Issuer: "https://issuer.test", Subject: "subject-1", Audience: jwt.Audience{"bond-exchange"}, ID: "assertion-1",
		IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), Expiry: jwt.NewNumericDate(now.Add(time.Minute)),
	}
}

func signAssertion(t *testing.T, key ed25519.PrivateKey, standard jwt.Claims, operation operationClaims) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := jwt.Signed(signer).Claims(standard).Claims(operation).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return serialized
}

func incomingContext(token, idempotencyKey string) context.Context {
	values := metadata.Pairs(AuthorizationMetadata, "Bearer "+token)
	if idempotencyKey != "" {
		values.Append(IdempotencyMetadata, idempotencyKey)
	}
	return metadata.NewIncomingContext(context.Background(), values)
}

func requestDigest(request []byte) string {
	digest := sha256.Sum256(request)
	return hex.EncodeToString(digest[:])
}
