package doger

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// DefaultRootScopeName is the default name for the root scope.
const DefaultRootScopeName = "[root]"

// discardLogger is the logger of a container without InjectorOpts.Logger.
var discardLogger = slog.New(slog.DiscardHandler)

// New creates a new dependency injection container with default options.
// This is the primary entry point for creating a new DI container.
//
// Parameters:
//   - packages: Optional package functions to execute during initialization
//
// Returns a new RootScope instance ready for service registration.
//
// Example:
//
//	injector := doger.New()
//	doger.Provide(injector, func(i doger.Injector) (*MyService, error) {
//	    return &MyService{}, nil
//	})
func New(packages ...func(Injector)) *RootScope {
	return NewWithOpts(&InjectorOpts{}, packages...)
}

// NewWithOpts creates a new dependency injection container with custom options.
// This allows you to configure logging, hooks, health check settings, and other options.
//
// Parameters:
//   - opts: Configuration options for the injector
//   - packages: Optional package functions to execute during initialization
//
// Returns a new RootScope instance with the specified configuration.
//
// The container keeps its own copy of opts, hook slices included: the
// caller's value is never written, so one value can configure several
// independent containers, and hooks added to one container reach no other.
// A nil opts behaves like &InjectorOpts{}.
//
// Example:
//
//	opts := &doger.InjectorOpts{
//	    Logger:                 slog.Default(),
//	    HealthCheckParallelism: 10,
//	}
//	injector := doger.NewWithOpts(opts)
func NewWithOpts(opts *InjectorOpts, packages ...func(Injector)) *RootScope {
	if opts == nil {
		opts = &InjectorOpts{}
	}

	validateEventOptions(opts)
	opts = opts.clone() // never write the caller's value
	opts.initDefaults()

	root := &RootScope{
		opts:  opts,
		kinds: defaultKinds,
	}
	root.hooks.take(opts)
	root.health.root = root
	initScope(&root.selfStorage, DefaultRootScopeName, root, nil)
	root.self = &root.selfStorage
	initDAG(&root.dagStorage)
	root.dag = &root.dagStorage

	if opts.HealthCheckParallelism > 0 {
		root.healthSlots = make(chan struct{}, opts.HealthCheckParallelism)
	}

	for _, pkg := range packages {
		pkg(root)
	}

	return root
}

// Ensure RootScope implements the Injector interface at compile time.
var _ Injector = (*RootScope)(nil)

// RootScope is the top-level scope in the dependency injection container hierarchy.
// It serves as the entry point for all DI operations and manages the overall container lifecycle.
//
// Key responsibilities:
//   - Service registration and resolution
//   - Child scope management
//   - Health check coordination
//   - Shutdown orchestration
//   - Dependency graph management
type RootScope struct {
	self        *Scope        // The root scope instance
	opts        *InjectorOpts // Configuration options; its hook lists moved to hooks
	hooks       containerHooks
	dag         *DAG          // Dependency graph for service relationships
	healthSlots chan struct{} // one value per running health check started by a scope-wide check; nil when unlimited
	health      healthMonitor // health checks in flight

	nextRegIndex atomic.Uint64 // next registration index, shared by every scope of this container
	declareMu    sync.Mutex    // serializes DeclareDependency (cycle check plus add)

	// ext holds the installed extensions; nil until the first Extend, so a
	// container without extensions holds none.
	ext atomic.Pointer[extensionRegistry]

	// events holds the container's event state; nil until first used, so a
	// container without events holds none.
	events atomic.Pointer[rootEvents]

	// eventTypes is the event type registry: an immutable snapshot replaced
	// under eventTypesMu, read without a lock; nil until the first
	// registration.
	eventTypesMu sync.Mutex
	eventTypes   atomic.Pointer[eventRegistry]

	// transport holds the transport state; nil until first used
	transport atomic.Pointer[eventTransportState]

	kindsMu sync.Mutex
	kinds   []kindEntry // kind handlers, in order of first Handle; copied before written, as it may be shared

	// Lifecycle state. Phases change only by compare-and-set under lifecycleMu,
	// which is never held while user code runs.
	lifecycleMu sync.Mutex
	startPhase  startPhase
	closePhase  closePhase              // next to startPhase: both fit one word, keeping RootScope in its size class
	startCancel context.CancelCauseFunc // cancels the running Start's context
	startDone   chan struct{}           // closed when the running Start returned
	closeDone   chan struct{}           // closed when the winning Shutdown finished
	closeReport *ShutdownReport         // private copy of the winner's report
	incCancel   context.CancelCauseFunc // cancels the running Refresh or Scope.Start
	incDone     chan struct{}           // closed when the running Refresh or Scope.Start returned

	// pipelineMu serializes the Start, Refresh and Scope.Start pipelines. It
	// is held across their user code; Shutdown never takes it, and nothing
	// takes it while holding lifecycleMu.
	pipelineMu sync.Mutex
	runScopes  map[*Scope]struct{}          // scopes with candidates in the running pipeline; guarded by lifecycleMu
	startedMu  sync.Mutex                   // guards started and handled; never held across user code
	started    map[any]struct{}             // Starters already started, keyed by wrapper
	handled    map[any]map[*kindID]struct{} // kinds already handled, by wrapper

	// selfStorage and dagStorage hold self and dag in the container's own
	// allocation; use self and dag. A clone points self at its cloned scope.
	selfStorage Scope
	dagStorage  DAG
}

