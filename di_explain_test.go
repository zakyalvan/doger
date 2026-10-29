package doger

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kartaladev/doger/stacktrace"
	"github.com/stretchr/testify/assert"
)

/////////////////////////////////////////////////////////////////////////////
// 							Templating helpers
/////////////////////////////////////////////////////////////////////////////

var dirname = must1(os.Getwd())

func fakeProvider1(i Injector) (int, error) {
	return 42, nil
}

func fakeProvider2(i Injector) (int, error) {
	_ = MustInvokeNamed[int](i, "SERVICE-A1")
	_ = MustInvokeNamed[int](i, "SERVICE-A2")
	return 42, nil
}

func fakeProvider3(i Injector) (int, error) {
	_ = MustInvokeNamed[int](i, "SERVICE-B")
	return 42, nil
}

func fakeProvider4(i Injector) (int, error) {
	_ = MustInvokeNamed[int](i, "SERVICE-C1")
	_ = MustInvokeNamed[int](i, "SERVICE-C2")
	return 42, nil
}

func fakeProvider5(i Injector) (int, error) {
	_ = MustInvokeNamed[int](i, "SERVICE-D")
	return 42, nil
}

func fakeProvider6(i Injector) (int, error) {
	_ = MustInvokeNamed[int](i, "SERVICE-E")
	return 42, nil
}

func TestFromTemplate(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name     string
		template string
		data     map[string]any
		expected string
	}{
		{name: "nil_data", template: "foobar", data: nil, expected: "foobar"},
		{name: "unused_data", template: "foobar", data: map[string]any{"foo": "bar"}, expected: "foobar"},
		{name: "field", template: "foo {{.Bar}}", data: map[string]any{"Bar": "bar"}, expected: "foo bar"},
		{name: "missing_field", template: "foo {{.Foo}}", data: map[string]any{"Baz": "bar"}, expected: "foo "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			output := fromTemplate(tt.template, tt.data)
			is.Equal(tt.expected, output)
		})
	}
}

/////////////////////////////////////////////////////////////////////////////
// 							Explain services
/////////////////////////////////////////////////////////////////////////////

func TestExplainService_String(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name     string
		output   ExplainServiceOutput
		expected string
	}{
		{
			name: "all_fields",
			output: ExplainServiceOutput{
				ScopeID:          "scope-123",
				ScopeName:        "test-scope",
				ServiceName:      "test-service",
				ServiceType:      ServiceTypeLazy,
				ServiceBuildTime: 2 * time.Second,
				Invoked:          &stacktrace.Frame{File: "test.go", Line: 42, Function: "provider"},
				Dependencies: []ExplainServiceDependencyOutput{
					{ScopeID: "scope-123", ScopeName: "test-scope", Service: "dep1", Recursive: []ExplainServiceDependencyOutput{}},
					{ScopeID: "scope-123", ScopeName: "test-scope", Service: "dep2", Recursive: []ExplainServiceDependencyOutput{}},
				},
				Dependents: []ExplainServiceDependencyOutput{
					{ScopeID: "scope-123", ScopeName: "test-scope", Service: "dependent1", Recursive: []ExplainServiceDependencyOutput{}},
					{ScopeID: "scope-123", ScopeName: "test-scope", Service: "dependent2", Recursive: []ExplainServiceDependencyOutput{}},
				},
			},
			expected: `
Scope ID: scope-123
Scope name: test-scope

Service name: test-service
Service type: lazy
Service build time: 2s
Invoked: test.go:provider:42

Dependencies:
* dep1 from scope test-scope
* dep2 from scope test-scope

Dependents:
* dependent1 from scope test-scope
* dependent2 from scope test-scope
`,
		},
		{
			name: "minimal_fields",
			output: ExplainServiceOutput{
				ScopeID:     "scope-123",
				ScopeName:   "test-scope",
				ServiceName: "test-service",
				ServiceType: ServiceTypeEager,
			},
			expected: `
Scope ID: scope-123
Scope name: test-scope

Service name: test-service
Service type: eager
Invoked: 

Dependencies:


Dependents:

`,
		},
		{
			name: "no_dependencies_or_dependents",
			output: ExplainServiceOutput{
				ScopeID:      "scope-123",
				ScopeName:    "test-scope",
				ServiceName:  "test-service",
				ServiceType:  ServiceTypeTransient,
				Dependencies: []ExplainServiceDependencyOutput{},
				Dependents:   []ExplainServiceDependencyOutput{},
			},
			expected: `
Scope ID: scope-123
Scope name: test-scope

Service name: test-service
Service type: transient
Invoked: 

Dependencies:


Dependents:

`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.expected, tt.output.String())
		})
	}
}

