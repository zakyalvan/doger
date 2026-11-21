package doger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"
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
	// version counts the changes of services: a reader that saw version v
	// knows the services are still as it saw them while version is v.
	// Guarded by mu.
	version uint64

	// Storing the invocation order is not needed anymore, but we keep it
	// for improved observability in unit tests.
	orderedInvocation      map[string]int // Map tracking service invocation order (faster than slice)
	orderedInvocationIndex int            // Counter for tracking invocation order
	// invokedNames holds the keys of orderedInvocation, for the lock-free
	// check of onServiceInvoke; it is written under mu with that map
	invokedNames sync.Map

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

	// closed is set, under mu as a writer, once the scope's shutdown began;
	// it is never cleared (see checkOpen)
	closed atomic.Bool

	// events is the event state of the scope (function listeners, in-flight
	// async deliveries); nil until first used, so scopes without events
	// keep their size
	events atomic.Pointer[scopeEvents]
}

// scopeClosing is the shutdown state of a child scope.
type scopeClosing struct {
	count int            // shutdowns of the scope in progress
	run   *scopeCloseRun // the running shutdown that others join, or nil
}

// ID returns the unique identifier of the scope.
// This ID is generated using UUID and is immutable throughout the scope's lifetime.
func (s *Scope) ID() string {
	return s.id
}

// Name returns the human-readable name of the scope.
// This name is provided when creating the scope and is immutable.
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
// Panics with an error matching [ErrScopeAlreadyDeclared] if the parent
// already has an attached child of that name (a child that was shut down is
// detached and no longer counts), and with an error matching [ErrShutdown]
// once the shutdown of s has begun, including from its Closing hooks: a
// scope is never left behind a shutdown.
func (s *Scope) Scope(name string, packages ...func(Injector)) *Scope {
	s.mu.Lock()

	if err := s.checkOpen(); err != nil {
		s.mu.Unlock()
		panic(err)
	}
	if _, ok := s.childScopes[name]; ok {
		s.mu.Unlock()
		panic(duplicateScopeError(name))
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

// ownScope returns s.
func (s *Scope) ownScope() *Scope { return s }

// checkOpen returns an error matching ErrShutdown once the shutdown of s
// began, and nil before. A nil s, the scope of an Injector implemented
// outside this package, is open.
func (s *Scope) checkOpen() error {
	if s != nil && s.closed.Load() {
		return libErrorMatching(ErrShutdown, "scope `%s` is shut down", s.name)
	}

	return nil
}

// close marks s as closed, under its write lock, so a registration or a
// child scope creation holding the lock either completed before or is
// refused.
func (s *Scope) close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed.Store(true)
}

// hasChild reports whether s has a direct child scope named name.
func (s *Scope) hasChild(name string) bool {
	return s.directChild(name) != nil
}

// directChild returns the direct child scope of s named name, or nil.
func (s *Scope) directChild(name string) *Scope {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.childScopes[name]
}

// detachChildScope removes child from the children of s, unless s already
// holds another scope under its name. Detaching twice is a no-op.
func (s *Scope) detachChildScope(child *Scope) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.detachChildLocked(child)
}

// detachChildLocked is detachChildScope with s.mu held as a writer.
func (s *Scope) detachChildLocked(child *Scope) {
	if s.childScopes[child.name] == child {
		delete(s.childScopes, child.name)
	}
}

// detachChild removes the child scope name from s, without shutting it
// down, so that the name can be used again.
func (s *Scope) detachChild(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.childScopes, name)
}

// removeService removes the service name from s, if match accepts it,
// without shutting it down or running any hook.
func (s *Scope) removeService(name string, match func(service any) bool) {
	s.mu.Lock()
	service, ok := s.services[name]
	removed := ok && match(service)
	if removed {
		s.removeLocked(name)
	}
	s.mu.Unlock()

	if removed {
		s.RootScope().forget(service)
	}
}

