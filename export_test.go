package doger

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exClient is the exported service of the scope export tests.
type exClient struct{ label string }

// exOther is a service type that an *exClient is not assignable to.
type exOther struct{}

// clientProvider returns a provider of a client labelled label, counting its
// calls in calls when it is not nil.
func clientProvider(label string, calls *atomic.Int32) Provider[*exClient] {
	return func(Injector) (*exClient, error) {
		if calls != nil {
			calls.Add(1)
		}
		return &exClient{label: label}, nil
	}
}

// recoverPanic returns the value f panics with, or nil.
func recoverPanic(f func()) (recovered any) {
	defer func() { recovered = recover() }()
	f()

	return nil
}

func TestExport_Publishes(t *testing.T) {
	t.Parallel()

	clientName := NameOf[*exClient]()
	tests := []struct {
		name      string
		export    func(from *Scope) error
		published string
	}{
		{
			name:      "source_name",
			export:    func(from *Scope) error { return Export[*exClient](from) },
			published: clientName,
		},
		{
			name:      "export_as",
			export:    func(from *Scope) error { return Export[*exClient](from, ExportAs("cache-client")) },
			published: "cache-client",
		},
		{
			name:      "export_named_as",
			export:    func(from *Scope) error { return ExportNamed[*exClient](from, clientName, ExportAs("c")) },
			published: "c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			redis := root.Scope("redis")
			var calls atomic.Int32
			Provide(redis, clientProvider("client-1", &calls))
			require.NoError(t, tt.export(redis))

			published, err := InvokeNamed[*exClient](root, tt.published)
			require.NoError(t, err)
			owned, err := Invoke[*exClient](redis)
			require.NoError(t, err)

			assert.Equal(t, "client-1", published.label)
			assert.Same(t, owned, published)
			assert.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestExport_OthersStayPrivate(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	Provide(redis, clientProvider("client-1", nil))
	Provide(redis, func(Injector) (*dfPool, error) { return &dfPool{}, nil })
	require.NoError(t, Export[*exClient](redis))

	_, err := Invoke[*dfPool](root)
	assert.ErrorIs(t, err, ErrServiceNotFound)
}

func TestExport_Chained(t *testing.T) {
	t.Parallel()

	root := New()
	a := root.Scope("a")
	b := a.Scope("b")
	Provide(b, clientProvider("client-1", nil))
	require.NoError(t, Export[*exClient](b))
	require.NoError(t, Export[*exClient](a))

	client, err := Invoke[*exClient](root)
	require.NoError(t, err)
	assert.Equal(t, "client-1", client.label)
}

func TestExport_Invalid(t *testing.T) {
	t.Parallel()

	clientName := NameOf[*exClient]()
	otherName := NameOf[*exOther]()
	tests := []struct {
		name    string
		export  func(root *RootScope, redis *Scope) error
		message string
		absent  []string // names the root must not hold
	}{
		{
			name:    "nil_scope",
			export:  func(*RootScope, *Scope) error { return Export[*exClient](nil) },
			message: "DI: cannot export from a nil scope",
			absent:  []string{clientName},
		},
		{
			name:    "root_scope",
			export:  func(root *RootScope, _ *Scope) error { return Export[*exClient](root.self) },
			message: "has no parent scope to export to",
			absent:  []string{clientName},
		},
		{
			name:    "missing",
			export:  func(_ *RootScope, redis *Scope) error { return Export[*exMissing](redis) },
			message: fmt.Sprintf("DI: service `%s` has not been declared", NameOf[*exMissing]()),
			absent:  []string{NameOf[*exMissing]()},
		},
		{
			name:    "type_mismatch",
			export:  func(_ *RootScope, redis *Scope) error { return ExportNamed[*exOther](redis, clientName) },
			message: fmt.Sprintf("DI: service `%s` is not assignable to", clientName),
			absent:  []string{clientName},
		},
		{
			name:    "empty_name",
			export:  func(_ *RootScope, redis *Scope) error { return Export[*exClient](redis, ExportAs("")) },
			message: "DI: export name cannot be empty",
			absent:  []string{""},
		},
		{
			name:    "duplicate",
			export:  func(_ *RootScope, redis *Scope) error { return Export[*exClient](redis, ExportAs(otherName)) },
			message: fmt.Sprintf("DI: service `%s` has already been declared", otherName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			redis := root.Scope("redis")
			Provide(redis, clientProvider("client-1", nil))
			other := &exOther{}
			ProvideValue(root, other)

			err := tt.export(root, redis)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)

			for _, name := range tt.absent {
				assert.False(t, root.serviceExist(name), name)
			}
			unchanged, err := Invoke[*exOther](root)
			require.NoError(t, err)
			assert.Same(t, other, unchanged)
		})
	}
}

// exMissing is a service type that is never registered.
type exMissing struct{}

func TestExport_SourceMustBeOwn(t *testing.T) {
	t.Parallel()

	root := New()
	a := root.Scope("a")
	b := a.Scope("b")
	Provide(a, clientProvider("client-1", nil))

	err := Export[*exClient](b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("DI: service `%s` has not been declared", NameOf[*exClient]()))
}

func TestMustExport_Panics(t *testing.T) {
	t.Parallel()

	clientName := NameOf[*exClient]()
	tests := []struct {
		name string
		call func(from *Scope)
	}{
		{name: "must_export", call: func(from *Scope) { MustExport[*exClient](from) }},
		{name: "must_export_named", call: func(from *Scope) { MustExportNamed[*exClient](from, clientName) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			redis := New().Scope("redis")

			recovered := recoverPanic(func() { tt.call(redis) })
			err, ok := recovered.(error)
			require.True(t, ok, "expected an error panic, got %v", recovered)
			assert.Contains(t, err.Error(), fmt.Sprintf("DI: service `%s` has not been declared", clientName))
		})
	}
}

func TestExport_ResolvesFromSource(t *testing.T) {
	t.Parallel()

	for _, from := range []string{"root", "app", "redis"} {
		t.Run(from, func(t *testing.T) {
			t.Parallel()

			root := New()
			redis := root.Scope("redis")
			app := root.Scope("app")
			Provide(redis, clientProvider("redis-client", nil))
			Provide(app, clientProvider("app-client", nil))
			require.NoError(t, Export[*exClient](redis, ExportAs("shared")))

			injectors := map[string]Injector{"root": root, "app": app, "redis": redis}
			client, err := InvokeNamed[*exClient](injectors[from], "shared")
			require.NoError(t, err)
			assert.Equal(t, "redis-client", client.label)
		})
	}
}

func TestExport_ListedAsAlias(t *testing.T) {
	t.Parallel()

	clientName := NameOf[*exClient]()
	root := New()
	redis := root.Scope("redis")
	Provide(redis, clientProvider("client-1", nil))
	require.NoError(t, Export[*exClient](redis))
	_, err := Invoke[*exClient](root)
	require.NoError(t, err)

	assert.Contains(t, root.ListProvidedServices(), newServiceDescription(root.ID(), root.Name(), clientName))

	explained, ok := ExplainNamedService(root, clientName)
	require.True(t, ok)
	assert.Equal(t, ServiceTypeAlias, explained.ServiceType)
	require.Len(t, explained.Dependencies, 1)
	assert.Equal(t, "redis", explained.Dependencies[0].ScopeName)
	assert.Equal(t, clientName, explained.Dependencies[0].Service)
}

func TestExport_DirectChildOnly(t *testing.T) {
	t.Parallel()

	for range 100 {
		root := New()
		redis := root.Scope("redis")
		app := root.Scope("app")
		nested := app.Scope("redis")
		Provide(redis, clientProvider("direct", nil))
		Provide(nested, clientProvider("nested", nil))
		require.NoError(t, Export[*exClient](redis))

		client, err := Invoke[*exClient](root)
		require.NoError(t, err)
		require.Equal(t, "direct", client.label)
	}
}

func TestExport_ReplacesUnresolvedDefault(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	Provide(root, clientProvider("root-default", nil), AsDefault())
	Provide(redis, clientProvider("redis-client", nil))

	require.NoError(t, Export[*exClient](redis))

	client, err := Invoke[*exClient](root)
	require.NoError(t, err)
	assert.Equal(t, "redis-client", client.label)
}

func TestExport_ConcurrentFirstInvocation(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	var calls atomic.Int32
	Provide(redis, clientProvider("client-1", &calls))
	require.NoError(t, Export[*exClient](redis))

	const goroutines = 100
	clients := make([]*exClient, goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range goroutines {
		wg.Go(func() {
			<-start
			clients[index], errs[index] = Invoke[*exClient](root)
		})
	}
	close(start)
	wg.Wait()

	for index := range goroutines {
		require.NoError(t, errs[index])
		assert.Same(t, clients[0], clients[index])
	}
	assert.Equal(t, int32(1), calls.Load())
}

// exPool is an exported pool with Start, Shutdown and HealthCheck, each
// counting its calls and recording an event when record is set.
type exPool struct {
	healthErr error
	label     string // names the pool in events; "pool" when empty
	record    func(event string)
	starts    atomic.Int32
	shutdowns atomic.Int32
	checks    atomic.Int32
}

func (p *exPool) Start(context.Context) error {
	p.starts.Add(1)
	p.emit("start")
	return nil
}

func (p *exPool) Shutdown() error {
	p.shutdowns.Add(1)
	p.emit("shutdown")
	return nil
}

func (p *exPool) HealthCheck() error {
	p.checks.Add(1)
	return p.healthErr
}

func (p *exPool) emit(event string) {
	if p.record == nil {
		return
	}
	label := p.label
	if label == "" {
		label = "pool"
	}
	p.record(event + " " + label)
}

// exRecorder records lifecycle events in order, safely across goroutines.
type exRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *exRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *exRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// exApp is a root service that depends on the exported pool.
type exApp struct{ pool *exPool }

func TestExport_Lifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		before    func(root *RootScope)
		operation func(root *RootScope)
		count     func(pool *exPool) int32
	}{
		{
			name:      "start",
			before:    func(*RootScope) {},
			operation: func(root *RootScope) { require.NoError(t, root.Start()) },
			count:     func(pool *exPool) int32 { return pool.starts.Load() },
		},
		{
			name:      "shutdown",
			before:    func(root *RootScope) { require.NoError(t, root.Start()) },
			operation: func(root *RootScope) { assert.True(t, root.Shutdown().Succeed) },
			count:     func(pool *exPool) int32 { return pool.shutdowns.Load() },
		},
		{
			name:      "healthcheck",
			before:    func(*RootScope) {},
			operation: func(root *RootScope) { root.HealthCheck() },
			count:     func(pool *exPool) int32 { return pool.checks.Load() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			redis := root.Scope("redis")
			pool := &exPool{}
			Provide(redis, func(Injector) (*exPool, error) { return pool, nil })
			require.NoError(t, Export[*exPool](redis))
			Provide(root, func(i Injector) (*exApp, error) {
				p, err := Invoke[*exPool](i)
				return &exApp{pool: p}, err
			})
			_, err := Invoke[*exApp](root)
			require.NoError(t, err)

			tt.before(root)
			tt.operation(root)

			assert.Equal(t, int32(1), tt.count(pool))
		})
	}
}

func TestExport_RootHealthCheck(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	Provide(redis, func(Injector) (*exPool, error) { return &exPool{healthErr: errors.New("pool down")}, nil })
	_, err := Invoke[*exPool](redis)
	require.NoError(t, err)
	require.NoError(t, Export[*exPool](redis))

	results := root.HealthCheck()

	poolName := NameOf[*exPool]()
	require.Contains(t, results, poolName)
	assert.ErrorContains(t, results[poolName], "pool down")
}

func TestExport_SourceScopeHealthCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []ProvideOption
	}{
		{name: "explicit_source"},
		{name: "default_source", opts: []ProvideOption{AsDefault()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			redis := root.Scope("redis")
			pool := &exPool{healthErr: errors.New("pool down")}
			Provide(redis, func(Injector) (*exPool, error) { return pool, nil }, tt.opts...)
			_, err := Invoke[*exPool](redis)
			require.NoError(t, err)
			require.NoError(t, Export[*exPool](redis, ExportAs("pool")))

			results := redis.HealthCheck()

			poolName := NameOf[*exPool]()
			require.Contains(t, results, poolName)
			assert.ErrorContains(t, results[poolName], "pool down")
			assert.NotContains(t, results, "pool")
			assert.Equal(t, int32(1), pool.checks.Load())
		})
	}
}

