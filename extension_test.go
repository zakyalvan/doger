package doger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extPool and extClient are the services the test extensions register.
type (
	extPool   struct{}
	extClient struct{ addr string }
)

// testExtension is an extension whose identity and Extend are set by each
// test; a nil extend succeeds without registering anything.
type testExtension struct {
	name     string
	instance string
	extend   func(x *ExtensionContext) error
}

func (e *testExtension) Name() string     { return e.name }
func (e *testExtension) Instance() string { return e.instance }

func (e *testExtension) Extend(x *ExtensionContext) error {
	if e.extend == nil {
		return nil
	}

	return e.extend(x)
}

// provideExtPool registers a lazy *extPool in i.
func provideExtPool(i Injector) {
	Provide(i, func(Injector) (*extPool, error) { return &extPool{}, nil })
}

// provideExtClient registers a lazy *extClient in i.
func provideExtClient(i Injector) {
	Provide(i, func(Injector) (*extClient, error) { return &extClient{}, nil })
}

// childNames returns the names of the direct child scopes of root.
func childNames(root *RootScope) []string {
	children := root.Children()
	names := make([]string, 0, len(children))
	for _, child := range children {
		names = append(names, child.Name())
	}

	return names
}

func TestExtend_PrivateLayout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		instance string
		scope    string
	}{
		{name: "no_instance", instance: "", scope: "ext:redis"},
		{name: "instance", instance: "cache", scope: "ext:redis/cache"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			ext := &testExtension{name: "redis", instance: tt.instance, extend: func(x *ExtensionContext) error {
				provideExtPool(x.Scope())
				provideExtClient(x.Scope())
				return ExtensionExport[*extClient](x)
			}}

			require.NoError(t, Extend(root, ext))

			assert.Equal(t, []string{tt.scope}, childNames(root))
			_, err := Invoke[*extClient](root)
			require.NoError(t, err)
			_, err = Invoke[*extPool](root)
			require.ErrorIs(t, err, ErrServiceNotFound)
		})
	}
}

func TestExtend_Context(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		instance string
		scope    string
	}{
		{name: "no_instance", instance: "", scope: "ext:redis"},
		{name: "instance", instance: "cache", scope: "ext:redis/cache"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			var (
				gotRoot     *RootScope
				gotInstance string
				gotScope    string
			)
			ext := &testExtension{name: "redis", instance: tt.instance, extend: func(x *ExtensionContext) error {
				gotRoot, gotInstance, gotScope = x.Root(), x.Instance(), x.Scope().Name()
				return nil
			}}

			require.NoError(t, Extend(root, ext))

			assert.Same(t, root, gotRoot)
			assert.Equal(t, tt.instance, gotInstance)
			assert.Equal(t, tt.scope, gotScope)
		})
	}
}

// TestExtend_NilRoot is not parallel: it replaces DefaultRootScope.
func TestExtend_NilRoot(t *testing.T) {
	previous := DefaultRootScope
	DefaultRootScope = New()
	t.Cleanup(func() { DefaultRootScope = previous })

	require.NoError(t, Extend(nil, &testExtension{name: "redis"}))

	assert.Equal(t, []string{"ext:redis"}, childNames(DefaultRootScope))
}

func TestExtend_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ext     Extension
		opts    []ExtendOption
		message string
		named   bool // the error is an *ExtensionError naming "redis"
	}{
		{name: "nil", ext: nil, message: "extension is nil"},
		{name: "typed_nil", ext: (*testExtension)(nil), message: "extension is nil"},
		{name: "trailing_dash", ext: &testExtension{name: "redis-"}, message: "invalid extension name"},
		{name: "double_dash", ext: &testExtension{name: "a--b"}, message: "invalid extension name"},
		{name: "empty_name", ext: &testExtension{name: ""}, message: "extension name is empty"},
		{name: "bad_name", ext: &testExtension{name: "Redis Cache"}, message: "invalid extension name"},
		{name: "bad_instance", ext: &testExtension{name: "redis", instance: "cache.v2"}, message: "invalid instance key", named: true},
		{name: "empty_scope_name", ext: &testExtension{name: "redis"}, opts: []ExtendOption{ExtendScopeName("")}, message: "scope name is empty", named: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()

			var err error
			assert.NotPanics(t, func() { err = Extend(root, tt.ext, tt.opts...) })

			require.ErrorIs(t, err, ErrInvalidExtension)
			assert.Contains(t, err.Error(), tt.message)
			assert.Empty(t, childNames(root))
			var extErr *ExtensionError
			if assert.Equal(t, tt.named, errors.As(err, &extErr)) && tt.named {
				assert.Equal(t, "redis", extErr.Name)
			}
		})
	}
}

