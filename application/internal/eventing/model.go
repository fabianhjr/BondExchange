package eventing

import (
	"context"
	"errors"
	"time"
)

const (
	TableSaleOffers = "sale_offers"
	TablePurchases  = "purchases"

	TypeSaleOfferCreated = "bond_exchange.sale_offer.created"
	TypePurchaseRecorded = "bond_exchange.purchase.recorded"
)

var (
	ErrNoPublishers       = errors.New("no event publishers are configured")
	ErrUnknownDestination = errors.New("unknown event publisher destination")
	ErrUnsupportedEvent   = errors.New("unsupported integration event")
)

type SourceRef struct {
	TableName string `json:"table_name"`
	ID        string `json:"id"`
}

type Envelope struct {
	ID            string    `json:"id"`
	Source        SourceRef `json:"source"`
	Type          string    `json:"type"`
	SchemaVersion uint16    `json:"schema_version"`
	CompletedAt   time.Time `json:"completed_at"`
	Data          any       `json:"data"`
}

type SaleOfferCreated struct {
	ID           string `json:"id"`
	BondSeries   string `json:"bond_series"`
	Price        string `json:"price"`
	CurrencyCode string `json:"currency_code"`
}

type PurchaseRecorded struct {
	SaleOfferID  string    `json:"sale_offer_id"`
	BondSeries   string    `json:"bond_series"`
	Price        string    `json:"price"`
	CurrencyCode string    `json:"currency_code"`
	BoughtAt     time.Time `json:"bought_at"`
}

type Publisher interface {
	Publish(ctx context.Context, event Envelope) error
}

type Destination struct {
	ID        string
	Publisher Publisher
}

type Summary struct {
	Attempted uint64
	Delivered uint64
	Failed    uint64
	Remaining uint64
}

type Store interface {
	LoadEvent(ctx context.Context, ref SourceRef) (Envelope, error)
	ClaimEvent(
		ctx context.Context,
		destinationID string,
		ref SourceRef,
		leaseToken string,
		leaseDuration time.Duration,
		force bool,
	) (attempt int, claimed bool, err error)
	MarkEventDelivered(ctx context.Context, destinationID string, ref SourceRef, leaseToken string) error
	MarkEventFailed(
		ctx context.Context,
		destinationID string,
		ref SourceRef,
		leaseToken string,
		errorClass string,
		retryAfter time.Duration,
	) error
	PendingEvents(ctx context.Context, destinationID string, after SourceRef, limit int) ([]SourceRef, error)
	CountPendingEvents(ctx context.Context, destinationID string) (uint64, error)
}
