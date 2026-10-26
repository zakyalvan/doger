package doger

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type (
	// non-zero size, so that two instances never share an address and
	// assert.Same can tell them apart
	buildDB  struct{ _ int }
	buildReq struct{ _ int }
	buildCfg struct{ _ int }

	buildDBIface interface{ isBuildDB() }
)

func (*buildDB) isBuildDB() {}

// buildRecord is one HookAfterBuild call.
type buildRecord struct {
	scope    string
	name     string
	instance any
}

// buildRecorder records HookAfterBuild calls, safe for concurrent use.
type buildRecorder struct {
	mu      sync.Mutex
	records []buildRecord
}

func (r *buildRecorder) hook(scope *Scope, name string, instance any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, buildRecord{scope: scope.Name(), name: name, instance: instance})
}

func (r *buildRecorder) list() []buildRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]buildRecord(nil), r.records...)
}

func TestAfterBuildHook_Fires(t *testing.T) {
	t.Parallel()

	errBuild := errors.New("build failed")

	tests := []struct {
		name  string
		setup func(root *RootScope) (invoke func() any)
		times int
		check func(t *testing.T, records []buildRecord, instances []any)
	}{
		{
			name: "lazy",
			setup: func(root *RootScope) func() any {
				ProvideNamed(root, "db", func(Injector) (*buildDB, error) { return &buildDB{}, nil })
				return func() any { db, _ := InvokeNamed[*buildDB](root, "db"); return db }
			},
			times: 2,
			check: func(t *testing.T, records []buildRecord, instances []any) {
				t.Helper()
				require.Len(t, records, 1)
				assert.Equal(t, buildRecord{scope: "[root]", name: "db", instance: instances[0]}, records[0])
				assert.Same(t, instances[0], instances[1])
			},
		},
		{
			name: "transient",
			setup: func(root *RootScope) func() any {
				ProvideNamedTransient(root, "req", func(Injector) (*buildReq, error) { return &buildReq{}, nil })
				return func() any { req, _ := InvokeNamed[*buildReq](root, "req"); return req }
			},
			times: 2,
			check: func(t *testing.T, records []buildRecord, instances []any) {
				t.Helper()
				require.Len(t, records, 2)
				assert.Same(t, instances[0], records[0].instance)
				assert.Same(t, instances[1], records[1].instance)
			},
		},
		{
			name: "eager value",
			setup: func(root *RootScope) func() any {
				ProvideNamedValue(root, "cfg", &buildCfg{})
				return func() any { cfg, _ := InvokeNamed[*buildCfg](root, "cfg"); return cfg }
			},
			times: 2,
			check: func(t *testing.T, records []buildRecord, instances []any) {
				t.Helper()
				require.Len(t, records, 1)
				assert.Equal(t, buildRecord{scope: "[root]", name: "cfg", instance: instances[0]}, records[0])
			},
		},
		{
			name: "failed build",
			setup: func(root *RootScope) func() any {
				ProvideNamed(root, "bad", func(Injector) (*buildDB, error) { return nil, errBuild })
				return func() any { _, _ = InvokeNamed[*buildDB](root, "bad"); return nil }
			},
			times: 1,
			check: func(t *testing.T, records []buildRecord, _ []any) { t.Helper(); assert.Empty(t, records) },
		},
		{
			name: "panicking provider",
			setup: func(root *RootScope) func() any {
				ProvideNamed(root, "boom", func(Injector) (*buildDB, error) { panic("kaboom") })
				return func() any { _, _ = InvokeNamed[*buildDB](root, "boom"); return nil }
			},
			times: 1,
			check: func(t *testing.T, records []buildRecord, _ []any) { t.Helper(); assert.Empty(t, records) },
		},
		{
			name: "alias",
			setup: func(root *RootScope) func() any {
				ProvideNamed(root, "db", func(Injector) (*buildDB, error) { return &buildDB{}, nil })
				if err := AsNamed[*buildDB, buildDBIface](root, "db", "iface"); err != nil {
					panic(err)
				}
				return func() any { db, _ := InvokeNamed[buildDBIface](root, "iface"); return db }
			},
			times: 1,
			check: func(t *testing.T, records []buildRecord, instances []any) {
				t.Helper()
				require.Len(t, records, 1)
				assert.Equal(t, "db", records[0].name)
				assert.Same(t, instances[0], records[0].instance)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			rec := &buildRecorder{}
			root := NewWithOpts(&InjectorOpts{HookAfterBuild: []func(*Scope, string, any){rec.hook}})
			invoke := tt.setup(root)

			instances := make([]any, 0, tt.times)
			for range tt.times {
				instances = append(instances, invoke())
			}

			tt.check(t, rec.list(), instances)
		})
	}
}

