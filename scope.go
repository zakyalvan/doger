package doger

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"
)

// newScope creates a new Scope instance with the provided parameters.
// This is an internal function used by the scope creation methods.
//
// Parameters:
//   - name: The human-readable name of the scope
//   - root: The root scope that this scope belongs to
//   - parent: The immediate parent scope, or nil for the root scope
//
// Returns a fully initialized Scope with all internal fields set to their default values.
func newScope(name string, root *RootScope, parent *Scope) *Scope {
	s := &Scope{}
	initScope(s, name, root, parent)

	return s
}

// initScope initializes the zero Scope s in place, so that a RootScope can
// hold its own scope in the same allocation.
func initScope(s *Scope, name string, root *RootScope, parent *Scope) {
	s.id = must1(newUUID())
	s.name = name
	s.rootScope = root
	s.parentScope = parent
	s.childScopes = map[string]*Scope{}
	s.services = make(map[string]any)
	s.orderedInvocation = map[string]int{}
}

// Ensure Scope implements the Injector interface at compile time.
var _ Injector = (*Scope)(nil)

// Scope represents a dependency injection container that can contain services
// and child scopes. Scopes form a hierarchical tree structure where child scopes
// can access services from their parent scopes, but not vice versa.
//
// Key features:
//   - Hierarchical service resolution (child scopes can access parent services)
//   - Isolated service registration (services in child scopes don't affect parents)
//   - Thread-safe operations
//   - Service lifecycle management (health checks, shutdown)
//   - Observability and debugging support
type Scope struct {
	id          string            // Unique identifier for the scope (immutable)
	name        string            // Human-readable name for the scope (immutable)
	rootScope   *RootScope        // Reference to the root scope (immutable)
	parentScope *Scope            // Reference to the immediate parent scope (immutable)
	childScopes map[string]*Scope // Map of child scopes (append only)

	mu       sync.RWMutex   // Mutex for thread-safe operations
	services map[string]any // Map of registered services

	// typeMatches caches, per target type, the services of this scope whose
	// registered type is assignable to it; reset whenever services change.
	// Guarded by mu.
	typeMatches map[reflect.Type][]typeMatch

	// Storing the invocation order is not needed anymore, but we keep it
	// for improved observability in unit tests.
	orderedInvocation      map[string]int // Map tracking service invocation order (faster than slice)
	orderedInvocationIndex int            // Counter for tracking invocation order

	// closing tracks the shutdowns of this child scope; nil until the first
	// one. Guarded by rootScope.lifecycleMu. It is a pointer so that the
	// scope, which the root container embeds, keeps its size: parallel
	// invocations are sensitive to the container's allocation layout.
	closing *scopeClosing

	// defaults counts the default registrations (AsDefault) in services, and
	// exports the exports (Export); both written under mu. They share one
	// word: growing Scope grows RootScope, which embeds it, so keep RootScope
	// in its allocation size class (see the phase fields there).
	defaults atomic.Int32
	exports  atomic.Int32
}

// scopeClosing is the shutdown state of a child scope.
type scopeClosing struct {
	count int            // shutdowns of the scope in progress
	run   *scopeCloseRun // the running shutdown that others join, or nil
}

// ID returns the unique identifier of the scope.
// This ID is generated using UUID and is immutable throughout the scope's lifetime.
//
// Play: https://go.dev/play/p/pao_03UdGE2
func (s *Scope) ID() string {
	return s.id
}

// Name returns the human-readable name of the scope.
// This name is provided when creating the scope and is immutable.
//
// Play: https://go.dev/play/p/pao_03UdGE2
func (s *Scope) Name() string {
	return s.name
}

// Scope creates a new child scope with the given name.
// Child scopes inherit access to services from their parent scopes,
// but services registered in child scopes are not accessible to parents.
//
// Parameters:
//   - name: The name for the new child scope (must be unique within the parent)
//   - packages: Optional package functions to execute in the new scope
//
// Returns the newly created child scope.
//
// Play: https://go.dev/play/p/pao_03UdGE2
//
// Panics if a scope with the same name already exists in the parent.
func (s *Scope) Scope(name string, packages ...func(Injector)) *Scope {
	s.mu.Lock()

	if _, ok := s.childScopes[name]; ok {
		s.mu.Unlock()
		panic(fmt.Errorf("DI: scope `%s` has already been declared", name))
	}

	child := newScope(name, s.rootScope, s)
	s.childScopes[name] = child

	s.mu.Unlock()

	// Execute any package functions in the new scope
	for _, pkg := range packages {
		pkg(child)
	}

	return child
}

