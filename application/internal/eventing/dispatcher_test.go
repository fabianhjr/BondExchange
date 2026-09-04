package eventing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

type deliveryKey struct {
	destination string
	ref         SourceRef
}

type fakeDelivery struct {
	delivered  bool
	lease      string
	attempts   int
	errorClass string
}

type storeFake struct {
	mutex      sync.Mutex
	events     map[SourceRef]Envelope
	deliveries map[deliveryKey]fakeDelivery
	loadErr    error
	claimErr   error
	markErr    error
	pendingErr error
	countErr   error
}

func newStoreFake(events ...Envelope) *storeFake {
	store := &storeFake{
		events:     make(map[SourceRef]Envelope),
		deliveries: make(map[deliveryKey]fakeDelivery),
	}
	for _, event := range events {
		store.events[event.Source] = event
	}
	return store
}

func (store *storeFake) LoadEvent(_ context.Context, ref SourceRef) (Envelope, error) {
	if store.loadErr != nil {
		return Envelope{}, store.loadErr
	}
	event, ok := store.events[ref]
	if !ok {
		return Envelope{}, ErrUnsupportedEvent
	}
	return event, nil
}

func (store *storeFake) ClaimEvent(
	_ context.Context,
	destination string,
	ref SourceRef,
	lease string,
	_ time.Duration,
	_ bool,
) (int, bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.claimErr != nil {
		return 0, false, store.claimErr
	}
	key := deliveryKey{destination: destination, ref: ref}
	delivery := store.deliveries[key]
	if delivery.delivered || delivery.lease != "" {
		return 0, false, nil
	}
	delivery.lease = lease
	delivery.attempts++
	store.deliveries[key] = delivery
	return delivery.attempts, true, nil
}

func (store *storeFake) MarkEventDelivered(_ context.Context, destination string, ref SourceRef, lease string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.markErr != nil {
		return store.markErr
	}
	key := deliveryKey{destination: destination, ref: ref}
	delivery := store.deliveries[key]
	if delivery.lease != lease {
		return errors.New("lost lease")
	}
	delivery.delivered = true
	delivery.lease = ""
	store.deliveries[key] = delivery
	return nil
}

func (store *storeFake) MarkEventFailed(
	_ context.Context,
	destination string,
	ref SourceRef,
	lease string,
	errorClass string,
	_ time.Duration,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.markErr != nil {
		return store.markErr
	}
	key := deliveryKey{destination: destination, ref: ref}
	delivery := store.deliveries[key]
	if delivery.lease != lease {
		return errors.New("lost lease")
	}
	delivery.lease = ""
	delivery.errorClass = errorClass
	store.deliveries[key] = delivery
	return nil
}

