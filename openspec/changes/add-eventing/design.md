# Design

## Context

See proposal.md (Why) and `specs/events/spec.md`. The parts of today's code that shape this design:

- **Lazy services.** Services are built on first invocation (`service_lazy.go` `instanceOrBuild` holds the wrapper's mutex for the whole provider call). Spring finds `@EventListener` methods by building every singleton; doger must not.
- **Opt-ins live on the registration.** `serviceOptions` (`options.go:10`) already carries `starter`, `kinds`, `order`, `isDefault`; `Start` reads them from the wrapper (`newStartCandidate`). Overrides (`di.go` `override`) and `AsDefault` replacement or yielding (`registerReplaced`, `registerSkipped`) swap or skip the wrapper, so anything stored with the wrapper follows those rules for free.
- **Cycle detection is per invocation chain.** `virtualScope` (`virtual_scope.go:45`) records the invoker chain; `ErrCircularDependency` already exists (`errors.go:15`). Resolving a service "by name from its own scope" would drop that chain.
- **`Ordered` / `WithOrder`** affect collections only, and `Ordered` is read from a built instance, so neither is used for listener order.
- **Container hooks** fire at fixed points of `Start` and `Shutdown` (`root_start.go`, the close path); Closed hooks run after the close phase is `closeClosed`.
- **Extensions** (`add-extensions`, archived) keep installed entries in `root.ext` (`atomic.Pointer[extensionRegistry]`, nil until the first `Extend`); scope names can be overridden (`ExtendScopeName`) or carry an instance (`ext:<name>/<instance>`), so names are not a reliable way to find them.
- **Logging.** `InjectorOpts.Logf` defaults to a no-op (`injector.go:271`), so an error routed only to `Logf` is silent with zero configuration.
- **Clone.** `CloneWithOpts` copies the scope tree, then kind entries; `Clone()` passes `s.opts.copy()`, which copies option fields by value.
- **RootScope size.** New root state follows the `ext` pattern: a lazily allocated pointer, so containers that never use events keep their size class.

## Goals / Non-Goals

**Goals:**
- Typed publish and listen with no service built before an event reaches it, and no reflection call per listener on the hot path.
- Listener state that cannot drift from the registration it belongs to.
- No deadlock from nested publishing, publishing during a build, or a full executor; no goroutine leaks.
- Zero behavior change for containers that register no listener.

**Non-Goals:**
- Durable or transaction-bound delivery (follow-up `add-durable-events`).
- Ordering across async deliveries; only the order of synchronous calls within one `Publish` is guaranteed.
- Unique listener IDs (they label listeners; uniqueness is a durable-delivery concern).

## Decisions

### Overview

```
+------------------------------ package doger -------------------------------+
|                                                                            |
|  Publish(ctx, i, event, opts...)                                           |
|        |                                                                   |
|        v                                                                   |
|  +--------------+  plan per (scope, type, local)   +-----------------------+ |
|  | dispatcher   |--------------------------------->| scope.eventPlans      | |
|  | (D1,D5-D7)   |                                  | (cache, invalidated   | |
|  +--------------+                                  |  by scope changes)    | |
|     |     |                                        +-----------------------+ |
|     |     |  plan built from                                                |
|     |     +-----> service wrappers: serviceOptions.listeners  (D2)          |
|     |     +-----> scope.funcListeners (Listen)                (D2)          |
|     |     +-----> root.ext installed entries -> ext scopes    (D4)          |
|     |                                                                      |
|     +-- sync ---> listener (built lazily, via the caller's chain) (D6)     |
|     +-- async --> EventExecutor (bounded default, inline when nested) (D8) |
|                                                                            |
|  Start/Shutdown --> lifecycle events after each hook (D9)                  |
|  errors with no caller --> HookEventError -> Logf -> log (D10)             |
+----------------------------------------------------------------------------+
```

### D1. Events are plain values; dispatch by assignability
- `Publish(ctx context.Context, i Injector, event any, opts ...PublishOption) error`. There is no type parameter: dispatch uses the dynamic type, so `E` would add no safety, and removing it later would break callers.
- A listener declared for `L` receives an event whose dynamic type `T` satisfies `T.AssignableTo(L)`. This mirrors Spring's supertype matching; pointer-receiver interfaces match only pointer events (spec rows `PtrEvent`).
- `nil`, and nil values of pointer, map, slice, chan and func kinds (also inside an interface), return `ErrInvalidEvent`. This is one `reflect.ValueOf(event)` per `Publish`, not per listener.
- *Alternative:* exact static type via `Publish[E]`. Rejected: no interface listeners, and the type parameter is misleading.

```
Publish(ctx, i, event)
   |
   +-- invalid event? ------------------------> ErrInvalidEvent
   +-- ctx done? -----------------------------> ctx.Err()
   +-- scope or root closed? -----------------> ErrPublishAfterShutdown
   |
   +-- plan = scope.plans[(type, local)]   miss: build (D3, D4)
   |
   +-- for each listener in plan:
          ctx done?            -> stop, return ctx.Err()
          filter false?        -> skip (not built)
          async?               -> executor.Submit (D8)
          else                 -> resolve with caller's chain (D6)
                                  OnEvent / method(inst, ctx, e)
                                  error|panic -> *ListenerError -> mode (D5)
```

### D2. Listener bindings live with what owns them
- **Service listeners:** `AsListener[E](opts...)` and `ListenMethod(m, opts...)` append a `listenerBinding{typ reflect.Type; call func(ctx, inst, ev any) error; opts listenerOptions; index int}` to `serviceOptions.listeners`, exactly as kinds append `kindBinding`. The typed closure is built at registration, so dispatch calls no `reflect.Value.Call`.
  - The registration check (implements `Listener[E]`, or is `S` for `ListenMethod`) runs with the existing `AsStarter`-style check and panic wording; interface-typed registrations are checked on the instance at first dispatch.
  - Repeated `AsListener[E]` for one `E` is deduplicated by `typ` on that registration.
  - Because the binding is on the wrapper, an override, an `AsDefault` replacement or yield, a removal at scope shutdown and an extension rollback all change what is reached, with no extra bookkeeping (spec "Registration changes end a listener").
- **Function listeners:** `Listen` stores `funcListener{id, typ, call, opts, seq}` in the scope's own list (allocated on first use). Scope shutdown clears it; `stop` removes one entry and reports whether it did (`func() bool`, like `context.AfterFunc`'s stop).
- *Alternative considered:* a root-level registry keyed by scope and name (the first design). Rejected: it must be resynchronized on override, `AsDefault`, scope shutdown and extension rollback, and the audit found it drifts in each.

