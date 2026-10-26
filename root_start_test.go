package doger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startCounter counts Start and Shutdown calls on one instance.
type startCounter struct {
	starts    atomic.Int32
	shutdowns atomic.Int32
}

func (c *startCounter) Shutdown() error {
	c.shutdowns.Add(1)
	return nil
}

// One fixture per Starter variant, plus one without Start.
type (
	startPlain      struct{ startCounter }
	startWithErr    struct{ startCounter }
	startWithCtx    struct{ startCounter }
	startWithCtxErr struct{ startCounter }
	startNone       struct{ startCounter }

	// startLifecycle is an interface without Start.
	startLifecycle interface{ Shutdown() error }
	// startStartable is an interface with Start.
	startStartable interface{ Start() error }
)

func (s *startPlain) Start()                           { s.starts.Add(1) }
func (s *startWithErr) Start() error                   { s.starts.Add(1); return nil }
func (s *startWithCtx) Start(context.Context)          { s.starts.Add(1) }
func (s *startWithCtxErr) Start(context.Context) error { s.starts.Add(1); return nil }
func (s *startPlain) counter() *startCounter           { return &s.startCounter }
func (s *startWithErr) counter() *startCounter         { return &s.startCounter }
func (s *startWithCtx) counter() *startCounter         { return &s.startCounter }
func (s *startWithCtxErr) counter() *startCounter      { return &s.startCounter }
func (s *startNone) counter() *startCounter            { return &s.startCounter }

// provideCounted registers lazy name of type T and returns the instance it builds.
func provideCounted[T any](scope Injector, name string, instance T, opts ...ProvideOption) T {
	ProvideNamed(scope, name, func(Injector) (T, error) { return instance, nil }, opts...)
	return instance
}

// recordingStarter records its name in rec when started.
type recordingStarter struct {
	name string
	rec  *callRecorder
}

func (s *recordingStarter) Start() error {
	s.rec.record(s.name)
	return nil
}

// provideRecordingStarter registers lazy Starter name in scope.
func provideRecordingStarter(scope Injector, rec *callRecorder, name string, opts ...ProvideOption) {
	ProvideNamed(scope, name, func(Injector) (*recordingStarter, error) {
		return &recordingStarter{name: name, rec: rec}, nil
	}, opts...)
}

func TestStart_Variants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(root *RootScope) *startCounter
	}{
		{name: "start", register: func(root *RootScope) *startCounter { return provideCounted(root, "s1", &startPlain{}).counter() }},
		{name: "start_error", register: func(root *RootScope) *startCounter { return provideCounted(root, "s2", &startWithErr{}).counter() }},
		{name: "start_context", register: func(root *RootScope) *startCounter { return provideCounted(root, "s3", &startWithCtx{}).counter() }},
		{name: "start_context_error", register: func(root *RootScope) *startCounter {
			return provideCounted(root, "s4", &startWithCtxErr{}).counter()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			counter := tt.register(root)

			assert.NoError(t, root.StartWithContext(t.Context()))
			assert.Equal(t, int32(1), counter.starts.Load())
		})
	}
}

func TestStart_Detection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		register  func(t *testing.T, root *RootScope) *startCounter
		starts    int32
		shutdowns int32
	}{
		{
			name: "lazy_type_has_start_ctx_error",
			register: func(_ *testing.T, root *RootScope) *startCounter {
				return provideCounted(root, "srv", &startWithCtxErr{}).counter()
			},
			starts: 1, shutdowns: 1,
		},
		{
			name: "eager_value_type_has_start_error",
			register: func(_ *testing.T, root *RootScope) *startCounter {
				instance := &startWithErr{}
				ProvideNamedValue(root, "srv", instance)
				return instance.counter()
			},
			starts: 1, shutdowns: 1,
		},
		{
			name: "interface_without_start",
			register: func(_ *testing.T, root *RootScope) *startCounter {
				instance := &startPlain{}
				provideCounted[startLifecycle](root, "life", instance)
				return instance.counter()
			},
			starts: 0, shutdowns: 0,
		},
		{
			name: "interface_without_start_as_starter",
			register: func(_ *testing.T, root *RootScope) *startCounter {
				instance := &startPlain{}
				provideCounted[startLifecycle](root, "life", instance, AsStarter())
				return instance.counter()
			},
			starts: 1, shutdowns: 1,
		},
		{
			name: "interface_without_start_invoked_before_start",
			register: func(t *testing.T, root *RootScope) *startCounter {
				instance := &startPlain{}
				provideCounted[startLifecycle](root, "life", instance)
				_, err := InvokeNamed[startLifecycle](root, "life")
				require.NoError(t, err)
				return instance.counter()
			},
			starts: 0, shutdowns: 1,
		},
		{
			name: "lazy_with_alias",
			register: func(t *testing.T, root *RootScope) *startCounter {
				instance := &startWithErr{}
				Provide(root, func(Injector) (*startWithErr, error) { return instance, nil })
				require.NoError(t, As[*startWithErr, startStartable](root))
				return instance.counter()
			},
			starts: 1, shutdowns: 1,
		},
		{
			name: "lazy_without_start",
			register: func(_ *testing.T, root *RootScope) *startCounter {
				return provideCounted(root, "cfg", &startNone{}).counter()
			},
			starts: 0, shutdowns: 0,
		},
		{
			name: "transient_with_start",
			register: func(_ *testing.T, root *RootScope) *startCounter {
				instance := &startWithErr{}
				ProvideNamedTransient(root, "srv", func(Injector) (*startWithErr, error) { return instance, nil })
				return instance.counter()
			},
			starts: 0, shutdowns: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			counter := tt.register(t, root)

			is.NoError(root.Start())
			is.True(root.Shutdown().Succeed)

			is.Equal(tt.starts, counter.starts.Load(), "starts")
			is.Equal(tt.shutdowns, counter.shutdowns.Load(), "shutdowns")
		})
	}
}

