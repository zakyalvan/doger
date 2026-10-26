# Implementation notes

Records the measurements and findings that tasks ask to keep.

## Baseline (task 0.2)

- `make test`, `make lint` and per-module `go vet ./... && go test -race ./...` passed on every `go.work` module.
- `BenchmarkNew`: 13 allocs/op, 952 B/op.

## Group 3: implicit Runnable handler (F4)

- `BenchmarkNew`: 11 allocs/op, 896 B/op after the refactor.
- A first-time `Runnable.Handle` keeps the first slot, where `NewWithOpts` used to register the default handler, so handler order is unchanged. `TestRunnable_FirstSlot` pins this; a plain append fails it (verified with a temporary, uncommitted mutation).

## Group 4: healthCheckPool race (F5)

- `go test -race -count=2000 -run TestRootScope_HealthCheckDuringShutdown .` (50 iterations each, 100,000 HealthCheck/Shutdown races): 0 races, 0 panics. Not reproduced; the test stays as a race guard and no fix is made.

## Groups 5–11: container hooks, tests that passed on arrival

Each was shown to fail with a temporary, uncommitted mutation:

- 7.1 `TestContainerFailedHook_GuardErrors`: passes through the start-lifecycle guard; fails when Failed hooks are called before `beginStart`.
- 9.1 `TestContainerHooks_HookCallsShutdown*`: pass; the Started and Closed rows deadlock (1s test timeout) when Started fires at `pointStartersUp` or Closed fires before `finishClose`.
- 10.1 `TestContainerHooks_ShutdownDuringStart`: passes through the D7 abort checks; firing Started at `pointAfterStart` regardless of the outcome fails every row.
- 11.1 `TestContainerHooks_NilContext`: **both rows pass on arrival**, including the `ShutdownWithContext` row the task expected to fail: start-lifecycle already normalizes a nil Shutdown context at the top of `RootScope.ShutdownWithContext` (D6 step 0), so 11.2 needs no production change. Passing the raw argument to the hooks fails both rows.

## Groups 12–15

- 12.1 `TestContainerHooks_RegistrationOrder` and the `CloneWithOpts` row passed on arrival; reversing the hook iteration and copying hooks in `CloneWithOpts` fail them.
- 12.2 asks to copy `HookContainerPanic` in `copy()`, but the field is added by group 13; it is copied there, pinned by `TestContainerHooks_ClonePanicPolicy`.
- 15.1 tests passed on arrival through the start-lifecycle CAS guards; replacing the close-phase CAS in `beginClose` with an unconditional store panics `TestContainerHooks_ConcurrentShutdown` (close of closed channel).
- 15.3 gopls review: `lifecycleMu` is locked in `waitForClose`, `beginClose`, `finishClose`, `markReady`, `beginStart` and `finishStart` only. Each is a short critical section with a deferred unlock that calls no hook, provider, Starter or handler.

## Part K: collections

- 20.1 `TestInvokeAs_HookReceivesTypeName` passes on arrival (characterization); passing the real name to `onBeforeInvocation` in `invokeByGenericType` fails it.
- 22.1 `TestInvokeAll_ProviderScopeVisibility` passes on arrival through `virtualScope` delegation; registering "b" in the root scope fails it.
- 23.1 the `InvokeAll` rows of `TestInvokeAll_Empty` pass on arrival: the group 21 pipeline already allocates its result with `make([]T, 0, n)`. A nil result fails them.
- 25.1 `TestInvokeAll_DeeperCycle` passes on arrival; excluding every invoker-chain member fails it.
- 26.1 tests pass on arrival through `invokeServiceInstance`; skipping `addDependency` fails `EdgesOrderShutdown`, and passing the type name as hook name fails `HooksPerMember`.
- 27.1 tests pass on arrival; registering the members as transients makes the single-build counter assertion fail (100 calls instead of 1).
- 28.1 `TestInvokeAll_OverrideKeepsPosition` passed on arrival in 20 runs (iteration over 3 small-map entries happened to yield a, b, c); giving overrides a fresh registration index fails it.

### Benchmarks (task 28.4)

`go test -run '^$' -bench BenchmarkInvokeAll -benchmem ./bench/` (half of the visible services match, all built):

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `BenchmarkInvokeAll/10` | 4,708 | 2,497 | 33 |
| `BenchmarkInvokeAll/100` | 42,024 | 24,408 | 224 |
| `BenchmarkInvokeAll/1000` | 594,513 | 293,626 | 2,424 |
| `BenchmarkInvokeAllParallel` (100) | 46,748 | 24,406 | 224 |
- 31.1 `TestInvokeAllMap_IgnoresOrdered` passes on arrival; running the ordering pass in `InvokeAllMap` fails it. `TestMustInvokeAll_OrderPanic` also passes on arrival, because the raw escaping panic value is the error itself; after 31.2 it is the wrapped error naming the member.

