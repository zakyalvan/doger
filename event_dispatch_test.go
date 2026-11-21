package doger

import (
	"context"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder records names in order; it is used from one goroutine at a time.
type recorder struct{ names []string }

// listener returns a function listener for E that records name and returns err.
func recordAs[E any](r *recorder, name string, err error) func(context.Context, E) error {
	return func(context.Context, E) error {
		r.names = append(r.names, name)
		return err
	}
}

func TestDispatch_FailFast(t *testing.T) {
	t.Parallel()

	root := New()
	rec := &recorder{}
	Listen(root, recordAs[OrderPlaced](rec, "a", nil), ListenerID("a"))
	Listen(root, recordAs[OrderPlaced](rec, "b", ErrOutOfStock), ListenerID("b"))
	Listen(root, recordAs[OrderPlaced](rec, "c", nil), ListenerID("c"))

	err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

	assert.Equal(t, []string{"a", "b"}, rec.names)
	var listenerErr *ListenerError
	require.ErrorAs(t, err, &listenerErr)
	assert.Equal(t, "b", listenerErr.Listener)
	assert.Equal(t, reflect.TypeFor[OrderPlaced](), listenerErr.EventType)
	require.ErrorIs(t, err, ErrOutOfStock)
	assert.Equal(t, "doger: listener `b` failed on event `doger.OrderPlaced`: out of stock", err.Error())
}

// MyErr is an error type whose nil pointer a listener returns.
type MyErr struct{}

func (e *MyErr) Error() string { return "my error" }

func TestDispatch_TypedNilError(t *testing.T) {
	t.Parallel()

	root := New()
	Listen(root, func(context.Context, OrderPlaced) error {
		var typedNil *MyErr
		return typedNil
	}, ListenerID("a"))

	err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

	var listenerErr *ListenerError
	require.ErrorAs(t, err, &listenerErr)
	assert.Equal(t, "a", listenerErr.Listener)
}

// failingWith returns a function listener for OrderPlaced returning err.
func failingWith(err error) func(context.Context, OrderPlaced) error {
	return func(context.Context, OrderPlaced) error { return err }
}

// listenerIDOf publishes OrderPlaced through i and returns the ID of the
// *ListenerError it fails with.
func listenerIDOf(t *testing.T, i Injector) string {
	t.Helper()

	err := Publish(context.Background(), i, OrderPlaced{ID: "1"})
	var listenerErr *ListenerError
	require.ErrorAs(t, err, &listenerErr)

	return listenerErr.Listener
}

// orderedListener is a listener service implementing Ordered.
type orderedListener struct{ rec *recorder }

func (l *orderedListener) OnEvent(context.Context, OrderPlaced) error {
	l.rec.names = append(l.rec.names, "*Mailer")
	return nil
}

func (*orderedListener) Order() int { return -20 }

func TestDispatch_Order(t *testing.T) {
	t.Parallel()

	root := New()
	rec := &recorder{}
	Listen(root, recordAs[OrderPlaced](rec, "r1", nil), ListenerOrder(0))
	Listen(root, recordAs[OrderPlaced](rec, "r2", nil), ListenerOrder(-1))
	require.NoError(t, Extend(root, &testExtension{name: "x", extend: func(x *ExtensionContext) error {
		Listen(x.Scope(), recordAs[OrderPlaced](rec, "x1", nil), ListenerOrder(0))
		return nil
	}}))
	req := root.Scope("req")
	Listen(req, recordAs[OrderPlaced](rec, "c1", nil))
	Listen(req, recordAs[OrderPlaced](rec, "c2", nil))
	ProvideNamed(req, "*Mailer", func(Injector) (*orderedListener, error) { return &orderedListener{rec: rec}, nil },
		AsListener[OrderPlaced](), WithOrder(-10))

	require.NoError(t, Publish(context.Background(), req, OrderPlaced{ID: "1"}))

	assert.Equal(t, []string{"r2", "c1", "c2", "*Mailer", "r1", "x1"}, rec.names)
}

// countingMailer is a listener service counting its OnEvent calls.
type countingMailer struct{ calls *atomic.Int32 }

func (m *countingMailer) OnEvent(context.Context, OrderPlaced) error {
	m.calls.Add(1)
	return nil
}

func TestDispatch_Filter(t *testing.T) {
	t.Parallel()

	above100 := func(e OrderPlaced) bool { return e.Amount > 100 }
	tests := []struct {
		name     string
		filters  []ListenerOption
		amount   int
		calls    int32
		builds   int32
		panicMsg string
	}{
		{name: "predicate true", filters: []ListenerOption{ListenerFilter(above100)}, amount: 150, calls: 1, builds: 1},
		{name: "predicate false", filters: []ListenerOption{ListenerFilter(above100)}, amount: 100, calls: 0, builds: 0},
		{name: "nil predicate", filters: []ListenerOption{ListenerFilter[OrderPlaced](nil)}, amount: 5, calls: 1, builds: 1},
		{name: "last filter wins", filters: []ListenerOption{
			ListenerFilter(func(OrderPlaced) bool { return false }),
			ListenerFilter(func(OrderPlaced) bool { return true }),
		}, amount: 5, calls: 1, builds: 1},
		{name: "predicate panics", filters: []ListenerOption{ListenerFilter(func(OrderPlaced) bool { panic("bad") })}, amount: 5, calls: 0, builds: 0, panicMsg: "bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			calls, builds := &atomic.Int32{}, &atomic.Int32{}
			ProvideNamed(root, "*Mailer", func(Injector) (*countingMailer, error) {
				builds.Add(1)
				return &countingMailer{calls: calls}, nil
			}, AsListener[OrderPlaced](tt.filters...))

			err := Publish(context.Background(), root, OrderPlaced{Amount: tt.amount})

			if tt.panicMsg != "" {
				var listenerErr *ListenerError
				require.ErrorAs(t, err, &listenerErr)
				assert.Contains(t, err.Error(), tt.panicMsg)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.calls, calls.Load())
			assert.Equal(t, tt.builds, builds.Load())
		})
	}
}