func TestStart_DisableAutoDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		disabled bool
		started  []string
	}{
		{name: "enabled", disabled: false, started: []string{"a", "b"}},
		{name: "disabled", disabled: true, started: []string{"b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := NewWithOpts(&InjectorOpts{DisableStarterAutoDetection: tt.disabled})
			rec := &callRecorder{}
			provideRecordingStarter(root, rec, "a")
			provideRecordingStarter(root, rec, "b", AsStarter())

			require.NoError(t, root.Start())
			assert.Equal(t, tt.started, rec.list())
		})
	}
}

func TestStart_Empty(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	assert.NoError(t, New().Start())
}

func TestStart_OneShot(t *testing.T) {
	t.Parallel()
	errBuild := errors.New("build failed")

	tests := []struct {
		name      string
		failBuild bool
		previous  func(root *RootScope)
		result    error
		calls     int32
	}{
		{name: "called_nothing", previous: func(*RootScope) {}, calls: 1},
		{name: "called_start", previous: func(root *RootScope) { _ = root.Start() }, result: ErrAlreadyStarted, calls: 1},
		{name: "called_failed_start", failBuild: true, previous: func(root *RootScope) { _ = root.Start() }, result: ErrAlreadyStarted, calls: 0},
		{name: "called_shutdown", previous: func(root *RootScope) { root.Shutdown() }, result: ErrStartAfterShutdown, calls: 0},
		{
			name: "called_start_then_shutdown",
			previous: func(root *RootScope) {
				_ = root.Start()
				root.Shutdown()
			},
			result: ErrStartAfterShutdown, calls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			instance := &startWithErr{}
			ProvideNamed(root, "a", func(Injector) (*startWithErr, error) {
				if tt.failBuild {
					return nil, errBuild
				}
				return instance, nil
			})

			tt.previous(root)
			err := root.Start()

			if tt.result == nil {
				is.NoError(err)
			} else {
				is.ErrorIs(err, tt.result)
			}
			is.Equal(tt.calls, instance.starts.Load())
		})
	}
}

// instanceCounter counts Start calls per instance.
type instanceCounter struct {
	mu     sync.Mutex
	starts map[*cloneStarter]int
}

type cloneStarter struct {
	counter *instanceCounter
}

func (s *cloneStarter) Start() error {
	s.counter.mu.Lock()
	defer s.counter.mu.Unlock()
	s.counter.starts[s]++
	return nil
}

func TestStart_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(root *RootScope, counter *instanceCounter)
		calls    []int // start calls per distinct instance, sorted
	}{
		{
			name: "lazy",
			register: func(root *RootScope, counter *instanceCounter) {
				ProvideNamed(root, "a", func(Injector) (*cloneStarter, error) { return &cloneStarter{counter: counter}, nil })
			},
			calls: []int{1, 1},
		},
		{
			name: "eager_value",
			register: func(root *RootScope, counter *instanceCounter) {
				ProvideNamedValue(root, "a", &cloneStarter{counter: counter})
			},
			calls: []int{2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			counter := &instanceCounter{starts: map[*cloneStarter]int{}}
			tt.register(root, counter)

			require.NoError(t, root.Start())
			is.NoError(root.Clone().Start())

			counter.mu.Lock()
			defer counter.mu.Unlock()
			calls := make([]int, 0, len(counter.starts))
			for _, n := range counter.starts {
				calls = append(calls, n)
			}
			slices.Sort(calls)
			is.Equal(tt.calls, calls)
		})
	}
}

func TestStart_LateRegistrationNotStarted(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	require.NoError(t, root.Start())

	instance := provideCounted(root, "late", &startWithErr{})
	_, err := InvokeNamed[*startWithErr](root, "late")

	is.NoError(err)
	is.Equal(int32(0), instance.starts.Load())
}

