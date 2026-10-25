# Proposal

## Why

The container has build, health-check and shutdown, but no notion of *starting*. Today:
- Background components such as servers, consumers and schedulers must be started by hand after wiring.
- One-shot startup work (runners) has no home.
- There is no way to introduce a new component type (e.g. `Scheduled`) whose instances a dedicated handler discovers and wires into another service (e.g. a scheduler).

These are the first features needed to match common DI container practice, built in a Go-idiomatic way: interfaces, functional options, generics.

Starting and shutting down must be serialized at the root, and root `Shutdown` is not safe today when called concurrently (see Defect). This change fixes that as part of the same lifecycle model.

## What Changes

**Start lifecycle**
- Add `Starter` interface variants that parallel the existing `Shutdowner` family: `Starter` (`Start()`), `StarterWithError` (`Start() error`), `StarterWithContext` (`Start(context.Context)`), `StarterWithContextAndError` (`Start(context.Context) error`). There is no Stop counterpart. A started service is stopped by its existing `Shutdowner*` method during `Shutdown`.
  - Unlike `Shutdowner*`, which is detected on the **instance** when shutting down, `Starter*` is detected on the **registered type**, so lazy services need not be built to be discovered. A service registered under an interface type that lacks `Start` is not auto-detected even if its concrete instance has `Start`; it opts in with `AsStarter()`.
- Add `Start() error` / `StartWithContext(ctx) error` on `RootScope`, mirroring `Shutdown` / `ShutdownWithContext`.
  - Starting is a container-level operation. It covers the root scope and every descendant scope that exists when `Start` takes its discovery snapshot, parent scopes before children.
  - `Scope` and the `Injector` interface do not gain `Start` in this change. Scope-level start, and scopes created after `Start`, are phase 2. Calls that reach the root's `*Scope` directly (a provider's injector, `Component.Scope`, `virtualScope.Shutdown`) bypass the root lifecycle; only `RootScope` methods are container-level.
- Start runs as a pipeline:
  1. Discover candidates by type or opt-in and instantiate them. Every service's invoked dependencies and static `DependsOn` dependencies finish construction before it. Independent services are taken in registration order.
  2. Run `StageBeforeStart` component-kind handlers.
  3. Reject dependency cycles that only became visible after construction (for example one closed by a dynamic edge), with the existing `ErrCircularDependency`.
  4. Start `Starter*` services one at a time in dependency (DAG) order, with parent scopes before child scopes and registration order for ties. Parallel start of independent services is a possible later option.
  5. Run `StageAfterStart` handlers, such as runnables.
- The dependency DAG is the single source of ordering for start and shutdown. There is no `Phase`/`Order` interface. Explicit ordering is expressed as a declared dependency edge. Registration order deterministically breaks ties between services that have no dependency between them; today that order follows Go map iteration and is nondeterministic.
- Construction remains initialization, following the Go constructor idiom: the provider returns a ready value. No separate post-construction initializer callback is added.
- On the first error, `Start` stops the pipeline and returns a wrapped error that identifies the failing service. It does **not** roll back. Cleanup is the caller's job (`Shutdown()`), and services must tolerate `Shutdown` without a prior `Start`.
- Lifecycle model, owned by this change and extended by `add-container-lifecycle-hooks`: two phases on `RootScope`, changed only by compare-and-set under a mutex that is never held while user code runs.
  - Start phase `idle → starting → started | failed`. `Start` is one-shot: any later call returns `ErrAlreadyStarted`, including after a failed Start.
  - Close phase `open → closing → closed`. `Start` after root `Shutdown` began returns `ErrStartAfterShutdown`, which takes precedence.