func TestDispatch_FilterTypeMismatch(t *testing.T) {
	t.Parallel()

	root := New()
	assert.PanicsWithError(t, "doger: ListenerFilter for doger.OrderCancelled cannot apply to listener of doger.OrderPlaced", func() {
		ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) { return &Mailer{}, nil },
			AsListener[OrderPlaced](ListenerFilter(func(OrderCancelled) bool { return true })))
	})
	assert.Empty(t, root.ListProvidedServices())
}

func TestDispatch_Reach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		listenerScope string
		publishScope  string
		opts          []PublishOption
		calls         int
	}{
		{name: "req from req", listenerScope: "req", publishScope: "req", calls: 1},
		{name: "tenant-a from req", listenerScope: "tenant-a", publishScope: "req", calls: 1},
		{name: "root from req", listenerScope: "[root]", publishScope: "req", calls: 1},
		{name: "sibling tenant-b from req", listenerScope: "tenant-b", publishScope: "req", calls: 0},
		{name: "descendant req from tenant-a", listenerScope: "req", publishScope: "tenant-a", calls: 0},
		{name: "ext:audit from req", listenerScope: "ext:audit", publishScope: "req", calls: 1},
		{name: "ext:audit from root", listenerScope: "ext:audit", publishScope: "[root]", calls: 1},
		{name: "plain ext:fake from req", listenerScope: "ext:fake", publishScope: "req", calls: 0},
		{name: "root from req with PublishLocal", listenerScope: "[root]", publishScope: "req", opts: []PublishOption{PublishLocal()}, calls: 0},
		{name: "req from req with PublishLocal", listenerScope: "req", publishScope: "req", opts: []PublishOption{PublishLocal()}, calls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			scopes := map[string]Injector{"[root]": root}
			scopes["tenant-a"] = root.Scope("tenant-a")
			scopes["tenant-b"] = root.Scope("tenant-b")
			scopes["req"] = scopes["tenant-a"].Scope("req")
			require.NoError(t, Extend(root, &testExtension{name: "audit", extend: func(x *ExtensionContext) error {
				scopes["ext:audit"] = x.Scope()
				return nil
			}}))
			scopes["ext:fake"] = root.Scope("ext:fake")

			calls := countListener[OrderPlaced](t, scopes[tt.listenerScope])
			require.NoError(t, Publish(context.Background(), scopes[tt.publishScope], OrderPlaced{ID: "1"}, tt.opts...))

			assert.Equal(t, tt.calls, *calls)
		})
	}
}

