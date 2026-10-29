package doger

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type transientTest struct {
	foobar string
}

//nolint:unused
type transientTestHeathcheckerOK struct {
	foobar string
}

//nolint:unused
func (t *transientTestHeathcheckerOK) HealthCheck() error {
	return nil
}

//nolint:unused
type transientTestHeathcheckerKO struct {
	foobar string
}

//nolint:unused
func (t *transientTestHeathcheckerKO) HealthCheck() error {
	return assert.AnError
}

//nolint:unused
type transientTestShutdownerOK struct {
	foobar string
}

//nolint:unused
func (t *transientTestShutdownerOK) Shutdown() error {
	return nil
}

//nolint:unused
type transientTestShutdownerKO struct {
	foobar string
}

//nolint:unused
func (t *transientTestShutdownerKO) Shutdown() error {
	return assert.AnError
}

// transientTestService is a *serviceTransient[T] of any T, for tables whose rows differ in T.
type transientTestService struct {
	serviceWrapperAny
	getInstance func(Injector) (any, error) // the typed getInstance, its instance as any

	// the fields set by newServiceTransient
	name     string
	typeName string
	provider any
}

// transientTestServiceOf wraps s for a table row.
func transientTestServiceOf[T any](s *serviceTransient[T]) transientTestService {
	return transientTestService{
		serviceWrapperAny: s,
		getInstance:       func(i Injector) (any, error) { return s.getInstance(i) },
		name:              s.name,
		typeName:          s.typeName,
		provider:          s.provider,
	}
}

func TestNewServiceTransient(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	testStruct := transientTest{foobar: "test"}

	tests := []struct {
		name         string
		service      transientTestService
		wantName     string
		wantTypeName string
		providerMsg  string
	}{
		{
			name: "int_provider",
			service: transientTestServiceOf(newServiceTransient("int-service", func(i Injector) (int, error) {
				return 42, nil
			})),
			wantName:     "int-service",
			wantTypeName: "int",
			providerMsg:  "Service1 provider should be set",
		},
		{
			name: "string_provider",
			service: transientTestServiceOf(newServiceTransient("string-service", func(i Injector) (string, error) {
				return "test-value", nil
			})),
			wantName:     "string-service",
			wantTypeName: "string",
			providerMsg:  "Service2 provider should be set",
		},
		{
			name: "struct_provider",
			service: transientTestServiceOf(newServiceTransient("struct-service", func(i Injector) (transientTest, error) {
				return testStruct, nil
			})),
			wantName:     "struct-service",
			wantTypeName: "github.com/kartaladev/doger.transientTest",
			providerMsg:  "Service3 provider should be set",
		},
		{
			name: "pointer_provider",
			service: transientTestServiceOf(newServiceTransient("pointer-service", func(i Injector) (*transientTest, error) {
				return &testStruct, nil
			})),
			wantName:     "pointer-service",
			wantTypeName: "*github.com/kartaladev/doger.transientTest",
			providerMsg:  "Service4 provider should be set",
		},
		{
			name: "interface_provider",
			service: transientTestServiceOf(newServiceTransient("interface-service", func(i Injector) (Healthchecker, error) {
				return &lazyTestHeathcheckerOK{foobar: "healthchecker"}, nil
			})),
			wantName:     "interface-service",
			wantTypeName: "github.com/kartaladev/doger.Healthchecker",
			providerMsg:  "Service5 provider should be set",
		},
		{
			name: "slice_provider",
			service: transientTestServiceOf(newServiceTransient("slice-service", func(i Injector) ([]string, error) {
				return []string{"a", "b", "c"}, nil
			})),
			wantName:     "slice-service",
			wantTypeName: "[]string",
			providerMsg:  "Service6 provider should be set",
		},
		{
			name: "map_provider",
			service: transientTestServiceOf(newServiceTransient("map-service", func(i Injector) (map[string]int, error) {
				return map[string]int{"key": 42}, nil
			})),
			wantName:     "map-service",
			wantTypeName: "map[string]int",
			providerMsg:  "Service7 provider should be set",
		},
		{
			name: "boolean_provider",
			service: transientTestServiceOf(newServiceTransient("bool-service", func(i Injector) (bool, error) {
				return true, nil
			})),
			wantName:     "bool-service",
			wantTypeName: "bool",
			providerMsg:  "Service8 provider should be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.Equal(tt.wantName, tt.service.name)
			is.Equal(tt.wantTypeName, tt.service.typeName)
			is.NotNil(tt.service.provider)

			// Test that the service is properly initialized
			is.NotNil(tt.service.provider, tt.providerMsg)
		})
	}
}

func TestServiceTransient_getName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := transientTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (transientTest, error) {
		return test, nil
	}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceTransient("foobar1", provider1), want: "foobar1"},
		{name: "struct", service: newServiceTransient("foobar2", provider2), want: "foobar2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getName())
		})
	}
}