func TestStart_DependencyOrder(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	events := &callRecorder{}
	starter := func(name string, deps ...string) {
		ProvideNamed(root, name, func(i Injector) (*recordingStarter, error) {
			for _, dep := range deps {
				if _, err := invokeAnyByName(i, dep); err != nil {
					return nil, err
				}
			}
			events.record("build " + name)
			return &recordingStarter{name: "start " + name, rec: events}, nil
		})
	}
	starter("http", "db")
	starter("db")
	ProvideNamed(root, "repo", func(i Injector) (*depValue, error) {
		_, err := InvokeNamed[*recordingStarter](i, "db")
		events.record("build repo")
		return &depValue{name: "repo"}, err
	})
	starter("consumer", "repo")

	require.NoError(t, root.Start())

	recorded := events.list()
	firstStart := slices.IndexFunc(recorded, func(e string) bool { return len(e) > 6 && e[:6] == "start " })
	require.GreaterOrEqual(t, firstStart, 0)
	for _, e := range recorded[firstStart:] {
		is.NotContains(e, "build", "every provider call happens before the first start call")
	}
	is.Contains(recorded, "build repo")
	is.Equal([]string{"start db", "start http", "start consumer"}, recorded[firstStart:])
}

func TestStart_ScopeTraversal(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	rec := &callRecorder{}
	tenant := root.Scope("tenant-a")
	jobs := tenant.Scope("jobs")

	provideRecordingStarter(jobs, rec, "cron")
	ProvideNamed(tenant, "worker", func(i Injector) (*recordingStarter, error) {
		_, err := InvokeNamed[*recordingStarter](i, "db")
		return &recordingStarter{name: "worker", rec: rec}, err
	})
	provideRecordingStarter(root, rec, "db")

	require.NoError(t, root.Start())
	assert.Equal(t, []string{"db", "worker", "cron"}, rec.list())
}

// tieBreakOrder registers "z" in "request", then "b" and "a" in root, starts
// the container and returns the start order.
func tieBreakOrder(t *testing.T) []string {
	t.Helper()

	root := New()
	rec := &callRecorder{}
	request := root.Scope("request")
	provideRecordingStarter(request, rec, "z")
	provideRecordingStarter(root, rec, "b")
	provideRecordingStarter(root, rec, "a")

	require.NoError(t, root.Start())

	return rec.list()
}

func TestStart_TieBreak(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	assert.Equal(t, []string{"b", "a", "z"}, tieBreakOrder(t))
}

func TestStart_TieBreak_Stable(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	for n := range 100 {
		assert.Equalf(t, []string{"b", "a", "z"}, tieBreakOrder(t), "run %d", n)
	}
}

func TestStart_RegistrationOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(root *RootScope, request *Scope, rec *callRecorder) *RootScope
		order     []string
	}{
		{
			name:      "nothing_else",
			operation: func(root *RootScope, _ *Scope, _ *callRecorder) *RootScope { return root },
			order:     []string{"a", "c", "b"},
		},
		{
			name: "override_in_root",
			operation: func(root *RootScope, _ *Scope, rec *callRecorder) *RootScope {
				OverrideNamed(root, "a", func(Injector) (*recordingStarter, error) {
					return &recordingStarter{name: "a", rec: rec}, nil
				})
				return root
			},
			order: []string{"a", "c", "b"},
		},
		{
			name: "override_in_request",
			operation: func(root *RootScope, request *Scope, rec *callRecorder) *RootScope {
				OverrideNamed(request, "a", func(Injector) (*recordingStarter, error) {
					return &recordingStarter{name: "request/a", rec: rec}, nil
				})
				return root
			},
			order: []string{"a", "c", "b", "request/a"},
		},
		{
			name: "clone_then_register",
			operation: func(root *RootScope, _ *Scope, rec *callRecorder) *RootScope {
				clone := root.Clone()
				provideRecordingStarter(clone, rec, "d")
				return clone
			},
			order: []string{"a", "c", "d", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			rec := &callRecorder{}
			request := root.Scope("request")
			provideRecordingStarter(root, rec, "a")
			provideRecordingStarter(request, rec, "b")
			provideRecordingStarter(root, rec, "c")

			container := tt.operation(root, request, rec)

			require.NoError(t, container.Start())
			assert.Equal(t, tt.order, rec.list())
		})
	}
}

func TestStart_RejectsDeclaredCycle(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	rec := &callRecorder{}
	provideRecordingStarter(root, rec, "scheduler")
	ProvideNamed(root, "job", func(i Injector) (*recordingStarter, error) {
		_, err := InvokeNamed[*recordingStarter](i, "scheduler")
		return &recordingStarter{name: "job", rec: rec}, err
	})
	require.NoError(t, DeclareDependency(root, "scheduler", "job"))

	err := root.Start()

	is.ErrorIs(err, ErrCircularDependency)
	is.ErrorContains(err, "`scheduler`")
	is.ErrorContains(err, "`job`")
	is.Empty(rec.list())
}

// scriptedStarter records its start in rec, then panics with panicValue (if
// set) or returns err.
type scriptedStarter struct {
	name       string
	rec        *callRecorder
	err        error
	panicValue any
}

func (s *scriptedStarter) Start(context.Context) error {
	if s.panicValue != nil {
		panic(s.panicValue)
	}
	if s.err != nil {
		return s.err
	}
	s.rec.record(s.name)
	return nil
}