func (store *storeFake) PendingEvents(
	_ context.Context,
	destination string,
	after SourceRef,
	limit int,
) ([]SourceRef, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.pendingErr != nil {
		return nil, store.pendingErr
	}
	refs := make([]SourceRef, 0, len(store.events))
	for ref := range store.events {
		delivery := store.deliveries[deliveryKey{destination: destination, ref: ref}]
		if !delivery.delivered && delivery.lease == "" && compareRefs(ref, after) > 0 {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(left, right int) bool { return compareRefs(refs[left], refs[right]) < 0 })
	if len(refs) > limit {
		refs = refs[:limit]
	}
	return refs, nil
}

func (store *storeFake) CountPendingEvents(_ context.Context, destination string) (uint64, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.countErr != nil {
		return 0, store.countErr
	}
	var count uint64
	for ref := range store.events {
		if !store.deliveries[deliveryKey{destination: destination, ref: ref}].delivered {
			count++
		}
	}
	return count, nil
}

func compareRefs(left, right SourceRef) int {
	if left.TableName < right.TableName || left.TableName == right.TableName && left.ID < right.ID {
		return -1
	}
	if left == right {
		return 0
	}
	return 1
}

type publisherFake struct {
	mutex  sync.Mutex
	events []Envelope
	err    error
	panic  bool
}

func (publisher *publisherFake) Publish(_ context.Context, event Envelope) error {
	if publisher.panic {
		panic("publisher panic")
	}
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	publisher.events = append(publisher.events, event)
	return publisher.err
}

func testEvent(table, id string) Envelope {
	return Envelope{
		Source:        SourceRef{TableName: table, ID: id},
		Type:          TypeSaleOfferCreated,
		SchemaVersion: 1,
		CompletedAt:   time.Unix(1, 0),
		Data:          SaleOfferCreated{ID: id},
	}
}

func TestDispatcherPublishesAndDoesNotRedeliver(t *testing.T) {
	t.Parallel()
	event := testEvent(TableSaleOffers, "offer-1")
	store := newStoreFake(event)
	first := &publisherFake{}
	second := &publisherFake{}
	dispatcher, err := NewDispatcher(store, []Destination{
		{ID: "webhook", Publisher: first},
		{ID: "security", Publisher: second},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.Publish(context.Background(), event.Source)
	dispatcher.Publish(context.Background(), event.Source)
	if len(first.events) != 1 || len(second.events) != 1 {
		t.Fatalf("published events = %d, %d", len(first.events), len(second.events))
	}
	if first.events[0].Source != event.Source {
		t.Fatalf("published event = %#v", first.events[0])
	}
}

func TestDispatcherLeavesFailuresPendingAndManualDrainRetriesOnce(t *testing.T) {
	t.Parallel()
	events := []Envelope{
		testEvent(TablePurchases, "offer-1"),
		testEvent(TableSaleOffers, "offer-2"),
	}
	store := newStoreFake(events...)
	publisher := &publisherFake{err: errors.New("unavailable")}
	dispatcher, err := NewDispatcher(store, []Destination{{ID: "sink", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.Publish(context.Background(), events[0].Source)
	publisher.err = nil
	summary, err := dispatcher.PublishPending(context.Background(), "sink")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Attempted != 2 || summary.Delivered != 2 || summary.Failed != 0 || summary.Remaining != 0 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestDispatcherContinuesAfterFailureAndHandlesPublisherPanic(t *testing.T) {
	t.Parallel()
	event := testEvent(TableSaleOffers, "offer-1")
	store := newStoreFake(event)
	publisher := &publisherFake{panic: true}
	dispatcher, err := NewDispatcher(store, []Destination{{ID: "sink", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := dispatcher.PublishPending(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Attempted != 1 || summary.Failed != 1 || summary.Remaining != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestDispatcherConfigurationAndStoreErrors(t *testing.T) {
	t.Parallel()
	if _, err := NewDispatcher(newStoreFake(), []Destination{{ID: "", Publisher: &publisherFake{}}}, 0); err == nil {
		t.Fatal("empty destination accepted")
	}
	if _, err := NewDispatcher(newStoreFake(), []Destination{{ID: "sink", Publisher: &publisherFake{}}, {ID: "sink", Publisher: &publisherFake{}}}, 0); err == nil {
		t.Fatal("duplicate destination accepted")
	}
	empty, err := NewDispatcher(newStoreFake(), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.PublishPending(context.Background(), ""); !errors.Is(err, ErrNoPublishers) {
		t.Fatalf("empty dispatcher error = %v", err)
	}
	dispatcher, err := NewDispatcher(newStoreFake(), []Destination{{ID: "sink", Publisher: &publisherFake{}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.PublishPending(context.Background(), "missing"); !errors.Is(err, ErrUnknownDestination) {
		t.Fatalf("unknown destination error = %v", err)
	}

	store := newStoreFake(testEvent(TableSaleOffers, "offer"))
	store.pendingErr = errors.New("database unavailable")
	dispatcher, _ = NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{}}}, 0)
	if _, err := dispatcher.PublishPending(context.Background(), "sink"); err == nil {
		t.Fatal("pending store error was ignored")
	}

	store = newStoreFake()
	store.countErr = errors.New("count failed")
	dispatcher, _ = NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{}}}, 0)
	if _, err := dispatcher.PublishPending(context.Background(), "sink"); !errors.Is(err, store.countErr) {
		t.Fatalf("count store error = %v", err)
	}
}

func TestDispatcherHandlesClaimLoadPublishAndMarkFailures(t *testing.T) {
	t.Parallel()
	event := testEvent(TableSaleOffers, "offer")
	ref := event.Source

	store := newStoreFake(event)
	store.claimErr = errors.New("claim failed")
	dispatcher, _ := NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{}}}, time.Second)
	if delivered, claimed, err := dispatcher.publishOne(context.Background(), "sink", ref, false); delivered || claimed || !errors.Is(err, store.claimErr) {
		t.Fatalf("claim failure = delivered %t, claimed %t, error %v", delivered, claimed, err)
	}

	store = newStoreFake(event)
	store.deliveries[deliveryKey{destination: "sink", ref: ref}] = fakeDelivery{lease: "other"}
	dispatcher, _ = NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{}}}, time.Second)
	if delivered, claimed, err := dispatcher.publishOne(context.Background(), "sink", ref, false); delivered || claimed || err != nil {
		t.Fatalf("unclaimed event = delivered %t, claimed %t, error %v", delivered, claimed, err)
	}

	store = newStoreFake(event)
	store.markErr = errors.New("mark failed")
	dispatcher, _ = NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{}}}, time.Second)
	if delivered, claimed, err := dispatcher.publishOne(context.Background(), "sink", ref, false); delivered || !claimed || !errors.Is(err, store.markErr) {
		t.Fatalf("delivery mark failure = delivered %t, claimed %t, error %v", delivered, claimed, err)
	}

	store = newStoreFake(event)
	store.loadErr = errors.New("load failed")
	dispatcher, _ = NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{}}}, time.Second)
	if delivered, claimed, err := dispatcher.publishOne(context.Background(), "sink", ref, false); delivered || !claimed || !errors.Is(err, store.loadErr) {
		t.Fatalf("load failure = delivered %t, claimed %t, error %v", delivered, claimed, err)
	}
	if delivery := store.deliveries[deliveryKey{destination: "sink", ref: ref}]; delivery.errorClass != "event_load_error" {
		t.Fatalf("load error class = %q", delivery.errorClass)
	}

	store = newStoreFake(event)
	dispatcher, _ = NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{err: context.DeadlineExceeded}}}, time.Second)
	if delivered, claimed, err := dispatcher.publishOne(context.Background(), "sink", ref, false); delivered || !claimed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("publisher timeout = delivered %t, claimed %t, error %v", delivered, claimed, err)
	}
	if delivery := store.deliveries[deliveryKey{destination: "sink", ref: ref}]; delivery.errorClass != "publisher_timeout" {
		t.Fatalf("timeout error class = %q", delivery.errorClass)
	}

	store = newStoreFake(event)
	dispatcher, _ = NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{err: errors.New("publish failed")}}}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if delivered, claimed, err := dispatcher.publishOne(ctx, "sink", ref, false); delivered || !claimed || err == nil {
		t.Fatalf("canceled publish = delivered %t, claimed %t, error %v", delivered, claimed, err)
	}
	if delivery := store.deliveries[deliveryKey{destination: "sink", ref: ref}]; delivery.errorClass != "context_canceled" {
		t.Fatalf("canceled error class = %q", delivery.errorClass)
	}

	store = newStoreFake(event)
	store.markErr = errors.New("failure mark failed")
	dispatcher, _ = NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{err: errors.New("publish failed")}}}, time.Second)
	if delivered, claimed, err := dispatcher.publishOne(context.Background(), "sink", ref, false); delivered || !claimed || !errors.Is(err, store.markErr) {
		t.Fatalf("failure mark error = delivered %t, claimed %t, error %v", delivered, claimed, err)
	}
}

