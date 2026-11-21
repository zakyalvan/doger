package doger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stuckShutdowner blocks in Shutdown until release is closed, ignoring its
// context, in one of the four Shutdowner forms.
type (
	stuckPlain     struct{ release chan struct{} }
	stuckErr       struct{ release chan struct{} }
	stuckCtx       struct{ release chan struct{} }
	stuckCtxErr    struct{ release chan struct{} }
	afterShutdowns struct {
		mu    sync.Mutex
		calls map[string][]error
	}
)

func (s *stuckPlain) Shutdown()                       { <-s.release }
func (s *stuckErr) Shutdown() error                   { <-s.release; return nil }
func (s *stuckCtx) Shutdown(context.Context)          { <-s.release }
func (s *stuckCtxErr) Shutdown(context.Context) error { <-s.release; return nil }
func (r *afterShutdowns) hook(_ context.Context, _ *Scope, name string, _ time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string][]error{}
	}
	r.calls[name] = append(r.calls[name], err)
}

func (r *afterShutdowns) of(name string) []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error(nil), r.calls[name]...)
}

func TestShutdownContext_HungShutdownBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		inTenant bool
		viaRoot  bool
		stuck    func(release chan struct{}) any
	}{
		{name: "root_plain", stuck: func(r chan struct{}) any { return &stuckPlain{r} }},
		{name: "root_error", stuck: func(r chan struct{}) any { return &stuckErr{r} }},
		{name: "root_ctx", stuck: func(r chan struct{}) any { return &stuckCtx{r} }},
		{name: "root_ctx_error", stuck: func(r chan struct{}) any { return &stuckCtxErr{r} }},
		{name: "tenant", inTenant: true, stuck: func(r chan struct{}) any { return &stuckErr{r} }},
		{name: "tenant_via_root", inTenant: true, viaRoot: true, stuck: func(r chan struct{}) any { return &stuckErr{r} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				hooks := &afterShutdowns{}
				root := NewWithOpts(&InjectorOpts{HookAfterShutdown: []func(context.Context, *Scope, string, time.Duration, error){hooks.hook}})
				tenant := root.Scope("tenant")
				scope := root.self
				if tt.inTenant {
					scope = tenant
				}
				release := make(chan struct{})
				stuck := tt.stuck(release)
				ProvideNamedValue(scope, "stuck", stuck)
				ok := &shutdownTestCounter{}
				provideBuilt(t, scope, "ok", ok)
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()

				start := time.Now()
				var report *ShutdownReport
				if tt.inTenant && !tt.viaRoot {
					report = tenant.ShutdownWithContext(ctx)
				} else {
					report = root.ShutdownWithContext(ctx)
				}

				is.Equal(50*time.Millisecond, time.Since(start), "returned at the deadline")
				is.False(report.Succeed)
				var serviceErr *ServiceError
				if is.ErrorAs(report.Err(), &serviceErr) {
					is.Equal("stuck", serviceErr.Service)
					is.Equal(scope.Name(), serviceErr.Scope)
					is.ErrorIs(serviceErr, context.DeadlineExceeded)
				}
				is.Equal(int32(1), ok.calls.Load(), "Shutdown calls of ok")
				is.NoError(reportError(report, scope, "ok"))
				is.Empty(hooks.of("stuck"), "after hooks of stuck before release")

				close(release)
				synctest.Wait()
				is.Equal([]error{nil}, hooks.of("stuck"), "after hooks of stuck once released")
			})
		})
	}
}

// sleepingShutdown sleeps for d in Shutdown.
type sleepingShutdown struct{ d time.Duration }

func (s *sleepingShutdown) Shutdown() error {
	time.Sleep(s.d)
	return nil
}

