package doger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublisherFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		nilRoot  bool
		failOn   string // the ID the local listener fails on
		events   []any
		err      error
		listener bool // the error is a *ListenerError
		ran      int
		sends    int
	}{
		{name: "two", events: []any{OrderPlaced{ID: "1"}, OrderPlaced{ID: "2"}}, ran: 2, sends: 2},
		{name: "none", ran: 0},
		{name: "first_fails", failOn: "1", events: []any{OrderPlaced{ID: "1"}, OrderPlaced{ID: "2"}}, err: ErrOutOfStock, listener: true, ran: 1},
		{name: "nil_event", events: []any{OrderPlaced{ID: "1"}, nil}, err: ErrInvalidEvent, ran: 1, sends: 1},
		// DefaultRootScope was removed from doger: a nil injector is
		// rejected, instead of the spec's fallback to it
		{name: "nil_injector", nilRoot: true, events: []any{OrderPlaced{ID: "1"}}, err: ErrNilInjector},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventTransport: transport})
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()))
			ran := 0
			Listen(root, func(_ context.Context, e OrderPlaced) error {
				ran++
				if e.ID == tt.failOn {
					return ErrOutOfStock
				}
				return nil
			}, ListenerID("local"))
			var injector Injector = root
			if tt.nilRoot {
				injector = nil
			}

			err := PublisherFor(injector).Publish(context.Background(), tt.events...)

			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
			if tt.listener {
				var listenerErr *ListenerError
				require.ErrorAs(t, err, &listenerErr)
			}
			assert.Equal(t, tt.ran, ran)
			assert.Len(t, transport.sent(), tt.sends)
		})
	}
}

// eventPublisher is the publishing port an application declares in its
// own layer.
type eventPublisher interface {
	Publish(ctx context.Context, events ...any) error
}

func TestPublisherFor_SatisfiesPort(t *testing.T) {
	t.Parallel()

	root := New()
	calls := countListener[OrderPlaced](t, root)
	var port eventPublisher = PublisherFor(root)

	require.NoError(t, port.Publish(context.Background(), OrderPlaced{ID: "1"}))
	assert.Equal(t, 1, *calls)
}
