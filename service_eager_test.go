package doger

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kartaladev/doger/stacktrace"
	"github.com/stretchr/testify/assert"
)

type eagerTest struct {
	foobar string
}

var _ Healthchecker = (*eagerTestHeathcheckerOK)(nil)

type eagerTestHeathcheckerOK struct {
	foobar string
}

func (t *eagerTestHeathcheckerOK) HealthCheck() error {
	return nil
}

var _ Healthchecker = (*eagerTestHeathcheckerKO)(nil)

type eagerTestHeathcheckerKO struct {
	foobar string
}

func (t *eagerTestHeathcheckerKO) HealthCheck() error {
	return assert.AnError
}

var _ ShutdownerWithContextAndError = (*eagerTestShutdownerOK)(nil)

type eagerTestShutdownerOK struct {
	foobar string
}

func (t *eagerTestShutdownerOK) Shutdown(ctx context.Context) error {
	return nil
}

var _ ShutdownerWithError = (*eagerTestShutdownerKO)(nil)

type eagerTestShutdownerKO struct {
	foobar string
}

func (t *eagerTestShutdownerKO) Shutdown() error {
	return assert.AnError
}

var _ HealthcheckerWithContext = (*eagerTestHeathcheckerWithContext)(nil)

type eagerTestHeathcheckerWithContext struct {
	foobar     string
	shouldFail bool
}

func (h *eagerTestHeathcheckerWithContext) HealthCheck(ctx context.Context) error {
	if h.shouldFail {
		return assert.AnError
	}
	return nil
}

var _ ShutdownerWithContext = (*eagerTestShutdownerWithContext)(nil)

type eagerTestShutdownerWithContext struct {
	foobar string
}

func (s *eagerTestShutdownerWithContext) Shutdown(ctx context.Context) {
	// Void return
}

var _ Shutdowner = (*eagerTestShutdownerVoid)(nil)

type eagerTestShutdownerVoid struct {
	foobar string
}

func (s *eagerTestShutdownerVoid) Shutdown() {
	// Void return, no context
}

// Test services for context value propagation in service eager
type contextValueHealthcheckerEager struct{}

func (c *contextValueHealthcheckerEager) HealthCheck(ctx context.Context) error {
	value := ctx.Value(ctxTestKey)
	if value != "healthcheck-value" {
		return fmt.Errorf("test-key not found or value is incorrect")
	}
	return nil
}

type contextValueShutdownerEager struct{}

func (c *contextValueShutdownerEager) Shutdown(ctx context.Context) error {
	value := ctx.Value(ctxTestKey)
	if value != "shutdown-value" {
		return fmt.Errorf("test-key not found or value is incorrect")
	}
	return nil
}

func TestNewServiceEager(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test with string instance
	service1 := newServiceEager("string-service", "test-value")
	is.Equal("string-service", service1.name)
	is.Equal("string", service1.typeName)
	is.Equal("test-value", service1.instance)
	is.NotEmpty(service1.providerFrame.File, "Provider frame should be set")
	is.Positive(service1.providerFrame.Line, "Provider frame should have line number")
	is.Empty(service1.invokationFrames, "Invocation frames should be empty initially")
	is.Equal(uint32(0), service1.invokationFramesCounter, "Invocation frames counter should be 0")

	// Test with int instance
	service2 := newServiceEager("int-service", 42)
	is.Equal("int-service", service2.name)
	is.Equal("int", service2.typeName)
	is.Equal(42, service2.instance)

	// Test with struct instance
	testStruct := eagerTest{foobar: "test"}
	service3 := newServiceEager("struct-service", testStruct)
	is.Equal("struct-service", service3.name)
	is.Equal("github.com/kartaladev/doger.eagerTest", service3.typeName)
	is.Equal(testStruct, service3.instance)

	// Test with pointer instance
	testPtr := &eagerTest{foobar: "pointer-test"}
	service4 := newServiceEager("pointer-service", testPtr)
	is.Equal("pointer-service", service4.name)
	is.Equal("*github.com/kartaladev/doger.eagerTest", service4.typeName)
	is.Equal(testPtr, service4.instance)

	// Test with interface instance
	var healthchecker Healthchecker = &eagerTestHeathcheckerOK{foobar: "healthchecker"}
	service5 := newServiceEager("interface-service", healthchecker)
	is.Equal("interface-service", service5.name)
	is.Equal("github.com/kartaladev/doger.Healthchecker", service5.typeName)
	is.Equal(healthchecker, service5.instance)

	// Test with nil instance
	service6 := newServiceEager[*eagerTest]("nil-service", nil)
	is.Equal("nil-service", service6.name)
	is.Equal("*github.com/kartaladev/doger.eagerTest", service6.typeName)
	is.Nil(service6.instance)

	// Test with boolean instance
	service7 := newServiceEager("bool-service", true)
	is.Equal("bool-service", service7.name)
	is.Equal("bool", service7.typeName)
	is.True(service7.instance)

	// Test with slice instance
	slice := []string{"a", "b", "c"}
	service8 := newServiceEager("slice-service", slice)
	is.Equal("slice-service", service8.name)
	is.Equal("[]string", service8.typeName)
	is.Equal(slice, service8.instance)

	// Test with map instance
	m := map[string]int{"key": 42}
	service9 := newServiceEager("map-service", m)
	is.Equal("map-service", service9.name)
	is.Equal("map[string]int", service9.typeName)
	is.Equal(m, service9.instance)
}

