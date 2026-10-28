package doger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// healthRunTestPanicking is a Healthchecker whose check panics with value.
type healthRunTestPanicking struct{ value any }

func (h *healthRunTestPanicking) HealthCheck() error { panic(h.value) }

// healthRunTestPanicRecorder records the HookContainerPanic calls it gets.
type healthRunTestPanicRecorder struct {
	mu    sync.Mutex
	calls [][2]any // (point, value)
}

func (r *healthRunTestPanicRecorder) hook(_ *RootScope, point string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]any{point, value})
}

func (r *healthRunTestPanicRecorder) recorded() [][2]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]any(nil), r.calls...)
}

func TestHealthCheck_Panic(t *testing.T) {
	t.Parallel()

	rootCheck := func(i *RootScope) error { return i.HealthCheck()["db"] }
	namedCheck := func(i *RootScope) error { return HealthCheckNamed(i, "db") }

	tests := []struct {
		name     string
		skip     string
		withHook bool
		value    any
		entry    func(i *RootScope) error
		message  string
		match    func(t *testing.T, err error)
	}{
		{
			name:    "no_hook_boom_HealthCheck",
			value:   "boom",
			entry:   rootCheck,
			message: "DI: health check panic: boom",
		},
		{
			name:    "no_hook_EOF_HealthCheckNamed",
			value:   io.EOF,
			entry:   namedCheck,
			message: "DI: health check panic: EOF",
			match:   func(t *testing.T, err error) { t.Helper(); assert.ErrorIs(t, err, io.EOF) },
		},
		{
			name:    "no_hook_nil_HealthReport",
			value:   nil,
			entry:   func(i *RootScope) error { return healthRunTestReportErr(i, "db") },
			message: "DI: health check panic: " + new(runtime.PanicNilError).Error(),
			match: func(t *testing.T, err error) {
				t.Helper()
				var nilErr *runtime.PanicNilError
				assert.ErrorAs(t, err, &nilErr)
			},
		},
		{
			name:     "hook_boom_HealthCheck",
			withHook: true,
			value:    "boom",
			entry:    rootCheck,
			message:  "DI: health check panic: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			testWithTimeout(t, 5*time.Second)

			recorder := &healthRunTestPanicRecorder{}
			opts := &InjectorOpts{}
			if tt.withHook {
				opts.HookContainerPanic = recorder.hook
			}
			i := NewWithOpts(opts)
			ProvideNamedValue(i, "db", &healthRunTestPanicking{value: tt.value})

			err := tt.entry(i)

			require.Error(t, err)
			assert.EqualError(t, err, tt.message)
			assert.ErrorIs(t, err, ErrHealthCheckPanic)
			if tt.match != nil {
				tt.match(t, err)
			}
			if tt.withHook {
				assert.Equal(t, [][2]any{{"healthcheck", tt.value}}, recorder.recorded())
			} else {
				assert.Empty(t, recorder.recorded())
			}
		})
	}
}

func TestHealthCheck_HookPanicPropagates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry func(i *RootScope, scheduler *manualScheduler)
	}{
		{name: "HealthCheckNamed", entry: func(i *RootScope, _ *manualScheduler) { _ = HealthCheckNamed(i, "db") }},
		{name: "HealthCheck", entry: func(i *RootScope, _ *manualScheduler) { i.HealthCheck() }},
		{name: "HealthReport", entry: func(i *RootScope, _ *manualScheduler) { i.HealthReport() }},
		{name: "stored_round", entry: func(_ *RootScope, scheduler *manualScheduler) { scheduler.runRound() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			scheduler := &manualScheduler{}
			i := NewWithOpts(&InjectorOpts{
				HealthScheduler:    scheduler,
				HookContainerPanic: func(_ *RootScope, _ string, value any) { panic(value) },
			})
			ProvideNamedValue(i, "db", &healthRunTestPanicking{value: "boom"})
			require.NoError(t, i.Start())
			defer i.Shutdown()

			recovered := func() (recovered any) {
				defer func() { recovered = recover() }()
				tt.entry(i, scheduler)
				return nil
			}()

			assert.Equal(t, "boom", recovered)
		})
	}
}

