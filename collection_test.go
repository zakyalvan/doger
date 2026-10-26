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
			handlers, err := c.All(nil)
			require.NoError(t, err)
			assert.Equal(t, []string{"console", "audit"}, handlerNames(handlers))
		}},
		{name: "Map", check: func(t *testing.T, c Collection[EventHandler]) {
			t.Helper()
			handlers, err := c.Map(nil)
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

	_, err := c.All(nil)
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

	peers, err := bus.peers.All(nil)

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
			handlers, err := c.All(nil)
			return handlerNames(handlers), err
		}},
		{name: "Map", read: func(c Collection[EventHandler]) ([]string, error) {
			handlers, err := c.Map(nil)
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
		if _, err := bus.peers.All(nil); err != nil {
			return nil, err
		}
		return newConsole("app"), nil
	})
	ProvideNamed(root, "bus", func(i Injector) (*busHandler, error) {
		return &busHandler{handlerBase: handlerBase{"bus"}, peers: InvokeCollection[EventHandler](i)}, nil
	})

	_, err := InvokeNamed[*ConsoleHandler](root, "app")

	assert.ErrorIs(t, err, ErrCircularDependency)
	// the holder "bus" sits between the running "app" and the member "app"
	assert.ErrorContains(t, err, "`app` -> `bus` -> `app`")
}

func TestCollection_DetachedReadRecordsNoEdge(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	// app is not an EventHandler, so the read succeeds while it runs
	ProvideNamed(root, "app", func(i Injector) (*Bus, error) {
		bus, err := InvokeNamed[*busHandler](i, "bus")
		if err != nil {
			return nil, err
		}
		handlers, err := bus.peers.All(nil)
		if err != nil {
			return nil, err
		}
		return &Bus{names: handlerNames(handlers)}, nil
	})
	ProvideNamed(root, "bus", func(i Injector) (*busHandler, error) {
		return &busHandler{handlerBase: handlerBase{"bus"}, peers: InvokeCollection[EventHandler](i)}, nil
	})
	provideHandler(root, "console", newConsole)

	app, err := InvokeNamed[*Bus](root, "app")

	require.NoError(t, err)
	assert.Equal(t, []string{"console"}, app.names)
	assert.Equal(t, []string{"bus"}, explainedNames(t, root, "app", false))
}

// appMarker is implemented by appService only.
type (
	appMarker   interface{ appMarker() }
	appService  struct{}
	appsHandler struct {
		handlerBase
		apps Collection[appMarker]
	}
)

func (*appService) appMarker() {}

func TestCollection_DetachedReadKeepsRunningChain(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	ProvideNamed(root, "app", func(i Injector) (*appService, error) {
		bus, err := InvokeNamed[*busHandler](i, "bus")
		if err != nil {
			return nil, err
		}
		// builds "member" through a detached read, since bus returned
		if _, err := bus.peers.All(nil); err != nil {
			return nil, err
		}
		member, err := InvokeNamed[*appsHandler](i, "member")
		if err != nil {
			return nil, err
		}
		// app is still running: reading it must fail, not wait for itself
		if _, err := member.apps.All(nil); err != nil {
			return nil, err
		}
		return &appService{}, nil
	})
	ProvideNamed(root, "bus", func(i Injector) (*busHandler, error) {
		return &busHandler{handlerBase: handlerBase{"bus"}, peers: InvokeCollection[EventHandler](i)}, nil
	})
	ProvideNamed(root, "member", func(i Injector) (*appsHandler, error) {
		return &appsHandler{handlerBase: handlerBase{"member"}, apps: InvokeCollection[appMarker](i)}, nil
	})

	_, err := InvokeNamed[*appService](root, "app")

	assert.ErrorIs(t, err, ErrCircularDependency)
}

