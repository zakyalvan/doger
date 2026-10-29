package doger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	is.NotNil(i)

	is.NotNil(i.opts.Logf)
	is.Empty(i.opts.HookBeforeRegistration)
	is.Empty(i.opts.HookAfterRegistration)
	is.Empty(i.opts.HookBeforeInvocation)
	is.Empty(i.opts.HookAfterInvocation)
	is.Empty(i.opts.HookBeforeShutdown)
	is.Empty(i.opts.HookAfterShutdown)
	is.Empty(i.opts.HealthCheckParallelism)
	is.Empty(i.opts.HealthCheckGlobalTimeout)
	is.Empty(i.opts.HealthCheckTimeout)

	is.Nil(i.healthSlots)
	is.NotNil(i.self)
	is.Equal("[root]", i.self.name)
	is.Equal(i.self.rootScope, i)
	is.Nil(i.self.parentScope)
}

func TestNewWithOpts(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := NewWithOpts(&InjectorOpts{
		HookBeforeRegistration: []func(*Scope, string){},
		HookAfterRegistration:  []func(*Scope, string){},
		HookBeforeInvocation:   []func(*Scope, string){},
		HookAfterInvocation:    []func(*Scope, string, error){},
		HookBeforeShutdown:     []func(*Scope, string){},
		HookAfterShutdown:      []func(*Scope, string, error){},

		Logf: func(format string, args ...any) {},

		HealthCheckParallelism:   42,
		HealthCheckGlobalTimeout: 42 * time.Second,
		HealthCheckTimeout:       42 * time.Second,
	})
	defer i.Shutdown() //nolint: errcheck

	is.NotNil(i)

	is.NotNil(i.opts.Logf)
	is.Empty(i.opts.HookBeforeRegistration)
	is.Empty(i.opts.HookAfterRegistration)
	is.Empty(i.opts.HookBeforeInvocation)
	is.Empty(i.opts.HookAfterInvocation)
	is.Empty(i.opts.HookBeforeShutdown)
	is.Empty(i.opts.HookAfterShutdown)
	is.EqualValues(42, i.opts.HealthCheckParallelism)
	is.Equal(42*time.Second, i.opts.HealthCheckGlobalTimeout)
	is.Equal(42*time.Second, i.opts.HealthCheckTimeout)

	is.NotNil(i.healthSlots)
	is.NotNil(i.self)
	is.Equal("[root]", i.self.name)
	is.Equal(i.self.rootScope, i)
	is.Nil(i.self.parentScope)
}

func TestRootScope_RootScope(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	is.Equal(i, i.RootScope())
}

func TestRootScope_Ancestors(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	is.Empty(i.Ancestors())
}