// healthRunTestCounter is a Healthchecker that counts its calls and returns
// err.
type healthRunTestCounter struct {
	calls atomic.Int32
	err   error
}

func (c *healthRunTestCounter) HealthCheck() error {
	c.calls.Add(1)
	return c.err
}

func TestHealthCheck_EachCheckOnce(t *testing.T) {
	t.Parallel()

	errDown := errors.New("down")
	dbName := NameOf[*healthRunTestCounter]()
	hcName := NameOf[Healthchecker]()

	// each row registers its services and returns the scope-wide check to
	// run, the map it must return and the counters with their expected calls
	tests := []struct {
		name   string
		setup  func(i *RootScope, a, b *healthRunTestCounter) func() map[string]error
		errA   error
		want   map[string]error
		countA int32
		countB int32
	}{
		{
			name: "shadowed_from_child",
			setup: func(i *RootScope, a, b *healthRunTestCounter) func() map[string]error {
				ProvideNamedValue(i, "svc", a)
				child := i.Scope("child")
				ProvideNamedValue(child, "svc", b)
				return child.HealthCheck
			},
			want: map[string]error{"svc": nil}, countA: 0, countB: 1,
		},
		{
			name: "shadowed_from_root",
			setup: func(i *RootScope, a, b *healthRunTestCounter) func() map[string]error {
				ProvideNamedValue(i, "svc", a)
				ProvideNamedValue(i.Scope("child"), "svc", b)
				return i.HealthCheck
			},
			want: map[string]error{"svc": nil}, countA: 1, countB: 0,
		},
		{
			name: "alias",
			setup: func(i *RootScope, a, _ *healthRunTestCounter) func() map[string]error {
				ProvideValue(i, a)
				must0(As[*healthRunTestCounter, Healthchecker](i))
				return i.HealthCheck
			},
			want: map[string]error{dbName: nil, hcName: nil}, countA: 1,
		},
		{
			name: "alias_error",
			setup: func(i *RootScope, a, _ *healthRunTestCounter) func() map[string]error {
				ProvideValue(i, a)
				must0(As[*healthRunTestCounter, Healthchecker](i))
				return i.HealthCheck
			},
			errA: errDown,
			want: map[string]error{dbName: errDown, hcName: errDown}, countA: 1,
		},
		{
			name: "export_from_root_scope",
			setup: func(i *RootScope, a, _ *healthRunTestCounter) func() map[string]error {
				child := i.Scope("child")
				ProvideNamedValue(child, "svc", a)
				must0(ExportNamed[*healthRunTestCounter](child, "svc"))
				return i.self.HealthCheck
			},
			want: map[string]error{"svc": nil}, countA: 1,
		},
		{
			name: "export_from_source_scope",
			setup: func(i *RootScope, a, _ *healthRunTestCounter) func() map[string]error {
				child := i.Scope("child")
				ProvideNamedValue(child, "svc", a)
				must0(ExportNamed[*healthRunTestCounter](child, "svc"))
				return child.HealthCheck
			},
			want: map[string]error{"svc": nil}, countA: 1,
		},
		{
			name: "yielded_default",
			setup: func(i *RootScope, a, b *healthRunTestCounter) func() map[string]error {
				child := i.Scope("child")
				ProvideNamedValue(child, "svc", a, AsDefault())
				ProvideNamedValue(i, "svc", b)
				return child.HealthCheck
			},
			want: map[string]error{"svc": nil}, countA: 0, countB: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			a, b := &healthRunTestCounter{err: tt.errA}, &healthRunTestCounter{}
			check := tt.setup(New(), a, b)

			is.Equal(tt.want, check())
			is.Equal(tt.countA, a.calls.Load(), "calls of A")
			is.Equal(tt.countB, b.calls.Load(), "calls of B")
		})
	}
}

