package doger

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
)

const (
	// DeliverLocal delivers events to the listener in-process, by Publish
	// (see the add-eventing listeners). It is the zero value, the default.
	DeliverLocal EventDelivery = iota
	// DeliverOnce delivers each event to one instance of the listener's
	// group, through the transport: the group is the listener ID, prefixed
	// with InjectorOpts.EventGroupPrefix.
	DeliverOnce
	// DeliverAll delivers each event to every instance, through the
	// transport: the group also ends with "/" and InjectorOpts.InstanceID.
	DeliverAll
)

// String returns the name of the mode, or "EventDelivery(n)" for another
// value.
func (d EventDelivery) String() string {
	switch d {
	case DeliverLocal:
		return "DeliverLocal"
	case DeliverOnce:
		return "DeliverOnce"
	case DeliverAll:
		return "DeliverAll"
	default:
		return "EventDelivery(" + strconv.Itoa(int(d)) + ")"
	}
}

// ListenerDelivery sets how the listener receives its events. With
// DeliverOnce or DeliverAll it is a remote listener: it is reached only
// through the [EventTransport], never by in-process dispatch, so its event
// type must be registered with [RegisterEventType] (not necessarily
// externalized in this process), and Start subscribes it.
//
// Registration panics when a remote listener is registered outside the
// root and the private scopes of extensions, is a function listener
// without a [ListenerID], is also async or has a [ListenerTxPhase] other
// than TxPhaseImmediate, or is registered once the root started; and with
// "doger: invalid EventDelivery <n>" for another value. Give every remote
// listener an explicit ListenerID: its group is named after it, and a
// renamed group starts at the latest event.
func ListenerDelivery(mode EventDelivery) ListenerOption {
	return func(o *listenerOptions) {
		o.delivery = mode
	}
}

// remote reports whether the listener is reached through the transport.
func (o *listenerOptions) remote() bool {
	return o.delivery != DeliverLocal
}

// validateDelivery panics for a delivery mode that is not a constant.
func validateDelivery(o *listenerOptions) {
	if o.delivery < DeliverLocal || o.delivery > DeliverAll {
		panic(libErrorf("invalid EventDelivery %d", int(o.delivery)))
	}
}

// validateRemoteListener panics when the remote listener id, with opts,
// cannot be registered in scope; fn says whether it is a function
// listener.
func validateRemoteListener(scope *Scope, id string, fn bool, opts *listenerOptions) {
	root := scope.rootScope
	switch {
	case scope != root.self && !root.isExtensionScope(scope):
		panic(libErrorf("remote listener %q must be registered in the root or an extension scope", id))
	case fn && opts.id == "":
		panic(libErrorf("remote function listener needs a ListenerID"))
	case opts.async:
		panic(libErrorf("remote listener %q cannot be async", id))
	case opts.deferred():
		panic(libErrorf("remote listener %q cannot have a transaction phase", id))
	case root.startedOnce():
		panic(libErrorf("remote listener %q must be registered before Start", id))
	}
}

// validateRemoteBindings panics when a remote listener binding of opts,
// declared on the service name of scope, cannot be registered there.
func validateRemoteBindings(scope *Scope, name string, opts *serviceOptions) {
	if opts == nil {
		return
	}
	for index := range opts.listeners {
		binding := &opts.listeners[index]
		if binding.opts.remote() {
			validateRemoteListener(scope, bindingListenerID(scope, name, binding), false, &binding.opts)
		}
	}
}

// isExtensionScope reports whether scope is the private scope of an
// extension, installing or installed.
func (s *RootScope) isExtensionScope(scope *Scope) bool {
	registry := s.ext.Load()
	if registry == nil || scope.parentScope != s.self {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	_, ok := registry.scopes[scope.name]

	return ok
}

// startedOnce reports whether Start was called on the root.
func (s *RootScope) startedOnce() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	return s.startPhase != startIdle
}

// ErrNoEventTransport is matched by the error of Start when remote
// listeners exist and no transport is resolved.
var ErrNoEventTransport = errors.New("doger: no event transport")

