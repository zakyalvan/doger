package doger

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/doger/stacktrace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServiceAlias(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	service1 := newServiceAlias[string, string]("foobar1", i, "foobar2")
	is.Equal("foobar1", service1.name)
	is.Equal(i, service1.scope)
	is.Equal("foobar2", service1.targetName)
}

func TestServiceAlias_getName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	service1 := newServiceAlias[string, string]("foobar1", i, "foobar2")
	is.Equal("foobar1", service1.getName())
}

func TestServiceAlias_getTypeName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	service1 := newServiceAlias[string, int]("foobar1", i, "foobar2")
	is.Equal("int", service1.getTypeName())
}

func TestServiceAlias_getServiceType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	service1 := newServiceAlias[string, string]("foobar1", i, "foobar2")
	is.Equal(ServiceTypeAlias, service1.getServiceType())
}

func TestServiceAlias_getReflectType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceAlias[string, int]("foobar1", nil, "foobar2"), want: "int"},
		{name: "pointer_to_interface", service: newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("foobar2", nil, "foobar3"), want: pkgName + ".Healthchecker"},
		{name: "interface_to_interface", service: newServiceAlias[iTestHeathchecker, Healthchecker]("foobar3", nil, "foobar4"), want: pkgName + ".Healthchecker"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getReflectType().String())
		})
	}
}

func TestServiceAlias_getInstanceAny(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	Provide(i, func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTestHeathcheckerOK, Healthchecker](i))

	// basic type
	service1 := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", i, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	instance1, err1 := service1.getInstanceAny(i)
	is.NoError(err1)
	is.EqualValues(&lazyTestHeathcheckerOK{foobar: "foobar"}, instance1)

	// target service not found
	service2 := newServiceAlias[*lazyTestHeathcheckerOK, int]("github.com/kartaladev/doger.Healthchecker", i, "int")
	instance2, err2 := service2.getInstanceAny(i)
	is.EqualError(err2, "DI: could not find service `int`, available services: `*github.com/kartaladev/doger.lazyTestHeathcheckerOK`, `github.com/kartaladev/doger.Healthchecker`")
	is.EqualValues(0, instance2) // getInstanceAny returns the zero value of the type

	Provide(i, func(i Injector) (int, error) {
		return 42, nil
	})

	// target service found but not convertible type
	service3 := newServiceAlias[*lazyTestHeathcheckerOK, int]("github.com/kartaladev/doger.Healthchecker", i, "int")
	instance3, err3 := service3.getInstanceAny(i)
	is.EqualError(err3, "DI: service found, but type mismatch: invoking `*github.com/kartaladev/doger.lazyTestHeathcheckerOK` but registered `int`")
	is.EqualValues(0, instance3) // getInstanceAny returns the zero value of the type

	// Test with child scopes
	parentScope := New()
	childScope := parentScope.Scope("child")
	Provide(childScope, func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "child-service"}, nil
	})

	// Test alias in child scope accessing child service
	childService := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", childScope, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	childInstance, childErr := childService.getInstanceAny(childScope)
	is.NoError(childErr)
	is.EqualValues(&lazyTestHeathcheckerOK{foobar: "child-service"}, childInstance)

	// Test alias in child scope accessing parent service (should not work)
	parentService := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", childScope, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	parentInstance, parentErr := parentService.getInstanceAny(parentScope)
	is.Error(parentErr)
	is.Nil(parentInstance) // For interface types, zero value is nil

	// Test stacktrace functionality
	stackService := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", i, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	providerFrame, invocationFrames := stackService.source()
	is.NotNil(providerFrame)
	is.Contains(providerFrame.File, "service_alias.go") // Provider frame points to the service_alias.go file
	is.NotNil(invocationFrames)
	is.Empty(invocationFrames) // No invocations yet

	// Invoke the service to generate invocation frames
	_, _ = stackService.getInstanceAny(i)
	providerFrame2, invocationFrames2 := stackService.source()
	is.NotNil(providerFrame2)
	is.NotNil(invocationFrames2)
	is.Len(invocationFrames2, 1) // Should have one invocation frame now
	if len(invocationFrames2) == 1 {
		is.Contains(invocationFrames2[0].File, "service_alias.go") // Invocation frame points to the alias implementation
	}
}

