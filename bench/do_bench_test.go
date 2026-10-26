package bench

import (
	"fmt"
	"testing"

	"github.com/kartaladev/doger"
)

//
// Benchmark fixtures
//

type benchSvcA struct{}

type benchSvcB struct{}

type benchSvcC struct{}

type benchSvcTop struct{}

type benchIface interface {
	benchMethod()
}

type benchSvcImpl struct{}

func (s *benchSvcImpl) benchMethod() {}

type benchHealthchecker struct{}

func (s *benchHealthchecker) HealthCheck() error { return nil }

type benchShutdowner struct{}

func (s *benchShutdowner) Shutdown() {}

//
// Invocation
//

func BenchmarkInvokeLazySteadyState(b *testing.B) {
	injector := doger.New()
	doger.Provide(injector, func(i doger.Injector) (*benchSvcA, error) {
		return &benchSvcA{}, nil
	})
	_ = doger.MustInvoke[*benchSvcA](injector)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = doger.Invoke[*benchSvcA](injector)
	}
}

func BenchmarkInvokeLazyParallel(b *testing.B) {
	injector := doger.New()
	doger.Provide(injector, func(i doger.Injector) (*benchSvcA, error) {
		return &benchSvcA{}, nil
	})
	_ = doger.MustInvoke[*benchSvcA](injector)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = doger.Invoke[*benchSvcA](injector)
		}
	})
}

func BenchmarkInvokeNamed(b *testing.B) {
	injector := doger.New()
	doger.ProvideNamed(injector, "bench-svc", func(i doger.Injector) (*benchSvcA, error) {
		return &benchSvcA{}, nil
	})
	_ = doger.MustInvokeNamed[*benchSvcA](injector, "bench-svc")

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = doger.InvokeNamed[*benchSvcA](injector, "bench-svc")
	}
}

func BenchmarkInvokeTransient(b *testing.B) {
	injector := doger.New()
	doger.ProvideTransient(injector, func(i doger.Injector) (*benchSvcA, error) {
		return &benchSvcA{}, nil
	})

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = doger.Invoke[*benchSvcA](injector)
	}
}

func BenchmarkInvokeAlias(b *testing.B) {
	injector := doger.New()
	doger.Provide(injector, func(i doger.Injector) (*benchSvcImpl, error) {
		return &benchSvcImpl{}, nil
	})
	if err := doger.As[*benchSvcImpl, benchIface](injector); err != nil {
		b.Fatal(err)
	}
	_ = doger.MustInvoke[benchIface](injector)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = doger.Invoke[benchIface](injector)
	}
}

func BenchmarkInvokeAs(b *testing.B) {
	injector := doger.New()
	for j := range 19 {
		doger.ProvideNamedValue(injector, fmt.Sprintf("bench-filler-%d", j), &benchSvcA{})
	}
	doger.Provide(injector, func(i doger.Injector) (*benchSvcImpl, error) {
		return &benchSvcImpl{}, nil
	})
	_ = doger.MustInvokeAs[benchIface](injector)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = doger.InvokeAs[benchIface](injector)
	}
}

// newCollectionInjector registers n visible services, half of them
// matching benchIface, and builds them once.
func newCollectionInjector(n int) *doger.RootScope {
	injector := doger.New()
	for j := range n {
		if j%2 == 0 {
			doger.ProvideNamedValue(injector, fmt.Sprintf("bench-member-%d", j), &benchSvcImpl{})
		} else {
			doger.ProvideNamedValue(injector, fmt.Sprintf("bench-filler-%d", j), &benchSvcA{})
		}
	}
	_ = doger.MustInvokeAll[benchIface](injector)

	return injector
}

func BenchmarkInvokeAll(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			injector := newCollectionInjector(n)

			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				_, _ = doger.InvokeAll[benchIface](injector)
			}
		})
	}
}

func BenchmarkInvokeAllParallel(b *testing.B) {
	injector := newCollectionInjector(100)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = doger.InvokeAll[benchIface](injector)
		}
	})
}

