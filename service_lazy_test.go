package doger

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kartaladev/doger/stacktrace"
	"github.com/stretchr/testify/assert"
)

type lazyTest struct {
	foobar string
}

// Alias to HealthChecker.
// Used for testing interface implements interface.
type iTestHeathchecker interface {
	HealthCheck() error
}

var _ Healthchecker = (*lazyTestHeathcheckerOK)(nil)

type lazyTestHeathcheckerOK struct {
	foobar string
}

func (t *lazyTestHeathcheckerOK) HealthCheck() error {
	return nil
}

var _ Healthchecker = (*lazyTestHeathcheckerKO)(nil)

type lazyTestHeathcheckerKO struct {
	foobar string
}

func (t *lazyTestHeathcheckerKO) HealthCheck() error {
	return assert.AnError
}

var _ HealthcheckerWithContext = (*lazyTestHeathcheckerKOCtx)(nil)

type lazyTestHeathcheckerKOCtx struct {
	foobar string
}

func (t *lazyTestHeathcheckerKOCtx) HealthCheck(ctx context.Context) error {
	return assert.AnError
}

var _ Healthchecker = (*lazyTestHeathcheckerOKTimeout)(nil)

type lazyTestHeathcheckerOKTimeout struct {
	foobar string
}

func (t *lazyTestHeathcheckerOKTimeout) HealthCheck() error {
	time.Sleep(20 * time.Millisecond)
	return nil
}

var _ ShutdownerWithContextAndError = (*lazyTestShutdownerOK)(nil)

type lazyTestShutdownerOK struct {
	foobar string
}

func (t *lazyTestShutdownerOK) Shutdown(ctx context.Context) error {
	return nil
}

var _ ShutdownerWithError = (*lazyTestShutdownerKO)(nil)

type lazyTestShutdownerKO struct {
	foobar string
}

func (t *lazyTestShutdownerKO) Shutdown() error {
	return assert.AnError
}

var _ ShutdownerWithContextAndError = (*lazyTestShutdownerKOCtx)(nil)

type lazyTestShutdownerKOCtx struct {
	foobar string
}

func (t *lazyTestShutdownerKOCtx) Shutdown(ctx context.Context) error {
	return assert.AnError
}

// Test services for context value propagation in service lazy
type contextValueHealthcheckerLazy struct{}

func (c *contextValueHealthcheckerLazy) HealthCheck(ctx context.Context) error {
	value := ctx.Value(ctxTestKey)
	if value != "healthcheck-value" {
		return fmt.Errorf("test-key not found or value is incorrect")
	}
	return nil
}

type contextValueShutdownerLazy struct{}

func (c *contextValueShutdownerLazy) Shutdown(ctx context.Context) error {
	value := ctx.Value(ctxTestKey)
	if value != "shutdown-value" {
		return fmt.Errorf("test-key not found or value is incorrect")
	}
	return nil
}

// lazyTestService is a *serviceLazy[T] of any T, for tables whose rows differ in T.
type lazyTestService struct {
	serviceWrapperAny
	getInstance func(Injector) (any, error) // the typed getInstance, its instance as any
	build       func(Injector) error
	built       func() bool          // reads the built field
	buildTime   func() time.Duration // reads the buildTime field
	instance    func() any           // reads the instance field
}

// lazyTestServiceOf wraps s for a table row.
func lazyTestServiceOf[T any](s *serviceLazy[T]) lazyTestService {
	return lazyTestService{
		serviceWrapperAny: s,
		getInstance:       func(i Injector) (any, error) { return s.getInstance(i) },
		build:             s.build,
		built:             func() bool { return s.built },
		buildTime:         func() time.Duration { return s.buildTime },
		instance:          func() any { return s.instance },
	}
}

