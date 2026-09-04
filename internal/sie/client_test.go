package sie

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/internal/exchangerates"
)

const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client, err := NewClient(Config{
		Token:      testToken,
		HTTPClient: &http.Client{Transport: transport},
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestFetchLatestAndRange(t *testing.T) {
	t.Parallel()
	requests := 0
	client := newTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.Header.Get("Bmx-Token") != testToken || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("request = %s, headers %v", request.Method, request.Header)
		}
		switch requests {
		case 1:
			if request.URL.Path != "/SieAPIRest/service/v1/series/SF43718,SF46410/datos/oportuno" || request.URL.RawQuery != "" {
				t.Fatalf("latest URL = %s", request.URL)
			}
			return response(http.StatusOK, "application/json;charset=UTF-8", `{"bmx":{"series":[{"idSerie":"SF43718","titulo":"FIX","datos":[{"fecha":"04/09/2026","dato":"19.8765"}]},{"idSerie":"SF46410","datos":[{"fecha":"2026-09-03","dato":"1,234.50"}]}]}}`), nil
		case 2:
			if request.URL.Path != "/SieAPIRest/service/v1/series/SF43718/datos/2024-01-01/2024-01-31" {
				t.Fatalf("range URL = %s", request.URL)
			}
			return response(http.StatusOK, "application/json", `{"bmx":{"series":[{"idSerie":"SF43718","datos":[]}]}}`), nil
		default:
			t.Fatalf("unexpected request %d", requests)
			return nil, nil
		}
	}))
	result, err := client.Fetch(context.Background(), exchangerates.FetchRequest{
		Kind:      exchangerates.FetchLatest,
		SeriesIDs: []exchangerates.SeriesID{"SF43718", "SF46410"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 2 || result.Observations[0].Value.String() != "19.8765" || result.Observations[1].Value.String() != "1234.5" {
		t.Fatalf("latest result = %#v", result)
	}
	result, err = client.Fetch(context.Background(), exchangerates.FetchRequest{
		Kind:      exchangerates.FetchRange,
		SeriesIDs: []exchangerates.SeriesID{"SF43718"},
		From:      time.Date(2024, 1, 1, 14, 0, 0, 0, time.FixedZone("test", -6*60*60)),
		To:        time.Date(2024, 1, 31, 14, 0, 0, 0, time.UTC),
	})
	if err != nil || len(result.Observations) != 0 || requests != 2 {
		t.Fatalf("range result/error/requests = %#v/%v/%d", result, err, requests)
	}
}

func TestFetchRejectsInvalidRequestsAndResponses(t *testing.T) {
	t.Parallel()
	valid := exchangerates.FetchRequest{Kind: exchangerates.FetchLatest, SeriesIDs: []exchangerates.SeriesID{"SF43718"}}
	invalidRequests := []exchangerates.FetchRequest{
		{},
		{Kind: "unknown", SeriesIDs: valid.SeriesIDs},
		{Kind: exchangerates.FetchLatest, SeriesIDs: []exchangerates.SeriesID{"invalid"}},
		{Kind: exchangerates.FetchLatest, SeriesIDs: []exchangerates.SeriesID{"SF43718", "SF43718"}},
		{Kind: exchangerates.FetchRange, SeriesIDs: valid.SeriesIDs},
		{Kind: exchangerates.FetchRange, SeriesIDs: valid.SeriesIDs, From: time.Now(), To: time.Now().Add(-24 * time.Hour)},
	}
	client := newTestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached transport")
		return nil, nil
	}))
	for _, request := range invalidRequests {
		if _, err := client.Fetch(context.Background(), request); err == nil {
			t.Fatalf("invalid request accepted: %#v", request)
		}
	}

	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "content type", contentType: "text/html", body: `{}`},
		{name: "malformed", contentType: "application/json", body: `{`},
		{name: "missing wrapper", contentType: "application/json", body: `{}`},
		{name: "unrequested", contentType: "application/json", body: `{"bmx":{"series":[{"idSerie":"SF46410","datos":[]}]}}`},
		{name: "duplicate series", contentType: "application/json", body: `{"bmx":{"series":[{"idSerie":"SF43718","datos":[]},{"idSerie":"SF43718","datos":[]}]}}`},
		{name: "missing series", contentType: "application/json", body: `{"bmx":{"series":[]}}`},
		{name: "series error", contentType: "application/json", body: `{"bmx":{"series":[{"idSerie":"SF43718","error":{"mensaje":"bad"}}]}}`},
		{name: "invalid date", contentType: "application/json", body: `{"bmx":{"series":[{"idSerie":"SF43718","datos":[{"fecha":"bad","dato":"19"}]}]}}`},
		{name: "invalid value", contentType: "application/json", body: `{"bmx":{"series":[{"idSerie":"SF43718","datos":[{"fecha":"04/09/2026","dato":"N/E"}]}]}}`},
		{name: "nonpositive", contentType: "application/json", body: `{"bmx":{"series":[{"idSerie":"SF43718","datos":[{"fecha":"04/09/2026","dato":"0"}]}]}}`},
		{name: "duplicate observation", contentType: "application/json", body: `{"bmx":{"series":[{"idSerie":"SF43718","datos":[{"fecha":"04/09/2026","dato":"19"},{"fecha":"04/09/2026","dato":"20"}]}]}}`},
		{name: "credential echo", contentType: "application/json", body: `{"bmx":{"series":[]},"echo":"` + testToken + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newTestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, test.contentType, test.body), nil
			}))
			if _, err := client.Fetch(context.Background(), valid); !errors.Is(err, ErrInvalidResponse) && !errors.Is(err, exchangerates.ErrInvalidObservation) {
				t.Fatalf("response error = %v", err)
			}
		})
	}
}