```
 registration                               dispatch
 ------------                               --------
 Provide(i, NewMailer,                      plan for (req, OrderPlaced):
   AsListener[OrderPlaced](),                 walk req, ancestors, ext scopes
   ListenMethod((*Mailer).OnCancelled))         for each wrapper:
        |                                         serviceOptions.listeners
        v                                         where typ matches
 wrapper "*Mailer"                              plus scope.funcListeners
   serviceOptions.listeners = [                 sort (D3)
     {OrderPlaced,    OnEvent, m0},           cache in scope.eventPlans
     {OrderCancelled, OnCancelled, m1} ]
 override / AsDefault replace / scope shutdown
   -> wrapper gone or swapped -> its bindings go with it
```

### D3. Order and listener IDs
- **Order:** ascending `ListenerOrder` (default 0); then reach rank (publishing scope = 0, each ancestor +1 up to the root, then extension scopes in installation order after the root); then registration index (the existing `nextRegIndex` for services, a per-container sequence for functions). Stable and build-free.
- **IDs:** `ListenerID(id)` if non-empty; otherwise `<scope path>/<service>#<event type>` with `/m<n>` for the n-th `ListenMethod` of a registration, or `<scope path>/func#<n>`. Scope path segments are scope names with `%` and `/` percent-escaped, so `ext:a/b` cannot collide with `ext:a` then `b`. The event type in IDs is the import-path form; messages use the short `reflect` form.

Worked example for a publish through `req` (child of root), extension `x` installed:

```
 listener   scope      ListenerOrder   rank   reg   position
 --------   ------     -------------   ----   ---   --------
 r2         [root]     -1              1      2     1
 c1         req         0              0      4     2
 c2         req         0              0      5     3
 *Mailer    req         0 (*)          0      6     4
 r1         [root]      0              1      1     5
 x1         ext:x       0              2      3     6

 (*) WithOrder(-10) and Ordered() = -20 are ignored: collection-only,
     and Ordered would need a build.
```

### D4. Reach and the plan cache
- Reached scopes: `i`'s scope, each ancestor up to the root, then the private scopes of extensions whose registry entry is `extensionInstalled` (looked up through `root.ext`, never by name). `PublishLocal()` keeps only `i`'s scope.
- **Plan cache:** per scope, keyed by `(dynamic type, local)`. A plan is invalidated when any reached scope's services or function listeners change, or an extension finishes installing or rolls back: each scope keeps an `eventsVersion` (bumped where `typeMatches` is reset today, and by `Listen`/stop), and a plan stores the versions it was built from. The cache dies with its scope, so request scopes don't grow a global map.