func TestServiceAlias_getInstance(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	Provide(i, func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTestHeathcheckerOK, Healthchecker](i))

	// basic type
	service1 := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", i, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	instance1, err1 := service1.getInstance(i)
	is.NoError(err1)
	is.EqualValues(&lazyTestHeathcheckerOK{foobar: "foobar"}, instance1)

	// target service not found
	service2 := newServiceAlias[*lazyTestHeathcheckerOK, int]("github.com/kartaladev/doger.Healthchecker", i, "int")
	instance2, err2 := service2.getInstance(i)
	is.EqualError(err2, "DI: could not find service `int`, available services: `*github.com/kartaladev/doger.lazyTestHeathcheckerOK`, `github.com/kartaladev/doger.Healthchecker`")
	is.Equal(0, instance2) // For getInstance, we get the zero value of the type

	Provide(i, func(i Injector) (int, error) {
		return 42, nil
	})

	// target service found but not convertible type
	service3 := newServiceAlias[*lazyTestHeathcheckerOK, int]("github.com/kartaladev/doger.Healthchecker", i, "int")
	instance3, err3 := service3.getInstance(i)
	is.EqualError(err3, "DI: service found, but type mismatch: invoking `*github.com/kartaladev/doger.lazyTestHeathcheckerOK` but registered `int`")
	is.Equal(0, instance3) // For getInstance, we get the zero value of the type

	// Test with child scopes
	parentScope := New()
	childScope := parentScope.Scope("child")
	Provide(childScope, func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "child-service"}, nil
	})

	// Test alias in child scope accessing child service
	childService := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", childScope, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	childInstance, childErr := childService.getInstance(childScope)
	is.NoError(childErr)
	is.EqualValues(&lazyTestHeathcheckerOK{foobar: "child-service"}, childInstance)

	// Test alias in child scope accessing parent service (should not work)
	parentService := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", childScope, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	parentInstance, parentErr := parentService.getInstance(parentScope)
	is.Error(parentErr)
	is.Nil(parentInstance) // For interface types, zero value is nil

	// Test stacktrace functionality
	stackService := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", i, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	providerFrame, invocationFrames := stackService.source()
	is.NotNil(providerFrame)
	is.Contains(providerFrame.File, "service_alias.go") // Provider frame points to the service_alias.go file
	is.NotNil(invocationFrames)
	is.Empty(invocationFrames) // No invocations yet

	// Invoke the service to generate invocation frames
	_, _ = stackService.getInstance(i)
	providerFrame2, invocationFrames2 := stackService.source()
	is.NotNil(providerFrame2)
	is.NotNil(invocationFrames2)
	is.Len(invocationFrames2, 1) // Should have one invocation frame now
}

func TestServiceAlias_isHealthchecker(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// no healthcheck
	i1 := New()
	Provide(i1, func(i Injector) (*lazyTest, error) {
		return &lazyTest{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTest, any](i1))
	service1, _ := i1.serviceGet("interface {}")
	is.False(service1.(serviceWrapper[any]).isHealthchecker())

	// target registered: a healthchecker once the target is built
	targets := []struct {
		name     string
		register func(i *RootScope) error // registers the target and its alias
	}{
		{
			name: "healthcheck_ok",
			register: func(i *RootScope) error {
				Provide(i, func(i Injector) (*lazyTestHeathcheckerOK, error) {
					return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
				})
				return As[*lazyTestHeathcheckerOK, Healthchecker](i)
			},
		},
		{
			name: "healthcheck_ko",
			register: func(i *RootScope) error {
				Provide(i, func(i Injector) (*lazyTestHeathcheckerKO, error) {
					return &lazyTestHeathcheckerKO{foobar: "foobar"}, nil
				})
				return As[*lazyTestHeathcheckerKO, Healthchecker](i)
			},
		},
	}

	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := New()
			is.NoError(tt.register(i))
			service, _ := i.serviceGet("github.com/kartaladev/doger.Healthchecker")
			is.False(service.(serviceWrapperIsHealthchecker).isHealthchecker())
			_, _ = service.(serviceWrapperGetInstanceAny).getInstanceAny(i)
			is.True(service.(serviceWrapperIsHealthchecker).isHealthchecker())
		})
	}

	// service not found
	missing := []struct {
		name    string
		service func(scope Injector) serviceWrapperAny
	}{
		{
			name: "wrong_type",
			service: func(scope Injector) serviceWrapperAny {
				return newServiceAlias[*lazyTestHeathcheckerKO, Healthchecker]("github.com/kartaladev/doger.Healthchecker", scope, "*github.com/kartaladev/doger.lazyTestHeathcheckerKO")
			},
		},
		{
			name: "wrong_name",
			service: func(scope Injector) serviceWrapperAny {
				return newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", scope, "*github.com/kartaladev/doger.lazyTestHeathcheckerKO")
			},
		},
	}

	for _, tt := range missing {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := New()
			service := tt.service(i)
			is.False(service.isHealthchecker())
			_, err := service.getInstanceAny(i)
			is.Error(err)
			is.False(service.isHealthchecker())
		})
	}
}