func TestExport_TargetShutdownOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(root *RootScope, redis *Scope)
		calls     int32
	}{
		{name: "root", operation: func(root *RootScope, _ *Scope) { root.Shutdown() }, calls: 1},
		{name: "published_name", operation: func(root *RootScope, _ *Scope) { _ = ShutdownNamed(root, "pool") }, calls: 0},
		{name: "owning_scope", operation: func(_ *RootScope, redis *Scope) { redis.Shutdown() }, calls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			redis := root.Scope("redis")
			pool := &exPool{}
			Provide(redis, func(Injector) (*exPool, error) { return pool, nil })
			_, err := Invoke[*exPool](redis)
			require.NoError(t, err)
			require.NoError(t, Export[*exPool](redis, ExportAs("pool")))

			tt.operation(root, redis)

			assert.Equal(t, tt.calls, pool.shutdowns.Load())
		})
	}
}

// exServer is a root Starter that depends on the exported pool.
type exServer struct{ record func(string) }

func (s *exServer) Start(context.Context) error {
	s.record("start server")
	return nil
}

func TestExport_StartOrder(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	recorder := &exRecorder{}
	Provide(redis, func(Injector) (*exPool, error) { return &exPool{record: recorder.record}, nil })
	require.NoError(t, Export[*exPool](redis))
	Provide(root, func(i Injector) (*exServer, error) {
		if _, err := Invoke[*exPool](i); err != nil {
			return nil, err
		}
		return &exServer{record: recorder.record}, nil
	})

	require.NoError(t, root.Start())

	assert.Equal(t, []string{"start pool", "start server"}, recorder.list())
}

