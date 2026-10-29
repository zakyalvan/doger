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

## 34. A caller's per-service bound starts when its check runs (second review R9)

- [x] 34.1 EVIDENCE: probe recorded in design.md (Second review evidence, R9) failed: with a limit of 1, the 9s check was recorded timed out at 10s after running 8s, and two of three 6s checks were recorded timed out
- [x] 34.2 RED: add `TestHealthCheck_BoundStartsAtSlot` (4 rows) for scenario "A caller's per-service bound does not count the wait for a slot"; verify `go test -race -run TestHealthCheck_BoundStartsAtSlot .` fails on the 9s/2s, three-6s and `HealthCheck()` rows (the 9s/0s row passes on arrival, as `TestHealthReport_BoundStartsAtSlot` does; record it)
- [x] 34.3 GREEN: a waiter whose call waits for a slot starts its per-service bound when the call is claimed, its ctx and `HealthCheckGlobalTimeout` still running from its call (design H3, H5); verify the test passes and `make test` is green
- [x] 34.4 REFACTOR: *Extract Function* the start of a waiter's per-service bound, shared by joining a running call and claiming a queued one; verify `make test` and `make lint` pass

## 35. Report times with a limiter (second review CR11)

- [x] 35.1 EVIDENCE: probe recorded in design.md (CR11) failed: a check waiting for a slot had a `PendingSince` though it had not started, and a timed-out record's Duration was 10s for 8s of running
- [x] 35.2 RED: add `TestHealthReport_TimesWithLimiter` for scenario "Report times with a limiter" (2 rows: the `PendingSince` of a check waiting for a slot is when it was requested; the Duration of a check timed out after waiting for a slot runs from its start); verify `go test -race -run TestHealthReport_TimesWithLimiter .` fails on the Duration row (the `PendingSince` row passes on arrival, now specified; record it)
- [x] 35.3 GREEN: a recorded timeout's Duration runs from when the check started (design H9); correct the godoc of `ServiceHealth.PendingSince` and `Duration`; verify the test passes, `make test` is green and `go doc` shows the text
- [x] 35.4 REFACTOR: none needed: one measurement point and godoc; verify `make test` and `make lint` pass

## 36. Callers queued behind a check they could not join keep their slot rule (second review R10)

- [x] 36.1 EVIDENCE: probes recorded in design.md (R10) failed: a check started by `HealthCheck()` ran while another held the only slot, and a single-service caller's check got a 50ms deadline with `HealthCheckSingleTimeout` -1ns
- [x] 36.2 RED: add `TestHealthCheck_QueuedKeepSlotRule` (2 rows) for scenario "Callers queued behind a check they could not join keep their own slot rule", under synctest; verify `go test -race -run TestHealthCheck_QueuedKeepSlotRule .` fails on both rows
- [x] 36.3 GREEN: a waiter queued in `next` keeps its own wait spec; `finishCall` starts the fresh call from its first waiter's spec, and each later waiter joins it as it would join any call, a single-service one taking it over (design H3); verify the test passes and `make test` is green
- [x] 36.4 REFACTOR: *Extract Function* `startFreshLocked(call)` from `finishCall`; verify `make test` and `make lint` pass

## 37. A service that leaves before its check starts is not recorded (second review R11)

- [x] 37.1 EVIDENCE: probes recorded in design.md (R11) failed: after the child scope shut down between listing and the start of its check, `b` was recorded and kept as a member, and the tracked container state was Unhealthy, through a root read and through a round
- [x] 37.2 RED: add `TestHealthReport_NotRecordedLeftBeforeStart` (2 rows) for scenario "A service that leaves before its check starts is not recorded"; verify `go test -race -run TestHealthReport_NotRecordedLeftBeforeStart .` fails on the record, the member and the state
- [x] 37.3 GREEN: `finishCall` records only while the target's scope still holds the wrapper under its name, checked before it takes the monitor mutex (design H2); verify the test passes and `make test` (including `TestHealthReport_NotRecordedAfterLeaving`) is green
- [x] 37.4 REFACTOR: *Extract Function* `stillRegistered(target)`; verify `make test` and `make lint` pass

## 38. A shutdown through the root's own *Scope ends health recording (W4)

