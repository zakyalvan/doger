package doger

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errHealthCheckDBDown = errors.New("db down")

// healthCheckTestResult is a Healthchecker returning a fixed result.
type healthCheckTestResult struct{ err error }

func (h *healthCheckTestResult) HealthCheck() error { return h.err }

// healthCheckTestCtxErr is a HealthcheckerWithContext returning ctx.Err().
type healthCheckTestCtxErr struct{}

func (h *healthCheckTestCtxErr) HealthCheck(ctx context.Context) error { return ctx.Err() }

// healthCheckTestUntilDone is a HealthcheckerWithContext that waits for its
// context to be done, then returns a fixed result.
type healthCheckTestUntilDone struct{ err error }

func (h *healthCheckTestUntilDone) HealthCheck(ctx context.Context) error {
	<-ctx.Done()
	return h.err
}

// healthCheckTestOnClock is a Healthchecker that returns a fixed result after
// a duration on its clock, ignoring its context.
type healthCheckTestOnClock struct {
	clock    clockwork.Clock
	duration time.Duration
	err      error
}

func (h *healthCheckTestOnClock) HealthCheck() error {
	h.clock.Sleep(h.duration)
	return h.err
}

// healthCheckTestBlocking is a Healthchecker that blocks until released,
// ignoring its context.
type healthCheckTestBlocking struct{ release chan struct{} }

func newHealthCheckTestBlocking() *healthCheckTestBlocking {
	return &healthCheckTestBlocking{release: make(chan struct{})}
}

func (h *healthCheckTestBlocking) HealthCheck() error {
	<-h.release
	return nil
}

// healthCheckTestAsync starts a scope-wide health check in its own goroutine.
func healthCheckTestAsync(ctx context.Context, i Injector) <-chan map[string]error {
	result := make(chan map[string]error, 1)
	go func() { result <- i.HealthCheckWithContext(ctx) }()
	return result
}

// healthCheckTestReturned reports whether a health check started with
// healthCheckTestAsync has returned.
func healthCheckTestReturned(result <-chan map[string]error) bool {
	return len(result) > 0
}

const healthCheckTestDeadlineMessage = "DI: health check timeout: context deadline exceeded"

// healthCheckTestEntry is the public API a test row calls.
type healthCheckTestEntry string

const (
	healthCheckTestEntryNamed    healthCheckTestEntry = "HealthCheckNamedWithContext"
	healthCheckTestEntryInjector healthCheckTestEntry = "HealthCheckWithContext (injector)"
	healthCheckTestEntryChild    healthCheckTestEntry = "HealthCheckWithContext (child scope)"
)

// call runs the entry for the named service and returns its result.
func (e healthCheckTestEntry) call(ctx context.Context, i *RootScope, name string) error {
	switch e {
	case healthCheckTestEntryNamed:
		return HealthCheckNamedWithContext(ctx, i, name)
	case healthCheckTestEntryInjector:
		return i.HealthCheckWithContext(ctx)[name]
	case healthCheckTestEntryChild:
		child, ok := i.ChildByName("c")
		if !ok {
			panic("child scope \"c\" is not declared")
		}
		return child.HealthCheckWithContext(ctx)[name]
	}

	panic(fmt.Sprintf("unknown entry %q", e))
}

// healthCheckTestContext returns a context in the given state.
func healthCheckTestContext(state string) (context.Context, context.CancelFunc) {
	switch state {
	case "cancelled":
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	case "past deadline":
		return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	case "live":
		return context.WithCancel(context.Background())
	}

	panic(fmt.Sprintf("unknown context state %q", state))
}

