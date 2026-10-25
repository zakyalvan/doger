# Tasks

Prerequisites: `fix-stacktrace-frame-filter`, `rename-module-doger` and `upgrade-go-1-27` are archived. The module is `github.com/kartaladev/doger`, the package is `doger`, the Go floor is 1.27, and `make test` is green before starting. Read design.md (D1–D12 and the Test Strategy table) before each group: tasks reference it for detail. Group 1 is a bug fix and starts with failing regression tests. Flaky-test policy for every `make test` gate (design Risks): if only `TestRootScope_queueServiceHealthcheck` fails, with its known symptom ("An error is expected but got nil" at `root_scope_test.go:119` or `:131`), rerun once and record it in the task evidence; any other failure blocks.

## 0. Baseline

- [x] 0.1 Record the benchmark baseline with `go test -run '^$' -bench . -benchmem -count 10 ./bench/ > <scratch>/bench-before.txt`; verify the file exists and `make test` passes.

## 1. Run-once root Shutdown (bug fix)

- [x] 1.1 RED: in `root_scope_test.go` add regression tests `TestRootScope_ConcurrentShutdown` (rows `default_opts`, `health_check_parallelism_2`; scenario "Concurrent root Shutdown runs once"), `TestRootScope_RepeatedShutdown` ("Later root Shutdown returns the first report"), `TestRootScope_ShutdownWaiterDeadline` ("Waiting Shutdown caller honours its context", synctest), `TestRootScope_ShutdownReportIsolated`, `TestRootScope_ShutdownWithEndedContext` (2 rows), `TestRootScope_ShutdownNilContext` (2 rows, asserted with `assert.NotPanics`) `TestRootScope_ShutdownWinnerPanic` (2 rows), plus the mechanism test `TestAwaitDone` (design Test Strategy); verify `go test -race -count=5 -run 'TestRootScope_(ConcurrentShutdown|RepeatedShutdown|ShutdownWaiterDeadline|ShutdownReportIsolated|ShutdownWithEndedContext|ShutdownNilContext|ShutdownWinnerPanic)|TestAwaitDone' .` fails on current code (`TestAwaitDone` only to compile, on the missing `awaitDone`): spurious `ErrServiceNotFound` entries (a race report on `healthCheckPool` may also appear; it is intermittent), empty later reports, a waiter that returns before its deadline, a nil-context panic, and a second panic from the panicking `Logf`.
- [x] 1.2 GREEN: add `lifecycleMu`, `closePhase`, `closeDone`, `closeReport` to `RootScope` and implement design D6 Shutdown steps 0–3 and 6–7 (nil context to `Background`, winner/waiter split, `awaitDone` preferring completion, `cloneShutdownReport`, `shutdownAsWinner` holding the deferred `finishClose` that stores a private copy or the "did not complete" report, so `pointClosed` runs after it, pool stopped only by the winner; no Start handling yet); verify the 1.1 command passes and `make test` is green.
- [x] 1.3 REFACTOR: *Extract Function* `shutdownAsWinner` and `waitForClose` out of `RootScope.ShutdownWithContext`; verify `make test` and `make lint` pass.
- [x] 1.4 Document run-once root Shutdown (copied report for later callers, completion preferred over an ended context, nil context as `Background`, failed report when the first caller panicked, waiter deadline, root `*Scope` bypass) in the godoc of `RootScope.Shutdown`/`ShutdownWithContext` and in `docs/docs/service-lifecycle/shutdowner.md`; verify `go doc RootScope.ShutdownWithContext` shows it and `npm run build` in `docs/` passes.

## 2. Registration index

- [x] 2.1 RED: in `options_test.go` add `TestRegistrationIndex_Order` (rows: same-scope override keeps a=0, child-scope override gets a new index, clone continues the counter; mechanism of scenario "Registration order survives override and clone") `TestRegistrationIndex_ConcurrentUnique` (100 goroutines, distinct indexes) and `TestRegistrationIndex_CloneDuringRegistration` (registrations racing `Clone`, distinct indexes in the clone; a stress test, see design D3); verify `go test -race -run TestRegistrationIndex .` fails to compile only on the missing `getRegistrationIndex`.
- [x] 2.2 GREEN: implement design D1/D3 (`nextRegIndex` on `RootScope`; `regIndex` and `getRegistrationIndex()` on the four wrappers and both wrapper interfaces; assign in `provide`/`override`, keep it on same-scope override, copy in `clone`, continue the counter in `CloneWithOpts`, read after copying the wrappers); verify the 2.1 command passes with `-count=20` and `make test` is green.
- [x] 2.3 REFACTOR: *Extract Function* `assignRegistrationIndex(scope, name)` shared by `provide`/`override`; verify `make test` and `make lint` pass. No docs: the index is internal.

