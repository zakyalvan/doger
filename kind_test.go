package doger

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Kind fixtures. Job is a test interface, rendered as `doger.Job` in errors.
type (
	Job interface {
		Run(ctx context.Context) error
	}

	Spec struct{ Every time.Duration }

	// CleanupJob implements Job and kindLifecycle.
	CleanupJob struct{ name string }

	// kindPlain implements kindLifecycle but not Job.
	kindPlain struct{}

	// kindLifecycle is an interface not assignable to Job.
	kindLifecycle interface{ Name() string }
)

func (*CleanupJob) Run(context.Context) error { return nil }
func (j *CleanupJob) Name() string            { return j.name }
func (*kindPlain) Name() string               { return "plain" }

// componentRecorder records the components a handler receives.
type componentRecorder[T, M any] struct {
	mu         sync.Mutex
	components []Component[T, M]
}

func (r *componentRecorder[T, M]) handler(_ context.Context, c Component[T, M]) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = append(r.components, c)
	return nil
}

func (r *componentRecorder[T, M]) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.components))
	for _, c := range r.components {
		names = append(names, c.Name)
	}
	return names
}

func TestKind_OptIn(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	scheduled := NewKind[Job, Spec]("scheduled")
	root := New()
	ProvideNamed(root, "cleanup", func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil },
		scheduled.With(Spec{Every: time.Minute}))
	rec := &componentRecorder[Job, Spec]{}
	scheduled.Handle(root, StageBeforeStart, rec.handler)

	require.NoError(t, root.Start())

	instance, err := InvokeNamed[*CleanupJob](root, "cleanup")
	require.NoError(t, err)
	if is.Len(rec.components, 1) {
		c := rec.components[0]
		is.Equal("cleanup", c.Name)
		is.Equal("[root]", c.Scope.Name())
		is.Equal(Spec{Every: time.Minute}, c.Meta)
		is.Same(instance, c.Instance)
	}
	is.Equal("scheduled", scheduled.Name())
}

func TestKind_DistinctIdentity(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	k1 := NewKind[Job, Spec]("scheduled")
	k2 := NewKind[Job, Spec]("scheduled")
	root := New()
	ProvideNamed(root, "a", func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil }, k1.With(Spec{}))
	ProvideNamed(root, "b", func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil }, k2.With(Spec{}))
	rec1, rec2 := &componentRecorder[Job, Spec]{}, &componentRecorder[Job, Spec]{}
	k1.Handle(root, StageBeforeStart, rec1.handler)
	k2.Handle(root, StageBeforeStart, rec2.handler)

	require.NoError(t, root.Start())

	is.Equal([]string{"a"}, rec1.names())
	is.Equal([]string{"b"}, rec2.names())
}

func TestKind_InterfaceRegistration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		instance kindLifecycle
		errMsg   string
		received []string
	}{
		{name: "instance_implements_job", instance: &CleanupJob{}, received: []string{"life"}},
		{
			name:     "instance_without_run",
			instance: &kindPlain{},
			errMsg:   "DI: service `life` does not implement `doger.Job` required by kind `scheduled`",
			received: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			scheduled := NewKind[Job, Spec]("scheduled")
			root := New()
			ProvideNamed(root, "life", func(Injector) (kindLifecycle, error) { return tt.instance, nil }, scheduled.With(Spec{}))
			rec := &componentRecorder[Job, Spec]{}
			scheduled.Handle(root, StageBeforeStart, rec.handler)

			err := root.Start()

			if tt.errMsg == "" {
				is.NoError(err)
			} else {
				is.ErrorContains(err, tt.errMsg)
			}
			is.Equal(tt.received, rec.names())
		})
	}
}

func TestKind_InvalidValues(t *testing.T) {
	t.Parallel()

	h := func(context.Context, Component[Job, Spec]) error { return nil }

	tests := []struct {
		name    string
		call    func(root *RootScope)
		message string
	}{
		{
			name:    "zero_with",
			call:    func(*RootScope) { var zero Kind[Job, Spec]; zero.With(Spec{}) },
			message: "DI: kind not created with NewKind",
		},
		{
			name:    "zero_handle",
			call:    func(root *RootScope) { var zero Kind[Job, Spec]; zero.Handle(root, StageBeforeStart, h) },
			message: "DI: kind not created with NewKind",
		},
		{
			name:    "invalid_stage",
			call:    func(root *RootScope) { NewKind[Job, Spec]("k").Handle(root, Stage(7), h) },
			message: "DI: invalid stage 7 for kind `k`",
		},
		{
			name:    "nil_handler",
			call:    func(root *RootScope) { NewKind[Job, Spec]("k").Handle(root, StageBeforeStart, nil) },
			message: "DI: nil handler for kind `k`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			assert.PanicsWithError(t, tt.message, func() { tt.call(root) })
		})
	}

	var zero Kind[Job, Spec]
	assert.Empty(t, zero.Name())
}

