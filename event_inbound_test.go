package doger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ErrBadOrder is the error listeners mark permanent.
var ErrBadOrder = errors.New("bad order")

// twoRoots returns roots "a" and "b" sharing transport, OrderPlaced
// registered as "orders.placed" on both, externalized immediately on "a",
// with the remote listeners registerB adds to "b"; both are started.
func twoRoots(t *testing.T, transport EventTransport, registerB func(b *RootScope)) (a, b *RootScope) {
	t.Helper()
	a = NewWithOpts(&InjectorOpts{EventTransport: transport})
	b = NewWithOpts(&InjectorOpts{EventTransport: transport})
	t.Cleanup(func() {
		a.Shutdown()
		b.Shutdown()
	})
	immediateOrders(t, a)
	require.NoError(t, RegisterEventType[OrderPlaced](b, "orders.placed"))
	registerB(b)
	require.NoError(t, a.Start())
	require.NoError(t, b.Start())

	return a, b
}

func TestInbound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []ListenerOption
		fn     func(context.Context, OrderPlaced) error
		amount int
		ran    int32
		check  func(t *testing.T, err error)
	}{
		{name: "ok", amount: 10, ran: 1},
		{name: "filtered_out", opts: []ListenerOption{ListenerFilter(func(e OrderPlaced) bool { return e.Amount > 100 })}, amount: 10},
		{name: "filtered_in", opts: []ListenerOption{ListenerFilter(func(e OrderPlaced) bool { return e.Amount > 100 })}, amount: 500, ran: 1},
		{name: "error", fn: func(context.Context, OrderPlaced) error { return ErrOutOfStock }, amount: 10, ran: 1, check: func(t *testing.T, err error) {
			t.Helper()
			var listenerErr *ListenerError
			require.ErrorAs(t, err, &listenerErr)
			assert.Equal(t, "x", listenerErr.Listener)
			require.ErrorIs(t, err, ErrOutOfStock)
		}},
		{name: "panic", fn: func(context.Context, OrderPlaced) error { panic("kaboom") }, amount: 10, ran: 1, check: func(t *testing.T, err error) {
			t.Helper()
			var listenerErr *ListenerError
			require.ErrorAs(t, err, &listenerErr)
			assert.Contains(t, err.Error(), "kaboom")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{inner: &loopbackTransport{}}
			var xRan, yRan atomic.Int32
			a, _ := twoRoots(t, transport, func(b *RootScope) {
				Listen(b, func(ctx context.Context, e OrderPlaced) error {
					xRan.Add(1)
					if tt.fn != nil {
						return tt.fn(ctx, e)
					}
					return nil
				}, append([]ListenerOption{ListenerID("x"), ListenerDelivery(DeliverOnce)}, tt.opts...)...)
				Listen(b, func(context.Context, OrderPlaced) error {
					yRan.Add(1)
					return nil
				}, ListenerID("y"))
			})

			require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "1", Amount: tt.amount}))

			assert.Equal(t, tt.ran, xRan.Load())
			assert.Zero(t, yRan.Load())
			results := transport.handlerResults()
			require.Len(t, results, 1)
			if tt.check != nil {
				tt.check(t, results[0])
			} else {
				require.NoError(t, results[0])
			}
		})
	}
}

func TestInbound_LazyOnce(t *testing.T) {
	t.Parallel()

	calls := 0
	a, _ := twoRoots(t, &loopbackTransport{}, func(b *RootScope) {
		Provide(b, func(Injector) (*Mailer, error) {
			calls++
			return &Mailer{}, nil
		}, ListenMethod((*Mailer).OnPlaced, ListenerDelivery(DeliverOnce)))
	})
	assert.Zero(t, calls, "not built by Start")

	for range 3 {
		require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "1"}))
	}

	assert.Equal(t, 1, calls)
}

func TestInbound_ResolvesPerDelivery(t *testing.T) {
	t.Parallel()

	rec := &syncRecorder{}
	var b *RootScope
	a, _ := twoRoots(t, &loopbackTransport{}, func(root *RootScope) {
		b = root
		ProvideValue(b, &syncNamedListener{name: "old", rec: rec}, AsListener[OrderPlaced](ListenerID("n"), ListenerDelivery(DeliverOnce)))
	})
	require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "1"}))

	OverrideValue(b, &syncNamedListener{name: "new", rec: rec})
	require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "2"}))

	assert.Equal(t, []string{"old", "new"}, rec.list())
}

