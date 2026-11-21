package doger

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type traceKey struct{}

func TestAsync_RunsAfterPublish(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	defer root.Shutdown()
	release := make(chan struct{})
	type observed struct {
		trace any
		err   error
	}
	ran := make(chan observed, 1)
	Listen(root, func(ctx context.Context, _ OrderPlaced) error {
		<-release
		ran <- observed{trace: ctx.Value(traceKey{}), err: ctx.Err()}
		return nil
	}, ListenerAsync())

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), traceKey{}, "t1"))
	require.NoError(t, Publish(ctx, root, OrderPlaced{ID: "1"}))
	cancel()
	close(release)

	select {
	case got := <-ran:
		assert.Equal(t, "t1", got.trace)
		assert.NoError(t, got.err)
	case <-time.After(time.Second):
		t.Fatal("the async listener did not run within 1s")
	}
}

// captureStdLog redirects the standard logger to a buffer until the test
// ends. Tests using it must not run in parallel.
func captureStdLog(t *testing.T) *syncBuffer {
	t.Helper()

	buffer := &syncBuffer{}
	previous, flags := log.Writer(), log.Flags()
	log.SetOutput(buffer)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previous)
		log.SetFlags(flags)
	})

	return buffer
}

// syncBuffer is a bytes.Buffer safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

//nolint:paralleltest // replaces the process's standard logger
func TestAsync_ErrorRouting(t *testing.T) {
	const message = "doger: listener `x` failed on event `doger.OrderPlaced`: out of stock"

	type hookCall struct{ err error }
	tests := []struct {
		name     string
		logger   bool
		hook     bool
		hookFail bool
		panics   bool
		check    func(t *testing.T, stdLog string, logged []logRecord, calls []hookCall)
	}{
		{
			name: "no Logger and no HookEventError",
			check: func(t *testing.T, stdLog string, _ []logRecord, _ []hookCall) {
				t.Helper()
				assert.Contains(t, stdLog, message)
			},
		},
		{
			name:   "a recording Logger and no HookEventError",
			logger: true,
			check: func(t *testing.T, stdLog string, logged []logRecord, _ []hookCall) {
				t.Helper()
				assert.Empty(t, stdLog)
				require.Len(t, logged, 1)
				assert.Contains(t, logged[0].String(), message)
			},
		},
		{
			name: "a recording HookEventError",
			hook: true, logger: true,
			check: func(t *testing.T, stdLog string, logged []logRecord, calls []hookCall) {
				t.Helper()
				require.Len(t, calls, 1)
				var listenerErr *ListenerError
				require.ErrorAs(t, calls[0].err, &listenerErr)
				assert.Equal(t, "x", listenerErr.Listener)
				require.ErrorIs(t, calls[0].err, ErrOutOfStock)
				assert.Empty(t, stdLog)
				assert.Empty(t, logged)
			},
		},
		{
			name: "a recording HookEventError and a panicking listener",
			hook: true, panics: true,
			check: func(t *testing.T, _ string, _ []logRecord, calls []hookCall) {
				t.Helper()
				require.Len(t, calls, 1)
				var listenerErr *ListenerError
				require.ErrorAs(t, calls[0].err, &listenerErr)
				assert.Equal(t, "x", listenerErr.Listener)
				assert.Contains(t, calls[0].err.Error(), "kaboom")
			},
		},
		{
			name: "a panicking HookEventError",
			hook: true, hookFail: true,
			check: func(t *testing.T, stdLog string, _ []logRecord, _ []hookCall) {
				t.Helper()
				assert.Contains(t, stdLog, "hookfail")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testWithTimeout(t, time.Second)
			stdLog := captureStdLog(t)

			opts := &InjectorOpts{}
			recorder := newLogRecorder(slog.LevelError)
			if tt.logger {
				opts.Logger = recorder.logger()
			}
			var mu sync.Mutex
			calls := []hookCall{}
			if tt.hook {
				opts.HookEventError = func(_ context.Context, _ *RootScope, err error) {
					if tt.hookFail {
						panic("hookfail")
					}
					mu.Lock()
					defer mu.Unlock()
					calls = append(calls, hookCall{err: err})
				}
			}
			root := NewWithOpts(opts)
			Listen(root, func(context.Context, OrderPlaced) error {
				if tt.panics {
					panic("kaboom")
				}
				return ErrOutOfStock
			}, ListenerID("x"), ListenerAsync())

			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			root.Shutdown()

			mu.Lock()
			defer mu.Unlock()
			recorder.mu.Lock()
			logged := append([]logRecord{}, recorder.records...)
			recorder.mu.Unlock()
			tt.check(t, stdLog.String(), eventErrorRecords(logged), calls)
		})
	}
}

// eventErrorRecords keeps the records about event listener failures.
func eventErrorRecords(records []logRecord) []logRecord {
	kept := []logRecord{}
	for _, record := range records {
		if strings.Contains(record.String(), "listener") {
			kept = append(kept, record)
		}
	}

	return kept
}

// blockingAsync is an async function listener for OrderPlaced whose
// deliveries signal started, then wait for release or their context's end.
type blockingAsync struct {
	started chan struct{}
	release chan struct{}
}