func TestShutdownContext_SlowShutdownWaited(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sleep time.Duration
		ctx   func() (context.Context, context.CancelFunc)
	}{
		{name: "background", sleep: 2 * time.Second, ctx: func() (context.Context, context.CancelFunc) { return context.Background(), func() {} }},
		{name: "timeout_3s", sleep: 2 * time.Second, ctx: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 3*time.Second)
		}},
		{name: "timeout_1ns_not_expired", ctx: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Nanosecond)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				provideBuilt(t, root, "slow", &sleepingShutdown{d: tt.sleep})
				ctx, cancel := tt.ctx()
				defer cancel()

				start := time.Now()
				report := root.ShutdownWithContext(ctx)

				is.Equal(tt.sleep, time.Since(start))
				is.NoError(report.Err())
			})
		})
	}
}

// shutdownContextConn counts its Shutdown calls.
type shutdownContextConn struct{ calls *atomic.Int32 }

func (c *shutdownContextConn) Shutdown() error {
	c.calls.Add(1)
	return nil
}

func TestShutdownContext_InFlightBuildBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fails  bool
		gErr   error
		closes int32
	}{
		{name: "late_instance", gErr: ErrShutdown, closes: 1},
		{name: "late_failure", fails: true, gErr: io.EOF, closes: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				release := make(chan struct{})
				var closes atomic.Int32
				ProvideNamed(root, "conn", func(Injector) (*shutdownContextConn, error) {
					<-release
					if tt.fails {
						return nil, io.EOF
					}
					return &shutdownContextConn{calls: &closes}, nil
				})
				type result struct {
					conn *shutdownContextConn
					err  error
				}
				g := make(chan result, 1)
				go func() {
					conn, err := InvokeNamed[*shutdownContextConn](root, "conn")
					g <- result{conn, err}
				}()
				synctest.Wait() // G is blocked in the provider
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()

				start := time.Now()
				report := root.ShutdownWithContext(ctx)

				is.Equal(50*time.Millisecond, time.Since(start), "returned at the deadline")
				is.ErrorIs(reportError(report, root.self, "conn"), context.DeadlineExceeded)

				close(release)
				got := <-g
				synctest.Wait()
				is.Nil(got.conn)
				is.ErrorIs(got.err, tt.gErr)
				is.Equal(tt.closes, closes.Load(), "Shutdown calls of the late instance")
			})
		})
	}
}

func TestShutdownContext_InFlightBuildWaited(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		root := New()
		var closes atomic.Int32
		ProvideNamed(root, "conn", func(Injector) (*shutdownContextConn, error) {
			time.Sleep(time.Second)
			return &shutdownContextConn{calls: &closes}, nil
		})
		g := make(chan error, 1)
		go func() {
			_, err := InvokeNamed[*shutdownContextConn](root, "conn")
			g <- err
		}()
		synctest.Wait()

		start := time.Now()
		report := root.ShutdownWithContext(context.Background())

		is.Equal(time.Second, time.Since(start))
		is.NoError(report.Err())
		is.Equal(int32(1), closes.Load(), "the instance was shut down before the call returned")
		if err := <-g; err != nil {
			is.ErrorIs(err, ErrShutdown)
		}
	})
}

// blockingPool blocks in Shutdown until release is closed, counting calls.
type blockingPool struct {
	release chan struct{}
	calls   atomic.Int32
}

func (p *blockingPool) Shutdown() error {
	p.calls.Add(1)
	<-p.release
	return nil
}

func TestShutdownContext_SharedEagerBounded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		pool := &blockingPool{release: make(chan struct{})}
		root := New()
		ProvideNamedValue(root, "pool", pool)
		clone := root.Clone()
		go root.Shutdown() // runs the shared Shutdown, blocked
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()

		start := time.Now()
		report := clone.ShutdownWithContext(ctx)

		is.Equal(30*time.Millisecond, time.Since(start), "the clone returned at its deadline")
		is.ErrorIs(reportError(report, clone.self, "pool"), context.DeadlineExceeded)

		close(pool.release)
		synctest.Wait()
		is.Equal(int32(1), pool.calls.Load(), "Shutdown calls of the shared value")
	})
}

// startedConn is a Starter built by a provider that blocks.
type startedConn struct{ closes *atomic.Int32 }

func (c *startedConn) Start(context.Context) error { return nil }

func (c *startedConn) Shutdown() error {
	c.closes.Add(1)
	return nil
}