func TestHealthCheck_MapShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target func(i *RootScope) map[string]error
		want   map[string]error
	}{
		{
			name:   "root_scope",
			target: func(i *RootScope) map[string]error { return i.HealthCheck() },
			want:   map[string]error{"answer": nil, "db": nil, "ext:redis/pool": nil},
		},
		{
			name: "child_scope",
			target: func(i *RootScope) map[string]error {
				child, ok := i.ChildByName("child")
				require.True(t, ok)
				return child.HealthCheck()
			},
			want: map[string]error{"answer": nil, "db": nil, "cache": nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			i := New()
			ProvideNamedValue(i, "answer", 42)
			ProvideNamedValue(i, "db", &healthRunTestCounter{})
			ProvideNamedValue(i.Scope("child"), "cache", &healthRunTestCounter{})
			require.NoError(t, Extend(i, &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
				ProvideNamedValue(x.Scope(), "pool", &healthRunTestCounter{})
				return nil
			}}))

			assert.Equal(t, tt.want, tt.target(i))
		})
	}
}

// healthRunTestBlocking is a Healthchecker that counts its calls, blocks
// until released, ignoring its context, then returns err.
type healthRunTestBlocking struct {
	calls   atomic.Int32
	release chan struct{}
	err     error
}

func newHealthRunTestBlocking(err error) *healthRunTestBlocking {
	return &healthRunTestBlocking{release: make(chan struct{}), err: err}
}

func (b *healthRunTestBlocking) HealthCheck() error {
	b.calls.Add(1)
	<-b.release
	return b.err
}

// Not parallel: goleak checks the goroutines this test leaves.
func TestHealthCheck_NoPileUp(t *testing.T) { //nolint:paralleltest
	tests := []struct {
		name  string
		skip  string
		entry func(i *RootScope) error
	}{
		{name: "HealthCheck", entry: func(i *RootScope) error { return i.HealthCheck()["db"] }},
		{name: "HealthCheckNamed", entry: func(i *RootScope) error { return HealthCheckNamed(i, "db") }},
		{name: "HealthReport", entry: func(i *RootScope) error { return healthRunTestReportErr(i, "db") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			testWithTimeout(t, 10*time.Second)
			defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
			is := assert.New(t)

			i := NewWithOpts(&InjectorOpts{HealthCheckTimeout: 5 * time.Millisecond})
			db := newHealthRunTestBlocking(nil)
			ProvideNamedValue(i, "db", db)

			for range 100 {
				is.EqualError(tt.entry(i), healthCheckTestDeadlineMessage)
			}
			is.Equal(int32(1), db.calls.Load(), "calls of the check")

			close(db.release)
		})
	}
}

func TestHealthCheck_ConcurrentShareInFlight(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		i := New()
		checks := make([]*healthRunTestBlocking, 10)
		for n := range checks {
			checks[n] = newHealthRunTestBlocking(nil)
			ProvideNamedValue(i, fmt.Sprintf("svc-%d", n), checks[n])
		}

		results := make(chan map[string]error, 50)
		for range 50 {
			go func() { results <- i.HealthCheck() }()
		}
		synctest.Wait() // every call waits

		for _, check := range checks {
			close(check.release)
		}

		maps := make([]map[string]error, 0, 50)
		for range 50 {
			maps = append(maps, <-results)
		}

		for n, check := range checks {
			is.Equal(int32(1), check.calls.Load(), "calls of svc-%d", n)
		}
		for _, m := range maps {
			is.Len(m, 10)
			for name, err := range m {
				is.NoError(err, name)
			}
		}
		delete(maps[0], "svc-0")
		for _, m := range maps[1:] {
			is.Len(m, 10)
		}
	})
}

func TestHealthCheck_JoinRunning(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		errDown := errors.New("down")
		i := New()
		db := newHealthRunTestBlocking(errDown)
		ProvideNamedValue(i, "db", db)

		forced := make(chan map[string]error, 1)
		go func() { forced <- i.HealthCheck() }()
		synctest.Wait() // the forced call runs the check

		named := make(chan error, 1)
		go func() { named <- HealthCheckNamed(i, "db") }()
		synctest.Wait()

		close(db.release)

		is.Same(errDown, <-named)
		is.Same(errDown, (<-forced)["db"])
		is.Equal(int32(1), db.calls.Load(), "calls of the check")
	})
}

