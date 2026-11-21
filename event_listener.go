package doger

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// funcListener is a function listener registered with Listen.
type funcListener struct {
	id       string
	regIndex uint64 // taken from the container's registration sequence
	typ      reflect.Type
	call     func(ctx context.Context, event any) error
	opts     listenerOptions
}

// ListenerOption configures a listener: see [ListenerID].
type ListenerOption func(*listenerOptions)

// listenerOptions are the options of one listener.
type listenerOptions struct {
	id         string         // explicit ID; empty means derived
	order      int            // ListenerOrder; lower runs first
	filter     func(any) bool // ListenerFilter; nil means none
	filterType reflect.Type   // the parameter type of filter
	async      bool           // ListenerAsync
	txPhase    TxPhase        // ListenerTxPhase
	txFallback TxFallbackMode // ListenerTxFallback
	delivery   EventDelivery  // ListenerDelivery
}

// ListenerID sets the ID of the listener, used in errors and logs. An empty
// id keeps the derived one. IDs need not be unique.
func ListenerID(id string) ListenerOption {
	return func(o *listenerOptions) {
		o.id = id
	}
}

// ListenerOrder sets the position of the listener among the synchronous
// listeners an event reaches: lower values run first, and the default is 0.
// Listeners of equal order run by reach (the publishing scope, then each
// ancestor up to the root, then the private scopes of installed extensions
// in installation order), then in registration order. Order is decided
// without building any listener: [WithOrder] and [Ordered] do not affect
// it. Given more than once, the last one wins.
func ListenerOrder(n int) ListenerOption {
	return func(o *listenerOptions) {
		o.order = n
	}
}

// ListenerFilter skips the listener for the events pred returns false for,
// without building a listener service. A nil pred means no filter, and the
// last ListenerFilter given wins. pred runs on the publisher's goroutine,
// during Publish: before an async delivery is submitted, and before a
// listener with a deferred [ListenerTxPhase] is deferred to the
// transaction, so the event is not filtered again when it ends. Its panic
// is recovered into a [*ListenerError].
//
// Registration panics when the listener's event type is not assignable to
// F ("doger: ListenerFilter for F cannot apply to listener of E").
func ListenerFilter[F any](pred func(F) bool) ListenerOption {
	return func(o *listenerOptions) {
		if pred == nil {
			o.filter, o.filterType = nil, nil
			return
		}
		o.filterType = reflect.TypeFor[F]()
		o.filter = func(event any) bool {
			return pred(event.(F)) //nolint:errcheck,forcetypeassert // checked at registration: every event the listener receives is assignable to F
		}
	}
}

// listenerOptionsFor applies opts for a listener of eventType, panicking
// when its filter cannot apply to it.
func listenerOptionsFor(eventType reflect.Type, opts []ListenerOption) listenerOptions {
	options := listenerOptions{}
	applyOptions(&options, opts)
	if options.filterType != nil && !eventType.AssignableTo(options.filterType) {
		panic(libErrorf("ListenerFilter for %s cannot apply to listener of %s", options.filterType, eventType))
	}
	validateTxOptions(&options)
	validateDelivery(&options)

	return options
}

// ListenerAsync delivers events to the listener through the container's
// [EventExecutor] instead of on the publisher's goroutine: Publish returns
// once the delivery is submitted. The listener's context keeps the values
// of the publisher's context but is not cancelled by it. Its errors and
// panics have no caller: they go to [InjectorOpts.HookEventError]. A
// listener service is resolved, and built if needed, on the publisher's
// goroutine when the delivery is submitted, so its build failure fails
// Publish with a [*ListenerError]. Async
// deliveries are not ordered among themselves. Repeating it is the same as
// giving it once.
//
// An async listener may shut its own scope, or the container, down by
// passing its context to ShutdownWithContext: that shutdown does not wait
// for the calling delivery. Without that context it would wait for itself.
func ListenerAsync() ListenerOption {
	return func(o *listenerOptions) {
		o.async = true
	}
}