func TestHealthCheck_EndedContextIsTimeout(t *testing.T) {
	t.Parallel()

	const runs = 1000

	tests := []struct {
		name       string
		entry      healthCheckTestEntry
		ctxState   string // "cancelled", "past deadline", or "cancelled during" the check
		check      Healthchecker
		checkCtx   HealthcheckerWithContext
		inRoot     bool // registered in the root scope, checked from child scope "c"
		ctxMessage string
		ctxErr     error
	}{
		{name: "named_cancelled_before_nil", entry: healthCheckTestEntryNamed, ctxState: "cancelled", check: &healthCheckTestResult{}, ctxMessage: "context canceled", ctxErr: context.Canceled},
		{name: "named_past_deadline_nil", entry: healthCheckTestEntryNamed, ctxState: "past deadline", check: &healthCheckTestResult{}, ctxMessage: "context deadline exceeded", ctxErr: context.DeadlineExceeded},
		{name: "named_cancelled_before_ctx_err", entry: healthCheckTestEntryNamed, ctxState: "cancelled", checkCtx: &healthCheckTestCtxErr{}, ctxMessage: "context canceled", ctxErr: context.Canceled},
		{name: "named_cancelled_during_nil", entry: healthCheckTestEntryNamed, ctxState: "cancelled during", checkCtx: &healthCheckTestUntilDone{}, ctxMessage: "context canceled", ctxErr: context.Canceled},
		{name: "named_cancelled_during_db_down", entry: healthCheckTestEntryNamed, ctxState: "cancelled during", checkCtx: &healthCheckTestUntilDone{err: errHealthCheckDBDown}, ctxMessage: "context canceled", ctxErr: context.Canceled},
		{name: "injector_cancelled_before_nil", entry: healthCheckTestEntryInjector, ctxState: "cancelled", check: &healthCheckTestResult{}, ctxMessage: "context canceled", ctxErr: context.Canceled},
		{name: "injector_past_deadline_ctx_err", entry: healthCheckTestEntryInjector, ctxState: "past deadline", checkCtx: &healthCheckTestCtxErr{}, ctxMessage: "context deadline exceeded", ctxErr: context.DeadlineExceeded},
		{name: "child_scope_cancelled_before_nil_inherited", entry: healthCheckTestEntryChild, ctxState: "cancelled", check: &healthCheckTestResult{}, inRoot: true, ctxMessage: "context canceled", ctxErr: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wantMessage := "DI: health check timeout: " + tt.ctxMessage

			var wrongMessage, notSentinel, notCtxErr int
			var firstErr error
			record := func(err error) {
				bad := false
				if err == nil || err.Error() != wantMessage {
					wrongMessage++
					bad = true
				}
				if !errors.Is(err, ErrHealthCheckTimeout) {
					notSentinel++
					bad = true
				}
				if !errors.Is(err, tt.ctxErr) {
					notCtxErr++
					bad = true
				}
				if bad && firstErr == nil {
					firstErr = fmt.Errorf("got %v", err) //nolint:errorlint
				}
			}

			newInjector := func() *RootScope {
				i := New()
				var svc any = tt.check
				if tt.checkCtx != nil {
					svc = tt.checkCtx
				}
				ProvideNamedValue(i, "svc", svc)
				if tt.inRoot {
					i.Scope("c")
				}
				return i
			}

			if tt.ctxState == "cancelled during" {
				synctest.Test(t, func(t *testing.T) {
					for range runs {
						i := newInjector()
						ctx, cancel := context.WithCancel(context.Background())
						result := make(chan error, 1)
						go func() { result <- tt.entry.call(ctx, i, "svc") }()
						synctest.Wait() // the check is blocked on ctx.Done()
						cancel()
						record(<-result)
						synctest.Wait()
					}
				})
			} else {
				for range runs {
					i := newInjector()
					ctx, cancel := healthCheckTestContext(tt.ctxState)
					record(tt.entry.call(ctx, i, "svc"))
					cancel()
				}
			}

			is := assert.New(t)
			is.Zero(wrongMessage, "calls without message %q (first: %v)", wantMessage, firstErr)
			is.Zero(notSentinel, "calls not matching ErrHealthCheckTimeout (first: %v)", firstErr)
			is.Zero(notCtxErr, "calls not matching %v (first: %v)", tt.ctxErr, firstErr)
		})
	}
}