func BenchmarkInvokeNestedScopes(b *testing.B) {
	injector := doger.New()
	doger.Provide(injector, func(i doger.Injector) (*benchSvcA, error) {
		return &benchSvcA{}, nil
	})

	scope := injector.Scope("level-1")
	for j := 2; j <= 5; j++ {
		scope = scope.Scope(fmt.Sprintf("level-%d", j))
	}
	_ = doger.MustInvoke[*benchSvcA](scope)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = doger.Invoke[*benchSvcA](scope)
	}
}

func BenchmarkProviderChainTransient(b *testing.B) {
	injector := doger.New()
	doger.Provide(injector, func(i doger.Injector) (*benchSvcA, error) {
		return &benchSvcA{}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*benchSvcB, error) {
		return &benchSvcB{}, nil
	})
	doger.Provide(injector, func(i doger.Injector) (*benchSvcC, error) {
		return &benchSvcC{}, nil
	})
	doger.ProvideTransient(injector, func(i doger.Injector) (*benchSvcTop, error) {
		_ = doger.MustInvoke[*benchSvcA](i)
		_ = doger.MustInvoke[*benchSvcB](i)
		_ = doger.MustInvoke[*benchSvcC](i)
		return &benchSvcTop{}, nil
	})
	_ = doger.MustInvoke[*benchSvcTop](injector)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, _ = doger.Invoke[*benchSvcTop](injector)
	}
}

//
// Registration
//

func BenchmarkProvide(b *testing.B) {
	names := make([]string, 100)
	for j := range 100 {
		names[j] = fmt.Sprintf("bench-svc-%d", j)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		injector := doger.New()
		for j := range 100 {
			doger.ProvideNamedValue(injector, names[j], &benchSvcA{})
		}
	}
}

func BenchmarkNew(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_ = doger.New()
	}
}

//
// Lifecycle
//

func BenchmarkHealthCheck(b *testing.B) {
	injector := doger.New()
	for j := range 50 {
		name := fmt.Sprintf("bench-hc-%d", j)
		doger.ProvideNamed(injector, name, func(i doger.Injector) (*benchHealthchecker, error) {
			return &benchHealthchecker{}, nil
		})
		_ = doger.MustInvokeNamed[*benchHealthchecker](injector, name)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_ = injector.HealthCheck()
	}
}

func BenchmarkShutdown(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	b.StopTimer()
	for n := 0; n < b.N; n++ {
		// Build a 20-service dependency chain, fully instantiated.
		injector := doger.New()
		doger.ProvideNamed(injector, "bench-chain-0", func(i doger.Injector) (*benchShutdowner, error) {
			return &benchShutdowner{}, nil
		})
		for j := 1; j < 20; j++ {
			name := fmt.Sprintf("bench-chain-%d", j)
			dependency := fmt.Sprintf("bench-chain-%d", j-1)
			doger.ProvideNamed(injector, name, func(i doger.Injector) (*benchShutdowner, error) {
				_ = doger.MustInvokeNamed[*benchShutdowner](i, dependency)
				return &benchShutdowner{}, nil
			})
		}
		_ = doger.MustInvokeNamed[*benchShutdowner](injector, "bench-chain-19")

		b.StartTimer()
		_ = injector.Shutdown()
		b.StopTimer()
	}
}

//
// Introspection
//

func BenchmarkListProvidedServices(b *testing.B) {
	injector := doger.New()
	var scope doger.Injector = injector
	for level := range 3 {
		for j := range 10 {
			doger.ProvideNamedValue(scope, fmt.Sprintf("bench-svc-%d-%d", level, j), &benchSvcA{})
		}
		scope = scope.Scope(fmt.Sprintf("level-%d", level+1))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_ = scope.ListProvidedServices()
	}
}

func BenchmarkAncestors(b *testing.B) {
	injector := doger.New()
	scope := injector.Scope("level-1")
	for j := 2; j <= 10; j++ {
		scope = scope.Scope(fmt.Sprintf("level-%d", j))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_ = scope.Ancestors()
	}
}