func TestServiceTransient_getTypeName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := transientTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (transientTest, error) {
		return test, nil
	}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceTransient("foobar1", provider1), want: "int"},
		{name: "struct", service: newServiceTransient("foobar2", provider2), want: "github.com/kartaladev/doger.transientTest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getTypeName())
		})
	}
}

func TestServiceTransient_getServiceType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := transientTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (transientTest, error) {
		return test, nil
	}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    ServiceType
	}{
		{name: "int", service: newServiceTransient("foobar1", provider1), want: ServiceTypeTransient},
		{name: "struct", service: newServiceTransient("foobar2", provider2), want: ServiceTypeTransient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getServiceType())
		})
	}
}

func TestServiceTransient_getReflectType(t *testing.T) {
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
		{name: "int", service: newServiceTransient("foobar1", provider1), want: "int"},
		{name: "struct", service: newServiceTransient("foobar2", provider2), want: pkgName + ".lazyTest"},
		{name: "interface", service: newServiceTransient("foobar3", provider3), want: pkgName + ".Healthchecker"},
		{name: "pointer", service: newServiceTransient("foobar1", provider4), want: "*" + pkgName + ".lazyTest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getReflectType().String())
		})
	}
}

func TestServiceTransient_getInstanceAny(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	test := transientTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (transientTest, error) {
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
		{name: "basic_type", service: newServiceTransient("foobar", provider1), want: 42},
		{name: "struct", service: newServiceTransient("hello", provider2), want: test},
		// provider panics, but panic is catched by getInstanceAny
		{name: "provider_panics_error", service: newServiceTransient("plop", provider4), wantErr: fmt.Errorf("error")},
		{name: "provider_returns_error", service: newServiceTransient("plop", provider5), wantErr: fmt.Errorf("error")},
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
		service3 := newServiceTransient("baz", provider3)
		_, _ = service3.getInstanceAny(i)
	})
}

func TestServiceTransient_getInstance(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	test := transientTest{foobar: "foobar"}

	provider1 := func(i Injector) (int, error) {
		return 42, nil
	}
	provider2 := func(i Injector) (transientTest, error) {
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
		service transientTestService
		want    any
		wantErr error // nil: the provider succeeds
	}{
		{name: "basic_type", service: transientTestServiceOf(newServiceTransient("foobar", provider1)), want: 42},
		{name: "struct", service: transientTestServiceOf(newServiceTransient("hello", provider2)), want: test},
		// provider panics, but panic is catched by getInstance
		{name: "provider_panics_error", service: transientTestServiceOf(newServiceTransient("plop", provider4)), wantErr: fmt.Errorf("error")},
		{name: "provider_returns_error", service: transientTestServiceOf(newServiceTransient("plop", provider5)), wantErr: fmt.Errorf("error")},
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
		service3 := newServiceTransient("baz", provider3)
		_, _ = service3.getInstance(i)
	})
}

func TestServiceTransient_isHealthchecker(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// no healthcheck
	service1 := newServiceTransient("foobar", func(i Injector) (lazyTest, error) {
		return lazyTest{foobar: "foobar"}, nil
	})
	is.False(service1.isHealthchecker())

	// healthcheck ok
	service2 := newServiceTransient("foobar", func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})
	is.False(service2.isHealthchecker())
	_, _ = service2.getInstance(nil)
	is.False(service2.isHealthchecker())

	// healthcheck ko
	service3 := newServiceTransient("foobar", func(i Injector) (*lazyTestHeathcheckerKO, error) {
		return &lazyTestHeathcheckerKO{foobar: "foobar"}, nil
	})
	is.False(service3.isHealthchecker())
	_, _ = service3.getInstance(nil)
	is.False(service3.isHealthchecker())

	// Test that transient services don't support healthchecking regardless of provider type
	is.False(service1.isHealthchecker())
	is.False(service2.isHealthchecker())
	is.False(service3.isHealthchecker())

	// Test with HealthcheckerWithContext provider
	service4 := newServiceTransient("foobar", func(i Injector) (*eagerTestHeathcheckerWithContext, error) {
		return &eagerTestHeathcheckerWithContext{foobar: "test", shouldFail: false}, nil
	})
	is.False(service4.isHealthchecker())
	_, _ = service4.getInstance(nil)
	is.False(service4.isHealthchecker())
}

