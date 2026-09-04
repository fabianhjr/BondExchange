package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/internal/exchangerates"
	"github.com/shopspring/decimal"
)

type rateProvider struct {
	mutex   sync.Mutex
	calls   int
	value   decimal.Decimal
	entered chan struct{}
	release chan struct{}
}

func (provider *rateProvider) Fetch(ctx context.Context, request exchangerates.FetchRequest) (exchangerates.FetchResult, error) {
	provider.mutex.Lock()
	provider.calls++
	call := provider.calls
	value := provider.value
	entered := provider.entered
	release := provider.release
	provider.mutex.Unlock()
	if call == 1 && entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return exchangerates.FetchResult{}, ctx.Err()
		}
	}
	date := request.To
	if request.Kind == exchangerates.FetchLatest {
		date = time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	}
	observations := make([]exchangerates.ProviderObservation, 0, len(request.SeriesIDs))
	for _, seriesID := range request.SeriesIDs {
		observations = append(observations, exchangerates.ProviderObservation{SeriesID: seriesID, Date: date, Value: value})
	}
	body := json.RawMessage(fmt.Sprintf(`{"call":%d,"value":%q}`, call, value.String()))
	return exchangerates.FetchResult{Response: body, Observations: observations}, nil
}

func (provider *rateProvider) callCount() int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.calls
}

func (provider *rateProvider) setValue(value string) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.value = decimal.RequireFromString(value)
}

