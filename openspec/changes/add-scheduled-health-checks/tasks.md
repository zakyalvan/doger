# Tasks

The branch is rebased on `main` at `76404ef`. Every RED test must be seen failing for the expected reason before its GREEN step. Concurrency tests run under `-race` and `testWithTimeout`. "Regression Dn" refers to proposal.md, Defect. The probe file from the 2026-10-28 re-verification seeds the D1–D9 RED tests. Groups 2–16 use containers without a `HealthScheduler`. Groups 17–19 add scheduling, and group 21 adds the gocron module.

## 1. Baseline

- [x] 1.1 Confirm the names this change builds on still exist on the base (`markReady`, `dispatchAfterStart`, `shutdownAsWinner` and its root-`*Scope` bypass, `HookContainerPanic`, `HealthCheckSingleTimeout`, `healthCheckQueued`, `queueOwnHealthChecks`, `isYieldedDefaultIn`, `readerSeesSource`, `extensionScopes`, `isStarted`, `Refresh`); record any rename in design.md; verify `make test` and `make lint` pass before any change

## 2. A health check never holds a service's lock (regression D3)

- [x] 2.1 RED: add `TestServiceLazy_HealthCheckWithoutLock` (rows Shutdown, Invoke) for scenario "A check in flight does not delay the service"; verify `go test -race -run TestServiceLazy_HealthCheckWithoutLock .` fails with the 1s bound exceeded on both rows
- [x] 2.2 GREEN: in `serviceLazy.healthcheck`, copy the instance under the read lock and call the check after releasing it (design H7); verify the test passes and `make test` is green
- [x] 2.3 REFACTOR: *Extract Function* `builtInstance()` shared by `isHealthchecker` and `healthcheck`; verify `make test` and `make lint` pass

## 3. Health checks never wait for a lazy service being built (regression D9)

