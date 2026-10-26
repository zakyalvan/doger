package doger

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wrapperOf returns the wrapper registered as name in scope.
func wrapperOf(t *testing.T, scope *Scope, name string) any {
	t.Helper()
	scope.mu.RLock()
	defer scope.mu.RUnlock()

	wrapper, ok := scope.services[name]
	require.True(t, ok, "service %s not registered", name)
	return wrapper
}

// tracked reports whether the start bookkeeping of root still holds wrapper.
func tracked(root *RootScope, wrapper any) (started, handled bool) {
	root.startedMu.Lock()
	defer root.startedMu.Unlock()

	_, started = root.started[wrapper]
	return started, len(root.handled[wrapper]) > 0
}

func TestRootScope_ForgetsShutDownServices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		shutdown func(t *testing.T, root *RootScope, tenant *Scope)
		keptUp   bool
	}{
		{
			name: "tenant.Shutdown()",
			shutdown: func(t *testing.T, _ *RootScope, tenant *Scope) {
				t.Helper()
				require.True(t, tenant.Shutdown().Succeed)
			},
			keptUp: true,
		},
		{
			name: "ShutdownNamed(tenant, …)",
			shutdown: func(t *testing.T, _ *RootScope, tenant *Scope) {
				t.Helper()
				require.NoError(t, ShutdownNamed(tenant, "worker"))
				require.NoError(t, ShutdownNamed(tenant, "job"))
			},
			keptUp: true,
		},
		{
			name:     "root.Shutdown()",
			shutdown: func(t *testing.T, root *RootScope, _ *Scope) { t.Helper(); require.True(t, root.Shutdown().Succeed) },
			keptUp:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			scheduled := NewKind[Job, Spec]("scheduled")
			root := New()
			scheduled.Handle(root, StageBeforeStart, func(context.Context, Component[Job, Spec]) error { return nil })
			starts := &countingStarters{}
			tenant := root.Scope("tenant")
			other := root.Scope("other")
			provideCountedStarter(tenant, starts, "worker")
			ProvideNamed(tenant, "job", func(Injector) (*CleanupJob, error) { return &CleanupJob{name: "job"}, nil }, scheduled.With(Spec{}))
			provideCountedStarter(other, starts, "keeper")
			require.NoError(t, root.Start())
			worker, job, keeper := wrapperOf(t, tenant, "worker"), wrapperOf(t, tenant, "job"), wrapperOf(t, other, "keeper")
			started, _ := tracked(root, worker)
			require.True(t, started)
			_, handled := tracked(root, job)
			require.True(t, handled)

			tt.shutdown(t, root, tenant)

			started, _ = tracked(root, worker)
			is.False(started, "worker still in started")
			_, handled = tracked(root, job)
			is.False(handled, "job still in handled")
			started, _ = tracked(root, keeper)
			is.Equal(tt.keptUp, started, "keeper in started")
		})
	}
}

func TestIncrementalStart_DuringStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(root *RootScope, tenant *Scope) error
	}{
		{
			name: "root.Refresh() synchronously",
			call: func(root *RootScope, _ *Scope) error { return root.Refresh() },
		},
		{
			name: "tenant.Start() synchronously",
			call: func(_ *RootScope, tenant *Scope) error { return tenant.Start() },
		},
		{
			name: "root.Refresh() in another goroutine, awaited",
			call: func(root *RootScope, _ *Scope) error {
				result := make(chan error, 1)
				go func() { result <- root.Refresh() }()
				return <-result
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			tenant := root.Scope("tenant")
			starts := &countingStarters{}
			provideCountedStarter(tenant, starts, "w")
			var recorded error
			provideLazy(root, "a", &ctxStarter{fn: func(context.Context) error {
				recorded = tt.call(root, tenant)
				return nil
			}})

			err := root.Start()

			is.NoError(err)
			is.ErrorIs(recorded, ErrNotStarted)
			is.Equal(1, starts.count("w"))
		})
	}
}

// gatedStarter blocks in Start until release is closed and records
// "start-returned" and "shutdown" in events.
type gatedStarter struct {
	entered chan struct{}
	release chan struct{}
	events  *callRecorder
}

