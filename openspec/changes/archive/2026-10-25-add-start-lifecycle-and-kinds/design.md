# Design

## Context

See proposal.md for the motivation and the pinned API contract, and `specs/` for the behavior. This change is implemented after `rename-module-doger` and `upgrade-go-1-27`, so code samples use package `doger` (module `github.com/kartaladev/doger`) and Go 1.27. The relevant current state of the code:

- **Registration** goes through `provide`/`override` in `di.go`. Both build a wrapper (`serviceLazy`, `serviceEager`, `serviceTransient`, `serviceAlias`) and call `Scope.serviceSet`, which fires the registration hooks. `AsNamed` also registers aliases through `provide`. `override` does not check for an existing name.
- **Services are stored** in `Scope.services map[string]any`. There is no registration order: map iteration is random, which is why `invokeByGenericType` carries a nondeterminism TODO.
- **Dependency edges.** The `DAG` (`dag.go`) holds `dependencies`/`dependents` keyed by `ServiceDescription{ScopeID, ScopeName, Service}`. Edges are recorded only by `virtualScope.addDependency` while a provider invokes another service, and always point to the invoker's scope or an ancestor. Cycles are detected by `virtualScope.detectCircularDependency` on the invoker chain only; the DAG itself is never checked for cycles.
- **Shutdown** (`Scope.ShutdownWithContext`) shuts children first, in parallel. It then repeatedly shuts down the scope's services that have no dependents, in parallel. `serviceShutdown` removes the service from the map under the scope lock and then removes its DAG node (lock order: scope `mu`, then DAG `mu`). Every wrapper's `shutdown` returns `ctx.Err()` without calling the instance when the context is already done; lazy wrappers skip unbuilt instances, eager wrappers always call it, aliases delegate to their target.
- **Root Shutdown is not run-once** (the Defect in proposal.md). `RootScope.ShutdownWithContext` (root_scope.go:176-185) lets every caller walk and remove services and set `healthCheckPool = nil` in a defer, so concurrent callers race.
- **Lifecycle interfaces** live in `di_lifecycle.go`: four `Shutdowner*` and two `Healthchecker*` variants, detected with type switches on the **instance**.
- **Hooks** are `InjectorOpts` slices plus `RootScope.Add*Hook` methods, copied in `InjectorOpts.copy()`. Invocation hooks run outside provider panic recovery, so a panicking hook propagates.
- **Clone** (`RootScope.CloneWithOpts`) goes through `NewWithOpts` and clones wrappers through `clone(Injector)`. Lazy clones reset the built instance; eager clones share the same instance.
- **`Injector` implementations** are `*RootScope`, `*Scope` and `*virtualScope` (gopls). A provider's injector, and every root service's scope, is the root's `*Scope` (`root.self`), not the `*RootScope`.

## Goals / Non-Goals

**Goals:**
- A container-level `Start` that builds candidates, runs kind handlers, and starts Starters deterministically in DAG order.
- One precise Start/Shutdown lifecycle model (D6) that `add-container-lifecycle-hooks` extends without changing it.
- Run-once root Shutdown, fixing the concurrent-Shutdown defect.
- Registration metadata that travels with the wrapper, so override replaces it and clone copies it with no extra bookkeeping.
- A kind/handler extension point that the built-in `Runnable` uses unchanged.
- Zero impact on the invoke hot path for registrations without options.

**Non-Goals:**
- Container-level hooks (`add-container-lifecycle-hooks`).
- Public collection invocation (`add-collection-invocation`).
- Parallel start, scope-level `Start`, post-Start child scopes, an instance-aware post-build hook, and `Refresh`. All are phase 2.
- Changing shutdown's per-scope algorithm. Shutdown just sees more edges.

## Decisions

**D1. Metadata lives on the wrapper.**
- Each wrapper type gets an `opts *serviceOptions` field (nil when registered without options) and a `regIndex uint64` field. The unexported `serviceWrapper[T]`/`serviceWrapperAny` interfaces gain `getOptions() *serviceOptions` and `getRegistrationIndex() uint64`.
- Aliases always return nil options. `Override*` builds a new wrapper, which naturally replaces the options, and copies the old wrapper's `regIndex` when the name already exists **in the same scope** (`serviceGet`, not `serviceGetRec`). An override in a child scope of a name inherited from an ancestor is a new registration with a new index.
- `clone()` copies `opts` and `regIndex`; per-instance state (the eager dependency flag of D4) is reset.
- *Alternative:* side maps on `Scope` (`options map[string]*serviceOptions`). Rejected: these have to be kept in sync with `services` on override, shutdown removal and clone, which is three more places to get wrong.

**D2. `ProvideOption` is `func(*serviceOptions)`, validated in `provide`/`override` before the service is stored.**
- An option only records intent: `starter`, `dependsOn []string`, `kinds []kindBinding{id *kindID, meta any, target reflect.Type, deferred bool}`.
- After building the wrapper and **before** `serviceSet` (so no registration hook fires and nothing is stored), `provide`/`override` validate the options against `getReflectType()` and `getServiceType()` and panic with the spec messages (as `error` values, so `assert.PanicsWithError` works):
  - transient plus starter or kind;
  - a duplicate kind;
  - starter on a concrete type with no `Start*`;
  - kind on a concrete type not assignable to `T`.
  For an **interface** registered type that is not assignable, starter and kind bindings are marked deferred and checked on the instance at Start (D7 step 3).
