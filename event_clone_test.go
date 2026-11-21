package doger

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClone_Listeners(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		onClone       bool
		ran           []string
		ownInstance   bool // the clone built an instance of its own
		originalEvent bool // the original's instance received the event
	}{
		{name: "publish on the clone", onClone: true, ran: []string{"f", "g"}, ownInstance: true},
		{name: "publish on the original", onClone: false, ran: []string{}, originalEvent: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			var mu sync.Mutex
			instances := []*Mailer{}
			ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) {
				mu.Lock()
				defer mu.Unlock()
				m := &Mailer{}
				instances = append(instances, m)
				return m, nil
			}, AsListener[OrderPlaced]())
			original, err := InvokeNamed[*Mailer](root, "*Mailer")
			require.NoError(t, err)
			rec := &syncRecorder{}
			stopF := Listen(root, func(context.Context, OrderPlaced) error { rec.add("f"); return nil })

			clone := root.Clone()
			Listen(clone, func(context.Context, OrderPlaced) error { rec.add("g"); return nil })
			require.True(t, stopF())

			target := root
			if tt.onClone {
				target = clone
			}
			require.NoError(t, Publish(context.Background(), target, OrderPlaced{ID: "1"}))

			assert.Equal(t, tt.ran, append([]string{}, rec.list()...))
			mu.Lock()
			defer mu.Unlock()
			if tt.ownInstance {
				require.Len(t, instances, 2)
				assert.Equal(t, []string{"event:1"}, instances[1].Received())
				assert.Empty(t, original.Received())
			}
			if tt.originalEvent {
				assert.Equal(t, []string{"event:1"}, original.Received())
			}
		})
	}
}

func TestClone_ExecutorOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		executor *inlineExecutor
	}{
		{name: "the default executor"},
		{name: "InjectorOpts.EventExecutor set", executor: &inlineExecutor{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			opts := &InjectorOpts{}
			if tt.executor != nil {
				opts.EventExecutor = tt.executor
			}
			root := NewWithOpts(opts)
			delivered := make(chan struct{}, 1)
			Listen(root, func(context.Context, OrderPlaced) error {
				delivered <- struct{}{}
				return nil
			}, ListenerAsync())

			clone := root.Clone()
			defer clone.Shutdown()
			root.Shutdown()

			require.NoError(t, Publish(context.Background(), clone, OrderPlaced{ID: "1"}))
			select {
			case <-delivered:
			case <-time.After(time.Second):
				t.Fatal("the async publish on the clone was not delivered within 1s")
			}
			if tt.executor != nil {
				assert.Equal(t, int32(1), tt.executor.shutdowns.Load())
				assert.Equal(t, int32(1), tt.executor.submits.Load())
			}
		})
	}
}

func TestClone_ShutdownLeavesSuppliedExecutor(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	executor := NewBoundedEventExecutor(2)
	root := NewWithOpts(&InjectorOpts{EventExecutor: executor})
	defer root.Shutdown()
	delivered := make(chan struct{}, 1)
	Listen(root, func(context.Context, OrderPlaced) error {
		delivered <- struct{}{}
		return nil
	}, ListenerAsync())

	clone := root.Clone()
	clone.Shutdown()

	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("the async publish on the source was not delivered within 1s")
	}
}