// closePhase is the shutdown state of a root container. It is an int32 so it
// shares a word with startPhase in RootScope.
type closePhase int32

const (
	closeOpen closePhase = iota
	closeClosing
	closeClosed
)

// Pass-through methods that delegate to the underlying scope
// These methods provide the same interface as Scope but operate on the root level

// ID returns the unique identifier of the root scope.
func (s *RootScope) ID() string { return s.self.ID() }

// Name returns the name of the root scope.
func (s *RootScope) Name() string { return s.self.Name() }

// Scope creates a new child scope under the root scope.
func (s *RootScope) Scope(name string, p ...func(Injector)) *Scope { return s.self.Scope(name, p...) }

// RootScope returns the root scope itself (this instance).
func (s *RootScope) RootScope() *RootScope { return s.self.RootScope() }
func (s *RootScope) ownScope() *Scope      { return s.self }

// Ancestors returns an empty slice since the root scope has no ancestors.
func (s *RootScope) Ancestors() []*Scope { return []*Scope{} }

// Children returns the list of immediate child scopes.
func (s *RootScope) Children() []*Scope { return s.self.Children() }

// ChildByID searches for a child scope by its unique ID across the entire scope hierarchy.
func (s *RootScope) ChildByID(id string) (*Scope, bool) { return s.self.ChildByID(id) }

// ChildByName searches for a child scope by its name across the entire scope hierarchy.
func (s *RootScope) ChildByName(name string) (*Scope, bool) { return s.self.ChildByName(name) }

// ListProvidedServices returns all services available in the root scope and all its descendant scopes.
func (s *RootScope) ListProvidedServices() []ServiceDescription { return s.self.ListProvidedServices() }

// ListInvokedServices returns all services that have been invoked in the root scope and all its descendant scopes.
func (s *RootScope) ListInvokedServices() []ServiceDescription { return s.self.ListInvokedServices() }

// HealthCheck performs health checks on the services of the root scope and
// of installed extension scopes (see [Extend]); other child scopes are not
// covered: use [RootScope.HealthReport] for the whole container.
func (s *RootScope) HealthCheck() map[string]error {
	return s.HealthCheckWithContext(context.Background())
}

// HealthCheckWithContext performs health checks with context support for
// cancellation and timeouts, covering what [RootScope.HealthCheck] covers.
//
// Concurrent callers share a service's check in flight: a caller that finds
// one waits for it within its own context and bounds instead of starting
// another, and the check runs with the context (values and deadline) of the
// caller that started it. A panicking check yields an error matching
// [ErrHealthCheckPanic]. The checks this call starts take a slot of
// InjectorOpts.HealthCheckParallelism.
func (s *RootScope) HealthCheckWithContext(ctx context.Context) map[string]error {
	return s.self.healthCheckTargets(ctx, rootForcedCheckers(s))
}

// Shutdown gracefully shuts down the root scope and all its descendant scopes.
// It runs once per root container; see [RootScope.ShutdownWithContext].
// It drops any context: called from code running inside this root's
// shutdown, such as a service's Shutdown method, it waits for that shutdown,
// so for itself. Call ShutdownWithContext with the context received there.
func (s *RootScope) Shutdown() *ShutdownReport { return s.ShutdownWithContext(context.Background()) }

