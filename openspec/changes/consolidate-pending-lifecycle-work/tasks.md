# Tasks

Read design.md before each group: checkbox lines carry only the summary. In groups carried from a folded change (5–16 from `add-container-lifecycle-hooks`, 20–32 from `add-collection-invocation`), `design Dn` means that group's Part (H or K) in design.md, and `start-lifecycle Dn` the archived start-lifecycle design. Load `golang-how-to`, then `golang-testing`, `golang-stretchr-testify`, `golang-concurrency`, `golang-context`, `golang-error-handling` and `golang-refactoring`. Use gopls for navigation.

Rules:
- Strict TDD: show the RED failure and the GREEN pass before checking a box.
- Run `make test` and `make lint` after every REFACTOR.
- Docs tasks verify both `npm --prefix docs run lint` and `npm --prefix docs run build`.
- Flaky-test policy: if only `TestRootScope_queueServiceHealthcheck` fails with its known symptom, rerun once and record it; any other failure blocks.
- Groups 5–11 must not extend `InjectorOpts.copy()` or add nil-skipping for container hooks: group 12 owns both, so its RED tests fail for the right reason.

## 0. Prerequisites and baseline

- [x] 0.1 Confirm PR #4 is merged into `main` and `add-start-lifecycle-and-kinds` is archived (`openspec/specs/container-start`, `component-kinds`, `registration-options` and `dependency-declaration` exist), then branch from `main`; verify `openspec validate consolidate-pending-lifecycle-work --strict` passes with no "Archive would refuse" notes, and stop and report if PR #4 is not merged.
- [x] 0.2 Record the baseline in the scratchpad: `go test -run '^$' -bench . -benchmem -count 10 ./bench/` (bench-before) and the per-package test inventory (`go test -list '.*' ./...`); verify `make test`, `make lint` and per-module `go vet ./... && go test -race ./...` pass.

## 1. An alias never shuts its target down (defect fix; design F1)

- [x] 1.1 RED: add `TestServiceAlias_ShutdownTargetOnce` (scenario "Target is shut down once", 6 rows), `TestServiceAlias_ShutdownTargetOnce_Stable` (scenario "Repeated runs are stable", 200 runs) and `TestServiceAlias_InvokeAfterTargetShutdown` to `service_alias_test.go`; verify `go test -race -run 'TestServiceAlias_(ShutdownTargetOnce|InvokeAfterTargetShutdown)' .` fails on the eager rows (2 calls) and the child-scope/ShutdownNamed rows (target shut by the alias); record any row that passes on arrival
- [x] 1.2 GREEN: make `serviceAlias.shutdown` return nil without delegating, keeping `isShutdowner` (design F1); verify the 1.1 command passes and `make test` is green
- [x] 1.3 REFACTOR: none needed, a one-method change; verify `make test` and `make lint` pass
- [x] 1.4 Document the alias shutdown rule in the `As`/`AsNamed`/`Bind`/`BindNamed` godoc, `docs/docs/service-registration/` alias docs, and an upgrading-page entry flagging the behavior change; verify `go doc As` shows it and the docs lint and build pass

## 2. mergeShutdownReports reports success correctly (defect fix; design F2)

- [x] 2.1 RED: add `TestMergeShutdownReports_Succeed` to `errors_test.go` (no reports, two successful, one failing); verify `go test -race -run TestMergeShutdownReports_Succeed .` fails on the successful rows (`Succeed` false)
- [x] 2.2 GREEN: set `Succeed = len(out.Errors) == 0` (design F2); verify the 2.1 command passes and `make test` is green
- [x] 2.3 REFACTOR: none needed, a one-line fix; verify `make test` and `make lint` pass

## 3. The default Runnable handler is implicit (design F4)

- [x] 3.1 Characterization: run `TestRunnable_*` and `TestKind_*` and record `BenchmarkNew` allocs/op (13 since start-lifecycle); verify they pass and the number is recorded
- [x] 3.2 REFACTOR: *Substitute Algorithm*: remove the `runnableKind.Handle` call from `NewWithOpts`, and let `runStage` use a package-level `defaultRunnableEntry` in the first `StageAfterStart` slot when the container has no `runnableKind` entry (design F4); verify the 3.1 tests pass unchanged, `make test` and `make lint` pass, and `BenchmarkNew` shows 11 allocs/op

## 4. healthCheckPool race investigation (design F5)

- [x] 4.1 Add `TestRootScope_HealthCheckDuringShutdown` (HealthCheck with `HealthCheckParallelism: 2` racing root Shutdown, 50 iterations) and run it with `go test -race -count=2000 -run TestRootScope_HealthCheckDuringShutdown .`; verify: if no race is reported, keep the test as a race guard and record "not reproduced"; if a race is reported, stop and ask the user before any fix (CLAUDE.md rule 5)

## 5. Starting hooks run first and can abort Start (Part H)