func TestServiceTransient_healthcheck(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	ctx := context.Background()

	tests := []struct {
		name    string
		service transientTestService
	}{
		{
			name: "no_healthcheck",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (lazyTest, error) {
				return lazyTest{foobar: "foobar"}, nil
			})),
		},
		{
			name: "healthcheck_ok",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (*lazyTestHeathcheckerOK, error) {
				return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
			})),
		},
		{
			name: "healthcheck_ko",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (*lazyTestHeathcheckerKO, error) {
				return &lazyTestHeathcheckerKO{foobar: "foobar"}, nil
			})),
		},
		{
			// Test with HealthcheckerWithContext provider: should still return nil even with failing healthchecker
			name: "healthchecker_with_context",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (*eagerTestHeathcheckerWithContext, error) {
				return &eagerTestHeathcheckerWithContext{foobar: "test", shouldFail: true}, nil
			})),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.NoError(tt.service.healthcheck(ctx))
			_, _ = tt.service.getInstance(nil)
			is.NoError(tt.service.healthcheck(ctx))
		})
	}

	// Test with different context scenarios - transient services always return nil
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	service4 := newServiceTransient("foobar", func(i Injector) (lazyTest, error) {
		return lazyTest{foobar: "foobar"}, nil
	})
	is.NoError(service4.healthcheck(canceledCtx))

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	is.NoError(service4.healthcheck(timeoutCtx))

	futureCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	is.NoError(service4.healthcheck(futureCtx))
}

func TestServiceTransient_isShutdowner(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// no shutdown
	service1 := newServiceTransient("foobar", func(i Injector) (lazyTest, error) {
		return lazyTest{foobar: "foobar"}, nil
	})
	is.False(service1.isShutdowner())

	// shutdown ok
	service2 := newServiceTransient("foobar", func(i Injector) (*lazyTestShutdownerOK, error) {
		return &lazyTestShutdownerOK{foobar: "foobar"}, nil
	})
	is.False(service2.isShutdowner())
	_, _ = service2.getInstance(nil)
	is.False(service2.isShutdowner())

	// shutdown ko
	service3 := newServiceTransient("foobar", func(i Injector) (*lazyTestShutdownerKO, error) {
		return &lazyTestShutdownerKO{foobar: "foobar"}, nil
	})
	is.False(service3.isShutdowner())
	_, _ = service3.getInstance(nil)
	is.False(service3.isShutdowner())

	// Test that transient services don't support shutdowning regardless of provider type
	is.False(service1.isShutdowner())
	is.False(service2.isShutdowner())
	is.False(service3.isShutdowner())

	// Test with different shutdowner types
	service4 := newServiceTransient("foobar", func(i Injector) (*eagerTestShutdownerWithContext, error) {
		return &eagerTestShutdownerWithContext{foobar: "test"}, nil
	})
	is.False(service4.isShutdowner())
	_, _ = service4.getInstance(nil)
	is.False(service4.isShutdowner())

	service5 := newServiceTransient("foobar", func(i Injector) (*eagerTestShutdownerVoid, error) {
		return &eagerTestShutdownerVoid{foobar: "test"}, nil
	})
	is.False(service5.isShutdowner())
	_, _ = service5.getInstance(nil)
	is.False(service5.isShutdowner())
}

func TestServiceTransient_shutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	ctx := context.Background()

	tests := []struct {
		name    string
		service transientTestService
	}{
		{
			name: "no_shutdown",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (lazyTest, error) {
				return lazyTest{foobar: "foobar"}, nil
			})),
		},
		{
			name: "shutdown_ok",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (*lazyTestShutdownerOK, error) {
				return &lazyTestShutdownerOK{foobar: "foobar"}, nil
			})),
		},
		{
			name: "shutdown_ko",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (*lazyTestShutdownerKO, error) {
				return &lazyTestShutdownerKO{foobar: "foobar"}, nil
			})),
		},
		{
			// Should still return nil even with context shutdowner
			name: "shutdowner_with_context",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (*eagerTestShutdownerWithContext, error) {
				return &eagerTestShutdownerWithContext{foobar: "test"}, nil
			})),
		},
		{
			// Should still return nil even with void shutdowner
			name: "shutdowner_void",
			service: transientTestServiceOf(newServiceTransient("foobar", func(i Injector) (*eagerTestShutdownerVoid, error) {
				return &eagerTestShutdownerVoid{foobar: "test"}, nil
			})),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			is.NoError(tt.service.shutdown(ctx))
			_, _ = tt.service.getInstance(nil)
			is.NoError(tt.service.shutdown(ctx))
		})
	}

	// Test with different context scenarios - transient services always return nil
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	service4 := newServiceTransient("foobar", func(i Injector) (lazyTest, error) {
		return lazyTest{foobar: "foobar"}, nil
	})
	is.NoError(service4.shutdown(canceledCtx))

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	is.NoError(service4.shutdown(timeoutCtx))

	futureCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	is.NoError(service4.shutdown(futureCtx))
}

