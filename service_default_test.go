package doger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dfDialer is the replaceable service of the default-registration tests.
type dfDialer struct{ label string }

// dialerProvider returns a provider of a dialer labelled label, counting its
// calls in calls when it is not nil.
func dialerProvider(label string, calls *atomic.Int32) Provider[*dfDialer] {
	return func(Injector) (*dfDialer, error) {
		if calls != nil {
			calls.Add(1)
		}
		return &dfDialer{label: label}, nil
	}
}

// dfPool is a dialer pool with every lifecycle method, each counting its calls.
type dfPool struct {
	healthErr error
	starts    atomic.Int32
	runs      atomic.Int32
	shutdowns atomic.Int32
	checks    atomic.Int32
}

func (p *dfPool) Start(context.Context) error {
	p.starts.Add(1)
	return nil
}

func (p *dfPool) Run(context.Context) error {
	p.runs.Add(1)
	return nil
}

func (p *dfPool) Shutdown() error {
	p.shutdowns.Add(1)
	return nil
}

func (p *dfPool) HealthCheck() error {
	p.checks.Add(1)
	return p.healthErr
}

func TestAsDefault_ResolvesWithoutReplacement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(i Injector, calls *atomic.Int32)
		times    int
		calls    int32
	}{
		{
			name: "lazy_once",
			register: func(i Injector, calls *atomic.Int32) {
				Provide(i, dialerProvider("default-dialer", calls), AsDefault())
			},
			times: 1,
			calls: 1,
		},
		{
			name: "lazy_three",
			register: func(i Injector, calls *atomic.Int32) {
				Provide(i, dialerProvider("default-dialer", calls), AsDefault())
			},
			times: 3,
			calls: 1,
		},
		{
			name:     "eager",
			register: func(i Injector, _ *atomic.Int32) { ProvideValue(i, &dfDialer{label: "default-dialer"}, AsDefault()) },
			times:    3,
			calls:    0,
		},
		{
			name: "transient",
			register: func(i Injector, calls *atomic.Int32) {
				ProvideTransient(i, dialerProvider("default-dialer", calls), AsDefault())
			},
			times: 3,
			calls: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			var calls atomic.Int32
			tt.register(root, &calls)

			for range tt.times {
				dialer, err := Invoke[*dfDialer](root)
				require.NoError(t, err)
				assert.Equal(t, "default-dialer", dialer.label)
			}
			assert.Equal(t, tt.calls, calls.Load())
		})
	}
}

func TestAsDefault_NilInjector(t *testing.T) {
	previous := DefaultRootScope
	DefaultRootScope = New()
	t.Cleanup(func() { DefaultRootScope = previous })

	Provide(nil, dialerProvider("default-dialer", nil), AsDefault())

	dialer, err := Invoke[*dfDialer](DefaultRootScope)
	require.NoError(t, err)
	assert.Equal(t, "default-dialer", dialer.label)
}

// wrapperView is what a wrapper exposes to the container, for comparing a
// registration with and without AsDefault.
type wrapperView struct {
	name, typeName  string
	serviceType     ServiceType
	reflectType     string
	providerFrame   string
	sameInstance    bool
	healthchecker   bool
	healthErr       string
	shutdowner      bool
	built           bool
	shutdownErr     error
	cloneType       ServiceType
	cloneReflect    string
	registrationIdx uint64
}

// viewOf invokes name in root and records what its wrapper exposes;
// sameInstance tells whether the instance is want.
func viewOf(t *testing.T, root *RootScope, name string, want *dfPool) wrapperView {
	t.Helper()

	serviceAny, ok := root.serviceGet(name)
	require.True(t, ok)
	service, ok := serviceAny.(serviceWrapperAny)
	require.True(t, ok)

	instance, err := invokeAnyByName(root, name)
	require.NoError(t, err)

	frame, _ := service.source()
	view := wrapperView{
		name:            service.getName(),
		typeName:        service.getTypeName(),
		serviceType:     service.getServiceType(),
		reflectType:     service.getReflectType().String(),
		providerFrame:   frame.String(),
		sameInstance:    instance == any(want),
		healthchecker:   service.isHealthchecker(),
		shutdowner:      service.isShutdowner(),
		registrationIdx: service.getRegistrationIndex(),
	}
	if err := service.healthcheck(context.Background()); err != nil {
		view.healthErr = err.Error()
	}
	if timed, ok := serviceAny.(serviceWrapperBuildTime); ok {
		_, view.built = timed.getBuildTime()
	}
	clone, ok := service.clone(root).(serviceWrapperAny)
	require.True(t, ok)
	view.cloneType = clone.getServiceType()
	view.cloneReflect = clone.getReflectType().String()
	view.shutdownErr = service.shutdown(context.Background())

	return view
}

