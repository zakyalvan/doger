# Proposal

## Why

All existing hooks fire per service (registration, invocation, shutdown, and per-service start from `add-start-lifecycle-and-kinds`). Nothing fires once for the whole container when it changes state. Consumers therefore have no single place for:
- container-wide work: fail-fast config validation before start, "ready" signals to orchestrators, metrics and logging of boot and shutdown
- reacting to a failed start

Common DI practice covers this with container lifecycle events such as "refreshed" and "ready". Here, "refresh" is a **lifecycle point**, not an operation: the container starts once, and there is no runtime rebuild.

## What Changes

- Fire container hooks on the lifecycle phases that `add-start-lifecycle-and-kinds` (start-lifecycle D6) defines for `RootScope`: a start phase (idle → starting → started | failed) and a close phase (open → closing → closed). This change adds a `ready` start phase after `started`.
- Add container-level hooks to `InjectorOpts`, as named fields with typed payloads, mirroring the existing `Hook*` style. The `Add*Hook` methods live on `RootScope`.
  - `HookContainerStarting`: `func(ctx, *RootScope) error`. Runs after the one-shot guard and before the start pipeline's first abort check (start-lifecycle D7 step 1). A non-nil error or a panic **aborts `Start`**, and `Start` returns it wrapped. A Starting hook must not call `Shutdown` synchronously, because that call waits for the running Start.
  - `HookContainerStarted`: `func(ctx, *RootScope)`. Runs after the start pipeline has succeeded, so Starters are up and post-start handlers such as runnables have completed. This is the "refreshed" point in common DI practice.
  - `HookContainerReady`: `func(ctx, *RootScope)`. Runs after every Started hook has returned. This is the point at which to signal readiness to an orchestrator.
  - `HookContainerFailed`: `func(ctx, *RootScope, error)`. Runs when `Start` fails, before the error is returned to the caller. That includes a `Shutdown` that began before the pipeline finished. It only observes, and it may run cleanup such as `root.Shutdown()`.
  - `HookContainerClosing`: `func(ctx, *RootScope)`. Runs when root `Shutdown` begins, after any in-flight Start has returned or the shutdown context ended, and before services are shut down. It cannot veto, and it must not call `Shutdown` synchronously.
  - `HookContainerClosed`: `func(ctx, *RootScope, *ShutdownReport)`. Runs after root `Shutdown` has shut every service down and released concurrent `Shutdown` callers.
- Started, Ready, Failed and Closed hooks run after the running Start or Shutdown has been released (start-lifecycle D6). They may therefore call `root.Shutdown()` without deadlocking. A concurrent `Shutdown` can fire Closing and Closed before or while Started, Ready or Failed run.
- Add `InjectorOpts.HookContainerPanic func(scope *RootScope, hook string, recovered any)`. A panic in a Started, Ready, Failed, Closing or Closed hook is recovered, so `Start`/`Shutdown` always complete their transitions. The first recovered panic is then passed to `HookContainerPanic` on the caller's goroutine. When `HookContainerPanic` is nil, the default re-panics with that value once the operation has completed. That is loud by default and consistent with per-service hooks, which propagate panics.
- Hooks fire once per root container, not per child scope. They run synchronously, in registration order, on the goroutine that called `Start` or `Shutdown`.
- Container hooks run on the existing `ShutdownOnSignals` path as well, because it ends in root `Shutdown`.
- `RootScope.Clone` copies container hooks. `CloneWithOpts(opts)` uses only the hooks in `opts`, as it already does for every other hook.
- No exported identifiers are removed or changed. This change is purely additive.

## Capabilities

### New Capabilities

- `container-lifecycle-hooks`: container-level state transitions and the global hooks fired on them, including veto semantics, ordering, panic handling, shutdown during start, and the one-shot start contract as seen by hooks.

### Modified Capabilities

None. `container-start` is introduced by `add-start-lifecycle-and-kinds`; this change adds requirements in its own capability rather than modifying that one.

## Impact

- `injector.go` (new `InjectorOpts` fields and `copy()`), `root_scope.go` (`Add*Hook` methods; firing points in `Start` and `Shutdown`), docs lifecycle page, and `Example*` tests.
- Depends on `add-start-lifecycle-and-kinds`: start and close phases, `startDone`/`closeDone`, cancellation of an in-flight Start, and first-report semantics for concurrent `Shutdown` callers (start-lifecycle D6).
- Phase 2, out of scope: per-scope container hooks for child scopes, and a re-runnable `Refresh` operation.

## Defaults & Control

| Behavior | Zero-config default | Override / opt-out |
|---|---|---|
| Container hooks | None registered, so no behavior change | Register via `InjectorOpts` fields or `RootScope.AddContainer<State>Hook` |
| Veto on start | Only `HookContainerStarting` can abort `Start` | Return `nil` to allow; other hooks only observe, so shutdown can never be blocked |
| Starting-hook panic | Converted to a veto error (wrapped with `%w` when the value is an error) | Recover inside the hook to allow `Start` |
| Panic in any other container hook | Recovered; the operation completes; then the first panic is re-raised on the caller's goroutine | `InjectorOpts.HookContainerPanic`, e.g. to log instead of panicking |
| Calling `Shutdown` from a hook | Safe from Started, Ready, Failed and Closed hooks | Starting and Closing hooks must not call it synchronously; `ShutdownWithContext` with a deadline returns when the deadline expires |
| Clone | `Clone` copies container hooks; `CloneWithOpts(opts)` uses `opts` only | Pass the hooks you want in `opts` |
| Scope granularity | Once per root container | Per-scope hooks deferred to phase 2 |
