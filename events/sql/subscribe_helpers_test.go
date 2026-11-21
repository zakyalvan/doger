package eventsql_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
)

// Waits of the integration tests: how long a test waits for an expected
// outcome, and how long it watches for one that must not happen.
const (
	waitTimeout = 15 * time.Second
	quietPeriod = 300 * time.Millisecond
)

// fast are the options of most integration tests: short polls and
// backoffs, so that outcomes arrive quickly.
func fast(opts ...eventsql.Option) []eventsql.Option {
	return append([]eventsql.Option{eventsql.PollInterval(20 * time.Millisecond)}, opts...)
}

// subscribe subscribes h and returns the stop function, also called at
// cleanup when the test did not.
func subscribe(t *testing.T, tr *eventsql.Transport, sub doger.Subscription, h doger.EnvelopeHandler) func(context.Context) error {
	t.Helper()
	stop, err := tr.Subscribe(context.Background(), sub, h)
	require.NoError(t, err)
	var once sync.Once
	stopOnce := func(ctx context.Context) error {
		var err error
		once.Do(func() { err = stop(ctx) })
		return err
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
		defer cancel()
		_ = stopOnce(ctx)
	})

	return stopOnce
}

// group returns a DeliverOnce subscription of group to destination.
func group(destination, name string) doger.Subscription {
	return doger.Subscription{Destination: destination, Group: name, Mode: doger.DeliverOnce}
}

// collector records the envelope IDs a handler receives.
type collector struct {
	mu  sync.Mutex
	ids []string
	ch  chan string
}

func newCollector() *collector { return &collector{ch: make(chan string, 4096)} }

// handle records env and acknowledges it.
func (c *collector) handle(_ context.Context, env doger.Envelope) error {
	c.record(env.ID)
	return nil
}

func (c *collector) record(id string) {
	c.mu.Lock()
	c.ids = append(c.ids, id)
	c.mu.Unlock()
	c.ch <- id
}

// IDs returns the received IDs, in order.
func (c *collector) IDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.ids)
}

// expect waits for n more envelopes and returns their IDs.
func (c *collector) expect(t *testing.T, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	timeout := time.After(waitTimeout)
	for len(ids) < n {
		select {
		case id := <-c.ch:
			ids = append(ids, id)
		case <-timeout:
			t.Fatalf("received %d envelopes %v, want %d", len(ids), ids, n)
		}
	}
	return ids
}

// quiet fails the test when an envelope arrives during the quiet period.
func (c *collector) quiet(t *testing.T) {
	t.Helper()
	select {
	case id := <-c.ch:
		t.Errorf("unexpected envelope %s", id)
	case <-time.After(quietPeriod):
	}
}

// send sends envelope id to destination, failing the test on error.
func send(t *testing.T, tr *eventsql.Transport, id, destination string) {
	t.Helper()
	require.NoError(t, tr.Send(context.Background(), envelope(id, destination)))
}