func TestHealthCheck_FinishedInTimeKeepsResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		call   func(ctx context.Context, i *RootScope) error
		result error
	}{
		{name: "named_nil", call: func(ctx context.Context, i *RootScope) error { return HealthCheckNamedWithContext(ctx, i, "svc") }, result: nil},
		{name: "named_db_down", call: func(ctx context.Context, i *RootScope) error { return HealthCheckNamedWithContext(ctx, i, "svc") }, result: errHealthCheckDBDown},
		{name: "injector_nil", call: func(ctx context.Context, i *RootScope) error { return i.HealthCheckWithContext(ctx)["svc"] }, result: nil},
		{name: "injector_db_down", call: func(ctx context.Context, i *RootScope) error { return i.HealthCheckWithContext(ctx)["svc"] }, result: errHealthCheckDBDown},
		{name: "named_no_context_nil", call: func(_ context.Context, i *RootScope) error { return HealthCheckNamed(i, "svc") }, result: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			i := New()
			ProvideNamedValue(i, "svc", &healthCheckTestResult{err: tt.result})

			ctx, cancel := healthCheckTestContext("live")
			err := tt.call(ctx, i)
			cancel() // only after the call returned

			assert.Equal(t, tt.result, err)
		})
	}
}

func TestHealthCheck_ServiceWithoutCheckNeverTimesOut(t *testing.T) {
	t.Parallel()

	const runs = 1000

	tests := []struct {
		name     string
		entry    healthCheckTestEntry
		ctxState string
		register func(i *RootScope) string // registers the service and returns the name to check
		wantErr  error                     // nil, or a sentinel the result must match
	}{
		{
			name: "named_plain_value_cancelled", entry: healthCheckTestEntryNamed, ctxState: "cancelled",
			register: func(i *RootScope) string { ProvideNamedValue(i, "svc", 42); return "svc" },
		},
		{
			name: "injector_plain_value_cancelled", entry: healthCheckTestEntryInjector, ctxState: "cancelled",
			register: func(i *RootScope) string { ProvideNamedValue(i, "svc", 42); return "svc" },
		},
		{
			name: "injector_plain_value_past_deadline", entry: healthCheckTestEntryInjector, ctxState: "past deadline",
			register: func(i *RootScope) string { ProvideNamedValue(i, "svc", 42); return "svc" },
		},
		{
			name: "injector_lazy_never_invoked_cancelled", entry: healthCheckTestEntryInjector, ctxState: "cancelled",
			register: func(i *RootScope) string {
				ProvideNamed(i, "svc", func(Injector) (*healthCheckTestResult, error) { return &healthCheckTestResult{}, nil })
				return "svc"
			},
		},
		{
			name: "injector_transient_cancelled", entry: healthCheckTestEntryInjector, ctxState: "cancelled",
			register: func(i *RootScope) string {
				ProvideNamedTransient(i, "svc", func(Injector) (*healthCheckTestResult, error) { return &healthCheckTestResult{}, nil })
				return "svc"
			},
		},
		{
			name: "injector_alias_to_lazy_never_invoked_cancelled", entry: healthCheckTestEntryInjector, ctxState: "cancelled",
			register: func(i *RootScope) string {
				Provide(i, func(Injector) (*healthCheckTestResult, error) { return &healthCheckTestResult{}, nil })
				must0(As[*healthCheckTestResult, Healthchecker](i))
				return NameOf[Healthchecker]()
			},
		},
		{
			name: "child_scope_plain_value_inherited_cancelled", entry: healthCheckTestEntryChild, ctxState: "cancelled",
			register: func(i *RootScope) string { ProvideNamedValue(i, "svc", 42); i.Scope("c"); return "svc" },
		},
		{
			name: "named_missing_cancelled", entry: healthCheckTestEntryNamed, ctxState: "cancelled",
			register: func(*RootScope) string { return "missing" },
			wantErr:  ErrServiceNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var unexpected int
			var firstErr error
			for range runs {
				i := New()
				name := tt.register(i)
				ctx, cancel := healthCheckTestContext(tt.ctxState)
				err := tt.entry.call(ctx, i, name)
				cancel()

				ok := err == nil
				if tt.wantErr != nil {
					ok = errors.Is(err, tt.wantErr)
				}
				if !ok {
					unexpected++
					if firstErr == nil {
						firstErr = fmt.Errorf("got %v", err) //nolint:errorlint
					}
				}
			}

			require.Zero(t, unexpected, "calls with an unexpected result (want %v; first: %v)", tt.wantErr, firstErr)
		})
	}
}

