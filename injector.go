package doger

import (
	"context"
	"time"

	"github.com/jonboulle/clockwork"
)

// Injector is the main interface for dependency injection containers.
// It provides methods for service registration, resolution, lifecycle management,
// and scope hierarchy management.
//
// The Injector interface is implemented by both RootScope and Scope types,
// allowing for a consistent API across different levels of the scope hierarchy.
type Injector interface {
	// Public API methods for scope and service management

	// ID returns the unique identifier of the injector.
	ID() string

	// Name returns the human-readable name of the injector.
	Name() string

	// Scope creates a new child scope with the given name.
	// Optional package functions can be provided to execute during scope creation.
	Scope(string, ...func(Injector)) *Scope

	// RootScope returns the root scope of the injector hierarchy.
	RootScope() *RootScope

	// Ancestors returns the list of all parent scopes in order from immediate parent to root.
	Ancestors() []*Scope

	// Children returns the list of immediate child scopes.
	Children() []*Scope

	// ChildByID searches for a child scope by its unique ID across the entire scope hierarchy.
	ChildByID(string) (*Scope, bool)

	// ChildByName searches for a child scope by its name across the entire scope hierarchy.
	ChildByName(string) (*Scope, bool)

	// ListProvidedServices returns all services available in the current scope and all its ancestor scopes.
	ListProvidedServices() []ServiceDescription

	// ListInvokedServices returns only the services that have been actually invoked in the current scope and its ancestors.
	ListInvokedServices() []ServiceDescription

	// HealthCheck performs health checks on all services in the current scope and its ancestors.
	HealthCheck() map[string]error

	// HealthCheckWithContext performs health checks with context support for cancellation and timeouts.
	HealthCheckWithContext(context.Context) map[string]error

	// Shutdown gracefully shuts down the injector and all its descendant scopes.
	Shutdown() *ShutdownReport

	// ShutdownWithContext gracefully shuts down the injector and all its descendant scopes with context support.
	ShutdownWithContext(context.Context) *ShutdownReport

	// clone creates a deep copy of the injector with all its services and child scopes.
	clone(*RootScope, *Scope) *Scope

	// Internal service lifecycle methods

	// serviceExist checks if a service with the given name exists in the current scope.
	serviceExist(string) bool
	// serviceExistRec checks if a service with the given name exists in the current scope or any of its ancestor scopes.
	serviceExistRec(string) bool
	// serviceGet retrieves a service from the current scope by name.
	serviceGet(string) (any, bool)
	// serviceGetRec retrieves a service by name from the current scope or any of its ancestor scopes.
	serviceGetRec(string) (any, *Scope, bool)
	// serviceSet registers a service in the current scope with the given name.
	// Note: This method is not protected against double registration.
	serviceSet(string, any)
	// serviceForEach iterates over all services in the current scope.
	serviceForEach(func(string, *Scope, any) bool)
	// serviceForEachRec iterates over all services in the current scope and all ancestor scopes.
	serviceForEachRec(func(string, *Scope, any) bool)
	// serviceHealthCheck performs a health check on a specific service in the current scope.
	serviceHealthCheck(context.Context, string) error
	// serviceShutdown gracefully shuts down a specific service in the current scope.
	serviceShutdown(context.Context, string) error
	// onServiceInvoke is called whenever a service is invoked in this scope.
	onServiceInvoke(string)
}

// getInjectorOrDefault returns the provided injector if it's not nil, otherwise returns the default root scope.
// This function ensures that DI operations always have a valid injector to work with.
//
// Parameters:
//   - i: The injector to check, can be nil
//
// Returns the provided injector if not nil, or DefaultRootScope if nil.
func getInjectorOrDefault(i Injector) Injector {
	if i != nil {
		return i
	}

	return DefaultRootScope
}

