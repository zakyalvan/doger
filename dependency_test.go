package doger

import (
	"encoding/json"
	"fmt"
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

// declareFixture registers the services of the "Declaring edges dynamically" scenario.
func declareFixture(t *testing.T) (*RootScope, *Scope) {
	t.Helper()

	root := New()
	rec := &callRecorder{}
	provideRecorded(root, rec, "scheduler")
	provideRecorded(root, rec, "job-a")
	ProvideNamed(root, "job-b", func(i Injector) (*depValue, error) {
		_, err := InvokeNamed[*depValue](i, "scheduler")
		return &depValue{name: "job-b"}, err
	})
	provideRecorded(root, rec, "job-s", DependsOnNamed("scheduler"))
	tenant := root.Scope("tenant")
	provideRecorded(tenant, rec, "tenant-job")

	// records job-b -> scheduler
	_, err := InvokeNamed[*depValue](root, "job-b")
	require.NoError(t, err)

	return root, tenant
}

// TestDeclareDependency is not parallel: one row replaces DefaultRootScope.
func TestDeclareDependency(t *testing.T) {
	tests := []struct {
		name       string
		given      func(t *testing.T, root *RootScope)
		injector   func(root *RootScope, tenant *Scope) Injector
		dependent  string
		dependency string
		err        error
		check      func(t *testing.T, root *RootScope, tenant *Scope)
	}{
		{
			name: "records_edge", dependent: "scheduler", dependency: "job-a",
			check: func(t *testing.T, root *RootScope, _ *Scope) {
				assert.Equal(t, []string{"job-a"}, explainedNames(t, root, "scheduler", false))
			},
		},
		{
			name: "repeated_edge_changes_nothing", dependent: "scheduler", dependency: "job-a",
			given: func(t *testing.T, root *RootScope) { require.NoError(t, DeclareDependency(root, "scheduler", "job-a")) },
			check: func(t *testing.T, root *RootScope, _ *Scope) {
				assert.Equal(t, []string{"job-a"}, explainedNames(t, root, "scheduler", false))
			},
		},
		{name: "missing_dependency", dependent: "scheduler", dependency: "missing", err: ErrServiceNotFound},
		{name: "missing_dependent", dependent: "missing", dependency: "job-a", err: ErrServiceNotFound},
		{name: "empty_dependent", dependent: "", dependency: "job-a", err: ErrServiceNotFound},
		{name: "cycle_through_invoked_edge", dependent: "scheduler", dependency: "job-b", err: ErrCircularDependency},
		{name: "self_edge", dependent: "job-a", dependency: "job-a", err: ErrCircularDependency},
		{name: "cycle_through_static_edge_not_built", dependent: "scheduler", dependency: "job-s", err: ErrCircularDependency},
		{
			name:      "dependency_not_visible_from_dependent_scope",
			injector:  func(_ *RootScope, tenant *Scope) Injector { return tenant },
			dependent: "scheduler", dependency: "tenant-job", err: ErrServiceNotFound,
		},
		{
			name:      "child_to_ancestor",
			injector:  func(_ *RootScope, tenant *Scope) Injector { return tenant },
			dependent: "tenant-job", dependency: "scheduler",
			check: func(t *testing.T, root *RootScope, tenant *Scope) {
				explained, ok := ExplainNamedService(tenant, "tenant-job")
				require.True(t, ok)
				if assert.Len(t, explained.Dependencies, 1) {
					assert.Equal(t, root.ID(), explained.Dependencies[0].ScopeID)
					assert.Equal(t, "scheduler", explained.Dependencies[0].Service)
				}
				assert.Equal(t, "tenant", explained.ScopeName)
			},
		},
		{
			name: "nil_injector_uses_default_root_scope",
			given: func(t *testing.T, root *RootScope) {
				previous := DefaultRootScope
				DefaultRootScope = root
				t.Cleanup(func() { DefaultRootScope = previous })
			},
			injector:  func(*RootScope, *Scope) Injector { return nil },
			dependent: "scheduler", dependency: "job-a",
			check: func(t *testing.T, root *RootScope, _ *Scope) {
				assert.Equal(t, []string{"job-a"}, explainedNames(t, root, "scheduler", false))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testWithTimeout(t, time.Second)

			root, tenant := declareFixture(t)
			if tt.given != nil {
				tt.given(t, root)
			}
			var injector Injector = root
			if tt.injector != nil {
				injector = tt.injector(root, tenant)
			}

			err := DeclareDependency(injector, tt.dependent, tt.dependency)

			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			tt.check(t, root, tenant)
		})
	}
}

func TestDeclareDependency_Concurrent(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	root := New()
	rec := &callRecorder{}
	provideRecorded(root, rec, "scheduler")
	const jobs = 50
	for n := range jobs {
		provideRecorded(root, rec, fmt.Sprintf("job-%d", n))
	}

	var wg sync.WaitGroup
	for n := range jobs {
		wg.Go(func() {
			is.NoError(DeclareDependency(root, "scheduler", fmt.Sprintf("job-%d", n)))
		})
	}
	wg.Wait()

	is.Len(explainedNames(t, root, "scheduler", false), jobs)
}

func TestDeclareDependency_Clone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		invoked string
		deps    []string
	}{
		{name: "declared_edge_not_copied", invoked: "scheduler", deps: []string{}},
		{name: "static_edge_recorded_again", invoked: "worker", deps: []string{"job"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			rec := &callRecorder{}
			provideRecorded(root, rec, "scheduler")
			provideRecorded(root, rec, "job")
			provideRecorded(root, rec, "worker", DependsOnNamed("job"))
			require.NoError(t, DeclareDependency(root, "scheduler", "job"))
			_, err := InvokeNamed[*depValue](root, "worker")
			require.NoError(t, err)

			clone := root.Clone()
			_, err = InvokeNamed[*depValue](clone, tt.invoked)
			require.NoError(t, err)

			assert.Equal(t, tt.deps, explainedNames(t, clone, tt.invoked, false))
		})
	}
}