// kindServer is a Starter recording its events.
type kindServer struct {
	rec *callRecorder
}

func (s *kindServer) Start() error {
	s.rec.record("start server")
	return nil
}

func TestKind_Stage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stage  Stage
		events []string
	}{
		{name: "before_start", stage: StageBeforeStart, events: []string{"build server", "build job", "handle job", "start server"}},
		{name: "after_start", stage: StageAfterStart, events: []string{"build server", "build job", "start server", "handle job"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			k := NewKind[Job, Spec]("k")
			root := New()
			events := &callRecorder{}
			ProvideNamed(root, "server", func(Injector) (*kindServer, error) {
				events.record("build server")
				return &kindServer{rec: events}, nil
			})
			ProvideNamed(root, "job", func(Injector) (*CleanupJob, error) {
				events.record("build job")
				return &CleanupJob{}, nil
			}, k.With(Spec{}))
			k.Handle(root, tt.stage, func(_ context.Context, c Component[Job, Spec]) error {
				events.record("handle " + c.Name)
				return nil
			})

			require.NoError(t, root.Start())
			assert.Equal(t, tt.events, events.list())
		})
	}
}

func TestKind_ReplaceHandler(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	k := NewKind[Job, Spec]("k")
	root := New()
	events := &callRecorder{}
	ProvideNamed(root, "server", func(Injector) (*kindServer, error) { return &kindServer{rec: events}, nil })
	ProvideNamed(root, "job", func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil }, k.With(Spec{}))
	k.Handle(root, StageBeforeStart, func(context.Context, Component[Job, Spec]) error {
		events.record("first")
		return nil
	})
	k.Handle(root, StageAfterStart, func(context.Context, Component[Job, Spec]) error {
		events.record("second")
		return nil
	})

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"start server", "second"}, events.list())
}

// TestKind_NilRootUsesDefault is not parallel: it replaces DefaultRootScope.
func TestKind_NilRootUsesDefault(t *testing.T) {
	testWithTimeout(t, time.Second)

	previous := DefaultRootScope
	DefaultRootScope = New()
	t.Cleanup(func() { DefaultRootScope = previous })

	k := NewKind[Job, Spec]("k")
	ProvideNamed(DefaultRootScope, "job", func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil }, k.With(Spec{}))
	rec := &componentRecorder[Job, Spec]{}
	k.Handle(nil, StageBeforeStart, rec.handler)

	require.NoError(t, DefaultRootScope.Start())
	assert.Equal(t, []string{"job"}, rec.names())
}

func TestKind_NoHandler(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	k := NewKind[Job, Spec]("k")
	root := New()
	built := false
	ProvideNamed(root, "job", func(Injector) (*CleanupJob, error) {
		built = true
		return &CleanupJob{}, nil
	}, k.With(Spec{}))

	assert.NoError(t, root.Start())
	assert.True(t, built)
}

func TestKind_ComponentOrder(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	k := NewKind[Job, Spec]("k")
	root := New()
	newJob := func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil }
	ProvideNamed(root, "c", newJob, k.With(Spec{}), DependsOnNamed("a"))
	ProvideNamed(root, "a", newJob, k.With(Spec{}))
	ProvideNamed(root.Scope("s"), "b", newJob, k.With(Spec{}))
	rec := &componentRecorder[Job, Spec]{}
	k.Handle(root, StageBeforeStart, rec.handler)

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"a", "c", "b"}, rec.names())
}

func TestKind_HandlerFailure(t *testing.T) {
	t.Parallel()
	errBoom := errors.New("boom")

	tests := []struct {
		name     string
		stage    Stage
		behavior func() error
		check    func(t *testing.T, err error)
		starts   int32
	}{
		{
			name: "before_start_error", stage: StageBeforeStart,
			behavior: func() error { return errBoom },
			check:    func(t *testing.T, err error) { assert.ErrorIs(t, err, errBoom) },
			starts:   0,
		},
		{
			name: "before_start_panic", stage: StageBeforeStart,
			behavior: func() error { panic("kaboom") },
			check:    func(t *testing.T, err error) { assert.ErrorContains(t, err, "kaboom") },
			starts:   0,
		},
		{
			name: "after_start_error", stage: StageAfterStart,
			behavior: func() error { return errBoom },
			check:    func(t *testing.T, err error) { assert.ErrorIs(t, err, errBoom) },
			starts:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			k := NewKind[Job, Spec]("k")
			root := New()
			server := provideCounted(root, "server", &startWithErr{})
			newJob := func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil }
			ProvideNamed(root, "a", newJob, k.With(Spec{}))
			ProvideNamed(root, "b", newJob, k.With(Spec{}))
			handled := &callRecorder{}
			k.Handle(root, tt.stage, func(_ context.Context, c Component[Job, Spec]) error {
				handled.record(c.Name)
				if c.Name == "a" {
					return tt.behavior()
				}
				return nil
			})

			err := root.Start()

			is.ErrorContains(err, "kind `k`")
			is.ErrorContains(err, "service `a`")
			tt.check(t, err)
			is.Equal([]string{"a"}, handled.list())
			is.Equal(tt.starts, server.starts.Load())
		})
	}
}

