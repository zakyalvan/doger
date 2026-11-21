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

// Mailer is a listener service fixture: it implements Listener[OrderPlaced]
// and Notifier, and records what it receives.
type Mailer struct {
	mu       sync.Mutex
	received []string
	err      error // returned by OnEvent
}

func (m *Mailer) record(entry string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, entry)
}

// Received returns a copy of what the mailer recorded.
func (m *Mailer) Received() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]string{}, m.received...)
}

func (m *Mailer) OnEvent(_ context.Context, e OrderPlaced) error {
	m.record("event:" + e.ID)
	return m.err
}

func (m *Mailer) OnPlaced(_ context.Context, e OrderPlaced) error {
	m.record("placed:" + e.ID)
	return m.err
}

func (m *Mailer) OnPlacedAudit(_ context.Context, e OrderPlaced) error {
	m.record("placed-audit:" + e.ID)
	return m.err
}

func (m *Mailer) OnCancelled(_ context.Context, e OrderCancelled) error {
	m.record("cancelled:" + e.ID)
	return m.err
}

func (*Mailer) Notify() {}

// Auditor is a service that does not implement Listener[OrderPlaced].
type Auditor struct{}

func (*Auditor) Notify() {}

// Notifier is an interface both Mailer and Auditor implement.
type Notifier interface{ Notify() }

// countingMailerProvider returns a provider of a new *Mailer that counts its
// calls in builds.
func countingMailerProvider(builds *atomic.Int32) Provider[*Mailer] {
	return func(Injector) (*Mailer, error) {
		builds.Add(1)
		return &Mailer{}, nil
	}
}

func TestAsListener_Registration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		register  func(root *RootScope, m *Mailer)
		panicMsg  string
		delivered int
	}{
		{
			name: "*Mailer with AsListener",
			register: func(root *RootScope, m *Mailer) {
				ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) { return m, nil }, AsListener[OrderPlaced]())
			},
			delivered: 1,
		},
		{
			name: "*Mailer with AsListener twice",
			register: func(root *RootScope, m *Mailer) {
				ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) { return m, nil }, AsListener[OrderPlaced](), AsListener[OrderPlaced]())
			},
			delivered: 1,
		},
		{
			name: "*Auditor with AsListener",
			register: func(root *RootScope, _ *Mailer) {
				ProvideNamed(root, "*Auditor", func(Injector) (*Auditor, error) { return &Auditor{}, nil }, AsListener[OrderPlaced]())
			},
			panicMsg: "doger: service `*Auditor` does not implement Listener[doger.OrderPlaced]",
		},
		{
			name: "Notifier with AsListener",
			register: func(root *RootScope, m *Mailer) {
				ProvideNamed(root, "mailer", func(Injector) (Notifier, error) { return m, nil }, AsListener[OrderPlaced]())
			},
			delivered: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			mailer := &Mailer{}
			if tt.panicMsg != "" {
				assert.PanicsWithError(t, tt.panicMsg, func() { tt.register(root, mailer) })
				assert.Empty(t, root.ListProvidedServices())
				return
			}
			tt.register(root, mailer)

			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "2"}))
			assert.Equal(t, []string{"event:1", "event:2"}, mailer.Received(), "%d event per publish", tt.delivered)
		})
	}
}

func TestAsListener_InterfaceCheckedAtDispatch(t *testing.T) {
	t.Parallel()

	root := New()
	ProvideNamed(root, "mailer", func(Injector) (Notifier, error) { return &Auditor{}, nil }, AsListener[OrderPlaced]())

	err := Publish(context.Background(), root, OrderPlaced{ID: "42"})

	var listenerErr *ListenerError
	require.ErrorAs(t, err, &listenerErr)
	assert.Equal(t, "[root]/mailer#github.com/kartaladev/doger.OrderPlaced", listenerErr.Listener)
	require.ErrorIs(t, err, ErrInvalidListener)
}

