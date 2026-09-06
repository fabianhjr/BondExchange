package exchangerates

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/telemetry"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
)

type Provider interface {
	Fetch(context.Context, FetchRequest) (FetchResult, error)
}

type WorkUnit struct {
	Key       string
	Kind      FetchKind
	Series    Series
	From      time.Time
	To        time.Time
	FetchTo   time.Time
	Permanent bool
}

type WorkState struct {
	Ready       bool
	LeaseUntil  time.Time
	CompletedAt time.Time
}

type Store interface {
	States(context.Context, []WorkUnit) (map[string]WorkState, error)
	Claim(context.Context, []WorkUnit, string, time.Duration, bool) ([]WorkUnit, error)
	Complete(context.Context, string, []WorkUnit, FetchRequest, FetchResult, time.Duration) error
	Fail(context.Context, string, []WorkUnit, string, time.Duration) error
	LatestObservations(context.Context, []Series) ([]Observation, error)
	RangeObservations(context.Context, []Series, time.Time, time.Time) ([]Observation, error)
	ProviderBlockedUntil(context.Context) (time.Time, error)
	BlockProviderUntil(context.Context, time.Time) error
}

type Config struct {
	FreshFor   time.Duration
	LeaseFor   time.Duration
	RetryAfter time.Duration
	PollEvery  time.Duration
	Now        func() time.Time
}

type Service struct {
	provider Provider
	store    Store
	config   Config
}