// placedExtension is a testExtension declaring its placement.
type placedExtension struct {
	testExtension
	placement Placement
}

func (e *placedExtension) Placement() Placement { return e.placement }

func TestExtend_Placement(t *testing.T) {
	t.Parallel()

	poolName := NameOf[*extPool]()
	tests := []struct {
		name      string
		placement *Placement // nil: no Placement method
		opts      []ExtendOption
		scope     string // the child scope holding the pool; empty: the root's own scope
	}{
		{name: "default", scope: "ext:redis"},
		{name: "author_private", placement: new(PlacementPrivate), scope: "ext:redis"},
		{name: "author_flat", placement: new(PlacementFlat), scope: ""},
		{name: "flatten", opts: []ExtendOption{Flatten()}, scope: ""},
		{name: "flat_ignores_scope_name", placement: new(PlacementFlat), opts: []ExtendOption{ExtendScopeName("cache")}, scope: ""},
		{name: "scope_name", opts: []ExtendOption{ExtendScopeName("cache")}, scope: "cache"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			base := testExtension{name: "redis", extend: func(x *ExtensionContext) error {
				provideExtPool(x.Scope())
				return ExtensionExport[*extPool](x)
			}}
			var ext Extension = &base
			if tt.placement != nil {
				ext = &placedExtension{testExtension: base, placement: *tt.placement}
			}

			require.NoError(t, Extend(root, ext, tt.opts...))

			if tt.scope == "" {
				assert.Empty(t, childNames(root))
				assert.True(t, root.self.serviceExist(poolName))
			} else {
				assert.Equal(t, []string{tt.scope}, childNames(root))
				assert.True(t, root.Children()[0].serviceExist(poolName))
			}
			_, err := Invoke[*extPool](root)
			require.NoError(t, err)
		})
	}
}

// redisExtension is a comparable extension named "redis", configured by
// plain data like a real extension; calls counts its Extend calls and is
// shared by the values a test compares.
type redisExtension struct {
	Addr        string
	InstanceKey string
	calls       *atomic.Int32
}

func (e redisExtension) Name() string     { return "redis" }
func (e redisExtension) Instance() string { return e.InstanceKey }

func (e redisExtension) Extend(x *ExtensionContext) error {
	e.calls.Add(1)
	addr := e.Addr
	Provide(x.Scope(), func(Injector) (*extClient, error) { return &extClient{addr: addr}, nil })
	if e.InstanceKey != "" { // each instance publishes its own client
		return ExtensionExport[*extClient](x, ExportAs(NameOf[*extClient]()+"#"+e.InstanceKey))
	}
	return ExtensionExport[*extClient](x)
}

// otherRedisExtension is another extension type also named "redis".
type otherRedisExtension struct{}

func (otherRedisExtension) Name() string                   { return "redis" }
func (otherRedisExtension) Extend(*ExtensionContext) error { return nil }

// extendNoPanic calls Extend, failing the test instead of panicking.
func extendNoPanic(t *testing.T, root *RootScope, ext Extension, opts ...ExtendOption) (err error) {
	t.Helper()
	assert.NotPanics(t, func() { err = Extend(root, ext, opts...) })

	return err
}