// ErrEventTypeNotFound is matched by the error of Start when the event
// type of a remote listener is not registered, and by the permanent error
// of a received envelope whose type name is not registered.
var ErrEventTypeNotFound = errors.New("doger: event type not found")

// ErrListenerGroupConflict is matched by the error of Start when two
// remote listeners share a group and a destination.
var ErrListenerGroupConflict = errors.New("doger: listener group conflict")

// remoteListener is a remote listener of the root, as Start subscribes it.
type remoteListener struct {
	listener *plannedListener
	typ      reflect.Type // the listener's event type
	sub      Subscription
}

// remoteListeners returns the remote listeners of the root and of the
// private scopes of its extensions, in registration order.
func (s *RootScope) remoteListeners() []*remoteListener {
	scopes := append([]*Scope{s.self}, s.extensionScopes()...)
	remotes := make([]*remoteListener, 0, len(scopes))
	for _, scope := range scopes {
		remotes = append(remotes, scopeRemoteListeners(scope)...)
	}
	slices.SortStableFunc(remotes, func(a, b *remoteListener) int {
		return cmp.Or(cmp.Compare(a.listener.regIndex, b.listener.regIndex), cmp.Compare(a.listener.sub, b.listener.sub))
	})

	return remotes
}

// scopeRemoteListeners returns the remote listeners of scope.
func scopeRemoteListeners(scope *Scope) []*remoteListener {
	var remotes []*remoteListener
	isRemote := func(opts *listenerOptions, _ reflect.Type) bool { return opts.remote() }
	for _, listener := range scopeListenersWhere(scope, 0, isRemote) {
		if !listener.yielded() {
			remotes = append(remotes, &remoteListener{listener: listener, typ: listener.eventType()})
		}
	}

	return remotes
}

// prepareRemoteListeners returns the remote listeners of the root with
// their event types and subscriptions, or an error when one cannot be
// subscribed.
func (s *RootScope) prepareRemoteListeners(remotes []*remoteListener) error {
	seen := map[[2]string]string{}
	for _, remote := range remotes {
		info := s.eventTypeOf(remote.typ)
		if info == nil {
			return libErrorMatching(ErrEventTypeNotFound, "remote listener %q: event type `%s` is not registered", remote.listener.id, remote.typ)
		}
		sub, err := s.subscriptionFor(remote.listener, info)
		if err != nil {
			return err
		}
		remote.sub = sub
		key := [2]string{sub.Group, sub.Destination}
		if other, ok := seen[key]; ok {
			return libErrorMatching(ErrListenerGroupConflict, "remote listeners %q and %q share group %q on %q", other, remote.listener.id, sub.Group, sub.Destination)
		}
		seen[key] = remote.listener.id
	}

	return nil
}

// subscriptionFor returns the subscription of the remote listener, of the
// event type info: the mapped destination, and the group
// EventGroupPrefix + ID, followed by "/" and the instance ID for
// DeliverAll, ephemeral when that ID was generated.
func (s *RootScope) subscriptionFor(listener *plannedListener, info *eventTypeInfo) (Subscription, error) {
	destination, err := s.mappedDestination(info)
	if err != nil {
		return Subscription{}, err
	}
	mode := listener.opts.delivery
	sub := Subscription{Destination: destination, Group: s.opts.EventGroupPrefix + listener.id, Mode: mode}
	if mode == DeliverAll {
		instance, generated := s.instanceID()
		sub.Group += "/" + instance
		sub.Ephemeral = generated
	}

	return sub, nil
}

// instanceID returns the instance ID of the root, and whether it was
// generated: InjectorOpts.InstanceID, else a UUIDv7 generated once.
func (s *RootScope) instanceID() (string, bool) {
	if id := s.opts.InstanceID; id != "" {
		return id, false
	}
	state := s.transportState()
	state.instanceOnce.Do(func() { state.instance = newUUIDv7(s.opts.Clock.Now()) })

	return state.instance, true
}

