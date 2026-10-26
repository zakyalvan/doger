package doger

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollection_LaterRegistrations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, c Collection[EventHandler])
	}{
		{name: "All", check: func(t *testing.T, c Collection[EventHandler]) {
			t.Helper()
			handlers, err := c.All()
			require.NoError(t, err)
			assert.Equal(t, []string{"console", "audit"}, handlerNames(handlers))
		}},
		{name: "Map", check: func(t *testing.T, c Collection[EventHandler]) {
			t.Helper()
			handlers, err := c.Map()
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"console", "audit"}, slices.Collect(maps.Keys(handlers)))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideHandler(root, "console", newConsole)
			c := InvokeCollection[EventHandler](root)
			provideHandler(root, "audit", newAudit)

			tt.check(t, c)
		})
	}
}

func TestCollection_CreationBuildsNothing(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	var calls atomic.Int32
	ProvideNamed(root, "console", func(Injector) (*ConsoleHandler, error) { calls.Add(1); return newConsole("console"), nil })

	c := InvokeCollection[EventHandler](root)
	assert.Equal(t, int32(0), calls.Load())

	_, err := c.All()
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

// busHandler is an EventHandler holding a live collection of its peers.
type busHandler struct {
	handlerBase
	peers Collection[EventHandler]
}

func TestCollection_HeldByProvider(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	ProvideNamed(root, "bus", func(i Injector) (*busHandler, error) {
		return &busHandler{handlerBase: handlerBase{"bus"}, peers: InvokeCollection[EventHandler](i)}, nil
	})
	provideHandler(root, "console", newConsole)
	bus, err := InvokeNamed[*busHandler](root, "bus")
	require.NoError(t, err)
	provideHandler(root, "audit", newAudit)

	peers, err := bus.peers.All()

	require.NoError(t, err)
	assert.Equal(t, []string{"console", "audit"}, handlerNames(peers))
	assert.Equal(t, []string{"audit", "console"}, explainedNames(t, root, "bus", false))
}

func TestCollection_ReadOutcomes(t *testing.T) {
	t.Parallel()

	errBuild := errors.New("build failed")

	tests := []struct {
		name  string
		setup func(root *RootScope)
		check func(t *testing.T, handlers []EventHandler, err error)
	}{
		{
			name: "after shutdown",
			setup: func(root *RootScope) {
				provideHandler(root, "console", newConsole)
				root.Shutdown()
			},
			check: func(t *testing.T, handlers []EventHandler, err error) {
				t.Helper()
				assert.NoError(t, err)
				assert.NotNil(t, handlers)
				assert.Empty(t, handlers)
			},
		},
		{
			name: "failing member",
			setup: func(root *RootScope) {
				ProvideNamed(root, "console", func(Injector) (*ConsoleHandler, error) { return nil, errBuild })
			},
			check: func(t *testing.T, handlers []EventHandler, err error) {
				t.Helper()
				assert.Nil(t, handlers)
				assert.ErrorIs(t, err, errBuild)
				assert.ErrorContains(t, err, "console")
			},
		},
		{
			name:  "no member",
			setup: func(*RootScope) {},
			check: func(t *testing.T, handlers []EventHandler, err error) {
				t.Helper()
				assert.NoError(t, err)
				assert.NotNil(t, handlers)
				assert.Empty(t, handlers)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			tt.setup(root)
			c := InvokeCollection[EventHandler](root)

			handlers, err := c.All()

			tt.check(t, handlers, err)
		})
	}
}

func TestCollection_ConcurrentReads(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, 5*time.Second)

	root := New()
	var calls atomic.Int32
	ProvideNamed(root, "console", func(Injector) (*ConsoleHandler, error) { calls.Add(1); return newConsole("console"), nil })
	c := InvokeCollection[EventHandler](root)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { _, _ = c.All() })
	}
	for i := range 10 {
		wg.Go(func() { provideHandler(root, fmt.Sprintf("h-%d", i), newAudit) })
	}
	wg.Wait()

	assert.Equal(t, int32(1), calls.Load())
}
