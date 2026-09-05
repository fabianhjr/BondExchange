package exchange

import (
	"crypto/sha256"
	"errors"

	"github.com/google/uuid"
)

const (
	OperationBuy                  = "purchases.buy"
	OperationCreateSaleOffer      = "offers.create"
	OperationQuoteSaleOffer       = "offers.quote"
	OperationListActiveOffers     = "offers.list"
	OperationListBondSeries       = "bond-series.list"
	OperationCheckHealth          = "health.read"
	OperationPublishPendingEvents = "events.publish-pending"
)

const (
	PermissionBuy              = "purchases.buy"
	PermissionCreateSaleOffer  = "offers.create"
	PermissionQuoteSaleOffer   = "offers.quote"
	PermissionListActiveOffers = "offers.list"
	PermissionListBondSeries   = "offers.list"
	PermissionCheckHealth      = "health.read"
	PermissionPublishEvents    = "events.publish"
)

var (
	ErrUnauthenticated       = errors.New("authentication required")
	ErrPermissionDenied      = errors.New("operation not permitted")
	ErrInvalidOperation      = errors.New("invalid operation authorization")
	ErrInvalidIdempotencyKey = errors.New("idempotency key must be a canonical UUIDv4 nonce")
	ErrIdempotencyConflict   = errors.New("idempotency key was already used for another operation")
)

type ClientClass string

const (
	ClientClassHuman     ClientClass = "human"
	ClientClassAutomated ClientClass = "automated"
)

type Principal struct {
	ID          UserID
	Issuer      string
	Subject     string
	ClientID    string
	ClientClass ClientClass
}

type AccessContext struct {
	Principal       Principal
	Operation       string
	AssertionID     string
	AssertionDigest [sha256.Size]byte
	RequestDigest   [sha256.Size]byte
}

type MutationContext struct {
	AccessContext
	IdempotencyKey string
}

func IsValidIdempotencyKey(value string) bool {
	return isCanonicalUUIDVersion(value, uuid.Version(4))
}
