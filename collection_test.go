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
			ProvideNamed(root, "missing", func(Injector) (int, error) { return 42, nil })
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
		handlers, err := peers.All(i)
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

// provideBus registers lazy "bus", an EventHandler holding a live collection
// of its peers, in scope.
func provideBus(scope Injector) {
	ProvideNamed(scope, "bus", func(i Injector) (*busHandler, error) {
		return &busHandler{handlerBase: handlerBase{"bus"}, peers: InvokeCollection[EventHandler](i)}, nil
	})
}

// namesHandler is an EventHandler keeping the member names it read.
type namesHandler struct {
	handlerBase
	names []string
}

func TestCollection_ReaderInjector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler bool // "app" implements EventHandler
	}{
		{name: "app is not an EventHandler"},
		{name: "app implements EventHandler", handler: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			provideHandler(root, "console", newConsole)
			provideBus(root)
			read := func(i Injector) ([]string, error) {
				bus, err := InvokeNamed[*busHandler](i, "bus")
				if err != nil {
					return nil, err
				}
				// the provider reads with its own injector
				handlers, err := bus.peers.All(i)
				return handlerNames(handlers), err
			}
			if tt.handler {
				ProvideNamed(root, "app", func(i Injector) (*namesHandler, error) {
					names, err := read(i)
					return &namesHandler{handlerBase: handlerBase{"app"}, names: names}, err
				})
			} else {
				ProvideNamed(root, "app", func(i Injector) (*Bus, error) {
					names, err := read(i)
					return &Bus{names: names}, err
				})
			}

			var kept []string
			var err error
			if tt.handler {
				var app *namesHandler
				app, err = InvokeNamed[*namesHandler](root, "app")
				if app != nil {
					kept = app.names
				}
			} else {
				var app *Bus
				app, err = InvokeNamed[*Bus](root, "app")
				if app != nil {
					kept = app.names
				}
			}

			is.NoError(err)
			is.Equal([]string{"console"}, kept)
			is.Equal([]string{"bus", "console"}, explainedNames(t, root, "app", false))
		})
	}
}

func TestCollection_ReadWithNilRecordsNoEdge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reader func(root *RootScope) Injector
	}{
		{name: "nil", reader: func(*RootScope) Injector { return nil }},
		{name: "the root, not a provider's injector", reader: func(root *RootScope) Injector { return root }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideHandler(root, "console", newConsole)
			provideBus(root)
			ProvideNamed(root, "app", func(i Injector) (*Bus, error) {
				bus, err := InvokeNamed[*busHandler](i, "bus")
				if err != nil {
					return nil, err
				}
				handlers, err := bus.peers.All(tt.reader(root))
				return &Bus{names: handlerNames(handlers)}, err
			})

			app, err := InvokeNamed[*Bus](root, "app")

			require.NoError(t, err)
			assert.Equal(t, []string{"console"}, app.names)
			assert.Equal(t, []string{"bus"}, explainedNames(t, root, "app", false))
			assert.Empty(t, explainedNames(t, root, "bus", false), "the holder records no edge either")
		})
	}
}

func TestCollection_ReaderExcludedByName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		own     bool // the child "app" reads with its own injector, not nil
		members []string
	}{
		{name: "own injector", own: true, members: []string{"console"}},
		{name: "nil", members: []string{"app", "console"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			provideBus(root)
			provideHandler(root, "app", newConsole)
			provideHandler(root, "console", newConsole)
			child := root.Scope("child")
			// the child "app" shadows the root "app" and reads the root's collection
			ProvideNamed(child, "app", func(i Injector) (*Bus, error) {
				bus, err := InvokeNamed[*busHandler](i, "bus")
				if err != nil {
					return nil, err
				}
				var reader Injector
				if tt.own {
					reader = i
				}
				handlers, err := bus.peers.Map(reader)
				return &Bus{names: slices.Sorted(maps.Keys(handlers))}, err
			})

			app, err := InvokeNamed[*Bus](child, "app")

			// the reader's name excludes the root "app" it shadows
			require.NoError(t, err)
			assert.Equal(t, tt.members, app.names)
		})
	}
}

func TestCollection_GoroutineReadDuringOuterProvider(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)
	is := assert.New(t)

	root := New()
	provideBus(root)
	busBuilt, release := make(chan *busHandler, 1), make(chan struct{})
	ProvideNamed(root, "app", func(i Injector) (*ConsoleHandler, error) {
		bus, err := InvokeNamed[*busHandler](i, "bus")
		if err != nil {
			return nil, err
		}
		busBuilt <- bus
		<-release
		return newConsole("app"), nil
	})
	appErr := make(chan error, 1)
	go func() {
		_, err := InvokeNamed[*ConsoleHandler](root, "app")
		appErr <- err
	}()
	bus := <-busBuilt

	// a goroutine outside the build chain reads while "app" runs
	type result struct {
		names []string
		err   error
	}
	read := make(chan result, 1)
	go func() {
		handlers, err := bus.peers.All(nil)
		read <- result{names: handlerNames(handlers), err: err}
	}()
	var got result
	select {
	case got = <-read: // returned while "app" was still running
		close(release)
	case <-time.After(100 * time.Millisecond):
		close(release)
		got = <-read
	}

	is.NoError(<-appErr)
	is.NoError(got.err)
	is.Equal([]string{"app"}, got.names)
}

func TestCollection_MemberProviderReads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		helper  bool // console reads through lazy "helper" instead of itself
		wantErr error
		kept    []string
	}{
		{name: "the member reads with its own injector", kept: []string{"audit"}},
		{name: "a provider deeper in the member's chain reads", helper: true, wantErr: ErrCircularDependency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			root := New()
			provideBus(root)
			provideHandler(root, "audit", newAudit)
			bus, err := InvokeNamed[*busHandler](root, "bus")
			require.NoError(t, err)
			readPeers := func(i Injector) ([]string, error) {
				handlers, err := bus.peers.All(i)
				return handlerNames(handlers), err
			}
			ProvideNamed(root, "helper", func(i Injector) (*Bus, error) {
				names, err := readPeers(i)
				return &Bus{names: names}, err
			})
			ProvideNamed(root, "console", func(i Injector) (*namesHandler, error) {
				if tt.helper {
					helper, err := InvokeNamed[*Bus](i, "helper")
					if err != nil {
						return nil, err
					}
					return &namesHandler{handlerBase: handlerBase{"console"}, names: helper.names}, nil
				}
				names, err := readPeers(i)
				return &namesHandler{handlerBase: handlerBase{"console"}, names: names}, err
			})

			console, err := InvokeNamed[*namesHandler](root, "console")

			if tt.wantErr != nil {
				is.ErrorIs(err, tt.wantErr)
				is.ErrorContains(err, "`console`")
				return
			}
			require.NoError(t, err)
			is.Equal(tt.kept, console.names)
		})
	}
}

func TestCollection_DefaultReplacedByType(t *testing.T) {
	t.Parallel()
	testWithTimeout(t, time.Second)

	root := New()
	provideEnvSourceAs[dfPropertySource](root)
	c := InvokeCollection[dfPropertySource](root)
	provideMySource(root, "mySource")

	sources, err := c.All(nil)

	require.NoError(t, err)
	assert.Equal(t, []string{"mySource"}, sourceNames(sources))
}