func TestRootScope_HealthCheckTimeouts(t *testing.T) {
	// Bubble time: the 20ms checks and 10ms/50ms timeouts no longer depend on scheduling.
	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		// no timeout
		i := New()
		ProvideValue(i, &lazyTestHeathcheckerOKTimeout{foobar: "foobar"})
		ProvideValue(i, &lazyTestHeathcheckerOK{})

		errAll := i.HealthCheckWithContext(context.Background())
		is.NoError(errAll[NameOf[*lazyTestHeathcheckerOKTimeout]()])
		is.NoError(errAll[NameOf[*lazyTestHeathcheckerOK]()])

		// with 10ms individual timeout
		i = NewWithOpts(&InjectorOpts{
			HealthCheckTimeout: 10 * time.Millisecond,
		})
		ProvideValue(i, &lazyTestHeathcheckerOKTimeout{})
		ProvideValue(i, &lazyTestHeathcheckerOK{})

		err1 := HealthCheckNamedWithContext(context.Background(), i, NameOf[*lazyTestHeathcheckerOKTimeout]())
		err2 := HealthCheckNamedWithContext(context.Background(), i, NameOf[*lazyTestHeathcheckerOK]())
		is.EqualError(err1, "DI: health check timeout: context deadline exceeded")
		is.NoError(err2)

		// with 10ms global timeout
		i = NewWithOpts(&InjectorOpts{
			HealthCheckGlobalTimeout: 10 * time.Millisecond,
		})
		ProvideValue(i, &lazyTestHeathcheckerOKTimeout{})
		ProvideValue(i, &lazyTestHeathcheckerOK{})

		errAll = i.HealthCheckWithContext(context.Background())
		is.Len(errAll, 2)
		is.EqualError(errAll[NameOf[*lazyTestHeathcheckerOKTimeout]()], "DI: health check timeout: context deadline exceeded")
		is.NoError(errAll[NameOf[*lazyTestHeathcheckerOK]()])

		// with 10ms global timeout with sequential healthchecks
		i = NewWithOpts(&InjectorOpts{
			HealthCheckParallelism:   1,
			HealthCheckGlobalTimeout: 50 * time.Millisecond,
		})
		defer i.Shutdown() //nolint: errcheck

		ProvideNamedValue(i, "a", &lazyTestHeathcheckerOKTimeout{})
		ProvideNamedValue(i, "b", &lazyTestHeathcheckerOKTimeout{})
		ProvideNamedValue(i, "c", &lazyTestHeathcheckerOKTimeout{})
		ProvideNamedValue(i, "d", &lazyTestHeathcheckerOKTimeout{})
		ProvideValue(i, &lazyTestHeathcheckerOK{})

		errAll = i.HealthCheckWithContext(context.Background())
		errors := []error{}
		for _, err := range errAll {
			if err != nil {
				errors = append(errors, err)
			}
		}

		is.Len(errAll, 5)
		// i do not check the exact number of errors due to sleep randomness
		is.NotEmpty(errors)
		is.Less(len(errors), 5)
		if len(errors) == 3 {
			is.EqualError(errors[0], "DI: health check timeout: context deadline exceeded")
		}
		// because executed last
		// is.EqualError(errAll[NameOf[*lazyTestHeathcheckerOK]()], "DI: health check timeout: context deadline exceeded")

		time.Sleep(50 * time.Millisecond) // let abandoned checks return
	})
}

func TestRootScope_Clone(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	opts := &InjectorOpts{
		HookBeforeRegistration: []func(*Scope, string){},
		HookAfterRegistration:  []func(*Scope, string){},
		HookBeforeInvocation:   []func(*Scope, string){},
		HookAfterInvocation:    []func(*Scope, string, error){},
		HookBeforeShutdown:     []func(*Scope, string){},
		HookAfterShutdown:      []func(*Scope, string, error){},

		Logf: func(format string, args ...any) {},

		HealthCheckParallelism:   42,
		HealthCheckGlobalTimeout: 42 * time.Second,
		HealthCheckTimeout:       42 * time.Second,
	}

	i := NewWithOpts(opts)
	clone := i.Clone()

	defer i.Shutdown()     //nolint: errcheck
	defer clone.Shutdown() //nolint: errcheck

	is.NotEqual(i.opts, clone.opts) // copy

	tests := []struct {
		name string
		opts *InjectorOpts
	}{
		{name: "original", opts: i.opts},
		{name: "clone", opts: clone.opts},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.Empty(tt.opts.HookBeforeRegistration)
			is.Empty(tt.opts.HookAfterRegistration)
			is.Empty(tt.opts.HookBeforeInvocation)
			is.Empty(tt.opts.HookAfterInvocation)
			is.Empty(tt.opts.HookBeforeShutdown)
			is.Empty(tt.opts.HookAfterShutdown)
			is.NotNil(tt.opts.Logf)
			is.NotNil(tt.opts.HealthCheckParallelism)
			is.NotNil(tt.opts.HealthCheckGlobalTimeout)
			is.NotNil(tt.opts.HealthCheckTimeout)
		})
	}

	is.EqualValues(42, clone.opts.HealthCheckParallelism)
	is.Equal(42*time.Second, clone.opts.HealthCheckGlobalTimeout)
	is.Equal(42*time.Second, clone.opts.HealthCheckTimeout)
	is.Equal(i.opts.HealthCheckParallelism, clone.opts.HealthCheckParallelism)
	is.Equal(i.opts.HealthCheckGlobalTimeout, clone.opts.HealthCheckGlobalTimeout)
	is.Equal(i.opts.HealthCheckTimeout, clone.opts.HealthCheckTimeout)
}

