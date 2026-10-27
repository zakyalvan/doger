package doger

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kartaladev/doger/stacktrace"
)

var (
	_ serviceWrapper[int]       = (*serviceDefault[int])(nil)
	_ serviceWrapperHealthcheck = (*serviceDefault[int])(nil)
	_ serviceWrapperShutdown    = (*serviceDefault[int])(nil)
	_ serviceWrapperClone       = (*serviceDefault[int])(nil)
	_ serviceWrapperBuildTime   = (*serviceDefault[int])(nil)
)

// defaultState is the decision of a default registration.
type defaultState int32

const (
	defaultUnresolved defaultState = iota // not resolved yet
	defaultChosen                         // uses its own provider or value
	defaultYielded                        // resolves to a non-default service of an ancestor
	defaultSuperseded                     // replaced in its scope by an explicit registration
)

// serviceWrapperDefault is implemented by the wrappers of default registrations.
type serviceWrapperDefault interface {
	decide() defaultState
	currentState() defaultState  // the state, without deciding
	yieldedTo() (*Scope, string) // the scope and name of the target, once yielded
}

// yieldedTo returns the scope and the name of the service the default
// yielded to; the scope is nil unless the default yielded.
func (s *serviceDefault[T]) yieldedTo() (*Scope, string) {
	if s.currentState() != defaultYielded {
		return nil, ""
	}

	return s.targetScope, s.getName()
}

// currentState returns the state of the default without deciding it.
func (s *serviceDefault[T]) currentState() defaultState {
	return defaultState(s.state.Load())
}

// defaultStateOf reports whether service is a default registration, and its
// state without deciding it.
func defaultStateOf(service any) (isDefault bool, state defaultState) {
	defaultService, ok := service.(serviceWrapperDefault)
	if !ok {
		return false, defaultUnresolved
	}

	return true, defaultService.currentState()
}

// hidesNonDefault reports whether service, registered in a nearer scope,
// hides a non-default service of the same name: a default hides it only
// once it chose its own provider, as by-name resolution would.
func hidesNonDefault(service any) bool {
	isDefault, state := defaultStateOf(service)
	return !isDefault || state == defaultChosen
}

// chainHasDefaults reports whether scope or one of its ancestors holds a
// default registration, so that containers without defaults skip their rules.
func chainHasDefaults(scope *Scope) bool {
	for ; scope != nil; scope = scope.parentScope {
		if scope.defaultCount() > 0 {
			return true
		}
	}

	return false
}

// isYieldedDefaultIn is isYieldedDefault for a service of scope, skipped
// when the scope holds no default.
func isYieldedDefaultIn(scope *Scope, service any) bool {
	return scope.defaultCount() > 0 && isYieldedDefault(service)
}

// isYieldedDefault reports whether service is a default registration that
// yields, deciding it if it is unresolved. Deciding builds nothing.
func isYieldedDefault(service any) bool {
	defaultService, ok := service.(serviceWrapperDefault)
	return ok && defaultService.decide() == defaultYielded
}

// serviceWrapperSupersede is implemented by the wrappers of default
// registrations, which an explicit registration may replace.
type serviceWrapperSupersede interface {
	supersede(store func()) bool
}

// serviceDefault decorates the lazy, eager or transient wrapper of a
// registration given AsDefault. On its first resolution it decides, once,
// whether it uses its own provider or yields to a non-default service of the
// same name in an ancestor of its scope.
type serviceDefault[T any] struct {
	inner serviceWrapper[T]
	scope *Scope // the scope holding the registration

	mu          sync.Mutex   // serializes the decision
	state       atomic.Int32 // a defaultState; target fields are set before it turns yielded
	target      any          // the wrapper yielded to
	targetScope *Scope       // the scope of target
}

func newServiceDefault[T any](inner serviceWrapper[T], scope *Scope) *serviceDefault[T] {
	return &serviceDefault[T]{inner: inner, scope: scope}
}

