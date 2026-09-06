package eventing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/telemetry"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
)

const pendingBatchSize = 100

type Dispatcher struct {
	store        Store
	destinations map[string]Publisher
	publishLimit time.Duration
	leasePeriod  time.Duration
}

func NewDispatcher(store Store, destinations []Destination, publishLimit time.Duration) (*Dispatcher, error) {
	if publishLimit <= 0 {
		publishLimit = 5 * time.Second
	}
	registered := make(map[string]Publisher, len(destinations))
	for _, destination := range destinations {
		if destination.ID == "" || destination.Publisher == nil {
			return nil, errorsForConfiguration("destination ID and publisher are required")
		}
		if _, exists := registered[destination.ID]; exists {
			return nil, errorsForConfiguration("destination IDs must be unique")
		}
		registered[destination.ID] = destination.Publisher
	}
	telemetry.RecordEventPublisherCount(context.Background(), len(registered))
	if len(registered) == 0 {
		slog.Warn("no integration-event publisher configured", "event", "integration_event.configuration", "publisher_count", 0)
	}
	return &Dispatcher{
		store:        store,
		destinations: registered,
		publishLimit: publishLimit,
		leasePeriod:  publishLimit + 5*time.Second,
	}, nil
}

func errorsForConfiguration(message string) error {
	return fmt.Errorf("event publisher configuration: %s", message)
}

func (dispatcher *Dispatcher) Publish(ctx context.Context, ref SourceRef) {
	for _, destinationID := range dispatcher.destinationIDs() {
		if _, _, err := dispatcher.publishOne(ctx, destinationID, ref, false); err != nil {
			slog.WarnContext(ctx, "integration event remains pending",
				"event", "integration_event.delivery",
				"destination_id", destinationID,
				"source_table", ref.TableName,
				"source_id", ref.ID,
				"error_type", fmt.Sprintf("%T", err),
			)
		}
	}
}

func (dispatcher *Dispatcher) PublishPending(ctx context.Context, destinationID string) (Summary, error) {
	if len(dispatcher.destinations) == 0 {
		return Summary{}, ErrNoPublishers
	}
	destinations, err := dispatcher.selectedDestinationIDs(destinationID)
	if err != nil {
		return Summary{}, err
	}
	var summary Summary
	for _, selected := range destinations {
		var after SourceRef
		for {
			refs, err := dispatcher.store.PendingEvents(ctx, selected, after, pendingBatchSize)
			if err != nil {
				telemetry.RecordEventStage(ctx, "scan", "error")
				return summary, err
			}
			telemetry.RecordEventStage(ctx, "scan", "succeeded")
			if len(refs) == 0 {
				break
			}
			claimedAny := false
			for _, ref := range refs {
				after = ref
				delivered, attempted, err := dispatcher.publishOne(ctx, selected, ref, true)
				if err != nil {
					if !attempted {
						return summary, err
					}
					summary.Attempted++
					summary.Failed++
					claimedAny = true
					continue
				}
				if delivered {
					summary.Attempted++
					summary.Delivered++
					claimedAny = true
				}
			}
			if !claimedAny || len(refs) < pendingBatchSize {
				break
			}
		}
		remaining, err := dispatcher.store.CountPendingEvents(ctx, selected)
		if err != nil {
			telemetry.RecordEventStage(ctx, "count_pending", "error")
			return summary, err
		}
		telemetry.RecordEventStage(ctx, "count_pending", "succeeded")
		summary.Remaining += remaining
	}
	return summary, nil
}