func TestExport_CollectionMembership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []ProvideOption
		from string
		keys []string
	}{
		{name: "root", from: "root", keys: []string{"pool"}},
		{name: "app", from: "app", keys: []string{"pool"}},
		{name: "redis", from: "redis", keys: []string{NameOf[*exPool]()}},
		{name: "default_source_root", opts: []ProvideOption{AsDefault()}, from: "root", keys: []string{"pool"}},
		{name: "default_source_redis", opts: []ProvideOption{AsDefault()}, from: "redis", keys: []string{NameOf[*exPool]()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			redis := root.Scope("redis")
			app := root.Scope("app")
			Provide(redis, func(Injector) (*exPool, error) { return &exPool{}, nil }, tt.opts...)
			require.NoError(t, Export[*exPool](redis, ExportAs("pool")))

			injectors := map[string]Injector{"root": root, "app": app, "redis": redis}
			members, err := InvokeAllMap[Healthchecker](injectors[tt.from])
			require.NoError(t, err)

			assert.ElementsMatch(t, tt.keys, slices.Collect(maps.Keys(members)))
		})
	}
}

func TestExport_InvokeAs(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	Provide(redis, func(Injector) (*exPool, error) { return &exPool{}, nil })
	require.NoError(t, Export[*exPool](redis))

	checker, err := InvokeAs[Healthchecker](root)
	require.NoError(t, err)
	owned, err := Invoke[*exPool](redis)
	require.NoError(t, err)
	assert.Same(t, owned, checker)
}

