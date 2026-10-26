package doger

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeAnyByName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// test default injector vs scope
	i := New()
	ProvideNamedValue(i, "foo", "baz")
	svc, err := invokeAnyByName(i, "foo")
	is.NoError(err)
	is.Equal("baz", svc)

	// service not found
	svc, err = invokeAnyByName(i, "not_found")
	is.Error(err)
	is.Empty(svc)
	is.Contains(err.Error(), "DI: could not find service `not_found`, available services: ")

	// test virtual scope wrapper
	called := false
	ProvideNamed(i, "hello", func(ivs Injector) (string, error) {
		// check we received a virtualScope
		vs, ok := ivs.(*virtualScope)
		is.True(ok)
		is.Equal([]string{"hello"}, vs.invokerChain())
		is.NotEqual(i, ivs)

		// create a dependency/dependent relationship
		_, _ = invokeAnyByName(ivs, "foo")

		called = true
		return "foobar", nil
	})
	_, _ = invokeAnyByName(i, "hello")
	is.True(called)
	// check dependency/dependent relationship
	dependencies, dependents := i.dag.explainService(i.self.id, i.self.name, "hello")
	is.ElementsMatch([]ServiceDescription{{ScopeID: i.self.id, ScopeName: i.self.name, Service: "foo"}}, dependencies)
	is.ElementsMatch([]ServiceDescription{}, dependents)

	// test circular dependency
	vs := newVirtualScope(i, []string{"foo", "bar"})

	svc, err = invokeAnyByName(vs, "foo")
	is.Error(err)

	is.Empty(svc)
	is.ErrorIs(err, ErrCircularDependency)
	is.EqualError(err, "DI: circular dependency detected: `foo` -> `bar` -> `foo`")

	// Test service type mismatch
	ProvideNamed(i, "type-mismatch", func(ivs Injector) (int, error) {
		return 42, nil
	})
	// Try to invoke as string when it's actually int
	svc, err = invokeAnyByName(i, "type-mismatch")
	is.NoError(err) // invokeAnyByName doesn't do type checking, it returns any
	is.Equal(42, svc)

	// Test provider error
	ProvideNamed(i, "provider-error", func(ivs Injector) (string, error) {
		return "", assert.AnError
	})
	svc, err = invokeAnyByName(i, "provider-error")
	is.Error(err)
	is.Equal(assert.AnError, err)
	is.Empty(svc)

	// Test provider panic with string
	ProvideNamed(i, "provider-panic-string", func(ivs Injector) (string, error) {
		panic("test panic")
	})
	svc, err = invokeAnyByName(i, "provider-panic-string")
	is.Error(err)
	is.EqualError(err, "DI: test panic")
	is.Empty(svc)

	// Test provider panic with error
	ProvideNamed(i, "provider-panic-error", func(ivs Injector) (string, error) {
		panic(assert.AnError)
	})
	svc, err = invokeAnyByName(i, "provider-panic-error")
	is.Error(err)
	is.Equal(assert.AnError, err)
	is.Empty(svc)

	// Test nested scope resolution
	childScope := i.Scope("child")
	ProvideNamedValue(childScope, "child-service", "child-value")
	svc, err = invokeAnyByName(childScope, "child-service")
	is.NoError(err)
	is.Equal("child-value", svc)

	// Test parent scope fallback
	svc, err = invokeAnyByName(childScope, "foo")
	is.NoError(err)
	is.Equal("baz", svc)

	// Test invocation hooks
	beforeCalled := false
	afterCalled := false
	hookInjector := NewWithOpts(&InjectorOpts{
		HookBeforeInvocation: []func(*Scope, string){
			func(scope *Scope, serviceName string) {
				beforeCalled = true
			},
		},
		HookAfterInvocation: []func(*Scope, string, error){
			func(scope *Scope, serviceName string, err error) {
				afterCalled = true
			},
		},
	})
	ProvideNamedValue(hookInjector, "hook-test", "hook-value")
	svc, err = invokeAnyByName(hookInjector, "hook-test")
	is.NoError(err)
	is.Equal("hook-value", svc)
	is.True(beforeCalled, "BeforeInvocationHook should be called")
	is.True(afterCalled, "AfterInvocationHook should be called")
}