- [x] 38.1 EVIDENCE: probes recorded in design.md (W4) showed the specified behavior: after `Shutdown()` on a provider's injector, a new healthy service stayed HealthUnknown through `HealthReport()` and `HealthCheck()`, Phase idle, no hook call
- [x] 38.2 Add `TestHealthScheduler_RootScopeShutdownEndsRecording` (2 rows) for scenario "A shutdown through the root's own *Scope ends health recording"; it passes on arrival: record it, and show it can fail by temporarily resetting the closing state after the bypass shutdown (not committed)
- [x] 38.3 Document it on `Scope.ShutdownWithContext` (the root's own `*Scope`) and on `InjectorOpts.HookServiceHealthChanged`: health recording and hooks end there while the phase is unchanged; verify `make lint` passes and `go doc` shows the text

## 39. Sort report entries without per-comparison allocations (second review CR12)

- [x] 39.1 EVIDENCE: measurement recorded in design.md (CR12): a monitored read of 2,000 services across the root and a grandchild scope took 30,656 allocations (15.3 per service), 6,076 with each scope's path computed once
- [x] 39.2 RED: add `TestHealthReport_ReadAllocs` (not parallel; `testing.AllocsPerRun`): a monitored read of 2,000 recorded services across the root and a grandchild scope takes under 8 allocations per service; verify `go test -race -run TestHealthReport_ReadAllocs .` fails (about 15.3)
- [x] 39.3 GREEN: compute each covered scope's name path once per read before sorting (design H10); verify the test passes and `make test` (including `TestHealthReport_Order`) is green
- [x] 39.4 REFACTOR: *Extract Function* `sortReportChecks(covered)`; *Remove Dead Code* what no longer has a caller, checking with gopls; re-run `BenchmarkHealthReport_Monitored` and record it in design.md; verify `make test` and `make lint` pass

## 40. Build the gocron task once per mode (second review CR13)

- [x] 40.1 EVIDENCE: code inspection recorded in design.md (CR13): `ScheduleHealthRounds` builds a task that its shared branch replaces
- [x] 40.2 REFACTOR: *Remove Dead Code*: build the round task once, for the mode in use (design H16); verify `cd scheduler/gocron && go test -race ./... && golangci-lint run ./...` pass

## 41. Integration checks after the second review

- [x] 41.1 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; verify all pass
- [x] 41.2 Cross-check every `#### Scenario:` against design.md Test Strategy and a passing test, as in 33.2; verify `openspec validate add-scheduled-health-checks --strict` passes

## 42. A timeout of a check started after its service left is not recorded (R12)

- [x] 42.1 EVIDENCE: probe recorded in design.md (R12) failed: with `HealthCheckTimeout` 20ms and a hung "b" whose scope shut down after the read listed it, a root `HealthReport()` recorded "b"'s timeout, kept it as a member, and the tracked state was Unhealthy
- [x] 42.2 RED: add `TestHealthReport_TimeoutNotRecordedLeftBeforeStart` (2 rows) for scenario "A timeout of a check started after its service left is not recorded"; verify `go test -race -run TestHealthReport_TimeoutNotRecordedLeftBeforeStart .` fails on the record, the member and the state
- [x] 42.3 GREEN: a report-path waiter whose bound elapsed records its timeout only while its service is still registered, checked in `wait` before it takes the monitor mutex; the waiter keeps its target (design H2, H9); verify the test passes and `make test` (including `TestHealthReport_NotRecordedLeftBeforeStart` and `TestHealthReport_TimeoutWhileAbandoned`) is green
- [x] 42.4 REFACTOR: none needed: one guard reusing `healthTarget.stillRegistered`; verify `make test` and `make lint` pass

## 43. Integration checks after R12

- [x] 43.1 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; verify all pass
- [x] 43.2 Cross-check every `#### Scenario:` against design.md Test Strategy and a passing test, as in 41.2; verify `openspec validate add-scheduled-health-checks --strict` passes

## 44. A caller whose context has already ended starts no check (verification W2)

- [x] 44.1 EVIDENCE: probe recorded in design.md (Verification findings, W2) failed: at `631c942`, `HealthCheckWithContext` with an already-cancelled context started the check in 3 runs out of 40, none at `76404ef`; `TestScope_HealthCheckWithContextExpiration_ChildScopes` failed 32 runs out of 300
- [x] 44.2 RED: add `TestHealthCheck_EndedContextStartsNoCheck` (3 rows) for scenario "A caller whose context has already ended starts no check"; verify `go test -race -run TestHealthCheck_EndedContextStartsNoCheck .` fails on all 3 rows on the recorded "requested health check for service db" line
- [x] 44.3 GREEN: `joinOrStart` creates no call for a caller whose context has already ended when no call it can join is in flight (an abandoned call too: `TestHealthCheck_EndedContextQueuesNoFreshCheck`, RED with 2 calls of the check); its waiter waits for none, and `wait` returns its timeout error (design H3); verify the test passes, `make test` is green, and `go test -race -count=500 -run TestScope_HealthCheckWithContextExpiration_ChildScopes .` has no failure
- [x] 44.4 REFACTOR: none needed: one guard; verify `make test` and `make lint` pass

## 45. A caller queued behind an abandoned check keeps its bound from its call (verification W1)

- [x] 45.1 EVIDENCE: probe recorded in design.md (W1): a `HealthCheck()` queued behind an abandoned "leaf" check returned after 20ms with a timeout while the check started for it waited for the slot; the `HealthCheckParallelism` godoc still says that waiting for a slot ends with each caller's bound
- [x] 45.2 Add `TestHealthCheck_QueuedBehindAbandonedKeepsBound` (2 rows) for scenario "A caller queued behind an abandoned check keeps its bound from its call", under synctest; it passes on arrival: record it, and show it fails by temporarily creating a waiter in `next` with a deferred bound (not committed)
- [x] 45.3 Correct the godoc of `InjectorOpts.HealthCheckParallelism`: waiting for a slot counts against each caller's context and `HealthCheckGlobalTimeout`, its per-service bound starts when the check starts running, except for a caller queued behind an abandoned check (design H5); verify `make lint` passes and `go doc` shows the text

## 46. Deterministic report scenario tests (verification W3)

- [x] 46.1 EVIDENCE: measurements recorded in design.md (W3): `TestHealthReport_NoRecordAfterShutdown` failed 2 runs out of 300; `TestHealthReport_Degraded` failed once in 200 at both `631c942` and the working tree
- [x] 46.2 Run `TestHealthReport_Degraded` and `TestHealthReport_NoRecordAfterShutdown` under `synctest`, keeping their rows and assertions (only the time source changes, so this is not a refactor); `NoRecordAfterShutdown` also waits for the first read's hook delivery before Shutdown, the state its Given step assumes (design W3); verify `go test -race -count=500 -run 'TestHealthReport_Degraded|TestHealthReport_NoRecordAfterShutdown' .` has no failure

## 47. Cleanups (verification S1–S3)

- [x] 47.1 REFACTOR: *Remove Field* `callWaiter.parent`, `reportPath` and `bound`, reading `spec.ctx`, `spec.reportPath` and `spec.bound`, checking with gopls that nothing else references them; verify `make test` and `make lint` pass
- [x] 47.2 REFACTOR: *Extract Function* `scopeHolds(scope, name, wrapper)`, shared by `stillRegistered(*startCandidate)` and `healthTarget.stillRegistered`; verify `make test` and `make lint` pass
- [x] 47.3 Correct the `healthCheckContext` comment to cover a waiter's context with a deferred bound, built by `newCallWaiter`; verify `make lint` passes

## 48. Integration checks after verification

- [x] 48.1 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; verify all pass
- [x] 48.2 Cross-check every `#### Scenario:` against design.md Test Strategy and a passing test, as in 43.2; verify `openspec validate add-scheduled-health-checks --strict` passes

## 49. Deterministic tests under load (verification 2, S2)

- [x] 49.1 EVIDENCE: recorded in design.md (Flaky tests after group 48): the race-enabled root test binary, run with `-test.cpu` 1, 2 or 4 beside 14 CPU-burning processes, failed 23 runs out of 60; temporary diagnostics showed starvation, not a defect, in `TestHealthReport_TimeoutNotRecordedLeftBeforeStart` and `TestHealthCheck_SingleTakesOverQueued`
- [x] 49.2 Run the tests that waited on real time under `synctest`, keeping their rows and assertions (only the time source changes): the `TestScope_ShutdownWithContextExpiration_*` family, `TestScope_ContextExpiration_ParallelOperations`, `TestHealthCheck_PerServiceErrorInTime`, `_NoPileUp`, `_SingleTakesOverQueued`, `_SlotWait*`, `TestHealthReport_TimeoutNotRecordedLeftBeforeStart`, `_NotRecordedAfterLeaving`, `_MonitoredFirstCheck`, the callers of `healthReportTestInPhase`, the health hook and scheduler tests with `Eventually` or `time.After`, and the two clock tests; verify temporary mutations still fail them (no takeover; a pile-up; the R12 guard removed; a wall-clock timeout)
- [x] 49.3 `testWithTimeout` (main_test.go) raises a timeout below 5s to 5s: it guards against hangs, not timing; verify `make lint` passes
- [x] 49.4 `TestPhase_RaceFree` observers yield (`runtime.Gosched`) instead of spinning; verify an unlocked `Phase` still trips the race detector
- [x] 49.5 Verify 0 failing runs out of 100 under the load of 49.1, and `make test` and `make lint` pass

## 50. The pile-up scenario's report row meets the check in flight (verification 2, W1)

- [x] 50.1 EVIDENCE: probe recorded in design.md (Verification 2, W1): with a temporary pile-up, the HealthReport row of `TestHealthCheck_NoPileUp` passed under the default cache; with caching off it failed (100 calls), and on correct code it passed 200 runs out of 200
- [x] 50.2 Set a negative `HealthReportCacheTTL` in `TestHealthCheck_NoPileUp`, as the scenario's Given step now says; it passes on arrival: record it, and show the HealthReport row fails with 100 calls under a temporary pile-up in `joinOrStart` (not committed)
- [x] 50.3 Verify `go test -race -count=200 -run '^TestHealthCheck_NoPileUp$' .` has no failure, and `make test` and `make lint` pass

## 51. Integration checks after verification 2

- [x] 51.1 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; verify all pass
- [x] 51.2 Cross-check every `#### Scenario:` against design.md Test Strategy and a passing test, as in 48.2; verify `openspec validate add-scheduled-health-checks --strict` passes

## 52. A check that outlives its bound gives its slot back (code review R13)

- [x] 52.1 EVIDENCE: probes recorded in design.md (Code review findings, R13) failed: with `HealthCheckParallelism` 1 and a hung "hog", `HealthCheck()`, `HealthReport()` and a round behind it had not returned after 1h of synctest time, nor `HealthReport()` behind an unbounded `HealthCheck()`; each returned at `631c942`
- [x] 52.2 Change the Given step of `TestHealthCheck_QueuedBehindAbandonedKeepsBound` as its scenario now says ("hog" takes the slot 1ms after the entry's call); its rows and assertions are unchanged, and it passes before and after 52.4. Show it still fails when a waiter in `next` defers its bound (temporary, not committed)
- [x] 52.3 RED: add `TestHealthCheck_OutlivedBoundFreesSlot` (4 rows) for scenario "A check that outlives its bound gives its slot back", under synctest, with `testWithTimeout` and a 1h bubble-time limit on each entry; verify `go test -race -run TestHealthCheck_OutlivedBoundFreesSlot .` fails on all 4 rows
- [x] 52.4 GREEN: a pooled call's goroutine gives its slot back when its check returns, when its context ends, or once the report bound has passed since it took the slot (design H5); verify the test passes and `make test` is green
- [x] 52.5 REFACTOR: *Extract Function* the slot hold (`holdSlot`) from `startCall`; verify `make test` and `make lint` pass
- [x] 52.6 Correct the godoc of `InjectorOpts.HealthCheckParallelism` ("A check that ignores its context keeps its slot until it returns" → the slot rule of design H5); verify `make lint` passes and `go doc` shows the text

## 53. A caller that gives up behind an abandoned check starts no fresh check (code review R14)

- [x] 53.1 EVIDENCE: probe recorded in design.md (R14) failed with 2 calls of the check, at `631c942` too
- [x] 53.2 RED: make `TestHealthCheck_EndedContextQueuesNoFreshCheck` a table test for scenario "A caller that gives up behind an abandoned check starts no fresh check": the existing case is row `ended_before_queueing`, unchanged; add `ended_after_queueing`; verify only that row fails (2 calls)
- [x] 53.3 GREEN: `startFreshLocked` skips a waiter whose context has ended; it leaves in `wait` (design H3); verify the test passes and `make test` is green
- [x] 53.4 REFACTOR: none needed: one guard; verify `make test` and `make lint` pass

## 54. Report reads and rounds start no check for a service that left (code review R15)

- [x] 54.1 EVIDENCE: probes recorded in design.md (R15) failed: "b" was checked once after its scope shut down, through `HealthReport()` and through a round, at `631c942` too
- [x] 54.2 RED: add `TestHealthReport_LeftBeforeReadNotChecked` (2 rows) for scenario "A service that left before the read reached it is not checked"; verify both rows fail (1 call of "b")
- [x] 54.3 GREEN: `checkOnDemand` and `healthRound` start no check for a covered service that `stillRegistered` rejects (design H10); verify the test passes and `make test` is green
- [x] 54.4 Add `TestHealthReport_LeftDuringStart` (2 rows, white-box: `joinOrStart` with a report-path spec for a service whose scope already shut down, one check returning an error, one timing out at a 20ms bound); verify it passes, and that each row fails when its recording guard (`finishCall`, `wait`) is removed (temporary, not committed), since after 54.3 the R11 and R12 tests no longer reach those guards
- [x] 54.5 REFACTOR: *Extract Function* the start of a report-path check, shared by `checkOnDemand` and `healthRound`; verify `make test` and `make lint` pass

## 55. PendingSince of a check started behind an abandoned one (code review R16)

- [x] 55.1 EVIDENCE: probe recorded in design.md (R16) failed: PendingSince 00:00:05 for a check requested at 00:00:01, at `631c942` too
- [x] 55.2 RED: add `TestHealthReport_PendingSinceBehindAbandoned` for scenario "PendingSince of a check started behind an abandoned one"; verify it fails (00:00:05)
- [x] 55.3 GREEN: a waiter queued in `next` records when its caller asked; `startFreshLocked` dates the fresh call from the earliest of them (design H9); check that the godoc of `ServiceHealth.PendingSince` still holds; verify the test passes and `make test` is green
- [x] 55.4 REFACTOR: none needed: one field and one comparison; verify `make test` and `make lint` pass

## 56. Fake-clock loops in tests have a step limit (code review CR14)

- [x] 56.1 EVIDENCE: recorded in design.md (CR14): under a temporary `runCall` that never returns, `TestHealthCheck_BoundStartsAtSlot` hung until `-timeout 30s`
- [x] 56.2 Give the fake-clock loops of `TestHealthCheck_BoundStartsAtSlot` (60 steps), `TestHealthReport_TimesWithLimiter` (60) and `TestHealthCheck_SequentialChecksShareGlobalTimeout` (100) a step limit (`require.Less`) and call `testWithTimeout`; rows and assertions are unchanged (a test-only change, not a refactor). Verify each fails within seconds under the same temporary `runCall` (not committed), and passes otherwise

## 57. One context type per role (code review CR15)

- [x] 57.1 REFACTOR: *Extract Class* `boundContext` (a context cancelled with a cause, whose `Err` reports `context.DeadlineExceeded` for that cause) from `healthCheckContext`, which embeds it and keeps `Deadline`; `newCallWaiter` uses `boundContext` for a waiter with a deferred bound (its `Deadline` is unchanged: `context.WithCancelCause` keeps the caller's). Correct both comments; verify with gopls that every reference compiles, and that `make test` and `make lint` pass

## 58. Registration check on finished checks (code review CR16)

- [x] 58.1 EVIDENCE: benchmark recorded in design.md (CR16): no measurable cost (`BenchmarkHealthRound` 3.29 ms vs 3.26 ms without the lock, p = 0.97); closed with no change

## 59. Integration checks after the code review

- [x] 59.1 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; verify all pass
- [x] 59.2 Run `go test -race -count=100` on the tests added or changed in groups 52–56; verify no failure
- [x] 59.3 Cross-check every `#### Scenario:` against design.md Test Strategy and a passing test, as in 51.2; verify `openspec validate add-scheduled-health-checks --strict` passes

## 60. A joining caller gets what the check returned (verification 3, W1)

- [x] 60.1 EVIDENCE: probe recorded in design.md (verification 3, W1) failed 20 of 20 runs: a joining `HealthCheckNamed` got the timeout, and the record was that timeout, although the check returned nil or `"down"` after the starter's 30ms global bound
- [x] 60.2 RED: add `TestHealthCheck_JoinerGetsCheckResult` (3 rows) for scenario "A joining caller gets the check's result after the starter's bound" and `TestHealthReport_RecordedAfterStarterBound` (3 rows) for scenario "A check its starter's bound cancelled is recorded with its result"; verify `go test -race -run 'TestHealthCheck_JoinerGetsCheckResult|TestHealthReport_RecordedAfterStarterBound' .` fails on the nil and "down" rows with the timeout error (the ctx.Err() rows pass on arrival; record it)
- [x] 60.3 GREEN: `callResult` and the record in `finishCall` use what the check returned unless the waiter's own context or bound ended, or the check returned its context's error after that context ended (design H3, H9); verify the tests pass, `TestHealthCheck_JoinRunning`, `_JoinerOwnBound` and `TestHealthReport_LateResultDiscarded` still pass, and `make test` is green
- [x] 60.4 REFACTOR: *Extract Function* the call's own result (the check's return, or a timeout for its ended context's error), shared by `callResult` and the record in `finishCall`, if both compute it; otherwise none needed; verify `make test` and `make lint` pass

## 61. A stop returned with a scheduler error is called (verification 3, S2)

- [x] 61.1 EVIDENCE: probe recorded in design.md (verification 3, S2) failed 20 of 20 runs: a stop returned together with `errors.New("partial")` was called 0 times through Start and Shutdown
- [x] 61.2 RED: add `TestHealthScheduler_StopReturnedWithError` for scenario "A stop returned with an error is called at once"; verify `go test -race -run TestHealthScheduler_StopReturnedWithError .` fails (stop called 0 times)
- [x] 61.3 GREEN: `callScheduler` (or `startScheduling`) calls a stop returned with an error at once through `callStop` with `context.Background()`, and keeps it out of `finishScheduling` (design H1); state the rule in the `HealthScheduler` godoc; verify the test passes, `TestHealthScheduler_Failure` and `_FailurePanicHook` still pass, and `make test` is green
- [x] 61.4 REFACTOR: none needed: one call in the error branch; verify `make test` and `make lint` pass

## 62. Tests that match the corrected scenarios (verification 3, W3, S4)

- [x] 62.1 Change `TestHealthRound_EachCheckOnce` to count the round's own calls (db +0, svc +1) besides db's total of 1, for the corrected "A round checks each service once"; verify it passes, and fails when the round temporarily checks svc twice or not at all (not committed)
- [x] 62.2 Add the row `forced_global_30ms_at_29ms` to `TestHealthCheck_JoinerOwnBound`; verify it passes, and fails when a forced joiner temporarily takes the call's context as its own (the S4 mutant in design.md; not committed) while the existing rows still pass under that mutant
- [x] 62.3 Change `TestHealthReport_Order` to the corrected example ("q" under w/leaf, "p" under x/leaf); verify it passes, and fails when `sortReportChecks` temporarily compares the scope's own name instead of its path (not committed)
- [x] 62.4 Check that `TestHealthScheduler_StopBeforeClosing` (W4) and `TestHealthReport_TimesWithLimiter` (S5) already assert the corrected rows; verify by reading them against the scenarios, with no test change

## 63. Untested requirement sentences get tests (verification 3, W5, S1, S3)

Each test pins behavior that exists, so it passes on arrival; for each, show it fails under its mutant recorded in design.md (verification 3 evidence), applied temporarily and not committed.

- [x] 63.1 Add `TestHealthReport_GlobalTimeoutNotApplied` (2 rows) for scenario "HealthCheckGlobalTimeout does not bound a read"; verify it passes, and fails under the `reportBound` mutant
- [x] 63.2 Add `TestHealthReport_NilContext` (2 rows) for scenario "A nil context reads like context.Background()"; verify it passes, and fails under the nil-check mutant
- [x] 63.3 Add `TestHealthReport_CoverageExtension` for scenario "The root report covers extension scopes"; verify it passes, and fails under the `containerCheckers` mutant
- [x] 63.4 Add `TestHealthReport_UnstartedByCaller` (3 rows) for scenario "Rounds skip a service not started yet, forced paths check it"; verify it passes, the round row fails under the `healthRound` mutant and the forced rows under the `healthCheckTargets` mutant
- [x] 63.5 Add `TestHealthHooks_CloneKeepsHooks` for scenario "Clone keeps the health hooks"; verify it passes, and fails under the `InjectorOpts.copy` mutant
- [x] 63.6 Add `TestHealthHooks_ContainerAfterService` for scenario "Container hooks follow the service hooks of the same result" (wait for delivery: hooks run after the caller's wait is released); verify it passes, and fails under the `recordLocked` mutant
- [x] 63.7 Add `TestHealthHooks_NilSkipped` for scenario "Nil hooks are skipped"; verify it passes, and fails under the nil-guard mutant
- [x] 63.8 Assert `Monitored` false in `TestHealthScheduler_FailurePanicHook` for the corrected scenario; verify it passes, and fails under the `errSchedulerInterrupted` mutant
- [x] 63.9 Add `TestHealthCheck_HookPanicNoWaiterCrashes` (child process) for scenario "With no caller waiting, the hook's panic is raised on the check's goroutine"; verify it passes, and fails under the `finishCall` re-raise mutant
- [x] 63.10 Add `TestHealthCheck_GlobalTimeoutBoundsSlotWait` (2 rows) for scenario "HealthCheckGlobalTimeout bounds the wait for a slot from the call"; verify it passes, and fails under the deferred-bound mutant
- [x] 63.11 Add `TestHealthScheduler_ShutdownCancelCause` for scenario "Callers of a check cancelled by Shutdown"; verify it passes, and fails when `stopScheduling` temporarily cancels checks with `context.Canceled` as the cause
- [x] 63.12 Add `TestHealthScheduler_ShutdownWaitsForSchedulerCall` (synctest) for scenario "Shutdown waits for a scheduler call in progress"; verify it passes, and fails when `stopScheduling` temporarily stops waiting for `scheduling` once its ctx ends

## 64. gocron option precedence and a rejected scheduler (verification 3, W5)

- [x] 64.1 Add `TestScheduler_OptionPrecedence` (5 rows) for scenario "Options win over the defaults and accumulate in order"; verify `go test -race -run TestScheduler_OptionPrecedence ./...` in `scheduler/gocron` passes, and each row fails under its mutant in design.md (defaults after the consumer's options; `WithClock` last; `WithJobOptions`/`WithSchedulerOptions` replacing; not committed)
- [x] 64.2 Turn `TestScheduler_RejectedJob` into a table with the row `WithSchedulerOptions(gocron.WithLocation(nil))` for the corrected scenario "A schedule gocron rejects"; verify it passes with goleak, and the new row fails when the `NewScheduler` error is temporarily swallowed

## 65. Documentation (verification 3, S1, S2, S3)

- [x] 65.1 Godoc: `HealthScheduler` (a stop returned with an error is called at once; Shutdown waits for a call in progress, so return promptly), `RootScope.ShutdownWithContext` (that wait outlives its context), and the health-check APIs' note on Shutdown (a check's context is cancelled with the cause `ErrStartAfterShutdown`; its callers get `ErrHealthCheckTimeout` with `context.Canceled`); verify with `go doc` on each symbol and `make lint`

## 66. Integration checks after verification 3

- [x] 66.1 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; verify all pass
- [x] 66.2 Run `go test -race -count=100` on the tests added or changed in groups 60–64; verify no failure
- [x] 66.3 Cross-check every `#### Scenario:` against design.md Test Strategy and a passing test, as in 59.3; verify `openspec validate add-scheduled-health-checks --strict` passes

## Workflow follow-up

- Open the follow-up change for the HTTP health handlers in `http/*`: readiness and liveness, serving tag-filtered `HealthReport` as JSON, 200 for Healthy and Degraded, 503 otherwise, with the phase gate on by default. Then the next items in `ROADMAP.md`.
- Archive this change after review, then verify the synced specs.