// ShutdownWithContext gracefully shuts down the root scope and all its descendant scopes with context support.
//
// Root shutdown runs once per root container:
//   - The first caller shuts the services down. Concurrent and later callers
//     do not shut anything down again; they wait for the first caller and
//     return a copy of its report. Each caller owns its copy: changing it
//     affects neither the stored report nor other callers.
//   - A caller that arrives after the shutdown completed always gets the
//     first report, even if its own context has already ended.
//   - A caller still waiting when its context ends returns a failed report
//     whose only error is keyed by the root scope with an empty service name
//     and wraps ctx.Err(). The first caller's shutdown keeps running.
//   - If the first caller panics before producing a report (for example in
//     a custom Logger handler), the panic propagates to it, and every other caller gets
//     a failed report containing "doger: root shutdown did not complete".
//   - A nil ctx is treated as context.Background().
//   - Services registered on the root after the first Shutdown are not shut
//     down by a later root Shutdown.
//
// A Shutdown that begins while [RootScope.StartWithContext] runs cancels
// Start, with cause [ErrShutdown], and waits for it to return
// before shutting services down. If ctx ends first, the report fails with a
// root-scope error wrapping ctx.Err(), and shutdown proceeds with the ended
// context while Start is still running, so it skips the services it then
// reaches (see below). Never call Shutdown synchronously from a Starter,
// handler, runner, provider or hook running during Start: it would wait for
// itself.
//
// Once ctx has ended, the services the shutdown then reaches are not shut
// down: their Shutdown method is not called, whatever its signature, and
// they are removed from their scope. The report holds ctx.Err() under the
// name of each built service implementing a Shutdowner interface; any other
// service (never built, without a Shutdown method, an alias, a transient
// service or an export) gets no error. The HookBeforeShutdown and
// HookAfterShutdown hooks still run for each of them, the latter with that
// error (nil when none).
// To give services time to shut down, pass a context that is still live,
// for example context.WithTimeout(context.Background(), grace) created when
// the shutdown begins, not one that has already ended, such as a
// [signal.NotifyContext] context once its signal arrived.
//
// Health checks: Shutdown first stops the health scheduling of
// InjectorOpts.HealthScheduler, before the Closing hooks, and cancels the
// context of every health check in flight, but does not wait for those
// checks: a check that ignores its context may still run while its service
// shuts down. A service whose Shutdown releases what its check uses must
// guard against a check still running. A check's context is cancelled with
// the cause [ErrShutdown] (see [context.Cause]); a caller still
// waiting for it gets an error matching [ErrHealthCheckTimeout] and
// [context.Canceled]. When ScheduleHealthRounds is still running, Shutdown
// waits for it to return, even after ctx has ended, then calls the stop
// function it returned, so no goroutine of the schedule outlives Shutdown.
//
// Only the [RootScope] methods follow this model. A shutdown reached through
// the root's *Scope (a provider's injector, or [Injector.Shutdown] on a
// virtual scope) bypasses it and behaves as before, except that it stops
// health scheduling and ends health recording for good; see
// [Scope.ShutdownWithContext]. Scope.Shutdown on a child scope waits for a
// start that is starting it; see [Scope.ShutdownWithContext].
//
// Re-entrancy: the context handed to services and scope hooks is derived
// from ctx and marked with this root. A ShutdownWithContext on the same root
// with that context, or one derived from it, such as from a service's
// Shutdown(ctx), returns at once a successful report listing no service,
// and the running shutdown completes normally. A call with any other
// context waits for the running shutdown, bounded by its own context. A
// marked context kept and used after that shutdown completed still returns
// an empty successful report, not the first report.
//
// Deadline: the call returns once ctx ends, even while a service's Shutdown
// or a build still runs; those services are reported with ctx's error and
// keep running in the background (see [Scope.ShutdownWithContext]).
func (s *RootScope) ShutdownWithContext(ctx context.Context) *ShutdownReport {
	if ctx == nil {
		ctx = context.Background()
	}
	if marked, _ := ctx.Value(rootShutdownKey{}).(*RootScope); marked == s {
		// called from inside this root's shutdown, with its context: waiting
		// would wait on itself
		return emptyShutdownReport()
	}

	ticket := s.beginClose()
	if !ticket.winner {
		return s.waitForClose(ctx, ticket.done)
	}

	panics := &shutdownPanics{}
	first := hookPanic{services: panics}
	report := s.shutdownAsWinner(ctx, ticket, &first)
	s.self.log(slog.LevelInfo, "container closed")

	// pointClosed: closeDone is closed, so code here may call Shutdown().
	runObserverHooksWith(s, "container", "closed", s.hooks.containerClosed.load(), &first, func(hook func(context.Context, *RootScope, *ShutdownReport)) {
		hook(ctx, s, report)
	})
	s.publishLifecycle(ctx, ContainerClosed{Root: s, Report: report})

	s.dispatchShutdownPanics(first, panics)

	return report
}