func TestListener_Build(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		kind   string
		times  int
		builds int32
	}{
		{name: "lazy published 0 times", kind: "lazy", times: 0, builds: 0},
		{name: "lazy published 1 time", kind: "lazy", times: 1, builds: 1},
		{name: "lazy published 3 times", kind: "lazy", times: 3, builds: 1},
		{name: "transient published 3 times", kind: "transient", times: 3, builds: 3},
		{name: "eager published 0 times", kind: "eager", times: 0, builds: 1},
		{name: "eager published 3 times", kind: "eager", times: 3, builds: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			builds := &atomic.Int32{}
			provider := countingMailerProvider(builds)
			switch tt.kind {
			case "lazy":
				ProvideNamed(root, "*Mailer", provider, AsListener[OrderPlaced]())
			case "transient":
				ProvideNamedTransient(root, "*Mailer", provider, AsListener[OrderPlaced]())
			case "eager":
				mailer, err := provider(root)
				require.NoError(t, err)
				ProvideNamedValue(root, "*Mailer", mailer, AsListener[OrderPlaced]())
			}

			for range tt.times {
				require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			}
			assert.Equal(t, tt.builds, builds.Load())
		})
	}
}

func TestListener_ProviderError(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	root := New()
	ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) { return nil, errBoom }, AsListener[OrderPlaced]())

	err := Publish(context.Background(), root, OrderPlaced{ID: "1"})

	var listenerErr *ListenerError
	require.ErrorAs(t, err, &listenerErr)
	assert.Equal(t, "[root]/*Mailer#github.com/kartaladev/doger.OrderPlaced", listenerErr.Listener)
	require.ErrorIs(t, err, errBoom)
}

func TestListenMethod_MultipleEvents(t *testing.T) {
	t.Parallel()

	root := New()
	mailer := &Mailer{}
	builds := &atomic.Int32{}
	ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) {
		builds.Add(1)
		return mailer, nil
	}, ListenMethod((*Mailer).OnPlaced), ListenMethod((*Mailer).OnCancelled))

	require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
	require.NoError(t, Publish(context.Background(), root, OrderCancelled{ID: "1"}))

	assert.Equal(t, []string{"placed:1", "cancelled:1"}, mailer.Received())
	assert.Equal(t, int32(1), builds.Load())
}

func TestListenMethod_Registration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(root *RootScope)
		panicMsg string
	}{
		{
			name: "*Mailer with (*Mailer).OnPlaced",
			register: func(root *RootScope) {
				ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) { return &Mailer{}, nil }, ListenMethod((*Mailer).OnPlaced))
			},
		},
		{
			name: "*Auditor with (*Mailer).OnPlaced",
			register: func(root *RootScope) {
				ProvideNamed(root, "*Auditor", func(Injector) (*Auditor, error) { return &Auditor{}, nil }, ListenMethod((*Mailer).OnPlaced))
			},
			panicMsg: "doger: service `*Auditor` is not of type *doger.Mailer required by ListenMethod",
		},
		{
			name: "*Mailer with a nil method",
			register: func(root *RootScope) {
				var method func(*Mailer, context.Context, OrderPlaced) error
				ProvideNamed(root, "*Mailer", func(Injector) (*Mailer, error) { return &Mailer{}, nil }, ListenMethod(method))
			},
			panicMsg: "doger: ListenMethod requires a non-nil method for service `*Mailer`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			if tt.panicMsg != "" {
				assert.PanicsWithError(t, tt.panicMsg, func() { tt.register(root) })
				assert.Empty(t, root.ListProvidedServices())
				return
			}
			assert.NotPanics(t, func() { tt.register(root) })
		})
	}
}

func TestListen_Stop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stops   int
		calls   int
		results []bool
	}{
		{name: "never stopped", stops: 0, calls: 1, results: []bool{}},
		{name: "stopped once", stops: 1, calls: 0, results: []bool{true}},
		{name: "stopped twice", stops: 2, calls: 0, results: []bool{true, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			calls := 0
			stop := Listen(root, func(context.Context, OrderPlaced) error {
				calls++
				return nil
			})
			results := []bool{}
			for range tt.stops {
				results = append(results, stop())
			}

			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			assert.Equal(t, tt.calls, calls)
			assert.Equal(t, tt.results, results)
		})
	}
}

