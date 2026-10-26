# Tasks

Implementation starts only after PR #8 (`consolidate-pending-lifecycle-work`) merges. Every RED test is seen failing for the expected reason before its GREEN step. Concurrency tests run under `-race` and `testWithTimeout`. "Regression Dn" refers to proposal.md, Defect.

## 1. Baseline after PR #8 (scaffolding)

- [ ] 1.1 Merge `main` (with PR #8) into the implementation branch and confirm the names this change uses still exist (`PhaseReady`, `PhaseClosing`, `markReady`, `shutdownAsWinner`, `HookContainerPanic`, the single-service timeout option); record any rename in design.md H1/H10; verify `make test` and `make lint` pass before any change

## 2. A health check never holds a service's lock (regression D3)

- [ ] 2.1 RED: add `TestServiceLazy_HealthCheckWithoutLock` (rows Shutdown, Invoke, ExplainNamedService) for scenario "A check in flight does not delay the service"; verify `go test -race -run TestServiceLazy_HealthCheckWithoutLock .` fails with the 1s bound exceeded
- [ ] 2.2 GREEN: in `serviceLazy.healthcheck`/`isHealthchecker`, copy `built` and `instance` under the read lock and call the check after releasing it (design H8); verify the test passes and `make test` is green
- [ ] 2.3 REFACTOR: *Extract Function* `builtInstance()` shared by `isHealthchecker` and `healthcheck`; verify `make test` and `make lint` pass

## 3. A panicking health check returns an error (regression D7)

- [ ] 3.1 RED: add `TestHealthCheck_Panic` (4 rows) for scenario "Panic results"; verify `go test -race -run TestHealthCheck_Panic .` fails: the process exits on the "boom" row, or the build fails naming only `ErrHealthCheckPanic`
- [ ] 3.2 GREEN: add `ErrHealthCheckPanic` (errors.go) and recover in the goroutine or function that calls each check, building the error as in design H7 and calling `HookContainerPanic(root, "healthcheck", v)` when set; verify the test passes and `make test` is green
- [ ] 3.3 REFACTOR: *Extract Function* `runHealthCheck(ctx, root, check) error` (recover plus classification) used by every path; verify `make test` and `make lint` pass

## 4. Shadowed and aliased services are checked once; forced results keep their shape (regressions D4, D5, D8)

- [ ] 4.1 RED: add `TestHealthCheck_ShadowAndAliasOnce` (4 rows, scenario "Each underlying check runs once") and `TestHealthCheck_MapShape` (2 rows, scenario "Map shape is unchanged"); verify `go test -race -run 'TestHealthCheck_(ShadowAndAliasOnce|MapShape)' .` fails on the call counts (MapShape passes on arrival; record that and show it can fail by temporarily dropping nil entries, not committed)
- [ ] 4.2 GREEN: add `visibleCheckers(scope)` (design H5): copy under each scope lock, nearest scope wins, alias resolves to its target's wrapper, non-checkers kept as nil entries; use it in `HealthCheckWithContext`; verify the tests pass and `make test` is green
- [ ] 4.3 REFACTOR: *Remove Dead Code* `asyncHealthCheckWithContext`'s ancestor merge and its `@TODO`; *Rename* the root doc comment to "root scope's services" (D8); verify `make test` and `make lint` pass

## 5. A service never has two checks in flight (regression D1)

- [ ] 5.1 RED: add `TestHealthCheck_NoPileUp` (2 rows), `TestHealthCheck_NextCallChecksAgain` for scenarios "A check that ignores its context does not pile up" and "After a check finishes, the next call checks again"; verify `go test -race -run 'TestHealthCheck_(NoPileUp|NextCallChecksAgain)' .` fails on NoPileUp's 100 calls and its goroutine count (NextCallChecksAgain passes on arrival; record it)
- [ ] 5.2 GREEN: add the `healthMonitor` on `RootScope` with an `inflight` map keyed by wrapper and the `sharedCall` primitive (design H2, H3); route the scope-wide and single-service paths through join-or-start; verify the tests pass and `make test` (including `healthcheck_timeout_test.go`) is green
- [ ] 5.3 REFACTOR: *Inline Function* `raceWithTimeout`/`awaitHealthCheck` into the shared-call wait where they become single-use; *Move Function* health execution code into `health_run.go`; verify `make test` and `make lint` pass

## 6. Each caller waits only as long as its own context allows

- [ ] 6.1 RED: add `TestHealthCheck_CallerLeaves` and `TestHealthCheck_LastCallerCancels` for scenarios "One caller leaving does not cancel the check for the others" and "The only caller leaving cancels the check"; verify `go test -race -run 'TestHealthCheck_(CallerLeaves|LastCallerCancels)' .` fails on B's result or on the recorded cancellation
- [ ] 6.2 GREEN: add the waiter count with cancel at zero, and a check context built from `context.WithoutCancel(starter)` plus its own bound and `withHealthCheckDeadline` (design H3); verify the tests pass and `make test` is green
- [ ] 6.3 REFACTOR: *Introduce Parameter Object* `checkStart{ctx, bound, pinned}` for join-or-start; verify `make test` and `make lint` pass

## 7. HealthCheckParallelism limits checks without blocking callers or registration (regressions D2, D6)

- [ ] 7.1 RED: add `TestHealthCheck_ParallelismCap` (3 rows), `_SlotWaitHonorsContext`, `_SlotWaitDoesNotBlockProvide`, `_NoQueueLimit` and `TestNew_NoHealthGoroutine` (2 rows); verify `go test -race -run 'TestHealthCheck_(ParallelismCap|SlotWait|NoQueueLimit)|TestNew_NoHealthGoroutine' .` fails on the slot wait, Provide and the n=4 goroutine row
- [ ] 7.2 GREEN: replace `jobPool` with a `chan struct{}` limiter acquired with the check context inside the check goroutine (design H6); remove `healthCheckPool` start/stop; verify the tests pass and `make test` is green
- [ ] 7.3 REFACTOR: *Remove Dead Code* `jobPool`, `newJobPool`, `queueServiceHealthcheck`'s pool branch; verify `make test` and `make lint` pass

## 8. Concurrent forced runs on a scope share one run

- [ ] 8.1 RED: add `TestHealthCheck_ConcurrentShareRun`, `TestHealthCheck_SequentialRunAgain` (2 rows), `TestHealthCheck_ScopesSeparate`; verify `go test -race -run 'TestHealthCheck_(ConcurrentShareRun|SequentialRunAgain|ScopesSeparate)' .` fails on ConcurrentShareRun's call counts
- [ ] 8.2 GREEN: coalesce `HealthCheckWithContext` per scope ID with a partial result map, `HealthCheckGlobalTimeout` from the run's start, per-caller copies and timeout fill-in on leave (design H4); verify the tests pass and `make test` is green
- [ ] 8.3 REFACTOR: *Extract Function* `copyResults(partial, ctxErr)`; verify `make test` and `make lint` pass

## 9. Report types and the overall state

- [ ] 9.1 RED: add `TestHealthState_String` (5 rows) and `TestHealthReport_OverallState` (11 rows, including HealthDegraded); verify `go test -race -run 'TestHealthState_String|TestHealthReport_OverallState' .` fails to compile naming only `HealthState`, `HealthReport`, `ServiceHealth` and their constants
- [ ] 9.2 GREEN: add `HealthState` (with `HealthDegraded`), `ServiceHealth`, `HealthReport` and the overall-state function (design H15 rule) in `health_report.go`; verify the tests pass and `make test` is green
- [ ] 9.3 REFACTOR: none needed: plain value types and one pure function; verify `make test` and `make lint` pass

## 10. Report membership, coverage, order and isolation

- [ ] 10.1 RED: add `TestHealthReport_Membership` (6 rows), `_Coverage` (3 rows), `_Order`, `_Isolated`, `_Entries` (3 rows); verify `go test -race -run 'TestHealthReport_(Membership|Coverage|Order|Isolated|Entries)' .` fails to compile naming only `HealthReport` methods
- [ ] 10.2 GREEN: add `HealthReport`/`HealthReportWithContext` on `Scope`, `RootScope`, `virtualScope` and `Injector` (design H11, H13), building entries from `records` via `visibleCheckers` and `containerCheckers`; record each completed check's result, time and duration on the injector clock; verify the tests pass and `make test` is green
- [ ] 10.3 REFACTOR: *Extract Function* `containerCheckers(root)` sharing the per-scope copy with `visibleCheckers`; verify `make test` and `make lint` pass

## 11. Every completed check refreshes the report

- [ ] 11.1 RED: add `TestHealthReport_RefreshedByForced` (4 rows) and `TestHealthReport_AbandonedNotRecorded`; verify `go test -race -run 'TestHealthReport_(RefreshedByForced|AbandonedNotRecorded)' .` fails on the single-service rows or on the abandoned record
- [ ] 11.2 GREEN: record from every path inside the shared call, skipping results marked abandoned (design H3); verify the tests pass and `make test` is green
- [ ] 11.3 REFACTOR: *Extract Function* `record(wrapper, result)` as the single write path; verify `make test` and `make lint` pass

## 12. Idle or failed reports run stale checks on demand, bounded by the caller's context

- [ ] 12.1 RED: add `TestHealthReport_OnDemandTTL` (5 rows), `_OnDemandShared`, `_OnDemandByPhase` (7 rows), `_ClosingKeepsResults`, `_Closed`, `_ReaderContext` (4 rows), `_ReaderLeaves`; verify `go test -race -run 'TestHealthReport_(OnDemand|Closing|Closed|Reader)' .` fails on the TTL and phase rows, or to compile naming only `HealthReportCacheTTL`
- [ ] 12.2 GREEN: add `HealthReportCacheTTL` and `DefaultHealthReportCacheTTL`; in `HealthReportWithContext`, check stale covered services when `Phase()` is Idle or Failed and scheduling is on, waiting on ctx only, and fill abandoned entries for that reader (design H11); verify the tests pass and `make test` is green
- [ ] 12.3 REFACTOR: *Decompose Conditional* the phase switch into `readMode()`; verify `make test` and `make lint` pass
- [ ] 12.4 Add `ExampleScope_HealthReport` (idle container, on-demand read with TTL) and godoc for `HealthReportCacheTTL`; verify `go test -run ExampleScope_HealthReport .` passes

## 13. Health classification at registration

- [ ] 13.1 RED: add `TestHealthClassification_Registration` (9 rows), `TestHealthTags_Constants` (2 rows), `TestHealthOptions_InvalidPanics` (5 rows) and `TestHealthClassification_CloneOverrideAlias` (4 rows) for the health-classification scenarios "Classification of a registration", "Predefined tag values", "Invalid classification" and "Classification across clone, override and alias"; verify `go test -race -run 'TestHealthClassification_|TestHealthTags_Constants|TestHealthOptions_InvalidPanics' .` fails to compile naming only `WithHealthTags`, `AsOptionalHealth` and the tag constants
- [ ] 13.2 GREEN: add `WithHealthTags`, `AsOptionalHealth`, `HealthTagReadiness`, `HealthTagLiveness`, the two option fields with registration-time validation, and `Tags`/`Optional` on `ServiceHealth`, carried by `visibleCheckers`/`containerCheckers` (design H15); verify the tests pass and `make test` is green
- [ ] 13.3 REFACTOR: *Extract Function* `healthClassification(opts)` returning the effective tags and severity; verify `make test` and `make lint` pass

## 14. Tag-filtered reports and zero-config liveness

- [ ] 14.1 RED: add `TestHealthReport_MatchTags` (8 rows), `TestHealthReport_MatchTagsOnDemand` and `TestHealthReport_LivenessZeroConfig` (4 rows) for scenarios "Filtered reports", "A filtered on-demand read checks only the covered services" and "Zero-config liveness"; verify `go test -race -run 'TestHealthReport_(MatchTags|LivenessZeroConfig)' .` fails to compile naming only `HealthReportOption` and `MatchHealthTags`
- [ ] 14.2 GREEN: add `HealthReportOption` and `MatchHealthTags`; make `HealthReport`/`HealthReportWithContext` variadic and filter the covered entries before the overall state and the on-demand stale set (design H15); verify the tests pass and `make test` is green
- [ ] 14.3 REFACTOR: *Introduce Parameter Object* `reportQuery{scope, tags}` passed through the report builder; verify `make test` and `make lint` pass
- [ ] 14.4 Add `ExampleMatchHealthTags` (readiness and liveness reports, one optional service) and godoc for the tag constants and options; verify `go test -run ExampleMatchHealthTags .` passes

## 15. A failing optional service is degraded

- [ ] 15.1 RED: add `TestHealthReport_Degraded` (5 rows) and `TestHealthCheck_IgnoresClassification` (3 rows) for scenarios "Degraded entries" and "Forced results ignore classification"; verify `go test -race -run 'TestHealthReport_Degraded|TestHealthCheck_IgnoresClassification' .` fails on the HealthDegraded rows (IgnoresClassification passes on arrival; record it and show it can fail by temporarily dropping optional errors, not committed)
- [ ] 15.2 GREEN: when recording a failing result of an optional service, store `HealthDegraded` with its error; forced paths keep returning the raw error (design H15); verify the tests pass and `make test` is green
- [ ] 15.3 REFACTOR: *Extract Function* `stateOf(result, optional)` as the single place mapping a result to a state; verify `make test` and `make lint` pass

## 16. Scheduling starts at Ready and runs at the interval

- [ ] 16.1 RED: add `TestHealthMonitor_StartsAtReady` (6 rows), `_NeverStartedNoGoroutine`, `_Interval` (6 rows), `_ConsecutiveRounds`, `_ClockJump` (2 rows), `_LateServices` (6 rows), `_RemovedServices` (3 rows); verify `go test -race -run 'TestHealthMonitor_(StartsAtReady|NeverStarted|Interval|ConsecutiveRounds|ClockJump|LateServices|RemovedServices)' .` fails to compile naming only `HealthCheckInterval`, or on the Start rows
- [ ] 16.2 GREEN: add `HealthCheckInterval` and `DefaultHealthCheckInterval`; start the scheduler in `markReady` under `lifecycleMu` when the close phase is open (design H1); loop dispatch, then `clock.NewTimer(interval)`, skipping services with a check in flight, with pruning (design H2, H9); verify the tests pass and `make test` is green
- [ ] 16.3 Audit the existing suite and `examples/*` for tests that call `Start` without `Shutdown`, or count health checks after `Start`; add the missing `Shutdown` or set `HealthCheckInterval: -1` with a comment naming this behavior change; verify `make test` (goleak) and each `examples/*` module's `go test ./...` pass
- [ ] 16.4 REFACTOR: *Extract Function* `dispatchRound()`; *Move Function* scheduler code into `health_monitor.go`; verify `make test` and `make lint` pass

## 17. Scheduled checks have a default timeout

- [ ] 17.1 RED: add `TestHealthMonitor_ScheduledTimeout` (4 rows) and `TestHealthCheck_ForcedUnboundedByDefault`; verify `go test -race -run 'TestHealthMonitor_ScheduledTimeout|TestHealthCheck_ForcedUnboundedByDefault' .` fails on the 10s row (ForcedUnboundedByDefault passes on arrival; record it)
- [ ] 17.2 GREEN: add `DefaultScheduledHealthCheckTimeout`; the scheduler starts checks with the bound of design H10; verify the tests pass and `make test` is green
- [ ] 17.3 REFACTOR: *Extract Function* `scheduledBound(opts)`; verify `make test` and `make lint` pass

## 18. Scheduling stops at root shutdown; clones are independent

- [ ] 18.1 RED: add `TestHealthMonitor_StopsBeforeClosing`, `_ShutdownSkipsHungCheck`, `_ShutdownCancelsCheck`, `_Clone` (2 rows), `_CloneShutdownKeepsSource`; verify `go test -race -run 'TestHealthMonitor_(Stops|Shutdown|Clone)' .` fails on rounds after Shutdown or on goleak
- [ ] 18.2 GREEN: stop the scheduler first in `shutdownAsWinner` (flag under `lifecycleMu`, cancel the monitor context, wait for the scheduler goroutine, release pinned references) (design H1); verify the tests pass and `make test` is green
- [ ] 18.3 REFACTOR: *Extract Function* `stopMonitor()`; verify `make test` and `make lint` pass

## 19. A monitored container's report never runs a check

- [ ] 19.1 RED: add `TestHealthReport_MonitoredNoChecks` and `_BeforeFirstResult`; verify `go test -race -run 'TestHealthReport_(MonitoredNoChecks|BeforeFirstResult)' .` fails on Monitored or PendingSince
- [ ] 19.2 GREEN: `readMode()` returns monitored while the scheduler runs; entries show `PendingSince` from the in-flight call; verify the tests pass and `make test` is green
- [ ] 19.3 REFACTOR: none needed: one branch of `readMode()`; verify `make test` and `make lint` pass
- [ ] 19.4 Add `ExampleRootScope_HealthReport` (started container, default interval, opt-out with `HealthCheckInterval: -1`) and godoc for `HealthCheckInterval` and the `Start` doc's note that Shutdown stops scheduling; verify `go test -run ExampleRootScope_HealthReport .` passes

## 20. Health-change hooks

- [ ] 20.1 RED: add `TestHookServiceHealthChanged` (3 rows), `_ForcedRun`, `_Serialized`, `TestHookContainerHealthChanged` (4 rows), `TestHealthHooks_Panic` (2 rows), `TestHealthHooks_Degraded`, `TestHealthMonitor_PanicKeepsScheduling`; verify `go test -race -run 'TestHook(Service|Container)HealthChanged|TestHealthHooks_(Panic|Degraded)|TestHealthMonitor_PanicKeepsScheduling' .` fails to compile naming only the hook fields and `Add*HealthChangedHook`
- [ ] 20.2 GREEN: add `HookServiceHealthChanged`, `HookContainerHealthChanged`, their `Add*` methods and their copies in `InjectorOpts.copy()`; deliver as in design H12 with per-hook recovery to `HookContainerPanic(root, "healthchanged", v)` or `Logf`; verify the tests pass and `make test` is green
- [ ] 20.3 REFACTOR: *Extract Function* `deliverHealthTransitions(ts)`; verify `make test` and `make lint` pass
- [ ] 20.4 Add `ExampleRootScope_AddServiceHealthChangedHook` and godoc stating hooks are synchronous and must be fast; verify `go test -run ExampleRootScope_AddServiceHealthChangedHook .` passes

## 21. The container measures health time on its clock

- [ ] 21.1 RED: add row `health check` to `TestClock_ReportedDurations` and `TestClock_HealthSchedulingOnClock` (2 rows) for the container-clock scenarios; verify `go test -race -run 'TestClock_(ReportedDurations|HealthSchedulingOnClock)' .` fails, or passes on arrival because groups 10-20 already used the clock (record which, and show the assertion can fail by temporarily using a real clock, not committed)
- [ ] 21.2 GREEN: read every health time from `opts.Clock` where any path still uses wall time; verify the tests pass and `make test` is green
- [ ] 21.3 REFACTOR: none needed if 18.1 passed on arrival; otherwise *Replace Inline Code with Function Call* to the clock helper; update the `InjectorOpts.Clock` godoc list; verify `make test` and `make lint` pass

## 22. Integration checks

- [ ] 22.1 Add `BenchmarkHealthReport_Monitored` (1,000 services) and record its result in design.md Test Strategy; verify `go test -run '^$' -bench BenchmarkHealthReport_Monitored -benchmem .` runs
- [ ] 22.2 Run `make test`, `make lint`, and `go test ./...` in every `http/*` and `examples/*` module; verify all pass
- [ ] 22.3 Cross-check every `#### Scenario:` in the five spec deltas against design.md Test Strategy and a passing test; verify `openspec validate add-scheduled-health-checks --strict` passes

## Workflow follow-up

- Open the follow-up change for the HTTP health handlers in `http/*` (readiness and liveness, serving tag-filtered `HealthReport` as JSON; 200 for Healthy and Degraded, 503 otherwise), then the next items in `ROADMAP.md`.
- Archive this change after review, then verify the synced specs.