// rootShutdownKey is the context key marking the context a root shutdown
// hands to services and scope hooks; its value is the *RootScope.
type rootShutdownKey struct{}

// emptyShutdownReport returns a successful report listing no service.
func emptyShutdownReport() *ShutdownReport {
	return &ShutdownReport{
		Succeed:             true,
		Services:            []ServiceDescription{},
		Errors:              map[ServiceDescription]error{},
		ServiceShutdownTime: map[ServiceDescription]time.Duration{},
	}
}

// waitForClose waits for the winning Shutdown, bounded by ctx, and returns a
// copy of its report.
func (s *RootScope) waitForClose(ctx context.Context, done <-chan struct{}) *ShutdownReport {
	if awaitDone(done, ctx) {
		s.lifecycleMu.Lock()
		defer s.lifecycleMu.Unlock()
		return cloneShutdownReport(s.closeReport)
	}

	return s.rootFailureReport(libErrorf("waiting for root shutdown: %w", ctx.Err()))
}

// shutdownAsWinner shuts the container down. Its deferred finishClose runs
// even when this goroutine panics, so waiters are always released.
func (s *RootScope) shutdownAsWinner(ctx context.Context, ticket closeTicket, first *hookPanic) (report *ShutdownReport) {
	defer func() { s.finishClose(report) }()

	// the winner's first record: a handler panicking on it still releases
	// the other callers through finishClose
	s.self.log(slog.LevelDebug, "requested shutdown")
	s.self.log(slog.LevelInfo, "container closing")
	s.self.close() // before the Closing hooks: nothing is registered from now on
	s.stopScheduling(ctx)

	startErr := waitForStart(ctx, ticket)
	s.closeSubscriptions(ctx)

	// pointClosing
	s.runObserverHooks(ctx, "closing", s.hooks.containerClosing.load(), first)
	s.publishLifecycle(ctx, ContainerClosing{Root: s})
	drainErr := s.drainEvents(ctx)

	// services and scope hooks get a context marked with this root, so a
	// re-entrant ShutdownWithContext with it returns at once
	report = s.self.shutdownWithContext(context.WithValue(ctx, rootShutdownKey{}, s), first)
	s.eventState().closed.Store(true)
	if err := joinErrors([]error{startErr, drainErr}); err != nil {
		report.Errors[newServiceDescription(s.ID(), s.Name(), "")] = err
		report.Succeed = false
	}

	return report
}

// waitForStart waits for a Start, Refresh or Scope.Start that was running
// when the close phase began, and that beginClose cancelled, to return,
// bounded by ctx. It returns an error when ctx ended first.
func waitForStart(ctx context.Context, ticket closeTicket) error {
	if !ticket.inFlight {
		return nil
	}

	if !awaitDone(ticket.startDone, ctx) {
		return libErrorf("waiting for start: %w", ctx.Err())
	}

	return nil
}

// closeTicket is what a Shutdown caller learns when it enters the close phase.
type closeTicket struct {
	done   chan struct{} // closed when the winner finished
	winner bool          // this caller runs the shutdown

	// winner only: a Start, Refresh or Scope.Start running when the close
	// phase began
	inFlight  bool
	startDone chan struct{}
}

// beginClose moves the close phase from open to closing. The caller that
// does it is the winner and runs the shutdown; any other caller waits on done.
// A Start, Refresh or Scope.Start running then is cancelled with cause
// ErrShutdown before lifecycleMu is released.
func (s *RootScope) beginClose() closeTicket {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.closePhase != closeOpen {
		return closeTicket{done: s.closeDone}
	}

	s.closePhase = closeClosing
	s.closeDone = make(chan struct{})

	ticket := closeTicket{done: s.closeDone, winner: true}
	var cancel context.CancelCauseFunc
	cancel, ticket.startDone, ticket.inFlight = s.inFlightRun()
	if ticket.inFlight {
		// under lifecycleMu, so a run finishing now already sees the cause
		cancel(ErrShutdown)
	}

	return ticket
}

// inFlightRun returns the cancel function and done channel of the Start,
// Refresh or Scope.Start running now, if any. lifecycleMu must be held.
func (s *RootScope) inFlightRun() (context.CancelCauseFunc, chan struct{}, bool) {
	switch {
	case s.startPhase == startStarting:
		return s.startCancel, s.startDone, true
	case s.incDone != nil:
		return s.incCancel, s.incDone, true
	default:
		return nil, nil, false
	}
}

