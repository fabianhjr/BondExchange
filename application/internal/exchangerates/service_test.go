package exchangerates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

type providerFake struct {
	mutex    sync.Mutex
	requests []FetchRequest
	err      error
}

func (provider *providerFake) Fetch(_ context.Context, request FetchRequest) (FetchResult, error) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.requests = append(provider.requests, request)
	if provider.err != nil {
		return FetchResult{}, provider.err
	}
	date := request.To
	if request.Kind == FetchLatest {
		date = time.Date(2026, time.September, 4, 0, 0, 0, 0, time.UTC)
	}
	observations := make([]ProviderObservation, 0, len(request.SeriesIDs))
	for index, seriesID := range request.SeriesIDs {
		observations = append(observations, ProviderObservation{
			SeriesID: seriesID,
			Date:     date,
			Value:    decimal.NewFromInt(int64(index + 18)),
		})
	}
	body := json.RawMessage(fmt.Sprintf(`{"request":%d}`, len(provider.requests)))
	return FetchResult{Response: body, Observations: observations}, nil
}

type memoryUnit struct {
	ready       bool
	lease       string
	completedAt time.Time
}

type memoryStore struct {
	mutex        sync.Mutex
	units        map[string]memoryUnit
	observations map[string][]Observation
	blockedUntil time.Time
	failures     int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{units: make(map[string]memoryUnit), observations: make(map[string][]Observation)}
}

func (store *memoryStore) States(_ context.Context, units []WorkUnit) (map[string]WorkState, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	states := make(map[string]WorkState, len(units))
	for _, unit := range units {
		states[unit.Key] = WorkState{Ready: store.units[unit.Key].ready, CompletedAt: store.units[unit.Key].completedAt}
	}
	return states, nil
}

func (store *memoryStore) Claim(_ context.Context, units []WorkUnit, token string, _ time.Duration, force bool) ([]WorkUnit, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	claimed := make([]WorkUnit, 0, len(units))
	for _, unit := range units {
		state := store.units[unit.Key]
		if (!force && state.ready) || state.lease != "" {
			continue
		}
		state.lease = token
		store.units[unit.Key] = state
		claimed = append(claimed, unit)
	}
	return claimed, nil
}

func (store *memoryStore) Complete(
	_ context.Context,
	token string,
	units []WorkUnit,
	_ FetchRequest,
	result FetchResult,
	_ time.Duration,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	series := make(map[SeriesID]Series, len(units))
	for _, unit := range units {
		state := store.units[unit.Key]
		if state.lease != token {
			return errors.New("lost lease")
		}
		state.ready = true
		state.lease = ""
		state.completedAt = time.Now()
		store.units[unit.Key] = state
		series[unit.Series.ID] = unit.Series
	}
	for _, item := range result.Observations {
		definition := series[item.SeriesID]
		key := string(item.SeriesID) + ":" + definition.Base + ":" + definition.Quote
		store.observations[key] = append(store.observations[key], Observation{
			SeriesID: item.SeriesID,
			Base:     definition.Base,
			Quote:    definition.Quote,
			Date:     item.Date,
			Value:    item.Value,
		})
	}
	return nil
}

func (store *memoryStore) Fail(_ context.Context, token string, units []WorkUnit, _ string, _ time.Duration) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.failures++
	for _, unit := range units {
		state := store.units[unit.Key]
		if state.lease != token {
			return errors.New("lost lease")
		}
		state.lease = ""
		store.units[unit.Key] = state
	}
	return nil
}

func (store *memoryStore) LatestObservations(_ context.Context, series []Series) ([]Observation, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	result := make([]Observation, 0, len(series))
	for _, definition := range series {
		values := store.observations[seriesKey(definition)]
		if len(values) > 0 {
			result = append(result, values[len(values)-1])
		}
	}
	return result, nil
}

func (store *memoryStore) RangeObservations(_ context.Context, series []Series, from, to time.Time) ([]Observation, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	result := make([]Observation, 0)
	for _, definition := range series {
		for _, observation := range store.observations[seriesKey(definition)] {
			if !observation.Date.Before(from) && !observation.Date.After(to) {
				result = append(result, observation)
			}
		}
	}
	return result, nil
}

func seriesKey(series Series) string {
	return string(series.ID) + ":" + series.Base + ":" + series.Quote
}

func (store *memoryStore) ProviderBlockedUntil(context.Context) (time.Time, error) {
	return store.blockedUntil, nil
}

