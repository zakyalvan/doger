# Design

## Context

See proposal.md (Why, Defect). The current code:

- **The skip.** The lazy and eager wrappers (`service_lazy.go` and `service_eager.go`, `shutdown(ctx)`) check `ctx.Err()` before calling each of the four Shutdowner signatures and return it instead. Their deferred reset drops the instance whatever happens, and `Scope.serviceShutdown` has already removed the service from its scope and the DAG. A built service implementing none of the four signatures matches no case of the type switch, so it returns nil: it is dropped without an error. A transient service has no instance; aliases and exports never shut their target down and return nil too. `Scope.serviceShutdown` runs the `HookBeforeShutdown` and `HookAfterShutdown` hooks around the wrapper call whatever it does, so they still run for a skipped service, the latter with `ctx.Err()` (confirmed during review: before=[s], after=[s] with context.Canceled, Shutdown called 0 times). A scope that a child exports services into shuts down through `Scope.shutdownWithExports`, which calls the same wrappers, so the exported services are skipped the same way. samber/do v2.1.0 has the same guards.
- **Where the context ends.** It ends before the call (a cancelled signal context), during the shutdown (a deadline used up by slow services), or while a child scope shutdown waits for a running start (`Scope.waitForStartingRun`, which then lets the shutdown "proceed" with the ended context).
- **The signal helper.** `RootScope.ShutdownOnSignalsWithContext` (`root_scope.go`) selects on a signal or `ctx.Done()`, then calls `s.ShutdownWithContext(ctx)` in both branches. In the `ctx.Done()` branch that context has always ended. In the signal branch it can end at any moment: a `signal.NotifyContext` context for the same signals is cancelled right after the helper wakes, and when both select cases are ready Go picks one at random (proposal Defect 2). `ShutdownOnSignals` passes `context.Background()`, so its shutdown is unbounded.
- **Tests and docs.** `TestRootScope_ShutdownOnSignals*` are empty placeholders and `ExampleRootScope_ShutdownOnSignals*` are commented out, so the helper's context path has never run under test. `ExampleScope_ShutdownWithContext_timeout` already shows the skip in its output. The Shutdowner docs page does not mention it, and its example does not compile.

## Goals / Non-Goals

**Goals:**
- The skip is specified, tested and documented where consumers look: the godoc of the shutdown methods and the Shutdowner page.
- The signal helper shuts the container down, whether a signal arrives or its context ends first.

**Non-Goals:**
- Changing the skip. Calling context-aware shutdowners after the deadline, a policy option and an `ErrShutdownSkipped` sentinel were considered and rejected (proposal Why): they would not help `Shutdown() error` services, the most common kind, and they diverge from samber/do.
- Changing the per-service shutdown hooks for a skipped service: they keep running (inherited); the docs say so.
- Keeping a skipped service registered so a later shutdown can retry it: root shutdown runs once (`container-start`), so a retry could not happen through the root anyway.
- Testing the signal branch on non-unix platforms: the test sends `SIGUSR1` with `syscall.Kill`, which only unix has. CI runs on ubuntu.

## Decisions