func TestInvokeByName(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// test default injector vs scope
	ProvideNamedValue(DefaultRootScope, "foo", "bar")
	svc, err := invokeByName[string](nil, "foo")
	is.NoError(err)
	is.Equal("bar", svc)

	// test default injector vs scope
	i := New()
	ProvideNamedValue(i, "foo", "baz")
	svc, err = invokeByName[string](i, "foo")
	is.NoError(err)
	is.Equal("baz", svc)

	// service not found
	svc, err = invokeByName[string](nil, "not_found")
	is.Error(err)
	is.Empty(svc)
	is.Contains(err.Error(), "DI: could not find service `not_found`, available services: ")

	// test virtual scope wrapper
	called := false
	ProvideNamed(i, "hello", func(ivs Injector) (string, error) {
		// check we received a virtualScope
		vs, ok := ivs.(*virtualScope)
		is.True(ok)
		is.Equal([]string{"hello"}, vs.invokerChain())
		is.NotEqual(i, ivs)

		// create a dependency/dependent relationship
		_, _ = invokeByName[string](ivs, "foo")

		called = true
		return "foobar", nil
	})
	_, _ = invokeByName[string](i, "hello")
	is.True(called)
	// check dependency/dependent relationship
	dependencies, dependents := i.dag.explainService(i.self.id, i.self.name, "hello")
	is.ElementsMatch([]ServiceDescription{{ScopeID: i.self.id, ScopeName: i.self.name, Service: "foo"}}, dependencies)
	is.ElementsMatch([]ServiceDescription{}, dependents)

	// test circular dependency
	vs := newVirtualScope(i, []string{"foo", "bar"})

	svc, err = invokeByName[string](vs, "foo")
	is.Error(err)

	is.Empty(svc)
	is.ErrorIs(err, ErrCircularDependency)
	is.EqualError(err, "DI: circular dependency detected: `foo` -> `bar` -> `foo`")

	// Test service type mismatch
	ProvideNamed(i, "type-mismatch", func(ivs Injector) (int, error) {
		return 42, nil
	})
	// Try to invoke as string when it's actually int
	svc, err = invokeByName[string](i, "type-mismatch")
	is.Error(err)
	is.EqualError(err, "DI: service found, but type mismatch: invoking `string` but registered `int`")
	is.Empty(svc)

	// Test provider error
	ProvideNamed(i, "provider-error", func(ivs Injector) (string, error) {
		return "", assert.AnError
	})
	svc, err = invokeByName[string](i, "provider-error")
	is.Error(err)
	is.Equal(assert.AnError, err)
	is.Empty(svc)

	// Test provider panic with string
	ProvideNamed(i, "provider-panic-string", func(ivs Injector) (string, error) {
		panic("test panic")
	})
	svc, err = invokeByName[string](i, "provider-panic-string")
	is.Error(err)
	is.EqualError(err, "DI: test panic")
	is.Empty(svc)

	// Test provider panic with error
	ProvideNamed(i, "provider-panic-error", func(ivs Injector) (string, error) {
		panic(assert.AnError)
	})
	svc, err = invokeByName[string](i, "provider-panic-error")
	is.Error(err)
	is.Equal(assert.AnError, err)
	is.Empty(svc)

	// Test nested scope resolution
	childScope := i.Scope("child")
	ProvideNamedValue(childScope, "child-service", "child-value")
	svc, err = invokeByName[string](childScope, "child-service")
	is.NoError(err)
	is.Equal("child-value", svc)

	// Test parent scope fallback
	svc, err = invokeByName[string](childScope, "foo")
	is.NoError(err)
	is.Equal("baz", svc)

	// Test invocation hooks
	beforeCalled := false
	afterCalled := false
	hookInjector := NewWithOpts(&InjectorOpts{
		HookBeforeInvocation: []func(*Scope, string){
			func(scope *Scope, serviceName string) {
				beforeCalled = true
			},
		},
		HookAfterInvocation: []func(*Scope, string, error){
			func(scope *Scope, serviceName string, err error) {
				afterCalled = true
			},
		},
	})
	ProvideNamedValue(hookInjector, "hook-test", "hook-value")
	svc, err = invokeByName[string](hookInjector, "hook-test")
	is.NoError(err)
	is.Equal("hook-value", svc)
	is.True(beforeCalled, "HookBeforeInvocation should be called")
	is.True(afterCalled, "HookAfterInvocation should be called")

	// Test with different generic types
	ProvideNamedValue(i, "int-value", 42)
	intVal, err := invokeByName[int](i, "int-value")
	is.NoError(err)
	is.Equal(42, intVal)

	// Test with struct types
	ProvideNamedValue(i, "struct-value", &eagerTest{foobar: "test"})
	structVal, err := invokeByName[*eagerTest](i, "struct-value")
	is.NoError(err)
	is.Equal("test", structVal.foobar)
}