func TestExplainServiceDependency_String(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	a1 := ExplainServiceDependencyOutput{
		ScopeID:   "1234",
		ScopeName: "scope-a",
		Service:   "service-a1",
		Recursive: []ExplainServiceDependencyOutput{},
	}
	a2 := ExplainServiceDependencyOutput{
		ScopeID:   "1234",
		ScopeName: "scope-a",
		Service:   "service-a2",
		Recursive: []ExplainServiceDependencyOutput{},
	}
	b := ExplainServiceDependencyOutput{
		ScopeID:   "1234",
		ScopeName: "scope-a",
		Service:   "service-b",
		Recursive: []ExplainServiceDependencyOutput{a1, a2},
	}
	c := ExplainServiceDependencyOutput{
		ScopeID:   "1234",
		ScopeName: "scope-a",
		Service:   "service-c",
		Recursive: []ExplainServiceDependencyOutput{b},
	}

	tests := []struct {
		name       string
		dependency ExplainServiceDependencyOutput
		expected   string
	}{
		{
			name:       "service-a1",
			dependency: a1,
			expected:   `* service-a1 from scope scope-a`,
		},
		{
			name:       "service-b",
			dependency: b,
			expected: `* service-b from scope scope-a
  * service-a1 from scope scope-a
  * service-a2 from scope scope-a`,
		},
		{
			name:       "service-c",
			dependency: c,
			expected: `* service-c from scope scope-a
  * service-b from scope scope-a
    * service-a1 from scope scope-a
    * service-a2 from scope scope-a`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.expected, tt.dependency.String())
		})
	}
}

func TestExplainService(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// prepare env
	i := New()
	scope := i.Scope("scope-child")
	scope.id = "scope-id-123"
	ProvideNamed(i, "SERVICE-A1", fakeProvider1)
	ProvideNamed(i, "SERVICE-A2", fakeProvider1)
	ProvideNamed(i, "SERVICE-B", fakeProvider2)
	ProvideNamed(scope, "SERVICE-C1", fakeProvider3)
	ProvideNamed(scope, "SERVICE-C2", fakeProvider3)
	ProvideNamed(scope, "SERVICE-D", fakeProvider4)
	ProvideNamed(scope, "SERVICE-E", fakeProvider5)
	_, _ = InvokeNamed[int](scope, "SERVICE-E")

	// Test explaining a service by type (needs to be invoked first)
	_, _ = InvokeNamed[int](scope, "SERVICE-E")
	output, ok := ExplainNamedService(scope, "SERVICE-E")
	is.True(ok)
	is.NotNil(output)
	is.Equal("SERVICE-E", output.ServiceName)
	is.Equal(ServiceTypeLazy, output.ServiceType)
	is.Equal("scope-id-123", output.ScopeID)
	is.Equal("scope-child", output.ScopeName)

	// Test explaining a service that doesn't exist
	output2, ok2 := ExplainService[string](scope)
	is.False(ok2)
	is.Equal(ExplainServiceOutput{}, output2)

	// Test explaining a service that exists but hasn't been invoked
	_, _ = InvokeNamed[int](i, "SERVICE-A1")
	output3, ok3 := ExplainNamedService(i, "SERVICE-A1")
	is.True(ok3)
	is.NotNil(output3)
	is.Equal("SERVICE-A1", output3.ServiceName)
	is.Equal(ServiceTypeLazy, output3.ServiceType)
	is.NotEmpty(output3.ScopeID) // Root scope has a generated ID
	is.Equal("[root]", output3.ScopeName)
}