## Part P: incremental start

- 33.1 `TestIncrementalStart_Phase`: "w was started 0 times" is read as "the operation started nothing". In the "Start then Shutdown" row the first `Start` itself starts "w" (the tenant scope exists before `Start`), so the test compares the count before and after the operation.
- 33.2 takes `pipelineMu` before the phase guard of `beginIncremental`, so a Shutdown that began while a Refresh waited for the lock is seen (`ErrStartAfterShutdown`). `Start` keeps its guard before the lock, as designed.
- 33.3 `runPipeline(ctx, roots)` takes no `incremental` flag: container hooks run outside it, so the flag would be unused.
- 35.1 `TestRefresh_Concurrent` passes on arrival through `pipelineMu`; without the lock, concurrent runs overwrite the shared run state and panic. `TestRefresh_ShutdownCancels` failed on arrival with a synctest deadlock (the Refresh was not cancelled).
- 35.3 gopls review: `pipelineMu` is locked only in `runStart` and `startIncremental`. `runStart` unlocks it before its deferred `finishStart` takes `lifecycleMu`; `startIncremental` takes `lifecycleMu` inside it (`beginIncremental`, `finishIncremental`), never the reverse. No Shutdown path takes it.
- 39.3 benchstat of the invoke benchmarks against the 0.2 baseline (10 runs each, not interleaved) flagged `InvokeLazyParallel` +8.2% and `InvokeAlias` +9.6% in time, with identical B/op and allocs/op. Re-measured interleaved against the baseline commit `ed65bdc` (separate worktree, 10 alternating runs): every invoke benchmark shows `~` (p ≥ 0.44). The first deltas were run-to-run noise on the shared VM.

## Part S

