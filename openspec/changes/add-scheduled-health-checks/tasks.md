# Tasks

Implementation starts only after PR #8 (`consolidate-pending-lifecycle-work`) merges. Every RED test is seen failing for the expected reason before its GREEN step. Concurrency tests run under `-race` and `testWithTimeout`. "Regression Dn" refers to proposal.md, Defect. Groups 2-15 use containers that are never started, so they do not depend on the scheduler (group 16).

## 1. Baseline after PR #8 (scaffolding)

- [ ] 1.1 Merge `main` (with PR #8) into the implementation branch and confirm the names this change uses still exist (`PhaseReady`, `PhaseClosing`, `markReady`, `shutdownAsWinner`, its root-`*Scope` bypass, `HookContainerPanic`, `HealthCheckSingleTimeout`); record any rename in design.md H1/H10; verify `make test` and `make lint` pass before any change

## 2. A health check never holds a service's lock (regression D3)

- [ ] 2.1 RED: add `TestServiceLazy_HealthCheckWithoutLock` (rows Shutdown, Invoke) for scenario "A check in flight does not delay the service"; verify `go test -race -run TestServiceLazy_HealthCheckWithoutLock .` fails with the 1s bound exceeded on both rows
- [ ] 2.2 GREEN: in `serviceLazy.healthcheck`, copy the instance under the read lock and call the check after releasing it (design H8); verify the test passes and `make test` is green
- [ ] 2.3 REFACTOR: *Extract Function* `builtInstance()` shared by `isHealthchecker` and `healthcheck`; verify `make test` and `make lint` pass

## 3. Listing services never waits for a lazy service being built (regression D9)

- [ ] 3.1 RED: add `TestServiceLazy_HealthCheckDuringBuild` (2 rows) for scenario "A provider in progress does not block health checks"; verify `go test -race -run TestServiceLazy_HealthCheckDuringBuild .` fails on the HealthCheckWithContext row (blocked past 1s) or to compile naming only `HealthReport` for the second row (keep that row skipped with `t.Skip("group 12")` until group 12)
- [ ] 3.2 GREEN: add an atomic `built` flag to `serviceLazy`, set after the build completes, and make `builtInstance()` read it without the build lock (design H5); verify the first row passes and `make test` is green
- [ ] 3.3 REFACTOR: none needed: one field and one accessor; verify `make test` and `make lint` pass

## 4. A panicking health check returns an error (regression D7)

- [ ] 4.1 RED: add `TestHealthCheck_Panic` (rows without the HealthReport row, which group 12 enables) for scenario "Panic results"; verify `go test -race -run TestHealthCheck_Panic .` fails: the process exits on the "boom" row, or the build fails naming only `ErrHealthCheckPanic`
- [ ] 4.2 GREEN: add `ErrHealthCheckPanic` (errors.go) and recover where each check runs, building the error as in design H7 and calling `HookContainerPanic(root, "healthcheck", v)` when set, without recovering a panic it raises; verify the test passes and `make test` is green
- [ ] 4.3 REFACTOR: *Extract Function* `runHealthCheck(ctx, root, check) error` (recover plus classification) used by every path; verify `make test` and `make lint` pass

## 5. Shadowed and aliased services are checked once; forced results keep their shape (regressions D4, D5, D8)

- [ ] 5.1 RED: add `TestHealthCheck_ShadowAndAliasOnce` (4 rows, scenario "Each underlying check runs once") and `TestHealthCheck_MapShape` (2 rows, scenario "Map shape is unchanged"); verify `go test -race -run 'TestHealthCheck_(ShadowAndAliasOnce|MapShape)' .` fails on the call counts (MapShape passes on arrival; record that and show it can fail by temporarily dropping nil entries, not committed)
- [ ] 5.2 GREEN: add `visibleCheckers(scope)` (design H5): copy under each scope lock, nearest scope wins, alias resolves to its target's wrapper, non-checkers kept as nil entries; use it in `HealthCheckWithContext`; verify the tests pass and `make test` is green
- [ ] 5.3 REFACTOR: *Remove Dead Code* `asyncHealthCheckWithContext`'s ancestor merge and its `@TODO`; correct the `RootScope.HealthCheck` doc comment to "root scope's services" (D8); verify `make test` and `make lint` pass

## 6. A service never has two checks in flight (regression D1)

- [ ] 6.1 RED: add `TestHealthCheck_NoPileUp` (2 rows), `_JoinRunning` and `_NextCallChecksAgain` for scenarios "A check that ignores its context does not pile up", "A call that joins a running check gets its result" and "After a check finishes, the next call checks again"; verify `go test -race -run 'TestHealthCheck_(NoPileUp|JoinRunning|NextCallChecksAgain)' .` fails on NoPileUp's call count and JoinRunning's count (NextCallChecksAgain passes on arrival; record it)
- [ ] 6.2 GREEN: add the `healthMonitor` on `RootScope` with the `inflight` map keyed by wrapper and the `sharedCall` primitive with done, result and waiter count (design H2, H3); route the scope-wide and single-service paths through join-or-start; verify the tests pass and `make test` (including `healthcheck_timeout_test.go`) is green
- [ ] 6.3 REFACTOR: *Move Function* health execution code into `health_run.go`; verify `make test` and `make lint` pass

## 7. Each caller keeps its own bound; a check sees its starter's context

- [ ] 7.1 RED: add `TestHealthCheck_JoinerOwnBound` (6 rows), `_CallerLeaves`, `_StarterContext` (2 rows), `_LastCallerCancels` and `_AbandonedNotJoined` for the scenarios of the same names in health-check-execution; verify `go test -race -run 'TestHealthCheck_(JoinerOwnBound|CallerLeaves|StarterContext|LastCallerCancels|AbandonedNotJoined)' .` fails on the joiner rows, the lost deadline and the abandoned join
- [ ] 7.2 GREEN: per-waiter bounds from the call (design H10), cancel at zero waiters, abandoned calls kept in flight and never joined, check context from `context.WithoutCancel(starter)` with the starter's deadline and bound (design H3); verify the tests pass and `make test` is green
- [ ] 7.3 REFACTOR: *Introduce Parameter Object* `waitSpec{ctx, bound, pinned}` for join-or-start; *Inline Function* `raceWithTimeout`/`awaitHealthCheck` where they become single-use; verify `make test` and `make lint` pass

## 8. HealthCheckParallelism caps top-level checks without blocking callers or registration (regressions D2, D6)

- [ ] 8.1 RED: add `TestHealthCheck_ParallelismCap` (3 rows), `_SlotWaitHonorsContext`, `_SlotWaitDoesNotBlockProvide`, `_NoQueueLimit` and `TestNew_NoHealthGoroutine` (2 rows); verify `go test -race -run 'TestHealthCheck_(ParallelismCap|SlotWait|NoQueueLimit)|TestNew_NoHealthGoroutine' .` fails on the slot wait, Provide and the n=4 goroutine row
- [ ] 8.2 GREEN: replace `jobPool` with a `chan struct{}` limiter acquired by a queued call's goroutine, the check's bound starting after acquisition (design H6); remove `healthCheckPool` start/stop; verify the tests pass and `make test` is green
- [ ] 8.3 REFACTOR: *Remove Dead Code* `jobPool`, `newJobPool` and `queueServiceHealthcheck`'s pool branch; verify `make test` and `make lint` pass

## 9. Nested checks run inline; cycles are errors

- [ ] 9.1 RED: add `TestHealthCheck_NestedInline` (2 rows) and `TestHealthCheck_Cycle` (2 rows) for scenarios "Nested checks complete under a limit of one" and "Self-referencing checks"; verify `go test -race -run 'TestHealthCheck_(NestedInline|Cycle)' .` fails: NestedInline deadlocks past 1s, Cycle fails to compile naming only `ErrHealthCheckCycle`
- [ ] 9.2 GREEN: add the context marker with the running chain, inline nested calls without a slot, take-over of a queued call, and `ErrHealthCheckCycle` (design H16); verify the tests pass and `make test` is green
- [ ] 9.3 REFACTOR: *Extract Function* `nestedChain(ctx)`; verify `make test` and `make lint` pass

## 10. Concurrent forced runs on a scope share one run

- [ ] 10.1 RED: add `TestHealthCheck_ConcurrentShareRun`, `_SequentialRunAgain` (2 rows), `_ScopesSeparate` and `_JoinedRunNilEntries`; verify `go test -race -run 'TestHealthCheck_(ConcurrentShareRun|SequentialRunAgain|ScopesSeparate|JoinedRunNilEntries)' .` fails on ConcurrentShareRun's call counts
- [ ] 10.2 GREEN: coalesce `HealthCheckWithContext` per scope ID; publish the run with its nil entries; each caller waits with its own global and per-service bounds and copies the results (design H4); verify the tests pass and `make test` is green
- [ ] 10.3 REFACTOR: *Extract Function* `copyResults(partial, cause)`; verify `make test` and `make lint` pass

## 11. Report types and the overall state

- [ ] 11.1 RED: add `TestHealthState_String` (5 rows) and `TestHealthReport_OverallState` (10 rows); verify `go test -race -run 'TestHealthState_String|TestHealthReport_OverallState' .` fails to compile naming only `HealthState`, `HealthReport`, `ServiceHealth` and their constants
- [ ] 11.2 GREEN: add `HealthState` (with `HealthDegraded`), `ServiceHealth`, `HealthReport` and the overall-state function with the strict Unknown rule (design H15) in `health_report.go`; verify the tests pass and `make test` is green
- [ ] 11.3 REFACTOR: none needed: plain value types and one pure function; verify `make test` and `make lint` pass

## 12. Unmonitored reports: recording, on-demand checks, membership, coverage and order

- [ ] 12.1 RED: add `TestHealthReport_OnDemandTTL` (5 rows), `_OnDemandShared`, `_ReaderContext` (4 rows), `_ReaderLeaves`, `_Membership` (6 rows), `_Coverage` (3 rows), `_Order`, `_Isolated`, `_RefreshedByForced` (4 rows) and `_AbandonedNotRecorded`, and enable the HealthReport rows of tasks 3.1 and 4.1; verify `go test -race -run 'TestHealthReport_|TestServiceLazy_HealthCheckDuringBuild|TestHealthCheck_Panic' .` fails to compile naming only the `HealthReport` methods and `HealthReportCacheTTL`
- [ ] 12.2 GREEN: add `HealthReport`/`HealthReportWithContext` on `Scope`, `RootScope`, `virtualScope` and `Injector` (design H11, H13), `HealthReportCacheTTL` and `DefaultHealthReportCacheTTL`; record every completed check on the injector clock except abandoned ones; prune records on removal (design H2); in Idle with no scheduler, check stale covered services on demand within the reader's ctx and `HealthCheckTimeout`; order by scope path; verify the tests pass and `make test` is green
- [ ] 12.3 REFACTOR: *Extract Function* `containerCheckers(root)` sharing the per-scope copy with `visibleCheckers`; *Extract Function* `record(wrapper, result)` as the single write path; *Decompose Conditional* the phase switch into `readMode()`; verify `make test` and `make lint` pass
- [ ] 12.4 Add `ExampleScope_HealthReport` (container never started, on-demand read with TTL) and godoc for `HealthReportCacheTTL`; verify `go test -run ExampleScope_HealthReport .` passes

## 13. Health classification at registration

- [ ] 13.1 RED: add `TestHealthClassification_Default` (2 rows), `TestHealthTags_Constants` (2 rows), `TestHealthClassification_Tags` (4 rows), `TestHealthClassification_Optional` (3 rows), `TestHealthOptions_InvalidPanics` (5 rows) and `TestHealthClassification_CloneOverrideAlias` (4 rows); verify `go test -race -run 'TestHealthClassification_|TestHealthTags_Constants|TestHealthOptions_InvalidPanics' .` fails to compile naming only `WithHealthTags`, `AsOptionalHealth` and the tag constants
- [ ] 13.2 GREEN: add `WithHealthTags`, `AsOptionalHealth`, `HealthTagReadiness`, `HealthTagLiveness`, the two option fields with registration-time validation, and `Tags`/`Optional` on `ServiceHealth`, carried by `visibleCheckers`/`containerCheckers` (design H15); verify the tests pass and `make test` is green
- [ ] 13.3 REFACTOR: *Extract Function* `healthClassification(opts)` returning the effective tags and severity; verify `make test` and `make lint` pass

## 14. Tag-filtered reports

- [ ] 14.1 RED: add `TestHealthReport_MatchTags` (8 rows) and `TestHealthReport_MatchTagsOnDemand` for scenarios "Filtered reports" and "A filtered on-demand read checks only the covered services"; verify `go test -race -run 'TestHealthReport_MatchTags' .` fails to compile naming only `HealthReportOption` and `MatchHealthTags`
- [ ] 14.2 GREEN: add `HealthReportOption` and `MatchHealthTags`; filter the covered entries before the overall state and the on-demand stale set (design H15); verify the tests pass and `make test` is green
- [ ] 14.3 REFACTOR: *Introduce Parameter Object* `reportQuery{scope, tags}` passed through the report builder; verify `make test` and `make lint` pass
- [ ] 14.4 Add `ExampleMatchHealthTags` (readiness and liveness reports, one optional service) and godoc for the tag constants and options; verify `go test -run ExampleMatchHealthTags .` passes

## 15. A failing optional service is degraded

- [ ] 15.1 RED: add `TestHealthReport_Degraded` (5 rows) and `TestHealthCheck_IgnoresClassification` (3 rows); verify `go test -race -run 'TestHealthReport_Degraded|TestHealthCheck_IgnoresClassification' .` fails on the HealthDegraded rows (IgnoresClassification passes on arrival; record it and show it can fail by temporarily dropping optional errors, not committed)
- [ ] 15.2 GREEN: when recording a failing result of an optional service, store `HealthDegraded` with its error; forced paths keep returning the raw error (design H15); verify the tests pass and `make test` is green
- [ ] 15.3 REFACTOR: *Extract Function* `stateOf(result, optional)` as the single place mapping a result to a state; verify `make test` and `make lint` pass

## 16. Scheduling starts at Ready, runs at the interval, and checks new services at once

- [ ] 16.1 RED: add `TestHealthMonitor_StartsAtReady` (5 rows), `_NeverStartedNoGoroutine` (2 rows), `_Interval` (5 rows), `_NegativeInterval`, `_ConsecutiveRounds`, `_ClockJump` (2 rows), `_RemovedServices` (3 rows) and `_PromptCheck` (6 rows); verify `go test -race -run 'TestHealthMonitor_(StartsAtReady|NeverStarted|Interval|NegativeInterval|ConsecutiveRounds|ClockJump|RemovedServices|PromptCheck)' .` fails to compile naming only `HealthCheckInterval`, or on the Start rows
- [ ] 16.2 GREEN: add `HealthCheckInterval` and `DefaultHealthCheckInterval`; start the scheduler in `markReady` under `lifecycleMu` when the close phase is open (design H1); loop with dispatch, `clock.NewTimer(interval)` and the `wake` channel fed by the internal registration and after-build hooks (design H9); in the same step, audit the existing suite and `examples/*` (e.g. `kind_test.go` calls Start 24 times and Shutdown twice) and add the missing `Shutdown` with `t.Cleanup`, or set `HealthCheckInterval: -1` with a comment naming this behavior change; verify the tests pass, `make test` (goleak) is green and each `examples/*` module's `go test ./...` passes
- [ ] 16.3 REFACTOR: *Extract Function* `dispatchRound(onlyNew bool)`; *Move Function* scheduler code into `health_monitor.go`; verify `make test` and `make lint` pass

## 17. Scheduled checks have a default timeout and bound checks other paths started

- [ ] 17.1 RED: add `TestHealthMonitor_ScheduledTimeout` (4 rows), `_RoundBoundsForeignCheck`, `_BoundStartsAtSlot`, `TestHealthCheck_ForcedUnboundedByDefault` and `TestHealthReport_TimeoutThenLateResult`; verify `go test -race -run 'TestHealthMonitor_(ScheduledTimeout|RoundBoundsForeignCheck|BoundStartsAtSlot)|TestHealthCheck_ForcedUnboundedByDefault|TestHealthReport_TimeoutThenLateResult' .` fails on the 10s row, the foreign check and the late result (ForcedUnboundedByDefault passes on arrival; record it)
- [ ] 17.2 GREEN: add `DefaultScheduledHealthCheckTimeout`; rounds join or start calls as pinned waiters with the scheduled bound (design H9, H10); record a timeout at a check's own bound and the late result when it returns (design H3); verify the tests pass and `make test` is green
- [ ] 17.3 REFACTOR: *Extract Function* `scheduledBound(opts)`; verify `make test` and `make lint` pass

## 18. Scheduling stops at root shutdown; clones are independent

- [ ] 18.1 RED: add `TestHealthMonitor_StopsBeforeClosing` (2 rows), `_ShutdownSkipsHungCheck`, `_ShutdownCancelsCheck`, `_Clone` (3 rows), `_CloneShutdownKeepsSource` and `TestHealthReport_NoRecordAfterShutdown`; verify `go test -race -run 'TestHealthMonitor_(Stops|Shutdown|Clone)|TestHealthReport_NoRecordAfterShutdown' .` fails on rounds after Shutdown, the provider-injector row, goleak or the recorded result
- [ ] 18.2 GREEN: stop the scheduler first in `shutdownAsWinner` and on the root-`*Scope` bypass path (set `closing`, cancel the monitor context, wait for the scheduler goroutine within Shutdown's ctx, release pinned waiters) (design H1); verify the tests pass and `make test` is green
- [ ] 18.3 REFACTOR: *Extract Function* `stopMonitor(ctx)`; verify `make test` and `make lint` pass

## 19. Monitored and phase-dependent reads

- [ ] 19.1 RED: add `TestHealthReport_MonitoredNoChecks`, `_BeforeFirstResult`, `_Entries` (3 rows), `_OnDemandByPhase` (8 rows), `_ClosingKeepsResults`, `_Closed` and `_LivenessZeroConfig` (4 rows); verify `go test -race -run 'TestHealthReport_(MonitoredNoChecks|BeforeFirstResult|Entries|OnDemandByPhase|ClosingKeepsResults|Closed|LivenessZeroConfig)' .` fails on Monitored, the Ready rows or the closed state
- [ ] 19.2 GREEN: `readMode()` returns monitored while the scheduler runs, on-demand for Idle, Failed and Ready with no scheduler, records only for Starting, Started and Closing, and empty Unknown when closed (design H11); verify the tests pass and `make test` is green
- [ ] 19.3 REFACTOR: none needed: branches of `readMode()`; verify `make test` and `make lint` pass
- [ ] 19.4 Add `ExampleRootScope_HealthReport` (started container, default interval, opt-out with `HealthCheckInterval: -1` and on-demand reads) and godoc for `HealthCheckInterval`, and the `Start` doc's note that Shutdown stops scheduling; verify `go test -run ExampleRootScope_HealthReport .` passes

## 20. Health-change hooks

- [ ] 20.1 RED: add `TestHookServiceHealthChanged` (3 rows), `_ForcedRun`, `_Serialized`, `_Reentrant` (3 rows), `TestHookContainerHealthChanged` (4 rows), `_Membership`, `TestHealthHooks_Panic` (2 rows), `TestHealthHooks_Degraded` and `TestHealthMonitor_PanicKeepsScheduling`; verify `go test -race -run 'TestHook(Service|Container)HealthChanged|TestHealthHooks_|TestHealthMonitor_PanicKeepsScheduling' .` fails to compile naming only the hook fields and `Add*HealthChangedHook`
- [ ] 20.2 GREEN: add `HookServiceHealthChanged`, `HookContainerHealthChanged`, their copy-on-write `Add*` methods and their copies in `InjectorOpts.copy()`; enqueue transitions on record, prune and prompt checks, and deliver them in order with a single deliverer and no lock held (design H12); recover hook panics to `HookContainerPanic(root, "healthchanged", v)` or `Logf`; verify the tests pass and `make test` is green
- [ ] 20.3 REFACTOR: *Extract Function* `deliverTransitions()`; verify `make test` and `make lint` pass
- [ ] 20.4 Add `ExampleRootScope_AddServiceHealthChangedHook` and godoc stating delivery order, re-entrancy and that hooks should be fast; verify `go test -run ExampleRootScope_AddServiceHealthChangedHook .` passes

## 21. The container measures health time on its clock

- [ ] 21.1 RED: add `TestClock_HealthReportTimes` and `TestClock_HealthSchedulingOnClock` (2 rows) for the container-clock scenarios; verify `go test -race -run 'TestClock_Health' .` fails, or passes on arrival because groups 12-20 already used the clock (record which, and show the assertion can fail by temporarily using a real clock, not committed)
- [ ] 21.2 GREEN: read every health time from `opts.Clock` where any path still uses wall time; verify the tests pass and `make test` is green
- [ ] 21.3 REFACTOR: none needed if 21.1 passed on arrival; otherwise *Replace Inline Code with Function Call* to the clock helper; update the `InjectorOpts.Clock` godoc list; verify `make test` and `make lint` pass

## 22. Integration checks

- [ ] 22.1 Add `BenchmarkHealthReport_Monitored` (1,000 services) and record its result in design.md Test Strategy; verify `go test -run '^$' -bench BenchmarkHealthReport_Monitored -benchmem .` runs
- [ ] 22.2 Run `make test`, `make lint`, and `go test ./...` in every `http/*` and `examples/*` module; verify all pass
- [ ] 22.3 Cross-check every `#### Scenario:` in the five spec deltas against design.md Test Strategy and a passing test; verify `openspec validate add-scheduled-health-checks --strict` passes

## Workflow follow-up

- Open the follow-up change for the HTTP health handlers in `http/*` (readiness and liveness, serving tag-filtered `HealthReport` as JSON; 200 for Healthy and Degraded, 503 otherwise), then the next items in `ROADMAP.md`.
- Archive this change after review, then verify the synced specs.
