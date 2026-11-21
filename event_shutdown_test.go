package doger

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evDB is a Shutdowner service recording its shutdown, and optionally
// running onShutdown first.
type evDB struct {
	rec        *syncRecorder
	label      string
	onShutdown func()
}

func (db *evDB) Shutdown() error {
	if db.onShutdown != nil {
		db.onShutdown()
	}
	db.rec.add(db.label)
	return nil
}

// listenDeliveredOrCancelled registers on i an async OrderPlaced listener
// that signals started, then records "delivered" once release is closed or
// "cancelled" once its context ends.
func listenDeliveredOrCancelled(i Injector, rec *syncRecorder, release <-chan struct{}) <-chan struct{} {
	started := make(chan struct{}, 1)
	Listen(i, func(ctx context.Context, _ OrderPlaced) error {
		started <- struct{}{}
		select {
		case <-release:
			rec.add("delivered")
		case <-ctx.Done():
			rec.add("cancelled")
		}
		return nil
	}, ListenerAsync())

	return started
}

func TestShutdown_Drain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		timeout  time.Duration
		release  bool
		record   []string
		matchErr error
	}{
		{name: "released after 10ms", release: true, record: []string{"delivered", "db-closed"}},
		// A shutdown context that ended skips the Shutdown of the services
		// it then reaches (container-shutdown rules), so "db-closed" is not
		// recorded once the drain used the whole context.
		{name: "context timing out at 20ms", timeout: 20 * time.Millisecond, record: []string{"cancelled"}, matchErr: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			rec := &syncRecorder{}
			ProvideNamed(root, "*DB", func(Injector) (*evDB, error) { return &evDB{rec: rec, label: "db-closed"}, nil })
			_, err := InvokeNamed[*evDB](root, "*DB")
			require.NoError(t, err)
			release := make(chan struct{})
			started := listenDeliveredOrCancelled(root, rec, release)
			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			<-started

			ctx := context.Background()
			if tt.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}
			if tt.release {
				time.AfterFunc(10*time.Millisecond, func() { close(release) })
			}
			report := root.ShutdownWithContext(ctx)

			// a delivery cancelled by the drain deadline ends on its own,
			// possibly after Shutdown returned
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				assert.Equal(c, tt.record, rec.list())
			}, time.Second, time.Millisecond)
			if tt.matchErr == nil {
				assert.True(t, report.Succeed, report.Error())
				return
			}
			assert.False(t, report.Succeed)
			require.ErrorIs(t, report, tt.matchErr)
		})
	}
}

func TestShutdown_ReleasesWaiters(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := NewWithOpts(&InjectorOpts{EventAsyncParallelism: 1})
	blocking := listenBlocking(root)
	blocking.fill(t, root, 1)

	waiting := make(chan error, 1)
	go func() { waiting <- Publish(context.Background(), root, OrderPlaced{ID: "2"}) }()
	time.Sleep(20 * time.Millisecond) // let the second Publish wait for the slot

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	root.ShutdownWithContext(ctx)

	select {
	case err := <-waiting:
		require.ErrorIs(t, err, ErrPublishAfterShutdown)
	case <-time.After(time.Second):
		t.Fatal("the waiting Publish was not released within 1s")
	}
}

// builtListener and lazyListener are service listeners of OrderPlaced.
type (
	builtListener struct{ calls *atomic.Int32 }
	lazyListener  struct{}
)

func (l *builtListener) OnEvent(context.Context, OrderPlaced) error {
	l.calls.Add(1)
	return nil
}

func (*lazyListener) OnEvent(context.Context, OrderPlaced) error { return nil }

func TestShutdown_PublishAfter(t *testing.T) {
	t.Parallel()

	root := New()
	var syncCalls, builtCalls, lazyBuilds atomic.Int32
	Listen(root, func(context.Context, OrderPlaced) error {
		syncCalls.Add(1)
		return nil
	})
	ProvideNamed(root, "*Built", func(Injector) (*builtListener, error) { return &builtListener{calls: &builtCalls}, nil }, AsListener[OrderPlaced]())
	_, err := InvokeNamed[*builtListener](root, "*Built")
	require.NoError(t, err)
	ProvideNamed(root, "*Lazy", func(Injector) (*lazyListener, error) {
		lazyBuilds.Add(1)
		return &lazyListener{}, nil
	}, AsListener[OrderPlaced]())
	Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerAsync())
	var fromShutdown error
	rec := &syncRecorder{}
	ProvideNamed(root, "*DB", func(i Injector) (*evDB, error) {
		// depends on *Built, so it shuts down, and publishes, before *Built
		if _, err := InvokeNamed[*builtListener](i, "*Built"); err != nil {
			return nil, err
		}
		return &evDB{rec: rec, label: "db-closed", onShutdown: func() {
			fromShutdown = Publish(context.Background(), root, OrderPlaced{ID: "1"})
		}}, nil
	})
	_, err = InvokeNamed[*evDB](root, "*DB")
	require.NoError(t, err)

	root.Shutdown()
	after := Publish(context.Background(), root, OrderPlaced{ID: "2"})

	require.ErrorIs(t, fromShutdown, ErrPublishAfterShutdown)
	assert.Equal(t, int32(1), syncCalls.Load())
	assert.Equal(t, int32(1), builtCalls.Load())
	assert.Zero(t, lazyBuilds.Load())
	require.ErrorIs(t, after, ErrPublishAfterShutdown)
}

