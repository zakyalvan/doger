package eventsql

import (
	"math"
	"math/rand/v2"
	"time"
)

// BackoffPolicy decides how long a delivery whose handler failed waits
// before its next attempt (see the Backoff option). Delay receives the
// attempt that failed, 1 for the first.
type BackoffPolicy interface {
	Delay(attempt int) time.Duration
}

// ExponentialBackoff waits Initial after the first attempt, multiplied by
// Factor after each further one, each delay varied by up to ±Jitter (a
// fraction: 0.1 is ±10%) and at most Max; a Max of zero or less means no
// cap. A delay is never negative.
type ExponentialBackoff struct {
	Initial time.Duration
	Factor  float64
	Max     time.Duration
	Jitter  float64
}

// Delay returns the delay after attempt: never negative, and uncapped
// (up to the largest time.Duration) when Max is not positive.
func (b ExponentialBackoff) Delay(attempt int) time.Duration {
	d := float64(b.Initial) * math.Pow(b.Factor, float64(max(attempt, 1)-1))
	if b.Max > 0 && d > float64(b.Max) {
		d = float64(b.Max)
	}
	if b.Jitter > 0 {
		d *= 1 + b.Jitter*(2*rand.Float64()-1) //nolint:gosec // G404: jitter is not security-relevant
	}

	return clampDuration(d)
}

// clampDuration converts d, in nanoseconds, to a duration between 0 and
// the largest time.Duration; NaN is 0.
func clampDuration(d float64) time.Duration {
	switch {
	case math.IsNaN(d) || d <= 0:
		return 0
	case d >= math.MaxInt64:
		return time.Duration(math.MaxInt64)
	}

	return time.Duration(d)
}

// DefaultBackoff returns the default policy: ExponentialBackoff from 1s,
// ×2, capped at 5m, ±10% jitter.
func DefaultBackoff() BackoffPolicy {
	return ExponentialBackoff{Initial: time.Second, Factor: 2, Max: 5 * time.Minute, Jitter: 0.1}
}

// FixedBackoff returns a policy waiting d after every attempt, for tests.
func FixedBackoff(d time.Duration) BackoffPolicy { return fixedBackoff(d) }

type fixedBackoff time.Duration

func (b fixedBackoff) Delay(int) time.Duration { return time.Duration(b) }