func TestServiceAlias_healthcheck(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	ctx := context.Background()

	// no healthcheck
	i1 := New()
	Provide(i1, func(i Injector) (*lazyTest, error) {
		return &lazyTest{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTest, any](i1))
	service1, _ := i1.serviceGet("interface {}")
	is.NoError(service1.(serviceWrapper[any]).healthcheck(ctx))

	// healthcheck ok
	i2 := New()
	Provide(i2, func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTestHeathcheckerOK, Healthchecker](i2))
	service2, _ := i2.serviceGet("github.com/kartaladev/doger.Healthchecker")
	is.NoError(service2.(serviceWrapper[Healthchecker]).healthcheck(ctx))
	_, _ = service2.(serviceWrapper[Healthchecker]).getInstance(i2)
	is.NoError(service2.(serviceWrapper[Healthchecker]).healthcheck(ctx))

	// healthcheck ko
	i3 := New()
	Provide(i3, func(i Injector) (*lazyTestHeathcheckerKO, error) {
		return &lazyTestHeathcheckerKO{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTestHeathcheckerKO, Healthchecker](i3))
	service3, _ := i3.serviceGet("github.com/kartaladev/doger.Healthchecker")
	is.NoError(service3.(serviceWrapper[Healthchecker]).healthcheck(ctx))
	_, _ = service3.(serviceWrapper[Healthchecker]).getInstance(i3)
	is.Equal(assert.AnError, service3.(serviceWrapper[Healthchecker]).healthcheck(ctx))

	// service not found
	missing := []struct {
		name    string
		service func(scope Injector) serviceWrapperAny
	}{
		{
			name: "wrong_type",
			service: func(scope Injector) serviceWrapperAny {
				return newServiceAlias[*lazyTestHeathcheckerKO, Healthchecker]("github.com/kartaladev/doger.Healthchecker", scope, "*github.com/kartaladev/doger.lazyTestHeathcheckerKO")
			},
		},
		{
			name: "wrong_name",
			service: func(scope Injector) serviceWrapperAny {
				return newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", scope, "*github.com/kartaladev/doger.lazyTestHeathcheckerKO")
			},
		},
	}

	for _, tt := range missing {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := New()
			service := tt.service(i)
			is.NoError(service.healthcheck(ctx))
			_, _ = service.getInstanceAny(i)
			is.NoError(service.healthcheck(ctx))
		})
	}

	// Test with context scenarios
	// Test with canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	service6 := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", i2, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	_, _ = service6.getInstanceAny(i2)
	err := service6.healthcheck(canceledCtx)
	is.Error(err)
	is.ErrorContains(err, "context canceled")

	// Test with timeout context
	timeoutCtx, cancel2 := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel2()
	time.Sleep(2 * time.Millisecond)
	err2 := service6.healthcheck(timeoutCtx)
	is.Error(err2)
	is.ErrorContains(err2, "context deadline exceeded")

	// Test with value context - verify context value is received
	valueCtx := context.WithValue(context.Background(), ctxTestKey, "healthcheck-value")
	i6 := New()
	Provide(i6, func(i Injector) (*contextValueHealthcheckerAlias, error) {
		return &contextValueHealthcheckerAlias{}, nil
	})
	serviceWithContext := newServiceAlias[*contextValueHealthcheckerAlias, HealthcheckerWithContext]("github.com/kartaladev/doger.HealthcheckerWithContext", i6, "*github.com/kartaladev/doger.contextValueHealthcheckerAlias")
	_, _ = serviceWithContext.getInstanceAny(i6)
	err3 := serviceWithContext.healthcheck(valueCtx)
	is.NoError(err3) // Should work normally when context value is correct

	// Test with incorrect context value - verify context value is checked
	incorrectValueCtx := context.WithValue(context.Background(), ctxTestKey, "wrong-value")
	err4 := serviceWithContext.healthcheck(incorrectValueCtx)
	is.Error(err4) // Should fail when context value is incorrect
	is.ErrorContains(err4, "test-key not found or value is incorrect")
}

