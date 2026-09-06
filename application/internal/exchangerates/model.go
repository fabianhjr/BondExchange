package exchangerates

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

const MaxSeriesPerRequest = 20

var seriesIDPattern = regexp.MustCompile(`^[A-Z]{2}[0-9]{1,20}$`)

var (
	ErrInvalidSeries           = errors.New("exchange-rate series must have a canonical Banxico identifier and distinct ISO currency codes")
	ErrInvalidDateRange        = errors.New("exchange-rate date range is invalid")
	ErrInvalidObservation      = errors.New("exchange-rate observation is invalid")
	ErrIncompleteResponse      = errors.New("SIE response did not contain a latest observation for every requested series")
	ErrUpstreamUnavailable     = errors.New("SIE API is unavailable")
	ErrProviderAuthentication  = errors.New("exchange-rate provider authentication failed")
	ErrProviderInvalidResponse = errors.New("exchange-rate provider response is invalid")
	ErrColdFetchInProgress     = errors.New("exchange-rate data is not cached and another fetch is in progress")
)

type SeriesID string

func (id SeriesID) Validate() error {
	if !seriesIDPattern.MatchString(string(id)) {
		return ErrInvalidSeries
	}
	return nil
}

type Series struct {
	ID    SeriesID
	Base  string
	Quote string
}

func (series Series) Validate() error {
	if series.ID.Validate() != nil || !isCurrency(series.Base) || !isCurrency(series.Quote) || series.Base == series.Quote {
		return ErrInvalidSeries
	}
	return nil
}

func isCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for index := range len(value) {
		if value[index] < 'A' || value[index] > 'Z' {
			return false
		}
	}
	return true
}

type Observation struct {
	RevisionID string
	SeriesID   SeriesID
	Base       string
	Quote      string
	Date       time.Time
	Value      decimal.Decimal
	RecordedAt time.Time
	Stale      bool
}

type FetchKind string

const (
	FetchLatest FetchKind = "latest"
	FetchRange  FetchKind = "range"
)

type FetchRequest struct {
	Kind      FetchKind
	SeriesIDs []SeriesID
	From      time.Time
	To        time.Time
}

type ProviderObservation struct {
	SeriesID SeriesID
	Date     time.Time
	Value    decimal.Decimal
}

type FetchResult struct {
	Response     json.RawMessage
	Observations []ProviderObservation
}

type RateLimitError struct {
	RetryAt time.Time
}

func (err *RateLimitError) Error() string {
	return "SIE API request limit was reached"
}

func normalizeSeries(input []Series) ([]Series, error) {
	byID := make(map[SeriesID]Series, len(input))
	for _, series := range input {
		if err := series.Validate(); err != nil {
			return nil, err
		}
		if previous, exists := byID[series.ID]; exists && previous != series {
			return nil, ErrInvalidSeries
		}
		byID[series.ID] = series
	}
	if len(byID) == 0 {
		return nil, ErrInvalidSeries
	}
	result := make([]Series, 0, len(byID))
	for _, series := range byID {
		result = append(result, series)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
