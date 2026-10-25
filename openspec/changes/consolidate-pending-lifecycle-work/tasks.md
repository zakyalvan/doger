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

- [ ] 15.1 RED: add `TestContainerHooks_ConcurrentStart` (scenario "Concurrent Start calls"), `TestContainerHooks_ConcurrentShutdown` (scenario "Repeated and concurrent Shutdown calls", 2 rows) and `TestContainerHooks_ReentrantStart` (scenario "Hook calling Start re-entrantly"), all with `testWithTimeout`; verify `go test -race -count=5 -run 'TestContainerHooks_(ConcurrentStart|ConcurrentShutdown|ReentrantStart)' .`; they pass on arrival through the start-lifecycle CAS guards, so record that and show they can fail by temporarily replacing the close-phase CAS with an unconditional store (not committed)
- [ ] 15.2 GREEN: no production change expected; fix any race or double firing the 15.1 run reports; verify the 15.1 command passes and goleak reports no leaks
- [ ] 15.3 REFACTOR: none expected; confirm with a `gopls` call-hierarchy review of `lifecycleMu.Lock` that no lock is held while hooks run; verify `make test` and `make lint` pass

## 16. Only the root container fires container hooks (Part H)

- [ ] 16.1 RED: add `TestContainerHooks_ChildScopeShutdown` (scenario "Child scope shutdown fires nothing") and `TestContainerHooks_NoHooksUnchanged` (scenario "No hooks registered leaves behavior unchanged", 3 rows); verify `go test -race -run 'TestContainerHooks_(ChildScopeShutdown|NoHooksUnchanged)' .`; they pass on arrival, so record that and show they can fail by temporarily firing Closing from `Scope.ShutdownWithContext` and by making a nil `HookContainerStarted` slice panic (not committed)
- [ ] 16.2 GREEN: no production change expected; verify the 16.1 command passes and `make test` is green
- [ ] 16.3 REFACTOR: none needed; verify `make test` and `make lint` pass
- [ ] 16.4 Add the "root only" section to the docs page (child `Scope`/`virtualScope` shutdown bypasses container hooks); verify `npm --prefix docs run build` succeeds

## 17. Clones copy kind handlers (component-kinds; design Q1)

- [ ] 17.1 RED: add `TestKind_CloneCopiesHandlers` (scenario "Clone keeps replaced handlers", 2 rows) and `TestKind_CloneReplaceIsolated` (scenario "Replacing a handler on the clone leaves the source unchanged") to `kind_test.go`; verify `go test -race -run 'TestKind_Clone' .` fails on both rows (clone runs the default Runnable handler and no k handler); record that `CloneReplaceIsolated` passes on arrival and show it can fail by temporarily sharing the source's `kinds` slice with the clone (not committed)
- [ ] 17.2 GREEN: copy `s.kinds` under `kindsMu` into the clone in `CloneWithOpts` (design Q1); verify the 17.1 command passes and `make test` is green
- [ ] 17.3 REFACTOR: *Extract Function* `(*RootScope).copyKindsTo(clone)`; verify `make test` and `make lint` pass
- [ ] 17.4 Update the `Clone`/`CloneWithOpts` godoc, the component-kinds page (clones), and an upgrading-page entry; verify the docs lint and build pass

## 18. Start failures are StartError values (container-start; design Q2)

- [ ] 18.1 RED: add `TestStartError_Fields` (scenario "StartError fields per failure point", 4 rows) and `TestStartError_NotForGuards` (scenario "Errors that are not service failures are not StartError", 3 rows) to `root_start_test.go`; verify `go test -race -run TestStartError_ .` fails to compile only on `StartError`, `StartOp` and the `StartOp*` constants
- [ ] 18.2 GREEN: add `StartError`/`StartOp` and return them from the build, start (including AsStarter-without-Start) and handle failure points with unchanged `Error()` text, keeping abort wrapping around them (design Q2); verify the 18.1 command passes, the existing `TestStart_*`/`TestKind_*`/`TestRunnable_*` message assertions pass unchanged, and `make test` is green
- [ ] 18.3 REFACTOR: *Replace Function with Constructor*: `wrapStartError` and the build/handle formats become `newStartError(op, scope, name, kind, cause)`; verify `make test` and `make lint` pass
- [ ] 18.4 Add `ExampleStartError` (errors.As on a failed Start) and the StartError section of the starter page; verify `go test -run ExampleStartError .` and the docs lint and build pass

## 19. Explained edges report their source (dependency-declaration; design Q3)

