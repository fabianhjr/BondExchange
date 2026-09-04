package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"google.golang.org/grpc/metadata"
)

const (
	AuthorizationMetadata = "authorization"
	IdempotencyMetadata   = "idempotency-key"
	AuthorizationType     = "urn:bond-exchange:operation:v1"
)

var errInvalidAssertion = errors.New("invalid operation assertion")

type PrincipalResolver interface {
	ResolvePrincipal(ctx context.Context, issuer string, subject string) (exchange.Principal, error)
}

type Authenticator interface {
	Authenticate(
		ctx context.Context,
		operation string,
		canonicalRequest []byte,
		idempotent bool,
	) (Result, error)
}

type Result struct {
	AccessContext  exchange.AccessContext
	IdempotencyKey string
}

type JWTConfig struct {
	Issuer     string
	Audience   string
	Keys       jose.JSONWebKeySet
	Algorithms []jose.SignatureAlgorithm
	MaximumAge time.Duration
	ClockSkew  time.Duration
	Now        func() time.Time
}

type JWTAuthenticator struct {
	config   JWTConfig
	resolver PrincipalResolver
}

type operationClaims struct {
	ClientID             string                 `json:"client_id"`
	AuthorizationDetails []authorizationDetails `json:"authorization_details"`
}

type authorizationDetails struct {
	Type           string   `json:"type"`
	Actions        []string `json:"actions"`
	RequestSHA256  string   `json:"request_sha256"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

func NewJWTAuthenticator(config JWTConfig, resolver PrincipalResolver) (*JWTAuthenticator, error) {
	if !validClaimText(config.Issuer, 1024) || !validClaimText(config.Audience, 1024) || resolver == nil || len(config.Keys.Keys) == 0 {
		return nil, errors.New("issuer, audience, verification keys, and principal resolver are required")
	}
	if len(config.Algorithms) == 0 {
		config.Algorithms = []jose.SignatureAlgorithm{jose.EdDSA, jose.ES256}
	}
	if config.MaximumAge <= 0 {
		config.MaximumAge = 5 * time.Minute
	}
	if config.ClockSkew < 0 || config.ClockSkew > time.Minute {
		return nil, errors.New("clock skew must be between zero and one minute")
	}
	if config.ClockSkew == 0 {
		config.ClockSkew = 30 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	allowedAlgorithms := make(map[string]struct{}, len(config.Algorithms))
	for _, algorithm := range config.Algorithms {
		allowedAlgorithms[string(algorithm)] = struct{}{}
	}
	seenKeyIDs := make(map[string]struct{}, len(config.Keys.Keys))
	for _, key := range config.Keys.Keys {
		_, algorithmAllowed := allowedAlgorithms[key.Algorithm]
		_, duplicateKeyID := seenKeyIDs[key.KeyID]
		if !validClaimText(key.KeyID, 128) || duplicateKeyID || !key.IsPublic() || key.Use != "sig" || !algorithmAllowed {
			return nil, errors.New("every verification key must be public, have a key ID, and have use sig")
		}
		seenKeyIDs[key.KeyID] = struct{}{}
	}
	return &JWTAuthenticator{config: config, resolver: resolver}, nil
}

func (authenticator *JWTAuthenticator) Authenticate(
	ctx context.Context,
	operation string,
	canonicalRequest []byte,
	idempotent bool,
) (Result, error) {
	rawAssertion, err := singleBearerToken(ctx)
	if err != nil {
		return Result{}, exchange.ErrUnauthenticated
	}
	idempotencyKey, err := requestIdempotencyKey(ctx, idempotent)
	if err != nil {
		return Result{}, err
	}

	token, err := jwt.ParseSigned(rawAssertion, authenticator.config.Algorithms)
	if err != nil || len(rawAssertion) > 16*1024 || len(token.Headers) != 1 || token.Headers[0].KeyID == "" {
		return Result{}, exchange.ErrUnauthenticated
	}
	keys := authenticator.config.Keys.Key(token.Headers[0].KeyID)
	if len(keys) != 1 {
		return Result{}, exchange.ErrUnauthenticated
	}
	if keys[0].Algorithm != token.Headers[0].Algorithm {
		return Result{}, exchange.ErrUnauthenticated
	}

	var standard jwt.Claims
	var operationClaim operationClaims
	if err := token.Claims(keys[0].Key, &standard, &operationClaim); err != nil {
		return Result{}, exchange.ErrUnauthenticated
	}
	if err := authenticator.validateClaims(standard, operationClaim, operation, canonicalRequest, idempotencyKey); err != nil {
		return Result{}, exchange.ErrUnauthenticated
	}

	principal, err := authenticator.resolver.ResolvePrincipal(ctx, standard.Issuer, standard.Subject)
	if err != nil {
		return Result{}, exchange.ErrUnauthenticated
	}
	principal.Issuer = standard.Issuer
	principal.Subject = standard.Subject
	principal.ClientID = operationClaim.ClientID
	requestDigest := sha256.Sum256(canonicalRequest)
	assertionDigest := sha256.Sum256([]byte(rawAssertion))
	return Result{
		AccessContext: exchange.AccessContext{
			Principal:       principal,
			Operation:       operation,
			AssertionID:     standard.ID,
			AssertionDigest: assertionDigest,
			RequestDigest:   requestDigest,
		},
		IdempotencyKey: idempotencyKey,
	}, nil
}

func (authenticator *JWTAuthenticator) validateClaims(
	standard jwt.Claims,
	operationClaim operationClaims,
	operation string,
	canonicalRequest []byte,
	idempotencyKey string,
) error {
	now := authenticator.config.Now().UTC()
	if !validClaimText(standard.Subject, 256) || !exchange.IsValidIdempotencyKey(standard.ID) || standard.IssuedAt == nil || standard.Expiry == nil {
		return errInvalidAssertion
	}
	if err := standard.ValidateWithLeeway(jwt.Expected{
		Issuer:      authenticator.config.Issuer,
		AnyAudience: jwt.Audience{authenticator.config.Audience},
		Time:        now,
	}, authenticator.config.ClockSkew); err != nil {
		return errInvalidAssertion
	}
	issuedAt := standard.IssuedAt.Time()
	expiresAt := standard.Expiry.Time()
	if issuedAt.After(now.Add(authenticator.config.ClockSkew)) ||
		expiresAt.Sub(issuedAt) <= 0 ||
		expiresAt.Sub(issuedAt) > authenticator.config.MaximumAge {
		return errInvalidAssertion
	}
	if !validClaimText(operationClaim.ClientID, 256) || len(operationClaim.AuthorizationDetails) != 1 {
		return errInvalidAssertion
	}
	detail := operationClaim.AuthorizationDetails[0]
	if detail.Type != AuthorizationType || len(detail.Actions) != 1 || detail.Actions[0] != operation {
		return errInvalidAssertion
	}
	digest := sha256.Sum256(canonicalRequest)
	wantDigest := hex.EncodeToString(digest[:])
	if len(detail.RequestSHA256) != len(wantDigest) ||
		subtle.ConstantTimeCompare([]byte(detail.RequestSHA256), []byte(wantDigest)) != 1 {
		return errInvalidAssertion
	}
	if detail.IdempotencyKey != idempotencyKey {
		return errInvalidAssertion
	}
	return nil
}

func validClaimText(value string, maximumBytes int) bool {
	if value == "" || len(value) > maximumBytes {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func singleBearerToken(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, AuthorizationMetadata)
	if len(values) != 1 {
		return "", errInvalidAssertion
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || scheme != "Bearer" || token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", errInvalidAssertion
	}
	return token, nil
}

func requestIdempotencyKey(ctx context.Context, required bool) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, IdempotencyMetadata)
	if !required {
		if len(values) != 0 {
			return "", fmt.Errorf("%w: not allowed for read operations", exchange.ErrInvalidIdempotencyKey)
		}
		return "", nil
	}
	if len(values) != 1 || !exchange.IsValidIdempotencyKey(values[0]) {
		return "", exchange.ErrInvalidIdempotencyKey
	}
	return values[0], nil
}
