package doger

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemote_NotLocal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		typ      []EventTypeOption
		delivery []ListenerOption
		ran      int32
		inbound  bool // the run comes through the transport
	}{
		{name: "local/no_option", ran: 1},
		{name: "local/deliver_local", delivery: []ListenerOption{ListenerDelivery(DeliverLocal)}, ran: 1},
		{name: "not_externalized/deliver_once", delivery: []ListenerOption{ListenerDelivery(DeliverOnce)}},
		{name: "externalized/deliver_once", typ: []EventTypeOption{EventExternalized()}, delivery: []ListenerOption{ListenerDelivery(DeliverOnce)}, ran: 1, inbound: true},
		{name: "externalized/deliver_all", typ: []EventTypeOption{EventExternalized()}, delivery: []ListenerOption{ListenerDelivery(DeliverAll)}, ran: 1, inbound: true},
		{name: "externalized/local", typ: []EventTypeOption{EventExternalized()}, ran: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := NewWithOpts(&InjectorOpts{EventTransport: &loopbackTransport{}})
			t.Cleanup(func() { root.Shutdown() })
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", tt.typ...))
			var ran atomic.Int32
			Listen(root, func(context.Context, OrderPlaced) error {
				ran.Add(1)
				return nil
			}, append([]ListenerOption{ListenerID("r")}, tt.delivery...)...)
			require.NoError(t, root.Start())

			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))
			assert.Equal(t, tt.ran, ran.Load())
		})
	}
}

// noopOrderListener is a function listener for OrderPlaced that does
// nothing.
func noopOrderListener(context.Context, OrderPlaced) error { return nil }

func TestRemote_RegistrationPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		register func(root *RootScope)
		message  string
	}{
		{name: "child_scope", register: func(r *RootScope) {
			Listen(r.Scope("req"), noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
		}, message: `doger: remote listener "r" must be registered in the root or an extension scope`},
		{name: "no_id", register: func(r *RootScope) {
			Listen(r, noopOrderListener, ListenerDelivery(DeliverOnce))
		}, message: "doger: remote function listener needs a ListenerID"},
		{name: "async", register: func(r *RootScope) {
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverAll), ListenerAsync())
		}, message: `doger: remote listener "r" cannot be async`},
		{name: "tx_phase", register: func(r *RootScope) {
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce), ListenerTxPhase(TxPhaseAfterCommit))
		}, message: `doger: remote listener "r" cannot have a transaction phase`},
		{name: "after_start", register: func(r *RootScope) {
			if err := r.Start(); err != nil {
				panic(err)
			}
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
		}, message: `doger: remote listener "r" must be registered before Start`},
		{name: "invalid_delivery", register: func(r *RootScope) {
			Listen(r, noopOrderListener, ListenerDelivery(EventDelivery(9)))
		}, message: "doger: invalid EventDelivery 9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			t.Cleanup(func() { root.Shutdown() })

			assert.PanicsWithError(t, tt.message, func() { tt.register(root) })
		})
	}
}

func TestRemote_RegistrationOK(t *testing.T) {
	t.Parallel()

	big := func(e OrderPlaced) bool { return e.Amount > 100 }
	tests := []struct {
		name     string
		register func(root *RootScope, billing *Scope)
	}{
		{name: "function", register: func(r *RootScope, _ *Scope) {
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
		}},
		{name: "listen_method", register: func(r *RootScope, _ *Scope) {
			Provide(r, func(Injector) (*Mailer, error) { return &Mailer{}, nil }, ListenMethod((*Mailer).OnPlaced, ListenerDelivery(DeliverAll)))
		}},
		{name: "as_listener", register: func(r *RootScope, _ *Scope) {
			Provide(r, func(Injector) (*Mailer, error) { return &Mailer{}, nil }, AsListener[OrderPlaced](ListenerDelivery(DeliverOnce), ListenerFilter(big)))
		}},
		{name: "extension_scope", register: func(_ *RootScope, billing *Scope) {
			Listen(billing, noopOrderListener, ListenerID("b"), ListenerDelivery(DeliverOnce))
		}},
		{name: "immediate_phase", register: func(r *RootScope, _ *Scope) {
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce), ListenerTxPhase(TxPhaseImmediate))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := New()
			var billing *Scope
			require.NoError(t, Extend(root, &testExtension{name: "billing", extend: func(x *ExtensionContext) error {
				billing = x.Scope()
				return nil
			}}))

			assert.NotPanics(t, func() { tt.register(root, billing) })
		})
	}
}

func TestEventDelivery_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value EventDelivery
		want  string
	}{
		{value: DeliverLocal, want: "DeliverLocal"},
		{value: DeliverOnce, want: "DeliverOnce"},
		{value: DeliverAll, want: "DeliverAll"},
		{value: EventDelivery(9), want: "EventDelivery(9)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.value.String())
		})
	}
}

