# Tasks

Prerequisite: `add-start-lifecycle-and-kinds` has merged with its final D6/D7: `lifecycleMu`, start phase `startIdle → startStarting → startStarted | startFailed`, close phase `closeOpen → closeClosing → closeClosed`, `startCancel`/`startDone`/`closeDone`/`closeReport`, `beginStart`/`finishStart`/`beginClose`/`finishClose`, and the empty extension points `pointStarting`, `pointAfterStart`, `pointClosing`, `pointClosed` (`pointStartersUp` stays unused). Tests live in `container_hooks_test.go` and `container_hooks_example_test.go` (package `doger`). Load `golang-how-to`, then `golang-testing`, `golang-stretchr-testify`, `golang-concurrency`, `golang-error-handling` and `golang-refactoring`. Groups 1–7 must not extend `InjectorOpts.copy()` or add nil-skipping; group 8 owns both, so its RED tests fail for the right reason. The docs page is `docs/docs/service-lifecycle/container-hooks.md`.

## 1. Starting hooks run first and can abort Start

- [ ] 1.1 RED: add `TestContainerStartingHook_Veto` (scenario "Starting hook allows or vetoes Start", 4 rows), `TestContainerStartingHook_BeforeContextCheck` (scenario "Starting hooks run before the context check", 2 rows) and `TestContainerStartingHook_ReceivesContext` (scenario "Starting hook receives the Start context"); verify `go test -race -run 'TestContainerStartingHook_(Veto|BeforeContextCheck|ReceivesContext)' .` fails to compile only on the missing `HookContainerStarting` field and `AddContainerStartingHook` method
- [ ] 1.2 GREEN: add `InjectorOpts.HookContainerStarting`, its nil-to-empty init in `NewWithOpts` and `AddContainerStartingHook`, and run the hooks with `startCtx` at `pointStarting` (before D7's first abort check), returning a veto as `DI: container start aborted: %w` (design D4, D5); verify the 1.1 command passes and `make test` is green
- [ ] 1.3 REFACTOR: *Extract Function* `runStartingHooks(ctx) error` out of `StartWithContext`; verify `make test` and `make lint` pass
- [ ] 1.4 Add `ExampleRootScope_AddContainerStartingHook` (fail-fast config validation that vetoes `Start`) and godoc on the field and method stating firing point, veto and the "must not call Shutdown synchronously" rule (D8); verify `go test -run ExampleRootScope_AddContainerStartingHook .` passes
- [ ] 1.5 Create `docs/docs/service-lifecycle/container-hooks.md` with the phase diagram (start-lifecycle D6 plus `ready`) and the Starting section; verify `npm --prefix docs run build` succeeds

## 2. Started and Ready hooks mark a successful start

- [ ] 2.1 RED: add `TestContainerHooks_StartOrder` (scenario "Hook order relative to the start pipeline") and `TestContainerHooks_EmptyContainer` (scenario "Started and Ready fire for an empty container"); verify `go test -race -run 'TestContainerHooks_(StartOrder|EmptyContainer)' .` fails to compile only on the missing Started/Ready/Failed fields and adders
- [ ] 2.2 GREEN: add `HookContainerStarted`, `HookContainerReady` and `HookContainerFailed` (field only) with init and adders; at `pointAfterStart` on `startStarted` fire Started, CAS `startStarted → startReady`, then fire Ready (design D1, D5, D7); verify the 2.1 command passes and `make test` is green
- [ ] 2.3 REFACTOR: *Extract Function* `runObserverHooks(ctx, point, hooks)` shared by Started and Ready; verify `make test` and `make lint` pass
- [ ] 2.4 Add `ExampleRootScope_AddContainerReadyHook` (signals readiness after runners complete) and godoc on the Started/Ready fields and adders (D7 meaning); verify `go test -run ExampleRootScope_AddContainerReadyHook .` passes
- [ ] 2.5 Add the Started/Ready section to the docs page, including "Started fires after runnables" (D7); verify `npm --prefix docs run build` succeeds

## 3. Failed hooks observe a failed start

- [ ] 3.1 RED: add `TestContainerFailedHook_FailurePoint` (scenario "Failure point decides which hooks run", 7 rows) and `TestContainerFailedHook_GuardErrors` (scenario "One-shot guard errors do not fire Failed", 3 rows); verify `go test -race -run 'TestContainerFailedHook_' .` fails on assertions (Failed hook not called); GuardErrors may pass on arrival via the start-lifecycle guard, so record that and show it can fail by temporarily calling Failed hooks before the guard (not committed)
- [ ] 3.2 GREEN: at `pointAfterStart` on `startFailed`, fire Failed hooks with the exact error `Start` returns, then return it (D5); verify the 3.1 command passes and `make test` is green
- [ ] 3.3 REFACTOR: *Consolidate Duplicate Conditional Fragments* so every exit of `StartWithContext` goes through one `pointAfterStart` dispatch of Started/Ready or Failed; verify `make test` and `make lint` pass
- [ ] 3.4 Add `ExampleRootScope_AddContainerFailedHook` (logs the error and calls `root.Shutdown()`) and godoc on the Failed field and adder; verify `go test -run ExampleRootScope_AddContainerFailedHook .` passes

## 4. Closing and Closed hooks bracket the root shutdown

- [ ] 4.1 RED: add `TestContainerClosingClosedHooks_Order` (scenario "Closing and Closed around service shutdown from every state", 3 rows), `TestContainerClosedHook_ReportErrors` (scenario "Closed hook sees shutdown errors in the report") and `TestContainerHooks_ShutdownOnSignalsWithContext` (scenario "Signal-driven shutdown fires the hooks"); verify `go test -race -run 'TestContainer(ClosingClosedHooks_Order|ClosedHook_ReportErrors|Hooks_ShutdownOnSignalsWithContext)' .` fails to compile only on the missing Closing/Closed fields and adders
- [ ] 4.2 GREEN: add `HookContainerClosing`/`HookContainerClosed` with init and adders; fire Closing at `pointClosing` (winner only, after the in-flight Start wait) and Closed at `pointClosed` (after `finishClose`) with the winner's report (design D3, D5; start-lifecycle D6); verify the 4.1 command passes, `make test` is green and the existing `root_scope_test.go` shutdown tests pass unchanged
- [ ] 4.3 REFACTOR: *Extract Function* `closeContainer(ctx) *ShutdownReport` if `ShutdownWithContext` exceeds ~25 lines, otherwise none needed; verify `make test` and `make lint` pass
- [ ] 4.4 Add `ExampleInjectorOpts_containerHooks` (all six hooks, printed order for a start and a shutdown) and the Closing/Closed docs section; verify `go test -run ExampleInjectorOpts_containerHooks .` passes and `npm --prefix docs run build` succeeds

## 5. Calling Shutdown from hooks

- [ ] 5.1 RED: add `TestContainerHooks_HookCallsShutdown` (scenario "Post-start or Closed hook calls Shutdown", 4 rows, `testWithTimeout`) and `TestContainerHooks_HookCallsShutdownWithDeadline` (scenario "Starting or Closing hook calls Shutdown with a deadline", 2 rows, `synctest.Test`); verify `go test -race -run 'TestContainerHooks_HookCallsShutdown' .`; both may pass on arrival because groups 1–4 use `pointAfterStart`/`pointClosed` and start-lifecycle's deadline handling, so record that and show they can fail by temporarily firing Started at `pointStartersUp` (timeout) and Closed before `finishClose` (timeout), neither committed
- [ ] 5.2 GREEN: no production change expected; if 5.1 failed, move the offending dispatch to `pointAfterStart`/`pointClosed` (D5, D8); verify the 5.1 command passes and `make test` is green
- [ ] 5.3 REFACTOR: none needed, since no structure changed; verify `make test` and `make lint` pass
- [ ] 5.4 Add a "Calling Shutdown from a hook" docs section and godoc on the Starting/Closing fields and adders (safe from Started, Ready, Failed and Closed; Starting and Closing need a deadline, D8); verify `npm --prefix docs run build` succeeds

## 6. Shutdown during an in-flight Start

- [ ] 6.1 RED: add `TestContainerHooks_ShutdownDuringStart` (scenario "Shutdown races a running Start", 3 rows, `synctest.Test`); verify `go test -race -count=5 -run TestContainerHooks_ShutdownDuringStart .`; the rows may pass on arrival through D7's abort checks, so record that and show they can fail by temporarily firing Started at `pointAfterStart` regardless of the outcome (not committed)
- [ ] 6.2 GREEN: no production change expected, since start-lifecycle's `finishStart` already turns a late Shutdown into `startFailed` (design D2); if 6.1 failed, fix the hook dispatch at `pointAfterStart` only; verify the 6.1 command passes and `make test` is green
- [ ] 6.3 REFACTOR: none needed, no production code was added in this group; verify `make test` and `make lint` pass
- [ ] 6.4 Add the ordering rules of design D3 to the field godoc and the docs page (Closing runs after an in-flight Start returned; Closing/Closed may run before or during Started/Ready/Failed; waiters may return before Closed runs); verify `go doc doger.InjectorOpts` shows them and `npm --prefix docs run build` succeeds

## 7. Hook contexts

- [ ] 7.1 RED: add `TestContainerHooks_NilContext` (scenario "Nil context reaches hooks as non-nil", 2 rows); verify `go test -race -run TestContainerHooks_NilContext .` fails on the `ShutdownWithContext` row (nil context passed to Closing); if the Start row passes on arrival via start-lifecycle's nil normalization, record that and show it can fail by temporarily passing the raw argument (not committed)
- [ ] 7.2 GREEN: normalize a nil Shutdown context to `context.Background()` before firing Closing/Closed, and pass the caller's context to Started/Ready/Failed (D5); verify the 7.1 command passes and `make test` is green
- [ ] 7.3 REFACTOR: none needed, a single normalization line; verify `make test` and `make lint` pass

## 8. Registration order, nil hooks and Clone

- [ ] 8.1 RED: add `TestContainerHooks_RegistrationOrder` (scenario "Registration order"), `TestContainerHooks_NilIgnored` (scenario "Nil hooks are ignored", 6 rows) and `TestContainerHooks_Clone` (scenario "Clone copies hooks but not state", 2 rows); verify `go test -race -run 'TestContainerHooks_(RegistrationOrder|NilIgnored|Clone)' .` fails on NilIgnored (nil call panics) and on the `Clone()` row (count 1); RegistrationOrder and the `CloneWithOpts` row pass on arrival, so record that and show they can fail by temporarily reversing the iteration and by copying hooks in `CloneWithOpts` (not committed)
- [ ] 8.2 GREEN: skip nil entries in both runners, make `Add*(nil)` a no-op, and copy the six slices plus `HookContainerPanic` in `InjectorOpts.copy()` (design D4); verify the 8.1 command passes and `make test` is green
- [ ] 8.3 REFACTOR: *Extract Function* for the six near-identical `Add*` bodies only if lint flags duplication, otherwise none needed (mirrors the existing `Add*Hook` style); verify `make test` and `make lint` pass

## 9. Observer hook panics are recovered and re-raised

- [ ] 9.1 RED: add `TestContainerHooks_ObserverPanicDefault` (scenario "Default re-panics after the operation completes", 5 rows) and `TestContainerHooks_ObserverPanicOverride` (scenario "HookContainerPanic overrides the default", 2 rows); verify `go test -race -run 'TestContainerHooks_ObserverPanic' .` fails to compile only on the missing `HookContainerPanic` field
- [ ] 9.2 GREEN: add `HookContainerPanic`, recover each observer hook call, keep the first panic and log later ones, and dispatch it last, after `finishStart`/`finishClose` and every hook ran, re-panicking when the field is nil (design D6); verify the 9.1 command passes and `make test` is green
- [ ] 9.3 REFACTOR: *Extract Function* `callHookSafely(fn func()) (recovered any, panicked bool)` and `dispatchHookPanic(point, value)`; verify `make test` and `make lint` pass
- [ ] 9.4 Add `ExampleInjectorOpts_hookContainerPanic` (log instead of re-panic), godoc on the field, and the panic section of the docs page; verify `go test -run ExampleInjectorOpts_hookContainerPanic .` passes and `npm --prefix docs run build` succeeds

## 10. Starting hook panics are vetoes

- [ ] 10.1 RED: add `TestContainerStartingHook_Panic` (scenario "Panicking Starting hook", 2 rows); verify `go test -race -run TestContainerStartingHook_Panic .` fails because the panic escapes `Start`
- [ ] 10.2 GREEN: recover in `runStartingHooks` with `callHookSafely`, converting the value with `%w` when it is an error and `%v` otherwise, as a veto that bypasses `HookContainerPanic` (design D6); verify the 10.1 command passes and `make test` is green
- [ ] 10.3 REFACTOR: none needed, since 10.2 reuses `callHookSafely`; verify `make test` and `make lint` pass

## 11. At most once and concurrency

- [ ] 11.1 RED: add `TestContainerHooks_ConcurrentStart` (scenario "Concurrent Start calls"), `TestContainerHooks_ConcurrentShutdown` (scenario "Repeated and concurrent Shutdown calls", 2 rows) and `TestContainerHooks_ReentrantStart` (scenario "Hook calling Start re-entrantly"), all with `testWithTimeout`; verify `go test -race -count=5 -run 'TestContainerHooks_(ConcurrentStart|ConcurrentShutdown|ReentrantStart)' .`; they pass on arrival through the start-lifecycle CAS guards, so record that and show they can fail by temporarily replacing the close-phase CAS with an unconditional store (not committed)
- [ ] 11.2 GREEN: no production change expected; fix any race or double firing the 11.1 run reports; verify the 11.1 command passes and goleak reports no leaks
- [ ] 11.3 REFACTOR: none expected; confirm with a `gopls` call-hierarchy review of `lifecycleMu.Lock` that no lock is held while hooks run; verify `make test` and `make lint` pass

## 12. Only the root container fires container hooks

- [ ] 12.1 RED: add `TestContainerHooks_ChildScopeShutdown` (scenario "Child scope shutdown fires nothing") and `TestContainerHooks_NoHooksUnchanged` (scenario "No hooks registered leaves behavior unchanged", 3 rows); verify `go test -race -run 'TestContainerHooks_(ChildScopeShutdown|NoHooksUnchanged)' .`; they pass on arrival, so record that and show they can fail by temporarily firing Closing from `Scope.ShutdownWithContext` and by making a nil `HookContainerStarted` slice panic (not committed)
- [ ] 12.2 GREEN: no production change expected; verify the 12.1 command passes and `make test` is green
- [ ] 12.3 REFACTOR: none needed; verify `make test` and `make lint` pass
- [ ] 12.4 Add the "root only" section to the docs page (child `Scope`/`virtualScope` shutdown bypasses container hooks); verify `npm --prefix docs run build` succeeds

## 13. Integration checks

- [ ] 13.1 Full run: verify `make test` and `make lint` pass, `go test -race -count=3 -run 'TestContainer' .` is stable, and `go build ./...` succeeds in every workspace module under `http/` and `examples/`
- [ ] 13.2 Traceability: verify `go test -v -run 'TestContainer|ExampleRootScope_AddContainer|ExampleInjectorOpts_(containerHooks|hookContainerPanic)' .` lists every test in design.md's Test Strategy as PASS, one per scenario and Examples row

## Workflow follow-up

- Archive the change after review once `make test` passes and the scenario-to-test mapping is confirmed.
- start-lifecycle D7 already places `pointStarting` before the first abort check, matching hooks D5; no reconciliation needed.
- Phase 2 candidates: per-scope container hooks and a public container phase accessor.