func TestDispatcherDrainsAFullBatch(t *testing.T) {
	t.Parallel()
	events := make([]Envelope, pendingBatchSize)
	for index := range events {
		events[index] = testEvent(TableSaleOffers, fmt.Sprintf("offer-%03d", index))
	}
	store := newStoreFake(events...)
	dispatcher, _ := NewDispatcher(store, []Destination{{ID: "sink", Publisher: &publisherFake{}}}, time.Second)
	summary, err := dispatcher.PublishPending(context.Background(), "sink")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Attempted != pendingBatchSize || summary.Delivered != pendingBatchSize || summary.Remaining != 0 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestRetryDelayIsBounded(t *testing.T) {
	t.Parallel()
	ref := SourceRef{TableName: TableSaleOffers, ID: "offer"}
	first := retryDelay(0, ref)
	last := retryDelay(100, ref)
	if first < 750*time.Millisecond || first > 1250*time.Millisecond {
		t.Fatalf("first delay = %s", first)
	}
	if last < 3*time.Minute || last > 5*time.Minute {
		t.Fatalf("capped delay = %s", last)
	}
	capped := false
	for index := 0; index < 1000; index++ {
		if retryDelay(100, SourceRef{TableName: TableSaleOffers, ID: fmt.Sprintf("offer-%d", index)}) == 5*time.Minute {
			capped = true
			break
		}
	}
	if !capped {
		t.Fatal("no deterministic jitter reached the delay cap")
	}
}

func TestDispatcherAbortsOnClaimEventError(t *testing.T) {
	t.Parallel()
	events := []Envelope{
		testEvent(TablePurchases, "offer-1"),
		testEvent(TableSaleOffers, "offer-2"),
	}
	store := newStoreFake(events...)
	store.claimErr = errors.New("claim error")
	publisher := &publisherFake{}
	dispatcher, err := NewDispatcher(store, []Destination{{ID: "sink", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := dispatcher.PublishPending(context.Background(), "sink")
	if err == nil {
		t.Fatal("expected error from ClaimEvent but got nil")
	}
	if !errors.Is(err, store.claimErr) {
		t.Fatalf("expected claim error but got: %v", err)
	}
	if summary.Attempted != 0 || summary.Failed != 0 {
		t.Fatalf("expected no attempts/failures on pre-claim error, got summary: %#v", summary)
	}
}

func TestDispatcherHandlesLoadEventErrorAsPostClaimFailure(t *testing.T) {
	t.Parallel()
	events := []Envelope{
		testEvent(TablePurchases, "offer-1"),
		testEvent(TableSaleOffers, "offer-2"),
	}
	store := newStoreFake(events...)
	store.loadErr = errors.New("load error")
	publisher := &publisherFake{}
	dispatcher, err := NewDispatcher(store, []Destination{{ID: "sink", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := dispatcher.PublishPending(context.Background(), "sink")
	if err != nil {
		t.Fatalf("expected no error from PublishPending on individual load error, got: %v", err)
	}
	if summary.Attempted != 2 || summary.Failed != 2 {
		t.Fatalf("expected summary to count both as attempted/failed, got: %#v", summary)
	}
}

type storeWithLoadDeadlineCheck struct {
	*storeFake
	t *testing.T
}

func (s *storeWithLoadDeadlineCheck) LoadEvent(ctx context.Context, ref SourceRef) (Envelope, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		s.t.Error("expected LoadEvent context to have a deadline, but it does not")
	} else {
		remaining := time.Until(deadline)
		if remaining > time.Second || remaining < 10*time.Millisecond {
			s.t.Errorf("expected LoadEvent context deadline to be close to 1 second from now, got remaining time: %v", remaining)
		}
	}
	return s.storeFake.LoadEvent(ctx, ref)
}

func TestDispatcherLoadEventHasTimeout(t *testing.T) {
	t.Parallel()
	event := testEvent(TableSaleOffers, "offer-1")
	fakeStore := newStoreFake(event)
	store := &storeWithLoadDeadlineCheck{
		storeFake: fakeStore,
		t:         t,
	}
	publisher := &publisherFake{}
	dispatcher, err := NewDispatcher(store, []Destination{{ID: "sink", Publisher: publisher}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.Publish(context.Background(), event.Source)
}
