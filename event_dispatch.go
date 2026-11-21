package doger

import (
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"
)

// plannedListener is a listener a publish reaches: a function listener, or
// a listener binding of a service registration.
type plannedListener struct {
	id       string
	scope    *Scope // the scope the listener lives in
	opts     *listenerOptions
	rank     int    // reach rank of scope: 0 for the publishing scope, then ancestors, then extension scopes
	regIndex uint64 // registration index of the function or the service
	sub      int    // position of the binding within its registration

	fn *funcListener // a function listener, or nil

	// a service listener
	name    string
	wrapper serviceWrapperAny
	binding *listenerBinding
}

// accepts reports whether the listener's filter accepts event; a filter
// panic is returned as an error.
func (l *plannedListener) accepts(event any) (accepted bool, err error) {
	if l.opts.filter == nil {
		return true, nil
	}

	err = recoverListenerError(func() error {
		accepted = l.opts.filter(event)
		return nil
	})

	return accepted && err == nil, err
}

// recoverListenerErrorValue is recoverListenerError for a function with a
// result.
func recoverListenerErrorValue[V any](fn func() (V, error)) (value V, err error) {
	err = recoverListenerError(func() error {
		var fnErr error
		value, fnErr = fn()
		return fnErr
	})

	return value, err
}

// recoverListenerError calls fn, returning its error or, when it panics,
// the panic as an error.
func recoverListenerError(fn func() error) (err error) {
	if recovered, panicked := callHookSafely(func() { err = fn() }); panicked {
		return panicAsError(recovered)
	}

	return err
}

// panicAsError returns a recovered panic value as an error: an error value
// is wrapped, any other value formatted.
func panicAsError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return fmt.Errorf("panic: %w", err)
	}

	return fmt.Errorf("panic: %v", recovered)
}

// yielded reports whether the listener belongs to a default registration
// that yields to a service of an ancestor: like an alias, it is not a
// listener of its own. Deciding it builds nothing.
func (l *plannedListener) yielded() bool {
	return l.fn == nil && isYieldedDefaultIn(l.scope, l.wrapper)
}

// unavailable reports whether the listener is a service listener that
// root service shutdown must skip: not built, so it is never built
// mid-shutdown, or no longer registered.
func (l *plannedListener) unavailable() bool {
	if l.fn != nil {
		return false
	}
	_, built := buildStateOf(l.wrapper)

	return !built || !scopeHolds(l.scope, l.name, l.wrapper)
}

// resolved returns a function delivering events to the listener: for a
// service listener, to its instance, resolved now. An async delivery
// resolves its service on the publisher's goroutine, so a build failure is
// returned by Publish, and a root shutdown beginning before the delivery
// runs does not refuse the build.
func (l *plannedListener) resolved() (func(ctx context.Context, event any) error, error) {
	if l.fn != nil {
		return l.fn.call, nil
	}

	instance, err := l.instance()
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, event any) error { return l.binding.call(ctx, instance, event) }, nil
}

// instance resolves the listener service from its own scope, with its
// invocation hooks. It records no dependency edge: a publisher does not
// depend on its listeners. A publish from a provider whose event reaches
// the service being built, directly or through the listener's
// dependencies, fails with ErrCircularDependency through the build
// wait-for graph instead of deadlocking.
func (l *plannedListener) instance() (any, error) {
	return invokeServiceInstance(l.scope, nil, l.name, l.name, l.scope, l.wrapper)
}

// matchListeners returns the listeners reached by an event of eventType
// published through scope, in dispatch order (see lessListener). The plan
// is cached in scope, keyed by eventType and local, until a reached scope's
// services or function listeners change, or an extension installation
// ends. The returned slice is never written.
func matchListeners(scope *Scope, eventType reflect.Type, local bool) []*plannedListener {
	state := scope.eventState()
	key := planKey{typ: eventType, local: local}

	state.planMu.Lock()
	plan := state.plans[key]
	state.planMu.Unlock()
	if plan != nil && plan.current(scope.rootScope) {
		return plan.listeners
	}

	plan = buildPlan(scope, eventType, local)
	state.planMu.Lock()
	if state.plans == nil {
		state.plans = map[planKey]*eventPlan{}
	}
	state.plans[key] = plan
	state.planMu.Unlock()

	return plan.listeners
}

// planKey identifies a dispatch plan of a scope.
type planKey struct {
	typ   reflect.Type
	local bool
}