func TestRootScope_CloneWithOpts(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	clone := i.CloneWithOpts(&InjectorOpts{
		HookBeforeRegistration: []func(*Scope, string){},
		HookAfterRegistration:  []func(*Scope, string){},
		HookBeforeInvocation:   []func(*Scope, string){},
		HookAfterInvocation:    []func(*Scope, string, error){},
		HookBeforeShutdown:     []func(*Scope, string){},
		HookAfterShutdown:      []func(*Scope, string, error){},

		Logf: func(format string, args ...any) {},

		HealthCheckParallelism:   42,
		HealthCheckGlobalTimeout: 42 * time.Second,
		HealthCheckTimeout:       42 * time.Second,
	})

	defer i.Shutdown()     //nolint: errcheck
	defer clone.Shutdown() //nolint: errcheck

	is.Empty(i.opts.HookBeforeRegistration)
	is.Empty(i.opts.HookAfterRegistration)
	is.Empty(i.opts.HookBeforeInvocation)
	is.Empty(i.opts.HookAfterInvocation)
	is.Empty(i.opts.HookBeforeShutdown)
	is.Empty(i.opts.HookAfterShutdown)
	is.NotNil(i.opts.Logf)
	is.Empty(i.opts.HealthCheckParallelism)
	is.Empty(i.opts.HealthCheckGlobalTimeout)
	is.Empty(i.opts.HealthCheckTimeout)

	is.Empty(clone.opts.HookBeforeRegistration)
	is.Empty(clone.opts.HookAfterRegistration)
	is.Empty(clone.opts.HookBeforeInvocation)
	is.Empty(clone.opts.HookAfterInvocation)
	is.Empty(clone.opts.HookBeforeShutdown)
	is.Empty(clone.opts.HookAfterShutdown)
	is.NotNil(clone.opts.Logf)
	is.Equal(uint(42), clone.opts.HealthCheckParallelism)
	is.Equal(42*time.Second, clone.opts.HealthCheckGlobalTimeout)
	is.Equal(42*time.Second, clone.opts.HealthCheckTimeout)

	// scope must be added only to initial scope
	i.Scope("foobar")
	is.Len(i.Children(), 1)
	is.Empty(clone.Children())

	is.Nil(i.healthSlots)
	is.NotNil(clone.healthSlots)
}

func TestRootScope_ShutdownOnSignals(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	// Note: This test cannot be fully automated because it waits for OS signals (SIGINT, SIGTERM)
	// which require user input or external process termination. In a real scenario, this method
	// would block until the application receives a termination signal from the OS.
}