func NewService(provider Provider, store Store, config Config) (*Service, error) {
	if provider == nil || store == nil {
		return nil, errors.New("exchange-rate provider and store are required")
	}
	if config.FreshFor <= 0 {
		config.FreshFor = 15 * time.Minute
	}
	if config.LeaseFor <= 0 {
		config.LeaseFor = 10 * time.Second
	}
	if config.RetryAfter <= 0 {
		config.RetryAfter = time.Minute
	}
	if config.PollEvery <= 0 {
		config.PollEvery = 25 * time.Millisecond
	}
	if config.LeaseFor >= config.FreshFor {
		return nil, errors.New("exchange-rate lease must be shorter than freshness period")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{provider: provider, store: store, config: config}, nil
}

func (service *Service) Latest(ctx context.Context, requested []Series) ([]Observation, error) {
	series, err := normalizeSeries(requested)
	if err != nil {
		return nil, err
	}
	units := make([]WorkUnit, 0, len(series))
	for _, item := range series {
		units = append(units, WorkUnit{Key: latestWorkKey(item), Kind: FetchLatest, Series: item})
	}
	for {
		err = service.resolve(ctx, units)
		if err == nil {
			break
		}
		observations, loadErr := service.store.LatestObservations(ctx, series)
		if loadErr == nil && len(observations) == len(series) {
			states, stateErr := service.store.States(ctx, units)
			if stateErr == nil {
				for index := range observations {
					item := Series{
						ID:    observations[index].SeriesID,
						Base:  observations[index].Base,
						Quote: observations[index].Quote,
					}
					observations[index].Stale = !states[latestWorkKey(item)].Ready
				}
				return observations, nil
			}
		}
		if !errors.Is(err, ErrColdFetchInProgress) {
			return nil, err
		}
		if err := waitForPoll(ctx, service.config.PollEvery); err != nil {
			return nil, err
		}
	}
	return service.store.LatestObservations(ctx, series)
}

func (service *Service) Range(
	ctx context.Context,
	requested []Series,
	from time.Time,
	to time.Time,
) ([]Observation, error) {
	series, err := normalizeSeries(requested)
	if err != nil {
		return nil, err
	}
	from = dateOnly(from)
	to = dateOnly(to)
	if from.After(to) {
		return nil, ErrInvalidDateRange
	}
	units := service.rangeUnits(series, from, to)
	if err := service.resolve(ctx, units); err != nil {
		return nil, err
	}
	return service.store.RangeObservations(ctx, series, from, to)
}

func (service *Service) RevalidateRange(
	ctx context.Context,
	requested []Series,
	from time.Time,
	to time.Time,
) ([]Observation, error) {
	series, err := normalizeSeries(requested)
	if err != nil {
		return nil, err
	}
	from = dateOnly(from)
	to = dateOnly(to)
	if from.After(to) {
		return nil, ErrInvalidDateRange
	}
	units := service.rangeUnits(series, from, to)
	if err := service.resolveForced(ctx, units); err != nil {
		return nil, err
	}
	return service.store.RangeObservations(ctx, series, from, to)
}

func (service *Service) rangeUnits(series []Series, from time.Time, to time.Time) []WorkUnit {
	today := dateOnly(service.config.Now())
	currentMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	firstMonth := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonth := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
	units := make([]WorkUnit, 0, len(series))
	for month := firstMonth; !month.After(lastMonth); month = month.AddDate(0, 1, 0) {
		monthEnd := month.AddDate(0, 1, -1)
		fetchTo := monthEnd
		if fetchTo.After(today) {
			fetchTo = today
		}
		if fetchTo.Before(month) {
			continue
		}
		for _, item := range series {
			units = append(units, WorkUnit{
				Key:       rangeWorkKey(item, month),
				Kind:      FetchRange,
				Series:    item,
				From:      month,
				To:        monthEnd,
				FetchTo:   fetchTo,
				Permanent: month.Before(currentMonth),
			})
		}
	}
	return units
}

func latestWorkKey(series Series) string {
	return fmt.Sprintf("latest:%s:%s:%s", series.ID, series.Base, series.Quote)
}

func rangeWorkKey(series Series, month time.Time) string {
	return fmt.Sprintf(
		"range:%s:%s:%s:%s",
		series.ID,
		series.Base,
		series.Quote,
		month.Format(time.DateOnly),
	)
}

func (service *Service) resolve(ctx context.Context, units []WorkUnit) error {
	for {
		states, err := service.store.States(ctx, units)
		if err != nil {
			return err
		}
		pending := pendingUnits(units, states)
		if len(pending) == 0 {
			telemetry.RecordRateCache(ctx, "hit")
			return nil
		}
		telemetry.RecordRateCache(ctx, "miss")
		leaseNonce, err := newLeaseNonce()
		if err != nil {
			return err
		}
		claimed, err := service.store.Claim(ctx, pending, leaseNonce, service.config.LeaseFor, false)
		if err != nil {
			return err
		}
		if len(claimed) == 0 {
			telemetry.RecordRateCache(ctx, "lease_contended")
			if allLatest(units) {
				return ErrColdFetchInProgress
			}
			if err := waitForPoll(ctx, service.config.PollEvery); err != nil {
				return err
			}
			continue
		}
		if err := service.fetchClaimed(ctx, leaseNonce, claimed); err != nil {
			return err
		}
	}
}

func (service *Service) resolveForced(ctx context.Context, units []WorkUnit) error {
	baseline, err := service.store.States(ctx, units)
	if err != nil {
		return err
	}
	remaining := append([]WorkUnit(nil), units...)
	for len(remaining) > 0 {
		states, err := service.store.States(ctx, remaining)
		if err != nil {
			return err
		}
		pending := remaining[:0]
		for _, unit := range remaining {
			if states[unit.Key].CompletedAt.After(baseline[unit.Key].CompletedAt) {
				continue
			}
			pending = append(pending, unit)
		}
		remaining = pending
		if len(remaining) == 0 {
			return nil
		}
		leaseNonce, err := newLeaseNonce()
		if err != nil {
			return err
		}
		claimed, err := service.store.Claim(ctx, remaining, leaseNonce, service.config.LeaseFor, true)
		if err != nil {
			return err
		}
		if len(claimed) == 0 {
			if err := waitForPoll(ctx, service.config.PollEvery); err != nil {
				return err
			}
			continue
		}
		if err := service.fetchClaimed(ctx, leaseNonce, claimed); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) fetchClaimed(ctx context.Context, leaseNonce string, units []WorkUnit) error {
	blockedUntil, err := service.store.ProviderBlockedUntil(ctx)
	if err != nil {
		return err
	}
	if blockedUntil.After(service.config.Now()) {
		telemetry.RecordRateFetchSkip(ctx, fetchKindName(units), "rate_limited")
		delay := blockedUntil.Sub(service.config.Now())
		if failErr := service.store.Fail(ctx, leaseNonce, units, "provider_rate_limited", delay); failErr != nil {
			return failErr
		}
		return &RateLimitError{RetryAt: blockedUntil}
	}
	batches := makeBatches(units)
	for index, batch := range batches {
		request := requestForBatch(batch)
		kind := "range"
		if request.Kind == FetchLatest {
			kind = "latest"
		}
		fetchContext, span := telemetry.Start(ctx, "exchange_rate.fetch",
			attribute.String("fetch.kind", kind),
			attribute.Int("fetch.unit.count", len(batch)),
		)
		started := time.Now()
		result, fetchErr := service.provider.Fetch(fetchContext, request)
		if fetchErr == nil && request.Kind == FetchLatest {
			fetchErr = requireLatestObservations(request, result)
		}
		if fetchErr == nil {
			telemetry.RecordRateFetch(fetchContext, kind, "succeeded", len(batch), time.Since(started))
			telemetry.End(span, "")
			if err := service.store.Complete(ctx, leaseNonce, batch, request, result, service.config.FreshFor); err != nil {
				return err
			}
			continue
		}
		delay := service.config.RetryAfter
		errorClass := "provider_error"
		metricOutcome := providerOutcome(fetchErr)
		var limited *RateLimitError
		var blockErr error
		if errors.As(fetchErr, &limited) {
			errorClass = "provider_rate_limited"
			if limited.RetryAt.After(service.config.Now()) {
				delay = limited.RetryAt.Sub(service.config.Now())
			}
			blockErr = service.store.BlockProviderUntil(ctx, limited.RetryAt)
		}
		telemetry.RecordRateFetch(fetchContext, kind, metricOutcome, len(batch), time.Since(started))
		telemetry.End(span, errorClass)
		if blockErr != nil {
			return blockErr
		}
		if err := service.store.Fail(ctx, leaseNonce, remainingUnits(batches[index:]), errorClass, delay); err != nil {
			return err
		}
		return fetchErr
	}
	return nil
}

func fetchKindName(units []WorkUnit) string {
	if allLatest(units) {
		return "latest"
	}
	return "range"
}

func providerOutcome(err error) string {
	var limited *RateLimitError
	switch {
	case errors.As(err, &limited):
		return "rate_limited"
	case errors.Is(err, ErrProviderAuthentication):
		return "authentication_failed"
	case errors.Is(err, ErrProviderInvalidResponse), errors.Is(err, ErrInvalidObservation), errors.Is(err, ErrIncompleteResponse):
		return "invalid_response"
	default:
		return "unavailable"
	}
}

func remainingUnits(batches [][]WorkUnit) []WorkUnit {
	count := 0
	for _, batch := range batches {
		count += len(batch)
	}
	units := make([]WorkUnit, 0, count)
	for _, batch := range batches {
		units = append(units, batch...)
	}
	return units
}

func pendingUnits(units []WorkUnit, states map[string]WorkState) []WorkUnit {
	result := make([]WorkUnit, 0, len(units))
	for _, unit := range units {
		if !states[unit.Key].Ready {
			result = append(result, unit)
		}
	}
	return result
}

func allLatest(units []WorkUnit) bool {
	for _, unit := range units {
		if unit.Kind != FetchLatest {
			return false
		}
	}
	return true
}

func waitForPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func makeBatches(units []WorkUnit) [][]WorkUnit {
	sorted := append([]WorkUnit(nil), units...)
	sort.Slice(sorted, func(left, right int) bool {
		if sorted[left].Kind != sorted[right].Kind {
			return sorted[left].Kind < sorted[right].Kind
		}
		if !sorted[left].From.Equal(sorted[right].From) {
			return sorted[left].From.Before(sorted[right].From)
		}
		if !sorted[left].FetchTo.Equal(sorted[right].FetchTo) {
			return sorted[left].FetchTo.Before(sorted[right].FetchTo)
		}
		return sorted[left].Series.ID < sorted[right].Series.ID
	})
	result := make([][]WorkUnit, 0)
	for len(sorted) > 0 {
		end := 1
		for end < len(sorted) && end < MaxSeriesPerRequest && samePeriod(sorted[0], sorted[end]) {
			end++
		}
		result = append(result, sorted[:end])
		sorted = sorted[end:]
	}
	return result
}

func samePeriod(left, right WorkUnit) bool {
	return left.Kind == right.Kind && left.From.Equal(right.From) && left.FetchTo.Equal(right.FetchTo)
}

func requestForBatch(batch []WorkUnit) FetchRequest {
	request := FetchRequest{Kind: batch[0].Kind, From: batch[0].From, To: batch[0].FetchTo}
	request.SeriesIDs = make([]SeriesID, 0, len(batch))
	for _, unit := range batch {
		request.SeriesIDs = append(request.SeriesIDs, unit.Series.ID)
	}
	return request
}

func requireLatestObservations(request FetchRequest, result FetchResult) error {
	seen := make(map[SeriesID]bool, len(result.Observations))
	for _, observation := range result.Observations {
		seen[observation.SeriesID] = true
	}
	for _, seriesID := range request.SeriesIDs {
		if !seen[seriesID] {
			return ErrIncompleteResponse
		}
	}
	return nil
}

func newLeaseNonce() (string, error) {
	nonce, err := uuid.NewRandom()
	return nonce.String(), err
}