func TestHealthCheck_SequentialCheckAgain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry func(i *RootScope) error
		n     int32
	}{
		{name: "HealthCheck_1", entry: func(i *RootScope) error { return i.HealthCheck()["db"] }, n: 1},
		{name: "HealthCheck_3", entry: func(i *RootScope) error { return i.HealthCheck()["db"] }, n: 3},
		{name: "HealthCheckNamed_2", entry: func(i *RootScope) error { return HealthCheckNamed(i, "db") }, n: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := New()
			db := &healthRunTestCounter{}
			ProvideNamedValue(i, "db", db)

			for range tt.n {
				is.NoError(tt.entry(i))
			}
			is.Equal(tt.n, db.calls.Load(), "calls of the check")
		})
	}
}

// healthRunTestCtxAware is a HealthcheckerWithContext that counts its calls,
// tracks how many run at once, blocks until released or, when untilCtx, until
// its context is done, records its context's error and returns it.
type healthRunTestCtxAware struct {
	calls, running, maxRunning atomic.Int32
	release                    chan struct{}
	untilCtx                   bool // also return once ctx is done
	ignoreCtx                  bool // wait for release only

	mu     sync.Mutex
	ctxErr []error
}

func newHealthRunTestCtxAware() *healthRunTestCtxAware {
	return &healthRunTestCtxAware{release: make(chan struct{}), untilCtx: true}
}

func (c *healthRunTestCtxAware) HealthCheck(ctx context.Context) error {
	c.calls.Add(1)
	if n := c.running.Add(1); n > c.maxRunning.Load() {
		c.maxRunning.Store(n)
	}
	defer c.running.Add(-1)

	if c.ignoreCtx {
		<-c.release
	} else {
		select {
		case <-c.release:
		case <-ctx.Done():
		}
	}

	err := contextDone(ctx)
	c.mu.Lock()
	c.ctxErr = append(c.ctxErr, err)
	c.mu.Unlock()

	return err
}

func (c *healthRunTestCtxAware) recordedErrs() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]error(nil), c.ctxErr...)
}

func TestHealthCheck_JoinerOwnBound(t *testing.T) {
	t.Parallel()

	named := func(i *RootScope) error { return HealthCheckNamed(i, "db") }
	forced := func(i *RootScope) error { return i.HealthCheck()["db"] }

	tests := []struct {
		name    string
		timeout time.Duration
		single  time.Duration
		global  time.Duration
		entry   func(i *RootScope) error
		advance time.Duration
		want    string // "" while still waiting
	}{
		{name: "named_timeout_50ms_at_50ms", timeout: 50 * time.Millisecond, entry: named, advance: 50 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "named_timeout_50ms_at_49ms", timeout: 50 * time.Millisecond, entry: named, advance: 49 * time.Millisecond},
		{name: "named_single_20ms_at_20ms", timeout: 50 * time.Millisecond, single: 20 * time.Millisecond, entry: named, advance: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "named_single_unbounded_at_10m", timeout: 50 * time.Millisecond, single: -time.Nanosecond, entry: named, advance: 10 * time.Minute},
		{name: "forced_global_30ms_at_30ms", global: 30 * time.Millisecond, entry: forced, advance: 30 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "named_global_30ms_does_not_apply", global: 30 * time.Millisecond, entry: named, advance: 10 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				fc := clockwork.NewFakeClock()
				i := NewWithOpts(&InjectorOpts{
					Clock:                    fc,
					HealthCheckTimeout:       tt.timeout,
					HealthCheckSingleTimeout: tt.single,
					HealthCheckGlobalTimeout: tt.global,
				})
				db := newHealthRunTestBlocking(nil)
				ProvideNamedValue(i, "db", db)

				// another caller starts the check, earlier
				starter := make(chan map[string]error, 1)
				go func() { starter <- i.HealthCheck() }()
				synctest.Wait()
				fc.Advance(25 * time.Millisecond)
				synctest.Wait()

				result := make(chan error, 1)
				go func() { result <- tt.entry(i) }()
				synctest.Wait()
				fc.Advance(tt.advance)
				synctest.Wait()

				if tt.want == "" {
					is.Empty(result, "the call returned before its own bound")
				} else {
					is.Len(result, 1, "the call did not return at its own bound")
				}
				is.Equal(int32(1), db.calls.Load(), "calls of the check")

				close(db.release)
				<-starter
				err := <-result
				if tt.want != "" {
					is.EqualError(err, tt.want)
				}
			})
		})
	}
}

