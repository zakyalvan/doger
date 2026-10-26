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

func TestPhase_Lifecycle(t *testing.T) {
	t.Parallel()

	errBuild := errors.New("build failed")

	tests := []struct {
		name     string
		failA    bool
		previous func(t *testing.T, root *RootScope, entered, release chan struct{}) (cleanup func())
		phase    ContainerPhase
		text     string
	}{
		{
			name:     "done nothing",
			previous: func(*testing.T, *RootScope, chan struct{}, chan struct{}) func() { return func() {} },
			phase:    PhaseIdle, text: "idle",
		},
		{
			name: "Start running",
			previous: func(t *testing.T, root *RootScope, entered, release chan struct{}) func() {
				t.Helper()
				done := make(chan error, 1)
				go func() { done <- root.Start() }()
				<-entered
				return func() {
					close(release)
					assert.NoError(t, <-done)
				}
			},
			phase: PhaseStarting, text: "starting",
		},
		{
			name:  "Start failed",
			failA: true,
			previous: func(t *testing.T, root *RootScope, _, _ chan struct{}) func() {
				t.Helper()
				assert.ErrorIs(t, root.Start(), errBuild)
				return func() {}
			},
			phase: PhaseFailed, text: "failed",
		},
		{
			name: "Start succeeded",
			previous: func(t *testing.T, root *RootScope, _, release chan struct{}) func() {
				t.Helper()
				close(release)
				require.NoError(t, root.Start())
				return func() {}
			},
			phase: PhaseReady, text: "ready",
		},
		{
			name: "Shutdown without Start",
			previous: func(_ *testing.T, root *RootScope, _, _ chan struct{}) func() {
				root.Shutdown()
				return func() {}
			},
			phase: PhaseClosed, text: "closed",
		},
		{
			name: "Start then Shutdown",
			previous: func(t *testing.T, root *RootScope, _, release chan struct{}) func() {
				t.Helper()
				close(release)
				require.NoError(t, root.Start())
				root.Shutdown()
				return func() {}
			},
			phase: PhaseClosed, text: "closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			entered := make(chan struct{})
			release := make(chan struct{})
			if tt.failA {
				ProvideNamed(root, "a", func(Injector) (*ctxStarter, error) { return nil, errBuild })
			} else {
				provideLazy(root, "a", &ctxStarter{fn: func(context.Context) error {
					close(entered)
					<-release
					return nil
				}})
			}
			cleanup := tt.previous(t, root, entered, release)
			defer cleanup()

			phase := root.Phase()

			assert.Equal(t, tt.phase, phase)
			assert.Equal(t, tt.text, phase.String())
		})
	}
}

func TestPhase_SeenByHooks(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	var phases []ContainerPhase
	root.AddContainerStartedHook(func(_ context.Context, r *RootScope) { phases = append(phases, r.Phase()) })
	root.AddContainerReadyHook(func(_ context.Context, r *RootScope) { phases = append(phases, r.Phase()) })
	root.AddContainerClosingHook(func(_ context.Context, r *RootScope) { phases = append(phases, r.Phase()) })

	require.NoError(t, root.Start())
	root.Shutdown()

	assert.Equal(t, []ContainerPhase{PhaseStarted, PhaseReady, PhaseClosing}, phases)
}

func TestPhase_RaceFree(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	provideLazy(root, "a", &startWithErr{})
	allowed := []ContainerPhase{PhaseIdle, PhaseStarting, PhaseStarted, PhaseReady, PhaseClosing, PhaseClosed}

	stop := make(chan struct{})
	observed := make(chan ContainerPhase, 1024)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					select {
					case observed <- root.Phase():
					default:
					}
				}
			}
		})
	}

	require.NoError(t, root.Start())
	root.Shutdown()
	close(stop)
	wg.Wait()
	close(observed)

	for phase := range observed {
		assert.Contains(t, allowed, phase)
	}
}

func TestContainerPhase_StringUnknown(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ContainerPhase(42)", ContainerPhase(42).String())
}

func TestPhase_CloneIdle(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	require.NoError(t, root.Start())

	assert.Equal(t, PhaseIdle, root.Clone().Phase())
	assert.Equal(t, PhaseReady, root.Phase())
}
