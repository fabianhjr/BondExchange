// Command load-targets emits authenticated Vegeta JSON targets for the
// repository's disposable integration-test server.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fabianhjr/BondExchange/application/cmd/internal/demoauth"
	"github.com/fabianhjr/BondExchange/application/internal/exchange"
	"github.com/google/uuid"
)

const usage = "usage: load-targets PRIVATE_JWK BASE_URL SCENARIO COUNT PREFIX PRINCIPAL_COUNT"

type target struct {
	Method string              `json:"method"`
	URL    string              `json:"url"`
	Body   string              `json:"body,omitempty"`
	Header map[string][]string `json:"header"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, output io.Writer, now time.Time) error {
	if len(arguments) != 6 {
		return errors.New(usage)
	}
	privateKey, baseURL, scenario, countText, prefix, principalCountText := arguments[0], arguments[1], arguments[2], arguments[3], arguments[4], arguments[5]
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Scheme != "http" || parsedBaseURL.Host == "" || parsedBaseURL.Path != "" {
		return errors.New("BASE_URL must be an HTTP origin without a path")
	}
	count, err := strconv.Atoi(countText)
	if err != nil || count < 1 || count > 1_000_000 {
		return errors.New("COUNT must be an integer from 1 through 1000000")
	}
	if prefix == "" || len(prefix) > 4096 || strings.ContainsAny(prefix, "\r\n") {
		return errors.New("PREFIX must be a nonempty label, UUIDv7, or @-prefixed offer-ID file")
	}
	principalCount, err := strconv.Atoi(principalCountText)
	if err != nil || principalCount < 1 || principalCount > 1_000_000 {
		return errors.New("PRINCIPAL_COUNT must be an integer from 1 through 1000000")
	}
	offerIDs, err := loadOfferIDs(scenario, prefix, count)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(output)
	for index := 1; index <= count; index++ {
		generated, err := makeTarget(privateKey, strings.TrimSuffix(baseURL, "/"), scenario, offerIDs, index, principalCount, now)
		if err != nil {
			return err
		}
		if err := encoder.Encode(generated); err != nil {
			return err
		}
	}
	return nil
}

func makeTarget(privateKey, baseURL, scenario string, offerIDs []string, index, principalCount int, now time.Time) (target, error) {
	var requestJSON string
	method := "GET"
	var path string
	subject := fmt.Sprintf("load-buyer-%d", ((index-1)%principalCount)+1)
	var operation string
	idempotencyKey := "-"

	switch scenario {
	case "create":
		method = "POST"
		path = "/sale-offers"
		subject = fmt.Sprintf("load-seller-%d", ((index-1)%principalCount)+1)
		operation = exchange.OperationCreateSaleOffer
		idempotencyKey = uuid.NewString()
		requestJSON = `{"bond_series":"DEMO2026","price":"101.25","currency_code":"MXN"}`
	case "buy":
		method = "POST"
		path = "/buys"
		operation = exchange.OperationBuy
		idempotencyKey = uuid.NewString()
		requestJSON = fmt.Sprintf(`{"sale_offer_id":%q}`, offerIDs[index-1])
	case "contended-buy":
		method = "POST"
		path = "/buys"
		operation = exchange.OperationBuy
		idempotencyKey = uuid.NewString()
		requestJSON = fmt.Sprintf(`{"sale_offer_id":%q}`, offerIDs[0])
	case "list-offers":
		path = "/active-offers?bond=DEMO2026"
		operation = exchange.OperationListActiveOffers
		requestJSON = `{"bond":"DEMO2026"}`
	case "list-series":
		path = "/active-bond-series"
		operation = exchange.OperationListBondSeries
		requestJSON = `{}`
	default:
		return target{}, fmt.Errorf("unsupported SCENARIO %q", scenario)
	}

	token, err := demoauth.IssueToken(privateKey, subject, operation, idempotencyKey, requestJSON, now)
	if err != nil {
		return target{}, fmt.Errorf("issue %s target %d: %w", scenario, index, err)
	}
	headers := map[string][]string{"Authorization": {"Bearer " + token}}
	generated := target{Method: method, URL: baseURL + path, Header: headers}
	if method == "POST" {
		headers["Content-Type"] = []string{"application/json"}
		headers["Idempotency-Key"] = []string{idempotencyKey}
		generated.Body = base64.StdEncoding.EncodeToString([]byte(requestJSON))
	}
	return generated, nil
}

func loadOfferIDs(scenario, value string, count int) ([]string, error) {
	if scenario != "buy" && scenario != "contended-buy" {
		return nil, nil
	}
	values := []string{value}
	if strings.HasPrefix(value, "@") {
		contents, err := os.ReadFile(strings.TrimPrefix(value, "@")) //nolint:gosec // Explicit CLI-selected test input.
		if err != nil {
			return nil, fmt.Errorf("read offer-ID file: %w", err)
		}
		values = strings.Fields(string(contents))
	}
	required := count
	if scenario == "contended-buy" {
		required = 1
	}
	if len(values) < required {
		return nil, fmt.Errorf("%s requires at least %d offer IDs", scenario, required)
	}
	for _, value := range values[:required] {
		if _, err := exchange.ParseOfferID(value); err != nil {
			return nil, fmt.Errorf("invalid sale-offer ID %q: %w", value, err)
		}
	}
	return values, nil
}