func TestHealthCheck_TimeoutOptionsOnClock(t *testing.T) {
	t.Parallel()

	const runs = 1000

	tests := []struct {
		name     string
		global   time.Duration
		perCheck time.Duration
		result   error
		duration time.Duration
		advance  time.Duration
		want     string // "" for the check's own result
	}{
		{name: "per_check_10ms_check_20ms", perCheck: 10 * time.Millisecond, duration: 20 * time.Millisecond, advance: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "per_check_10ms_check_9ms", perCheck: 10 * time.Millisecond, duration: 9 * time.Millisecond, advance: 9 * time.Millisecond},
		{name: "per_check_10ms_check_10ms_exact_limit", perCheck: 10 * time.Millisecond, duration: 10 * time.Millisecond, advance: 10 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "per_check_10ms_db_down_5ms", perCheck: 10 * time.Millisecond, result: errHealthCheckDBDown, duration: 5 * time.Millisecond, advance: 5 * time.Millisecond},
		{name: "global_10ms_check_20ms", global: 10 * time.Millisecond, duration: 20 * time.Millisecond, advance: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "global_10ms_check_9ms", global: 10 * time.Millisecond, duration: 9 * time.Millisecond, advance: 9 * time.Millisecond},
		{name: "per_check_50ms_global_10ms_check_20ms", global: 10 * time.Millisecond, perCheck: 50 * time.Millisecond, duration: 20 * time.Millisecond, advance: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "per_check_10ms_global_50ms_check_20ms", global: 50 * time.Millisecond, perCheck: 10 * time.Millisecond, duration: 20 * time.Millisecond, advance: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "no_timeout_check_1h", duration: time.Hour, advance: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var unexpected int
			var firstErr error
			synctest.Test(t, func(t *testing.T) {
				for range runs {
					fc := clockwork.NewFakeClock()
					i := NewWithOpts(&InjectorOpts{
						Clock:                    fc,
						HealthCheckTimeout:       tt.perCheck,
						HealthCheckGlobalTimeout: tt.global,
					})
					ProvideNamedValue(i, "svc", &healthCheckTestOnClock{clock: fc, duration: tt.duration, err: tt.result})

					result := healthCheckTestAsync(context.Background(), i)
					synctest.Wait()
					fc.Advance(tt.advance)
					err := (<-result)["svc"]

					ok := err == tt.result //nolint:errorlint // the check's own error value
					if tt.want != "" {
						ok = err != nil && err.Error() == tt.want
					}
					if !ok {
						unexpected++
						if firstErr == nil {
							firstErr = fmt.Errorf("got %v", err) //nolint:errorlint
						}
					}

					fc.Advance(tt.duration) // let an abandoned check return
					synctest.Wait()
				}
			})

			want := tt.want
			if want == "" {
				want = fmt.Sprint(tt.result)
			}
			assert.Zero(t, unexpected, "runs not returning %q (first: %v)", want, firstErr)
		})
	}
}

func TestHealthCheck_SequentialChecksShareGlobalTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		fc := clockwork.NewFakeClock()
		i := NewWithOpts(&InjectorOpts{
			Clock:                    fc,
			HealthCheckParallelism:   1,
			HealthCheckGlobalTimeout: 50 * time.Millisecond,
		})
		for _, name := range []string{"a", "b", "c", "d"} {
			ProvideNamedValue(i, name, &healthCheckTestOnClock{clock: fc, duration: 20 * time.Millisecond})
		}

		result := healthCheckTestAsync(context.Background(), i)
		for {
			synctest.Wait()
			if healthCheckTestReturned(result) {
				break
			}
			fc.Advance(10 * time.Millisecond)
		}
		results := <-result

		var healthy, timedOut int
		for _, err := range results {
			switch {
			case err == nil:
				healthy++
			case err.Error() == healthCheckTestDeadlineMessage:
				timedOut++
			}
		}
		is.Len(results, 4)
		is.Equal(2, healthy, "results: %v", results)
		is.Equal(2, timedOut, "results: %v", results)

		fc.Advance(time.Hour) // let abandoned checks return
		synctest.Wait()
		i.Shutdown()
	})
}

