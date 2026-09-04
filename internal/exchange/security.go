package exchange

import (
	"crypto/sha256"
	"errors"
)

const (
	OperationBuy              = "purchases.buy"
	OperationCreateSaleOffer  = "offers.create"
	OperationListActiveOffers = "offers.list"
	OperationListBondSeries   = "bond-series.list"
	OperationCheckHealth      = "health.read"
	OperationReflection       = "reflection.use"
)

const (
	PermissionBuy              = "purchases.buy"
	PermissionCreateSaleOffer  = "offers.create"
	PermissionListActiveOffers = "offers.list"
	PermissionListBondSeries   = "offers.list"
	PermissionCheckHealth      = "health.read"
	PermissionReflection       = "reflection.use"
)

var (
	ErrUnauthenticated       = errors.New("authentication required")
	ErrPermissionDenied      = errors.New("operation not permitted")
	ErrInvalidOperation      = errors.New("invalid operation authorization")
	ErrInvalidIdempotencyKey = errors.New("idempotency key must contain 16-128 visible ASCII characters")
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
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