func TestAsDefault_ForwardsWrapperInterfaces(t *testing.T) {
	t.Parallel()

	sharedPool := &dfPool{healthErr: errors.New("pool down")}
	newPool := func(Injector) (*dfPool, error) { return sharedPool, nil }

	tests := []struct {
		name     string
		register func(i Injector, opts ...ProvideOption)
	}{
		{name: "lazy", register: func(i Injector, opts ...ProvideOption) { ProvideNamed(i, "pool", newPool, opts...) }},
		{name: "eager", register: func(i Injector, opts ...ProvideOption) { ProvideNamedValue(i, "pool", sharedPool, opts...) }},
		{name: "transient", register: func(i Injector, opts ...ProvideOption) { ProvideNamedTransient(i, "pool", newPool, opts...) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plain := New()
			tt.register(plain)
			withDefault := New()
			tt.register(withDefault, AsDefault())

			assert.Equal(t, viewOf(t, plain, "pool", sharedPool), viewOf(t, withDefault, "pool", sharedPool))
		})
	}
}

// dfDialing is an interface of dfDialer, for aliases.
type dfDialing interface{ Label() string }

func (d *dfDialer) Label() string { return d.label }

func TestAsDefault_YieldsByPlacement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		explicitIn func(root *RootScope, ext, sub, other *Scope) Injector // nil: no explicit registration
		want       string
		calls      int32
	}{
		{name: "root", explicitIn: func(root *RootScope, _, _, _ *Scope) Injector { return root }, want: "app-dialer", calls: 0},
		{name: "sibling", explicitIn: func(_ *RootScope, _, _, other *Scope) Injector { return other }, want: "default-dialer", calls: 1},
		{name: "descendant", explicitIn: func(_ *RootScope, _, sub, _ *Scope) Injector { return sub }, want: "default-dialer", calls: 1},
		{name: "none", want: "default-dialer", calls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			ext := root.Scope("ext")
			sub := ext.Scope("sub")
			other := root.Scope("other")
			var calls atomic.Int32
			Provide(ext, dialerProvider("default-dialer", &calls), AsDefault())
			if tt.explicitIn != nil {
				Provide(tt.explicitIn(root, ext, sub, other), dialerProvider("app-dialer", nil))
			}

			dialer, err := Invoke[*dfDialer](ext)

			require.NoError(t, err)
			assert.Equal(t, tt.want, dialer.label)
			assert.Equal(t, tt.calls, calls.Load())
		})
	}
}

func TestAsDefault_GrandchildYieldsToRoot(t *testing.T) {
	t.Parallel()

	root := New()
	grandchild := root.Scope("a").Scope("b")
	Provide(grandchild, dialerProvider("default-dialer", nil), AsDefault())
	Provide(root, dialerProvider("app-dialer", nil))

	dialer, err := Invoke[*dfDialer](grandchild)

	require.NoError(t, err)
	assert.Equal(t, "app-dialer", dialer.label)
}

func TestAsDefault_OrderIndependent(t *testing.T) {
	t.Parallel()

	registerDefault := func(_ *RootScope, ext *Scope) {
		Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
	}
	registerExplicit := func(root *RootScope, _ *Scope) { Provide(root, dialerProvider("app-dialer", nil)) }

	tests := []struct {
		name  string
		order []func(root *RootScope, ext *Scope)
	}{
		{name: "default_first", order: []func(*RootScope, *Scope){registerDefault, registerExplicit}},
		{name: "explicit_first", order: []func(*RootScope, *Scope){registerExplicit, registerDefault}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			ext := root.Scope("ext")
			for _, register := range tt.order {
				register(root, ext)
			}

			dialer, err := Invoke[*dfDialer](ext)

			require.NoError(t, err)
			assert.Equal(t, "app-dialer", dialer.label)
		})
	}
}

func TestAsDefault_Repeated(t *testing.T) {
	t.Parallel()

	root := New()
	ext := root.Scope("ext")
	Provide(ext, dialerProvider("default-dialer", nil), AsDefault(), AsDefault())
	Provide(root, dialerProvider("app-dialer", nil))

	dialer, err := Invoke[*dfDialer](ext)

	require.NoError(t, err)
	assert.Equal(t, "app-dialer", dialer.label)
}