func (s *scriptedStarter) Shutdown() error {
	s.rec.record("shutdown " + s.name)
	return nil
}

// errorStarter has Start() error.
type errorStarter struct{ err error }

func (s *errorStarter) Start() error { return s.err }

func TestStart_StarterFailure(t *testing.T) {
	t.Parallel()
	errBoom := errors.New("boom")

	tests := []struct {
		name    string
		provide func(root *RootScope)
		check   func(t *testing.T, err error)
	}{
		{
			name: "start_returns_error",
			provide: func(root *RootScope) {
				ProvideNamed(root, "b", func(Injector) (*errorStarter, error) { return &errorStarter{err: errBoom}, nil })
			},
			check: func(t *testing.T, err error) { assert.ErrorIs(t, err, errBoom) },
		},
		{
			name: "start_panics_with_string",
			provide: func(root *RootScope) {
				ProvideNamed(root, "b", func(Injector) (*scriptedStarter, error) { return &scriptedStarter{panicValue: "kaboom"}, nil })
			},
			check: func(t *testing.T, err error) { assert.ErrorContains(t, err, "kaboom") },
		},
		{
			name: "start_panics_with_error",
			provide: func(root *RootScope) {
				ProvideNamed(root, "b", func(Injector) (*scriptedStarter, error) { return &scriptedStarter{panicValue: errBoom}, nil })
			},
			check: func(t *testing.T, err error) { assert.ErrorIs(t, err, errBoom) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			rec := &callRecorder{}
			builds := &callRecorder{}
			ProvideNamed(root, "a", func(Injector) (*scriptedStarter, error) {
				builds.record("a")
				return &scriptedStarter{name: "a", rec: rec}, nil
			})
			tt.provide(root)
			ProvideNamed(root, "c", func(Injector) (*scriptedStarter, error) {
				builds.record("c")
				return &scriptedStarter{name: "c", rec: rec}, nil
			})

			err := root.Start()

			is.ErrorContains(err, "service `b`")
			tt.check(t, err)
			is.Equal([]string{"a"}, rec.list())
			is.Equal([]string{"a", "c"}, builds.list())
		})
	}
}

func TestStart_BuildFailure(t *testing.T) {
	t.Parallel()
	errBuild := errors.New("build failed")

	tests := []struct {
		name  string
		opts  []ProvideOption
		fail  bool
		cause error
	}{
		{name: "provider_error", fail: true, cause: errBuild},
		{name: "missing_declared_dependency", opts: []ProvideOption{DependsOnNamed("missing")}, cause: ErrServiceNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			rec := &callRecorder{}
			builds := &callRecorder{}
			for _, name := range []string{"a", "b", "c"} {
				var opts []ProvideOption
				if name == "b" {
					opts = tt.opts
				}
				ProvideNamed(root, name, func(Injector) (*scriptedStarter, error) {
					if name == "b" && tt.fail {
						return nil, errBuild
					}
					builds.record(name)
					return &scriptedStarter{name: name, rec: rec}, nil
				}, opts...)
			}

			err := root.Start()

			is.ErrorContains(err, "service `b`")
			is.ErrorIs(err, tt.cause)
			is.Equal([]string{"a"}, builds.list())
			is.Empty(rec.list())
		})
	}
}

func TestStart_AsStarterInstanceWithoutStart(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	ProvideNamed(root, "app.Lifecycle", func(Injector) (startLifecycle, error) { return &startNone{}, nil }, AsStarter())

	err := root.Start()

	assert.ErrorContains(t, err, "DI: service `app.Lifecycle` was opted in with AsStarter but does not implement any Starter interface")
}

func TestStart_ShutdownAfterFailure(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)
	errBoom := errors.New("boom")

	root := New()
	rec := &callRecorder{}
	ProvideNamed(root, "a", func(Injector) (*scriptedStarter, error) { return &scriptedStarter{name: "a", rec: rec}, nil })
	ProvideNamed(root, "b", func(Injector) (*scriptedStarter, error) {
		return &scriptedStarter{name: "b", rec: rec, err: errBoom}, nil
	})
	ProvideNamed(root, "c", func(Injector) (*scriptedStarter, error) { return &scriptedStarter{name: "c", rec: rec}, nil })

	require.Error(t, root.Start())
	root.Shutdown()

	is.ElementsMatch([]string{"a", "shutdown a", "shutdown b", "shutdown c"}, rec.list())
}

// ctxStarter runs fn with the context its Start(ctx) error receives.
type ctxStarter struct {
	fn func(ctx context.Context) error
}

func (s *ctxStarter) Start(ctx context.Context) error { return s.fn(ctx) }