func newGatedStarter(events *callRecorder) *gatedStarter {
	return &gatedStarter{entered: make(chan struct{}), release: make(chan struct{}), events: events}
}

func (b *gatedStarter) Start() error {
	close(b.entered)
	<-b.release
	b.events.record("start-returned")
	return nil
}

func (b *gatedStarter) Shutdown() error {
	b.events.record("shutdown")
	return nil
}

func TestScopeShutdown_WaitsForStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(root *RootScope, tenant *Scope, provideW func()) error
	}{
		{
			name: "root.Start()",
			run: func(root *RootScope, _ *Scope, provideW func()) error {
				provideW()
				return root.Start()
			},
		},
		{
			name: "root.Refresh()",
			run: func(root *RootScope, _ *Scope, provideW func()) error {
				if err := root.Start(); err != nil {
					return err
				}
				provideW()
				return root.Refresh()
			},
		},
		{
			name: "tenant.Start()",
			run: func(root *RootScope, tenant *Scope, provideW func()) error {
				if err := root.Start(); err != nil {
					return err
				}
				provideW()
				return tenant.Start()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				tenant := root.Scope("tenant")
				events := &callRecorder{}
				w := newGatedStarter(events)
				provideW := func() { ProvideNamed(tenant, "w", func(Injector) (*gatedStarter, error) { return w, nil }) }

				runErr := make(chan error, 1)
				go func() { runErr <- tt.run(root, tenant, provideW) }()
				<-w.entered
				wrapper := wrapperOf(t, tenant, "w")
				shutdown := make(chan *ShutdownReport, 1)
				go func() { shutdown <- tenant.Shutdown() }()
				synctest.Wait()

				select {
				case <-shutdown:
					t.Fatal("tenant.Shutdown() returned while w was still starting")
				default:
				}

				close(w.release)
				is.NoError(<-runErr)
				report := <-shutdown
				is.True(report.Succeed)
				is.Equal([]string{"start-returned", "shutdown"}, events.list())
				started, _ := tracked(root, wrapper)
				is.False(started, "w still in started")
			})
		})
	}
}

func TestScopeShutdown_LaterStartSkips(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	k := NewKind[Job, Spec]("k")
	var handled atomic.Int32
	k.Handle(root, StageBeforeStart, func(context.Context, Component[Job, Spec]) error {
		handled.Add(1)
		return nil
	})
	require.NoError(t, root.Start())
	tenant := root.Scope("tenant")
	starts := &countingStarters{}
	provideCountedStarter(tenant, starts, "w")
	ProvideNamed(tenant, "j", func(Injector) (*CleanupJob, error) { return &CleanupJob{name: "j"}, nil }, k.With(Spec{}))
	entered, release := make(chan struct{}), make(chan struct{})
	root.AddScopeClosingHook(func(context.Context, *Scope) {
		close(entered)
		<-release
	})
	shutdown := make(chan *ShutdownReport, 1)
	go func() { shutdown <- tenant.Shutdown() }()
	<-entered

	err := root.Refresh()

	close(release)
	is.NoError(err)
	is.Equal(0, starts.count("w"))
	is.Equal(int32(0), handled.Load(), "the kind handler handled a service of the closing scope")
	is.True((<-shutdown).Succeed)
}

// countedShutdowner counts its Shutdown calls.
type countedShutdowner struct{ calls atomic.Int32 }

func (s *countedShutdowner) Shutdown() error {
	s.calls.Add(1)
	return nil
}

