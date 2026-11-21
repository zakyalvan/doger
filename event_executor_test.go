package doger

import (
	"bytes"
	"context"
	"errors"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundedEventExecutor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		n        uint
		running  int32
		panicMsg string
	}{
		{name: "n 1", n: 1, running: 1},
		{name: "n 3", n: 3, running: 3},
		{name: "n 0", n: 0, panicMsg: "doger: NewBoundedEventExecutor requires n > 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			if tt.panicMsg != "" {
				assert.PanicsWithError(t, tt.panicMsg, func() { NewBoundedEventExecutor(tt.n) })
				return
			}

			executor := NewBoundedEventExecutor(tt.n)
			release := make(chan struct{})
			started := make(chan struct{}, 5)
			var running, peak atomic.Int32
			task := func(context.Context) {
				current := running.Add(1)
				for {
					old := peak.Load()
					if current <= old || peak.CompareAndSwap(old, current) {
						break
					}
				}
				started <- struct{}{}
				<-release
				running.Add(-1)
			}
			submitted := make(chan error, 5)
			for range 5 {
				go func() { submitted <- executor.Submit(context.Background(), task) }()
			}
			for range tt.running {
				<-started
			}
			select {
			case <-started:
				t.Fatalf("more than %d tasks run at once", tt.running)
			case <-time.After(50 * time.Millisecond):
			}
			close(release)
			for range 5 {
				require.NoError(t, <-submitted)
			}
			require.NoError(t, executor.Shutdown(context.Background()))
			assert.Equal(t, tt.running, peak.Load())
		})
	}
}

// inlineExecutor runs tasks inline and counts its calls; submitErr and
// submitPanic make Submit fail instead.
type inlineExecutor struct {
	submits, shutdowns atomic.Int32
	submitErr          error
	submitPanic        any
}

func (e *inlineExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	e.submits.Add(1)
	if e.submitPanic != nil {
		panic(e.submitPanic)
	}
	if e.submitErr != nil {
		return e.submitErr
	}
	task(ctx)
	return nil
}

func (e *inlineExecutor) Shutdown(context.Context) error {
	e.shutdowns.Add(1)
	return nil
}

func TestExecutor_Custom(t *testing.T) {
	t.Parallel()

	executor := &inlineExecutor{}
	root := NewWithOpts(&InjectorOpts{EventExecutor: executor})
	var ran atomic.Int32
	Listen(root, func(context.Context, OrderPlaced) error {
		ran.Add(1)
		return nil
	}, ListenerAsync())

	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	root.Shutdown()

	assert.Equal(t, int32(2), executor.submits.Load())
	assert.Equal(t, int32(1), executor.shutdowns.Load())
	assert.Equal(t, int32(2), ran.Load())
}

// discardingExecutor accepts every task and never runs it, breaking the
// EventExecutor contract.
type discardingExecutor struct{}

func (discardingExecutor) Submit(context.Context, func(context.Context)) error { return nil }
func (discardingExecutor) Shutdown(context.Context) error                      { return nil }

func TestExecutor_DiscardedTaskBoundedByShutdownContext(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := NewWithOpts(&InjectorOpts{EventExecutor: discardingExecutor{}})
	Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerAsync())
	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	report := root.ShutdownWithContext(ctx)

	require.ErrorIs(t, report, context.DeadlineExceeded)
}

func TestExecutor_SubmitFailure(t *testing.T) {
	t.Parallel()

	errQueueFull := errors.New("queue full")
	tests := []struct {
		name     string
		executor *inlineExecutor
		check    func(t *testing.T, err error)
	}{
		{name: "Submit returns ErrQueueFull", executor: &inlineExecutor{submitErr: errQueueFull}, check: func(t *testing.T, err error) {
			t.Helper()
			require.ErrorIs(t, err, errQueueFull)
		}},
		{name: "Submit panics", executor: &inlineExecutor{submitPanic: "boom"}, check: func(t *testing.T, err error) {
			t.Helper()
			assert.Contains(t, err.Error(), "boom")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{EventExecutor: tt.executor})
			Listen(root, func(context.Context, OrderPlaced) error { return nil }, ListenerAsync(), ListenerID("x"))

			err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

			var listenerErr *ListenerError
			require.ErrorAs(t, err, &listenerErr)
			assert.Equal(t, "x", listenerErr.Listener)
			tt.check(t, err)
		})
	}
}

// executorShutdownWaiters counts the goroutines waiting in a
// BoundedEventExecutor Shutdown.
func executorShutdownWaiters() int {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2) // one stack per goroutine

	return strings.Count(buf.String(), "(*BoundedEventExecutor).Shutdown.func")
}

//nolint:paralleltest // counts goroutines of the process
func TestBoundedEventExecutor_ShutdownTimeoutsLeakNoWaiter(t *testing.T) {
	testWithTimeout(t, time.Second)

	executor := NewBoundedEventExecutor(1)
	release, started := make(chan struct{}), make(chan struct{})
	require.NoError(t, executor.Submit(context.Background(), func(context.Context) {
		close(started)
		<-release // ignores its context's cancellation
	}))
	<-started
	before := executorShutdownWaiters()

	for range 5 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		require.ErrorIs(t, executor.Shutdown(ctx), context.DeadlineExceeded)
		cancel()
	}
	waiters := executorShutdownWaiters() - before

	close(release)
	require.NoError(t, executor.Shutdown(context.Background()))
	assert.LessOrEqual(t, waiters, 1, "timed-out Shutdown calls left goroutines waiting")
}