- `StartWithContext` derives its own cancellable context and checks it before each candidate, kind-handler call and Starter. A cancelled or expired context stops the pipeline with an error that wraps `ctx.Err()`. The context given to Start-time code is cancelled when `Start` returns.
- Root `Shutdown` during a running `Start` cancels that context with cause `ErrStartAfterShutdown`, waits for `Start` to return or for its own context to end, then shuts services down. Start stops before its next Starter or handler. A Starter that ignores its context and finishes after Shutdown stopped waiting is not rolled back (documented).
- `Runner` is **one-shot**: `Run` must return. Long-running work belongs in a Starter that spawns its own goroutine plus a Shutdowner that stops it. A runner that honours its context returns when Shutdown cancels Start.
- A panic inside a `Start*` method or a kind handler is recovered and returned as an error naming the service. A panic that escapes `Start` (for example from a hook) still releases the lifecycle state, so `Shutdown` never blocks on it.
- Transient services are never started or handled. A factory has no single instance to start. Opting a transient registration into a kind, or into `AsStarter()`, panics at registration.
- A `Starter*` that implements no `Shutdowner*` variant is allowed. Its lifecycle simply ends with the process.
- Add per-service start hooks `HookBeforeStart []func(*Scope, string)` / `HookAfterStart []func(*Scope, string, error)` to `InjectorOpts`, plus `RootScope.AddBeforeStartHook` / `AddAfterStartHook`, mirroring the per-service shutdown hooks.

**Bug fix: root Shutdown runs once**
- Root `Shutdown`/`ShutdownWithContext` shuts services down exactly once per root container. The caller that moves the close phase from `open` to `closing` does the work; concurrent and later callers wait for it (bounded by their own context) and return a copy of its report.
- **Behavior change (flagged):** a second root `Shutdown` returns a copy of the first report instead of an empty successful one, and no longer shuts down services registered on the root after the first `Shutdown`. Child `Scope.Shutdown` is unchanged.
- A caller that arrives after the shutdown has completed always gets the first report, even if its own context has already ended. The stored report is isolated from every caller's copy, including the first caller's.
- A nil Shutdown context is treated as `context.Background()`, as a nil Start context is. Before, it panicked as soon as a built service had a `Shutdowner*` method.
- If the first caller panics on its own goroutine before producing a report, the close still completes and every other caller gets a failed report instead of nil.

**Registration options and metadata**
- **BREAKING (signature)**: `Provide*`, `Override*` and the `Package` helpers (`Lazy*`, `Eager*`, `Transient*`) gain a trailing variadic `...ProvideOption`. Every existing call compiles unchanged. Code that stores these generic functions as values of the old function type (assignment, or passing them as an argument of that type) no longer compiles. Migration: wrap the call in a closure, e.g. `func(i doger.Injector, p doger.Provider[T]) { doger.Provide(i, p) }`. The note goes into `docs/docs/upgrading/`.
- `ProvideOption` is an opaque functional-option type. Consumers obtain options only from constructors this library exports: currently `AsStarter()`, `DependsOn[T]()`, `DependsOnNamed(name)`, `kind.With(meta)`, `AsRunnable()`. Later changes may add more (e.g. `WithOrder` in `add-collection-invocation`).
- Each registration carries metadata. `clone()` copies it, and `Override*` replaces it. Aliases (`As`/`Bind`) do not inherit opt-ins, so one service is never started or handled twice.
- Invalid opt-ins panic at registration, before the service is stored.

**Component kinds**
- Add a typed component kind, `Kind[T, M]`, created with `NewKind[T, M](name)`: a target interface `T` plus per-registration metadata `M`.
  - Its option (`kind.With(meta)`) opts a registration in.
  - Registration panics immediately if the registered type is a concrete type not assignable to `T`. A registration under an interface type is checked on its instance at Start, exactly like `AsStarter()`; a mismatch fails `Start`.
- Consumers register one handler per kind with `kind.Handle(root, stage, handler)`.
  - `stage` is `StageBeforeStart` (after discovery, before Starters start) or `StageAfterStart` (after all Starters are up).
  - The handler is `func(ctx context.Context, c Component[T, M]) error`, where `Component` carries the scope, the service name, the instance typed `T`, and the metadata `M`.
  - Registering a handler for a kind that already has one replaces it.
  - Opted-in services of a kind that has no handler are still instantiated at Start, but nothing else happens to them.
  - A zero `Kind` (not from `NewKind`), an invalid stage and a nil handler panic with explicit messages.
  - A nil root passed to `Handle` means `DefaultRootScope`, as a nil `Injector` does elsewhere in the library.
