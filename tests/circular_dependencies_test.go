package tests

import (
	"testing"
	"time"

	"github.com/kartaladev/doger"
	"github.com/stretchr/testify/assert"
)

func TestCrossScopeCircularDependencies(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)
	is := assert.New(t)

	// Create multi-level nested scope structure
	root := doger.New()
	level1 := root.Scope("level1")
	level2a := level1.Scope("level2a")
	level2b := level1.Scope("level2b")

	// Create circular dependency: ServiceA in level1 depends on ServiceB in level2a
	// ServiceB depends on ServiceC in level2b, ServiceC depends on ServiceA in level1
	doger.ProvideNamed(level1, "service-a", func(i doger.Injector) (string, error) {
		// This should cause circular dependency
		_, err := doger.InvokeNamed[string](i, "service-b")
		if err != nil {
			return "", err
		}
		return "service-a-value", nil
	})

	doger.ProvideNamed(level2a, "service-b", func(i doger.Injector) (string, error) {
		// This should cause circular dependency
		_, err := doger.InvokeNamed[string](i, "service-c")
		if err != nil {
			return "", err
		}
		return "service-b-value", nil
	})

	doger.ProvideNamed(level2b, "service-c", func(i doger.Injector) (string, error) {
		// This should cause circular dependency
		_, err := doger.InvokeNamed[string](i, "service-a")
		if err != nil {
			return "", err
		}
		return "service-c-value", nil
	})

	// Attempting to invoke should detect the dependency issue
	// Cross-scope circular dependencies currently result in "service not found"
	// because the service resolution fails before reaching circular dependency detection
	_, err := doger.InvokeNamed[string](level1, "service-a")
	is.Error(err)
	is.Contains(err.Error(), "could not find service", "Expected service not found error, got: %s", err.Error())
}

func TestCircularDependencyDetection(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)

	// Test various circular dependency scenarios
	testCases := []struct {
		name        string
		setupFunc   func(doger.Injector) error
		expectError bool
	}{
		{
			name: "Direct circular dependency",
			setupFunc: func(i doger.Injector) error {
				doger.ProvideNamed(i, "service-a", func(inj doger.Injector) (string, error) {
					_, err := doger.InvokeNamed[string](inj, "service-b")
					return "a", err
				})
				doger.ProvideNamed(i, "service-b", func(inj doger.Injector) (string, error) {
					_, err := doger.InvokeNamed[string](inj, "service-a")
					return "b", err
				})
				return nil
			},
			expectError: true,
		},
		{
			name: "Indirect circular dependency",
			setupFunc: func(i doger.Injector) error {
				doger.ProvideNamed(i, "service-a", func(inj doger.Injector) (string, error) {
					_, err := doger.InvokeNamed[string](inj, "service-b")
					return "a", err
				})
				doger.ProvideNamed(i, "service-b", func(inj doger.Injector) (string, error) {
					_, err := doger.InvokeNamed[string](inj, "service-c")
					return "b", err
				})
				doger.ProvideNamed(i, "service-c", func(inj doger.Injector) (string, error) {
					_, err := doger.InvokeNamed[string](inj, "service-a")
					return "c", err
				})
				return nil
			},
			expectError: true,
		},
		{
			name: "No circular dependency",
			setupFunc: func(i doger.Injector) error {
				doger.ProvideNamedValue(i, "service-a", "a")
				doger.ProvideNamed(i, "service-b", func(inj doger.Injector) (string, error) {
					_, err := doger.InvokeNamed[string](inj, "service-a")
					return "b", err
				})
				return nil
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			is := assert.New(t)

			root := doger.New()
			err := tc.setupFunc(root)
			is.NoError(err)

			if tc.expectError {
				_, err = doger.InvokeNamed[string](root, "service-a")
				is.Error(err)
				is.Contains(err.Error(), "circular dependency")
			} else {
				_, err = doger.InvokeNamed[string](root, "service-b")
				is.NoError(err)
			}
		})
	}
}

// circularService is a named string service whose provider invokes dependency
// and returns value.
type circularService struct {
	name       string
	dependency string
	value      string
}

// provideCircularServices registers services in order.
func provideCircularServices(i doger.Injector, services []circularService) {
	for _, svc := range services {
		doger.ProvideNamed(i, svc.name, func(inj doger.Injector) (string, error) {
			_, err := doger.InvokeNamed[string](inj, svc.dependency)
			return svc.value, err
		})
	}
}

// TestCircularDependency_Simple tests simple circular dependency detection
func TestCircularDependency_Simple(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name     string
		services []circularService
	}{
		{
			// Test case 1: A -> A (self-reference)
			name: "self_reference",
			services: []circularService{
				{name: "serviceA", dependency: "serviceA", value: "A"},
			},
		},
		{
			// Test case 2: A -> B -> A
			name: "two_service_cycle",
			services: []circularService{
				{name: "serviceA", dependency: "serviceB", value: "A"},
				{name: "serviceB", dependency: "serviceA", value: "B"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := doger.New()
			provideCircularServices(i, tt.services)

			_, err := doger.InvokeNamed[string](i, "serviceA")
			is.Error(err)
			is.Contains(err.Error(), "circular dependency")
		})
	}
}