- [x] 5.1 RED: add `TestContainerStartingHook_Veto` (scenario "Starting hook allows or vetoes Start", 4 rows), `TestContainerStartingHook_BeforeContextCheck` (scenario "Starting hooks run before the context check", 2 rows) and `TestContainerStartingHook_ReceivesContext` (scenario "Starting hook receives the Start context"); verify `go test -race -run 'TestContainerStartingHook_(Veto|BeforeContextCheck|ReceivesContext)' .` fails to compile only on the missing `HookContainerStarting` field and `AddContainerStartingHook` method
- [x] 5.2 GREEN: add `InjectorOpts.HookContainerStarting`, its nil-to-empty init in `NewWithOpts` and `AddContainerStartingHook`, and run the hooks with `startCtx` at `pointStarting` (before D7's first abort check), returning a veto as `DI: container start aborted: %w` (design D4, D5); verify the 5.1 command passes and `make test` is green
- [x] 5.3 REFACTOR: *Extract Function* `runStartingHooks(ctx) error` out of `StartWithContext`; verify `make test` and `make lint` pass
- [x] 5.4 Add `ExampleRootScope_AddContainerStartingHook` (fail-fast config validation that vetoes `Start`) and godoc on the field and method stating firing point, veto and the "must not call Shutdown synchronously" rule (D8); verify `go test -run ExampleRootScope_AddContainerStartingHook .` passes
- [x] 5.5 Create `docs/docs/service-lifecycle/container-hooks.md` with the phase diagram (start-lifecycle D6 plus `ready`) and the Starting section; verify `npm --prefix docs run build` succeeds

## 6. Started and Ready hooks mark a successful start (Part H)

- [x] 6.1 RED: add `TestContainerHooks_StartOrder` (scenario "Hook order relative to the start pipeline") and `TestContainerHooks_EmptyContainer` (scenario "Started and Ready fire for an empty container"); verify `go test -race -run 'TestContainerHooks_(StartOrder|EmptyContainer)' .` fails to compile only on the missing Started/Ready/Failed fields and adders
- [x] 6.2 GREEN: add `HookContainerStarted`, `HookContainerReady` and `HookContainerFailed` (field only) with init and adders; at `pointAfterStart` on `startStarted` fire Started, CAS `startStarted → startReady`, then fire Ready (design D1, D5, D7); verify the 6.1 command passes and `make test` is green
- [x] 6.3 REFACTOR: *Extract Function* `runObserverHooks(ctx, point, hooks)` shared by Started and Ready; verify `make test` and `make lint` pass
- [x] 6.4 Add `ExampleRootScope_AddContainerReadyHook` (signals readiness after runners complete) and godoc on the Started/Ready fields and adders (D7 meaning); verify `go test -run ExampleRootScope_AddContainerReadyHook .` passes
- [x] 6.5 Add the Started/Ready section to the docs page, including "Started fires after runnables" (D7); verify `npm --prefix docs run build` succeeds

## 7. Failed hooks observe a failed start (Part H)

- [x] 7.1 RED: add `TestContainerFailedHook_FailurePoint` (scenario "Failure point decides which hooks run", 7 rows) and `TestContainerFailedHook_GuardErrors` (scenario "One-shot guard errors do not fire Failed", 3 rows); verify `go test -race -run 'TestContainerFailedHook_' .` fails on assertions (Failed hook not called); GuardErrors may pass on arrival via the start-lifecycle guard, so record that and show it can fail by temporarily calling Failed hooks before the guard (not committed)
- [x] 7.2 GREEN: at `pointAfterStart` on `startFailed`, fire Failed hooks with the exact error `Start` returns, then return it (D5); verify the 7.1 command passes and `make test` is green
- [x] 7.3 REFACTOR: *Consolidate Duplicate Conditional Fragments* so every exit of `StartWithContext` goes through one `pointAfterStart` dispatch of Started/Ready or Failed; verify `make test` and `make lint` pass
- [x] 7.4 Add `ExampleRootScope_AddContainerFailedHook` (logs the error and calls `root.Shutdown()`) and godoc on the Failed field and adder; verify `go test -run ExampleRootScope_AddContainerFailedHook .` passes

## 8. Closing and Closed hooks bracket the root shutdown (Part H)

- [x] 8.1 RED: add `TestContainerClosingClosedHooks_Order` (scenario "Closing and Closed around service shutdown from every state", 3 rows), `TestContainerClosedHook_ReportErrors` (scenario "Closed hook sees shutdown errors in the report") and `TestContainerHooks_ShutdownOnSignalsWithContext` (scenario "Signal-driven shutdown fires the hooks"); verify `go test -race -run 'TestContainer(ClosingClosedHooks_Order|ClosedHook_ReportErrors|Hooks_ShutdownOnSignalsWithContext)' .` fails to compile only on the missing Closing/Closed fields and adders
- [x] 8.2 GREEN: add `HookContainerClosing`/`HookContainerClosed` with init and adders; fire Closing at `pointClosing` (winner only, after the in-flight Start wait) and Closed at `pointClosed` (after `finishClose`) with the winner's report (design D3, D5; start-lifecycle D6); verify the 8.1 command passes, `make test` is green and the existing `root_scope_test.go` shutdown tests pass unchanged
- [x] 8.3 REFACTOR: *Extract Function* `closeContainer(ctx) *ShutdownReport` if `ShutdownWithContext` exceeds ~25 lines, otherwise none needed; verify `make test` and `make lint` pass
- [x] 8.4 Add `ExampleInjectorOpts_containerHooks` (all six hooks, printed order for a start and a shutdown) and the Closing/Closed docs section; verify `go test -run ExampleInjectorOpts_containerHooks .` passes and `npm --prefix docs run build` succeeds

## 9. Calling Shutdown from hooks (Part H)

- [x] 9.1 RED: add `TestContainerHooks_HookCallsShutdown` (scenario "Post-start or Closed hook calls Shutdown", 4 rows, `testWithTimeout`) and `TestContainerHooks_HookCallsShutdownWithDeadline` (scenario "Starting or Closing hook calls Shutdown with a deadline", 2 rows, `synctest.Test`); verify `go test -race -run 'TestContainerHooks_HookCallsShutdown' .`; both may pass on arrival because groups 5–8 use `pointAfterStart`/`pointClosed` and start-lifecycle's deadline handling, so record that and show they can fail by temporarily firing Started at `pointStartersUp` (timeout) and Closed before `finishClose` (timeout), neither committed
- [x] 9.2 GREEN: no production change expected; if 9.1 failed, move the offending dispatch to `pointAfterStart`/`pointClosed` (D5, D8); verify the 9.1 command passes and `make test` is green
- [x] 9.3 REFACTOR: none needed, since no structure changed; verify `make test` and `make lint` pass
- [x] 9.4 Add a "Calling Shutdown from a hook" docs section and godoc on the Starting/Closing fields and adders (safe from Started, Ready, Failed and Closed; Starting and Closing need a deadline, D8); verify `npm --prefix docs run build` succeeds

## 10. Shutdown during an in-flight Start (Part H)

- [x] 10.1 RED: add `TestContainerHooks_ShutdownDuringStart` (scenario "Shutdown races a running Start", 3 rows, `synctest.Test`); verify `go test -race -count=5 -run TestContainerHooks_ShutdownDuringStart .`; the rows may pass on arrival through D7's abort checks, so record that and show they can fail by temporarily firing Started at `pointAfterStart` regardless of the outcome (not committed)
- [x] 10.2 GREEN: no production change expected, since start-lifecycle's `finishStart` already turns a late Shutdown into `startFailed` (design D2); if 10.1 failed, fix the hook dispatch at `pointAfterStart` only; verify the 10.1 command passes and `make test` is green
- [x] 10.3 REFACTOR: none needed, no production code was added in this group; verify `make test` and `make lint` pass
- [x] 10.4 Add the ordering rules of design D3 to the field godoc and the docs page (Closing runs after an in-flight Start returned; Closing/Closed may run before or during Started/Ready/Failed; waiters may return before Closed runs); verify `go doc doger.InjectorOpts` shows them and `npm --prefix docs run build` succeeds

## 11. Hook contexts (Part H)

- [x] 11.1 RED: add `TestContainerHooks_NilContext` (scenario "Nil context reaches hooks as non-nil", 2 rows); verify `go test -race -run TestContainerHooks_NilContext .` fails on the `ShutdownWithContext` row (nil context passed to Closing); if the Start row passes on arrival via start-lifecycle's nil normalization, record that and show it can fail by temporarily passing the raw argument (not committed)
- [x] 11.2 GREEN: normalize a nil Shutdown context to `context.Background()` before firing Closing/Closed, and pass the caller's context to Started/Ready/Failed (D5); verify the 11.1 command passes and `make test` is green
- [x] 11.3 REFACTOR: none needed, a single normalization line; verify `make test` and `make lint` pass

## 12. Registration order, nil hooks and Clone (Part H)

- [x] 12.1 RED: add `TestContainerHooks_RegistrationOrder` (scenario "Registration order"), `TestContainerHooks_NilIgnored` (scenario "Nil hooks are ignored", 6 rows) and `TestContainerHooks_Clone` (scenario "Clone copies hooks but not state", 2 rows); verify `go test -race -run 'TestContainerHooks_(RegistrationOrder|NilIgnored|Clone)' .` fails on NilIgnored (nil call panics) and on the `Clone()` row (count 1); RegistrationOrder and the `CloneWithOpts` row pass on arrival, so record that and show they can fail by temporarily reversing the iteration and by copying hooks in `CloneWithOpts` (not committed)
- [x] 12.2 GREEN: skip nil entries in both runners, make `Add*(nil)` a no-op, and copy the six slices plus `HookContainerPanic` in `InjectorOpts.copy()` (design D4); verify the 12.1 command passes and `make test` is green
- [x] 12.3 REFACTOR: *Extract Function* for the six near-identical `Add*` bodies only if lint flags duplication, otherwise none needed (mirrors the existing `Add*Hook` style); verify `make test` and `make lint` pass

## 13. Observer hook panics are recovered and re-raised (Part H)

- [x] 13.1 RED: add `TestContainerHooks_ObserverPanicDefault` (scenario "Default re-panics after the operation completes", 5 rows) and `TestContainerHooks_ObserverPanicOverride` (scenario "HookContainerPanic overrides the default", 2 rows); verify `go test -race -run 'TestContainerHooks_ObserverPanic' .` fails to compile only on the missing `HookContainerPanic` field
- [x] 13.2 GREEN: add `HookContainerPanic`, recover each observer hook call, keep the first panic and log later ones, and dispatch it last, after `finishStart`/`finishClose` and every hook ran, re-panicking when the field is nil (design D6); verify the 13.1 command passes and `make test` is green
- [x] 13.3 REFACTOR: *Extract Function* `callHookSafely(fn func()) (recovered any, panicked bool)` and `dispatchHookPanic(point, value)`; verify `make test` and `make lint` pass
- [x] 13.4 Add `ExampleInjectorOpts_hookContainerPanic` (log instead of re-panic), godoc on the field, and the panic section of the docs page; verify `go test -run ExampleInjectorOpts_hookContainerPanic .` passes and `npm --prefix docs run build` succeeds

## 14. Starting hook panics are vetoes (Part H)

- [x] 14.1 RED: add `TestContainerStartingHook_Panic` (scenario "Panicking Starting hook", 2 rows); verify `go test -race -run TestContainerStartingHook_Panic .` fails because the panic escapes `Start`
- [x] 14.2 GREEN: recover in `runStartingHooks` with `callHookSafely`, converting the value with `%w` when it is an error and `%v` otherwise, as a veto that bypasses `HookContainerPanic` (design D6); verify the 14.1 command passes and `make test` is green
- [x] 14.3 REFACTOR: none needed, since 14.2 reuses `callHookSafely`; verify `make test` and `make lint` pass

## 15. At most once and concurrency (Part H)

- [x] 15.1 RED: add `TestContainerHooks_ConcurrentStart` (scenario "Concurrent Start calls"), `TestContainerHooks_ConcurrentShutdown` (scenario "Repeated and concurrent Shutdown calls", 2 rows) and `TestContainerHooks_ReentrantStart` (scenario "Hook calling Start re-entrantly"), all with `testWithTimeout`; verify `go test -race -count=5 -run 'TestContainerHooks_(ConcurrentStart|ConcurrentShutdown|ReentrantStart)' .`; they pass on arrival through the start-lifecycle CAS guards, so record that and show they can fail by temporarily replacing the close-phase CAS with an unconditional store (not committed)
- [x] 15.2 GREEN: no production change expected; fix any race or double firing the 15.1 run reports; verify the 15.1 command passes and goleak reports no leaks
- [x] 15.3 REFACTOR: none expected; confirm with a `gopls` call-hierarchy review of `lifecycleMu.Lock` that no lock is held while hooks run; verify `make test` and `make lint` pass

## 16. Only the root container fires container hooks (Part H)

- [x] 16.1 RED: add `TestContainerHooks_ChildScopeShutdown` (scenario "Child scope shutdown fires nothing") and `TestContainerHooks_NoHooksUnchanged` (scenario "No hooks registered leaves behavior unchanged", 3 rows); verify `go test -race -run 'TestContainerHooks_(ChildScopeShutdown|NoHooksUnchanged)' .`; they pass on arrival, so record that and show they can fail by temporarily firing Closing from `Scope.ShutdownWithContext` and by making a nil `HookContainerStarted` slice panic (not committed)
- [x] 16.2 GREEN: no production change expected; verify the 16.1 command passes and `make test` is green
- [x] 16.3 REFACTOR: none needed; verify `make test` and `make lint` pass
- [x] 16.4 Add the "root only" section to the docs page (child `Scope`/`virtualScope` shutdown bypasses container hooks); verify `npm --prefix docs run build` succeeds

## 17. Clones copy kind handlers (component-kinds; design Q1)

- [x] 17.1 RED: add `TestKind_CloneCopiesHandlers` (scenario "Clone keeps replaced handlers", 2 rows) and `TestKind_CloneReplaceIsolated` (scenario "Replacing a handler on the clone leaves the source unchanged") to `kind_test.go`; verify `go test -race -run 'TestKind_Clone' .` fails on both rows (clone runs the default Runnable handler and no k handler); record that `CloneReplaceIsolated` passes on arrival and show it can fail by temporarily sharing the source's `kinds` slice with the clone (not committed)
- [x] 17.2 GREEN: copy `s.kinds` under `kindsMu` into the clone in `CloneWithOpts` (design Q1); verify the 17.1 command passes and `make test` is green
- [x] 17.3 REFACTOR: *Extract Function* `(*RootScope).copyKindsTo(clone)`; verify `make test` and `make lint` pass
- [x] 17.4 Update the `Clone`/`CloneWithOpts` godoc, the component-kinds page (clones), and an upgrading-page entry; verify the docs lint and build pass

## 18. Start failures are StartError values (container-start; design Q2)

- [x] 18.1 RED: add `TestStartError_Fields` (scenario "StartError fields per failure point", 4 rows) and `TestStartError_NotForGuards` (scenario "Errors that are not service failures are not StartError", 3 rows) to `root_start_test.go`; verify `go test -race -run TestStartError_ .` fails to compile only on `StartError`, `StartOp` and the `StartOp*` constants
- [x] 18.2 GREEN: add `StartError`/`StartOp` and return them from the build, start (including AsStarter-without-Start) and handle failure points with unchanged `Error()` text, keeping abort wrapping around them (design Q2); verify the 18.1 command passes, the existing `TestStart_*`/`TestKind_*`/`TestRunnable_*` message assertions pass unchanged, and `make test` is green
- [x] 18.3 REFACTOR: *Replace Function with Constructor*: `wrapStartError` and the build/handle formats become `newStartError(op, scope, name, kind, cause)`; verify `make test` and `make lint` pass
- [x] 18.4 Add `ExampleStartError` (errors.As on a failed Start) and the StartError section of the starter page; verify `go test -run ExampleStartError .` and the docs lint and build pass

## 19. Explained edges report their source (dependency-declaration; design Q3)

- [x] 19.1 RED: add `TestExplain_EdgeSource` (scenario "Source per edge", 2 rows), `TestExplain_EdgeSourceDependents` and `TestExplain_EdgeSourceCompat` (scenario "Output without declared edges is unchanged") to `dependency_test.go`; verify `go test -race -run TestExplain_EdgeSource .` fails to compile only on `ExplainServiceDependencyOutput.Source` and the `DependencySource*` constants
- [x] 19.2 GREEN: store a `DependencySource` per DAG edge with invoked > depends-on > declared precedence, record it at the three call sites (`invokeAnyByName`, `resolveDeclaredDependencies` via `invokeAnyByNameAs`, `DeclareDependency`), and expose it with `String`/`MarshalText` and the `String()` suffix (design Q3); verify the 19.1 command passes, `di_explain_test.go` passes unchanged and `make test` is green
- [x] 19.3 REFACTOR: *Introduce Parameter Object* if `addDependency` has more than seven parameters (`edge{from, to, source}`), otherwise none needed; verify `make test`, `make lint` and `go test -race -run TestDAG .` pass
- [x] 19.4 Add `ExampleExplainNamedService_source` and the source note on the declared-dependencies page; verify the example and the docs lint and build pass

## 20. InvokeAs behavior is unchanged (characterization baseline) (Part K)

- [x] 20.1 RED: add `TestInvokeAs_HookReceivesTypeName` (scenario "InvokeAs hooks receive the requested type name"); verify `go test -race -run TestInvokeAs_HookReceivesTypeName .` passes on arrival (characterization), record that, and show it can fail by temporarily passing the real service name to the hooks in `invokeByGenericType` (not committed)
- [x] 20.2 GREEN: no production change; this test guards the D3 extraction in 21.3; verify `make test` is green
- [x] 20.3 REFACTOR: none needed, since no code changed; verify `make test` and `make lint` pass

## 21. Collect every visible service assignable to a type (Part K)

- [x] 21.1 RED: add `TestInvokeAll_Membership` (scenario "Membership by registered type", 4 rows), `TestInvokeAll_EagerMembers` (scenario "Eager values are members without a provider call"), `TestInvokeAll_NilInjector` (scenario "Nil injector uses the default root scope") and `TestInvokeAll_TypedNilMember` (scenario "Typed-nil member is returned as is"); verify `go test -race -run 'TestInvokeAll_(Membership|EagerMembers|NilInjector|TypedNilMember)' .` fails to compile only on the missing `InvokeAll`
- [x] 21.2 GREEN: add `InvokeAll[T]` in `di.go` backed by a pipeline in `invoke.go` that filters `serviceForEachRec` by `serviceCanCastToGeneric[T]` only and builds members sequentially, copying the per-member logic of `invokeByGenericType` with the member name for hooks (design D3); verify the 21.1 command passes and `make test` is green
- [x] 21.3 REFACTOR: *Extract Function* `invokeServiceInstance(injector, vScope, hookName, realName, scope, service)` (design D3) and call it from `invokeByGenericType` (hookName = type name) and the collection pipeline (hookName = member name); verify `go test -race -run 'TestInvokeAs|TestMustInvokeAs|TestInvokeByGenericType|TestInvokeAll' .`, `make test` and `make lint` pass
- [x] 21.4 Add `ExampleInvokeAll` (members by registered type) and godoc on `InvokeAll` (registered-type membership, the `InvokeAll[any]` sharp edge, per-member hooks, snapshot semantics); verify `go test -run ExampleInvokeAll$ .` passes
- [x] 21.5 Create `docs/docs/service-invocation/collection-invocation.md` with the membership section; verify `npm --prefix docs run build` succeeds

## 22. Visibility, shadowing and alias exclusion (Part K)

- [x] 22.1 RED: add `TestInvokeAll_Visibility` (scenario "Scope visibility and shadowing", 5 rows), `TestInvokeAll_AnyType` (scenario "Interface type any matches every non-alias service"), `TestInvokeAll_ProviderScopeVisibility` (scenario "Collection inside a provider sees the provider's scope") and `TestInvokeAll_AliasExcluded` (scenario "Alias does not duplicate its target"); verify `go test -race -run 'TestInvokeAll_(Visibility|AnyType|ProviderScopeVisibility|AliasExcluded)' .` fails on the shadowing rows `x/*BHandler` and `x/*sql.DB` and on the alias duplicates; ProviderScopeVisibility passes on arrival through `virtualScope` delegation, so record that and show its assertion can fail by temporarily registering "b" in the root scope (not committed)
- [x] 22.2 GREEN: keep the first occurrence of each name before type filtering (nearest wins) and drop `ServiceTypeAlias` (design D1 steps 1, 3, 4); verify the 22.1 command passes and `make test` is green
- [x] 22.3 REFACTOR: *Extract Function* `collectionCandidates[T](injector) []collectionCandidate` (name, scope, depth, registration index, service; not `candidate`, which start-lifecycle already declares in the same package) for reuse by the map variant; verify `make test` and `make lint` pass
- [x] 22.4 Add the visibility, shadowing and alias section to the docs page and godoc (shadowing by a non-matching nearest service; provider-scope visibility); verify `npm --prefix docs run build` succeeds

## 23. Empty results, failure semantics and MustInvokeAll (Part K)

- [x] 23.1 RED: add `TestInvokeAll_Empty` (scenario "Empty results", `InvokeAll` rows), `TestInvokeAll_MemberFailure` (scenario "Member failure modes", 3 rows), and `TestMustInvokeAll_Panics` / `TestMustInvokeAll_Success` (scenarios "Must variants panic with the same error" / "Must variants return the collection on success", `MustInvokeAll` rows); verify `go test -race -run 'TestInvokeAll_(Empty|MemberFailure)|TestMustInvokeAll_(Panics|Success)' .` fails to compile only on `MustInvokeAll`, then on the nil-vs-empty and member-name assertions
- [x] 23.2 GREEN: return `[]T{}` on no match, wrap the first member error as ``DI: could not invoke collection member `%s`: %w`` and add `MustInvokeAll` via `must1` (design D3, D6, D7); verify the 23.1 command passes and `make test` is green
- [x] 23.3 REFACTOR: *Extract Function* `wrapMemberError(name string, err error) error`; verify `make test` and `make lint` pass

## 24. Name-keyed collection (InvokeAllMap) (Part K)

- [x] 24.1 RED: add `TestInvokeAllMap_Keys` (scenario "Map collection keys", 2 rows) and the `InvokeAllMap`/`MustInvokeAllMap` rows of `TestInvokeAll_Empty`, `TestMustInvokeAll_Panics` and `TestMustInvokeAll_Success`; verify `go test -race -run 'TestInvokeAllMap_Keys|TestInvokeAll_Empty|TestMustInvokeAll_' .` fails to compile only on the missing `InvokeAllMap`/`MustInvokeAllMap`
- [x] 24.2 GREEN: add `InvokeAllMap` on `collectionCandidates` with the same build loop, returning `map[string]T{}` on no match, and `MustInvokeAllMap` via `must1`; verify the 24.1 command passes and `make test` is green
- [x] 24.3 REFACTOR: *Extract Function* `buildMembers` shared by the slice and map variants, removing the duplicated loop; verify `make test` and `make lint` pass
- [x] 24.4 Add `ExampleInvokeAllMap` and godoc on `InvokeAllMap`/`MustInvokeAllMap` (same members as `InvokeAll`, keyed by name, unordered); verify `go test -run ExampleInvokeAllMap .` passes

## 25. A collection excludes the service that invokes it (Part K)

- [x] 25.1 RED: add `TestInvokeAll_CompositeExcludesSelf` (scenario "Composite collects every other implementation") and `TestInvokeAll_DeeperCycle` (scenario "Deeper cycles still fail"); verify `go test -race -run 'TestInvokeAll_(CompositeExcludesSelf|DeeperCycle)' .` fails on CompositeExcludesSelf with `ErrCircularDependency`; DeeperCycle passes on arrival, so record that and show it can fail by temporarily excluding every invoker-chain member (not committed)
- [x] 25.2 GREEN: drop the candidate named like the last entry of the virtual scope's invoker chain (design D3a); verify the 25.1 command passes and `make test` is green
- [x] 25.3 REFACTOR: *Extract Function* `invokingServiceName(injector) (string, bool)` if the check is inline in `collectionCandidates`, otherwise none needed; verify `make test` and `make lint` pass
- [x] 25.4 Add the composite section to the docs page and the self-exclusion rule to the `InvokeAll`/`InvokeAllMap` godoc; verify `npm --prefix docs run build` succeeds

## 26. Dependency edges, hooks and snapshot semantics (Part K)

- [x] 26.1 RED: add `TestInvokeAll_EdgesOrderShutdown`, `TestInvokeAll_HooksPerMember`, `TestInvokeAll_NoEdgesOutsideProvider`, `TestInvokeAll_TransientFresh`, `TestInvokeAll_Snapshot`, `TestInvokeAll_CallerMutation` and `TestInvokeAll_AfterShutdown` (scenarios "Dependency edges order shutdown", "Invocation hooks per member", "No edges when invoked outside a provider", "Transient and singleton members across calls", "Later registration is not visible in an earlier snapshot", "Caller mutation does not leak", "Collections after shutdown"); verify `go test -race -run 'TestInvokeAll_(Edges|Hooks|NoEdges|TransientFresh|Snapshot|CallerMutation|AfterShutdown)' .`; these pass on arrival through `invokeServiceInstance`, so record that and show EdgesOrderShutdown and HooksPerMember can fail by temporarily skipping `addDependency` and passing the type name as hookName (not committed)
- [x] 26.2 GREEN: no production change expected; fix only what 26.1 reports; verify the 26.1 command passes and `make test` is green
- [x] 26.3 REFACTOR: none needed if 26.2 changed nothing, otherwise *Inline Function* any helper made redundant; verify `make test` and `make lint` pass

## 27. Concurrent collection callers (Part K)

- [x] 27.1 RED: add `TestInvokeAll_ConcurrentSingleBuild` (100 goroutines), `TestInvokeAll_ConcurrentRegistration` (50 + 50 goroutines) and `TestInvokeAll_ConcurrentShutdown` (50 collectors + 1 shutdown), all with `testWithTimeout`, for scenarios "Concurrent callers build each member once", "Concurrent registration and collection" and "Concurrent collection and shutdown"; verify `go test -race -count=5 -run 'TestInvokeAll_Concurrent' .`; they pass on arrival because the wrappers serialize builds, so record that and show the counter assertion can fail with a deliberately unsynchronized fake provider in a throwaway experiment (not committed)
- [x] 27.2 GREEN: no production change expected; fix any race, leak or double build reported; verify the 27.1 command passes with `-race` and goleak reports no leak
- [x] 27.3 REFACTOR: none needed: no new shared state (design D8); verify `make test` and `make lint` pass

## 28. Default collection order (Part K)

- [x] 28.1 RED: add `TestInvokeAll_DefaultOrder` (scenario "Default order across scopes", 4 rows), `TestInvokeAll_DefaultOrderStable` (50 services × 20 calls), `TestInvokeAll_OverrideKeepsPosition` and `TestInvokeAll_DefaultOrderClone` (scenarios "Order is stable across repeated calls", "Override replaces a registration in place", "Default order in a clone"); verify `go test -race -count=10 -run 'TestInvokeAll_(DefaultOrder|OverrideKeepsPosition)' .` fails on order assertions (map iteration order)
- [x] 28.2 GREEN: sort candidates by (scope depth, registration index, name) before building (design D2); verify the 28.1 command passes with `-count=10` and `make test` is green
- [x] 28.3 REFACTOR: *Introduce Parameter Object*: make `collectionCandidate` carry `depth` and `index` explicitly if they are still loose values; verify `make test` and `make lint` pass
- [x] 28.4 Add `BenchmarkInvokeAll/{10,100,1000}` and `BenchmarkInvokeAllParallel` to `bench/do_bench_test.go`; verify `go test -run '^$' -bench BenchmarkInvokeAll -benchmem ./bench/` runs and record the results in the change notes

## 29. Ordered members sort ascending and stably (Part K)

- [x] 29.1 RED: add `TestInvokeAll_Ordered` (scenario "Ordered values", 6 rows) and `TestInvokeAll_OrderedTransient` (scenario "Order is read from each transient instance", 2 rows); verify `go test -race -run 'TestInvokeAll_Ordered' .` fails to compile only on the missing `Ordered`
- [x] 29.2 GREEN: add `type Ordered interface{ Order() int }` (in `di_lifecycle.go` or a new `ordered.go`) and the stable ordering pass for `InvokeAll` only, with `Ordered` > `0` (design D4); verify the 29.1 command passes and `make test` is green
- [x] 29.3 REFACTOR: *Extract Function* `orderValue(name, instance, opts) (int, error)` and *Replace Loop with Pipeline* using `slices.SortStableFunc`; verify `make test` and `make lint` pass
- [x] 29.4 Add `ExampleOrdered` and godoc on `Ordered` (ascending, missing = 0, stable, collection-only); verify `go test -run ExampleOrdered .` passes

## 30. WithOrder is a registration option for every registration style (Part K)

- [x] 30.1 RED: add `TestInvokeAll_WithOrderPrecedence` (scenario "Precedence", 5 rows), `TestInvokeAll_WithOrderHelpers` (scenario "WithOrder on every registration helper", 18 rows), `TestInvokeAll_WithOrderSkipsOrderMethod` (scenario "Order() is not called when WithOrder is set") and `TestWithOrder_NoEffectOnInvoke` (scenario "WithOrder outside collections has no effect"); verify `go test -race -run 'TestInvokeAll_WithOrder|TestWithOrder_' .` fails to compile only on the missing `WithOrder`
- [x] 30.2 GREEN: add `WithOrder(n int) ProvideOption` storing `*int` in the service metadata, last one wins, and read it first in `orderValue` (precedence WithOrder > Ordered > 0, design D4, D5); verify the 30.1 command passes and `make test` is green
- [x] 30.3 REFACTOR: none needed, since 30.2 only adds one branch to `orderValue`; verify `make test` and `make lint` pass
- [x] 30.4 Add `ExampleWithOrder` (third-party type ordered without editing it), godoc on `WithOrder`, and the ordering section of the docs page; verify `go test -run ExampleWithOrder .` passes and `npm --prefix docs run build` succeeds

## 31. Order() panic and map-collection independence (Part K)

- [x] 31.1 RED: add `TestInvokeAll_OrderPanic` (scenario "Panicking Order()", 3 rows), `TestMustInvokeAll_OrderPanic` (scenario "MustInvokeAll surfaces the Order() panic as its panic") and `TestInvokeAllMap_IgnoresOrdered` (scenario "Order() not called for map collections"); verify `go test -race -run 'TestInvokeAll_OrderPanic|TestMustInvokeAll_OrderPanic|TestInvokeAllMap_IgnoresOrdered' .` fails because the panic escapes `InvokeAll`; IgnoresOrdered passes on arrival, so record that and show it can fail by temporarily running the ordering pass in `InvokeAllMap` (not committed)
- [x] 31.2 GREEN: recover inside `orderValue`, wrapping error panics with `%w` and stringifying others, both naming the member (design D4); verify the 31.1 command passes and `make test` is green
- [x] 31.3 REFACTOR: reuse start-lifecycle's existing `recoverAsError(r any) error` inside `orderValue` (same `%w`/`%v` rule as start-lifecycle D11) instead of a local recover-to-error block; add no new helper; verify `make test` and `make lint` pass

## 32. Ordering never affects lifecycle order (Part K)

- [x] 32.1 RED: add `TestInvokeAll_OrderDoesNotAffectShutdown` and `TestInvokeAll_OrderDoesNotAffectStart` (scenarios "Shutdown ignores collection order" and "Start ignores collection order"); verify `go test -race -run 'TestInvokeAll_OrderDoesNotAffect' .`; they pass on arrival, so record that and show each can fail against a throwaway change that feeds `WithOrder` into start and shutdown ordering (not committed)
- [x] 32.2 GREEN: no production change expected, since `WithOrder` is read only by `orderValue`; verify the 32.1 command passes and `make test` is green
- [x] 32.3 REFACTOR: none needed; verify `make test` and `make lint` pass
- [x] 32.4 Add the "collection order vs lifecycle order" section to the docs page; verify `npm --prefix docs run build` succeeds

## 33. Refresh starts what is not started yet (container-start; design P1–P3)

- [x] 33.1 RED: add `TestRefresh_StartsNew` (scenario "Refresh starts late registrations and new scopes once", 2 rows), `TestRefresh_NothingNew` and `TestIncrementalStart_Phase` (scenario "Incremental start in the wrong phase", the 3 `root.Refresh()` rows) to `root_start_test.go`; verify `go test -race -run 'TestRefresh_(StartsNew|NothingNew)|TestIncrementalStart_Phase' .` fails to compile only on `Refresh` and `ErrNotStarted`
- [x] 33.2 GREEN: add `ErrNotStarted`, `pipelineMu` with the `started`/`handled` sets keyed by wrapper (Start records them), `beginIncremental` with the phase guard, and `RootScope.Refresh`/`RefreshWithContext` running the pipeline over unstarted candidates of the whole tree (design P1–P3); verify the 33.1 command passes and `make test` is green
- [x] 33.3 REFACTOR: *Extract Function* `runPipeline(ctx, roots, incremental)` shared by `Start` and `Refresh`, and *Parameterize Function* `discoverCandidates(roots, skip)`; verify `make test` and `make lint` pass
- [x] 33.4 Add `ExampleRootScope_Refresh` and a "Refresh" section on the starter page (late registrations, at-most-once, `ErrNotStarted`, never call it from Start-time code); verify the example and the docs lint and build pass

## 34. Scope.Start starts a subtree (container-start; design P2)

- [x] 34.1 RED: add `TestScopeStart_Subtree`, `TestScopeStart_RootScope` and the two `tenant.Start()` rows of `TestIncrementalStart_Phase`; verify `go test -race -run 'TestScopeStart_|TestIncrementalStart_Phase' .` fails to compile only on `Scope.Start`
- [x] 34.2 GREEN: add `Scope.Start`/`StartWithContext` running the pipeline with `roots = [s]`, delegating to Refresh on the root's own scope (design P2); verify the 34.1 command passes and `make test` is green
- [x] 34.3 REFACTOR: none needed if 34.2 only calls `runPipeline`; otherwise *Inline Function* any wrapper it left; verify `make test` and `make lint` pass
- [x] 34.4 Add `ExampleScope_Start` and the scope-start section of the starter page; verify the example and the docs lint and build pass

## 35. Incremental starts are serialized and cancelled by Shutdown (container-start; design P1, P2)

- [x] 35.1 RED: add `TestRefresh_Concurrent` (scenario "Concurrent incremental starts") and `TestRefresh_ShutdownCancels` (scenario "Shutdown cancels a running Refresh", `synctest.Test`); verify `go test -race -count=10 -run 'TestRefresh_(Concurrent|ShutdownCancels)' .` fails on ShutdownCancels (Refresh not cancelled, deadlock) and record whether Concurrent passes on arrival through `pipelineMu`, showing it can fail by temporarily dropping the lock (not committed)
- [x] 35.2 GREEN: record `incCancel`/`incDone` in `beginIncremental`, capture them in `beginClose`, and cancel and await them in `waitForStart` (design P2); verify the 35.1 command passes, goleak is clean, and `make test` is green
- [x] 35.3 REFACTOR: *Extract Function* `inFlightRun()` returning the running Start or Refresh cancel/done pair for `beginClose`; then a gopls call-hierarchy review that `pipelineMu` is never taken under `lifecycleMu` or by Shutdown (design X3); verify `make test` and `make lint` pass

## 36. Kind handlers and runners on incremental start (component-kinds; design P1)

- [x] 36.1 RED: add `TestKind_RefreshHandlesNew` and `TestRunnable_RefreshRunsOnce`; verify `go test -race -run 'TestKind_RefreshHandlesNew|TestRunnable_RefreshRunsOnce' .` fails on duplicate handling ("a" handled twice, "migrate" run twice) unless 33.2 already skips handled pairs; if they pass on arrival, record it and show they fail with the `handled` check temporarily removed (not committed)
- [x] 36.2 GREEN: skip `(kind, wrapper)` pairs in `handled` in `runStage` and record new ones (design P1); verify the 36.1 command passes and `make test` is green
- [x] 36.3 REFACTOR: none needed, a guard in one loop; verify `make test` and `make lint` pass

## 37. Incremental start fires no container hooks (container-lifecycle-hooks; design X2)

- [x] 37.1 RED: add `TestContainerHooks_IncrementalStartFiresNone` (scenario "Refresh after Start fires no container hook", 2 rows); verify `go test -race -run TestContainerHooks_IncrementalStartFiresNone .`; it likely passes on arrival because `runPipeline` never reaches `pointStarting`/`pointAfterStart` for incremental runs, so record that and show it fails when the Starting hooks are temporarily run in `runPipeline` (not committed)
- [x] 37.2 GREEN: no production change expected; if 37.1 failed, guard the container-hook points with `!incremental`; verify the 37.1 command passes and `make test` is green
- [x] 37.3 REFACTOR: none needed; verify `make test` and `make lint` pass
- [x] 37.4 Note in the container-hooks page that Refresh and Scope.Start fire no container hooks; verify the docs lint and build pass

## 38. Parallel start (container-start; design P4)

- [x] 38.1 RED: add `TestStart_Parallel` (scenario "Independent Starters start concurrently", 5 rows, `synctest.Test`), `TestStart_ParallelFailure` and `TestStart_ParallelHooks`; verify `go test -race -run 'TestStart_Parallel' .` fails to compile only on `InjectorOpts.StartParallelism`
- [x] 38.2 GREEN: add `StartParallelism` (copied in `copy()`) and the Kahn worker scheduler in `startStarters` for n > 1, keeping the sequential loop for 0 and 1 (design P4); verify the 38.1 command passes with `-race -count=20`, goleak is clean and `make test` is green
- [x] 38.3 REFACTOR: *Extract Function* `startStartersParallel(ctx, candidates, n)` and *Replace Conditional with Polymorphism* only if a third mode appears, otherwise a plain branch; verify `make test` and `make lint` pass
- [x] 38.4 Add `ExampleInjectorOpts_startParallelism`, godoc on the field (hooks may run concurrently), and the parallel-start section of the starter page; verify the example and the docs lint and build pass

## 39. HookAfterBuild (service-build-hooks; design P5)

- [x] 39.1 RED: add `build_hooks_test.go` with `TestAfterBuildHook_Fires` (6 rows), `TestAfterBuildHook_Order`, `TestAfterBuildHook_RegistrationOrder`, `TestAfterBuildHook_Scopes` (2 rows) and `TestAfterBuildHook_Panic`; verify `go test -race -run TestAfterBuildHook_ .` fails to compile only on `HookAfterBuild` and `AddAfterBuildHook`
- [x] 39.2 GREEN: add the field (nil-to-empty init, `copy()`, nil-skipping) and the adder, and fire it in `serviceLazy.build`, `serviceTransient.getInstance` and once in `serviceEager.getInstance` outside provider recovery (design P5); verify the 39.1 command passes and `make test` is green
- [x] 39.3 REFACTOR: *Extract Function* `onAfterBuild(i Injector, name string, instance any)` resolving the scope from the virtual scope, shared by the three wrappers; verify `make test`, `make lint` and `bench` invoke results `~` against 0.2 (`benchstat`)
- [x] 39.4 Add `ExampleRootScope_AddAfterBuildHook` and the build hook in `docs/docs/container/options.md`; verify the example and the docs lint and build pass

## 40. Scope hooks: registration and Scope.Start veto (scope-lifecycle-hooks; design S1)

- [x] 40.1 RED: add `scope_hooks_test.go` with `TestScopeHooks_RegistrationOrder` and `TestScopeStartingHook_Veto` (3 rows); verify `go test -race -run 'TestScopeHooks_RegistrationOrder|TestScopeStartingHook_Veto' .` fails to compile only on the `HookScope*` fields and `AddScope*Hook` methods
- [x] 40.2 GREEN: add the four fields (init, `copy()`, nil-skipping), the adders, and fire Starting (veto, recovered as a veto) and Started in `Scope.Start` for child scopes (design S1); verify the 40.1 command passes and `make test` is green
- [x] 40.3 REFACTOR: *Extract Function* `runScopeStartingHooks(ctx, scope) error`, reusing Part H's `callHookSafely`; verify `make test` and `make lint` pass

## 41. Scope Closing and Closed hooks (scope-lifecycle-hooks; design S1)

- [x] 41.1 RED: add `TestScopeHooks_ClosingClosed` (4 rows) and `TestScopeHooks_RootFiresNone`; verify `go test -race -run 'TestScopeHooks_(ClosingClosed|RootFiresNone)' .` fails on the missing events; record that RootFiresNone passes on arrival and show it can fail by temporarily firing scope hooks for the root's own scope (not committed)
- [x] 41.2 GREEN: fire Closing after the children and before the services, and Closed at the end of `Scope.ShutdownWithContext` for non-root scopes (design S1); verify the 41.1 command passes, the existing `scope_test.go` shutdown tests pass unchanged, and `make test` is green
- [x] 41.3 REFACTOR: none needed if 41.2 adds two calls; verify `make test` and `make lint` pass

## 42. Scope hook panics (scope-lifecycle-hooks; design S1)

- [x] 42.1 RED: add `TestScopeHooks_ObserverPanic` (3 rows); verify `go test -race -run TestScopeHooks_ObserverPanic .` fails because the panic escapes before the scope's services shut down
- [x] 42.2 GREEN: recover each observer scope hook with `callHookSafely` and dispatch the first panic last, through `HookContainerPanic` (points `scope-started`, `scope-closing`, `scope-closed`) (design S1, Part H D6); verify the 42.1 command passes and `make test` is green
- [x] 42.3 REFACTOR: *Extract Function* `runObserverHooksWithPanicPolicy` shared by container and scope observer hooks if they still duplicate the dispatch; verify `make test` and `make lint` pass
- [x] 42.4 Add `ExampleRootScope_AddScopeClosingHook` and a scope-hooks section on the container-hooks page; verify the example and the docs lint and build pass

## 43. RootScope.Phase (container-phase; design S2)

- [x] 43.1 RED: add `phase_test.go` with `TestPhase_Lifecycle` (6 rows), `TestPhase_SeenByHooks`, `TestPhase_RaceFree`, `TestContainerPhase_StringUnknown` and `TestPhase_CloneIdle`; verify `go test -race -run 'TestPhase_|TestContainerPhase_' .` fails to compile only on `ContainerPhase`, the `Phase*` constants and `RootScope.Phase`
- [x] 43.2 GREEN: add `ContainerPhase` with `String()` and `RootScope.Phase()` mapping both phases under `lifecycleMu` (design S2); verify the 43.1 command passes and `make test` is green
- [x] 43.3 REFACTOR: none needed, one mapping function; verify `make test` and `make lint` pass
- [x] 43.4 Add `ExampleRootScope_Phase` and the phase diagram mapping on the container-hooks page; verify the example and the docs lint and build pass

## 44. Deterministic InvokeAs (type-resolution; design S5)

- [x] 44.1 RED: add `TestInvokeAs_Deterministic` (6 rows), `TestInvokeAs_DeterministicStable`, `TestInvokeAs_SingleAndMissing` (2 rows) and `TestInvokeStruct_ImplicitAliasingDeterministic` to `invoke_test.go`; verify `go test -race -count=10 -run 'TestInvokeAs_Deterministic|TestInvokeStruct_ImplicitAliasingDeterministic' .` fails on the multi-match rows (random pick); record that SingleAndMissing passes on arrival
- [x] 44.2 GREEN: select the minimum `(distance, regIndex, name)` candidate in `invokeByGenericType` and in the `invokeByTags` fallback (design S5), removing the two nondeterminism TODOs; verify the 44.1 command passes with `-count=10`, the existing `TestInvokeAs`/`TestInvokeByGenericType*`/`TestInvokeAs_HookReceivesTypeName` pass unchanged, and `make test` is green
- [x] 44.3 REFACTOR: *Extract Function* `selectByType(injector, target reflect.Type) (candidate, bool)` shared by both call sites; verify `make test` and `make lint` pass
- [x] 44.4 Document the selection rule in the `InvokeAs`/`MustInvokeAs`/`InvokeStruct` godoc and the invocation docs, plus an upgrading-page entry (flagged); verify the docs lint and build pass

## 45. Collection struct injection (collection-struct-injection; design S3)

- [x] 45.1 RED: add `invoke_all_struct_test.go` with `TestInvokeStruct_CollectionFields` (4 rows), `TestInvokeStruct_CollectionCustomTag`, `TestInvokeStruct_CollectionInvalidField` (2 rows), `TestInvokeStruct_CollectionMemberFailure` and `TestInvokeStruct_CollectionExcludesSelf`; verify `go test -race -run TestInvokeStruct_Collection .` fails because `*` is looked up as a service name (ErrServiceNotFound)
- [x] 45.2 GREEN: generalize the collection pipeline to a `reflect.Type` target and handle the `*` tag value in `invokeByTags` for slice and `map[string]` fields (design S3); verify the 45.1 command passes, the Part K tests pass unchanged, and `make test` is green
- [x] 45.3 REFACTOR: *Extract Function* `injectCollectionField(injector, field, value)` out of `invokeByTags`, bringing its `gocyclo` down; verify `make test` and `make lint` pass
- [x] 45.4 Add `ExampleInvokeStruct_collection`, the struct-injection docs section, and an upgrading-page entry for the `*` name (BREAKING edge case); verify the example and the docs lint and build pass

## 46. Live collections (live-collections; design S4)

- [x] 46.1 RED: add `collection_test.go` with `TestCollection_LaterRegistrations` (2 rows), `TestCollection_CreationBuildsNothing`, `TestCollection_HeldByProvider`, `TestCollection_ReadOutcomes` (3 rows) and `TestCollection_ConcurrentReads`; verify `go test -race -run TestCollection_ .` fails to compile only on `Collection` and `InvokeCollection`
- [x] 46.2 GREEN: add `Collection[T]` with `All`/`Map` over the captured injector and `InvokeCollection` (design S4); verify the 46.1 command passes and `make test` is green
- [x] 46.3 REFACTOR: none needed if `All`/`Map` only delegate to the Part K pipeline; verify `make test` and `make lint` pass
- [x] 46.4 Add `ExampleInvokeCollection` and the live-collection section of the collection-invocation page (snapshot vs live); verify the example and the docs lint and build pass

## 47. Per-service health checks honor HealthCheckTimeout (health-check-timeouts; design S6)

- [x] 47.1 RED: add `TestHealthCheck_PerServiceTimeout` (6 rows, fake clock in `synctest.Test`) and `TestHealthCheck_PerServiceErrorInTime` to `healthcheck_timeout_test.go`; verify `go test -race -run 'TestHealthCheck_PerService' .` fails on the timeout rows (nil instead of the timeout error)
- [x] 47.2 GREEN: wrap the per-service functions' context with `clockwork.WithTimeout(ctx, clock, HealthCheckTimeout)` when the option is positive (design S6); verify the 47.1 command passes, the existing health-check suites pass unchanged, and `make test` is green
- [x] 47.3 REFACTOR: *Extract Function* `perServiceHealthCheckContext(injector, ctx)`; verify `make test` and `make lint` pass
- [x] 47.4 Document it on the healthchecker page and the options page, plus an upgrading-page entry (flagged); verify the docs lint and build pass

## 48. Integration checks

- [x] 48.1 Compare benchmarks against 0.2 with `benchstat`; verify invoke and provide show `~` and `BenchmarkNew` allocs/op is 11 (otherwise stop and investigate)
- [x] 48.2 Full run: verify `make test`, `make lint`, per-module `go vet ./... && go test -race ./...` in every `go.work` module, and `npm --prefix docs run lint && npm --prefix docs run build` all pass
- [x] 48.3 Traceability: verify `go test -race -v -run '<every test in design.md Test Strategy, new and folded>' .` lists every test and subtest as PASS, with row counts matching the scenarios
- [x] 48.4 Validate the plan; verify `openspec validate consolidate-pending-lifecycle-work --strict` passes

## Workflow follow-up

- Archive this change after review (`/opsx:archive`).
- The folded changes' own follow-ups are all absorbed here; there is no remaining phase-2 list.

## 49. Forget shut-down services in the start bookkeeping (design P1, Risks)

- [x] 49.1 RED: add `root_refresh_test.go` with `TestRootScope_ForgetsShutDownServices` (3 rows: `tenant.Shutdown()`, `ShutdownNamed(tenant, …)`, `root.Shutdown()`), checking that a started Starter and a handled kind component of the shut-down scope are no longer in `started`/`handled`, while a service of a scope that stays up is kept; verify `go test -race -run TestRootScope_ForgetsShutDownServices .` fails because the entries remain
- [x] 49.2 GREEN: drop the wrapper from `started` and `handled` when `serviceShutdown` removes it from its scope; verify the 49.1 command passes, the Part P tests pass unchanged, and `make test` is green
- [x] 49.3 REFACTOR: none needed if the change is one `forget` helper next to `markStarted`/`markHandled`; verify `make test` and `make lint` pass
- [x] 49.4 Re-run the integration checks touched by the change: `make test`, `make lint`, `openspec validate consolidate-pending-lifecycle-work --strict`, and `BenchmarkShutdown` against `ed65bdc` with `benchstat`

## 50. Shutdown sets the cancel cause under the lifecycle lock (container-lifecycle-hooks; design V1)

- [x] 50.1 RED: add `TestContainerHooks_ShutdownCauseRace` (2 rows, 20000 fresh containers each) to `container_hooks_test.go`; verify `go test -race -run TestContainerHooks_ShutdownCauseRace .` fails with `DI: start aborted: %!w(<nil>)`
- [x] 50.2 GREEN: cancel the in-flight run with `ErrStartAfterShutdown` inside `beginClose` while holding `lifecycleMu`, leaving `waitForStart` to wait only; verify the 50.1 command passes, the Part H and Refresh suites pass unchanged, and `make test` is green
- [x] 50.3 REFACTOR: *Rename* `waitForStart`'s role in its godoc only if the name no longer fits; verify `make test` and `make lint` pass

## 51. Parallel start re-raises hook panics on the caller (container-start; design V2)

- [x] 51.1 RED: add `TestStart_ParallelHookPanic` (3 rows) to `root_start_test.go`; verify `go test -race -run TestStart_ParallelHookPanic .` crashes the test binary with `panic: hook-boom` created by `startStartersParallel`
- [x] 51.2 GREEN: recover the per-service hook panic in the parallel worker, report it to the coordinator, stop dispatching, drain the running workers and re-panic the first value; verify the 51.1 command passes and `make test` is green
- [x] 51.3 REFACTOR: *Extract Function* for the worker body if `startStartersParallel` grows past the gocyclo limit; verify `make test` and `make lint` pass

## 52. Incremental start fails fast while Start runs (container-start; design V3)

- [x] 52.1 RED: add `TestIncrementalStart_DuringStart` (3 rows) to `root_refresh_test.go`; verify `go test -race -run TestIncrementalStart_DuringStart .` fails: the synchronous rows time out and the goroutine row gets nil instead of `ErrNotStarted`
- [x] 52.2 GREEN: check the phase under `lifecycleMu` before `pipelineMu` in `startIncremental`, keeping the re-check in `beginIncremental`; verify the 52.1 command passes, `TestIncrementalStart_Phase` and the Refresh suites pass unchanged, and `make test` is green
- [x] 52.3 REFACTOR: *Extract Function* `checkIncrementalPhase()` shared by the pre-check and `beginIncremental`; update the `Refresh`/`Scope.Start` godoc and the starter page; verify `make test`, `make lint` and the docs lint and build pass

## 53. Child scope shutdown waits for a start that is starting it (container-start; design V4)

- [x] 53.1 RED: add `TestScopeShutdown_WaitsForStart` (3 rows), `TestScopeShutdown_LaterStartSkips`, `TestScopeShutdown_CreatedDuringStart` and `TestScopeShutdown_WaitBounded` to `root_refresh_test.go`; verify `go test -race -run TestScopeShutdown_ .` fails on WaitsForStart, LaterStartSkips and WaitBounded, and record that CreatedDuringStart passes on arrival
- [x] 53.2 GREEN: record `runScopes` under `lifecycleMu` after discovery (dropping closing scopes), mark a child scope closing under `lifecycleMu` at shutdown and wait, bounded by its context, for an in-flight run that has it in `runScopes`; verify the 53.1 command passes, `TestRootScope_ForgetsShutDownServices` and the scope-hook suites pass unchanged, and `make test` is green
- [x] 53.3 REFACTOR: *Extract Function* `beginClosing(ctx)`/`endClosing()` out of `shutdownWithContext`; verify `make test` and `make lint` pass
- [x] 53.4 Document the wait and its deadlock rule on `Scope.Shutdown`/`ShutdownWithContext`, the starter page and an upgrading-page entry (flagged); verify the docs lint and build pass

## 54. Live-collection reads after the provider returned (live-collections; design V5)

- [x] 54.1 RED: add `TestCollection_AfterProviderReturned` (2 rows) to `collection_test.go` and change `TestCollection_HeldByProvider`'s explain assertion to the updated scenario (no edges recorded by reads after the provider returned); verify `go test -race -run TestCollection_ .` fails with a circular-dependency error and with the recorded edges
- [x] 54.2 GREEN: mark a provider's virtual scope returned once `getInstance` returned, and resolve reads on a returned scope from its underlying scope, excluding only the holder, without recording edges; verify the 54.1 command passes, the Part K tables pass unchanged, and `make test` is green
- [x] 54.3 REFACTOR: *Introduce Parameter Object* for the collection pipeline's exclusion and edge options if it takes more than three parameters; update the `InvokeCollection` godoc and the collection page; verify `make test`, `make lint` and the docs lint and build pass

## 55. The `*` tag requires exactly map[string] (collection-struct-injection; design V6)

- [x] 55.1 RED: add the string-kinded key row to `TestInvokeStruct_CollectionInvalidField`; verify `go test -race -run TestInvokeStruct_CollectionInvalidField .` fails because no error is returned
- [x] 55.2 GREEN: compare the map key type with `reflect.TypeFor[string]()`; verify the 55.1 command passes and `make test` is green
- [x] 55.3 REFACTOR: none needed for a one-line check; verify `make test` and `make lint` pass

## 56. Missing incremental-start tests (container-start; design V7, V8)

- [x] 56.1 Add `TestStartError_Incremental` (3 rows), `TestScopeStart_AncestorsNotStarted` (to `root_refresh_test.go`) and `TestStart_ParallelIncremental` (2 rows, to `root_start_test.go`); verify they pass, record the pass on arrival, and show each fails under a temporary mutation of the code it covers (reverted)

## 57. Build hooks run outside the wrapper lock (service-build-hooks; design V10)

- [x] 57.1 RED: add `TestAfterBuildHook_ReentersService` (3 rows) to `build_hooks_test.go`; verify `go test -race -run TestAfterBuildHook_ReentersService .` fails by timing out on the lazy rows
- [x] 57.2 GREEN: fire `HookAfterBuild` after the lazy wrapper releases `s.mu` and after the eager wrapper releases its dependency lock; verify the 57.1 command passes, `TestAfterBuildHook_*` pass unchanged, and `make test` is green
- [x] 57.3 REFACTOR: *Extract Function* if the lazy `getInstance` gains a second unlock path; update the `HookAfterBuild` godoc; verify `make test` and `make lint` pass

## 58. DependencySource decodes from text (dependency-declaration; design V11)

- [x] 58.1 RED: add `TestDependencySource_UnmarshalText` (4 rows) to `dependency_test.go`; verify `go test -race -run TestDependencySource_UnmarshalText .` fails to decode the string into a `DependencySource`
- [x] 58.2 GREEN: add `DependencySource.UnmarshalText`; verify the 58.1 command passes and `make test` is green
- [x] 58.3 REFACTOR: *Extract Variable* a shared text table if `String` and `UnmarshalText` repeat the names; verify `make test` and `make lint` pass

## 59. Override forgets the replaced wrapper (design V12)

- [x] 59.1 RED: add `TestRootScope_ForgetsOverriddenServices` (5 overrides plus Refresh) to `root_refresh_test.go`; verify `go test -race -run TestRootScope_ForgetsOverriddenServices .` fails with 6 started entries instead of 1
- [x] 59.2 GREEN: drop the replaced wrapper from `started` and `handled` where registration replaces it; verify the 59.1 command passes and `make test` is green
- [x] 59.3 REFACTOR: none needed if it reuses `forget`; verify `make test` and `make lint` pass

## 60. StartParallelism is clamped to the number of Starters (container-start; design V13)

- [x] 60.1 RED: add the max-uint row to `TestStart_Parallel`; verify `go test -race -run TestStart_Parallel/18446744073709551615 .` fails because no Starter is started
- [x] 60.2 GREEN: clamp the parallelism to the number of Starters before the `int` conversion and drop the `nolint`; verify the 60.1 command passes and `make test` is green
- [x] 60.3 REFACTOR: none needed; verify `make test` and `make lint` pass

## 61. InvokeAs honors shadowing (type-resolution; design V14)

- [x] 61.1 RED: add `TestInvokeAs_Shadowing` (3 rows) to `invoke_test.go`; verify `go test -race -run TestInvokeAs_Shadowing .` fails on the two shadowed rows
- [x] 61.2 GREEN: skip a candidate whose name is registered in a scope between the invoking scope and its own; verify the 61.1 command passes, `TestInvokeAs_*` and `TestInvokeStruct_*` pass unchanged, and `make test` is green
- [x] 61.3 REFACTOR: *Extract Function* `shadowedBetween(from, to *Scope, name string) bool`; verify `make test` and `make lint` pass
- [x] 61.4 Document shadowing in the `InvokeAs`/`InvokeStruct` godoc, the invocation docs and the upgrading-page entry (flagged); re-measure `BenchmarkInvokeAs` against `ed65bdc` and record it in notes.md; verify the docs lint and build pass

## 62. Health-check timeout in one place (design V15)

- [x] 62.1 REFACTOR: *Replace Inline Code with Function Call* - `queueServiceHealthcheck` uses `perServiceHealthCheckContext`; verify `make test` (health-check suites unchanged) and `make lint` pass

## 63. One veto loop and one observer loop for container and scope hooks (design V16)

- [x] 63.1 REFACTOR: *Extract Function* a generic veto loop taking the panic-wrapping function and use it from `runStartingHooks` and `runScopeStartingHooks`; verify `make test` (messages unchanged) and `make lint` pass
- [x] 63.2 REFACTOR: *Extract Function* a generic observer loop and use it from `runObserverHooks` and `runScopeObserverHooks`; verify `make test` and `make lint` pass

## 64. Integration checks after the verification fixes

- [x] 64.1 Full run: `make test`, `make lint`, per-module `go vet ./... && go test -race ./...` in every `go.work` module, `npm --prefix docs run lint && npm --prefix docs run build`, and `openspec validate consolidate-pending-lifecycle-work --strict`
- [x] 64.2 Traceability: run every test named in the design's Test Strategy rows for Part V with `-race -v` and verify each lists PASS with row counts matching the scenarios
- [x] 64.3 Benchmarks: compare `BenchmarkInvokeAs`, `BenchmarkInvokeAlias`, `BenchmarkProvide`, `BenchmarkNew`, `BenchmarkShutdown` and the invoke benchmarks against `ed65bdc` with `benchstat` (20 interleaved runs) and record them in notes.md

## 65. Live-collection reads keep the still-running chain (live-collections; design V17)

- [x] 65.1 RED: add `TestCollection_ReadDuringOuterProvider` to `collection_test.go`; verify `go test -race -run TestCollection_ReadDuringOuterProvider .` times out on the deadlock
- [x] 65.2 GREEN: link each provider's virtual scope to its invoker's, and resolve a read on a returned scope through a detached virtual scope that keeps the still-running providers, records no edge and excludes only the holder; verify the 65.1 command passes, `TestCollection_*` pass unchanged, and `make test` is green
- [x] 65.3 REFACTOR: *Extract Function* `runningChain(vScope)`; verify `make test` and `make lint` pass

## 66. Start failures after Shutdown began carry the abort cause (container-lifecycle-hooks, container-start; design V18)

- [x] 66.1 RED: add the errVeto row to `TestContainerHooks_HookCallsShutdownWithDeadline`, `TestContainerHooks_ShutdownInterruptsLateStep` (3 rows) and `TestScopeStartingHook_ShutdownCause`; verify `go test -race -run 'TestContainerHooks_HookCallsShutdownWithDeadline|TestContainerHooks_ShutdownInterruptsLateStep|TestScopeStartingHook_ShutdownCause' .` fails because the errors do not match `ErrStartAfterShutdown`
- [x] 66.2 GREEN: wrap the Starting-hook vetoes, the deferred kind-binding error and the cycle error with `withAbortCause`; verify the 66.1 command passes, the Part H tables pass unchanged, and `make test` is green
- [x] 66.3 REFACTOR: none needed if each site is one wrap; verify `make test` and `make lint` pass

## 67. Scope hooks get a non-nil context (scope-lifecycle-hooks; design V20)

- [x] 67.1 RED: add `TestScopeHooks_NilContext` (2 rows) to `scope_hooks_test.go`; verify `go test -race -run TestScopeHooks_NilContext .` fails on the shutdown row and record whether the start row passes on arrival
- [x] 67.2 GREEN: normalize a nil context at the top of `Scope.ShutdownWithContext` (and `Scope.StartWithContext` if needed); verify the 67.1 command passes and `make test` is green
- [x] 67.3 REFACTOR: drop the now redundant nil check in `beginClosing`; verify `make test` and `make lint` pass

## 68. Bookkeeping skips services that left their scope (container-start; design V21)

- [x] 68.1 RED: extend `TestScopeShutdown_WaitBounded` with the bookkeeping assertion; verify `go test -race -run TestScopeShutdown_WaitBounded .` fails because the shut-down wrapper is in `started`
- [x] 68.2 GREEN: mark started and handled only while the candidate's scope still holds its wrapper; verify the 68.1 command passes and `make test` is green
- [x] 68.3 REFACTOR: *Extract Function* `stillRegistered(candidate)`; verify `make test` and `make lint` pass

## 69. A scope's shutdown covers its descendants (container-start; design V22)

- [x] 69.1 RED: add `TestScopeShutdown_DescendantSkipped` to `root_refresh_test.go`; verify `go test -race -run TestScopeShutdown_DescendantSkipped .` fails because "w" was started
- [x] 69.2 GREEN: drop candidates whose scope or any ancestor is closing in `claimCandidates`; verify the 69.1 command passes, `TestScopeShutdown_*` pass unchanged, and `make test` is green
- [x] 69.3 REFACTOR: *Extract Function* `closingInLine(scope)`; update the `Scope.ShutdownWithContext` godoc and the starter page; verify `make test`, `make lint` and the docs lint and build pass

## 70. Test corrections (design V19)

- [x] 70.1 Fix the `TestRefresh_ShutdownCancels` flake with `synctest.Wait()`; verify `go test -race -count=20000 -run '^TestRefresh_ShutdownCancels$' .` passes
- [x] 70.2 Require the events before the `indexOf` comparisons in `invoke_all_test.go` and `invoke_all_order_test.go`, assert non-nil in `TestInvokeAll_AfterShutdown`, and create the collection before the shutdown in `TestCollection_ReadOutcomes`; verify the tests pass and each strengthened check fails under a temporary mutation (reverted)
- [x] 70.3 Assert the phase reached after each row of `TestContainerHooks_ObserverPanicDefault`; verify it passes
- [x] 70.4 Switch the eager fixtures to lazy where the spec says lazy (`TestPhase_Lifecycle`, `TestPhase_RaceFree`, `TestScopeHooks_ObserverPanic`, `TestStartError_Fields`, `TestStartError_Incremental`, `TestStart_ParallelFailure`, `TestRefresh_ShutdownCancels`) and assert the failed row's error in `TestPhase_Lifecycle`; verify they pass
- [x] 70.5 Add `TestCollection_ReadDuringHolderProvider` and an `InvokeCollection(nil)` row; make `TestScopeStart_RootScope` use a provider's scope; verify they pass, and that the new tests fail under a temporary mutation (reverted)

## 71. Integration checks after the second verification

- [x] 71.1 Full run: `make test`, `make lint`, per-module `go vet ./... && go test -race ./...` in every `go.work` module, the docs lint and build, and `openspec validate consolidate-pending-lifecycle-work --strict`
- [x] 71.2 Stress: run the Part V and V2 lifecycle tests with `-race -count=2000`; verify no failure

## 72. Each invocation copies its invoker chain (live-collections; design V23)

- [x] 72.1 RED: add `TestCollection_HolderAfterSiblings` to `collection_test.go`; verify `go test -race -run TestCollection_HolderAfterSiblings .` fails because the map holds "d" and lacks "e"
- [x] 72.2 GREEN: extend the chain with `append(slices.Clip(chain), name)` in `invokeAnyByNameAs` and `invokeByName`; verify the 72.1 command passes and `make test` is green
- [x] 72.3 REFACTOR: none needed (V36 replaces the slice); verify `make test` and `make lint` pass

## 73. An invocation is over once it returned (live-collections; design V24)

- [x] 73.1 RED: add `TestCollection_InvocationsWithoutProvider` (2 rows) to `collection_test.go`; verify `go test -race -run TestCollection_InvocationsWithoutProvider .` fails with `ErrCircularDependency` on both rows
- [x] 73.2 GREEN: mark the virtual scope `returned` once `getInstance` returns, at the three sites that create one; verify the 73.1 command passes, `TestCollection_*` pass unchanged, and `make test` is green
- [x] 73.3 REFACTOR: *Extract Function* for the create-invoke-mark sequence if the three sites repeat it; verify `make test` and `make lint` pass

## 74. A failure returned after Shutdown began carries the abort cause (container-start; design V25)

- [x] 74.1 RED: add `TestStart_FailureThenShutdown` (2 rows) to `root_start_test.go`; verify `go test -race -run TestStart_FailureThenShutdown .` fails because the errors do not match `ErrStartAfterShutdown`
- [x] 74.2 GREEN: in `finishStart` and `finishIncremental`, wrap a failure that does not match the abort cause with `withAbortCause` once the close phase is not open; verify the 74.1 command passes, the Part H and V tables pass unchanged, and `make test` is green
- [x] 74.3 REFACTOR: *Extract Function* the shared "abort outcome" rule of `finishStart` and `finishIncremental`; verify `make test` and `make lint` pass

## 75. StartError tolerates nil fields (container-start; design V26)

- [x] 75.1 RED: add `TestStartError_ConsumerBuilt` (4 rows) to `errors_test.go`; verify `go test -race -run TestStartError_ConsumerBuilt .` fails with a nil pointer panic
- [x] 75.2 GREEN: read a nil `Scope` as an empty name and a nil `Cause` as `<nil>` in `Error()`; verify the 75.1 command passes, `TestStartError_*` pass unchanged, and `make test` is green
- [x] 75.3 REFACTOR: *Extract Function* `scopeName()` and `causeText()`; verify `make test` and `make lint` pass

## 76. InvokeAs skips the invoking service (type-resolution; design V27)

- [x] 76.1 RED: add `TestInvokeAs_SkipsInvoker` (3 rows) and `TestInvokeStruct_FallbackSkipsInvoker` to `invoke_test.go`; verify `go test -race -run 'TestInvokeAs_SkipsInvoker|TestInvokeStruct_FallbackSkipsInvoker' .` fails with `ErrCircularDependency`
- [x] 76.2 GREEN: skip the name `invokingServiceName` returns in `selectByType`; verify the 76.1 command passes, `TestInvokeAs_*` and `TestInvokeStruct_*` pass unchanged, and `make test` is green
- [x] 76.3 REFACTOR: none needed if it is one check; update the `InvokeAs` godoc and the upgrading page (flagged); verify `make test`, `make lint` and the docs lint and build pass

## 77. A pipeline panic runs Failed hooks before it is re-raised (container-lifecycle-hooks; design V28)

- [x] 77.1 RED: add `TestContainerFailedHook_PipelinePanic` (3 rows) and `TestContainerFailedHook_PanicDuringPipelinePanic` (2 rows) to `container_hooks_test.go`; verify `go test -race -run 'TestContainerFailedHook_Pipeline|TestContainerFailedHook_PanicDuring' .` fails because the Failed hook did not run
- [x] 77.2 GREEN: recover the pipeline panic in `StartWithContext`, run the Failed hooks with `recoverAsError(value)`, route a Failed hook panic to `HookContainerPanic` or `Logf`, then re-panic; verify the 77.1 command passes, `TestStart_ParallelHookPanic` passes unchanged, and `make test` is green
- [x] 77.3 REFACTOR: *Extract Function* the panic branch of `StartWithContext`; update the `StartWithContext` and `HookContainerFailed` godoc, the container-hooks page and the upgrading page (flagged); verify `make test`, `make lint` and the docs lint and build pass

## 78. A failed Starter or handler is tried again (container-start, component-kinds; design V29)

- [x] 78.1 Add `TestRefresh_RetriesFailedStarter` (2 rows) to `root_refresh_test.go`, `TestKind_RetriesFailedHandler` and `TestKind_ScopeStartSubtree` to `kind_test.go`; verify they pass on arrival, and that each fails under a temporary mutation (reverted) recorded in notes.md
- [x] 78.2 Document the retry in the `RefreshWithContext`, `Scope.StartWithContext` and `Kind.Handle` godoc and the starter page; verify `make test`, `make lint` and the docs lint and build pass

## 79. Scope Started hooks skip a scope that is shutting down (scope-lifecycle-hooks; design V30)

- [x] 79.1 RED: add `TestScopeStartedHook_SkippedWhileClosing` (2 rows) to `scope_hooks_test.go`; verify `go test -race -run TestScopeStartedHook_SkippedWhileClosing .` fails because the Started hook ran
- [x] 79.2 GREEN: report from `runPipeline` whether its roots were closing at claim time, pass it up from `startIncremental`, and skip `HookScopeStarted` then; verify the 79.1 command passes, `TestScopeShutdown_*` pass unchanged, and `make test` is green
- [x] 79.3 REFACTOR: none needed if the flag is one return value; update the `HookScopeStarted` godoc; verify `make test` and `make lint` pass

## 80. Messages (collection-ordering, scope-lifecycle-hooks; design V31)

- [x] 80.1 RED: assert the exact `Order()` panic message in `TestInvokeAll_OrderPanic`, add the scope Starting panic rows to `TestScopeStartingHook_Veto`, and add `TestScopeHooks_LaterPanicLogged` to `scope_hooks_test.go`; verify `go test -race -run 'TestInvokeAll_OrderPanic|TestScopeStartingHook_Veto|TestScopeHooks_LaterPanicLogged' .` fails on the three messages
- [x] 80.2 GREEN: wrap the `Order()` panic without `recoverAsError`, give `startingHookPanicError` the subject, and name the point in the later-panic log line; verify the 80.1 command passes and `make test` is green
- [x] 80.3 REFACTOR: none needed; verify `make test` and `make lint` pass

## 81. Test strengthening (design V32)

Each test below that passes on arrival is checked with a temporary mutation (reverted), recorded in notes.md.

- [x] 81.1 `TestRefresh_ShutdownCancels`: assert that Shutdown returns only after the cancelled Refresh returned (1s of bubble time); verify it fails when `beginClose` stops waiting for incremental runs
- [x] 81.2 Give `buildReq` and `buildDB` a field; verify the "same instance" rows of `TestAfterBuildHook_*` fail when the transient wrapper passes another instance to the hook
- [x] 81.3 Add `TestExplain_EdgeSourceStrongest` (2 rows: declared then invoked; depends-on and declared on one edge) to `dependency_test.go`, and pre-set a non-zero `Source` in the `invoked` row of `TestDependencySource_UnmarshalText`; verify they fail when the DAG keeps the first source, and when `UnmarshalText` never assigns invoked
- [x] 81.4 Add `TestContainerHooks_ObserverContexts` (5 rows) to `container_hooks_test.go`, and assert exact counts in `TestContainerHooks_NilContext`; verify they fail when the observer hooks receive `context.Background()`
- [x] 81.5 Collections: `InvokeAllMap` failure, self-exclusion, edges and alias exclusion; `WithOrder` and `Ordered` on a `do:"*"` slice field; built members stay built after a failure; caller mutation of a map; construction order ignores `WithOrder`; the results of `TestCollection_ConcurrentReads`
- [x] 81.6 Kinds: `TestKind_CloneReplaceIsolated` covers the clone row and the slot order (component-kinds "Replacing a handler on the clone leaves the source unchanged")
- [x] 81.7 Hooks:
  - the `scope-closing` point with a recording policy;
  - `Same` and per-row step errors in `TestContainerHooks_ShutdownInterruptsLateStep`;
  - a child scope shut down through a provider's injector fires no container hook;
  - `PhaseClosing` while Shutdown waits for Start;
  - the default-policy `HookScopeStarted` panic re-raised after Scope.Start finished.
- [x] 81.8 Start:
  - an explicit not-a-Starter assertion in `TestStart_DependencyOrder`;
  - a child scope created after Start is not started by it;
  - `errors.As` and `Op` for a recovered Start panic, in sequential and parallel mode;
  - `MustInvokeAs` determinism;
  - build-hook panics for eager and transient services;
  - a typed-nil instance passed to the build hook as is;
  - shadowing through an intermediate scope.
- [x] 81.9 Verify `make test` and `make lint` pass

## 82. Documentation (design V33)

- [x] 82.1 Document the two live-collection hazards on `Collection` and the collections page
- [x] 82.2 Fix the `RootScope.StartWithContext` godoc on child scopes and wrap `root_start.go:56`. Name the `scope-*` points in the `HookContainerPanic` godoc, and the panic policy on the scope observer fields. State on the `Add*Hook` methods that lists are copied before they run and that the adders are configuration-time only, and on the container adders that a nil hook is ignored. Mirror both in `container-hooks.md`
- [x] 82.3 Upgrading page: the unkeyed-literal break, the explain output change, and the V27 and V28 behavior changes
- [x] 82.4 Verify the docs lint and build pass

## 83. Examples (design V34)

- [x] 83.1 Add `ExampleRootScope_AddScopeStartingHook`, `ExampleRootScope_AddScopeStartedHook`, `ExampleRootScope_Refresh_notStarted`, `ExampleMustInvokeAllMap` and `ExampleDependencySource_UnmarshalText`; verify `go test -race -run Example .` passes

## 84. Code-review cleanups (design V36; refactors, behavior unchanged)

Each step runs `make test` and `make lint` after it; no test input or assertion changes, except the white-box chain accessor in 84.6.

- [x] 84.1 *Remove Dead Code*: the non-Starter check in `startStarters`
- [x] 84.2 *Replace Temp with Query* / *Inline Variable*: `collectionMember.order` with `slices.SortStableFunc` in `invokeCollection`; drop the no-op key `Convert` in `injectCollectionField`
- [x] 84.3 *Parameterize Function*: one observer loop with a call adapter, used by the Failed and Closed loops
- [x] 84.4 *Extract Function* `placement.compare` for the three default-order comparators; `Scope.Ancestors` calls `scopeDepth`
- [x] 84.5 *Substitute Algorithm*: `root.kinds` starts as the shared `defaultKinds` slice; `Kind.Handle` copies before writing; `runStage` and `Handle` lose their Runnable special cases; verify `BenchmarkNew` stays at 11 allocs/op
- [x] 84.6 *Replace Data Value with Object*: the virtual scope stores its name and invoker link, and the chain is derived by walking up; scopes built from a chain keep it as a base. Run the bench suite against `52bb53d` with `benchstat` (20 runs) and record it in notes.md

## 85. Integration checks after the third verification

- [x] 85.1 Full run: `make test`, `make lint`, per-module `go vet ./... && go test -race ./...` in every `go.work` module, the docs lint and build, and `openspec validate consolidate-pending-lifecycle-work --strict`
- [x] 85.2 Stress: run the Part V, V2 and V3 lifecycle and collection tests with `-race -count=2000`; verify no failure

## 86. A run skips services shut down before it claims them (container-start, scope-lifecycle-hooks; design V37)

- [x] 86.1 RED: add `TestStart_ShutdownBetweenCollectAndClaim` (4 rows) to `root_start_test.go`, pausing collection with a white-box wrapper whose `getServiceType` blocks, and `TestScopeStartedHook_ShutdownBeforeClaim` to `scope_hooks_test.go`; verify `go test -race -run 'TestStart_ShutdownBetweenCollectAndClaim|TestScopeStartedHook_ShutdownBeforeClaim' .` fails with a build error matching `ErrServiceNotFound`, and with the root "w" started twice
- [x] 86.2 GREEN: in `claimCandidates`, also drop candidates for which `stillRegistered` is false; verify the 86.1 command passes, `TestScopeShutdown_*` pass unchanged, and `make test` is green
- [x] 86.3 REFACTOR: none needed: the claim predicate is one line with a comment; verify `make test` and `make lint` pass

## 87. An aborted run matches ErrStartAfterShutdown when the caller cancelled first (container-start; design V38)

- [x] 87.1 RED: add `TestStart_CallerCancelThenShutdown` (6 rows) to `root_start_test.go` and `TestAbortOutcome` (6 rows, white-box) to `root_start_test.go`; verify `go test -race -run 'TestStart_CallerCancelThenShutdown|TestAbortOutcome' .` fails because the errors do not match `ErrStartAfterShutdown`
- [x] 87.2 GREEN: `abortOutcome` wraps an abort cause that always matches `ErrStartAfterShutdown`, and adds only `ErrStartAfterShutdown` to an error that already carries the caller's cause; verify the 87.1 command passes, `TestStart_FailureThenShutdown` and the Part H tables pass unchanged, and `make test` is green
- [x] 87.3 REFACTOR: *Extract Function* `shutdownCause(ctx)`; update the `StartWithContext` godoc and the upgrading page (flagged); verify `make test`, `make lint` and the docs lint and build pass

## 88. The Failed error after a pipeline panic carries the abort cause (container-lifecycle-hooks; design V39)

- [x] 88.1 RED: add `TestContainerFailedHook_PipelinePanicAfterShutdown` (2 rows) to `container_hooks_test.go`; verify `go test -race -run TestContainerFailedHook_PipelinePanicAfterShutdown .` fails because the Failed error does not match `ErrStartAfterShutdown`
- [x] 88.2 GREEN: `runStart` recovers the pipeline panic into `recoverAsError(value)` before its deferred `finishStart`, which applies the abort outcome to it; `StartWithContext` passes that error to the Failed hooks and re-panics with the value; verify the 88.1 command passes, `TestContainerFailedHook_Pipeline*` and `TestStart_ParallelHookPanic` pass unchanged, and `make test` is green
- [x] 88.3 REFACTOR: *Inline Function* `runStartRecovering`; *Remove Parameter* `panicked` from `finishStart`; verify `make test` and `make lint` pass

## 89. InvokeAs skips aliases of the invoking service (type-resolution; design V40)

- [x] 89.1 RED: add the three alias rows to `TestInvokeAs_SkipsInvoker` and `TestInvokeStruct_FallbackSkipsInvokerAlias` to `invoke_test.go`; verify `go test -race -run 'TestInvokeAs_SkipsInvoker|TestInvokeStruct_FallbackSkipsInvokerAlias' .` fails with `ErrCircularDependency`
- [x] 89.2 GREEN: in `selectByType`, skip an alias match whose target chain resolves to the invoking service's name, following targets through each alias's scope and stopping on a loop; verify the 89.1 command passes, `TestInvokeAs_*` and `TestInvokeStruct_*` pass unchanged, and `make test` is green
- [x] 89.3 REFACTOR: none needed (`aliasResolvesTo` was written as its own function in GREEN, with `TestAliasResolvesTo_Loop` for its loop bound); update the `InvokeAs` godoc and the upgrading page (flagged); verify `make test`, `make lint` and the docs lint and build pass

## 90. Scope Starting hooks skip a scope that is shutting down (scope-lifecycle-hooks; design V41)

- [x] 90.1 RED: add `TestScopeStartingHook_SkippedWhileClosing` (2 rows) to `scope_hooks_test.go`; verify `go test -race -run TestScopeStartingHook_SkippedWhileClosing .` fails because the Starting hook ran
- [x] 90.2 GREEN: in `startIncremental`, for a child scope, return closing without running hooks or the pipeline when `closingInLine` holds under `lifecycleMu`; verify the 90.1 command passes, `TestScopeStartedHook_SkippedWhileClosing` passes unchanged, and `make test` is green
- [x] 90.3 REFACTOR: *Extract Function* `scopeClosing`; update the `HookScopeStarting` and `Scope.StartWithContext` godoc, `container-hooks.md` and the upgrading page (flagged); verify `make test`, `make lint` and the docs lint and build pass

## 91. Tests that could not fail (design V42)

Each test below that passes on arrival is checked with the named temporary mutation (reverted), recorded in notes.md.

- [x] 91.1 Add `TestIncrementalStart_CallerContext` (6 rows) to `root_refresh_test.go`; verify it fails when `RefreshWithContext` or `Scope.StartWithContext` pass `context.Background()` instead of ctx, and when their nil-ctx normalization is dropped
- [x] 91.2 `TestContainerHooks_ObserverContexts`: assert the Started, Ready and Failed hooks receive the caller's context itself, not cancelled; verify it fails when `dispatchAfterStart` receives the Start-time context
- [x] 91.3 Collections (with spec scenarios in collection-ordering and live-collections): a child member registered before a root member (fails under `depth: 0`); 13 equal-order members (fails under `slices.SortFunc`); `InvokeStruct` `do:"e"` and `InvokeAs` mark their invocation over (fail when `child.returned.Store(true)` is removed in `invokeAnyByNameAs` and `invokeServiceInstance`); a detached read records no edge (fails without `if s.detached { return }`) and keeps the running chain through `parent` (fails without `read.parent = vScope.parent`)
- [x] 91.4 `InvokeAs`: the alias row of `TestInvokeAs_Deterministic` (fails when aliases are filtered); the clone row "b then a, cloned" (fails when `serviceLazy.clone` drops `regIndex`)

## 92. The cycle message of a live-collection read names the holder (live-collections; design V47)

- [x] 92.1 RED: assert the message in `TestCollection_ReadDuringOuterProvider`; verify `go test -race -run TestCollection_ReadDuringOuterProvider .` fails on `` `app` -> `app` ``
- [x] 92.2 GREEN: the detached read keeps the holder, and `invokerChain` appends it after the running chain; verify the 92.1 command passes, `TestCollection_*` pass unchanged, and `make test` is green
- [x] 92.3 REFACTOR: none needed; verify `make test` and `make lint` pass

## 93. Observer hook panics carry their subject (design V48; refactor, behavior unchanged)

- [x] 93.1 *Replace Type Code with Field*: `hookPanic` stores its subject, set by the observer loop; `logHookPanic` formats from it, without parsing the point; verify `make test` and `make lint` pass with no test change

## 94. Suggestion tests (design V45)

Each test that passes on arrival is checked with a temporary mutation (reverted), recorded in notes.md.

- [x] 94.1 `TestStart_ParallelHookPanic` "n=4, HookAfterStart": "b" returns at once while "a" and "c" sleep; verify it fails when the coordinator re-panics without waiting for running Starters
- [x] 94.2 Add `TestKind_HandledGuard` for the V21 guard in `runStage`; verify it fails without `if stillRegistered(component)`
- [x] 94.3 Add parallel dependency-order rows for Refresh and Scope.Start; verify they fail when the parallel scheduler ignores requirements
- [x] 94.4 `TestInvokeAs_StoredInjectorSkipsHolder` pins the 95.3 godoc claim; `assert.Same` in the eager and lazy build-hook rows; `MustInvokeAs` from a provider; an edge recorded both as depends-on and invoked; `PhaseStarted.String()` and `PhaseClosing.String()`; `TestScopeHooks_LaterPanicLogged` starts the root, as its scenario says; per-member `HookBeforeInvocation` for collections
- [x] 94.5 Verify `make test` and `make lint` pass

## 95. Documentation (design V43, V44, V46)

- [x] 95.1 proposal.md: F4, What Changes (V27, V28, V38–V41 flagged), assumption 6
- [x] 95.2 design.md: the flagged list, X4, V23, Q1 and the Test Strategy row counts
- [x] 95.3 Godoc: `HookBeforeStart`, `HookAfterStart`, `AddBeforeStartHook`, `AddAfterStartHook` (Start, Refresh and Scope.Start), `AddAfterBuildHook` (configuration-time), `InvokeAs` (a stored injector still skips its holder)
- [ ] 95.4 Upgrading page: the latest `InvokeAs` measurement replaces "+9%"; verify the docs lint and build pass

## 96. Examples (design V44)

- [ ] 96.1 Add `ExampleInvokeAs_decorator`, `ExampleMustInvokeAll`, `ExampleRootScope_AddContainerStartedHook`, `ExampleRootScope_AddContainerClosingHook`, `ExampleRootScope_AddContainerClosedHook`, `ExampleRootScope_AddScopeClosedHook`, `ExampleRootScope_RefreshWithContext` and `ExampleScope_StartWithContext`; verify `go test -race -run Example .` passes

## 97. Integration checks after the fourth verification

- [ ] 97.1 Full run: `make test`, `make lint`, per-module `go vet ./... && go test -race ./...` in every `go.work` module, the docs lint and build, and `openspec validate consolidate-pending-lifecycle-work --strict`
- [ ] 97.2 Stress: run the lifecycle and collection tests with `-race -count=2000 -timeout 110m`; verify no failure
- [ ] 97.3 Benchmarks: `benchstat` against `ed65bdc` (10 runs, alternating the two test binaries; the base built from a temporary worktree with `GOWORK=off`), recorded in notes.md