- Ship the built-in `Runnable` kind, `Kind[Runner, struct{}]`, where `Runner` is `Run(context.Context) error`. `AsRunnable()` is shorthand for `Runnable.With(struct{}{})`.
  - Its default `StageAfterStart` handler runs every opted-in runner once, in dependency then registration order, and stops at the first error.
  - `AsRunnable()` and the default handler use an unexported copy of the kind, so reassigning the exported `Runnable` variable cannot break them. Godoc says "do not reassign".
  - The library's own feature is built on the public extension mechanism.

**Explicit dependency edges**
- Declare a dependency between two registered services without one invoking the other. There are two forms, and both feed the same DAG:
  - **Static**: a `DependsOn` registration option. Covers hidden dependencies, e.g. a provider that keeps the injector and invokes a service later at runtime. A static edge orders construction, start and shutdown, exactly like an invoked dependency.
  - **Dynamic**: `DeclareDependency(i Injector, dependent, dependency string) error`, typically called by a kind handler during Start. Example: a scheduler handler records `Scheduler -> Job`, so shutdown stops the scheduler before the jobs it drives. A dynamic edge orders start (when declared before or during `StageBeforeStart`) and shutdown, **not** construction, because it builds nothing.
- `dependent` is resolved from `i`; `dependency` is resolved from the dependent's own scope or its ancestors, as `Invoke` would. Edges therefore always point to the same scope or an ancestor, which keeps them consistent with children-first shutdown.
- A static edge to an unregistered service makes building the dependent (and so `Start`) fail with a not-found error. A dynamic edge to a service that is not visible makes `DeclareDependency` return a not-found error.
- A declared edge that closes a cycle fails with the existing circular-dependency error, naming the chain: at build time (static), in `DeclareDependency` (dynamic, also following static `DependsOn` edges of services not yet built), or at Start (cycles that only appear once services are built).

**Discovery**
- Internal, deterministic enumeration of registered services by assignable type or by kind, without invoking unrelated services. Order is ancestor scopes first, then registration order. This requires recording a registration index for each service, unique per root container; a clone continues its source's counter.
- A public collection API is out of scope for this change.

## Capabilities

### New Capabilities

- `container-start`: starting a container — `Starter*` detection, ordering, scope traversal, errors, one-shot semantics, Start/Shutdown serialization (including run-once root Shutdown), per-service start hooks, and the shutdown-without-start contract.
- `registration-options`: options and metadata attached to a service registration, and their behavior under clone, override and alias, including registration order.
- `component-kinds`: typed component kinds, opt-in, handler registration and staging, and the built-in `Runnable` kind.
- `dependency-declaration`: explicitly declared dependency edges, static and dynamic, and their effect on construction, start and shutdown order. Also covers deterministic tie-breaking by registration order.

### Modified Capabilities

None (no existing specs).

## Impact

- Core package: `service.go` and all four service wrappers (options, registration index, declared dependencies), `root_scope.go` and a new `root_start.go` (lifecycle phases, Start, run-once Shutdown), `scope.go` (discovery snapshot), `dag.go` (reachability, cycle check), `di.go` (option parameters), `injector.go` (hooks, toggles), `di_lifecycle.go` (`Starter*`, `Runner`), new `options.go`, `dependency.go`, `kind.go`.
- Docs: new lifecycle pages, the upgrading note for the signature break, and `Example*` tests for each default and override.
- Phase 2, out of scope here: an instance-aware post-build hook, scope-level `Start`, handling of child scopes created after `Start`, parallel start, and a `Refresh` operation. Public `InvokeAll[T]` is in `add-collection-invocation`.
- Depends on `upgrade-go-1-27` (and transitively on `fix-stacktrace-frame-filter` and `rename-module-doger`). `add-container-lifecycle-hooks` and `add-collection-invocation` build on this change; the hooks change builds on the lifecycle model above exactly as named in design D6.