func TestExtend_Repeat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		first    func(calls *atomic.Int32) Extension
		second   func(calls *atomic.Int32) Extension
		opts     []ExtendOption
		conflict bool
		mentions string // in the conflict error
		children []string
		calls    int32 // Extend calls of redisExtension values, both installs together
	}{
		{
			name:     "equal",
			first:    func(c *atomic.Int32) Extension { return &redisExtension{Addr: "a:1", calls: c} },
			second:   func(c *atomic.Int32) Extension { return &redisExtension{Addr: "a:1", calls: c} },
			children: []string{"ext:redis"},
			calls:    1,
		},
		{
			name:     "unequal",
			first:    func(c *atomic.Int32) Extension { return &redisExtension{Addr: "a:1", calls: c} },
			second:   func(c *atomic.Int32) Extension { return &redisExtension{Addr: "b:2", calls: c} },
			conflict: true,
			children: []string{"ext:redis"},
			calls:    1,
		},
		{
			name:     "other_instance",
			first:    func(c *atomic.Int32) Extension { return &redisExtension{InstanceKey: "cache", calls: c} },
			second:   func(c *atomic.Int32) Extension { return &redisExtension{InstanceKey: "queue", calls: c} },
			children: []string{"ext:redis/cache", "ext:redis/queue"},
			calls:    2, // the first's Extend once, and the second's once
		},
		{
			name:     "same_name_other_type",
			first:    func(c *atomic.Int32) Extension { return &redisExtension{calls: c} },
			second:   func(*atomic.Int32) Extension { return &otherRedisExtension{} },
			conflict: true,
			mentions: "ext:redis",
			children: []string{"ext:redis"},
			calls:    1,
		},
		{
			name:     "other_options",
			first:    func(c *atomic.Int32) Extension { return &redisExtension{calls: c} },
			second:   func(c *atomic.Int32) Extension { return &redisExtension{calls: c} },
			opts:     []ExtendOption{Flatten()},
			conflict: true,
			children: []string{"ext:redis"},
			calls:    1,
		},
		{
			name:     "pointer_vs_value",
			first:    func(c *atomic.Int32) Extension { return &redisExtension{calls: c} },
			second:   func(c *atomic.Int32) Extension { return redisExtension{calls: c} },
			conflict: true,
			mentions: "ext:redis",
			children: []string{"ext:redis"},
			calls:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			var calls atomic.Int32
			require.NoError(t, extendNoPanic(t, root, tt.first(&calls)))

			err := extendNoPanic(t, root, tt.second(&calls), tt.opts...)

			if tt.conflict {
				require.ErrorIs(t, err, ErrExtensionConflict)
				assert.Contains(t, err.Error(), tt.mentions)
			} else {
				require.NoError(t, err)
			}
			assert.ElementsMatch(t, tt.children, childNames(root))
			assert.Equal(t, tt.calls, calls.Load())
		})
	}
}

func TestExtend_ScopeNameResolvesCollision(t *testing.T) {
	t.Parallel()

	root := New()
	var calls atomic.Int32
	require.NoError(t, extendNoPanic(t, root, &redisExtension{calls: &calls}))

	err := extendNoPanic(t, root, &otherRedisExtension{}, ExtendScopeName("other-redis"))

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ext:redis", "other-redis"}, childNames(root))
}

func TestExtend_Concurrent(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	var calls atomic.Int32
	ext := &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond) // keep the install running while the others arrive
		provideExtClient(x.Scope())
		return ExtensionExport[*extClient](x)
	}}

	const goroutines = 50
	errs := make([]error, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range goroutines {
		wg.Go(func() {
			<-start
			errs[index] = extendNoPanic(t, root, ext)
		})
	}
	close(start)
	wg.Wait()

	for index, err := range errs {
		assert.NoError(t, err, "goroutine %d", index)
	}
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, []string{"ext:redis"}, childNames(root))
}

// funcOptionExtension is an extension holding a func option, which
// reflect.DeepEqual never finds equal when non-nil.
type funcOptionExtension struct {
	onConnect func()
}

func (*funcOptionExtension) Name() string                   { return "redis" }
func (*funcOptionExtension) Extend(*ExtensionContext) error { return nil }

func TestExtend_FuncOptionConflicts(t *testing.T) {
	t.Parallel()

	root := New()
	onConnect := func() {}
	require.NoError(t, extendNoPanic(t, root, &funcOptionExtension{onConnect: onConnect}))

	err := extendNoPanic(t, root, &funcOptionExtension{onConnect: onConnect})

	require.ErrorIs(t, err, ErrExtensionConflict)
}