// @TODO: missing tests for context
func TestServiceAlias_isShutdowner(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// no shutdown
	i1 := New()
	Provide(i1, func(i Injector) (*lazyTest, error) {
		return &lazyTest{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTest, any](i1))
	service1, _ := i1.serviceGet("interface {}")
	is.False(service1.(serviceWrapper[any]).isShutdowner())

	// target registered: a shutdowner once the target is built
	targets := []struct {
		name     string
		register func(i *RootScope) error // registers the target and its alias
	}{
		{
			name: "shutdown_ok",
			register: func(i *RootScope) error {
				Provide(i, func(i Injector) (*lazyTestShutdownerOK, error) {
					return &lazyTestShutdownerOK{foobar: "foobar"}, nil
				})
				return As[*lazyTestShutdownerOK, ShutdownerWithContextAndError](i)
			},
		},
		{
			name: "shutdown_ko",
			register: func(i *RootScope) error {
				Provide(i, func(i Injector) (*contextValueShutdownerAlias, error) {
					return &contextValueShutdownerAlias{}, nil
				})
				return As[*contextValueShutdownerAlias, ShutdownerWithContextAndError](i)
			},
		},
	}

	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := New()
			is.NoError(tt.register(i))
			service, _ := i.serviceGet("github.com/kartaladev/doger.ShutdownerWithContextAndError")
			is.False(service.(serviceWrapper[ShutdownerWithContextAndError]).isShutdowner())
			_, _ = service.(serviceWrapper[ShutdownerWithContextAndError]).getInstance(i)
			is.True(service.(serviceWrapper[ShutdownerWithContextAndError]).isShutdowner())
		})
	}

	// service not found
	missing := []struct {
		name    string
		service func(scope Injector) serviceWrapperAny
	}{
		{
			name: "wrong_type",
			service: func(scope Injector) serviceWrapperAny {
				return newServiceAlias[*lazyTestShutdownerKO, Healthchecker]("*github.com/kartaladev/doger.Healthchecker", scope, "*github.com/kartaladev/doger.lazyTestShutdownerKO")
			},
		},
		{
			name: "wrong_name",
			service: func(scope Injector) serviceWrapperAny {
				return newServiceAlias[*lazyTestShutdownerOK, Healthchecker]("*github.com/kartaladev/doger.Healthchecker", scope, "*github.com/kartaladev/doger.lazyTestShutdownerKO")
			},
		},
	}

	for _, tt := range missing {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := New()
			service := tt.service(i)
			is.False(service.isShutdowner())
			_, _ = service.getInstanceAny(i)
			is.False(service.isShutdowner())
		})
	}
}

