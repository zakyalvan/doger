package doger

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lifecycleFailingStarter is a Starter whose Start returns err.
type lifecycleFailingStarter struct{ err error }

func (s *lifecycleFailingStarter) Start() error { return s.err }

// syncRecorder records names in order; it is safe for concurrent use.
type syncRecorder struct {
	mu    sync.Mutex
	names []string
}

func (r *syncRecorder) add(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
}

func (r *syncRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.names...)
}

// recordLifecycle adds a hook recording "hook:<point>" and a function
// listener recording "event:<point>" for each container lifecycle point.
func recordLifecycle(root *RootScope, rec *syncRecorder) {
	root.AddContainerStartingHook(func(context.Context, *RootScope) error { rec.add("hook:Starting"); return nil })
	root.AddContainerStartedHook(func(context.Context, *RootScope) { rec.add("hook:Started") })
	root.AddContainerReadyHook(func(context.Context, *RootScope) { rec.add("hook:Ready") })
	root.AddContainerFailedHook(func(context.Context, *RootScope, error) { rec.add("hook:Failed") })
	root.AddContainerClosingHook(func(context.Context, *RootScope) { rec.add("hook:Closing") })
	root.AddContainerClosedHook(func(context.Context, *RootScope, *ShutdownReport) { rec.add("hook:Closed") })
	Listen(root, func(context.Context, ContainerStarting) error { rec.add("event:Starting"); return nil })
	Listen(root, func(context.Context, ContainerStarted) error { rec.add("event:Started"); return nil })
	Listen(root, func(context.Context, ContainerReady) error { rec.add("event:Ready"); return nil })
	Listen(root, func(context.Context, ContainerFailed) error { rec.add("event:Failed"); return nil })
	Listen(root, func(context.Context, ContainerClosing) error { rec.add("event:Closing"); return nil })
	Listen(root, func(context.Context, ContainerClosed) error { rec.add("event:Closed"); return nil })
}

func TestLifecycleEvents_Order(t *testing.T) {
	t.Parallel()

	errBoom, errNo := errors.New("boom"), errors.New("no")
	tests := []struct {
		name      string
		setup     func(root *RootScope)
		operation func(root *RootScope)
		record    []string
	}{
		{
			name: "Start, then Shutdown",
			operation: func(root *RootScope) {
				_ = root.Start()
				root.Shutdown()
			},
			record: []string{"hook:Starting", "event:Starting", "hook:Started", "event:Started", "hook:Ready", "event:Ready", "hook:Closing", "event:Closing", "hook:Closed", "event:Closed"},
		},
		{
			name: "Start with a Starter failing",
			setup: func(root *RootScope) {
				ProvideNamedValue(root, "starter", &lifecycleFailingStarter{err: errBoom}, AsStarter())
			},
			operation: func(root *RootScope) { _ = root.Start() },
			record:    []string{"hook:Starting", "event:Starting", "hook:Failed", "event:Failed"},
		},
		{
			name: "Start with a Starting hook vetoing",
			setup: func(root *RootScope) {
				root.AddContainerStartingHook(func(context.Context, *RootScope) error { return errNo })
			},
			operation: func(root *RootScope) { _ = root.Start() },
			record:    []string{"hook:Starting", "hook:Failed", "event:Failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			rec := &syncRecorder{}
			recordLifecycle(root, rec)
			if tt.setup != nil {
				tt.setup(root)
			}

			tt.operation(root)

			assert.Equal(t, tt.record, rec.list())
		})
	}
}

func TestLifecycleEvents_Data(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	root := New()
	ProvideNamedValue(root, "starter", &lifecycleFailingStarter{err: errBoom}, AsStarter())
	var failed ContainerFailed
	var closed ContainerClosed
	Listen(root, func(_ context.Context, e ContainerFailed) error { failed = e; return nil })
	Listen(root, func(_ context.Context, e ContainerClosed) error { closed = e; return nil })

	require.Error(t, root.Start())
	report := root.Shutdown()

	assert.Same(t, root, failed.Root)
	require.ErrorIs(t, failed.Err, errBoom)
	assert.Same(t, root, closed.Root)
	assert.Same(t, report, closed.Report)
}

// lifecycleFirstListener is a lazy listener service whose provider fails.
type lifecycleFirstListener struct{}

func (*lifecycleFirstListener) OnEvent(context.Context, ContainerStarting) error { return nil }

