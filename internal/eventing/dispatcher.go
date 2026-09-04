package eventing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"
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
				return summary, err
			}
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
			return summary, err
		}
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
	leaseToken, err := newLeaseToken()
	if err != nil {
		return false, false, err
	}
	attempt, claimed, err := dispatcher.store.ClaimEvent(
		ctx, destinationID, ref, leaseToken, dispatcher.leasePeriod, force,
	)
	if err != nil {
		return false, false, err
	}
	if !claimed {
		return false, false, nil
	}
	publishContext, cancel := context.WithTimeout(ctx, dispatcher.publishLimit)
	defer cancel()
	event, err := dispatcher.store.LoadEvent(publishContext, ref)
	loadFailed := err != nil
	if err == nil {
		err = safelyPublish(publishContext, dispatcher.destinations[destinationID], event)
	}
	if err == nil {
		if err := dispatcher.store.MarkEventDelivered(ctx, destinationID, ref, leaseToken); err != nil {
			return false, true, err
		}
		return true, true, nil
	}
	errorClass := "publisher_error"
	if loadFailed {
		errorClass = "event_load_error"
	} else if ctx.Err() != nil {
		errorClass = "context_canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		errorClass = "publisher_timeout"
	}
	if markErr := dispatcher.store.MarkEventFailed(
		ctx,
		destinationID,
		ref,
		leaseToken,
		errorClass,
		retryDelay(attempt, ref),
	); markErr != nil {
		return false, true, markErr
	}
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
	for _, character := range ref.TableName + "\x00" + ref.ID {
		hash ^= uint32(character)
		hash *= 16777619
	}
	jitter := time.Duration(hash%501) * base / 1000
	delay := base*3/4 + jitter
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func newLeaseToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
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
