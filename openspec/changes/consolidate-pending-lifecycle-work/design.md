# Design

## Context

See proposal.md (Why, What Changes, Defect) for the motivation, and `specs/` for the behavior. This change builds on `add-start-lifecycle-and-kinds` (archived before apply), whose design is cited as **start-lifecycle D1–D12**:
- the lifecycle model: `lifecycleMu`, start and close phases, `startCancel`/`startDone`, `closeDone`/`closeReport`, `beginStart`/`finishStart`/`beginClose`/`finishClose`, and the extension points;
- the pipeline `runStartPipeline` → `discoverCandidates`, `buildCandidates`, `runStage`, `checkCycles`, `orderByDependencies`, `startStarters`;
- the registration index, `serviceOptions`/`ProvideOption`, `DeclareDependency`/`dependencyNeighbors`, and kinds (`kindEntry`, `kindsMu`).

Two previously planned changes are folded in. Their designs are carried below verbatim, as Parts H (container hooks) and K (collections). The third, `add-clock-fix-healthcheck-timeouts`, landed in #5 (`InjectorOpts.Clock`, deterministic health-check timeouts) and is cited as **clock D1–D6** (archived design). Inside a Part, `Dn` means that Part's own decision n; `start-lifecycle Dn` means the archived design. Parts F, Q, P, S and X are new.

**Precedence.** Some folded Parts (and the archived clock design) list as a Non-Goal, or as "phase 2", items this change now covers. For those items, the new Parts supersede that text:
- clock design Non-Goals: per-service timeouts → S6.
- Part H: per-scope hooks, `Refresh`, phase accessor → S1, P2, S2.
- Part K: deterministic `InvokeAs`, struct-tag `[]T` injection, live collections → S5, S3, S4.

Everything else in a folded Part stands as written. The specs are authoritative wherever prose and specs differ.