func TestAsDefault_DoesNotYieldToDefault(t *testing.T) {
	t.Parallel()

	root := New()
	ext := root.Scope("ext")
	Provide(root, dialerProvider("root-default", nil), AsDefault())
	Provide(ext, dialerProvider("ext-default", nil), AsDefault())

	dialer, err := Invoke[*dfDialer](ext)

	require.NoError(t, err)
	assert.Equal(t, "ext-default", dialer.label)
}

func TestAsDefault_AliasResolvesThroughDecision(t *testing.T) {
	t.Parallel()

	root := New()
	ext := root.Scope("ext")
	Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
	require.NoError(t, As[*dfDialer, dfDialing](ext))
	Provide(root, dialerProvider("app-dialer", nil))

	dialer, err := Invoke[dfDialing](ext)

	require.NoError(t, err)
	assert.Equal(t, "app-dialer", dialer.Label())
}

func TestAsDefault_RealCycleThroughYield(t *testing.T) {
	t.Parallel()

	root := New()
	ext := root.Scope("ext")
	Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
	// the root's dialer needs itself: a real loop, reached through the yield
	Provide(root, func(i Injector) (*dfDialer, error) {
		if _, err := Invoke[*dfDialer](i); err != nil {
			return nil, err
		}
		return &dfDialer{label: "app-dialer"}, nil
	})

	_, err := Invoke[*dfDialer](ext)

	require.ErrorIs(t, err, ErrCircularDependency)
}

func TestAsDefault_ProviderRegistersOwnName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 3*time.Second)

	root := New()
	Provide(root, func(i Injector) (*dfDialer, error) {
		// the default is decided before its provider runs: no replacement
		Provide(i, dialerProvider("app-dialer", nil))
		return &dfDialer{label: "default-dialer"}, nil
	}, AsDefault())

	_, err := Invoke[*dfDialer](root)

	require.EqualError(t, err, fmt.Sprintf("DI: service `%s` has already been declared", NameOf[*dfDialer]()))
}

func TestAsDefault_DecisionFrozen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from func(root *RootScope, ext *Scope) Injector
		want string
	}{
		{name: "ext", from: func(_ *RootScope, ext *Scope) Injector { return ext }, want: "default-dialer"},
		{name: "root", from: func(root *RootScope, _ *Scope) Injector { return root }, want: "app-dialer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			ext := root.Scope("ext")
			Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
			_, err := Invoke[*dfDialer](ext)
			require.NoError(t, err)
			Provide(root, dialerProvider("app-dialer", nil))

			dialer, err := Invoke[*dfDialer](tt.from(root, ext))

			require.NoError(t, err)
			assert.Equal(t, tt.want, dialer.label)
		})
	}
}

func TestAsDefault_Clone(t *testing.T) {
	t.Parallel()

	t.Run("chosen_reset", func(t *testing.T) {
		t.Parallel()

		root := New()
		ext := root.Scope("ext")
		Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
		_, err := Invoke[*dfDialer](ext)
		require.NoError(t, err)

		clone := root.Clone()
		Provide(clone, dialerProvider("app-dialer", nil))
		cloneExt, ok := clone.ChildByName("ext")
		require.True(t, ok)

		dialer, err := Invoke[*dfDialer](cloneExt)

		require.NoError(t, err)
		assert.Equal(t, "app-dialer", dialer.label)
	})

	t.Run("yielded_rebinds", func(t *testing.T) {
		t.Parallel()

		root := New()
		ext := root.Scope("ext")
		Provide(ext, dialerProvider("default-dialer", nil), AsDefault())
		Provide(root, dialerProvider("app-dialer", nil))
		_, err := Invoke[*dfDialer](ext)
		require.NoError(t, err)

		clone := root.Clone()
		cloneExt, ok := clone.ChildByName("ext")
		require.True(t, ok)

		dialer, err := Invoke[*dfDialer](cloneExt)

		require.NoError(t, err)
		assert.Same(t, MustInvoke[*dfDialer](clone), dialer)
		assert.NotSame(t, MustInvoke[*dfDialer](root), dialer)
	})
}

// poolProvider returns a provider of pool.
func poolProvider(pool *dfPool) Provider[*dfPool] {
	return func(Injector) (*dfPool, error) { return pool, nil }
}