// removeLocked removes the service name from s, with its bookkeeping:
// default and export counts, type matches, invocation order and the DAG. It
// returns the removed service, and whether there was one. s.mu must be held.
func (s *Scope) removeLocked(name string) (service any, ok bool) {
	service, ok = s.services[name]
	if !ok {
		return nil, false
	}

	delete(s.services, name) // service is removed from DI container
	s.countDefault(service, -1)
	s.countExport(service, -1)
	s.typeMatches = nil
	s.version++
	delete(s.orderedInvocation, name)
	s.invokedNames.Delete(name)
	s.RootScope().dag.removeService(s.id, s.name, name)

	return service, true
}

// RootScope returns the root scope of the scope hierarchy.
// All scopes in a hierarchy share the same root scope, regardless of their depth.
func (s *Scope) RootScope() *RootScope {
	return s.rootScope
}

// clock returns the time source of the container.
func (s *Scope) clock() Clock {
	return s.rootScope.opts.Clock
}

// Ancestors returns the list of all parent scopes in order from immediate parent to root.
// This is useful for understanding the scope hierarchy and for operations that need
// to traverse up the scope tree.
//
// Returns an empty slice for the root scope, and a slice of parent scopes
// for child scopes, ordered from immediate parent to root.
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
// A child whose shutdown has completed is detached: it is no longer listed.
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
// A scope whose shutdown has completed is detached from its parent, and not
// found.
//
// Parameters:
//   - name: The name of the scope to find
//
// Returns the found scope and true if found, or nil and false if not found.
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

	return services
}

// ListInvokedServices returns only the services that have been actually invoked
// (instantiated) in the current scope and its ancestors.
// This is useful for understanding which services are actually being used
// and for debugging dependency issues.
//
// Returns a slice of ServiceDescription objects representing only the invoked services.
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

	return services
}

// HealthCheck performs health checks on all services in the current scope and its ancestors
// that implement the Healthchecker interface.
//
// Returns a map of service names to error values. A nil error indicates a successful health check.
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
// Concurrent callers share a service's check in flight: a caller that finds
// one waits for it within its own context and bounds instead of starting
// another, and the check runs with the context (values and deadline) of the
// caller that started it. A panicking check yields an error matching
// [ErrHealthCheckPanic]. The checks this call starts take a slot of
// InjectorOpts.HealthCheckParallelism.
func (s *Scope) HealthCheckWithContext(ctx context.Context) map[string]error {
	return s.healthCheckTargets(ctx, visibleCheckers(s))
}