func TestExtend_FailureRollback(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	tests := []struct {
		name  string
		fail  func() error
		check func(t *testing.T, err error)
	}{
		{
			name:  "error",
			fail:  func() error { return errors.New("dial failed") },
			check: func(t *testing.T, err error) { t.Helper(); assert.Contains(t, err.Error(), "dial failed") },
		},
		{
			name:  "error_panic",
			fail:  func() error { panic(boom) },
			check: func(t *testing.T, err error) { t.Helper(); assert.ErrorIs(t, err, boom) },
		},
		{
			name:  "string_panic",
			fail:  func() error { panic("boom") },
			check: func(t *testing.T, err error) { t.Helper(); assert.Contains(t, err.Error(), "boom") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			var attempts atomic.Int32
			ext := &testExtension{name: "redis", instance: "cache", extend: func(x *ExtensionContext) error {
				provideExtPool(x.Scope())
				if err := ExtensionExport[*extPool](x); err != nil {
					return err
				}
				if attempts.Add(1) == 1 {
					return tt.fail()
				}
				return nil
			}}

			err := extendNoPanic(t, root, ext)

			var extErr *ExtensionError
			require.ErrorAs(t, err, &extErr)
			assert.Equal(t, "redis", extErr.Name)
			assert.Equal(t, "cache", extErr.Instance)
			tt.check(t, err)
			assert.Empty(t, childNames(root))
			assert.False(t, root.self.serviceExist(NameOf[*extPool]()))

			require.NoError(t, extendNoPanic(t, root, ext))
			_, err = Invoke[*extPool](root)
			require.NoError(t, err)
		})
	}
}

func TestExtend_FailedExportRollsBackExports(t *testing.T) {
	t.Parallel()

	root := New()
	ext := &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		provideExtPool(x.Scope())
		if err := ExtensionExport[*extPool](x); err != nil {
			return err
		}
		return ExtensionExport[*extClient](x) // never registered: this export fails
	}}

	err := extendNoPanic(t, root, ext)

	var extErr *ExtensionError
	require.ErrorAs(t, err, &extErr)
	assert.Empty(t, childNames(root))
	assert.False(t, root.self.serviceExist(NameOf[*extPool]()))
}

func TestExtend_FlattenNoRollback(t *testing.T) {
	t.Parallel()

	root := New()
	ext := &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		provideExtPool(x.Scope())
		return errors.New("dial failed")
	}}

	err := extendNoPanic(t, root, ext, Flatten())

	require.ErrorContains(t, err, "dial failed")
	assert.True(t, root.self.serviceExist(NameOf[*extPool]()))
}

// extShutdownPool is a pool counting its Shutdown calls.
type extShutdownPool struct{ shutdowns *atomic.Int32 }

func (p *extShutdownPool) Shutdown() error {
	p.shutdowns.Add(1)
	return nil
}

// hookEvents records hook calls as "<hook> <scope> [<service>]" lines.
type hookEvents struct {
	mu     sync.Mutex
	events []string
}

func (h *hookEvents) add(event string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
}

func (h *hookEvents) list() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.events...)
}

// opts returns options whose registration, shutdown and scope closing hooks
// record into h.
func (h *hookEvents) opts() *InjectorOpts {
	return &InjectorOpts{
		HookAfterRegistration: []func(*Scope, string){func(scope *Scope, name string) { h.add("registered " + scope.Name() + " " + name) }},
		HookBeforeShutdown:    []func(*Scope, string){func(scope *Scope, name string) { h.add("before-shutdown " + scope.Name() + " " + name) }},
		HookAfterShutdown:     []func(*Scope, string, error){func(scope *Scope, name string, _ error) { h.add("after-shutdown " + scope.Name() + " " + name) }},
		HookScopeClosing:      []func(context.Context, *Scope){func(_ context.Context, scope *Scope) { h.add("scope-closing " + scope.Name()) }},
		HookScopeClosed:       []func(context.Context, *Scope){func(_ context.Context, scope *Scope) { h.add("scope-closed " + scope.Name()) }},
	}
}

func TestExtend_RollbackFiresHooks(t *testing.T) {
	t.Parallel()

	var events hookEvents
	root := NewWithOpts(events.opts())
	var shutdowns atomic.Int32
	poolName := NameOf[*extShutdownPool]()
	ext := &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		Provide(x.Scope(), func(Injector) (*extShutdownPool, error) { return &extShutdownPool{shutdowns: &shutdowns}, nil })
		if _, err := Invoke[*extShutdownPool](x.Scope()); err != nil {
			return err
		}
		return errors.New("dial failed")
	}}

	require.Error(t, extendNoPanic(t, root, ext))

	assert.Equal(t, []string{
		"registered ext:redis " + poolName,
		"scope-closing ext:redis",
		"before-shutdown ext:redis " + poolName,
		"after-shutdown ext:redis " + poolName,
		"scope-closed ext:redis",
	}, events.list())
	assert.Equal(t, int32(1), shutdowns.Load())
}