// shutdownOnCleanup shuts root down at the end of the test.
func shutdownOnCleanup(t *testing.T, root *RootScope) {
	t.Helper()
	t.Cleanup(func() { _ = root.Shutdown() })
}

func TestAsDefault_YieldedLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		before    func(t *testing.T, root *RootScope)
		operation func(root *RootScope, ext *Scope)
		count     func(pool *dfPool) int32
		want      int32
	}{
		{
			name:      "start",
			operation: func(root *RootScope, _ *Scope) { _ = root.Start() },
			count:     func(pool *dfPool) int32 { return pool.starts.Load() },
			want:      1,
		},
		{
			name:      "run",
			operation: func(root *RootScope, _ *Scope) { _ = root.Start() },
			count:     func(pool *dfPool) int32 { return pool.runs.Load() },
			want:      0,
		},
		{
			name:      "shutdown",
			before:    func(t *testing.T, root *RootScope) { t.Helper(); require.NoError(t, root.Start()) },
			operation: func(root *RootScope, _ *Scope) { _ = root.Shutdown() },
			count:     func(pool *dfPool) int32 { return pool.shutdowns.Load() },
			want:      1,
		},
		{
			name:      "root_healthcheck",
			operation: func(root *RootScope, _ *Scope) { _ = root.HealthCheck() },
			count:     func(pool *dfPool) int32 { return pool.checks.Load() },
			want:      1,
		},
		{
			name:      "ext_healthcheck",
			operation: func(_ *RootScope, ext *Scope) { _ = ext.HealthCheck() },
			count:     func(pool *dfPool) int32 { return pool.checks.Load() },
			want:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 3*time.Second)

			rootPool := &dfPool{healthErr: errors.New("pool down")}
			root := New()
			shutdownOnCleanup(t, root)
			ext := root.Scope("ext")
			Provide(root, poolProvider(rootPool))
			Provide(ext, poolProvider(&dfPool{}), AsDefault(), AsRunnable())
			_, err := Invoke[*dfPool](ext)
			require.NoError(t, err)
			if tt.before != nil {
				tt.before(t, root)
			}

			tt.operation(root, ext)

			assert.Equal(t, tt.want, tt.count(rootPool))
		})
	}
}

func TestAsDefault_YieldedHealthCheckResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func(root *RootScope, ext *Scope) Injector // where the root's pool is first invoked from
	}{
		{name: "invoked", build: func(_ *RootScope, ext *Scope) Injector { return ext }},
		{name: "never_invoked", build: func(root *RootScope, _ *Scope) Injector { return root }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 3*time.Second)

			root := New()
			ext := root.Scope("ext")
			Provide(root, poolProvider(&dfPool{healthErr: errors.New("pool down")}))
			Provide(ext, poolProvider(&dfPool{}), AsDefault())
			_, err := Invoke[*dfPool](tt.build(root, ext))
			require.NoError(t, err)

			results := ext.HealthCheck()

			require.Contains(t, results, NameOf[*dfPool]())
			assert.ErrorContains(t, results[NameOf[*dfPool]()], "pool down")
		})
	}
}

func TestAsDefault_ChosenLifecycle(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 3*time.Second)

	pool := &dfPool{}
	root := New()
	Provide(root, poolProvider(pool), AsDefault())
	require.NoError(t, root.Start())

	_ = root.Shutdown()

	assert.Equal(t, int32(1), pool.starts.Load())
	assert.Equal(t, int32(1), pool.shutdowns.Load())
}

func TestAsDefault_EagerShutdownOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, root *RootScope, ext *Scope, value *dfPool)
		want  int32
	}{
		{
			name:  "alone",
			setup: func(_ *testing.T, _ *RootScope, ext *Scope, value *dfPool) { ProvideValue(ext, value, AsDefault()) },
			want:  1,
		},
		{
			name: "replaced_uninvoked",
			setup: func(_ *testing.T, root *RootScope, ext *Scope, value *dfPool) {
				ProvideValue(ext, value, AsDefault())
				Provide(root, poolProvider(&dfPool{}))
			},
			want: 0,
		},
		{
			name: "replaced_invoked",
			setup: func(t *testing.T, root *RootScope, ext *Scope, value *dfPool) {
				t.Helper()
				ProvideValue(ext, value, AsDefault())
				Provide(root, poolProvider(&dfPool{}))
				_, err := Invoke[*dfPool](ext)
				require.NoError(t, err)
			},
			want: 0,
		},
		{
			name: "superseded",
			setup: func(_ *testing.T, root *RootScope, _ *Scope, value *dfPool) {
				ProvideValue(root, value, AsDefault())
				Provide(root, poolProvider(&dfPool{}))
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 3*time.Second)

			value := &dfPool{}
			root := New()
			tt.setup(t, root, root.Scope("ext"), value)

			_ = root.Shutdown()

			assert.Equal(t, tt.want, value.shutdowns.Load())
		})
	}
}

