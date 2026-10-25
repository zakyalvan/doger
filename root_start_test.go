package doger

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
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