// rootEvents is the event state of a container, allocated on first use
// and at root shutdown.
type rootEvents struct {
	funcSeq atomic.Uint64 // the last function listener sequence number

	parallelism uint // of the default executor

	executorMu       sync.Mutex
	executor         EventExecutor // the async executor; nil until the first async delivery
	borrowedExecutor bool          // executor is a clone's InjectorOpts.EventExecutor shared with its source: never shut down here

	// intakeMu orders async admissions with the drain: an admission holds
	// it for reading, and closing the intake takes it for writing, so every
	// admitted delivery is counted before the drain waits.
	intakeMu     sync.RWMutex
	shuttingDown atomic.Bool // set under intakeMu: root shutdown stopped accepting async deliveries, and services are shutting down
	closed       atomic.Bool // root shutdown completed its services: Publish through the root fails

	// abortCtx is cancelled, with cause ErrPublishAfterShutdown, when a root
	// drain deadline passes: every async delivery's context follows it.
	abortCtx context.Context //nolint:containedctx // the cancellation source of every delivery context
	abort    context.CancelCauseFunc
}

// newRootEvents returns the event state of root.
func newRootEvents(root *RootScope) *rootEvents {
	abortCtx, abort := context.WithCancelCause(context.Background())

	return &rootEvents{
		parallelism: root.opts.EventAsyncParallelism,
		executor:    root.opts.EventExecutor,
		abortCtx:    abortCtx,
		abort:       abort,
	}
}

// asyncExecutor returns the executor of async deliveries, creating the
// default one on first use.
func (e *rootEvents) asyncExecutor() EventExecutor {
	e.executorMu.Lock()
	defer e.executorMu.Unlock()

	if e.executor == nil {
		e.executor = NewBoundedEventExecutor(e.parallelism)
	}

	return e.executor
}

// shutdownExecutor shuts the async executor down, if one was used and
// it is not borrowed from a clone's source.
func (e *rootEvents) shutdownExecutor(ctx context.Context) error {
	e.executorMu.Lock()
	executor := e.executor
	e.executorMu.Unlock()

	if executor == nil || e.borrowedExecutor {
		return nil
	}

	return executor.Shutdown(ctx)
}

// closeIntake stops accepting async deliveries. Deliveries admitted before
// are counted in their scopes when it returns.
func (e *rootEvents) closeIntake() {
	e.intakeMu.Lock()
	defer e.intakeMu.Unlock()

	e.shuttingDown.Store(true)
}

// eventState returns the event state of the container, allocating it on
// first use.
func (s *RootScope) eventState() *rootEvents {
	return loadOrInit(&s.events, func() *rootEvents { return newRootEvents(s) })
}

// Listener is implemented by a service that receives events of type E
// once registered with [AsListener].
type Listener[E any] interface {
	OnEvent(ctx context.Context, event E) error
}

// ErrInvalidListener is matched by the *ListenerError of a listener service
// registered under an interface type whose instance does not implement
// what its registration declared.
var ErrInvalidListener = errors.New("doger: invalid listener")

// listenerBinding is a listener declared on a service registration, by
// AsListener or ListenMethod.
type listenerBinding struct {
	typ       reflect.Type // the event type E
	eventName string       // E in import-path form, for IDs
	target    reflect.Type // what the registered type must be assignable to
	call      func(ctx context.Context, instance, event any) error
	opts      listenerOptions
	method    int  // n for the n-th ListenMethod of the registration; 0 for AsListener
	nilMethod bool // ListenMethod was given a nil method
}

// AsListener opts the registration into receiving the events assignable to
// E through its [Listener] OnEvent method. The service is not built before
// an event reaches it: a lazy service is built by its first event, a
// transient one once per delivery, and an eager one is used as built.
//
// Registration panics, before the service is stored, when the registered
// type is a concrete type that does not implement Listener[E] ("doger:
// service `name` does not implement Listener[E]"). A service registered
// under an interface type is checked on its instance at its first
// delivery: a failure is a *ListenerError matching [ErrInvalidListener].
// Repeating AsListener for one E registers one listener, with the options
// of the last one.
func AsListener[E any](opts ...ListenerOption) ProvideOption {
	return func(o *serviceOptions) {
		binding := newBinding(Listener[E].OnEvent, "does not implement Listener["+reflect.TypeFor[E]().String()+"]", opts)
		index := slices.IndexFunc(o.listeners, func(b listenerBinding) bool { return b.method == 0 && b.typ == binding.typ })
		if index >= 0 {
			o.listeners[index] = binding
			return
		}
		o.listeners = append(o.listeners, binding)
	}
}