func TestShutdownContext_LateBuildDuringStart(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		root := New()
		release := make(chan struct{})
		var closes atomic.Int32
		ProvideNamed(root, "conn", func(Injector) (*startedConn, error) {
			<-release
			return &startedConn{closes: &closes}, nil
		}, AsStarter())
		started := make(chan error, 1)
		go func() { started <- root.Start() }()
		synctest.Wait() // Start is blocked in the provider
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		start := time.Now()
		report := root.ShutdownWithContext(ctx)

		is.Equal(50*time.Millisecond, time.Since(start), "returned at the deadline")
		is.False(report.Succeed)

		close(release)
		err := <-started
		synctest.Wait()
		is.ErrorIs(err, ErrShutdown)
		is.Equal(int32(1), closes.Load(), "the late instance is shut down once")
	})
}

// lateLog is an ordered log of shutdowns.
type lateLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *lateLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, s)
}

func (l *lateLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

type lateDB struct{ log *lateLog }

func (d *lateDB) Shutdown() error { d.log.add("db"); return nil }

// lateConn sleeps 100ms in Shutdown, then fails or panics.
type lateConn struct {
	log    *lateLog
	panics bool
}

func (c *lateConn) Shutdown() error {
	time.Sleep(100 * time.Millisecond)
	c.log.add("conn")
	if c.panics {
		panic("conn boom")
	}
	return errors.New("conn close failed")
}

func TestShutdownContext_UnboundedWaitsForLateInstance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		panics bool
		match  error
		text   string
	}{
		{name: "late_shutdown_error", text: "conn close failed"},
		{name: "late_shutdown_panic", panics: true, match: ErrShutdownPanic, text: "conn boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				log := &lateLog{}
				root := New()
				ProvideNamed(root, "db", func(Injector) (*lateDB, error) { return &lateDB{log: log}, nil })
				release := make(chan struct{})
				ProvideNamed(root, "conn", func(i Injector) (*lateConn, error) {
					MustInvokeNamed[*lateDB](i, "db") // conn depends on db
					<-release
					return &lateConn{log: log, panics: tt.panics}, nil
				})
				g := make(chan error, 1)
				go func() {
					_, err := InvokeNamed[*lateConn](root, "conn")
					g <- err
				}()
				synctest.Wait() // conn's provider holds db, blocked

				reports := make(chan *ShutdownReport, 1)
				go func() { reports <- root.ShutdownWithContext(context.Background()) }()
				synctest.Wait()
				close(release)
				report := <-reports
				atReturn := log.list()

				is.Equal([]string{"conn", "db"}, atReturn, "the late instance shut down before its dependency, before the call returned")
				err := reportError(report, root.self, "conn")
				is.ErrorContains(err, tt.text)
				if tt.match != nil {
					is.ErrorIs(err, tt.match)
				}
				is.ErrorIs(<-g, ErrShutdown)
			})
		})
	}
}

// lateBoomShutdowner panics in Shutdown once released.
type lateBoomShutdowner struct{ release chan struct{} }

func (p *lateBoomShutdowner) Shutdown() error {
	<-p.release
	panic("late-boom")
}

func TestShutdownContext_AbandonedShutdownPanicIsLogged(t *testing.T) {
	t.Parallel()

	for _, withHook := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_hook", true: "with_hook"}[withHook], func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				recorder := newLogRecorder(slog.LevelDebug)
				var hookCalls atomic.Int32
				opts := &InjectorOpts{Logger: recorder.logger()}
				if withHook {
					opts.HookContainerPanic = func(*RootScope, string, any) { hookCalls.Add(1) }
				}
				root := NewWithOpts(opts)
				p := &lateBoomShutdowner{release: make(chan struct{})}
				ProvideNamedValue(root, "p", p)
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()

				report := root.ShutdownWithContext(ctx)
				is.ErrorIs(report.Err(), context.DeadlineExceeded)

				close(p.release)
				synctest.Wait()
				is.Zero(hookCalls.Load(), "the dispatch was over when the panic arrived")
				recovered := recorder.withMessage("panic recovered")
				if is.Len(recovered, 1, "records: %v", recorder.all()) {
					is.Equal("service-shutdown", recovered[0].attrs["point"])
					is.Equal(slog.LevelError, recovered[0].level)
				}
				is.Len(recorder.containing("late-boom"), 1, "the value, at Debug only")
			})
		})
	}
}

