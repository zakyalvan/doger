package doger

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Envelope is an externalized event as it travels between processes.
type Envelope struct {
	ID          string            // unique per publish: UUIDv7 unless InjectorOpts.EventIDGenerator is set
	Destination string            // where it is sent: the type's destination, mapped by InjectorOpts.EventDestinationMapper
	EventType   string            // the registered name of the event type
	ContentType string            // the codec's content type
	Version     int               // the registered version of the event type
	Payload     []byte            // the encoded event
	Headers     map[string]string // from InjectorOpts.EventPropagator; empty without one
	PublishedAt time.Time         // from the root clock
}

// EventDelivery is how a listener receives its events (see
// ListenerDelivery).
type EventDelivery int

// Subscription is what a remote listener asks a transport to deliver.
type Subscription struct {
	Destination string        // where the events are sent
	Group       string        // one delivery per group per event
	Mode        EventDelivery // DeliverOnce or DeliverAll
	Ephemeral   bool          // DeliverAll with a generated instance ID: the group ends with the subscription
}

// EnvelopeHandler handles one delivered envelope. Its result is the
// acknowledgement: nil acknowledges, an error asks the transport to
// redeliver, and an error for which [IsPermanentError] is true asks it to
// stop redelivering (dead-letter or drop).
type EnvelopeHandler func(ctx context.Context, env Envelope) error

// EventTransport is the seam between doger and a messaging library: it
// sends externalized events and delivers them to remote listeners. It is
// resolved once per root: [InjectorOpts.EventTransport], else a root
// service named NameOf[EventTransport](), else none.
//
// Send hands env to the transport; it may be called concurrently.
// Subscribe starts delivering the envelopes sent to sub.Destination to h,
// once per sub.Group, a new group starting at the latest event and an
// existing one resuming where it stopped; ctx is cancelled when the
// subscription stops. The returned stop function stops delivering and
// returns once the handlers in flight returned, or once its context ended.
// A transport that cannot honor sub returns an error, matching
// [ErrSubscriptionNotSupported] for an unsupported mode.
type EventTransport interface {
	Send(ctx context.Context, env Envelope) error
	Subscribe(ctx context.Context, sub Subscription, h EnvelopeHandler) (stop func(context.Context) error, err error)
}

// TxEventSender is an [EventTransport] with an outbox: SendTx writes env in
// the transaction its TxSource finds in ctx, and the transport delivers it
// once that transaction committed. A rollback discards it.
type TxEventSender interface {
	EventTransport
	SendTx(ctx context.Context, env Envelope) error
	TxSource() TxSource
}

// ErrSubscriptionNotSupported is matched by the error of a transport's
// Subscribe for a subscription it cannot honor, such as DeliverAll on a
// transport that cannot broadcast.
var ErrSubscriptionNotSupported = errors.New("doger: subscription not supported")

// eventTransportState is the transport state of a root, allocated on first
// use, so a container that externalizes nothing holds none.
type eventTransportState struct {
	mu        sync.Mutex
	transport EventTransport // the resolved service transport, once found
	txSource  TxSource       // the resolved service TxSource, once found

	noTransport sync.Once // logs that externalized events stay in-process

	instanceOnce sync.Once
	instance     string // the generated instance ID

	subscriptions []openSubscription // opened by Start, in order
	closed        bool               // root shutdown stopped the subscriptions
}

// transportState returns the transport state of the root, allocating it
// on first use.
func (s *RootScope) transportState() *eventTransportState {
	return loadOrInit(&s.transport, func() *eventTransportState { return &eventTransportState{} })
}

// resolveTransport returns the transport of the root: InjectorOpts.EventTransport,
// else a root service named NameOf[EventTransport](), built once, else nil.
// A failing service provider is returned as an error, every time.
func (s *RootScope) resolveTransport() (EventTransport, error) {
	if transport := s.opts.EventTransport; transport != nil {
		return transport, nil
	}
	state := s.transportState()

	return resolveRootService(s, &state.mu, &state.transport)
}

// resolveRootService returns the root service named NameOf[T](), once
// cached in *cache under mu, or the zero T when none is registered. A
// found service is cached, so it is invoked once; a missing one or a
// failing provider is looked up again by the next call.
func resolveRootService[T comparable](s *RootScope, mu *sync.Mutex, cache *T) (T, error) {
	var zero T
	mu.Lock()
	cached := *cache
	mu.Unlock()
	if cached != zero {
		return cached, nil
	}

	name := NameOf[T]()
	if !s.self.serviceExist(name) {
		return zero, nil
	}
	service, err := InvokeNamed[T](s, name)
	if err != nil {
		return zero, err
	}

	mu.Lock()
	defer mu.Unlock()
	if *cache == zero {
		*cache = service
	}

	return *cache, nil
}

// validateEventTransport checks, at Start, that the transport can do what
// the container asks of it: an outbox when ExternalizeOutbox is configured
// globally or for a registered type, and no TxSource conflicting with its
// own; then it subscribes the remote listeners. A failing transport
// provider fails Start. A container that externalizes nothing and has no
// remote listener resolves no transport.
func (s *RootScope) validateEventTransport() error {
	externalized, outbox := s.externalizes()
	remotes := s.remoteListeners()
	if s.opts.EventTransport == nil && !externalized && len(remotes) == 0 {
		return nil
	}
	transport, err := s.resolveTransport()
	if err != nil {
		return err
	}
	if transport == nil {
		return s.openSubscriptions(nil, remotes)
	}
	if outbox {
		if err := checkOutbox(transport); err != nil {
			return err
		}
	}
	if _, err = s.resolveTxSource(transport); err != nil {
		return err
	}

	return s.openSubscriptions(transport, remotes)
}

// checkOutbox returns an error matching ErrOutboxNotSupported when
// transport has no outbox.
func checkOutbox(transport EventTransport) error {
	if _, ok := transport.(TxEventSender); !ok {
		return libErrorMatching(ErrOutboxNotSupported, "ExternalizeOutbox needs a transport implementing TxEventSender")
	}

	return nil
}

// externalizes reports whether a registered type is externalized, and
// whether InjectorOpts.EventExternalize or the strategy of a registered
// externalized type is ExternalizeOutbox.
func (s *RootScope) externalizes() (externalized, outbox bool) {
	outbox = s.opts.EventExternalize == ExternalizeOutbox
	if registry := s.eventTypes.Load(); registry != nil {
		for _, info := range registry.byType {
			externalized = externalized || info.externalized
			outbox = outbox || info.externalized && info.strategy == ExternalizeOutbox
		}
	}

	return externalized || outbox, outbox
}
