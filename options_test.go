package doger

import (
	"fmt"
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