func (dispatcher *Dispatcher) publishOne(
	ctx context.Context,
	destinationID string,
	ref SourceRef,
	force bool,
) (bool, bool, error) {
	leaseNonce, err := newLeaseNonce()
	if err != nil {
		telemetry.RecordEventStage(ctx, "claim", "error")
		return false, false, err
	}
	attempt, claimed, err := dispatcher.store.ClaimEvent(
		ctx, destinationID, ref, leaseNonce, dispatcher.leasePeriod, force,
	)
	if err != nil {
		telemetry.RecordEventStage(ctx, "claim", "error")
		return false, false, err
	}
	if !claimed {
		telemetry.RecordEventStage(ctx, "claim", "skipped")
		return false, false, nil
	}
	telemetry.RecordEventStage(ctx, "claim", "succeeded")
	deliveryContext, span := telemetry.Start(ctx, "integration_event.deliver",
		attribute.String("messaging.destination.name", destinationID),
		attribute.Bool("delivery.forced", force),
	)
	started := time.Now()
	publishContext, cancel := context.WithTimeout(deliveryContext, dispatcher.publishLimit)
	defer cancel()
	event, err := dispatcher.store.LoadEvent(publishContext, ref)
	loadFailed := err != nil
	if err == nil {
		telemetry.RecordEventStage(deliveryContext, "load", "succeeded")
		err = safelyPublish(publishContext, dispatcher.destinations[destinationID], event)
		if err == nil {
			telemetry.RecordEventStage(deliveryContext, "publish", "succeeded")
		} else {
			telemetry.RecordEventStage(deliveryContext, "publish", "error")
		}
	} else {
		telemetry.RecordEventStage(deliveryContext, "load", "error")
	}
	if err == nil {
		if err := dispatcher.store.MarkEventDelivered(ctx, destinationID, ref, leaseNonce); err != nil {
			telemetry.RecordEventStage(deliveryContext, "mark_delivered", "error")
			telemetry.RecordEventDelivery(deliveryContext, "failed", "mark_delivery_error", time.Since(started))
			telemetry.End(span, "mark_delivery_error")
			return false, true, err
		}
		telemetry.RecordEventStage(deliveryContext, "mark_delivered", "succeeded")
		telemetry.RecordEventDelivery(deliveryContext, "delivered", "", time.Since(started))
		telemetry.End(span, "")
		return true, true, nil
	}
	errorClass := "publisher_error"
	switch {
	case loadFailed:
		errorClass = "event_load_error"
	case ctx.Err() != nil:
		errorClass = "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		errorClass = "publisher_timeout"
	}
	if markErr := dispatcher.store.MarkEventFailed(
		ctx,
		destinationID,
		ref,
		leaseNonce,
		errorClass,
		retryDelay(attempt, ref),
	); markErr != nil {
		telemetry.RecordEventStage(deliveryContext, "mark_failed", "error")
		telemetry.RecordEventDelivery(deliveryContext, "failed", "mark_failure_error", time.Since(started))
		telemetry.End(span, "mark_failure_error")
		return false, true, markErr
	}
	telemetry.RecordEventStage(deliveryContext, "mark_failed", "succeeded")
	telemetry.RecordEventDelivery(deliveryContext, "failed", errorClass, time.Since(started))
	telemetry.End(span, errorClass)
	return false, true, err
}

func safelyPublish(ctx context.Context, publisher Publisher, event Envelope) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("event publisher panic: %T", recovered)
		}
	}()
	return publisher.Publish(ctx, event)
}

func retryDelay(attempt int, ref SourceRef) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 10 {
		attempt = 10
	}
	base := time.Second * time.Duration(1<<(attempt-1))
	if base > 5*time.Minute {
		base = 5 * time.Minute
	}
	var hash uint32 = 2166136261
	value := ref.TableName + "\x00" + ref.ID
	for index := range len(value) {
		hash ^= uint32(value[index])
		hash *= 16777619
	}
	jitter := time.Duration(hash%501) * base / 1000
	delay := base*3/4 + jitter
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func newLeaseNonce() (string, error) {
	nonce, err := uuid.NewRandom()
	return nonce.String(), err
}

func (dispatcher *Dispatcher) destinationIDs() []string {
	result := make([]string, 0, len(dispatcher.destinations))
	for destinationID := range dispatcher.destinations {
		result = append(result, destinationID)
	}
	sort.Strings(result)
	return result
}

func (dispatcher *Dispatcher) selectedDestinationIDs(destinationID string) ([]string, error) {
	if destinationID == "" {
		return dispatcher.destinationIDs(), nil
	}
	if _, exists := dispatcher.destinations[destinationID]; !exists {
		return nil, ErrUnknownDestination
	}
	return []string{destinationID}, nil
}