// RootScope returns the root scope of the scope hierarchy.
// All scopes in a hierarchy share the same root scope, regardless of their depth.
//
// Play: https://go.dev/play/p/pao_03UdGE2
func (s *Scope) RootScope() *RootScope {
	return s.rootScope
}

// clock returns the time source of the container.
func (s *Scope) clock() clockwork.Clock {
	return s.rootScope.opts.Clock
}

// Ancestors returns the list of all parent scopes in order from immediate parent to root.
// This is useful for understanding the scope hierarchy and for operations that need
// to traverse up the scope tree.
//
// Returns an empty slice for the root scope, and a slice of parent scopes
// for child scopes, ordered from immediate parent to root.
//
// Play: https://go.dev/play/p/e_oxd7b-q9h
func (s *Scope) Ancestors() []*Scope {
	// parentScope is immutable, so the chain can be walked without locking.
	// This reaches into the unexported parentScope field of other *Scope
	// instances (not just s), which breaks normal encapsulation, but it lets
	// the whole chain be walked iteratively with one right-sized allocation,
	// avoiding the O(n) allocations and O(n^2) total element copies/bytes of
	// the previous recursive, method-based traversal.
	ancestors := make([]*Scope, scopeDepth(s))
	for i, p := 0, s.parentScope; p != nil; i, p = i+1, p.parentScope {
		ancestors[i] = p
	}

	return ancestors
}

// Children returns the list of immediate child scopes.
// This method only returns direct children, not grandchildren or deeper descendants.
//
// Returns a slice of child scopes. The order is not guaranteed to be stable.
//
// Play: https://go.dev/play/p/pao_03UdGE2
func (s *Scope) Children() []*Scope {
	s.mu.RLock()
	defer s.mu.RUnlock()

	scopes := make([]*Scope, 0, len(s.childScopes))
	for _, scope := range s.childScopes {
		scopes = append(scopes, scope)
	}

	return scopes
}

// ChildByID searches for a child scope by its unique ID across the entire scope hierarchy.
// This method performs a recursive search through all descendant scopes.
//
// Parameters:
//   - id: The unique ID of the scope to find
//
// Returns the found scope and true if found, or nil and false if not found.
//
// Play: https://go.dev/play/p/pao_03UdGE2
func (s *Scope) ChildByID(id string) (*Scope, bool) {
	s.mu.RLock()
	children := make([]*Scope, 0, len(s.childScopes))
	for _, c := range s.childScopes {
		children = append(children, c)
	}
	s.mu.RUnlock()

	for _, scope := range children {
		if scope.id == id {
			return scope, true
		}

		if child, ok := scope.ChildByID(id); ok {
			return child, true
		}
	}

	return nil, false
}

// ChildByName searches for a child scope by its name across the entire scope hierarchy.
// This method performs a recursive search through all descendant scopes.
//
// Parameters:
//   - name: The name of the scope to find
//
// Returns the found scope and true if found, or nil and false if not found.
//
// Play: https://go.dev/play/p/pao_03UdGE2
func (s *Scope) ChildByName(name string) (*Scope, bool) {
	s.mu.RLock()
	children := make([]*Scope, 0, len(s.childScopes))
	for _, c := range s.childScopes {
		children = append(children, c)
	}
	s.mu.RUnlock()

	for _, scope := range children {
		if scope.name == name {
			return scope, true
		}

		if child, ok := scope.ChildByName(name); ok {
			return child, true
		}
	}

	return nil, false
}

// ListProvidedServices returns all services available in the current scope and all its ancestor scopes.
// This provides a complete view of the service hierarchy, showing all services
// that can be accessed from the current scope.
//
// Returns a slice of ServiceDescription objects representing all available services,
// including those inherited from parent scopes.
//
// Play: https://go.dev/play/p/e_oxd7b-q9h
func (s *Scope) ListProvidedServices() []ServiceDescription {
	services := []ServiceDescription{}

	// Add services from current scope
	s.mu.RLock()
	for name := range s.services {
		services = append(services, newServiceDescription(s.id, s.name, name))
	}
	s.mu.RUnlock()

	// Add services from ancestor scopes
	for _, ancestor := range s.Ancestors() {
		services = append(services, ancestor.ListProvidedServices()...)
	}

	services = orderedUniq(services)

	s.logf("exported list of services: %v", services)

	return services
}

