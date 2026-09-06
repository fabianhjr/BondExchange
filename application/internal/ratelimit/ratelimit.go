// Package ratelimit defines authenticated request-admission policy without
// coupling the RPC adapter to its PostgreSQL coordination mechanism.
package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/fabianhjr/BondExchange/application/internal/exchange"
)

const RequestsPerMinute = 100

var (
	ErrExceeded    = errors.New("request rate limit exceeded")
	ErrUnavailable = errors.New("request rate limit unavailable")
)

type ExceededError struct {
	RetryAfter time.Duration
}

func (err *ExceededError) Error() string { return ErrExceeded.Error() }
func (err *ExceededError) Unwrap() error { return ErrExceeded }

type Limiter interface {
	AdmitRequest(ctx context.Context, principalID exchange.PrincipalID) error
}

func RetryAfter(err error) (time.Duration, bool) {
	var exceeded *ExceededError
	if !errors.As(err, &exceeded) || exceeded.RetryAfter <= 0 {
		return 0, false
	}
	return exceeded.RetryAfter, true
}