func TestNewServiceLazy(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	testStruct := lazyTest{foobar: "test"}

	tests := []struct {
		name         string
		service      lazyTestService
		wantName     string
		wantTypeName string
		zero         any // the zero value of the instance; nil: not checked
	}{
		{
			name: "int_provider",
			service: lazyTestServiceOf(newServiceLazy("test-service-1", func(i Injector) (int, error) {
				return 42, nil
			})),
			wantName:     "test-service-1",
			wantTypeName: "int",
			zero:         0, // Zero value for int
		},
		{
			name: "struct_provider",
			service: lazyTestServiceOf(newServiceLazy("test-service-2", func(i Injector) (lazyTest, error) {
				return testStruct, nil
			})),
			wantName:     "test-service-2",
			wantTypeName: "github.com/kartaladev/doger.lazyTest",
			zero:         lazyTest{}, // Zero value for struct
		},
		{
			name: "pointer_provider",
			service: lazyTestServiceOf(newServiceLazy("test-service-3", func(i Injector) (*lazyTest, error) {
				return &testStruct, nil
			})),
			wantName:     "test-service-3",
			wantTypeName: "*github.com/kartaladev/doger.lazyTest",
		},
		{
			name: "interface_provider",
			service: lazyTestServiceOf(newServiceLazy("test-service-4", func(i Injector) (Healthchecker, error) {
				return &lazyTestHeathcheckerOK{foobar: "health"}, nil
			})),
			wantName:     "test-service-4",
			wantTypeName: "github.com/kartaladev/doger.Healthchecker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.NotNil(tt.service.serviceWrapperAny)
			is.Equal(tt.wantName, tt.service.getName())
			is.Equal(tt.wantTypeName, tt.service.getTypeName())
			is.Equal(ServiceTypeLazy, tt.service.getServiceType())
			if tt.zero != nil {
				is.False(tt.service.built())
				is.Equal(tt.zero, tt.service.instance())
			}

			// Test that the service is not built initially
			is.False(tt.service.built())
		})
	}
}

func TestServiceLazy_getName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := lazyTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (lazyTest, error) {
		return test, nil
	}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceLazy("foobar1", provider1), want: "foobar1"},
		{name: "struct", service: newServiceLazy("foobar2", provider2), want: "foobar2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getName())
		})
	}
}

func TestServiceLazy_getTypeName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := lazyTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (lazyTest, error) {
		return test, nil
	}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceLazy("foobar1", provider1), want: "int"},
		{name: "struct", service: newServiceLazy("foobar2", provider2), want: "github.com/kartaladev/doger.lazyTest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getTypeName())
		})
	}
}

func TestServiceLazy_getServiceType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := lazyTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (lazyTest, error) {
		return test, nil
	}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    ServiceType
	}{
		{name: "int", service: newServiceLazy("foobar1", provider1), want: ServiceTypeLazy},
		{name: "struct", service: newServiceLazy("foobar2", provider2), want: ServiceTypeLazy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getServiceType())
		})
	}
}

func TestServiceLazy_getReflectType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := lazyTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (lazyTest, error) {
		return test, nil
	}
	provider3 := func(i Injector) (Healthchecker, error) {
		return nil, nil
	}
	provider4 := func(i Injector) (*lazyTest, error) {
		return &test, nil
	}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceLazy("foobar1", provider1), want: "int"},
		{name: "struct", service: newServiceLazy("foobar2", provider2), want: pkgName + ".lazyTest"},
		{name: "interface", service: newServiceLazy("foobar3", provider3), want: pkgName + ".Healthchecker"},
		{name: "pointer", service: newServiceLazy("foobar1", provider4), want: "*" + pkgName + ".lazyTest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getReflectType().String())
		})
	}
}