```
                        [root]                 reached (ancestor, rank 1)
                     /     |     \
          ext:audit    tenant-a    tenant-b    ext:fake (plain scope)
          reached      reached     NOT reached NOT reached
          (installed   (ancestor,  (sibling)   (not in root.ext)
           extension,   rank ...)
           after root)    |
                         req   <-------------- Publish(ctx, req, e)
                          |                    reached (self, rank 0)
                       child                   NOT reached (descendant)
```

### D5. Errors
- `type ListenerError struct { Listener string; EventType reflect.Type; Err error }`, message ``DI: listener `<id>` failed on event `<type>`: <cause>``, `Unwrap() error`.
- `EventErrorMode`: `EventFailFast` (zero value, following the `PlacementPrivate` precedent for a safe zero default) and `EventContinue` (`errors.Join` in listener order). `String()` is provided. Other values panic in `New`/`NewWithOpts`.
- Panics are recovered into `*ListenerError` (`%w` for errors, `%v` otherwise), also for filter predicates.
- Context: checked before dispatch and between listeners; a done context stops dispatch with `ctx.Err()`.

```
 EventFailFast (default)              EventContinue
 -----------------------              -------------
 a ok -> b ERR -> stop                a ok -> b ERR -> c ERR
 return *ListenerError(b)             return errors.Join(LE(b), LE(c))
```

### D6. Resolving listeners without deadlocks
- A service listener is resolved through the publisher's injector chain: dispatch wraps the reached scope in a `virtualScope` whose parent is the caller's virtual scope (when `i` is one). Invoking the listener's service then runs the existing cycle check, so publishing from a provider whose event reaches the service being built (directly or through the listener's dependencies) returns `ErrCircularDependency` instead of re-entering `instanceOrBuild`'s mutex on the same goroutine.
- The resolution records **no DAG edge**: a publisher does not depend on its listeners, and an edge would distort shutdown order.
- Nested `Publish` from a listener is plain recursion: dispatch holds no lock while user code runs (plans are immutable snapshots).

### D7. Lifetime and shutdown interaction
- Scope shutdown first marks the scope closed for publishing, drains async deliveries addressed to listeners of that scope and its descendants, then clears its function listeners and lets service shutdown remove wrappers as today. Dispatch wraps each async task so it increments an in-flight counter on the listener's scope before `Submit` and decrements it when the task ends; the scope drain waits for its own and its descendants' counters to reach zero, bounded by the shutdown context. This works with any `EventExecutor`, since it does not rely on the executor's own `Shutdown`.
- During root service shutdown, a synchronous dispatch skips service listeners that are not built yet or already removed (never building a service mid-shutdown), and still calls function listeners.

### D8. Async delivery and executors
- `type EventExecutor interface { Submit(ctx context.Context, task func(context.Context)) error; Shutdown(ctx context.Context) error }`. `Shutdown` matches `http.Server.Shutdown` and lets an executor also be a doger `ShutdownerWithContextAndError`.
- **Default:** `NewBoundedEventExecutor(n uint) *BoundedEventExecutor` (panics for `n == 0`), a semaphore of `n` slots plus a `sync.WaitGroup`. The root uses `InjectorOpts.EventAsyncParallelism`, 0 meaning `runtime.GOMAXPROCS(0) * 4` read at creation.
  - `Submit` selects on a free slot, `ctx.Done()` and the executor's closing channel; a done `ctx` fails first even with a free slot (checked before the select), so the outcome is deterministic.
  - **Nested submissions:** each task's context carries an unexported marker. A `Submit` whose context carries the marker of the same executor and finds no free slot runs the task inline on the caller's goroutine. That rules out the "every slot waits for itself" deadlock (spec "Async listener publishing to an async listener").
  - The task context is `context.WithoutCancel(publishCtx)` wrapped in `context.WithCancelCause`, cancelled with `ErrPublishAfterShutdown` when a drain deadline passes.
- **Errors:** a `Submit` error or panic becomes a `*ListenerError` returned by `Publish`; the task's own errors and panics go to `HookEventError` (D10).
- *Overrides:* `InjectorOpts.EventExecutor` replaces the default. An executor given in options is owned by the caller and shared by clones (D11).