func TestStart_Context(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		ctx   func(ctx context.Context) (context.Context, context.CancelFunc)
		a     func(ctx context.Context, cancel context.CancelFunc) error
		cause error
	}{
		{
			name: "already_cancelled",
			ctx: func(ctx context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return ctx, cancel
			},
			a:     func(context.Context, context.CancelFunc) error { return nil },
			cause: context.Canceled,
		},
		{
			name: "deadline_while_a_blocks",
			ctx: func(ctx context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeout(ctx, 5*time.Second)
			},
			a: func(ctx context.Context, _ context.CancelFunc) error {
				<-ctx.Done()
				return ctx.Err()
			},
			cause: context.DeadlineExceeded,
		},
		{
			name: "cancelled_while_a_runs",
			ctx:  context.WithCancel,
			a: func(_ context.Context, cancel context.CancelFunc) error {
				cancel()
				return nil
			},
			cause: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				root := New()
				rec := &callRecorder{}
				ctx, cancel := tt.ctx(t.Context())
				defer cancel()

				ProvideNamed(root, "a", func(Injector) (*ctxStarter, error) {
					return &ctxStarter{fn: func(ctx context.Context) error { return tt.a(ctx, cancel) }}, nil
				})
				provideRecordingStarter(root, rec, "b")

				err := root.StartWithContext(ctx)

				assert.ErrorIs(t, err, tt.cause)
				assert.Empty(t, rec.list(), "b was not started")
			})
		})
	}
}

func TestStart_NilContext(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	var received context.Context
	ProvideNamed(root, "a", func(Injector) (*ctxStarter, error) {
		return &ctxStarter{fn: func(ctx context.Context) error {
			received = ctx
			return nil
		}}, nil
	})

	var err error
	assert.NotPanics(t, func() {
		err = root.StartWithContext(nil) //nolint:staticcheck // nil context is the case under test
	})
	assert.NoError(t, err)
	assert.NotNil(t, received)
}

func TestStart_ContextEndsWithStart(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	var kept context.Context
	ProvideNamed(root, "a", func(Injector) (*ctxStarter, error) {
		return &ctxStarter{fn: func(ctx context.Context) error {
			kept = ctx
			return nil
		}}, nil
	})

	require.NoError(t, root.Start())
	require.NotNil(t, kept)
	assert.ErrorIs(t, kept.Err(), context.Canceled)
}

// blockingStarter signals entered when Start begins, then runs behavior.
type blockingStarter struct {
	entered   chan struct{}
	behavior  func(ctx context.Context) error
	shutdowns atomic.Int32
}

func (s *blockingStarter) Start(ctx context.Context) error {
	close(s.entered)
	return s.behavior(ctx)
}

func (s *blockingStarter) Shutdown() error {
	s.shutdowns.Add(1)
	return nil
}

// blockingRunner signals entered when Run begins, then runs behavior.
type blockingRunner struct {
	entered   chan struct{}
	behavior  func(ctx context.Context) error
	shutdowns atomic.Int32
}

func (r *blockingRunner) Run(ctx context.Context) error {
	close(r.entered)
	return r.behavior(ctx)
}

func (r *blockingRunner) Shutdown() error {
	r.shutdowns.Add(1)
	return nil
}

// countingRunner counts its Run calls.
type countingRunner struct {
	runs atomic.Int32
}

func (r *countingRunner) Run(context.Context) error {
	r.runs.Add(1)
	return nil
}