// InjectorOpts contains all configuration options for the dependency injection container.
// These options control logging, hooks, health checks, and other behavioral aspects
// of the DI container.
type InjectorOpts struct {
	// HookBeforeRegistration is called before a service is registered in a scope.
	// This hook can be used for validation, logging, or other pre-registration tasks.
	HookBeforeRegistration []func(scope *Scope, serviceName string)

	// HookAfterRegistration is called after a service is successfully registered in a scope.
	// This hook can be used for logging, metrics collection, or other post-registration tasks.
	HookAfterRegistration []func(scope *Scope, serviceName string)

	// HookBeforeInvocation is called before a service is invoked (instantiated).
	// This hook can be used for logging, metrics, or other pre-invocation tasks.
	HookBeforeInvocation []func(scope *Scope, serviceName string)

	// HookAfterInvocation is called after a service is invoked, with the result error.
	// This hook can be used for logging, metrics collection, or error handling.
	HookAfterInvocation []func(scope *Scope, serviceName string, err error)

	// HookAfterBuild is called with each instance the container builds:
	// after every successful provider call (each transient instance
	// included) and after an eager value's first successful invocation. It
	// is not called for a failed build, a cached lazy instance or an alias,
	// and runs between the before- and after-invocation hooks. A panic
	// propagates to the caller, as for the other per-service hooks. It runs
	// after the service released its lock, so it may invoke, health-check or
	// explain that service; another goroutine may obtain the instance before
	// the hook returned.
	HookAfterBuild []func(scope *Scope, serviceName string, instance any)

	// HookBeforeShutdown is called before a service is shut down.
	// This hook can be used for cleanup preparation or logging.
	HookBeforeShutdown []func(scope *Scope, serviceName string)

	// HookAfterShutdown is called after a service is shut down, with the result error.
	// This hook can be used for logging, metrics collection, or error handling.
	HookAfterShutdown []func(scope *Scope, serviceName string, err error)

	// HookBeforeStart is called by Start just before a service's Start method.
	// With StartParallelism > 1, calls for different services may run
	// concurrently.
	HookBeforeStart []func(scope *Scope, serviceName string)

	// HookAfterStart is called by Start just after a service's Start method,
	// with the error the method returned, unwrapped (nil on success; a
	// recovered panic as an error). With StartParallelism > 1, calls for
	// different services may run concurrently.
	HookAfterStart []func(scope *Scope, serviceName string, err error)

	// HookContainerStarting is called by the root container's first Start,
	// after its one-shot guard and before its context check, discovery or any
	// instantiation, with the Start-time context. A non-nil error vetoes
	// Start: the remaining Starting hooks do not run, nothing is built or
	// started, and Start returns an error wrapping it. A panic is a veto too:
	// Start returns an error containing the panic value (wrapping it when it
	// is an error), and HookContainerPanic is not called.
	//
	// A Starting hook must not call root Shutdown synchronously without a
	// deadline: Shutdown waits for the running Start, which waits for the hook.
	// With a deadline, Shutdown returns a failed report when it expires, and
	// Start then fails with an error matching ErrStartAfterShutdown.
	HookContainerStarting []func(ctx context.Context, scope *RootScope) error

	// HookContainerStarted is called by the first Start once its pipeline
	// succeeded: after every Starter and every StageAfterStart handler,
	// including the Runnable handler. It receives the context passed to
	// Start. Use it for last wiring, such as route registration. It runs
	// after the running Start was released, so it may call root Shutdown.
	HookContainerStarted []func(ctx context.Context, scope *RootScope)

	// HookContainerReady is called by the first Start after every Started
	// hook has returned, with the context passed to Start. Use it to signal
	// readiness to an orchestrator. It may call root Shutdown.
	HookContainerReady []func(ctx context.Context, scope *RootScope)

	// HookContainerFailed is called by the first Start when it fails after
	// its one-shot guard, with the context passed to Start and the exact
	// error Start returns. It may call root Shutdown, for example to clean up
	// what did start. When a per-service hook panics during Start (such as
	// HookBeforeStart or HookAfterBuild), the Failed hooks run with an error
	// wrapping the panic value before Start re-raises it; a Failed hook panic
	// then goes to HookContainerPanic when set, and is logged otherwise.
	HookContainerFailed []func(ctx context.Context, scope *RootScope, err error)

	// HookContainerClosing is called by the root Shutdown that begins the
	// close phase (the first one), before any service is shut down, with the
	// Shutdown context. It fires whether or not Start ran or succeeded.
	//
	// A Closing hook must not call root Shutdown synchronously without a
	// deadline: that call waits for the running Shutdown, which waits for the
	// hook. With a deadline, it returns a failed report when it expires.
	//
	// Ordering with Start: a Shutdown that begins while Start runs cancels
	// it and fires Closing only after Start's pipeline returned, or after the
	// Shutdown context ended. Once the pipeline has returned, Closing and
	// Closed may run before or while the Started, Ready or Failed hooks run on
	// the Start goroutine: hooks sharing state across these points must
	// synchronize it.
	HookContainerClosing []func(ctx context.Context, scope *RootScope)

	// HookContainerClosed is called by the first root Shutdown once every
	// service is shut down and the close phase is closed, with the Shutdown
	// context and the report Shutdown returns. Mutating the report is
	// unsupported. Concurrent Shutdown callers may return before the Closed
	// hooks have run. A Closed hook may call root Shutdown, which returns a
	// copy of the first report.
	HookContainerClosed []func(ctx context.Context, scope *RootScope, report *ShutdownReport)

	// HookScopeStarting is called by Scope.Start on a child scope, after its
	// phase check and before anything starts, with the scope. A non-nil error
	// or a panic vetoes Scope.Start, which returns an error wrapping it. It is
	// not called when the scope, or one of its ancestors, is shutting down:
	// Scope.Start then starts nothing and returns nil. The root's own scope
	// never fires scope hooks.
	HookScopeStarting []func(ctx context.Context, scope *Scope) error

	// HookScopeStarted is called by Scope.Start on a child scope once its
	// services started successfully. It is not called when the scope, or one
	// of its ancestors, was shutting down, so that Scope.Start started
	// nothing. A panic is recovered and dispatched like a container observer
	// hook panic, with the point "scope-started" (see HookContainerPanic).
	HookScopeStarted []func(ctx context.Context, scope *Scope)

	// HookScopeClosing is called when a child scope shuts down, directly or
	// as part of its parent's shutdown, after its children shut down and
	// before its own services. Sibling scopes shut down in parallel, so their
	// hooks may run concurrently. A panic is dispatched with the point
	// "scope-closing" (see HookContainerPanic).
	HookScopeClosing []func(ctx context.Context, scope *Scope)

	// HookScopeClosed is called when a child scope shut down, after its
	// services. A panic is dispatched with the point "scope-closed" (see
	// HookContainerPanic).
	HookScopeClosed []func(ctx context.Context, scope *Scope)

	// HookContainerPanic receives the first panic recovered from a Started,
	// Ready, Failed, Closing or Closed hook, once the operation is complete:
	// every hook of the list has run, and so has the rest of Start or
	// Shutdown. hook names the point ("started", "ready", "failed",
	// "closing" or "closed"); later panics of the same operation are logged
	// with Logf. Start or Shutdown then returns its normal result. Scope
	// observer hooks follow the same policy for Scope.Start and a child
	// scope shutdown, with the points "scope-started", "scope-closing" and
	// "scope-closed". Default (nil): re-panic with the recovered value.
	// Panics in Starting hooks are vetoes and never reach it.
	HookContainerPanic func(scope *RootScope, hook string, recovered any)

	// Logf is the logging function used by the DI container for internal logging.
	// If not provided, no logging will occur. This function should handle the format
	// string and arguments similar to fmt.Printf.
	Logf func(format string, args ...any)

	// HealthCheckParallelism controls the number of concurrent health checks that can run simultaneously.
	// Default: all health checks run in parallel (unlimited).
	// Setting this to a positive number limits the concurrency for better resource management.
	HealthCheckParallelism uint

	// HealthCheckGlobalTimeout sets a global timeout for all health check operations.
	// This timeout applies to the entire health check process across all services.
	// Default: no timeout (health checks can run indefinitely).
	HealthCheckGlobalTimeout time.Duration

	// HealthCheckTimeout sets a timeout for individual service health checks.
	// This timeout applies to each service's health check method, both in
	// HealthCheck() and in the single-service HealthCheck[T],
	// HealthCheckWithContext[T], HealthCheckNamed and
	// HealthCheckNamedWithContext functions.
	// Default: no timeout (individual health checks can run indefinitely).
	HealthCheckTimeout time.Duration

	// StructTagKey specifies the tag key used for struct field injection.
	// Default: "do" (see DefaultStructTagKey constant).
	// This allows customization of the struct tag format for injection.
	StructTagKey string

	// Clock is the time source of the container. It measures HealthCheckTimeout
	// and HealthCheckGlobalTimeout, lazy service build times, and shutdown
	// durations. Child scopes and Clone use the root scope's clock.
	// Default: the real clock (clockwork.NewRealClock()).
	// Set a clockwork.NewFakeClock() to control time in tests. A health check
	// running on a fake clock must wait on ctx.Done(): ctx.Err() blocks on a
	// live fake-clock context.
	Clock clockwork.Clock

	// DisableStarterAutoDetection makes Start start only the services
	// registered with AsStarter. By default, every non-transient service whose
	// registered type implements a Starter variant is started too.
	DisableStarterAutoDetection bool

	// StartParallelism is the number of Starters that Start, Refresh and
	// Scope.Start may run concurrently, each only after every Starter it
	// depends on has started. The first failure stops scheduling new
	// Starters; running ones are awaited, then the failure is returned.
	// Default: 0 or 1, sequential. With n > 1, HookBeforeStart and
	// HookAfterStart may run concurrently and must synchronize their state.
	// Kind handlers always run sequentially.
	StartParallelism uint

	// EnableRunnerAutoDetection makes Start run, as a Runnable, every
	// non-transient service whose registered type implements Runner. By
	// default, runners must opt in with AsRunnable, because Run is too
	// common a method name to auto-detect. Custom kinds always need opt-in.
	EnableRunnerAutoDetection bool
}