```
 publisher goroutine                 BoundedEventExecutor (n slots)
 -------------------                 ------------------------------
 Publish -> Submit(ctx, task) --+    +---+---+---+---+
                                |    | 1 | 2 | 3 | 4 |
   ctx done? -> ctx.Err()       +--->+---+---+---+---+
   closing?  -> ErrPublishAfterShutdown
   full and nested (marker)?  -> run task inline
   full?  -> wait: slot | ctx.Done | closing
 Publish returns <--------------+
                                     task ctx = WithCancelCause(
                                       WithoutCancel(publishCtx))
 Shutdown(ctx) ------------------->  close intake, wake waiters,
                                     wait WaitGroup or ctx end
                                       -> cancel tasks with cause
```

### D9. Lifecycle events
- Types `ContainerStarting`, `ContainerStarted`, `ContainerReady`, `ContainerFailed{Err error}`, `ContainerClosing`, `ContainerClosed{Report *ShutdownReport}`, each with `Root *RootScope`.
- Each is published right after the hooks of the same point, on that goroutine, through an internal path that: always uses continue mode; sends every error, panic and build failure to `HookEventError`; ignores the closed check for `ContainerClosed`; and costs one nil check on the root's event state when no listener exists.
- `ContainerStarting` follows the Starting hooks, so a vetoed `Start` publishes only `ContainerFailed`. A listener service built for `ContainerStarting` is the pipeline's first instantiation; a failed build is reported, not a veto.
- On a re-raised pipeline panic (`failAndRepanic`), `ContainerFailed` is published with the Failed hooks, before the re-panic.

```
 Start                                         Shutdown (root)
 -----                                         ---------------
 HookContainerStarting (may veto)              HookContainerClosing
 event ContainerStarting                       event ContainerClosing
 build + start services, kinds, Runnables      executor.Shutdown(ctx): close intake,
 HookContainerStarted                            wake waiting publishers, drain
 event ContainerStarted                        shut services down (as today;
 HookContainerReady                              sync dispatch skips unbuilt and
 event ContainerReady                            removed service listeners)
                                               HookContainerClosed
 on failure / re-raised panic:                 event ContainerClosed (function
 HookContainerFailed -> event ContainerFailed    listeners only, past the closed check)
                                               any later Publish -> ErrPublishAfterShutdown
```

### D10. Errors with no caller
- `InjectorOpts.HookEventError func(ctx context.Context, root *RootScope, err error)` receives async and lifecycle failures as `*ListenerError` (named like the other `Hook*` fields).
- Default when unset: `Logf` with the error message; when `Logf` is also unset, the standard `log` package. This is the one place the library logs without `Logf`; it keeps zero-config failures visible, and either field silences or redirects it. A panic in the hook is recovered and logged the same way.

### D11. Clone
`CloneWithOpts` copies wrappers with their `serviceOptions` (so service bindings come along) and copies each scope's function listeners as the same functions. Plan caches and the default executor are per root and never copied; an `EventExecutor` set in `InjectorOpts` is shared, since `copy()` copies the field and the caller owns it (documented on the field).

## Risks / Trade-offs

- [The standard `log` fallback logs outside `Logf`] → only for event failures with no caller, documented on `HookEventError`; setting `Logf` or the hook redirects it.
- [Inline execution of nested async tasks when the executor is full turns them synchronous] → only on that task's goroutine and only when full; documented on `NewBoundedEventExecutor`.
- [A listener that ignores its context can outlive `Shutdown` after a drain timeout] → the report holds the deadline error; documented. goleak tests use listeners that honor the context.
- [Building a listener service at `ContainerStarting` instantiates before discovery] → documented; listen to `ContainerStarted` to avoid it.
- [Unbounded recursive `Publish` overflows the stack] → not detected, as with recursive calls; documented.
- [Plan invalidation by version counters on every reached scope costs a few loads per publish] → measured by `BenchmarkPublish`; still far cheaper than rebuilding.

## Public API & Defaults

Additive only. **No exported API breaks.** `Ordered` and `WithOrder` keep their collection-only meaning.

