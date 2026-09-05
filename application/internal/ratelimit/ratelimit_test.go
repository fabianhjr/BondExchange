package ratelimit

import (
	"errors"
	"testing"
	"time"
)

func TestExceededErrorCarriesRetryDelay(t *testing.T) {
	want := 17 * time.Second
	err := &ExceededError{RetryAfter: want}
	if !errors.Is(err, ErrExceeded) || err.Error() != ErrExceeded.Error() {
		t.Fatalf("exceeded error = %v", err)
	}
	got, ok := RetryAfter(err)
	if !ok || got != want {
		t.Fatalf("RetryAfter() = %v, %t; want %v, true", got, ok, want)
	}
	for _, candidate := range []error{errors.New("other"), &ExceededError{}} {
		if delay, found := RetryAfter(candidate); found || delay != 0 {
			t.Fatalf("RetryAfter(%v) = %v, %t", candidate, delay, found)
		}
	}
}
