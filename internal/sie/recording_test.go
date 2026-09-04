package sie

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/internal/exchangerates"
)

type cassette struct {
	FormatVersion int           `json:"format_version"`
	CaptureMode   string        `json:"capture_mode"`
	RecordedAt    string        `json:"recorded_at"`
	Interactions  []interaction `json:"interactions"`
}

type interaction struct {
	Request  recordedRequest  `json:"request"`
	Response recordedResponse `json:"response"`
}

type recordedRequest struct {
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Headers http.Header `json:"headers"`
}

type recordedResponse struct {
	Status  int             `json:"status"`
	Headers http.Header     `json:"headers"`
	Body    json.RawMessage `json:"body"`
}

type replayTransport struct {
	testing      *testing.T
	interactions []interaction
	index        int
}

func (transport *replayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.testing.Helper()
	if transport.index >= len(transport.interactions) {
		transport.testing.Fatal("unexpected request after cassette was exhausted")
	}
	recorded := transport.interactions[transport.index]
	transport.index++
	if request.Method != recorded.Request.Method || request.URL.RequestURI() != recorded.Request.Path {
		transport.testing.Fatalf("request = %s %s, want %s %s", request.Method, request.URL.RequestURI(), recorded.Request.Method, recorded.Request.Path)
	}
	if request.Header.Get("Bmx-Token") == "" || recorded.Request.Headers.Get("Bmx-Token") != "<REDACTED>" {
		transport.testing.Fatal("SIE credential was absent from the request or not redacted in the cassette")
	}
	if request.Header.Get("Accept") != recorded.Request.Headers.Get("Accept") {
		transport.testing.Fatalf("Accept header = %q", request.Header.Get("Accept"))
	}
	return &http.Response{
		StatusCode: recorded.Response.Status,
		Header:     recorded.Response.Headers.Clone(),
		Body:       io.NopCloser(bytes.NewReader(recorded.Response.Body)),
	}, nil
}

func TestRecordedInteractionsReplayOffline(t *testing.T) {
	paths, err := filepath.Glob("testdata/recordings/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no SIE recording fixtures found")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			encoded, err := os.ReadFile(path) //nolint:gosec // The glob is restricted to the package's fixed testdata directory.
			if err != nil {
				t.Fatal(err)
			}
			var recording cassette
			if err := json.Unmarshal(encoded, &recording); err != nil {
				t.Fatal(err)
			}
			if recording.FormatVersion != 1 || len(recording.Interactions) != 2 {
				t.Fatalf("cassette metadata = %#v", recording)
			}
			transport := &replayTransport{testing: t, interactions: recording.Interactions}
			client := newTestClient(t, transport)
			latest, err := client.Fetch(context.Background(), exchangerates.FetchRequest{
				Kind:      exchangerates.FetchLatest,
				SeriesIDs: []exchangerates.SeriesID{"SF43718"},
			})
			if err != nil || len(latest.Observations) != 1 {
				t.Fatalf("latest replay = %#v, %v", latest, err)
			}
			historical, err := client.Fetch(context.Background(), exchangerates.FetchRequest{
				Kind:      exchangerates.FetchRange,
				SeriesIDs: []exchangerates.SeriesID{"SF43718"},
				From:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				To:        time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
			})
			if err != nil || len(historical.Observations) == 0 || transport.index != len(recording.Interactions) {
				t.Fatalf("historical replay = %#v, %v; used %d interactions", historical, err, transport.index)
			}
		})
	}
}

type recordingTransport struct {
	base         http.RoundTripper
	token        string
	mutex        sync.Mutex
	interactions []interaction
}

func (transport *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, defaultMaxBodySize+1))
	closeErr := response.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(body) > defaultMaxBodySize {
		return nil, ErrInvalidResponse
	}
	if bytes.Contains(body, []byte(transport.token)) {
		return nil, errors.New("SIE response unexpectedly contained the credential")
	}
	recorded := interaction{
		Request: recordedRequest{
			Method: request.Method,
			Path:   request.URL.RequestURI(),
			Headers: http.Header{
				"Accept":    []string{request.Header.Get("Accept")},
				"Bmx-Token": []string{"<REDACTED>"},
			},
		},
		Response: recordedResponse{
			Status:  response.StatusCode,
			Headers: selectedResponseHeaders(response.Header),
			Body:    append(json.RawMessage(nil), body...),
		},
	}
	transport.mutex.Lock()
	transport.interactions = append(transport.interactions, recorded)
	transport.mutex.Unlock()
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func selectedResponseHeaders(headers http.Header) http.Header {
	result := make(http.Header)
	for _, name := range []string{"Content-Type", "Bmx-Timereset", "Bmx-Secondstoreset"} {
		if values := headers.Values(name); len(values) > 0 {
			result[name] = append([]string(nil), values...)
		}
	}
	return result
}

func TestRecordLiveSIE(t *testing.T) {
	token := os.Getenv("BANXICO_SIE_TOKEN")
	if token == "" {
		t.Skip("BANXICO_SIE_TOKEN enables explicit live recording")
	}
	transport := &recordingTransport{base: http.DefaultTransport, token: token}
	client, err := NewClient(Config{Token: token, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	requests := []exchangerates.FetchRequest{
		{Kind: exchangerates.FetchLatest, SeriesIDs: []exchangerates.SeriesID{"SF43718"}},
		{
			Kind:      exchangerates.FetchRange,
			SeriesIDs: []exchangerates.SeriesID{"SF43718"},
			From:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			To:        time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, request := range requests {
		if _, err := client.Fetch(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	recording := cassette{
		FormatVersion: 1,
		CaptureMode:   "live-banxico-sie",
		RecordedAt:    time.Now().UTC().Format(time.RFC3339),
		Interactions:  transport.interactions,
	}
	writeCassette(t, "testdata/recordings/banxico_sie.json", recording, token)
}

func writeCassette(t *testing.T, target string, recording cassette, token string) {
	t.Helper()
	directory := filepath.Dir(target)
	temporary, err := os.CreateTemp(directory, ".sie-recording-*.json")
	if err != nil {
		t.Fatal(err)
	}
	temporaryName := temporary.Name()
	t.Cleanup(func() { _ = os.Remove(temporaryName) })
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(recording); err != nil {
		_ = temporary.Close()
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(temporaryName) //nolint:gosec // CreateTemp returned this path inside the fixed fixture directory.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(token)) || strings.Contains(string(encoded), fmt.Sprintf("%q", token)) {
		t.Fatal("refusing to store an unredacted SIE credential")
	}
	if err := os.Rename(temporaryName, target); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingHeaderSelection(t *testing.T) {
	t.Parallel()
	headers := http.Header{
		"Content-Type":       []string{"application/json"},
		"Bmx-Timereset":      []string{"1"},
		"Bmx-Secondstoreset": []string{"2"},
		"Set-Cookie":         []string{"secret=value"},
	}
	want := headers.Clone()
	want.Del("Set-Cookie")
	if got := selectedResponseHeaders(headers); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected headers = %v, want %v", got, want)
	}
}