func TestServiceLazy_getInstanceAny(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	test := lazyTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (lazyTest, error) {
		return test, nil
	}
	provider3 := func(i Injector) (int, error) {
		panic("error")
	}
	provider4 := func(i Injector) (int, error) {
		panic(fmt.Errorf("error"))
	}
	provider5 := func(i Injector) (int, error) {
		return 42, fmt.Errorf("error")
	}

	i := New()

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    any
		wantErr error // nil: the provider succeeds
	}{
		{name: "basic_type", service: newServiceLazy("foobar", provider1), want: 42},
		{name: "struct", service: newServiceLazy("hello", provider2), want: test},
		// provider panics, but panic is catched by getInstanceAny
		{name: "provider_panics_error", service: newServiceLazy("plop", provider4), wantErr: fmt.Errorf("error")},
		{name: "provider_returns_error", service: newServiceLazy("plop", provider5), wantErr: fmt.Errorf("error")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			if tt.wantErr == nil {
				instance, err := tt.service.getInstanceAny(i)
				is.NoError(err)
				is.Equal(tt.want, instance)
				return
			}

			is.NotPanics(func() {
				instance, err := tt.service.getInstanceAny(i)
				is.Error(err)
				is.Empty(instance)
				is.Equal(tt.wantErr, err)
			})
		})
	}

	// provider panics, but panic is catched by getInstanceAny
	is.NotPanics(func() {
		service3 := newServiceLazy("baz", provider3)
		_, _ = service3.getInstanceAny(i)
	})
}

func TestServiceLazy_getInstance(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	test := lazyTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (lazyTest, error) {
		return test, nil
	}
	provider3 := func(i Injector) (int, error) {
		panic("error")
	}
	provider4 := func(i Injector) (int, error) {
		panic(fmt.Errorf("error"))
	}
	provider5 := func(i Injector) (int, error) {
		return 42, fmt.Errorf("error")
	}

	i := New()

	tests := []struct {
		name    string
		service lazyTestService
		want    any
		wantErr error // nil: the provider succeeds
	}{
		{name: "basic_type", service: lazyTestServiceOf(newServiceLazy("foobar", provider1)), want: 42},
		{name: "struct", service: lazyTestServiceOf(newServiceLazy("hello", provider2)), want: test},
		// provider panics, but panic is catched by getInstance
		{name: "provider_panics_error", service: lazyTestServiceOf(newServiceLazy("plop", provider4)), wantErr: fmt.Errorf("error")},
		{name: "provider_returns_error", service: lazyTestServiceOf(newServiceLazy("plop", provider5)), wantErr: fmt.Errorf("error")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			if tt.wantErr == nil {
				instance, err := tt.service.getInstance(i)
				is.NoError(err)
				is.Equal(tt.want, instance)
				return
			}

			is.NotPanics(func() {
				instance, err := tt.service.getInstance(i)
				is.Error(err)
				is.Empty(instance)
				is.Equal(tt.wantErr, err)
			})
		})
	}

	// provider panics, but panic is catched by getInstance
	is.NotPanics(func() {
		service3 := newServiceLazy("baz", provider3)
		_, _ = service3.getInstance(i)
	})
}

func TestServiceLazy_build(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	test := lazyTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (lazyTest, error) {
		return test, nil
	}
	provider3 := func(i Injector) (int, error) {
		panic("error")
	}
	provider4 := func(i Injector) (int, error) {
		panic(fmt.Errorf("error"))
	}
	provider5 := func(i Injector) (int, error) {
		return 42, fmt.Errorf("error")
	}

	i := New()

	tests := []struct {
		name    string
		service lazyTestService
		wantErr error // nil: the provider succeeds
	}{
		{name: "basic_type", service: lazyTestServiceOf(newServiceLazy("foobar", provider1))},
		{name: "struct", service: lazyTestServiceOf(newServiceLazy("hello", provider2))},
		// provider panics, but panic is catched by getInstance
		{name: "provider_panics_error", service: lazyTestServiceOf(newServiceLazy("plop", provider4)), wantErr: fmt.Errorf("error")},
		{name: "provider_returns_error", service: lazyTestServiceOf(newServiceLazy("plop", provider5)), wantErr: fmt.Errorf("error")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			if tt.wantErr == nil {
				is.False(tt.service.built())
				is.Empty(tt.service.buildTime())
				err := tt.service.build(i)
				is.True(tt.service.built())
				is.NotEmpty(tt.service.buildTime())
				is.NoError(err)
				return
			}

			is.NotPanics(func() {
				is.False(tt.service.built())
				is.Empty(tt.service.buildTime())
				err := tt.service.build(i)
				is.False(tt.service.built())
				is.Empty(tt.service.buildTime())
				is.Error(err)
				is.Equal(tt.wantErr, err)
			})
		})
	}

	// provider panics, but panic is catched by getInstance
	is.NotPanics(func() {
		service3 := newServiceLazy("baz", provider3)
		is.False(service3.built)
		is.Empty(service3.buildTime)
		_ = service3.build(i)
		is.False(service3.built)
		is.Empty(service3.buildTime)
	})
}