func TestScopeShutdown_ConcurrentShutdownsJoin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		second func(root *RootScope, tenant *Scope) *ShutdownReport
	}{
		{name: "tenant.Shutdown twice", second: func(_ *RootScope, tenant *Scope) *ShutdownReport { return tenant.Shutdown() }},
		{name: "tenant.Shutdown, then root.Shutdown", second: func(root *RootScope, _ *Scope) *ShutdownReport { return root.Shutdown() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				entered, release := make(chan struct{}), make(chan struct{})
				var closing, closed atomic.Int32
				root := New()
				root.AddScopeClosingHook(func(context.Context, *Scope) {
					if closing.Add(1) == 1 {
						close(entered)
						<-release
					}
				})
				root.AddScopeClosedHook(func(context.Context, *Scope) { closed.Add(1) })
				tenant := root.Scope("tenant")
				services := map[string]*countedShutdowner{}
				for _, name := range []string{"a", "b", "c", "d"} {
					services[name] = &countedShutdowner{}
					ProvideNamedValue(tenant, name, services[name])
				}

				firstDone := make(chan *ShutdownReport, 1)
				go func() { firstDone <- tenant.Shutdown() }()
				<-entered
				secondDone := make(chan *ShutdownReport, 1)
				go func() { secondDone <- tt.second(root, tenant) }()
				synctest.Wait() // the second shutdown reached tenant, or ended
				close(release)
				first, second := <-firstDone, <-secondDone

				// one shutdown of the scope ran; the other joined it
				is.True(first.Succeed, "first report: %v", first.Errors)
				is.True(second.Succeed, "second report: %v", second.Errors)
				is.Equal(int32(1), closing.Load(), "HookScopeClosing runs")
				is.Equal(int32(1), closed.Load(), "HookScopeClosed runs")
				for name, service := range services {
					is.Equal(int32(1), service.calls.Load(), "%s shut down", name)
				}
			})
		})
	}
}

func TestScopeShutdown_JoinBoundedByContext(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		entered, release := make(chan struct{}), make(chan struct{})
		root := New()
		root.AddScopeClosingHook(func(context.Context, *Scope) {
			close(entered)
			<-release
		})
		tenant := root.Scope("tenant")
		service := &countedShutdowner{}
		ProvideNamedValue(tenant, "a", service)
		firstDone := make(chan *ShutdownReport, 1)
		go func() { firstDone <- tenant.Shutdown() }()
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		second := tenant.ShutdownWithContext(ctx)

		// the joining shutdown gives up when its context ends; the first goes on
		is.False(second.Succeed)
		is.ErrorIs(second.Errors[newServiceDescription(tenant.ID(), tenant.Name(), "")], context.DeadlineExceeded)
		close(release)
		is.True((<-firstDone).Succeed)
		is.Equal(int32(1), service.calls.Load())
	})
}

func TestScopeShutdown_JoinedWinnerPanics(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		entered, release := make(chan struct{}), make(chan struct{})
		var panicked atomic.Bool
		root := NewWithOpts(&InjectorOpts{
			// the winning shutdown panics after its services shut down
			Logf: func(format string, _ ...any) {
				if format == "DI <scope=tenant>: shut down services" && panicked.CompareAndSwap(false, true) {
					panic("log-boom")
				}
			},
		})
		root.AddScopeClosingHook(func(context.Context, *Scope) {
			close(entered)
			<-release
		})
		tenant := root.Scope("tenant")
		firstPanic := make(chan any, 1)
		go func() {
			defer func() { firstPanic <- recover() }()
			tenant.Shutdown()
		}()
		<-entered
		secondDone := make(chan *ShutdownReport, 1)
		go func() { secondDone <- tenant.Shutdown() }()
		synctest.Wait()
		close(release)

		is.Equal("log-boom", <-firstPanic)
		second := <-secondDone
		if is.NotNil(second) {
			is.False(second.Succeed)
			is.ErrorContains(second.Errors[newServiceDescription(tenant.ID(), tenant.Name(), "")], "DI: scope shutdown did not complete")
		}
	})
}