// Shutdown gracefully shuts down the scope and all its children.
// This method calls ShutdownWithContext with a background context.
//
// Returns a ShutdownReport containing any errors and timings that occurred during shutdown.
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
// an empty service name, wrapping ctx.Err(), and the shutdown proceeds with
// the ended context, so it skips the services it then reaches (see below).
// Never shut down, synchronously from code running in such a start, a scope
// that the start is starting: it would wait for itself.
//
// Once ctx has ended, the services the shutdown then reaches are not shut
// down: their Shutdown method is not called, whatever its signature, and
// they are removed from their scope. The report holds ctx.Err() under the
// name of each built service implementing a Shutdowner interface; any other
// service (never built, without a Shutdown method, an alias, a transient
// service or an export) gets no error. The HookBeforeShutdown and
// HookAfterShutdown hooks still run for each of them, the latter with that
// error (nil when none), and the call waits for them: a hook that never
// returns blocks the call, even though ctx has ended.
//
// The call returns once ctx ends, even while a service's Shutdown, or the
// build of a service, still runs: each such service is reported with ctx's
// error and abandoned. Its Shutdown keeps running in the background, and its
// HookAfterShutdown hooks run when it returns; a panic it raises then is
// logged, the operation's HookContainerPanic dispatch being over. A build in
// flight finds the service shut down when it completes: its instance is
// never handed out (the invoker gets an error matching ErrShutdown) and is
// shut down once, with this call's context, before the call returns when
// ctx allows it, so its error or panic is reported under the service and
// its dependencies shut down after it. With a context that never ends, the
// call waits for every service and build.
//
// To give services time to shut down, pass a context that is still live,
// for example context.WithTimeout(context.Background(), grace) created when
// the shutdown begins, not one that has already ended, such as a
// [os/signal.NotifyContext] context once its signal arrived.
//
// A child scope is shut down once at a time: a call that finds a shutdown of
// it running, its own or root Shutdown's, waits for that shutdown and
// returns a copy of its report, so services and scope hooks run once. If
// ctx ends first, the report fails with an error keyed by this scope with an
// empty service name, wrapping ctx.Err(), and the running shutdown goes on.
// If the running shutdown panics before producing a report (for example in a
// custom Logger handler), the joined calls get a failed report containing "doger: scope
// shutdown did not complete". Never shut a scope down synchronously from its
// own Closing or Closed hooks, or from its services' Shutdown: it would wait
// for itself.
//
// On the root's own *Scope (the Injector a provider receives in the root
// scope), it stops health scheduling as root Shutdown does, without the
// Closing and Closed hooks, and leaves the container phase unchanged. Health
// recording and the health hooks end there for good: a check run afterwards,
// by any path, records no result and calls no hook.
//
// A scope is closed once its shutdown begins: from then on, registrations
// and Scope() on it panic, and invocations return, with an error matching
// [ErrShutdown]. A second Shutdown of a closed scope succeeds with nothing to
// do. Once the shutdown of a child scope completed, whether called on it or
// reached through an ancestor, the child is detached from its parent: it
// leaves Children and ChildByName, and its name can be used again, so
// per-request scopes created and shut down repeatedly do not accumulate.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns a ShutdownReport containing any errors and timings that occurred during shutdown.
func (s *Scope) ShutdownWithContext(ctx context.Context) *ShutdownReport {
	if ctx == nil {
		ctx = context.Background()
	}

	if s.parentScope == nil {
		// the root's own scope: root Shutdown's model, without its hooks
		s.rootScope.stopScheduling(ctx)
	}

	panics := &shutdownPanics{}
	first := hookPanic{services: panics}
	report := s.shutdownWithContext(ctx, &first)
	s.rootScope.dispatchShutdownPanics(first, panics)

	return report
}