## 3. ProvideOption and variadic registration signatures

- [x] 3.1 RED: add `TestProvideOptions_Registration` rows 1–4 and 6 of "Registration with and without options" (row 5 needs `AsRunnable`, see 15.1), `TestProvideOptions_SignatureHasVariadic` (18 rows of "Registration functions keep their parameters and add a variadic option list") and the `AsStarter`/`DependsOn`/`DependsOnNamed` rows of `TestProvideOptions_Constructors` ("Each constructor yields an option recognized by registration"); verify `go test -race -run 'TestProvideOptions_(Registration|SignatureHasVariadic|Constructors)' .` fails to compile only on `ProvideOption`, `AsStarter`, `DependsOn`, `DependsOnNamed` and `getOptions`.
- [x] 3.2 GREEN: add `options.go` (design D2: `serviceOptions`, `ProvideOption`, `AsStarter`, `DependsOn[T]`, `DependsOnNamed`), add `opts ...ProvideOption` to the 18 functions, thread options through `provide`/`override` into each wrapper's `opts` (`getOptions()`, aliases nil), skip nil options; verify the 3.1 command passes and `make test` is green with existing tests unchanged.
- [x] 3.3 REFACTOR: *Introduce Parameter Object* `buildServiceOptions(opts []ProvideOption) *serviceOptions` shared by `provide`/`override`; verify `make test` and `make lint` pass.
- [x] 3.4 Document options: an "Options" godoc paragraph on all 18 changed functions, and a new page under `docs/docs/upgrading/` for this release with the variadic break, the closure workaround and the run-once root Shutdown behavior change, and link that page from `docs/docs/migrating/migrating-from-samber-do.md`; verify `go doc Provide` shows the variadic parameter and `npm run build` in `docs/` passes.

## 4. Option validation, combination, clone, override and alias

- [x] 4.1 RED: add `TestProvideOptions_Repeated` (non-kind rows of "Repeated options"), `TestProvideOptions_Invalid` (non-kind rows cfg/AsStarter, srv/transient, life/AsStarter, req, srv/eager of "Invalid opt-in combinations") and `TestProvideOptions_CloneOverrideAlias` (5 rows of "Clone, override and alias"); verify `go test -race -run 'TestProvideOptions_(Repeated|Invalid|CloneOverrideAlias)' .` fails on missing panics, uncollapsed duplicates and the Clone row; the Override and As rows pass on arrival after 3.2, so show they can fail by temporarily copying the old wrapper's options in `override` and returning the target's options from aliases.
- [x] 4.2 GREEN: implement `validateServiceOptions` (design D2: exact spec panic messages, run before `serviceSet`, interface types deferred), collapse duplicate dependencies, copy `opts` in each wrapper's `clone`; verify the 4.1 command passes and `make test` is green.
- [x] 4.3 REFACTOR: *Extract Function* per rule (`validateStarterOpt`, `validateTransientOpts`) so group 13 adds `validateKindOpt` without branching; verify `make test` and `make lint` pass.
- [x] 4.4 Document the registration-time panics in the godoc of `AsStarter` and `DependsOnNamed`; verify `go doc AsStarter` shows them and `make lint` passes.

## 5. Static declared dependencies (DependsOn)

