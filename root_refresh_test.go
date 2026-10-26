package doger

import (
	"context"
	"errors"
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
			ProvideNamedValue(root, "a", &ctxStarter{fn: func(context.Context) error {
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
	require.NoError(t, root.Start())
	tenant := root.Scope("tenant")
	starts := &countingStarters{}
	provideCountedStarter(tenant, starts, "w")
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
	is.True((<-shutdown).Succeed)
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
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		begin := time.Now()

		report := tenant.ShutdownWithContext(ctx)

		is.Equal(time.Second, time.Since(begin))
		is.False(report.Succeed)
		is.ErrorIs(report.Errors[newServiceDescription(tenant.ID(), tenant.Name(), "")], context.DeadlineExceeded)
		close(w.release)
		is.NoError(<-runErr)
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
				ProvideNamedValue(tenant, "b", &errorStarter{err: errBoom})
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