func TestHealthCheck_CallerLeaves(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		i := New()
		db := newHealthRunTestCtxAware()
		ProvideNamedValue(i, "db", db)

		ctxA, cancelA := context.WithCancel(context.Background())
		a := make(chan map[string]error, 1)
		go func() { a <- i.HealthCheckWithContext(ctxA) }()
		synctest.Wait() // A started the check

		b := make(chan map[string]error, 1)
		go func() { b <- i.HealthCheck() }()
		synctest.Wait() // B joined it

		cancelA()
		synctest.Wait()
		close(db.release)

		is.EqualError((<-a)["db"], "DI: health check timeout: context canceled")
		is.NoError((<-b)["db"])
	})
}

// healthRunTestTraceKey is the context key of TestHealthCheck_StarterContext.
type healthRunTestTraceKey struct{}

// healthRunTestContextRecorder is a HealthcheckerWithContext recording
// whether its context has a deadline and its trace value.
type healthRunTestContextRecorder struct {
	hasDeadline atomic.Bool
	trace       atomic.Value
}

func (r *healthRunTestContextRecorder) HealthCheck(ctx context.Context) error {
	_, ok := ctx.Deadline()
	r.hasDeadline.Store(ok)
	r.trace.Store(ctx.Value(healthRunTestTraceKey{}))
	return nil
}

func TestHealthCheck_StarterContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		skip  string
		entry func(ctx context.Context, i *RootScope) error
	}{
		{name: "HealthCheckWithContext", entry: func(ctx context.Context, i *RootScope) error { return i.HealthCheckWithContext(ctx)["c"] }},
		{name: "HealthCheckNamedWithContext", entry: func(ctx context.Context, i *RootScope) error { return HealthCheckNamedWithContext(ctx, i, "c") }},
		{name: "HealthReportWithContext", entry: func(ctx context.Context, i *RootScope) error {
			return healthRunTestReportErrWithContext(ctx, i, "c")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := New()
			c := &healthRunTestContextRecorder{}
			ProvideNamedValue(i, "c", c)

			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), healthRunTestTraceKey{}, "trace-1"), time.Minute)
			defer cancel()

			is.NoError(tt.entry(ctx, i))
			is.True(c.hasDeadline.Load(), "the check saw no deadline")
			is.Equal("trace-1", c.trace.Load())
		})
	}
}

func TestHealthCheck_LastCallerCancels(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		i := New()
		db := newHealthRunTestCtxAware()
		ProvideNamedValue(i, "db", db)

		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan map[string]error, 1)
		go func() { result <- i.HealthCheckWithContext(ctx) }()
		synctest.Wait()
		cancel()

		is.EqualError((<-result)["db"], "DI: health check timeout: context canceled")
		synctest.Wait() // the check returned
		is.Equal([]error{context.Canceled}, db.recordedErrs())

		close(db.release)
	})
}

func TestHealthCheck_AbandonedNotJoined(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		i := New()
		db := newHealthRunTestCtxAware()
		db.ignoreCtx = true
		ProvideNamedValue(i, "db", db)

		ctx, cancel := context.WithCancel(context.Background())
		first := make(chan map[string]error, 1)
		go func() { first <- i.HealthCheckWithContext(ctx) }()
		synctest.Wait()
		cancel()
		<-first // the check is left running

		second := make(chan map[string]error, 1)
		go func() { second <- i.HealthCheck() }()
		synctest.Wait()
		close(db.release)

		err := (<-second)["db"]
		is.NotErrorIs(err, context.Canceled)
		is.NoError(err)
		is.Equal(int32(2), db.calls.Load(), "calls of the check")
		is.Equal(int32(1), db.maxRunning.Load(), "checks running at once")
	})
}