- [x] 5.1 RED: in `dependency_test.go` add `TestDependsOn_BuildOrder` (3 rows of "Declared dependency built before the dependent"), `TestDependsOn_EagerTarget` ("Declared dependency on an eager service"), `TestDependsOn_EagerDependent` (3 rows of "Declared dependency of an eager service"), `TestDependsOn_AncestorScope`, `TestDependsOn_ConcurrentSingleBuild`, `TestDependsOn_Errors` (5 rows of "Missing or circular declared dependency") and `TestEager_NoOptionsNeverErrors`; verify `go test -race -run 'TestDependsOn|TestEager_NoOptionsNeverErrors' .` fails because dependencies are not built first and no edges are recorded; `TestEager_NoOptionsNeverErrors` passes on arrival (it pins current behavior), so show it can fail by temporarily returning an error from `serviceEager.getInstance`.
- [x] 5.2 GREEN: implement design D4 (lazy `build` and transient `getInstance` invoke declared names on their virtual scope before the provider; eager uses `depsMu`/`depsResolved`, set only on success, consulted only when it has dependencies; clone resets it); verify the 5.1 command passes and `make test` is green.
- [x] 5.3 REFACTOR: *Extract Function* `resolveDeclaredDependencies(i Injector, opts *serviceOptions) error` shared by the three wrappers; verify `make test` and `make lint` pass.
- [x] 5.4 Add `ExampleDependsOn` and the static part of a new "Declared dependencies" page (`docs/docs/service-registration/declared-dependencies.md`); verify `go test -run ExampleDependsOn .` and `npm run build` in `docs/` pass.

## 6. Dynamic declared dependencies and shutdown order