// finishClose stores a private copy of the winner's report and releases the
// waiters. A nil report means the winner panicked before producing one.
func (s *RootScope) finishClose(report *ShutdownReport) {
	stored := cloneShutdownReport(report)
	if report == nil {
		stored = s.rootFailureReport(libErrorf("root shutdown did not complete"))
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.closePhase = closeClosed
	s.closeReport = stored
	close(s.closeDone)
}

// rootFailureReport returns a failed report whose only error is keyed by the
// root scope with an empty service name.
func (s *RootScope) rootFailureReport(err error) *ShutdownReport {
	return &ShutdownReport{
		Succeed:             false,
		Services:            []ServiceDescription{},
		Errors:              map[ServiceDescription]error{newServiceDescription(s.ID(), s.Name(), ""): err},
		ShutdownTime:        0,
		ServiceShutdownTime: map[ServiceDescription]time.Duration{},
	}
}

// awaitDone waits until done is closed or ctx ends, and prefers completion:
// it reports true whenever done is closed, even if ctx has ended too.
func awaitDone(done <-chan struct{}, ctx context.Context) bool { //nolint:revive // done first mirrors the design
	return awaitLayer(ctx, done, func(struct{}) {})
}

// cloneShutdownReport deep-copies a report, so callers cannot alter each other's copy.
func cloneShutdownReport(report *ShutdownReport) *ShutdownReport {
	if report == nil {
		return nil
	}

	return &ShutdownReport{
		Succeed:             report.Succeed,
		Services:            slices.Clone(report.Services),
		Errors:              maps.Clone(report.Errors),
		ShutdownTime:        report.ShutdownTime,
		ServiceShutdownTime: maps.Clone(report.ServiceShutdownTime),
	}
}

func (s *RootScope) clone(root *RootScope, parent *Scope) *Scope { return s.self.clone(root, parent) }

func (s *RootScope) serviceExist(name string) bool { return s.self.serviceExist(name) }

func (s *RootScope) serviceExistRec(name string) bool { return s.self.serviceExistRec(name) }

func (s *RootScope) serviceGet(name string) (any, bool) { return s.self.serviceGet(name) }

func (s *RootScope) serviceGetRec(name string) (any, *Scope, bool) { return s.self.serviceGetRec(name) }

func (s *RootScope) serviceSet(name string, service any)              { s.self.serviceSet(name, service) } // serviceSet is not protected against double registration
func (s *RootScope) serviceForEach(cb func(string, *Scope, any) bool) { s.self.serviceForEach(cb) }

func (s *RootScope) serviceForEachRec(cb func(string, *Scope, any) bool) {
	s.self.serviceForEachRec(cb)
}

func (s *RootScope) serviceHealthCheck(ctx context.Context, name string) error {
	return s.self.serviceHealthCheck(ctx, name)
}

func (s *RootScope) shutdownService(ctx context.Context, name string) error {
	return s.self.shutdownService(ctx, name)
}
func (s *RootScope) onServiceInvoke(name string) { s.self.onServiceInvoke(name) }

/**
 * RootScope stuff
 */

// AddBeforeRegistrationHook adds a hook that will be called before a service is registered.
// A nil hook is ignored. See [InjectorOpts.HookBeforeRegistration].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddBeforeRegistrationHook(hook func(*Scope, string)) {
	if hook == nil {
		return
	}
	s.hooks.beforeRegistration.add(hook)
}

// AddAfterRegistrationHook adds a hook that will be called after a service is registered.
// A nil hook is ignored. See [InjectorOpts.HookAfterRegistration].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddAfterRegistrationHook(hook func(*Scope, string)) {
	if hook == nil {
		return
	}
	s.hooks.afterRegistration.add(hook)
}

// AddBeforeInvocationHook adds a hook that will be called before a service is invoked.
// A nil hook is ignored. See [InjectorOpts.HookBeforeInvocation].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddBeforeInvocationHook(hook func(*Scope, string)) {
	if hook == nil {
		return
	}
	s.hooks.beforeInvocation.add(hook)
}

// AddAfterInvocationHook adds a hook that will be called after a service is invoked.
// A nil hook is ignored. See [InjectorOpts.HookAfterInvocation].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddAfterInvocationHook(hook func(scope *Scope, serviceName string, elapsed time.Duration, err error)) {
	if hook == nil {
		return
	}
	s.hooks.afterInvocation.add(hook)
}

