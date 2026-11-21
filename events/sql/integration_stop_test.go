package eventsql_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
)

// TestStop is not parallel with the other tests: it asserts that the
// release takes well under a second, which a loaded machine can stretch.
func TestStop(t *testing.T) {
	tests := []struct {
		name        string
		stopTimeout time.Duration // 0: context.Background()
		wantI2      int
	}{
		{name: "background", wantI2: 7},
		{name: "timeout", stopTimeout: 10 * time.Millisecond, wantI2: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				const dest = "orders.placed"
				opts := fast(eventsql.Lease(30*time.Second), eventsql.BatchSize(8), eventsql.Workers(1))
				i1 := schemaTransport(t, db, opts...)
				i2 := db.Transport(t, opts...)
				seedGroup(t, db, dest, "mail", false)
				for i := range 8 {
					send(t, i1, fmt.Sprintf("e%d", i), dest)
				}

				running := make(chan struct{}, 8)
				var finished, cancelled atomic.Bool
				stop := subscribe(t, i1, group(dest, "mail"), func(ctx context.Context, _ doger.Envelope) error {
					running <- struct{}{}
					select {
					case <-time.After(100 * time.Millisecond):
						finished.Store(true)
						return nil
					case <-ctx.Done():
						cancelled.Store(true)
						return ctx.Err()
					}
				})
				<-running
				// the 8 were claimed in one transaction, committed before the
				// first handler started
				require.Equal(t, 8, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE lease_token IS NOT NULL"))

				ctx := context.Background()
				if tt.stopTimeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tt.stopTimeout)
					defer cancel()
				}
				stopped := time.Now()
				_ = stop(ctx)
				stopTook := time.Since(stopped)

				if tt.stopTimeout == 0 {
					assert.True(t, finished.Load(), "stop returned before the running handler finished")
				} else {
					assert.Less(t, stopTook, time.Second)
					assert.True(t, cancelled.Load(), "the running handler's context was not cancelled")
				}
				released := time.Now()
				c := newCollector()
				subscribe(t, i2, group(dest, "mail"), c.handle)
				assert.Len(t, distinct(c.expect(t, tt.wantI2)), tt.wantI2)
				assert.Less(t, time.Since(released), time.Second, "i2 waited for the lease instead of the release")
				c.quiet(t)
			})
		})
	}
}

// TestStop_NoGoroutine is not parallel: it checks every goroutine of the
// process.
func TestStop_NoGoroutine(t *testing.T) {
	forEachEngine(t, func(t *testing.T, tdb *testDB) {
		testWithTimeout(t, time.Minute)
		ignore := goleak.IgnoreCurrent()
		db, err := sql.Open(tdb.driver, scopedDSN(t, tdb.engine, tdb.schema))
		require.NoError(t, err)
		tr, err := eventsql.New(db, tdb.Dialect(), fast()...)
		require.NoError(t, err)
		require.NoError(t, tr.EnsureSchema(context.Background()))

		stops := make([]func(context.Context) error, 0, 3)
		for _, g := range []string{"a", "b", "c/x"} {
			sub := group("orders.placed", g)
			sub.Ephemeral = g == "c/x"
			stop, err := tr.Subscribe(context.Background(), sub, func(context.Context, doger.Envelope) error { return nil })
			require.NoError(t, err)
			stops = append(stops, stop)
		}
		send(t, tr, "e1", "orders.placed")
		time.Sleep(100 * time.Millisecond)
		for _, stop := range stops {
			require.NoError(t, stop(context.Background()))
		}
		require.NoError(t, db.Close()) // the pool's own goroutines

		goleak.VerifyNone(t, ignore)
	})
}

// TestStop_HandlerIgnoresContext is a regression test: stop waited for a
// handler that ignores its context after its own context ended, so one
// such handler hung the shutdown. Stop returns once its context ended.
func TestStop_HandlerIgnoresContext(t *testing.T) {
	t.Parallel()
	forEachEngine(t, func(t *testing.T, db *testDB) {
		testWithTimeout(t, time.Minute)
		const dest = "orders.placed"
		tr := schemaTransport(t, db, fast()...)
		seedGroup(t, db, dest, "mail", false)
		send(t, tr, "e1", dest)

		running, unblock := make(chan struct{}), make(chan struct{})
		var once sync.Once
		t.Cleanup(func() { once.Do(func() { close(unblock) }) })
		stop := subscribe(t, tr, group(dest, "mail"), func(context.Context, doger.Envelope) error {
			close(running)
			<-unblock // ignores its context
			return nil
		})
		<-running

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		stopped := make(chan error, 1)
		go func() { stopped <- stop(ctx) }()

		select {
		case err := <-stopped:
			assert.ErrorIs(t, err, context.DeadlineExceeded)
		case <-time.After(2 * time.Second):
			t.Error("stop did not return once its context ended")
			once.Do(func() { close(unblock) })
			<-stopped
		}
	})
}