func (o *InjectorOpts) copy() *InjectorOpts {
	return &InjectorOpts{
		HookBeforeRegistration:   append([]func(*Scope, string){}, o.HookBeforeRegistration...),
		HookAfterRegistration:    append([]func(*Scope, string){}, o.HookAfterRegistration...),
		HookBeforeInvocation:     append([]func(*Scope, string){}, o.HookBeforeInvocation...),
		HookAfterInvocation:      append([]func(*Scope, string, error){}, o.HookAfterInvocation...),
		HookAfterBuild:           append([]func(*Scope, string, any){}, o.HookAfterBuild...),
		HookBeforeShutdown:       append([]func(*Scope, string){}, o.HookBeforeShutdown...),
		HookAfterShutdown:        append([]func(*Scope, string, error){}, o.HookAfterShutdown...),
		HookBeforeStart:          append([]func(*Scope, string){}, o.HookBeforeStart...),
		HookAfterStart:           append([]func(*Scope, string, error){}, o.HookAfterStart...),
		HookContainerStarting:    append([]func(context.Context, *RootScope) error{}, o.HookContainerStarting...),
		HookContainerStarted:     append([]func(context.Context, *RootScope){}, o.HookContainerStarted...),
		HookContainerReady:       append([]func(context.Context, *RootScope){}, o.HookContainerReady...),
		HookContainerFailed:      append([]func(context.Context, *RootScope, error){}, o.HookContainerFailed...),
		HookContainerClosing:     append([]func(context.Context, *RootScope){}, o.HookContainerClosing...),
		HookContainerClosed:      append([]func(context.Context, *RootScope, *ShutdownReport){}, o.HookContainerClosed...),
		HookContainerPanic:       o.HookContainerPanic,
		HookScopeStarting:        append([]func(context.Context, *Scope) error{}, o.HookScopeStarting...),
		HookScopeStarted:         append([]func(context.Context, *Scope){}, o.HookScopeStarted...),
		HookScopeClosing:         append([]func(context.Context, *Scope){}, o.HookScopeClosing...),
		HookScopeClosed:          append([]func(context.Context, *Scope){}, o.HookScopeClosed...),
		Logf:                     o.Logf,
		HealthCheckParallelism:   o.HealthCheckParallelism,
		HealthCheckGlobalTimeout: o.HealthCheckGlobalTimeout,
		HealthCheckTimeout:       o.HealthCheckTimeout,
		StructTagKey:             o.StructTagKey,
		Clock:                    o.Clock,

		DisableStarterAutoDetection: o.DisableStarterAutoDetection,
		StartParallelism:            o.StartParallelism,
		EnableRunnerAutoDetection:   o.EnableRunnerAutoDetection,
	}
}