func TestLifecycleEvents_Observational(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	failing := func(context.Context, any) error { return errBoom }
	tests := []struct {
		name     string
		register func(root *RootScope, second func())
	}{
		{name: "ContainerStarting, a lazy service whose provider fails", register: func(root *RootScope, second func()) {
			ProvideNamed(root, "first", func(Injector) (*lifecycleFirstListener, error) { return nil, errBoom }, AsListener[ContainerStarting]())
			Listen(root, func(context.Context, ContainerStarting) error { second(); return nil })
		}},
		{name: "ContainerStarted, returns an error", register: func(root *RootScope, second func()) {
			Listen(root, func(ctx context.Context, e ContainerStarted) error { return failing(ctx, e) })
			Listen(root, func(context.Context, ContainerStarted) error { second(); return nil })
		}},
		{name: "ContainerReady, panics", register: func(root *RootScope, second func()) {
			Listen(root, func(context.Context, ContainerReady) error { panic("x") })
			Listen(root, func(context.Context, ContainerReady) error { second(); return nil })
		}},
		{name: "ContainerClosing, returns an error", register: func(root *RootScope, second func()) {
			Listen(root, func(ctx context.Context, e ContainerClosing) error { return failing(ctx, e) })
			Listen(root, func(context.Context, ContainerClosing) error { second(); return nil })
		}},
		{name: "ContainerClosed, returns an error", register: func(root *RootScope, second func()) {
			Listen(root, func(ctx context.Context, e ContainerClosed) error { return failing(ctx, e) })
			Listen(root, func(context.Context, ContainerClosed) error { second(); return nil })
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			reported := []error{}
			root := NewWithOpts(&InjectorOpts{
				EventErrorMode: EventFailFast,
				HookEventError: func(_ context.Context, _ *RootScope, err error) {
					mu.Lock()
					defer mu.Unlock()
					reported = append(reported, err)
				},
			})
			secondRan := 0
			tt.register(root, func() { secondRan++ })

			require.NoError(t, root.Start())
			report := root.Shutdown()

			assert.True(t, report.Succeed)
			assert.Empty(t, report.Errors)
			assert.Equal(t, 1, secondRan)
			mu.Lock()
			defer mu.Unlock()
			require.Len(t, reported, 1)
			var listenerErr *ListenerError
			require.ErrorAs(t, reported[0], &listenerErr)
		})
	}
}

func TestLifecycleEvents_HooksUnchanged(t *testing.T) {
	t.Parallel()

	errNo := errors.New("no")
	root := New()
	root.AddContainerStartingHook(func(context.Context, *RootScope) error { return errNo })
	builds := 0
	ProvideNamed(root, "starter", func(Injector) (*lifecycleFailingStarter, error) {
		builds++
		return &lifecycleFailingStarter{}, nil
	}, AsStarter())

	err := root.Start()

	require.ErrorIs(t, err, errNo)
	assert.Zero(t, builds)
}

// startedListener is a service listener of ContainerStarted.
type startedListener struct{ calls *atomic.Int32 }

func (l *startedListener) OnEvent(context.Context, ContainerStarted) error {
	l.calls.Add(1)
	return nil
}

func TestLifecycleEvents_ServiceListenerOnly(t *testing.T) {
	t.Parallel()

	root := New()
	var calls atomic.Int32
	ProvideNamed(root, "*Starter", func(Injector) (*startedListener, error) { return &startedListener{calls: &calls}, nil }, AsListener[ContainerStarted]())

	require.NoError(t, root.Start())
	root.Shutdown()

	assert.Equal(t, int32(1), calls.Load())
}

func TestLifecycleEvents_AsyncClosed(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	reported := []error{}
	root := NewWithOpts(&InjectorOpts{HookEventError: func(_ context.Context, _ *RootScope, err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err)
	}})
	var calls atomic.Int32
	Listen(root, func(context.Context, ContainerClosed) error {
		calls.Add(1)
		return nil
	}, ListenerAsync())

	root.Shutdown()

	assert.Equal(t, int32(1), calls.Load())
	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, reported)
}

func TestLifecycleEvents_FailedBeforeRepanic(t *testing.T) {
	t.Parallel()

	root := NewWithOpts(&InjectorOpts{
		HookBeforeInvocation: []func(*Scope, string){
			func(*Scope, string) { panic("hook-boom") },
		},
	})
	ProvideNamedValue(root, "starter", &lifecycleFailingStarter{}, AsStarter())
	rec := &syncRecorder{}
	var failed ContainerFailed
	Listen(root, func(_ context.Context, e ContainerFailed) error {
		rec.add("event:Failed")
		failed = e
		return nil
	})

	assert.PanicsWithValue(t, "hook-boom", func() { _ = root.Start() })

	assert.Equal(t, []string{"event:Failed"}, rec.list())
	assert.Same(t, root, failed.Root)
	require.ErrorContains(t, failed.Err, "hook-boom")
}
