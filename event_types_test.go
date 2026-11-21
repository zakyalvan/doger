package doger

import (
	"context"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolvedEventType returns the type the registry of root binds name to,
// by name or alias, or nil.
func resolvedEventType(root *RootScope, name string) reflect.Type {
	info := root.eventTypeByName(name)
	if info == nil {
		return nil
	}

	return info.typ
}

func TestRegisterEventType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		existing bool // "orders.placed" registered for OrderPlaced
		register func(root *RootScope) error
		lookup   string
		err      error
		resolved reflect.Type
	}{
		{name: "new", register: func(r *RootScope) error { return RegisterEventType[OrderPlaced](r, "orders.placed") }, lookup: "orders.placed", resolved: reflect.TypeFor[OrderPlaced]()},
		{name: "pointer", register: func(r *RootScope) error { return RegisterEventType[*OrderPlaced](r, "orders.placed") }, lookup: "orders.placed", resolved: reflect.TypeFor[*OrderPlaced]()},
		{name: "same_again", existing: true, register: func(r *RootScope) error { return RegisterEventType[OrderPlaced](r, "orders.placed") }, lookup: "orders.placed", resolved: reflect.TypeFor[OrderPlaced]()},
		{name: "name_of_other_type", existing: true, register: func(r *RootScope) error { return RegisterEventType[OrderCancelled](r, "orders.placed") }, lookup: "orders.placed", err: ErrEventTypeConflict, resolved: reflect.TypeFor[OrderPlaced]()},
		{name: "type_under_other_name", existing: true, register: func(r *RootScope) error { return RegisterEventType[OrderPlaced](r, "orders.v2") }, lookup: "orders.placed", err: ErrEventTypeConflict, resolved: reflect.TypeFor[OrderPlaced]()},
		{name: "empty_name", register: func(r *RootScope) error { return RegisterEventType[OrderPlaced](r, "") }, lookup: "", err: ErrInvalidEventType},
		{name: "interface", register: func(r *RootScope) error { return RegisterEventType[DomainEvent](r, "domain.event") }, lookup: "domain.event", err: ErrInvalidEventType},
		{name: "any", register: func(r *RootScope) error { return RegisterEventType[any](r, "anything") }, lookup: "anything", err: ErrInvalidEventType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			if tt.existing {
				require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
			}

			err := tt.register(root)

			if tt.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.err)
			}
			assert.Equal(t, tt.resolved, resolvedEventType(root, tt.lookup))
			if tt.name == "type_under_other_name" {
				assert.Nil(t, resolvedEventType(root, "orders.v2"))
			}
		})
	}
}

// DefaultRootScope was removed from doger: a nil root is rejected like a
// nil injector, instead of the spec's fallback to it.
func TestRegisterEventType_NilRoot(t *testing.T) {
	t.Parallel()

	err := RegisterEventType[OrderPlaced](nil, "orders.placed")

	require.ErrorIs(t, err, ErrNilInjector)
}

func TestEventTypeOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		opts        []EventTypeOption
		err         error
		version     int
		destination string
		lookup      string
		decoded     reflect.Type
	}{
		{name: "defaults", version: 1, destination: "orders.placed", lookup: "orders.placed", decoded: reflect.TypeFor[OrderPlaced]()},
		{name: "version", opts: []EventTypeOption{EventVersion(3)}, version: 3, destination: "orders.placed", lookup: "orders.placed", decoded: reflect.TypeFor[OrderPlaced]()},
		{name: "destination", opts: []EventTypeOption{EventDestination("shop.orders")}, version: 1, destination: "shop.orders", lookup: "orders.placed", decoded: reflect.TypeFor[OrderPlaced]()},
		{name: "alias", opts: []EventTypeOption{EventAlias("order.created")}, version: 1, destination: "orders.placed", lookup: "order.created", decoded: reflect.TypeFor[OrderPlaced]()},
		{name: "version_zero", opts: []EventTypeOption{EventVersion(0)}, err: ErrInvalidEventType, lookup: "orders.placed"},
		{name: "destination_empty", opts: []EventTypeOption{EventDestination("")}, err: ErrInvalidEventType, lookup: "orders.placed"},
		{name: "alias_empty", opts: []EventTypeOption{EventAlias("")}, err: ErrInvalidEventType, lookup: "orders.placed"},
		{name: "last_version_wins", opts: []EventTypeOption{EventVersion(2), EventVersion(4)}, version: 4, destination: "orders.placed", lookup: "orders.placed", decoded: reflect.TypeFor[OrderPlaced]()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			err := RegisterEventType[OrderPlaced](root, "orders.placed", tt.opts...)

			assert.Equal(t, tt.decoded, resolvedEventType(root, tt.lookup))
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				assert.Nil(t, root.eventTypeOf(reflect.TypeFor[OrderPlaced]()))
				return
			}
			require.NoError(t, err)
			info := root.eventTypeOf(reflect.TypeFor[OrderPlaced]())
			require.NotNil(t, info)
			assert.Equal(t, tt.version, info.version)
			assert.Equal(t, tt.destination, info.destination)
		})
	}
}

func TestEventTypeOptions_AliasConflict(t *testing.T) {
	t.Parallel()

	root := New()
	require.NoError(t, RegisterEventType[OrderCancelled](root, "order.created"))

	err := RegisterEventType[OrderPlaced](root, "orders.placed", EventAlias("order.created"))

	require.ErrorIs(t, err, ErrEventTypeConflict)
	assert.Nil(t, resolvedEventType(root, "orders.placed"))
	assert.Equal(t, reflect.TypeFor[OrderCancelled](), resolvedEventType(root, "order.created"))
}

func TestRegisterEventType_Concurrent(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 10*time.Second)

	root := New()
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	const n = 50
	publishErrs := make(chan error, n)
	registerErrs := make(chan error, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			publishErrs <- Publish(context.Background(), root, OrderPlaced{ID: strconv.Itoa(i)})
		})
		wg.Go(func() {
			// distinct types [1]int, [2]int, ...
			registerErrs <- root.registerEventType(reflect.ArrayOf(i+1, reflect.TypeFor[int]()), "type."+strconv.Itoa(i), nil)
		})
	}
	wg.Wait()
	close(publishErrs)
	close(registerErrs)

	for err := range publishErrs {
		require.NoError(t, err)
	}
	for err := range registerErrs {
		require.NoError(t, err)
	}
	for i := range n {
		assert.Equal(t, reflect.ArrayOf(i+1, reflect.TypeFor[int]()), resolvedEventType(root, "type."+strconv.Itoa(i)))
	}
	assert.Equal(t, reflect.TypeFor[OrderPlaced](), resolvedEventType(root, "orders.placed"))
}

func TestRegisterEventType_Clone(t *testing.T) {
	t.Parallel()

	root := New()
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	clone := root.Clone()

	require.NoError(t, RegisterEventType[OrderCancelled](clone, "orders.cancelled"))

	assert.Equal(t, reflect.TypeFor[OrderPlaced](), resolvedEventType(clone, "orders.placed"))
	assert.Equal(t, reflect.TypeFor[OrderCancelled](), resolvedEventType(clone, "orders.cancelled"))
	assert.Equal(t, reflect.TypeFor[OrderPlaced](), resolvedEventType(root, "orders.placed"))
	assert.Nil(t, resolvedEventType(root, "orders.cancelled"))
}