// initDefaults replaces unset options with their defaults: a no-op Logf,
// empty hook lists and the real clock.
func (o *InjectorOpts) initDefaults() {
	if o.Logf == nil {
		o.Logf = noOpLogf
	}

	emptyIfNil(&o.HookBeforeRegistration)
	emptyIfNil(&o.HookAfterRegistration)
	emptyIfNil(&o.HookBeforeInvocation)
	emptyIfNil(&o.HookAfterInvocation)
	emptyIfNil(&o.HookAfterBuild)
	emptyIfNil(&o.HookBeforeShutdown)
	emptyIfNil(&o.HookAfterShutdown)
	emptyIfNil(&o.HookBeforeStart)
	emptyIfNil(&o.HookAfterStart)
	emptyIfNil(&o.HookContainerStarting)
	emptyIfNil(&o.HookContainerStarted)
	emptyIfNil(&o.HookContainerReady)
	emptyIfNil(&o.HookContainerFailed)
	emptyIfNil(&o.HookContainerClosing)
	emptyIfNil(&o.HookContainerClosed)
	emptyIfNil(&o.HookScopeStarting)
	emptyIfNil(&o.HookScopeStarted)
	emptyIfNil(&o.HookScopeClosing)
	emptyIfNil(&o.HookScopeClosed)

	if o.Clock == nil {
		o.Clock = clockwork.NewRealClock()
	}
}