// slowShutdown sleeps 1ms in Shutdown.
type slowShutdown struct{}

func (slowShutdown) Shutdown() error { time.Sleep(time.Millisecond); return nil }

func TestShutdownContext_AbandonUnscheduledOnce(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 120*time.Second)

	notFound, dups, other := 0, 0, 0
	for round := range 300 {
		root := New()
		ProvideNamed(root, "base", func(Injector) (slowShutdown, error) { return slowShutdown{}, nil })
		for n := range 300 {
			name := fmt.Sprintf("s%d", n)
			ProvideNamed(root, name, func(i Injector) (slowShutdown, error) {
				MustInvokeNamed[slowShutdown](i, "base")
				return slowShutdown{}, nil
			})
			MustInvokeNamed[slowShutdown](root, name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(20+round%200)*time.Microsecond)
		report := root.ShutdownWithContext(ctx)
		cancel()

		seen := map[ServiceDescription]int{}
		for _, d := range report.Services {
			seen[d]++
			if seen[d] == 2 {
				dups++
			}
		}
		for _, err := range report.Errors {
			switch {
			case errors.Is(err, ErrServiceNotFound):
				notFound++
			case !errors.Is(err, context.DeadlineExceeded):
				other++
			}
		}
	}

	assert.Zero(t, notFound, "services reported not found")
	assert.Zero(t, dups, "services listed twice")
	assert.Zero(t, other, "other errors")
}

// closingPool records that its Shutdown ran.
type closingPool struct{ closed atomic.Bool }

func (p *closingPool) Shutdown() error { p.closed.Store(true); return nil }

func TestShutdownContext_EagerDependsOnAfterShutdown(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		root := New()
		release := make(chan struct{})
		ProvideNamed(root, "dep", func(Injector) (int, error) { <-release; return 1, nil })
		tenant := root.Scope("tenant")
		pool := &closingPool{}
		ProvideNamedValue(tenant, "pool", pool, DependsOnNamed("dep"))
		type result struct {
			pool *closingPool
			err  error
		}
		g := make(chan result, 1)
		go func() {
			p, err := InvokeNamed[*closingPool](tenant, "pool")
			g <- result{p, err}
		}()
		synctest.Wait() // G resolves "dep", blocked

		is.NoError(tenant.Shutdown().Err())
		is.True(pool.closed.Load())
		close(release)
		got := <-g

		is.Nil(got.pool, "a shut-down eager value is not handed out")
		is.ErrorIs(got.err, ErrShutdown)
		root.Shutdown()
	})
}

// namedConn counts its Shutdown calls.
type namedConn struct{ calls *atomic.Int32 }

func (c *namedConn) Shutdown() error { c.calls.Add(1); return nil }

func TestShutdownContext_ShutdownNamedBoundedInFlight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		self     bool // the provider shuts its own service down
		wantTook time.Duration
		wantErr  error
	}{
		{name: "other_goroutine", wantTook: 50 * time.Millisecond, wantErr: context.DeadlineExceeded},
		// waiting would wait for itself: it returns at once, and the build
		// shuts its late instance down
		{name: "provider_shuts_itself_down", self: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				release := make(chan struct{})
				var calls atomic.Int32
				type shutdownResult struct {
					err  error
					took time.Duration
				}
				done := make(chan shutdownResult, 1)
				ProvideNamed(root, "conn", func(i Injector) (*namedConn, error) {
					if tt.self {
						ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
						defer cancel()
						start := time.Now()
						err := ShutdownNamedWithContext(ctx, i, "conn")
						done <- shutdownResult{err, time.Since(start)}
						return &namedConn{calls: &calls}, nil
					}
					<-release
					return &namedConn{calls: &calls}, nil
				})
				g := make(chan error, 1)
				go func() { _, err := InvokeNamed[*namedConn](root, "conn"); g <- err }()

				if !tt.self {
					synctest.Wait() // G is blocked in the provider
					ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
					defer cancel()
					go func() {
						start := time.Now()
						err := ShutdownNamedWithContext(ctx, root, "conn")
						done <- shutdownResult{err, time.Since(start)}
					}()
				}

				var got shutdownResult
				select {
				case got = <-done:
				case <-time.After(10 * time.Second):
					got.took = -1
				}
				close(release)
				if got.took < 0 {
					got = <-done
					got.took = -1
				}
				gErr := <-g
				synctest.Wait()

				is.Equal(tt.wantTook, got.took, "returned at its deadline, or at once")
				if tt.wantErr == nil {
					is.NoError(got.err)
				} else {
					is.ErrorIs(got.err, tt.wantErr)
				}
				is.ErrorIs(gErr, ErrShutdown)
				is.Equal(int32(1), calls.Load(), "the late instance is shut down once")
				root.Shutdown()
			})
		})
	}
}