// TestCircularDependency_Complex tests complex circular dependency scenarios
func TestCircularDependency_Complex(t *testing.T) {
	testWithTimeout(t, 300*time.Millisecond)

	tests := []struct {
		name     string
		services []circularService
	}{
		{
			// Test case 1: A -> B -> C -> A (3-service cycle)
			name: "three_service_cycle",
			services: []circularService{
				{name: "serviceA", dependency: "serviceB", value: "A"},
				{name: "serviceB", dependency: "serviceC", value: "B"},
				{name: "serviceC", dependency: "serviceA", value: "C"},
			},
		},
		{
			// Test case 2: A -> B -> C -> D -> A (4-service cycle)
			name: "four_service_cycle",
			services: []circularService{
				{name: "serviceA", dependency: "serviceB", value: "A"},
				{name: "serviceB", dependency: "serviceC", value: "B"},
				{name: "serviceC", dependency: "serviceD", value: "C"},
				{name: "serviceD", dependency: "serviceA", value: "D"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := doger.New()
			provideCircularServices(i, tt.services)

			_, err := doger.InvokeNamed[string](i, "serviceA")
			is.Error(err)
			is.Contains(err.Error(), "circular dependency")
		})
	}
}

// TestCircularDependency_GenericType tests circular dependencies with generic type resolution
func TestCircularDependency_GenericType(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	type ServiceA struct{ Value string }
	type ServiceB struct{ Value string }

	// Test case: ServiceA depends on ServiceB which depends on ServiceA
	i := doger.New()
	doger.Provide(i, func(inj doger.Injector) (*ServiceA, error) {
		_, err := doger.Invoke[*ServiceB](inj)
		return &ServiceA{Value: "A"}, err
	})
	doger.Provide(i, func(inj doger.Injector) (*ServiceB, error) {
		_, err := doger.Invoke[*ServiceA](inj)
		return &ServiceB{Value: "B"}, err
	})

	_, err := doger.Invoke[*ServiceA](i)
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestCircularDependency_MixedInvocation tests circular dependencies with mixed invocation methods
func TestCircularDependency_MixedInvocation(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test case: Mix of named and generic type invocations
	i := doger.New()
	doger.ProvideNamed(i, "serviceA", func(inj doger.Injector) (string, error) {
		_, err := doger.Invoke[string](inj)
		return "A", err
	})
	doger.Provide(i, func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "serviceA")
		return "B", err
	})

	_, err := doger.InvokeNamed[string](i, "serviceA")
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestCircularDependency_TransientServices tests circular dependencies with transient services
func TestCircularDependency_TransientServices(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test case: Transient services can also create circular dependencies
	i := doger.New()
	doger.ProvideTransient(i, func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "serviceB")
		return "A", err
	})
	doger.ProvideNamed(i, "serviceB", func(inj doger.Injector) (string, error) {
		_, err := doger.Invoke[string](inj)
		return "B", err
	})

	_, err := doger.Invoke[string](i)
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestCircularDependency_EagerServices tests circular dependencies with eager services
func TestCircularDependency_EagerServices(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test case: Eager services can create circular dependencies during initialization
	i := doger.New()
	doger.ProvideNamedValue(i, "serviceA", "A")
	doger.ProvideNamed(i, "serviceB", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "serviceA")
		return "B", err
	})
	doger.ProvideNamed(i, "serviceC", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "serviceB")
		return "C", err
	})

	// This should work since serviceA is eager and doesn't depend on others
	svc, err := doger.InvokeNamed[string](i, "serviceC")
	is.NoError(err)
	is.Equal("C", svc)
}

// TestCircularDependency_DeepChain tests very deep dependency chains that eventually become circular
func TestCircularDependency_DeepChain(t *testing.T) {
	testWithTimeout(t, 200*time.Millisecond)
	is := assert.New(t)

	// Test case: Deep chain A -> B -> C -> D -> E -> A
	i := doger.New()
	doger.ProvideNamed(i, "A", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "B")
		return "A", err
	})
	doger.ProvideNamed(i, "B", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "C")
		return "B", err
	})
	doger.ProvideNamed(i, "C", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "D")
		return "C", err
	})
	doger.ProvideNamed(i, "D", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "E")
		return "D", err
	})
	doger.ProvideNamed(i, "E", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "A")
		return "E", err
	})

	_, err := doger.InvokeNamed[string](i, "A")
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestCircularDependency_NoCircular tests that valid dependency chains work correctly
func TestCircularDependency_NoCircular(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test case: Valid chain A -> B -> C (no circular dependency)
	i := doger.New()
	doger.ProvideNamed(i, "A", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "B")
		return "A", err
	})
	doger.ProvideNamed(i, "B", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "C")
		return "B", err
	})
	doger.ProvideNamed(i, "C", func(inj doger.Injector) (string, error) {
		return "C", nil
	})

	svc, err := doger.InvokeNamed[string](i, "A")
	is.NoError(err)
	is.Equal("A", svc)
}