// eventService records its build, start and shutdown in rec.
type eventService struct {
	name string
	rec  *callRecorder
}

func (s *eventService) Start() error {
	s.rec.record("start " + s.name)
	return nil
}

func (s *eventService) Shutdown() error {
	s.rec.record("shutdown " + s.name)
	return nil
}

func (*eventService) Run(context.Context) error { return nil }

func TestKind_DeclareDependencyInHandler(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	scheduled := NewKind[Job, Spec]("scheduled")
	root := New()
	events := &callRecorder{}
	for _, name := range []string{"scheduler", "job"} {
		var opts []ProvideOption
		if name == "job" {
			opts = []ProvideOption{scheduled.With(Spec{Every: time.Minute})}
		}
		ProvideNamed(root, name, func(Injector) (*eventService, error) {
			events.record("build " + name)
			return &eventService{name: name, rec: events}, nil
		}, opts...)
	}
	scheduled.Handle(root, StageBeforeStart, func(_ context.Context, c Component[Job, Spec]) error {
		return DeclareDependency(c.Scope, "scheduler", c.Name)
	})

	require.NoError(t, root.Start())
	require.True(t, root.Shutdown().Succeed)

	is.Equal([]string{
		"build scheduler", "build job",
		"start job", "start scheduler", // the declared edge orders start
		"shutdown scheduler", "shutdown job",
	}, events.list())
}

// runRecorder is a Runner recording its runs in rec and returning err.
type runRecorder struct {
	name string
	rec  *callRecorder
	err  error
}

func (r *runRecorder) Run(context.Context) error {
	r.rec.record("run " + r.name)
	return r.err
}

// provideRunner registers lazy runner name in root.
func provideRunner(root *RootScope, rec *callRecorder, name string, err error, opts ...ProvideOption) {
	ProvideNamed(root, name, func(Injector) (*runRecorder, error) {
		return &runRecorder{name: name, rec: rec, err: err}, nil
	}, opts...)
}

func TestRunnable_RunsAfterStarters(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	events := &callRecorder{}
	ProvideNamed(root, "server", func(Injector) (*kindServer, error) { return &kindServer{rec: events}, nil })
	provideRunner(root, events, "migrate", nil, AsRunnable())
	provideRunner(root, events, "warmup", nil, AsRunnable())

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"start server", "run migrate", "run warmup"}, events.list())
}

func TestRunnable_Failure(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)
	errMigrate := errors.New("migrate failed")

	root := New()
	events := &callRecorder{}
	provideRunner(root, events, "a", errMigrate, AsRunnable())
	provideRunner(root, events, "b", nil, AsRunnable())

	err := root.Start()

	is.ErrorIs(err, errMigrate)
	is.ErrorContains(err, "kind `runnable`")
	is.ErrorContains(err, "service `a`")
	is.Equal([]string{"run a"}, events.list())
}

func TestRunnable_AutoDetection(t *testing.T) {
	t.Parallel()

	newJob := func(rec *callRecorder) func(Injector) (*runRecorder, error) {
		return func(Injector) (*runRecorder, error) { return &runRecorder{name: "job", rec: rec}, nil }
	}

	tests := []struct {
		name     string
		enabled  bool
		register func(t *testing.T, root *RootScope, rec *callRecorder)
		runs     int
	}{
		{
			name: "disabled_no_options", enabled: false,
			register: func(_ *testing.T, root *RootScope, rec *callRecorder) { ProvideNamed(root, "job", newJob(rec)) },
			runs:     0,
		},
		{
			name: "enabled_no_options", enabled: true,
			register: func(_ *testing.T, root *RootScope, rec *callRecorder) { ProvideNamed(root, "job", newJob(rec)) },
			runs:     1,
		},
		{
			name: "enabled_with_as_runnable", enabled: true,
			register: func(_ *testing.T, root *RootScope, rec *callRecorder) {
				ProvideNamed(root, "job", newJob(rec), AsRunnable())
			},
			runs: 1,
		},
		{
			name: "enabled_transient", enabled: true,
			register: func(_ *testing.T, root *RootScope, rec *callRecorder) {
				ProvideNamedTransient(root, "job", newJob(rec))
			},
			runs: 0,
		},
		{
			name: "enabled_with_alias", enabled: true,
			register: func(t *testing.T, root *RootScope, rec *callRecorder) {
				Provide(root, newJob(rec))
				require.NoError(t, As[*runRecorder, Runner](root))
			},
			runs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := NewWithOpts(&InjectorOpts{EnableRunnerAutoDetection: tt.enabled})
			rec := &callRecorder{}
			tt.register(t, root, rec)

			require.NoError(t, root.Start())
			assert.Len(t, rec.list(), tt.runs)
		})
	}
}