// emptyIfNil replaces a nil slice with an empty one.
func emptyIfNil[T any](s *[]T) {
	if *s == nil {
		*s = []T{}
	}
}

func (o *InjectorOpts) onBeforeRegistration(scope *Scope, serviceName string) {
	for _, fn := range o.HookBeforeRegistration {
		fn(scope, serviceName)
	}
}

func (o *InjectorOpts) onAfterRegistration(scope *Scope, serviceName string) {
	for _, fn := range o.HookAfterRegistration {
		fn(scope, serviceName)
	}
}

func (o *InjectorOpts) onBeforeInvocation(scope *Scope, serviceName string) {
	for _, fn := range o.HookBeforeInvocation {
		fn(scope, serviceName)
	}
}

func (o *InjectorOpts) onAfterInvocation(scope *Scope, serviceName string, err error) {
	for _, fn := range o.HookAfterInvocation {
		fn(scope, serviceName, err)
	}
}

func (o *InjectorOpts) onAfterBuild(scope *Scope, serviceName string, instance any) {
	for _, fn := range o.HookAfterBuild {
		if fn != nil {
			fn(scope, serviceName, instance)
		}
	}
}

func (o *InjectorOpts) onBeforeShutdown(scope *Scope, serviceName string) {
	for _, fn := range o.HookBeforeShutdown {
		fn(scope, serviceName)
	}
}

func (o *InjectorOpts) onAfterShutdown(scope *Scope, serviceName string, err error) {
	for _, fn := range o.HookAfterShutdown {
		fn(scope, serviceName, err)
	}
}

func (o *InjectorOpts) onBeforeStart(scope *Scope, serviceName string) {
	for _, fn := range o.HookBeforeStart {
		fn(scope, serviceName)
	}
}

func (o *InjectorOpts) onAfterStart(scope *Scope, serviceName string, err error) {
	for _, fn := range o.HookAfterStart {
		fn(scope, serviceName, err)
	}
}