func TestServiceAlias_shutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	ctx := context.Background()

	// no shutdown
	i1 := New()
	Provide(i1, func(i Injector) (*lazyTest, error) {
		return &lazyTest{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTest, any](i1))
	service1, _ := i1.serviceGet("interface {}")
	is.NoError(service1.(serviceWrapper[any]).shutdown(ctx))

	// shutdown ok
	i2 := New()
	Provide(i2, func(i Injector) (*lazyTestShutdownerOK, error) {
		return &lazyTestShutdownerOK{foobar: "foobar"}, nil
	})
	is.NoError(As[*lazyTestShutdownerOK, ShutdownerWithContextAndError](i2))
	service2, _ := i2.serviceGet("github.com/kartaladev/doger.ShutdownerWithContextAndError")
	is.NoError(service2.(serviceWrapper[ShutdownerWithContextAndError]).shutdown(ctx))
	_, _ = service2.(serviceWrapper[ShutdownerWithContextAndError]).getInstance(i2)
	is.NoError(service2.(serviceWrapper[ShutdownerWithContextAndError]).shutdown(ctx))

	// shutdown ko
	i3 := New()
	Provide(i3, func(i Injector) (*contextValueShutdownerAlias, error) {
		return &contextValueShutdownerAlias{}, nil
	})
	is.NoError(As[*contextValueShutdownerAlias, ShutdownerWithContextAndError](i3))
	service3, _ := i3.serviceGet("github.com/kartaladev/doger.ShutdownerWithContextAndError")
	is.NoError(service3.(serviceWrapper[ShutdownerWithContextAndError]).shutdown(ctx))
	_, _ = service3.(serviceWrapper[ShutdownerWithContextAndError]).getInstance(i3)
	is.NoError(service3.(serviceWrapper[ShutdownerWithContextAndError]).shutdown(ctx)) // the target is never shut down through its alias

	// service not found
	missing := []struct {
		name    string
		service func(scope Injector) serviceWrapperAny
	}{
		{
			name: "wrong_type",
			service: func(scope Injector) serviceWrapperAny {
				return newServiceAlias[*lazyTestShutdownerKO, Healthchecker]("github.com/kartaladev/doger.Healthchecker", scope, "*github.com/kartaladev/doger.lazyTestShutdownerKO")
			},
		},
		{
			name: "wrong_name",
			service: func(scope Injector) serviceWrapperAny {
				return newServiceAlias[*lazyTestShutdownerOK, Healthchecker]("github.com/kartaladev/doger.Healthchecker", scope, "*github.com/kartaladev/doger.lazyTestHeathcheckerKO")
			},
		},
	}

	for _, tt := range missing {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := New()
			service := tt.service(i)
			is.NoError(service.shutdown(ctx))
			_, _ = service.getInstanceAny(i)
			is.NoError(service.shutdown(ctx))
		})
	}

	// Context scenarios: the alias never shuts its target down, so its own
	// shutdown succeeds whatever the context.
	// Test with canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	service5Ctx := newServiceAlias[*lazyTestShutdownerOK, ShutdownerWithContextAndError]("github.com/kartaladev/doger.ShutdownerWithContextAndError", i2, "*github.com/kartaladev/doger.lazyTestShutdownerOK")
	_, _ = service5Ctx.getInstanceAny(i2)
	err := service5Ctx.shutdown(canceledCtx)
	is.NoError(err)

	// Test with timeout context
	timeoutCtx, cancel2 := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel2()
	time.Sleep(2 * time.Millisecond)
	service6Ctx := newServiceAlias[*lazyTestShutdownerOK, ShutdownerWithContextAndError]("github.com/kartaladev/doger.ShutdownerWithContextAndError", i2, "*github.com/kartaladev/doger.lazyTestShutdownerOK")
	_, _ = service6Ctx.getInstanceAny(i2)
	err2 := service6Ctx.shutdown(timeoutCtx)
	is.NoError(err2)

	// Test with value context - verify context value is received
	valueCtx := context.WithValue(context.Background(), ctxTestKey, "shutdown-value")
	i8 := New()
	Provide(i8, func(i Injector) (*contextValueShutdownerAlias, error) {
		return &contextValueShutdownerAlias{}, nil
	})
	serviceWithContext := newServiceAlias[*contextValueShutdownerAlias, ShutdownerWithContextAndError]("github.com/kartaladev/doger.ShutdownerWithContextAndError", i8, "*github.com/kartaladev/doger.contextValueShutdownerAlias")
	_, _ = serviceWithContext.getInstanceAny(i8)
	err3 := serviceWithContext.shutdown(valueCtx)
	is.NoError(err3) // Should work normally when context value is correct

	// Test with incorrect context value - the target is not called, so the value is not checked
	incorrectValueCtx := context.WithValue(context.Background(), ctxTestKey, "wrong-value")
	i9 := New()
	Provide(i9, func(i Injector) (*contextValueShutdownerAlias, error) {
		return &contextValueShutdownerAlias{}, nil
	})
	serviceWithIncorrectContext := newServiceAlias[*contextValueShutdownerAlias, ShutdownerWithContextAndError]("github.com/kartaladev/doger.ShutdownerWithContextAndError", i9, "*github.com/kartaladev/doger.contextValueShutdownerAlias")
	_, _ = serviceWithIncorrectContext.getInstanceAny(i9)
	err6 := serviceWithIncorrectContext.shutdown(incorrectValueCtx)
	is.NoError(err6)

	// Test with non-shutdownable service (should return nil even with canceled context)
	i6 := New()
	Provide(i6, func(i Injector) (int, error) { return 42, nil })
	nonShutdownableService := newServiceAlias[int, int]("int", i6, "int")
	_, _ = nonShutdownableService.getInstanceAny(i6)
	err4 := nonShutdownableService.shutdown(canceledCtx)
	is.NoError(err4) // Non-shutdownable services return nil regardless of context

	// Test with service that implements ShutdownerWithError (the target is not called, even with a canceled context)
	i7 := New()
	Provide(i7, func(i Injector) (*lazyTestShutdownerKO, error) {
		return &lazyTestShutdownerKO{foobar: "foobar"}, nil
	})
	errorService := newServiceAlias[*lazyTestShutdownerKO, ShutdownerWithError]("github.com/kartaladev/doger.ShutdownerWithError", i7, "*github.com/kartaladev/doger.lazyTestShutdownerKO")
	_, _ = errorService.getInstanceAny(i7)
	err5 := errorService.shutdown(canceledCtx)
	is.NoError(err5)
}