- [ ] 19.1 RED: add `TestExplain_EdgeSource` (scenario "Source per edge", 2 rows), `TestExplain_EdgeSourceDependents` and `TestExplain_EdgeSourceCompat` (scenario "Output without declared edges is unchanged") to `dependency_test.go`; verify `go test -race -run TestExplain_EdgeSource .` fails to compile only on `ExplainServiceDependencyOutput.Source` and the `DependencySource*` constants
- [ ] 19.2 GREEN: store a `DependencySource` per DAG edge with invoked > depends-on > declared precedence, record it at the three call sites (`invokeAnyByName`, `resolveDeclaredDependencies` via `invokeAnyByNameAs`, `DeclareDependency`), and expose it with `String`/`MarshalText` and the `String()` suffix (design Q3); verify the 19.1 command passes, `di_explain_test.go` passes unchanged and `make test` is green
- [ ] 19.3 REFACTOR: *Introduce Parameter Object* if `addDependency` has more than seven parameters (`edge{from, to, source}`), otherwise none needed; verify `make test`, `make lint` and `go test -race -run TestDAG .` pass
- [ ] 19.4 Add `ExampleExplainNamedService_source` and the source note on the declared-dependencies page; verify the example and the docs lint and build pass

## 20. InvokeAs behavior is unchanged (characterization baseline) (Part K)

- [ ] 20.1 RED: add `TestInvokeAs_HookReceivesTypeName` (scenario "InvokeAs hooks receive the requested type name"); verify `go test -race -run TestInvokeAs_HookReceivesTypeName .` passes on arrival (characterization), record that, and show it can fail by temporarily passing the real service name to the hooks in `invokeByGenericType` (not committed)
- [ ] 20.2 GREEN: no production change; this test guards the D3 extraction in 21.3; verify `make test` is green
- [ ] 20.3 REFACTOR: none needed, since no code changed; verify `make test` and `make lint` pass

## 21. Collect every visible service assignable to a type (Part K)

- [ ] 21.1 RED: add `TestInvokeAll_Membership` (scenario "Membership by registered type", 4 rows), `TestInvokeAll_EagerMembers` (scenario "Eager values are members without a provider call"), `TestInvokeAll_NilInjector` (scenario "Nil injector uses the default root scope") and `TestInvokeAll_TypedNilMember` (scenario "Typed-nil member is returned as is"); verify `go test -race -run 'TestInvokeAll_(Membership|EagerMembers|NilInjector|TypedNilMember)' .` fails to compile only on the missing `InvokeAll`
- [ ] 21.2 GREEN: add `InvokeAll[T]` in `di.go` backed by a pipeline in `invoke.go` that filters `serviceForEachRec` by `serviceCanCastToGeneric[T]` only and builds members sequentially, copying the per-member logic of `invokeByGenericType` with the member name for hooks (design D3); verify the 21.1 command passes and `make test` is green
- [ ] 21.3 REFACTOR: *Extract Function* `invokeServiceInstance(injector, vScope, hookName, realName, scope, service)` (design D3) and call it from `invokeByGenericType` (hookName = type name) and the collection pipeline (hookName = member name); verify `go test -race -run 'TestInvokeAs|TestMustInvokeAs|TestInvokeByGenericType|TestInvokeAll' .`, `make test` and `make lint` pass
- [ ] 21.4 Add `ExampleInvokeAll` (members by registered type) and godoc on `InvokeAll` (registered-type membership, the `InvokeAll[any]` sharp edge, per-member hooks, snapshot semantics); verify `go test -run ExampleInvokeAll$ .` passes
- [ ] 21.5 Create `docs/docs/service-invocation/collection-invocation.md` with the membership section; verify `npm --prefix docs run build` succeeds

## 22. Visibility, shadowing and alias exclusion (Part K)

- [ ] 22.1 RED: add `TestInvokeAll_Visibility` (scenario "Scope visibility and shadowing", 5 rows), `TestInvokeAll_AnyType` (scenario "Interface type any matches every non-alias service"), `TestInvokeAll_ProviderScopeVisibility` (scenario "Collection inside a provider sees the provider's scope") and `TestInvokeAll_AliasExcluded` (scenario "Alias does not duplicate its target"); verify `go test -race -run 'TestInvokeAll_(Visibility|AnyType|ProviderScopeVisibility|AliasExcluded)' .` fails on the shadowing rows `x/*BHandler` and `x/*sql.DB` and on the alias duplicates; ProviderScopeVisibility passes on arrival through `virtualScope` delegation, so record that and show its assertion can fail by temporarily registering "b" in the root scope (not committed)
- [ ] 22.2 GREEN: keep the first occurrence of each name before type filtering (nearest wins) and drop `ServiceTypeAlias` (design D1 steps 1, 3, 4); verify the 22.1 command passes and `make test` is green
- [ ] 22.3 REFACTOR: *Extract Function* `collectionCandidates[T](injector) []collectionCandidate` (name, scope, depth, registration index, service; not `candidate`, which start-lifecycle already declares in the same package) for reuse by the map variant; verify `make test` and `make lint` pass
- [ ] 22.4 Add the visibility, shadowing and alias section to the docs page and godoc (shadowing by a non-matching nearest service; provider-scope visibility); verify `npm --prefix docs run build` succeeds