func TestStart_ShutdownCancels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// behavior of a's Start; release is closed by the test as the row says
		behavior func(release <-chan struct{}) func(ctx context.Context) error
		// releaseWhileShutdownBlocked releases "a" once Shutdown is blocked
		releaseWhileShutdownBlocked bool
		shutdownTimeout             time.Duration // 0: context.Background()
		wantSucceed                 bool
		wantAShutdowns              int32
		runner                      bool // "a" and "z" are lazy Runners instead of Starters
	}{
		{
			name: "runner_honours_ctx",
			behavior: func(<-chan struct{}) func(ctx context.Context) error {
				return func(ctx context.Context) error {
					<-ctx.Done()
					return ctx.Err()
				}
			},
			runner:      true,
			wantSucceed: true, wantAShutdowns: 1,
		},
		{
			name: "starter_honours_ctx",
			behavior: func(<-chan struct{}) func(ctx context.Context) error {
				return func(ctx context.Context) error {
					<-ctx.Done()
					return ctx.Err()
				}
			},
			wantSucceed: true, wantAShutdowns: 1,
		},
		{
			name: "starter_ignores_ctx_released_while_shutdown_waits",
			behavior: func(release <-chan struct{}) func(ctx context.Context) error {
				return func(context.Context) error {
					<-release
					return nil
				}
			},
			releaseWhileShutdownBlocked: true,
			wantSucceed:                 true, wantAShutdowns: 1,
		},
		{
			name: "starter_ignores_ctx_shutdown_deadline",
			behavior: func(release <-chan struct{}) func(ctx context.Context) error {
				return func(context.Context) error {
					<-release
					return nil
				}
			},
			shutdownTimeout: time.Second,
			wantSucceed:     false, wantAShutdowns: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				is := assert.New(t)

				root := New()
				release := make(chan struct{})
				entered := make(chan struct{})
				var aShutdowns, zCalls func() int32
				if tt.runner {
					a := &blockingRunner{entered: entered, behavior: tt.behavior(release)}
					ProvideNamed(root, "a", func(Injector) (*blockingRunner, error) { return a, nil }, AsRunnable())
					z := &countingRunner{}
					ProvideNamed(root, "z", func(Injector) (*countingRunner, error) { return z, nil }, AsRunnable())
					aShutdowns, zCalls = a.shutdowns.Load, z.runs.Load
				} else {
					a := &blockingStarter{entered: entered, behavior: tt.behavior(release)}
					ProvideNamed(root, "a", func(Injector) (*blockingStarter, error) { return a, nil })
					z := provideCounted(root, "z", &startWithCtxErr{})
					aShutdowns, zCalls = a.shutdowns.Load, z.starts.Load
				}

				started := make(chan error, 1)
				go func() { started <- root.Start() }()
				<-entered

				helperDone := make(chan struct{})
				if tt.releaseWhileShutdownBlocked {
					go func() {
						defer close(helperDone)
						synctest.Wait() // Shutdown is blocked waiting for Start
						close(release)
					}()
				} else {
					close(helperDone)
				}

				ctx := context.Background()
				if tt.shutdownTimeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tt.shutdownTimeout)
					defer cancel()
				}

				begin := time.Now()
				report := root.ShutdownWithContext(ctx)
				elapsed := time.Since(begin)

				if tt.shutdownTimeout > 0 {
					is.Equal(tt.shutdownTimeout, elapsed, "returns at the deadline")
					select {
					case <-started:
						is.Fail("Start returned before Shutdown stopped waiting")
					default:
					}
					is.False(report.Succeed)
					is.Contains(report.Errors, rootServiceDescription(root))
					for desc, err := range report.Errors {
						is.ErrorIsf(err, context.DeadlineExceeded, "error of %v", desc)
					}
					close(release)
				} else {
					<-helperDone // only one goroutine may call synctest.Wait at a time
					synctest.Wait()
					is.Len(started, 1, "Start returned before Shutdown")
					is.True(report.Succeed)
				}

				is.ErrorIs(<-started, ErrStartAfterShutdown)
				is.Equal(tt.wantAShutdowns, aShutdowns())
				is.Equal(int32(0), zCalls())
			})
		})
	}
}

func TestStart_PanicReleasesShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := NewWithOpts(&InjectorOpts{
		HookBeforeInvocation: []func(*Scope, string){
			func(*Scope, string) { panic("hook-boom") },
		},
	})
	provideCounted(root, "a", &startWithErr{})

	is.PanicsWithValue("hook-boom", func() { _ = root.Start() })

	is.True(root.Shutdown().Succeed)
	is.ErrorIs(root.Start(), ErrStartAfterShutdown)
}

func TestStart_Concurrent(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	root := New()
	instance := provideCounted(root, "a", &startWithErr{})

	const callers = 10
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for n := range callers {
		wg.Go(func() {
			<-start
			errs[n] = root.Start()
		})
	}
	close(start)
	wg.Wait()

	succeeded, already := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrAlreadyStarted):
			already++
		}
	}
	is.Equal(1, succeeded)
	is.Equal(callers-1, already)
	is.Equal(int32(1), instance.starts.Load())
}

func TestStart_ConcurrentInvoke(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	root := New()
	var builds atomic.Int32
	ProvideNamed(root, "a", func(Injector) (*startWithErr, error) {
		builds.Add(1)
		return &startWithErr{}, nil
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		is.NoError(root.Start())
	})
	for range 50 {
		wg.Go(func() {
			<-start
			_, err := InvokeNamed[*startWithErr](root, "a")
			is.NoError(err)
		})
	}
	close(start)
	wg.Wait()

	is.Equal(int32(1), builds.Load())
}

func TestStart_Hooks(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)
	errBoom := errors.New("boom")

	rec := &callRecorder{}
	var errB error
	root := NewWithOpts(&InjectorOpts{
		HookBeforeStart: []func(*Scope, string){
			func(_ *Scope, name string) { rec.record("before " + name) },
		},
		HookAfterStart: []func(*Scope, string, error){
			func(_ *Scope, name string, err error) {
				if name == "b" {
					errB = err
				}
				if err == nil {
					rec.record("after " + name + " nil")
					return
				}
				rec.record("after " + name + " " + err.Error())
			},
		},
	})
	root.AddAfterStartHook(func(_ *Scope, name string, _ error) { rec.record("late-after " + name) })
	ProvideNamed(root, "a", func(Injector) (*errorStarter, error) { return &errorStarter{}, nil })
	ProvideNamed(root, "b", func(Injector) (*errorStarter, error) { return &errorStarter{err: errBoom}, nil })

	is.ErrorIs(root.Start(), errBoom)
	is.Equal([]string{
		"before a", "after a nil", "late-after a",
		"before b", "after b boom", "late-after b",
	}, rec.list())
	is.Same(errBoom, errB, "the hook receives the method's own error, not a wrapping one")
}