func TestServiceLazy_isHealthchecker(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// no healthcheck
	service1 := newServiceLazy("foobar", func(i Injector) (lazyTest, error) {
		return lazyTest{foobar: "foobar"}, nil
	})
	is.False(service1.isHealthchecker())

	tests := []struct {
		name    string
		service lazyTestService
	}{
		{
			name: "healthcheck_ok",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (*lazyTestHeathcheckerOK, error) {
				return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
			})),
		},
		{
			name: "healthcheck_ko",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (*lazyTestHeathcheckerKO, error) {
				return &lazyTestHeathcheckerKO{foobar: "foobar"}, nil
			})),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.False(tt.service.isHealthchecker())
			_, _ = tt.service.getInstance(nil)
			is.True(tt.service.isHealthchecker())
		})
	}
}

// @TODO: missing tests for context
func TestServiceLazy_healthcheck(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	ctx := context.Background()

	tests := []struct {
		name    string
		service lazyTestService
		wantErr error // of the check once built
	}{
		{
			name: "no_healthcheck",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (lazyTest, error) {
				return lazyTest{foobar: "foobar"}, nil
			})),
		},
		{
			name: "healthcheck_ok",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (*lazyTestHeathcheckerOK, error) {
				return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
			})),
		},
		{
			name: "healthcheck_ko",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (*lazyTestHeathcheckerKO, error) {
				return &lazyTestHeathcheckerKO{foobar: "foobar"}, nil
			})),
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.NoError(tt.service.healthcheck(ctx))
			_, _ = tt.service.getInstance(nil)
			if tt.wantErr == nil {
				is.NoError(tt.service.healthcheck(ctx))
			} else {
				is.Equal(tt.wantErr, tt.service.healthcheck(ctx))
			}
		})
	}

	// Test with context scenarios
	// Test with canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	service4 := newServiceLazy("foobar", func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})
	_, _ = service4.getInstance(nil)
	err := service4.healthcheck(canceledCtx)
	is.Error(err)
	is.ErrorContains(err, "context canceled")

	// Test with timeout context
	timeoutCtx, cancel2 := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel2()
	time.Sleep(2 * time.Millisecond)
	err2 := service4.healthcheck(timeoutCtx)
	is.Error(err2)
	is.ErrorContains(err2, "context deadline exceeded")

	// Test with value context - verify context value is received
	valueCtx := context.WithValue(context.Background(), ctxTestKey, "healthcheck-value")
	serviceWithContext := newServiceLazy("foobar", func(i Injector) (*contextValueHealthcheckerLazy, error) {
		return &contextValueHealthcheckerLazy{}, nil
	})
	_, _ = serviceWithContext.getInstance(nil)
	err3 := serviceWithContext.healthcheck(valueCtx)
	is.NoError(err3) // Should work normally when context value is correct

	// Test with incorrect context value - verify context value is checked
	incorrectValueCtx := context.WithValue(context.Background(), ctxTestKey, "wrong-value")
	err4 := serviceWithContext.healthcheck(incorrectValueCtx)
	is.Error(err4) // Should fail when context value is incorrect
	is.ErrorContains(err4, "test-key not found or value is incorrect")
}