func TestRootScope_ShutdownOnSignalsWithContext(t *testing.T) {
	t.Parallel()

	// The signal branch needs an OS signal, which a test cannot deliver
	// safely; these rows end the context instead.
	tests := []struct {
		name string
		ctx  func(t *testing.T, parent context.Context) context.Context
	}{
		{
			name: "cancelled_before",
			ctx: func(_ *testing.T, parent context.Context) context.Context {
				ctx, cancel := context.WithCancel(parent)
				cancel()
				return ctx
			},
		},
		{
			name: "timeout_while_waiting",
			ctx: func(t *testing.T, parent context.Context) context.Context {
				ctx, cancel := context.WithTimeout(parent, 10*time.Millisecond)
				t.Cleanup(cancel)
				return ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			probe := &ctxShutdownProbe{}
			ProvideNamed(root, "s", func(Injector) (*ctxShutdownProbe, error) { return probe, nil })
			MustInvokeNamed[*ctxShutdownProbe](root, "s")
			hookCtxs := map[string][]context.Context{}
			root.AddContainerClosingHook(func(ctx context.Context, _ *RootScope) {
				hookCtxs["closing"] = append(hookCtxs["closing"], ctx)
			})
			root.AddContainerClosedHook(func(ctx context.Context, _ *RootScope, _ *ShutdownReport) {
				hookCtxs["closed"] = append(hookCtxs["closed"], ctx)
			})
			ctx := tt.ctx(t, context.WithValue(context.Background(), ctxTestKey, "r-1"))

			sig, report := root.ShutdownOnSignalsWithContext(ctx)

			is.Nil(sig)
			is.True(report.Succeed, report.Error())
			probe.mu.Lock()
			defer probe.mu.Unlock()
			if is.Len(probe.ctxs, 1, "Shutdown calls") {
				is.NoError(probe.ctxs[0].Err())
				is.Equal("r-1", probe.ctxs[0].Value(ctxTestKey))
			}
			for _, hook := range []string{"closing", "closed"} {
				if is.Len(hookCtxs[hook], 1, hook) {
					is.NoError(hookCtxs[hook][0].Err(), hook)
					is.Equal("r-1", hookCtxs[hook][0].Value(ctxTestKey), hook)
				}
			}
		})
	}
}

// rootShutdownProbe counts its Shutdown() error calls and returns err.
type rootShutdownProbe struct {
	calls atomic.Int32
	err   error
}

func (p *rootShutdownProbe) Shutdown() error {
	p.calls.Add(1)
	return p.err
}

// provideBuiltProbe registers a lazy probe under name and builds it.
func provideBuiltProbe(t *testing.T, root *RootScope, name string, err error) *rootShutdownProbe {
	t.Helper()

	probe := &rootShutdownProbe{err: err}
	ProvideNamed(root, name, func(Injector) (*rootShutdownProbe, error) { return probe, nil })
	_, invokeErr := InvokeNamed[*rootShutdownProbe](root, name)
	require.NoError(t, invokeErr)

	return probe
}

func rootServiceDescription(root *RootScope) ServiceDescription {
	return ServiceDescription{ScopeID: root.ID(), ScopeName: root.Name(), Service: ""}
}

func TestRootScope_ConcurrentShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts *InjectorOpts
	}{
		{name: "default_opts", opts: &InjectorOpts{}},
		{name: "health_check_parallelism_2", opts: &InjectorOpts{HealthCheckParallelism: 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			root := NewWithOpts(tt.opts)
			probes := []*rootShutdownProbe{
				provideBuiltProbe(t, root, "a", nil),
				provideBuiltProbe(t, root, "b", nil),
				provideBuiltProbe(t, root, "c", nil),
			}

			const callers = 20
			reports := make([]*ShutdownReport, callers)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for n := range callers {
				wg.Go(func() {
					<-start
					reports[n] = root.Shutdown()
				})
			}
			close(start)
			wg.Wait()

			for n, report := range reports {
				is.Truef(report.Succeed, "report %d", n)
				is.Emptyf(report.Errors, "report %d", n)
				is.Equalf(reports[0], report, "report %d", n)
			}
			for n, probe := range probes {
				is.Equalf(int32(1), probe.calls.Load(), "probe %d", n)
			}
		})
	}
}

func TestRootScope_RepeatedShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)
	errClose := errors.New("close failed")

	root := New()
	probe := provideBuiltProbe(t, root, "a", errClose)
	descA := newServiceDescription(root.ID(), root.Name(), "a")

	r1 := root.Shutdown()
	r2 := root.Shutdown()

	is.Equal(r1, r2)
	is.ErrorIs(r2.Errors[descA], errClose)
	is.NotSame(r1, r2)

	delete(r2.Errors, descA)
	is.ErrorIs(r1.Errors[descA], errClose)
	is.Equal(int32(1), probe.calls.Load())
}