// AddAfterBuildHook adds a hook that will be called with each instance the
// container builds. A nil hook is ignored. See [InjectorOpts.HookAfterBuild].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddAfterBuildHook(hook func(*Scope, string, any)) {
	if hook == nil {
		return
	}
	s.hooks.afterBuild.add(hook)
}

// AddBeforeShutdownHook adds a hook that will be called before a service is shutdown.
// A nil hook is ignored. See [InjectorOpts.HookBeforeShutdown].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddBeforeShutdownHook(hook func(ctx context.Context, scope *Scope, serviceName string)) {
	if hook == nil {
		return
	}
	s.hooks.beforeShutdown.add(hook)
}

// AddAfterShutdownHook adds a hook that will be called after a service is shutdown.
// A nil hook is ignored. See [InjectorOpts.HookAfterShutdown].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddAfterShutdownHook(hook func(ctx context.Context, scope *Scope, serviceName string, elapsed time.Duration, err error)) {
	if hook == nil {
		return
	}
	s.hooks.afterShutdown.add(hook)
}

// AddBeforeStartHook adds a hook that will be called by Start, Refresh and
// Scope.Start just before a service's Start method. See
// [InjectorOpts.HookBeforeStart].
// A nil hook is ignored.
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddBeforeStartHook(hook func(ctx context.Context, scope *Scope, serviceName string)) {
	if hook == nil {
		return
	}
	s.hooks.beforeStart.add(hook)
}

// AddAfterStartHook adds a hook that will be called by Start, Refresh and
// Scope.Start just after a service's Start method, with the error the method
// returned. See [InjectorOpts.HookAfterStart].
// A nil hook is ignored.
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddAfterStartHook(hook func(ctx context.Context, scope *Scope, serviceName string, elapsed time.Duration, err error)) {
	if hook == nil {
		return
	}
	s.hooks.afterStart.add(hook)
}

// AddContainerStartingHook adds a hook that will be called by the first
// Start, after its one-shot guard and before its context check or any
// instantiation. A non-nil error vetoes Start: later Starting hooks do not
// run, nothing is built, and Start returns an error wrapping it. A nil hook
// is ignored.
//
// The hook must not call root Shutdown synchronously without a deadline: it
// would wait for the running Start, which waits for the hook. See
// [InjectorOpts.HookContainerStarting].
// A nil hook is ignored.
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddContainerStartingHook(hook func(context.Context, *RootScope) error) {
	if hook == nil {
		return
	}
	s.hooks.containerStarting.add(hook)
}

// AddContainerStartedHook adds a hook that will be called by the first Start
// once its pipeline succeeded. A nil hook is ignored. See
// [InjectorOpts.HookContainerStarted].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddContainerStartedHook(hook func(context.Context, *RootScope)) {
	if hook == nil {
		return
	}
	s.hooks.containerStarted.add(hook)
}

// AddContainerReadyHook adds a hook that will be called by the first Start
// after every Started hook returned. A nil hook is ignored. See
// [InjectorOpts.HookContainerReady].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddContainerReadyHook(hook func(context.Context, *RootScope)) {
	if hook == nil {
		return
	}
	s.hooks.containerReady.add(hook)
}

// AddContainerFailedHook adds a hook that will be called by the first Start
// when it fails, with the error Start returns. A nil hook is ignored. See
// [InjectorOpts.HookContainerFailed].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddContainerFailedHook(hook func(context.Context, *RootScope, error)) {
	if hook == nil {
		return
	}
	s.hooks.containerFailed.add(hook)
}

// AddContainerClosingHook adds a hook that will be called by the first root
// Shutdown before any service is shut down. A nil hook is ignored.
//
// The hook must not call root Shutdown synchronously without a deadline: it
// would wait for the running Shutdown, which waits for the hook. See
// [InjectorOpts.HookContainerClosing].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddContainerClosingHook(hook func(context.Context, *RootScope)) {
	if hook == nil {
		return
	}
	s.hooks.containerClosing.add(hook)
}

// AddContainerClosedHook adds a hook that will be called by the first root
// Shutdown once every service is shut down, with its report. A nil hook is
// ignored. See [InjectorOpts.HookContainerClosed].
// A nil hook is ignored.
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddContainerClosedHook(hook func(context.Context, *RootScope, *ShutdownReport)) {
	if hook == nil {
		return
	}
	s.hooks.containerClosed.add(hook)
}

