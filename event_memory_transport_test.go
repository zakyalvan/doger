package doger

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitIdle waits until transport delivered everything sent, failing t
// after 5s.
func waitIdle(t *testing.T, transport *MemoryEventTransport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.True(t, transport.waitIdle(ctx), "the transport did not go idle")
}

// logRecorder of the memory transport: it records each formatted line.
type lineRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *lineRecorder) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *lineRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.lines...)
}

// memoryRoot returns a started root sharing transport, with OrderPlaced
// registered as "orders.placed", externalized immediately, and the
// listeners register adds.
func memoryRoot(t *testing.T, transport EventTransport, register func(root *RootScope)) *RootScope {
	t.Helper()
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	t.Cleanup(func() { root.Shutdown() })
	immediateOrders(t, root)
	register(root)
	require.NoError(t, root.Start())

	return root
}

func TestMemoryTransport_Groups(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 10*time.Second)

	transport := NewMemoryEventTransport()
	var mail atomic.Int32
	var cache [2]atomic.Int32
	a := memoryRoot(t, transport, func(*RootScope) {})
	for i := range 2 {
		memoryRoot(t, transport, func(b *RootScope) {
			Listen(b, func(context.Context, OrderPlaced) error {
				mail.Add(1)
				return nil
			}, ListenerID("mail"), ListenerDelivery(DeliverOnce))
			Listen(b, func(context.Context, OrderPlaced) error {
				cache[i].Add(1)
				return nil
			}, ListenerID("cache"), ListenerDelivery(DeliverAll))
		})
	}

	for range 10 {
		require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "1"}))
	}
	waitIdle(t, transport)

	assert.Equal(t, int32(10), mail.Load())
	assert.Equal(t, int32(10), cache[0].Load())
	assert.Equal(t, int32(10), cache[1].Load())
}

func TestMemoryTransport_Redelivery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []MemoryTransportOption
		failed func(attempt int32) error
		ran    int32
		logs   int
	}{
		{name: "default", failed: func(int32) error { return ErrOutOfStock }, ran: 3, logs: 1},
		{name: "one_attempt", opts: []MemoryTransportOption{MemoryMaxAttempts(1)}, failed: func(int32) error { return ErrOutOfStock }, ran: 1, logs: 1},
		{name: "succeeds_third", opts: []MemoryTransportOption{MemoryMaxAttempts(5)}, failed: func(attempt int32) error {
			if attempt <= 2 {
				return ErrOutOfStock
			}
			return nil
		}, ran: 3},
		{name: "permanent", failed: func(int32) error { return PermanentError(ErrBadOrder) }, ran: 1, logs: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 10*time.Second)

			logs := &lineRecorder{}
			transport := NewMemoryEventTransport(append([]MemoryTransportOption{MemoryLogger(logs.logf)}, tt.opts...)...)
			var ran atomic.Int32
			root := memoryRoot(t, transport, func(r *RootScope) {
				Listen(r, func(context.Context, OrderPlaced) error {
					return tt.failed(ran.Add(1))
				}, ListenerID("x"), ListenerDelivery(DeliverOnce))
			})

			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			waitIdle(t, transport)

			assert.Equal(t, tt.ran, ran.Load())
			assert.Len(t, logs.list(), tt.logs)
		})
	}
}

func TestMemoryTransport_NewGroupAtLatest(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 10*time.Second)

	transport := NewMemoryEventTransport()
	a := memoryRoot(t, transport, func(*RootScope) {})
	for range 5 {
		require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "early"}))
	}
	var late atomic.Int32
	memoryRoot(t, transport, func(r *RootScope) {
		Listen(r, func(context.Context, OrderPlaced) error {
			late.Add(1)
			return nil
		}, ListenerID("late"), ListenerDelivery(DeliverOnce))
	})

	require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "1"}))
	waitIdle(t, transport)

	assert.Equal(t, int32(1), late.Load())
}

func TestMemoryTransport_InvalidOptions(t *testing.T) {
	t.Parallel()

	assert.PanicsWithError(t, "doger: invalid MemoryMaxAttempts 0", func() {
		NewMemoryEventTransport(MemoryMaxAttempts(0))
	})
}

func TestMemoryTransport_StopTimeoutKeepsInFlight(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	transport := NewMemoryEventTransport(MemoryLogger(func(string, ...any) {}))
	sub := Subscription{Destination: "orders.placed", Group: "g"}
	handling := make(chan struct{})
	stop, err := transport.Subscribe(context.Background(), sub, func(ctx context.Context, _ Envelope) error {
		close(handling)
		<-ctx.Done()
		return ctx.Err()
	})
	require.NoError(t, err)
	require.NoError(t, transport.Send(context.Background(), Envelope{ID: "e1", Destination: "orders.placed"}))
	<-handling

	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, stop(stopCtx), context.DeadlineExceeded)

	received := make(chan string, 1)
	stopNext, err := transport.Subscribe(context.Background(), sub, func(_ context.Context, env Envelope) error {
		received <- env.ID
		return nil
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, stopNext(context.Background())) }()

	assert.Equal(t, "e1", <-received, "the in-flight envelope goes to the group's next subscription")
}
