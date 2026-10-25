package doger

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callRecorder records names in call order, safe for concurrent use.
type callRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *callRecorder) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
}

func (r *callRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// depValue is the instance type of the services in dependency tests.
type depValue struct{ name string }

// provideRecorded registers lazy name in scope, recording its provider calls.
func provideRecorded(scope Injector, rec *callRecorder, name string, opts ...ProvideOption) {
	ProvideNamed(scope, name, func(Injector) (*depValue, error) {
		rec.record(name)
		return &depValue{name: name}, nil
	}, opts...)
}

// explainedNames returns the sorted service names of the direct dependencies
// (or dependents) of name, explained from scope.
func explainedNames(t *testing.T, scope Injector, name string, dependents bool) []string {
	t.Helper()

	explained, ok := ExplainNamedService(scope, name)
	require.Truef(t, ok, "service %q not found", name)

	edges := explained.Dependencies
	if dependents {
		edges = explained.Dependents
	}

	names := make([]string, 0, len(edges))
	for _, edge := range edges {
		names = append(names, edge.Service)
	}
	slices.Sort(names)

	return names
}

func TestDependsOn_BuildOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		opts  []ProvideOption
		order []string
		deps  []string
	}{
		{name: "one", opts: []ProvideOption{DependsOnNamed("db")}, order: []string{"db", "worker"}, deps: []string{"db"}},
		{name: "two_in_declaration_order", opts: []ProvideOption{DependsOnNamed("cache"), DependsOnNamed("db")}, order: []string{"cache", "db", "worker"}, deps: []string{"cache", "db"}},
		{name: "none", opts: nil, order: []string{"worker"}, deps: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			rec := &callRecorder{}
			provideRecorded(root, rec, "db")
			provideRecorded(root, rec, "cache")
			provideRecorded(root, rec, "worker", tt.opts...)

			_, err := InvokeNamed[*depValue](root, "worker")
			require.NoError(t, err)

			is.Equal(tt.order, rec.list())
			is.Equal(tt.deps, explainedNames(t, root, "worker", false))
		})
	}
}

func TestDependsOn_EagerTarget(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	rec := &callRecorder{}
	ProvideNamedValue(root, "cfg", &depValue{name: "cfg"})
	provideRecorded(root, rec, "worker", DependsOnNamed("cfg"))

	_, err := InvokeNamed[*depValue](root, "worker")
	require.NoError(t, err)

	is.Equal([]string{"cfg"}, explainedNames(t, root, "worker", false))
	is.Equal([]string{"worker"}, explainedNames(t, root, "cfg", true))
}

func TestDependsOn_EagerDependent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dbBefore    bool
		dbBetween   bool
		invocations int
		wantErrs    []error
		deps        []string
	}{
		{name: "db_registered_before", dbBefore: true, invocations: 2, wantErrs: []error{nil, nil}, deps: []string{"db"}},
		{name: "db_registered_between", dbBetween: true, invocations: 2, wantErrs: []error{ErrServiceNotFound, nil}, deps: []string{"db"}},
		{name: "db_never_registered", invocations: 1, wantErrs: []error{ErrServiceNotFound}, deps: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			rec := &callRecorder{}
			ProvideNamedValue(root, "srv", &depValue{name: "srv"}, DependsOnNamed("db"))
			if tt.dbBefore {
				provideRecorded(root, rec, "db")
			}

			for n := range tt.invocations {
				if n == 1 && tt.dbBetween {
					provideRecorded(root, rec, "db")
				}

				_, err := InvokeNamed[*depValue](root, "srv")
				if tt.wantErrs[n] == nil {
					is.NoErrorf(err, "invocation %d", n)
				} else {
					is.ErrorIsf(err, tt.wantErrs[n], "invocation %d", n)
				}
			}

			is.Equal(tt.deps, explainedNames(t, root, "srv", false))
		})
	}
}

func TestDependsOn_AncestorScope(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	rec := &callRecorder{}
	provideRecorded(root, rec, "db")
	request := root.Scope("request")
	provideRecorded(request, rec, "handler", DependsOnNamed("db"))

	_, err := InvokeNamed[*depValue](request, "handler")
	require.NoError(t, err)

	is.Equal([]string{"db", "handler"}, rec.list())

	explained, ok := ExplainNamedService(request, "handler")
	require.True(t, ok)
	if is.Len(explained.Dependencies, 1) {
		is.Equal(root.ID(), explained.Dependencies[0].ScopeID)
		is.Equal("[root]", explained.Dependencies[0].ScopeName)
		is.Equal("db", explained.Dependencies[0].Service)
	}
}

func TestDependsOn_ConcurrentSingleBuild(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	root := New()
	var dbCalls, workerCalls atomic.Int32
	ProvideNamed(root, "db", func(Injector) (*depValue, error) {
		dbCalls.Add(1)
		return &depValue{name: "db"}, nil
	})
	ProvideNamed(root, "worker", func(Injector) (*depValue, error) {
		workerCalls.Add(1)
		return &depValue{name: "worker"}, nil
	}, DependsOnNamed("db"))

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			_, err := InvokeNamed[*depValue](root, "worker")
			is.NoError(err)
		})
	}
	wg.Wait()

	is.Equal(int32(1), dbCalls.Load())
	is.Equal(int32(1), workerCalls.Load())
}

func TestDependsOn_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(root *RootScope, rec *callRecorder)
		invoked string
		err     error
		message string
	}{
		{
			name:    "missing",
			setup:   func(root *RootScope, rec *callRecorder) { provideRecorded(root, rec, "worker", DependsOnNamed("db")) },
			invoked: "worker", err: ErrServiceNotFound, message: "`db`",
		},
		{
			name:    "empty_name",
			setup:   func(root *RootScope, rec *callRecorder) { provideRecorded(root, rec, "worker", DependsOnNamed("")) },
			invoked: "worker", err: ErrServiceNotFound, message: "``",
		},
		{
			name: "declared_cycle",
			setup: func(root *RootScope, rec *callRecorder) {
				provideRecorded(root, rec, "a", DependsOnNamed("b"))
				provideRecorded(root, rec, "b", DependsOnNamed("a"))
			},
			invoked: "a", err: ErrCircularDependency, message: "`a` -> `b` -> `a`",
		},
		{
			name:    "self",
			setup:   func(root *RootScope, rec *callRecorder) { provideRecorded(root, rec, "a", DependsOnNamed("a")) },
			invoked: "a", err: ErrCircularDependency, message: "`a` -> `a`",
		},
		{
			name: "cycle_through_invocation",
			setup: func(root *RootScope, rec *callRecorder) {
				provideRecorded(root, rec, "a", DependsOnNamed("b"))
				ProvideNamed(root, "b", func(i Injector) (*depValue, error) {
					rec.record("b")
					_, err := InvokeNamed[*depValue](i, "a")
					return &depValue{name: "b"}, err
				})
			},
			invoked: "a", err: ErrCircularDependency, message: "`a` -> `b` -> `a`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			rec := &callRecorder{}
			tt.setup(root, rec)

			_, err := InvokeNamed[*depValue](root, tt.invoked)

			is.ErrorIs(err, tt.err)
			is.ErrorContains(err, tt.message)
			is.NotContains(rec.list(), tt.invoked)
		})
	}
}

func TestEager_NoOptionsNeverErrors(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	ProvideNamedValue(root, "cfg", &depValue{name: "cfg"})

	for n := range 3 {
		value, err := InvokeNamed[*depValue](root, "cfg")
		is.NoErrorf(err, "invocation %d", n)
		is.Equal("cfg", value.name)
	}
}