// syncNamedListener is a listener service recording its name in a
// syncRecorder.
type syncNamedListener struct {
	name string
	rec  *syncRecorder
}

func (l *syncNamedListener) OnEvent(context.Context, OrderPlaced) error {
	l.rec.add(l.name)
	return nil
}

func TestInbound_Decode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		eventType   string
		contentType string
		payload     string
		ran         int
		err         error // nil: any permanent error, when permanent
		permanent   bool
	}{
		{name: "name", eventType: "orders.placed", contentType: "application/json", payload: `{"ID":"1"}`, ran: 1},
		{name: "alias", eventType: "order.created", contentType: "application/json", payload: `{"ID":"1"}`, ran: 1},
		{name: "content_type", eventType: "orders.placed", contentType: "text/csv", payload: "1", err: ErrCodecMismatch, permanent: true},
		{name: "payload", eventType: "orders.placed", contentType: "application/json", payload: "{", permanent: true},
		{name: "unknown_type", eventType: "orders.unknown", contentType: "application/json", payload: "{}", err: ErrEventTypeNotFound, permanent: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport})
			t.Cleanup(func() { root.Shutdown() })
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventAlias("order.created")))
			ran := 0
			Listen(root, func(context.Context, OrderPlaced) error {
				ran++
				return nil
			}, ListenerID("x"), ListenerDelivery(DeliverOnce))
			require.NoError(t, root.Start())

			err := transport.handler(0)(context.Background(), Envelope{
				ID: "id-1", Destination: "orders.placed", EventType: tt.eventType,
				ContentType: tt.contentType, Version: 1, Payload: []byte(tt.payload),
			})

			assert.Equal(t, tt.ran, ran)
			if !tt.permanent {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, IsPermanentError(err), err)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			}
		})
	}
}

func TestPermanentError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		eval func() bool
	}{
		{name: "marked", eval: func() bool { return IsPermanentError(PermanentError(ErrBadOrder)) }},
		{name: "unwraps", eval: func() bool { return errors.Is(PermanentError(ErrBadOrder), ErrBadOrder) }},
		{name: "unmarked", eval: func() bool { return !IsPermanentError(ErrBadOrder) }},
		{name: "nil", eval: func() bool { return PermanentError(nil) == nil }},
		{name: "wrapped", eval: func() bool { return IsPermanentError(fmt.Errorf("wrap: %w", PermanentError(ErrBadOrder))) }},
		{name: "listener_error", eval: func() bool {
			return IsPermanentError(&ListenerError{Listener: "x", Err: PermanentError(ErrBadOrder)})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, tt.eval())
		})
	}
}

func TestInbound_Concurrent(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 10*time.Second)

	transport := &recordingTransport{}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	t.Cleanup(func() { root.Shutdown() })
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	var calls atomic.Int32
	mailer := &Mailer{}
	Provide(root, func(Injector) (*Mailer, error) {
		calls.Add(1)
		return mailer, nil
	}, ListenMethod((*Mailer).OnPlaced, ListenerID("mailer"), ListenerDelivery(DeliverOnce)))
	var fRan atomic.Int32
	Listen(root, func(context.Context, OrderPlaced) error {
		fRan.Add(1)
		return nil
	}, ListenerID("f"), ListenerDelivery(DeliverOnce))
	require.NoError(t, root.Start())
	env, err := root.newEnvelope(context.Background(), root.eventTypeByName("orders.placed"), OrderPlaced{ID: "1"})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 5 {
				assert.NoError(t, transport.handler(0)(context.Background(), env))
				assert.NoError(t, transport.handler(1)(context.Background(), env))
			}
		})
	}
	wg.Wait()

	assert.Len(t, mailer.Received(), 100)
	assert.Equal(t, int32(100), fRan.Load())
	assert.Equal(t, int32(1), calls.Load())
}