// AddScopeStartingHook adds a hook that will be called by Scope.Start on a
// child scope before anything starts; a non-nil error vetoes it. A nil hook
// is ignored. See [InjectorOpts.HookScopeStarting].
// A nil hook is ignored.
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddScopeStartingHook(hook func(context.Context, *Scope) error) {
	if hook == nil {
		return
	}
	s.hooks.scopeStarting.add(hook)
}

// AddScopeStartedHook adds a hook that will be called by Scope.Start on a
// child scope once its services started. A nil hook is ignored. See
// [InjectorOpts.HookScopeStarted].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddScopeStartedHook(hook func(context.Context, *Scope)) {
	if hook == nil {
		return
	}
	s.hooks.scopeStarted.add(hook)
}

// AddScopeClosingHook adds a hook that will be called when a child scope
// shuts down, before its services. A nil hook is ignored. See
// [InjectorOpts.HookScopeClosing].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddScopeClosingHook(hook func(context.Context, *Scope)) {
	if hook == nil {
		return
	}
	s.hooks.scopeClosing.add(hook)
}

// AddScopeClosedHook adds a hook that will be called when a child scope shut
// down. A nil hook is ignored. See [InjectorOpts.HookScopeClosed].
//
// It is safe to call at any time, concurrently with registration,
// invocation, Start, health checks, Shutdown and other adders. The hook
// applies to operations that begin after this call returned; whether an
// operation already running sees it is unspecified. Use the InjectorOpts
// field to set hooks at configuration time, and this method for plugins
// that add hooks to a container they did not create.
func (s *RootScope) AddScopeClosedHook(hook func(context.Context, *Scope)) {
	if hook == nil {
		return
	}
	s.hooks.scopeClosed.add(hook)
}

// Clone clones injector with provided services but not with invoked instances.
// The clone keeps the options, hooks included, and the kind handlers
// (including a replaced Runnable handler), but not the lifecycle state: it
// starts and shuts down independently.
//
// An eager value (ProvideValue, ProvideNamedValue) is shared by the source
// and its clones: the first of them to shut it down calls its Shutdown, and
// the others only drop their registration. A lazy service is built and
// shut down once per container.
func (s *RootScope) Clone() *RootScope {
	return s.CloneWithOpts(s.hooks.optsWith(s.opts))
}

// CloneWithOpts clones injector with provided services but not with invoked instances, with options.
// The clone uses only a copy of opts (hooks included; the caller's value is
// never written), and keeps the source's kind
// handlers, in order. Handling a kind on the clone replaces its handler on
// the clone only. Installed extensions are copied with their private
// scopes, and disabled ones as disabled: Extend of such an identity on the
// clone follows the repeat rules of [Extend], and the clone's exports
// resolve from its own scopes. An installation still running when the clone
// is taken is not copied, nor is its partly built private scope, so the
// clone can install that extension itself.
//
// Events: service listeners come with their registrations, and function
// listeners are copied as the same functions; stopping or adding a listener
// on one container leaves the other unchanged. The clone gets its own
// default executor, while an InjectorOpts.EventExecutor the clone keeps is
// shared: the clone's shutdown never calls its Shutdown, so shutting a
// clone down leaves the source's async deliveries working.
//
// Event transports: the clone copies the event type registry and the
// delivery modes of its listeners, and shares an InjectorOpts.EventTransport
// it keeps. It resolves a service transport on its own, opens its own
// subscriptions at its own Start, and generates its own instance ID unless
// InjectorOpts.InstanceID is set.
func (s *RootScope) CloneWithOpts(opts *InjectorOpts) *RootScope {
	clone := NewWithOpts(opts)
	s.cloneWithExtensions(clone)
	s.copyKindsTo(clone)
	s.copyEventsTo(clone)

	// Read after copying: every copied wrapper got its index before it was
	// stored, so later registrations in the clone come after all of them.
	clone.nextRegIndex.Store(s.nextRegIndex.Load())

	return clone
}

// copyKindsTo gives clone the kind handler entries, in their order. Entries
// do not depend on the container, and kind lists are copied before they are
// written, so the list itself is shared.
func (s *RootScope) copyKindsTo(clone *RootScope) {
	s.kindsMu.Lock()
	kinds := s.kinds
	s.kindsMu.Unlock()

	clone.kindsMu.Lock()
	clone.kinds = kinds
	clone.kindsMu.Unlock()
}