func TestServiceAlias_clone(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Create original service alias
	originalScope := New()
	newScope := New()

	originalService := newServiceAlias[string, int]("test-alias", originalScope, "target-service")

	// Set some invocation frames to test that they are reset
	originalService.mu.Lock()
	originalService.invokationFrames[stacktrace.Frame{File: "test.go", Line: 42}] = struct{}{}
	originalService.invokationFrames[stacktrace.Frame{File: "test2.go", Line: 100}] = struct{}{}
	originalService.invokationFramesCounter = 5
	originalService.mu.Unlock()

	// Clone the service
	clonedServiceAny := originalService.clone(newScope)
	clonedService, ok := clonedServiceAny.(*serviceAlias[string, int])
	is.True(ok, "Clone should return the correct type")

	// Test that all fields are properly copied
	is.Equal(originalService.name, clonedService.name, "Name should be copied")
	is.Equal(originalService.typeName, clonedService.typeName, "Type name should be copied")
	is.Equal(originalService.targetName, clonedService.targetName, "Target name should be copied")
	is.Equal(originalService.providerFrame, clonedService.providerFrame, "Provider frame should be copied")

	// Test that scope is replaced with new scope
	is.Equal(newScope, clonedService.scope, "Scope should be replaced with new scope")
	is.NotEqual(originalService.scope, clonedService.scope, "Original and cloned scopes should be different")

	// Test that invocation frames are reset
	is.Empty(clonedService.invokationFrames, "Invocation frames should be reset to empty")
	is.Equal(uint32(0), clonedService.invokationFramesCounter, "Invocation frames counter should be reset to 0")

	// Test that original service is not affected
	is.NotEmpty(originalService.invokationFrames, "Original service invocation frames should remain unchanged")
	is.Equal(uint32(5), originalService.invokationFramesCounter, "Original service counter should remain unchanged")

	// Test that cloned service has a new mutex (not shared with original)
	originalService.mu.Lock()
	clonedService.mu.Lock()
	// If mutexes were shared, this would deadlock
	//nolint:ineffassign,staticcheck
	ok = !ok // fake stuff to avoid staticcheck warning below
	clonedService.mu.Unlock()
	originalService.mu.Unlock()

	// Test clone with different types
	originalService2 := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("healthchecker-alias", originalScope, "healthchecker-target")
	clonedService2Any := originalService2.clone(newScope)
	clonedService2, ok := clonedService2Any.(*serviceAlias[*lazyTestHeathcheckerOK, Healthchecker])
	is.True(ok, "Clone should return the correct type for different generic types")
	is.Equal(originalService2.name, clonedService2.name)
	is.Equal(originalService2.typeName, clonedService2.typeName)
	is.Equal(newScope, clonedService2.scope)

	// Test clone with nil scope
	originalService3 := newServiceAlias[bool, string]("bool-alias", nil, "bool-target")
	clonedService3Any := originalService3.clone(newScope)
	clonedService3, ok := clonedService3Any.(*serviceAlias[bool, string])
	is.True(ok)
	is.Equal(newScope, clonedService3.scope, "Clone should work even when original scope is nil")

	// Test that cloned service can be used independently
	// This tests that the clone is a completely independent copy
	clonedService.mu.Lock()
	clonedService.invokationFrames[stacktrace.Frame{File: "cloned.go", Line: 1}] = struct{}{}
	clonedService.invokationFramesCounter = 10
	clonedService.mu.Unlock()

	// Original should not be affected
	is.NotContains(originalService.invokationFrames, stacktrace.Frame{File: "cloned.go", Line: 1})
	is.Equal(uint32(5), originalService.invokationFramesCounter)

	// Cloned should have the new data
	is.Contains(clonedService.invokationFrames, stacktrace.Frame{File: "cloned.go", Line: 1})
	is.Equal(uint32(10), clonedService.invokationFramesCounter)
}