| Identifier | Kind | Default / override |
|---|---|---|
| `Publish(ctx, i, event any, opts ...PublishOption) error` | func | Sync, fail-fast |
| `PublishOption`, `PublishLocal()` | option | Off: ancestors and extension scopes are reached |
| `Listener[E]` | interface | `OnEvent(context.Context, E) error` |
| `AsListener[E](opts ...ListenerOption)`, `ListenMethod[S, E](m func(S, context.Context, E) error, opts ...ListenerOption)` | `ProvideOption` | Built on first event |
| `Listen[E](i, fn func(context.Context, E) error, opts ...ListenerOption) (stop func() bool)` | func | Ends at stop or scope shutdown |
| `ListenerOption`: `ListenerOrder(int)`, `ListenerFilter[F](func(F) bool)`, `ListenerAsync()`, `ListenerID(string)` | options | 0, none, sync, derived |
| `ListenerError{Listener string; EventType reflect.Type; Err error}` | error type | `errors.As`, `Unwrap` |
| `ErrInvalidEvent`, `ErrInvalidListener`, `ErrPublishAfterShutdown` | sentinels | `ErrCircularDependency` reused |
| `EventErrorMode`: `EventFailFast`, `EventContinue` | enum + `String()` | `InjectorOpts.EventErrorMode` |
| `EventExecutor`, `NewBoundedEventExecutor(n uint) *BoundedEventExecutor` | interface, constructor | `InjectorOpts.EventExecutor`, `InjectorOpts.EventAsyncParallelism uint` (0 = `GOMAXPROCS*4`) |
| `InjectorOpts.HookEventError func(ctx, *RootScope, error)` | field | `Logf`, else standard `log` |
| `ContainerStarting`, `ContainerStarted`, `ContainerReady`, `ContainerFailed`, `ContainerClosing`, `ContainerClosed` | event types | Published after their hooks |

## Test Strategy

Each scenario maps to one Go test; `Examples` rows run as named subtests. Everything runs under `-race`; `TestMain`'s goleak check covers every async and shutdown test; tests that could hang use `testWithTimeout`.

| Scenario | Test | Kind |
|---|---|---|
| Publish matches listeners by the event's dynamic type | `event_test.go: TestPublish_Assignability/<row>` | table-driven |
| Publish with no listeners changes nothing | `event_test.go: TestPublish_NoListeners` | unit (compatibility) |
| Publish rejects or normalizes zero arguments | `event_test.go: TestPublish_ZeroArguments/<row>` | table-driven |
| AsListener validates the registered type | `event_listener_test.go: TestAsListener_Registration/<row>` | table-driven |
| Interface-typed listener is checked at first dispatch | `event_listener_test.go: TestAsListener_InterfaceCheckedAtDispatch` | unit |
| Listener build follows the service kind | `event_listener_test.go: TestListener_Build/<row>` | table-driven |
| Listener provider error fails the delivery | `event_listener_test.go: TestListener_ProviderError` | unit |
| One service listens to several event types | `event_listener_test.go: TestListenMethod_MultipleEvents` | unit |
| ListenMethod validates its input | `event_listener_test.go: TestListenMethod_Registration/<row>` | table-driven |
| Stop unregisters a function listener | `event_listener_test.go: TestListen_Stop/<row>` | table-driven |
| Listen argument handling | `event_listener_test.go: TestListen_Arguments/<row>` | table-driven |
| Default and explicit IDs | `event_listener_test.go: TestListenerID/<row>` | table-driven |
| Registration changes end a listener | `event_lifetime_test.go: TestListenerLifetime/<row>` | table-driven |
| Publishing through a shut-down scope | `event_lifetime_test.go: TestPublish_ShutDownScope` | unit |
| Listeners run in order, rank, then registration order | `event_dispatch_test.go: TestDispatch_Order` | unit |
| Filter decides delivery | `event_dispatch_test.go: TestDispatch_Filter/<row>` | table-driven |
| Filter type mismatch panics at registration | `event_dispatch_test.go: TestDispatch_FilterTypeMismatch` | unit |
| Reach depends on where the listener lives | `event_dispatch_test.go: TestDispatch_Reach/<row>` | table-driven |
| Extensions still installing are not reached | `event_dispatch_test.go: TestDispatch_ReachInstallingExtension` | unit |
| First error stops dispatch | `event_dispatch_test.go: TestDispatch_FailFast` | unit |
| A typed-nil error is an error | `event_dispatch_test.go: TestDispatch_TypedNilError` | unit |
| Errors are joined in continue mode | `event_dispatch_test.go: TestDispatch_ErrorMode/<row>` | table-driven |
| Panic becomes a listener error | `event_dispatch_test.go: TestDispatch_Panic/<row>` | table-driven |
| Nested publish runs inline | `event_dispatch_test.go: TestDispatch_Nested` | unit |
| Publishing during a build | `event_dispatch_test.go: TestPublish_DuringBuild/<row>` | table-driven, `testWithTimeout` |
| Cancellation stops dispatch | `event_dispatch_test.go: TestDispatch_Context/<row>` | table-driven |
| Async listener runs after Publish returns | `event_async_test.go: TestAsync_RunsAfterPublish` | race, `testWithTimeout` |
| Where an async error goes | `event_async_test.go: TestAsync_ErrorRouting/<row>` | table-driven (captures `log` output) |
| Backpressure at the limit | `event_async_test.go: TestAsync_Backpressure/<row>` | table-driven, `testWithTimeout` |
| Default parallelism | `event_async_test.go: TestAsync_DefaultParallelism/<row>` | table-driven (sets GOMAXPROCS, restores it) |
| Async listener publishing to an async listener | `event_async_test.go: TestAsync_NestedNoDeadlock` | `testWithTimeout` |
| Custom executor receives async deliveries | `event_executor_test.go: TestExecutor_Custom` | unit |
| Submit failures are returned | `event_executor_test.go: TestExecutor_SubmitFailure/<row>` | table-driven |
| NewBoundedEventExecutor bounds concurrency | `event_executor_test.go: TestBoundedEventExecutor/<row>` | table-driven |
| Lifecycle events follow their hooks | `event_lifecycle_test.go: TestLifecycleEvents_Order/<row>` | table-driven |
| Lifecycle events carry their data | `event_lifecycle_test.go: TestLifecycleEvents_Data` | unit |
| Lifecycle listener failures are reported, not returned | `event_lifecycle_test.go: TestLifecycleEvents_Observational/<row>` | table-driven |
| Existing hooks behave as before | `event_lifecycle_test.go: TestLifecycleEvents_HooksUnchanged` | unit (compatibility) |
| In-flight deliveries finish before services shut down | `event_shutdown_test.go: TestShutdown_Drain/<row>` | table-driven, goleak |
| Waiting publishers are released at shutdown | `event_shutdown_test.go: TestShutdown_ReleasesWaiters` | `testWithTimeout` |
| Child scope drain | `event_shutdown_test.go: TestScopeShutdown_Drain` | unit, goleak |
| Publishing during and after shutdown | `event_shutdown_test.go: TestShutdown_PublishAfter` | unit |
| ContainerClosed reaches function listeners only | `event_shutdown_test.go: TestShutdown_ClosedFunctionOnly` | unit |
| Concurrent publishers during shutdown | `event_race_test.go: TestPublish_RacingShutdown` | race, `testWithTimeout` |
| Concurrent publishers and registrations | `event_race_test.go: TestPublish_ConcurrentRegistration` | race, `testWithTimeout` |
| Clone delivers to its own listeners | `event_clone_test.go: TestClone_Listeners/<row>` | table-driven |
| Executor ownership in clones | `event_clone_test.go: TestClone_ExecutorOwnership/<row>` | table-driven |
| Option validation | `event_options_test.go: TestEventOptions/<row>` | table-driven |