func TestFetchClassifiesTransportStatusLimitsAndSize(t *testing.T) {
	t.Parallel()
	request := exchangerates.FetchRequest{Kind: exchangerates.FetchLatest, SeriesIDs: []exchangerates.SeriesID{"SF43718"}}
	tests := []struct {
		name      string
		transport http.RoundTripper
		check     func(error) bool
	}{
		{
			name: "transport",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("offline")
			}),
			check: func(err error) bool { return errors.Is(err, exchangerates.ErrUpstreamUnavailable) },
		},
		{
			name: "authentication",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusUnauthorized, "application/json", `{}`), nil
			}),
			check: func(err error) bool { return errors.Is(err, ErrAuthentication) },
		},
		{
			name: "status",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusServiceUnavailable, "application/json", `{}`), nil
			}),
			check: func(err error) bool { return errors.Is(err, exchangerates.ErrUpstreamUnavailable) },
		},
		{
			name: "rate limit header",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				result := response(http.StatusBadRequest, "application/json", `{}`)
				result.Header.Set("Bmx-Timereset", "1893456000")
				return result, nil
			}),
			check: isRateLimit,
		},
		{
			name: "rate limit body",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusTooManyRequests, "application/json", `{"error":{"timeReset":1893456000}}`), nil
			}),
			check: isRateLimit,
		},
		{
			name: "oversized",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, "application/json", strings.Repeat("x", 9)), nil
			}),
			check: func(err error) bool { return errors.Is(err, ErrInvalidResponse) },
		},
		{
			name: "credential echo",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, "application/json", testToken), nil
			}),
			check: func(err error) bool { return errors.Is(err, ErrInvalidResponse) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newTestClient(t, test.transport)
			if test.name == "oversized" {
				client.maxBodySize = 8
			}
			_, err := client.Fetch(context.Background(), request)
			if !test.check(err) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func isRateLimit(err error) bool {
	var limited *exchangerates.RateLimitError
	return errors.As(err, &limited) && limited.RetryAt.Equal(time.Unix(1893456000, 0))
}

func TestClientConfigurationAndRedirects(t *testing.T) {
	t.Parallel()
	for _, config := range []Config{
		{},
		{Token: "short"},
		{Token: strings.Repeat("!", 64)},
		{Token: testToken, Timeout: -1},
		{Token: testToken, MaxBodySize: -1},
	} {
		if _, err := NewClient(config); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("config %#v error = %v", config, err)
		}
	}
	client, err := NewClient(Config{Token: testToken})
	if err != nil || client.httpClient.Timeout != defaultTimeout || client.maxBodySize != defaultMaxBodySize {
		t.Fatalf("defaults = %#v, %v", client, err)
	}
	allowed, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/next", nil)
	if err := sameOriginRedirect(allowed, nil); err != nil {
		t.Fatal(err)
	}
	denied, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/next", nil)
	if sameOriginRedirect(denied, nil) == nil {
		t.Fatal("cross-origin redirect accepted")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func TestResponseReadingAndRateLimitFallbacks(t *testing.T) {
	t.Parallel()
	if _, err := readBounded(failingReader{}, 10); err == nil {
		t.Fatal("response read error was lost")
	}
	headers := make(http.Header)
	headers.Set("Bmx-Secondstoreset", "30")
	before := time.Now()
	retryAt, ok := rateLimitReset(headers, []byte(`{}`))
	if !ok || retryAt.Before(before.Add(29*time.Second)) || retryAt.After(time.Now().Add(31*time.Second)) {
		t.Fatalf("seconds-to-reset = %v, %t", retryAt, ok)
	}
	headers.Set("Bmx-Timereset", "invalid")
	headers.Set("Bmx-Secondstoreset", "invalid")
	if _, ok := rateLimitReset(headers, []byte(`{"error":{}}`)); ok {
		t.Fatal("invalid rate-limit metadata was accepted")
	}
}
