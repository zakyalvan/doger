package doger

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registrationIndexOf returns the registration index of name, registered in scope itself.
func registrationIndexOf(t *testing.T, scope Injector, name string) uint64 {
	t.Helper()

	serviceAny, ok := scope.serviceGet(name)
	require.Truef(t, ok, "service %q not registered", name)
	service, ok := serviceAny.(serviceWrapperAny)
	require.True(t, ok)

	return service.getRegistrationIndex()
}

func TestRegistrationIndex_Order(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		act    func(root *RootScope) Injector
		target string
		want   uint64
	}{
		{
			name: "same_scope_override_keeps_index",
			act: func(root *RootScope) Injector {
				OverrideNamedValue(root, "a", 10)
				return root
			},
			target: "a",
			want:   0,
		},
		{
			name: "child_scope_override_gets_new_index",
			act: func(root *RootScope) Injector {
				child := root.Scope("request")
				OverrideNamedValue(child, "a", 10)
				return child
			},
			target: "a",
			want:   2,
		},
		{
			name: "clone_continues_counter",
			act: func(root *RootScope) Injector {
				clone := root.Clone()
				ProvideNamedValue(clone, "c", 3)
				return clone
			},
			target: "c",
			want:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			ProvideNamedValue(root, "a", 1)
			ProvideNamedValue(root, "b", 2)

			scope := tt.act(root)

			is.Equal(tt.want, registrationIndexOf(t, scope, tt.target))
			is.Equal(uint64(0), registrationIndexOf(t, root, "a"))
			is.Equal(uint64(1), registrationIndexOf(t, root, "b"))
		})
	}
}

func TestRegistrationIndex_ConcurrentUnique(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	root := New()
	child := root.Scope("child")

	const services = 100
	var wg sync.WaitGroup
	for n := range services {
		wg.Go(func() {
			scope := Injector(root)
			if n%2 == 1 {
				scope = child
			}
			ProvideNamedValue(scope, fmt.Sprintf("s%d", n), n)
		})
	}
	wg.Wait()

	seen := map[uint64]string{}
	for n := range services {
		scope := Injector(root)
		if n%2 == 1 {
			scope = child
		}
		name := fmt.Sprintf("s%d", n)
		index := registrationIndexOf(t, scope, name)
		if previous, ok := seen[index]; ok {
			is.Failf("duplicate registration index", "%s and %s share %d", previous, name, index)
		}
		seen[index] = name
		is.Less(index, uint64(services))
	}
}

func TestRegistrationIndex_CloneDuringRegistration(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)
	is := assert.New(t)

	root := New()

	const services = 50
	start := make(chan struct{})
	var wg sync.WaitGroup
	for n := range services {
		wg.Go(func() {
			<-start
			ProvideNamedValue(root, fmt.Sprintf("s%d", n), n)
		})
	}

	var clone *RootScope
	wg.Go(func() {
		<-start
		clone = root.Clone()
	})
	close(start)
	wg.Wait()

	// registrations made in the clone after it was taken must not reuse copied indexes
	for n := range services {
		ProvideNamedValue(clone, fmt.Sprintf("late%d", n), n)
	}

	seen := map[uint64]string{}
	clone.serviceForEach(func(name string, _ *Scope, _ any) bool {
		index := registrationIndexOf(t, clone, name)
		if previous, ok := seen[index]; ok {
			is.Failf("duplicate registration index", "%s and %s share %d", previous, name, index)
		}
		seen[index] = name
		return true
	})
}

// Registration-option fixtures.
type (
	optDB     struct{}
	optConfig struct{}
	optServer struct{}
)

func (*optServer) Start() error                  { return nil }
func (*optServer) Run(ctx context.Context) error { return nil }

// optIns describes the opt-ins a registration carries.
type optIns struct {
	starter   bool
	dependsOn []string
}

// optInsOf returns the opt-ins of name, registered in scope itself.
func optInsOf(t *testing.T, scope Injector, name string) optIns {
	t.Helper()

	serviceAny, ok := scope.serviceGet(name)
	require.Truef(t, ok, "service %q not registered", name)
	service, ok := serviceAny.(serviceWrapperAny)
	require.True(t, ok)

	opts := service.getOptions()
	if opts == nil {
		return optIns{}
	}

	return optIns{starter: opts.starter, dependsOn: opts.dependsOn}
}

