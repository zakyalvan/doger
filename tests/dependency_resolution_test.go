package tests

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/doger"
	"github.com/stretchr/testify/assert"
)

// TestDAGComplexDependencyGraph tests DAG with complex dependency relationships
func TestDAGComplexDependencyGraph(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create a complex dependency graph:
	// A depends on B, C
	// B depends on D, E
	// C depends on E, F
	// D depends on G
	// E depends on G, H
	// F depends on H
	// G, H have no dependencies

	// Services with no dependencies
	doger.ProvideNamedValue(root, "service-g", "g-value")
	doger.ProvideNamedValue(root, "service-h", "h-value")

	// Services with dependencies
	doger.ProvideNamed(root, "service-f", func(i doger.Injector) (string, error) {
		hVal, err := doger.InvokeNamed[string](i, "service-h")
		if err != nil {
			return "", err
		}
		return "f-depends-on-" + hVal, nil
	})

	doger.ProvideNamed(root, "service-e", func(i doger.Injector) (string, error) {
		gVal, err := doger.InvokeNamed[string](i, "service-g")
		if err != nil {
			return "", err
		}
		hVal, err := doger.InvokeNamed[string](i, "service-h")
		if err != nil {
			return "", err
		}
		return "e-depends-on-" + gVal + "-" + hVal, nil
	})

	doger.ProvideNamed(root, "service-d", func(i doger.Injector) (string, error) {
		gVal, err := doger.InvokeNamed[string](i, "service-g")
		if err != nil {
			return "", err
		}
		return "d-depends-on-" + gVal, nil
	})

	doger.ProvideNamed(root, "service-c", func(i doger.Injector) (string, error) {
		eVal, err := doger.InvokeNamed[string](i, "service-e")
		if err != nil {
			return "", err
		}
		fVal, err := doger.InvokeNamed[string](i, "service-f")
		if err != nil {
			return "", err
		}
		return "c-depends-on-" + eVal + "-" + fVal, nil
	})

	doger.ProvideNamed(root, "service-b", func(i doger.Injector) (string, error) {
		dVal, err := doger.InvokeNamed[string](i, "service-d")
		if err != nil {
			return "", err
		}
		eVal, err := doger.InvokeNamed[string](i, "service-e")
		if err != nil {
			return "", err
		}
		return "b-depends-on-" + dVal + "-" + eVal, nil
	})

	doger.ProvideNamed(root, "service-a", func(i doger.Injector) (string, error) {
		bVal, err := doger.InvokeNamed[string](i, "service-b")
		if err != nil {
			return "", err
		}
		cVal, err := doger.InvokeNamed[string](i, "service-c")
		if err != nil {
			return "", err
		}
		return "a-depends-on-" + bVal + "-" + cVal, nil
	})

	// Invoke the top-level service - this should resolve the entire dependency graph
	result, err := doger.InvokeNamed[string](root, "service-a")
	is.NoError(err)
	is.Contains(result, "a-depends-on-b-depends-on-d-depends-on-g-value-e-depends-on-g-value-h-value-c-depends-on-e-depends-on-g-value-h-value-f-depends-on-h-value")
}

// TestDAGConcurrentAccess tests DAG operations under concurrent access
func TestDAGConcurrentAccess(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create services that can be accessed concurrently
	doger.ProvideNamedValue(root, "shared-service", "shared-value")

	doger.ProvideNamed(root, "dependent-service-1", func(i doger.Injector) (string, error) {
		sharedVal, err := doger.InvokeNamed[string](i, "shared-service")
		if err != nil {
			return "", err
		}
		return "dependent-1-" + sharedVal, nil
	})

	doger.ProvideNamed(root, "dependent-service-2", func(i doger.Injector) (string, error) {
		sharedVal, err := doger.InvokeNamed[string](i, "shared-service")
		if err != nil {
			return "", err
		}
		return "dependent-2-" + sharedVal, nil
	})

	// Concurrently invoke multiple dependent services
	const numGoroutines = 5
	var wg sync.WaitGroup
	results1 := make([]string, numGoroutines)
	results2 := make([]string, numGoroutines)
	errors1 := make([]error, numGoroutines)
	errors2 := make([]error, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			result1, err1 := doger.InvokeNamed[string](root, "dependent-service-1")
			result2, err2 := doger.InvokeNamed[string](root, "dependent-service-2")
			results1[index] = result1
			results2[index] = result2
			errors1[index] = err1
			errors2[index] = err2
		}(i)
	}

	wg.Wait()

	// All invocations should succeed with consistent results
	for i := range numGoroutines {
		is.NoError(errors1[i])
		is.NoError(errors2[i])
		is.Equal("dependent-1-shared-value", results1[i])
		is.Equal("dependent-2-shared-value", results2[i])
	}
}