func TestStart_HooksSkipNonStarter(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	rec := &callRecorder{}
	root := New()
	root.AddBeforeStartHook(func(_ *Scope, name string) { rec.record("before " + name) })
	root.AddAfterStartHook(func(_ *Scope, name string, _ error) { rec.record("after " + name) })
	ProvideNamed(root, "app.Lifecycle", func(Injector) (startLifecycle, error) { return &startNone{}, nil }, AsStarter())

	err := root.Start()

	is.ErrorContains(err, "DI: service `app.Lifecycle` was opted in with AsStarter but does not implement any Starter interface")
	is.Empty(rec.list())
}

func TestStartError_Fields(t *testing.T) {
	t.Parallel()

	errBuild := errors.New("build failed")
	errBoom := errors.New("boom")

	tests := []struct {
		name    string
		setup   func(root *RootScope)
		service string
		kind    string
		op      StartOp
		cause   error
		message string // the message before StartError existed
	}{
		{
			name: "provider returns errBuild",
			setup: func(root *RootScope) {
				ProvideNamed(root, "b", func(Injector) (*startWithErr, error) { return nil, errBuild })
			},
			service: "b", op: StartOpBuild, cause: errBuild,
			message: "DI: failed to build service `b` in scope `[root]`: build failed",
		},
		{
			name: "Start returns errBoom",
			setup: func(root *RootScope) {
				ProvideNamedValue(root, "b", &errorStarter{err: errBoom})
			},
			service: "b", op: StartOpStart, cause: errBoom,
			message: "DI: failed to start service `b` in scope `[root]`: boom",
		},
		{
			name: "kind handler returns errBoom",
			setup: func(root *RootScope) {
				k := NewKind[Job, Spec]("k")
				ProvideNamed(root, "j", func(Injector) (*CleanupJob, error) { return &CleanupJob{}, nil }, k.With(Spec{}))
				k.Handle(root, StageBeforeStart, func(context.Context, Component[Job, Spec]) error { return errBoom })
			},
			service: "j", kind: "k", op: StartOpHandle, cause: errBoom,
			message: "DI: kind `k` failed for service `j` in scope `[root]`: boom",
		},
		{
			name: "missing declared dependency",
			setup: func(root *RootScope) {
				ProvideNamed(root, "b", func(Injector) (*startWithErr, error) { return &startWithErr{}, nil }, DependsOnNamed("missing"))
			},
			service: "b", op: StartOpBuild, cause: ErrServiceNotFound,
			message: "DI: failed to build service `b` in scope `[root]`: DI: could not find service `missing`, available services: `b`, path: `b` -> `missing`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			tt.setup(root)

			err := root.Start()

			var startErr *StartError
			require.ErrorAs(t, err, &startErr)
			is.Equal(tt.service, startErr.Service)
			is.Equal("[root]", startErr.Scope.Name())
			is.Equal(tt.kind, startErr.Kind)
			is.Equal(tt.op, startErr.Op)
			is.ErrorIs(err, tt.cause)
			is.EqualError(err, tt.message)
		})
	}
}

func TestStartError_NotForGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		start    func(root *RootScope) error
		sentinel error
	}{
		{
			name: "already started",
			start: func(root *RootScope) error {
				_ = root.Start()
				return root.Start()
			},
			sentinel: ErrAlreadyStarted,
		},
		{
			name: "already shut down",
			start: func(root *RootScope) error {
				root.Shutdown()
				return root.Start()
			},
			sentinel: ErrStartAfterShutdown,
		},
		{
			name: "context already cancelled",
			start: func(root *RootScope) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return root.StartWithContext(ctx)
			},
			sentinel: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			ProvideNamedValue(root, "b", &startWithErr{})

			err := tt.start(root)

			var startErr *StartError
			assert.NotErrorAs(t, err, &startErr)
			assert.ErrorIs(t, err, tt.sentinel)
		})
	}
}

// countingStarters counts Start calls per service name and records their order.
type countingStarters struct {
	mu     sync.Mutex
	counts map[string]int
	order  []string
}

func (c *countingStarters) record(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[name]++
	c.order = append(c.order, name)
}

func (c *countingStarters) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[name]
}

func (c *countingStarters) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = nil
}

func (c *countingStarters) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.order)
}

// countedStarter is a Starter reporting its starts to starts.
type countedStarter struct {
	name   string
	starts *countingStarters
}

func (s *countedStarter) Start() error {
	s.starts.record(s.name)
	return nil
}

// provideCountedStarter registers lazy Starter name in scope.
func provideCountedStarter(scope Injector, starts *countingStarters, name string, opts ...ProvideOption) {
	ProvideNamed(scope, name, func(Injector) (*countedStarter, error) {
		return &countedStarter{name: name, starts: starts}, nil
	}, opts...)
}

