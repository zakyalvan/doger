package doger

import (
	"context"
	"testing"
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