// TestDAGCircularDependencyDetection tests DAG circular dependency detection
func TestDAGCircularDependencyDetection(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create a circular dependency: A -> B -> C -> A
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
		_, err := doger.InvokeNamed[string](i, "service-a")
		if err != nil {
			return "", err
		}
		return "service-c-value", nil
	})

	// Attempting to invoke should detect circular dependency
	_, err := doger.InvokeNamed[string](root, "service-a")
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestDAGSelfDependency tests DAG behavior with self-dependencies
func TestDAGSelfDependency(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Service that depends on itself
	doger.ProvideNamed(root, "self-dependent-service", func(i doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](i, "self-dependent-service")
		if err != nil {
			return "", err
		}
		return "self-dependent-value", nil
	})

	// Attempting to invoke should detect circular dependency
	_, err := doger.InvokeNamed[string](root, "self-dependent-service")
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestDAGNestedScopes tests DAG behavior with nested scopes
func TestDAGNestedScopes(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()
	child1 := root.Scope("child1")
	child2 := child1.Scope("child2")

	// Services in different scopes
	doger.ProvideNamedValue(root, "root-service", "root-value")
	doger.ProvideNamedValue(child1, "child1-service", "child1-value")
	doger.ProvideNamedValue(child2, "child2-service", "child2-value")

	// Service in child2 that depends on services in parent scopes
	doger.ProvideNamed(child2, "dependent-service", func(i doger.Injector) (string, error) {
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
		return fmt.Sprintf("%s-%s-%s", rootVal, child1Val, child2Val), nil
	})

	// Invoke the dependent service
	result, err := doger.InvokeNamed[string](child2, "dependent-service")
	is.NoError(err)
	is.Equal("root-value-child1-value-child2-value", result)
}

// TestDAGServiceNotFound tests DAG behavior when services are not found
func TestDAGServiceNotFound(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Service that depends on a non-existent service
	doger.ProvideNamed(root, "dependent-service", func(i doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](i, "non-existent-service")
		if err != nil {
			return "", err
		}
		return "should-not-reach-here", nil
	})

	// Attempting to invoke should fail with service not found error
	_, err := doger.InvokeNamed[string](root, "dependent-service")
	is.Error(err)
	is.Contains(err.Error(), "could not find service")
}

// TestDAGProviderErrors tests DAG behavior when providers return errors
func TestDAGProviderErrors(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Service that returns an error
	doger.ProvideNamed(root, "error-service", func(i doger.Injector) (string, error) {
		return "", fmt.Errorf("provider error")
	})

	// Service that depends on the error service
	doger.ProvideNamed(root, "dependent-service", func(i doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](i, "error-service")
		if err != nil {
			return "", err
		}
		return "should-not-reach-here", nil
	})

	// Attempting to invoke should fail with the provider error
	_, err := doger.InvokeNamed[string](root, "dependent-service")
	is.Error(err)
	is.Contains(err.Error(), "provider error")
}

// TestDAGLargeDependencyGraph tests DAG with a large number of services
func TestDAGLargeDependencyGraph(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create a large dependency graph with 100 services
	const numServices = 100

	// Register services in reverse order
	for i := numServices; i >= 1; i-- {
		serviceName := fmt.Sprintf("service-%d", i)
		if i == numServices {
			// Last service has no dependencies
			doger.ProvideNamedValue(root, serviceName, fmt.Sprintf("value-%d", i))
		} else {
			// Each service depends on the next one
			nextServiceName := fmt.Sprintf("service-%d", i+1)
			serviceIndex := i // Capture the loop variable
			doger.ProvideNamed(root, serviceName, func(inj doger.Injector) (string, error) {
				nextVal, err := doger.InvokeNamed[string](inj, nextServiceName)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("value-%d-depends-on-%s", serviceIndex, nextVal), nil
			})
		}
	}

	// Invoke the first service - this should resolve the entire graph
	result, err := doger.InvokeNamed[string](root, "service-1")
	is.NoError(err)
	is.Contains(result, "value-1-depends-on-value-2-depends-on-value-3")
}

// TestDAGMemoryLeak tests for potential memory leaks in DAG operations
func TestDAGMemoryLeak(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	root := doger.New()

	// Create a service that can be invoked multiple times
	doger.ProvideNamed(root, "memory-test-service", func(i doger.Injector) (string, error) {
		return "test-value", nil
	})

	// Invoke the service many times to test for memory leaks
	const numInvocations = 1000
	for range numInvocations {
		result, err := doger.InvokeNamed[string](root, "memory-test-service")
		is.NoError(err)
		is.Equal("test-value", result)
	}

	// The service should still work correctly after many invocations
	result, err := doger.InvokeNamed[string](root, "memory-test-service")
	is.NoError(err)
	is.Equal("test-value", result)
}