// lazyTestBlockingDB is a Healthchecker whose check blocks until released,
// ignoring its context.
type lazyTestBlockingDB struct{ release chan struct{} }

func (d *lazyTestBlockingDB) HealthCheck() error {
	<-d.release
	return nil
}

func TestServiceLazy_HealthCheckWithoutLock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action func(i *RootScope)
	}{
		{name: "Shutdown", action: func(i *RootScope) { i.Shutdown() }},
		{name: "Invoke", action: func(i *RootScope) { _, _ = Invoke[*lazyTestBlockingDB](i) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 5*time.Second)
			is := assert.New(t)

			i := NewWithOpts(&InjectorOpts{HealthCheckTimeout: 5 * time.Millisecond})
			db := &lazyTestBlockingDB{release: make(chan struct{})}
			Provide(i, func(Injector) (*lazyTestBlockingDB, error) { return db, nil })
			_, err := Invoke[*lazyTestBlockingDB](i)
			is.NoError(err)

			results := i.HealthCheck()
			is.ErrorIs(results[NameOf[*lazyTestBlockingDB]()], ErrHealthCheckTimeout)

			returned := make(chan struct{})
			go func() {
				defer close(returned)
				tt.action(i)
			}()

			select {
			case <-returned:
			case <-time.After(time.Second):
				is.Fail("the call did not return within 1s while a health check was in flight")
			}

			close(db.release)
			<-returned
			i.Shutdown()
		})
	}
}

func TestServiceLazy_HealthCheckDuringBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		skip  string
		check func(t *testing.T, i *RootScope)
	}{
		{
			name: "HealthCheckWithContext",
			check: func(t *testing.T, i *RootScope) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				defer cancel()
				assert.Equal(t, map[string]error{"slow": nil, "db": nil}, i.HealthCheckWithContext(ctx))
			},
		},
		{
			name: "HealthReport",
			check: func(t *testing.T, i *RootScope) {
				t.Helper()
				report := i.HealthReport()
				listed := make([]string, 0, len(report.Services))
				for _, entry := range report.Services {
					listed = append(listed, entry.Service.Service)
				}
				assert.Equal(t, []string{"db"}, listed)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			testWithTimeout(t, 5*time.Second)

			i := New()
			started, release := make(chan struct{}), make(chan struct{})
			ProvideNamed(i, "slow", func(Injector) (*lazyTestHeathcheckerOK, error) {
				close(started)
				<-release
				return &lazyTestHeathcheckerOK{}, nil
			})
			ProvideNamedValue(i, "db", &lazyTestHeathcheckerOK{})

			invoked := make(chan struct{})
			go func() {
				defer close(invoked)
				_, _ = InvokeNamed[*lazyTestHeathcheckerOK](i, "slow")
			}()
			<-started

			returned := make(chan struct{})
			go func() {
				defer close(returned)
				tt.check(t, i)
			}()

			select {
			case <-returned:
			case <-time.After(time.Second):
				assert.Fail(t, "the health check did not return within 1s while a provider was in progress")
			}

			close(release)
			<-invoked
			<-returned
		})
	}
}

// @TODO: missing tests for context
func TestServiceLazy_isShutdowner(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// no shutdown
	service1 := newServiceLazy("foobar", func(i Injector) (lazyTest, error) {
		return lazyTest{foobar: "foobar"}, nil
	})
	is.False(service1.isShutdowner())

	tests := []struct {
		name    string
		service lazyTestService
	}{
		{
			name: "shutdown_ok",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (*lazyTestShutdownerOK, error) {
				return &lazyTestShutdownerOK{foobar: "foobar"}, nil
			})),
		},
		{
			name: "shutdown_ko",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (*lazyTestShutdownerKO, error) {
				return &lazyTestShutdownerKO{foobar: "foobar"}, nil
			})),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.False(tt.service.isShutdowner())
			_, _ = tt.service.getInstance(nil)
			is.True(tt.service.isShutdowner())
		})
	}
}