// sickCheck always reports unhealthy.
type sickCheck struct{}

func (sickCheck) HealthCheck() error { return errors.New("sick") }

func TestShutdownContext_HealthHookDuringBoundedShutdown(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		release := make(chan struct{})
		var calls atomic.Int32
		scheduler := &manualScheduler{}
		root := NewWithOpts(&InjectorOpts{HealthScheduler: scheduler, HookContainerHealthChanged: []func(*RootScope, HealthState, HealthState){
			func(*RootScope, HealthState, HealthState) {
				if calls.Add(1) > 1 {
					<-release // the change the tenant's removal causes
				}
			},
		}})
		tenant := root.Scope("tenant")
		ProvideNamedValue(tenant, "h", sickCheck{})
		require.NoError(t, root.Start())
		scheduler.runRound()
		synctest.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		done := make(chan *ShutdownReport, 1)
		go func() { done <- tenant.ShutdownWithContext(ctx) }()
		took := time.Duration(-1)
		select {
		case <-done:
			took = time.Since(start)
		case <-time.After(10 * time.Second):
		}
		close(release)
		if took < 0 {
			<-done
		}
		synctest.Wait()

		is.GreaterOrEqual(took, time.Duration(0), "the shutdown returned before the hook was released")
		is.LessOrEqual(took, 50*time.Millisecond)
		root.Shutdown()
	})
}

// boomShutdowner panics in Shutdown.
type boomShutdowner struct{}

func (boomShutdowner) Shutdown() error { panic("fallback-boom") }

func TestShutdownContext_FallbackLatePanicLogged(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		recorder := newLogRecorder(slog.LevelDebug)
		root := NewWithOpts(&InjectorOpts{Logger: recorder.logger()})
		release := make(chan struct{})
		ProvideNamed(root, "conn", func(Injector) (boomShutdowner, error) { <-release; return boomShutdowner{}, nil })
		g := make(chan error, 1)
		go func() { _, err := InvokeNamed[boomShutdowner](root, "conn"); g <- err }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the shutdown reaches "conn" after its context ended

		root.ShutdownWithContext(ctx)
		close(release)
		gErr := <-g
		synctest.Wait()

		is.ErrorIs(gErr, ErrShutdown)
		recovered := recorder.withMessage("panic recovered")
		if is.Len(recovered, 1, "records: %v", recorder.all()) {
			is.Equal("service-shutdown", recovered[0].attrs["point"])
			is.Equal("conn", recovered[0].attrs["service"])
		}
		is.Len(recorder.containing("fallback-boom"), 1, "the value, at Debug only")
	})
}

// lateCounted counts its Shutdown calls.
type lateCounted struct{ calls *atomic.Int32 }

func (c *lateCounted) Shutdown() error { c.calls.Add(1); return nil }