func TestServiceAlias_source(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Create a service alias
	scope := New()
	service := newServiceAlias[string, string]("test-alias", scope, "string")

	// Test initial state - should have provider frame but no invocation frames
	providerFrame, invocationFrames := service.source()

	// Provider frame should be set (from newServiceAlias)
	is.NotEmpty(providerFrame.File, "Provider frame should have a file")
	is.Positive(providerFrame.Line, "Provider frame should have a line number")

	// Initially no invocation frames
	is.Empty(invocationFrames, "Should have no invocation frames initially")

	// Test after getting instance (which should add invocation frames)
	// First, we need to provide the target service
	Provide(scope, func(i Injector) (string, error) {
		return "test-value", nil
	})

	// Get instance to trigger invocation frame collection
	_, err := service.getInstance(scope)
	is.NoError(err, "Should be able to get instance")

	// Check source after invocation
	providerFrame2, invocationFrames2 := service.source()

	// Provider frame should remain the same
	is.Equal(providerFrame, providerFrame2, "Provider frame should remain unchanged")

	// Should now have invocation frames
	is.NotEmpty(invocationFrames2, "Should have invocation frames after getInstance")
	is.Len(invocationFrames2, 1, "Should have exactly one invocation frame")

	// Test multiple invocations
	_, err = service.getInstance(scope)
	is.NoError(err)

	_, invocationFrames3 := service.source()
	is.Len(invocationFrames3, 1, "Should still have only one unique invocation frame (duplicates are ignored)")

	// Test with different service types
	service2 := newServiceAlias[*lazyTestHeathcheckerOK, Healthchecker]("healthchecker-alias", scope, "healthchecker-target")

	// Provide the target service
	Provide(scope, func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "test"}, nil
	})
	ProvideNamed(scope, "healthchecker-target", func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "target"}, nil
	})

	// Get instance multiple times
	_, err = service2.getInstance(scope)
	is.NoError(err)
	_, err = service2.getInstance(scope)
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
	// This tests the frame collection mechanism

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
}

// Test services for context value propagation in service alias
type contextValueHealthcheckerAlias struct{}

func (c *contextValueHealthcheckerAlias) HealthCheck(ctx context.Context) error {
	value := ctx.Value(ctxTestKey)
	if value != "healthcheck-value" {
		return fmt.Errorf("test-key not found or value is incorrect")
	}
	return nil
}

type contextValueShutdownerAlias struct{}

func (c *contextValueShutdownerAlias) Shutdown(ctx context.Context) error {
	value := ctx.Value(ctxTestKey)
	if value != "shutdown-value" {
		return fmt.Errorf("test-key not found or value is incorrect")
	}
	return nil
}

// Test context value propagation for service alias
func TestServiceAlias_ContextValuePropagation(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Create injector and services
	injector := New()

	// Create target services
	healthcheckService := &contextValueHealthcheckerAlias{}
	shutdownService := &contextValueShutdownerAlias{}

	// Provide target services
	ProvideNamedValue(injector, "target-healthcheck", healthcheckService)
	ProvideNamedValue(injector, "target-shutdown", shutdownService)

	// Create service aliases
	healthcheckAlias := newServiceAlias[*contextValueHealthcheckerAlias, *contextValueHealthcheckerAlias]("healthcheck-alias", injector, "target-healthcheck")
	shutdownAlias := newServiceAlias[*contextValueShutdownerAlias, *contextValueShutdownerAlias]("shutdown-alias", injector, "target-shutdown")

	// Invoke services to make them healthcheckable/shutdownable
	_, err1 := healthcheckAlias.getInstance(injector)
	_, err2 := shutdownAlias.getInstance(injector)
	is.NoError(err1)
	is.NoError(err2)

	// Test context value propagation for healthcheck
	ctx1 := context.WithValue(context.Background(), ctxTestKey, "healthcheck-value")
	err := healthcheckAlias.healthcheck(ctx1)
	is.NoError(err)

	// Test context value propagation for shutdown
	ctx2 := context.WithValue(context.Background(), ctxTestKey, "shutdown-value")
	err = shutdownAlias.shutdown(ctx2)
	is.NoError(err)

	// Test that alias properly delegates to target service
	// The alias should not store context values itself, but pass them through
	// to the underlying target service
}