// logLines records the lines a container logs.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// containing returns the recorded lines containing text.
func (l *logLines) containing(text string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	found := []string{}
	for _, line := range l.lines {
		if strings.Contains(line, text) {
			found = append(found, line)
		}
	}

	return found
}

// typedExtension is a testExtension of its own type for each K, so that
// extensions of different K have different identities.
type typedExtension[K any] struct{ testExtension }

// Markers of the typedExtension types.
type (
	kindA       struct{}
	kindB       struct{}
	kindCache   struct{}
	kindSession struct{}
	kindMetrics struct{}
	kindTracing struct{}
)

// newTypedExtension returns an extension of type K named name.
func newTypedExtension[K any](name string, extend func(x *ExtensionContext) error) *typedExtension[K] {
	return &typedExtension[K]{testExtension{name: name, extend: extend}}
}

// disabledExtension is an extension of type K named name that registers a
// service and then declines to install.
func disabledExtension[K any](name string) *typedExtension[K] {
	return newTypedExtension[K](name, func(x *ExtensionContext) error {
		provideExtPool(x.Scope())
		return fmt.Errorf("off by config: %w", ErrExtensionDisabled)
	})
}

// requiringExtension is an extension of type K named name whose Extend
// requires dep and returns the error of Require.
func requiringExtension[K any](name string, dep Extension) *typedExtension[K] {
	return newTypedExtension[K](name, func(x *ExtensionContext) error { return x.Require(dep) })
}

func TestExtend_Disabled(t *testing.T) {
	t.Parallel()

	var logs logLines
	root := NewWithOpts(&InjectorOpts{Logf: logs.logf})

	err := extendNoPanic(t, root, disabledExtension[kindMetrics]("metrics"))

	require.NoError(t, err)
	assert.Len(t, logs.containing("extension metrics disabled"), 1)
	assert.Empty(t, childNames(root))
}

func TestRequire_Disabled(t *testing.T) {
	t.Parallel()

	root := New()
	metrics := disabledExtension[kindMetrics]("metrics")
	require.NoError(t, extendNoPanic(t, root, metrics))

	err := extendNoPanic(t, root, requiringExtension[kindTracing]("tracing", metrics))

	require.ErrorIs(t, err, ErrRequiredExtensionDisabled)
	require.NotErrorIs(t, err, ErrExtensionDisabled)
	assert.Contains(t, err.Error(), "metrics")
	assert.Empty(t, childNames(root))
}

func TestRequire_Idempotent(t *testing.T) {
	t.Parallel()

	root := New()
	var calls atomic.Int32

	errCache := extendNoPanic(t, root, requiringExtension[kindCache]("cache", &redisExtension{calls: &calls}))
	errSession := extendNoPanic(t, root, requiringExtension[kindSession]("session", &redisExtension{calls: &calls}))

	require.NoError(t, errCache)
	require.NoError(t, errSession)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRequire_Cycle(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	a := newTypedExtension[kindA]("a", nil)
	b := requiringExtension[kindB]("b", a)
	a.extend = func(x *ExtensionContext) error { return x.Require(b) }

	err := extendNoPanic(t, root, a)

	require.ErrorIs(t, err, ErrExtensionCycle)
	assert.Contains(t, err.Error(), "a -> b -> a")
	assert.Empty(t, childNames(root))
}

func TestRequire_CrossGoroutineCycle(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	aStarted, bStarted := make(chan struct{}), make(chan struct{})
	signalA, signalB := sync.OnceFunc(func() { close(aStarted) }), sync.OnceFunc(func() { close(bStarted) })
	a := newTypedExtension[kindA]("a", nil)
	b := newTypedExtension[kindB]("b", nil)
	a.extend = func(x *ExtensionContext) error {
		signalA()
		<-bStarted
		return x.Require(b)
	}
	b.extend = func(x *ExtensionContext) error {
		signalB()
		<-aStarted
		return x.Require(a)
	}

	var errA, errB error
	var wg sync.WaitGroup
	wg.Go(func() { errA = extendNoPanic(t, root, a) })
	wg.Go(func() { errB = extendNoPanic(t, root, b) })
	wg.Wait()

	assert.True(t, errors.Is(errA, ErrExtensionCycle) || errors.Is(errB, ErrExtensionCycle), "errA: %v, errB: %v", errA, errB)
}

func TestExtend_AfterRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		step     func(t *testing.T, root *RootScope)
		conflict bool
		addr     string
	}{
		{
			name: "unresolved_replaces",
			step: func(*testing.T, *RootScope) {},
			addr: "prod:6379",
		},
		{
			name: "unrelated_root_invoked",
			step: func(t *testing.T, root *RootScope) {
				t.Helper()
				ProvideValue(root, &extPool{})
				_, err := Invoke[*extPool](root)
				require.NoError(t, err)
			},
			addr: "prod:6379",
		},
		{
			name: "resolved_conflicts",
			step: func(t *testing.T, root *RootScope) {
				t.Helper()
				_, err := Invoke[*extClient](root)
				require.NoError(t, err)
			},
			conflict: true,
			addr:     "default:6379",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			var calls atomic.Int32
			require.NoError(t, extendNoPanic(t, root, requiringExtension[kindCache]("cache", &redisExtension{Addr: "default:6379", calls: &calls})))
			tt.step(t, root)

			err := extendNoPanic(t, root, &redisExtension{Addr: "prod:6379", calls: &calls})

			if tt.conflict {
				require.ErrorIs(t, err, ErrExtensionConflict)
			} else {
				require.NoError(t, err)
			}
			client, err := Invoke[*extClient](root)
			require.NoError(t, err)
			assert.Equal(t, tt.addr, client.addr)
		})
	}
}