// ListenMethod opts a registration of type S into receiving the events
// assignable to E through method, called as method(instance, ctx, event): a
// method expression such as (*Mailer).OnPlaced, the Go form of an event
// listener method. One service can listen to several event types with one
// ListenMethod each, and is built once, by the first event that reaches it.
// The n-th ListenMethod of a registration has the default ID
// "<scope path>/<service>#<E>/m<n>".
//
// Registration panics, before the service is stored, when the registered
// type is a concrete type other than S ("doger: service `name` is not of
// type S required by ListenMethod") or method is nil ("doger: ListenMethod
// requires a non-nil method for service `name`"). A service registered
// under an interface type is checked on its instance at its first delivery.
func ListenMethod[S, E any](method func(S, context.Context, E) error, opts ...ListenerOption) ProvideOption {
	return func(o *serviceOptions) {
		n := 1
		for _, binding := range o.listeners {
			if binding.method > 0 {
				n++
			}
		}
		binding := newBinding(method, "is not of type "+reflect.TypeFor[S]().String()+" required by ListenMethod", opts)
		binding.method = n
		binding.nilMethod = method == nil
		o.listeners = append(o.listeners, binding)
	}
}

// newBinding returns the binding calling method(instance, ctx, event) for
// the events assignable to E, on an instance of type S. An instance of
// another type fails the delivery with an error matching
// ErrInvalidListener, whose message ends with mismatch.
func newBinding[S, E any](method func(S, context.Context, E) error, mismatch string, opts []ListenerOption) listenerBinding {
	return listenerBinding{
		typ:       reflect.TypeFor[E](),
		eventName: inferServiceName[E](),
		target:    reflect.TypeFor[S](),
		call: func(ctx context.Context, instance, event any) error {
			receiver, ok := instance.(S)
			if !ok {
				return libErrorMatching(ErrInvalidListener, "instance of type `%T` %s", instance, mismatch)
			}
			return method(receiver, ctx, event.(E)) //nolint:errcheck,forcetypeassert // dispatched only when assignable to E
		},
		opts: listenerOptionsFor(reflect.TypeFor[E](), opts),
	}
}

// validateListenerOpt checks the listener bindings against the registered
// type. An interface-typed registration is checked on its instance.
func validateListenerOpt(name string, registered reflect.Type, opts *serviceOptions) {
	for _, binding := range opts.listeners {
		if binding.method == 0 {
			if checkRegisteredType(registered, typeCanCastToType(registered, binding.target)) == registeredTypeFails {
				panic(libErrorf("service `%s` does not implement Listener[%s]", name, binding.typ))
			}
			continue
		}

		if binding.nilMethod {
			panic(libErrorf("ListenMethod requires a non-nil method for service `%s`", name))
		}
		if checkRegisteredType(registered, registered == binding.target) == registeredTypeFails {
			panic(libErrorf("service `%s` is not of type %s required by ListenMethod", name, binding.target))
		}
	}
}

// bindingListenerID returns the ID of binding, declared on the service
// name of scope: its ListenerID, else its default ID.
func bindingListenerID(scope *Scope, name string, binding *listenerBinding) string {
	return cmp.Or(binding.opts.id, serviceListenerID(scope, name, binding))
}

// serviceListenerID returns the default ID of binding, declared on the
// service name of scope.
func serviceListenerID(scope *Scope, name string, binding *listenerBinding) string {
	id := scopePath(scope) + "/" + name + "#" + binding.eventName
	if binding.method > 0 {
		id += "/m" + strconv.Itoa(binding.method)
	}

	return id
}

// ListenerError is the error of a listener that failed on an event: it
// returned an error, panicked, or could not be built or reached. It unwraps
// to the cause.
type ListenerError struct {
	Listener  string       // the listener's ID
	EventType reflect.Type // the dynamic type of the event
	Err       error        // the cause
}

// Error returns "doger: listener `<id>` failed on event `<type>`: <cause>".
func (e *ListenerError) Error() string {
	return libErrorf("listener `%s` failed on event `%s`: %w", e.Listener, e.EventType, e.Err).Error()
}

// Unwrap returns the cause.
func (e *ListenerError) Unwrap() error { return e.Err }