// aliasShutdownProbe counts its Shutdown calls.
type aliasShutdownProbe struct {
	calls atomic.Int32
}

func (p *aliasShutdownProbe) Shutdown() error {
	p.calls.Add(1)
	return nil
}

type aliasShutdownIface interface {
	Shutdown() error
}

func TestServiceAlias_ShutdownTargetOnce(t *testing.T) {
	t.Parallel()

	// register registers the target on root and returns its probe.
	eagerValue := func(root *RootScope) *aliasShutdownProbe {
		probe := &aliasShutdownProbe{}
		ProvideValue(root, probe)
		return probe
	}
	lazyBuilt := func(root *RootScope) *aliasShutdownProbe {
		probe := &aliasShutdownProbe{}
		Provide(root, func(Injector) (*aliasShutdownProbe, error) { return probe, nil })
		return probe
	}

	tests := []struct {
		name       string
		register   func(*RootScope) *aliasShutdownProbe
		childAlias bool
		operation  func(t *testing.T, root *RootScope, child *Scope)
		calls      int32
	}{
		{
			name:      "eager value, alias in root, root.Shutdown",
			register:  eagerValue,
			operation: rootShutdownOK,
			calls:     1,
		},
		{
			name:      "lazy built, alias in root, root.Shutdown",
			register:  lazyBuilt,
			operation: rootShutdownOK,
			calls:     1,
		},
		{
			name:       "eager value, alias in child, child.Shutdown",
			register:   eagerValue,
			childAlias: true,
			operation: func(t *testing.T, _ *RootScope, child *Scope) {
				t.Helper()
				report := child.Shutdown()
				assert.True(t, report.Succeed, report.Error())
			},
			calls: 0,
		},
		{
			name:       "eager value, alias in child, root.Shutdown",
			register:   eagerValue,
			childAlias: true,
			operation:  rootShutdownOK,
			calls:      1,
		},
		{
			name:     "eager value, alias in root, ShutdownNamed on the alias",
			register: eagerValue,
			operation: func(t *testing.T, root *RootScope, _ *Scope) {
				t.Helper()
				assert.NoError(t, ShutdownNamed(root, NameOf[aliasShutdownIface]()))
			},
			calls: 0,
		},
		{
			name:     "lazy built, alias in root, ShutdownNamed on the target",
			register: lazyBuilt,
			operation: func(t *testing.T, root *RootScope, _ *Scope) {
				t.Helper()
				assert.NoError(t, ShutdownNamed(root, NameOf[*aliasShutdownProbe]()))
			},
			calls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			must := require.New(t)

			root := New()
			probe := tt.register(root)
			child := root.Scope("c")

			aliasScope := Injector(root)
			if tt.childAlias {
				aliasScope = child
			}
			must.NoError(As[*aliasShutdownProbe, aliasShutdownIface](aliasScope))
			_, err := Invoke[aliasShutdownIface](aliasScope)
			must.NoError(err)

			tt.operation(t, root, child)

			assert.Equal(t, tt.calls, probe.calls.Load())
		})
	}
}

func rootShutdownOK(t *testing.T, root *RootScope, _ *Scope) {
	t.Helper()
	report := root.Shutdown()
	assert.True(t, report.Succeed, report.Error())
}

func TestServiceAlias_ShutdownTargetOnce_Stable(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	for range 200 {
		root := New()
		probe := &aliasShutdownProbe{}
		ProvideValue(root, probe)
		require.NoError(t, As[*aliasShutdownProbe, aliasShutdownIface](root))
		_, err := Invoke[aliasShutdownIface](root)
		require.NoError(t, err)

		is.True(root.Shutdown().Succeed)
		is.Equal(int32(1), probe.calls.Load())
	}
}

func TestServiceAlias_InvokeAfterTargetShutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	must := require.New(t)

	root := New()
	Provide(root, func(Injector) (*aliasShutdownProbe, error) { return &aliasShutdownProbe{}, nil })
	must.NoError(As[*aliasShutdownProbe, aliasShutdownIface](root))
	must.NoError(ShutdownNamed(root, NameOf[*aliasShutdownProbe]()))

	_, err := Invoke[aliasShutdownIface](root)
	assert.ErrorIs(t, err, ErrServiceNotFound)
}