func TestHealthCheck_NotBeforeClockReachesTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		fc := clockwork.NewFakeClock()
		i := NewWithOpts(&InjectorOpts{Clock: fc, HealthCheckTimeout: 10 * time.Millisecond})
		check := newHealthCheckTestBlocking()
		ProvideNamedValue(i, "svc", check)

		result := healthCheckTestAsync(context.Background(), i)
		synctest.Wait()
		fc.Advance(9 * time.Millisecond)
		synctest.Wait()
		is.False(healthCheckTestReturned(result), "returned before the clock reached the timeout")

		fc.Advance(time.Millisecond)
		err := (<-result)["svc"]
		is.EqualError(err, healthCheckTestDeadlineMessage)

		close(check.release)
		synctest.Wait()
	})
}

func TestHealthCheck_ConsumerDeadlineOnContextOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		perCheck time.Duration
		waiters  int // fake-clock waiters once the check runs
	}{
		{name: "no_timeout_options", waiters: 1},
		{name: "per_check_3h", perCheck: 3 * time.Hour, waiters: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Not in a bubble: bubble time would reach the consumer's 1h deadline.
			testWithTimeout(t, 5*time.Second)
			must := require.New(t)

			fc := clockwork.NewFakeClock()
			i := NewWithOpts(&InjectorOpts{Clock: fc, HealthCheckTimeout: tt.perCheck})
			ProvideNamedValue(i, "svc", &healthCheckTestOnClock{clock: fc, duration: 2 * time.Hour})

			ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
			defer cancel()

			result := healthCheckTestAsync(ctx, i)
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer waitCancel()
			must.NoError(fc.BlockUntilContext(waitCtx, tt.waiters))
			fc.Advance(2 * time.Hour)

			must.NoError((<-result)["svc"])
		})
	}
}

func TestHealthCheck_PerServiceTimeout(t *testing.T) {
	t.Parallel()

	const runs = 100

	type svc = healthCheckTestOnClock
	named := func(_ context.Context, i Injector) error { return HealthCheckNamed(i, "svc") }
	namedCtx := func(ctx context.Context, i Injector) error { return HealthCheckNamedWithContext(ctx, i, "svc") }
	typed := func(_ context.Context, i Injector) error { return HealthCheck[*svc](i) }
	typedCtx := func(ctx context.Context, i Injector) error { return HealthCheckWithContext[*svc](ctx, i) }

	tests := []struct {
		name     string
		call     func(context.Context, Injector) error
		byType   bool // registered under its type name, for the typed functions
		timeout  time.Duration
		global   time.Duration
		duration time.Duration
		want     string // "" for nil
	}{
		{name: "HealthCheckNamed_10ms_check_20ms", call: named, timeout: 10 * time.Millisecond, duration: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "HealthCheckNamedWithContext_10ms_check_20ms", call: namedCtx, timeout: 10 * time.Millisecond, duration: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "HealthCheck_10ms_check_9ms", call: typed, byType: true, timeout: 10 * time.Millisecond, duration: 9 * time.Millisecond},
		{name: "HealthCheckWithContext_10ms_check_10ms", call: typedCtx, byType: true, timeout: 10 * time.Millisecond, duration: 10 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "HealthCheckNamed_global_only", call: named, global: 10 * time.Millisecond, duration: 20 * time.Millisecond},
		{name: "HealthCheckNamed_no_timeout_check_1h", call: named, duration: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var unexpected int
			var firstErr error
			synctest.Test(t, func(t *testing.T) {
				for range runs {
					fc := clockwork.NewFakeClock()
					i := NewWithOpts(&InjectorOpts{
						Clock:                    fc,
						HealthCheckTimeout:       tt.timeout,
						HealthCheckGlobalTimeout: tt.global,
					})
					check := &svc{clock: fc, duration: tt.duration}
					if tt.byType {
						ProvideValue(i, check)
					} else {
						ProvideNamedValue(i, "svc", check)
					}

					result := make(chan error, 1)
					go func() { result <- tt.call(context.Background(), i) }()
					synctest.Wait()
					fc.Advance(tt.duration)
					err := <-result

					ok := err == nil
					if tt.want != "" {
						ok = err != nil && err.Error() == tt.want
					}
					if !ok {
						unexpected++
						if firstErr == nil {
							firstErr = fmt.Errorf("got %v", err) //nolint:errorlint
						}
					}

					fc.Advance(tt.duration) // let an abandoned check return
					synctest.Wait()
				}
			})

			assert.Zero(t, unexpected, "runs not returning %q (first: %v)", tt.want, firstErr)
		})
	}
}

