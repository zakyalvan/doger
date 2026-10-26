package doger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopeHooks_RegistrationOrder(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	calls := &callRecorder{}
	recordAs := func(label string) func(context.Context, *Scope) {
		return func(_ context.Context, scope *Scope) { calls.record(label + " " + scope.Name()) }
	}
	root := NewWithOpts(&InjectorOpts{
		HookScopeStarted: []func(context.Context, *Scope){recordAs("h1"), nil, recordAs("h2")},
	})
	root.AddScopeStartedHook(recordAs("h3"))
	root.AddScopeStartedHook(nil)
	require.NoError(t, root.Start())
	tenant := root.Scope("tenant")

	assert.NotPanics(t, func() { assert.NoError(t, tenant.Start()) })
	assert.Equal(t, []string{"h1 tenant", "h2 tenant", "h3 tenant"}, calls.list())
}

func TestScopeStartingHook_Veto(t *testing.T) {
	t.Parallel()

	errVeto := errors.New("veto")

	tests := []struct {
		name     string
		behavior func() error
		check    func(t *testing.T, err error)
		starts   int
		recorded []string
	}{
		{
			name:     "returns nil",
			behavior: func() error { return nil },
			check:    func(t *testing.T, err error) { t.Helper(); assert.NoError(t, err) },
			starts:   1,
			recorded: []string{"tenant"},
		},
		{
			name:     "returns errVeto",
			behavior: func() error { return errVeto },
			check: func(t *testing.T, err error) {
				t.Helper()
				assert.ErrorIs(t, err, errVeto)
				assert.ErrorContains(t, err, "scope `tenant`")
			},
		},
		{
			name:     "panics",
			behavior: func() error { panic("veto-boom") },
			check:    func(t *testing.T, err error) { t.Helper(); assert.ErrorContains(t, err, "veto-boom") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			require.NoError(t, root.Start())
			tenant := root.Scope("tenant")
			starts := &countingStarters{}
			provideCountedStarter(tenant, starts, "worker")
			started := &callRecorder{}
			root.AddScopeStartingHook(func(context.Context, *Scope) error { return tt.behavior() })
			root.AddScopeStartedHook(func(_ context.Context, scope *Scope) { started.record(scope.Name()) })

			var err error
			require.NotPanics(t, func() { err = tenant.Start() })

			tt.check(t, err)
			assert.Equal(t, tt.starts, starts.count("worker"))
			assert.Equal(t, tt.recorded, started.list())
		})
	}
}

// scopeShutdowner records "shutdown <scope>" when shut down.
type scopeShutdowner struct {
	scope string
	rec   *callRecorder
}

func (s *scopeShutdowner) Shutdown() error {
	s.rec.record("shutdown " + s.scope)
	return nil
}

// provideBuiltScopeShutdowner registers and builds a scopeShutdowner in scope.
func provideBuiltScopeShutdowner(t *testing.T, scope *Scope, rec *callRecorder) {
	t.Helper()
	ProvideNamed(scope, "svc", func(Injector) (*scopeShutdowner, error) {
		return &scopeShutdowner{scope: scope.Name(), rec: rec}, nil
	})
	_, err := InvokeNamed[*scopeShutdowner](scope, "svc")
	require.NoError(t, err)
}

func TestScopeHooks_ClosingClosed(t *testing.T) {
	t.Parallel()

	nested := []string{"closing jobs", "shutdown jobs", "closed jobs", "closing tenant", "shutdown tenant", "closed tenant"}

	tests := []struct {
		name      string
		operation func(root *RootScope, tenant, jobs *Scope)
		events    []string
	}{
		{name: "tenant.Shutdown()", operation: func(_ *RootScope, tenant, _ *Scope) { tenant.Shutdown() }, events: nested},
		{name: "root.Shutdown()", operation: func(root *RootScope, _, _ *Scope) { root.Shutdown() }, events: nested},
		{name: "jobs.Shutdown()", operation: func(_ *RootScope, _, jobs *Scope) { jobs.Shutdown() }, events: []string{"closing jobs", "shutdown jobs", "closed jobs"}},
		{
			name: "tenant.Shutdown() then root.Shutdown()",
			operation: func(root *RootScope, tenant, _ *Scope) {
				tenant.Shutdown()
				root.Shutdown()
			},
			events: append(append([]string{}, nested...), "closing tenant", "closed tenant"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			rec := &callRecorder{}
			tenant := root.Scope("tenant")
			jobs := tenant.Scope("jobs")
			provideBuiltScopeShutdowner(t, tenant, rec)
			provideBuiltScopeShutdowner(t, jobs, rec)
			root.AddScopeClosingHook(func(_ context.Context, scope *Scope) { rec.record("closing " + scope.Name()) })
			root.AddScopeClosedHook(func(_ context.Context, scope *Scope) { rec.record("closed " + scope.Name()) })

			tt.operation(root, tenant, jobs)

			assert.Equal(t, tt.events, rec.list())
		})
	}
}

func TestScopeHooks_RootFiresNone(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	var calls atomic.Int32
	root.AddScopeStartingHook(func(context.Context, *Scope) error { calls.Add(1); return nil })
	root.AddScopeStartedHook(func(context.Context, *Scope) { calls.Add(1) })
	root.AddScopeClosingHook(func(context.Context, *Scope) { calls.Add(1) })
	root.AddScopeClosedHook(func(context.Context, *Scope) { calls.Add(1) })

	require.NoError(t, root.Start())
	root.Shutdown()

	assert.Equal(t, int32(0), calls.Load())
}