// TestShutdownContext_HandoverThenCtxEnds changes GOMAXPROCS, so it is not
// parallel: the context ends right after the build hands its instance over.
func TestShutdownContext_HandoverThenCtxEnds(t *testing.T) { //nolint:paralleltest // changes GOMAXPROCS
	testWithTimeout(t, 60*time.Second)
	prev := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prev)

	leaked := 0
	const n = 200
	for range n {
		root := New()
		var calls atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		entered, release := make(chan struct{}), make(chan struct{})
		ProvideNamed(root, "conn", func(Injector) (*lateCounted, error) {
			close(entered)
			<-release
			return &lateCounted{calls: &calls}, nil
		})
		g := make(chan error, 1)
		go func() {
			_, err := InvokeNamed[*lateCounted](root, "conn")
			cancel() // the deadline passes right after the handover
			g <- err
		}()
		<-entered
		done := make(chan struct{})
		go func() { defer close(done); _ = ShutdownNamedWithContext(ctx, root, "conn") }()
		time.Sleep(time.Millisecond) // the shutdown waits for the build
		close(release)
		<-g
		<-done
		assert.Eventually(t, func() bool { return calls.Load() >= 1 }, time.Second, time.Millisecond)
		// a second Shutdown call, if any, has a window to appear
		if !assert.Never(t, func() bool { return calls.Load() > 1 }, 20*time.Millisecond, time.Millisecond) || calls.Load() != 1 {
			leaked++
		}
		root.Shutdown()
	}

	assert.Zero(t, leaked, "late instances not shut down exactly once, out of %d", n)
}

// drainingConn drains until its context ends, or for at most 1h,
// recording the context its Shutdown receives.
type drainingConn struct {
	calls *atomic.Int32
	ctx   *atomic.Pointer[context.Context]
}

func (c *drainingConn) Shutdown(ctx context.Context) error {
	c.calls.Add(1)
	c.ctx.Store(&ctx)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Hour):
		return nil
	}
}

func TestShutdownContext_InvokerNotHeldByLateShutdown(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"root", "named", "ended"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				var calls atomic.Int32
				var received atomic.Pointer[context.Context]
				release := make(chan struct{})
				ProvideNamed(root, "conn", func(Injector) (*drainingConn, error) {
					<-release
					return &drainingConn{calls: &calls, ctx: &received}, nil
				})
				type outcome struct {
					at  time.Time
					err error
				}
				invoked := make(chan outcome, 1)
				go func() {
					_, err := InvokeNamed[*drainingConn](root, "conn")
					invoked <- outcome{time.Now(), err}
				}()
				synctest.Wait()
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				if mode == "ended" {
					time.Sleep(60 * time.Millisecond)
				}
				if mode == "named" {
					_ = ShutdownNamedWithContext(ctx, root, "conn")
				} else {
					_ = root.ShutdownWithContext(ctx)
				}

				released := time.Now()
				close(release)
				got := <-invoked
				is.Zero(got.at.Sub(released), "the invoker returns at once")
				is.ErrorIs(got.err, ErrShutdown, "the late instance is not handed out")
				synctest.Wait()
				is.Equal(int32(1), calls.Load(), "the late instance was shut down")
				// with the shutdown's own context, ended, not a fresh one
				if shutdownCtx := received.Load(); is.NotNil(shutdownCtx) {
					want, _ := ctx.Deadline()
					deadline, ok := (*shutdownCtx).Deadline()
					is.True(ok, "the Shutdown context has the shutdown's deadline")
					is.Equal(want, deadline)
					is.ErrorIs((*shutdownCtx).Err(), context.DeadlineExceeded)
				}
			})
		})
	}
}

// ctxDrainingConn drains until its context ends.
type ctxDrainingConn struct{ calls *atomic.Int32 }