func newRateService(t *testing.T, provider exchangerates.Provider, store exchangerates.Store) *exchangerates.Service {
	t.Helper()
	service, err := exchangerates.NewService(provider, store, exchangerates.Config{
		FreshFor:   time.Hour,
		LeaseFor:   time.Minute,
		RetryAfter: time.Millisecond,
		PollEvery:  time.Millisecond,
		Now: func() time.Time {
			return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func uniqueRateSeries(t *testing.T) exchangerates.Series {
	t.Helper()
	return exchangerates.Series{
		ID:    exchangerates.SeriesID(fmt.Sprintf("SF%d", time.Now().UnixNano())),
		Base:  "USD",
		Quote: "MXN",
	}
}

func TestSIELatestDeduplicatesAcrossPools(t *testing.T) {
	firstPool := openTestPool(t)
	secondPool := openTestPool(t)
	provider := &rateProvider{
		value:   decimal.RequireFromString("19.8765"),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	first := newRateService(t, provider, NewStore(firstPool))
	second := newRateService(t, provider, NewStore(secondPool))
	series := uniqueRateSeries(t)

	type result struct {
		observations []exchangerates.Observation
		err          error
	}
	results := make(chan result, 2)
	go func() {
		observations, err := first.Latest(context.Background(), []exchangerates.Series{series})
		results <- result{observations: observations, err: err}
	}()
	<-provider.entered
	go func() {
		observations, err := second.Latest(context.Background(), []exchangerates.Series{series})
		results <- result{observations: observations, err: err}
	}()
	close(provider.release)
	for range 2 {
		result := <-results
		if result.err != nil || len(result.observations) != 1 || result.observations[0].Value.String() != "19.8765" {
			t.Fatalf("latest result = %#v, %v", result.observations, result.err)
		}
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d", provider.callCount())
	}
	var imports, observations int
	if err := firstPool.QueryRow(context.Background(), `
SELECT
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_imports WHERE $1 = ANY(series_ids)),
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_observations WHERE series_id = $1)`, series.ID).Scan(&imports, &observations); err != nil {
		t.Fatal(err)
	}
	if imports != 1 || observations != 1 {
		t.Fatalf("imports/observations = %d/%d", imports, observations)
	}
}

func TestSIEHistoricalCoverageAndCorrectionsPersist(t *testing.T) {
	pool := openTestPool(t)
	provider := &rateProvider{value: decimal.RequireFromString("18.25")}
	service := newRateService(t, provider, NewStore(pool))
	series := uniqueRateSeries(t)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	first, err := service.Range(context.Background(), []exchangerates.Series{series}, from, to)
	if err != nil || len(first) != 1 || first[0].Value.String() != "18.25" {
		t.Fatalf("first historical result = %#v, %v", first, err)
	}
	if _, err := service.Range(context.Background(), []exchangerates.Series{series}, from, to); err != nil {
		t.Fatal(err)
	}
	if provider.callCount() != 1 {
		t.Fatalf("persisted range made %d calls", provider.callCount())
	}

	provider.setValue("18.5")
	current, err := service.RevalidateRange(context.Background(), []exchangerates.Series{series}, from, to)
	if err != nil || len(current) != 1 || current[0].Value.String() != "18.5" {
		t.Fatalf("revalidated result = %#v, %v", current, err)
	}
	var imports, revisions int
	if err := pool.QueryRow(context.Background(), `
SELECT
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_imports WHERE $1 = ANY(series_ids)),
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_observations WHERE series_id = $1)`, series.ID).Scan(&imports, &revisions); err != nil {
		t.Fatal(err)
	}
	if imports != 2 || revisions != 2 {
		t.Fatalf("imports/revisions = %d/%d", imports, revisions)
	}

	provider.setValue("18.25")
	reverted, err := service.RevalidateRange(context.Background(), []exchangerates.Series{series}, from, to)
	if err != nil || len(reverted) != 1 || reverted[0].Value.String() != "18.25" {
		t.Fatalf("reverted result = %#v, %v", reverted, err)
	}
	if err := pool.QueryRow(context.Background(), `
SELECT
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_imports WHERE $1 = ANY(series_ids)),
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_observations WHERE series_id = $1)`, series.ID).Scan(&imports, &revisions); err != nil {
		t.Fatal(err)
	}
	if imports != 3 || revisions != 3 {
		t.Fatalf("reverted imports/revisions = %d/%d", imports, revisions)
	}
}

func TestSIEStoreLeaseFailureCooldownAndDuplicatePersistence(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	series := uniqueRateSeries(t)
	unit := exchangerates.WorkUnit{
		Key:    "latest:" + string(series.ID) + ":" + series.Base + ":" + series.Quote,
		Kind:   exchangerates.FetchLatest,
		Series: series,
	}
	ctx := context.Background()
	states, err := store.States(ctx, []exchangerates.WorkUnit{unit})
	if err != nil || states[unit.Key].Ready {
		t.Fatalf("initial state = %#v, %v", states, err)
	}
	claimed, err := store.Claim(ctx, []exchangerates.WorkUnit{unit}, "lease-one", time.Minute, false)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("first claim = %#v, %v", claimed, err)
	}
	states, err = store.States(ctx, []exchangerates.WorkUnit{unit})
	if err != nil || states[unit.Key].LeaseUntil.IsZero() {
		t.Fatalf("leased state = %#v, %v", states, err)
	}
	claimed, err = store.Claim(ctx, []exchangerates.WorkUnit{unit}, "lease-two", time.Minute, false)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("overlapping claim = %#v, %v", claimed, err)
	}
	if err := store.Fail(ctx, "wrong", []exchangerates.WorkUnit{unit}, "provider_error", 0); err == nil {
		t.Fatal("failure with wrong lease succeeded")
	}
	if err := store.Fail(ctx, "lease-one", []exchangerates.WorkUnit{unit}, "provider_error", 0); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.Claim(ctx, []exchangerates.WorkUnit{unit}, "lease-three", time.Minute, false)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim after failure = %#v, %v", claimed, err)
	}
	request := exchangerates.FetchRequest{Kind: exchangerates.FetchLatest, SeriesIDs: []exchangerates.SeriesID{series.ID}}
	result := exchangerates.FetchResult{
		Response: json.RawMessage(`{"bmx":{"series":[]}}`),
		Observations: []exchangerates.ProviderObservation{{
			SeriesID: series.ID,
			Date:     time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC),
			Value:    decimal.RequireFromString("19.25"),
		}},
	}
	if err := store.Complete(ctx, "wrong", claimed, request, result, time.Hour); err == nil {
		t.Fatal("completion with wrong lease succeeded")
	}
	if err := store.Complete(ctx, "lease-three", claimed, request, result, time.Hour); err != nil {
		t.Fatal(err)
	}
	states, err = store.States(ctx, []exchangerates.WorkUnit{unit})
	if err != nil || !states[unit.Key].Ready || states[unit.Key].CompletedAt.IsZero() {
		t.Fatalf("completed state = %#v, %v", states, err)
	}
	latest, err := store.LatestObservations(ctx, []exchangerates.Series{series})
	if err != nil || len(latest) != 1 || latest[0].Value.String() != "19.25" {
		t.Fatalf("latest = %#v, %v", latest, err)
	}
	missing, err := store.LatestObservations(ctx, []exchangerates.Series{uniqueRateSeries(t)})
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing latest = %#v, %v", missing, err)
	}

	claimed, err = store.Claim(ctx, []exchangerates.WorkUnit{unit}, "lease-four", time.Minute, true)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("forced claim = %#v, %v", claimed, err)
	}
	if err := store.Complete(ctx, "lease-four", claimed, request, result, time.Hour); err != nil {
		t.Fatal(err)
	}
	var imports, observations int
	if err := pool.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_imports WHERE $1 = ANY(series_ids)),
  (SELECT count(*) FROM bond_exchange.sie_exchange_rate_observations WHERE series_id = $1)`, series.ID).Scan(&imports, &observations); err != nil {
		t.Fatal(err)
	}
	if imports != 2 || observations != 1 {
		t.Fatalf("duplicate import/observation counts = %d/%d", imports, observations)
	}

	blockedUntil := time.Now().Add(time.Minute).UTC()
	if err := store.BlockProviderUntil(ctx, blockedUntil); err != nil {
		t.Fatal(err)
	}
	blocked, err := store.ProviderBlockedUntil(ctx)
	if err != nil || blocked.Sub(blockedUntil).Abs() > time.Microsecond {
		t.Fatalf("provider block = %v, %v", blocked, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
UPDATE bond_exchange.sie_provider_state
SET blocked_until = NULL
WHERE provider_id = 'banxico-sie'`)
	})
}

