# Design

## Context

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

## Goals / Non-Goals

**Goals:**
- Six container hook lists with typed payloads, registered via `InjectorOpts` or `RootScope.AddContainer<State>Hook`.
- One panic policy, `HookContainerPanic`, that is loud by default and overridable.
- Hooks placed only at start-lifecycle's extension points: at most once per container, no data race, no deadlock when post-start or Closed hooks call `Shutdown`.

**Non-Goals:**
- Exposing the lifecycle phases publicly (no `State()` accessor in this change).
- Per-scope container hooks, or firing hooks from child `Scope` or `virtualScope` shutdown (phase 2).
- A `Refresh` operation, or restarting a failed container.
- Redefining phases, guards, cancellation, waiter semantics or report copying. Those are start-lifecycle D6.

## Decisions

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

## Risks / Trade-offs

- **[A hook registers more hooks during a run]** → Each list is copied into a local slice before iterating. Additions to the list that is currently running take effect only on its next run, which never comes because transitions happen once. Additions to a later list (e.g. a Started hook adding a Ready hook) do fire. Documented on the `Add*` methods.
- **[`Add*Hook` racing with `Start`/`Shutdown` on another goroutine]** → Same as today's `Add*Hook`: registration is configuration-time only. Documented, and not made concurrent-safe, for consistency with the existing hooks.
- **[Closing/Closed run concurrently with Started/Ready/Failed (D3)]** → Hooks that share state across these points must synchronize it. Documented on the fields and on the docs page.
- **[Concurrent `Shutdown` waiters may return before the Closed hooks have run]** → They are released by `finishClose`, which precedes `pointClosed` (start-lifecycle D6). Documented on `HookContainerClosed`.
- **[The default re-panic from a Closing/Closed hook means the caller never sees the report]** → Accepted as the loud default. `HookContainerPanic` restores normal returns.
- **[The start-lifecycle `finishStart` branch (see D2) is a narrow race window that no test can hit deterministically]** → Its observable rule ("Shutdown before the pipeline returns → Failed with `ErrStartAfterShutdown`") is pinned through the D7 abort checks by "Shutdown races a running Start". The branch keeps the rule true inside that window too.
- **[A slow or blocking Closing/Closed hook delays process exit]** → Accepted. Hooks are synchronous by design and receive the shutdown context so they can honor deadlines.
- **[Root shutdown through `virtualScope` or the root's `*Scope` skips the hooks]** → Documented. Only `RootScope` methods are container-level.

## Public API & Defaults

Added, non-breaking:
- `InjectorOpts` fields: `HookContainerStarting`, `HookContainerStarted`, `HookContainerReady`, `HookContainerFailed`, `HookContainerClosing`, `HookContainerClosed`, `HookContainerPanic` (signatures in D4).
- `RootScope` methods: `AddContainerStartingHook`, `AddContainerStartedHook`, `AddContainerReadyHook`, `AddContainerFailedHook`, `AddContainerClosingHook`, `AddContainerClosedHook`.

Defaults:
- No hooks are registered, so `Start` and `Shutdown` behave exactly as start-lifecycle defines.
- Only Starting can veto. A Starting-hook panic is a veto.
- An observer-hook panic is recovered, the operation completes, and then the first panic is re-raised on the caller's goroutine.
- Container hooks fire at most once per container.
- `Clone` copies container hooks and `HookContainerPanic`; `CloneWithOpts(opts)` uses `opts`.

Override:
- Consumers register hooks.
- Set `HookContainerPanic` to log or count observer panics instead of re-panicking.
- To allow `Start` despite a failing check, return nil from, or recover inside, the Starting hook.

Existing behavior change: none beyond start-lifecycle's own. No breaking change.

## Test Strategy

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

## Migration Plan

Additive only. No migration needed. Rollback is a revert.

## Open Questions

None. Choices that could have changed the specs are recorded in Decisions D1–D9.