func TestProvideOptions_Registration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(root *RootScope)
		service  string
		instance func(t *testing.T, root *RootScope) (any, error)
		want     optIns
	}{
		{
			name:     "provide_none",
			register: func(root *RootScope) { Provide(root, func(Injector) (*optServer, error) { return &optServer{}, nil }) },
			service:  NameOf[*optServer](),
			instance: func(_ *testing.T, root *RootScope) (any, error) { return Invoke[*optServer](root) },
			want:     optIns{},
		},
		{
			name: "provide_named_as_starter",
			register: func(root *RootScope) {
				ProvideNamed(root, "srv", func(Injector) (*optServer, error) { return &optServer{}, nil }, AsStarter())
			},
			service:  "srv",
			instance: func(_ *testing.T, root *RootScope) (any, error) { return InvokeNamed[*optServer](root, "srv") },
			want:     optIns{starter: true},
		},
		{
			name:     "provide_value_nil_option",
			register: func(root *RootScope) { ProvideValue(root, &optConfig{}, nil) },
			service:  NameOf[*optConfig](),
			instance: func(_ *testing.T, root *RootScope) (any, error) { return Invoke[*optConfig](root) },
			want:     optIns{},
		},
		{
			name:     "provide_named_value_nil_and_as_starter",
			register: func(root *RootScope) { ProvideNamedValue(root, "srv", &optServer{}, nil, AsStarter()) },
			service:  "srv",
			instance: func(_ *testing.T, root *RootScope) (any, error) { return InvokeNamed[*optServer](root, "srv") },
			want:     optIns{starter: true},
		},
		{
			name:     "eager_named_depends_on",
			register: func(root *RootScope) { EagerNamed("cfg", &optConfig{}, DependsOnNamed("db"))(root) },
			service:  "cfg",
			instance: func(_ *testing.T, root *RootScope) (any, error) { return InvokeNamed[*optConfig](root, "cfg") },
			want:     optIns{dependsOn: []string{"db"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			ProvideNamed(root, "db", func(Injector) (*optDB, error) { return &optDB{}, nil })

			tt.register(root)

			instance, err := tt.instance(t, root)
			is.NoError(err)
			is.NotNil(instance)
			is.Equal(tt.want, optInsOf(t, root, tt.service))
		})
	}
}

func TestProvideOptions_SignatureHasVariadic(t *testing.T) {
	t.Parallel()

	injector := reflect.TypeFor[Injector]()
	str := reflect.TypeFor[string]()
	integer := reflect.TypeFor[int]()
	provider := reflect.TypeFor[Provider[int]]()

	tests := []struct {
		name   string
		fn     any
		params []reflect.Type
	}{
		{name: "Provide", fn: Provide[int], params: []reflect.Type{injector, provider}},
		{name: "ProvideNamed", fn: ProvideNamed[int], params: []reflect.Type{injector, str, provider}},
		{name: "ProvideValue", fn: ProvideValue[int], params: []reflect.Type{injector, integer}},
		{name: "ProvideNamedValue", fn: ProvideNamedValue[int], params: []reflect.Type{injector, str, integer}},
		{name: "ProvideTransient", fn: ProvideTransient[int], params: []reflect.Type{injector, provider}},
		{name: "ProvideNamedTransient", fn: ProvideNamedTransient[int], params: []reflect.Type{injector, str, provider}},
		{name: "Override", fn: Override[int], params: []reflect.Type{injector, provider}},
		{name: "OverrideNamed", fn: OverrideNamed[int], params: []reflect.Type{injector, str, provider}},
		{name: "OverrideValue", fn: OverrideValue[int], params: []reflect.Type{injector, integer}},
		{name: "OverrideNamedValue", fn: OverrideNamedValue[int], params: []reflect.Type{injector, str, integer}},
		{name: "OverrideTransient", fn: OverrideTransient[int], params: []reflect.Type{injector, provider}},
		{name: "OverrideNamedTransient", fn: OverrideNamedTransient[int], params: []reflect.Type{injector, str, provider}},
		{name: "Lazy", fn: Lazy[int], params: []reflect.Type{provider}},
		{name: "LazyNamed", fn: LazyNamed[int], params: []reflect.Type{str, provider}},
		{name: "Eager", fn: Eager[int], params: []reflect.Type{integer}},
		{name: "EagerNamed", fn: EagerNamed[int], params: []reflect.Type{str, integer}},
		{name: "Transient", fn: Transient[int], params: []reflect.Type{provider}},
		{name: "TransientNamed", fn: TransientNamed[int], params: []reflect.Type{str, provider}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			fnType := reflect.TypeOf(tt.fn)
			is.True(fnType.IsVariadic())
			if !is.Equal(len(tt.params)+1, fnType.NumIn()) {
				return
			}
			for n, param := range tt.params {
				is.Equalf(param, fnType.In(n), "parameter %d", n)
			}
			is.Equal(reflect.TypeFor[[]ProvideOption](), fnType.In(fnType.NumIn()-1))
		})
	}
}

func TestProvideOptions_Constructors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		option ProvideOption
		want   optIns
	}{
		{name: "as_starter", option: AsStarter(), want: optIns{starter: true}},
		{name: "depends_on", option: DependsOn[*optDB](), want: optIns{dependsOn: []string{NameOf[*optDB]()}}},
		{name: "depends_on_named", option: DependsOnNamed("primary-db"), want: optIns{dependsOn: []string{"primary-db"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			Provide(root, func(Injector) (*optServer, error) { return &optServer{}, nil }, tt.option)

			assert.Equal(t, tt.want, optInsOf(t, root, NameOf[*optServer]()))
		})
	}
}