// mappedDestination returns the destination of info, mapped by
// InjectorOpts.EventDestinationMapper when it is set.
func (s *RootScope) mappedDestination(info *eventTypeInfo) (string, error) {
	mapper := s.opts.EventDestinationMapper
	if mapper == nil {
		return info.destination, nil
	}
	if destination := mapper(info.destination); destination != "" {
		return destination, nil
	}

	return "", libErrorMatching(ErrInvalidEventType, "EventDestinationMapper mapped %q to an empty destination", info.destination)
}

// openSubscriptions validates the remote listeners of the root and
// subscribes each one with transport, which is nil when none is resolved,
// in registration order. A failing Subscribe, or a panicking one, stops
// the subscriptions opened before, in reverse order, and fails Start.
func (s *RootScope) openSubscriptions(transport EventTransport, remotes []*remoteListener) error {
	if len(remotes) == 0 {
		return nil
	}
	if transport == nil {
		return libErrorMatching(ErrNoEventTransport, "%d remote listeners but no EventTransport", len(remotes))
	}
	if err := s.prepareRemoteListeners(remotes); err != nil {
		return err
	}

	state := s.transportState()
	opened := make([]openSubscription, 0, len(remotes))
	for _, remote := range remotes {
		ctx, cancel := context.WithCancel(context.Background())
		stop, err := recoverListenerErrorValue(func() (func(context.Context) error, error) {
			return transport.Subscribe(ctx, remote.sub, s.inboundHandler(remote))
		})
		if err != nil {
			cancel()
			s.stopSubscriptions(context.Background(), opened)
			return libErrorf("subscribing remote listener %q: %w", remote.listener.id, err)
		}
		opened = append(opened, openSubscription{id: remote.listener.id, fn: remote.listener.fn, stop: stop, cancel: cancel})
	}

	state.mu.Lock()
	closed := state.closed
	if !closed {
		state.subscriptions = opened
	}
	state.mu.Unlock()
	if closed { // a shutdown began meanwhile and will not stop them
		s.stopSubscriptions(context.Background(), opened)
	}

	return nil
}

// openSubscription is a subscription Start opened.
type openSubscription struct {
	id     string        // the remote listener's ID
	fn     *funcListener // the function listener, or nil for a service listener
	stop   func(context.Context) error
	cancel context.CancelFunc // cancels the subscription context
}

// close stops the subscription with ctx, then cancels its context. A stop
// error or panic goes to HookEventError.
func (o openSubscription) close(ctx context.Context, root *RootScope) {
	defer o.cancel()
	if err := recoverListenerError(func() error { return o.stop(ctx) }); err != nil {
		root.reportEventError(ctx, libErrorf("stopping the subscription of remote listener %q: %w", o.id, err))
	}
}

// closeSubscriptions stops the subscriptions Start opened, in reverse
// order, each waiting for its handlers in flight, bounded by ctx. Later
// subscriptions are stopped at once.
func (s *RootScope) closeSubscriptions(ctx context.Context) {
	state := s.transport.Load()
	if state == nil {
		return
	}
	state.mu.Lock()
	state.closed = true
	subscriptions := state.subscriptions
	state.subscriptions = nil
	state.mu.Unlock()

	s.stopSubscriptions(ctx, subscriptions)
}

// closeSubscriptionOf stops the subscription Start opened for the remote
// function listener fn, if any, waiting for its handlers in flight.
func (s *RootScope) closeSubscriptionOf(fn *funcListener) {
	state := s.transport.Load()
	if state == nil {
		return
	}
	state.mu.Lock()
	index := slices.IndexFunc(state.subscriptions, func(o openSubscription) bool { return o.fn == fn })
	if index < 0 {
		state.mu.Unlock()
		return
	}
	subscription := state.subscriptions[index]
	state.subscriptions = slices.Delete(slices.Clone(state.subscriptions), index, index+1)
	state.mu.Unlock()

	subscription.close(context.Background(), s)
}

// stopSubscriptions stops subscriptions in reverse order with ctx.
func (s *RootScope) stopSubscriptions(ctx context.Context, subscriptions []openSubscription) {
	for _, subscription := range slices.Backward(subscriptions) {
		subscription.close(ctx, s)
	}
}