// listenBlocking registers a blockingAsync listener on i.
func listenBlocking(i Injector) *blockingAsync {
	b := &blockingAsync{started: make(chan struct{}, 1024), release: make(chan struct{})}
	Listen(i, func(ctx context.Context, _ OrderPlaced) error {
		b.started <- struct{}{}
		select {
		case <-b.release:
		case <-ctx.Done():
		}
		return nil
	}, ListenerAsync(), ListenerID("x"))

	return b
}

// fill publishes n deliveries through i and waits until they all started.
func (b *blockingAsync) fill(t *testing.T, i Injector, n int) {
	t.Helper()
	for range n {
		require.NoError(t, Publish(context.Background(), i, OrderPlaced{ID: "fill"}))
		<-b.started
	}
}

// probe publishes through i with a context timing out after 50ms.
func probe(i Injector) error {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	return Publish(ctx, i, OrderPlaced{ID: "n"})
}

func TestAsync_Backpressure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		limit        uint
		inflight     int
		cancelBefore bool
		check        func(t *testing.T, err error)
	}{
		{name: "limit 4, 0 in flight", limit: 4, inflight: 0, check: noError},
		{name: "limit 4, 3 in flight", limit: 4, inflight: 3, check: noError},
		{name: "limit 4, 4 in flight", limit: 4, inflight: 4, check: listenerErrorMatching(context.DeadlineExceeded)},
		{name: "limit 1, 1 in flight", limit: 1, inflight: 1, check: listenerErrorMatching(context.DeadlineExceeded)},
		{name: "limit 4, cancelled before Publish", limit: 4, inflight: 0, cancelBefore: true, check: func(t *testing.T, err error) {
			t.Helper()
			require.ErrorIs(t, err, context.Canceled)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := NewWithOpts(&InjectorOpts{EventAsyncParallelism: tt.limit})
			blocking := listenBlocking(root)
			defer root.Shutdown()
			defer close(blocking.release)
			blocking.fill(t, root, tt.inflight)

			var err error
			if tt.cancelBefore {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				err = Publish(ctx, root, OrderPlaced{ID: "n"})
			} else {
				err = probe(root)
			}
			tt.check(t, err)
		})
	}
}

func noError(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}

// listenerErrorMatching checks for a *ListenerError matching target.
func listenerErrorMatching(target error) func(t *testing.T, err error) {
	return func(t *testing.T, err error) {
		t.Helper()
		var listenerErr *ListenerError
		require.ErrorAs(t, err, &listenerErr)
		require.ErrorIs(t, err, target)
	}
}

//nolint:paralleltest // sets GOMAXPROCS for the process
func TestAsync_DefaultParallelism(t *testing.T) {
	tests := []struct {
		name  string
		procs int
		limit int
	}{
		{name: "GOMAXPROCS 1", procs: 1, limit: 4},
		{name: "GOMAXPROCS 2", procs: 2, limit: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testWithTimeout(t, time.Second)

			previous := runtime.GOMAXPROCS(tt.procs)
			root := New()
			runtime.GOMAXPROCS(previous)
			blocking := listenBlocking(root)
			defer root.Shutdown()
			defer close(blocking.release)

			blocking.fill(t, root, tt.limit)
			listenerErrorMatching(context.DeadlineExceeded)(t, probe(root))
		})
	}
}

func TestAsync_NestedNoDeadlock(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := NewWithOpts(&InjectorOpts{EventAsyncParallelism: 1})
	defer root.Shutdown()
	recorded := make(chan string, 1)
	Listen(root, func(ctx context.Context, _ OrderPlaced) error {
		return Publish(ctx, root, InvoiceRequested{ID: "1"})
	}, ListenerAsync())
	Listen(root, func(context.Context, InvoiceRequested) error {
		recorded <- "i"
		return nil
	}, ListenerAsync())

	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))

	select {
	case name := <-recorded:
		assert.Equal(t, "i", name)
	case <-time.After(time.Second):
		t.Fatal(`"i" was not recorded within 1s`)
	}
}

// asyncDependent is an async listener service that depends on *Mailer.
type asyncDependent struct{ received chan<- string }

func (d *asyncDependent) OnEvent(_ context.Context, e OrderPlaced) error {
	d.received <- e.ID
	return nil
}

// TestAsync_ServiceBuiltBeforeShutdown pins that an async listener service
// is resolved when its delivery is submitted: a root shutdown beginning
// right after Publish returned closes the root, which would refuse a build
// started by the delivery itself.
func TestAsync_ServiceBuiltBeforeShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	var mu sync.Mutex
	reported := []error{}
	root := NewWithOpts(&InjectorOpts{HookEventError: func(_ context.Context, _ *RootScope, err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err)
	}})
	received := make(chan string, 1)
	ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) { return &Mailer{}, nil })
	ProvideNamed(root, "dependent", func(i Injector) (*asyncDependent, error) {
		if _, err := InvokeNamed[*Mailer](i, "*Mailer"); err != nil {
			return nil, err
		}
		return &asyncDependent{received: received}, nil
	}, AsListener[OrderPlaced](ListenerAsync()))

	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	report := root.Shutdown()

	assert.True(t, report.Succeed, report.Error())
	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, reported)
	select {
	case id := <-received:
		assert.Equal(t, "1", id)
	default:
		t.Fatal("the async listener service did not receive the event")
	}
}