// blockingShutdownProbe signals entered when Shutdown starts and blocks until release is closed.
type blockingShutdownProbe struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (p *blockingShutdownProbe) Shutdown() error {
	p.calls.Add(1)
	close(p.entered)
	<-p.release
	return nil
}

func TestRootScope_ShutdownWaiterDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		root := New()
		probe := &blockingShutdownProbe{entered: make(chan struct{}), release: make(chan struct{})}
		ProvideNamed(root, "a", func(Injector) (*blockingShutdownProbe, error) { return probe, nil })
		_, err := InvokeNamed[*blockingShutdownProbe](root, "a")
		require.NoError(t, err)

		first := make(chan *ShutdownReport, 1)
		go func() { first <- root.Shutdown() }()
		<-probe.entered

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		begin := time.Now()
		report := root.ShutdownWithContext(ctx)

		is.Equal(time.Second, time.Since(begin))
		is.False(report.Succeed)
		is.Len(report.Errors, 1)
		is.ErrorIs(report.Errors[rootServiceDescription(root)], context.DeadlineExceeded)

		close(probe.release)
		is.True((<-first).Succeed)
		is.Equal(int32(1), probe.calls.Load())
	})
}

func TestRootScope_ShutdownReportIsolated(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)
	errClose := errors.New("close failed")

	root := New()
	provideBuiltProbe(t, root, "a", errClose)
	descA := newServiceDescription(root.ID(), root.Name(), "a")

	r1 := root.Shutdown()
	clear(r1.Errors)

	r2 := root.Shutdown()
	is.ErrorIs(r2.Errors[descA], errClose)
}

func TestRootScope_ShutdownWithEndedContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
	}{
		{
			name: "cancelled",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
		},
		{
			name: "deadline_passed",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				return ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			root := New()
			probe := provideBuiltProbe(t, root, "a", nil)

			r1 := root.Shutdown()
			ctx := tt.ctx(t)
			for n := range 100 {
				is.Equalf(r1, root.ShutdownWithContext(ctx), "call %d", n)
			}
			is.Equal(int32(1), probe.calls.Load())
		})
	}
}

func TestRootScope_ShutdownExportedAfterContextEnded(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	tenant := root.Scope("tenant")
	service := &countedShutdowner{}
	ProvideNamed(tenant, "s", func(Injector) (*countedShutdowner, error) { return service, nil })
	require.NoError(t, ExportNamed[*countedShutdowner](tenant, "s"))
	MustInvokeNamed[*countedShutdowner](root, "s")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report := root.ShutdownWithContext(ctx)

	is.Equal(int32(0), service.calls.Load())
	is.False(report.Succeed)
	is.ErrorIs(report.Errors[newServiceDescription(tenant.ID(), tenant.Name(), "s")], context.Canceled)
	is.ErrorIs(ShutdownNamed(tenant, "s"), ErrServiceNotFound)
}

// sleepingShutdowner sleeps for d in Shutdown() error, counting its calls.
type sleepingShutdowner struct {
	calls atomic.Int32
	d     time.Duration
}

func (s *sleepingShutdowner) Shutdown() error {
	s.calls.Add(1)
	time.Sleep(s.d)
	return nil
}

func TestRootScope_ShutdownDeadlineDuringShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		root := New()
		b := &countedShutdowner{}
		a := &sleepingShutdowner{d: 2 * time.Second}
		ProvideNamed(root, "b", func(Injector) (*countedShutdowner, error) { return b, nil })
		ProvideNamed(root, "a", func(i Injector) (*sleepingShutdowner, error) {
			MustInvokeNamed[*countedShutdowner](i, "b")
			return a, nil
		})
		MustInvokeNamed[*sleepingShutdowner](root, "a")
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		report := root.ShutdownWithContext(ctx)

		is.Equal(int32(1), a.calls.Load())
		is.NotContains(report.Errors, newServiceDescription(root.ID(), root.Name(), "a"))
		is.Equal(int32(0), b.calls.Load())
		is.False(report.Succeed)
		is.ErrorIs(report.Errors[newServiceDescription(root.ID(), root.Name(), "b")], context.DeadlineExceeded)
	})
}