func TestListenerID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		scope    string
		register func(root *RootScope, scope *Scope)
		id       string
	}{
		{
			name:  "function listener, the 3rd registered in this container",
			scope: "billing",
			register: func(root *RootScope, scope *Scope) {
				Listen(root, func(context.Context, OrderCancelled) error { return nil })
				Listen(root, func(context.Context, OrderCancelled) error { return nil })
				Listen(scope, failingWith(ErrOutOfStock))
			},
			id: "[root]/billing/func#3",
		},
		{
			name:  "function listener with an explicit ID",
			scope: "billing",
			register: func(_ *RootScope, scope *Scope) {
				Listen(scope, failingWith(ErrOutOfStock), ListenerID("mailer"))
			},
			id: "mailer",
		},
		{
			name:  "function listener with an empty ID",
			scope: "billing",
			register: func(_ *RootScope, scope *Scope) {
				Listen(scope, failingWith(ErrOutOfStock), ListenerID(""))
			},
			id: "[root]/billing/func#1",
		},
		{
			name:  "service listener",
			scope: "billing",
			register: func(_ *RootScope, scope *Scope) {
				ProvideNamed(scope, "*Mailer", failingMailer, AsListener[OrderPlaced]())
			},
			id: "[root]/billing/*Mailer#github.com/kartaladev/doger.OrderPlaced",
		},
		{
			name:  "the 2nd ListenMethod of a service",
			scope: "billing",
			register: func(_ *RootScope, scope *Scope) {
				ProvideNamed(scope, "*Mailer", func(Injector) (*Mailer, error) { return &Mailer{}, nil },
					ListenMethod((*Mailer).OnPlaced), ListenMethod(func(m *Mailer, ctx context.Context, e OrderPlaced) error {
						_ = m.OnPlacedAudit(ctx, e)
						return ErrOutOfStock
					}))
			},
			id: "[root]/billing/*Mailer#github.com/kartaladev/doger.OrderPlaced/m2",
		},
		{
			name:  "service listener with an explicit ID",
			scope: "billing",
			register: func(_ *RootScope, scope *Scope) {
				ProvideNamed(scope, "*Mailer", failingMailer, AsListener[OrderPlaced](ListenerID("mailer")))
			},
			id: "mailer",
		},
		{
			name:  "service listener with an empty ID",
			scope: "billing",
			register: func(_ *RootScope, scope *Scope) {
				ProvideNamed(scope, "*Mailer", failingMailer, AsListener[OrderPlaced](ListenerID("")))
			},
			id: "[root]/billing/*Mailer#github.com/kartaladev/doger.OrderPlaced",
		},
		{
			name:  "service listener in an escaped scope",
			scope: "ext:a/b",
			register: func(_ *RootScope, scope *Scope) {
				ProvideNamed(scope, "x", failingMailer, AsListener[OrderPlaced]())
			},
			id: "[root]/ext:a%2Fb/x#github.com/kartaladev/doger.OrderPlaced",
		},
		{
			name:  "function listener in an escaped scope",
			scope: "ext:a/b%c",
			register: func(_ *RootScope, scope *Scope) {
				Listen(scope, failingWith(ErrOutOfStock))
			},
			id: "[root]/ext:a%2Fb%25c/func#1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			scope := root.Scope(tt.scope)
			tt.register(root, scope)

			assert.Equal(t, tt.id, listenerIDOf(t, scope))
		})
	}
}

// failingMailer provides a *Mailer whose listener methods fail.
func failingMailer(Injector) (*Mailer, error) {
	return &Mailer{err: ErrOutOfStock}, nil
}

func TestListen_ClosedScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope func(root *RootScope) Injector // shut down, the scope Listen is called on
	}{
		{name: "a child scope shut down", scope: func(root *RootScope) Injector {
			req := root.Scope("req")
			req.Shutdown()
			return req
		}},
		{name: "a root shut down", scope: func(root *RootScope) Injector {
			root.Shutdown()
			return root
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			defer root.Shutdown()
			scope := tt.scope(root)

			recovered := recoverPanic(func() {
				Listen(scope, func(context.Context, OrderPlaced) error { return nil })
			})

			err, ok := recovered.(error)
			require.True(t, ok, "Listen on a closed scope must panic with an error, got %v", recovered)
			require.ErrorIs(t, err, ErrPublishAfterShutdown)
		})
	}
}