// closedListener is a service listener of ContainerClosed.
type closedListener struct{ calls *atomic.Int32 }

func (l *closedListener) OnEvent(context.Context, ContainerClosed) error {
	l.calls.Add(1)
	return nil
}

func TestShutdown_ClosedFunctionOnly(t *testing.T) {
	t.Parallel()

	root := New()
	var serviceCalls, funcCalls atomic.Int32
	ProvideNamed(root, "*Mailer", func(Injector) (*closedListener, error) { return &closedListener{calls: &serviceCalls}, nil }, AsListener[ContainerClosed]())
	_, err := InvokeNamed[*closedListener](root, "*Mailer")
	require.NoError(t, err)
	Listen(root, func(context.Context, ContainerClosed) error {
		funcCalls.Add(1)
		return nil
	})

	root.Shutdown()
	root.Shutdown()

	assert.Equal(t, int32(1), funcCalls.Load())
	assert.Zero(t, serviceCalls.Load())
}

func TestScopeShutdown_Drain(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	defer root.Shutdown()
	req := root.Scope("req")
	rec := &syncRecorder{}
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	Listen(req, func(context.Context, OrderPlaced) error {
		started <- struct{}{}
		<-release
		rec.add("delivered")
		return nil
	}, ListenerAsync())
	ProvideNamed(req, "*Conn", func(Injector) (*evDB, error) { return &evDB{rec: rec, label: "conn-closed"}, nil })
	_, err := InvokeNamed[*evDB](req, "*Conn")
	require.NoError(t, err)
	require.NoError(t, Publish(context.Background(), req, OrderPlaced{ID: "1"}))
	<-started

	time.AfterFunc(10*time.Millisecond, func() { close(release) })
	req.Shutdown()

	assert.Equal(t, []string{"delivered", "conn-closed"}, rec.list())
}

// publishOnShutdown registers on root a built Shutdowner service "*DB" that
// publishes OrderPlaced{ID: "1"} through root from its Shutdown, and
// returns the result of that Publish, read once root shut down. "*DB"
// depends on the services named deps, so it shuts down before them.
func publishOnShutdown(t *testing.T, root *RootScope, deps ...string) *error {
	t.Helper()

	var fromShutdown error
	ProvideNamed(root, "*DB", func(i Injector) (*evDB, error) {
		for _, name := range deps {
			if _, err := InvokeNamed[any](i, name); err != nil {
				return nil, err
			}
		}
		return &evDB{rec: &syncRecorder{}, label: "db-closed", onShutdown: func() {
			fromShutdown = Publish(context.Background(), root, OrderPlaced{ID: "1"})
		}}, nil
	})
	_, err := InvokeNamed[*evDB](root, "*DB")
	require.NoError(t, err)

	return &fromShutdown
}

func TestShutdown_SyncListenersRunAfterAsyncRefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode EventErrorMode
	}{
		{name: "EventFailFast", mode: EventFailFast},
		{name: "EventContinue", mode: EventContinue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{EventErrorMode: tt.mode})
			Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerAsync())
			var syncCalls atomic.Int32
			Listen(root, func(context.Context, OrderPlaced) error {
				syncCalls.Add(1)
				return nil
			})
			fromShutdown := publishOnShutdown(t, root)

			root.Shutdown()

			require.ErrorIs(t, *fromShutdown, ErrPublishAfterShutdown)
			assert.Equal(t, int32(1), syncCalls.Load())
		})
	}
}

// shutdownErrListener is a service listener whose OnEvent fails with an
// error matching ErrShutdown.
type shutdownErrListener struct{}

func (*shutdownErrListener) OnEvent(context.Context, OrderPlaced) error {
	return fmt.Errorf("mailer closing: %w", ErrShutdown)
}