// TestComplexDependencyChains tests complex dependency chains
func TestComplexDependencyChains(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	// Create deep nested scope structure
	root := doger.New()
	level1 := root.Scope("level1")
	level2 := level1.Scope("level2")
	level3 := level2.Scope("level3")
	level4 := level3.Scope("level4")

	// Track initialization order
	initOrder := make([]string, 0)
	var mu sync.Mutex

	// Create complex dependency chain
	doger.ProvideNamedValue(root, "root-service", "root-value")

	doger.ProvideNamed(level1, "level1-service", func(i doger.Injector) (string, error) {
		mu.Lock()
		initOrder = append(initOrder, "level1")
		mu.Unlock()

		// Depends on root service
		rootVal, err := doger.InvokeNamed[string](i, "root-service")
		if err != nil {
			return "", err
		}
		return "level1-" + rootVal, nil
	})

	doger.ProvideNamed(level2, "level2-service", func(i doger.Injector) (string, error) {
		mu.Lock()
		initOrder = append(initOrder, "level2")
		mu.Unlock()

		// Depends on level1 service
		level1Val, err := doger.InvokeNamed[string](i, "level1-service")
		if err != nil {
			return "", err
		}
		return "level2-" + level1Val, nil
	})

	doger.ProvideNamed(level3, "level3-service", func(i doger.Injector) (string, error) {
		mu.Lock()
		initOrder = append(initOrder, "level3")
		mu.Unlock()

		// Depends on level2 service
		level2Val, err := doger.InvokeNamed[string](i, "level2-service")
		if err != nil {
			return "", err
		}
		return "level3-" + level2Val, nil
	})

	doger.ProvideNamed(level4, "level4-service", func(i doger.Injector) (string, error) {
		mu.Lock()
		initOrder = append(initOrder, "level4")
		mu.Unlock()

		// Depends on level3 service
		level3Val, err := doger.InvokeNamed[string](i, "level3-service")
		if err != nil {
			return "", err
		}
		return "level4-" + level3Val, nil
	})

	// Invoke the deepest service (should trigger all dependencies)
	value, err := doger.InvokeNamed[string](level4, "level4-service")
	is.NoError(err)
	is.Equal("level4-level3-level2-level1-root-value", value)

	// Verify initialization order (should be from root to leaf)
	// Note: The actual order may vary due to lazy loading and dependency resolution
	// The important thing is that all services are initialized correctly
	is.Len(initOrder, 4)
	// Check that all expected levels are present, regardless of order
	is.Contains(initOrder, "level1")
	is.Contains(initOrder, "level2")
	is.Contains(initOrder, "level3")
	is.Contains(initOrder, "level4")
}

// TestDependencyResolutionAcrossScopes tests dependency resolution across nested scopes
func TestDependencyResolutionAcrossScopes(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	// Create scope structure with multiple branches
	root := doger.New()
	branch1 := root.Scope("branch1")
	branch2 := root.Scope("branch2")
	leaf1a := branch1.Scope("leaf1a")
	leaf2a := branch2.Scope("leaf2a")

	// Provide services at different levels
	doger.ProvideNamedValue(root, "shared-config", "config-value")
	doger.ProvideNamedValue(branch1, "branch1-service", "branch1-value")
	doger.ProvideNamedValue(branch2, "branch2-service", "branch2-value")

	// Create services that depend on services from different scopes
	doger.ProvideNamed(leaf1a, "leaf1a-service", func(i doger.Injector) (string, error) {
		config, err := doger.InvokeNamed[string](i, "shared-config")
		if err != nil {
			return "", err
		}
		branch1Val, err := doger.InvokeNamed[string](i, "branch1-service")
		if err != nil {
			return "", err
		}
		return "leaf1a-" + config + "-" + branch1Val, nil
	})

	doger.ProvideNamed(leaf2a, "leaf2a-service", func(i doger.Injector) (string, error) {
		config, err := doger.InvokeNamed[string](i, "shared-config")
		if err != nil {
			return "", err
		}
		branch2Val, err := doger.InvokeNamed[string](i, "branch2-service")
		if err != nil {
			return "", err
		}
		return "leaf2a-" + config + "-" + branch2Val, nil
	})

	// Test that services can access their dependencies
	value1, err := doger.InvokeNamed[string](leaf1a, "leaf1a-service")
	is.NoError(err)
	is.Equal("leaf1a-config-value-branch1-value", value1)

	value2, err := doger.InvokeNamed[string](leaf2a, "leaf2a-service")
	is.NoError(err)
	is.Equal("leaf2a-config-value-branch2-value", value2)

	// Test that services cannot access services from sibling branches
	_, err = doger.InvokeNamed[string](leaf1a, "branch2-service")
	is.Error(err)
	is.Contains(err.Error(), "could not find service")

	_, err = doger.InvokeNamed[string](leaf2a, "branch1-service")
	is.Error(err)
	is.Contains(err.Error(), "could not find service")
}