// ListInvokedServices returns only the services that have been actually invoked
// (instantiated) in the current scope and its ancestors.
// This is useful for understanding which services are actually being used
// and for debugging dependency issues.
//
// Returns a slice of ServiceDescription objects representing only the invoked services.
//
// Play: https://go.dev/play/p/pJcJGOF5zeK
func (s *Scope) ListInvokedServices() []ServiceDescription {
	services := []ServiceDescription{}

	// Add invoked services from current scope
	s.mu.RLock()
	for name := range s.orderedInvocation {
		services = append(services, newServiceDescription(s.id, s.name, name))
	}
	s.mu.RUnlock()

	// Add invoked services from ancestor scopes
	for _, ancestor := range s.Ancestors() {
		services = append(services, ancestor.ListInvokedServices()...)
	}

	services = orderedUniq(services)

	s.logf("exported list of invoked services: %v", services)

	return services
}

// HealthCheck performs health checks on all services in the current scope and its ancestors
// that implement the Healthchecker interface.
//
// Returns a map of service names to error values. A nil error indicates a successful health check.
//
// Play: https://go.dev/play/p/pJcJGOF5zeK
func (s *Scope) HealthCheck() map[string]error {
	return s.HealthCheckWithContext(context.Background())
}

// HealthCheckWithContext performs health checks on all services in the current scope and its ancestors
// that implement the Healthchecker interface, with context support for cancellation and timeouts.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns a map of service names to error values. A nil error indicates a successful health check.
//
// Play: https://go.dev/play/p/pJcJGOF5zeK
func (s *Scope) HealthCheckWithContext(ctx context.Context) map[string]error {
	s.logf("requested health check")

	if s.rootScope.opts.HealthCheckGlobalTimeout > 0 {
		clock := s.clock()
		ctx = withHealthCheckDeadline(ctx, clock.Now().Add(s.rootScope.opts.HealthCheckGlobalTimeout))
		var cancel context.CancelFunc
		ctx, cancel = clockwork.WithTimeout(ctx, clock, s.rootScope.opts.HealthCheckGlobalTimeout)
		defer cancel()
	}

	results := map[string]error{}

	asyncResults := s.asyncHealthCheckWithContext(ctx, s)
	for name, err := range asyncResults {
		results[name] = <-err
	}

	s.logf("got health check results: %v", results)

	return results
}

// asyncHealthCheckWithContext queues the health checks of the services of s
// and its ancestors, for a health check requested from requester: an export
// is skipped when requester is its source scope or a descendant of it, where
// the exported service is checked under its own name.
func (s *Scope) asyncHealthCheckWithContext(ctx context.Context, requester *Scope) map[string]<-chan error {
	asyncResults := map[string]<-chan error{}

	s.mu.RLock()
	for name, service := range s.services {
		// a default that yields is not checked here: the name keeps the
		// result of the ancestor service it resolves to
		if isYieldedDefaultIn(s, service) {
			continue
		}
		if s.exportCount() > 0 && readerSeesSource(requester, service) {
			continue
		}
		asyncResults[name] = s.rootScope.queueServiceHealthcheck(ctx, s, name)
	}
	s.mu.RUnlock()

	// @TODO: We should not check the status of services that are not inherited (overridden in a child tree)
	for _, ancestor := range s.Ancestors() {
		health := ancestor.asyncHealthCheckWithContext(ctx, requester)
		for name, err := range health {
			if _, ok := asyncResults[name]; !ok {
				asyncResults[name] = err
			}
		}
	}

	return asyncResults
}

// Shutdown gracefully shuts down the scope and all its children.
// This method calls ShutdownWithContext with a background context.
//
// Returns a ShutdownReport containing any errors and timings that occurred during shutdown.
//
// Play: https://go.dev/play/p/nkiBBYow2d5
func (s *Scope) Shutdown() *ShutdownReport {
	return s.ShutdownWithContext(context.Background())
}

// ShutdownWithContext gracefully shuts down the scope and all its children with context support.
// This method performs shutdown operations in parallel for better performance.
//
// On a child scope, it first waits, without cancelling it, for a running
// Start, Refresh or Scope.Start that is starting services of this scope, and
// a run that begins while the scope shuts down skips it and its descendants.
// If ctx ends first, the report fails with an error keyed by this scope with
// an empty service name, wrapping ctx.Err(), and the shutdown proceeds. Never
// shut down, synchronously from code running in such a start, a scope that
// the start is starting: it would wait for itself.
//
// A child scope is shut down once at a time: a call that finds a shutdown of
// it running, its own or root Shutdown's, waits for that shutdown and
// returns a copy of its report, so services and scope hooks run once. If
// ctx ends first, the report fails with an error keyed by this scope with an
// empty service name, wrapping ctx.Err(), and the running shutdown goes on.
// If the running shutdown panics before producing a report (for example in a
// custom Logf), the joined calls get a failed report containing "DI: scope
// shutdown did not complete". Never shut a scope down synchronously from its
// own Closing or Closed hooks, or from its services' Shutdown: it would wait
// for itself.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns a ShutdownReport containing any errors and timings that occurred during shutdown.
//
// Play: https://go.dev/play/p/nkiBBYow2d5
func (s *Scope) ShutdownWithContext(ctx context.Context) *ShutdownReport {
	if ctx == nil {
		ctx = context.Background()
	}

	var first hookPanic
	report := s.shutdownWithContext(ctx, &first)
	s.rootScope.dispatchFirstPanic(first)

	return report
}