func TestExtensionContext_AfterExtend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, x *ExtensionContext, logs *logLines)
	}{
		{
			name: "export",
			check: func(t *testing.T, x *ExtensionContext, _ *logLines) {
				t.Helper()
				require.ErrorIs(t, ExtensionExport[*extPool](x), ErrInvalidExtension)
			},
		},
		{
			name: "require",
			check: func(t *testing.T, x *ExtensionContext, _ *logLines) {
				t.Helper()
				require.ErrorIs(t, x.Require(newTypedExtension[kindB]("other", nil)), ErrInvalidExtension)
			},
		},
		{
			name: "properties",
			check: func(t *testing.T, x *ExtensionContext, logs *logLines) {
				t.Helper()
				before := len(logs.containing("redis"))

				value, ok := x.Properties().Lookup("addr")

				assert.False(t, ok)
				assert.Empty(t, value)
				assert.Len(t, logs.containing("redis"), before+1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var logs logLines
			root := NewWithOpts(&InjectorOpts{Logf: logs.logf, PropertySource: mapSource{"redis.addr": "a:1"}})
			var stored *ExtensionContext
			require.NoError(t, extendNoPanic(t, root, &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
				stored = x
				return nil
			}}))

			tt.check(t, stored, &logs)
		})
	}
}

// extStarterPool is a pool counting its Start calls.
type extStarterPool struct{ starts *atomic.Int32 }

func (p *extStarterPool) Start(context.Context) error {
	p.starts.Add(1)
	return nil
}

// failingStarter is a root service whose Start fails.
type failingStarter struct{}

func (failingStarter) Start(context.Context) error { return errors.New("start failed") }

// starterExtension is an extension "redis" registering a lazy
// *extStarterPool, counting its starts in starts, and exporting it.
func starterExtension(starts *atomic.Int32) Extension {
	return &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		Provide(x.Scope(), func(Injector) (*extStarterPool, error) { return &extStarterPool{starts: starts}, nil })
		return ExtensionExport[*extStarterPool](x)
	}}
}