// eventPlan is the immutable list of the listeners an event reaches, with
// the versions it was built from.
type eventPlan struct {
	listeners  []*plannedListener
	reached    []planScope // the reached scopes, with their versions read before the plan was built
	extensions uint64      // extensionsVersion, read before the plan was built
}

// planScope is a scope a plan reaches, with the version it was built from.
type planScope struct {
	scope   *Scope
	version planVersion
}

// planVersion is the version of what a scope contributes to a plan.
type planVersion struct {
	services uint64
	funcs    uint64
}

// eventsVersion returns the version of the services and function listeners
// of s.
func (s *Scope) eventsVersion() planVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()

	version := planVersion{services: s.version}
	if state := s.events.Load(); state != nil {
		version.funcs = state.funcsVer
	}

	return version
}

// current reports whether the plan still holds: nothing it was built from
// changed since.
func (p *eventPlan) current(root *RootScope) bool {
	if root.extensionsVersion() != p.extensions {
		return false
	}
	for _, r := range p.reached {
		if r.scope.eventsVersion() != r.version {
			return false
		}
	}

	return true
}

// buildPlan returns the plan of an event of eventType published through
// scope. Versions are read before the listeners, so a change made while it
// is built makes it stale.
func buildPlan(scope *Scope, eventType reflect.Type, local bool) *eventPlan {
	extensions := scope.rootScope.extensionsVersion()
	reached := reachedScopes(scope, local)
	plan := &eventPlan{reached: make([]planScope, len(reached)), extensions: extensions}
	for i, s := range reached {
		plan.reached[i] = planScope{scope: s, version: s.eventsVersion()}
	}

	matched := make([]*plannedListener, 0, len(reached))
	for rank, s := range reached {
		matched = append(matched, scopeListeners(s, rank, eventType)...)
	}
	slices.SortStableFunc(matched, lessListener)
	plan.listeners = matched

	return plan
}

// reachedScopes returns the scopes an event published through scope
// reaches, by rank: scope, its ancestors up to the root, then the private
// scopes of the installed extensions in installation order. A local
// publish reaches scope only.
func reachedScopes(scope *Scope, local bool) []*Scope {
	if local {
		return []*Scope{scope}
	}

	reached := append([]*Scope{scope}, scope.Ancestors()...)
	for _, s := range scope.rootScope.installedExtensionScopes() {
		if !slices.Contains(reached, s) {
			reached = append(reached, s)
		}
	}

	return reached
}

// lessListener orders listeners by ascending ListenerOrder, then reach
// rank, then registration order, then position within a registration.
func lessListener(a, b *plannedListener) int {
	return cmp.Or(
		cmp.Compare(a.opts.order, b.opts.order),
		cmp.Compare(a.rank, b.rank),
		cmp.Compare(a.regIndex, b.regIndex),
		cmp.Compare(a.sub, b.sub),
	)
}

// scopeListeners returns the local listeners of s for eventType, at rank.
func scopeListeners(s *Scope, rank int, eventType reflect.Type) []*plannedListener {
	return scopeListenersWhere(s, rank, func(opts *listenerOptions, typ reflect.Type) bool {
		return eventType.AssignableTo(typ)
	})
}

// scopeListenersWhere returns the listeners of s, at rank, whose options
// and event type keep accepts: service listeners first, then function
// listeners.
func scopeListenersWhere(s *Scope, rank int, keep func(opts *listenerOptions, typ reflect.Type) bool) []*plannedListener {
	var matched []*plannedListener
	s.serviceForEach(func(name string, _ *Scope, serviceAny any) bool {
		service, ok := serviceAny.(serviceWrapperAny)
		if !ok {
			return true
		}
		opts := service.getOptions()
		if opts == nil || len(opts.listeners) == 0 {
			return true
		}
		for index := range opts.listeners {
			binding := &opts.listeners[index]
			if keep(&binding.opts, binding.typ) {
				matched = append(matched, &plannedListener{
					id:       bindingListenerID(s, name, binding),
					scope:    s,
					opts:     &binding.opts,
					rank:     rank,
					regIndex: service.getRegistrationIndex(),
					sub:      index,
					name:     name,
					wrapper:  service,
					binding:  binding,
				})
			}
		}
		return true
	})

	if state := s.events.Load(); state != nil {
		s.mu.RLock()
		funcs := state.funcs
		s.mu.RUnlock()
		for _, listener := range funcs {
			if keep(&listener.opts, listener.typ) {
				matched = append(matched, &plannedListener{id: listener.id, scope: s, opts: &listener.opts, rank: rank, regIndex: listener.regIndex, fn: listener})
			}
		}
	}

	return matched
}
