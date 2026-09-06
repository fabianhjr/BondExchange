package sie

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchangerates"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
)

const (
	baseURL            = "https://www.banxico.org.mx"
	defaultTimeout     = 5 * time.Second
	defaultMaxBodySize = 1024 * 1024
)

var (
	ErrInvalidConfiguration = errors.New("invalid SIE client configuration")
	ErrAuthentication       = fmt.Errorf("%w: SIE API rejected its configured token", exchangerates.ErrProviderAuthentication)
	ErrInvalidResponse      = fmt.Errorf("%w: SIE API returned an invalid response", exchangerates.ErrProviderInvalidResponse)
)

type Config struct {
	Token       string
	HTTPClient  *http.Client
	Timeout     time.Duration
	MaxBodySize int64
}

type Client struct {
	token       string
	httpClient  *http.Client
	maxBodySize int64
}

func NewClient(config Config) (*Client, error) {
	if !validToken(config.Token) || config.Timeout < 0 || config.MaxBodySize < 0 {
		return nil, ErrInvalidConfiguration
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.MaxBodySize == 0 {
		config.MaxBodySize = defaultMaxBodySize
	}
	client := http.DefaultClient
	if config.HTTPClient != nil {
		client = config.HTTPClient
	}
	clientCopy := *client
	clientCopy.Timeout = config.Timeout
	clientCopy.CheckRedirect = sameOriginRedirect
	transport := clientCopy.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	// Banxico is a public dependency, not part of this service's trace trust domain.
	// Create a child client span without injecting trace or baggage headers upstream.
	clientCopy.Transport = otelhttp.NewTransport(
		transport,
		otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()),
	)
	return &Client{token: config.Token, httpClient: &clientCopy, maxBodySize: config.MaxBodySize}, nil
}

func validToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for index := range len(token) {
		character := token[index]
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func sameOriginRedirect(request *http.Request, _ []*http.Request) error {
	if request.URL.Scheme != "https" || request.URL.Host != "www.banxico.org.mx" {
		return errors.New("SIE redirect left the configured HTTPS origin")
	}
	return nil
}

func (client *Client) Fetch(ctx context.Context, request exchangerates.FetchRequest) (exchangerates.FetchResult, error) {
	endpoint, requested, err := requestURL(request)
	if err != nil {
		return exchangerates.FetchResult{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return exchangerates.FetchResult{}, fmt.Errorf("create SIE request: %w", err)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Bmx-Token", client.token)
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return exchangerates.FetchResult{}, fmt.Errorf("%w: %w", exchangerates.ErrUpstreamUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := readBounded(response.Body, client.maxBodySize)
	if err != nil {
		return exchangerates.FetchResult{}, err
	}
	if bytes.Contains(body, []byte(client.token)) {
		return exchangerates.FetchResult{}, fmt.Errorf("%w: response contained credential", ErrInvalidResponse)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return exchangerates.FetchResult{}, ErrAuthentication
	}
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusTooManyRequests {
		if retryAt, ok := rateLimitReset(response.Header, body); ok {
			return exchangerates.FetchResult{}, &exchangerates.RateLimitError{RetryAt: retryAt}
		}
	}
	if response.StatusCode != http.StatusOK {
		return exchangerates.FetchResult{}, fmt.Errorf("%w: HTTP status %d", exchangerates.ErrUpstreamUnavailable, response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return exchangerates.FetchResult{}, fmt.Errorf("%w: unexpected content type", ErrInvalidResponse)
	}
	observations, err := decodeResponse(body, requested)
	if err != nil {
		return exchangerates.FetchResult{}, err
	}
	return exchangerates.FetchResult{Response: json.RawMessage(body), Observations: observations}, nil
}

func requestURL(request exchangerates.FetchRequest) (string, map[exchangerates.SeriesID]struct{}, error) {
	if len(request.SeriesIDs) == 0 || len(request.SeriesIDs) > exchangerates.MaxSeriesPerRequest {
		return "", nil, exchangerates.ErrInvalidSeries
	}
	requested := make(map[exchangerates.SeriesID]struct{}, len(request.SeriesIDs))
	identifiers := make([]string, 0, len(request.SeriesIDs))
	for _, seriesID := range request.SeriesIDs {
		if err := seriesID.Validate(); err != nil {
			return "", nil, err
		}
		if _, exists := requested[seriesID]; exists {
			return "", nil, exchangerates.ErrInvalidSeries
		}
		requested[seriesID] = struct{}{}
		identifiers = append(identifiers, string(seriesID))
	}
	path := "/SieAPIRest/service/v1/series/" + strings.Join(identifiers, ",") + "/datos"
	switch request.Kind {
	case exchangerates.FetchLatest:
		path += "/oportuno"
	case exchangerates.FetchRange:
		from := dateOnly(request.From)
		to := dateOnly(request.To)
		if request.From.IsZero() || request.To.IsZero() || from.After(to) {
			return "", nil, exchangerates.ErrInvalidDateRange
		}
		path += "/" + from.Format(time.DateOnly) + "/" + to.Format(time.DateOnly)
	default:
		return "", nil, exchangerates.ErrInvalidDateRange
	}
	parsed, err := url.Parse(baseURL + path)
	if err != nil {
		return "", nil, err
	}
	return parsed.String(), requested, nil
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read SIE response: %w", err)
	}
	if int64(len(body)) > maximum {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, maximum)
	}
	return body, nil
}

type wireResponse struct {
	BMX struct {
		Series []wireSeries `json:"series"`
	} `json:"bmx"`
}

type wireSeries struct {
	ID           string            `json:"idSerie"`
	Observations []wireObservation `json:"datos"`
	Error        json.RawMessage   `json:"error"`
}

type wireObservation struct {
	Date  string `json:"fecha"`
	Value string `json:"dato"`
}

func decodeResponse(body []byte, requested map[exchangerates.SeriesID]struct{}) ([]exchangerates.ProviderObservation, error) {
	var decoded wireResponse
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.BMX.Series == nil {
		return nil, fmt.Errorf("%w: malformed JSON body", ErrInvalidResponse)
	}
	seenSeries := make(map[exchangerates.SeriesID]struct{}, len(decoded.BMX.Series))
	seenObservation := make(map[string]struct{})
	result := make([]exchangerates.ProviderObservation, 0)
	for _, series := range decoded.BMX.Series {
		seriesID := exchangerates.SeriesID(series.ID)
		if _, expected := requested[seriesID]; !expected {
			return nil, fmt.Errorf("%w: unrequested series", ErrInvalidResponse)
		}
		if _, duplicate := seenSeries[seriesID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate series", ErrInvalidResponse)
		}
		seenSeries[seriesID] = struct{}{}
		if len(series.Error) != 0 && string(series.Error) != "null" {
			return nil, fmt.Errorf("%w: series error", ErrInvalidResponse)
		}
		for _, item := range series.Observations {
			observation, err := parseObservation(seriesID, item)
			if err != nil {
				return nil, err
			}
			key := string(seriesID) + "\x00" + observation.Date.Format(time.DateOnly)
			if _, duplicate := seenObservation[key]; duplicate {
				return nil, fmt.Errorf("%w: duplicate observation", ErrInvalidResponse)
			}
			seenObservation[key] = struct{}{}
			result = append(result, observation)
		}
	}
	if len(seenSeries) != len(requested) {
		return nil, fmt.Errorf("%w: missing series", ErrInvalidResponse)
	}
	return result, nil
}

func parseObservation(seriesID exchangerates.SeriesID, item wireObservation) (exchangerates.ProviderObservation, error) {
	var observedAt time.Time
	var err error
	for _, layout := range []string{"02/01/2006", time.DateOnly} {
		observedAt, err = time.Parse(layout, item.Date)
		if err == nil {
			break
		}
	}
	value, valueErr := decimal.NewFromString(strings.ReplaceAll(item.Value, ",", ""))
	if err != nil || valueErr != nil || !value.IsPositive() {
		return exchangerates.ProviderObservation{}, exchangerates.ErrInvalidObservation
	}
	return exchangerates.ProviderObservation{SeriesID: seriesID, Date: observedAt, Value: value}, nil
}

type wireRateLimit struct {
	Error struct {
		TimeReset      int64 `json:"timeReset"`
		SecondsToReset int64 `json:"secondsToReset"`
	} `json:"error"`
}

func rateLimitReset(headers http.Header, body []byte) (time.Time, bool) {
	if value := headers.Get("Bmx-Timereset"); value != "" {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err == nil && seconds > 0 {
			return time.Unix(seconds, 0), true
		}
	}
	var response wireRateLimit
	if json.Unmarshal(body, &response) == nil && response.Error.TimeReset > 0 {
		return time.Unix(response.Error.TimeReset, 0), true
	}
	if value := headers.Get("Bmx-Secondstoreset"); value != "" {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err == nil && seconds > 0 {
			return time.Now().Add(time.Duration(seconds) * time.Second), true
		}
	}
	return time.Time{}, false
}