func TestExport_Cycle(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	Provide(redis, func(i Injector) (*exClient, error) {
		_, err := Invoke[*exApp](i)
		return &exClient{label: "client-1"}, err
	})
	require.NoError(t, Export[*exClient](redis))
	Provide(root, func(i Injector) (*exApp, error) {
		_, err := Invoke[*exClient](i)
		return &exApp{}, err
	})

	_, err := Invoke[*exApp](root)
	assert.ErrorIs(t, err, ErrCircularDependency)
}

func TestExport_Clone(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	Provide(redis, clientProvider("client-1", nil))
	require.NoError(t, Export[*exClient](redis))
	original, err := Invoke[*exClient](root)
	require.NoError(t, err)

	clone := root.Clone()
	cloned, err := Invoke[*exClient](clone)
	require.NoError(t, err)

	assert.NotSame(t, original, cloned)
	clonedRedis, ok := clone.ChildByName("redis")
	require.True(t, ok)
	builtByClone, err := Invoke[*exClient](clonedRedis)
	require.NoError(t, err)
	assert.Same(t, builtByClone, cloned)
}

func TestExport_AfterSourceShutdown(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	Provide(redis, clientProvider("client-1", nil))
	require.NoError(t, Export[*exClient](redis))
	_, err := Invoke[*exClient](root)
	require.NoError(t, err)

	redis.Shutdown()

	_, err = Invoke[*exClient](root)
	require.ErrorIs(t, err, ErrServiceNotFound)
	assert.Contains(t, err.Error(), "redis")
}