## 23. Empty results, failure semantics and MustInvokeAll (Part K)

- [ ] 23.1 RED: add `TestInvokeAll_Empty` (scenario "Empty results", `InvokeAll` rows), `TestInvokeAll_MemberFailure` (scenario "Member failure modes", 3 rows), and `TestMustInvokeAll_Panics` / `TestMustInvokeAll_Success` (scenarios "Must variants panic with the same error" / "Must variants return the collection on success", `MustInvokeAll` rows); verify `go test -race -run 'TestInvokeAll_(Empty|MemberFailure)|TestMustInvokeAll_(Panics|Success)' .` fails to compile only on `MustInvokeAll`, then on the nil-vs-empty and member-name assertions
- [ ] 23.2 GREEN: return `[]T{}` on no match, wrap the first member error as ``DI: could not invoke collection member `%s`: %w`` and add `MustInvokeAll` via `must1` (design D3, D6, D7); verify the 23.1 command passes and `make test` is green
- [ ] 23.3 REFACTOR: *Extract Function* `wrapMemberError(name string, err error) error`; verify `make test` and `make lint` pass

## 24. Name-keyed collection (InvokeAllMap) (Part K)

- [ ] 24.1 RED: add `TestInvokeAllMap_Keys` (scenario "Map collection keys", 2 rows) and the `InvokeAllMap`/`MustInvokeAllMap` rows of `TestInvokeAll_Empty`, `TestMustInvokeAll_Panics` and `TestMustInvokeAll_Success`; verify `go test -race -run 'TestInvokeAllMap_Keys|TestInvokeAll_Empty|TestMustInvokeAll_' .` fails to compile only on the missing `InvokeAllMap`/`MustInvokeAllMap`
- [ ] 24.2 GREEN: add `InvokeAllMap` on `collectionCandidates` with the same build loop, returning `map[string]T{}` on no match, and `MustInvokeAllMap` via `must1`; verify the 24.1 command passes and `make test` is green
- [ ] 24.3 REFACTOR: *Extract Function* `buildMembers` shared by the slice and map variants, removing the duplicated loop; verify `make test` and `make lint` pass
- [ ] 24.4 Add `ExampleInvokeAllMap` and godoc on `InvokeAllMap`/`MustInvokeAllMap` (same members as `InvokeAll`, keyed by name, unordered); verify `go test -run ExampleInvokeAllMap .` passes

## 25. A collection excludes the service that invokes it (Part K)

- [ ] 25.1 RED: add `TestInvokeAll_CompositeExcludesSelf` (scenario "Composite collects every other implementation") and `TestInvokeAll_DeeperCycle` (scenario "Deeper cycles still fail"); verify `go test -race -run 'TestInvokeAll_(CompositeExcludesSelf|DeeperCycle)' .` fails on CompositeExcludesSelf with `ErrCircularDependency`; DeeperCycle passes on arrival, so record that and show it can fail by temporarily excluding every invoker-chain member (not committed)
- [ ] 25.2 GREEN: drop the candidate named like the last entry of the virtual scope's invoker chain (design D3a); verify the 25.1 command passes and `make test` is green
- [ ] 25.3 REFACTOR: *Extract Function* `invokingServiceName(injector) (string, bool)` if the check is inline in `collectionCandidates`, otherwise none needed; verify `make test` and `make lint` pass
- [ ] 25.4 Add the composite section to the docs page and the self-exclusion rule to the `InvokeAll`/`InvokeAllMap` godoc; verify `npm --prefix docs run build` succeeds

## 26. Dependency edges, hooks and snapshot semantics (Part K)

- [ ] 26.1 RED: add `TestInvokeAll_EdgesOrderShutdown`, `TestInvokeAll_HooksPerMember`, `TestInvokeAll_NoEdgesOutsideProvider`, `TestInvokeAll_TransientFresh`, `TestInvokeAll_Snapshot`, `TestInvokeAll_CallerMutation` and `TestInvokeAll_AfterShutdown` (scenarios "Dependency edges order shutdown", "Invocation hooks per member", "No edges when invoked outside a provider", "Transient and singleton members across calls", "Later registration is not visible in an earlier snapshot", "Caller mutation does not leak", "Collections after shutdown"); verify `go test -race -run 'TestInvokeAll_(Edges|Hooks|NoEdges|TransientFresh|Snapshot|CallerMutation|AfterShutdown)' .`; these pass on arrival through `invokeServiceInstance`, so record that and show EdgesOrderShutdown and HooksPerMember can fail by temporarily skipping `addDependency` and passing the type name as hookName (not committed)
- [ ] 26.2 GREEN: no production change expected; fix only what 26.1 reports; verify the 26.1 command passes and `make test` is green
- [ ] 26.3 REFACTOR: none needed if 26.2 changed nothing, otherwise *Inline Function* any helper made redundant; verify `make test` and `make lint` pass

