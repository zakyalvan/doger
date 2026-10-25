package doger

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

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