// shutdownWithContext shuts the scope and its children down, keeping the
// first scope hook panic in first instead of raising it.
func (s *Scope) shutdownWithContext(ctx context.Context, first *hookPanic) (report *ShutdownReport) {
	s.logf("requested shutdown")
	start := s.clock().Now()

	// child scopes only: the root's own scope fires no scope hook and follows
	// root Shutdown's model
	isChild := s.parentScope != nil

	var waitErr error
	if isChild {
		defer s.endClosing()
		run, winner := s.joinClose()
		if !winner {
			return s.waitForScopeClose(ctx, run)
		}
		defer func() { s.finishScopeClose(run, report) }()
		waitErr = s.waitForStartingRun(ctx)
	}

	if s.exportCount() > 0 {
		report = s.shutdownWithExports(ctx, first, isChild)
	} else {
		rep1 := s.shutdownChildrenInParallel(ctx, first, func(*Scope) bool { return true })

		if isChild {
			s.runScopeObserverHooks(ctx, "scope-closing", s.rootScope.opts.HookScopeClosing, first)
		}

		rep2 := s.shutdownServicesInParallel(ctx)
		s.logf("shut down services")

		if isChild {
			s.runScopeObserverHooks(ctx, "scope-closed", s.rootScope.opts.HookScopeClosed, first)
		}

		report = mergeShutdownReports(rep1, rep2)
	}
	if waitErr != nil {
		report.Errors[newServiceDescription(s.id, s.name, "")] = waitErr
	}
	report.ShutdownTime = s.clock().Since(start)
	report.Succeed = len(report.Errors) == 0
	return report
}

// scopeCloseRun is a running shutdown of a child scope: concurrent
// shutdowns of the scope wait for it instead of walking the scope again.
type scopeCloseRun struct {
	done   chan struct{}
	report *ShutdownReport // set before done is closed
}

// joinClose marks the child scope as shutting down, so a Start, Refresh or
// Scope.Start beginning now skips it, and returns its running shutdown,
// which this call runs when winner is true. Each call must be paired with
// endClosing.
func (s *Scope) joinClose() (run *scopeCloseRun, winner bool) {
	root := s.rootScope
	root.lifecycleMu.Lock()
	defer root.lifecycleMu.Unlock()

	if s.closing == nil {
		s.closing = &scopeClosing{}
	}
	s.closing.count++
	if s.closing.run != nil {
		return s.closing.run, false
	}
	s.closing.run = &scopeCloseRun{done: make(chan struct{})}

	return s.closing.run, true
}

// finishScopeClose publishes the report of the winning shutdown and releases
// the shutdowns that joined it. A nil report means the winner panicked
// before producing one, for example in a custom Logf.
func (s *Scope) finishScopeClose(run *scopeCloseRun, report *ShutdownReport) {
	if report == nil {
		report = s.failureReport(errors.New("DI: scope shutdown did not complete"))
	}

	root := s.rootScope
	root.lifecycleMu.Lock()
	defer root.lifecycleMu.Unlock()

	run.report = report
	s.closing.run = nil
	close(run.done)
}

// waitForScopeClose waits, bounded by ctx, for the shutdown a concurrent
// call is running, and returns a copy of its report.
func (s *Scope) waitForScopeClose(ctx context.Context, run *scopeCloseRun) *ShutdownReport {
	if awaitDone(run.done, ctx) {
		s.rootScope.lifecycleMu.Lock()
		defer s.rootScope.lifecycleMu.Unlock()
		return cloneShutdownReport(run.report)
	}

	return s.failureReport(fmt.Errorf("DI: waiting for scope shutdown: %w", ctx.Err()))
}