## 27. Concurrent collection callers (Part K)

- [ ] 27.1 RED: add `TestInvokeAll_ConcurrentSingleBuild` (100 goroutines), `TestInvokeAll_ConcurrentRegistration` (50 + 50 goroutines) and `TestInvokeAll_ConcurrentShutdown` (50 collectors + 1 shutdown), all with `testWithTimeout`, for scenarios "Concurrent callers build each member once", "Concurrent registration and collection" and "Concurrent collection and shutdown"; verify `go test -race -count=5 -run 'TestInvokeAll_Concurrent' .`; they pass on arrival because the wrappers serialize builds, so record that and show the counter assertion can fail with a deliberately unsynchronized fake provider in a throwaway experiment (not committed)
- [ ] 27.2 GREEN: no production change expected; fix any race, leak or double build reported; verify the 27.1 command passes with `-race` and goleak reports no leak
- [ ] 27.3 REFACTOR: none needed: no new shared state (design D8); verify `make test` and `make lint` pass

## 28. Default collection order (Part K)

- [ ] 28.1 RED: add `TestInvokeAll_DefaultOrder` (scenario "Default order across scopes", 4 rows), `TestInvokeAll_DefaultOrderStable` (50 services × 20 calls), `TestInvokeAll_OverrideKeepsPosition` and `TestInvokeAll_DefaultOrderClone` (scenarios "Order is stable across repeated calls", "Override replaces a registration in place", "Default order in a clone"); verify `go test -race -count=10 -run 'TestInvokeAll_(DefaultOrder|OverrideKeepsPosition)' .` fails on order assertions (map iteration order)
- [ ] 28.2 GREEN: sort candidates by (scope depth, registration index, name) before building (design D2); verify the 28.1 command passes with `-count=10` and `make test` is green
- [ ] 28.3 REFACTOR: *Introduce Parameter Object*: make `collectionCandidate` carry `depth` and `index` explicitly if they are still loose values; verify `make test` and `make lint` pass
- [ ] 28.4 Add `BenchmarkInvokeAll/{10,100,1000}` and `BenchmarkInvokeAllParallel` to `bench/do_bench_test.go`; verify `go test -run '^$' -bench BenchmarkInvokeAll -benchmem ./bench/` runs and record the results in the change notes

## 29. Ordered members sort ascending and stably (Part K)

- [ ] 29.1 RED: add `TestInvokeAll_Ordered` (scenario "Ordered values", 6 rows) and `TestInvokeAll_OrderedTransient` (scenario "Order is read from each transient instance", 2 rows); verify `go test -race -run 'TestInvokeAll_Ordered' .` fails to compile only on the missing `Ordered`
- [ ] 29.2 GREEN: add `type Ordered interface{ Order() int }` (in `di_lifecycle.go` or a new `ordered.go`) and the stable ordering pass for `InvokeAll` only, with `Ordered` > `0` (design D4); verify the 29.1 command passes and `make test` is green
- [ ] 29.3 REFACTOR: *Extract Function* `orderValue(name, instance, opts) (int, error)` and *Replace Loop with Pipeline* using `slices.SortStableFunc`; verify `make test` and `make lint` pass
- [ ] 29.4 Add `ExampleOrdered` and godoc on `Ordered` (ascending, missing = 0, stable, collection-only); verify `go test -run ExampleOrdered .` passes

## 30. WithOrder is a registration option for every registration style (Part K)

- [ ] 30.1 RED: add `TestInvokeAll_WithOrderPrecedence` (scenario "Precedence", 5 rows), `TestInvokeAll_WithOrderHelpers` (scenario "WithOrder on every registration helper", 18 rows), `TestInvokeAll_WithOrderSkipsOrderMethod` (scenario "Order() is not called when WithOrder is set") and `TestWithOrder_NoEffectOnInvoke` (scenario "WithOrder outside collections has no effect"); verify `go test -race -run 'TestInvokeAll_WithOrder|TestWithOrder_' .` fails to compile only on the missing `WithOrder`
- [ ] 30.2 GREEN: add `WithOrder(n int) ProvideOption` storing `*int` in the service metadata, last one wins, and read it first in `orderValue` (precedence WithOrder > Ordered > 0, design D4, D5); verify the 30.1 command passes and `make test` is green
- [ ] 30.3 REFACTOR: none needed, since 30.2 only adds one branch to `orderValue`; verify `make test` and `make lint` pass
- [ ] 30.4 Add `ExampleWithOrder` (third-party type ordered without editing it), godoc on `WithOrder`, and the ordering section of the docs page; verify `go test -run ExampleWithOrder .` passes and `npm --prefix docs run build` succeeds