func TestHealthCheck_PerServiceErrorInTime(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	errDBDown := errors.New("db down")
	i := NewWithOpts(&InjectorOpts{HealthCheckTimeout: 10 * time.Millisecond})
	ProvideNamedValue(i, "svc", &healthCheckTestResult{err: errDBDown})

	err := HealthCheckNamed(i, "svc")

	assert.Same(t, errDBDown, err)
	assert.NotErrorIs(t, err, ErrHealthCheckTimeout)
}

func TestHealthCheckSingleTimeout(t *testing.T) {
	t.Parallel()

	const runs = 20

	type svc = healthCheckTestOnClock
	named := func(_ context.Context, i Injector) error { return HealthCheckNamed(i, "svc") }
	namedCtx := func(ctx context.Context, i Injector) error { return HealthCheckNamedWithContext(ctx, i, "svc") }
	typed := func(_ context.Context, i Injector) error { return HealthCheck[*svc](i) }
	typedCtx := func(ctx context.Context, i Injector) error { return HealthCheckWithContext[*svc](ctx, i) }
	all := func(_ context.Context, i Injector) error { return i.HealthCheck()["svc"] }

	tests := []struct {
		name     string
		call     func(context.Context, Injector) error
		byType   bool // registered under its type name, for the typed functions
		timeout  time.Duration
		single   time.Duration
		duration time.Duration
		want     string // "" for nil
	}{
		{name: "HealthCheckNamed_inherits", call: named, timeout: 10 * time.Millisecond, duration: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "HealthCheckNamed_own_larger_bound_in_time", call: named, timeout: 10 * time.Millisecond, single: 30 * time.Millisecond, duration: 20 * time.Millisecond},
		{name: "HealthCheckNamed_own_larger_bound_past", call: named, timeout: 10 * time.Millisecond, single: 30 * time.Millisecond, duration: 40 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "HealthCheckNamedWithContext_own_bound_only", call: namedCtx, single: 10 * time.Millisecond, duration: 10 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "HealthCheck_negative_unbounded", call: typed, byType: true, timeout: 10 * time.Millisecond, single: -1, duration: time.Hour},
		{name: "HealthCheckWithContext_negative_both_unset", call: typedCtx, byType: true, single: -1, duration: time.Hour},
		{name: "injector_HealthCheck_keeps_HealthCheckTimeout", call: all, timeout: 10 * time.Millisecond, single: -1, duration: 20 * time.Millisecond, want: healthCheckTestDeadlineMessage},
		{name: "injector_HealthCheck_ignores_single", call: all, single: 10 * time.Millisecond, duration: 20 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var unexpected int
			var firstErr error
			synctest.Test(t, func(t *testing.T) {
				for range runs {
					fc := clockwork.NewFakeClock()
					i := NewWithOpts(&InjectorOpts{
						Clock:                    fc,
						HealthCheckTimeout:       tt.timeout,
						HealthCheckSingleTimeout: tt.single,
					})
					check := &svc{clock: fc, duration: tt.duration}
					if tt.byType {
						ProvideValue(i, check)
					} else {
						ProvideNamedValue(i, "svc", check)
					}

					result := make(chan error, 1)
					go func() { result <- tt.call(context.Background(), i) }()
					synctest.Wait()
					fc.Advance(tt.duration)
					err := <-result

					ok := err == nil
					if tt.want != "" {
						ok = err != nil && err.Error() == tt.want
					}
					if !ok {
						unexpected++
						if firstErr == nil {
							firstErr = fmt.Errorf("got %v", err) //nolint:errorlint
						}
					}

					fc.Advance(tt.duration) // let an abandoned check return
					synctest.Wait()
					i.Shutdown()
				}
			})

			assert.Zero(t, unexpected, "first unexpected: %v", firstErr)
		})
	}
}
