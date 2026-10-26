package doger

import (
	"context"
	"fmt"
	"slices"
)

var _ Injector = (*virtualScope)(nil)

// newVirtualScope returns a virtual scope over predecessor whose whole
// invoker chain is invokerChain.
func newVirtualScope(predecessor Injector, invokerChain []string) *virtualScope {
	if predecessor == nil {
		panic("DI: predecessor cannot be nil")
	}
	if len(invokerChain) == 0 {
		panic("DI: invokerChain cannot be empty")
	}

	return &virtualScope{
		self: predecessor,
		base: invokerChain,
	}
}

// virtualScope is a simple wrapper to Injector (Scope or RootScope or virtualScope) that
// contains the invoker name.
// It is used to track the dependency graph.
//
// The virtualScope acts as a proxy that wraps an existing injector and tracks the chain
// of service invocations. This is essential for:
//   - Detecting circular dependencies during service resolution
//   - Building the dependency graph (DAG) for proper shutdown ordering
//   - Providing debugging information about service invocation chains
//
// Fields:
//   - self: The underlying injector being wrapped
//   - name, parent, base: The chain of service names that have been invoked,
//     used for circular dependency detection: name follows the chain of
//     parent, unless base holds the whole chain
//
// The chain is not copied into each virtual scope: an invocation allocates
// one virtual scope, and a stored injector keeps O(depth) memory alive.
type virtualScope struct {
	self   Injector
	name   string        // the service this invocation builds; unused when base is set
	parent *virtualScope // the virtual scope of the invoking provider, if any
	base   []string      // the whole invoker chain, for a scope not built by an invocation
}

// newChildVirtualScope returns the virtual scope of an invocation of name
// made through parent (nil outside a provider): its chain is parent's,
// followed by name.
func newChildVirtualScope(parent *virtualScope, predecessor Injector, name string) *virtualScope {
	return &virtualScope{self: predecessor, name: name, parent: parent}
}

// invokerChain returns a copy of the invoker chain, outermost first.
func (s *virtualScope) invokerChain() []string {
	var names []string
	v := s
	for ; v != nil && v.base == nil; v = v.parent {
		names = append(names, v.name)
	}
	slices.Reverse(names)
	if v != nil {
		names = slices.Concat(v.base, names)
	}

	return names
}

// chainTo returns the invoker chain of vScope (nil outside a provider)
// followed by name, for error messages.
func chainTo(vScope *virtualScope, name string) []string {
	if vScope == nil {
		return []string{name}
	}

	return append(vScope.invokerChain(), name)
}

// pass through
func (s *virtualScope) ID() string                                 { return s.self.ID() }
func (s *virtualScope) Name() string                               { return s.self.Name() }
func (s *virtualScope) Scope(n string, p ...func(Injector)) *Scope { return s.self.Scope(n, p...) }
func (s *virtualScope) RootScope() *RootScope                      { return s.self.RootScope() }
func (s *virtualScope) Ancestors() []*Scope                        { return s.self.Ancestors() }
func (s *virtualScope) Children() []*Scope                         { return s.self.Children() }
func (s *virtualScope) ChildByID(id string) (*Scope, bool)         { return s.self.ChildByID(id) }
func (s *virtualScope) ChildByName(name string) (*Scope, bool)     { return s.self.ChildByName(name) }

func (s *virtualScope) ListProvidedServices() []ServiceDescription {
	return s.self.ListProvidedServices()
}

func (s *virtualScope) ListInvokedServices() []ServiceDescription {
	return s.self.ListInvokedServices()
}
func (s *virtualScope) HealthCheck() map[string]error { return s.self.HealthCheck() }
func (s *virtualScope) HealthCheckWithContext(ctx context.Context) map[string]error {
	return s.self.HealthCheckWithContext(ctx)
}
func (s *virtualScope) Shutdown() *ShutdownReport { return s.self.Shutdown() }
func (s *virtualScope) ShutdownWithContext(ctx context.Context) *ShutdownReport {
	return s.self.ShutdownWithContext(ctx)
}

func (s *virtualScope) clone(r *RootScope, p *Scope) *Scope { return s.self.clone(r, p) }

func (s *virtualScope) serviceExist(name string) bool { return s.self.serviceExist(name) }