func TestRemote_StartValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		transport bool
		setup     func(t *testing.T, root *RootScope)
		err       error
	}{
		{name: "no_transport", setup: func(t *testing.T, r *RootScope) {
			t.Helper()
			require.NoError(t, RegisterEventType[OrderPlaced](r, "orders.placed"))
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
		}, err: ErrNoEventTransport},
		{name: "unregistered_type", transport: true, setup: func(t *testing.T, r *RootScope) {
			t.Helper()
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
		}, err: ErrEventTypeNotFound},
		{name: "group_conflict", transport: true, setup: func(t *testing.T, r *RootScope) {
			t.Helper()
			require.NoError(t, RegisterEventType[OrderPlaced](r, "orders.placed"))
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
		}, err: ErrListenerGroupConflict},
		{name: "same_group_other_destination", transport: true, setup: func(t *testing.T, r *RootScope) {
			t.Helper()
			require.NoError(t, RegisterEventType[OrderPlaced](r, "orders.placed"))
			require.NoError(t, RegisterEventType[OrderCancelled](r, "orders.cancelled"))
			Listen(r, noopOrderListener, ListenerID("r"), ListenerDelivery(DeliverOnce))
			Listen(r, func(context.Context, OrderCancelled) error { return nil }, ListenerID("r"), ListenerDelivery(DeliverOnce))
		}},
		{name: "no_remote_listener", setup: func(*testing.T, *RootScope) {}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := &InjectorOpts{}
			if tt.transport {
				opts.EventTransport = &loopbackTransport{}
			}
			root := NewWithOpts(opts)
			t.Cleanup(func() { root.Shutdown() })
			tt.setup(t, root)

			err := root.Start()

			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRemote_Subscriptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prefix    string
		instance  string
		mode      EventDelivery
		group     string // a trailing "/" is followed by the generated instance ID
		ephemeral bool
	}{
		{name: "once", mode: DeliverOnce, group: "mail"},
		{name: "once/prefix", prefix: "shop.", mode: DeliverOnce, group: "shop.mail"},
		{name: "all/instance", instance: "pod-0", mode: DeliverAll, group: "mail/pod-0"},
		{name: "all/prefix_instance", prefix: "shop.", instance: "pod-0", mode: DeliverAll, group: "shop.mail/pod-0"},
		{name: "all/generated", mode: DeliverAll, group: "mail/", ephemeral: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventGroupPrefix: tt.prefix, InstanceID: tt.instance, EventTransport: transport})
			t.Cleanup(func() { root.Shutdown() })
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
			Listen(root, noopOrderListener, ListenerID("mail"), ListenerDelivery(tt.mode))

			require.NoError(t, root.Start())

			subs := transport.subscriptions()
			require.Len(t, subs, 1)
			sub := subs[0]
			assert.Equal(t, "orders.placed", sub.Destination)
			assert.Equal(t, tt.mode, sub.Mode)
			assert.Equal(t, tt.ephemeral, sub.Ephemeral)
			if tt.ephemeral {
				require.True(t, strings.HasPrefix(sub.Group, tt.group), sub.Group)
				uuidV7Millis(t, strings.TrimPrefix(sub.Group, tt.group))
			} else {
				assert.Equal(t, tt.group, sub.Group)
			}
		})
	}
}

func TestRemote_DefaultGroup(t *testing.T) {
	t.Parallel()

	transport := &recordingTransport{}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	t.Cleanup(func() { root.Shutdown() })
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	Provide(root, func(Injector) (*Mailer, error) { return &Mailer{}, nil }, ListenMethod((*Mailer).OnPlaced, ListenerDelivery(DeliverOnce)))

	require.NoError(t, root.Start())

	subs := transport.subscriptions()
	require.Len(t, subs, 1)
	assert.Equal(t, "[root]/*github.com/kartaladev/doger.Mailer#github.com/kartaladev/doger.OrderPlaced/m1", subs[0].Group)
}

func TestDestinationMapper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mapper      func(string) string
		destination string
		err         error
	}{
		{name: "unset", destination: "orders.placed"},
		{name: "prefix", mapper: func(d string) string { return "prod." + d }, destination: "prod.orders.placed"},
		{name: "empty", mapper: func(string) string { return "" }, err: ErrInvalidEventType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := &recordingTransport{}
			root := NewWithOpts(&InjectorOpts{EventDestinationMapper: tt.mapper, EventTransport: transport})
			t.Cleanup(func() { root.Shutdown() })
			require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()))
			Listen(root, noopOrderListener, ListenerID("mail"), ListenerDelivery(DeliverOnce))

			err := root.Start()
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			require.NoError(t, Publish(context.Background(), root, OrderPlaced{ID: "1"}))

			assert.Equal(t, []string{"subscribe:mail", "send:" + tt.destination}, transport.callList())
			require.Len(t, transport.subscriptions(), 1)
			assert.Equal(t, tt.destination, transport.subscriptions()[0].Destination)
		})
	}
}

func TestRemote_Clone(t *testing.T) {
	t.Parallel()

	transport := &recordingTransport{}
	root := NewWithOpts(&InjectorOpts{EventTransport: transport})
	require.NoError(t, RegisterEventType[OrderPlaced](root, "orders.placed"))
	Listen(root, noopOrderListener, ListenerID("mail"), ListenerDelivery(DeliverOnce))
	Listen(root, noopOrderListener, ListenerID("cache"), ListenerDelivery(DeliverAll))
	clone := root.Clone()
	t.Cleanup(func() {
		root.Shutdown()
		clone.Shutdown()
	})

	require.NoError(t, root.Start())
	require.NoError(t, clone.Start())

	groups := map[string]int{}
	var caches []string
	for _, sub := range transport.subscriptions() {
		if strings.HasPrefix(sub.Group, "cache/") {
			caches = append(caches, sub.Group)
			continue
		}
		groups[sub.Group]++
	}
	assert.Len(t, transport.subscriptions(), 4)
	assert.Equal(t, map[string]int{"mail": 2}, groups)
	require.Len(t, caches, 2)
	assert.NotEqual(t, caches[0], caches[1], "each root has its own instance ID")
}