## 31. Order() panic and map-collection independence (Part K)

- [ ] 31.1 RED: add `TestInvokeAll_OrderPanic` (scenario "Panicking Order()", 3 rows), `TestMustInvokeAll_OrderPanic` (scenario "MustInvokeAll surfaces the Order() panic as its panic") and `TestInvokeAllMap_IgnoresOrdered` (scenario "Order() not called for map collections"); verify `go test -race -run 'TestInvokeAll_OrderPanic|TestMustInvokeAll_OrderPanic|TestInvokeAllMap_IgnoresOrdered' .` fails because the panic escapes `InvokeAll`; IgnoresOrdered passes on arrival, so record that and show it can fail by temporarily running the ordering pass in `InvokeAllMap` (not committed)
- [ ] 31.2 GREEN: recover inside `orderValue`, wrapping error panics with `%w` and stringifying others, both naming the member (design D4); verify the 31.1 command passes and `make test` is green
- [ ] 31.3 REFACTOR: reuse start-lifecycle's existing `recoverAsError(r any) error` inside `orderValue` (same `%w`/`%v` rule as start-lifecycle D11) instead of a local recover-to-error block; add no new helper; verify `make test` and `make lint` pass

## 32. Ordering never affects lifecycle order (Part K)

- [ ] 32.1 RED: add `TestInvokeAll_OrderDoesNotAffectShutdown` and `TestInvokeAll_OrderDoesNotAffectStart` (scenarios "Shutdown ignores collection order" and "Start ignores collection order"); verify `go test -race -run 'TestInvokeAll_OrderDoesNotAffect' .`; they pass on arrival, so record that and show each can fail against a throwaway change that feeds `WithOrder` into start and shutdown ordering (not committed)
- [ ] 32.2 GREEN: no production change expected, since `WithOrder` is read only by `orderValue`; verify the 32.1 command passes and `make test` is green
- [ ] 32.3 REFACTOR: none needed; verify `make test` and `make lint` pass
- [ ] 32.4 Add the "collection order vs lifecycle order" section to the docs page; verify `npm --prefix docs run build` succeeds

## 33. Refresh starts what is not started yet (container-start; design P1–P3)

- [ ] 33.1 RED: add `TestRefresh_StartsNew` (scenario "Refresh starts late registrations and new scopes once", 2 rows), `TestRefresh_NothingNew` and `TestIncrementalStart_Phase` (scenario "Incremental start in the wrong phase", the 3 `root.Refresh()` rows) to `root_start_test.go`; verify `go test -race -run 'TestRefresh_(StartsNew|NothingNew)|TestIncrementalStart_Phase' .` fails to compile only on `Refresh` and `ErrNotStarted`
- [ ] 33.2 GREEN: add `ErrNotStarted`, `pipelineMu` with the `started`/`handled` sets keyed by wrapper (Start records them), `beginIncremental` with the phase guard, and `RootScope.Refresh`/`RefreshWithContext` running the pipeline over unstarted candidates of the whole tree (design P1–P3); verify the 33.1 command passes and `make test` is green
- [ ] 33.3 REFACTOR: *Extract Function* `runPipeline(ctx, roots, incremental)` shared by `Start` and `Refresh`, and *Parameterize Function* `discoverCandidates(roots, skip)`; verify `make test` and `make lint` pass
- [ ] 33.4 Add `ExampleRootScope_Refresh` and a "Refresh" section on the starter page (late registrations, at-most-once, `ErrNotStarted`, never call it from Start-time code); verify the example and the docs lint and build pass

## 34. Scope.Start starts a subtree (container-start; design P2)

- [ ] 34.1 RED: add `TestScopeStart_Subtree`, `TestScopeStart_RootScope` and the two `tenant.Start()` rows of `TestIncrementalStart_Phase`; verify `go test -race -run 'TestScopeStart_|TestIncrementalStart_Phase' .` fails to compile only on `Scope.Start`
- [ ] 34.2 GREEN: add `Scope.Start`/`StartWithContext` running the pipeline with `roots = [s]`, delegating to Refresh on the root's own scope (design P2); verify the 34.1 command passes and `make test` is green
- [ ] 34.3 REFACTOR: none needed if 34.2 only calls `runPipeline`; otherwise *Inline Function* any wrapper it left; verify `make test` and `make lint` pass
- [ ] 34.4 Add `ExampleScope_Start` and the scope-start section of the starter page; verify the example and the docs lint and build pass

## 35. Incremental starts are serialized and cancelled by Shutdown (container-start; design P1, P2)