**Docs and performance**

| Purpose | Test | Kind |
|---|---|---|
| Defaults and overrides in godoc | `event_example_test.go: ExamplePublish`, `ExampleListen`, `ExampleListenMethod`, `ExampleAsListener`, `ExampleListenerAsync`, `ExampleNewBoundedEventExecutor`, `ExampleInjectorOpts_eventErrorMode` | Example |
| Plan cache and dispatch hot path | `bench: BenchmarkPublish/{0,1,10,100}-listeners`, `BenchmarkPublish/nested-scopes` | benchmark (regression baseline) |

**Hot paths and their coverage:**
- **Publish / dispatch:** assignability, argument checks, order, reach, error modes, context, plan invalidation under concurrent registration and scope churn (`-race`).
- **Listener resolution:** laziness per service kind, cycle detection during builds, lifetime across override, `AsDefault` and scope shutdown.
- **Lifecycle:** event placement relative to hooks, observational semantics, root and scope drains, publish-after-shutdown, racing shutdown (goleak).
- **Scope traversal:** the reach table, installing extensions, clones.
- **Concurrency:** executor bounds, nested async publishing, waiter release at shutdown.

## Migration Plan

Additive; nothing to migrate. `examples/event-driven` is rewritten on the new API to show replacing a hand-rolled bus (the workspace module list is unchanged). Rollback means not using the API.

## Open Questions

- The default parallelism factor (`GOMAXPROCS * 4`) is a starting point to revisit with `BenchmarkPublish`. It is overridable through `EventAsyncParallelism`; changing the factor changes one spec row (`Default parallelism`), so a change would come with a spec update.