func TestAsDefault_ConcurrentFirstResolution(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	ext := root.Scope("ext")
	var calls atomic.Int32
	Provide(ext, dialerProvider("default-dialer", &calls), AsDefault())

	const goroutines = 100
	results := make([]*dfDialer, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range goroutines {
		wg.Go(func() {
			<-start
			results[index], _ = Invoke[*dfDialer](ext)
		})
	}
	close(start)
	wg.Wait()

	require.NotNil(t, results[0])
	for _, dialer := range results {
		assert.Same(t, results[0], dialer)
	}
	assert.Equal(t, int32(1), calls.Load())
}

// dfConn is a default service whose Shutdown counts its calls.
type dfConn struct {
	label     string
	shutdowns *atomic.Int32
}

func (c *dfConn) Shutdown() { c.shutdowns.Add(1) }

// raceOutcome is what a registration racing a first resolution produced.
type raceOutcome struct {
	registered       bool // the explicit registration succeeded
	label            string
	defaultShutdowns int32 // shutdowns of the default's instance after root Shutdown
}

// raceReplaceAndResolve registers a default *dfConn whose provider calls
// building then waits for release, then runs an explicit registration and an
// invocation of the name concurrently; order sequences them.
func raceReplaceAndResolve(t *testing.T, order func(register, invoke func(), building <-chan struct{}, release chan<- struct{})) raceOutcome {
	t.Helper()

	root := New()
	var defaultShutdowns atomic.Int32
	building := make(chan struct{}, 1)
	release := make(chan struct{})
	Provide(root, func(Injector) (*dfConn, error) {
		building <- struct{}{}
		<-release
		return &dfConn{label: "default", shutdowns: &defaultShutdowns}, nil
	}, AsDefault())

	var outcome raceOutcome
	var explicitShutdowns atomic.Int32
	register := func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				assert.EqualError(t, recovered.(error), fmt.Sprintf("DI: service `%s` has already been declared", NameOf[*dfConn]())) //nolint:errcheck,forcetypeassert
			}
		}()
		Provide(root, func(Injector) (*dfConn, error) { return &dfConn{label: "explicit", shutdowns: &explicitShutdowns}, nil })
		outcome.registered = true
	}
	invoke := func() {
		conn, err := Invoke[*dfConn](root)
		if assert.NoError(t, err) {
			outcome.label = conn.label
		}
	}

	order(register, invoke, building, release)
	_ = root.Shutdown()
	outcome.defaultShutdowns = defaultShutdowns.Load()

	return outcome
}

func TestAsDefault_ReplaceRacesResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		order func(register, invoke func(), building <-chan struct{}, release chan<- struct{})
		want  raceOutcome
	}{
		{
			name: "replace_first",
			order: func(register, invoke func(), _ <-chan struct{}, release chan<- struct{}) {
				close(release) // the default's provider never blocks
				registered := make(chan struct{})
				var wg sync.WaitGroup
				wg.Go(func() { register(); close(registered) })
				wg.Go(func() { <-registered; invoke() })
				wg.Wait()
			},
			want: raceOutcome{registered: true, label: "explicit", defaultShutdowns: 0},
		},
		{
			name: "resolve_first",
			order: func(register, invoke func(), building <-chan struct{}, release chan<- struct{}) {
				var wg sync.WaitGroup
				wg.Go(invoke)
				wg.Go(func() {
					<-building // the invocation decided and builds the default
					register()
					close(release)
				})
				wg.Wait()
			},
			want: raceOutcome{registered: false, label: "default", defaultShutdowns: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)

			assert.Equal(t, tt.want, raceReplaceAndResolve(t, tt.order))
		})
	}
}

