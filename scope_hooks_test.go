package doger

import (
	"context"
	"errors"
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