func (s *virtualScope) serviceExistRec(name string) bool { return s.self.serviceExistRec(name) }

func (s *virtualScope) serviceGet(name string) (any, bool) { return s.self.serviceGet(name) }

func (s *virtualScope) serviceGetRec(n string) (any, *Scope, bool) { return s.self.serviceGetRec(n) }

func (s *virtualScope) serviceSet(name string, service any) { s.self.serviceSet(name, service) } // serviceSet is not protected against double registration.

func (s *virtualScope) serviceForEach(cb func(string, *Scope, any) bool) { s.self.serviceForEach(cb) }

func (s *virtualScope) serviceForEachRec(cb func(string, *Scope, any) bool) {
	s.self.serviceForEachRec(cb)
}

func (s *virtualScope) serviceHealthCheck(ctx context.Context, n string) error {
	return s.self.serviceHealthCheck(ctx, n)
}

func (s *virtualScope) serviceShutdown(ctx context.Context, name string) error {
	return s.self.serviceShutdown(ctx, name)
}
func (s *virtualScope) onServiceInvoke(name string) { s.self.onServiceInvoke(name) }

// detectCircularDependency checks for circular dependencies in the virtualScope.
// This method analyzes the current invoker chain to detect if adding the specified service
// would create a circular dependency. A circular dependency occurs when service A depends
// on service B, which depends on service A (directly or indirectly).
//
// Parameters:
//   - name: The name of the service being invoked
//
// Returns ErrCircularDependency if a circular dependency is detected, or nil if the
// dependency is valid.
//
// Example of circular dependency:
//
//	ServiceA depends on ServiceB
//	ServiceB depends on ServiceC
//	ServiceC depends on ServiceA  // This creates a circular dependency
//
// The method checks if the service, its name registered in scope, already
// exists in the invoker chain, which would indicate a circular dependency. A
// service with the same name in another scope is a different service.
func (s *virtualScope) detectCircularDependency(name string, scope *Scope) error {
	if s.chainContains(name, scope) {
		return fmt.Errorf("%w: %s", ErrCircularDependency, humanReadableInvokerChain(chainTo(s, name)))
	}
	return nil
}

// chainContains reports whether the service name of scope is in the invoker
// chain, without building it. A base chain holds names only, so any scope
// matches it.
func (s *virtualScope) chainContains(name string, scope *Scope) bool {
	for v := s; v != nil; v = v.parent {
		if v.base != nil {
			return contains(v.base, name)
		}
		if v.name == name && injectorScope(v.self) == scope {
			return true
		}
	}

	return false
}

// addDependency adds a dependency to the DAG in the virtualScope.
// This method records the dependency relationship between the current service (the last
// invoker in the chain) and the service being invoked. This information is used to
// build the dependency graph for proper shutdown ordering.
//
// Parameters:
//   - injector: The injector containing the services
//   - name: The name of the service being invoked (dependency)
//   - serviceScope: The scope containing the service being invoked
//   - source: How the edge was recorded
//
// The dependency is recorded as: current service -> invoked service
// This ensures that during shutdown, dependencies are shut down before the services
// that depend on them.
func (s *virtualScope) addDependency(injector Injector, name string, serviceScope *Scope, source DependencySource) {
	last, ok := s.getLastInvokerName()
	if !ok {
		// This should never happen, but we'll handle it gracefully
		return
	}

	injector.RootScope().dag.addDependency(injector.ID(), injector.Name(), last, serviceScope.ID(), serviceScope.Name(), name, source)
}

// getLastInvokerName retrieves the last invoker name from the invoker chain in the virtualScope.
// This method returns the name of the service that is currently being resolved and is
// about to invoke another service. This is used for building dependency relationships
// in the DAG.
//
// Returns the name of the last service in the invoker chain, or an empty string if
// the chain is empty.
//
// The invoker chain represents the path of service invocations, where each service
// in the chain depends on the next service in the chain.
func (s *virtualScope) getLastInvokerName() (output string, ok bool) {
	if s.base == nil {
		return s.name, true
	}
	if len(s.base) > 0 {
		return s.base[len(s.base)-1], true
	}

	// This should never happen, but we'll handle it gracefully
	return "", false
}