- [x] 3.1 RED: add `TestServiceLazy_HealthCheckDuringBuild` (2 rows) for scenario "A provider in progress does not block health checks"; verify `go test -race -run TestServiceLazy_HealthCheckDuringBuild .` fails on the HealthCheckWithContext row (blocked past 1s in the queued goroutine's checker detection); keep the HealthReport row skipped with `t.Skip("group 11")`
- [x] 3.2 GREEN: add an atomic `built` flag to `serviceLazy`, set after the build completes, and make `builtInstance()` read it without the build lock (design H4); verify the first row passes and `make test` is green
- [x] 3.3 REFACTOR: none needed: one field and one accessor; verify `make test` and `make lint` pass

## 4. A panicking health check returns an error (regression D7)

- [x] 4.1 RED: add `TestHealthCheck_Panic` (rows without the HealthReport row, enabled in group 11) for scenario "Panic results"; verify `go test -race -run TestHealthCheck_Panic .` fails: the process exits on the "boom" row, or the build fails naming only `ErrHealthCheckPanic`
- [x] 4.2 GREEN: add `ErrHealthCheckPanic` (errors.go) and recover where each check runs (design H6), calling `HookContainerPanic(root, "healthcheck", v)` when set without recovering its own panic; verify the test passes and `make test` is green
- [x] 4.3 REFACTOR: *Extract Function* `runHealthCheck(ctx, root, check) error` used by every path; verify `make test` and `make lint` pass

## 5. Each underlying check runs once; forced results keep their shape (regressions D4, D5, D8)

- [x] 5.1 RED: add `TestHealthCheck_EachCheckOnce` (7 rows, scenario "Each underlying check runs once") and `TestHealthCheck_MapShape` (2 rows, scenario "Map shape is unchanged", with an extension scope); verify `go test -race -run 'TestHealthCheck_(EachCheckOnce|MapShape)' .` fails on the shadow and alias counts (the export, default and MapShape rows pass on arrival on `76404ef`; record that, and show each can fail by temporarily dropping its skip rule, not committed)
- [x] 5.2 GREEN: add `visibleCheckers(scope)` and `rootForcedCheckers(root)` (design H4), keeping the yielded-default and export skip rules and the extension-scope keys, and use them in both `HealthCheckWithContext` methods; verify the tests pass and `make test` (including the extension and export suites) is green
- [x] 5.3 REFACTOR: *Remove Dead Code* `asyncHealthCheckWithContext`'s ancestor merge and its `@TODO`, and *Inline Function* the bespoke loop of `RootScope.HealthCheckWithContext`; correct the `RootScope.HealthCheck` doc comment (design H14, D8); verify `make test` and `make lint` pass

## 6. A service never has two checks in flight (regression D1)

- [x] 6.1 RED: add `TestHealthCheck_NoPileUp` (2 rows; the HealthReport row is enabled in group 11), `_ConcurrentShareInFlight`, `_JoinRunning` and `_SequentialCheckAgain` (3 rows); verify `go test -race -run 'TestHealthCheck_(NoPileUp|ConcurrentShareInFlight|JoinRunning|SequentialCheckAgain)' .` fails on the call counts (SequentialCheckAgain passes on arrival; record it)
- [x] 6.2 GREEN: add the `healthMonitor` on `RootScope` with the `inflight` map keyed by wrapper and the `sharedCall` primitive (design H2, H3); route the forced and single-service paths through join-or-start; verify the tests pass and `make test` (including `healthcheck_timeout_test.go`) is green
- [x] 6.3 REFACTOR: *Move Function* the execution code into `health_run.go`; verify `make test` and `make lint` pass

## 7. Each caller keeps its own bound; a check sees its starter's context

- [x] 7.1 RED: add `TestHealthCheck_JoinerOwnBound` (6 rows), `_CallerLeaves`, `_StarterContext` (2 rows; the HealthReport row is enabled in group 11), `_LastCallerCancels` and `_AbandonedNotJoined`; verify `go test -race -run 'TestHealthCheck_(JoinerOwnBound|CallerLeaves|StarterContext|LastCallerCancels|AbandonedNotJoined)' .` fails on the joiner rows, the lost deadline and the abandoned join
- [x] 7.2 GREEN: per-waiter bounds from the call (design H8), cancel at zero waiters, abandoned calls kept in flight and never joined, and the check context from `context.WithoutCancel(starter)` with the starter's deadline and bound (design H3); verify the tests pass and `make test` is green
- [x] 7.3 REFACTOR: *Introduce Parameter Object* `waitSpec{ctx, bound, reportPath}` for join-or-start; *Inline Function* `raceWithTimeout`/`awaitHealthCheck` where they become single-use; verify `make test` and `make lint` pass

## 8. HealthCheckParallelism caps checks without blocking callers or registration (regressions D2, D6)

- [x] 8.1 RED: add `TestHealthCheck_ParallelismCap` (3 rows), `_SlotWaitHonorsContext`, `_SlotWaitDoesNotBlockProvide`, `_NoQueueLimit`, `_SingleTakesOverQueued` (2 rows) and `TestNew_NoHealthGoroutine` (rows n=0 and n=4; the scheduler row is enabled in group 17); verify `go test -race -run 'TestHealthCheck_(ParallelismCap|SlotWait|NoQueueLimit|SingleTakesOverQueued)|TestNew_NoHealthGoroutine' .` fails on the slot wait, Provide and the n=4 goroutine row
- [x] 8.2 GREEN: replace `jobPool` with a `chan struct{}` limiter acquired by a queued call's goroutine, the check's bound starting after acquisition; single-service calls take no slot and take over a queued call (design H5); remove `healthCheckPool` start/stop; verify the tests pass and `make test` is green
- [x] 8.3 REFACTOR: *Remove Dead Code* `jobPool`, `newJobPool` and `queueServiceHealthcheck`'s pool branch; verify `make test` and `make lint` pass

## 9. Report types and the overall state

- [x] 9.1 RED: add `TestHealthState_String` (5 rows) and `TestHealthReport_OverallState` (10 rows); verify `go test -race -run 'TestHealthState_String|TestHealthReport_OverallState' .` fails to compile naming only `HealthState`, `HealthReport`, `ServiceHealth` and their constants
- [x] 9.2 GREEN: add `HealthState` (with `HealthDegraded`), `ServiceHealth`, `HealthReport` (with `Phase`) and the overall-state function with the strict Unknown rule (design H15) in `health_report.go`; verify the tests pass and `make test` is green
- [x] 9.3 REFACTOR: none needed: value types and one pure function; verify `make test` and `make lint` pass

## 10. Unmonitored reports: recording, on-demand checks, membership, coverage and order

- [x] 10.1 RED: add `TestHealthReport_Entries` (3 rows), `_Isolated`, `_Order`, `_Membership` (8 rows), `_Coverage` (3 rows), `_OnDemandTTL` (5 rows), `_OnDemandShared`, `_RefreshedByForced` (4 rows) and `_AbandonedNotRecorded`; verify `go test -race -run 'TestHealthReport_' .` fails to compile naming only the `HealthReport` methods and `HealthReportCacheTTL`
- [x] 10.2 GREEN: add `HealthReport`/`HealthReportWithContext` on `Scope`, `RootScope`, `virtualScope` and `Injector` (design H10, H13), `HealthReportCacheTTL` and `DefaultHealthReportCacheTTL`; record every completed check except abandoned ones (design H9); prune records on removal (design H2); check stale covered services on demand in Idle; list one entry per underlying check via `containerCheckers` (design H4); order by scope path; verify the tests pass and `make test` is green
- [x] 10.3 REFACTOR: *Extract Function* `containerCheckers(root)` sharing the per-scope copy with `visibleCheckers`; *Extract Function* `record(wrapper, result)` as the single write path; verify `make test` and `make lint` pass
- [x] 10.4 Add `ExampleScope_HealthReport` (idle container, on-demand read with TTL) and godoc for `HealthReportCacheTTL`; verify `go test -run ExampleScope_HealthReport .` passes

## 11. Report bounds and late results

- [x] 11.1 RED: add `TestHealthReport_ReaderBound` (5 rows), `_ReaderLeaves`, `_LateResultDiscarded` and `_BoundStartsAtSlot`, and enable the HealthReport rows of tasks 3.1, 4.1, 6.1 and 7.1; verify `go test -race -run 'TestHealthReport_(ReaderBound|ReaderLeaves|LateResultDiscarded|BoundStartsAtSlot)|TestServiceLazy_HealthCheckDuringBuild|TestHealthCheck_(Panic|NoPileUp|StarterContext)' .` fails to compile naming only `DefaultHealthReportCheckTimeout`, or on the 10s row and the late result
- [x] 11.2 GREEN: add `DefaultHealthReportCheckTimeout`; report paths use the report bound, record a timeout at it, and discard that check's late result; a caller's ctx ending records nothing (design H8, H9); verify the tests pass and `make test` is green
- [x] 11.3 REFACTOR: *Extract Function* `reportBound(opts)`; verify `make test` and `make lint` pass

## 12. Health classification at registration

- [x] 12.1 RED: add `TestHealthClassification_Default` (2 rows), `TestHealthTags_Constants` (2 rows), `TestHealthClassification_Tags` (4 rows), `_Optional` (3 rows), `TestHealthOptions_InvalidPanics` (5 rows) and `TestHealthClassification_CloneOverrideDefaultAlias` (5 rows); verify `go test -race -run 'TestHealthClassification_|TestHealthTags_Constants|TestHealthOptions_InvalidPanics' .` fails to compile naming only `WithHealthTags`, `AsOptionalHealth` and the tag constants
- [x] 12.2 GREEN: add `WithHealthTags`, `AsOptionalHealth`, `HealthTagReadiness`, `HealthTagLiveness` and the two `serviceOptions` fields with registration-time validation, and expose `Tags`/`Optional` on entries (design H15); verify the tests pass and `make test` is green
- [x] 12.3 REFACTOR: *Extract Function* `healthClassification(opts)` returning the effective tags and severity; verify `make test` and `make lint` pass

## 13. Tag-filtered reports

- [x] 13.1 RED: add `TestHealthReport_MatchTags` (8 rows) and `_MatchTagsOnDemand`; verify `go test -race -run 'TestHealthReport_MatchTags' .` fails to compile naming only `HealthReportOption` and `MatchHealthTags`
- [x] 13.2 GREEN: add `HealthReportOption` and `MatchHealthTags`; filter the covered entries before the overall state and the on-demand stale set (design H15); verify the tests pass and `make test` is green
- [x] 13.3 REFACTOR: *Introduce Parameter Object* `reportQuery{scope, tags, ignorePhase}` passed through the report builder; verify `make test` and `make lint` pass
- [x] 13.4 Add `ExampleMatchHealthTags` (readiness and liveness reports, one optional service) and godoc for the tag constants and options; verify `go test -run ExampleMatchHealthTags .` passes

## 14. A failing optional service is degraded

- [x] 14.1 RED: add `TestHealthReport_Degraded` (5 rows) and `TestHealthCheck_IgnoresClassification` (3 rows); verify `go test -race -run 'TestHealthReport_Degraded|TestHealthCheck_IgnoresClassification' .` fails on the HealthDegraded rows (IgnoresClassification passes on arrival; record it and show it can fail by temporarily dropping optional errors, not committed)
- [x] 14.2 GREEN: when recording a failing result of an optional service, store `HealthDegraded` with its error; forced paths keep the raw error (design H9); verify the tests pass and `make test` is green
- [x] 14.3 REFACTOR: *Extract Function* `stateOf(result, optional)`; verify `make test` and `make lint` pass

## 15. The container phase gates readiness and liveness; reads by phase

- [x] 15.1 RED: add `TestHealthReport_PhaseGate` (11 rows), `_PhaseUnchanged`, `_Closed` (2 rows), `_OnDemandByPhase` (the rows without a scheduler), `_ClosingKeepsResults` and `_LivenessZeroConfig` (6 rows); verify `go test -race -run 'TestHealthReport_(PhaseGate|PhaseUnchanged|Closed|OnDemandByPhase|ClosingKeepsResults|LivenessZeroConfig)' .` fails to compile naming only `IgnoreContainerPhase`, or on the gated rows and the closed state
- [x] 15.2 GREEN: add `IgnoreContainerPhase`; `readMode()` per phase (design H10); apply the readiness and liveness gate last (design H12); report `Phase`; verify the tests pass and `make test` is green
- [x] 15.3 REFACTOR: *Decompose Conditional* the gate into `phaseGate(phase, query) (HealthState, bool)`; verify `make test` and `make lint` pass

## 16. Services not started yet are not checked by reports

- [x] 16.1 RED: add `TestHealthReport_UnstartedNotChecked` (2 rows) and `_IdleChecksStarters`; verify `go test -race -run 'TestHealthReport_(UnstartedNotChecked|IdleChecksStarters)' .` fails on the "does nothing else" row (the service is checked before Refresh)
- [x] 16.2 GREEN: once the phase is not Idle, list a Start candidate not yet `isStarted` as Unknown and exclude it from report and round checks (design H10); verify the tests pass and `make test` is green
- [x] 16.3 REFACTOR: *Extract Function* `eligibleForReport(root, wrapper)` sharing the candidacy rules of the start pipeline; verify `make test` and `make lint` pass

## 17. The HealthScheduler is called at Ready; rounds; monitored reads

- [x] 17.1 RED: add the `manualScheduler` test double and `TestHealthScheduler_NilNoRounds`, `_NilNoGoroutine`, `_CalledAtReady` (5 rows), `_Failure` (3 rows), `TestHealthRound_Results` (3 rows), `_EachCheckOnce`, `TestHealthReport_MonitoredNoChecks`, `_MonitoredFirstCheck` (2 rows), the scheduler rows of `_OnDemandByPhase`, `TestHealthRound_PanicKeepsRecording` and the scheduler row of `TestNew_NoHealthGoroutine`; verify `go test -race -run 'TestHealthScheduler_|TestHealthRound_|TestHealthReport_(Monitored|OnDemandByPhase)|TestNew_NoHealthGoroutine' .` fails to compile naming only `HealthScheduler`
- [x] 17.2 GREEN: add the `HealthScheduler` interface and `InjectorOpts.HealthScheduler`; call it after `markReady` and before the Ready hooks, outside `lifecycleMu`, with recovery and logging (design H1); implement `round(ctx)` over `containerCheckers` with the report bound; monitored reads check only never-checked services (design H10); verify the tests pass and `make test` is green
- [x] 17.3 REFACTOR: *Move Function* the scheduling state and round into `health_monitor.go`; verify `make test` and `make lint` pass

## 18. Scheduling stops at root shutdown; clones are independent

- [x] 18.1 RED: add `TestHealthScheduler_StopBeforeClosing` (3 rows), `_ShutdownCancelsRound`, `_ShutdownSkipsHungCheck`, `_Clone` (3 rows), `_CloneShutdownKeepsSource`, `TestHealthRound_AfterStop` and `TestHealthReport_NoRecordAfterShutdown`; verify `go test -race -run 'TestHealthScheduler_(Stop|Shutdown|Clone)|TestHealthRound_AfterStop|TestHealthReport_NoRecordAfterShutdown' .` fails on the stop order, the cancelled-context row, the provider-injector row or the recorded result
- [x] 18.2 GREEN: stop first in `shutdownAsWinner` and on the root-`*Scope` bypass: set `closing`, cancel the monitor context, and call stop with `context.WithoutCancel(ctx)`, waiting for an in-progress schedule call (design H1); copy `HealthScheduler` in `InjectorOpts.copy()`; verify the tests pass and `make test` is green
- [x] 18.3 REFACTOR: *Extract Function* `stopScheduling(ctx)`; verify `make test` and `make lint` pass
- [x] 18.4 Add `ExampleRootScope_HealthReport` (started container, readiness gated while Closing, `IgnoreContainerPhase`, a scheduler set) and godoc for `HealthScheduler`, `DefaultHealthReportCheckTimeout` and the `Start`/`Extend` notes on unstarted services; verify `go test -run ExampleRootScope_HealthReport .` passes

## 19. Health-change hooks

- [x] 19.1 RED: add `TestHookServiceHealthChanged` (3 rows), `_ForcedRun`, `_Serialized`, `_Reentrant` (3 rows), `TestHookContainerHealthChanged` (4 rows), `_Membership`, `TestHealthHooks_Panic` (2 rows) and `TestHealthHooks_Degraded`; verify `go test -race -run 'TestHook(Service|Container)HealthChanged|TestHealthHooks_' .` fails to compile naming only the hook fields and `Add*HealthChangedHook`
- [x] 19.2 GREEN: add `HookServiceHealthChanged`, `HookContainerHealthChanged`, their copy-on-write `Add*` methods and their copies in `InjectorOpts.copy()`; enqueue transitions on record and prune, computing the container state without the phase gate, and deliver them in order with a single deliverer and no lock held (design H11); recover hook panics to `HookContainerPanic(root, "healthchanged", v)` or `Logf`; verify the tests pass and `make test` is green
- [x] 19.3 REFACTOR: *Extract Function* `deliverTransitions()`; verify `make test` and `make lint` pass
- [x] 19.4 Add `ExampleRootScope_AddServiceHealthChangedHook` and godoc stating delivery order, re-entrancy, the absence of ctx and that hooks should be fast; verify `go test -run ExampleRootScope_AddServiceHealthChangedHook .` passes

## 20. The container measures health time on its clock

- [x] 20.1 RED: add `TestClock_HealthReportTimes`, `TestClock_HealthCacheOnClock` and `TestClock_HealthSchedulerClock` (2 rows); verify `go test -race -run 'TestClock_Health' .` fails, or passes on arrival because groups 10–18 already used the clock (record which, and show an assertion can fail by temporarily using a real clock, not committed)
- [x] 20.2 GREEN: read every health time from `opts.Clock` and pass it to the scheduler where any path still does not; verify the tests pass and `make test` is green
- [x] 20.3 REFACTOR: update the `InjectorOpts.Clock` godoc list; verify `make test` and `make lint` pass

## 21. The gocron adapter module

- [x] 21.1 Scaffold `scheduler/gocron` as module `github.com/kartaladev/doger/scheduler/gocron` (package `dogergocron`) requiring the root module and `github.com/go-co-op/gocron/v2` (pin the latest v2 release at least two weeks old, ≥ v2.23.0), add it to `go.work`, and add `main_test.go` with `goleak.VerifyTestMain`; verify `cd scheduler/gocron && go build ./...` passes
- [x] 21.2 RED: add `TestNew_InvalidOptions` (5 rows), `TestScheduler_DefaultCadence` (4 rows), `_SingletonSkipsTick`, `_Overrides` (5 rows), `_SchedulerOptions`, `_RejectedJob`, `_Shared` and `_CleanStop` (2 rows); verify `cd scheduler/gocron && go test -race ./...` fails to compile naming only `New`, the options, `DefaultInterval` and `ErrInvalidOption`
- [x] 21.3 GREEN: implement `New`, the error-returning options and `ScheduleHealthRounds` in own and shared modes, with the defaults `WithClock(clock)`, `WithName("doger-health-round")`, `WithSingletonMode(LimitModeReschedule)` and `WithStartAt(WithStartImmediately())`, user options appended, and cleanup on a rejected job (design H16); verify `cd scheduler/gocron && go test -race ./...` passes with goleak
- [x] 21.4 REFACTOR: *Extract Function* `jobOptions(cfg)` and `schedulerOptions(cfg, clock)`; add the `var _ doger.HealthScheduler = (*Scheduler)(nil)` check; verify `cd scheduler/gocron && go test -race ./... && golangci-lint run ./...` pass
- [x] 21.5 Add `ExampleNew` and `ExampleWithJobDefinition` (cron), a README section with the distributed elector/locker caveat, and the module's release-tag convention; verify `cd scheduler/gocron && go test -run Example ./...` passes

## 22. Integration checks

- [x] 22.1 Add `BenchmarkHealthReport_Monitored` (1,000 services) and record its result in design.md Test Strategy; verify `go test -run '^$' -bench BenchmarkHealthReport_Monitored -benchmem .` runs
- [x] 22.2 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; if CI lists workspace modules explicitly, add `scheduler/gocron` there; verify all pass
- [x] 22.3 Cross-check every `#### Scenario:` in the six spec deltas against design.md Test Strategy and a passing test; update `ROADMAP.md` (move this change to done once archived); verify `openspec validate add-scheduled-health-checks --strict` passes

## 23. A check whose service left the container is not recorded (review R1)

- [x] 23.1 RED: add `TestHealthReport_NotRecordedAfterLeaving` (2 rows: scope shutdown, `OverrideNamedValue`) for scenario "A check in flight when its service leaves is not recorded"; verify `go test -race -run TestHealthReport_NotRecordedAfterLeaving .` fails on the record, the member and the container hook call
- [x] 23.2 GREEN: `forget` marks the service's call in flight as left; `finishCall` records nothing for a left call and resolves its `next` waiters with its result instead of starting a fresh call (design H2, H9); verify the test passes and `make test` is green
- [x] 23.3 REFACTOR: *Remove Dead Code* the `health.forget(replaced, false)` call in `storeLocked`, since both replace paths call `RootScope.forget` after releasing the scope lock (CR10); verify `make test` and `make lint` pass

## 24. A report-path wait on a check it cannot join records its timeout (review R2)

- [x] 24.1 RED: add `TestHealthReport_TimeoutWhileAbandoned` (2 rows: monitored round, unmonitored read) for scenario "A wait that times out on an abandoned check records the timeout"; verify `go test -race -run TestHealthReport_TimeoutWhileAbandoned .` fails with the entry still `HealthHealthy`
- [x] 24.2 GREEN: in `wait`, a report-path waiter leaving `call.next` at its own bound records its timeout once (design H9); verify the test passes and `make test` is green
- [x] 24.3 REFACTOR: *Extract Function* `leaveLocked(waiter)` from `wait`; verify `make test` and `make lint` pass

## 25. Scheduler panics: a re-panicking hook leaves scheduling consistent; stop panics reach the hook (reviews R3, R8)

- [x] 25.1 RED: add `TestHealthScheduler_FailurePanicHook` (scenario "A re-panicking HookContainerPanic leaves scheduling stopped") and `TestHealthScheduler_StopFailure` (3 rows, scenario "A failing stop does not fail Shutdown"), wrapped in `testWithTimeout`; verify `go test -race -run 'TestHealthScheduler_(FailurePanicHook|StopFailure)' .` fails on the Shutdown wait and on the hook row
- [x] 25.2 GREEN: `startScheduling` finalizes its state and closes the "starting" signal in a `defer`; `stopScheduling` passes a stop panic to `HookContainerPanic(root, "healthscheduler", v)` when set, logs it otherwise (design H1); verify the tests pass and `make test` is green
- [x] 25.3 REFACTOR: *Extract Function* `finishScheduling(stop, err)` and `callStop(ctx, stop)`; verify `make test` and `make lint` pass

## 26. A panic HookContainerPanic raises for a check reaches the waiting callers (review R4)

- [x] 26.1 RED: add `TestHealthCheck_HookPanicPropagates` (4 rows: `HealthCheckNamed`, `HealthCheck()`, `HealthReport()`, the stored round) for scenario "A panic HookContainerPanic raises reaches the waiting callers"; verify `go test -race -run TestHealthCheck_HookPanicPropagates .` fails: the test binary exits from the check's goroutine (record the output)
- [x] 26.2 GREEN: `runHealthCheck` recovers the hook's panic; `finishCall` carries it to every waiter; `wait` re-raises it on the waiter's goroutine; with no waiter, the check's goroutine re-raises it (design H6); verify the test passes and `make test` is green
- [x] 26.3 REFACTOR: *Introduce Parameter Object* `checkOutcome{err, hookPanic}` between the check, `finishCall` and the waiters; verify `make test` and `make lint` pass

## 27. Container state in constant time per result (review R7)

- [x] 27.1 RED: add `TestHealthRound_AllocsLinear`: with `testing.AllocsPerRun`, the allocations of a round re-recording every result at 1,000 services stay under 2.5× those at 500; verify `go test -race -run TestHealthRound_AllocsLinear .` fails (about 3.85× on the current code)
- [x] 27.2 GREEN: keep counts of members by state and severity in the monitor, updated on record, `forget` and membership change, and derive the container state from them (design H11); verify the test passes and `make test` (including every container-hook test) is green
- [x] 27.3 REFACTOR: *Extract Function* `memberCountsLocked` updates; add `BenchmarkHealthRound` (1,000 services) and record its result next to `BenchmarkHealthReport_Monitored` in design.md; verify `make test` and `make lint` pass

## 28. Log a check request only when a check starts (CR8)

- [x] 28.1 RED: add `TestHealthReport_CachedReadsLogNothing`: 50 services, a first read, then 10 reads within the TTL log no "requested health check for service" line; verify `go test -race -run TestHealthReport_CachedReadsLogNothing .` fails (500 lines)
- [x] 28.2 GREEN: log in `joinOrStart` when a call is created, not in `wrapperHealthCheckFunc` (design H11); verify the test passes and `make test` is green
- [x] 28.3 REFACTOR: none needed: one log line moves; verify `make test` and `make lint` pass

## 29. A shared gocron scheduler: stop waits for its round and tolerates a removed job (reviews R5, R6)

- [x] 29.1 RED: add `TestScheduler_SharedStop` (2 rows: a round running; the owner shut the scheduler down) for scenario "Stopping a shared schedule"; verify `cd scheduler/gocron && go test -race -run TestScheduler_SharedStop ./...` fails on both rows
- [x] 29.2 GREEN: wrap the round to count this job's rounds in progress; the shared stop calls `RemoveJob`, treats `gocron.ErrJobNotFound` as removed, and waits within ctx until no round of the job runs (design H16); verify `cd scheduler/gocron && go test -race ./...` passes with goleak
- [x] 29.3 REFACTOR: *Extract Function* `sharedStop(scheduler, id, rounds)`; verify `cd scheduler/gocron && go test -race ./... && golangci-lint run ./...` pass

## 30. Documentation of the flagged behavior (W1, CR4, W3)

- [x] 30.1 Update the godoc of `InjectorOpts.HealthCheckParallelism`, `Scope.HealthCheckWithContext`, `RootScope.HealthCheckWithContext` and `HealthCheckNamedWithContext`: concurrent callers share a check in flight, which runs with the context of the caller that started it; a panic becomes an error matching `ErrHealthCheckPanic`; checks started by `HealthCheck()`, reads and rounds take a slot, a hung one keeping it; single-service functions take none
- [x] 30.2 Correct the `stopScheduling` comment and state on `RootScope.ShutdownWithContext` and `Healthchecker` that root Shutdown cancels checks in flight without waiting, so a check may run while its service shuts down (CR4); state the appearance timing on `InjectorOpts.HookContainerHealthChanged` (W3); verify `make lint` passes and `go doc` shows the new text

## 31. Remove the queue helpers (CR9)

- [x] 31.1 Port `TestRootScope_queueServiceHealthcheck` to `HealthCheckWithContext` and `HealthCheckNamedWithContext`, keeping each of its rows and assertions (its inputs change, so this is not a refactor); verify it passes
- [x] 31.2 REFACTOR: *Remove Dead Code* `queueServiceHealthcheck` and `queueWrapperHealthcheck`, checking with gopls that nothing references them; verify `make test` and `make lint` pass

## 32. Clone rows and test placement (S2, S1)

- [x] 32.1 Add the `Clone()` and `CloneWithOpts(...)` rows to `TestNew_NoHealthGoroutine` (scenario "New starts no goroutine"); they pass on arrival: record it, and show one can fail by temporarily starting a goroutine in `CloneWithOpts` (not committed)
- [x] 32.2 REFACTOR: *Move Function* `TestHealthReport_MonitoredNoChecks`, `_MonitoredFirstCheck`, `_NoRecordAfterShutdown` and `BenchmarkHealthReport_Monitored` from `health_monitor_test.go` to `health_report_test.go` (design Test Strategy); verify `make test` and `make lint` pass

## 33. Integration checks after review

- [x] 33.1 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; verify all pass
- [x] 33.2 Cross-check every `#### Scenario:` against design.md Test Strategy and a passing test, as in 22.3; verify `openspec validate add-scheduled-health-checks --strict` passes

## Workflow follow-up

- Open the follow-up change for the HTTP health handlers in `http/*`: readiness and liveness, serving tag-filtered `HealthReport` as JSON, 200 for Healthy and Degraded, 503 otherwise, with the phase gate on by default. Then the next items in `ROADMAP.md`.
- Archive this change after review, then verify the synced specs.