- [ ] 35.1 RED: add `TestRefresh_Concurrent` (scenario "Concurrent incremental starts") and `TestRefresh_ShutdownCancels` (scenario "Shutdown cancels a running Refresh", `synctest.Test`); verify `go test -race -count=10 -run 'TestRefresh_(Concurrent|ShutdownCancels)' .` fails on ShutdownCancels (Refresh not cancelled, deadlock) and record whether Concurrent passes on arrival through `pipelineMu`, showing it can fail by temporarily dropping the lock (not committed)
- [ ] 35.2 GREEN: record `incCancel`/`incDone` in `beginIncremental`, capture them in `beginClose`, and cancel and await them in `waitForStart` (design P2); verify the 35.1 command passes, goleak is clean, and `make test` is green
- [ ] 35.3 REFACTOR: *Extract Function* `inFlightRun()` returning the running Start or Refresh cancel/done pair for `beginClose`; then a gopls call-hierarchy review that `pipelineMu` is never taken under `lifecycleMu` or by Shutdown (design X3); verify `make test` and `make lint` pass

## 36. Kind handlers and runners on incremental start (component-kinds; design P1)

- [ ] 36.1 RED: add `TestKind_RefreshHandlesNew` and `TestRunnable_RefreshRunsOnce`; verify `go test -race -run 'TestKind_RefreshHandlesNew|TestRunnable_RefreshRunsOnce' .` fails on duplicate handling ("a" handled twice, "migrate" run twice) unless 33.2 already skips handled pairs; if they pass on arrival, record it and show they fail with the `handled` check temporarily removed (not committed)
- [ ] 36.2 GREEN: skip `(kind, wrapper)` pairs in `handled` in `runStage` and record new ones (design P1); verify the 36.1 command passes and `make test` is green
- [ ] 36.3 REFACTOR: none needed, a guard in one loop; verify `make test` and `make lint` pass

## 37. Incremental start fires no container hooks (container-lifecycle-hooks; design X2)

- [ ] 37.1 RED: add `TestContainerHooks_IncrementalStartFiresNone` (scenario "Refresh after Start fires no container hook", 2 rows); verify `go test -race -run TestContainerHooks_IncrementalStartFiresNone .`; it likely passes on arrival because `runPipeline` never reaches `pointStarting`/`pointAfterStart` for incremental runs, so record that and show it fails when the Starting hooks are temporarily run in `runPipeline` (not committed)
- [ ] 37.2 GREEN: no production change expected; if 37.1 failed, guard the container-hook points with `!incremental`; verify the 37.1 command passes and `make test` is green
- [ ] 37.3 REFACTOR: none needed; verify `make test` and `make lint` pass
- [ ] 37.4 Note in the container-hooks page that Refresh and Scope.Start fire no container hooks; verify the docs lint and build pass

## 38. Parallel start (container-start; design P4)

- [ ] 38.1 RED: add `TestStart_Parallel` (scenario "Independent Starters start concurrently", 5 rows, `synctest.Test`), `TestStart_ParallelFailure` and `TestStart_ParallelHooks`; verify `go test -race -run 'TestStart_Parallel' .` fails to compile only on `InjectorOpts.StartParallelism`
- [ ] 38.2 GREEN: add `StartParallelism` (copied in `copy()`) and the Kahn worker scheduler in `startStarters` for n > 1, keeping the sequential loop for 0 and 1 (design P4); verify the 38.1 command passes with `-race -count=20`, goleak is clean and `make test` is green
- [ ] 38.3 REFACTOR: *Extract Function* `startStartersParallel(ctx, candidates, n)` and *Replace Conditional with Polymorphism* only if a third mode appears, otherwise a plain branch; verify `make test` and `make lint` pass
- [ ] 38.4 Add `ExampleInjectorOpts_startParallelism`, godoc on the field (hooks may run concurrently), and the parallel-start section of the starter page; verify the example and the docs lint and build pass

## 39. HookAfterBuild (service-build-hooks; design P5)

- [ ] 39.1 RED: add `build_hooks_test.go` with `TestAfterBuildHook_Fires` (6 rows), `TestAfterBuildHook_Order`, `TestAfterBuildHook_RegistrationOrder`, `TestAfterBuildHook_Scopes` (2 rows) and `TestAfterBuildHook_Panic`; verify `go test -race -run TestAfterBuildHook_ .` fails to compile only on `HookAfterBuild` and `AddAfterBuildHook`
- [ ] 39.2 GREEN: add the field (nil-to-empty init, `copy()`, nil-skipping) and the adder, and fire it in `serviceLazy.build`, `serviceTransient.getInstance` and once in `serviceEager.getInstance` outside provider recovery (design P5); verify the 39.1 command passes and `make test` is green
- [ ] 39.3 REFACTOR: *Extract Function* `onAfterBuild(i Injector, name string, instance any)` resolving the scope from the virtual scope, shared by the three wrappers; verify `make test`, `make lint` and `bench` invoke results `~` against 0.2 (`benchstat`)
- [ ] 39.4 Add `ExampleRootScope_AddAfterBuildHook` and the build hook in `docs/docs/container/options.md`; verify the example and the docs lint and build pass