### D1. Characterize the skip, change nothing
- The skip gets two scenarios (`shutdown-context`) and characterization tests. They are expected to pass on arrival, so each RED task shows them failing under a temporary mutation (removing the `ctx.Err()` guard of the lazy, then the eager wrapper).
- The `container-start` requirement "A child shutdown wait is bounded by its context" is MODIFIED to say what "proceed" means, and its existing test `TestScopeShutdown_WaitBounded` gains the two assertions of the modified scenario (the skipped service's error, its Shutdown not called).
- *Alternative:* `skip_specs` and docs only. Rejected: the docs would make a behavioral claim that no test pins, against the project rule that claims are proven by tests.

### D2. The signal helper shuts down with `context.WithoutCancel(ctx)` in both branches
- `ShutdownOnSignalsWithContext` calls `s.ShutdownWithContext(context.WithoutCancel(ctx))` both after a signal and when `ctx` ends first. `ctx` then only ends the wait for a signal, as the requirement's name says.
- Root shutdown hands its context to the `HookContainerClosing` and `HookContainerClosed` hooks (container-lifecycle-hooks "Hook contexts": the context passed to `Shutdown`). They therefore receive `context.WithoutCancel(ctx)`: its values are kept, `Err()` is nil, and it has no deadline. Before, they received `ctx`, ended or about to end. This is part of the flagged behavior change. The "Hook contexts" requirement still holds as written, because the helper is the caller of `ShutdownWithContext`.
- `WithoutCancel` keeps the context's values (request IDs, loggers, tracing spans that shutdown hooks and context-aware shutdowners read) and drops its cancellation and deadline. The shutdown is then unbounded, exactly as with `ShutdownOnSignals`.
- *Alternatives:*
  - `context.Background()`: also shuts everything down, but loses the values, which `ShutdownOnSignalsWithContext` callers may rely on in hooks.
  - Keeping `ctx` in the signal branch (the first version of this decision): rejected after review. A `signal.NotifyContext` context for the same signals, or a deadline expiring just after the signal, ends during the shutdown, so the helper skipped the services in 46-50 of 50 runs (proposal Defect 2). Its only benefit, bounding the shutdown by the wait's deadline, is better served by the consumer's own deadline.
  - A new deadline parameter or option for the shutdown: new API for a case the consumer can already control by waiting for the signal itself and calling `ShutdownWithContext` with its own deadline. Documented instead.
  - Leaving the helper as is and documenting it: the helper would remain a trap the library sets itself.

### D3. Documentation
- **Godoc:**
  - `Scope.ShutdownWithContext` and `RootScope.ShutdownWithContext` get one paragraph: once ctx has ended, the services the shutdown then reaches are not shut down (their Shutdown method is not called, whatever its signature); a built service implementing a Shutdowner interface has ctx's error under its name in the report, any other service (no Shutdown method, never built, an alias, a transient service, an export) gets no error; all are dropped; the `HookBeforeShutdown` and `HookAfterShutdown` hooks still run for each, the latter with ctx's error. The wording "the shutdown proceeds" after a start wait is completed accordingly.
  - `ShutdownOnSignalsWithContext` says that ctx only ends the wait for a signal: whichever comes first, the call shuts down, and runs the Closing and Closed hooks, with ctx's values and no cancellation or deadline; when ctx ends first it returns a nil signal. The godoc must not say the shutdown uses ctx after a signal.
- **Shutdowner page** (`docs/docs/service-lifecycle/shutdowner.md`):
  - a "Shutdown with a deadline" section stating the skip (which services get an error, and that the per-service shutdown hooks still run), warning against passing a `signal.NotifyContext` context (already cancelled once the signal arrived), and showing the pattern: wait for the signal, then `context.WithTimeout(context.Background(), grace)`;
  - a short Defaults & control table;
  - the existing example fixed (`context.WithTimeout(context.Background(), 10*time.Second)`, a report, not `errors`/`err`).
- **Upgrading page:** a new `docs/docs/upgrading/shutdown-context.md`, laid out like `scope-export.md` (title "Shutdown context release", `sidebar_position: 4`, a "Behavior change" paragraph and a "Migration:" line). It flags the helper's behavior change for services and for the Closing and Closed hooks, in both branches: when ctx ends first, services are now shut down; after a signal, the shutdown is no longer bounded or cancelled by ctx, so a `Shutdown` that blocks now blocks the helper. The migration: "none, unless code relied on the helper shutting nothing down when its context ended, on ctx's deadline bounding the shutdown after a signal, or on a Closing or Closed hook seeing that context end".
- **Examples:** `ExampleRootScope_ShutdownOnSignalsWithContext` becomes a real example: a context with a 10ms timeout, a service printing on Shutdown, the nil signal and a succeeded report in `// Output:`.

## Risks / Trade-offs

- [The helper's shutdown has no deadline, in either branch; a `Shutdown` that blocks now blocks the helper, where before it returned once ctx ended (having skipped the remaining services)] → the same as `ShutdownOnSignals` today; a consumer needing a bound waits for the signal itself and calls `ShutdownWithContext` with a deadline, as the Shutdowner page shows; flagged on the upgrading page.
- [A caller relied on the helper shutting nothing down when its context ended, or on ctx's deadline bounding the shutdown after a signal] → unlikely (the report was all errors, and the bound skipped services silently); flagged on the upgrading page.
- [`ExampleRootScope_ShutdownOnSignalsWithContext` registers a signal handler in the test process] → it registers SIGTERM and os.Interrupt only for the call and stops them before returning (the helper calls `signal.Stop`); no signal is sent.
- [The signal-branch test sends `SIGUSR1` to the test process; an unhandled `SIGUSR1` kills the process] → the test registers its own guard `signal.Notify` for `SIGUSR1` before sending and stops it at the end, so the signal is always handled; it resends until the helper returns, so it does not depend on when the helper registers; no other test uses `SIGUSR1`, and the test does not run in parallel with itself (one `t.Run`-free test, 50 sequential repeats).

## Public API & Defaults

No exported identifier is added or changed; nothing is BREAKING.

| Identifier | Change | Default / Override |
|---|---|---|
| `RootScope.ShutdownOnSignalsWithContext` | Behavior (flagged): after a signal and when `ctx` ends first, it shuts down with `context.WithoutCancel(ctx)` instead of `ctx`, and that context also reaches the Closing and Closed hooks | Unbounded shutdown with ctx's values; to bound it, wait for the signal yourself and call `ShutdownWithContext` with a deadline |
| `Scope.ShutdownWithContext`, `RootScope.ShutdownWithContext` | Godoc only | Services reached after ctx ended are skipped and dropped; Shutdowners are reported with ctx's error; the per-service shutdown hooks still run; give the shutdown a live context with enough time |

## Test Strategy

Tests are table-driven with named subtests and testify, run under `-race` with goleak (`TestMain`), and use `testWithTimeout` or a synctest bubble where a test could hang.

| Scenario | Test | Kind |
|---|---|---|
| A service reached after the context ended is skipped | scope_test.go: `TestScopeShutdown_AfterContextEnded/{lazy_cancelled,lazy_context_aware_cancelled,value_cancelled,lazy_deadline_expired,lazy_never_built,lazy_no_shutdowner,alias_cancelled,transient_cancelled,lazy_background}`, each row also asserting the names the Before/After shutdown hooks saw and the After hook's error | unit, table |
| An exported service reached after the context ended is skipped | root_scope_test.go: `TestRootScope_ShutdownExportedAfterContextEnded` | unit |
| A deadline that ends during the shutdown skips the services after it | root_scope_test.go: `TestRootScope_ShutdownDeadlineDuringShutdown` | unit, synctest |
| The signal helper shuts down when its context ends | root_scope_test.go: `TestRootScope_ShutdownOnSignalsWithContext/{cancelled_before,timeout_while_waiting}` (replaces the empty placeholder), each row asserting the service's and the Closing and Closed hooks' context; container_hooks_test.go: existing `TestContainerHooks_ShutdownOnSignalsWithContext`, which also asserts the hooks' context. Context values use the existing `ctxTestKey` (main_test.go) | unit, table, timeout |
| The signal helper shuts down after a signal even when its context ends with it | root_scope_unix_test.go (`//go:build unix`): `TestRootScope_ShutdownOnSignalsWithContext_Signal`, 50 sequential repeats with a `signal.NotifyContext` context for `SIGUSR1` | unit, signal, timeout |
| A child shutdown bounded by its context | root_refresh_test.go: existing `TestScopeShutdown_WaitBounded`, with the modified scenario's two new assertions | unit, synctest |

Examples (docs): `ExampleRootScope_ShutdownOnSignalsWithContext` becomes executable with `// Output:`; the existing `ExampleScope_ShutdownWithContext_timeout` already shows a skipped service's report line.

**Hot paths and their coverage:**
- The skip in both wrappers (lazy and eager, context-unaware and context-aware): the `TestScopeShutdown_AfterContextEnded` rows with a call count of 0 for a built service, each shown to fail with the guard of its wrapper removed. The `lazy_never_built`, `lazy_no_shutdowner` and `lazy_background` rows pin the boundaries; no guard mutation can make them fail. The `alias_cancelled` and `transient_cancelled` rows pin that those wrappers report no error (and that an alias never shuts its target down), each shown to fail with its wrapper's `shutdown` returning `ctx.Err()`.
- The per-service shutdown hooks around a skipped service (`Scope.serviceShutdown`): the hook assertions of `TestScopeShutdown_AfterContextEnded`, shown to fail with the `onBeforeShutdown`/`onAfterShutdown` calls of `Scope.serviceShutdown` temporarily removed.
- The skip after a deadline that ends mid-shutdown: `TestRootScope_ShutdownDeadlineDuringShutdown`.
- The skip on the export shutdown path (`Scope.shutdownWithExports`): `TestRootScope_ShutdownExportedAfterContextEnded`.
- The helper's `ctx.Done()` branch: both rows of `TestRootScope_ShutdownOnSignalsWithContext` (service and hook contexts), the hooks' context in `TestContainerHooks_ShutdownOnSignalsWithContext`, and the example.
- The helper's signal branch: `TestRootScope_ShutdownOnSignalsWithContext_Signal` (unix), the regression test of proposal Defect 2.
- The existing shutdown suite, including `TestRootScope_ShutdownWithEndedContext`, `TestScope_ContextExpiration_ParallelOperations` and the container-start "Shutdown cancels a running Start" rows, passes unchanged.

## Migration Plan

One flagged behavior change, in `ShutdownOnSignalsWithContext` (both branches); no consumer code change is needed. The upgrading page describes it. Rollback is a revert.
