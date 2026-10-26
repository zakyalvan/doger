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
