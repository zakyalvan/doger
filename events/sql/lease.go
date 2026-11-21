package eventsql

import (
	"context"
	"errors"
	"time"
)

// ErrLeaseLost is logged, never returned, when a handler's result arrives
// after its lease was lost: the lease expired and another member claimed
// the delivery, which that member now settles. The late result is
// ignored.
var ErrLeaseLost = errors.New("doger: eventsql: lease lost")

// attemptKey is the context key of the attempt of a delivery.
type attemptKey struct{}

// AttemptFromContext returns the attempt (1 for the first delivery) of
// the delivery a handler of this transport is running, from the handler's
// context, and false for any other context. Attempts are counted when a
// delivery is claimed, so an attempt interrupted by a crash counts.
func AttemptFromContext(ctx context.Context) (int, bool) {
	if ctx == nil {
		return 0, false
	}
	n, ok := ctx.Value(attemptKey{}).(int)

	return n, ok
}

// leaseMargin is how long before its lease ends a handler's context ends,
// so a slow handler is cancelled before another member can claim the
// delivery: a tenth of the lease, at most 1s.
func leaseMargin(lease time.Duration) time.Duration {
	return min(lease/10, time.Second)
}