Facts verified for the new parts (scratch probes on a copy of the repo at `e116a5f`, outside the repository):
- `serviceAlias.shutdown` delegates to the target wrapper. An eager target has no "built" flag, so it is shut down twice whenever its alias is shut down first. That is always the case once the alias was invoked, because invocation records the edge alias → target (200/200 runs).
- `mergeShutdownReports` sets `Succeed = len(Errors) > 0` (`errors.go:91`). Its four callers in `scope.go` are all overwritten by `Scope.ShutdownWithContext`, so the bug is not observable through the public API.
- `TestMixedServiceTypesPerformance`, a timing flake (5/200 before #5), runs on `synctest` bubble time since #5: 0/200 failures on the merged branch. It needs no work here.
- `New()` costs 13 allocs/op since `add-start-lifecycle-and-kinds`, up from 11. The difference is the default `Runnable` handler entry and its closure.
- `HealthCheck` concurrent with root `Shutdown` with `HealthCheckParallelism: 2`: no race detected in 2,000 runs.
- A child scope shut down directly stays in its parent's `childScopes`; only a parent's shutdown clears them.
- `invokeByGenericType` and the `invokeByTags` fallback stop at the first match of `serviceForEachRec`, which visits the current scope, then its ancestors, and is unordered within a scope.

## Goals / Non-Goals

**Goals:**
- Carry the three folded plans without changing their decided behavior, except where the merge forces it: container hooks and `Refresh` (X2); `WithOrder` in `registration-options` (spec delta).
- One incremental-start mechanism behind `Start`, `Refresh` and `Scope.Start`, so ordering, errors, hooks and Shutdown interplay are shared, not duplicated.
- Every flagged behavior change is pinned by a test and documented on the upgrading page.

**Non-Goals:**
- Restarting a container, or re-starting a service that was already started. Each service starts at most once.
- Container hooks for incremental starts, and scope hooks fired by `Refresh` for new child scopes. Scope hooks fire only on explicit `Scope.Start` and on child-scope shutdown.
- Changing `Injector` (no `Start` on the interface), or applying `HealthCheckGlobalTimeout` to per-service checks.
- A fix for the unreproduced `healthCheckPool` race (F5).

## Decisions

### Part F — Findings from PR #4

**F1. An alias never shuts its target down.**
- `serviceAlias.shutdown` returns `nil` without touching the target; scope shutdown still removes the alias entry.
- `serviceAlias.isShutdowner` keeps delegating, so explain and debug output are unchanged.
- *Alternative:* a "shut down" flag on `serviceEager`. Rejected by decision (user): it keeps the surprising cross-scope effect, where shutting down a child scope stops a parent's target.

**F2. `mergeShutdownReports` computes `Succeed = len(Errors) == 0`.**
- A failing internal unit test (`TestMergeShutdownReports_Succeed`) proves the defect first. No spec covers it, because it is not observable.

**F3. `TestMixedServiceTypesPerformance`.** Resolved by #5, which runs it on `synctest` bubble time. Re-measured on the merged branch: 0 failures in 200 runs. No task.

**F4. The default `Runnable` handler is implicit.**
- `NewWithOpts` no longer calls `runnableKind.Handle`. Instead `runStage` treats a container without a `runnableKind` entry as if the package-level `defaultRunnableEntry` (built once) occupied the **first** `StageAfterStart` slot, exactly where `NewWithOpts` used to register it.
- `Runnable.Handle` appends a real entry, which replaces the implicit one. Clones copy entries (Q1), and the absence of an entry is copied as absence.
- Verified with `BenchmarkNew` allocs/op equal to 11, not by a unit test: alloc counts vary across Go versions.

**F5. The `healthCheckPool` race is investigated, not fixed.**
- One task writes `TestRootScope_HealthCheckDuringShutdown` (HealthCheck with `HealthCheckParallelism: 2` racing root Shutdown) and runs it with `-race -count=2000`.
- If no race is reported, the test stays as a race guard and the item is closed as not reproduced.
- If a race is reported, implementation stops and the fix is proposed to the user as added scope (CLAUDE.md rule 5).

### Part Q — Decided open questions of start-lifecycle

**Q1. Clones copy kind handlers.**
- `CloneWithOpts` copies `s.kinds` (a slice clone under `kindsMu`) into the clone after `NewWithOpts`. `Clone` goes through it.
- Entries are stateless with respect to the container (their `run` closure receives scope, name, instance and meta), so sharing them is safe.

**Q2. `StartError`.**
- `type StartError struct { Scope *Scope; Service string; Kind string; Op StartOp; Cause error }`, with `type StartOp string` and constants `StartOpBuild`, `StartOpStart`, `StartOpHandle`.
- `Error()` renders exactly the current messages:
  - build: ``DI: failed to build service `s` in scope `x`: <cause>``;
  - start: ``DI: failed to start service `s` in scope `x`: <cause>``;
  - handle: ``DI: kind `k` failed for service `s` in scope `x`: <cause>``.
- The AsStarter-without-Start failure is `Op: start` with a cause whose message is today's full message. Its `Error()` returns that message unprefixed, so the text is unchanged.
- `Unwrap()` returns `Cause`. Abort wrapping (`%w (start aborted: %w)`) wraps the `*StartError`, so `errors.As` still finds it.
- `wrapStartError` and the build/handle formats become constructors of it. Non-service errors (guards, `checkAbort`, cycles) stay plain errors.
- *Alternative:* separate error types per op. Rejected: one type with `Op` is easier to match.

**Q3. Edge source in the DAG.**
- `DAG` maps become `map[ServiceDescription]map[ServiceDescription]DependencySource`. `addDependency` takes a source and keeps the strongest: invoked (0) < depends-on (1) < declared (2), lower wins.
- `invokeAnyByName` records `invoked`; `resolveDeclaredDependencies` records `depends-on`, through an internal variant `invokeAnyByNameAs(source)`; `DeclareDependency` records `declared`.
- `explainService` returns sources. `ExplainServiceDependencyOutput.Source` is a `DependencySource` int enum with `String()` and `MarshalText`, so JSON shows `"invoked"`. Its zero value is `DependencySourceInvoked`, so existing literals keep their meaning.
- *Alternative:* a string type. Rejected: its zero value `""` would not mean invoked.

### Part P — Phase 2 of the start lifecycle

**P1. Started and handled bookkeeping.**
- The `RootScope` keeps `started map[any]struct{}`, keyed by **wrapper pointer**, and `handled map[any]map[*kindID]struct{}` (wrapper → kinds that handled it), both guarded by `startedMu`, which is never held across user code, so shutdown can drop entries while a pipeline runs (V12, group 49).
- Keying by wrapper means an `Override` after Start registers a new wrapper, which a later `Refresh` starts.
- A new `pipelineMu sync.Mutex` serializes `Start`, `Refresh` and `Scope.Start` pipelines. It **is** held across user code, unlike `lifecycleMu`. Shutdown never takes it and nothing takes it while holding `lifecycleMu`, so Shutdown cannot deadlock on it.
- `Refresh`/`Scope.Start` check the phase before taking `pipelineMu` (V3), so a call made while the first Start runs fails fast with `ErrNotStarted`. Calling them synchronously from code running inside a Refresh or Scope.Start still deadlocks, like calling `Shutdown` there (start-lifecycle Risks). Documented.

**P2. One incremental pipeline.**
- `runPipeline(ctx, roots []*Scope)` discovers candidates in the subtrees of `roots`, using the absolute depth from the root container for ordering. It skips wrappers in `started` (Starters) and pairs in `handled` (kinds), then builds, runs stages, checks cycles and starts, as start-lifecycle D7 does. The first Start has nothing in either set, so one code path serves both; no `incremental` flag is needed.
- `Start` runs it with `roots = [root.self]`. `Refresh` uses `[root.self]`; `Scope.Start` uses `[s]`, and on `root.self` behaves as Refresh.
- Ancestors that the subtree depends on are built through normal invocation, but are not candidates, so they are not started.
- Lifecycle state for an incremental run:
  - `beginIncremental` (under `lifecycleMu`) checks the phase (Q-P3), then records `incCancel`/`incDone` for the running run;
  - `beginClose` captures them like `startCancel`/`startDone`;
  - `waitForStart` cancels and awaits either one.
- No container hook fires (X2). Scope hooks fire only for an explicit `Scope.Start` on a child scope (S1).

**P3. `ErrNotStarted`.**
- New sentinel `ErrNotStarted = errors.New("DI: container not started")`, returned when the start phase is not `startStarted`/`startReady`.
- `ErrStartAfterShutdown` takes precedence once the close phase is not open, as in `beginStart`.

**P4. Parallel start.**
- `InjectorOpts.StartParallelism uint` is copied in `copy()`; 0 and 1 mean sequential and keep the current loop.
- For n > 1, `startStarters` runs a Kahn scheduler over the `requires` sets that `orderByDependencies` already computes:
  - the coordinating goroutine dispatches the ready Starter with the smallest `(depth, regIndex)` to one of n worker goroutines, after an abort check;
  - workers run hooks and `startInstance` and report back;
  - the first error stops dispatching, the coordinator waits for in-flight workers, then returns that error.
- Every worker exits before `startStarters` returns (goleak). Kind handlers stay sequential.
- *Alternative:* `errgroup.SetLimit`. Rejected: it adds a dependency (`x/sync`) for one loop, and its first-error cancellation does not respect DAG readiness.

**P5. `HookAfterBuild`.**
- It fires inside the wrappers, after a successful provider call and outside `handleProviderPanic`, so a hook panic propagates:
  - `serviceLazy.build`;
  - each successful `serviceTransient.getInstance`;
  - `serviceEager.getInstance` once, guarded by an `atomic.Bool`.
- The scope is the `*Scope` underlying the virtual scope the wrapper receives (`invokeAnyByName` always passes `newVirtualScope(serviceScope, …)`).
- It therefore runs between `onBeforeInvocation` and `onAfterInvocation`. Aliases never fire it, because their `getInstanceAny` delegates to the target, which fires it under its own name.

### Part S — Phase 2 of hooks and collections, and remaining follow-ups

**S1. Scope hooks.**
- New `InjectorOpts` fields `HookScopeStarting/Started/Closing/Closed` with nil-to-empty init, `copy()`, `AddScope*Hook` and nil-skipping, exactly as Part H D4.
- `Scope.ShutdownWithContext` on a non-root scope fires Closing after its children shut down and before its services, and Closed at the end. A child scope shut down directly and again through its parent fires each time.
- `Scope.Start` on a child scope fires Starting (veto, like H D5) before the pipeline and Started after success.
- Observer panics reuse Part H D6 (`callHookSafely`, dispatch last), with points `scope-started`, `scope-closing` and `scope-closed`.
- The root's own scope never fires scope hooks.

**S2. `RootScope.Phase()`.**
- It reads both phases under `lifecycleMu`: a non-open close phase maps to `PhaseClosing`/`PhaseClosed`; otherwise the start phase maps to `PhaseIdle/Starting/Started/Ready/Failed`. This requires Part H D1 (`startReady`).
- `ContainerPhase` is an `int` enum with `String()`; out-of-range values print as `ContainerPhase(n)`.

**S3. Struct tag `*`.**
- `invokeByTags` checks the tag value `*` before name resolution: `[]E` → collection of `E`, `map[string]E` → map collection, anything else → ``DI: tag `*` requires a slice or map[string] field, got `S.F` ``.
- The Part K pipeline is generalized to a `reflect.Type` target: `collectionCandidatesFor(injector, target reflect.Type)`, with the generic `InvokeAll[T]` calling it with `reflect.TypeFor[T]()`. The result is built with `reflect.MakeSlice`/`reflect.MakeMap`.

**S4. Live collections.**
- `type Collection[T any] struct{ injector Injector }`, created by `InvokeCollection[T](i)` with `getInjectorOrDefault(i)`, so creation builds nothing.
- `All()`/`Map()` run the Part K pipeline on the captured injector at read time. While `i`, a provider's virtual scope, belongs to a provider that is still running, its invoker chain gives self-exclusion and edge recording, as in `InvokeAll`. Once that provider has returned, reads drop the chain (V5).
- The value is safe to copy and to read concurrently, because the pipeline shares no new state (Part K D8).

**S5. Deterministic `InvokeAs`.**
- `selectByType` walks the visible services nearest scope first, aliases included, and picks the minimum of `(distance asc, regIndex asc, name asc)`. Because distance comes first, it stops after the nearest scope that has a match; the result equals collecting every candidate. A candidate hidden by a same-name service in a nearer scope is skipped (V14).
- The `invokeByTags` implicit-aliasing fallback uses the same selection.
- This is nearest-first, the opposite direction of collection order (ancestors first, Part K D2), on purpose: it preserves today's "current scope first" behavior.

**S6. Per-service health-check timeout.**
- `HealthCheck*`/`HealthCheckNamed*` wrap their context with `clockwork.WithTimeout(ctx, clock, HealthCheckTimeout)` when the option is greater than zero, before calling `serviceHealthCheck`.
- The clock D1–D3 rule (`healthCheckResult`, already on main) then decides the result. The global timeout is not applied.

### Part X — Cross-cutting rules

**X1. Implementation order.** F → H → Q → K → P → S1/S2 (needs H) → S3/S4 (needs K) → S5 → S6 → integration. Each Part's tests are written against the code state that Part assumes; tasks follow this order.

**X2. Container hooks and incremental start.**
- Part H's "at most once per container" stays true: `Refresh`/`Scope.Start` fire no container hooks.
- Failed hooks fire only for the first `Start`, whose error (now a `*StartError` when a service failed) they receive unchanged.

**X3. Lock rules.** `lifecycleMu` is never held across user code (start-lifecycle D6). `pipelineMu` (P1) is held across pipeline user code and never taken under `lifecycleMu` or by Shutdown. `kindsMu` and `declareMu` keep their scopes.

**X4. Upgrading page.** Every flagged item gets an entry in `docs/docs/upgrading/start-lifecycle.md`, renamed in title to cover this release: alias shutdown, `InvokeAs` determinism, the `*` struct tag, the per-service health-check timeout, and clone handler copying. (The health-check timeout rule from #5 is already documented in `migrating-from-samber-do.md`.)

### Part V — Verification fixes

Found by `/opsx:verify` and a code review, each reproduced by a failing test before its fix (CLAUDE.md rule 5). Decisions V3, V4, V5, V6, V10 and V14 were made by the user.

**V1. Shutdown sets the cancel cause while it holds `lifecycleMu`.** `beginClose` calls the in-flight run's cancel function with `ErrStartAfterShutdown` before releasing `lifecycleMu`; `waitForStart` only waits. `finishStart` and `finishIncremental` therefore always see the cause when the close phase is not open. Before, a pipeline returning between `beginClose` and `waitForStart` produced `DI: start aborted: %!w(<nil>)`.

**V2. Parallel start re-raises per-service hook panics on the caller.** The worker goroutine recovers a panic from `startCandidate` (the per-service hooks; a Starter's own panic is already an error) and reports it to the coordinator, which stops dispatching, waits for the running workers, then re-panics the first value. `finishStart` then sees `panicked` as in sequential mode.

**V3. Incremental start fails fast while Start runs (user decision).** `startIncremental` checks the phase under `lifecycleMu` before taking `pipelineMu` and returns `ErrNotStarted` (or `ErrStartAfterShutdown`, which takes precedence) without waiting. `beginIncremental` re-checks after the lock, because Shutdown may begin meanwhile.

**V4. A child scope shutdown waits for a run that is starting its services (user decision).**
- Under `lifecycleMu`, the running pipeline records `runScopes`, the scopes of its candidates, after discovery, and drops candidates whose scope is closing. A child scope's shutdown marks the scope closing under `lifecycleMu` and, if the in-flight run has it in `runScopes`, waits for that run's done channel without cancelling it, bounded by its context (failure keyed by the scope with an empty service name, like root).
- The closing mark is a counter that lasts while the shutdown runs. A directly shut-down child scope stays attached to its parent, so registrations made in it afterwards are started by a later Refresh, as before.
- Only runs with candidates in the scope are waited for, so a scope created and shut down by a provider during Start does not deadlock. Shutting a scope down synchronously from code of a run that is starting it deadlocks, as for root; documented.
- The root's own scope keeps root Shutdown's model (start-lifecycle D6).

**V5. Live-collection reads drop a returned provider's chain (user decision).** `virtualScope` gets a `returned atomic.Bool`, set by `invokeAnyByNameAs` once the wrapper's `getInstance` returned. A read on a returned virtual scope resolves from its underlying `*Scope`, excludes only the holder (the chain's last name), and records no edge. Reads while the provider runs keep the chain.

**V6. The `*` tag requires `map[string]E` exactly (user decision).** The key check compares the key type with `reflect.TypeFor[string]()`, matching the spec and S3.

**V7, V8. Missing tests.** `Refresh`/`Scope.Start` StartError rows, Scope.Start ancestors, and parallel incremental start get tests. They are expected to pass on arrival; each is mutation-checked and recorded in notes.md.

**V9. Stale design text.** P1, P2, S4 and S5 above now describe the implementation.

**V10. `HookAfterBuild` runs outside the wrapper lock (user decision).** `serviceLazy.build` returns whether it built; `getInstance` fires the hook after releasing `s.mu`. `serviceEager` fires it after releasing its dependency lock. The hook still fires once per build, on the building goroutine, between the invocation hooks.

**V11. `DependencySource.UnmarshalText`** decodes `invoked`, `depends-on` and `declared`, and rejects other text with an error naming it.

**V12. Override forgets the replaced wrapper.** When registration replaces a wrapper (Override), the old wrapper is dropped from `started` and `handled`, like shutdown does (group 49).

**V13. `StartParallelism` is clamped to the number of Starters** before the `int` conversion, so huge values (`^uint(0)`) behave as "all at once" instead of wrapping negative.

**V14. `InvokeAs` honors shadowing (user decision).** A candidate in an ancestor scope is skipped when a scope between the invoking scope and it registers the same name. The check runs only for matching candidates and walks parent pointers, so it allocates nothing. The struct fallback is then consistent: the selected name always resolves to the selected service.

**V15, V16. Duplication (refactors).** `queueServiceHealthcheck` calls `perServiceHealthCheckContext`. One generic veto loop serves container and scope Starting hooks, and one observer loop serves container and scope observer hooks, each taking its panic-wrapping function, so messages are unchanged.

### Part H — Container lifecycle hooks (folded from `add-container-lifecycle-hooks`)

#### H: Context

See proposal.md (Why, What Changes) and `specs/container-lifecycle-hooks/spec.md` for the required behavior.

This change fills extension points that `add-start-lifecycle-and-kinds` reserves. Its design is cited below as **start-lifecycle D6** (lifecycle state) and **D7** (pipeline). This change uses those names and does not redefine them:
- **State under `lifecycleMu`:**
  - start phase `startIdle → startStarting → startStarted | startFailed`;
  - close phase `closeOpen → closeClosing → closeClosed`;
  - `startCancel`, `startDone`, `closeDone`, `closeReport`.
  - Phases change only by compare-and-set, and the lock is never held while hooks run.
- **`StartWithContext`:**
  - normalizes a nil ctx;
  - `beginStart` returns a wrapped `ErrStartAfterShutdown` unless `closeOpen`, and `ErrAlreadyStarted` unless `startIdle`; it creates `startCtx` with `context.WithCancelCause`;
  - `runStart(startCtx)` holds `defer finishStart(&err)`, which sets `startStarted`/`startFailed` (turning a nil result into the abort error when `closePhase != closeOpen`), calls `startCancel(nil)` and closes `startDone`, and runs `runStartPipeline(startCtx)`;
  - then, after `runStart` returns, `pointAfterStart`, which therefore runs after `startDone` is closed.
- **D7 pipeline:**
  1. `pointStarting`, then an abort check.
  2. Discover.
  3. Build.
  4. `StageBeforeStart`.
  5. Cycle check.
  6. Starters.
  7. `pointStartersUp`, then `StageAfterStart`, then a final abort check.

  An abort returns `DI: start aborted: %w` with `context.Cause(startCtx)`.
- **`RootScope.ShutdownWithContext`:**
  1. `beginClose` decides the winner: the caller whose `closeOpen → closeClosing` CAS succeeds.
  2. Waiters wait on `closeDone` or their ctx. They get a copy of `closeReport`, or, on deadline, a failed report keyed by the root scope.
  3. The winner calls `startCancel(ErrStartAfterShutdown)` if a Start is in flight, and waits for `startDone` or its ctx.
  4. `pointClosing`.
  5. The winner shuts down services and stops the health-check pool.
  6. `finishClose` sets `closeClosed`, stores `closeReport` and closes `closeDone`.
  7. `pointClosed`.
- **Fixes owned by start-lifecycle.** The reproduced concurrent-`Shutdown` defects (spurious `ErrServiceNotFound` entries, the `healthCheckPool` race) are fixed there by the winner/waiter split. This change does not duplicate them.

Facts in the current code that shape the approach:
- **Hook storage.** `InjectorOpts` (`injector.go`) holds every hook list as an exported slice.
  - `NewWithOpts` (`root_scope.go`) replaces nil slices with empty ones.
  - `RootScope.Add*Hook` methods `append` to `s.opts`. The lists are not mutex-protected and are assumed to be configured before use.
  - The existing per-service runners neither skip nil entries nor recover panics. A per-service hook panic propagates to the caller.
- **Clone.** `RootScope.Clone()` calls `CloneWithOpts(s.opts.copy())`. `CloneWithOpts(opts)` builds the clone from `opts` only (`root_scope.go:300-313`). So only `Clone` inherits hooks.
- **Shutdown paths.** `ShutdownOnSignals*` ends in `RootScope.ShutdownWithContext` (`root_scope.go:342,348`). `Scope.ShutdownWithContext` and `virtualScope.Shutdown` never pass through `RootScope`.
- **Logging.** `InjectorOpts.Logf` is always non-nil after `NewWithOpts` (`noOpLogf` default).

#### H: Goals / Non-Goals

**Goals:**
- Six container hook lists with typed payloads, registered via `InjectorOpts` or `RootScope.AddContainer<State>Hook`.
- One panic policy, `HookContainerPanic`, that is loud by default and overridable.
- Hooks placed only at start-lifecycle's extension points: at most once per container, no data race, no deadlock when post-start or Closed hooks call `Shutdown`.

**Non-Goals:**
- Exposing the lifecycle phases publicly (no `State()` accessor in this change).
- Per-scope container hooks, or firing hooks from child `Scope` or `virtualScope` shutdown (phase 2).
- A `Refresh` operation, or restarting a failed container.
- Redefining phases, guards, cancellation, waiter semantics or report copying. Those are start-lifecycle D6.

#### H: Decisions

**D1. Phases come from start-lifecycle D6. This change adds only `startReady`.**
- The start phase becomes `startIdle → startStarting → startStarted → startReady`, or `→ startFailed`.
- At `pointAfterStart`, after its Started hooks have returned, Start moves `startStarted → startReady` by CAS under `lifecycleMu`. D6 states that no code may assume `startStarted` is terminal.
- `startReady` is "not idle" for `beginStart`, which therefore returns `ErrAlreadyStarted`.
- No other state is added.
- `pointStartersUp` is not used by this change and stays empty. Started fires at `pointAfterStart` (D7).

**D2. Start's outcome is decided once, in start-lifecycle's `finishStart`.**
- start-lifecycle D6 step 3 already turns a nil pipeline result into `startFailed` with `err = fmt.Errorf("DI: start aborted: %w", context.Cause(startCtx))` (cause `ErrStartAfterShutdown`) when `closePhase != closeOpen`. This change does not modify `finishStart`.
- That covers the window between D7's final abort check and `finishStart`. The D7 abort checks cover every earlier point.
- At `pointAfterStart`, Start fires Started+Ready or Failed purely from that local outcome, never by re-reading shared state.
- *Alternative:* re-check the close phase before Ready. Rejected: it adds a second decision point and a "Started but not Ready" outcome.

**D3. Ordering against a concurrent `Shutdown`.**
- *Shutdown begins while the pipeline runs.* The winner cancels `startCtx`, then waits for `startDone` or its ctx. Only then does it reach `pointClosing` and fire Closing. Start aborts and takes the Failed path at `pointAfterStart`. Failed (Start goroutine) and Closing/Closed (Shutdown goroutine) run concurrently, so their relative order is unspecified. What is specified: Closing runs after the pipeline returned, or after the Shutdown ctx ended.
- *Shutdown begins after the pipeline returned.* `startDone` is already closed. Closing and Closed may run before or while Started, Ready or Failed run on the Start goroutine.
- The old guarantee "Started/Ready/Failed fire before Closing" is dropped. It required keeping `startDone` open across those hooks, and that made a hook calling `Shutdown` deadlock.

**D4. Hook storage and registration follow the existing style.**
- New `InjectorOpts` fields:
  - `HookContainerStarting []func(context.Context, *RootScope) error`
  - `HookContainerStarted`, `HookContainerReady`, `HookContainerClosing` `[]func(context.Context, *RootScope)`
  - `HookContainerFailed []func(context.Context, *RootScope, error)`
  - `HookContainerClosed []func(context.Context, *RootScope, *ShutdownReport)`
  - `HookContainerPanic func(scope *RootScope, hook string, recovered any)`, a single function where nil means "re-panic"
- `NewWithOpts` initializes the nil slices to empty, as it does for the existing hooks. `copy()` duplicates the six slices and copies `HookContainerPanic`.
  - So `Clone()` inherits the hooks, while `CloneWithOpts(opts)` uses only `opts`. That matches the existing behavior for every other hook (spec "Clone copies container hooks but not state").
- `RootScope.AddContainerStartingHook`, `AddContainerStartedHook`, `AddContainerReadyHook`, `AddContainerFailedHook`, `AddContainerClosingHook` and `AddContainerClosedHook` append to the lists.
  - `Add*(nil)` is a no-op, and nil entries are skipped at run time. The existing per-service runners are unchanged.
- *Alternative:* a single `HookContainerLifecycle` listener with an event enum. Rejected during exploration: payloads would become optional fields.

**D5. Firing points (all are start-lifecycle extension points):**

```
StartWithContext:
  beginStart -> runStart { defer finishStart(&err) (D2) ; pipeline } returns
  runStartPipeline(startCtx):
    pointStarting:  Starting hooks(startCtx)   // error or panic = veto -> pipeline returns it
    abort check, discover, build, StageBeforeStart, cycle check, Starters,
    pointStartersUp (unused), StageAfterStart, final abort check
  pointAfterStart (startDone closed):
    startStarted: Started hooks(ctx); CAS startStarted -> startReady; Ready hooks(ctx); result nil
    startFailed:  Failed hooks(ctx, err); result err
  dispatch the first observer panic (D6), else return the result

ShutdownWithContext (winner only):
  beginClose -> [startCancel + wait] -> pointClosing: Closing hooks(ctx)
  -> shut down services, stop pool -> finishClose (closeDone closed)
  -> pointClosed: Closed hooks(ctx, report) -> dispatch the first observer panic (D6)
```

- **Starting is before the abort check.** D7 step 1 is "`pointStarting`, then the abort check", so the Starting hooks run even with a cancelled context (spec "Starting hooks run before the context check"). This matches the agreed order. No reconciliation is needed.
- **Veto error.** A veto returns `fmt.Errorf("DI: container start aborted: %w", err)` from the pipeline, so `errors.Is` matches the hook's own error. `finishStart` then sets `startFailed`, and Failed hooks receive exactly the error `Start` returns.
- **Contexts.** Starting hooks receive `startCtx`, which keeps the caller's values and is cancelled by a Shutdown. Started, Ready and Failed receive the caller's normalized ctx, because `finishStart` has already cancelled `startCtx`. Closing and Closed receive the Shutdown ctx, normalized from nil to `context.Background()`.
- No new sentinel error is added.

**D6. Panic handling: recover always, dispatch once, loud by default.**
- `callHookSafely(fn) (recovered any, panicked bool)` wraps every hook call.
- **Starting hooks.** A panic is a veto. The error is `fmt.Errorf("DI: container starting hook panicked: %w", v)` when `v` is an error, and `%v` otherwise. It is wrapped as the veto error (D5) and never reaches `HookContainerPanic`.
- **Observer hooks** (Started, Ready, Failed, Closing, Closed).
  - Every hook in the list still runs, and so does the rest of the operation: `startReady`, service shutdown, `finishClose` and the Closed hooks.
  - The first recovered value of the operation is kept with its point name: `started`, `ready`, `failed`, `closing` or `closed`. Later ones are logged with `Logf("DI: container %s hook panicked: %v", point, v)`.
  - Dispatch happens last, after `startDone`/`closeDone` are closed and every hook has run. A non-nil `HookContainerPanic(root, point, value)` is called, and `Start`/`Shutdown` return their normal result. A nil one triggers `panic(value)` with the original value.
  - Because dispatch comes after `finishStart`/`finishClose`, waiters never hang on a panicking winner.
- *Why re-panic by default:* per-service hooks already propagate panics, and a silently swallowed panic in a readiness hook is a production bug that `noOpLogf` would hide.
- *Alternative:* return a joined error from `Start`. Rejected: observers must not change the outcome, and `Shutdown` has no error return.

**D7. Started means "the pipeline succeeded"; Ready means "every Started hook has returned".**
- Both fire at `pointAfterStart`, after `startDone` closes, so they may call `Shutdown` (spec "Post-start and Closed hooks may shut the container down").
- Consequence: Started runs after the `StageAfterStart` handlers, including `Runnable`, not at `pointStartersUp`.
- Use Started for last wiring (route registration, cache warm-up) and Ready to signal orchestrators.

**D8. Starting and Closing hooks must not call `Shutdown` synchronously.**
- **Starting.** It runs inside the pipeline, before `startDone` closes. A root `Shutdown` from it becomes the winner, cancels `startCtx` and waits for `startDone`. With a deadline it proceeds when the deadline expires, and its report fails with a root-scope error matching `context.DeadlineExceeded`, recorded even when no service was built (start-lifecycle D6 Shutdown step 4). After the hook returns, the D7 abort check fails with cause `ErrStartAfterShutdown`.
- **Closing.** It runs at `pointClosing`, before `closeDone` closes. A nested `Shutdown` is a waiter on `closeDone`. With a deadline it gets start-lifecycle's failed waiter report keyed by the root scope.
- **Closed** runs after `finishClose`, so a nested `Shutdown` immediately returns a copy of the first report.
- These rules are documented on the fields and adders, and pinned by "Starting or Closing hook calls Shutdown with a deadline" and "Post-start or Closed hook calls Shutdown".

**D9. Naming: keep `Closing`/`Closed`.** The container-level `Starting`/`Started` pair mirrors them, and `HookBeforeShutdown`/`HookAfterShutdown` already mean per-service events.

### Part K — Collection invocation and ordering (folded from `add-collection-invocation`)

#### K: Context

See proposal.md (Why, What Changes) and the specs `collection-invocation` and `collection-ordering`.

Current mechanics this change builds on:
- **`invokeByGenericType`** (`invoke.go:137-199`) walks `serviceForEachRec` and stops at the first service for which `serviceCanCastToGeneric[T]` is true. That check is a reflect-type check on `getReflectType()`, which every wrapper (lazy, eager, transient, alias) implements without building.
  - It detects cycles with `virtualScope.detectCircularDependency(realName)`.
  - It builds via `getInstanceAny(newVirtualScope(serviceScope, chain+realName))`.
  - It records the edge with `vScope.addDependency` **after** a successful build. `invokeAnyByName` records it before building.
  - It fires `onBeforeInvocation`/`onAfterInvocation` and `Logf` with the **requested type name** (`serviceAliasName`), but uses the **real name** for the chain, the edge and `serviceScope.onServiceInvoke`. No existing test asserts the hook name (`invoke_test.go:353-373`).
- **`serviceForEachRec`** (`scope.go:697`) visits the **current scope first, then ancestors**, over map snapshots taken under `RLock`. It is unordered within a scope. A `virtualScope` delegates to the scope where the invoking service is registered.
- **`AsNamed`** (`di.go:501`) refuses an alias unless `Initial` is assignable to `Alias`. So an alias can never match `T` while its target does not.
- **Shutdown** removes services from the scope map (`scope.go:776`). A collection after shutdown therefore sees nothing.
- **From `add-start-lifecycle-and-kinds`:**
  - a per-service registration index, unique per root container (start-lifecycle D3), kept by `Override*` and copied, with the counter, on clone;
  - service metadata carried by the opaque `ProvideOption` (start-lifecycle D1, D2), which `registration-options` explicitly lets later changes extend.
- `ServiceTypeAlias` and `getServiceType()` already exist (`service.go:36`, `service_alias.go:59`).
- **Error style** (`errors.go`, `invoke.go`): sentinel errors with a `DI:` prefix, wrapped with `fmt.Errorf("...%w...")`. `handleProviderPanic` turns a panic value into an error (`error` values kept as-is, others become `DI: %v`). `MustInvoke` uses `must1`, which panics with the error itself.

#### K: Goals / Non-Goals

**Goals:**
- One shared collection pipeline behind `InvokeAll`, `InvokeAllMap` and their `Must*` variants.
- Per-member semantics (cycle detection, edges, hooks, invocation tracking) shared with `InvokeAs` through one helper, without changing what `InvokeAs` reports.
- Deterministic order with no dependence on map iteration.
- Linear cost in the number of visible services, plus the sort.

**Non-Goals:**
- Caching collections or invalidating them on registration (snapshot semantics only).
- Struct-tag `[]T` injection, live or lazy collections, deterministic `InvokeAs` (proposal Out of scope).
- Parallel building of members. They are built sequentially in default order, which keeps construction deterministic and matches how `Start` builds.

#### K: Decisions

**D1. Candidate selection: nearest-name view, then self, alias and type filters.**
1. Walk the invoking scope and its ancestors with `serviceForEachRec`, keeping the **first occurrence of each name**: nearest wins. Record each candidate's scope depth (root = 0) and registration index.
2. Drop the invoking service itself: when the injector is a `virtualScope`, the candidate whose name equals the last name of `invokerChain` (D3a).
3. Drop aliases (`getServiceType() == ServiceTypeAlias`).
4. Keep only services where `serviceCanCastToGeneric[T]` is true.

Because the type filter comes last, a nearest service that is not assignable to `T` hides an ancestor that is (spec: shadowing row 4). This matches what `InvokeNamed` would resolve for that name.
- *Alternative:* filter by type first, then deduplicate names. Rejected: it would include an ancestor service that named resolution can never reach from this scope.

**D2. Default order: (scope depth asc, registration index asc, name asc).**
- Collect candidates with depth and index, then `slices.SortFunc` by that key. Iteration order of `serviceForEachRec` is irrelevant.
- Within one container, indexes are unique. start-lifecycle D3 copies the counter on clone, so clones keep unique indexes too (spec "Default order in a clone"). The name is a defensive final tie-break, so the order is total even if indexes ever collide.

**D3. Build members sequentially in default order through one shared per-member helper.**
- Extract from `invokeByGenericType` the part after selection into `invokeServiceInstance(injector, vScope, hookName, realName, scope, service) (any, error)`. In order, it does:
  - cycle detection on `realName`;
  - `onBeforeInvocation(scope, hookName)`, `getInstanceAny`, `onAfterInvocation(scope, hookName, err)`;
  - on success only: `addDependency(realName)`, `onServiceInvoke(realName)`, `Logf` with `hookName`.
- `invokeByGenericType` passes `hookName = inferServiceName[T]()`, so `InvokeAs` keeps reporting the type name. `TestInvokeAs_HookReceivesTypeName` is written before the extraction to pin this.
- Collections pass `hookName = realName = member name`.
- Edges are recorded only after a successful build, as `invokeByGenericType` does today, so a failing member records no edge.
- On the first error, return `fmt.Errorf("DI: could not invoke collection member `%s`: %w", name, err)`. The call stops, and members already built stay built (lazy caches are kept).
- *Alternative:* build every member and join the errors with `errors.Join`. Rejected: building after a failure wastes work and can trigger further side effects, while the spec only requires the first failing member.
- *Alternative:* a single `name` parameter. Rejected: it either changes `InvokeAs` hook names (an unflagged behavior change) or gives collection hooks the type name.

**D3a. Self-exclusion.**
- Only the *last* name in the invoker chain is excluded: the service whose provider is running.
- Any earlier chain member that is also a candidate is kept. `detectCircularDependency` then fails the call with `ErrCircularDependency` (spec "Deeper cycles still fail").
- Outside a provider there is no invoker, so nothing is excluded.
- *Alternative:* exclude every chain member. Rejected: it would silently hide genuine cycles.
- *Alternative:* no exclusion. Rejected: then every composite fails, and no override could make including itself work.

**D4. Ordering is a separate stable pass, for `InvokeAll` only.**
After all members are built, compute each member's order value:
1. `WithOrder` metadata, if present. `Order()` is then never called.
2. Otherwise, if `any(instance).(Ordered)` succeeds, `Order()` is called inside a `recover`.
3. Otherwise `0`.

Then run `slices.SortStableFunc` by order value over the default-ordered slice.
- An `Order()` panic becomes `fmt.Errorf("DI: Order() of collection member `%s` panicked: %w", name, cause)`. `cause` is the panic value if it is an error, or `fmt.Errorf("%v", r)` otherwise.
- `InvokeAllMap` skips this pass entirely.
- *Alternative:* sort by `(order, depth, index, name)` in a single pass. Equivalent, but it would need the order value before building, and `Order()` is an instance method.

**D5. `WithOrder` storage and acceptance.**
- `WithOrder(n)` returns a `ProvideOption` that sets `order *int` in the service metadata from start-lifecycle D1. A pointer is used so "set" is distinguishable from "set to 0".
- Because it is an ordinary `ProvideOption`, every helper that takes `...ProvideOption` accepts it. That is all 18 `Provide*`/`Override*`/`Lazy*`/`Eager*`/`Transient*` helpers (spec "WithOrder on every registration helper").
- Applying it again overwrites the value, so the last one wins. It needs no registration-time validation.
- `clone()` copies it, and `Override*` replaces the metadata wholesale, as start-lifecycle specifies. Nothing other than the ordering pass reads it.

**D6. Empty results are non-nil.**
Return `[]T{}` / `map[string]T{}` when nothing matches. On error, return `nil, err`.

**D7. `Must*` variants use `must1`**, so they panic with the exact error value, like `MustInvoke`.

**D8. Concurrency.**
- Candidate selection reads scope maps through the existing snapshotting iterators (`serviceForEachRec` under `RLock`).
- Members are built through the service wrappers, which already serialize their own builds (`serviceLazy.mu`). Concurrent callers therefore build each lazy member exactly once while no shutdown runs.
- A member removed by a concurrent shutdown after the snapshot is still built from the wrapper held in the snapshot. This is the same as a concurrent `Invoke` today, and it is race-free (spec "Concurrent collection and shutdown").
- No new shared state is introduced.

## Risks / Trade-offs

- [`pipelineMu` held across user code: `Refresh`/`Scope.Start` called synchronously from Refresh-time code deadlocks] → Documented on `Refresh`, `Scope.Start` and the starter page, next to the existing Shutdown rule. From Start-time code they fail fast with `ErrNotStarted` (V3), and `Start` still returns `ErrAlreadyStarted` (guards before the lock).
- [A child scope shutdown waits for a run that is starting its services (V4)] → Shutting such a scope down synchronously from that run's code deadlocks. Documented on `Scope.Shutdown`; runs without candidates in the scope never wait.
- [Build hooks run outside the wrapper lock (V10)] → Another goroutine may receive the instance before the hook returned. Documented on `HookAfterBuild`.
- [Started set keyed by wrapper pointer keeps wrappers reachable until shutdown] → A wrapper is dropped from `started` and `handled` when shutdown removes it from its scope: its scope's shutdown (child or root) or its own `ShutdownNamed`. Memory is bounded by the live registrations.
- [Parallel start runs per-service hooks concurrently] → Opt-in only; documented on `StartParallelism` and `HookBeforeStart`/`HookAfterStart`.
- [Alias shutdown change: child-scope shutdown no longer stops a parent target through an alias] → Flagged, on the upgrading page, pinned by the service-aliases rows.
- [`InvokeAs` determinism changes which service some consumers get] → Flagged. The previous pick was random, so no consumer could rely on it.
- [`do:"*"` takes over a service named `*`] → Flagged as BREAKING (edge case). Migration: rename the service or inject it by type.
- [Struct-literal breakage from new exported fields (`InjectorOpts`, `ExplainServiceDependencyOutput`)] → Only unkeyed literals break, which the Go 1 compatibility guidelines exclude.
- [MODIFIED deltas cannot be archived before `add-start-lifecycle-and-kinds`] → Task 0.1 gates the change on that archive.
- [The change is large] → Tasks are grouped per Part, each group green and committed on its own, so the work can pause between groups.

**Part H — Container lifecycle hooks:**

- **[A hook registers more hooks during a run]** → Each list is copied into a local slice before iterating. Additions to the list that is currently running take effect only on its next run, which never comes because transitions happen once. Additions to a later list (e.g. a Started hook adding a Ready hook) do fire. Documented on the `Add*` methods.
- **[`Add*Hook` racing with `Start`/`Shutdown` on another goroutine]** → Same as today's `Add*Hook`: registration is configuration-time only. Documented, and not made concurrent-safe, for consistency with the existing hooks.
- **[Closing/Closed run concurrently with Started/Ready/Failed (D3)]** → Hooks that share state across these points must synchronize it. Documented on the fields and on the docs page.
- **[Concurrent `Shutdown` waiters may return before the Closed hooks have run]** → They are released by `finishClose`, which precedes `pointClosed` (start-lifecycle D6). Documented on `HookContainerClosed`.
- **[The default re-panic from a Closing/Closed hook means the caller never sees the report]** → Accepted as the loud default. `HookContainerPanic` restores normal returns.
- **[The start-lifecycle `finishStart` branch (see D2) is a narrow race window that no test can hit deterministically]** → Its observable rule ("Shutdown before the pipeline returns → Failed with `ErrStartAfterShutdown`") is pinned through the D7 abort checks by "Shutdown races a running Start". The branch keeps the rule true inside that window too.
- **[A slow or blocking Closing/Closed hook delays process exit]** → Accepted. Hooks are synchronous by design and receive the shutdown context so they can honor deadlines.
- **[Root shutdown through `virtualScope` or the root's `*Scope` skips the hooks]** → Documented. Only `RootScope` methods are container-level.

**Part K — Collection invocation and ordering:**

- **[O(N) scan of every visible service on every call]** → `serviceCanCastToGeneric` is a reflect check with no build. Measured by `BenchmarkInvokeAll/{10,100,1000}`. Callers in hot loops should resolve once and keep the snapshot, as documented.
- **[`InvokeAll[any]` builds every non-alias service]** → Documented as a sharp edge, and pinned by a spec scenario. Not prevented, because it is a legitimate (if rare) use.
- **[Shadowing by a non-matching nearest service surprises users]** → Documented with an example. It is consistent with named resolution, and the alternative would expose unreachable services.
- **[Visibility inside a provider is the provider's registration scope, not the requester's]** → Same rule as `Invoke`. Documented in godoc and pinned by "Collection inside a provider sees the provider's scope".
- **[The registration index depends on `add-start-lifecycle-and-kinds`]** → This change is ordered after it. Group 9's RED tests fail if the index is missing or not copied on clone.
- **[Hooks fire once per member, so N hook calls per `InvokeAll`]** → Same as calling `Invoke` N times. Documented.

## Public API & Defaults

Added (all additive unless flagged):
- **Part H:**
  - `InjectorOpts.HookContainerStarting/Started/Ready/Failed/Closing/Closed`, `HookContainerPanic`;
  - `RootScope.AddContainerStartingHook` … `AddContainerClosedHook`.
- **Part K:** `InvokeAll`, `MustInvokeAll`, `InvokeAllMap`, `MustInvokeAllMap`, `Ordered`, `WithOrder`.
- **Part Q:**
  - `StartError`, `StartOp`, `StartOpBuild`, `StartOpStart`, `StartOpHandle`;
  - `DependencySource`, `DependencySourceInvoked`, `DependencySourceDependsOn`, `DependencySourceDeclared`;
  - `ExplainServiceDependencyOutput.Source`.
- **Part P:**
  - `RootScope.Refresh`, `RootScope.RefreshWithContext`, `Scope.Start`, `Scope.StartWithContext`, `ErrNotStarted`;
  - `InjectorOpts.StartParallelism`;
  - `InjectorOpts.HookAfterBuild`, `RootScope.AddAfterBuildHook`.
- **Part S:**
  - `InjectorOpts.HookScopeStarting/Started/Closing/Closed`, `RootScope.AddScopeStartingHook` … `AddScopeClosedHook`;
  - `ContainerPhase`, `PhaseIdle` … `PhaseClosed`, `RootScope.Phase`;
  - `Collection[T]`, `InvokeCollection`.
- **Part V:** `DependencySource.UnmarshalText`.

Changed behavior, flagged, with migration:
- Per-service `HealthCheck*` honor `HealthCheckTimeout`. Migration: none if the option is 0; otherwise expect the bound.
- An alias never shuts its target down. Migration: shut the target by its own name or through its own scope.
- `InvokeAs` / implicit struct aliasing pick deterministically. Migration: use `InvokeNamed` to pin a specific service.
- **BREAKING (edge case):** the struct tag `do:"*"` means a collection. Migration: rename a service called `*`.
- Clones copy replaced kind handlers. Migration: re-register a different handler on the clone if needed.
- `InvokeAs` / implicit struct aliasing skip services shadowed by a same-name service in a nearer scope (V14). Migration: invoke the ancestor service by name from its own scope.
- A child scope `Shutdown` waits for a running Start, Refresh or Scope.Start that is starting its services (V4). Migration: do not shut down, from Start-time code, a scope that the running start is starting.

Defaults: see the proposal's Defaults & Control table. Every new hook list defaults to empty, and `StartParallelism` to sequential.

## Test Strategy

All tests run under `go test -race` with goleak (`TestMain`). Tests that could hang use `testWithTimeout`, except inside `synctest.Test` bubbles, which use bubble time and handshakes (clock D5). The folded Parts keep their test tables unchanged. New scenarios map as follows:

| Spec | Scenario | Test | Kind |
|------|----------|------|------|
| container-phase | Phase follows the lifecycle (6 rows) | `phase_test.go: TestPhase_Lifecycle/<row>` | table |
| container-phase | Hooks observe the intermediate phases | `phase_test.go: TestPhase_SeenByHooks` | unit |
| container-phase | Phase is race-free | `phase_test.go: TestPhase_RaceFree` | race |
| container-phase | Unknown phase values print as a number | `phase_test.go: TestContainerPhase_StringUnknown` | unit |
| container-phase | Clone starts idle | `phase_test.go: TestPhase_CloneIdle` | unit |
| scope-lifecycle-hooks | Registration order and nil hooks | `scope_hooks_test.go: TestScopeHooks_RegistrationOrder` | unit |
| scope-lifecycle-hooks | Starting hook allows or vetoes Scope.Start (3 rows) | `scope_hooks_test.go: TestScopeStartingHook_Veto/<row>` | table |
| scope-lifecycle-hooks | Closing and Closed around child scope shutdown (4 rows) | `scope_hooks_test.go: TestScopeHooks_ClosingClosed/<row>` | table |
| scope-lifecycle-hooks | Root scope fires no scope hook | `scope_hooks_test.go: TestScopeHooks_RootFiresNone` | unit |
| scope-lifecycle-hooks | Observer scope hook panics (3 rows) | `scope_hooks_test.go: TestScopeHooks_ObserverPanic/<row>` | table |
| service-build-hooks | Which invocations fire the build hook (6 rows) | `build_hooks_test.go: TestAfterBuildHook_Fires/<row>` | table |
| service-build-hooks | Build hook order relative to invocation hooks | `build_hooks_test.go: TestAfterBuildHook_Order` | unit |
| service-build-hooks | Registration order and nil hooks | `build_hooks_test.go: TestAfterBuildHook_RegistrationOrder` | unit |
| service-build-hooks | Build hook in a child scope and clones (2 rows) | `build_hooks_test.go: TestAfterBuildHook_Scopes/<row>` | table |
| service-build-hooks | A panicking build hook propagates | `build_hooks_test.go: TestAfterBuildHook_Panic` | unit |
| service-aliases | Target is shut down once (6 rows) | `service_alias_test.go: TestServiceAlias_ShutdownTargetOnce/<row>` | table (regression) |
| service-aliases | Repeated runs are stable | `service_alias_test.go: TestServiceAlias_ShutdownTargetOnce_Stable` (200 runs) | race (regression) |
| service-aliases | Invoking the alias after its target is shut down | `service_alias_test.go: TestServiceAlias_InvokeAfterTargetShutdown` | unit |
| type-resolution | Selection among several matches (6 rows) | `invoke_test.go: TestInvokeAs_Deterministic/<row>` | table |
| type-resolution | Selection is stable across runs | `invoke_test.go: TestInvokeAs_DeterministicStable` | unit |
| type-resolution | Single and missing matches are unchanged (2 rows) | `invoke_test.go: TestInvokeAs_SingleAndMissing/<row>` | table |
| type-resolution | Struct fallback selects deterministically | `invoke_test.go: TestInvokeStruct_ImplicitAliasingDeterministic` | unit |
| collection-struct-injection | Collection fields (4 rows) | `invoke_all_struct_test.go: TestInvokeStruct_CollectionFields/<row>` | table |
| collection-struct-injection | Custom struct tag key | `invoke_all_struct_test.go: TestInvokeStruct_CollectionCustomTag` | unit |
| collection-struct-injection | Invalid collection fields (2 rows) | `invoke_all_struct_test.go: TestInvokeStruct_CollectionInvalidField/<row>` | table |
| collection-struct-injection | A failing member fails the struct | `invoke_all_struct_test.go: TestInvokeStruct_CollectionMemberFailure` | unit |
| collection-struct-injection | Struct collections inside a provider exclude the invoking service | `invoke_all_struct_test.go: TestInvokeStruct_CollectionExcludesSelf` | unit |
| live-collections | Later registrations are visible (2 rows) | `collection_test.go: TestCollection_LaterRegistrations/<read>` | table |
| live-collections | Creating a live collection builds nothing | `collection_test.go: TestCollection_CreationBuildsNothing` | unit |
| live-collections | A live collection held by a provider | `collection_test.go: TestCollection_HeldByProvider` | unit |
| live-collections | Reads after shutdown and failures (3 rows) | `collection_test.go: TestCollection_ReadOutcomes/<row>` | table |
| live-collections | Concurrent reads are race-free | `collection_test.go: TestCollection_ConcurrentReads` | race |
| container-start | Dependency-ordered build and start; Child scopes are started after their ancestors; Start on an empty container; Late registration | unchanged: `TestStart_DependencyOrder`, `TestStart_ScopeTraversal`, `TestStart_Empty`, `TestStart_LateRegistrationNotStarted` | unit |
| container-start | Refresh starts late registrations and new scopes once (2 rows) | `root_start_test.go: TestRefresh_StartsNew/<times>` | table |
| container-start | Refresh with nothing new | `root_start_test.go: TestRefresh_NothingNew` | unit |
| container-start | Scope.Start starts only its subtree | `root_start_test.go: TestScopeStart_Subtree` | unit |
| container-start | Scope.Start on the root's own scope | `root_start_test.go: TestScopeStart_RootScope` | unit |
| container-start | Incremental start in the wrong phase (5 rows) | `root_start_test.go: TestIncrementalStart_Phase/<row>` | table |
| container-start | Concurrent incremental starts | `root_start_test.go: TestRefresh_Concurrent` | race |
| container-start | Shutdown cancels a running Refresh | `root_start_test.go: TestRefresh_ShutdownCancels` | synctest / race |
| container-start | Independent Starters start concurrently (5 rows) | `root_start_test.go: TestStart_Parallel/<n>` | table / synctest |
| container-start | A failure stops scheduling | `root_start_test.go: TestStart_ParallelFailure` | synctest |
| container-start | Start hooks under parallel start | `root_start_test.go: TestStart_ParallelHooks` | race |
| container-start | StartError fields per failure point (4 rows) | `root_start_test.go: TestStartError_Fields/<row>` | table |
| container-start | Errors that are not service failures are not StartError (3 rows) | `root_start_test.go: TestStartError_NotForGuards/<row>` | table |
| component-kinds | Clone keeps replaced handlers (2 rows) | `kind_test.go: TestKind_CloneCopiesHandlers/<clone>` | table |
| component-kinds | Replacing a handler on the clone leaves the source unchanged | `kind_test.go: TestKind_CloneReplaceIsolated` | unit |
| component-kinds | Refresh handles only new components | `kind_test.go: TestKind_RefreshHandlesNew` | unit |
| component-kinds | Runners run once | `kind_test.go: TestRunnable_RefreshRunsOnce` | unit |
| registration-options | Each constructor yields an option recognized by registration (new `with_order` row) | `options_test.go: TestProvideOptions_Constructors/with_order` | table |
| dependency-declaration | Source per edge (2 rows) | `dependency_test.go: TestExplain_EdgeSource/<row>` | table |
| dependency-declaration | Dependents report the same source | `dependency_test.go: TestExplain_EdgeSourceDependents` | unit |
| dependency-declaration | Output without declared edges is unchanged | `dependency_test.go: TestExplain_EdgeSourceCompat` | unit |
| health-check-timeouts | Per-service checks are bounded by HealthCheckTimeout (6 rows) | `healthcheck_timeout_test.go: TestHealthCheck_PerServiceTimeout/<row>` | table / synctest |
| health-check-timeouts | A per-service check returning an error in time keeps it | `healthcheck_timeout_test.go: TestHealthCheck_PerServiceErrorInTime` | unit |
| container-lifecycle-hooks | Refresh after Start fires no container hook (2 rows) | `container_hooks_test.go: TestContainerHooks_IncrementalStartFiresNone/<row>` | table |
| container-lifecycle-hooks | A start finishing as Shutdown begins (2 rows, 20000 runs) | `container_hooks_test.go: TestContainerHooks_ShutdownCauseRace/<row>` | stress (regression, V1) |
| container-start | Independent Starters start concurrently (new max-uint row) | `root_start_test.go: TestStart_Parallel/18446744073709551615` | synctest (regression, V13) |
| container-start | Parallel incremental start (2 rows) | `root_start_test.go: TestStart_ParallelIncremental/<operation>` | synctest (V8) |
| container-start | A panicking start hook under parallel start (3 rows) | `root_start_test.go: TestStart_ParallelHookPanic/<row>` | table (regression, V2) |
| container-start | Incremental start while Start runs (3 rows) | `root_refresh_test.go: TestIncrementalStart_DuringStart/<row>` | table (regression, V3) |
| container-start | Scope.Start builds ancestors but does not start them | `root_refresh_test.go: TestScopeStart_AncestorsNotStarted` | unit (V8) |
| container-start | Incremental start failures are StartError values (3 rows) | `root_refresh_test.go: TestStartError_Incremental/<row>` | table (V7) |
| container-start | Child shutdown waits for a running start (3 rows) | `root_refresh_test.go: TestScopeShutdown_WaitsForStart/<row>` | table / race (regression, V4) |
| container-start | A start that begins during a child shutdown skips the scope | `root_refresh_test.go: TestScopeShutdown_LaterStartSkips` | unit (V4) |
| container-start | A scope created during a running start does not wait | `root_refresh_test.go: TestScopeShutdown_CreatedDuringStart` | unit (V4) |
| container-start | A child shutdown bounded by its context | `root_refresh_test.go: TestScopeShutdown_WaitBounded` | synctest (V4) |
| live-collections | Reads after the holder's provider returned (2 rows) | `collection_test.go: TestCollection_AfterProviderReturned/<read>` | table (regression, V5) |
| collection-struct-injection | Invalid collection fields (new string-kinded key row) | `invoke_all_struct_test.go: TestInvokeStruct_CollectionInvalidField/map_with_string-kinded_keys` | table (regression, V6) |
| service-build-hooks | A build hook can use its own service (3 rows) | `build_hooks_test.go: TestAfterBuildHook_ReentersService/<row>` | table (regression, V10) |
| dependency-declaration | Explained output round-trips through JSON (4 rows) | `dependency_test.go: TestDependencySource_UnmarshalText/<text>` | table (regression, V11) |
| type-resolution | A shadowed service is not a candidate (3 rows) | `invoke_test.go: TestInvokeAs_Shadowing/<row>` | table (regression, V14) |

Finding tests without a spec scenario (F2, F4, F5, V12):
- `root_refresh_test.go: TestRootScope_ForgetsShutDownServices` (group 49) and `TestRootScope_ForgetsOverriddenServices` (regression, V12, internal);
- `errors_test.go: TestMergeShutdownReports_Succeed` (regression, internal);
- `bench/do_bench_test.go: BenchmarkNew`, with benchstat allocs/op = 11;
- `root_scope_test.go: TestRootScope_HealthCheckDuringShutdown` (race guard, `-count=2000`).

Documentation tests:
- `ExampleRootScope_Refresh`, `ExampleScope_Start`, `ExampleInjectorOpts_startParallelism`, `ExampleRootScope_AddAfterBuildHook`;
- `ExampleRootScope_Phase`, `ExampleRootScope_AddScopeClosingHook`;
- `ExampleInvokeCollection`, `ExampleInvokeStruct_collection`;
- `ExampleStartError`, `ExampleExplainNamedService_source`.

Hot paths and their coverage:
- **Resolution/invocation:**
  - `TestInvokeAs_*` and `TestInvokeStruct_*` (S5, S3), and `TestAfterBuildHook_*` (P5 is in every wrapper's build path);
  - the Part K tables;
  - the unchanged `invoke_test.go`/`di_test.go` suites, and `bench/` compared with benchstat (`~` for invoke).
- **Lifecycle:**
  - `TestRefresh_*`, `TestScopeStart_*`, `TestIncrementalStart_Phase`, `TestStart_Parallel*`, `TestStartError_*`, `TestPhase_*`, `TestServiceAlias_*`;
  - the Part H tables and the unchanged start-lifecycle suite.
- **Scope traversal:** `TestScopeStart_Subtree`, `TestScopeHooks_ClosingClosed`, `TestInvokeAs_Deterministic` (child and clone rows), `TestAfterBuildHook_Scopes`.
- **Concurrency:** `TestRefresh_Concurrent`, `TestRefresh_ShutdownCancels`, `TestStart_ParallelHooks`, `TestPhase_RaceFree`, `TestCollection_ConcurrentReads`, `TestRootScope_HealthCheckDuringShutdown`, all under `-race` with goleak; plus the Part H/K concurrency tests.

### Part H — Container lifecycle hooks

Files: `container_hooks_test.go` and `container_hooks_example_test.go` (package `doger`). Tests that could hang use `testWithTimeout`. Shutdown/Start interleavings use `testing/synctest` with channel handshakes, so they are deterministic. start-lifecycle's `TestStart_ShutdownCancels`, `TestStart_StarterFailure` and `TestStart_BuildFailure` must stay green unchanged.

| Scenario | Test | Kind |
|----------|------|------|
| Starting hook allows or vetoes Start (4 rows) | `TestContainerStartingHook_Veto/<row>` | table |
| Starting hooks run before the context check (2 rows) | `TestContainerStartingHook_BeforeContextCheck/<behavior>` | table |
| Starting hook receives the Start context | `TestContainerStartingHook_ReceivesContext` | unit |
| Nil context reaches hooks as non-nil (2 rows) | `TestContainerHooks_NilContext/<operation>` | table |
| Hook order relative to the start pipeline | `TestContainerHooks_StartOrder` | unit |
| Started and Ready fire for an empty container | `TestContainerHooks_EmptyContainer` | unit |
| Failure point decides which hooks run (7 rows) | `TestContainerFailedHook_FailurePoint/<row>` | table |
| One-shot guard errors do not fire Failed (3 rows) | `TestContainerFailedHook_GuardErrors/<row>` | table |
| Post-start or Closed hook calls Shutdown (4 rows) | `TestContainerHooks_HookCallsShutdown/<hook>` (`testWithTimeout`) | table |
| Starting or Closing hook calls Shutdown with a deadline (2 rows) | `TestContainerHooks_HookCallsShutdownWithDeadline/<hook>` (`synctest.Test`) | table / synctest |
| Closing and Closed around service shutdown from every state (3 rows) | `TestContainerClosingClosedHooks_Order/<state>` | table |
| Closed hook sees shutdown errors in the report | `TestContainerClosedHook_ReportErrors` | unit |
| Signal-driven shutdown fires the hooks | `TestContainerHooks_ShutdownOnSignalsWithContext` | unit |
| Shutdown races a running Start (3 rows) | `TestContainerHooks_ShutdownDuringStart/<shutdown_point>` (`synctest.Test`) | table / synctest / race |
| Registration order | `TestContainerHooks_RegistrationOrder` | unit |
| Nil hooks are ignored (6 rows) | `TestContainerHooks_NilIgnored/<field>` | table |
| Clone copies hooks but not state (2 rows) | `TestContainerHooks_Clone/<clone_call>` | table |
| Default re-panics after the operation completes (5 rows) | `TestContainerHooks_ObserverPanicDefault/<hook>` | table |
| HookContainerPanic overrides the default (2 rows) | `TestContainerHooks_ObserverPanicOverride/<hook>` | table |
| Panicking Starting hook (2 rows) | `TestContainerStartingHook_Panic/<value>` | table |
| Concurrent Start calls | `TestContainerHooks_ConcurrentStart` (50 goroutines, `testWithTimeout`) | race |
| Repeated and concurrent Shutdown calls (2 rows) | `TestContainerHooks_ConcurrentShutdown/<row>` (`testWithTimeout`) | race |
| Hook calling Start re-entrantly | `TestContainerHooks_ReentrantStart` (`testWithTimeout`) | unit |
| Child scope shutdown fires nothing | `TestContainerHooks_ChildScopeShutdown` | unit |
| No hooks registered leaves behavior unchanged (3 rows) | `TestContainerHooks_NoHooksUnchanged/<constructor>` | table |

**Documentation tests** (`container_hooks_example_test.go`):
- `ExampleRootScope_AddContainerStartingHook`: fail-fast veto.
- `ExampleRootScope_AddContainerReadyHook`: readiness signal.
- `ExampleRootScope_AddContainerFailedHook`: cleanup with `root.Shutdown()`.
- `ExampleInjectorOpts_containerHooks`: all six hooks and the printed order.
- `ExampleInjectorOpts_hookContainerPanic`: the override.

**Hot paths and their coverage:**
- **Lifecycle (Start and Shutdown entry points).** Every extension point and guard is covered by the Veto, BeforeContextCheck, FailurePoint, GuardErrors, ClosingClosedHooks_Order, HookCallsShutdown and ShutdownDuringStart tests. The existing `di_lifecycle_test.go` and `root_scope_test.go` tests, and the start-lifecycle tests above, must stay green unchanged.
- **Concurrency.** `ConcurrentStart`, `ConcurrentShutdown` and `ShutdownDuringStart` run under `-race`, with goleak via `TestMain`. `ReentrantStart` and `HookCallsShutdown` use `testWithTimeout` to catch deadlocks. `HookCallsShutdownWithDeadline` and `ShutdownDuringStart` use `synctest` for deterministic timing.
- **Panic isolation.** `ObserverPanicDefault`, `ObserverPanicOverride` and `StartingHook_Panic` cover all six lists and both policies.

**RED expectation:** each group's tests first fail to compile on that group's missing field or method, then fail on assertions (hooks not called, wrong order, panics escaping). Groups whose tests can pass on arrival say so in tasks.md and name the temporary mutation that shows the assertion can fail.

### Part K — Collection invocation and ordering

| Scenario | Test | Kind |
|----------|------|------|
| InvokeAs hooks receive the requested type name | `invoke_all_test.go: TestInvokeAs_HookReceivesTypeName` | unit (characterization) |
| Membership by registered type (4 rows) | `invoke_all_test.go: TestInvokeAll_Membership/<T>` | table |
| Eager values are members without a provider call | `invoke_all_test.go: TestInvokeAll_EagerMembers` | unit |
| Nil injector uses the default root scope | `invoke_all_test.go: TestInvokeAll_NilInjector` (restores `DefaultRootScope`) | unit |
| Typed-nil member is returned as is | `invoke_all_test.go: TestInvokeAll_TypedNilMember` | unit |
| Scope visibility and shadowing (5 rows) | `invoke_all_test.go: TestInvokeAll_Visibility/<row>` | table |
| Interface type any matches every non-alias service | `invoke_all_test.go: TestInvokeAll_AnyType` | unit |
| Collection inside a provider sees the provider's scope | `invoke_all_test.go: TestInvokeAll_ProviderScopeVisibility` | unit |
| Alias does not duplicate its target | `invoke_all_test.go: TestInvokeAll_AliasExcluded` | unit |
| Empty results (4 rows) | `invoke_all_test.go: TestInvokeAll_Empty/<function>_<services>` | table |
| Member failure modes (3 rows) | `invoke_all_test.go: TestInvokeAll_MemberFailure/<behavior>` | table |
| Must variants panic with the same error (2 rows) | `invoke_all_test.go: TestMustInvokeAll_Panics/<function>` | table |
| Must variants return the collection on success (2 rows) | `invoke_all_test.go: TestMustInvokeAll_Success/<function>` | table |
| Composite collects every other implementation | `invoke_all_test.go: TestInvokeAll_CompositeExcludesSelf` | unit |
| Deeper cycles still fail | `invoke_all_test.go: TestInvokeAll_DeeperCycle` | unit |
| Map collection keys (2 rows) | `invoke_all_test.go: TestInvokeAllMap_Keys/<from>` | table |
| Dependency edges order shutdown | `invoke_all_test.go: TestInvokeAll_EdgesOrderShutdown` | unit |
| Invocation hooks per member | `invoke_all_test.go: TestInvokeAll_HooksPerMember` | unit |
| No edges when invoked outside a provider | `invoke_all_test.go: TestInvokeAll_NoEdgesOutsideProvider` | unit |
| Transient and singleton members across calls | `invoke_all_test.go: TestInvokeAll_TransientFresh` | unit |
| Later registration is not visible in an earlier snapshot | `invoke_all_test.go: TestInvokeAll_Snapshot` | unit |
| Caller mutation does not leak | `invoke_all_test.go: TestInvokeAll_CallerMutation` | unit |
| Collections after shutdown | `invoke_all_test.go: TestInvokeAll_AfterShutdown` | unit |
| Concurrent callers build each member once | `invoke_all_test.go: TestInvokeAll_ConcurrentSingleBuild` (`testWithTimeout`, `-race`) | race |
| Concurrent registration and collection | `invoke_all_test.go: TestInvokeAll_ConcurrentRegistration` (`testWithTimeout`, `-race`) | race |
| Concurrent collection and shutdown | `invoke_all_test.go: TestInvokeAll_ConcurrentShutdown` (`testWithTimeout`, `-race`) | race |
| Default order across scopes (4 rows) | `invoke_all_order_test.go: TestInvokeAll_DefaultOrder/<row>` | table |
| Order is stable across repeated calls | `invoke_all_order_test.go: TestInvokeAll_DefaultOrderStable` | unit |
| Override replaces a registration in place | `invoke_all_order_test.go: TestInvokeAll_OverrideKeepsPosition` | unit |
| Default order in a clone | `invoke_all_order_test.go: TestInvokeAll_DefaultOrderClone` | unit |
| Ordered values (6 rows) | `invoke_all_order_test.go: TestInvokeAll_Ordered/<values>` | table |
| Order is read from each transient instance (2 rows) | `invoke_all_order_test.go: TestInvokeAll_OrderedTransient/call_<n>` | table |
| Precedence (5 rows) | `invoke_all_order_test.go: TestInvokeAll_WithOrderPrecedence/<row>` | table |
| WithOrder on every registration helper (18 rows) | `invoke_all_order_test.go: TestInvokeAll_WithOrderHelpers/<helper>` | table |
| Order() is not called when WithOrder is set | `invoke_all_order_test.go: TestInvokeAll_WithOrderSkipsOrderMethod` | unit |
| WithOrder outside collections has no effect | `invoke_all_order_test.go: TestWithOrder_NoEffectOnInvoke` | unit |
| Panicking Order() (3 rows) | `invoke_all_order_test.go: TestInvokeAll_OrderPanic/<panic>` | table |
| MustInvokeAll surfaces the Order() panic as its panic | `invoke_all_order_test.go: TestMustInvokeAll_OrderPanic` | unit |
| Shutdown ignores collection order | `invoke_all_order_test.go: TestInvokeAll_OrderDoesNotAffectShutdown` | unit |
| Start ignores collection order | `invoke_all_order_test.go: TestInvokeAll_OrderDoesNotAffectStart` | unit |
| Order() not called for map collections | `invoke_all_order_test.go: TestInvokeAllMap_IgnoresOrdered` | unit |

**Documentation tests** (`di_example_test.go`): `ExampleInvokeAll`, `ExampleInvokeAllMap`, `ExampleOrdered`, `ExampleWithOrder`. Each documents a default (empty non-nil result, registration order, order 0, self-exclusion) and the matching override.

**Hot paths and their coverage:**
- **Resolution/invocation** (shared per-member helper from D3): `TestInvokeAll_*`, plus `TestInvokeAs_HookReceivesTypeName` and the unchanged `TestInvokeAs`, `TestMustInvokeAs`, `TestInvokeByGenericType` and `TestInvokeByGenericType_race`. Together these prove the extraction did not change `invokeByGenericType`.
- **Scope traversal** (D1, D2): `TestInvokeAll_Visibility`, `TestInvokeAll_ProviderScopeVisibility`, `TestInvokeAll_DefaultOrder`, `TestInvokeAllMap_Keys`.
- **Cycles and self-exclusion** (D3a): `TestInvokeAll_CompositeExcludesSelf`, `TestInvokeAll_DeeperCycle`.
- **Concurrency:** `TestInvokeAll_ConcurrentSingleBuild`, `TestInvokeAll_ConcurrentRegistration` and `TestInvokeAll_ConcurrentShutdown` under `-race`, with goleak via `TestMain`.
- **Lifecycle interaction:** `TestInvokeAll_EdgesOrderShutdown`, `TestInvokeAll_OrderDoesNotAffect{Shutdown,Start}`, `TestInvokeAll_AfterShutdown`.
- **Performance:** `bench/do_bench_test.go: BenchmarkInvokeAll/{10,100,1000}` (visible services, half matching) and `BenchmarkInvokeAllParallel`, recorded with `-benchmem`. Not a gate, but the baseline for future changes.

## Migration Plan

1. Prerequisite: PR #4 merged, then `/opsx:archive add-start-lifecycle-and-kinds`, so the MODIFIED deltas resolve.
2. Implement in the X1 order. Each group goes RED → GREEN → REFACTOR and gets a conventional commit.
3. The upgrading page collects every flagged change (X4).
4. Rollback: revert the commit series. No persisted state.