func TestDispatch_ReachInstallingExtension(t *testing.T) {
	t.Parallel()

	root := New()
	calls := 0
	duringExtend := -1
	require.NoError(t, Extend(root, &testExtension{name: "x", extend: func(x *ExtensionContext) error {
		Listen(x.Scope(), func(context.Context, OrderPlaced) error {
			calls++
			return nil
		})
		err := Publish(context.Background(), root, OrderPlaced{ID: "1"})
		duringExtend = calls
		return err
	}}))

	assert.Equal(t, 0, duringExtend)
	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	assert.Equal(t, 1, calls)
}

// listenerIDs returns the IDs of the *ListenerErrors err holds, in order:
// err itself, or each error it joins.
func listenerIDs(t *testing.T, err error) []string {
	t.Helper()

	ids := []string{}
	if err == nil {
		return ids
	}
	errs := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	}
	for _, e := range errs {
		listenerErr, ok := e.(*ListenerError) //nolint:errorlint // each joined error must be a *ListenerError itself
		require.True(t, ok, "%v is not a *ListenerError", e)
		ids = append(ids, listenerErr.Listener)
	}

	return ids
}

func TestDispatch_ErrorMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mode      EventErrorMode
		failing   []string
		panicking []string
		ran       []string
		named     []string
	}{
		{name: "continue, none failing", mode: EventContinue, ran: []string{"a", "b", "c"}, named: []string{}},
		{name: "continue, b failing", mode: EventContinue, failing: []string{"b"}, ran: []string{"a", "b", "c"}, named: []string{"b"}},
		{name: "continue, a and c failing", mode: EventContinue, failing: []string{"a", "c"}, ran: []string{"a", "b", "c"}, named: []string{"a", "c"}},
		{name: "continue, b panicking", mode: EventContinue, panicking: []string{"b"}, ran: []string{"a", "b", "c"}, named: []string{"b"}},
		{name: "fail fast, a and c failing", mode: EventFailFast, failing: []string{"a", "c"}, ran: []string{"a"}, named: []string{"a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{EventErrorMode: tt.mode})
			rec := &recorder{}
			for _, name := range []string{"a", "b", "c"} {
				Listen(root, func(context.Context, OrderPlaced) error {
					rec.names = append(rec.names, name)
					switch {
					case slices.Contains(tt.failing, name):
						return ErrOutOfStock
					case slices.Contains(tt.panicking, name):
						panic("x")
					}
					return nil
				}, ListenerID(name))
			}

			err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

			assert.Equal(t, tt.ran, rec.names)
			assert.Equal(t, tt.named, listenerIDs(t, err))
		})
	}
}

func TestDispatch_Panic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    any
		matchErr error
		contains string
	}{
		{name: "error value", value: ErrOutOfStock, matchErr: ErrOutOfStock},
		{name: "string value", value: "kaboom", contains: "kaboom"},
		{name: "int value", value: 42, contains: "42"},
		{name: "nil value", value: nil, contains: "nil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			Listen(root, func(context.Context, OrderPlaced) error { panic(tt.value) }, ListenerID("p"))

			err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

			var listenerErr *ListenerError
			require.ErrorAs(t, err, &listenerErr)
			assert.Equal(t, "p", listenerErr.Listener)
			if tt.matchErr != nil {
				require.ErrorIs(t, err, tt.matchErr)
			}
			assert.Contains(t, err.Error(), tt.contains)
		})
	}
}