// @TODO: missing tests for context
func TestServiceLazy_shutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	ctx := context.Background()

	tests := []struct {
		name    string
		service lazyTestService
		wantErr error // of the shutdown once built
	}{
		{
			name: "no_shutdown",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (lazyTest, error) {
				return lazyTest{foobar: "foobar"}, nil
			})),
		},
		{
			name: "shutdown_ok",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (*lazyTestShutdownerOK, error) {
				return &lazyTestShutdownerOK{foobar: "foobar"}, nil
			})),
		},
		{
			name: "shutdown_ko",
			service: lazyTestServiceOf(newServiceLazy("foobar", func(i Injector) (*lazyTestShutdownerKO, error) {
				return &lazyTestShutdownerKO{foobar: "foobar"}, nil
			})),
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.False(tt.service.built())
			is.NoError(tt.service.shutdown(ctx))
			_, _ = tt.service.getInstance(nil)
			is.True(tt.service.built())
			if tt.wantErr == nil {
				is.NoError(tt.service.shutdown(ctx))
			} else {
				is.Equal(tt.wantErr, tt.service.shutdown(ctx))
			}
			is.False(tt.service.built())
		})
	}

	// Test with context scenarios
	// Test with canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	service4 := newServiceLazy("foobar", func(i Injector) (*lazyTestShutdownerOK, error) {
		return &lazyTestShutdownerOK{foobar: "foobar"}, nil
	})
	_, _ = service4.getInstance(nil)
	err := service4.shutdown(canceledCtx)
	is.Error(err)
	is.ErrorContains(err, "context canceled")

	// Test with timeout context
	timeoutCtx, cancel2 := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel2()
	time.Sleep(2 * time.Millisecond)
	service5 := newServiceLazy("foobar", func(i Injector) (*lazyTestShutdownerOK, error) {
		return &lazyTestShutdownerOK{foobar: "foobar"}, nil
	})
	_, _ = service5.getInstance(nil)
	err2 := service5.shutdown(timeoutCtx)
	is.Error(err2)
	is.ErrorContains(err2, "context deadline exceeded")

	// Test with value context - verify context value is received
	valueCtx := context.WithValue(context.Background(), ctxTestKey, "shutdown-value")
	serviceWithContext := newServiceLazy("foobar", func(i Injector) (*contextValueShutdownerLazy, error) {
		return &contextValueShutdownerLazy{}, nil
	})
	_, _ = serviceWithContext.getInstance(nil)
	err3 := serviceWithContext.shutdown(valueCtx)
	is.NoError(err3) // Should work normally when context value is correct

	// Test with incorrect context value - verify context value is checked
	incorrectValueCtx := context.WithValue(context.Background(), ctxTestKey, "wrong-value")
	serviceWithIncorrectContext := newServiceLazy("foobar", func(i Injector) (*contextValueShutdownerLazy, error) {
		return &contextValueShutdownerLazy{}, nil
	})
	_, _ = serviceWithIncorrectContext.getInstance(nil)
	err6 := serviceWithIncorrectContext.shutdown(incorrectValueCtx)
	is.Error(err6) // Should fail when context value is incorrect
	is.ErrorContains(err6, "test-key not found or value is incorrect")

	// Test with non-shutdownable service (should return nil even with canceled context)
	nonShutdownableService := newServiceLazy("foobar", func(i Injector) (int, error) {
		return 42, nil
	})
	_, _ = nonShutdownableService.getInstance(nil)
	err4 := nonShutdownableService.shutdown(canceledCtx)
	is.NoError(err4) // Non-shutdownable services return nil regardless of context

	// Test with service that implements ShutdownerWithError (should return context error when context is canceled)
	errorService := newServiceLazy("foobar", func(i Injector) (*lazyTestShutdownerKO, error) {
		return &lazyTestShutdownerKO{foobar: "foobar"}, nil
	})
	_, _ = errorService.getInstance(nil)
	err5 := errorService.shutdown(canceledCtx)
	is.Error(err5)
	is.ErrorContains(err5, "context canceled") // Should return context error when context is canceled
}