// failureReport returns a failed report whose only error, err, is keyed by
// the scope with an empty service name.
func (s *Scope) failureReport(err error) *ShutdownReport {
	return &ShutdownReport{
		Succeed:             false,
		Services:            []ServiceDescription{},
		Errors:              map[ServiceDescription]error{newServiceDescription(s.id, s.name, ""): err},
		ServiceShutdownTime: map[ServiceDescription]time.Duration{},
	}
}

// waitForStartingRun waits, bounded by ctx, for a Start, Refresh or
// Scope.Start that is running with candidates in this scope. It returns an
// error when ctx ended first.
func (s *Scope) waitForStartingRun(ctx context.Context) error {
	root := s.rootScope
	root.lifecycleMu.Lock()
	_, done, inFlight := root.inFlightRun()
	_, starting := root.runScopes[s]
	root.lifecycleMu.Unlock()

	if inFlight && starting && !awaitDone(done, ctx) {
		return fmt.Errorf("DI: waiting for start: %w", ctx.Err())
	}

	return nil
}

// endClosing ends the shutdown that joinClose marked.
func (s *Scope) endClosing() {
	s.rootScope.lifecycleMu.Lock()
	defer s.rootScope.lifecycleMu.Unlock()

	s.closing.count--
}

// runScopeObserverHooks runs scope observer hooks for s in order, recovering
// their panics into first.
func (s *Scope) runScopeObserverHooks(ctx context.Context, point string, hooks []func(context.Context, *Scope), first *hookPanic) {
	runObserverHooksWith(s.rootScope, "scope", point, hooks, first, func(hook func(context.Context, *Scope)) { hook(ctx, s) })
}

// shutdownChildrenInParallel runs a parallel shutdown of the selected
// children scopes. This method removes them from the scope hierarchy, then
// shuts them down concurrently.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - selected: Reports whether a child scope is shut down by this call
//
// Returns a ShutdownReport containing any errors from child scope shutdowns.
func (s *Scope) shutdownChildrenInParallel(ctx context.Context, first *hookPanic, selected func(*Scope) bool) *ShutdownReport {
	// Snapshot children under lock
	s.mu.Lock()
	children := make([]*Scope, 0, len(s.childScopes))
	for name, c := range s.childScopes {
		if selected(c) {
			children = append(children, c)
			delete(s.childScopes, name) // scopes are removed from DI container
		}
	}
	s.mu.Unlock()

	reports := make([]*ShutdownReport, len(children))
	panics := make([]hookPanic, len(children))

	var wg sync.WaitGroup
	for index, child := range children {
		wg.Go(func() {
			reports[index] = child.shutdownWithContext(ctx, &panics[index])
		})
	}
	wg.Wait()

	// raised on the caller's goroutine, never on a child's
	for _, p := range panics {
		s.rootScope.keepFirstPanic(first, p)
	}

	return mergeShutdownReports(reports...)
}

// shutdownServicesInParallel runs a parallel shutdown of scope services.
// This method implements a dependency-aware shutdown algorithm that shuts down
// services in the correct order to avoid dependency issues.
//
// We look for services having no dependents. Then we shutdown them.
// And repeat, until every scope services have been shutdown.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns a ShutdownReport containing any errors from service shutdowns.
func (s *Scope) shutdownServicesInParallel(ctx context.Context) *ShutdownReport {
	report := &ShutdownReport{
		Succeed:             true,
		Services:            []ServiceDescription{},
		Errors:              map[ServiceDescription]error{},
		ShutdownTime:        0,
		ServiceShutdownTime: map[ServiceDescription]time.Duration{},
	}

	listServices := func() []string {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return keys(s.services)
	}

	for {
		// loop over the services that have not been shutdown already
		services := listServices()
		if len(services) == 0 {
			break
		}

		// Check, under a single DAG lock, which services have no dependents
		// (dependencies allowed here). Services having dependents must be
		// shutdown first; the next iteration will shutdown the current service.
		servicesToShutdown := s.rootScope.dag.listServicesHavingNoDependent(s.id, s.name, services)

		if len(servicesToShutdown) > 0 {
			r := s.shutdownServicesWithoutDependenciesInParallel(ctx, servicesToShutdown)
			report = mergeShutdownReports(report, r)
		} else {
			// In this branch, we expect that there is a circular dependency. We shutdown all services, without taking care of order.
			// This is a fallback mechanism to ensure all services are eventually shut down.
			r := s.shutdownServicesWithoutDependenciesInParallel(ctx, services)
			report = mergeShutdownReports(report, r)
		}
	}

	return report
}

