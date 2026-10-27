# Proposal

## Why

Once the context of a shutdown has ended, the remaining services' `Shutdown` is skipped and their instance dropped. This is inherited from samber/do, and it is documented nowhere. Two consequences reach real applications:

- **The library's own signal helper falls into it.** When the context passed to `ShutdownOnSignalsWithContext` ends before a signal arrives, typically an app or errgroup context ended by another component, the helper shuts down with that ended context, so it shuts nothing down.
- **Users fall into it too.** A common pattern passes the `signal.NotifyContext` context, already cancelled once the signal arrived, to `ShutdownWithContext`. The godoc also says that after a timed-out start wait "the shutdown proceeds", which reads as if services still shut down.

This came up while verifying `add-scope-export`. A richer policy (calling context-aware shutdowners after the deadline, an option and a sentinel) was considered and rejected as not worth its API surface: it would not help the most common `Shutdown() error` services, and it diverges from samber/do. Documenting the behavior and fixing the helper removes the traps at no API cost.

## What Changes

- **Fix:** `ShutdownOnSignalsWithContext` shuts down with a context that keeps the values of its `ctx` but not its cancellation when `ctx` ends before a signal arrives, so every service is shut down, as with `ShutdownOnSignals`. The `HookContainerClosing` and `HookContainerClosed` hooks receive that same context, so its `Err()` is nil and it has no deadline. BEHAVIOR CHANGE (flagged): until now it shut nothing down in that case, returned a report of context errors, and gave the hooks the ended context.
- **Specify and test the existing behavior:** a shutdown that reaches a service after its context ended skips that service's `Shutdown`, reports the context error under the service's name, and drops the service. Nothing changes in code; the behavior gets a spec, characterization tests and an executable example.
- **Docs:** the godoc of `Scope.ShutdownWithContext` and `RootScope.ShutdownWithContext` says what happens after the context ended, including after a timed-out start wait; the godoc of `ShutdownOnSignalsWithContext` says how it shuts down; the Shutdowner page gets a "Shutdown with a deadline" section warning against passing the signal context, with the right pattern, and its broken example is fixed; an upgrading page flags the helper's behavior change.
- **Not BREAKING:** no exported identifier changes.

## Capabilities

### New Capabilities
- `shutdown-context`: how a shutdown honors its context: services reached after it ended are skipped and reported, and the signal helper's context only ends the wait for a signal.

### Modified Capabilities
- `container-start`: MODIFIED "A child shutdown wait is bounded by its context": the shutdown proceeds with the ended context, so the services it then reaches are skipped as `shutdown-context` specifies.

## Impact

- Code: `root_scope.go` (`ShutdownOnSignalsWithContext`, one line). Tests: `root_scope_test.go` (the empty `TestRootScope_ShutdownOnSignalsWithContext` placeholder becomes real tests), `scope_test.go`, `root_refresh_test.go`, `container_hooks_test.go`; an example in `di_lifecycle_example_test.go`.
- Docs: godoc of `Scope.ShutdownWithContext`, `RootScope.ShutdownWithContext` and `RootScope.ShutdownOnSignalsWithContext`; `docs/docs/service-lifecycle/shutdowner.md`; a new upgrading page.
- No dependency or module path change.

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| A service reached after the shutdown context ended | Its `Shutdown` is skipped; the report has the context error under its name; the service is dropped | Pass a context that is still live with enough time, for example `context.WithTimeout(context.Background(), grace)` created after the signal; a context-aware shutdowner bounds its own work with the context it receives |
| `ShutdownOnSignalsWithContext` when `ctx` ends before a signal | Shuts every service down, with `ctx`'s values and no cancellation or deadline, as `ShutdownOnSignals` does; the Closing and Closed hooks receive that context | To bound that shutdown, wait for the signal or `ctx` yourself (`signal.NotifyContext`) and call `ShutdownWithContext` with your own deadline |

## Defect

- **Observed:** with a built lazy service implementing `Shutdown() error` and a context cancelled before any signal, `root.ShutdownOnSignalsWithContext(ctx)` returns a nil signal and a failed report whose only error is `context canceled` for that service, and its `Shutdown` was called 0 times (probe run during `add-scope-export` verification, not committed).
- **Expected:** a nil signal, a succeeded report, and `Shutdown` called once.
- **Re-checked:** the reproduction gives the observed result on main at `357f47d`.
- **Reproduction:** `root := New()`; register and invoke a lazy `*countedShutdowner`; `ctx, cancel := context.WithCancel(context.Background()); cancel()`; `sig, report := root.ShutdownOnSignalsWithContext(ctx)`. The fix starts with this as a failing regression test.