// shutdownWithContext shuts the scope and its children down, keeping the
// first scope hook panic in first instead of raising it.
func (s *Scope) shutdownWithContext(ctx context.Context, first *hookPanic) (report *ShutdownReport) {
	start := s.clock().Now()

	// closed before its children are listed: a scope created from now on is
	// refused instead of left running
	s.close()

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
		// once the shutdown completed, the parent forgets the scope, and its
		// name can be used again
		defer s.parentScope.detachChildScope(s)
		waitErr = joinErrors([]error{s.waitForStartingRun(ctx), s.closeEvents(ctx)})
	}

	if s.exportCount() > 0 {
		report = s.shutdownWithExports(ctx, first, isChild)
	} else {
		rep1 := s.shutdownChildrenInParallel(ctx, first, func(*Scope) bool { return true })

		if isChild {
			s.runScopeObserverHooks(ctx, "scope-closing", s.rootScope.hooks.scopeClosing.load(), first)
		}

		rep2 := s.shutdownServicesInParallel(ctx, first.services)

		if isChild {
			s.runScopeObserverHooks(ctx, "scope-closed", s.rootScope.hooks.scopeClosed.load(), first)
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

// closeEvents ends the event listeners of the child scope s, which is
// closed for publishing: it waits for the async deliveries in flight to
// listeners of s and its descendants, bounded by ctx, then drops the
// function listeners of s; its service listeners go with its services. It
// returns an error when ctx ended first.
func (s *Scope) closeEvents(ctx context.Context) error {
	state := s.events.Load()
	if state == nil {
		return nil
	}

	var err error
	if !s.awaitInflight(ctx) {
		err = drainTimeoutError(ctx)
	}

	s.mu.Lock()
	state.setFuncsLocked(nil)
	s.mu.Unlock()

	return err
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
// before producing one, for example in a custom Logger handler.
func (s *Scope) finishScopeClose(run *scopeCloseRun, report *ShutdownReport) {
	if report == nil {
		report = s.failureReport(libErrorf("scope shutdown did not complete"))
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

	return s.failureReport(libErrorf("waiting for scope shutdown: %w", ctx.Err()))
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
		return libErrorf("waiting for start: %w", ctx.Err())
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
	for _, c := range s.childScopes {
		if selected(c) {
			children = append(children, c)
			s.detachChildLocked(c) // scopes are removed from DI container
		}
	}
	s.mu.Unlock()

	reports := make([]*ShutdownReport, len(children))
	panics := make([]hookPanic, len(children))
	for index := range panics {
		panics[index] = first.child()
	}

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
func (s *Scope) shutdownServicesInParallel(ctx context.Context, panics *shutdownPanics) *ShutdownReport {
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

	// Each layer is the set of services the graph shows without a dependent
	// when it is formed, as before; only how the layer is found changes.
	// Instead of checking every remaining service after each layer (O(V)
	// per layer, O(V^2) for a chain), only the services that can have
	// become free are checked: the dependencies, within s, of the layer just
	// shut down, and the services found free of dependents in s but still
	// held by a dependent outside it. When none of them is free, every
	// remaining service is checked as before, and a cycle is shut down at
	// once, as before.
	plan := newShutdownPlan(s, listServices())
	for {
		layer := plan.nextLayer(s, listServices)
		if len(layer) == 0 {
			break
		}

		r := s.shutdownServicesWithoutDependenciesInParallel(ctx, layer, panics)
		appendShutdownReport(report, r)
		plan.done(layer)
		plan.done(plan.departed(s))
	}

	return report
}

// shutdownPlan finds the layers of a scope's shutdown with in-degree
// counters (Kahn): dependents counts, per service, its dependents within
// the scope not shut down yet; candidates holds the services whose count
// reached 0 since the last layer, and blocked those found with no dependent
// left in the scope but still held by the graph (a dependent outside the
// scope, or an edge added once the plan was made).
type shutdownPlan struct {
	dependencies map[string][]string // within the scope, at the start of the shutdown
	dependents   map[string]int
	candidates   []string
	blocked      map[string]struct{}
	pending      map[string]struct{} // not yet counted as gone from the scope
}

// newShutdownPlan returns the plan of the shutdown of services, the
// services of s: the ones without a dependent among them are the candidates
// of the first layer.
func newShutdownPlan(s *Scope, services []string) *shutdownPlan {
	set := make(map[ServiceDescription]struct{}, len(services))
	for _, name := range services {
		set[newServiceDescription(s.id, s.name, name)] = struct{}{}
	}

	plan := &shutdownPlan{pending: make(map[string]struct{}, len(services))}
	for _, name := range services {
		plan.pending[name] = struct{}{}
	}
	snapshot := s.rootScope.dag.snapshotDependents(set)
	if len(snapshot) > 0 {
		plan.dependencies, plan.dependents = map[string][]string{}, make(map[string]int, len(snapshot))
	}
	for dependency, dependents := range snapshot {
		plan.dependents[dependency.Service] = len(dependents)
		for _, dependent := range dependents {
			plan.dependencies[dependent.Service] = append(plan.dependencies[dependent.Service], dependency.Service)
		}
	}
	for _, name := range services {
		if plan.dependents[name] == 0 {
			plan.candidates = append(plan.candidates, name)
		}
	}

	return plan
}

// nextLayer returns the next layer: the candidates and blocked services
// still in s that the graph shows without a dependent; else, as before
// this plan existed, every service still in s without a dependent; else,
// when s holds services that all have one (a cycle), all of them; nil once
// s is empty.
func (p *shutdownPlan) nextLayer(s *Scope, listServices func() []string) []string {
	// candidates are distinct (each is added once, when its count reaches
	// 0), and blocked ones are never candidates again
	check := make([]string, 0, len(p.candidates)+len(p.blocked))
	for _, name := range p.candidates {
		if s.serviceExist(name) {
			check = append(check, name)
		}
	}
	for name := range p.blocked {
		if s.serviceExist(name) {
			check = append(check, name)
		}
	}
	p.candidates = nil

	layer := s.rootScope.dag.listServicesHavingNoDependent(s.id, s.name, check)
	p.blocked = nil
	if len(layer) < len(check) {
		free := make(map[string]struct{}, len(layer))
		for _, name := range layer {
			free[name] = struct{}{}
		}
		p.blocked = map[string]struct{}{}
		for _, name := range check {
			if _, ok := free[name]; !ok {
				p.blocked[name] = struct{}{}
			}
		}
	}
	if len(layer) > 0 {
		return layer
	}

	services := listServices()
	if len(services) == 0 {
		return nil
	}
	if layer = s.rootScope.dag.listServicesHavingNoDependent(s.id, s.name, services); len(layer) > 0 {
		return layer
	}

	// a circular dependency: every remaining service is shut down at once
	return services
}

// departed returns the pending services that left s other than through a
// layer of the plan, such as a service shut down by ShutdownNamed from the
// Shutdown of another one. Their dependencies may have become free, as the
// live graph shows; without counting them, those would wait for the
// fallback that checks every service. It scans the pending services only
// when s holds fewer of them than the plan expects.
func (p *shutdownPlan) departed(s *Scope) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.services) >= len(p.pending) {
		return nil
	}

	var gone []string
	for name := range p.pending {
		if _, ok := s.services[name]; !ok {
			gone = append(gone, name)
		}
	}

	return gone
}

// done records that services left the scope, in a layer or otherwise: each
// of their dependencies within the scope that has no dependent left there
// is a candidate of the next layer.
func (p *shutdownPlan) done(layer []string) {
	for _, name := range layer {
		if _, ok := p.pending[name]; !ok {
			continue
		}
		delete(p.pending, name)
		delete(p.blocked, name)
		for _, dependency := range p.dependencies[name] {
			if p.dependents[dependency]--; p.dependents[dependency] == 0 {
				p.candidates = append(p.candidates, dependency)
			}
		}
	}
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
func (s *Scope) shutdownServicesWithoutDependenciesInParallel(ctx context.Context, serviceNames []string, panics *shutdownPanics) *ShutdownReport {
	if len(serviceNames) == 0 {
		return &ShutdownReport{Succeed: true, Services: []ServiceDescription{}, Errors: map[ServiceDescription]error{}, ShutdownTime: 0, ServiceShutdownTime: map[ServiceDescription]time.Duration{}}
	}

	type outcome struct {
		desc ServiceDescription
		err  error
		took time.Duration
	}

	// each service shuts down on its own goroutine, which sends its outcome
	// here; the channel holds them all, so a goroutine whose service was
	// abandoned (D4) still finishes, running its AfterShutdown hooks, after
	// this call returned
	outcomes := make(chan outcome, len(serviceNames))
	// services reached after ctx ended run no Shutdown, only their hooks:
	// they are waited for, not abandoned
	waitCtx := ctx
	if ctx.Err() != nil {
		waitCtx = context.WithoutCancel(ctx)
	}
	for _, name := range serviceNames {
		// removed before its goroutine starts: an abandoned service is never
		// listed, and dispatched, again by a later layer
		serviceAny, err := s.takeService(name)
		if err != nil {
			outcomes <- outcome{desc: newServiceDescription(s.id, s.name, name), err: err}
			continue
		}
		go func() {
			start := s.clock().Now()
			err := s.shutdownTaken(ctx, name, serviceAny, panics)
			outcomes <- outcome{desc: newServiceDescription(s.id, s.name, name), err: err, took: s.clock().Since(start)}
		}()
	}

	services := []ServiceDescription{}
	errors := map[ServiceDescription]error{}
	perServiceTimes := map[ServiceDescription]time.Duration{}
	pending := make(map[string]struct{}, len(serviceNames))
	for _, name := range serviceNames {
		pending[name] = struct{}{}
	}
	for len(pending) > 0 {
		if !awaitLayer(waitCtx, outcomes, func(o outcome) {
			delete(pending, o.desc.Service)
			services = append(services, o.desc)
			if o.err != nil {
				errors[o.desc] = o.err
			}
			perServiceTimes[o.desc] = o.took
		}) {
			// the context ended first: the services still shutting down are
			// abandoned and reported with its error
			for name := range pending {
				desc := newServiceDescription(s.id, s.name, name)
				services = append(services, desc)
				errors[desc] = ctx.Err()
			}
			break
		}
	}

	return &ShutdownReport{
		Succeed:             len(errors) == 0,
		Services:            services,
		Errors:              errors,
		ShutdownTime:        0,
		ServiceShutdownTime: perServiceTimes,
	}
}

// awaitLayer receives one value from values (or the close of values) and
// passes it to got, unless ctx ends first: then it reports false. A value
// ready when ctx ends wins over the ended context.
func awaitLayer[V any](ctx context.Context, values <-chan V, got func(V)) bool {
	select {
	case v := <-values:
		got(v)
		return true
	default:
	}

	select {
	case v := <-values:
		got(v)
		return true
	case <-ctx.Done():
		select {
		case v := <-values:
			got(v)
			return true
		default:
			return false
		}
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

		s.rootScope.hooks.onAfterRegistration(clone, name)
	}

	s.copyFuncListenersTo(clone)

	for name, index := range childScopes {
		clone.childScopes[name] = index.clone(root, clone)
	}

	return clone
}

// copyFuncListenersTo gives clone, a new scope, the function listeners of
// s: the same functions, in a list of its own, so stopping or adding one on
// either side leaves the other unchanged.
func (s *Scope) copyFuncListenersTo(clone *Scope) {
	state := s.events.Load()
	if state == nil {
		return
	}

	s.mu.RLock()
	funcs := state.funcs
	s.mu.RUnlock()
	if len(funcs) > 0 {
		clone.eventState().setFuncsLocked(slices.Clone(funcs)) // clone is not shared yet
	}
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

// scopeHolds reports whether scope still holds wrapper under name. It takes
// the scope's read lock.
func scopeHolds(scope *Scope, name string, wrapper any) bool {
	current, ok := scope.serviceGet(name)
	return ok && current == wrapper
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
	if err := s.checkOpen(); err != nil {
		panic(err)
	}

	s.RootScope().hooks.onBeforeRegistration(s, name)

	s.mu.Lock()
	if err := s.checkOpen(); err != nil {
		s.mu.Unlock()
		panic(err)
	}
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

	s.RootScope().hooks.onAfterRegistration(s, name)
}

// storeLocked stores service as name, in place of replaced (nil when the name
// was free), keeping the count of defaults. mu must be held as a writer; the
// caller forgets replaced once it released mu (see RootScope.forget).
func (s *Scope) storeLocked(name string, replaced, service any) {
	s.countDefault(replaced, -1)
	s.countDefault(service, 1)
	s.countExport(replaced, -1)
	s.countExport(service, 1)
	s.services[name] = service
	s.typeMatches = nil
	s.version++
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
	matches, _ := s.matchesForVersion(target)
	return matches
}

// matchesForVersion is matchesFor, with the version of the services the
// matches were read from.
func (s *Scope) matchesForVersion(target reflect.Type) ([]typeMatch, uint64) {
	s.mu.RLock()
	matches, ok := s.typeMatches[target]
	version := s.version
	s.mu.RUnlock()
	if ok {
		return matches, version
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if matches, ok := s.typeMatches[target]; ok {
		return matches, s.version
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

	return matches, s.version
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

// shutdownService gracefully shuts down a specific service in the current scope.
// This method checks if the service implements the Shutdowner interface and calls
// its shutdown method. The service is then removed from the scope.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - name: The name of the service to shut down
//
// Returns an error if the shutdown fails, or nil if the shutdown was successful.
// Panics if the service doesn't implement the Shutdowner interface.
func (s *Scope) shutdownService(ctx context.Context, name string) error {
	return s.shutdownServiceOf(ctx, name, nil)
}

// shutdownServiceOf shuts the service name down as part of a shutdown whose
// first panic panics keeps (nil for none).
func (s *Scope) shutdownServiceOf(ctx context.Context, name string, panics *shutdownPanics) error {
	serviceAny, err := s.takeService(name)
	if err != nil {
		return err
	}

	return s.shutdownTaken(ctx, name, serviceAny, panics)
}

// takeService removes the service name from s for its shutdown, or returns
// an error matching ErrServiceNotFound. It runs no user code: forgetting
// the service, which may run health hooks, is shutdownTaken's.
func (s *Scope) takeService(name string) (any, error) {
	s.mu.Lock()
	serviceAny, ok := s.removeLocked(name)
	s.mu.Unlock()

	if !ok {
		return nil, serviceNotFound(s, ErrServiceNotFound, []string{name})
	}

	return serviceAny, nil
}

// shutdownTaken forgets serviceAny, the service name takeService removed
// from s, then shuts it down with its hooks.
func (s *Scope) shutdownTaken(ctx context.Context, name string, serviceAny any, panics *shutdownPanics) error {
	service, ok := serviceAny.(serviceWrapperShutdown)
	if !ok {
		// Should never happen.
		panic(libErrorf("service `%s` is not shutdowner", name))
	}

	// each hook and the Shutdown call are recovered one by one: a panic is
	// recorded as an error and never skips the steps after it. A returned
	// error is logged as a failed shutdown; a panic is kept for
	// HookContainerPanic or logged, its value at Debug level only.
	var errs, failures []error
	run := func(step func() error) error {
		recovered, panicked, err := recoverShutdownStep(step)
		if panicked {
			s.recordShutdownPanic(panics, name, recovered)
		} else {
			failures = append(failures, err)
		}
		errs = append(errs, err)
		return err
	}

	// forgetting it runs the health hooks: recovered like any step, so a
	// panic there is recorded under the service, never ends the process
	_ = run(func() error { s.RootScope().forget(serviceAny); return nil })

	hooks := &s.RootScope().hooks
	for _, hook := range hooks.beforeShutdown.load() {
		_ = run(func() error { hook(ctx, s, name); return nil })
	}
	op := beginServiceOp(ctx, s, name)
	shutdownErr := run(func() error { return service.shutdown(ctx) })
	elapsed := op.elapsed()
	for _, hook := range hooks.afterShutdown.load() {
		_ = run(func() error { hook(ctx, s, name, elapsed, shutdownErr); return nil })
	}

	if failure := joinErrors(failures); failure != nil {
		s.log(slog.LevelError, "service shutdown failed", slog.String("service", name), slog.Any("error", failure))
	}

	return joinErrors(errs)
}

// shutdownPanics keeps the first panic a shutdown recovered from a service
// Shutdown or a shutdown hook, for HookContainerPanic.
type shutdownPanics struct {
	mu       sync.Mutex
	scope    *Scope // of the service that panicked
	service  string
	value    any
	panicked bool
	// closed is set once the operation's dispatch ran: a panic of an
	// abandoned Shutdown arriving later is logged, never kept
	closed bool
}

// keepFirst keeps the panic value of the service name of scope when it is
// the first panic, and reports whether it did. A nil p keeps nothing.
func (p *shutdownPanics) keepFirst(scope *Scope, name string, value any) bool {
	if p == nil {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.panicked || p.closed {
		return false
	}
	p.scope, p.service, p.value, p.panicked = scope, name, value, true
	return true
}

// close ends keeping panics, and returns the one kept, if any.
func (p *shutdownPanics) close() (scope *Scope, service string, value any, panicked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	return p.scope, p.service, p.value, p.panicked
}

// recordShutdownPanic handles a panic recovered from the shutdown of the
// service name: the first one of a shutdown is kept in panics for
// HookContainerPanic when that option is set; any other is logged. panics
// is nil for a single service shut down by name.
func (s *Scope) recordShutdownPanic(panics *shutdownPanics, name string, value any) {
	if s.rootScope.opts.HookContainerPanic != nil && panics.keepFirst(s, name, value) {
		return
	}

	s.logRecoveredPanic("service-shutdown", name, value)
}

// dispatchShutdownPanic passes the first panic panics kept to
// HookContainerPanic, with the point "service-shutdown". Call it once the
// shutdown is complete; a panic HookContainerPanic raises propagates.
func (s *RootScope) dispatchShutdownPanic(value any, panicked bool) {
	if panicked && s.opts.HookContainerPanic != nil {
		s.opts.HookContainerPanic(s, "service-shutdown", value)
	}
}

// dispatchShutdownPanics dispatches, once a root or child shutdown is
// complete, its first observer hook panic, then its first service shutdown
// panic. When the first dispatch raises a panic, the service panic is
// logged instead of dispatched, so it is never lost.
func (s *RootScope) dispatchShutdownPanics(first hookPanic, panics *shutdownPanics) {
	// from now on, a panic of an abandoned Shutdown is logged
	scope, service, value, panicked := panics.close()

	dispatched := false
	defer func() {
		if !dispatched && panicked {
			scope.logRecoveredPanic("service-shutdown", service, value)
		}
	}()

	s.dispatchFirstPanic(first)
	dispatched = true
	s.dispatchShutdownPanic(value, panicked)
}

// recoverShutdownStep calls step, a hook or a Shutdown call, and returns its
// error; a panic is recovered, reported with its value, and returned as an
// error matching ErrShutdownPanic.
func recoverShutdownStep(step func() error) (recovered any, panicked bool, err error) {
	recovered, panicked = callHookSafely(func() { err = step() })
	if panicked {
		err = shutdownPanicError(recovered)
	}

	return recovered, panicked, err
}

// shutdownPanicError returns the error recorded for a shutdown step that
// panicked with value: it matches ErrShutdownPanic and, for an error value
// (such as a *runtime.PanicNilError), that error too.
func shutdownPanicError(value any) error {
	if err, ok := value.(error); ok {
		return fmt.Errorf("%w: %w", ErrShutdownPanic, err)
	}

	return fmt.Errorf("%w: %v", ErrShutdownPanic, value)
}

// joinErrors returns nil when errs holds no error, the error itself when it
// holds one, and errors.Join of them otherwise.
func joinErrors(errs []error) error {
	var found []error
	for _, err := range errs {
		if err != nil {
			found = append(found, err)
		}
	}

	switch len(found) {
	case 0:
		return nil
	case 1:
		return found[0]
	default:
		return errors.Join(found...)
	}
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
	// every invocation after the first one only reads invokedNames, without
	// a lock: the write lock is taken for the first invocation only
	if _, invoked := s.invokedNames.Load(name); invoked {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orderedInvocation[name]; !ok {
		s.orderedInvocation[name] = s.orderedInvocationIndex
		s.orderedInvocationIndex++
		s.invokedNames.Store(name, struct{}{})
	}
}

// log emits a record at level with message msg and the attributes scope
// (the name of s) and attrs to the container's Logger, unless it discards
// level: then no record is built.
func (s *Scope) log(level slog.Level, msg string, attrs ...slog.Attr) {
	if !s.logEnabled(level) {
		return
	}

	all := make([]slog.Attr, 0, len(attrs)+1)
	all = append(all, slog.String("scope", s.name))
	all = append(all, attrs...)
	s.rootScope.opts.Logger.LogAttrs(context.Background(), level, msg, all...)
}

// logEnabled reports whether the container's Logger keeps records at level,
// so that a caller can skip building costly attributes.
func (s *Scope) logEnabled(level slog.Level) bool {
	return s.rootScope.opts.Logger.Enabled(context.Background(), level)
}