func TestRefresh_QueuedBehindShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	// a slow Closing hook keeps Shutdown in progress while the queued
	// Refresh takes the pipeline
	slow := func(context.Context, *RootScope) { time.Sleep(100 * time.Millisecond) }
	root := NewWithOpts(&InjectorOpts{HookContainerClosing: []func(context.Context, *RootScope){slow}})
	require.NoError(t, root.Start())
	entered := make(chan struct{})
	var once sync.Once
	var starts atomic.Int32
	provideLazy(root, "late", &ctxStarter{fn: func(ctx context.Context) error {
		starts.Add(1)
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	}})
	firstDone := make(chan error, 1)
	go func() { firstDone <- root.Refresh() }()
	<-entered
	secondDone := make(chan error, 1)
	go func() { secondDone <- root.Refresh() }()
	time.Sleep(50 * time.Millisecond) // the second Refresh waits for the pipeline

	root.Shutdown()

	is.ErrorIs(<-firstDone, ErrStartAfterShutdown)
	is.ErrorIs(<-secondDone, ErrStartAfterShutdown)
	is.Equal(int32(1), starts.Load(), "the queued Refresh started nothing")
}

func TestScopeShutdown_CreatedDuringStart(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	starts := &countingStarters{}
	var tmpReport *ShutdownReport
	ProvideNamed(root, "a", func(i Injector) (*countedStarter, error) {
		tmp := i.Scope("tmp")
		ProvideNamed(tmp, "x", func(Injector) (*buildDB, error) { return &buildDB{}, nil })
		if _, err := InvokeNamed[*buildDB](tmp, "x"); err != nil {
			return nil, err
		}
		tmpReport = tmp.Shutdown()
		return &countedStarter{name: "a", starts: starts}, nil
	})

	err := root.Start()

	is.NoError(err)
	is.Equal(1, starts.count("a"))
	require.NotNil(t, tmpReport)
	is.True(tmpReport.Succeed)
}

func TestScopeShutdown_WaitBounded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		root := New()
		require.NoError(t, root.Start())
		tenant := root.Scope("tenant")
		w := newGatedStarter(&callRecorder{})
		ProvideNamed(tenant, "w", func(Injector) (*gatedStarter, error) { return w, nil })
		runErr := make(chan error, 1)
		go func() { runErr <- root.Refresh() }()
		<-w.entered
		wrapper := wrapperOf(t, tenant, "w")
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		begin := time.Now()

		report := tenant.ShutdownWithContext(ctx)

		is.Equal(time.Second, time.Since(begin))
		is.False(report.Succeed)
		is.ErrorIs(report.Errors[newServiceDescription(tenant.ID(), tenant.Name(), "")], context.DeadlineExceeded)
		close(w.release)
		is.NoError(<-runErr)
		started, _ := tracked(root, wrapper)
		is.False(started, "shut-down w re-added to started")
	})
}

func TestStartError_Incremental(t *testing.T) {
	t.Parallel()

	errBuild := errors.New("build failed")
	errBoom := errors.New("boom")

	tests := []struct {
		name    string
		setup   func(root *RootScope, tenant *Scope)
		call    func(root *RootScope, tenant *Scope) error
		service string
		kind    string
		op      StartOp
		cause   error
	}{
		{
			name: "provider returns errBuild, root.Refresh()",
			setup: func(_ *RootScope, tenant *Scope) {
				ProvideNamed(tenant, "b", func(Injector) (*startWithErr, error) { return nil, errBuild })
			},
			call:    func(root *RootScope, _ *Scope) error { return root.Refresh() },
			service: "b", op: StartOpBuild, cause: errBuild,
		},
		{
			name: "Start returns errBoom, tenant.Start()",
			setup: func(_ *RootScope, tenant *Scope) {
				provideLazy(tenant, "b", &errorStarter{err: errBoom})
			},
			call:    func(_ *RootScope, tenant *Scope) error { return tenant.Start() },
			service: "b", op: StartOpStart, cause: errBoom,
		},
		{
			name: "kind handler returns errBoom, root.Refresh()",
			setup: func(root *RootScope, tenant *Scope) {
				k := NewKind[Job, Spec]("k")
				ProvideNamed(tenant, "j", func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil }, k.With(Spec{}))
				k.Handle(root, StageBeforeStart, func(context.Context, Component[Job, Spec]) error { return errBoom })
			},
			call:    func(root *RootScope, _ *Scope) error { return root.Refresh() },
			service: "j", kind: "k", op: StartOpHandle, cause: errBoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			require.NoError(t, root.Start())
			tenant := root.Scope("tenant")
			tt.setup(root, tenant)

			err := tt.call(root, tenant)

			var startErr *StartError
			require.ErrorAs(t, err, &startErr)
			is.Equal(tt.service, startErr.Service)
			is.Equal("tenant", startErr.Scope.Name())
			is.Equal(tt.kind, startErr.Kind)
			is.Equal(tt.op, startErr.Op)
			is.ErrorIs(err, tt.cause)
		})
	}
}