// scopeEvents is the event state of one scope, allocated on first use.
type scopeEvents struct {
	funcs    []*funcListener // copied before written; guarded by the scope's mu
	funcsVer uint64          // counts the changes of funcs; guarded by the scope's mu
	inflight inflightCounter // async deliveries in flight to listeners of the scope and its descendants

	planMu sync.Mutex
	plans  map[planKey]*eventPlan // dispatch plans of the events published through the scope
}

// setFuncsLocked replaces the function listeners; the scope's mu is held
// as a writer.
func (e *scopeEvents) setFuncsLocked(funcs []*funcListener) {
	e.funcs = funcs
	e.funcsVer++
}

// inflightCounter counts deliveries in flight and lets a drain wait for
// at most some of them. The zero value is ready to use.
type inflightCounter struct {
	mu      sync.Mutex
	n       int
	waiters map[int]chan struct{} // by bound: closed once n drops to at most it; shared by the waiters of one bound
}

// add adds delta to the count, waking the waiters whose bound it reached.
func (c *inflightCounter) add(delta int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.n += delta
	for bound, ch := range c.waiters {
		if c.n <= bound {
			close(ch)
			delete(c.waiters, bound)
		}
	}
}

// atMost returns a channel closed once at most bound deliveries are in
// flight. Waiters of one bound share a channel, so a waiter giving up
// leaves nothing behind.
func (c *inflightCounter) atMost(bound int) <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.n <= bound {
		return closedChannel
	}
	ch, ok := c.waiters[bound]
	if !ok {
		if c.waiters == nil {
			c.waiters = map[int]chan struct{}{}
		}
		ch = make(chan struct{})
		c.waiters[bound] = ch
	}

	return ch
}

// closedChannel is a closed channel.
var closedChannel = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

// admitInflight counts one delivery in flight in s and each of its
// ancestors, so the drain of a scope waits for its descendants too, and
// reports true; it counts nothing and reports false when one of them is a
// child scope whose shutdown began. Each check holds the scope's lock, which
// marking it closed takes: a delivery is either counted before the scope's
// drain waits, or refused.
func (s *Scope) admitInflight() bool {
	for scope := s; scope != nil; scope = scope.parentScope {
		if !scope.admitOne() {
			for counted := s; counted != scope; counted = counted.parentScope {
				counted.eventState().inflight.add(-1)
			}
			return false
		}
	}

	return true
}

// admitOne counts one delivery in flight in s, unless s is a child scope
// whose shutdown began.
func (s *Scope) admitOne() bool {
	state := s.eventState()
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.parentScope != nil && s.closed.Load() {
		return false
	}
	state.inflight.add(1)

	return true
}

// releaseInflight ends one delivery counted by admitInflight.
func (s *Scope) releaseInflight() {
	for scope := s; scope != nil; scope = scope.parentScope {
		scope.eventState().inflight.add(-1)
	}
}

// awaitInflight waits until no async delivery to a listener of s or its
// descendants is in flight, bounded by ctx; it reports whether it did. The
// running deliveries whose context ctx is, or derives from, are not waited
// for: an async listener shutting down its own scope or container cannot
// end before that shutdown returns.
func (s *Scope) awaitInflight(ctx context.Context) bool {
	return awaitDone(s.eventState().inflight.atMost(callerDeliveries(ctx, s)), ctx)
}

// deliveryKey marks the context of an async delivery with its *delivery.
type deliveryKey struct{}

// delivery is a running async delivery, as its context knows it.
type delivery struct {
	scope    *Scope       // the listener's scope
	parent   *delivery    // the delivery whose context published this one, if any
	inSubmit *atomic.Bool // true while the Submit of this delivery has not returned
	ended    atomic.Bool
}

// withDelivery returns ctx marked as the context of a delivery to a
// listener of scope, submitted by a Submit call running while inSubmit is
// true, and the function ending the delivery.
func withDelivery(ctx context.Context, scope *Scope, inSubmit *atomic.Bool) (context.Context, func()) {
	parent, _ := ctx.Value(deliveryKey{}).(*delivery)
	d := &delivery{scope: scope, parent: parent, inSubmit: inSubmit}

	return context.WithValue(ctx, deliveryKey{}, d), func() { d.ended.Store(true) }
}