func TestShutdown_ListenerErrorMatchingErrShutdown(t *testing.T) {
	t.Parallel()

	root := New()
	ProvideNamed(root, "*Mailer", func(Injector) (*shutdownErrListener, error) { return &shutdownErrListener{}, nil }, AsListener[OrderPlaced]())
	_, err := InvokeNamed[*shutdownErrListener](root, "*Mailer")
	require.NoError(t, err)
	fromShutdown := publishOnShutdown(t, root, "*Mailer")

	root.Shutdown()

	var listenerErr *ListenerError
	require.ErrorAs(t, *fromShutdown, &listenerErr)
	require.ErrorIs(t, listenerErr, ErrShutdown)
	assert.ErrorContains(t, listenerErr, "mailer closing")
}

func TestShutdown_FromAsyncListener(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		child bool // the listener lives in, and shuts down, a child scope; the root otherwise
	}{
		{name: "its own child scope", child: true},
		{name: "the root", child: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 2*time.Second)

			root := New()
			defer root.Shutdown()
			var scope Injector = root
			shutdown := func(ctx context.Context) *ShutdownReport { return root.ShutdownWithContext(ctx) }
			if tt.child {
				req := root.Scope("req")
				scope = req
				shutdown = req.ShutdownWithContext
			}
			reports := make(chan *ShutdownReport, 1)
			Listen(scope, func(ctx context.Context, _ OrderPlaced) error {
				reports <- shutdown(ctx)
				return nil
			}, ListenerAsync())

			require.NoError(t, Publish(context.Background(), scope, OrderPlaced{ID: "1"}))

			select {
			case report := <-reports:
				assert.True(t, report.Succeed, report.Error())
			case <-time.After(time.Second):
				t.Fatal("the shutdown called from the async listener did not return within 1s")
			}
		})
	}
}

func TestScopeShutdown_RefusesDeliveryAdmittedAfter(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	defer root.Shutdown()
	req := root.Scope("req")
	filtering, releaseFilter := make(chan struct{}), make(chan struct{})
	delivered := make(chan struct{}, 1)
	Listen(req, func(context.Context, OrderPlaced) error {
		delivered <- struct{}{}
		return nil
	}, ListenerAsync(), ListenerFilter(func(OrderPlaced) bool {
		close(filtering)
		<-releaseFilter
		return true
	}))
	published := make(chan error, 1)
	go func() { published <- Publish(context.Background(), req, OrderPlaced{ID: "1"}) }()
	<-filtering

	req.Shutdown()
	close(releaseFilter)

	require.ErrorIs(t, <-published, ErrPublishAfterShutdown)
	select {
	case <-delivered:
		t.Fatal("an async delivery ran after its scope's Shutdown returned")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestShutdown_DrainHonoursDeadline(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 3*time.Second)

	root := New()
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 1)
	Listen(root, func(context.Context, OrderPlaced) error { // ignores its context's cancellation
		started <- struct{}{}
		<-release
		return nil
	}, ListenerAsync())
	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	begin := time.Now()
	report := root.ShutdownWithContext(ctx)
	elapsed := time.Since(begin)

	require.ErrorIs(t, report, context.DeadlineExceeded)
	assert.Less(t, elapsed, 500*time.Millisecond, "ShutdownWithContext overran its 20ms context")
}

func TestScopeShutdown_PublishingDelivery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		executor EventExecutor
		want     []string
	}{
		// the publishing delivery runs on its own goroutine: the drain waits for it
		{name: "elsewhere", want: []string{"publisher:done", "shutdown:returned"}},
		// the executor runs the published delivery inside the publisher's Submit:
		// waiting for the publisher would never end
		{name: "inline", executor: &inlineExecutor{}, want: []string{"shutdown:returned", "publisher:done"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			root := NewWithOpts(&InjectorOpts{EventExecutor: tt.executor})
			defer root.Shutdown()
			req := root.Scope("req")
			rec := &syncRecorder{}
			release := make(chan struct{})
			Listen(req, func(ctx context.Context, _ OrderPlaced) error {
				if err := Publish(ctx, req, OrderCancelled{ID: "1"}); err != nil {
					return err
				}
				<-release
				rec.add("publisher:done")
				return nil
			}, ListenerAsync())
			reports := make(chan *ShutdownReport, 1)
			Listen(req, func(ctx context.Context, _ OrderCancelled) error {
				reports <- req.ShutdownWithContext(ctx)
				rec.add("shutdown:returned")
				return nil
			}, ListenerAsync())

			published := make(chan error, 1)
			go func() { published <- Publish(context.Background(), req, OrderPlaced{ID: "1"}) }()
			time.Sleep(50 * time.Millisecond)
			close(release)
			report := <-reports
			require.NoError(t, <-published)

			assert.True(t, report.Succeed, report.Error())
			assert.Eventually(t, func() bool { return len(rec.list()) == 2 }, time.Second, 5*time.Millisecond)
			assert.Equal(t, tt.want, rec.list())
		})
	}
}