// shutdownServicesWithoutDependenciesInParallel shuts down multiple services concurrently
// without considering dependency order. This method is used when services have no dependents
// or when handling circular dependencies.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - serviceNames: List of service names to shut down
//
// Returns a ShutdownReport containing any errors from the shutdown operations.
func (s *Scope) shutdownServicesWithoutDependenciesInParallel(ctx context.Context, serviceNames []string) *ShutdownReport {
	if len(serviceNames) == 0 {
		return &ShutdownReport{Succeed: true, Services: []ServiceDescription{}, Errors: map[ServiceDescription]error{}, ShutdownTime: 0, ServiceShutdownTime: map[ServiceDescription]time.Duration{}}
	}

	mu := sync.Mutex{}
	services := []ServiceDescription{}
	errors := map[ServiceDescription]error{}
	perServiceTimes := map[ServiceDescription]time.Duration{}

	var wg sync.WaitGroup

	for _, name := range serviceNames {
		wg.Go(func() {
			desc := newServiceDescription(s.id, s.name, name)
			start := s.clock().Now()
			e := s.serviceShutdown(ctx, name)
			dt := s.clock().Since(start)

			mu.Lock()
			services = append(services, desc)
			if e != nil {
				errors[desc] = e
			}
			perServiceTimes[desc] = dt
			mu.Unlock()
		})
	}

	wg.Wait()

	return &ShutdownReport{
		Succeed:             len(errors) == 0,
		Services:            services,
		Errors:              errors,
		ShutdownTime:        0,
		ServiceShutdownTime: perServiceTimes,
	}
}

// clone creates a deep copy of the scope with all its services and child scopes.
// This method is used for scope isolation and testing scenarios where you need
// a complete copy of the scope hierarchy.
//
// Parameters:
//   - root: The root scope for the cloned scope hierarchy
//   - parent: The parent scope for the cloned scope
//
// Returns a new Scope instance that is a deep copy of the original.
func (s *Scope) clone(root *RootScope, parent *Scope) *Scope {
	clone := newScope(s.name, root, parent)

	s.mu.RLock()
	services := make(map[string]any, len(s.services))
	childScopes := make(map[string]*Scope, len(s.childScopes))
	maps.Copy(services, s.services)
	maps.Copy(childScopes, s.childScopes)
	s.mu.RUnlock()

	for name, serviceAny := range services {
		if service, ok := serviceAny.(serviceWrapperClone); ok {
			clone.services[name] = service.clone(clone)
		} else {
			clone.services[name] = service
		}
		clone.countDefault(clone.services[name], 1)
		clone.countExport(clone.services[name], 1)

		s.rootScope.opts.onAfterRegistration(clone, name)
	}

	for name, index := range childScopes {
		clone.childScopes[name] = index.clone(root, clone)
	}

	s.logf("injector cloned")

	return clone
}

/**********************************
 *        Service lifecycle       *
 **********************************/

// serviceExist checks if a service with the given name exists in the current scope.
// This method only checks the current scope, not parent scopes.
//
// Parameters:
//   - name: The name of the service to check
//
// Returns true if the service exists in the current scope, false otherwise.
func (s *Scope) serviceExist(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.services[name]
	return ok
}

// serviceExistRec checks if a service with the given name exists in the current scope
// or any of its ancestor scopes. This method performs a recursive search up the scope hierarchy.
//
// Parameters:
//   - name: The name of the service to check
//
// Returns true if the service exists in the current scope or any ancestor scope, false otherwise.
func (s *Scope) serviceExistRec(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.services[name]
	if ok {
		return ok
	}

	if s.parentScope == nil {
		return false
	}

	return s.parentScope.serviceExistRec(name)
}

// serviceGet retrieves a service from the current scope by name.
// This method only searches in the current scope, not parent scopes.
//
// Parameters:
//   - name: The name of the service to retrieve
//
// Returns the service instance and true if found, or nil and false if not found.
func (s *Scope) serviceGet(name string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	svc, ok := s.services[name]
	return svc, ok
}

// serviceGetRec retrieves a service by name from the current scope or any of its ancestor scopes.
// This method performs a recursive search up the scope hierarchy and returns the scope
// where the service was found.
//
// Parameters:
//   - name: The name of the service to retrieve
//
// Returns the service instance, the scope where it was found, and true if found,
// or nil, nil, and false if not found.
func (s *Scope) serviceGetRec(name string) (any, *Scope, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	svc, ok := s.services[name]
	if ok {
		return svc, s, ok
	}

	if s.parentScope == nil {
		return nil, nil, false
	}

	return s.parentScope.serviceGetRec(name)
}