func TestServiceTransient_clone(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// initial
	service1 := newServiceTransient("foobar", func(i Injector) (transientTest, error) {
		return transientTest{foobar: "foobar"}, nil
	})
	is.Equal("foobar", service1.getName())
	is.Equal("github.com/kartaladev/doger.transientTest", service1.getTypeName())
	is.NotNil(service1.provider)

	// clone
	service2, ok := service1.clone(nil).(*serviceTransient[transientTest])
	is.True(ok)
	is.Equal("foobar", service2.getName())
	is.Equal("github.com/kartaladev/doger.transientTest", service2.getTypeName())
	is.NotNil(service2.provider)

	// change initial and check clone
	service1.name = "baz"
	is.Equal("baz", service1.getName())
	is.Equal("foobar", service2.getName())

	// Test that clone is independent
	service1.typeName = "changed"
	is.Equal("changed", service1.getTypeName())
	is.Equal("github.com/kartaladev/doger.transientTest", service2.getTypeName())

	// Test with different types
	service3 := newServiceTransient("int-service", func(i Injector) (int, error) {
		return 42, nil
	})
	service4, ok := service3.clone(nil).(*serviceTransient[int])
	is.True(ok)
	is.Equal("int-service", service4.getName())
	is.Equal("int", service4.getTypeName())

	// Test with interface types
	service5 := newServiceTransient("interface-service", func(i Injector) (Healthchecker, error) {
		return &lazyTestHeathcheckerOK{foobar: "healthchecker"}, nil
	})
	service6, ok := service5.clone(nil).(*serviceTransient[Healthchecker])
	is.True(ok)
	is.Equal("interface-service", service6.getName())
	is.Equal("github.com/kartaladev/doger.Healthchecker", service6.getTypeName())

	// Test that cloned services can be used independently
	i := New()
	instance1, err1 := service1.getInstance(i)
	is.NoError(err1)
	is.Equal("foobar", instance1.foobar)

	instance2, err2 := service2.getInstance(i)
	is.NoError(err2)
	is.Equal("foobar", instance2.foobar)

	instance3, err3 := service3.getInstance(i)
	is.NoError(err3)
	is.Equal(42, instance3)

	instance4, err4 := service4.getInstance(i)
	is.NoError(err4)
	is.Equal(42, instance4)
}

func TestServiceTransient_source(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	service1 := newServiceTransient("foobar", func(i Injector) (transientTest, error) {
		return transientTest{foobar: "foobar"}, nil
	})
	_, _ = service1.getInstance(nil)

	a, b := service1.source()
	is.Empty(a)
	is.Empty(b)
}

// Test services for context value propagation in service transient
type contextValueHealthcheckerTransient struct {
	ID int
}

func (c *contextValueHealthcheckerTransient) HealthCheck(ctx context.Context) error {
	value := ctx.Value(ctxTestKey)
	if value != "healthcheck-value" {
		return fmt.Errorf("test-key not found or value is incorrect")
	}
	return nil
}

type contextValueShutdownerTransient struct {
	ID int
}

func (c *contextValueShutdownerTransient) Shutdown(ctx context.Context) error {
	value := ctx.Value(ctxTestKey)
	if value != "shutdown-value" {
		return fmt.Errorf("test-key not found or value is incorrect")
	}
	return nil
}

// Test context value propagation for service transient
// Note: Transient services don't support healthcheck/shutdown, so we test that behavior
func TestServiceTransient_ContextValuePropagation(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Create service transient instances with providers that return context-aware services
	callCount := 0
	healthcheckTransient := newServiceTransient("healthcheck-transient", func(i Injector) (*contextValueHealthcheckerTransient, error) {
		callCount++
		return &contextValueHealthcheckerTransient{ID: callCount}, nil
	})

	shutdownTransient := newServiceTransient("shutdown-transient", func(i Injector) (*contextValueShutdownerTransient, error) {
		return &contextValueShutdownerTransient{ID: 1}, nil
	})

	// Test that transient services don't support healthcheck (should return nil)
	ctx1 := context.WithValue(context.Background(), ctxTestKey, "healthcheck-value")
	err := healthcheckTransient.healthcheck(ctx1)
	is.NoError(err) // Transient services return nil for healthcheck

	// Test that transient services don't support shutdown (should return nil)
	ctx2 := context.WithValue(context.Background(), ctxTestKey, "shutdown-value")
	err = shutdownTransient.shutdown(ctx2)
	is.NoError(err) // Transient services return nil for shutdown

	// Verify that transient services are created fresh each time
	instance1, err := healthcheckTransient.getInstance(nil)
	is.NoError(err)

	instance2, err := healthcheckTransient.getInstance(nil)
	is.NoError(err)

	// These should be different instances (transient behavior)
	is.NotEqual(instance1, instance2)

	// Test that transient services properly ignore context values
	// This is the expected behavior for transient services - they don't participate
	// in healthcheck/shutdown lifecycle, so context values are not propagated
}
