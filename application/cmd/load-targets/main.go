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
)

const usage = "usage: load-targets PRIVATE_JWK BASE_URL SCENARIO COUNT PREFIX"

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
	if len(arguments) != 5 {
		return errors.New(usage)
	}
	privateKey, baseURL, scenario, countText, prefix := arguments[0], arguments[1], arguments[2], arguments[3], arguments[4]
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Scheme != "http" || parsedBaseURL.Host == "" || parsedBaseURL.Path != "" {
		return errors.New("BASE_URL must be an HTTP origin without a path")
	}
	count, err := strconv.Atoi(countText)
	if err != nil || count < 1 || count > 1_000_000 {
		return errors.New("COUNT must be an integer from 1 through 1000000")
	}
	if _, err := exchange.ParseOfferID(prefix); err != nil || len(prefix) > 96 {
		return errors.New("PREFIX must be 1-96 visible ASCII characters")
	}

	encoder := json.NewEncoder(output)
	for index := 1; index <= count; index++ {
		generated, err := makeTarget(privateKey, strings.TrimSuffix(baseURL, "/"), scenario, prefix, index, now)
		if err != nil {
			return err
		}
		if err := encoder.Encode(generated); err != nil {
			return err
		}
	}
	return nil
}

func makeTarget(privateKey, baseURL, scenario, prefix string, index int, now time.Time) (target, error) {
	offerID := fmt.Sprintf("%s-%06d", prefix, index)
	var requestJSON string
	method := "GET"
	var path string
	subject := "demo-buyer"
	var operation string
	idempotencyKey := "-"

	switch scenario {
	case "create":
		method = "POST"
		path = "/sale-offers"
		subject = "demo-seller"
		operation = exchange.OperationCreateSaleOffer
		idempotencyKey = fmt.Sprintf("%s-create-%06d", prefix, index)
		requestJSON = fmt.Sprintf(`{"id":%q,"bond_series":"DEMO2026","price":"101.25","currency_code":"USD"}`, offerID)
	case "buy":
		method = "POST"
		path = "/buys"
		operation = exchange.OperationBuy
		idempotencyKey = fmt.Sprintf("%s-buy-%06d", prefix, index)
		requestJSON = fmt.Sprintf(`{"sale_offer_id":%q}`, offerID)
	case "contended-buy":
		method = "POST"
		path = "/buys"
		operation = exchange.OperationBuy
		idempotencyKey = fmt.Sprintf("%s-contend-%06d", prefix, index)
		requestJSON = fmt.Sprintf(`{"sale_offer_id":%q}`, fmt.Sprintf("%s-%06d", prefix, 1))
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