func TestScopeStart_AncestorsNotStarted(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	require.NoError(t, root.Start())
	starts := &countingStarters{}
	var dbBuilds int
	ProvideNamed(root, "db", func(Injector) (*countedStarter, error) {
		dbBuilds++
		return &countedStarter{name: "db", starts: starts}, nil
	})
	tenant := root.Scope("tenant")
	ProvideNamed(tenant, "worker", func(i Injector) (*countedStarter, error) {
		if _, err := InvokeNamed[*countedStarter](i, "db"); err != nil {
			return nil, err
		}
		return &countedStarter{name: "worker", starts: starts}, nil
	})

	err := tenant.Start()

	is.NoError(err)
	is.Equal(1, starts.count("worker"))
	is.Equal(1, dbBuilds)
	is.Equal(0, starts.count("db"))
}

func TestRootScope_ForgetsOverriddenServices(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	starts := &countingStarters{}
	provideCountedStarter(root, starts, "s")
	require.NoError(t, root.Start())
	for range 5 {
		OverrideNamed(root, "s", func(Injector) (*countedStarter, error) { return &countedStarter{name: "s", starts: starts}, nil })
		require.NoError(t, root.Refresh())
	}

	root.startedMu.Lock()
	defer root.startedMu.Unlock()
	assert.Len(t, root.started, 1)
	assert.Equal(t, 6, starts.count("s"))
}

func TestScopeStartingHook_ShutdownCause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		root := New()
		require.NoError(t, root.Start())
		tenant := root.Scope("tenant")
		done := make(chan struct{})
		root.AddScopeStartingHook(func(ctx context.Context, _ *Scope) error {
			go func() {
				root.Shutdown()
				close(done)
			}()
			<-ctx.Done()
			return ctx.Err()
		})

		err := tenant.Start()
		<-done

		assert.ErrorIs(t, err, ErrStartAfterShutdown)
	})
}

func TestScopeShutdown_DescendantSkipped(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	require.NoError(t, root.Start())
	tenant := root.Scope("tenant")
	entered, release := make(chan struct{}), make(chan struct{})
	root.AddScopeClosingHook(func(_ context.Context, scope *Scope) {
		if scope == tenant {
			close(entered)
			<-release
		}
	})
	shutdown := make(chan *ShutdownReport, 1)
	go func() { shutdown <- tenant.Shutdown() }()
	<-entered
	jobs := tenant.Scope("jobs")
	starts := &countingStarters{}
	provideCountedStarter(jobs, starts, "w")

	err := root.Refresh()

	close(release)
	is.NoError(err)
	is.Equal(0, starts.count("w"))
	is.True((<-shutdown).Succeed)
}

// flakyStarter fails its first Start with err and succeeds afterwards.
type flakyStarter struct {
	err   error
	calls atomic.Int32
}

func (s *flakyStarter) Start() error {
	if s.calls.Add(1) == 1 {
		return s.err
	}
	return nil
}

