package doger

import (
	"context"
	"errors"
	"fmt"
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

// DefaultRootScope is a global instance of the root scope that can be used
// for simple dependency injection scenarios without creating a custom scope.
var DefaultRootScope = New()

// noOpLogf is a no-operation logging function used as a default when no logger is provided.
var noOpLogf = func(format string, args ...any) {}

// New creates a new dependency injection container with default options.
// This is the primary entry point for creating a new DI container.
//
// Parameters:
//   - packages: Optional package functions to execute during initialization
//
// Returns a new RootScope instance ready for service registration.
//
// Play: https://go.dev/play/p/-Fvet5zLoVY
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
// Play: https://go.dev/play/p/SmhFpWZGKUw
//
// Example:
//
//	opts := &doger.InjectorOpts{
//	    Logf: func(format string, args ...any) {
//	        log.Printf(format, args...)
//	    },
//	    HealthCheckParallelism: 10,
//	}
//	injector := doger.NewWithOpts(opts)
func NewWithOpts(opts *InjectorOpts, packages ...func(Injector)) *RootScope {
	if opts == nil {
		opts = &InjectorOpts{}
	}

	if opts.Logf == nil {
		opts.Logf = noOpLogf
	}

	if opts.HookBeforeRegistration == nil {
		opts.HookBeforeRegistration = []func(*Scope, string){}
	}
	if opts.HookAfterRegistration == nil {
		opts.HookAfterRegistration = []func(*Scope, string){}
	}
	if opts.HookBeforeInvocation == nil {
		opts.HookBeforeInvocation = []func(*Scope, string){}
	}
	if opts.HookAfterInvocation == nil {
		opts.HookAfterInvocation = []func(*Scope, string, error){}
	}
	if opts.HookBeforeShutdown == nil {
		opts.HookBeforeShutdown = []func(*Scope, string){}
	}
	if opts.HookAfterShutdown == nil {
		opts.HookAfterShutdown = []func(*Scope, string, error){}
	}

	root := &RootScope{
		self:            newScope(DefaultRootScopeName, nil, nil),
		opts:            opts,
		dag:             newDAG(),
		healthCheckPool: nil,
	}
	root.self.rootScope = root

	if opts.HealthCheckParallelism > 0 {
		root.healthCheckPool = newJobPool[error](opts.HealthCheckParallelism)
		root.healthCheckPool.start()
	}

	root.opts.Logf("DI: injector created")

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
	self            *Scope          // The root scope instance
	opts            *InjectorOpts   // Configuration options
	dag             *DAG            // Dependency graph for service relationships
	healthCheckPool *jobPool[error] // Pool for parallel health check operations

	nextRegIndex atomic.Uint64 // next registration index, shared by every scope of this container
	declareMu    sync.Mutex    // serializes DeclareDependency (cycle check plus add)

	// Lifecycle state. Phases change only by compare-and-set under lifecycleMu,
	// which is never held while user code runs.
	lifecycleMu sync.Mutex
	startPhase  startPhase
	startCancel context.CancelCauseFunc // cancels the running Start's context
	startDone   chan struct{}           // closed when the running Start returned
	closePhase  closePhase
	closeDone   chan struct{}   // closed when the winning Shutdown finished
	closeReport *ShutdownReport // private copy of the winner's report
}

// closePhase is the shutdown state of a root container.
type closePhase int

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

// HealthCheck performs health checks on all services in the root scope and all its descendant scopes.
func (s *RootScope) HealthCheck() map[string]error { return s.self.HealthCheck() }

// HealthCheckWithContext performs health checks with context support for cancellation and timeouts.
func (s *RootScope) HealthCheckWithContext(ctx context.Context) map[string]error {
	return s.self.HealthCheckWithContext(ctx)
}

// Shutdown gracefully shuts down the root scope and all its descendant scopes.
// It runs once per root container; see [RootScope.ShutdownWithContext].
func (s *RootScope) Shutdown() *ShutdownReport { return s.ShutdownWithContext(context.Background()) }

// ShutdownWithContext gracefully shuts down the root scope and all its descendant scopes with context support.
// This method ensures proper cleanup of the health check pool and all registered services.
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
//     a custom Logf), the panic propagates to it, and every other caller gets
//     a failed report containing "DI: root shutdown did not complete".
//   - A nil ctx is treated as context.Background().
//   - Services registered on the root after the first Shutdown are not shut
//     down by a later root Shutdown.
//
// A Shutdown that begins while [RootScope.StartWithContext] runs cancels
// Start, with cause [ErrStartAfterShutdown], and waits for it to return
// before shutting services down. If ctx ends first, the report fails with a
// root-scope error wrapping ctx.Err(), and shutdown proceeds while Start is
// still running. Never call Shutdown synchronously from a Starter, handler,
// runner, provider or hook running during Start: it would wait for itself.
//
// Only the [RootScope] methods follow this model. A shutdown reached through
// the root's *Scope (a provider's injector, or [Injector.Shutdown] on a
// virtual scope) and Scope.Shutdown on child scopes bypass it and behave as
// before.
func (s *RootScope) ShutdownWithContext(ctx context.Context) *ShutdownReport {
	if ctx == nil {
		ctx = context.Background()
	}

	ticket := s.beginClose()
	if !ticket.winner {
		return s.waitForClose(ctx, ticket.done)
	}

	report := s.shutdownAsWinner(ctx, ticket)

	// pointClosed: closeDone is closed, so code here may call Shutdown().
	return report
}

// waitForClose waits for the winning Shutdown, bounded by ctx, and returns a
// copy of its report.
func (s *RootScope) waitForClose(ctx context.Context, done <-chan struct{}) *ShutdownReport {
	if awaitDone(done, ctx) {
		s.lifecycleMu.Lock()
		defer s.lifecycleMu.Unlock()
		return cloneShutdownReport(s.closeReport)
	}

	return s.rootFailureReport(fmt.Errorf("DI: waiting for root shutdown: %w", ctx.Err()))
}

// shutdownAsWinner shuts the container down. Its deferred finishClose runs
// even when this goroutine panics, so waiters are always released.
func (s *RootScope) shutdownAsWinner(ctx context.Context, ticket closeTicket) (report *ShutdownReport) {
	defer func() { s.finishClose(report) }()
	defer func() {
		// Only the winner touches the pool.
		if s.healthCheckPool != nil {
			s.healthCheckPool.stop()
			s.healthCheckPool = nil
		}
	}()

	startErr := waitForStart(ctx, ticket)

	// pointClosing

	report = s.self.ShutdownWithContext(ctx)
	if startErr != nil {
		report.Errors[newServiceDescription(s.ID(), s.Name(), "")] = startErr
		report.Succeed = false
	}

	return report
}

// waitForStart cancels a Start that was running when the close phase began,
// with cause ErrStartAfterShutdown, and waits for it to return, bounded by
// ctx. It returns an error when ctx ended first.
func waitForStart(ctx context.Context, ticket closeTicket) error {
	if !ticket.inFlight {
		return nil
	}

	ticket.startCancel(ErrStartAfterShutdown)
	if !awaitDone(ticket.startDone, ctx) {
		return fmt.Errorf("DI: waiting for start: %w", ctx.Err())
	}

	return nil
}

// closeTicket is what a Shutdown caller learns when it enters the close phase.
type closeTicket struct {
	done   chan struct{} // closed when the winner finished
	winner bool          // this caller runs the shutdown

	// winner only: a Start running when the close phase began
	inFlight    bool
	startCancel context.CancelCauseFunc
	startDone   chan struct{}
}

// beginClose moves the close phase from open to closing. The caller that
// does it is the winner and runs the shutdown; any other caller waits on done.
func (s *RootScope) beginClose() closeTicket {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.closePhase != closeOpen {
		return closeTicket{done: s.closeDone}
	}

	s.closePhase = closeClosing
	s.closeDone = make(chan struct{})

	return closeTicket{
		done:        s.closeDone,
		winner:      true,
		inFlight:    s.startPhase == startStarting,
		startCancel: s.startCancel,
		startDone:   s.startDone,
	}
}

// finishClose stores a private copy of the winner's report and releases the
// waiters. A nil report means the winner panicked before producing one.
func (s *RootScope) finishClose(report *ShutdownReport) {
	stored := cloneShutdownReport(report)
	if report == nil {
		stored = s.rootFailureReport(errors.New("DI: root shutdown did not complete"))
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
	select {
	case <-done:
		return true
	default:
	}

	select {
	case <-done:
		return true
	case <-ctx.Done():
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
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

func (s *RootScope) serviceShutdown(ctx context.Context, name string) error {
	return s.self.serviceShutdown(ctx, name)
}
func (s *RootScope) onServiceInvoke(name string) { s.self.onServiceInvoke(name) }

func (s *RootScope) queueServiceHealthcheck(ctx context.Context, scope *Scope, serviceName string) <-chan error {
	cancel := func() {}
	if s.opts.HealthCheckTimeout > 0 {
		// `ctx` might already contain a timeout, but we add another one
		ctx, cancel = context.WithTimeout(ctx, s.opts.HealthCheckTimeout)
	}

	// when no pooling policy has been defined
	if s.opts.HealthCheckParallelism == 0 || s.healthCheckPool == nil {
		err := make(chan error, 1) // a single message will be sent (nil or error)

		go func() {
			defer cancel()

			select {
			case e := <-func() chan error {
				c := make(chan error, 1)
				go func() { c <- scope.serviceHealthCheck(ctx, serviceName) }()
				return c
			}():
				err <- e
				close(err)
			case <-ctx.Done():
				err <- fmt.Errorf("%w: %s", ErrHealthCheckTimeout, ctx.Err()) //nolint:errorlint
				close(err)
			}
		}()

		return err
	}

	// delegate execution to the healthcheck pool
	return s.healthCheckPool.rpc(func() error {
		defer cancel()
		return scope.serviceHealthCheck(ctx, serviceName)
	})
}

/**
 * RootScope stuff
 */

// AddBeforeRegistrationHook adds a hook that will be called before a service is registered.
//
// Play: https://go.dev/play/p/IstT_4oovQD
func (s *RootScope) AddBeforeRegistrationHook(hook func(*Scope, string)) {
	s.opts.HookBeforeRegistration = append(s.opts.HookBeforeRegistration, hook)
}

// AddAfterRegistrationHook adds a hook that will be called after a service is registered.
//
// Play: https://go.dev/play/p/IstT_4oovQD
func (s *RootScope) AddAfterRegistrationHook(hook func(*Scope, string)) {
	s.opts.HookAfterRegistration = append(s.opts.HookAfterRegistration, hook)
}

// AddBeforeInvocationHook adds a hook that will be called before a service is invoked.
//
// Play: https://go.dev/play/p/IstT_4oovQD
func (s *RootScope) AddBeforeInvocationHook(hook func(*Scope, string)) {
	s.opts.HookBeforeInvocation = append(s.opts.HookBeforeInvocation, hook)
}

// AddAfterInvocationHook adds a hook that will be called after a service is invoked.
//
// Play: https://go.dev/play/p/IstT_4oovQD
func (s *RootScope) AddAfterInvocationHook(hook func(*Scope, string, error)) {
	s.opts.HookAfterInvocation = append(s.opts.HookAfterInvocation, hook)
}

// AddBeforeShutdownHook adds a hook that will be called before a service is shutdown.
//
// Play: https://go.dev/play/p/FFKDcV0hMJx
func (s *RootScope) AddBeforeShutdownHook(hook func(*Scope, string)) {
	s.opts.HookBeforeShutdown = append(s.opts.HookBeforeShutdown, hook)
}

// AddAfterShutdownHook adds a hook that will be called after a service is shutdown.
//
// Play: https://go.dev/play/p/FFKDcV0hMJx
func (s *RootScope) AddAfterShutdownHook(hook func(*Scope, string, error)) {
	s.opts.HookAfterShutdown = append(s.opts.HookAfterShutdown, hook)
}

// Clone clones injector with provided services but not with invoked instances.
//
// Play: https://go.dev/play/p/DqIlXhZ8c4t
func (s *RootScope) Clone() *RootScope {
	return s.CloneWithOpts(s.opts.copy())
}

// CloneWithOpts clones injector with provided services but not with invoked instances, with options.
//
// Play: https://go.dev/play/p/fT-v63fbFk5
func (s *RootScope) CloneWithOpts(opts *InjectorOpts) *RootScope {
	clone := NewWithOpts(opts)
	clone.self = s.clone(clone, nil)
	// Read after copying: every copied wrapper got its index before it was
	// stored, so later registrations in the clone come after all of them.
	clone.nextRegIndex.Store(s.nextRegIndex.Load())

	s.opts.Logf("DI: injector cloned")

	return clone
}

// ShutdownOnSignals listens for the provided signals in order to gracefully stop services.
// It will block until receiving any of these signals.
// If no signal is provided, syscall.SIGTERM and os.Interrupt will be handled by default.
func (s *RootScope) ShutdownOnSignals(signals ...os.Signal) (os.Signal, *ShutdownReport) {
	return s.ShutdownOnSignalsWithContext(context.Background(), signals...)
}

// ShutdownOnSignalsWithContext listens for the provided signals in order to gracefully stop services.
// It will block until receiving any of these signals.
// If no signal is provided, syscall.SIGTERM and os.Interrupt will be handled by default.
func (s *RootScope) ShutdownOnSignalsWithContext(ctx context.Context, signals ...os.Signal) (os.Signal, *ShutdownReport) {
	// Make sure there is at least syscall.SIGTERM and os.Interrupt as a signal
	if len(signals) < 1 {
		signals = append(signals, syscall.SIGTERM, os.Interrupt)
	}

	ch := make(chan os.Signal, 5)
	signal.Notify(ch, signals...)

	var sig os.Signal
	select {
	case sig = <-ch:
		// got a signal
	case <-ctx.Done():
		signal.Stop(ch)
		close(ch)
		return nil, s.ShutdownWithContext(ctx)
	}

	signal.Stop(ch)
	close(ch)

	return sig, s.ShutdownWithContext(ctx)
}
