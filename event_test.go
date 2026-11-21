package doger

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Event fixtures shared by the event tests.
type (
	OrderPlaced struct {
		ID     string
		Amount int
	}
	OrderCancelled   struct{ ID string }
	InvoiceRequested struct{ ID string }

	// DomainEvent is implemented by OrderPlaced with a value receiver.
	DomainEvent interface{ EventName() string }
	// PtrEvent is implemented by *OrderPlaced only.
	PtrEvent interface{ PtrEventName() string }

	// Tags is a map event type.
	Tags map[string]string
)

func (OrderPlaced) EventName() string     { return "order-placed" }
func (*OrderPlaced) PtrEventName() string { return "order-placed" }

// ErrOutOfStock is the error listeners return in the error tests.
var ErrOutOfStock = errors.New("out of stock")

// countListener registers a function listener for E on i that counts its
// calls, and returns the counter.
func countListener[E any](t *testing.T, i Injector) *int {
	t.Helper()
	calls := new(int)
	Listen(i, func(context.Context, E) error {
		*calls++
		return nil
	})

	return calls
}

func TestPublish_Assignability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		listen   func(t *testing.T, i Injector) *int
		event    any
		received int
	}{
		{name: "OrderPlaced/OrderPlaced", listen: countListener[OrderPlaced], event: OrderPlaced{ID: "42"}, received: 1},
		{name: "DomainEvent/OrderPlaced", listen: countListener[DomainEvent], event: OrderPlaced{ID: "42"}, received: 1},
		{name: "OrderPlaced/DomainEvent(OrderPlaced)", listen: countListener[OrderPlaced], event: DomainEvent(OrderPlaced{ID: "42"}), received: 1},
		{name: "OrderPlaced/OrderCancelled", listen: countListener[OrderPlaced], event: OrderCancelled{ID: "42"}, received: 0},
		{name: "*OrderPlaced/OrderPlaced", listen: countListener[*OrderPlaced], event: OrderPlaced{ID: "42"}, received: 0},
		{name: "OrderPlaced/&OrderPlaced", listen: countListener[OrderPlaced], event: &OrderPlaced{ID: "42"}, received: 0},
		{name: "PtrEvent/OrderPlaced", listen: countListener[PtrEvent], event: OrderPlaced{ID: "42"}, received: 0},
		{name: "PtrEvent/&OrderPlaced", listen: countListener[PtrEvent], event: &OrderPlaced{ID: "42"}, received: 1},
		{name: "any/OrderPlaced", listen: countListener[any], event: OrderPlaced{ID: "42"}, received: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			calls := tt.listen(t, root)

			require.NoError(t, Publish(context.Background(), root, tt.event))
			assert.Equal(t, tt.received, *calls)
		})
	}
}

func TestPublish_NoListeners(t *testing.T) {
	t.Parallel()

	root := New()
	ProvideNamed(root, "a", func(Injector) (*OrderPlaced, error) { return &OrderPlaced{}, nil })
	ProvideNamed(root, "b", func(Injector) (*OrderCancelled, error) { return &OrderCancelled{}, nil })

	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "42"}))
	assert.Empty(t, root.ListInvokedServices())
}

// recordingListener registers a function listener for E on i that counts
// its calls made with a non-nil context, and returns the counter.
func recordingListener[E any](i Injector) *int {
	calls := new(int)
	Listen(i, func(ctx context.Context, _ E) error {
		if ctx != nil {
			*calls++
		}
		return nil
	})

	return calls
}

func TestPublish_ZeroArguments(t *testing.T) {
	t.Parallel()

	// DefaultRootScope was removed from doger: a nil injector is rejected
	// with ErrNilInjector, as for every other entry point.
	tests := []struct {
		name     string
		listen   func(i Injector) *int
		ctx      context.Context //nolint:containedctx // the table row's context
		nilRoot  bool
		event    any
		matchErr error
		calls    int
	}{
		{name: "nil event", listen: recordingListener[any], ctx: context.Background(), event: nil, matchErr: ErrInvalidEvent},
		{name: "nil pointer", listen: recordingListener[*OrderPlaced], ctx: context.Background(), event: (*OrderPlaced)(nil), matchErr: ErrInvalidEvent},
		{name: "nil pointer in interface", listen: recordingListener[DomainEvent], ctx: context.Background(), event: DomainEvent((*OrderPlaced)(nil)), matchErr: ErrInvalidEvent},
		{name: "nil map", listen: recordingListener[Tags], ctx: context.Background(), event: Tags(nil), matchErr: ErrInvalidEvent},
		{name: "empty map", listen: recordingListener[Tags], ctx: context.Background(), event: Tags{}, calls: 1},
		{name: "nil injector", listen: recordingListener[OrderPlaced], ctx: context.Background(), nilRoot: true, event: OrderPlaced{ID: "42"}, matchErr: ErrNilInjector},
		{name: "nil context", listen: recordingListener[OrderPlaced], ctx: nil, event: OrderPlaced{ID: "42"}, calls: 1},
		{name: "zero value event", listen: recordingListener[OrderPlaced], ctx: context.Background(), event: OrderPlaced{}, calls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			calls := tt.listen(root)
			var injector Injector = root
			if tt.nilRoot {
				injector = nil
			}

			err := Publish(tt.ctx, injector, tt.event)
			if tt.matchErr != nil {
				require.ErrorIs(t, err, tt.matchErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.calls, *calls)
		})
	}
}

func TestListen_Arguments(t *testing.T) {
	t.Parallel()

	t.Run("nil function", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithError(t, "doger: Listen requires a non-nil function", func() {
			Listen[OrderPlaced](New(), nil)
		})
	})

	t.Run("nil injector", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithError(t, ErrNilInjector.Error(), func() {
			Listen(nil, func(context.Context, OrderPlaced) error { return nil })
		})
	})
}
