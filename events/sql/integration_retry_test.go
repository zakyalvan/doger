package eventsql_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
)

// errBadOrder is the permanent handler error of the retry tests.
var errBadOrder = errors.New("bad order")

func TestRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		max       int
		behavior  func(run int32) error
		wantRuns  int32
		wantDead  bool
		wantError string // contained in last_error of a dead letter
	}{
		{name: "succeeds", max: 10, behavior: func(int32) error { return nil }, wantRuns: 1},
		{name: "always_fails", max: 3, behavior: func(int32) error { return errOutOfStock }, wantRuns: 3, wantDead: true, wantError: "out of stock"},
		{name: "one_attempt", max: 1, behavior: func(int32) error { return errOutOfStock }, wantRuns: 1, wantDead: true},
		{name: "permanent", max: 10, behavior: func(int32) error { return doger.PermanentError(errBadOrder) }, wantRuns: 1, wantDead: true, wantError: "bad order"},
		{name: "permanent_invalid_utf8", max: 10, behavior: func(int32) error { return doger.PermanentError(errors.New("bad payload \xff\xfe")) }, wantRuns: 1, wantDead: true, wantError: "bad payload"},
		{name: "permanent_nul", max: 10, behavior: func(int32) error { return doger.PermanentError(errors.New("bad\x00payload")) }, wantRuns: 1, wantDead: true, wantError: "badpayload"},
		{name: "retry_invalid_utf8", max: 2, behavior: func(int32) error { return errors.New("out of \xffstock") }, wantRuns: 2, wantDead: true, wantError: "out of stock"},
		{name: "fails_twice", max: 10, behavior: func(run int32) error {
			if run <= 2 {
				return errOutOfStock
			}
			return nil
		}, wantRuns: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				const dest = "orders.placed"
				tr := schemaTransport(t, db, fast(eventsql.MaxAttempts(tt.max), eventsql.Backoff(eventsql.FixedBackoff(10*time.Millisecond)))...)
				var runs atomic.Int32
				c := newCollector()
				subscribe(t, tr, group(dest, "mail"), func(ctx context.Context, env doger.Envelope) error {
					err := tt.behavior(runs.Add(1))
					c.record(env.ID)
					return err
				})
				send(t, tr, "e1", dest)

				c.expect(t, int(tt.wantRuns))
				c.quiet(t)
				assert.Equal(t, tt.wantRuns, runs.Load())
				if !tt.wantDead {
					waitFor(t, func() bool { return db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries") == 0 }, "the delivery was not removed")
					return
				}
				assert.Equal(t, 1, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'dead'"))
				if tt.wantError != "" {
					assert.Equal(t, 1, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'dead' AND last_error LIKE ?", "%"+tt.wantError+"%"))
				}
			})
		})
	}
}

// TestRetry_ExhaustedAtClaim is a regression test: a delivery whose
// attempts were used up without being settled (its member crashed during
// the last attempt) was still claimed and handed to the handler with an
// attempt beyond MaxAttempts; it is dead-lettered at claim time instead.
func TestRetry_ExhaustedAtClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		max      int
		attempts int // already counted
		wantRuns int32
		wantDead bool
	}{
		{name: "last_attempt_left", max: 3, attempts: 2, wantRuns: 1},
		{name: "attempts_used_up", max: 3, attempts: 3, wantDead: true},
		{name: "beyond_max", max: 3, attempts: 7, wantDead: true},
		{name: "one_attempt_used", max: 1, attempts: 1, wantDead: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				const dest = "orders.placed"
				tr := schemaTransport(t, db, fast(eventsql.MaxAttempts(tt.max))...)
				seedGroup(t, db, dest, "mail", false)
				send(t, tr, "e1", dest)
				// the member of the last attempt crashed: no lease remains
				db.Exec(t, "UPDATE doger_event_deliveries SET attempts = ?", tt.attempts)

				var runs atomic.Int32
				got := &attempts{}
				c := newCollector()
				subscribe(t, tr, group(dest, "mail"), func(ctx context.Context, env doger.Envelope) error {
					runs.Add(1)
					got.record(ctx)
					return c.handle(ctx, env)
				})

				if tt.wantDead {
					waitFor(t, func() bool {
						return db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'dead'") == 1
					}, "the delivery was not dead-lettered")
					c.quiet(t)
					assert.Zero(t, runs.Load(), "the handler ran attempts %v", got.Got())
					assert.Equal(t, 1, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'dead' AND lease_token IS NULL AND last_error LIKE ?", "%attempts%"))
					return
				}
				c.expect(t, int(tt.wantRuns))
				assert.Equal(t, []int{tt.attempts + 1}, got.Got())
			})
		})
	}
}

// TestRetry_InterruptedAttemptCounts is a regression test: a handler
// interrupted by stop had its attempt uncounted, so a handler that always
// hangs until stopped was never dead-lettered. Only the claims that never
// started are uncounted.
func TestRetry_InterruptedAttemptCounts(t *testing.T) {
	t.Parallel()
	forEachEngine(t, func(t *testing.T, db *testDB) {
		testWithTimeout(t, time.Minute)
		const dest = "orders.placed"
		tr := schemaTransport(t, db, fast(eventsql.MaxAttempts(2))...)
		seedGroup(t, db, dest, "mail", false)
		send(t, tr, "e1", dest)

		for cycle := 1; cycle <= 2; cycle++ {
			started := make(chan struct{}, 1)
			stop := subscribe(t, tr, group(dest, "mail"), func(ctx context.Context, _ doger.Envelope) error {
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			})
			select {
			case <-started:
			case <-time.After(waitTimeout):
				t.Fatalf("cycle %d: the handler did not start", cycle)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			_ = stop(ctx)
			cancel()
			assert.Equal(t, cycle, db.Count(t, "SELECT attempts FROM doger_event_deliveries"), "cycle %d", cycle)
			assert.Equal(t, 0, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE lease_token IS NOT NULL"), "cycle %d: the lease was released", cycle)
		}

		// the third claim exceeds MaxAttempts(2)
		c := newCollector()
		subscribe(t, tr, group(dest, "mail"), c.handle)
		waitFor(t, func() bool {
			return db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'dead'") == 1
		}, "the delivery was not dead-lettered")
		c.quiet(t)
	})
}