func TestRunnable_ReplaceHandler(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	events := &callRecorder{}
	release := make(chan struct{})
	finished := make(chan struct{})
	ProvideNamed(root, "a", func(Injector) (*runRecorder, error) {
		return &runRecorder{name: "a", rec: events}, nil
	}, AsRunnable())

	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	Runnable.Handle(root, StageAfterStart, func(_ context.Context, c Component[Runner, struct{}]) error {
		wg.Go(func() {
			defer close(finished)
			<-release
			_ = c.Instance.Run(context.Background())
		})
		return nil
	})

	require.NoError(t, root.Start())

	select {
	case <-finished:
		is.Fail("Start returned after the runner finished")
	default:
	}
	is.Empty(events.list(), "the default handler did not run")

	close(release)
	<-finished
	is.Equal([]string{"run a"}, events.list())
}

// TestRunnable_FirstSlot pins that the Runnable handler, default or
// replaced, runs before the handlers of kinds handled later in the same stage.
func TestRunnable_FirstSlot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		replace  bool
		expected []string
	}{
		{name: "default handler", replace: false, expected: []string{"run a", "other b"}},
		{name: "handler replaced after another kind", replace: true, expected: []string{"replaced a", "other b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			events := &callRecorder{}
			other := NewKind[Runner, struct{}]("other")
			provideRunner(root, events, "a", nil, AsRunnable())
			provideRunner(root, events, "b", nil, other.With(struct{}{}))

			other.Handle(root, StageAfterStart, func(_ context.Context, c Component[Runner, struct{}]) error {
				events.record("other " + c.Name)
				return nil
			})
			if tt.replace {
				Runnable.Handle(root, StageAfterStart, func(_ context.Context, c Component[Runner, struct{}]) error {
					events.record("replaced " + c.Name)
					return nil
				})
			}

			require.NoError(t, root.Start())
			assert.Equal(t, tt.expected, events.list())
		})
	}
}

// TestRunnable_ReassignedVariable is not parallel: it reassigns Runnable.
func TestRunnable_ReassignedVariable(t *testing.T) {
	testWithTimeout(t, time.Second)

	previous := Runnable
	Runnable = NewKind[Runner, struct{}]("other")
	t.Cleanup(func() { Runnable = previous })

	root := New()
	events := &callRecorder{}
	provideRunner(root, events, "job", nil, AsRunnable())

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"run job"}, events.list())
}

func TestKind_CloneCopiesHandlers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		clone func(*RootScope) *RootScope
	}{
		{name: "Clone()", clone: (*RootScope).Clone},
		{name: "CloneWithOpts(&InjectorOpts{})", clone: func(root *RootScope) *RootScope { return root.CloneWithOpts(&InjectorOpts{}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			events := &callRecorder{}
			k := NewKind[Job, Spec]("k")
			ProvideNamed(root, "job", func(Injector) (*CleanupJob, error) { return &CleanupJob{name: "job"}, nil }, k.With(Spec{}))
			provideRunner(root, events, "migrate", nil, AsRunnable())
			k.Handle(root, StageBeforeStart, func(_ context.Context, c Component[Job, Spec]) error {
				events.record("k " + c.Name)
				return nil
			})
			Runnable.Handle(root, StageAfterStart, func(_ context.Context, c Component[Runner, struct{}]) error {
				events.record("custom " + c.Name)
				return nil
			})

			clone := tt.clone(root)

			require.NoError(t, clone.Start())
			assert.Equal(t, []string{"k job", "custom migrate"}, events.list())
		})
	}
}

func TestKind_CloneReplaceIsolated(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	events := &callRecorder{}
	k := NewKind[Job, Spec]("k")
	ProvideNamed(root, "job", func(Injector) (*CleanupJob, error) { return &CleanupJob{name: "job"}, nil }, k.With(Spec{}))
	k.Handle(root, StageBeforeStart, func(context.Context, Component[Job, Spec]) error {
		events.record("source")
		return nil
	})

	clone := root.Clone()
	k.Handle(clone, StageBeforeStart, func(context.Context, Component[Job, Spec]) error {
		events.record("clone")
		return nil
	})

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"source"}, events.list())
}