func TestAfterBuildHook_Order(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	calls := &callRecorder{}
	root := NewWithOpts(&InjectorOpts{
		HookBeforeInvocation: []func(*Scope, string){func(_ *Scope, name string) { calls.record("before-invocation " + name) }},
		HookAfterBuild:       []func(*Scope, string, any){func(_ *Scope, name string, _ any) { calls.record("after-build " + name) }},
		HookAfterInvocation:  []func(*Scope, string, error){func(_ *Scope, name string, _ error) { calls.record("after-invocation " + name) }},
	})
	ProvideNamed(root, "db", func(Injector) (*buildDB, error) { return &buildDB{}, nil })

	_, err := InvokeNamed[*buildDB](root, "db")

	require.NoError(t, err)
	assert.Equal(t, []string{"before-invocation db", "after-build db", "after-invocation db"}, calls.list())
}

func TestAfterBuildHook_RegistrationOrder(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	calls := &callRecorder{}
	recordAs := func(label string) func(*Scope, string, any) {
		return func(_ *Scope, name string, _ any) { calls.record(label + " " + name) }
	}
	root := NewWithOpts(&InjectorOpts{HookAfterBuild: []func(*Scope, string, any){recordAs("h1"), nil, recordAs("h2")}})
	root.AddAfterBuildHook(recordAs("h3"))
	root.AddAfterBuildHook(nil)
	ProvideNamed(root, "db", func(Injector) (*buildDB, error) { return &buildDB{}, nil })

	_, err := InvokeNamed[*buildDB](root, "db")

	require.NoError(t, err)
	assert.Equal(t, []string{"h1 db", "h2 db", "h3 db"}, calls.list())
}

func TestAfterBuildHook_Scopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		container func(root *RootScope) *RootScope
	}{
		{name: "root", container: func(root *RootScope) *RootScope { return root }},
		{name: "clone", container: (*RootScope).Clone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			rec := &buildRecorder{}
			root := NewWithOpts(&InjectorOpts{HookAfterBuild: []func(*Scope, string, any){rec.hook}})
			request := root.Scope("request")
			ProvideNamed(request, "handler", func(Injector) (*buildReq, error) { return &buildReq{}, nil })

			container := tt.container(root)
			child, ok := container.ChildByName("request")
			require.True(t, ok)
			_, err := InvokeNamed[*buildReq](child, "handler")

			require.NoError(t, err)
			records := rec.list()
			require.Len(t, records, 1)
			assert.Equal(t, "request", records[0].scope)
		})
	}
}

func TestAfterBuildHook_Panic(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := NewWithOpts(&InjectorOpts{
		HookAfterBuild: []func(*Scope, string, any){func(*Scope, string, any) { panic("hook-boom") }},
	})
	ProvideNamed(root, "db", func(Injector) (*buildDB, error) { return &buildDB{}, nil })

	assert.PanicsWithValue(t, "hook-boom", func() { _, _ = InvokeNamed[*buildDB](root, "db") })
}

// buildHC is a health checker built by a lazy provider.
type buildHC struct{}

func (*buildHC) HealthCheck() error { return nil }

func TestAfterBuildHook_ReentersService(t *testing.T) {
	t.Parallel()

	reenter := map[string]func(scope *Scope, name string) error{
		"HealthCheckNamed": func(scope *Scope, name string) error { return HealthCheckNamed(scope, name) },
		"InvokeNamed": func(scope *Scope, name string) error {
			_, err := InvokeNamed[any](scope, name)
			return err
		},
	}

	tests := []struct {
		name    string
		service string
		call    string
		setup   func(root *RootScope)
		invoke  func(root *RootScope) (any, error)
	}{
		{
			name:    "lazy health checker, HealthCheckNamed",
			service: "hc",
			call:    "HealthCheckNamed",
			setup: func(root *RootScope) {
				ProvideNamed(root, "hc", func(Injector) (*buildHC, error) { return &buildHC{}, nil })
			},
			invoke: func(root *RootScope) (any, error) { return InvokeNamed[*buildHC](root, "hc") },
		},
		{
			name:    "lazy, InvokeNamed",
			service: "db",
			call:    "InvokeNamed",
			setup: func(root *RootScope) {
				ProvideNamed(root, "db", func(Injector) (*buildDB, error) { return &buildDB{}, nil })
			},
			invoke: func(root *RootScope) (any, error) { return InvokeNamed[*buildDB](root, "db") },
		},
		{
			name:    "eager value with DependsOn, InvokeNamed",
			service: "cfg",
			call:    "InvokeNamed",
			setup: func(root *RootScope) {
				ProvideNamed(root, "db", func(Injector) (*buildDB, error) { return &buildDB{}, nil })
				ProvideNamedValue(root, "cfg", &buildCfg{}, DependsOnNamed("db"))
			},
			invoke: func(root *RootScope) (any, error) { return InvokeNamed[*buildCfg](root, "cfg") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			var mu sync.Mutex
			recorded := map[string]error{}
			root := NewWithOpts(&InjectorOpts{HookAfterBuild: []func(*Scope, string, any){func(scope *Scope, name string, _ any) {
				err := reenter[tt.call](scope, name)
				mu.Lock()
				defer mu.Unlock()
				recorded[name] = err
			}}})
			tt.setup(root)

			instance, err := tt.invoke(root)

			is.NoError(err)
			is.NotNil(instance)
			mu.Lock()
			defer mu.Unlock()
			is.Contains(recorded, tt.service)
			is.NoError(recorded[tt.service])
		})
	}
}