// TestCircularDependency_ServiceAlias tests circular dependencies with service aliases
func TestCircularDependency_ServiceAlias(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test case: Service alias can create circular dependencies
	i := doger.New()
	doger.ProvideNamed(i, "serviceA", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "aliasB")
		return "A", err
	})
	doger.ProvideNamed(i, "serviceB", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "serviceA")
		return "B", err
	})
	doger.MustAsNamed[string, string](i, "serviceB", "aliasB")

	_, err := doger.InvokeNamed[string](i, "serviceA")
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestCircularDependency_SelfInjection tests services that inject themselves
func TestCircularDependency_SelfInjection(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test case 1: Named service injecting itself
	i := doger.New()
	doger.ProvideNamed(i, "self-injecting-service", func(inj doger.Injector) (string, error) {
		// This service tries to inject itself
		_, err := doger.InvokeNamed[string](inj, "self-injecting-service")
		return "self-injected", err
	})

	_, err := doger.InvokeNamed[string](i, "self-injecting-service")
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")

	// Test case 2: Generic service injecting itself
	i = doger.New()
	doger.Provide(i, func(inj doger.Injector) (string, error) {
		// This service tries to inject itself using generic type
		_, err := doger.Invoke[string](inj)
		return "self-injected-generic", err
	})

	_, err = doger.Invoke[string](i)
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")

	// Test case 3: Struct service injecting itself
	type SelfInjectingStruct struct {
		Value string
		Self  *SelfInjectingStruct
	}

	i = doger.New()
	doger.Provide(i, func(inj doger.Injector) (*SelfInjectingStruct, error) {
		// This service tries to inject itself
		self, err := doger.Invoke[*SelfInjectingStruct](inj)
		if err != nil {
			return nil, err
		}
		return &SelfInjectingStruct{
			Value: "self-injected-struct",
			Self:  self,
		}, nil
	})

	_, err = doger.Invoke[*SelfInjectingStruct](i)
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")

	// Test case 4: Transient service injecting itself
	i = doger.New()
	doger.ProvideTransient(i, func(inj doger.Injector) (string, error) {
		// Transient services can also create self-injection circular dependencies
		_, err := doger.Invoke[string](inj)
		return "self-injected-transient", err
	})

	_, err = doger.Invoke[string](i)
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")

	// Test case 5: Service with multiple self-injections
	i = doger.New()
	doger.ProvideNamed(i, "multi-self-inject", func(inj doger.Injector) (string, error) {
		// Multiple attempts to inject itself
		_, err1 := doger.InvokeNamed[string](inj, "multi-self-inject")
		if err1 != nil {
			return "", err1
		}
		_, err2 := doger.InvokeNamed[string](inj, "multi-self-inject")
		if err2 != nil {
			return "", err2
		}
		return "multi-self-injected", nil
	})

	_, err = doger.InvokeNamed[string](i, "multi-self-inject")
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestCircularDependency_InterfaceResolution tests circular dependencies with interface resolution
func TestCircularDependency_InterfaceResolution(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// Test case: Interface resolution can create circular dependencies
	i := doger.New()
	doger.Provide(i, func(inj doger.Injector) (string, error) {
		_, err := doger.Invoke[string](inj)
		return "A", err
	})
	doger.Provide(i, func(inj doger.Injector) (int, error) {
		_, err := doger.Invoke[string](inj)
		return 42, err
	})

	_, err := doger.Invoke[string](i)
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")
}

// TestCircularDependency_EdgeCases tests edge cases in circular dependency detection
func TestCircularDependency_EdgeCases(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name    string
		service string
		value   string
	}{
		// Test case 1: Empty service name (should not cause issues)
		{name: "empty_service_name", service: "", value: "empty"},
		// Test case 2: Very long service names
		{name: "very_long_service_name", service: "very_long_service_name_that_is_quite_long_and_should_be_handled_properly", value: "long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			i := doger.New()
			doger.ProvideNamed(i, tt.service, func(inj doger.Injector) (string, error) {
				_, err := doger.InvokeNamed[string](inj, tt.service)
				return tt.value, err
			})

			_, err := doger.InvokeNamed[string](i, tt.service)
			is.Error(err)
			is.Contains(err.Error(), "circular dependency")
		})
	}
}

// TestCircularDependency_Recovery tests that circular dependency errors don't leave the container in a bad state
func TestCircularDependency_Recovery(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := doger.New()
	doger.ProvideNamed(i, "serviceA", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "serviceB")
		return "A", err
	})
	doger.ProvideNamed(i, "serviceB", func(inj doger.Injector) (string, error) {
		_, err := doger.InvokeNamed[string](inj, "serviceA")
		return "B", err
	})

	// First invocation should fail with circular dependency
	_, err := doger.InvokeNamed[string](i, "serviceA")
	is.Error(err)
	is.Contains(err.Error(), "circular dependency")

	// Container should still be usable for other services
	doger.ProvideNamed(i, "serviceC", func(inj doger.Injector) (string, error) {
		return "C", nil
	})

	svc, err := doger.InvokeNamed[string](i, "serviceC")
	is.NoError(err)
	is.Equal("C", svc)
}