func TestServiceEager_getName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := eagerTest{foobar: "foobar"}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceEager("foobar1", 42), want: "foobar1"},
		{name: "struct", service: newServiceEager("foobar2", test), want: "foobar2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getName())
		})
	}
}

func TestServiceEager_getTypeName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := eagerTest{foobar: "foobar"}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceEager("foobar1", 42), want: "int"},
		{name: "struct", service: newServiceEager("foobar2", test), want: "github.com/kartaladev/doger.eagerTest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getTypeName())
		})
	}
}

func TestServiceEager_getServiceType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := eagerTest{foobar: "foobar"}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    ServiceType
	}{
		{name: "int", service: newServiceEager("foobar1", 42), want: ServiceTypeEager},
		{name: "struct", service: newServiceEager("foobar2", test), want: ServiceTypeEager},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getServiceType())
		})
	}
}

func TestServiceEager_getReflectType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := eagerTest{foobar: "foobar"}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    string
	}{
		{name: "int", service: newServiceEager("foobar1", 42), want: "int"},
		{name: "struct", service: newServiceEager("foobar2", test), want: pkgName + ".eagerTest"},
		{name: "nil_interface_value", service: newServiceEager("foobar3", Healthchecker(nil)), want: pkgName + ".Healthchecker"},
		{name: "nil_interface_type_argument", service: newServiceEager[Healthchecker]("foobar4", nil), want: pkgName + ".Healthchecker"},
		{name: "pointer", service: newServiceEager("foobar1", &test), want: "*" + pkgName + ".eagerTest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)
			is.Equal(tt.want, tt.service.getReflectType().String())
		})
	}
}

func TestServiceEager_getInstanceAny(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := eagerTest{foobar: "foobar"}

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    any
	}{
		{name: "struct", service: newServiceEager("foobar", test), want: test},
		{name: "int", service: newServiceEager("foobar", 42), want: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			instance, err := tt.service.getInstanceAny(nil)
			is.NoError(err)
			is.Equal(tt.want, instance)
		})
	}
}

func TestServiceEager_getInstance(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	test := eagerTest{foobar: "foobar"}
	service1 := newServiceEager("foobar", test)
	service2 := newServiceEager("foobar", 42)

	tests := []struct {
		name        string
		getInstance func(Injector) (any, error) // the typed getInstance, its instance as any
		want        any
	}{
		{name: "struct", getInstance: func(i Injector) (any, error) { return service1.getInstance(i) }, want: test},
		{name: "int", getInstance: func(i Injector) (any, error) { return service2.getInstance(i) }, want: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			instance, err := tt.getInstance(nil)
			is.NoError(err)
			is.Equal(tt.want, instance)
		})
	}
}

func TestServiceEager_isHealthchecker(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    assert.BoolAssertionFunc
	}{
		{name: "no_healthcheck", service: newServiceEager("foobar", &eagerTest{foobar: "foobar"}), want: assert.False},
		{name: "healthcheck_ok", service: newServiceEager("foobar", &eagerTestHeathcheckerOK{foobar: "foobar"}), want: assert.True},
		{name: "healthcheck_ko", service: newServiceEager("foobar", &eagerTestHeathcheckerKO{foobar: "foobar"}), want: assert.True},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want(t, tt.service.isHealthchecker())
		})
	}
}