func TestExplainNamedService(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	// prepare env
	i := New()
	scope := i.Scope("scope-child")
	scope.id = "scope-id-123"
	ProvideNamed(i, "SERVICE-A1", fakeProvider1)
	ProvideNamed(i, "SERVICE-A2", fakeProvider1)
	ProvideNamed(i, "SERVICE-B", fakeProvider2)
	ProvideNamed(scope, "SERVICE-C1", fakeProvider3)
	ProvideNamed(scope, "SERVICE-C2", fakeProvider3)
	ProvideNamed(scope, "SERVICE-D", fakeProvider4)
	ProvideNamed(scope, "SERVICE-E", fakeProvider5)
	ProvideNamed(scope, "SERVICE-F", fakeProvider6)
	_, _ = InvokeNamed[int](scope, "SERVICE-F")

	output, ok := ExplainNamedService(scope, "SERVICE-E")
	is.True(ok)

	tests := []struct {
		name      string
		buildTime time.Duration
		expected  string
	}{
		{
			name:      "full_explain",
			buildTime: 1 * time.Second,
			expected: `
Scope ID: scope-id-123
Scope name: scope-child

Service name: SERVICE-E
Service type: lazy
Service build time: 1s
Invoked: ` + dirname + `/di_explain_test.go:fakeProvider5:40

Dependencies:
* SERVICE-D from scope scope-child
  * SERVICE-C1 from scope scope-child
    * SERVICE-B from scope [root]
      * SERVICE-A1 from scope [root]
      * SERVICE-A2 from scope [root]
  * SERVICE-C2 from scope scope-child
    * SERVICE-B from scope [root]
      * SERVICE-A1 from scope [root]
      * SERVICE-A2 from scope [root]

Dependents:
* SERVICE-F from scope scope-child
`,
		},
		{
			name:      "without_build_time",
			buildTime: 0,
			expected: `
Scope ID: scope-id-123
Scope name: scope-child

Service name: SERVICE-E
Service type: lazy
Invoked: ` + dirname + `/di_explain_test.go:fakeProvider5:40

Dependencies:
* SERVICE-D from scope scope-child
  * SERVICE-C1 from scope scope-child
    * SERVICE-B from scope [root]
      * SERVICE-A1 from scope [root]
      * SERVICE-A2 from scope [root]
  * SERVICE-C2 from scope scope-child
    * SERVICE-B from scope [root]
      * SERVICE-A1 from scope [root]
      * SERVICE-A2 from scope [root]

Dependents:
* SERVICE-F from scope scope-child
`,
		},
	}

	// not parallel: each row copies output before it is reassigned below
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			explained := output
			explained.ServiceBuildTime = tt.buildTime
			is.Equal(tt.expected, explained.String())
		})
	}

	// service not found
	output, ok = ExplainNamedService(scope, "not_found")
	is.False(ok)
	is.Equal(ExplainServiceOutput{}, output)
}

// explainTree renders dependencies as name(children), siblings comma-separated.
func explainTree(deps []ExplainServiceDependencyOutput) string {
	tree := ""
	for index, dep := range deps {
		if index > 0 {
			tree += ","
		}
		tree += dep.Service + "(" + explainTree(dep.Recursive) + ")"
	}
	return tree
}

func TestExplainNamedService_GraphCycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		declared     [][2]string // dependent, dependency: declared before the build closes the cycle
		dependencies string
		dependents   string
	}{
		{name: "two services", declared: [][2]string{{"b", "a"}}, dependencies: "b(a())", dependents: "b(a())"},
		{name: "three services", declared: [][2]string{{"c", "a"}}, dependencies: "b(c(a()))", dependents: "c(b(a()))"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			// a invokes b, which invokes c when registered
			root := New()
			ProvideNamed(root, "c", func(Injector) (int, error) { return 1, nil })
			ProvideNamed(root, "b", func(i Injector) (int64, error) {
				if tt.name == "three services" {
					_, err := InvokeNamed[int](i, "c")
					return 2, err
				}
				return 2, nil
			})
			ProvideNamed(root, "a", func(i Injector) (string, error) {
				_, err := InvokeNamed[int64](i, "b")
				return "a", err
			})
			for _, edge := range tt.declared {
				is.NoError(DeclareDependency(root, edge[0], edge[1]))
			}
			_, err := InvokeNamed[string](root, "a")
			is.NoError(err)

			output, ok := ExplainNamedService(root, "a")

			// the repeated service is listed without its dependencies
			is.True(ok)
			is.Equal(tt.dependencies, explainTree(output.Dependencies))
			is.Equal(tt.dependents, explainTree(output.Dependents))
			is.NotEmpty(output.String())
		})
	}
}