// serviceSet registers a service in the current scope with the given name.
// This method is not protected against double registration - the calling layer
// should check if the service is already registered. It permits service override.
//
// Parameters:
//   - name: The name to register the service under
//   - service: The service instance to register
func (s *Scope) serviceSet(name string, service any) {
	s.RootScope().opts.onBeforeRegistration(s, name)

	s.mu.Lock()
	replaced, ok := s.services[name]
	// readers holding an unresolved default resolve its name again
	previous, isDefault := replaced.(serviceWrapperSupersede)
	if !isDefault || !previous.supersede(func() { s.storeLocked(name, replaced, service) }) {
		s.storeLocked(name, replaced, service)
	}
	s.mu.Unlock()

	// an Override leaves the replaced wrapper behind for good
	if ok && replaced != service {
		s.RootScope().forget(replaced)
	}

	s.RootScope().opts.onAfterRegistration(s, name)
}

// storeLocked stores service as name, in place of replaced (nil when the name
// was free), keeping the count of defaults. mu must be held as a writer.
func (s *Scope) storeLocked(name string, replaced, service any) {
	s.countDefault(replaced, -1)
	s.countDefault(service, 1)
	s.countExport(replaced, -1)
	s.countExport(service, 1)
	s.services[name] = service
	s.typeMatches = nil
}

// countDefault adds delta to the count of default registrations when service
// is one. The caller holds mu as a writer, or owns the scope alone.
func (s *Scope) countDefault(service any, delta int32) {
	if _, ok := service.(serviceWrapperDefault); ok {
		s.defaults.Add(delta)
	}
}

// defaultCount returns the number of default registrations the scope holds.
func (s *Scope) defaultCount() int64 {
	return int64(s.defaults.Load())
}

// countExport adds delta to the count of exports when service is one, a
// default decorating one included. The caller holds mu as a writer, or owns
// the scope alone.
func (s *Scope) countExport(service any, delta int32) {
	if wrapsExport(service) {
		s.exports.Add(delta)
	}
}

// exportCount returns the number of exports the scope holds.
func (s *Scope) exportCount() int64 {
	return int64(s.exports.Load())
}

// serviceForEach iterates over all services in the current scope and calls the provided callback
// for each service. The iteration stops if the callback returns false.
//
// Parameters:
//   - cb: Callback function that receives the service name, scope, and service instance.
//     Return true to continue iteration, false to stop.
func (s *Scope) serviceForEach(cb func(name string, scope *Scope, service any) bool) {
	// Take a snapshot under read lock to avoid iterating a map while it may be mutated
	s.mu.RLock()
	snapshot := make([]struct {
		name string
		svc  any
	}, 0, len(s.services))
	for name, service := range s.services {
		snapshot = append(snapshot, struct {
			name string
			svc  any
		}{name: name, svc: service})
	}
	s.mu.RUnlock()

	for _, item := range snapshot {
		keepGoing := cb(item.name, s, item.svc)
		if !keepGoing {
			break
		}
	}
}

// typeMatch is a service of a scope whose registered type is assignable to
// a target type.
type typeMatch struct {
	name      string
	service   any
	regIndex  uint64
	isDefault bool // registered with AsDefault
}