func TestExport_DefaultSource(t *testing.T) {
	t.Parallel()

	appClient := func(root *RootScope) { Provide(root, clientProvider("app-client", nil)) }
	tests := []struct {
		name     string
		register func(root *RootScope, redis *Scope)
		from     string
		label    string
	}{
		{name: "export_only_root", register: func(_ *RootScope, redis *Scope) { MustExport[*exClient](redis) }, from: "root", label: "ext-client"},
		{name: "export_only_redis", register: func(_ *RootScope, redis *Scope) { MustExport[*exClient](redis) }, from: "redis", label: "ext-client"},
		{name: "app_first_root", register: func(root *RootScope, redis *Scope) { appClient(root); MustExport[*exClient](redis) }, from: "root", label: "app-client"},
		{name: "app_first_redis", register: func(root *RootScope, redis *Scope) { appClient(root); MustExport[*exClient](redis) }, from: "redis", label: "app-client"},
		{name: "app_after_root", register: func(root *RootScope, redis *Scope) { MustExport[*exClient](redis); appClient(root) }, from: "root", label: "app-client"},
		{name: "app_after_redis", register: func(root *RootScope, redis *Scope) { MustExport[*exClient](redis); appClient(root) }, from: "redis", label: "app-client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			redis := root.Scope("redis")
			Provide(redis, clientProvider("ext-client", nil), AsDefault())
			require.Nil(t, recoverPanic(func() { tt.register(root, redis) }))

			injectors := map[string]Injector{"root": root, "redis": redis}
			client, err := Invoke[*exClient](injectors[tt.from])
			require.NoError(t, err)
			assert.Equal(t, tt.label, client.label)
		})
	}
}

func TestExport_DefaultOverExplicitSkipped(t *testing.T) {
	t.Parallel()

	root := New()
	redis := root.Scope("redis")
	Provide(redis, clientProvider("ext-client", nil), AsDefault())
	Provide(root, clientProvider("app-client", nil))
	explicit, ok := root.serviceGet(NameOf[*exClient]())
	require.True(t, ok)

	require.NoError(t, Export[*exClient](redis))

	current, ok := root.serviceGet(NameOf[*exClient]())
	require.True(t, ok)
	assert.Same(t, explicit, current)
}
