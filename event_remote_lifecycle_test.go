package doger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ErrStopFailed is the error of the subscription stops that fail.
var ErrStopFailed = errors.New("stop failed")

// startRecorder records "start:<name>" when it starts.
type startRecorder struct {
	name string
	rec  *syncRecorder
}

func (s *startRecorder) Start(context.Context) error {
	s.rec.add("start:" + s.name)
	return nil
}

func TestRemote_SubscribeOrder(t *testing.T) {
	t.Parallel()

	rec := &syncRecorder{}
	transport := &recordingTransport{onSubscribe: func(sub Subscription) error {
		rec.add("subscribe:" + sub.Group)
		return nil
	}}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	t.Cleanup(func() { root.Shutdown() })
	ProvideNamed(root, "db", func(Injector) (*startRecorder, error) { return &startRecorder{name: "db", rec: rec}, nil }, AsStarter())
	root.AddContainerStartedHook(func(context.Context, *RootScope) { rec.add("hook:started") })
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	Listen(root, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))

	require.NoError(t, root.Start())

	assert.Equal(t, []string{"start:db", "subscribe:r", "hook:started"}, rec.list())
}

func TestRemote_SubscribeError(t *testing.T) {
	t.Parallel()

	transport := &recordingTransport{onSubscribe: func(sub Subscription) error {
		if sub.Destination == "orders.cancelled" {
			return ErrNoBroker
		}
		return nil
	}}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	t.Cleanup(func() { root.Shutdown() })
	var failedWith error
	root.AddContainerFailedHook(func(_ context.Context, _ *RootScope, err error) { failedWith = err })
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	require.NoError(t, RegisterEventType[OrderCancelled](root, "orders.cancelled"))
	Listen(root, noopOrderListener, ListenerID("a"), ListenerDelivery(DeliverOnce))
	Listen(root, func(context.Context, OrderCancelled) error { return nil }, ListenerID("b"), ListenerDelivery(DeliverOnce))

	err := root.Start()

	require.ErrorIs(t, err, ErrNoBroker)
	assert.Equal(t, []string{"subscribe:a", "subscribe:b", "unsubscribe:a"}, transport.callList())
	require.ErrorIs(t, failedWith, ErrNoBroker)
}

func TestRemote_ShutdownWaits(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	rec := &syncRecorder{}
	inner := &waitingTransport{}
	transport := &recordingTransport{inner: inner, onStop: func(sub Subscription) error {
		rec.add("unsubscribe:" + sub.Group)
		return nil
	}}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	root.AddContainerClosingHook(func(context.Context, *RootScope) { rec.add("hook:closing") })
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	release, handling := make(chan struct{}), make(chan struct{})
	Listen(root, func(context.Context, OrderPlaced) error {
		close(handling)
		<-release
		rec.add("handler:r:done")
		return nil
	}, ListenerID("r"), ListenerDelivery(DeliverOnce))
	require.NoError(t, root.Start())
	env, err := root.newEnvelope(context.Background(), root.eventTypeByName("orders.placed"), OrderPlaced{ID: "1"})
	require.NoError(t, err)
	inner.deliver(0, env)
	<-handling

	done := make(chan *ShutdownReport)
	go func() { done <- root.Shutdown() }()
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, rec.list(), "the stop waits for the handler")
	close(release)
	<-done

	assert.Equal(t, []string{"handler:r:done", "unsubscribe:r", "hook:closing"}, rec.list())
}

func TestRemote_ShutdownDeadline(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	inner := &waitingTransport{}
	root := NewWithOpts(&InjectorOpts{EventTransport: inner})
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	handling, cancelled := make(chan struct{}), make(chan error, 1)
	Listen(root, func(ctx context.Context, _ OrderPlaced) error {
		close(handling)
		<-ctx.Done()
		cancelled <- ctx.Err()
		return ctx.Err()
	}, ListenerID("r"), ListenerDelivery(DeliverOnce))
	require.NoError(t, root.Start())
	env, err := root.newEnvelope(context.Background(), root.eventTypeByName("orders.placed"), OrderPlaced{ID: "1"})
	require.NoError(t, err)
	inner.deliver(0, env)
	<-handling

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	root.ShutdownWithContext(ctx)

	require.ErrorIs(t, <-cancelled, context.Canceled)
}

func TestRemote_StopError(t *testing.T) {
	t.Parallel()

	errs := &errorRecorder{}
	transport := &recordingTransport{onStop: func(Subscription) error { return ErrStopFailed }}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport, HookEventError: errs.hook})
	closing := false
	root.AddContainerClosingHook(func(context.Context, *RootScope) { closing = true })
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	Listen(root, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
	require.NoError(t, root.Start())

	root.Shutdown()

	got := errs.list()
	require.Len(t, got, 1)
	require.ErrorIs(t, got[0], ErrStopFailed)
	assert.True(t, closing, "the remaining shutdown steps ran")
}

func TestRemote_ListenStopClosesSubscription(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	transport := NewMemoryEventTransport()
	a := memoryRoot(t, transport, func(*RootScope) {})
	var ran atomic.Int32
	var stop func() bool
	memoryRoot(t, transport, func(b *RootScope) {
		stop = Listen(b, func(context.Context, OrderPlaced) error {
			ran.Add(1)
			return nil
		}, ListenerID("r"), ListenerDelivery(DeliverOnce))
	})
	require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "1"}))
	waitIdle(t, transport)
	require.Equal(t, int32(1), ran.Load())

	require.True(t, stop())
	require.NoError(t, Publish(context.Background(), a, OrderPlaced{ID: "2"}))
	waitIdle(t, transport)

	assert.Equal(t, int32(1), ran.Load(), "no event after stop")
	assert.False(t, stop())
}