func TestServiceLazy_clone(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// initial
	service1 := newServiceLazy("foobar", func(i Injector) (lazyTest, error) {
		return lazyTest{foobar: "foobar"}, nil
	})
	is.Equal("foobar", service1.getName())
	is.False(service1.built)
	_, _ = service1.getInstance(nil)
	is.True(service1.built)

	// clone
	service2, ok := service1.clone(nil).(*serviceLazy[lazyTest])
	is.True(ok)
	is.Equal("foobar", service2.getName())
	is.Empty(service2.instance)
	is.False(service2.built)
	_, _ = service2.getInstance(nil)
	is.NotEmpty(service2.instance)
	is.True(service2.built)

	// change initial and check clone
	service1.name = "baz"
	is.Equal("baz", service1.getName())
	is.Equal("foobar", service2.getName())
}

func TestServiceLazy_source(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Create a service lazy
	service := newServiceLazy("test-lazy", func(i Injector) (lazyTest, error) {
		return lazyTest{foobar: "test-value"}, nil
	})

	// Test initial state - should have provider frame but no invocation frames
	providerFrame, invocationFrames := service.source()

	// Provider frame should be set (from newServiceLazy)
	is.NotEmpty(providerFrame.File, "Provider frame should have a file")
	is.Positive(providerFrame.Line, "Provider frame should have a line number")

	// Initially no invocation frames
	is.Empty(invocationFrames, "Should have no invocation frames initially")

	// Test after getting instance (which should add invocation frames)
	_, err := service.getInstance(nil)
	is.NoError(err, "Should be able to get instance")

	// Check source after invocation
	providerFrame2, invocationFrames2 := service.source()

	// Provider frame should remain the same
	is.Equal(providerFrame, providerFrame2, "Provider frame should remain unchanged")

	// Should now have invocation frames
	is.NotEmpty(invocationFrames2, "Should have invocation frames after getInstance")
	is.Len(invocationFrames2, 1, "Should have exactly one invocation frame")

	// Test multiple invocations
	_, err = service.getInstance(nil)
	is.NoError(err)

	_, invocationFrames3 := service.source()
	is.Len(invocationFrames3, 1, "Should still have only one unique invocation frame (duplicates are ignored)")

	// Test with different service types
	service2 := newServiceLazy("healthchecker-lazy", func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "healthchecker-value"}, nil
	})

	// Get instance multiple times
	_, err = service2.getInstance(nil)
	is.NoError(err)
	_, err = service2.getInstance(nil)
	is.NoError(err)

	providerFrame3, invocationFrames4 := service2.source()
	is.NotEmpty(providerFrame3.File, "Provider frame should be set")
	is.Len(invocationFrames4, 1, "Should have one unique invocation frame")

	// Test concurrent access to source method
	done := make(chan bool, 10)
	for range 10 {
		go func() {
			_, _ = service.source()
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for range 10 {
		<-done
	}

	// Source should still work correctly after concurrent access
	providerFrame4, invocationFrames5 := service.source()
	is.Equal(providerFrame, providerFrame4, "Provider frame should remain consistent under concurrent access")
	is.Len(invocationFrames5, 1, "Invocation frames should remain consistent under concurrent access")

	// Test that invocation frames are properly collected from different call sites
	// Add a frame manually to simulate different call sites
	service.mu.Lock()
	service.invokationFrames[stacktrace.Frame{File: "different_file.go", Line: 42}] = struct{}{}
	service.mu.Unlock()

	_, invocationFrames6 := service.source()
	is.Len(invocationFrames6, 2, "Should have two invocation frames after adding different call site")

	// Verify the frames are different
	frameFiles := make(map[string]bool)
	for _, frame := range invocationFrames6 {
		frameFiles[frame.File] = true
	}
	is.Len(frameFiles, 2, "Should have frames from two different files")

	// Test with primitive types
	intService := newServiceLazy("int-lazy", func(i Injector) (int, error) {
		return 42, nil
	})
	_, err = intService.getInstance(nil)
	is.NoError(err)

	providerFrame5, invocationFrames7 := intService.source()
	is.NotEmpty(providerFrame5.File, "Provider frame should be set for primitive types")
	is.Len(invocationFrames7, 1, "Should have invocation frames for primitive types")

	// Test that the returned frames are copies, not references
	// This ensures thread safety
	originalFrames := make([]stacktrace.Frame, len(invocationFrames6))
	copy(originalFrames, invocationFrames6)

	// Modify the service's internal frames
	service.mu.Lock()
	service.invokationFrames[stacktrace.Frame{File: "new_file.go", Line: 100}] = struct{}{}
	service.mu.Unlock()

	// Get frames again
	_, invocationFrames8 := service.source()

	// Original frames should not be affected
	is.Len(originalFrames, 2, "Original frames should not be affected by internal changes")
	is.Len(invocationFrames8, 3, "New frames should include the additional frame")

	// Test that source works correctly after service is built and reset
	// This tests the lazy service's unique behavior
	_, err = service.getInstance(nil)
	is.NoError(err)
	is.True(service.built, "Service should be built")

	providerFrame6, invocationFrames9 := service.source()
	is.Equal(providerFrame, providerFrame6, "Provider frame should remain unchanged after build")
	is.Len(invocationFrames9, 3, "Should have all invocation frames after build")

	// Test with error provider
	errorService := newServiceLazy("error-lazy", func(i Injector) (lazyTest, error) {
		return lazyTest{}, assert.AnError
	})

	// Even with error, source should still work
	providerFrame7, invocationFrames10 := errorService.source()
	is.NotEmpty(providerFrame7.File, "Provider frame should be set even for error providers")
	is.Empty(invocationFrames10, "Should have no invocation frames for error providers initially")

	// After attempting to get instance (which fails), should still have invocation frames
	// because the frame is collected before the error occurs
	_, err = errorService.getInstance(nil)
	is.Error(err, "Should get error from error provider")

	_, invocationFrames11 := errorService.source()
	is.NotEmpty(invocationFrames11, "Should have invocation frames even after error (frame collected before error)")
}

// Test context value propagation for service lazy
func TestServiceLazy_ContextValuePropagation(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Create service lazy instances with providers that return context-aware services
	healthcheckLazy := newServiceLazy("healthcheck-lazy", func(i Injector) (*contextValueHealthcheckerLazy, error) {
		return &contextValueHealthcheckerLazy{}, nil
	})

	shutdownLazy := newServiceLazy("shutdown-lazy", func(i Injector) (*contextValueShutdownerLazy, error) {
		return &contextValueShutdownerLazy{}, nil
	})

	// Test context value propagation for healthcheck
	ctx1 := context.WithValue(context.Background(), ctxTestKey, "healthcheck-value")
	err := healthcheckLazy.healthcheck(ctx1)
	is.NoError(err)

	// Test context value propagation for shutdown
	ctx2 := context.WithValue(context.Background(), ctxTestKey, "shutdown-value")
	err = shutdownLazy.shutdown(ctx2)
	is.NoError(err)

	// Test that lazy service properly delegates to the underlying instance
	// The lazy service should not store context values itself, but pass them through
	// to the underlying service instance created by the provider
}