func TestRefresh_RetriesFailedStarter(t *testing.T) {
	t.Parallel()

	refresh := func(root *RootScope, _ *Scope) error { return root.Refresh() }
	scopeStart := func(_ *RootScope, scope *Scope) error { return scope.Start() }
	inRoot := func(root *RootScope) *Scope { return root.self }
	inTenant := func(root *RootScope) *Scope { return root.Scope("tenant") }

	tests := []struct {
		name       string
		scope      func(root *RootScope) *Scope // where "late" is registered
		run        func(root *RootScope, scope *Scope) error
		buildFails bool // the provider fails on its first call, not Start
		builds     int32
		starts     int32
	}{
		{name: "root.Refresh, Start fails", scope: inRoot, run: refresh, builds: 1, starts: 2},
		{name: "tenant.Start, Start fails", scope: inTenant, run: scopeStart, builds: 1, starts: 2},
		{name: "root.Refresh, build fails", scope: inRoot, run: refresh, buildFails: true, builds: 2, starts: 1},
		{name: "tenant.Start, build fails", scope: inTenant, run: scopeStart, buildFails: true, builds: 2, starts: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			errBoom := errors.New("boom")
			root := New()
			require.NoError(t, root.Start())
			scope := tt.scope(root)
			late := &flakyStarter{}
			if !tt.buildFails {
				late.err = errBoom
			}
			var builds atomic.Int32
			ProvideNamed(scope, "late", func(Injector) (*flakyStarter, error) {
				if builds.Add(1) == 1 && tt.buildFails {
					return nil, errBoom
				}
				return late, nil
			})

			is.ErrorIs(tt.run(root, scope), errBoom)
			is.NoError(tt.run(root, scope))
			is.Equal(tt.builds, builds.Load(), "builds")
			is.Equal(tt.starts, late.calls.Load(), "starts")

			is.NoError(tt.run(root, scope))
			is.Equal(tt.builds, builds.Load(), "builds")
			is.Equal(tt.starts, late.calls.Load(), "starts")
		})
	}
}

// callerCtxKey keys the value TestIncrementalStart_CallerContext puts in
// the caller's context.
type callerCtxKey struct{}

func TestIncrementalStart_CallerContext(t *testing.T) {
	t.Parallel()

	withValue := func() context.Context { return context.WithValue(context.Background(), callerCtxKey{}, "v") }
	cancelled := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	none := func() context.Context { return nil }

	tests := []struct {
		name     string
		scope    bool // the run is tenant.StartWithContext, not root.RefreshWithContext
		ctx      func() context.Context
		wantErr  error
		starts   int
		hasValue bool // the recorded contexts hold "v" for callerCtxKey
	}{
		{name: "Refresh, value", ctx: withValue, starts: 1, hasValue: true},
		{name: "Scope.Start, value", scope: true, ctx: withValue, starts: 1, hasValue: true},
		{name: "Refresh, cancelled", ctx: cancelled, wantErr: context.Canceled},
		{name: "Scope.Start, cancelled", scope: true, ctx: cancelled, wantErr: context.Canceled},
		{name: "Refresh, nil", ctx: none, starts: 1},
		{name: "Scope.Start, nil", scope: true, ctx: none, starts: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			var mu sync.Mutex
			var recorded []context.Context
			record := func(ctx context.Context) {
				mu.Lock()
				defer mu.Unlock()
				recorded = append(recorded, ctx)
			}
			root := New()
			root.AddScopeStartingHook(func(ctx context.Context, _ *Scope) error {
				record(ctx)
				return nil
			})
			require.NoError(t, root.Start())
			target := root.Scope("tenant")
			if !tt.scope {
				target = root.self
			}
			var starts atomic.Int32
			provideLazy(target, "a", &ctxStarter{fn: func(ctx context.Context) error {
				starts.Add(1)
				record(ctx)
				return nil
			}})

			var err error
			if tt.scope {
				err = target.StartWithContext(tt.ctx())
			} else {
				err = root.RefreshWithContext(tt.ctx())
			}

			if tt.wantErr != nil {
				is.ErrorIs(err, tt.wantErr)
			} else {
				is.NoError(err)
			}
			is.Equal(int32(tt.starts), starts.Load())
			wantRecorded := tt.starts
			if tt.scope {
				wantRecorded++ // the Starting hook, which runs before the abort check
			}
			mu.Lock()
			defer mu.Unlock()
			is.Len(recorded, wantRecorded)
			for _, ctx := range recorded {
				if !is.NotNil(ctx) {
					continue
				}
				if tt.hasValue {
					is.Equal("v", ctx.Value(callerCtxKey{}))
				}
			}
		})
	}
}
