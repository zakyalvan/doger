package eventsql_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	eventsql "github.com/kartaladev/doger/events/sql"
)

func TestExponentialBackoff_Default(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attempt  int
		min, max time.Duration
	}{
		{attempt: 1, min: 900 * time.Millisecond, max: 1100 * time.Millisecond},
		{attempt: 2, min: 1800 * time.Millisecond, max: 2200 * time.Millisecond},
		{attempt: 5, min: 14400 * time.Millisecond, max: 17600 * time.Millisecond},
		{attempt: 20, min: 4*time.Minute + 30*time.Second, max: 5*time.Minute + 30*time.Second},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			t.Parallel()
			policy := eventsql.DefaultBackoff()
			// the jitter is random: every draw stays within the range
			for range 1000 {
				d := policy.Delay(tt.attempt)
				assert.GreaterOrEqual(t, d, tt.min)
				assert.LessOrEqual(t, d, tt.max)
			}
		})
	}
}

func TestFixedBackoff(t *testing.T) {
	t.Parallel()
	policy := eventsql.FixedBackoff(10 * time.Millisecond)
	for _, attempt := range []int{1, 2, 50} {
		assert.Equal(t, 10*time.Millisecond, policy.Delay(attempt))
	}
}

// TestExponentialBackoff_Delay is a regression test: with Max unset the
// delay was capped at 0, and no delay may be negative.
func TestExponentialBackoff_Delay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  eventsql.ExponentialBackoff
		attempt int
		want    time.Duration
	}{
		{name: "max_unset_first", policy: eventsql.ExponentialBackoff{Initial: time.Second, Factor: 2}, attempt: 1, want: time.Second},
		{name: "max_unset_third", policy: eventsql.ExponentialBackoff{Initial: time.Second, Factor: 2}, attempt: 3, want: 4 * time.Second},
		{name: "max_negative_is_no_cap", policy: eventsql.ExponentialBackoff{Initial: time.Second, Factor: 2, Max: -time.Second}, attempt: 2, want: 2 * time.Second},
		{name: "max_caps", policy: eventsql.ExponentialBackoff{Initial: time.Second, Factor: 2, Max: 3 * time.Second}, attempt: 3, want: 3 * time.Second},
		{name: "attempt_zero_is_first", policy: eventsql.ExponentialBackoff{Initial: time.Second, Factor: 2}, attempt: 0, want: time.Second},
		{name: "zero_value", policy: eventsql.ExponentialBackoff{}, attempt: 1, want: 0},
		{name: "negative_initial", policy: eventsql.ExponentialBackoff{Initial: -time.Second, Factor: 2}, attempt: 1, want: 0},
		{name: "overflow_without_max", policy: eventsql.ExponentialBackoff{Initial: time.Second, Factor: 10}, attempt: 400, want: time.Duration(math.MaxInt64)},
		{name: "jitter_over_one_never_negative", policy: eventsql.ExponentialBackoff{Initial: time.Second, Factor: 1, Jitter: 5}, attempt: 1, want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.want < 0 { // random: only the sign is fixed
				for range 1000 {
					assert.GreaterOrEqual(t, tt.policy.Delay(tt.attempt), time.Duration(0))
				}
				return
			}
			assert.Equal(t, tt.want, tt.policy.Delay(tt.attempt))
		})
	}
}