- Type names in messages use `reflect.Type.String()` (e.g. `doger.Runner`).
- Nil options are skipped. `ProvideOption`'s parameter type is unexported, so consumers cannot write their own; constructors come only from this library. Later changes add constructors and fields (`add-collection-invocation` adds `WithOrder` and an `order *int` field).
- *Alternative:* an interface with an unexported method. This is equivalent; the function is simpler.

**D3. Registration index comes from an atomic counter on `RootScope`.**
- `nextRegIndex atomic.Uint64` lives on `RootScope` and is assigned in `provide` (and in `override` for a new name). Child scopes share it through `rootScope`, so indexes are unique across the root container, not per scope.
- `CloneWithOpts` sets `clone.nextRegIndex` to the source's current value **after** copying wrappers, so later registrations in the clone come after every copied one (finding: a reset counter would collide with copied indexes). After, not before: every copied wrapper got its index before it was stored, so its index is below a counter read after the copy. A counter read before the copy could miss a registration racing the clone, whose index the clone would then hand out again. Pinned by `TestRegistrationIndex_CloneDuringRegistration`.
- Orderings in this change use `(scope depth, regIndex)`, which is a total order inside one container. Consumers that combine other keys (e.g. `add-collection-invocation`'s order values) fall back to the service name as the final tie-break on their side.

**D4. Declared dependencies are resolved inside the build path.**
- Before calling the provider, `serviceLazy.build` and `serviceTransient.getInstance` invoke each `dependsOn` name through `invokeAnyByName` on the virtual scope they already receive. This reuses circular-dependency detection, DAG edge recording and invocation hooks unchanged.
- `serviceEager.getInstance` resolves its declared dependencies before returning, guarded by `depsMu sync.Mutex` and `depsResolved bool`. The flag is set only on success, so a failed resolution (e.g. dependency registered later) is retried on the next invocation, like an unbuilt lazy service. The guard is consulted only when `opts != nil && len(opts.dependsOn) > 0`, so eager registrations without options keep their current code path.
- `DependsOn[T]()` is `DependsOnNamed(NameOf[T]())`.
- *Alternative:* record declared edges at registration. Rejected: the target may not be registered yet, and registration cannot return an error.
- *Alternative:* `sync.Once` with a stored error for eager. Rejected: a transient registration-order mistake would fail the eager service forever.

**D5. `DeclareDependency` resolves visibility like `Invoke` and checks reachability over recorded and static edges.**
- `dependent` is resolved from `i` with `serviceGetRec`; `dependency` is resolved with `serviceGetRec` **from the dependent's own scope**. Either missing gives a wrapped `ErrServiceNotFound` naming it. Edges therefore point to the same scope or an ancestor, matching invoked edges and children-first shutdown.
- `RootScope.declareMu sync.Mutex` serializes `DeclareDependency` calls (check plus add).
- Under it, `reachable(from, to)` runs a DFS whose neighbours of a node are its DAG `dependencies` (read under a short DAG `RLock` per node) **plus** the static `dependsOn` of the node's registered wrapper, resolved from the node's scope. A path from `dependency` to `dependent` gives a wrapped `ErrCircularDependency` naming the path. Self-edges are cycles. Otherwise `dag.addDependency`, which is idempotent.
- Lock order is scope `mu` → DAG `mu`, as in `serviceShutdown`. The DFS never holds the DAG lock while taking a scope lock.
- A dynamic edge builds nothing, so it orders start (if declared before or during `StageBeforeStart`) and shutdown, not construction.

**D6. Lifecycle model on `RootScope` (owned here; `add-container-lifecycle-hooks` builds on these exact names).**

State, all guarded by `lifecycleMu sync.Mutex`:
- `startPhase`: `startIdle → startStarting → startStarted | startFailed`. The hooks change may add `startReady` after `startStarted`; no code here may assume `startStarted` is terminal.
- `closePhase`: `closeOpen → closeClosing → closeClosed`.
- `startCancel context.CancelCauseFunc`, `startDone chan struct{}` (created by `beginStart`), `closeDone chan struct{}` and `closeReport *ShutdownReport` (created by `beginClose`).

Rule: phases change only by compare-and-set inside `lifecycleMu`, and the lock is **never** held while Starters, handlers, providers, hooks or service shutdowns run.

`StartWithContext(ctx)`:
1. A nil `ctx` becomes `context.Background()`.
2. `beginStart` (locked): if `closePhase != closeOpen` → return `fmt.Errorf("DI: cannot start: %w", ErrStartAfterShutdown)`. Else if `startPhase != startIdle` → return `ErrAlreadyStarted` (a failed Start stays failed, so a retry also gets it). Else set `startStarting`, create `startCtx, startCancel := context.WithCancelCause(ctx)` and `startDone`.
3. Steps 3–4 run inside a helper `runStart(startCtx) (err error)`, so its deferred call completes before step 5. `runStart` starts with `defer finishStart(&err)` (locked): if `err == nil` but `closePhase != closeOpen` (Shutdown began after the final abort check), set `err = fmt.Errorf("DI: start aborted: %w", context.Cause(startCtx))`; then set `startStarted` if `err == nil`, else `startFailed` (also on panic, detected with `recover` + re-panic); call `startCancel(nil)` (the Start-time context ends with Start); close `startDone`. Being deferred, it runs on panics too.
4. `runStart` calls `runStartPipeline(startCtx)` (D7) and returns its error.
5. `pointAfterStart`: `StartWithContext` reaches it only after `runStart` has returned, so `finishStart` has run and `startDone` is closed. Code here (the hooks change's Started/Ready/Failed hooks) may call `Shutdown()` without deadlocking. In this change it is empty. A `defer` in `StartWithContext` itself would run after this point, which is why `finishStart` lives in `runStart`.

Abort rule inside the pipeline: before each candidate build, each handler call, each Starter, and once after the last step, `if startCtx.Err() != nil` → stop with `fmt.Errorf("DI: start aborted: %w", context.Cause(startCtx))`. When a step fails while `startCtx` is done, the step error additionally wraps the cause (`"%w (start aborted: %w)"`). So a caller cancellation matches `ctx.Err()`, and a Shutdown-initiated abort matches `ErrStartAfterShutdown`.

`RootScope.ShutdownWithContext(ctx)`:
0. A nil `ctx` becomes `context.Background()`, as in `StartWithContext`.
1. `beginClose` (locked): if `closePhase == closeOpen` → set `closeClosing`, create `closeDone`, and capture `inFlight := startPhase == startStarting`, `startCancel` and `startDone`. This caller is the **winner**. Otherwise capture `closeDone` and become a **waiter**.
2. Waiter: `awaitDone(closeDone, ctx)`. If it reports done, return `cloneShutdownReport(closeReport)` (a deep copy of slices and maps). Otherwise return `&ShutdownReport{Succeed: false, Errors: {rootDesc: fmt.Errorf("DI: waiting for root shutdown: %w", ctx.Err())}}`, with `rootDesc = ServiceDescription{ScopeID: root.ID(), ScopeName: root.Name(), Service: ""}`.
   - `awaitDone(done <-chan struct{}, ctx) bool` prefers completion. It returns true at once if `done` is already closed; otherwise it selects on `done` and `ctx.Done()`, and when `ctx.Done()` wins it checks `done` once more, non-blocking, before returning false. A plain `select` picks at random when both are ready, so a caller arriving after completion with an ended context would get a deadline report half the time (spec "Later Shutdown with an ended context still returns the first report").
3. Winner: steps 4–6 run inside a helper `shutdownAsWinner(ctx) (report *ShutdownReport)`, which starts with `defer finishClose(report)`. `finishClose` (locked) sets `closeClosed`, stores `closeReport = cloneShutdownReport(report)`, so no caller's copy, the winner's included, can change what later callers get, and closes `closeDone`. If `report` is nil because the winner panicked on its own goroutine (for example in a consumer `Logf`), it stores `&ShutdownReport{Succeed: false, Errors: {rootDesc: errors.New("DI: root shutdown did not complete")}}` instead, so waiters never receive nil. Being deferred, it releases waiters when the winner's goroutine panics. Panics in per-service shutdown hooks are not among these: `Scope` runs each service shutdown on its own goroutine (`wg.Go`), so such a panic terminates the process, as it does today.
4. If `inFlight`: `startCancel(ErrStartAfterShutdown)`, then `awaitDone(startDone, ctx)`. If the deadline wins, the winner records `rootDesc: fmt.Errorf("DI: waiting for start: %w", ctx.Err())` in its report (so the report fails even when no service had anything to shut down) and shutdown proceeds while Start is still running (spec: last row of "Shutdown cancels a running Start"). Start then stops at its next abort check; a Starter that finishes late is not rolled back.
5. `pointClosing` (empty here; the hooks change fires Closing hooks here, after the wait).
6. `report := s.self.ShutdownWithContext(ctx)`, then stop and nil the health-check pool. Only the winner touches the pool, which removes the data race.
7. `shutdownAsWinner` returns, so `finishClose` has run; `ShutdownWithContext` then reaches `pointClosed` (empty here). Being after `closeDone`, code there may call `Shutdown()` and gets the copied report.

Other rules:
- Re-entrancy: `Start` called from Start-time code returns `ErrAlreadyStarted` immediately. `Shutdown()` called **synchronously from inside the pipeline** (a Starter, handler, runner, provider or per-service hook on the Start goroutine) waits for `startDone`, which that same goroutine must close: a deadlock unless its context has a deadline. Godoc says: return an error instead; only `pointAfterStart` code may call `Shutdown()`.
- `Scope.ShutdownWithContext` on a child scope, and any shutdown reached through `root.self` (a provider's injector, `Component.Scope` for root services, `virtualScope.Shutdown`), bypasses this model. Documented on `Shutdown`, `Component` and the docs page.
- *Alternative:* a `sync.Once` for Start. Rejected: it cannot express "Shutdown cancels and waits for Start" or "Start after Shutdown".
- *Alternative:* hold `lifecycleMu` for the whole pipeline. Rejected: Start-time code calling `Start` would deadlock, and a stuck Starter would block `Shutdown` with no escape.
- *Alternative:* a single combined state enum. Rejected: start and close are independent (a container can be closed without ever starting, or close while starting), and two phases keep each compare-and-set trivial.

**D7. The pipeline (`root_start.go`, `runStartPipeline`).**
1. `pointStarting` (empty here; the hooks change fires Starting hooks here). Abort check.
2. **Discover.** Snapshot the scope tree breadth-first, root first, recording depth. For each non-transient, non-alias wrapper, mark it a candidate if any of these hold:
   - auto Starter: the registered type is assignable to one of the four `Starter*` interfaces (`typeCanCastToType`) and `!DisableStarterAutoDetection`;
   - `opts.starter`;
   - it has any kind binding;
   - `EnableRunnerAutoDetection` is set, the type is assignable to `Runner`, and it has no explicit `Runnable` binding (implicit binding, so a service never runs twice).

   Sort candidates by `(depth, regIndex)`.
3. **Build.** For each candidate: abort check, then `invokeAnyByName(scope, name)`, keeping the instance. Then check deferred kind bindings on the instance (`reflect.TypeOf(instance)` must implement `T`; a nil instance fails), with `DI: service \`%s\` does not implement \`%s\` required by kind \`%s\``. Errors are wrapped as `DI: failed to build service \`%s\` in scope \`%s\`: %w`.
4. **`StageBeforeStart` handlers** (D9).
5. **Cycle check.** DFS over the candidates with the D5 neighbour function. A cycle fails Start with a wrapped `ErrCircularDependency` naming the chain. This catches cycles that only exist once providers have run (e.g. a dynamic edge declared before Start, closed by an invoked edge).
6. **Order Starters** (D8), then for each one:
   - abort check;
   - a deferred `AsStarter` instance with no variant yields the spec error **before** any hook runs (no `Start` method is called, so no start hook fires);
   - run `HookBeforeStart`;
   - `startInstance(startCtx, instance)`: a type switch over the four variants, with `recover`;
   - run `HookAfterStart(err)` with the method's own (unwrapped) error.

   The first error stops the loop.
7. `pointStartersUp` (empty here). **`StageAfterStart` handlers.**
8. Final abort check.

**D8. Start order is a Kahn topological sort over transitive Starter dependencies.**
- For each Starter, compute which other Starters it reaches through `DAG.dependencies`, using the DFS shared with D5.
- Repeatedly emit the ready Starter with the smallest `(depth, regIndex)`.
- Step 5 guarantees no cycles; the defensive fallback (emit the rest in `(depth, regIndex)` order) remains for robustness only.
- Computed after `StageBeforeStart`, so handler-declared edges count.
- *Alternative:* start in build-completion order (`orderedInvocation`). Rejected: it ignores declared edges added by handlers and is per-scope only.

**D9. Kinds.**
- `Kind[T, M]` is `struct{ id *kindID }`, where `kindID{name string}`. `NewKind` allocates a new `kindID`, so equal names stay distinct. A zero `Kind` has a nil `id`: `Name()` returns `""`, and `With`/`Handle` panic with `DI: kind not created with NewKind`.
- `With(meta)` adds a `kindBinding` with `target = reflect.TypeFor[T]()`; D2 decides immediate or deferred checking.
- `Handle(root *RootScope, stage Stage, h func(context.Context, Component[T, M]) error)` replaces a nil `root` with `DefaultRootScope`, the same rule `getInjectorOrDefault` applies to a nil `Injector`. It panics on a nil handler (`DI: nil handler for kind \`%s\``) or an unknown stage (`DI: invalid stage %d for kind \`%s\``). It stores, under `RootScope.kindsMu`, an erased entry `{id, stage, run func(ctx, *Scope, string, any, any) error}`. The `run` closure uses checked assertions (`instance.(T)` with `, ok`, which also satisfies `forcetypeassert`) inside the recover scope.
- Entries are a slice. Replacement keeps the original slot, which gives "order of first `Handle`". Each stage iterates a snapshot of the entries taken when the stage begins.
- Per stage, iterate entries in order. Each gets its candidates in D8 order generalized to all candidates (dependency, depth, regIndex). Every call has an abort check before it, is wrapped with `recover`, and errors are formatted as `DI: kind \`%s\` failed for service \`%s\` in scope \`%s\`: %w`.
- The type `Stage int` has `StageBeforeStart` and `StageAfterStart`.
- `Component[T, M]` is the exported struct `{Scope *Scope; Name string; Instance T; Meta M}`. `Scope` is the registering scope; for root services it is the root's `*Scope` (see D6 bypass note).
- *Alternative:* a `Handler[T, M]` interface. Rejected: a func type is simpler, and a consumer that wants an interface can pass a method value.

**D10. `Runnable` is an ordinary kind, protected from reassignment.**
- `var runnableKind = NewKind[Runner, struct{}]("runnable")` (unexported), `var Runnable = runnableKind` (exported, same `*kindID`), and `func AsRunnable() ProvideOption { return runnableKind.With(struct{}{}) }`.
- `NewWithOpts` registers the default handler on `runnableKind` (`StageAfterStart`, calls `Run(startCtx)` inline, fail-fast) on each new root, so replacing it affects only that container. `Runnable.Handle` replaces it as long as `Runnable` is not reassigned; reassigning `Runnable` changes neither `AsRunnable()` nor the default handler. Godoc: "do not reassign".
- `Runner` godoc: one-shot; `Run` must return; long-running work belongs in a Starter plus a Shutdowner; honour `ctx`, which Shutdown cancels.
- `CloneWithOpts` goes through `NewWithOpts` and gets the default handler. Consumer-replaced handlers are not copied to clones (see Open Questions).

**D11. Errors.**
- New sentinels in `errors.go`: `ErrAlreadyStarted` ("DI: container already started") and `ErrStartAfterShutdown` ("DI: container is shut down"). The latter is also the cancel cause Shutdown gives the Start context.
- Failures are wrapped with `fmt.Errorf("...: %w", cause)`, so `errors.Is`/`errors.As` reach the cause.
- Recovered panics are formatted with `%v`, plus `%w` when the panic value is an error.
- No exported `StartError` type in this change. It can be added later without breaking callers.

**D12. Go 1.27 features.**
- `reflect.TypeFor` is used for target types; `context.WithCancelCause`/`context.Cause` for Start cancellation.
- `testing/synctest` is used for deadline, cancellation and wait tests.
- Generic methods are not needed here: `Kind.Handle`/`With` only use the receiver's type parameters. The package-level API stays function-based because `Injector` is an interface.

## Risks / Trade-offs

- **[Deadlock]** Start-time code (Starter, handler, runner, provider, per-service hook) that calls `root.Shutdown()` without a deadline on the Start goroutine waits for `startDone` forever. → Godoc on `Start`, `Shutdown`, `Runner` and `Kind.Handle`: return an error instead. `pointAfterStart` and `pointClosed` are placed after `startDone`/`closeDone` so the hooks change's container hooks can call `Shutdown()` safely.
- **[Late Starter after a Shutdown deadline]** A Starter that ignores its context and finishes after Shutdown stopped waiting leaves a running component that nothing shuts down. → Specified and tested (last row of "Shutdown cancels a running Start"); godoc: honour `ctx`.
- **[Behavior change: repeated root Shutdown]** A second root `Shutdown` returns a copy of the first report and does not shut down services registered after the first `Shutdown`. → Flagged in the proposal and the upgrading note; child `Scope.Shutdown` is unchanged; pinned by "Later root Shutdown returns the first report". Three existing tests call root `Shutdown` concurrently on one container: `TestConcurrentShutdown`, `TestConcurrentBlockingOperations` (`tests/concurrent_operations_test.go`) and `TestRaceConditionShutdownInvocation` (`tests/race_conditions_test.go`). Their assertions (each service shut down once; fewer than two services per report; the service is not invokable afterwards) hold under run-once, and they stay unchanged as compatibility evidence.
- **[Random pick between completion and a caller's ended context]** A plain `select` on `closeDone`/`startDone` and `ctx.Done()` picks at random when both are ready. → `awaitDone` prefers completion (D6 step 2); pinned by "Later Shutdown with an ended context still returns the first report" (100 calls per row) and the `TestAwaitDone` mechanism test.
- **[Nil Shutdown context]** The waiter and in-flight paths select on `ctx.Done()`, which panics on a nil context. → Normalized to `context.Background()` (D6 step 0); pinned by "Nil Shutdown context". This also fixes the existing panic when a built service has a `Shutdowner*` method.
- **[`*RootScope` now has `Start() error`]** A `*RootScope` registered as a service of another container is auto-started by that container's `Start`. As today, it is never shut down automatically, because its `Shutdown` returns `*ShutdownReport` and matches no `Shutdowner*` variant. → Documented on the starter page; opt out with `DisableStarterAutoDetection` or register it under an interface type without `Start`.
- **[Clones do not copy declared edges]** A clone gets a fresh DAG: `DependsOn` edges come back when the clone builds the dependent, `DeclareDependency` edges do not. → Specified ("Clones do not copy declared edges") and documented; kind handlers re-declare their edges when the clone is started.
- **[Known flaky test during the gates]** `TestRootScope_queueServiceHealthcheck` fails about 4 times in 1,000 runs on `main`; the fix belongs to `add-clock-fix-healthcheck-timeouts`. → Policy for this change's `make test` gates: if only that test fails, with its known symptom ("An error is expected but got nil" at `root_scope_test.go:119` or `:131`), rerun once and record it in the task evidence. Any other failure blocks.
- **[Invoke hot path]** D4 adds an `opts != nil` check per lazy build and per eager invoke, not per cached lazy invoke. → `bench/do_bench_test.go` runs before and after, compared with `benchstat`; the target is `~`.
- **[Eager invoke can now fail]** This happens only when it has `DependsOn` with a missing or cyclic target. Eager registrations without options keep returning a nil error. → Pinned by `TestEager_NoOptionsNeverErrors`.
- **[Interface-registered Starters are invisible to auto-detection]** This is by design, because detection uses the registered type. → `AsStarter()` is documented in the godoc and the docs page; the Detection "Invoke before Start" row pins the asymmetry with instance-based Shutdowner detection.
- **[Surprising auto-detection]** Some standard-library types have `Start() error` (e.g. `*exec.Cmd`, `*httptest.Server`'s `Start()`) and would be auto-started when registered directly. This repository's own examples also register types with `Start()` that they call by hand (`Car` in `examples/dag`, `examples/shutdownable` and `examples/http/chi`, `Application` in `examples/eager-loading`). They are unaffected because they never call `root.Start()`, but a consumer adopting `Start` with such code would start them twice. → Documented, with the `DisableStarterAutoDetection` opt-out.
- **[Clones share eager instances]** Starting a clone starts an eager Starter's instance a second time. → Pinned by "Clone of a started container" and documented.
- **[Registrations during Start]** A provider or Starter that registers services while Start runs gets no guarantee about whether they are discovered. → Documented as unspecified; phase 2.
- **[Wider unexported wrapper interface]** It touches all four wrappers. → Compile-time assertions (`var _ serviceWrapper[int] = ...`) already exist and catch omissions.
- **[`DisableStarterAutoDetection` / `EnableRunnerAutoDetection` are fixed at container creation]** They are read from `opts` at Start, and `opts` is copied on clone. → Pinned by tests.

## Public API & Defaults

New identifiers (package `doger`):

```go
// di_lifecycle.go
type Starter interface{ Start() }
type StarterWithError interface{ Start() error }
type StarterWithContext interface{ Start(context.Context) }
type StarterWithContextAndError interface{ Start(context.Context) error }
type Runner interface{ Run(context.Context) error } // one-shot: Run must return

// root_start.go / root_scope.go
func (s *RootScope) Start() error
func (s *RootScope) StartWithContext(ctx context.Context) error
func (s *RootScope) AddBeforeStartHook(hook func(*Scope, string))
func (s *RootScope) AddAfterStartHook(hook func(*Scope, string, error))

// injector.go (InjectorOpts fields)
HookBeforeStart             []func(scope *Scope, serviceName string)
HookAfterStart              []func(scope *Scope, serviceName string, err error)
DisableStarterAutoDetection bool
EnableRunnerAutoDetection   bool

// options.go
type ProvideOption func(*serviceOptions)
func AsStarter() ProvideOption
func DependsOn[T any]() ProvideOption
func DependsOnNamed(name string) ProvideOption
func AsRunnable() ProvideOption

// dependency.go
func DeclareDependency(i Injector, dependent, dependency string) error

// kind.go
type Kind[T, M any] struct{ /* unexported */ }
type Component[T, M any] struct { Scope *Scope; Name string; Instance T; Meta M }
type Stage int
const ( StageBeforeStart Stage = iota; StageAfterStart )
func NewKind[T, M any](name string) Kind[T, M]
func (k Kind[T, M]) Name() string
func (k Kind[T, M]) With(meta M) ProvideOption
func (k Kind[T, M]) Handle(root *RootScope, stage Stage, h func(context.Context, Component[T, M]) error)
var Runnable = runnableKind // do not reassign; AsRunnable and the default handler use runnableKind

// errors.go
var ErrAlreadyStarted, ErrStartAfterShutdown error
```

**Changed signatures — BREAKING for function-value use only:**
- `Provide`, `ProvideNamed`, `ProvideValue`, `ProvideNamedValue`, `ProvideTransient`, `ProvideNamedTransient`
- `Override`, `OverrideNamed`, `OverrideValue`, `OverrideNamedValue`, `OverrideTransient`, `OverrideNamedTransient`
- `Lazy`, `LazyNamed`, `Eager`, `EagerNamed`, `Transient`, `TransientNamed`

All gain a trailing `opts ...ProvideOption`. Call sites compile unchanged. Storing one of these generic functions as a value of its old function type, or passing it where that type is expected, no longer compiles; migration: wrap it in a closure. `Injector` is unchanged (and, having unexported methods, can only be implemented by embedding, which would keep satisfying it anyway).

**Changed behavior — root Shutdown (bug fix):** runs once; concurrent and later callers receive a copy of the first report (see Risks).

**Defaults:** see the proposal's Defaults & Control table. Additional defaults fixed here:
- nil option ignored
- duplicate `DependsOn` collapsed
- duplicate kind panics
- `Override` in the same scope keeps the registration index; clones continue the counter
- nil Start context becomes `Background`; the Start-time context is cancelled when Start returns
- a failed Start still counts as started
- Shutdown cancels a running Start and waits for it, bounded by its own context
- clones get the default `Runnable` handler, not consumer replacements
- a nil Shutdown context becomes `Background`; a caller arriving after completion always gets the first report; waiters never get a nil report
- a nil root in `Kind.Handle` means `DefaultRootScope`
- clones re-record `DependsOn` edges on build and do not copy `DeclareDependency` edges

**Example** (docs and `ExampleRootScope_Start`):

```go
root := doger.New()
doger.Provide(root, NewDB)                                  // Start(ctx) error -> auto-started
doger.Provide(root, NewHTTPServer)                          // invokes *DB -> started after DB
doger.Provide(root, NewMigrator, doger.AsRunnable())        // runs once after Starters
if err := root.StartWithContext(ctx); err != nil {
    _ = root.Shutdown()                                     // caller owns cleanup
    return err
}
```

## Test Strategy

Test files:
- `root_scope_test.go`: run-once root Shutdown (bug fix)
- `options_test.go`: registration-options, registration index
- `dependency_test.go`: dependency-declaration
- `root_start_test.go`: container-start
- `kind_test.go`: component-kinds and Runnable
- Example tests: `options_example_test.go`, `dependency_example_test.go`, `root_start_example_test.go`, `kind_example_test.go`

All tests run under `go test -race`. goleak runs through `TestMain` (`main_test.go`). Tests that could hang use `testWithTimeout`. Deadline, cancellation and wait scenarios run inside `synctest.Test` with channel handshakes and `synctest.Wait()`; they do not use `testWithTimeout` (its timer would be virtual inside the bubble). Tests that replace `DefaultRootScope` or reassign `Runnable` are not parallel and restore the value in `t.Cleanup`.

| Spec | Scenario | Test | Kind |
|------|----------|------|------|
| container-start | Each Starter variant is started once (4 rows) | `root_start_test.go: TestStart_Variants/<method>` | table |
| container-start | Which registrations are started (8 rows) | `TestStart_Detection/<row>` | table |
| container-start | Auto-detection toggle (2 rows) | `TestStart_DisableAutoDetection/<row>` | table |
| container-start | Dependency-ordered build and start | `TestStart_DependencyOrder` | unit |
| container-start | Child scopes are started after their ancestors | `TestStart_ScopeTraversal` | unit |
| container-start | Start on an empty container | `TestStart_Empty` | unit |
| container-start | A Starter failure stops the remaining Starters (3 rows) | `TestStart_StarterFailure/<row>` | table |
| container-start | A build failure stops Start before any Starter (2 rows) | `TestStart_BuildFailure/<row>` | table |
| container-start | AsStarter on an interface whose instance cannot start | `TestStart_AsStarterInstanceWithoutStart` | unit |
| container-start | Shutdown after a failed Start reaches every built service | `TestStart_ShutdownAfterFailure` | unit |
| container-start | A panic escaping Start still releases Shutdown | `TestStart_PanicReleasesShutdown` (`testWithTimeout`) | unit |
| container-start | Cancellation and deadline (3 rows) | `TestStart_Context/<row>` | table / synctest |
| container-start | Nil context | `TestStart_NilContext` | unit |
| container-start | Start-time context ends when Start returns | `TestStart_ContextEndsWithStart` | unit |
| container-start | Repeated and late Start calls (5 rows) | `TestStart_OneShot/<row>` | table |
| container-start | Concurrent Start calls | `TestStart_Concurrent` | race |
| container-start | Concurrent Start and Invoke | `TestStart_ConcurrentInvoke` | race |
| container-start | Clone of a started container (2 rows) | `TestStart_Clone/<registration>` | table |
| container-start | Shutdown cancels a running Start (4 rows) | `TestStart_ShutdownCancels/<row>` | table / synctest / race |
| container-start | Concurrent root Shutdown runs once (2 rows) | `root_scope_test.go: TestRootScope_ConcurrentShutdown/<opts>` (20 goroutines, `testWithTimeout`) | table / race (regression) |
| container-start | Later root Shutdown returns the first report | `TestRootScope_RepeatedShutdown` | unit (regression) |
| container-start | Waiting Shutdown caller honours its context | `TestRootScope_ShutdownWaiterDeadline` | synctest |
| container-start | The stored report is isolated from the first caller | `TestRootScope_ShutdownReportIsolated` | unit (regression) |
| container-start | Later Shutdown with an ended context still returns the first report (2 rows) | `TestRootScope_ShutdownWithEndedContext/<ctx>` (100 calls per row) | table (regression) |
| container-start | Nil Shutdown context (2 rows) | `TestRootScope_ShutdownNilContext/<previous>` | table |
| container-start | A panicking first caller still releases the others (2 rows) | `TestRootScope_ShutdownWinnerPanic/<call>` | table |
| container-start | Late registration | `TestStart_LateRegistrationNotStarted` | unit |
| container-start | Hooks around each Starter | `TestStart_Hooks` | unit |
| container-start | No hooks for an AsStarter instance without a Start method | `TestStart_HooksSkipNonStarter` | unit |
| registration-options | Registration with and without options (6 rows) | `options_test.go: TestProvideOptions_Registration/<row>` | table |
| registration-options | Registration functions keep their parameters and add a variadic option list (18 rows) | `TestProvideOptions_SignatureHasVariadic/<function>` (reflect: `IsVariadic`, last param `[]ProvideOption`, other params unchanged) | table |
| registration-options | Each constructor yields an option recognized by registration (5 rows) | `TestProvideOptions_Constructors/<option>` | table |
| registration-options | Repeated options (5 rows) | `TestProvideOptions_Repeated/<row>` (panic rows use `assert.PanicsWithError`) | table |
| registration-options | Invalid opt-in combinations (10 rows) | `TestProvideOptions_Invalid/<row>` (asserts the panic and that the name is not registered) | table |
| registration-options | Clone, override and alias (5 rows) | `TestProvideOptions_CloneOverrideAlias/<row>` | table |
| registration-options | Registration order survives override and clone (4 rows) | `root_start_test.go: TestStart_RegistrationOrder/<row>` | table |
| component-kinds | Opting a service into a custom kind | `kind_test.go: TestKind_OptIn` | unit |
| component-kinds | Same name, distinct kinds | `TestKind_DistinctIdentity` | unit |
| component-kinds | Kind opt-in on an interface-typed registration (2 rows) | `TestKind_InterfaceRegistration/<instance>` | table |
| component-kinds | Invalid kind values panic (4 rows) | `TestKind_InvalidValues/<call>` | table |
| component-kinds | Handler stage relative to Starters (2 rows) | `TestKind_Stage/<stage>` | table |
| component-kinds | Replacing a handler | `TestKind_ReplaceHandler` | unit |
| component-kinds | Nil root uses the default root scope | `TestKind_NilRootUsesDefault` (not parallel; restores `DefaultRootScope` in `t.Cleanup`) | unit |
| component-kinds | Kind without a handler | `TestKind_NoHandler` | unit |
| component-kinds | Components are passed in deterministic order | `TestKind_ComponentOrder` | unit |
| component-kinds | Handler failure stops Start (3 rows) | `TestKind_HandlerFailure/<row>` | table |
| component-kinds | Scheduler handler orders start and shutdown | `TestKind_DeclareDependencyInHandler` | unit |
| component-kinds | Runners run after Starters | `TestRunnable_RunsAfterStarters` | unit |
| component-kinds | Runner failure | `TestRunnable_Failure` | unit |
| component-kinds | Runner auto-detection (5 rows) | `TestRunnable_AutoDetection/<row>` | table |
| component-kinds | Replacing the Runnable handler | `TestRunnable_ReplaceHandler` (handler goroutine joined in `t.Cleanup` so goleak passes) | unit |
| component-kinds | Reassigning the Runnable variable does not break AsRunnable | `TestRunnable_ReassignedVariable` (not parallel; restores `Runnable`) | unit |
| dependency-declaration | Declared dependency built before the dependent (3 rows) | `dependency_test.go: TestDependsOn_BuildOrder/<row>` | table |
| dependency-declaration | Declared dependency on an eager service | `TestDependsOn_EagerTarget` | unit |
| dependency-declaration | Declared dependency of an eager service (3 rows) | `TestDependsOn_EagerDependent/<row>` | table |
| dependency-declaration | Declared dependency resolved through scope ancestry | `TestDependsOn_AncestorScope` | unit |
| dependency-declaration | Concurrent invocation builds declared dependencies once | `TestDependsOn_ConcurrentSingleBuild` | race |
| dependency-declaration | Missing or circular declared dependency (5 rows) | `TestDependsOn_Errors/<row>` | table |
| dependency-declaration | Declaring edges dynamically (11 rows) | `TestDeclareDependency/<row>` | table |
| dependency-declaration | Concurrent declarations are race-free | `TestDeclareDependency_Concurrent` | race |
| dependency-declaration | Clones do not copy declared edges (2 rows) | `TestDeclareDependency_Clone/<invoked>` | table |
| dependency-declaration | Start rejects a cycle closed by a dynamic edge | `root_start_test.go: TestStart_RejectsDeclaredCycle` | unit |
| dependency-declaration | Scheduler is shut down before the jobs it drives (2 rows) | `TestDeclaredEdges_ShutdownOrder/<row>` | table |
| dependency-declaration | Ties broken by scope depth then registration order | `root_start_test.go: TestStart_TieBreak`; repeated 100× in `TestStart_TieBreak_Stable` | unit |

Additional mechanism tests (no scenario of their own): `TestRegistrationIndex_Order` (same-scope override keeps the index, child-scope override gets a new one, clone continues the counter), `TestRegistrationIndex_ConcurrentUnique` (100 goroutines, all indexes distinct, `-race`), `TestRegistrationIndex_CloneDuringRegistration` (50 goroutines register on the source while `Clone` runs, `-race -count=20`; all indexes in the clone are distinct; a stress test, because the wrong snapshot order is a narrow race), `TestAwaitDone` (done closed and context ended: true in 1,000 of 1,000 calls; only the context ended: false; done closed while waiting: true), `TestEager_NoOptionsNeverErrors` (compatibility).

**Documentation tests** (each in the group that builds the feature, after its REFACTOR):
- `ExampleRootScope_Start` (default auto-detection and the caller-cleanup idiom)
- `ExampleAsStarter` (interface-registered service)
- `ExampleDependsOn`
- `ExampleDeclareDependency`
- `ExampleNewKind` (a scheduled-job kind with a handler that declares edges)
- `ExampleAsRunnable`
- `ExampleKind_Handle` (replacing the Runnable handler)

**Hot paths and their coverage:**
- **Resolution/invocation:** `Invoke`/`MustInvoke` with and without options. The existing `di_test.go`/`invoke_test.go` stay green unchanged. `TestDependsOn_*` covers the new build branch, including eager dependents. `bench/do_bench_test.go` is compared before and after with `benchstat`.
- **Lifecycle:** `TestStart_*` (start), `TestRootScope_*Shutdown*` (run-once shutdown), `TestStart_ShutdownCancels`, `TestDeclaredEdges_ShutdownOrder` and `TestStart_ShutdownAfterFailure` (shutdown). The existing `di_lifecycle_test.go`, `root_scope_test.go` and `scope_test.go` shutdown tests stay green.
- **Scope traversal:** `TestStart_ScopeTraversal`, `TestStart_TieBreak`, `TestStart_RegistrationOrder`, `TestDependsOn_AncestorScope`, the cross-scope rows of `TestDeclareDependency`.
- **Concurrency:** `TestRootScope_ConcurrentShutdown`, `TestRootScope_ShutdownWithEndedContext`, `TestAwaitDone`, `TestRegistrationIndex_CloneDuringRegistration`, `TestStart_Concurrent`, `TestStart_ConcurrentInvoke`, `TestStart_ShutdownCancels`, `TestRootScope_ShutdownWaiterDeadline`, `TestDependsOn_ConcurrentSingleBuild`, `TestDeclareDependency_Concurrent`, `TestRegistrationIndex_ConcurrentUnique`, all under `-race` with goleak.
- **Panics:** `TestStart_StarterFailure` (recovered Start panic), `TestKind_HandlerFailure` (recovered handler panic), `TestStart_PanicReleasesShutdown` (escaping panic).
- **Compatibility:**
  - `TestProvideOptions_Registration/none` and `TestEager_NoOptionsNeverErrors`: eager invoke without options never errors.
  - `TestProvideOptions_SignatureHasVariadic`: the only signature change.
  - The whole existing suite unchanged.

## Migration Plan

- The change is additive apart from the variadic signature break and the run-once root Shutdown. Both go into `docs/docs/upgrading/` (a new page for this release), with the fix for the break: wrap the generic function in a closure if it was stored as a value.
- Docs gain a "Starting the container" page (Starters, ordering, errors, `AsStarter`, Start/Shutdown interplay), a "Component kinds" page (`NewKind`, `Handle`, `Runnable`, the scheduled-job example) and a "Declared dependencies" section, each written in the task group that builds the feature.
- Rollback is a plain revert. No persisted state.

## Open Questions

- Should `CloneWithOpts` copy consumer-replaced kind handlers? Today a clone gets only the default `Runnable` handler. This affects only clones of containers that replaced handlers, and can be revisited without changing these specs, which do not cover clone plus handlers.
- Whether to export a structured `StartError` (scope, service, kind, cause) later. Callers already get `errors.Is`/`As` through wrapping, so adding it later is non-breaking.
- Whether `ExplainService` should mark declared edges differently from invoked ones. This is display only.