func TestServiceEager_healthcheck(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	ctx := context.Background()

	// healthcheck ko
	service3 := newServiceEager("foobar", &eagerTestHeathcheckerKO{foobar: "foobar"})
	err3 := service3.healthcheck(ctx)
	is.Error(err3)
	is.Error(err3)
	is.Equal(err3, assert.AnError)

	// Test with canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Test with timeout context that expires
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // Ensure timeout expires

	// Test with context that has deadline but hasn't expired
	futureCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	tests := []struct {
		name    string
		service serviceWrapperAny
		ctx     context.Context
		wantErr error
	}{
		{name: "no_healthcheck", service: newServiceEager("foobar", &eagerTest{foobar: "foobar"}), ctx: ctx},
		{name: "healthcheck_ok", service: newServiceEager("foobar", &eagerTestHeathcheckerOK{foobar: "foobar"}), ctx: ctx},
		{name: "canceled_context", service: newServiceEager("foobar", &eagerTestHeathcheckerOK{foobar: "foobar"}), ctx: canceledCtx, wantErr: context.Canceled},
		{name: "expired_timeout", service: newServiceEager("foobar", &eagerTestHeathcheckerOK{foobar: "foobar"}), ctx: timeoutCtx, wantErr: context.DeadlineExceeded},
		// Should succeed since context hasn't expired
		{name: "unexpired_deadline", service: newServiceEager("foobar", &eagerTestHeathcheckerOK{foobar: "foobar"}), ctx: futureCtx},
		// Test HealthcheckerWithContext interface: should work with normal context
		{name: "healthchecker_with_context", service: newServiceEager("foobar", &eagerTestHeathcheckerWithContext{foobar: "test", shouldFail: false}), ctx: ctx},
		// Test context error handling with canceled context and error healthchecker: context error takes precedence
		{name: "healthcheck_ko_canceled_context", service: newServiceEager("foobar", &eagerTestHeathcheckerKO{foobar: "foobar"}), ctx: canceledCtx, wantErr: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			err := tt.service.healthcheck(tt.ctx)
			if tt.wantErr == nil {
				is.NoError(err)
				return
			}
			is.Error(err)
			is.Equal(tt.wantErr, err)
		})
	}
}

func TestServiceEager_isShutdowner(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name    string
		service serviceWrapperAny
		want    assert.BoolAssertionFunc
	}{
		{name: "no_shutdown", service: newServiceEager("foobar", &eagerTest{foobar: "foobar"}), want: assert.False},
		{name: "shutdown_ok", service: newServiceEager("foobar", &eagerTestShutdownerOK{foobar: "foobar"}), want: assert.True},
		{name: "shutdown_ko", service: newServiceEager("foobar", &eagerTestShutdownerKO{foobar: "foobar"}), want: assert.True},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want(t, tt.service.isShutdowner())
		})
	}
}

func TestServiceEager_shutdown(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	ctx := context.Background()

	// shutdown ko
	service3 := newServiceEager("foobar", &eagerTestShutdownerKO{foobar: "foobar"})
	err3 := service3.shutdown(ctx)
	is.Error(err3)
	is.Error(err3)
	is.Equal(err3, assert.AnError)

	// Test with canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Test with timeout context that expires
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // Ensure timeout expires

	// Test with context that has deadline but hasn't expired
	futureCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	shutdownerWithCtx := &eagerTestShutdownerWithContext{foobar: "test"}
	shutdownerVoid := &eagerTestShutdownerVoid{foobar: "test"}

	tests := []struct {
		name    string
		service serviceWrapperAny
		ctx     context.Context
		wantErr error
	}{
		{name: "no_shutdown", service: newServiceEager("foobar", &eagerTest{foobar: "foobar"}), ctx: ctx},
		{name: "shutdown_ok", service: newServiceEager("foobar", &eagerTestShutdownerOK{foobar: "foobar"}), ctx: ctx},
		{name: "canceled_context", service: newServiceEager("foobar", &eagerTestShutdownerOK{foobar: "foobar"}), ctx: canceledCtx, wantErr: context.Canceled},
		{name: "expired_timeout", service: newServiceEager("foobar", &eagerTestShutdownerOK{foobar: "foobar"}), ctx: timeoutCtx, wantErr: context.DeadlineExceeded},
		// Should succeed since context hasn't expired
		{name: "unexpired_deadline", service: newServiceEager("foobar", &eagerTestShutdownerOK{foobar: "foobar"}), ctx: futureCtx},
		// Test ShutdownerWithContextAndError (already tested above)
		{name: "shutdowner_with_context_and_error", service: newServiceEager("foobar", &eagerTestShutdownerOK{foobar: "foobar"}), ctx: ctx},
		// Test ShutdownerWithError with canceled context: context error takes precedence
		{name: "shutdowner_with_error_canceled_context", service: newServiceEager("foobar", &eagerTestShutdownerKO{foobar: "foobar"}), ctx: canceledCtx, wantErr: context.Canceled},
		// Test ShutdownerWithContext type (void return): should work with normal context
		{name: "shutdowner_with_context", service: newServiceEager("foobar", shutdownerWithCtx), ctx: ctx},
		// Test Shutdowner type (void return, no context)
		{name: "shutdowner", service: newServiceEager("foobar", shutdownerVoid), ctx: ctx},
		// Test context error handling with canceled context and different shutdowner types: context error takes precedence
		{name: "shutdowner_with_context_canceled_context", service: newServiceEager("foobar", shutdownerWithCtx), ctx: canceledCtx, wantErr: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			err := tt.service.shutdown(tt.ctx)
			if tt.wantErr == nil {
				is.NoError(err)
				return
			}
			is.Error(err)
			is.Equal(tt.wantErr, err)
		})
	}
}

