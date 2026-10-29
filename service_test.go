package doger

import (
	"reflect"
	"testing"
	"time"

	"github.com/kartaladev/doger/stacktrace"
	"github.com/stretchr/testify/assert"
)

func TestInferServiceName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	// more tests in the package
	tests := []struct {
		name  string
		infer func() string
		want  string
	}{
		{name: "int", infer: inferServiceName[int], want: "int"},
		{name: "struct", infer: inferServiceName[eagerTest], want: "github.com/kartaladev/doger.eagerTest"},
		{name: "pointer", infer: inferServiceName[*eagerTest], want: "*github.com/kartaladev/doger.eagerTest"},
		{name: "interface", infer: inferServiceName[Healthchecker], want: "github.com/kartaladev/doger.Healthchecker"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.infer())
		})
	}
}

func TestInferServiceProviderStacktrace(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	Provide(i, func(i Injector) (int, error) {
		return 42, nil
	})

	tests := []struct {
		name         string
		service      serviceWrapperAny
		wantFile     string
		wantFunction string
	}{
		{
			name: "lazy",
			service: newServiceLazy("lazy-service", func(i Injector) (int, error) {
				return 42, nil
			}),
			wantFile:     "service_test.go",
			wantFunction: "TestInferServiceProviderStacktrace",
		},
		{
			name: "eager",
			service: newServiceEager("eager-service", func(i Injector) (int, error) {
				return 42, nil
			}),
			wantFile:     "service_eager.go",
			wantFunction: "newServiceEager",
		},
		{
			name:         "alias",
			service:      newServiceAlias[int, int]("alias-service", i, "int"),
			wantFile:     "service_alias.go", // Provider frame points to the service_alias.go file
			wantFunction: "newServiceAlias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			frame, ok := inferServiceProviderStacktrace(tt.service)
			is.True(ok)
			is.NotNil(frame)
			is.Contains(frame.File, tt.wantFile)
			is.Contains(frame.Function, tt.wantFunction)
		})
	}

	// Test with transient service (should return false)
	transientService := newServiceTransient("transient-service", func(i Injector) (int, error) {
		return 42, nil
	})
	frame3, ok3 := inferServiceProviderStacktrace(transientService)
	is.False(ok3)
	is.Equal(stacktrace.Frame{}, frame3)
}

func TestInferServiceInfo(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test with lazy service
	i := New()
	Provide(i, func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})

	info, ok := inferServiceInfo(i, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	is.True(ok)
	is.Equal("*github.com/kartaladev/doger.lazyTestHeathcheckerOK", info.name)
	is.Equal(ServiceTypeLazy, info.serviceType)
	is.Equal(time.Duration(0), info.serviceBuildTime) // Not built yet
	is.False(info.healthchecker)                      // Not built yet
	is.False(info.shutdowner)                         // Not built yet

	// Build the service
	_, _ = Invoke[*lazyTestHeathcheckerOK](i)

	info2, ok2 := inferServiceInfo(i, "*github.com/kartaladev/doger.lazyTestHeathcheckerOK")
	is.True(ok2)
	is.Equal("*github.com/kartaladev/doger.lazyTestHeathcheckerOK", info2.name)
	is.Equal(ServiceTypeLazy, info2.serviceType)
	is.Positive(info2.serviceBuildTime) // Should have build time now
	is.True(info2.healthchecker)        // Should be healthchecker now
	is.False(info2.shutdowner)          // Not a shutdowner

	// Test with eager service
	i2 := New()
	ProvideValue(i2, &eagerTest{foobar: "foobar"})

	info3, ok3 := inferServiceInfo(i2, "*github.com/kartaladev/doger.eagerTest")
	is.True(ok3)
	is.Equal("*github.com/kartaladev/doger.eagerTest", info3.name)
	is.Equal(ServiceTypeEager, info3.serviceType)
	is.Equal(time.Duration(0), info3.serviceBuildTime) // Eager services don't have build time
	is.False(info3.healthchecker)
	is.False(info3.shutdowner)

	// Test with transient service
	i3 := New()
	ProvideTransient(i3, func(i Injector) (int, error) {
		return 42, nil
	})

	info4, ok4 := inferServiceInfo(i3, "int")
	is.True(ok4)
	is.Equal("int", info4.name)
	is.Equal(ServiceTypeTransient, info4.serviceType)
	is.Equal(time.Duration(0), info4.serviceBuildTime) // Transient services don't have build time
	is.False(info4.healthchecker)
	is.False(info4.shutdowner)

	// Test with service that doesn't exist
	info5, ok5 := inferServiceInfo(i, "non-existent")
	is.False(ok5)
	is.Equal(serviceInfo{}, info5)
}

func TestDoServiceCanCastToGeneric(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	svc1 := newServiceLazy("foobar", func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})
	svc2 := newServiceLazy("foobar", func(i Injector) (iTestHeathchecker, error) {
		return &lazyTestHeathcheckerOK{}, nil
	})

	tests := []struct {
		name    string
		service any
		canCast func(service any) bool
		want    assert.BoolAssertionFunc
	}{
		{name: "own_type", service: svc1, canCast: serviceCanCastToGeneric[*lazyTestHeathcheckerOK], want: assert.True},
		{name: "implemented_interface", service: svc1, canCast: serviceCanCastToGeneric[Healthchecker], want: assert.True},
		{name: "other_interface", service: svc1, canCast: serviceCanCastToGeneric[Shutdowner], want: assert.False},
		{name: "other_type", service: svc1, canCast: serviceCanCastToGeneric[*lazyTestHeathcheckerKO], want: assert.False},
		{name: "interface_implementing_interface", service: svc2, canCast: serviceCanCastToGeneric[Healthchecker], want: assert.True},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want(t, tt.canCast(tt.service))
		})
	}
}

func TestDoServiceCanCastToType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	svc1 := newServiceLazy("foobar", func(i Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})
	svc2 := newServiceLazy("foobar", func(i Injector) (iTestHeathchecker, error) {
		return &lazyTestHeathcheckerOK{}, nil
	})

	tests := []struct {
		name    string
		service any
		toType  reflect.Type
		want    assert.BoolAssertionFunc
	}{
		{name: "own_type", service: svc1, toType: reflect.TypeFor[*lazyTestHeathcheckerOK](), want: assert.True},
		{name: "implemented_interface", service: svc1, toType: reflect.TypeFor[Healthchecker](), want: assert.True},
		{name: "other_interface", service: svc1, toType: reflect.TypeFor[Shutdowner](), want: assert.False},
		{name: "other_type", service: svc1, toType: reflect.TypeFor[*lazyTestHeathcheckerKO](), want: assert.False},
		{name: "interface_implementing_interface", service: svc2, toType: reflect.TypeFor[Healthchecker](), want: assert.True},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want(t, serviceCanCastToType(tt.service, tt.toType))
		})
	}
}