## 40. Scope hooks: registration and Scope.Start veto (scope-lifecycle-hooks; design S1)

- [ ] 40.1 RED: add `scope_hooks_test.go` with `TestScopeHooks_RegistrationOrder` and `TestScopeStartingHook_Veto` (3 rows); verify `go test -race -run 'TestScopeHooks_RegistrationOrder|TestScopeStartingHook_Veto' .` fails to compile only on the `HookScope*` fields and `AddScope*Hook` methods
- [ ] 40.2 GREEN: add the four fields (init, `copy()`, nil-skipping), the adders, and fire Starting (veto, recovered as a veto) and Started in `Scope.Start` for child scopes (design S1); verify the 40.1 command passes and `make test` is green
- [ ] 40.3 REFACTOR: *Extract Function* `runScopeStartingHooks(ctx, scope) error`, reusing Part H's `callHookSafely`; verify `make test` and `make lint` pass

## 41. Scope Closing and Closed hooks (scope-lifecycle-hooks; design S1)

- [ ] 41.1 RED: add `TestScopeHooks_ClosingClosed` (4 rows) and `TestScopeHooks_RootFiresNone`; verify `go test -race -run 'TestScopeHooks_(ClosingClosed|RootFiresNone)' .` fails on the missing events; record that RootFiresNone passes on arrival and show it can fail by temporarily firing scope hooks for the root's own scope (not committed)
- [ ] 41.2 GREEN: fire Closing after the children and before the services, and Closed at the end of `Scope.ShutdownWithContext` for non-root scopes (design S1); verify the 41.1 command passes, the existing `scope_test.go` shutdown tests pass unchanged, and `make test` is green
- [ ] 41.3 REFACTOR: none needed if 41.2 adds two calls; verify `make test` and `make lint` pass

## 42. Scope hook panics (scope-lifecycle-hooks; design S1)

- [ ] 42.1 RED: add `TestScopeHooks_ObserverPanic` (3 rows); verify `go test -race -run TestScopeHooks_ObserverPanic .` fails because the panic escapes before the scope's services shut down
- [ ] 42.2 GREEN: recover each observer scope hook with `callHookSafely` and dispatch the first panic last, through `HookContainerPanic` (points `scope-started`, `scope-closing`, `scope-closed`) (design S1, Part H D6); verify the 42.1 command passes and `make test` is green
- [ ] 42.3 REFACTOR: *Extract Function* `runObserverHooksWithPanicPolicy` shared by container and scope observer hooks if they still duplicate the dispatch; verify `make test` and `make lint` pass
- [ ] 42.4 Add `ExampleRootScope_AddScopeClosingHook` and a scope-hooks section on the container-hooks page; verify the example and the docs lint and build pass

## 43. RootScope.Phase (container-phase; design S2)

- [ ] 43.1 RED: add `phase_test.go` with `TestPhase_Lifecycle` (6 rows), `TestPhase_SeenByHooks`, `TestPhase_RaceFree`, `TestContainerPhase_StringUnknown` and `TestPhase_CloneIdle`; verify `go test -race -run 'TestPhase_|TestContainerPhase_' .` fails to compile only on `ContainerPhase`, the `Phase*` constants and `RootScope.Phase`
- [ ] 43.2 GREEN: add `ContainerPhase` with `String()` and `RootScope.Phase()` mapping both phases under `lifecycleMu` (design S2); verify the 43.1 command passes and `make test` is green
- [ ] 43.3 REFACTOR: none needed, one mapping function; verify `make test` and `make lint` pass
- [ ] 43.4 Add `ExampleRootScope_Phase` and the phase diagram mapping on the container-hooks page; verify the example and the docs lint and build pass

## 44. Deterministic InvokeAs (type-resolution; design S5)