// healthRunTestGate is a Healthchecker that counts the checks running at
// once on a shared gauge, blocks until released, and returns nil.
type healthRunTestGate struct {
	running, maxRunning *atomic.Int32
	release             chan struct{}
}

func (g *healthRunTestGate) HealthCheck() error {
	n := g.running.Add(1)
	for {
		current := g.maxRunning.Load()
		if n <= current || g.maxRunning.CompareAndSwap(current, n) {
			break
		}
	}
	<-g.release
	g.running.Add(-1)
	return nil
}

func TestHealthCheck_ParallelismCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		n       uint
		running int32
	}{
		{name: "n_1", n: 1, running: 1},
		{name: "n_4", n: 4, running: 4},
		{name: "n_0_default", n: 0, running: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				i := NewWithOpts(&InjectorOpts{HealthCheckParallelism: tt.n})
				var running, maxRunning atomic.Int32
				release := make(chan struct{})
				for n := range 20 {
					ProvideNamedValue(i, fmt.Sprintf("svc-%d", n), &healthRunTestGate{running: &running, maxRunning: &maxRunning, release: release})
				}

				result := make(chan map[string]error, 1)
				go func() { result <- i.HealthCheck() }()
				synctest.Wait() // no further check starts

				is.Equal(tt.running, running.Load(), "checks running")
				close(release)

				results := <-result
				is.Len(results, 20)
				for name, err := range results {
					is.NoError(err, name)
				}
				is.Equal(tt.running, maxRunning.Load(), "checks running at once")
				i.Shutdown()
			})
		})
	}
}

func TestHealthCheck_SlotWaitHonorsContext(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	i := NewWithOpts(&InjectorOpts{HealthCheckParallelism: 1})
	hung := newHealthRunTestBlocking(nil)
	ProvideNamedValue(i, "hung", hung)
	ProvideNamedValue(i, "db", &healthRunTestCounter{})

	holder := make(chan map[string]error, 1)
	go func() { holder <- i.HealthCheck() }()
	for hung.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan map[string]error, 1)
	go func() { result <- i.HealthCheckWithContext(ctx) }()

	select {
	case results := <-result:
		is.ErrorIs(results["db"], ErrHealthCheckTimeout)
		is.ErrorIs(results["db"], context.DeadlineExceeded)
	case <-time.After(time.Second):
		is.Fail("the call did not return within 1s while waiting for a slot")
	}

	close(hung.release)
	<-holder
	i.Shutdown()
}

func TestHealthCheck_SlotWaitDoesNotBlockProvide(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 10*time.Second)
	is := assert.New(t)

	i := NewWithOpts(&InjectorOpts{HealthCheckParallelism: 1})
	hung := newHealthRunTestBlocking(nil)
	ProvideNamedValue(i, "hung", hung)

	var wg sync.WaitGroup
	wg.Go(func() { _ = i.HealthCheck() }) // runs "hung"
	for hung.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	for range 1001 {
		wg.Go(func() { _ = i.HealthCheck() })
	}
	time.Sleep(50 * time.Millisecond) // let them queue

	provided := make(chan struct{})
	go func() {
		defer close(provided)
		ProvideNamedValue(i, "late", 42)
	}()

	select {
	case <-provided:
	case <-time.After(time.Second):
		is.Fail("ProvideNamedValue did not return within 1s while health checks waited for a slot")
	}

	close(hung.release)
	wg.Wait()
	<-provided
	i.Shutdown()
}

func TestHealthCheck_NoQueueLimit(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 10*time.Second)
	is := assert.New(t)

	i := NewWithOpts(&InjectorOpts{HealthCheckParallelism: 1})
	for n := range 2000 {
		ProvideNamedValue(i, fmt.Sprintf("svc-%d", n), &healthRunTestCounter{})
	}

	results := i.HealthCheck()
	is.Len(results, 2000)
	for name, err := range results {
		is.NoError(err, name)
	}
	i.Shutdown()
}

