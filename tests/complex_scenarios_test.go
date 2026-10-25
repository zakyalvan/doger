package tests

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/doger"
	"github.com/stretchr/testify/assert"
)

// TestComplexServiceLifecycle tests complex service lifecycle scenarios
func TestComplexServiceLifecycle(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create services with complex lifecycle dependencies
	shutdownOrder := make([]string, 0)
	var mu sync.Mutex

	// Service that depends on another service
	doger.ProvideNamed(root, "service-a", func(i doger.Injector) (*complexLifecycleService, error) {
		// This service depends on service-b
		_, err := doger.InvokeNamed[*complexLifecycleService](i, "service-b")
		if err != nil {
			return nil, err
		}
		return &complexLifecycleService{name: "service-a", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	// Service that depends on service-a (creates dependency chain)
	doger.ProvideNamed(root, "service-b", func(i doger.Injector) (*complexLifecycleService, error) {
		// This service depends on service-c
		_, err := doger.InvokeNamed[*complexLifecycleService](i, "service-c")
		if err != nil {
			return nil, err
		}
		return &complexLifecycleService{name: "service-b", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	// Base service with no dependencies
	doger.ProvideNamed(root, "service-c", func(i doger.Injector) (*complexLifecycleService, error) {
		return &complexLifecycleService{name: "service-c", shutdownOrder: &shutdownOrder, mu: &mu}, nil
	})

	// Invoke service-a to initialize the entire chain
	_, err := doger.InvokeNamed[*complexLifecycleService](root, "service-a")
	is.NoError(err)

	// Shutdown the root scope
	report := root.Shutdown()
	is.True(report.Succeed)
	is.Empty(report.Errors)

	// Verify all services were shut down
	is.Len(shutdownOrder, 3)
	is.Contains(shutdownOrder, "service-a")
	is.Contains(shutdownOrder, "service-b")
	is.Contains(shutdownOrder, "service-c")
}

// TestComplexConcurrentAccess tests complex concurrent access patterns
func TestComplexConcurrentAccess(t *testing.T) {
	testWithTimeout(t, 450*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create a service that can be accessed concurrently
	doger.ProvideNamed(root, "concurrent-service", func(i doger.Injector) (string, error) {
		time.Sleep(5 * time.Millisecond) // Simulate some work
		return "concurrent-value", nil
	})

	// Service that depends on the concurrent service
	doger.ProvideNamed(root, "dependent-service", func(i doger.Injector) (string, error) {
		concurrentVal, err := doger.InvokeNamed[string](i, "concurrent-service")
		if err != nil {
			return "", err
		}
		return "dependent-" + concurrentVal, nil
	})

	// Concurrently invoke the dependent service
	const numGoroutines = 20
	var wg sync.WaitGroup
	results := make([]string, numGoroutines)
	errors := make([]error, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			result, err := doger.InvokeNamed[string](root, "dependent-service")
			results[index] = result
			errors[index] = err
		}(i)
	}

	wg.Wait()

	// All invocations should succeed with the same result
	for i := range numGoroutines {
		is.NoError(errors[i])
		is.Equal("dependent-concurrent-value", results[i])
	}
}

// TestComplexNestedScopes tests complex nested scope scenarios
func TestComplexNestedScopes(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()
	child1 := root.Scope("child1")
	child2 := child1.Scope("child2")
	child3 := child2.Scope("child3")

	// Services in different scopes
	doger.ProvideNamedValue(root, "root-service", "root-value")
	doger.ProvideNamedValue(child1, "child1-service", "child1-value")
	doger.ProvideNamedValue(child2, "child2-service", "child2-value")
	doger.ProvideNamedValue(child3, "child3-service", "child3-value")

	// Service in deepest scope that depends on all parent services
	doger.ProvideNamed(child3, "deep-dependent", func(i doger.Injector) (string, error) {
		rootVal, err := doger.InvokeNamed[string](i, "root-service")
		if err != nil {
			return "", err
		}
		child1Val, err := doger.InvokeNamed[string](i, "child1-service")
		if err != nil {
			return "", err
		}
		child2Val, err := doger.InvokeNamed[string](i, "child2-service")
		if err != nil {
			return "", err
		}
		child3Val, err := doger.InvokeNamed[string](i, "child3-service")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s-%s-%s-%s", rootVal, child1Val, child2Val, child3Val), nil
	})

	// Invoke the dependent service
	result, err := doger.InvokeNamed[string](child3, "deep-dependent")
	is.NoError(err)
	is.Equal("root-value-child1-value-child2-value-child3-value", result)
}

// TestComplexErrorHandling tests complex error handling scenarios
func TestComplexErrorHandling(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create a chain of services where one fails
	doger.ProvideNamed(root, "service-a", func(i doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](i, "service-b")
		if err != nil {
			return "", err
		}
		return "service-a-value", nil
	})

	doger.ProvideNamed(root, "service-b", func(i doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](i, "service-c")
		if err != nil {
			return "", err
		}
		return "service-b-value", nil
	})

	doger.ProvideNamed(root, "service-c", func(i doger.Injector) (string, error) {
		return "", fmt.Errorf("service-c failed")
	})

	// Attempting to invoke service-a should fail due to cascading failure
	_, err := doger.InvokeNamed[string](root, "service-a")
	is.Error(err)
	is.Contains(err.Error(), "service-c failed")
}

// TestComplexTimeoutScenarios tests complex timeout scenarios
func TestComplexTimeoutScenarios(t *testing.T) {
	testWithTimeout(t, 450*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create a service that takes a long time to resolve
	doger.ProvideNamed(root, "slow-service", func(i doger.Injector) (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "slow-value", nil
	})

	// Service that depends on the slow service
	doger.ProvideNamed(root, "dependent-on-slow", func(i doger.Injector) (string, error) {
		slowVal, err := doger.InvokeNamed[string](i, "slow-service")
		if err != nil {
			return "", err
		}
		return "dependent-" + slowVal, nil
	})

	// Invoke the dependent service
	result, err := doger.InvokeNamed[string](root, "dependent-on-slow")
	is.NoError(err)
	is.Equal("dependent-slow-value", result)
}

// TestComplexMemoryManagement tests complex memory management scenarios
func TestComplexMemoryManagement(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create many services to test memory management
	const numServices = 500

	for i := range numServices {
		serviceName := fmt.Sprintf("service-%d", i)
		serviceIndex := i // Capture the loop variable
		doger.ProvideNamed(root, serviceName, func(i doger.Injector) (string, error) {
			return fmt.Sprintf("value-%d", serviceIndex), nil
		})
	}

	// Invoke random services to test memory management
	const numInvocations = 100
	for i := range numInvocations {
		serviceIndex := i % numServices
		serviceName := fmt.Sprintf("service-%d", serviceIndex)
		result, err := doger.InvokeNamed[string](root, serviceName)
		is.NoError(err)
		is.Equal(fmt.Sprintf("value-%d", serviceIndex), result)
	}

	// Test that services still work after many invocations
	result, err := doger.InvokeNamed[string](root, "service-0")
	is.NoError(err)
	is.Equal("value-0", result)
}

// TestComplexServiceTypes tests complex interactions between different service types
func TestComplexServiceTypes(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create services of different types with complex dependencies
	doger.ProvideNamedValue(root, "eager-base", "eager-base-value")

	doger.ProvideNamed(root, "lazy-dependent", func(i doger.Injector) (string, error) {
		eagerVal, err := doger.InvokeNamed[string](i, "eager-base")
		if err != nil {
			return "", err
		}
		return "lazy-" + eagerVal, nil
	})

	doger.ProvideNamedTransient(root, "transient-dependent", func(i doger.Injector) (string, error) {
		lazyVal, err := doger.InvokeNamed[string](i, "lazy-dependent")
		if err != nil {
			return "", err
		}
		return "transient-" + lazyVal, nil
	})

	// Invoke the transient service multiple times
	result1, err := doger.InvokeNamed[string](root, "transient-dependent")
	is.NoError(err)
	is.Contains(result1, "transient-lazy-eager-base-value")

	result2, err := doger.InvokeNamed[string](root, "transient-dependent")
	is.NoError(err)
	is.Contains(result2, "transient-lazy-eager-base-value")

	// Transient services should return the same value (no timestamp in this test)
	is.Equal(result1, result2)
}

// TestComplexContextHandling tests complex context handling scenarios
func TestComplexContextHandling(t *testing.T) {
	testWithTimeout(t, 450*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create a service that can be shut down with context
	doger.ProvideNamed(root, "context-aware-service", func(i doger.Injector) (*contextAwareService, error) {
		return &contextAwareService{name: "context-aware"}, nil
	})

	// Invoke the service
	_, err := doger.InvokeNamed[*contextAwareService](root, "context-aware-service")
	is.NoError(err)

	// Shutdown with context
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	report := root.ShutdownWithContext(ctx)
	is.True(report.Succeed)
	is.Empty(report.Errors)
}

// Helper types for testing

type complexLifecycleService struct {
	name          string
	shutdownOrder *[]string
	mu            *sync.Mutex
}

func (c *complexLifecycleService) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	*c.shutdownOrder = append(*c.shutdownOrder, c.name)
	c.mu.Unlock()
	return nil
}

type contextAwareService struct {
	name string
}

func (c *contextAwareService) Shutdown(ctx context.Context) error {
	// Simulate some shutdown work
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return nil
	}
}