// decide returns the decision of the default, making it on the first call:
// it yields when a scope above its own holds a non-default service of its
// name, and uses its own provider otherwise. Later calls take no lock.
func (s *serviceDefault[T]) decide() defaultState {
	if state := defaultState(s.state.Load()); state != defaultUnresolved {
		return state
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if state := defaultState(s.state.Load()); state != defaultUnresolved {
		return state
	}

	if target, targetScope, ok := lookupNonDefaultAbove(s.scope, s.getName()); ok {
		s.target, s.targetScope = target, targetScope
		s.state.Store(int32(defaultYielded))
		return defaultYielded
	}

	s.state.Store(int32(defaultChosen))
	return defaultChosen
}

// supersede runs store, which replaces the default in its scope, and marks it
// superseded, if the default is still unresolved. It reports whether it did.
// The caller holds the scope's write lock, so a reader that finds the
// default superseded and resolves its name again finds the replacement.
func (s *serviceDefault[T]) supersede(store func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if defaultState(s.state.Load()) != defaultUnresolved {
		return false
	}

	store()
	s.state.Store(int32(defaultSuperseded))
	return true
}

// lookupNonDefaultAbove returns the nearest service named name, and its
// scope, in an ancestor of scope, skipping default registrations. An alias
// counts as a non-default service.
func lookupNonDefaultAbove(scope *Scope, name string) (any, *Scope, bool) {
	for ancestor := scope.parentScope; ancestor != nil; ancestor = ancestor.parentScope {
		service, ok := ancestor.serviceGet(name)
		if !ok {
			continue
		}
		if _, isDefault := service.(serviceWrapperDefault); isDefault {
			continue
		}

		return service, ancestor, true
	}

	return nil, nil, false
}

func (s *serviceDefault[T]) getName() string              { return s.inner.getName() }
func (s *serviceDefault[T]) getTypeName() string          { return s.inner.getTypeName() }
func (s *serviceDefault[T]) getServiceType() ServiceType  { return s.inner.getServiceType() }
func (s *serviceDefault[T]) getReflectType() reflect.Type { return s.inner.getReflectType() }

func (s *serviceDefault[T]) getInstanceAny(i Injector) (any, error) {
	return s.getInstance(i)
}

func (s *serviceDefault[T]) getInstance(i Injector) (T, error) {
	switch s.decide() {
	case defaultUnresolved, defaultChosen:
		return s.inner.getInstance(i)
	case defaultSuperseded:
		return s.replacementInstance(i)
	case defaultYielded:
	}

	instance, err := invokeWrapperInScope(i, s.targetScope, s.getName(), s.target)
	if err != nil {
		return empty[T](), err
	}

	typed, ok := instance.(T)
	if !ok && instance != nil {
		return empty[T](), serviceTypeMismatch(inferServiceName[T](), s.target.(serviceWrapperAny).getTypeName()) //nolint:errcheck,forcetypeassert
	}

	return typed, nil
}

// replacementInstance resolves the registration that replaced the default
// in its scope, for a reader that found the default before it was replaced.
// The replacement has the default's name and scope, so the reader's hooks,
// cycle check and virtual scope apply to it unchanged.
func (s *serviceDefault[T]) replacementInstance(i Injector) (T, error) {
	name := s.getName()
	current, ok := s.scope.serviceGet(name)
	if !ok {
		return empty[T](), serviceNotFound(s.scope, ErrServiceNotFound, []string{name})
	}

	replacement, ok := current.(serviceWrapper[T])
	if !ok {
		return empty[T](), serviceTypeMismatch(inferServiceName[T](), current.(serviceWrapperAny).getTypeName()) //nolint:errcheck,forcetypeassert
	}

	return replacement.getInstance(i)
}

// invokeWrapperInScope invokes the wrapper service, registered as name in
// scope, directly rather than by name from i: with scope's invocation hooks,
// a child virtual scope of i, and a dependency edge from i's invoker to it.
// Cycle detection compares names and scopes, so a hop to a same-name service
// of another scope is not a cycle.
func invokeWrapperInScope(i Injector, scope *Scope, name string, service any) (any, error) {
	vScope, _ := i.(*virtualScope)
	return invokeServiceInstance(i, vScope, name, name, scope, service)
}

// isHealthchecker is false once the default yielded: its target is checked
// through its own registration.
func (s *serviceDefault[T]) isHealthchecker() bool {
	if defaultState(s.state.Load()) == defaultYielded {
		return false
	}

	return s.inner.isHealthchecker()
}

func (s *serviceDefault[T]) healthcheck(ctx context.Context) error {
	if defaultState(s.state.Load()) == defaultYielded {
		return nil
	}

	return s.inner.healthcheck(ctx)
}

// isShutdowner is false once the default yielded or was superseded. It never
// decides: introspection, such as ExplainInjector, must leave an unresolved
// default replaceable; only shutdown decides an unresolved eager default.
func (s *serviceDefault[T]) isShutdowner() bool {
	if state := s.currentState(); state == defaultYielded || state == defaultSuperseded {
		return false
	}

	return s.inner.isShutdowner()
}

// shutdown shuts the inner instance down only when the default uses it: a
// yielded or superseded value belongs to the code that created it.
func (s *serviceDefault[T]) shutdown(ctx context.Context) error {
	if !s.ownsInstance() {
		return nil
	}

	return s.inner.shutdown(ctx)
}

// ownsInstance reports whether the container owns the inner instance, that
// is the default did not yield and was not superseded. It decides an
// unresolved eager default, whose value exists already.
func (s *serviceDefault[T]) ownsInstance() bool {
	state := defaultState(s.state.Load())
	if state == defaultUnresolved && s.inner.getServiceType() == ServiceTypeEager {
		state = s.decide()
	}

	return state != defaultYielded && state != defaultSuperseded
}

func (s *serviceDefault[T]) clone(newScope Injector) any {
	return newServiceDefault(s.inner.clone(newScope).(serviceWrapper[T]), injectorScope(newScope)) //nolint:errcheck,forcetypeassert
}

func (s *serviceDefault[T]) source() (stacktrace.Frame, []stacktrace.Frame) {
	return s.inner.source()
}

// getBuildTime forwards to the inner wrapper; a wrapper without a build time
// reports none, as if the method were missing.
func (s *serviceDefault[T]) getBuildTime() (time.Duration, bool) {
	if timed, ok := s.inner.(serviceWrapperBuildTime); ok {
		return timed.getBuildTime()
	}

	return 0, false
}

func (s *serviceDefault[T]) getRegistrationIndex() uint64 { return s.inner.getRegistrationIndex() }

func (s *serviceDefault[T]) setRegistrationIndex(index uint64) {
	s.inner.setRegistrationIndex(index)
}

func (s *serviceDefault[T]) getOptions() *serviceOptions { return s.inner.getOptions() }

func (s *serviceDefault[T]) setOptions(opts *serviceOptions) { s.inner.setOptions(opts) }
