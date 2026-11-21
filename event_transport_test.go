package doger

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ErrNoBroker is the error of the transport providers that fail.
var ErrNoBroker = errors.New("no broker")

// immediateOrders registers OrderPlaced as "orders.placed", externalized
// immediately, on root.
func immediateOrders(t *testing.T, root *RootScope) {
	t.Helper()
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)))
}

func TestTransportResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func() (*RootScope, func(t *testing.T, err error))
	}{
		{name: "opts", build: func() (*RootScope, func(*testing.T, error)) {
			a := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventTransport: a})
			return root, func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Len(t, a.sent(), 1)
			}
		}},
		{name: "service", build: func() (*RootScope, func(*testing.T, error)) {
			b := &recordingTransport{}
			root := New()
			ProvideNamedValue[EventTransport](root, NameOf[EventTransport](), b)
			return root, func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Len(t, b.sent(), 1)
			}
		}},
		{name: "opts_over_service", build: func() (*RootScope, func(*testing.T, error)) {
			a, b := &recordingTransport{}, &recordingTransport{}
			calls := 0
			root := NewWithOpts(&InjectorOpts{EventTransport: a})
			ProvideNamed(root, NameOf[EventTransport](), func(Injector) (EventTransport, error) {
				calls++
				return b, nil
			})
			return root, func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Len(t, a.sent(), 1)
				assert.Empty(t, b.sent())
				assert.Zero(t, calls)
			}
		}},
		{name: "application_over_default", build: func() (*RootScope, func(*testing.T, error)) {
			b, c := &recordingTransport{}, &recordingTransport{}
			root := New()
			ProvideNamedValue[EventTransport](root, NameOf[EventTransport](), c, AsDefault())
			ProvideNamedValue[EventTransport](root, NameOf[EventTransport](), b)
			return root, func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Len(t, b.sent(), 1)
				assert.Empty(t, c.sent())
			}
		}},
		{name: "failing_provider", build: func() (*RootScope, func(*testing.T, error)) {
			root := New()
			ProvideNamed(root, NameOf[EventTransport](), func(Injector) (EventTransport, error) {
				return nil, ErrNoBroker
			})
			return root, func(t *testing.T, err error) {
				t.Helper()
				var externalizeErr *ExternalizeError
				require.ErrorAs(t, err, &externalizeErr)
				require.ErrorIs(t, err, ErrNoBroker)
			}
		}},
		{name: "none", build: func() (*RootScope, func(*testing.T, error)) {
			return New(), func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, check := tt.build()
			immediateOrders(t, root)

			check(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
		})
	}
}

func TestTransportResolution_Once(t *testing.T) {
	t.Parallel()

	root := New()
	transport := &recordingTransport{}
	var calls atomic.Int32
	ProvideNamed(root, NameOf[EventTransport](), func(Injector) (EventTransport, error) {
		calls.Add(1)
		return transport, nil
	})
	immediateOrders(t, root)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			assert.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
		})
	}
	wg.Wait()

	assert.Equal(t, int32(1), calls.Load())
	assert.Len(t, transport.sent(), 20)
}

func TestTransportResolution_FailingProviderFailsStart(t *testing.T) {
	t.Parallel()

	root := New()
	t.Cleanup(func() { root.Shutdown() })
	ProvideNamed(root, NameOf[EventTransport](), func(Injector) (EventTransport, error) {
		return nil, ErrNoBroker
	})
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()))

	require.ErrorIs(t, root.Start(), ErrNoBroker)
}