func TestSIEStoreRejectsInvalidCompletionAndPropagatesClosedPoolErrors(t *testing.T) {
	pool := openTestPool(t)
	store := NewStore(pool)
	series := uniqueRateSeries(t)
	unit := exchangerates.WorkUnit{
		Key:    "latest:" + string(series.ID) + ":" + series.Base + ":" + series.Quote,
		Kind:   exchangerates.FetchLatest,
		Series: series,
	}
	request := exchangerates.FetchRequest{Kind: exchangerates.FetchLatest, SeriesIDs: []exchangerates.SeriesID{series.ID}}
	validResult := exchangerates.FetchResult{
		Response: json.RawMessage(`{}`),
		Observations: []exchangerates.ProviderObservation{{
			SeriesID: series.ID,
			Date:     time.Now(),
			Value:    decimal.NewFromInt(19),
		}},
	}
	if err := store.Complete(context.Background(), "lease", nil, request, validResult, time.Hour); !errors.Is(err, exchangerates.ErrInvalidObservation) {
		t.Fatalf("empty completion error = %v", err)
	}
	invalidJSON := validResult
	invalidJSON.Response = json.RawMessage(`{`)
	if err := store.Complete(context.Background(), "lease", []exchangerates.WorkUnit{unit}, request, invalidJSON, time.Hour); !errors.Is(err, exchangerates.ErrInvalidObservation) {
		t.Fatalf("invalid JSON error = %v", err)
	}
	claimed, err := store.Claim(context.Background(), []exchangerates.WorkUnit{unit}, "lease", time.Minute, false)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("invalid-completion claim = %#v, %v", claimed, err)
	}
	unknownSeries := validResult
	unknownSeries.Observations = []exchangerates.ProviderObservation{{
		SeriesID: "SF999999",
		Date:     time.Now(),
		Value:    decimal.NewFromInt(19),
	}}
	if err := store.Complete(context.Background(), "lease", claimed, request, unknownSeries, time.Hour); !errors.Is(err, exchangerates.ErrInvalidObservation) {
		t.Fatalf("unknown observation series error = %v", err)
	}
	invalidValue := validResult
	invalidValue.Observations[0].Value = decimal.Zero
	if err := store.Complete(context.Background(), "lease", claimed, request, invalidValue, time.Hour); !errors.Is(err, exchangerates.ErrInvalidObservation) {
		t.Fatalf("invalid observation value error = %v", err)
	}
	if err := store.Fail(context.Background(), "lease", claimed, "invalid_test", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := scanRate(rowScannerFunc(func(...any) error { return errors.New("scan") })); err == nil {
		t.Fatal("scan error was lost")
	}
	if _, err := scanRate(rowScannerFunc(func(destinations ...any) error {
		*destinations[4].(*string) = "invalid" //nolint:forcetypeassert // scanRate controls this destination's concrete type.
		return nil
	})); !errors.Is(err, exchangerates.ErrInvalidObservation) {
		t.Fatalf("invalid persisted decimal error = %v", err)
	}

	pool.Close()
	ctx := context.Background()
	if _, err := store.States(ctx, []exchangerates.WorkUnit{unit}); err == nil {
		t.Fatal("States succeeded with closed pool")
	}
	if _, err := store.Claim(ctx, []exchangerates.WorkUnit{unit}, "lease", time.Minute, false); err == nil {
		t.Fatal("Claim succeeded with closed pool")
	}
	if err := store.Complete(ctx, "lease", []exchangerates.WorkUnit{unit}, request, validResult, time.Hour); err == nil {
		t.Fatal("Complete succeeded with closed pool")
	}
	if err := store.Fail(ctx, "lease", []exchangerates.WorkUnit{unit}, "error", time.Minute); err == nil {
		t.Fatal("Fail succeeded with closed pool")
	}
	if _, err := store.LatestObservations(ctx, []exchangerates.Series{series}); err == nil {
		t.Fatal("LatestObservations succeeded with closed pool")
	}
	if _, err := store.RangeObservations(ctx, []exchangerates.Series{series}, time.Now(), time.Now()); err == nil {
		t.Fatal("RangeObservations succeeded with closed pool")
	}
	if _, err := store.ProviderBlockedUntil(ctx); err == nil {
		t.Fatal("ProviderBlockedUntil succeeded with closed pool")
	}
	if err := store.BlockProviderUntil(ctx, time.Now()); err == nil {
		t.Fatal("BlockProviderUntil succeeded with closed pool")
	}
}