- 41.1 `TestScopeHooks_RootFiresNone` passes on arrival; firing scope hooks for the root's own scope fails it.
- 42.2: when a parent shuts down, child scopes shut down on their own goroutines (`wg.Go`), so a re-panic there would terminate the process. Child scope shutdown therefore keeps its first hook panic and the parent merges it after `wg.Wait`; it is dispatched once, on the calling goroutine (for root `Shutdown`, through the container's own dispatch). Pinned by an extra test, `TestScopeHooks_ObserverPanicThroughParent`.
- 45.1 the invalid-field message uses the struct name `InvokeStruct` passes to `invokeByTags` (`NameOf[S]()`, the full type name), like the existing "not assignable to field" error. The spec's `` `S.H` `` is read as shorthand for it.
- 45.3 `invokeByTags` is still above the gocyclo limit after extracting `injectCollectionField`, so its existing `//nolint:gocyclo` stays.

## Group 48: integration

### 48.1 benchmarks: first measurement

Interleaved comparison against the baseline commit `ed65bdc` (two test binaries, 10 alternating runs; `InvokeAlias`/`InvokeAs` re-run with 20):

| Benchmark | time | B/op | allocs/op |
|---|---|---|---|
| InvokeLazySteadyState, InvokeLazyParallel, InvokeNamed, InvokeTransient, InvokeNestedScopes | ~ | ~ | ~ |
| Provide | ~ | +0.18% | -0.09% |
| InvokeAlias | +3.0% (p=0.033, n=20) | ~ | ~ |
| InvokeAs | +13.9% (p=0.000, n=20) | 937 → 961 | 11 → 9 |
| New | ~ | 952 → 1232 | 13 → 11 (target met) |

- `InvokeAs`: design S5 makes selection deterministic, so it must evaluate every visible service; the previous code stopped at the first (random) match. In the benchmark (19 fillers, 1 match) that is about 10 more type-assignability checks per call. Inherent to S5 as designed.
- `InvokeAlias`: within noise or a small cost of `DAG.addDependency` now looking up the existing edge source before writing (Q3, strongest source kept). Allocations are unchanged.
- `New`: B/op grows with the new `InjectorOpts` and `RootScope` fields; allocations dropped to 11 as targeted.

Task 48.1 says to stop and investigate when invoke is not `~`; it is left unchecked pending a decision.

### 48.2, 48.3, 48.4

- `make test`, `make lint`, per-module `go vet ./... && go test -race ./...` on all 25 `go.work` modules, and `npm --prefix docs run lint` / `build`: all pass.
- Traceability: all 134 tests named in design.md pass (`-race -v`), and the subtest counts of the 51 tests with row counts match their scenarios.
- `openspec validate consolidate-pending-lifecycle-work --strict`: valid.

### 48.1 after optimization (decision: optimize, then re-measure)

Profile of `BenchmarkInvokeAs`: about 58% in `serviceForEachRec` (per-scope snapshot and map iteration, paid before this change too) and 26% in the selection callback, mostly the reflect assignability check. Two behavior-preserving optimizations:

- `selectByType` stops at the first scope that has a match (scopes are visited nearest first, each one whole) and asserts each wrapper's interface once.
- `DAG.addDependency` looks up each inner map once.

Re-measured interleaved against `ed65bdc` (20 alternating runs):

| Benchmark | before optimization | after |
|---|---|---|
| InvokeAlias | +3.0% | -5.7% (p=0.002) |
| InvokeAs | +13.9% | +12.3% (p=0.000) |
| other invoke benchmarks | ~ | ~ |
| Provide | ~ (p=0.912) | +6.1% (p=0.009), not reproduced across runs |
| New | ~ (p=0.190), 11 allocs/op | +7.0% (p=0.000), 11 allocs/op |

The remaining `InvokeAs` cost is the full scan of the nearest scope that deterministic selection needs; the benchmark registers 20 services in one scope, so the scope early-exit does not apply to it. Closing the gap further would need new cached state (for example an assignability index per scope, invalidated on registration, override and shutdown). 48.1 stays unchecked pending a decision.


### 48.1 final measurement and decision (accepted, closed)

Decision: accept the remaining `InvokeAs` cost and close 48.1. Before closing, `Provide` and `New` were re-measured to settle whether the earlier `Provide` +6.1% was real.

Final interleaved comparison against `ed65bdc` (20 alternating runs, head `b4564de`):

| Benchmark | time | B/op | allocs/op |
|---|---|---|---|
| InvokeAlias | -3.7% (p=0.017) | ~ | ~ |
| InvokeAs | +9.2% (p=0.000), 1.169µs → 1.276µs | 937 → 961 | 11 → 9 |
| Provide | ~ (p=0.758) | +0.18% | -0.09% |
| New | +6.7% (p=0.000), 730ns → 779ns | 952 → 1232 | 13 → 11 (target met) |

- `Provide` is `~`: the earlier +6.1% did not reproduce and was noise.
- `New` meets the allocation target. Its time and bytes grow with the new `InjectorOpts` and `RootScope` fields (hook slices, phase state, incremental-start sets); the gate checks only allocs/op.
- `InvokeAs` (+9.2%, about 0.1µs per call in a 20-service scope) is accepted: it is the cost of deterministic selection (S5), which must scan the whole nearest scope that has a match. It now allocates less than before. Closing the gap would need a cached assignability index, judged not worth its invalidation complexity. The cost is documented in the upgrade guide.

### 49. Forget shut-down services in the start bookkeeping

The design's Risks section required the `started`/`handled` entries of a scope's wrappers to be cleared on shutdown; no task covered it, so group 49 was added.

- RED: `TestRootScope_ForgetsShutDownServices` failed on all 3 rows (`worker still in started`, `job still in handled`; on `root.Shutdown()` also `keeper in started`). A first run failed for the wrong reason (`require.NoError` on the `*ShutdownReport` that `Shutdown` returns); the assertions were fixed to `Succeed` before the RED was accepted.
- GREEN: `serviceShutdown`, the one place a wrapper leaves its scope, now calls `RootScope.forget`. This covers child-scope shutdown, root shutdown and `ShutdownNamed`. The design sentence was updated to say so.
- Residual: a pipeline that already collected a wrapper as a candidate and starts it after a concurrent `ShutdownNamed` can re-add its entry. Starting a service that is being shut down concurrently is outside the container-start spec.
- `BenchmarkShutdown` against `ed65bdc` (15 interleaved runs): `~` on time (p=0.935), B/op and allocs/op.

## Part V: verification fixes (groups 50-64)

Each defect was first reproduced by a probe in a scratch copy of the repo during `/opsx:verify`, then by the in-repo RED test below. User decisions: V3 fail fast, V4 wait for the run, V5 drop the stale chain, V6 exact `map[string]`, V10 hook after unlock, V14 apply shadowing.

| Group | RED (seen before GREEN) | Notes |
|---|---|---|
| 50 | `TestContainerHooks_ShutdownCauseRace`: 259 and 278 of 20000 runs per row returned `DI: start aborted: %!w(<nil>)` | |
| 51 | `TestStart_ParallelHookPanic`: the test binary crashed with `panic: hook-boom`, created by `startStartersParallel` | |
| 52 | `TestIncrementalStart_DuringStart`: all 3 rows timed out (deadlock) | The task line expected the goroutine row to return nil; the scenario has a's Start await that goroutine, so it deadlocks as well. Unawaited, the verify probe saw nil after Start returned. |
| 53 | `TestScopeShutdown_WaitsForStart` (Shutdown returned while "w" was starting), `TestScopeShutdown_LaterStartSkips` ("w" started 1 time), `TestScopeShutdown_WaitBounded` (returned at 0s with a successful report) | `TestScopeShutdown_CreatedDuringStart` passed on arrival (it guards against waiting too much: a mutation that waits for every in-flight run, ignoring `runScopes`, makes it time out on a deadlock). The closing mark became a counter that lasts while the shutdown runs, because a directly shut-down child scope stays attached and may be reused; spec and design wording were aligned. The extracted functions are `beginClosing`/`endClosing` (the task named `awaitRunForScope`). |
| 54 | `TestCollection_AfterProviderReturned`: `circular dependency detected: app -> bus -> app`; `TestCollection_HeldByProvider`: edges [audit console] recorded | The HeldByProvider assertion changed with the agreed scenario: reads after the provider returned record no edge. |
| 55 | string-kinded key row: no error returned | |
| 56 | n/a: `TestStartError_Incremental`, `TestScopeStart_AncestorsNotStarted`, `TestStart_ParallelIncremental` passed on arrival | Mutations, each reverted: `StartError.Scope` forced to the root scope fails all 3 StartError rows; `Scope.Start` run on the root scope fails AncestorsNotStarted (db started 1 time); parallel start disabled fails both ParallelIncremental rows (3s instead of 1s). |
| 57 | `TestAfterBuildHook_ReentersService`: all 3 rows timed out (deadlock) | |
| 58 | `TestDependencySource_UnmarshalText`: `json: cannot unmarshal string into Go struct field ... of type doger.DependencySource` | |
| 59 | `TestRootScope_ForgetsOverriddenServices`: 6 started entries instead of 1 | |
| 60 | `TestStart_Parallel/18446744073709551615`: Start returned at 0s with no Starter started | The `nolint:gosec` is gone; the clamped conversion no longer triggers it. |
| 61 | `TestInvokeAs_Shadowing`: both shadowed rows returned the hidden root service | Writing the test showed that the struct fallback returns `ErrServiceNotFound` when no service matches (its error path is unchanged), so that spec row was corrected from `ErrServiceNotMatch`. |
| 62, 63 | refactors | Suites unchanged before and after each step. |

### 61.4 InvokeAs after shadowing

Interleaved against `ed65bdc` (20 alternating runs) with shadowing in place: `InvokeAs` +6.9% (p=0.001, 1.229µs → 1.314µs), 937 → 961 B/op, 11 → 9 allocs/op; `InvokeAlias` -5.8%. The shadow check runs only for matching candidates and walks parent pointers, so it adds no allocation and no measurable time over the +9.2% accepted in 48.1.

### 64. Integration checks after the verification fixes

- 64.1: `make test`, `make lint`, `go vet ./... && go test -race ./...` in all 25 `go.work` modules, the docs lint and build, and `openspec validate --strict`: all pass.
- 64.2: every Part V test in the design's Test Strategy passes under `-race -v`, with subtest counts matching the scenario rows (InvalidField 3 = 2 + 1 new row; `TestStart_Parallel` 6 = 5 + max uint).
- 64.3: interleaved against `ed65bdc` (20 alternating runs, head `4a799a2`):

| Benchmark | time | B/op | allocs/op |
|---|---|---|---|
| InvokeLazySteadyState, InvokeLazyParallel, InvokeNamed, InvokeTransient, InvokeNestedScopes | ~ | ~ | ~ |
| InvokeAlias | -5.4% (p=0.000) | ~ | ~ |
| InvokeAs | +7.6% (p=0.000) | 937 → 961 | 11 → 9 |
| Provide | ~ (p=0.242) | +0.18% | -0.09% |
| New | +7.7% (p=0.000) | 952 → 1232 | 13 → 11 |
| Shutdown | ~ (p=0.841) | ~ | ~ |

These match the 48.1 decision: `InvokeAs` stays within the accepted cost of deterministic selection, `New` meets the 11 allocs/op target, and the fixes add no measurable cost elsewhere.

## Part V2: second verification (groups 65-71)

The second `/opsx:verify` reproduced four defects with probe tests (the scratch copies of the repo), re-run before acting. The user asked to clear every warning and apply every suggestion; for suggestion 10 (descendants of a shutting-down scope) the recommended reading was taken: descendants are covered (V22).

| Group | RED (seen before GREEN) | Notes |
|---|---|---|
| 65 | `TestCollection_ReadDuringOuterProvider` timed out (deadlock) | A regression from V5 (`7ce61bd`): before it, the same read failed with `ErrCircularDependency`. `runningChain` was written as its own function in GREEN, so 65.3 needed no further step. |
| 66 | 5 rows failed: errors without `ErrStartAfterShutdown` (`container start aborted: context canceled`, `... veto`, the binding error, `cannot start: ... circular dependency ...`, `scope "tenant" start aborted: context canceled`) | The cycle row uses the existing "cycle closed by a dynamic edge" setup; its spec row text was aligned. |
| 67 | `TestScopeHooks_NilContext/ShutdownWithContext(nil)`: nil contexts recorded | The `StartWithContext(nil)` row passed on arrival (it already normalized nil). |
| 68 | `TestScopeShutdown_WaitBounded`: the shut-down wrapper was re-added to `started` | `stillRegistered` was written as its own function in GREEN. |
| 69 | `TestScopeShutdown_DescendantSkipped`: "w" started 1 time | |
| 70 | test-only | 70.1: 20000 runs pass after `synctest.Wait()`. 70.2: a mutation that skips bus's Shutdown passes the old order checks and fails the new ones; a mutation returning nil instead of an empty slice fails `TestInvokeAll_AfterShutdown`. 70.5: reads always detached fails `TestCollection_ReadDuringHolderProvider`; removing the default-root fallback on the collection path fails `TestCollection_NilInjector` (removing it only in `InvokeCollection` changes nothing, because `All()` falls back too). |
| 71 | integration | `make test`, `make lint`, all 25 `go.work` modules, docs lint and build, strict validation: pass. The Part V and V2 lifecycle tests ran 2000 times each under `-race` with no failure. |

Unrelated, pre-existing: `TestInvokeByName` panics when run with `-count` > 1 (it registers on `DefaultRootScope`); it passes with the default count, as `make test` runs it.

## Part V3: third verification (groups 72-85)

The third `/opsx:verify` and a `/code-review` reproduced five defects with probe tests (W1-W3 and code-review findings 1-3, two of them the same), and a sixth (V23) turned up while planning the fixes, proven by a probe before any change. The user chose the recommended option for every open decision, asked to fix V23 in this round, and to apply every suggestion including the code-review cleanups. Code-review finding 4 (a child scope shutting down between discovery and claim) was not reproduced and gets no change.

| Group | RED (seen before GREEN) | Notes |
|---|---|---|
| 72 | `TestCollection_HolderAfterSiblings` (both rows): the map held "d" (the holder) and lacked "e" | Found while planning: `append` reused spare capacity of the depth-3 chain. |
| 73 | `TestCollection_InvocationsWithoutProvider` (both rows): ``circular dependency detected: `e` -> `e` `` and `` `l` -> `l` `` | 73.3: no extraction; the three sites call differently typed `getInstance` methods, and V36 restructures them. |
| 74 | `TestStart_FailureThenShutdown` (Start and Refresh rows): ``in chain: "DI: failed to start service `a` in scope `[root]`: boom"``, no `ErrStartAfterShutdown` | 74.3: *Extract Function* `abortOutcome`, shared by `finishStart` and `finishIncremental`. |
| 75 | `TestStartError_ConsumerBuilt`: all 4 rows panicked with a nil pointer dereference | GREEN was written with `scopeName()` and `causeText()` from the start, so 75.3 needed no further step. |
| 76 | `TestInvokeAs_SkipsInvoker` ("base after decor" and "no other match") and `TestInvokeStruct_FallbackSkipsInvoker`: ``circular dependency detected: `decor` -> `decor` `` | The "base before decor" row passed on arrival, as expected (earliest registration). |
| 77 | `TestContainerFailedHook_PipelinePanic` (3 rows): hooks ran `[starting]` only and the Failed error was nil; `TestContainerFailedHook_PanicDuringPipelinePanic` (2 rows): nothing recorded | 77.3: *Extract Function* `failAndRepanic`. `runStartRecovering` tracks completion with a flag, not `recover() != nil`, so a `panic(nil)` under `GODEBUG=panicnil=1` is still handled. |
| 78 | test-only: `TestRefresh_RetriesFailedStarter` (2 rows), `TestKind_RetriesFailedHandler`, `TestKind_ScopeStartSubtree` passed on arrival | Mutations (reverted): marking a Starter started, or a component handled, on failure fails the retry tests (`expected: 2, actual: 1`); running `Scope.Start`'s pipeline over the root fails `TestKind_ScopeStartSubtree`. |
| 79 | `TestScopeStartedHook_SkippedWhileClosing` (2 rows): "w" was not started, but `HookScopeStarted` ran 1 time | The parent row holds the parent's Closing hook, which runs after "tenant" already shut down, so it isolates the ancestor check. |
| 80 | `TestInvokeAll_OrderPanic/string_boom`: ``panicked: DI: recovered panic: boom``; `TestScopeStartingHook_Veto` (2 panic rows): `DI: recovered panic: veto-boom`; `TestScopeHooks_LaterPanicLogged`: `DI: container scope-closing hook panicked: second` | 80.3: *Extract Function* `logHookPanic`, shared by `keepFirstPanic` and `failAndRepanic`. While running 80.2, `go build` hit "no space left on device": the Go build cache had grown to 28 GB across the verification worktrees; `go clean -cache` freed it. |
| 81.1 | test-only | Mutation (reverted): clearing `ticket.inFlight` for incremental runs in `beginClose` fails the strengthened `TestRefresh_ShutdownCancels` (`expected: 1s, actual: 0s`); before, the whole suite passed under it. |
| 81.2 | test-only | Mutation (reverted): the transient wrapper passing a second provider result to the hook fails `TestAfterBuildHook_Fires/transient`; with zero-size fixtures it passed. |
| 81.3 | test-only | Mutations (reverted): keeping the first-recorded source in `dag.go` fails `TestExplain_EdgeSourceStrongest`; an `UnmarshalText` that never assigns invoked fails the `invoked` row now that it starts from another source. |
| 81.4 | test-only | Mutation (reverted): passing `context.Background()` to the Started, Ready, Failed, Closing and Closed hooks fails all 5 rows of `TestContainerHooks_ObserverContexts`; before, the suite passed under it. `TestContainerHooks_NilContext` asserts 3 and 2 contexts. |
| 81.5 | test-only | Mutations (reverted), each failing only its new test: `InvokeAllMap` resolving from the plain scope instead of the provider's injector (`TestInvokeAllMap_FromProvider` times out: composite rebuilds itself); `InvokeAllMap` swallowing a member failure; dropping the alias filter; shutting built members down after a failure; building in collection order; an unordered `*` slice; one map shared across calls; `All()` returning no member (`TestCollection_ConcurrentReads`). The 81.2 fixture fields were renamed to blank fields after `unused` flagged them. |
| 81.6 | test-only | Mutations (reverted): a replacement that moves the kind to the end, and a `Handle` that ignores a replacement, each fail the clone row; the old test passed under both. |
| 81.7 | test-only | Mutations (reverted), each failing its new check: renaming the `scope-closing` point; skipping the cycle check (the cycle row still matched `ErrStartAfterShutdown` before it checked its own step); `Phase()` ignoring the closing phase; observer loops stopping at the first panic; a child `Scope.ShutdownWithContext` firing the container Closing hooks. A broad `git checkout -- '*.go'` meant to revert one mutation was blocked by the permission check, because it would also have discarded the uncommitted new tests; the tests were committed first, and each later revert named only the mutated file. |
| 81.8 | test-only | Mutations (reverted): marking every service of a started candidate's scope as started fails the explicit `repo` check (marking only the candidates cannot reach `repo`, which is not a Starter); a `Refresh` that does nothing fails `TestStart_LateScopeNotStarted`; a Start panic recovered without `%w` fails `TestStartError_StarterPanic`; a `MustInvokeAs` returning the last collection member fails its test; removing the eager or transient hook call fails its `TestAfterBuildHook_Panic` row; turning a typed nil into an untyped one fails `TestAfterBuildHook_TypedNil`; a shadow check of the invoking scope alone fails the nested row, once that row put `db` in the intermediate scope (its first version put it in the invoking scope and passed under the mutation). testify's `NotNil` treats a typed nil as nil, so `TestAfterBuildHook_TypedNil` checks the dynamic type. |
| 84 | refactors | Each step left `make test` and `make lint` green. 84.4 used Go 1.27 promoted fields in composite literals (the `modernize` linter's `embedlit`). 84.5: `BenchmarkNew` stays at 11 allocs/op, 1232 B/op. 84.6: see below. |

### 84.6 Invoker chain from parent links

Interleaved against the head before 84.6 (`a95ccee`, 10 alternating runs, same machine):

| Benchmark | time | B/op | allocs/op |
|---|---|---|---|
| InvokeLazySteadyState, InvokeLazyParallel, InvokeAs, InvokeAll/100, InvokeAllParallel | ~ | ~ | -1 per invocation (6 → 5, 9 → 8, 225 → 175) |
| InvokeNamed | -6.7% | ~ | 4 → 3 |
| InvokeTransient | -8.0% | ~ | 6 → 5 |
| InvokeAlias | -6.9% | 288 → 272 | 9 → 7 |
| InvokeNestedScopes | -6.7% | ~ | 6 → 5 |
| ProviderChainTransient | -9.6% | 880 → 832 | 24 → 20 |
| geomean | -5.3% | -0.9% | -18.6% |

### 85. Integration checks after the third verification

- 85.1: `make test`, `make lint`, `go vet ./... && go test -race ./...` in all 25 `go.work` modules, the docs lint and build, and `openspec validate --strict`: all pass on `cb9de50`.
  - A first module run, made while the stress run below loaded the machine and with its output discarded, failed once in the root module. It did not reproduce in 10 runs under the same load or in the final run. The failing test is unknown; the data race below, in the same package, is the likely cause but this is not confirmed.
- 85.2: the Part V, V2 and V3 lifecycle and collection tests ran 2000 times each under `-race`.
  - The first run reported one data race, in `TestContainerHooks_ShutdownDuringStart` ("Shutdown in the last step"), a test from Part H. Its shutdown function called `wg.Wait()` while the kind handler could still be calling `wg.Go()`, a Wait racing an Add at a zero counter. It reproduced once in 3000 isolated runs; with the fix (`cb9de50`, the function waits until Shutdown was launched), 6000 runs pass.
  - That run also hit `go test`'s 10-minute default timeout; the goroutine dump showed only test runners waiting on parallel subtests, with no stuck worker. The rerun with `-timeout 110m` passed: 688s, no race, no failure.
- 85.3: interleaved against the PR base `ed65bdc` (10 alternating runs, `cb9de50`):

| Benchmark | time | B/op | allocs/op |
|---|---|---|---|
| InvokeLazySteadyState, InvokeLazyParallel, InvokeTransient, InvokeNestedScopes | ~ | 192 → 208 | 6 → 5 |
| InvokeNamed | -5.6% (p=0.042) | 96 → 112 | 4 → 3 |
| InvokeAlias | -11.0% (p=0.000) | 256 → 272 | 9 → 7 |
| InvokeAs | +5.3% (p=0.001) | 937 → 1009 | 11 → 8 |
| ProviderChainTransient | -6.6% (p=0.005) | 816 → 832 | 24 → 20 |
| New | +8.1% (p=0.000) | 952 → 1232 | 13 → 11 |
| Provide, HealthCheck, Shutdown, ListProvidedServices, Ancestors | ~ | ~ | ~ |

The +16 B/op on invocations is the larger virtual scope (its invoker link, from V17); 84.6 then removed one allocation per invocation. `InvokeAs` and `New` stay within the costs accepted in 48.1. `NewFrameFromPC` (+7.5%, p=0.018) is in the untouched `stacktrace` package: noise.


## Part V4: fourth verification (groups 86-97)

The fourth `/opsx:verify` reproduced four defects with probe tests (D1-D4, design V37-V40) and asked for one decision (D5, V41). Every probe was run on `2012c60` before any change:

- D1 (`TestProbe_DiscoverClaimWindowSmall`, 3000 iterations per row): 128 failures (`` DI: failed to build service `w` in scope `tenant`: DI: could not find service `w` ``) without a root "w"; 75 double starts of the root "w" with one. A second probe, `Scope.Start` racing its own scope's `Shutdown` (200,000 iterations), was also written; after the fix it reports 0 failures.
- D2 (`TestProbe_AbortOutcomeCallerCancelledFirst`), D3 (`TestZZProbe_PipelinePanicAfterShutdownBegan`), D4 (`TestProbe_SkipWithAliasOfSelf`): failed on every run, with the messages below.

After the fixes, every probe passes (D1: 0 failures and 0 double starts in 3 × 3000 iterations per row). The probes stayed outside the repository.

| Group | RED (seen before GREEN) | Notes |
|---|---|---|
| 86 | `TestStart_ShutdownBetweenCollectAndClaim`: the Refresh and Scope.Start rows fail with `` DI: failed to build service `w` in scope `tenant`: DI: could not find service `w`, no service available ``; the two rows with a root "w" fail with the root "w" started 2 times. `TestScopeStartedHook_ShutdownBeforeClaim`: the same build error, Started hook ran 0 times | Deterministic: a white-box `pausingWrapper` blocks collection in its `getServiceType`, in a scope visited after "tenant", while `tenant.Shutdown()` runs to completion. 86.3: no extraction needed. |
| 87 | `TestStart_CallerCancelThenShutdown` (6 rows): no `ErrStartAfterShutdown` in ``DI: failed to start service `a` in scope `[root]`: boom (start aborted: context canceled)`` (sequential and parallel failure rows) or in `DI: start aborted: context canceled` (nil rows). `TestAbortOutcome`: the 4 caller-cause rows fail; the 2 Shutdown-cause rows pass, so those messages are unchanged | In RED, the "already aborted" row also asserted `context.Canceled`, which contradicts its spec row ("that error unchanged"); that assertion was corrected before GREEN. 87.3: *Extract Function* `shutdownCause`. |
| 88 | `TestContainerFailedHook_PipelinePanicAfterShutdown` (n=1, n=4): the Failed error was `DI: recovered panic: boom`, without `ErrStartAfterShutdown` | 88.3: *Inline Function* `runStartRecovering`, *Remove Parameter* `panicked` from `finishStart`. |
| 89 | `TestInvokeAs_SkipsInvoker` (3 alias rows) and `TestInvokeStruct_FallbackSkipsInvokerAlias`: ``circular dependency detected: `decor` -> `decor-alias` -> `decor` `` (chained row: `` `decor` -> `a1` -> `decor` ``) | `aliasResolvesTo` was written as its own function in GREEN. `TestAliasResolvesTo_Loop` covers the loop bound: an unbounded loop times out after 1s. While mutating for that test, `git checkout -- invoke.go` also reverted the uncommitted GREEN change in that file; it was re-applied and re-verified, and from then on every change was committed before a mutation. |
| 90 | `TestScopeStartingHook_SkippedWhileClosing` (2 rows): `HookScopeStarting ran`, expected 0, actual 1 | 90.3: *Extract Function* `scopeClosing`. |
| 91.1 | test-only: `TestIncrementalStart_CallerContext` (6 rows) passed on arrival | Mutations (reverted): `RefreshWithContext` passing `context.Background()` fails its value and cancelled rows; the same in `Scope.StartWithContext` fails its two rows; dropping either nil normalization panics the nil row (`cannot create context from nil parent`). |
| 91.2 | test-only | Mutation (reverted): `dispatchAfterStart` receiving the Start-time context fails the Started, Ready and Failed rows of `TestContainerHooks_ObserverContexts`, which now asserts the caller's context itself, not cancelled. |
| 91.3 | test-only | Mutations (reverted), each failing only its new check: `depth: 0` in `collectionCandidatesFor` (`TestInvokeAll_DefaultOrder/child_registered_first`); `slices.SortFunc` instead of `SortStableFunc` (`TestInvokeAll_OrderedStableBeyondTwelve`, 26 members, 13 per order value); removing the invocation-over marker in `invokeAnyByNameAs` or `invokeServiceInstance` (the struct-tag or by-type row of `TestCollection_InvocationsWithoutProvider`); removing the detached guard in `addDependency` (`TestCollection_DetachedReadRecordsNoEdge`); removing `read.parent = vScope.parent` (`TestCollection_DetachedReadKeepsRunningChain` deadlocks, 1s timeout). |
| 91.4 | test-only | Mutations (reverted): filtering aliases in `selectByType` fails `TestInvokeAs_Deterministic/an_alias_is_a_candidate`; dropping `regIndex` in `serviceLazy.clone` fails `.../clone_keeps_each_registration_index`. |
| 92 | `TestCollection_ReadDuringOuterProvider`: ``circular dependency detected: `app` -> `app` `` does not contain `` `app` -> `bus` -> `app` `` | Existing message assertions were unaffected. |
| 93 | refactor | `make test` and `make lint` green with no test change. |
| 94 | test-only | Mutations (reverted): the parallel coordinator re-panicking on receipt fails `TestStart_ParallelHookPanic/n=4,_HookAfterStart` (now that "b" returns at once); `markHandled` without `stillRegistered` fails `TestKind_HandledGuard`; an empty requirement map fails both rows of `TestStart_ParallelIncrementalDependencyOrder` (3 runs of 3, after making "a" the slowest Starter; with equal durations only the Refresh row failed); build hooks receiving a copy of the pointee fail the lazy, alias and eager-value rows of `TestAfterBuildHook_Fires`; changing the `started` or `closing` phase names fails `TestContainerPhase_String`; keeping the first edge source fails `TestExplain_EdgeSourceStrongest` (queue row); `MustInvokeAs` resolving from the root scope deadlocks `TestMustInvokeAs_FromProvider`; removing `onBeforeInvocation` from `invokeServiceInstance` fails `TestInvokeAll_HooksPerMember`; removing the invoker skip fails `TestInvokeAs_StoredInjectorSkipsHolder`. `TestScopeHooks_LaterPanicLogged` now starts the root, as its scenario says. |
| 95, 96 | docs | Godoc, proposal and design drift, upgrading page; eight Examples pass with `go test -race -run Example`. |

### 97. Integration checks after the fourth verification