## Defaults & Control

| Behavior | Zero-config default | Override / opt-out |
|---|---|---|
| `Starter*` discovery | Auto-detected by **registered** type (unlike `Shutdowner*`, which is detected on the instance) | `InjectorOpts.DisableStarterAutoDetection` switches to explicit opt-in via `AsStarter()`; interface-typed registrations opt in with `AsStarter()` |
| `Runner` and custom kinds | Explicit opt-in only (`AsRunnable()`, `kind.With(...)`), because `Run` is too common a method name to auto-detect | `InjectorOpts.EnableRunnerAutoDetection` auto-detects `Runner` by registered type (custom kinds always require opt-in) |
| Kind type check | Registration panics for a concrete type not assignable to `T`; interface-typed registrations are checked on the instance at Start | Register under an interface type to defer the check |
| Construction and start order | Dependency DAG forward, parent scope before children, registration order for ties | Declare edges with the `DependsOn` option or from a kind handler; reorder registrations to change ties |
| `Runnable` handling | Built-in handler: run each one-shot runner once after Starters, fail-fast | Replace the `Runnable` handler with a consumer handler |
| Start failure | Return wrapped error, no rollback | Caller decides: `Shutdown()`, retry in a new container, or exit |
| Start context | Cancelled when Start returns, or when root Shutdown begins | Long-running work uses its own context (Starter plus Shutdowner) |
| Shutdown during Start | Cancels Start, waits for it, bounded by Shutdown's own context | Pass a deadline to `ShutdownWithContext` to bound the wait |
| Repeated or concurrent root Shutdown | Runs once; other callers get a copy of the first report | None needed: shutting the same services down twice is never correct |
| Nil Shutdown context | Treated as `context.Background()` | Pass a context with a deadline to bound the shutdown |
| Nil root in `Kind.Handle` | `DefaultRootScope`, like a nil `Injector` | Pass an explicit `*RootScope` |
| Declared edges on clone | `DependsOn` edges are recorded again when the clone builds the dependent; `DeclareDependency` edges are not copied | Re-declare them on the clone, e.g. from a kind handler, which runs again when the clone starts |
| Per-service start hooks | None registered | `HookBeforeStart` / `HookAfterStart` |
| Calling `Start` again / after shutdown | `ErrAlreadyStarted` / `ErrStartAfterShutdown` | None needed: a container is started once. Re-running belongs to the deferred `Refresh` operation. |
| Start order among independent services | Sequential, registration order | Declare edges; parallel start is a possible later option |
| Transient services | Never started or handled; opting one in panics at registration | Register as lazy or eager to make it startable |

## Defect (bug fix included in this change)

- **Observed:** with 20 goroutines calling `root.Shutdown()` concurrently on a container with built services, every run yields spurious `ErrServiceNotFound` errors in the reports (several callers list the same services and race to remove them in `Scope.serviceShutdown`, scope.go:772-784). Re-measured on 2026-10-25: 3–17 per run, in 6 of 6 runs, with default options and with `HealthCheckParallelism: 2`; each service's `Shutdown` was still called once. With `HealthCheckParallelism: 2`, `go test -race` also reports a data race on `RootScope.healthCheckPool`, which every caller reads and sets to nil in `RootScope.ShutdownWithContext` (root_scope.go:177-180). The race report is intermittent (1 of 3 runs). A second, later `Shutdown()` returns an empty report with `Succeed: true`.
- **Expected:** services are shut down once, every caller gets the same successful report, and there is no race.
- **Minimal reproduction:** `root := NewWithOpts(&InjectorOpts{HealthCheckParallelism: 2})`; register and invoke lazy "a", "b", "c" with `Shutdown() error`; start 20 goroutines calling `root.Shutdown()`; assert every report has no errors. It fails on the current code (reproduced by the `add-container-lifecycle-hooks` audit), and becomes the regression test `TestRootScope_ConcurrentShutdown` (tasks group 1).