// matchesFor returns the services of this scope whose registered type is
// assignable to target, in no particular order. The result is cached until
// the scope's services change; callers must not modify it.
func (s *Scope) matchesFor(target reflect.Type) []typeMatch {
	s.mu.RLock()
	matches, ok := s.typeMatches[target]
	s.mu.RUnlock()
	if ok {
		return matches
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if matches, ok := s.typeMatches[target]; ok {
		return matches
	}
	matches = []typeMatch{}
	for name, serviceAny := range s.services {
		service, ok := serviceAny.(serviceWrapperAny)
		if ok && typeCanCastToType(service.getReflectType(), target) {
			_, isDefault := serviceAny.(serviceWrapperDefault)
			matches = append(matches, typeMatch{name: name, service: serviceAny, regIndex: service.getRegistrationIndex(), isDefault: isDefault})
		}
	}
	if s.typeMatches == nil {
		s.typeMatches = map[reflect.Type][]typeMatch{}
	}
	s.typeMatches[target] = matches

	return matches
}

// serviceForEachRec iterates over all services in the current scope and all ancestor scopes,
// calling the provided callback for each service. The iteration stops if the callback returns false.
//
// Parameters:
//   - cb: Callback function that receives the service name, scope, and service instance.
//     Return true to continue iteration, false to stop.
func (s *Scope) serviceForEachRec(cb func(name string, scope *Scope, service any) bool) {
	// Snapshot current services and parent under read lock to avoid deadlocks and map races
	s.mu.RLock()
	snapshot := make([]struct {
		name string
		svc  any
	}, 0, len(s.services))
	for name, service := range s.services {
		snapshot = append(snapshot, struct {
			name string
			svc  any
		}{name: name, svc: service})
	}
	parent := s.parentScope // immutable, but we copy its reference for safety
	s.mu.RUnlock()

	for _, item := range snapshot {
		keepGoing := cb(item.name, s, item.svc)
		if !keepGoing {
			return
		}
	}

	if parent != nil {
		parent.serviceForEachRec(cb)
	}
}

// serviceHealthCheck performs a health check on a specific service in the current scope.
// This method checks if the service implements the Healthchecker interface and calls
// its health check method if available.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - name: The name of the service to health check
//
// Returns an error if the health check fails, or nil if the service is healthy
// or doesn't implement the Healthchecker interface.
func (s *Scope) serviceHealthCheck(ctx context.Context, name string) error {
	check, err := s.serviceHealthCheckFunc(name)
	if err != nil || check == nil {
		return err
	}

	// If the healthchecker does not support context.Timeout, the error will be triggered by raceWithTimeout().
	return raceWithTimeout(ctx, s.clock(), check)
}

// serviceHealthCheckFunc returns the health check of a service in the current
// scope, or nil when the service is not a health checker at this moment (a
// value without a check, a lazy service not built yet, a transient service).
// A timeout error is never triggered for such a service. It returns an error
// when the service is not found.
func (s *Scope) serviceHealthCheckFunc(name string) (func(context.Context) error, error) {
	s.mu.RLock()

	serviceAny, ok := s.services[name]
	if !ok {
		s.mu.RUnlock()
		return nil, serviceNotFound(s, ErrServiceNotFound, []string{name})
	}

	s.mu.RUnlock()

	service, ok := serviceAny.(interface {
		serviceWrapperIsHealthchecker
		serviceWrapperHealthcheck
	})
	if ok {
		s.logf("requested health check for service %s", name)

		if !service.isHealthchecker() {
			return nil, nil
		}

		return service.healthcheck, nil
	}

	// Should never happen.
	panic(fmt.Errorf("DI: service `%s` is not healthchecker", name))
}

// serviceShutdown gracefully shuts down a specific service in the current scope.
// This method checks if the service implements the Shutdowner interface and calls
// its shutdown method. The service is then removed from the scope.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - name: The name of the service to shut down
//
// Returns an error if the shutdown fails, or nil if the shutdown was successful.
// Panics if the service doesn't implement the Shutdowner interface.
func (s *Scope) serviceShutdown(ctx context.Context, name string) error {
	s.mu.Lock()
	serviceAny, ok := s.services[name]
	if ok {
		delete(s.services, name) // service is removed from DI container
		s.countDefault(serviceAny, -1)
		s.countExport(serviceAny, -1)
		s.typeMatches = nil
		delete(s.orderedInvocation, name)
		s.RootScope().dag.removeService(s.id, s.name, name)
	}
	s.mu.Unlock()

	if !ok {
		return serviceNotFound(s, ErrServiceNotFound, []string{name})
	}

	s.RootScope().forget(serviceAny)

	var err error

	service, ok := serviceAny.(serviceWrapperShutdown)
	if ok {
		s.logf("requested shutdown for service %s", name)

		s.RootScope().opts.onBeforeShutdown(s, name)
		err = service.shutdown(ctx)
		s.RootScope().opts.onAfterShutdown(s, name, err)
	} else {
		// Should never happen.
		panic(fmt.Errorf("DI: service `%s` is not shutdowner", name))
	}

	return err
}

/**********************************
 *             Hooks              *
 **********************************/

// onServiceInvoke is called whenever a service is invoked in this scope.
// This method tracks the order of service invocations for observability and debugging purposes.
//
// Parameters:
//   - name: The name of the service that was invoked
func (s *Scope) onServiceInvoke(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orderedInvocation[name]; !ok {
		s.orderedInvocation[name] = s.orderedInvocationIndex
		s.orderedInvocationIndex++
	}
}

// logf logs a formatted message with the scope name prefix.
// This method is used internally for consistent logging across the scope.
//
// Parameters:
//   - format: The format string for the log message
//   - args: Arguments to format the message with
func (s *Scope) logf(format string, args ...any) {
	format = fmt.Sprintf("DI <scope=%s>: %s", s.name, format)
	s.RootScope().opts.Logf(format, args...)
}
