// Command demo-auth creates ephemeral development keys and request-bound
// assertions for the disposable demo. It is not linked into the server binary.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/fabianhjr/BondExchange/internal/authn"
	"github.com/fabianhjr/BondExchange/internal/exchange"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	demoIssuer   = "https://demo-issuer.invalid"
	demoAudience = "bond-exchange-demo"
)

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

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 2 && arguments[0] == "init" {
		return initialize(arguments[1])
	}
	if len(arguments) == 6 && arguments[0] == "token" {
		return issue(arguments[1], arguments[2], arguments[3], arguments[4], arguments[5])
	}
	return errors.New("usage: demo-auth init DIR | demo-auth token PRIVATE_JWK SUBJECT OPERATION IDEMPOTENCY_KEY_OR_DASH REQUEST_JSON")
}

func initialize(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privateJWK := jose.JSONWebKey{Key: privateKey, KeyID: "demo-key", Algorithm: string(jose.EdDSA), Use: "sig"}
	publicSet := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: publicKey, KeyID: "demo-key", Algorithm: string(jose.EdDSA), Use: "sig"}}}
	if err := writeJSON(filepath.Join(directory, "private.jwk"), privateJWK, 0o600); err != nil {
		return err
	}
	return writeJSON(filepath.Join(directory, "public.jwks"), publicSet, 0o600)
}

func issue(privateKeyPath, subject, operation, idempotencyKey, requestJSON string) error {
	serialized, err := issueToken(privateKeyPath, subject, operation, idempotencyKey, requestJSON, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Println(serialized)
	return nil
}

func issueToken(privateKeyPath, subject, operation, idempotencyKey, requestJSON string, now time.Time) (string, error) {
	key, err := readPrivateKey(privateKeyPath)
	if err != nil {
		return "", err
	}
	request, err := requestMessage(operation)
	if err != nil {
		return "", err
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal([]byte(requestJSON), request); err != nil {
		return "", fmt.Errorf("decode request JSON: %w", err)
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	if idempotencyKey == "-" {
		idempotencyKey = ""
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: key.Key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", key.KeyID),
	)
	if err != nil {
		return "", err
	}
	serialized, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer: demoIssuer, Subject: subject, Audience: jwt.Audience{demoAudience},
		ID: hex.EncodeToString(nonce), IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(2 * time.Minute)),
	}).Claims(operationClaims{
		ClientID: "demo-cli",
		AuthorizationDetails: []authorizationDetails{{
			Type: authn.AuthorizationType, Actions: []string{operation},
			RequestSHA256: hex.EncodeToString(digest[:]), IdempotencyKey: idempotencyKey,
		}},
	}).Serialize()
	if err != nil {
		return "", err
	}
	return serialized, nil
}

func requestMessage(operation string) (proto.Message, error) {
	switch operation {
	case exchange.OperationBuy:
		return &bondexchangev1.BuyRequest{}, nil
	case exchange.OperationCreateSaleOffer:
		return &bondexchangev1.CreateSaleOfferRequest{}, nil
	case exchange.OperationListActiveOffers:
		return &bondexchangev1.ListActiveOffersRequest{}, nil
	case exchange.OperationListBondSeries:
		return &bondexchangev1.ListActiveBondSeriesRequest{}, nil
	case exchange.OperationCheckHealth:
		return &bondexchangev1.CheckHealthRequest{}, nil
	default:
		return nil, fmt.Errorf("unsupported operation %q", operation)
	}
}

func readPrivateKey(path string) (jose.JSONWebKey, error) {
	file, err := os.Open(path)
	if err != nil {
		return jose.JSONWebKey{}, err
	}
	defer file.Close()
	var key jose.JSONWebKey
	decoder := json.NewDecoder(io.LimitReader(file, 64*1024))
	if err := decoder.Decode(&key); err != nil {
		return jose.JSONWebKey{}, err
	}
	if _, ok := key.Key.(ed25519.PrivateKey); !ok || key.KeyID == "" {
		return jose.JSONWebKey{}, errors.New("demo signing key is not an Ed25519 private JWK")
	}
	return key, nil
}

func writeJSON(path string, value any, mode os.FileMode) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), mode)
}