func (store *memoryStore) BlockProviderUntil(_ context.Context, until time.Time) error {
	store.blockedUntil = until
	return nil
}

func testSeries(index int) Series {
	return Series{ID: SeriesID(fmt.Sprintf("SF%d", 43718+index)), Base: "USD", Quote: "MXN"}
}

func mustDate(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func testService(t *testing.T, provider Provider, store Store, now time.Time) *Service {
	t.Helper()
	service, err := NewService(provider, store, Config{
		FreshFor:   time.Hour,
		LeaseFor:   time.Minute,
		RetryAfter: time.Minute,
		PollEvery:  time.Millisecond,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLatestCachesAndCanonicalizesSeries(t *testing.T) {
	t.Parallel()
	provider := &providerFake{}
	store := newMemoryStore()
	service := testService(t, provider, store, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	first := testSeries(0)
	second := testSeries(1)
	observations, err := service.Latest(context.Background(), []Series{second, first, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 || len(provider.requests) != 1 {
		t.Fatalf("observations/requests = %d/%d", len(observations), len(provider.requests))
	}
	if provider.requests[0].SeriesIDs[0] != first.ID || provider.requests[0].SeriesIDs[1] != second.ID {
		t.Fatalf("request IDs = %v", provider.requests[0].SeriesIDs)
	}
	if _, err := service.Latest(context.Background(), []Series{first, second}); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("cached latest made %d requests", len(provider.requests))
	}
}

func TestLatestDoesNotShareCoverageAcrossCurrencyMappings(t *testing.T) {
	t.Parallel()
	provider := &providerFake{}
	store := newMemoryStore()
	service := testService(t, provider, store, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	dollars := testSeries(0)
	euros := dollars
	euros.Base = "EUR"

	for _, series := range []Series{dollars, euros} {
		observations, err := service.Latest(context.Background(), []Series{series})
		if err != nil || len(observations) != 1 || observations[0].Base != series.Base {
			t.Fatalf("latest for %s = %#v, %v", series.Base, observations, err)
		}
	}
	if len(provider.requests) != 2 {
		t.Fatalf("distinct currency mappings made %d requests", len(provider.requests))
	}
}

func TestLatestReturnsStaleDataWhileAnotherFetchOwnsLease(t *testing.T) {
	t.Parallel()
	provider := &providerFake{}
	store := newMemoryStore()
	series := testSeries(0)
	store.units[latestWorkKey(series)] = memoryUnit{lease: "other"}
	store.observations[seriesKey(series)] = []Observation{{SeriesID: series.ID, Base: series.Base, Quote: series.Quote, Value: decimal.NewFromInt(19)}}
	service := testService(t, provider, store, time.Now())
	observations, err := service.Latest(context.Background(), []Series{series})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || !observations[0].Stale || len(provider.requests) != 0 {
		t.Fatalf("stale result = %#v, requests = %d", observations, len(provider.requests))
	}
}

func TestRangeUsesMonthlyCoverageAndTwentySeriesBatches(t *testing.T) {
	t.Parallel()
	provider := &providerFake{}
	store := newMemoryStore()
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	service := testService(t, provider, store, now)
	series := make([]Series, 21)
	for index := range series {
		series[index] = testSeries(index)
	}
	from := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	observations, err := service.Range(context.Background(), series, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("provider requests = %d", len(provider.requests))
	}
	for _, request := range provider.requests {
		if len(request.SeriesIDs) > MaxSeriesPerRequest || request.Kind != FetchRange {
			t.Fatalf("invalid batch = %#v", request)
		}
	}
	if len(observations) != 21 {
		t.Fatalf("filtered observations = %d", len(observations))
	}
	if _, err := service.Range(context.Background(), series, from, to); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("persisted range made %d requests", len(provider.requests))
	}
}

func TestFailuresAndValidation(t *testing.T) {
	t.Parallel()
	series := testSeries(0)
	for _, invalid := range []Series{
		{},
		{ID: "bad", Base: "USD", Quote: "MXN"},
		{ID: series.ID, Base: "usd", Quote: "MXN"},
		{ID: series.ID, Base: "USD", Quote: "USD"},
	} {
		if invalid.Validate() == nil {
			t.Fatalf("valid series = %#v", invalid)
		}
	}
	if _, err := NewService(nil, newMemoryStore(), Config{}); err == nil {
		t.Fatal("nil provider accepted")
	}
	if _, err := NewService(&providerFake{}, nil, Config{}); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := NewService(&providerFake{}, newMemoryStore(), Config{FreshFor: time.Second, LeaseFor: time.Second}); err == nil {
		t.Fatal("invalid durations accepted")
	}
	service := testService(t, &providerFake{}, newMemoryStore(), time.Now())
	if _, err := service.Latest(context.Background(), nil); !errors.Is(err, ErrInvalidSeries) {
		t.Fatalf("empty latest error = %v", err)
	}
	if _, err := service.Latest(context.Background(), []Series{{}}); !errors.Is(err, ErrInvalidSeries) {
		t.Fatalf("invalid latest series error = %v", err)
	}
	conflict := series
	conflict.Quote = "EUR"
	if _, err := service.Latest(context.Background(), []Series{series, conflict}); !errors.Is(err, ErrInvalidSeries) {
		t.Fatalf("conflicting series error = %v", err)
	}
	if _, err := service.Range(context.Background(), []Series{series}, time.Now(), time.Now().Add(-24*time.Hour)); !errors.Is(err, ErrInvalidDateRange) {
		t.Fatalf("invalid range error = %v", err)
	}

	providerErr := errors.New("failed")
	provider := &providerFake{err: providerErr}
	store := newMemoryStore()
	service = testService(t, provider, store, time.Now())
	if _, err := service.Latest(context.Background(), []Series{series}); !errors.Is(err, providerErr) || store.failures != 1 {
		t.Fatalf("provider failure = %v, failures = %d", err, store.failures)
	}

	retryAt := time.Now().Add(time.Minute)
	provider = &providerFake{err: &RateLimitError{RetryAt: retryAt}}
	store = newMemoryStore()
	service = testService(t, provider, store, time.Now())
	if _, err := service.Latest(context.Background(), []Series{series}); err == nil || !store.blockedUntil.Equal(retryAt) {
		t.Fatalf("rate limit = %v, blocked until = %v", err, store.blockedUntil)
	}
}

func TestColdLatestWaitHonorsContext(t *testing.T) {
	t.Parallel()
	series := testSeries(0)
	store := newMemoryStore()
	store.units[latestWorkKey(series)] = memoryUnit{lease: "other"}
	service := testService(t, &providerFake{}, store, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Latest(ctx, []Series{series})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cold latest cancellation = %v", err)
	}
}

func TestRangeWaitHonorsContext(t *testing.T) {
	t.Parallel()
	series := testSeries(0)
	store := newMemoryStore()
	unit := rangeWorkKey(series, mustDate(t, "2026-08-01"))
	store.units[unit] = memoryUnit{lease: "other"}
	service := testService(t, &providerFake{}, store, time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Range(ctx, []Series{series}, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled range error = %v", err)
	}
}

func TestRevalidateRangeAndProviderCooldown(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	series := testSeries(0)
	provider := &providerFake{}
	store := newMemoryStore()
	service := testService(t, provider, store, now)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	if _, err := service.Range(context.Background(), []Series{series}, from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RevalidateRange(context.Background(), []Series{series}, from, to); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("requests after revalidation = %d", len(provider.requests))
	}
	if _, err := service.RevalidateRange(context.Background(), nil, from, to); !errors.Is(err, ErrInvalidSeries) {
		t.Fatalf("invalid revalidation series error = %v", err)
	}
	if _, err := service.RevalidateRange(context.Background(), []Series{series}, to, from); !errors.Is(err, ErrInvalidDateRange) {
		t.Fatalf("invalid revalidation range error = %v", err)
	}

	blockedStore := newMemoryStore()
	blockedStore.blockedUntil = now.Add(time.Minute)
	blockedProvider := &providerFake{}
	blockedService := testService(t, blockedProvider, blockedStore, now)
	_, err := blockedService.Latest(context.Background(), []Series{series})
	var limited *RateLimitError
	if !errors.As(err, &limited) || len(blockedProvider.requests) != 0 || blockedStore.failures != 1 {
		t.Fatalf("blocked provider = %v, requests/failures = %d/%d", err, len(blockedProvider.requests), blockedStore.failures)
	}

	incomplete := providerFunc(func(context.Context, FetchRequest) (FetchResult, error) {
		return FetchResult{Response: json.RawMessage(`{}`)}, nil
	})
	_, err = testService(t, incomplete, newMemoryStore(), now).Latest(context.Background(), []Series{series})
	if !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("incomplete latest error = %v", err)
	}
}

func TestProviderFailureReleasesUnattemptedBatchLeases(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	series := testSeries(0)
	calls := 0
	marker := errors.New("upstream failed")
	provider := providerFunc(func(_ context.Context, request FetchRequest) (FetchResult, error) {
		calls++
		if calls == 2 {
			return FetchResult{}, marker
		}
		return FetchResult{
			Response: json.RawMessage(`{}`),
			Observations: []ProviderObservation{{
				SeriesID: series.ID,
				Date:     request.To,
				Value:    decimal.NewFromInt(19),
			}},
		}, nil
	})
	store := newMemoryStore()
	service := testService(t, provider, store, now)
	_, err := service.Range(
		context.Background(),
		[]Series{series},
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
	)
	if !errors.Is(err, marker) || calls != 2 {
		t.Fatalf("range error/calls = %v/%d", err, calls)
	}
	for key, state := range store.units {
		if state.lease != "" {
			t.Fatalf("work unit %q retained lease %q", key, state.lease)
		}
	}
}

type providerFunc func(context.Context, FetchRequest) (FetchResult, error)

func (function providerFunc) Fetch(ctx context.Context, request FetchRequest) (FetchResult, error) {
	return function(ctx, request)
}

type failingStore struct {
	Store
	statesErr        error
	claimErr         error
	completeErr      error
	failErr          error
	latestErr        error
	rangeErr         error
	blockedErr       error
	blockProviderErr error
}

func (store *failingStore) States(ctx context.Context, units []WorkUnit) (map[string]WorkState, error) {
	if store.statesErr != nil {
		return nil, store.statesErr
	}
	return store.Store.States(ctx, units)
}

func (store *failingStore) Claim(ctx context.Context, units []WorkUnit, token string, lease time.Duration, force bool) ([]WorkUnit, error) {
	if store.claimErr != nil {
		return nil, store.claimErr
	}
	return store.Store.Claim(ctx, units, token, lease, force)
}

func (store *failingStore) Complete(ctx context.Context, token string, units []WorkUnit, request FetchRequest, result FetchResult, fresh time.Duration) error {
	if store.completeErr != nil {
		return store.completeErr
	}
	return store.Store.Complete(ctx, token, units, request, result, fresh)
}

func (store *failingStore) Fail(ctx context.Context, token string, units []WorkUnit, errorClass string, retry time.Duration) error {
	if store.failErr != nil {
		return store.failErr
	}
	return store.Store.Fail(ctx, token, units, errorClass, retry)
}

func (store *failingStore) LatestObservations(ctx context.Context, series []Series) ([]Observation, error) {
	if store.latestErr != nil {
		return nil, store.latestErr
	}
	return store.Store.LatestObservations(ctx, series)
}

func (store *failingStore) RangeObservations(ctx context.Context, series []Series, from, to time.Time) ([]Observation, error) {
	if store.rangeErr != nil {
		return nil, store.rangeErr
	}
	return store.Store.RangeObservations(ctx, series, from, to)
}

func (store *failingStore) ProviderBlockedUntil(ctx context.Context) (time.Time, error) {
	if store.blockedErr != nil {
		return time.Time{}, store.blockedErr
	}
	return store.Store.ProviderBlockedUntil(ctx)
}

func (store *failingStore) BlockProviderUntil(ctx context.Context, until time.Time) error {
	if store.blockProviderErr != nil {
		return store.blockProviderErr
	}
	return store.Store.BlockProviderUntil(ctx, until)
}

func TestServicePropagatesStoreFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	series := testSeries(0)
	marker := errors.New("store failed")
	provider := &providerFake{}
	tests := []struct {
		name  string
		store *failingStore
		run   func(*Service) error
	}{
		{
			name:  "states",
			store: &failingStore{Store: newMemoryStore(), statesErr: marker},
			run: func(service *Service) error {
				_, err := service.Range(context.Background(), []Series{series}, now.AddDate(0, -1, 0), now.AddDate(0, -1, 1))
				return err
			},
		},
		{
			name:  "claim",
			store: &failingStore{Store: newMemoryStore(), claimErr: marker},
			run: func(service *Service) error {
				_, err := service.Latest(context.Background(), []Series{series})
				return err
			},
		},
		{
			name:  "provider block lookup",
			store: &failingStore{Store: newMemoryStore(), blockedErr: marker},
			run: func(service *Service) error {
				_, err := service.Latest(context.Background(), []Series{series})
				return err
			},
		},
		{
			name:  "complete",
			store: &failingStore{Store: newMemoryStore(), completeErr: marker},
			run: func(service *Service) error {
				_, err := service.Latest(context.Background(), []Series{series})
				return err
			},
		},
		{
			name:  "fail",
			store: &failingStore{Store: newMemoryStore(), failErr: marker},
			run: func(service *Service) error {
				service.provider = providerFunc(func(context.Context, FetchRequest) (FetchResult, error) { return FetchResult{}, errors.New("upstream") })
				_, err := service.Latest(context.Background(), []Series{series})
				return err
			},
		},
		{
			name:  "block provider",
			store: &failingStore{Store: newMemoryStore(), blockProviderErr: marker},
			run: func(service *Service) error {
				service.provider = providerFunc(func(context.Context, FetchRequest) (FetchResult, error) {
					return FetchResult{}, &RateLimitError{RetryAt: now.Add(time.Minute)}
				})
				_, err := service.Latest(context.Background(), []Series{series})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := testService(t, provider, test.store, now)
			if err := test.run(service); !errors.Is(err, marker) {
				t.Fatalf("error = %v", err)
			}
		})
	}

	readyLatest := newMemoryStore()
	readyLatest.units[latestWorkKey(series)] = memoryUnit{ready: true}
	latestStore := &failingStore{Store: readyLatest, latestErr: marker}
	if _, err := testService(t, provider, latestStore, now).Latest(context.Background(), []Series{series}); !errors.Is(err, marker) {
		t.Fatalf("latest load error = %v", err)
	}

	readyRange := newMemoryStore()
	readyRange.units[rangeWorkKey(series, mustDate(t, "2026-08-01"))] = memoryUnit{ready: true}
	rangeStore := &failingStore{Store: readyRange, rangeErr: marker}
	if _, err := testService(t, provider, rangeStore, now).Range(
		context.Background(), []Series{series},
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
	); !errors.Is(err, marker) {
		t.Fatalf("range load error = %v", err)
	}
}

func TestDefaultsRateLimitTextAndFutureRange(t *testing.T) {
	t.Parallel()
	service, err := NewService(&providerFake{}, newMemoryStore(), Config{})
	if err != nil || service.config.FreshFor != 15*time.Minute || service.config.LeaseFor != 10*time.Second || service.config.Now == nil {
		t.Fatalf("default service = %#v, %v", service, err)
	}
	limited := &RateLimitError{}
	if limited.Error() == "" {
		t.Fatal("rate-limit error text is empty")
	}
	if _, err := service.Range(context.Background(), []Series{testSeries(0)}, time.Now().AddDate(1, 0, 0), time.Now().AddDate(1, 0, 1)); err != nil {
		t.Fatalf("future empty range = %v", err)
	}
}

func TestConcurrentForcedRefreshUsesOtherCompletion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	series := testSeries(0)
	store := newMemoryStore()
	key := rangeWorkKey(series, mustDate(t, "2026-08-01"))
	baseline := now.Add(-time.Hour)
	store.units[key] = memoryUnit{ready: true, lease: "other", completedAt: baseline}
	service := testService(t, &providerFake{}, store, now)
	go func() {
		time.Sleep(5 * time.Millisecond)
		store.mutex.Lock()
		store.units[key] = memoryUnit{ready: true, completedAt: now}
		store.mutex.Unlock()
	}()
	result, err := service.RevalidateRange(
		context.Background(),
		[]Series{series},
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
	)
	if err != nil || len(result) != 0 {
		t.Fatalf("concurrent revalidation = %#v, %v", result, err)
	}

	marker := errors.New("forced state failed")
	failing := &failingStore{Store: newMemoryStore(), statesErr: marker}
	if _, err := testService(t, &providerFake{}, failing, now).RevalidateRange(
		context.Background(), []Series{series},
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
	); !errors.Is(err, marker) {
		t.Fatalf("forced state error = %v", err)
	}
	failing = &failingStore{Store: newMemoryStore(), claimErr: marker}
	if _, err := testService(t, &providerFake{}, failing, now).RevalidateRange(
		context.Background(), []Series{series},
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
	); !errors.Is(err, marker) {
		t.Fatalf("forced claim error = %v", err)
	}
}