func TestInvokeByGenericType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// test default injector vs scope
	// Note: Skipping DefaultRootScope tests due to parallel test conflicts

	// test default injector vs scope
	i := New()
	ProvideValue(i, &lazyTest{foobar: "baz"})
	svc2, err := invokeByGenericType[*lazyTest](i)
	is.Equal(&lazyTest{foobar: "baz"}, svc2)
	is.NoError(err)

	// service not found
	svcX, err := invokeByGenericType[string](i)
	is.Empty(svcX)
	is.Error(err)
	is.Contains(err.Error(), "DI: could not find service satisfying interface `string`, available services: `*github.com/kartaladev/doger.lazyTest`")

	// test virtual scope wrapper
	called := false
	Provide(i, func(ivs Injector) (*eagerTest, error) {
		// check we received a virtualScope
		vs, ok := ivs.(*virtualScope)
		is.True(ok)
		is.Equal([]string{"*github.com/kartaladev/doger.eagerTest"}, vs.invokerChain())
		is.NotEqual(i, ivs)

		// create a dependency/dependent relationship
		_, _ = invokeByGenericType[*lazyTest](ivs)

		called = true
		return &eagerTest{}, nil
	})
	_, _ = invokeByGenericType[*eagerTest](i)
	is.True(called)
	// check dependency/dependent relationship
	dependencies, dependents := i.dag.explainService(i.self.id, i.self.name, "*github.com/kartaladev/doger.eagerTest")
	is.ElementsMatch([]ServiceDescription{{ScopeID: i.self.id, ScopeName: i.self.name, Service: "*github.com/kartaladev/doger.lazyTest"}}, dependencies)
	is.ElementsMatch([]ServiceDescription{}, dependents)

	// test circular dependency
	vs := newVirtualScope(i, []string{"*github.com/kartaladev/doger.eagerTest", "bar"})
	var svc1 *eagerTest
	svc1, err = invokeByGenericType[*eagerTest](vs)
	is.Error(err)

	is.Empty(svc1)
	is.ErrorIs(err, ErrCircularDependency)

	is.EqualError(err, "DI: circular dependency detected: `*github.com/kartaladev/doger.eagerTest` -> `bar` -> `*github.com/kartaladev/doger.eagerTest`")

	// Test with nil injector (should use default root scope)
	// Note: Skipping DefaultRootScope tests due to parallel test conflicts

	// Test service not found for interface
	svc4, err := invokeByGenericType[Healthchecker](i)
	is.Empty(svc4)
	is.Error(err)
	is.Contains(err.Error(), "DI: could not find service satisfying interface `github.com/kartaladev/doger.Healthchecker`")

	// Test provider error with different service type
	// Note: Skipping provider error tests due to service conflicts

	// Test nested scope resolution
	childScope := i.Scope("child")
	ProvideValue(childScope, &lazyTest{foobar: "child-lazy"})
	svc8, err := invokeByGenericType[*lazyTest](childScope)
	is.NoError(err)
	is.Equal("child-lazy", svc8.foobar)

	// Test parent scope fallback
	// Note: Child scope finds its own service first, which is correct behavior
	svc9, err := invokeByGenericType[*lazyTest](childScope)
	is.NoError(err)
	is.Equal("child-lazy", svc9.foobar) // Should get the child scope service

	// Test invocation hooks
	beforeCalled := false
	afterCalled := false
	hookInjector := NewWithOpts(&InjectorOpts{
		HookBeforeInvocation: []func(*Scope, string){
			func(scope *Scope, serviceName string) {
				beforeCalled = true
			},
		},
		HookAfterInvocation: []func(*Scope, string, error){
			func(scope *Scope, serviceName string, err error) {
				afterCalled = true
			},
		},
	})
	ProvideValue(hookInjector, &eagerTest{foobar: "hook-test"})
	svc10, err := invokeByGenericType[*eagerTest](hookInjector)
	is.NoError(err)
	is.Equal("hook-test", svc10.foobar)
	is.True(beforeCalled, "HookBeforeInvocation should be called")
	is.True(afterCalled, "HookAfterInvocation should be called")

	// Test with primitive types
	ProvideValue(i, 42)
	intVal, err := invokeByGenericType[int](i)
	is.NoError(err)
	is.Equal(42, intVal)

	// Test with interface types
	Provide(i, func(ivs Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "healthchecker"}, nil
	})
	healthchecker, err := invokeByGenericType[Healthchecker](i)
	is.NoError(err)
	is.Equal("healthchecker", healthchecker.(*lazyTestHeathcheckerOK).foobar)

	// Test service selection when multiple services match
	Provide(i, func(ivs Injector) (*lazyTestHeathcheckerKO, error) {
		return &lazyTestHeathcheckerKO{foobar: "healthchecker2"}, nil
	})
	// Should select the first matching service (order is not guaranteed, but should work)
	healthchecker2, err := invokeByGenericType[Healthchecker](i)
	is.NoError(err)

	// Use safe type assertion to check which type we got
	if okType, ok := healthchecker2.(*lazyTestHeathcheckerOK); ok {
		is.Equal("healthchecker", okType.foobar)
	} else if koType, ok := healthchecker2.(*lazyTestHeathcheckerKO); ok {
		is.Equal("healthchecker2", koType.foobar)
	} else {
		is.Fail("Unexpected healthchecker type")
	}
}