func TestCollection_HolderAfterSiblings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		invoke func(i Injector, name string) error
	}{
		{name: "untyped, as InvokeStruct", invoke: func(i Injector, name string) error {
			_, err := invokeAnyByName(i, name)
			return err
		}},
		{name: "typed, as InvokeNamed", invoke: func(i Injector, name string) error {
			if name == "d" {
				_, err := InvokeNamed[*busHandler](i, name)
				return err
			}
			_, err := InvokeNamed[*ConsoleHandler](i, name)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			// a -> b -> c, then c invokes d and e: at this depth the invoker
			// chain has spare capacity, which e must not write into d's chain
			root := New()
			chain := func(name string, next ...string) {
				ProvideNamed(root, name, func(i Injector) (*ConsoleHandler, error) {
					for _, n := range next {
						if err := tt.invoke(i, n); err != nil {
							return nil, err
						}
					}
					return newConsole(name), nil
				})
			}
			chain("a", "b")
			chain("b", "c")
			chain("c", "d", "e")
			chain("e")
			ProvideNamed(root, "d", func(i Injector) (*busHandler, error) {
				return &busHandler{handlerBase: handlerBase{"d"}, peers: InvokeCollection[EventHandler](i)}, nil
			})
			_, err := InvokeNamed[*ConsoleHandler](root, "a")
			require.NoError(t, err)
			d, err := InvokeNamed[*busHandler](root, "d")
			require.NoError(t, err)

			peers, err := d.peers.Map(nil)

			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"a", "b", "c", "e"}, slices.Collect(maps.Keys(peers)))
		})
	}
}

func TestCollection_InvocationsWithoutProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, root *RootScope) string
	}{
		{name: "eager value resolving its DependsOn", setup: func(t *testing.T, root *RootScope) string {
			t.Helper()
			ProvideNamedValue(root, "e", newConsole("e"), DependsOnNamed("bus"))
			_, err := InvokeNamed[*ConsoleHandler](root, "e")
			require.NoError(t, err)
			return "e"
		}},
		{name: "eager value invoked through a struct tag", setup: func(t *testing.T, root *RootScope) string {
			t.Helper()
			ProvideNamedValue(root, "e", newConsole("e"), DependsOnNamed("bus"))
			_, err := InvokeStruct[struct {
				E *ConsoleHandler `do:"e"`
			}](root)
			require.NoError(t, err)
			return "e"
		}},
		{name: "eager value invoked by type", setup: func(t *testing.T, root *RootScope) string {
			t.Helper()
			ProvideNamedValue(root, "e", newConsole("e"), DependsOnNamed("bus"))
			_, err := InvokeAs[*ConsoleHandler](root)
			require.NoError(t, err)
			return "e"
		}},
		{name: "lazy attempt failing before its provider", setup: func(t *testing.T, root *RootScope) string {
			t.Helper()
			ProvideNamed(root, "l", func(Injector) (*ConsoleHandler, error) { return newConsole("l"), nil },
				DependsOnNamed("bus"), DependsOnNamed("missing"))
			_, err := InvokeNamed[*ConsoleHandler](root, "l")
			require.ErrorIs(t, err, ErrServiceNotFound)
			ProvideNamedValue(root, "missing", 42)
			_, err = InvokeNamed[*ConsoleHandler](root, "l")
			require.NoError(t, err)
			return "l"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			ProvideNamed(root, "bus", func(i Injector) (*busHandler, error) {
				return &busHandler{handlerBase: handlerBase{"bus"}, peers: InvokeCollection[EventHandler](i)}, nil
			})
			dependent := tt.setup(t, root)
			bus, err := InvokeNamed[*busHandler](root, "bus")
			require.NoError(t, err)

			peers, err := bus.peers.Map(nil)

			require.NoError(t, err)
			assert.Equal(t, []string{dependent}, slices.Collect(maps.Keys(peers)))
		})
	}
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

			handlers, err := c.All(nil)

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
	results := make(chan []string, 50)
	errs := make(chan error, 50)
	for range 50 {
		wg.Go(func() {
			handlers, err := c.All(nil)
			results <- handlerNames(handlers)
			errs <- err
		})
	}
	for i := range 10 {
		wg.Go(func() { provideHandler(root, fmt.Sprintf("h-%d", i), newAudit) })
	}
	wg.Wait()
	close(results)
	close(errs)

	assert.Equal(t, int32(1), calls.Load())
	for err := range errs {
		assert.NoError(t, err)
	}
	for names := range results {
		// console first, then whichever late handlers were registered already
		require.NotEmpty(t, names)
		assert.Equal(t, "console", names[0])
		assert.LessOrEqual(t, len(names), 11)
	}
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
		handlers, err := peers.All(nil)
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

	handlers, err := c.All(nil)

	require.NoError(t, err)
	assert.Equal(t, []string{"console"}, handlerNames(handlers))
}
