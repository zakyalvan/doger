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
	assert.Empty(t, explainedNames(t, root, "bus", false))
}

func TestCollection_AfterProviderReturned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		read func(c Collection[EventHandler]) ([]string, error)
	}{
		{name: "All", read: func(c Collection[EventHandler]) ([]string, error) {
			handlers, err := c.All()
			return handlerNames(handlers), err
		}},
		{name: "Map", read: func(c Collection[EventHandler]) ([]string, error) {
			handlers, err := c.Map()
			return slices.Collect(maps.Keys(handlers)), err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			ProvideNamed(root, "app", func(i Injector) (*ConsoleHandler, error) {
				if _, err := InvokeNamed[*busHandler](i, "bus"); err != nil {
					return nil, err
				}
				return newConsole("app"), nil
			})
			ProvideNamed(root, "bus", func(i Injector) (*busHandler, error) {
				return &busHandler{handlerBase: handlerBase{"bus"}, peers: InvokeCollection[EventHandler](i)}, nil
			})
			_, err := InvokeNamed[*ConsoleHandler](root, "app")
			require.NoError(t, err)
			bus, err := InvokeNamed[*busHandler](root, "bus")
			require.NoError(t, err)

			names, err := tt.read(bus.peers)

			is.NoError(err)
			is.Equal([]string{"app"}, names)
			is.NotContains(explainedNames(t, root, "bus", false), "app")
		})
	}
}

func TestCollection_ReadDuringOuterProvider(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	ProvideNamed(root, "app", func(i Injector) (*ConsoleHandler, error) {
		bus, err := InvokeNamed[*busHandler](i, "bus")
		if err != nil {
			return nil, err
		}
		if _, err := bus.peers.All(); err != nil {
			return nil, err
		}
		return newConsole("app"), nil
	})
	ProvideNamed(root, "bus", func(i Injector) (*busHandler, error) {
		return &busHandler{handlerBase: handlerBase{"bus"}, peers: InvokeCollection[EventHandler](i)}, nil
	})

	_, err := InvokeNamed[*ConsoleHandler](root, "app")

	assert.ErrorIs(t, err, ErrCircularDependency)
	assert.ErrorContains(t, err, "app")
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
			c := InvokeCollection[EventHandler](root) // live: the reads cross the setup
			tt.setup(root)

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

func TestCollection_ReadDuringHolderProvider(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	provideHandler(root, "console", newConsole)
	var read []string
	ProvideNamed(root, "bus", func(i Injector) (*busHandler, error) {
		peers := InvokeCollection[EventHandler](i)
		handlers, err := peers.All()
		read = handlerNames(handlers)
		return &busHandler{handlerBase: handlerBase{"bus"}, peers: peers}, err
	})

	_, err := InvokeNamed[*busHandler](root, "bus")

	require.NoError(t, err)
	is.Equal([]string{"console"}, read)
	is.Equal([]string{"console"}, explainedNames(t, root, "bus", false))
}

// TestCollection_NilInjector is not parallel: it replaces DefaultRootScope.
func TestCollection_NilInjector(t *testing.T) {
	testWithTimeout(t, time.Second)

	previous := DefaultRootScope
	DefaultRootScope = New()
	t.Cleanup(func() { DefaultRootScope = previous })
	c := InvokeCollection[EventHandler](nil)
	provideHandler(DefaultRootScope, "console", newConsole)

	handlers, err := c.All()

	require.NoError(t, err)
	assert.Equal(t, []string{"console"}, handlerNames(handlers))
}