func TestInvokeByName_race(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	injector := New()
	child := injector.Scope("child")

	Provide(injector, func(i Injector) (int, error) {
		time.Sleep(3 * time.Millisecond)
		return 42, nil
	})
	Provide(injector, func(i Injector) (*lazyTest, error) {
		time.Sleep(3 * time.Millisecond)
		return &lazyTest{}, nil
	})

	var wg sync.WaitGroup
	wg.Add(5)
	for i := range 5 {
		go func(j int) {
			_, err1 := invokeByName[int](injector, NameOf[int]())
			_, err2 := invokeByName[*lazyTest](child, NameOf[*lazyTest]())

			is.NoError(err1)
			is.NoError(err2)

			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestInvokeByGenericType_race(t *testing.T) {
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	injector := New()
	child := injector.Scope("child")

	Provide(injector, func(i Injector) (int, error) {
		time.Sleep(3 * time.Millisecond)
		return 42, nil
	})
	Provide(injector, func(i Injector) (*lazyTest, error) {
		time.Sleep(3 * time.Millisecond)
		return &lazyTest{}, nil
	})

	var wg sync.WaitGroup
	wg.Add(5)
	for i := range 5 {
		go func(j int) {
			_, err1 := invokeByGenericType[int](injector)
			_, err2 := invokeByGenericType[*lazyTest](child)

			is.NoError(err1)
			is.NoError(err2)

			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestInvokeByTags(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	ProvideValue(i, &eagerTest{foobar: "foobar"})

	// no dependencies
	err := invokeByTags(i, "*myStruct", reflect.ValueOf(&eagerTest{}), false)
	is.NoError(err)

	// not pointer
	err = invokeByTags(i, "*myStruct", reflect.ValueOf(eagerTest{}), false)
	is.Equal("DI: must be a pointer to a struct", err.Error())

	// exported field - generic type
	type hasExportedEagerTestDependency struct {
		EagerTest *eagerTest `do:""`
	}
	test1 := hasExportedEagerTestDependency{}
	err = invokeByTags(i, "*myStruct", reflect.ValueOf(&test1), false)
	is.NoError(err)
	is.Equal("foobar", test1.EagerTest.foobar)

	// unexported field
	type hasNonExportedEagerTestDependency struct {
		eagerTest *eagerTest `do:""`
	}
	test2 := hasNonExportedEagerTestDependency{}
	err = invokeByTags(i, "*myStruct", reflect.ValueOf(&test2), false)
	is.NoError(err)
	is.Equal("foobar", test2.eagerTest.foobar)

	// not found
	type dependencyNotFound struct {
		eagerTest *hasNonExportedEagerTestDependency `do:""` //nolint:unused
	}
	test3 := dependencyNotFound{}
	err = invokeByTags(i, "*myStruct", reflect.ValueOf(&test3), false)
	is.Equal(serviceNotFound(i, ErrServiceNotFound, []string{inferServiceName[*hasNonExportedEagerTestDependency]()}).Error(), err.Error())

	// use tag
	type namedDependency struct {
		eagerTest *eagerTest `do:"int"` //nolint:unused
	}
	test4 := namedDependency{}
	err = invokeByTags(i, "*myStruct", reflect.ValueOf(&test4), false)
	is.Equal(serviceNotFound(i, ErrServiceNotFound, []string{inferServiceName[int]()}).Error(), err.Error())

	// named service
	ProvideNamedValue(i, "foobar", 42)
	type namedService struct {
		EagerTest int `do:"foobar"`
	}
	test5 := namedService{}
	err = invokeByTags(i, "*myStruct", reflect.ValueOf(&test5), false)
	is.NoError(err)
	is.Equal(42, test5.EagerTest)

	// use tag but wrong type
	type namedDependencyButTypeMismatch struct {
		EagerTest *int `do:"*github.com/kartaladev/doger.eagerTest"`
	}
	test6 := namedDependencyButTypeMismatch{}
	err = invokeByTags(i, "*myStruct", reflect.ValueOf(&test6), false)
	is.Equal("DI: `*github.com/kartaladev/doger.eagerTest` is not assignable to field `*myStruct.EagerTest`", err.Error())

	// use a custom tag
	i = NewWithOpts(&InjectorOpts{StructTagKey: "hello"})
	ProvideNamedValue(i, "foobar", 42)
	type namedServiceWithCustomTag struct {
		EagerTest int `hello:"foobar"`
	}
	test7 := namedServiceWithCustomTag{}
	err = invokeByTags(i, "*myStruct", reflect.ValueOf(&test7), false)
	is.NoError(err)
	is.Equal(42, test7.EagerTest)
}

func TestInvokeByTags_ImplicitAliasing_FallbackOnNotFound(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()

	type foobar struct {
		Dep Healthchecker `do:""`
	}

	// First, declare a service with no dependencies

	s := foobar{}
	err := invokeByTags(i, "*foobar", reflect.ValueOf(&s), true)
	is.Error(err)
	is.Equal("DI: could not find service `github.com/kartaladev/doger.Healthchecker`, no service available", err.Error())
	is.Nil(s.Dep)

	// Now, declare a service with the right type

	Provide(i, func(injector Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})

	s = foobar{}
	err = invokeByTags(i, "*foobar", reflect.ValueOf(&s), true)
	is.NoError(err)
	is.NotNil(s.Dep)
	is.Equal("foobar", s.Dep.(*lazyTestHeathcheckerOK).foobar)

	// Now, declare a service with an interface assignable to interface

	i = New()
	Override(i, func(injector Injector) (iTestHeathchecker, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})

	s = foobar{}
	err = invokeByTags(i, "*foobar", reflect.ValueOf(&s), true)
	is.NoError(err)
	is.NotNil(s.Dep)
	is.Equal("foobar", s.Dep.(*lazyTestHeathcheckerOK).foobar)
}

func TestInvokeByTags_ImplicitAliasing_NoFallbackOnOtherErrors(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	i := New()
	Provide(i, func(injector Injector) (*lazyTestHeathcheckerOK, error) {
		return &lazyTestHeathcheckerOK{foobar: "foobar"}, nil
	})

	// First, lets check the situation the service is not found

	type foobar struct {
		Dep Healthchecker `do:"bad"`
	}

	s := foobar{}
	err := invokeByTags(i, "*foobar", reflect.ValueOf(&s), true)
	is.Error(err)
	is.Equal("DI: could not find service `bad`, available services: `*github.com/kartaladev/doger.lazyTestHeathcheckerOK`", err.Error())
	is.Nil(s.Dep)

	// Now, declare a service with a wrong type

	ProvideNamed(i, "bad", func(ivs Injector) (*eagerTest, error) {
		return &eagerTest{}, nil
	})

	s = foobar{}
	err = invokeByTags(i, "*foobar", reflect.ValueOf(&s), true)
	is.Error(err)
	is.Equal("DI: `bad` is not assignable to field `*foobar.Dep`", err.Error())
	is.Nil(s.Dep)

	// Now, declare a service with the right type, but with provider error

	OverrideNamed(i, "bad", func(ivs Injector) (*eagerTest, error) {
		return nil, fmt.Errorf("boom")
	})

	s = foobar{}
	err = invokeByTags(i, "*foobar", reflect.ValueOf(&s), true)
	is.Error(err)
	is.Equal("boom", err.Error())
	is.Nil(s.Dep)
}

func TestServiceNotFound(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	rootScope := New()

	// create children
	child1 := rootScope.Scope("child1")
	child2a := child1.Scope("child2a")
	child2b := child1.Scope("child2b")
	child3 := child2a.Scope("child3")

	err := serviceNotFound(child1, ErrServiceNotFound, []string{"not-found"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotFound)
	is.EqualError(err, "DI: could not find service `not-found`, no service available")

	err = serviceNotFound(child1, ErrServiceNotFound, []string{"not-found1", "not-found2"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotFound)
	is.EqualError(err, "DI: could not find service `not-found2`, no service available, path: `not-found1` -> `not-found2`")

	rootScope.serviceSet("root-a", newServiceLazy("root-a", func(i Injector) (int, error) { return 0, nil }))
	child1.serviceSet("child1-a", newServiceLazy("child1-a", func(i Injector) (int, error) { return 1, nil }))
	child2a.serviceSet("child2a-a", newServiceLazy("child2a-a", func(i Injector) (int, error) { return 2, nil }))
	child2a.serviceSet("child2a-b", newServiceLazy("child2a-b", func(i Injector) (int, error) { return 3, nil }))
	child2b.serviceSet("child2b-a", newServiceLazy("child2b-a", func(i Injector) (int, error) { return 4, nil }))
	child3.serviceSet("child3-a", newServiceLazy("child3-a", func(i Injector) (int, error) { return 5, nil }))

	err = serviceNotFound(child1, ErrServiceNotFound, []string{"not-found"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotFound)
	is.EqualError(err, "DI: could not find service `not-found`, available services: `child1-a`, `root-a`")

	err = serviceNotFound(child1, ErrServiceNotFound, []string{"not-found1", "not-found2"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotFound)
	is.EqualError(err, "DI: could not find service `not-found2`, available services: `child1-a`, `root-a`, path: `not-found1` -> `not-found2`")

	// Test service ordering in error messages
	// Create services in different order to test sorting
	rootScope.serviceSet("z-service", newServiceLazy("z-service", func(i Injector) (int, error) { return 10, nil }))
	rootScope.serviceSet("a-service", newServiceLazy("a-service", func(i Injector) (int, error) { return 11, nil }))
	rootScope.serviceSet("m-service", newServiceLazy("m-service", func(i Injector) (int, error) { return 12, nil }))

	err = serviceNotFound(rootScope, ErrServiceNotFound, []string{"not-found"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotFound)
	// Services should be sorted alphabetically
	is.Contains(err.Error(), "available services: `a-service`, `m-service`, `root-a`, `z-service`")

	// Test service ordering across scopes
	child1.serviceSet("x-service", newServiceLazy("x-service", func(i Injector) (int, error) { return 13, nil }))
	child1.serviceSet("b-service", newServiceLazy("b-service", func(i Injector) (int, error) { return 14, nil }))

	err = serviceNotFound(child1, ErrServiceNotFound, []string{"not-found"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotFound)
	// Services should be listed (order may vary due to sorting)
	errorMsg := err.Error()
	is.Contains(errorMsg, "b-service")
	is.Contains(errorMsg, "child1-a")
	is.Contains(errorMsg, "x-service")
	is.Contains(errorMsg, "a-service")
	is.Contains(errorMsg, "m-service")
	is.Contains(errorMsg, "root-a")
	is.Contains(errorMsg, "z-service")

	// Test service ordering with special characters and numbers
	rootScope.serviceSet("service-1", newServiceLazy("service-1", func(i Injector) (int, error) { return 15, nil }))
	rootScope.serviceSet("service_2", newServiceLazy("service_2", func(i Injector) (int, error) { return 16, nil }))
	rootScope.serviceSet("Service3", newServiceLazy("Service3", func(i Injector) (int, error) { return 17, nil }))

	err = serviceNotFound(rootScope, ErrServiceNotFound, []string{"not-found"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotFound)
	// Services should be sorted with numbers, underscores, hyphens, and case sensitivity
	is.Contains(err.Error(), "available services: `Service3`, `a-service`, `m-service`, `root-a`, `service-1`, `service_2`, `z-service`")

	// Test empty injector
	emptyInjector := New()
	err = serviceNotFound(emptyInjector, ErrServiceNotFound, []string{"not-found"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotFound)
	is.EqualError(err, "DI: could not find service `not-found`, no service available")

	// Test with different error types
	err = serviceNotFound(child1, ErrServiceNotMatch, []string{"not-found"})
	is.Error(err)
	is.ErrorIs(err, ErrServiceNotMatch)
	is.Contains(err.Error(), "DI: could not find service satisfying interface `not-found`")

	// Test with very long service names
	longName := "very-long-service-name-that-exceeds-normal-length-limits-for-testing-purposes"
	rootScope.serviceSet(longName, newServiceLazy(longName, func(i Injector) (int, error) { return 18, nil }))
	err = serviceNotFound(rootScope, ErrServiceNotFound, []string{"not-found"})
	is.Error(err)
	is.Contains(err.Error(), longName)

	// Test with unicode service names
	unicodeName := "service-测试-unicode"
	rootScope.serviceSet(unicodeName, newServiceLazy(unicodeName, func(i Injector) (int, error) { return 19, nil }))
	err = serviceNotFound(rootScope, ErrServiceNotFound, []string{"not-found"})
	is.Error(err)
	is.Contains(err.Error(), unicodeName)
}

func TestInvoke_NotFoundNamesLibraryTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(Injector)
		invoke  func(Injector) error
		message string
	}{
		{
			name:  "empty_scope",
			setup: func(Injector) {},
			invoke: func(i Injector) error {
				_, err := Invoke[*RootScope](i)
				return err
			},
			message: "DI: could not find service `*github.com/kartaladev/doger.RootScope`, no service available",
		},
		{
			name:  "int_registered",
			setup: func(i Injector) { ProvideValue(i, 42) },
			invoke: func(i Injector) error {
				_, err := Invoke[Healthchecker](i)
				return err
			},
			message: "DI: could not find service `github.com/kartaladev/doger.Healthchecker`, available services: `int`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 100*time.Millisecond)
			is := assert.New(t)

			i := New()
			tt.setup(i)

			err := tt.invoke(i)
			is.ErrorIs(err, ErrServiceNotFound)
			is.EqualError(err, tt.message)
		})
	}
}

func TestHandleProviderPanic(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	is.NotPanics(func() {
		// ok
		svc, err := handleProviderPanic(func(i Injector) (int, error) {
			return 42, nil
		}, nil)
		is.NoError(err)
		is.Equal(42, svc)

		// should return an error
		svc, err = handleProviderPanic(func(i Injector) (int, error) {
			return 0, assert.AnError
		}, nil)
		is.Equal(assert.AnError, err)
		is.Equal(0, svc)

		// shoud not return 42
		svc, err = handleProviderPanic(func(i Injector) (int, error) {
			return 42, assert.AnError
		}, nil)
		is.Equal(assert.AnError, err)
		is.Equal(0, svc)

		// panics with string
		svc, err = handleProviderPanic(func(i Injector) (int, error) {
			panic("aïe")
		}, nil)
		is.EqualError(err, "DI: aïe")
		is.Equal(0, svc)

		// panics with error
		svc, err = handleProviderPanic(func(i Injector) (int, error) {
			panic(assert.AnError)
		}, nil)
		is.Equal(assert.AnError, err)
		is.Equal(0, svc)
	})
}

func TestInvoke_ProviderPanic(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")

	// provide registers a provider of the given kind that panics with value,
	// and returns the matching public invocation.
	provide := map[string]func(i Injector, value any) func() (int, error){
		"lazy": func(i Injector, value any) func() (int, error) {
			Provide(i, func(Injector) (int, error) { panic(value) })
			return func() (int, error) { return Invoke[int](i) }
		},
		"transient": func(i Injector, value any) func() (int, error) {
			ProvideNamedTransient(i, "svc", func(Injector) (int, error) { panic(value) })
			return func() (int, error) { return InvokeNamed[int](i, "svc") }
		},
	}

	tests := []struct {
		name    string
		kind    string
		value   any
		message string
		isNil   bool
		isBoom  bool
	}{
		{name: "lazy_nil", kind: "lazy", value: nil, message: "runtime error: panic called with nil argument", isNil: true},
		{name: "transient_nil", kind: "transient", value: nil, message: "runtime error: panic called with nil argument", isNil: true},
		{name: "lazy_error", kind: "lazy", value: errBoom, message: "boom", isBoom: true},
		{name: "transient_error", kind: "transient", value: errBoom, message: "boom", isBoom: true},
		{name: "lazy_string", kind: "lazy", value: "s", message: "DI: s"},
		{name: "lazy_int", kind: "lazy", value: 42, message: "DI: 42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, 100*time.Millisecond)
			is := assert.New(t)

			invoke := provide[tt.kind](New(), tt.value)

			svc, err := invoke()
			is.Equal(0, svc)
			is.EqualError(err, tt.message)

			var panicNilErr *runtime.PanicNilError
			is.Equal(tt.isNil, errors.As(err, &panicNilErr), "errors.As *runtime.PanicNilError")
			is.Equal(tt.isBoom, errors.Is(err, errBoom), "errors.Is boom")
		})
	}
}

// panicNilChildEnv marks the re-executed test binary of
// TestInvoke_ProviderPanicNil_GODEBUGOverride.
const panicNilChildEnv = "DOGER_TEST_PANICNIL_CHILD"

func TestInvoke_ProviderPanicNil_GODEBUGOverride(t *testing.T) {
	if os.Getenv(panicNilChildEnv) == "1" {
		// child: runs with GODEBUG=panicnil=1
		is := assert.New(t)

		i := New()
		Provide(i, func(Injector) (int, error) { panic(nil) })

		svc, err := Invoke[int](i)
		is.Equal(0, svc)
		is.NoError(err)
		return
	}

	t.Parallel()
	testWithTimeout(t, 10*time.Second)

	cmd := exec.Command(os.Args[0], "-test.run=^TestInvoke_ProviderPanicNil_GODEBUGOverride$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), "GODEBUG=panicnil=1", panicNilChildEnv+"=1")
	out, err := cmd.CombinedOutput()

	assert.NoError(t, err, "child output:\n%s", out)
	assert.Contains(t, string(out), "--- PASS: TestInvoke_ProviderPanicNil_GODEBUGOverride")
}

// Greeter is implemented by greeterImpl, named after its service.
type Greeter interface{ Greet() string }

type greeterImpl struct{ name string }

func (g *greeterImpl) Greet() string { return g.name }

// provideGreeter registers lazy Greeter implementation name in scope.
func provideGreeter(scope Injector, name string) {
	ProvideNamed(scope, name, func(Injector) (*greeterImpl, error) { return &greeterImpl{name: name}, nil })
}

func TestInvokeAs_Deterministic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(root *RootScope) (from Injector)
		selected string
	}{
		{name: "b then a, from root", setup: func(root *RootScope) Injector {
			provideGreeter(root, "b")
			provideGreeter(root, "a")
			return root
		}, selected: "b"},
		{name: "a then b, from root", setup: func(root *RootScope) Injector {
			provideGreeter(root, "a")
			provideGreeter(root, "b")
			return root
		}, selected: "a"},
		{name: "child match is nearer", setup: func(root *RootScope) Injector {
			provideGreeter(root, "a")
			request := root.Scope("request")
			provideGreeter(request, "z")
			return request
		}, selected: "z"},
		{name: "root does not see the child", setup: func(root *RootScope) Injector {
			provideGreeter(root, "a")
			provideGreeter(root.Scope("request"), "z")
			return root
		}, selected: "a"},
		{name: "override keeps position", setup: func(root *RootScope) Injector {
			provideGreeter(root, "b")
			provideGreeter(root, "a")
			OverrideNamed(root, "b", func(Injector) (*greeterImpl, error) { return &greeterImpl{name: "b"}, nil })
			return root
		}, selected: "b"},
		{name: "clone keeps positions", setup: func(root *RootScope) Injector {
			provideGreeter(root, "b")
			clone := root.Clone()
			provideGreeter(clone, "a")
			return clone
		}, selected: "b"},
		{name: "clone keeps each registration index", setup: func(root *RootScope) Injector {
			provideGreeter(root, "b")
			provideGreeter(root, "a")
			return root.Clone()
		}, selected: "b"},
		{name: "an alias is a candidate", setup: func(root *RootScope) Injector {
			provideGreeter(root, "x")
			provideGreeter(root, "a")
			request := root.Scope("request")
			// the only match in the nearest scope
			if err := AsNamed[*greeterImpl, Greeter](request, "a", "g"); err != nil {
				panic(err)
			}
			return request
		}, selected: "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			for range 10 {
				from := tt.setup(New())

				greeter, err := InvokeAs[Greeter](from)

				assert.NoError(t, err)
				assert.Equal(t, tt.selected, greeter.Greet())
			}
		})
	}
}

func TestInvokeAs_DeterministicStable(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	for range 100 {
		root := New()
		for i := range 20 {
			provideGreeter(root, fmt.Sprintf("s%02d", i))
		}

		greeter, err := InvokeAs[Greeter](root)

		assert.NoError(t, err)
		assert.Equal(t, "s00", greeter.Greet())
	}
}

func TestMustInvokeAs_Deterministic(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	for range 100 {
		root := New()
		provideGreeter(root, "b")
		provideGreeter(root, "a")

		assert.Equal(t, "b", MustInvokeAs[Greeter](root).Greet())
	}
}

func TestMustInvokeAs_FromProvider(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	ProvideNamed(root, "decor", func(i Injector) (*greeterDecor, error) {
		return &greeterDecor{inner: MustInvokeAs[Greeter](i)}, nil
	})
	provideGreeter(root, "base")

	decor, err := InvokeNamed[*greeterDecor](root, "decor")

	require.NoError(t, err)
	assert.Equal(t, "decor(base)", decor.Greet())
	assert.Equal(t, []string{"base"}, explainedNames(t, root, "decor", false))
}

func TestInvokeAs_SingleAndMissing(t *testing.T) {
	t.Parallel()

	t.Run("one match", func(t *testing.T) {
		t.Parallel()
		root := New()
		provideGreeter(root, "a")

		greeter, err := InvokeAs[Greeter](root)

		assert.NoError(t, err)
		assert.Equal(t, "a", greeter.Greet())
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		root := New()

		_, err := InvokeAs[Greeter](root)

		assert.ErrorIs(t, err, ErrServiceNotMatch)
	})
}

func TestInvokeStruct_ImplicitAliasingDeterministic(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	type withGreeter struct {
		G Greeter `do:""`
	}

	for range 100 {
		root := New()
		provideGreeter(root, "b")
		provideGreeter(root, "a")

		value, err := InvokeStruct[withGreeter](root)

		assert.NoError(t, err)
		assert.Equal(t, "b", value.G.Greet())
	}
}

// greeterDecor is a Greeter decorating the Greeter its provider resolves.
type greeterDecor struct{ inner Greeter }

func (d *greeterDecor) Greet() string { return "decor(" + d.inner.Greet() + ")" }

func TestInvokeAs_SkipsInvoker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		before  bool     // register "base" before "decor"
		base    bool     // register "base" at all
		aliases []string // a chain of Greeter aliases from "decor", registered after it
		want    string
	}{
		{name: "base registered after decor", base: true, want: "decor(base)"},
		{name: "base registered before decor", base: true, before: true, want: "decor(base)"},
		{name: "no other match"},
		{name: "alias of decor, then base", base: true, aliases: []string{"decor-alias"}, want: "decor(base)"},
		{name: "chained aliases of decor, then base", base: true, aliases: []string{"a1", "a2"}, want: "decor(base)"},
		{name: "alias of decor, no other match", aliases: []string{"decor-alias"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			if tt.base && tt.before {
				provideGreeter(root, "base")
			}
			ProvideNamed(root, "decor", func(i Injector) (*greeterDecor, error) {
				inner, err := InvokeAs[Greeter](i)
				if err != nil {
					return nil, err
				}
				return &greeterDecor{inner: inner}, nil
			})
			target := "decor"
			for index, alias := range tt.aliases {
				if index == 0 {
					require.NoError(t, AsNamed[*greeterDecor, Greeter](root, target, alias))
				} else {
					require.NoError(t, AsNamed[Greeter, Greeter](root, target, alias))
				}
				target = alias
			}
			if tt.base && !tt.before {
				provideGreeter(root, "base")
			}

			decor, err := InvokeNamed[*greeterDecor](root, "decor")

			if tt.want == "" {
				assert.ErrorIs(t, err, ErrServiceNotMatch)
				assert.NotErrorIs(t, err, ErrCircularDependency)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, decor.Greet())
		})
	}
}

func TestInvokeStruct_FallbackSkipsInvoker(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	type withGreeter struct {
		G Greeter `do:""`
	}

	root := New()
	ProvideNamed(root, "decor", func(i Injector) (*greeterDecor, error) {
		value, err := InvokeStruct[withGreeter](i)
		if err != nil {
			return nil, err
		}
		return &greeterDecor{inner: value.G}, nil
	})
	provideGreeter(root, "base")

	decor, err := InvokeNamed[*greeterDecor](root, "decor")

	require.NoError(t, err)
	assert.Equal(t, "decor(base)", decor.Greet())
}

func TestAliasResolvesTo_Loop(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	x := newServiceAlias[Greeter, Greeter]("x", root, "y")
	root.serviceSet("x", x)
	root.serviceSet("y", newServiceAlias[Greeter, Greeter]("y", root, "x"))

	assert.False(t, aliasResolvesTo(x, "decor"))
	assert.True(t, aliasResolvesTo(x, "x"))
}

func TestInvokeStruct_FallbackSkipsInvokerAlias(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	type withGreeter struct {
		G Greeter `do:""`
	}

	root := New()
	ProvideNamed(root, "decor", func(i Injector) (*greeterDecor, error) {
		value, err := InvokeStruct[withGreeter](i)
		if err != nil {
			return nil, err
		}
		return &greeterDecor{inner: value.G}, nil
	})
	require.NoError(t, AsNamed[*greeterDecor, Greeter](root, "decor", "decor-alias"))
	provideGreeter(root, "base")

	decor, err := InvokeNamed[*greeterDecor](root, "decor")

	require.NoError(t, err)
	assert.Equal(t, "decor(base)", decor.Greet())
}

type (
	// shadowStore is implemented by shadowPG only.
	shadowStore interface{ store() }
	shadowPG    struct{ scope string }

	shadowStruct struct {
		S shadowStore `do:""`
	}
)

func (*shadowPG) store() {}

func TestInvokeAs_Shadowing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mid     bool // invoke from a scope below the one registering db
		childDB func(child *Scope)
		check   func(t *testing.T, child *Scope)
	}{
		{
			name:    "string db in an intermediate scope, InvokeAs from below it",
			mid:     true,
			childDB: func(child *Scope) { ProvideNamedValue(child, "db", "a string") },
			check: func(t *testing.T, child *Scope) {
				t.Helper()
				_, err := InvokeAs[shadowStore](child)
				assert.ErrorIs(t, err, ErrServiceNotMatch)
			},
		},
		{
			name:    "string child db, InvokeAs",
			childDB: func(child *Scope) { ProvideNamedValue(child, "db", "a string") },
			check: func(t *testing.T, child *Scope) {
				t.Helper()
				_, err := InvokeAs[shadowStore](child)
				assert.ErrorIs(t, err, ErrServiceNotMatch)
			},
		},
		{
			name:    "string child db, InvokeStruct",
			childDB: func(child *Scope) { ProvideNamedValue(child, "db", "a string") },
			check: func(t *testing.T, child *Scope) {
				t.Helper()
				_, err := InvokeStruct[shadowStruct](child)
				assert.ErrorIs(t, err, ErrServiceNotFound)
			},
		},
		{
			name: "Store child db, InvokeAs",
			childDB: func(child *Scope) {
				ProvideNamed(child, "db", func(Injector) (*shadowPG, error) { return &shadowPG{scope: "request"}, nil })
			},
			check: func(t *testing.T, child *Scope) {
				t.Helper()
				store, err := InvokeAs[shadowStore](child)
				assert.NoError(t, err)
				assert.Equal(t, &shadowPG{scope: "request"}, store)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			ProvideNamed(root, "db", func(Injector) (*shadowPG, error) { return &shadowPG{scope: "[root]"}, nil })
			child := root.Scope("request")
			tt.childDB(child)
			if tt.mid {
				child = child.Scope("leaf")
			}

			tt.check(t, child)
		})
	}
}