func TestExplainNamedService_FromOtherScope(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	ProvideNamed(root, "b", func(Injector) (int, error) { return 1, nil })
	ProvideNamed(root, "a", func(i Injector) (string, error) {
		_, err := InvokeNamed[int](i, "b")
		return "a", err
	})
	_, err := InvokeNamed[string](root, "a")
	is.NoError(err)
	child := root.Scope("request")

	fromRoot, okRoot := ExplainNamedService(root, "a")
	fromChild, okChild := ExplainNamedService(child, "a")
	fromChildB, _ := ExplainNamedService(child, "b")

	is.True(okRoot)
	is.True(okChild)
	is.Equal("b()", explainTree(fromRoot.Dependencies))
	is.Equal(fromRoot.Dependencies, fromChild.Dependencies)
	is.Equal("a()", explainTree(fromChildB.Dependents))
}

/////////////////////////////////////////////////////////////////////////////
// 							Explain scopes
/////////////////////////////////////////////////////////////////////////////

func TestExplainInjector(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	// prepare env
	i := New()
	i.self.id = "scope-id-root"
	scope0 := i.Scope("scope-0")
	scope0.id = "scope-id-0"
	scope1a := scope0.Scope("scope-1a")
	scope1a.id = "scope-id-1a"
	scope1b := scope0.Scope("scope-1b")
	scope1b.id = "scope-id-1b"
	scope2a := scope1a.Scope("scope-2a")
	scope2a.id = "scope-id-2a"
	scope2b := scope1a.Scope("scope-2b")
	scope2b.id = "scope-id-2b"
	ProvideNamed(i, "SERVICE-A1", fakeProvider1)
	ProvideNamed(i, "SERVICE-A2", fakeProvider1)
	ProvideNamed(i, "SERVICE-B", fakeProvider2)
	ProvideNamed(scope1a, "SERVICE-C1", fakeProvider3)
	ProvideNamed(scope1a, "SERVICE-C2", fakeProvider3)
	ProvideNamed(scope1a, "SERVICE-D", fakeProvider4)
	ProvideNamed(scope1a, "SERVICE-E", fakeProvider5)
	ProvideNamed(scope1b, "SERVICE-F", fakeProvider6)
	ProvideNamedTransient[*lazyTest](scope2a, "SERVICE-TRANSIENT-SIMPLE", func(i Injector) (*lazyTest, error) { return &lazyTest{}, nil })
	ProvideNamed[*lazyTestHeathcheckerOK](scope2a, "SERVICE-LAZY-HEALTH", func(i Injector) (*lazyTestHeathcheckerOK, error) { return &lazyTestHeathcheckerOK{}, nil })
	ProvideNamed[*lazyTestShutdownerOK](scope2b, "SERVICE-LAZY-SHUTDOWN", func(i Injector) (*lazyTestShutdownerOK, error) { return &lazyTestShutdownerOK{}, nil })
	ProvideNamedValue[int](scope1a, "SERVICE-EAGER-VALUE", 1)
	_ = AsNamed[*lazyTestHeathcheckerOK, Healthchecker](scope2a, "SERVICE-LAZY-HEALTH", "SERVICE-ALIAS-HEALTH")
	_, _ = InvokeNamed[int](scope1a, "SERVICE-D")
	_, _ = InvokeNamed[*lazyTestHeathcheckerOK](scope2a, "SERVICE-LAZY-HEALTH")
	_, _ = InvokeNamed[*lazyTestShutdownerOK](scope2b, "SERVICE-LAZY-SHUTDOWN")

	tests := []struct {
		name     string
		injector Injector
		expected string
	}{
		{
			name:     "root",
			injector: i,
			expected: `Scope ID: scope-id-root
Scope name: [root]

DAG:
 |
  \_ [root] (ID: scope-id-root)
      * 😴 SERVICE-A1
      * 😴 SERVICE-A2
      * 😴 SERVICE-B
      |
      |
       \_ scope-0 (ID: scope-id-0)
           |
           |
           |\_ scope-1a (ID: scope-id-1a)
           |    * 😴 SERVICE-C1
           |    * 😴 SERVICE-C2
           |    * 😴 SERVICE-D
           |    * 😴 SERVICE-E
           |    * 🔁 SERVICE-EAGER-VALUE
           |    |
           |    |
           |    |\_ scope-2a (ID: scope-id-2a)
           |    |    * 🔗 SERVICE-ALIAS-HEALTH 🫀
           |    |    * 😴 SERVICE-LAZY-HEALTH 🫀
           |    |    * 🏭 SERVICE-TRANSIENT-SIMPLE
           |    |
           |    |
           |     \_ scope-2b (ID: scope-id-2b)
           |         * 😴 SERVICE-LAZY-SHUTDOWN 🙅
           |
           |
            \_ scope-1b (ID: scope-id-1b)
                * 😴 SERVICE-F
`,
		},
		{
			name:     "scope0",
			injector: scope0,
			expected: `Scope ID: scope-id-0
Scope name: scope-0

DAG:
 |
  \_ [root] (ID: scope-id-root)
      * 😴 SERVICE-A1
      * 😴 SERVICE-A2
      * 😴 SERVICE-B
      |
      |
       \_ scope-0 (ID: scope-id-0)
           |
           |
           |\_ scope-1a (ID: scope-id-1a)
           |    * 😴 SERVICE-C1
           |    * 😴 SERVICE-C2
           |    * 😴 SERVICE-D
           |    * 😴 SERVICE-E
           |    * 🔁 SERVICE-EAGER-VALUE
           |    |
           |    |
           |    |\_ scope-2a (ID: scope-id-2a)
           |    |    * 🔗 SERVICE-ALIAS-HEALTH 🫀
           |    |    * 😴 SERVICE-LAZY-HEALTH 🫀
           |    |    * 🏭 SERVICE-TRANSIENT-SIMPLE
           |    |
           |    |
           |     \_ scope-2b (ID: scope-id-2b)
           |         * 😴 SERVICE-LAZY-SHUTDOWN 🙅
           |
           |
            \_ scope-1b (ID: scope-id-1b)
                * 😴 SERVICE-F
`,
		},
		{
			name:     "scope1a",
			injector: scope1a,
			expected: `Scope ID: scope-id-1a
Scope name: scope-1a

DAG:
 |
  \_ [root] (ID: scope-id-root)
      * 😴 SERVICE-A1
      * 😴 SERVICE-A2
      * 😴 SERVICE-B
      |
      |
       \_ scope-0 (ID: scope-id-0)
           |
           |
            \_ scope-1a (ID: scope-id-1a)
                * 😴 SERVICE-C1
                * 😴 SERVICE-C2
                * 😴 SERVICE-D
                * 😴 SERVICE-E
                * 🔁 SERVICE-EAGER-VALUE
                |
                |
                |\_ scope-2a (ID: scope-id-2a)
                |    * 🔗 SERVICE-ALIAS-HEALTH 🫀
                |    * 😴 SERVICE-LAZY-HEALTH 🫀
                |    * 🏭 SERVICE-TRANSIENT-SIMPLE
                |
                |
                 \_ scope-2b (ID: scope-id-2b)
                     * 😴 SERVICE-LAZY-SHUTDOWN 🙅
`,
		},
		{
			name:     "scope1b",
			injector: scope1b,
			expected: `Scope ID: scope-id-1b
Scope name: scope-1b

DAG:
 |
  \_ [root] (ID: scope-id-root)
      * 😴 SERVICE-A1
      * 😴 SERVICE-A2
      * 😴 SERVICE-B
      |
      |
       \_ scope-0 (ID: scope-id-0)
           |
           |
            \_ scope-1b (ID: scope-id-1b)
                * 😴 SERVICE-F
`,
		},
		{
			name:     "scope2a",
			injector: scope2a,
			expected: `Scope ID: scope-id-2a
Scope name: scope-2a

DAG:
 |
  \_ [root] (ID: scope-id-root)
      * 😴 SERVICE-A1
      * 😴 SERVICE-A2
      * 😴 SERVICE-B
      |
      |
       \_ scope-0 (ID: scope-id-0)
           |
           |
            \_ scope-1a (ID: scope-id-1a)
                * 😴 SERVICE-C1
                * 😴 SERVICE-C2
                * 😴 SERVICE-D
                * 😴 SERVICE-E
                * 🔁 SERVICE-EAGER-VALUE
                |
                |
                 \_ scope-2a (ID: scope-id-2a)
                     * 🔗 SERVICE-ALIAS-HEALTH 🫀
                     * 😴 SERVICE-LAZY-HEALTH 🫀
                     * 🏭 SERVICE-TRANSIENT-SIMPLE
`,
		},
		{
			name:     "scope2b",
			injector: scope2b,
			expected: `Scope ID: scope-id-2b
Scope name: scope-2b

DAG:
 |
  \_ [root] (ID: scope-id-root)
      * 😴 SERVICE-A1
      * 😴 SERVICE-A2
      * 😴 SERVICE-B
      |
      |
       \_ scope-0 (ID: scope-id-0)
           |
           |
            \_ scope-1a (ID: scope-id-1a)
                * 😴 SERVICE-C1
                * 😴 SERVICE-C2
                * 😴 SERVICE-D
                * 😴 SERVICE-E
                * 🔁 SERVICE-EAGER-VALUE
                |
                |
                 \_ scope-2b (ID: scope-id-2b)
                     * 😴 SERVICE-LAZY-SHUTDOWN 🙅
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := assert.New(t)

			output := ExplainInjector(tt.injector)
			is.Equal(tt.expected, output.String())
		})
	}
}

func TestExplainInjector_repeatedServices(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)
	is := assert.New(t)

	rootCtx := context.Background()

	// Create the rootScope with a context and a string provided.
	rootScope := New(
		Eager(rootCtx),
		Eager("Hello, World!"),
	)
	rootScope.self.id = "root-scope"
	_ = MustInvoke[context.Context](rootScope) // Add (rootScope).context.Context to the invoked list.

	// Create a childScope with only a context.
	childScope := rootScope.Scope("child",
		Eager(rootCtx),
	)
	childScope.id = "scope-123"
	_ = MustInvoke[context.Context](childScope) // Add (childScope).context.Context to the invoked list.

	output := ExplainInjector(childScope)

	is.Equal(`Scope ID: scope-123
Scope name: child

DAG:
 |
  \_ [root] (ID: root-scope)
      * 🔁 context.Context
      * 🔁 string
      |
      |
       \_ child (ID: scope-123)
           * 🔁 context.Context
`, output.String())

	is.Equal([]ExplainInjectorScopeOutput{
		{
			ScopeID:   "root-scope",
			ScopeName: "[root]",
			Scope:     rootScope.self,
			Services: []ExplainInjectorServiceOutput{
				{
					ServiceName:      "context.Context",
					ServiceType:      ServiceTypeEager,
					ServiceTypeIcon:  "🔁",
					ServiceBuildTime: 0,
					IsHealthchecker:  false,
					IsShutdowner:     false,
				},
				{
					ServiceName:      "string",
					ServiceType:      ServiceTypeEager,
					ServiceTypeIcon:  "🔁",
					ServiceBuildTime: 0,
					IsHealthchecker:  false,
					IsShutdowner:     false,
				},
			},
			Children: []ExplainInjectorScopeOutput{
				{
					ScopeID:   "scope-123",
					ScopeName: "child",
					Scope:     childScope,
					Services: []ExplainInjectorServiceOutput{
						{
							ServiceName:      "context.Context",
							ServiceType:      ServiceTypeEager,
							ServiceTypeIcon:  "🔁",
							ServiceBuildTime: 0,
							IsHealthchecker:  false,
							IsShutdowner:     false,
						},
					},
					Children:   []ExplainInjectorScopeOutput{},
					IsAncestor: false,
					IsChildren: false,
				},
			},
			IsAncestor: true,
			IsChildren: false,
		},
	}, output.DAG)
}

func TestExplainInjector_String(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name     string
		output   ExplainInjectorOutput
		expected string
	}{
		{
			name: "all_fields",
			output: ExplainInjectorOutput{
				ScopeID:   "scope-123",
				ScopeName: "test-scope",
				DAG: []ExplainInjectorScopeOutput{
					{
						ScopeID:   "scope-123",
						ScopeName: "test-scope",
						Services: []ExplainInjectorServiceOutput{
							{
								ServiceName:      "service1",
								ServiceType:      ServiceTypeLazy,
								ServiceTypeIcon:  "😴",
								ServiceBuildTime: 1 * time.Second,
								IsHealthchecker:  true,
								IsShutdowner:     false,
							},
							{
								ServiceName:      "service2",
								ServiceType:      ServiceTypeEager,
								ServiceTypeIcon:  "🔁",
								ServiceBuildTime: 0,
								IsHealthchecker:  false,
								IsShutdowner:     true,
							},
						},
						Children: []ExplainInjectorScopeOutput{
							{
								ScopeID:   "child-123",
								ScopeName: "child-scope",
								Services:  []ExplainInjectorServiceOutput{},
								Children:  []ExplainInjectorScopeOutput{},
							},
						},
					},
				},
			},
			expected: `Scope ID: scope-123
Scope name: test-scope

DAG:
 |
  \_ test-scope (ID: scope-123)
      * 😴 service1 🫀
      * 🔁 service2 🙅
      |
      |
       \_ child-scope (ID: child-123)
`,
		},
		{
			name: "minimal_fields",
			output: ExplainInjectorOutput{
				ScopeID:   "scope-123",
				ScopeName: "test-scope",
				DAG:       []ExplainInjectorScopeOutput{},
			},
			expected: `Scope ID: scope-123
Scope name: test-scope

DAG:

`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.expected, tt.output.String())
		})
	}
}

func TestExplainInjectorScope_String(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name     string
		output   ExplainInjectorScopeOutput
		expected string
	}{
		{
			name: "all_fields",
			output: ExplainInjectorScopeOutput{
				ScopeID:   "scope-123",
				ScopeName: "test-scope",
				Services: []ExplainInjectorServiceOutput{
					{
						ServiceName:      "service1",
						ServiceType:      ServiceTypeLazy,
						ServiceTypeIcon:  "😴",
						ServiceBuildTime: 1 * time.Second,
						IsHealthchecker:  true,
						IsShutdowner:     false,
					},
					{
						ServiceName:      "service2",
						ServiceType:      ServiceTypeEager,
						ServiceTypeIcon:  "🔁",
						ServiceBuildTime: 0,
						IsHealthchecker:  false,
						IsShutdowner:     true,
					},
				},
				Children: []ExplainInjectorScopeOutput{
					{
						ScopeID:   "child-123",
						ScopeName: "child-scope",
						Services:  []ExplainInjectorServiceOutput{},
						Children:  []ExplainInjectorScopeOutput{},
					},
				},
				IsAncestor: false,
				IsChildren: true,
			},
			expected: `test-scope (ID: scope-123)
 * 😴 service1 🫀
 * 🔁 service2 🙅
 |
 |
  \_ child-scope (ID: child-123)`,
		},
		{
			name: "minimal_fields",
			output: ExplainInjectorScopeOutput{
				ScopeID:    "scope-123",
				ScopeName:  "test-scope",
				Services:   []ExplainInjectorServiceOutput{},
				Children:   []ExplainInjectorScopeOutput{},
				IsAncestor: true,
				IsChildren: false,
			},
			expected: `test-scope (ID: scope-123)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.expected, tt.output.String())
		})
	}
}

func TestExplainInjectorService_String(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 100*time.Millisecond)

	tests := []struct {
		name     string
		svc      ExplainInjectorServiceOutput
		expected string
	}{
		{
			name:     "lazy_healthchecker_shutdowner",
			svc:      ExplainInjectorServiceOutput{ServiceName: "service-name", ServiceType: ServiceTypeLazy, ServiceTypeIcon: "😴", ServiceBuildTime: 1 * time.Second, IsHealthchecker: true, IsShutdowner: true},
			expected: ` * 😴 service-name 🫀 🙅`,
		},
		{
			name:     "eager_healthchecker",
			svc:      ExplainInjectorServiceOutput{ServiceName: "service-name", ServiceType: ServiceTypeEager, ServiceTypeIcon: "🔁", IsHealthchecker: true, IsShutdowner: false},
			expected: ` * 🔁 service-name 🫀`,
		},
		{
			name:     "no_service_type",
			svc:      ExplainInjectorServiceOutput{ServiceName: "service-name", IsHealthchecker: true, IsShutdowner: true},
			expected: ` * ❓ service-name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.expected, tt.svc.String())
		})
	}
}