func TestExtend_Phases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, starts *atomic.Int32)
	}{
		{
			name: "idle",
			run: func(t *testing.T, starts *atomic.Int32) {
				t.Helper()
				require.NoError(t, Extend(New(), starterExtension(starts)))
			},
		},
		{
			name: "starting",
			run: func(t *testing.T, starts *atomic.Int32) {
				t.Helper()
				var extendErr error
				root := NewWithOpts(&InjectorOpts{HookContainerStarting: []func(context.Context, *RootScope) error{
					func(_ context.Context, root *RootScope) error {
						extendErr = Extend(root, starterExtension(starts))
						return nil
					},
				}})
				require.NoError(t, root.Start())
				require.NoError(t, extendErr)
				assert.Equal(t, int32(1), starts.Load())
			},
		},
		{
			name: "started",
			run: func(t *testing.T, starts *atomic.Int32) {
				t.Helper()
				var extendErr error
				root := NewWithOpts(&InjectorOpts{HookContainerStarted: []func(context.Context, *RootScope){
					func(_ context.Context, root *RootScope) { extendErr = Extend(root, starterExtension(starts)) },
				}})
				require.NoError(t, root.Start())
				require.NoError(t, extendErr)
				require.NoError(t, root.Refresh())
				assert.Equal(t, int32(1), starts.Load())
			},
		},
		{
			name: "ready",
			run: func(t *testing.T, starts *atomic.Int32) {
				t.Helper()
				root := New()
				require.NoError(t, root.Start())
				require.NoError(t, Extend(root, starterExtension(starts)))
				require.NoError(t, root.Refresh())
				assert.Equal(t, int32(1), starts.Load())
			},
		},
		{
			name: "failed",
			run: func(t *testing.T, starts *atomic.Int32) {
				t.Helper()
				root := New()
				ProvideValue(root, failingStarter{})
				require.Error(t, root.Start())
				require.NoError(t, Extend(root, starterExtension(starts)))
				require.ErrorIs(t, root.Refresh(), ErrNotStarted)
				assert.Equal(t, int32(0), starts.Load())
			},
		},
		{
			name: "closing",
			run: func(t *testing.T, starts *atomic.Int32) {
				t.Helper()
				var extendErr error
				root := NewWithOpts(&InjectorOpts{HookContainerClosing: []func(context.Context, *RootScope){
					func(_ context.Context, root *RootScope) { extendErr = Extend(root, starterExtension(starts)) },
				}})
				root.Shutdown()
				require.ErrorIs(t, extendErr, ErrStartAfterShutdown)
				assert.Empty(t, childNames(root))
			},
		},
		{
			name: "closed",
			run: func(t *testing.T, starts *atomic.Int32) {
				t.Helper()
				root := New()
				root.Shutdown()
				require.ErrorIs(t, Extend(root, starterExtension(starts)), ErrStartAfterShutdown)
				assert.Empty(t, childNames(root))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var starts atomic.Int32
			tt.run(t, &starts)
		})
	}
}

// extCache is a service of the redis test extension using its client.
type extCache struct{ client *extClient }

// yieldingExtension is an extension "redis" registering a default client
// labelled "ext-client" and a cache using the client, and exporting both.
func yieldingExtension() Extension {
	return &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		Provide(x.Scope(), func(Injector) (*extClient, error) { return &extClient{addr: "ext-client"}, nil }, AsDefault())
		Provide(x.Scope(), func(i Injector) (*extCache, error) {
			client, err := Invoke[*extClient](i)
			return &extCache{client: client}, err
		})
		if err := ExtensionExport[*extClient](x); err != nil {
			return err
		}
		return ExtensionExport[*extCache](x)
	}}
}

func TestExtend_AppServiceWins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		before bool // the application registers its client before Extend
	}{
		{name: "before", before: true},
		{name: "after", before: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			provideApp := func() { ProvideValue(root, &extClient{addr: "app-client"}) }
			if tt.before {
				provideApp()
			}
			require.NoError(t, Extend(root, yieldingExtension()))
			if !tt.before {
				provideApp()
			}

			cache, err := Invoke[*extCache](root)
			require.NoError(t, err)
			client, err := Invoke[*extClient](root)
			require.NoError(t, err)

			assert.Equal(t, "app-client", cache.client.addr)
			assert.Equal(t, "app-client", client.addr)
		})
	}
}

// extCheckedClient is a client counting its health checks.
type extCheckedClient struct{ checks *atomic.Int32 }

func (c *extCheckedClient) HealthCheck() error {
	c.checks.Add(1)
	return nil
}

func TestExtend_AppServiceCheckedOnce(t *testing.T) {
	t.Parallel()

	root := New()
	var appChecks, extChecks atomic.Int32
	clientName := NameOf[*extCheckedClient]()
	ProvideValue(root, &extCheckedClient{checks: &appChecks})
	require.NoError(t, Extend(root, &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		Provide(x.Scope(), func(Injector) (*extCheckedClient, error) { return &extCheckedClient{checks: &extChecks}, nil }, AsDefault())
		Provide(x.Scope(), func(i Injector) (*extCache, error) {
			_, err := Invoke[*extCheckedClient](i)
			return &extCache{}, err
		})
		if err := ExtensionExport[*extCheckedClient](x); err != nil {
			return err
		}
		return ExtensionExport[*extCache](x)
	}}))
	_, err := Invoke[*extCache](root)
	require.NoError(t, err)

	health := root.HealthCheck()

	assert.Equal(t, int32(1), appChecks.Load())
	assert.Equal(t, int32(0), extChecks.Load())
	assert.Contains(t, health, clientName)
	assert.NotContains(t, health, "ext:redis/"+clientName)
}