func TestRefresh_StartsNew(t *testing.T) {
	t.Parallel()

	for _, times := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d", times), func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			starts := &countingStarters{}
			provideCountedStarter(root, starts, "a")
			require.NoError(t, root.Start())
			starts.reset()

			provideCountedStarter(root, starts, "late")
			tenant := root.Scope("tenant")
			provideCountedStarter(tenant, starts, "worker")

			for call := range times {
				is.NoError(root.Refresh())
				if call == 0 {
					is.Equal([]string{"late", "worker"}, starts.list())
				}
			}

			is.Equal(1, starts.count("a"))
			is.Equal(1, starts.count("late"))
			is.Equal(1, starts.count("worker"))
		})
	}
}

func TestRefresh_NothingNew(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	starts := &countingStarters{}
	provideCountedStarter(root, starts, "a")
	require.NoError(t, root.Start())

	assert.NoError(t, root.Refresh())
	assert.Equal(t, 1, starts.count("a"))
}

func TestIncrementalStart_Phase(t *testing.T) {
	t.Parallel()

	errBuild := errors.New("build failed")

	tests := []struct {
		name      string
		failA     bool
		previous  func(root *RootScope)
		operation func(root *RootScope, tenant *Scope) error
		want      error
	}{
		{
			name:      "nothing done, root.Refresh",
			previous:  func(*RootScope) {},
			operation: func(root *RootScope, _ *Scope) error { return root.Refresh() },
			want:      ErrNotStarted,
		},
		{
			name:      "Start failed, root.Refresh",
			failA:     true,
			previous:  func(root *RootScope) { _ = root.Start() },
			operation: func(root *RootScope, _ *Scope) error { return root.Refresh() },
			want:      ErrNotStarted,
		},
		{
			name:      "nothing done, tenant.Start",
			previous:  func(*RootScope) {},
			operation: func(_ *RootScope, tenant *Scope) error { return tenant.Start() },
			want:      ErrNotStarted,
		},
		{
			name:      "Shutdown without Start, tenant.Start",
			previous:  func(root *RootScope) { root.Shutdown() },
			operation: func(_ *RootScope, tenant *Scope) error { return tenant.Start() },
			want:      ErrStartAfterShutdown,
		},
		{
			name: "Start then Shutdown, root.Refresh",
			previous: func(root *RootScope) {
				_ = root.Start()
				root.Shutdown()
			},
			operation: func(root *RootScope, _ *Scope) error { return root.Refresh() },
			want:      ErrStartAfterShutdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			starts := &countingStarters{}
			if tt.failA {
				ProvideNamed(root, "a", func(Injector) (*countedStarter, error) { return nil, errBuild })
			} else {
				provideCountedStarter(root, starts, "a")
			}
			tenant := root.Scope("tenant")
			provideCountedStarter(tenant, starts, "w")
			tt.previous(root)
			before := starts.count("w") // the first Start may have started "w"

			err := tt.operation(root, tenant)

			assert.ErrorIs(t, err, tt.want)
			assert.Equal(t, before, starts.count("w"), "the operation started nothing")
		})
	}
}

func TestScopeStart_Subtree(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	require.NoError(t, root.Start())
	starts := &countingStarters{}
	tenantA := root.Scope("tenant-a")
	provideCountedStarter(tenantA, starts, "wa")
	tenantB := root.Scope("tenant-b")
	provideCountedStarter(tenantB, starts, "wb")
	jobs := tenantA.Scope("jobs")
	provideCountedStarter(jobs, starts, "cron")
	provideCountedStarter(root, starts, "late")

	is.NoError(tenantA.Start())

	is.Equal([]string{"wa", "cron"}, starts.list())
	is.Equal(0, starts.count("wb"))
	is.Equal(0, starts.count("late"))
}

func TestScopeStart_RootScope(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	require.NoError(t, root.Start())
	starts := &countingStarters{}
	provideCountedStarter(root, starts, "late")

	// the *Scope a root service's provider works with
	scope := root.self

	assert.NoError(t, scope.Start())
	assert.Equal(t, 1, starts.count("late"))
}

func TestRefresh_Concurrent(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	root := New()
	require.NoError(t, root.Start())
	starts := &countingStarters{}
	for i := range 10 {
		provideCountedStarter(root, starts, fmt.Sprintf("s%d", i))
	}

	errs := make(chan error, 10)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { errs <- root.Refresh() })
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		is.NoError(err)
	}
	for i := range 10 {
		is.Equal(1, starts.count(fmt.Sprintf("s%d", i)))
	}
}

func TestRefresh_ShutdownCancels(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		is := assert.New(t)

		root := New()
		require.NoError(t, root.Start())
		entered := make(chan struct{})
		ProvideNamedValue(root, "late", &ctxStarter{fn: func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}})

		refreshErr := make(chan error, 1)
		go func() { refreshErr <- root.Refresh() }()
		<-entered

		report := root.Shutdown()

		select {
		case err := <-refreshErr:
			is.ErrorIs(err, ErrStartAfterShutdown)
		default:
			is.Fail("Shutdown returned before Refresh returned")
		}
		is.True(report.Succeed, report.Error())
	})
}