func (c *ctxDrainingConn) Shutdown(ctx context.Context) error {
	c.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

// TestShutdownContext_HandoverCtxBranchBounded changes GOMAXPROCS, so it is
// not parallel: a late instance handed over as the context ends must not
// hold ShutdownNamedWithContext or the invoker.
func TestShutdownContext_HandoverCtxBranchBounded(t *testing.T) { //nolint:paralleltest // changes GOMAXPROCS
	testWithTimeout(t, 120*time.Second)
	prev := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prev)

	hung := 0
	const n = 200
	for range n {
		root := New()
		var calls atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		entered, release := make(chan struct{}), make(chan struct{})
		ProvideNamed(root, "conn", func(Injector) (*ctxDrainingConn, error) {
			close(entered)
			<-release
			cancel()
			return &ctxDrainingConn{calls: &calls}, nil
		})
		g := make(chan error, 1)
		go func() { _, err := InvokeNamed[*ctxDrainingConn](root, "conn"); g <- err }()
		<-entered
		done := make(chan struct{})
		go func() { defer close(done); _ = ShutdownNamedWithContext(ctx, root, "conn") }()
		time.Sleep(time.Millisecond)
		close(release)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			hung++
		}
		select {
		case <-g:
		case <-time.After(2 * time.Second):
			hung++
		}
		root.Shutdown()
	}

	assert.Zero(t, hung, "calls held past their bound, out of %d", n)
}

func TestShutdownContext_SelfShutdownUnbounded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		root := New()
		var calls atomic.Int32
		res := make(chan error, 1)
		ProvideNamed(root, "conn", func(i Injector) (*lateCounted, error) {
			res <- ShutdownNamed(i, "conn") // unbounded self-shutdown
			return &lateCounted{calls: &calls}, nil
		})

		_, err := InvokeNamed[*lateCounted](root, "conn")
		synctest.Wait()

		is.ErrorIs(err, ErrShutdown)
		is.NoError(<-res)
		is.Equal(int32(1), calls.Load(), "the build shut its late instance down")
	})
}

// healthChildEnv makes the test binary run the child side of
// TestShutdownContext_HealthHookRepanicDuringShutdown.
const healthChildEnv = "DOGER_HEALTH_REPANIC_CHILD"

// waitHealthDeliveryIdle waits until no goroutine delivers health
// transitions of root and none is queued.
func waitHealthDeliveryIdle(root *RootScope) {
	m := &root.health
	for {
		m.mu.Lock()
		idle := !m.delivering && len(m.transitions) == 0
		m.mu.Unlock()
		if idle {
			return
		}
		runtime.Gosched()
	}
}

func TestShutdownContext_HealthHookRepanicDuringShutdown(t *testing.T) {
	if os.Getenv(healthChildEnv) == "1" {
		scheduler := &manualScheduler{}
		var armed atomic.Bool
		firstChange := make(chan struct{})
		var once sync.Once
		root := NewWithOpts(&InjectorOpts{
			HealthScheduler:    scheduler,
			HookContainerPanic: func(_ *RootScope, _ string, v any) { panic(v) },
			HookContainerHealthChanged: []func(*RootScope, HealthState, HealthState){
				func(*RootScope, HealthState, HealthState) {
					if armed.Load() {
						panic("health-hook-boom")
					}
					once.Do(func() {
						close(firstChange)
						// keeps this delivering goroutine busy after the
						// signal, as a preemption here would
						time.Sleep(50 * time.Millisecond)
					})
				},
			},
		})
		tenant := root.Scope("tenant")
		ProvideNamedValue(tenant, "h", sickCheck{})
		if err := root.Start(); err != nil {
			panic(err)
		}
		scheduler.runRound()
		<-firstChange // the round's change was delivered
		// the round's goroutine still holds the delivery until its loop
		// ends; the shutdown's change must find no delivery running, or that
		// goroutine delivers it instead of the caller
		waitHealthDeliveryIdle(root)
		armed.Store(true)
		func() {
			defer func() { fmt.Printf("RECOVERED:%v\n", recover()) }() // HookContainerPanic re-panics on the caller
			tenant.ShutdownWithContext(context.Background())
		}()
		fmt.Println("RETURNED")
		return
	}

	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestShutdownContext_HealthHookRepanicDuringShutdown$") //nolint:gosec // G204: re-runs this test binary with a fixed flag
	cmd.Env = append(os.Environ(), healthChildEnv+"=1")
	output, err := cmd.CombinedOutput()

	require.NoError(t, err, "the child process crashed:\n%s", output)
	assert.Contains(t, string(output), "RECOVERED:health-hook-boom", "the panic value reaches the caller")
	assert.Contains(t, string(output), "RETURNED")
}