// copyEventsTo gives clone the event type registry of s, and continues the
// function listener sequence of s in clone, so
// the listeners clone registers get IDs the copied ones do not have. Each
// scope's function listeners were copied with the scope tree; plan caches
// and the default executor are per container and never copied. An
// InjectorOpts.EventExecutor clone shares with s is borrowed: the clone's
// shutdown never shuts it down, only the container it was supplied to.
func (s *RootScope) copyEventsTo(clone *RootScope) {
	// a registry snapshot is never written: registrations on either side
	// publish a new one, so sharing it copies it
	clone.eventTypes.Store(s.eventTypes.Load())
	if events := s.events.Load(); events != nil {
		clone.eventState().funcSeq.Store(events.funcSeq.Load())
	}
	if supplied := s.opts.EventExecutor; supplied != nil && sameInstance(clone.opts.EventExecutor, supplied) {
		clone.eventState().borrowedExecutor = true
	}
}

// cloneWithExtensions gives clone the scope tree and the extensions of s,
// copied under the registry mutex, so that no installation ends between the
// two copies.
func (s *RootScope) cloneWithExtensions(clone *RootScope) {
	registry := s.extensionRegistry()
	registry.mu.Lock()
	defer registry.mu.Unlock()

	clone.self = s.clone(clone, nil)
	s.copyExtensionsTo(clone, registry)
}

// copyExtensionsTo gives clone the installed and disabled extensions of
// registry, the container's, bound to clone's private scopes of the same
// names, so that Extend on the clone follows the repeat rules. Running
// installations, sessions and the waits-for graph are not copied, and the
// private scopes of running installations are detached from clone, so that
// it can install them itself. Call it after cloning the scope tree, with
// registry.mu held since before that.
func (s *RootScope) copyExtensionsTo(clone *RootScope, registry *extensionRegistry) {
	if len(registry.entries) == 0 {
		return
	}

	cloned := clone.extensionRegistry()
	cloned.installs = registry.installs
	for id, entry := range registry.entries {
		if entry.state == extensionInstalling {
			continue
		}
		copied := *entry
		if entry.scope != nil {
			copied.scope = clone.self.directChild(entry.scope.name)
			cloned.scopes[entry.plan.scopeName] = id
		}
		cloned.entries[id] = &copied
	}
	for scopeName, id := range registry.scopes {
		if registry.entries[id].state == extensionInstalling {
			clone.self.detachChild(scopeName)
		}
	}
}

// defaultShutdownSignals are the signals ShutdownOnSignals and
// ShutdownOnSignalsWithContext wait for when none is given: SIGTERM and
// SIGINT. Tests replace them with a signal they can send.
var defaultShutdownSignals = []os.Signal{syscall.SIGTERM, os.Interrupt}

// ShutdownOnSignals listens for the provided signals in order to gracefully stop services.
// It will block until receiving any of these signals.
// If no signal is provided, syscall.SIGTERM and os.Interrupt will be handled by default.
func (s *RootScope) ShutdownOnSignals(signals ...os.Signal) (os.Signal, *ShutdownReport) {
	return s.ShutdownOnSignalsWithContext(context.Background(), signals...)
}

// ShutdownOnSignalsWithContext listens for the provided signals in order to gracefully stop services.
// It will block until receiving any of these signals or until ctx ends.
// If no signal is provided, syscall.SIGTERM and os.Interrupt will be handled by default.
//
// ctx only ends the wait for a signal; it neither bounds nor cancels the
// shutdown. Whichever comes first, a signal or the end of ctx, the call shuts
// the container down, and runs the HookContainerClosing and
// HookContainerClosed hooks, with a context that keeps ctx's values but has
// no cancellation or deadline, so every service is shut down, as with
// [RootScope.ShutdownOnSignals]. It returns the signal, or nil when ctx ended
// first. The shutdown is unbounded: to bound it, wait for the signal yourself
// ([signal.NotifyContext]) and call [RootScope.ShutdownWithContext] with a
// new context that has your own deadline. A nil ctx is context.Background():
// the call then waits for a signal.
func (s *RootScope) ShutdownOnSignalsWithContext(ctx context.Context, signals ...os.Signal) (os.Signal, *ShutdownReport) {
	if ctx == nil {
		ctx = context.Background()
	}

	if len(signals) < 1 {
		signals = defaultShutdownSignals
	}

	ch := make(chan os.Signal, 5)
	signal.Notify(ch, signals...)

	var sig os.Signal
	select {
	case sig = <-ch:
		// got a signal
	case <-ctx.Done():
		// ctx ended first: no signal
	}

	signal.Stop(ch)
	close(ch)

	return sig, s.ShutdownWithContext(context.WithoutCancel(ctx))
}