// orderedShutdown records its name in rec when shut down.
type orderedShutdown struct {
	name string
	rec  *callRecorder
}

func (s *orderedShutdown) Shutdown() error {
	s.rec.record(s.name)
	return nil
}

func TestDeclaredEdges_ShutdownOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		schedulerOpts []ProvideOption
		declare       bool
	}{
		{name: "declare_dependency", declare: true},
		{name: "depends_on_named", schedulerOpts: []ProvideOption{DependsOnNamed("job")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			rec := &callRecorder{}
			for _, name := range []string{"job", "scheduler"} {
				var opts []ProvideOption
				if name == "scheduler" {
					opts = tt.schedulerOpts
				}
				ProvideNamed(root, name, func(Injector) (*orderedShutdown, error) {
					return &orderedShutdown{name: name, rec: rec}, nil
				}, opts...)
			}
			for _, name := range []string{"scheduler", "job"} {
				_, err := InvokeNamed[*orderedShutdown](root, name)
				require.NoError(t, err)
			}
			if tt.declare {
				require.NoError(t, DeclareDependency(root, "scheduler", "job"))
			}

			require.True(t, root.Shutdown().Succeed)
			assert.Equal(t, []string{"scheduler", "job"}, rec.list())
		})
	}
}

// edgeSourceFixture builds the "Source per edge" setup: worker invokes db,
// depends on cache, and is declared to depend on job.
func edgeSourceFixture(t *testing.T) *RootScope {
	t.Helper()

	root := New()
	rec := &callRecorder{}
	provideRecorded(root, rec, "db")
	provideRecorded(root, rec, "cache")
	provideRecorded(root, rec, "job")
	ProvideNamed(root, "worker", func(i Injector) (*depValue, error) {
		if _, err := InvokeNamed[*depValue](i, "db"); err != nil {
			return nil, err
		}
		return &depValue{name: "worker"}, nil
	}, DependsOnNamed("cache"))
	_, err := InvokeNamed[*depValue](root, "worker")
	require.NoError(t, err)
	require.NoError(t, DeclareDependency(root, "worker", "job"))

	return root
}

func TestExplain_EdgeSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		extra   func(t *testing.T, root *RootScope)
		marks   []string
		noMarks []string
	}{
		{
			name:    "nothing else",
			extra:   func(*testing.T, *RootScope) {},
			marks:   []string{"* cache (depends-on) from scope", "* job (declared) from scope", "* db from scope"},
			noMarks: []string{"db (", "cache (invoked)"},
		},
		{
			name: "db also declared",
			extra: func(t *testing.T, root *RootScope) {
				t.Helper()
				require.NoError(t, DeclareDependency(root, "worker", "db"))
			},
			marks:   []string{"* db from scope"},
			noMarks: []string{"db ("},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := edgeSourceFixture(t)
			tt.extra(t, root)

			explained, ok := ExplainNamedService(root, "worker")
			require.True(t, ok)

			sources := map[string]DependencySource{}
			for _, edge := range explained.Dependencies {
				sources[edge.Service] = edge.Source
			}
			is.Equal(map[string]DependencySource{
				"cache": DependencySourceDependsOn,
				"db":    DependencySourceInvoked,
				"job":   DependencySourceDeclared,
			}, sources)

			output := explained.String()
			for _, mark := range tt.marks {
				is.Contains(output, mark)
			}
			for _, mark := range tt.noMarks {
				is.NotContains(output, mark)
			}
		})
	}
}

func TestExplain_EdgeSourceDependents(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := edgeSourceFixture(t)

	explained, ok := ExplainNamedService(root, "job")
	require.True(t, ok)
	require.Len(t, explained.Dependents, 1)
	assert.Equal(t, "worker", explained.Dependents[0].Service)
	assert.Equal(t, DependencySourceDeclared, explained.Dependents[0].Source)
}

func TestExplain_EdgeSourceCompat(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	rec := &callRecorder{}
	provideRecorded(root, rec, "db")
	ProvideNamed(root, "worker", func(i Injector) (*depValue, error) {
		_, err := InvokeNamed[*depValue](i, "db")
		return &depValue{name: "worker"}, err
	})
	_, err := InvokeNamed[*depValue](root, "worker")
	require.NoError(t, err)

	explained, ok := ExplainNamedService(root, "worker")
	require.True(t, ok)
	require.Len(t, explained.Dependencies, 1)

	// the output produced before edge sources existed
	is.Equal("* db from scope [root]", explained.Dependencies[0].String())
	is.Contains(explained.String(), "\nDependencies:\n* db from scope [root]\n\nDependents:\n")

	encoded, err := json.Marshal(explained.Dependencies[0])
	require.NoError(t, err)
	is.Contains(string(encoded), `"source":"invoked"`)
}