func TestDispatch_Nested(t *testing.T) {
	t.Parallel()

	root := New()
	rec := &recorder{}
	Listen(root, func(ctx context.Context, _ OrderPlaced) error {
		if err := Publish(ctx, root, InvoiceRequested{ID: "1"}); err != nil {
			return err
		}
		rec.names = append(rec.names, "a")
		return nil
	})
	Listen(root, recordAs[OrderPlaced](rec, "b", nil))
	Listen(root, recordAs[InvoiceRequested](rec, "i", nil))

	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	assert.Equal(t, []string{"i", "a", "b"}, rec.names)
}

func TestDispatch_Context(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		cancelBefore bool
		aCancels     bool
		ran          []string
		matchErr     error
	}{
		{name: "cancelled before Publish", cancelBefore: true, ran: []string{}, matchErr: context.Canceled},
		{name: "cancelled by listener a", aCancels: true, ran: []string{"a"}, matchErr: context.Canceled},
		{name: "never cancelled", ran: []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root := New()
			rec := &recorder{names: []string{}}
			Listen(root, func(context.Context, OrderPlaced) error {
				rec.names = append(rec.names, "a")
				if tt.aCancels {
					cancel()
				}
				return nil
			})
			Listen(root, recordAs[OrderPlaced](rec, "b", nil))
			if tt.cancelBefore {
				cancel()
			}

			err := Publish(ctx, root, OrderPlaced{ID: "1"})

			assert.Equal(t, tt.ran, rec.names)
			if tt.matchErr != nil {
				require.ErrorIs(t, err, tt.matchErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Billing is a service whose provider publishes an event.
type Billing struct{}

func (*Billing) OnEvent(context.Context, OrderPlaced) error { return nil }

func TestPublish_DuringBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		billingListen bool
		mailer        func(root *RootScope, mailer *Mailer)
		matchErr      error
		delivered     bool
	}{
		{name: "*Billing itself listens", billingListen: true, matchErr: ErrCircularDependency},
		{
			name: "*Mailer listens and invokes *Billing",
			mailer: func(root *RootScope, mailer *Mailer) {
				ProvideNamed(root, "*Mailer", func(i Injector) (*Mailer, error) {
					if _, err := InvokeNamed[*Billing](i, "*Billing"); err != nil {
						return nil, err
					}
					return mailer, nil
				}, AsListener[OrderPlaced]())
			},
			matchErr: ErrCircularDependency,
		},
		{
			name: "*Mailer listens with no dependencies",
			mailer: func(root *RootScope, mailer *Mailer) {
				ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) { return mailer, nil }, AsListener[OrderPlaced]())
			},
			delivered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)

			root := New()
			var opts []ProvideOption
			if tt.billingListen {
				opts = append(opts, AsListener[OrderPlaced]())
			}
			ProvideNamed(root, "*Billing", func(i Injector) (*Billing, error) {
				if err := Publish(context.Background(), i, OrderPlaced{ID: "1"}); err != nil {
					return nil, err
				}
				return &Billing{}, nil
			}, opts...)
			mailer := &Mailer{}
			if tt.mailer != nil {
				tt.mailer(root, mailer)
			}

			_, err := InvokeNamed[*Billing](root, "*Billing")

			if tt.matchErr != nil {
				require.ErrorIs(t, err, tt.matchErr)
			} else {
				require.NoError(t, err)
			}
			if tt.delivered {
				assert.Equal(t, []string{"event:1"}, mailer.Received())
			}
			dependencies, _ := root.dag.explainService(root.ID(), root.Name(), "*Billing")
			assert.Empty(t, dependencies)
		})
	}
}