// ctxShutdownProbe records the contexts its Shutdown(ctx) error receives.
type ctxShutdownProbe struct {
	mu   sync.Mutex
	ctxs []context.Context
}

func (p *ctxShutdownProbe) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctxs = append(p.ctxs, ctx)
	return nil
}

func TestRootScope_ShutdownNilContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		previous func(root *RootScope)
	}{
		{name: "called_nothing", previous: func(*RootScope) {}},
		{name: "called_shutdown", previous: func(root *RootScope) { root.Shutdown() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			probe := &ctxShutdownProbe{}
			ProvideNamed(root, "a", func(Injector) (*ctxShutdownProbe, error) { return probe, nil })
			_, err := InvokeNamed[*ctxShutdownProbe](root, "a")
			require.NoError(t, err)

			tt.previous(root)

			var report *ShutdownReport
			is.NotPanics(func() {
				report = root.ShutdownWithContext(nil) //nolint:staticcheck // nil context is the case under test
			})
			if is.NotNil(report) {
				is.True(report.Succeed)
			}

			probe.mu.Lock()
			defer probe.mu.Unlock()
			if is.Len(probe.ctxs, 1) {
				is.NotNil(probe.ctxs[0])
			}
		})
	}
}

func TestRootScope_ShutdownWinnerPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(t *testing.T, root *RootScope) *ShutdownReport
	}{
		{
			name: "shutdown",
			call: func(_ *testing.T, root *RootScope) *ShutdownReport { return root.Shutdown() },
		},
		{
			name: "shutdown_with_deadline",
			call: func(t *testing.T, root *RootScope) *ShutdownReport {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				return root.ShutdownWithContext(ctx)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 2*time.Second)
			is := assert.New(t)

			root := NewWithOpts(&InjectorOpts{
				Logf: func(format string, args ...any) {
					if strings.Contains(fmt.Sprintf(format, args...), "requested shutdown") {
						panic("log-boom")
					}
				},
			})
			is.PanicsWithValue("log-boom", func() { root.Shutdown() })

			var report *ShutdownReport
			is.NotPanics(func() { report = tt.call(t, root) })
			if is.NotNil(report) {
				is.False(report.Succeed)
				is.Len(report.Errors, 1)
				is.ErrorContains(report.Errors[rootServiceDescription(root)], "DI: root shutdown did not complete")
			}
		})
	}
}

// healthyProbe is a health checker that always succeeds.
type healthyProbe struct{}

func (*healthyProbe) HealthCheck() error { return nil }

// TestRootScope_HealthCheckDuringShutdown is a race guard: HealthCheck with
// a parallelism pool racing root Shutdown must not race on the pool.
func TestRootScope_HealthCheckDuringShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 10*time.Second)

	for range 50 {
		root := NewWithOpts(&InjectorOpts{HealthCheckParallelism: 2})
		ProvideNamedValue(root, "db", &healthyProbe{})
		ProvideNamedValue(root, "cache", &healthyProbe{})

		begin := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			<-begin
			_ = root.HealthCheck()
		})

		close(begin)
		_ = root.Shutdown()
		wg.Wait()
	}
}

func TestAwaitDone(t *testing.T) {
	t.Parallel()

	t.Run("done_closed_and_context_ended", func(t *testing.T) {
		t.Parallel()
		is := assert.New(t)

		done := make(chan struct{})
		close(done)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		trues := 0
		for range 1000 {
			if awaitDone(done, ctx) {
				trues++
			}
		}
		is.Equal(1000, trues)
	})

	t.Run("only_context_ended", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.False(t, awaitDone(make(chan struct{}), ctx))
	})

	t.Run("done_closed_while_waiting", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				time.Sleep(time.Second)
				close(done)
			}()
			assert.True(t, awaitDone(done, t.Context()))
		})
	})
}