func TestAsDefault_ReplaceRacesResolveStress(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 30*time.Second)

	replaced := 0
	for range 1000 {
		outcome := raceReplaceAndResolve(t, func(register, invoke func(), _ <-chan struct{}, release chan<- struct{}) {
			close(release) // the provider does not block: the order is up to the scheduler
			var wg sync.WaitGroup
			wg.Go(invoke)
			wg.Go(register)
			wg.Wait()
		})

		// exactly one of the two outcomes, and no built instance is dropped
		if outcome.registered {
			replaced++
			require.Equal(t, raceOutcome{registered: true, label: "explicit", defaultShutdowns: 0}, outcome)
		} else {
			require.Equal(t, raceOutcome{registered: false, label: "default", defaultShutdowns: 1}, outcome)
		}
	}
	t.Logf("the registration replaced the default in %d of 1000 runs", replaced)
}

// dfPropertySource is the interface of the by-type default tests.
type dfPropertySource interface{ Source() string }

// dfEnvSource is the default implementation of dfPropertySource.
type dfEnvSource struct{ name string }

func (s *dfEnvSource) Source() string { return s.name }

// dfMySource is the application's implementation of dfPropertySource.
type dfMySource struct{ name string }

func (s *dfMySource) Source() string { return s.name }

// dfOther implements no dfPropertySource.
type dfOther struct{}

// provideEnvSource registers a lazy *dfEnvSource named name in scope.
func provideEnvSource(scope Injector, name string, opts ...ProvideOption) {
	ProvideNamed(scope, name, func(Injector) (*dfEnvSource, error) { return &dfEnvSource{name: name}, nil }, opts...)
}

// provideMySource registers a lazy *dfMySource named name in scope.
func provideMySource(scope Injector, name string, opts ...ProvideOption) {
	ProvideNamed(scope, name, func(Injector) (*dfMySource, error) { return &dfMySource{name: name}, nil }, opts...)
}

func TestScope_DefaultCount(t *testing.T) {
	t.Parallel()

	lazyA := func(i Injector, opts ...ProvideOption) {
		ProvideNamed(i, "a", func(Injector) (int, error) { return 1, nil }, opts...)
	}

	tests := []struct {
		name  string
		act   func(t *testing.T, root *RootScope, ext *Scope) (Injector, Injector) // the root and ext to count
		root  int64
		child int64
	}{
		{name: "registered", act: func(_ *testing.T, root *RootScope, ext *Scope) (Injector, Injector) {
			lazyA(root, AsDefault())
			ProvideNamed(ext, "b", func(Injector) (int, error) { return 2, nil }, AsDefault())
			return root, ext
		}, root: 1, child: 1},
		{name: "skipped", act: func(_ *testing.T, root *RootScope, ext *Scope) (Injector, Injector) {
			lazyA(root)
			lazyA(root, AsDefault())
			return root, ext
		}, root: 0, child: 0},
		{name: "replaced", act: func(_ *testing.T, root *RootScope, ext *Scope) (Injector, Injector) {
			lazyA(root, AsDefault())
			lazyA(root)
			return root, ext
		}, root: 0, child: 0},
		{name: "overridden", act: func(_ *testing.T, root *RootScope, ext *Scope) (Injector, Injector) {
			lazyA(root, AsDefault())
			OverrideNamed(root, "a", func(Injector) (int, error) { return 3, nil })
			return root, ext
		}, root: 0, child: 0},
		{name: "override_as_default", act: func(_ *testing.T, root *RootScope, ext *Scope) (Injector, Injector) {
			OverrideNamed(root, "a", func(Injector) (int, error) { return 3, nil }, AsDefault())
			return root, ext
		}, root: 0, child: 0},
		{name: "shutdown", act: func(_ *testing.T, root *RootScope, ext *Scope) (Injector, Injector) {
			lazyA(ext, AsDefault())
			_ = ext.Shutdown()
			return root, ext
		}, root: 0, child: 0},
		{name: "clone", act: func(t *testing.T, root *RootScope, ext *Scope) (Injector, Injector) {
			t.Helper()
			lazyA(root, AsDefault())
			lazyA(ext, AsDefault())
			clone := root.Clone()
			cloneExt, ok := clone.ChildByName("ext")
			require.True(t, ok)
			return clone, cloneExt
		}, root: 1, child: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			countedRoot, countedChild := tt.act(t, root, root.Scope("ext"))

			assert.Equal(t, tt.root, injectorScope(countedRoot).defaultCount())
			assert.Equal(t, tt.child, injectorScope(countedChild).defaultCount())
		})
	}
}