// extLifecyclePool is a pool counting its starts, health checks and
// shutdowns; its health check fails.
type extLifecyclePool struct{ starts, checks, shutdowns atomic.Int32 }

func (p *extLifecyclePool) Start(context.Context) error { p.starts.Add(1); return nil }
func (p *extLifecyclePool) HealthCheck() error          { p.checks.Add(1); return errors.New("pool down") }
func (p *extLifecyclePool) Shutdown() error             { p.shutdowns.Add(1); return nil }

// extApp uses the pool and records whether it was closed at its own
// shutdown.
type extApp struct {
	pool       *extLifecyclePool
	sawClosed  atomic.Bool
	shutdowned atomic.Bool
}

func (a *extApp) Shutdown() error {
	a.sawClosed.Store(a.pool.shutdowns.Load() > 0)
	a.shutdowned.Store(true)
	return nil
}

// extJob is a service of an ordinary child scope with a health check.
type extJob struct{}

func (extJob) HealthCheck() error { return nil }

func TestExtend_Lifecycle(t *testing.T) {
	t.Parallel()

	root := New()
	pool := &extLifecyclePool{}
	poolName := NameOf[*extLifecyclePool]()
	require.NoError(t, Extend(root, &testExtension{name: "redis", extend: func(x *ExtensionContext) error {
		Provide(x.Scope(), func(Injector) (*extLifecyclePool, error) { return pool, nil })
		return ExtensionExport[*extLifecyclePool](x)
	}}))
	Provide(root, func(i Injector) (*extApp, error) {
		p, err := Invoke[*extLifecyclePool](i)
		return &extApp{pool: p}, err
	})
	app, err := Invoke[*extApp](root)
	require.NoError(t, err)
	jobs := root.Scope("jobs")
	ProvideValue(jobs, extJob{})
	_, err = Invoke[extJob](jobs)
	require.NoError(t, err)

	require.NoError(t, root.Start())
	health := root.HealthCheck()
	report := root.Shutdown()

	require.True(t, report.Succeed, report.Error())
	assert.Equal(t, int32(1), pool.starts.Load())
	assert.Equal(t, int32(1), pool.checks.Load())
	assert.Equal(t, int32(1), pool.shutdowns.Load())
	require.Contains(t, health, "ext:redis/"+poolName)
	assert.ErrorContains(t, health["ext:redis/"+poolName], "pool down")
	assert.NotContains(t, health, poolName)
	assert.NotContains(t, health, NameOf[extJob]())
	assert.NotContains(t, health, "jobs/"+NameOf[extJob]())
	assert.True(t, app.shutdowned.Load())
	assert.False(t, app.sawClosed.Load())
}

// cloneExtension is an extension "redis" whose client records the scope
// that built it; its fields are shared by the values a test compares.
type cloneExtension struct {
	calls   *atomic.Int32
	builtIn *atomic.Pointer[Scope]
}

func (e cloneExtension) Name() string { return "redis" }

func (e cloneExtension) Extend(x *ExtensionContext) error {
	e.calls.Add(1)
	Provide(x.Scope(), func(i Injector) (*extClient, error) {
		e.builtIn.Store(injectorScope(i))
		return &extClient{}, nil
	})
	return ExtensionExport[*extClient](x)
}

func TestExtend_Clone(t *testing.T) {
	t.Parallel()

	root := New()
	ext := &cloneExtension{calls: &atomic.Int32{}, builtIn: &atomic.Pointer[Scope]{}}
	require.NoError(t, Extend(root, ext))
	clone := root.Clone()

	err := extendNoPanic(t, clone, ext)

	require.NoError(t, err)
	assert.Equal(t, int32(1), ext.calls.Load())
	_, err = Invoke[*extClient](clone)
	require.NoError(t, err)
	cloned, ok := clone.ChildByName("ext:redis")
	require.True(t, ok)
	assert.Same(t, cloned, ext.builtIn.Load())
}