// callerDeliveries returns how many running deliveries counted in s the
// caller with context ctx blocks: the delivery ctx marks, and each
// publishing delivery still inside the Submit of the one it published, as
// an executor running the task inline keeps it. A publishing delivery whose
// Submit returned runs on its own and is waited for.
func callerDeliveries(ctx context.Context, s *Scope) int {
	n := 0
	d, _ := ctx.Value(deliveryKey{}).(*delivery)
	for ; d != nil; d = d.parent {
		if !d.ended.Load() && isScopeOrDescendant(d.scope, s) {
			n++
		}
		if !d.inSubmit.Load() {
			break
		}
	}

	return n
}

// isScopeOrDescendant reports whether scope is ancestor or one of its
// descendants.
func isScopeOrDescendant(scope, ancestor *Scope) bool {
	for ; scope != nil; scope = scope.parentScope {
		if scope == ancestor {
			return true
		}
	}

	return false
}

// eventState returns the event state of s, allocating it on first use.
func (s *Scope) eventState() *scopeEvents {
	return loadOrInit(&s.events, func() *scopeEvents { return &scopeEvents{} })
}

// Listen registers fn as a listener of the events assignable to E in the
// scope of i, and returns a function that unregisters it: its first call
// unregisters the listener and returns true, later calls return false,
// like the stop function of [context.AfterFunc]. For a remote listener
// (see [ListenerDelivery]) subscribed by Start, it also stops the
// listener's subscription, waiting for its handlers in flight: do not call
// it from that listener.
//
// The listener is reached by events published through that scope or its
// descendants. Its default ID, used in errors and logs, is
// "<scope path>/func#<n>" for the n-th function listener of the container;
// [ListenerID] sets another.
//
// Listen panics with "doger: Listen requires a non-nil function" for a nil
// fn, with an error matching [ErrNilInjector] for a nil injector, and with
// an error matching [ErrPublishAfterShutdown] for a scope no event can
// reach anymore: a child scope whose shutdown began, or a root whose
// shutdown completed.
func Listen[E any](i Injector, fn func(context.Context, E) error, opts ...ListenerOption) (stop func() bool) {
	scope := injectorScope(mustInjector(i))
	if fn == nil {
		panic(libErrorf("Listen requires a non-nil function"))
	}
	options := listenerOptionsFor(reflect.TypeFor[E](), opts)
	if options.remote() {
		validateRemoteListener(scope, options.id, true, &options)
	}
	seq := scope.rootScope.eventState().funcSeq.Add(1)
	listener := &funcListener{
		regIndex: scope.rootScope.nextRegIndex.Add(1) - 1,
		id:       cmp.Or(options.id, scopePath(scope)+"/func#"+strconv.FormatUint(seq, 10)),
		opts:     options,
		typ:      reflect.TypeFor[E](),
		call: func(ctx context.Context, event any) error {
			return fn(ctx, event.(E)) //nolint:errcheck,forcetypeassert // dispatched only when assignable to E
		},
	}

	state := scope.eventState()
	scope.mu.Lock()
	if scope.closedForEvents() { // under the lock marking a child scope closed
		scope.mu.Unlock()
		panic(libErrorMatching(ErrPublishAfterShutdown, "cannot listen on scope `%s`: it is shut down", scope.name))
	}
	state.setFuncsLocked(append(slices.Clone(state.funcs), listener))
	scope.mu.Unlock()

	return func() bool {
		if !scope.removeFuncListener(listener) {
			return false
		}
		if options.remote() {
			scope.rootScope.closeSubscriptionOf(listener)
		}
		return true
	}
}

// removeFuncListener unregisters listener from s, reporting whether it was
// registered there.
func (s *Scope) removeFuncListener(listener *funcListener) bool {
	state := s.eventState()
	s.mu.Lock()
	defer s.mu.Unlock()

	index := slices.Index(state.funcs, listener)
	if index < 0 {
		return false
	}
	state.setFuncsLocked(slices.Delete(slices.Clone(state.funcs), index, index+1))

	return true
}

// scopePathEscaper escapes the characters that would make scope paths
// ambiguous.
var scopePathEscaper = strings.NewReplacer("%", "%25", "/", "%2F")

// scopePath returns the path of scope from the root, its segments the
// escaped scope names joined by "/".
func scopePath(scope *Scope) string {
	segments := scopeNamePath(scope)
	for i, name := range segments {
		segments[i] = scopePathEscaper.Replace(name)
	}

	return strings.Join(segments, "/")
}