- [ ] 44.1 RED: add `TestInvokeAs_Deterministic` (6 rows), `TestInvokeAs_DeterministicStable`, `TestInvokeAs_SingleAndMissing` (2 rows) and `TestInvokeStruct_ImplicitAliasingDeterministic` to `invoke_test.go`; verify `go test -race -count=10 -run 'TestInvokeAs_Deterministic|TestInvokeStruct_ImplicitAliasingDeterministic' .` fails on the multi-match rows (random pick); record that SingleAndMissing passes on arrival
- [ ] 44.2 GREEN: select the minimum `(distance, regIndex, name)` candidate in `invokeByGenericType` and in the `invokeByTags` fallback (design S5), removing the two nondeterminism TODOs; verify the 44.1 command passes with `-count=10`, the existing `TestInvokeAs`/`TestInvokeByGenericType*`/`TestInvokeAs_HookReceivesTypeName` pass unchanged, and `make test` is green
- [ ] 44.3 REFACTOR: *Extract Function* `selectByType(injector, target reflect.Type) (candidate, bool)` shared by both call sites; verify `make test` and `make lint` pass
- [ ] 44.4 Document the selection rule in the `InvokeAs`/`MustInvokeAs`/`InvokeStruct` godoc and the invocation docs, plus an upgrading-page entry (flagged); verify the docs lint and build pass

## 45. Collection struct injection (collection-struct-injection; design S3)

- [ ] 45.1 RED: add `invoke_all_struct_test.go` with `TestInvokeStruct_CollectionFields` (4 rows), `TestInvokeStruct_CollectionCustomTag`, `TestInvokeStruct_CollectionInvalidField` (2 rows), `TestInvokeStruct_CollectionMemberFailure` and `TestInvokeStruct_CollectionExcludesSelf`; verify `go test -race -run TestInvokeStruct_Collection .` fails because `*` is looked up as a service name (ErrServiceNotFound)
- [ ] 45.2 GREEN: generalize the collection pipeline to a `reflect.Type` target and handle the `*` tag value in `invokeByTags` for slice and `map[string]` fields (design S3); verify the 45.1 command passes, the Part K tests pass unchanged, and `make test` is green
- [ ] 45.3 REFACTOR: *Extract Function* `injectCollectionField(injector, field, value)` out of `invokeByTags`, bringing its `gocyclo` down; verify `make test` and `make lint` pass
- [ ] 45.4 Add `ExampleInvokeStruct_collection`, the struct-injection docs section, and an upgrading-page entry for the `*` name (BREAKING edge case); verify the example and the docs lint and build pass

## 46. Live collections (live-collections; design S4)

- [ ] 46.1 RED: add `collection_test.go` with `TestCollection_LaterRegistrations` (2 rows), `TestCollection_CreationBuildsNothing`, `TestCollection_HeldByProvider`, `TestCollection_ReadOutcomes` (3 rows) and `TestCollection_ConcurrentReads`; verify `go test -race -run TestCollection_ .` fails to compile only on `Collection` and `InvokeCollection`
- [ ] 46.2 GREEN: add `Collection[T]` with `All`/`Map` over the captured injector and `InvokeCollection` (design S4); verify the 46.1 command passes and `make test` is green
- [ ] 46.3 REFACTOR: none needed if `All`/`Map` only delegate to the Part K pipeline; verify `make test` and `make lint` pass
- [ ] 46.4 Add `ExampleInvokeCollection` and the live-collection section of the collection-invocation page (snapshot vs live); verify the example and the docs lint and build pass

## 47. Per-service health checks honor HealthCheckTimeout (health-check-timeouts; design S6)

- [ ] 47.1 RED: add `TestHealthCheck_PerServiceTimeout` (6 rows, fake clock in `synctest.Test`) and `TestHealthCheck_PerServiceErrorInTime` to `healthcheck_timeout_test.go`; verify `go test -race -run 'TestHealthCheck_PerService' .` fails on the timeout rows (nil instead of the timeout error)
- [ ] 47.2 GREEN: wrap the per-service functions' context with `clockwork.WithTimeout(ctx, clock, HealthCheckTimeout)` when the option is positive (design S6); verify the 47.1 command passes, the existing health-check suites pass unchanged, and `make test` is green
- [ ] 47.3 REFACTOR: *Extract Function* `perServiceHealthCheckContext(injector, ctx)`; verify `make test` and `make lint` pass
- [ ] 47.4 Document it on the healthchecker page and the options page, plus an upgrading-page entry (flagged); verify the docs lint and build pass

## 48. Integration checks

- [ ] 48.1 Compare benchmarks against 0.2 with `benchstat`; verify invoke and provide show `~` and `BenchmarkNew` allocs/op is 11 (otherwise stop and investigate)
- [ ] 48.2 Full run: verify `make test`, `make lint`, per-module `go vet ./... && go test -race ./...` in every `go.work` module, and `npm --prefix docs run lint && npm --prefix docs run build` all pass
- [ ] 48.3 Traceability: verify `go test -race -v -run '<every test in design.md Test Strategy, new and folded>' .` lists every test and subtest as PASS, with row counts matching the scenarios
- [ ] 48.4 Validate the plan; verify `openspec validate consolidate-pending-lifecycle-work --strict` passes

## Workflow follow-up

- Archive this change after review (`/opsx:archive`).
- The folded changes' own follow-ups are all absorbed here; there is no remaining phase-2 list.