- [x] 6.1 RED: add `TestDeclareDependency` (11 rows of "Declaring edges dynamically"), `TestDeclareDependency_Concurrent` ("Concurrent declarations are race-free"), `TestDeclareDependency_Clone` (2 rows of "Clones do not copy declared edges") and `TestDeclaredEdges_ShutdownOrder` (2 rows of "Scheduler is shut down before the jobs it drives"); verify `go test -race -run 'TestDeclareDependency|TestDeclaredEdges_ShutdownOrder' .` fails to compile only on the missing `DeclareDependency` (once it compiles, the `DependsOnNamed` shutdown row passes on arrival from 5.2; show it can fail by temporarily skipping `resolveDeclaredDependencies`).
- [x] 6.2 GREEN: implement design D5 in `dependency.go` (`declareMu`, `dependent` from `i`, `dependency` from the dependent's scope, `reachable` over DAG plus static `dependsOn` edges, self-edges as cycles, wrapped `ErrServiceNotFound`/`ErrCircularDependency`, lock order scope then DAG); verify the 6.1 command passes and `make test` is green, including `dag_test.go`.
- [x] 6.3 REFACTOR: *Extract Function* `dependencyNeighbors(desc)` (the DFS neighbour function reused by group 8's cycle check and ordering); verify `make test` and `make lint` pass.
- [x] 6.4 Add `ExampleDeclareDependency` and the dynamic part of the "Declared dependencies" page (same-scope-or-ancestor rule, start/shutdown but not construction, clones re-record `DependsOn` edges but not declared ones); verify `go test -run ExampleDeclareDependency .` and `npm run build` in `docs/` pass.

## 7. Starter interfaces, detection and one-shot Start

- [x] 7.1 RED: in `root_start_test.go` add `TestStart_Variants` (4 rows), `TestStart_Detection` (8 rows of "Which registrations are started"), `TestStart_DisableAutoDetection` (2 rows of "Auto-detection toggle"), `TestStart_Empty`, `TestStart_OneShot` (5 rows of "Repeated and late Start calls"), `TestStart_Clone` (2 rows of "Clone of a started container") and `TestStart_LateRegistrationNotStarted`; verify `go test -race -run 'TestStart_(Variants|Detection|DisableAutoDetection|Empty|OneShot|Clone|LateRegistrationNotStarted)' .` fails to compile only on `Start`, `StartWithContext`, the `Starter*` interfaces, `ErrAlreadyStarted`, `ErrStartAfterShutdown` and `DisableStarterAutoDetection`.
- [x] 7.2 GREEN: add the `Starter*` interfaces, both sentinels and both toggles (in `copy()` too), the D6 start phase on the group-1 `lifecycleMu` (`beginStart` with `ErrStartAfterShutdown` precedence, `runStart` holding the deferred `finishStart` so `pointAfterStart` runs after it), and D7 steps 2, 3 and 6 in `root_start.go` ordered by `(depth, regIndex)` only; verify the 7.1 command passes and `make test` is green.
- [x] 7.3 REFACTOR: *Extract Function* `discoverCandidates`, `buildCandidates` and `startStarters` (the type switch mirrors `shutdown`, so no polymorphism); verify `make test` and `make lint` pass.
- [x] 7.4 Add `ExampleRootScope_Start` and `ExampleAsStarter`, godoc for `Starter*`/`Start` (registered-type detection vs instance-based `Shutdowner*`, the `*exec.Cmd` caveat, a registered `*RootScope` is started but not shut down, the repository examples that call `Start()` by hand), and the page `docs/docs/service-lifecycle/starter.md` ("Starting the container"); verify `go test -run 'Example(RootScope_Start|AsStarter)' .` and `npm run build` in `docs/` pass.

## 8. Dependency-ordered start, deterministic ties and cycle check

- [x] 8.1 RED: add `TestStart_DependencyOrder`, `TestStart_ScopeTraversal`, `TestStart_TieBreak` plus `TestStart_TieBreak_Stable` (100 repeats), `TestStart_RegistrationOrder` (4 rows of "Registration order survives override and clone") and `TestStart_RejectsDeclaredCycle` ("Start rejects a cycle closed by a dynamic edge"); verify `go test -race -run 'TestStart_(DependencyOrder|ScopeTraversal|TieBreak|RegistrationOrder|RejectsDeclaredCycle)' .` fails on the DependencyOrder order and the missing cycle error; ScopeTraversal, TieBreak and RegistrationOrder pass on arrival from 7.2, so show they fail when sorting temporarily by `regIndex` only (first two) or when override temporarily drops the index (third).
- [x] 8.2 GREEN: implement design D8 (Kahn order over transitive Starter dependencies via `dependencyNeighbors`, `(depth, regIndex)` ties, defensive fallback) and the D7 step 5 cycle check, both after the `StageBeforeStart` point (still empty); verify the 8.1 command passes and `make test` is green.
- [x] 8.3 REFACTOR: *Extract Function* `orderByDependencies(candidates []candidate) []candidate` (generic over candidates for group 13) and *Move Function* the ordering code into `ordering.go`; verify `make test` and `make lint` pass.
- [x] 8.4 Document start ordering, ties and the cycle check in `docs/docs/service-lifecycle/starter.md`; verify `npm run build` in `docs/` passes.

## 9. Start errors and panics

- [x] 9.1 RED: add `TestStart_StarterFailure` (3 rows of "A Starter failure stops the remaining Starters"), `TestStart_BuildFailure` (2 rows of "A build failure stops Start before any Starter"), `TestStart_AsStarterInstanceWithoutStart` and `TestStart_ShutdownAfterFailure`; verify `go test -race -run 'TestStart_(StarterFailure|BuildFailure|AsStarterInstanceWithoutStart|ShutdownAfterFailure)' .` fails on the error-wrapping and message assertions and on the unrecovered panic rows; `ShutdownAfterFailure` passes on arrival (existing shutdown), so show it fails with `serviceLazy.shutdown` temporarily returning early.
- [x] 9.2 GREEN: wrap build and start failures with the D7/D11 formats, recover in `startInstance`, return the spec error for a deferred `AsStarter` instance without a `Start` variant, stop at the first failure; verify the 9.1 command passes and `make test` is green.
- [x] 9.3 REFACTOR: *Extract Function* `wrapStartError(scope, name, err)` and `recoverAsError(r any) error`; verify `make test` and `make lint` pass.
- [x] 9.4 Document failure semantics (no rollback, caller-owned `Shutdown`, services must tolerate Shutdown without Start) in the `Start` godoc and the starter docs page; verify `go doc RootScope.Start` and `npm run build` in `docs/` pass.

## 10. Context handling

- [x] 10.1 RED: add `TestStart_Context` (3 rows of "Cancellation and deadline", inside `synctest.Test`), `TestStart_NilContext` and `TestStart_ContextEndsWithStart`; verify `go test -race -run 'TestStart_(Context|NilContext|ContextEndsWithStart)' .` fails because "b" starts after cancellation, the nil context panics, and the kept context is not cancelled.
- [x] 10.2 GREEN: implement the D6 Start context (`context.WithCancelCause`, nil to `Background`, abort checks before each candidate, handler call and Starter and after the last step, abort-error wrapping, `startCancel(nil)` in `finishStart`, and `finishStart` turning a nil result into the abort error when `closePhase != closeOpen`); verify the 10.1 command passes and `make test` is green.
- [x] 10.3 REFACTOR: *Extract Function* `checkAbort(startCtx) error` used at every loop head; verify `make test` and `make lint` pass.
- [x] 10.4 Document the Start context contract (derived, cancelled when Start returns, do not retain it) in the `StartWithContext` godoc and the starter docs page; verify `go doc RootScope.StartWithContext` and `npm run build` in `docs/` pass.

## 11. Shutdown cancels and waits for a running Start

- [x] 11.1 RED: add `TestStart_ShutdownCancels` (the three Starter rows of "Shutdown cancels a running Start"; the Runner row is 15.1), `TestStart_PanicReleasesShutdown`, `TestStart_Concurrent` and `TestStart_ConcurrentInvoke`; verify `go test -race -count=20 -run 'TestStart_(ShutdownCancels|PanicReleasesShutdown|Concurrent|ConcurrentInvoke)' .` fails on ShutdownCancels (no cancel, no wait); the other three pass on arrival from 7.2, so show they fail with the CAS in `beginStart` removed (Concurrent), `serviceLazy.mu` dropped (ConcurrentInvoke) and `startDone` closed outside the defer once 11.2 waits (PanicReleasesShutdown).
- [x] 11.2 GREEN: implement D6 Shutdown step 4 (capture `inFlight`, `startCancel` and `startDone` in `beginClose`; `startCancel(ErrStartAfterShutdown)`; `awaitDone(startDone, ctx)`), never holding `lifecycleMu` across pipeline code; verify the 11.1 command passes with `-race -count=20`, goleak is clean, and `make test` is green.
- [x] 11.3 REFACTOR: *Extract Function* `waitForStart(ctx)`, then confirm with a gopls call-hierarchy review of every `lifecycleMu.Lock` site that no lock is held across user code; verify `make test` and `make lint` pass.
- [x] 11.4 Document the Start/Shutdown interplay (cancellation cause, deadline, late Starter not rolled back, never call `Shutdown()` synchronously from Start-time code, re-entrant `Start` returns `ErrAlreadyStarted`, root `*Scope` bypass) in the `Start`/`Shutdown` godoc and the starter docs page; verify `npm run build` in `docs/` passes.

## 12. Per-service start hooks

- [x] 12.1 RED: add `TestStart_Hooks` ("Hooks around each Starter") and `TestStart_HooksSkipNonStarter` ("No hooks for an AsStarter instance without a Start method"); verify `go test -race -run TestStart_Hooks .` fails to compile only on `HookBeforeStart`, `HookAfterStart`, `AddBeforeStartHook` and `AddAfterStartHook`.
- [x] 12.2 GREEN: add the `InjectorOpts` fields (initialized in `NewWithOpts`, copied in `copy()`), `onBeforeStart`/`onAfterStart` and the `RootScope.Add*StartHook` methods, called around `startInstance` with the method's unwrapped error; verify the 12.1 command passes and `make test` is green.
- [x] 12.3 REFACTOR: none needed, the helpers mirror `onBeforeShutdown`/`onAfterShutdown`; verify `make test` and `make lint` pass.
- [x] 12.4 Document the start hooks in `docs/docs/container/options.md` and their godoc; verify `npm run build` in `docs/` passes.

## 13. Component kinds

- [x] 13.1 RED: in `kind_test.go` add `TestKind_OptIn`, `TestKind_DistinctIdentity`, `TestKind_InterfaceRegistration` (2 rows), `TestKind_InvalidValues` (4 rows), `TestKind_Stage` (2 rows), `TestKind_ReplaceHandler`, `TestKind_NilRootUsesDefault` (not parallel), `TestKind_NoHandler`, `TestKind_ComponentOrder`, `TestKind_HandlerFailure` (3 rows), plus the `scheduled` rows of `TestProvideOptions_Constructors`, `TestProvideOptions_Repeated` and `TestProvideOptions_Invalid` (tjob, plain, life); verify `go test -race -run 'TestKind_|TestProvideOptions_' .` fails to compile only on `NewKind`, `Kind`, `Component`, `Stage`, `StageBeforeStart` and `StageAfterStart`.
- [x] 13.2 GREEN: add `kind.go` (design D9: `kindID`, nil root to `DefaultRootScope`, zero-Kind/invalid-stage/nil-handler panics, `With`, `Handle`, erased entries under `kindsMu`), `validateKindOpt` (D2, deferred for interface types), the D7 step 3 instance check, kind-bound candidates, and stage runs at D7 steps 4 and 7 ordered with `orderByDependencies`; verify the 13.1 command passes and `make test` is green.
- [x] 13.3 REFACTOR: *Extract Function* `runStage(ctx, stage)`; verify `make test` and `make lint` pass.
- [x] 13.4 Add `ExampleNewKind` (scheduled-job kind whose handler declares edges) and the page `docs/docs/service-lifecycle/component-kinds.md` (custom kinds, stages, interface-typed opt-in); verify `go test -run ExampleNewKind .` and `npm run build` in `docs/` pass.

## 14. Handlers declaring dependencies

- [x] 14.1 RED: add `TestKind_DeclareDependencyInHandler` ("Scheduler handler orders start and shutdown"); verify `go test -race -run TestKind_DeclareDependencyInHandler .` runs; it passes on arrival (8.2 already orders after `StageBeforeStart`, 6.2 records edges), so show it fails when the start order is temporarily computed before `StageBeforeStart`.
- [x] 14.2 GREEN: no production change needed beyond 8.2 and 13.2; verify the 14.1 command passes and `make test` is green.
- [x] 14.3 REFACTOR: none needed, this only pins pipeline order; verify `make test` and `make lint` pass.

## 15. Built-in Runnable kind

- [x] 15.1 RED: add `TestRunnable_RunsAfterStarters`, `TestRunnable_Failure`, `TestRunnable_AutoDetection` (5 rows of "Runner auto-detection"), `TestRunnable_ReplaceHandler`, `TestRunnable_ReassignedVariable`, the Runner row of `TestStart_ShutdownCancels`, and the runnable rows of `TestProvideOptions_Registration` (row 5), `_Constructors`, `_Repeated` and `_Invalid` (job, cfg); verify `go test -race -run 'TestRunnable_|TestStart_ShutdownCancels|TestProvideOptions_' .` fails to compile only on `Runner`, `Runnable`, `AsRunnable` and `EnableRunnerAutoDetection`.
- [x] 15.2 GREEN: implement design D10 (`runnableKind`, exported `Runnable` copy, `AsRunnable`, default inline `StageAfterStart` handler registered in `NewWithOpts`, `EnableRunnerAutoDetection` implicit binding without duplicates and excluding transients and aliases); verify the 15.1 command passes, goleak is clean, and `make test` is green.
- [x] 15.3 REFACTOR: none needed, `Runnable` reuses `Kind` unchanged; verify `make test` and `make lint` pass.
- [x] 15.4 Add `ExampleAsRunnable` and `ExampleKind_Handle` (replacing the Runnable handler), godoc for `Runner` (one-shot) and `Runnable` (do not reassign), and the Runnable section of the component-kinds page; verify `go test -run 'Example(AsRunnable|Kind_Handle)' .` and `npm run build` in `docs/` pass.

## 16. Integration checks

- [ ] 16.1 Compare benchmarks: rerun the 0.1 command into `<scratch>/bench-after.txt` and run `benchstat bench-before.txt bench-after.txt`; verify the invoke and provide benchmarks show `~` (otherwise stop and investigate).
- [ ] 16.2 Run the full suite: `make test`, `make lint`, and `go vet ./... && go test -race ./...` in every `go.work` module; verify all pass.
- [ ] 16.3 Check traceability against design.md's Test Strategy table; verify `go test -race -run '<every test name in the table>' -v .` lists each test and subtest as PASS.
- [ ] 16.4 Validate the plan; verify `openspec validate add-start-lifecycle-and-kinds --strict` passes.

## Workflow follow-up

- Archive this change after review, then start `add-container-lifecycle-hooks` and `add-collection-invocation`, which build on it (the hooks change on the D6 phase names and points).
- Revisit the design Open Questions (clone of replaced handlers, exported `StartError`, and how `ExplainService` should show declared edges) when a consumer needs them.