// healthRunTestAggregate is a HealthcheckerWithContext whose check is the
// single-service check of "leaf".
type healthRunTestAggregate struct{ injector Injector }

func (a *healthRunTestAggregate) HealthCheck(ctx context.Context) error {
	return HealthCheckNamedWithContext(ctx, a.injector, "leaf")
}

func TestHealthCheck_SingleTakesOverQueued(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry func(i *RootScope) any
		want  any
	}{
		{name: "HealthCheck", entry: func(i *RootScope) any { return i.HealthCheck() }, want: map[string]error{"agg": nil, "leaf": nil}},
		{name: "HealthCheckNamed", entry: func(i *RootScope) any { return HealthCheckNamed(i, "agg") }, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			for range 50 { // the order the limiter runs the checks in varies
				i := NewWithOpts(&InjectorOpts{HealthCheckParallelism: 1})
				leaf := newHealthRunTestCtxAware()
				close(leaf.release)
				ProvideNamedValue(i, "leaf", leaf)
				ProvideNamedValue(i, "agg", &healthRunTestAggregate{injector: i})

				result := make(chan any, 1)
				go func() { result <- tt.entry(i) }()

				select {
				case got := <-result:
					if tt.want == nil {
						is.Nil(got)
					} else {
						is.Equal(tt.want, got)
					}
				case <-time.After(time.Second):
					is.Fail("the call did not return within 1s")
					return
				}
				is.LessOrEqual(leaf.maxRunning.Load(), int32(1), "leaf checks running at once")
				i.Shutdown()
			}
		})
	}
}

// Not parallel: goleak checks the goroutines a new container leaves.
func TestNew_NoHealthGoroutine(t *testing.T) { //nolint:paralleltest
	tests := []struct {
		name      string
		n         uint
		scheduler HealthScheduler
		// build builds the container with opts; nil means NewWithOpts
		build func(opts *InjectorOpts) *RootScope
	}{
		{name: "n_0", n: 0},
		{name: "n_4", n: 4},
		{name: "scheduler", scheduler: healthRunTestGoroutineScheduler{}},
		{name: "Clone", n: 4, scheduler: healthRunTestGoroutineScheduler{}, build: func(opts *InjectorOpts) *RootScope {
			return NewWithOpts(opts).Clone()
		}},
		{name: "CloneWithOpts", n: 4, scheduler: healthRunTestGoroutineScheduler{}, build: func(opts *InjectorOpts) *RootScope {
			return New().CloneWithOpts(opts)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ignore := goleak.IgnoreCurrent()

			build := tt.build
			if build == nil {
				build = func(opts *InjectorOpts) *RootScope { return NewWithOpts(opts) }
			}
			i := build(&InjectorOpts{HealthCheckParallelism: tt.n, HealthScheduler: tt.scheduler})
			ProvideNamedValue(i, "db", &healthRunTestCounter{})

			assert.NoError(t, goleak.Find(ignore))
			i.Shutdown()
		})
	}
}

// healthRunTestReportErr returns the Err of the entry for name in a root
// HealthReport, or an error when there is none.
func healthRunTestReportErr(i *RootScope, name string) error {
	return healthRunTestReportErrWithContext(context.Background(), i, name)
}

// healthRunTestReportErrWithContext is healthRunTestReportErr with ctx.
func healthRunTestReportErrWithContext(ctx context.Context, i *RootScope, name string) error {
	for _, entry := range i.HealthReportWithContext(ctx).Services {
		if entry.Service.Service == name {
			return entry.Err
		}
	}

	return fmt.Errorf("no entry for %q", name)
}

// healthRunTestGoroutineScheduler is a HealthScheduler that would start a
// goroutine when called.
type healthRunTestGoroutineScheduler struct{}

func (healthRunTestGoroutineScheduler) ScheduleHealthRounds(_ clockwork.Clock, _ func(context.Context)) (func(context.Context) error, error) {
	done := make(chan struct{})
	go func() { <-done }()
	return func(context.Context) error { close(done); return nil }, nil
}