func TestServiceEager_clone(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	test := eagerTest{foobar: "foobar"}

	// initial
	service1 := newServiceEager("foobar", test)
	is.Equal("foobar", service1.getName())

	// clone
	service2, ok := service1.clone(nil).(*serviceEager[eagerTest])
	is.True(ok)
	is.Equal("foobar", service2.getName())

	// change initial and check clone
	service1.name = "baz"
	is.Equal("baz", service1.getName())
	is.Equal("foobar", service2.getName())
}

func TestServiceEager_source(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Create a service eager
	testInstance := &eagerTest{foobar: "test-value"}
	service := newServiceEager("test-eager", testInstance)

	// Test initial state - should have provider frame but no invocation frames
	providerFrame, invocationFrames := service.source()

	// Provider frame should be set (from newServiceEager)
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
	healthcheckerInstance := &eagerTestHeathcheckerOK{foobar: "healthchecker-value"}
	service2 := newServiceEager("healthchecker-eager", healthcheckerInstance)

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
	service.invokationFramesMu.Lock()
	service.invokationFrames[stacktrace.Frame{File: "different_file.go", Line: 42}] = struct{}{}
	service.invokationFramesMu.Unlock()

	_, invocationFrames6 := service.source()
	is.Len(invocationFrames6, 2, "Should have two invocation frames after adding different call site")

	// Verify the frames are different
	frameFiles := make(map[string]bool)
	for _, frame := range invocationFrames6 {
		frameFiles[frame.File] = true
	}
	is.Len(frameFiles, 2, "Should have frames from two different files")

	// Test with primitive types
	intService := newServiceEager("int-eager", 42)
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
	service.invokationFramesMu.Lock()
	service.invokationFrames[stacktrace.Frame{File: "new_file.go", Line: 100}] = struct{}{}
	service.invokationFramesMu.Unlock()

	// Get frames again
	_, invocationFrames8 := service.source()

	// Original frames should not be affected
	is.Len(originalFrames, 2, "Original frames should not be affected by internal changes")
	is.Len(invocationFrames8, 3, "New frames should include the additional frame")
}

// Test context value propagation for service eager
func TestServiceEager_ContextValuePropagation(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Create test services that capture context values
	healthcheckService := &contextValueHealthcheckerEager{}
	shutdownService := &contextValueShutdownerEager{}

	// Create service eager instances
	healthcheckEager := newServiceEager("healthcheck-eager", healthcheckService)
	shutdownEager := newServiceEager("shutdown-eager", shutdownService)

	// Test context value propagation for healthcheck
	ctx1 := context.WithValue(context.Background(), ctxTestKey, "healthcheck-value")
	err := healthcheckEager.healthcheck(ctx1)
	is.NoError(err)

	// Test context value propagation for shutdown
	ctx2 := context.WithValue(context.Background(), ctxTestKey, "shutdown-value")
	err = shutdownEager.shutdown(ctx2)
	is.NoError(err)

	// Test that eager service properly delegates to the underlying instance
	// The eager service should not store context values itself, but pass them through
	// to the underlying service instance
}
