# Tasks

The branch is rebased on `main` at `76404ef`. Every RED test must be seen failing for the expected reason before its GREEN step. Concurrency tests run under `-race` and `testWithTimeout`. "Regression Dn" refers to proposal.md, Defect. The probe file from the 2026-10-28 re-verification seeds the D1–D9 RED tests. Groups 2–16 use containers without a `HealthScheduler`. Groups 17–19 add scheduling, and group 21 adds the gocron module.

## 1. Baseline

- [ ] 1.1 Confirm the names this change builds on still exist on the base (`markReady`, `dispatchAfterStart`, `shutdownAsWinner` and its root-`*Scope` bypass, `HookContainerPanic`, `HealthCheckSingleTimeout`, `healthCheckQueued`, `queueOwnHealthChecks`, `isYieldedDefaultIn`, `readerSeesSource`, `extensionScopes`, `isStarted`, `Refresh`); record any rename in design.md; verify `make test` and `make lint` pass before any change

## 2. A health check never holds a service's lock (regression D3)

- [ ] 2.1 RED: add `TestServiceLazy_HealthCheckWithoutLock` (rows Shutdown, Invoke) for scenario "A check in flight does not delay the service"; verify `go test -race -run TestServiceLazy_HealthCheckWithoutLock .` fails with the 1s bound exceeded on both rows
- [ ] 2.2 GREEN: in `serviceLazy.healthcheck`, copy the instance under the read lock and call the check after releasing it (design H7); verify the test passes and `make test` is green
- [ ] 2.3 REFACTOR: *Extract Function* `builtInstance()` shared by `isHealthchecker` and `healthcheck`; verify `make test` and `make lint` pass

## 3. Health checks never wait for a lazy service being built (regression D9)

- [ ] 3.1 RED: add `TestServiceLazy_HealthCheckDuringBuild` (2 rows) for scenario "A provider in progress does not block health checks"; verify `go test -race -run TestServiceLazy_HealthCheckDuringBuild .` fails on the HealthCheckWithContext row (blocked past 1s in the queued goroutine's checker detection); keep the HealthReport row skipped with `t.Skip("group 11")`
- [ ] 3.2 GREEN: add an atomic `built` flag to `serviceLazy`, set after the build completes, and make `builtInstance()` read it without the build lock (design H4); verify the first row passes and `make test` is green
- [ ] 3.3 REFACTOR: none needed: one field and one accessor; verify `make test` and `make lint` pass

## 4. A panicking health check returns an error (regression D7)

- [ ] 4.1 RED: add `TestHealthCheck_Panic` (rows without the HealthReport row, enabled in group 11) for scenario "Panic results"; verify `go test -race -run TestHealthCheck_Panic .` fails: the process exits on the "boom" row, or the build fails naming only `ErrHealthCheckPanic`
- [ ] 4.2 GREEN: add `ErrHealthCheckPanic` (errors.go) and recover where each check runs (design H6), calling `HookContainerPanic(root, "healthcheck", v)` when set without recovering its own panic; verify the test passes and `make test` is green
- [ ] 4.3 REFACTOR: *Extract Function* `runHealthCheck(ctx, root, check) error` used by every path; verify `make test` and `make lint` pass

## 5. Each underlying check runs once; forced results keep their shape (regressions D4, D5, D8)

- [ ] 5.1 RED: add `TestHealthCheck_EachCheckOnce` (7 rows, scenario "Each underlying check runs once") and `TestHealthCheck_MapShape` (2 rows, scenario "Map shape is unchanged", with an extension scope); verify `go test -race -run 'TestHealthCheck_(EachCheckOnce|MapShape)' .` fails on the shadow and alias counts (the export, default and MapShape rows pass on arrival on `76404ef`; record that, and show each can fail by temporarily dropping its skip rule, not committed)
- [ ] 5.2 GREEN: add `visibleCheckers(scope)` and `rootForcedCheckers(root)` (design H4), keeping the yielded-default and export skip rules and the extension-scope keys, and use them in both `HealthCheckWithContext` methods; verify the tests pass and `make test` (including the extension and export suites) is green
- [ ] 5.3 REFACTOR: *Remove Dead Code* `asyncHealthCheckWithContext`'s ancestor merge and its `@TODO`, and *Inline Function* the bespoke loop of `RootScope.HealthCheckWithContext`; correct the `RootScope.HealthCheck` doc comment (design H14, D8); verify `make test` and `make lint` pass

## 6. A service never has two checks in flight (regression D1)

- [ ] 6.1 RED: add `TestHealthCheck_NoPileUp` (2 rows; the HealthReport row is enabled in group 11), `_ConcurrentShareInFlight`, `_JoinRunning` and `_SequentialCheckAgain` (3 rows); verify `go test -race -run 'TestHealthCheck_(NoPileUp|ConcurrentShareInFlight|JoinRunning|SequentialCheckAgain)' .` fails on the call counts (SequentialCheckAgain passes on arrival; record it)
- [ ] 6.2 GREEN: add the `healthMonitor` on `RootScope` with the `inflight` map keyed by wrapper and the `sharedCall` primitive (design H2, H3); route the forced and single-service paths through join-or-start; verify the tests pass and `make test` (including `healthcheck_timeout_test.go`) is green
- [ ] 6.3 REFACTOR: *Move Function* the execution code into `health_run.go`; verify `make test` and `make lint` pass

## 7. Each caller keeps its own bound; a check sees its starter's context

- [ ] 7.1 RED: add `TestHealthCheck_JoinerOwnBound` (6 rows), `_CallerLeaves`, `_StarterContext` (2 rows; the HealthReport row is enabled in group 11), `_LastCallerCancels` and `_AbandonedNotJoined`; verify `go test -race -run 'TestHealthCheck_(JoinerOwnBound|CallerLeaves|StarterContext|LastCallerCancels|AbandonedNotJoined)' .` fails on the joiner rows, the lost deadline and the abandoned join
- [ ] 7.2 GREEN: per-waiter bounds from the call (design H8), cancel at zero waiters, abandoned calls kept in flight and never joined, and the check context from `context.WithoutCancel(starter)` with the starter's deadline and bound (design H3); verify the tests pass and `make test` is green
- [ ] 7.3 REFACTOR: *Introduce Parameter Object* `waitSpec{ctx, bound, reportPath}` for join-or-start; *Inline Function* `raceWithTimeout`/`awaitHealthCheck` where they become single-use; verify `make test` and `make lint` pass

## 8. HealthCheckParallelism caps checks without blocking callers or registration (regressions D2, D6)

- [ ] 8.1 RED: add `TestHealthCheck_ParallelismCap` (3 rows), `_SlotWaitHonorsContext`, `_SlotWaitDoesNotBlockProvide`, `_NoQueueLimit`, `_SingleTakesOverQueued` (2 rows) and `TestNew_NoHealthGoroutine` (rows n=0 and n=4; the scheduler row is enabled in group 17); verify `go test -race -run 'TestHealthCheck_(ParallelismCap|SlotWait|NoQueueLimit|SingleTakesOverQueued)|TestNew_NoHealthGoroutine' .` fails on the slot wait, Provide and the n=4 goroutine row
- [ ] 8.2 GREEN: replace `jobPool` with a `chan struct{}` limiter acquired by a queued call's goroutine, the check's bound starting after acquisition; single-service calls take no slot and take over a queued call (design H5); remove `healthCheckPool` start/stop; verify the tests pass and `make test` is green
- [ ] 8.3 REFACTOR: *Remove Dead Code* `jobPool`, `newJobPool` and `queueServiceHealthcheck`'s pool branch; verify `make test` and `make lint` pass

## 9. Report types and the overall state

- [ ] 9.1 RED: add `TestHealthState_String` (5 rows) and `TestHealthReport_OverallState` (10 rows); verify `go test -race -run 'TestHealthState_String|TestHealthReport_OverallState' .` fails to compile naming only `HealthState`, `HealthReport`, `ServiceHealth` and their constants
- [ ] 9.2 GREEN: add `HealthState` (with `HealthDegraded`), `ServiceHealth`, `HealthReport` (with `Phase`) and the overall-state function with the strict Unknown rule (design H15) in `health_report.go`; verify the tests pass and `make test` is green
- [ ] 9.3 REFACTOR: none needed: value types and one pure function; verify `make test` and `make lint` pass

## 10. Unmonitored reports: recording, on-demand checks, membership, coverage and order

- [ ] 10.1 RED: add `TestHealthReport_Entries` (3 rows), `_Isolated`, `_Order`, `_Membership` (8 rows), `_Coverage` (3 rows), `_OnDemandTTL` (5 rows), `_OnDemandShared`, `_RefreshedByForced` (4 rows) and `_AbandonedNotRecorded`; verify `go test -race -run 'TestHealthReport_' .` fails to compile naming only the `HealthReport` methods and `HealthReportCacheTTL`
- [ ] 10.2 GREEN: add `HealthReport`/`HealthReportWithContext` on `Scope`, `RootScope`, `virtualScope` and `Injector` (design H10, H13), `HealthReportCacheTTL` and `DefaultHealthReportCacheTTL`; record every completed check except abandoned ones (design H9); prune records on removal (design H2); check stale covered services on demand in Idle; list one entry per underlying check via `containerCheckers` (design H4); order by scope path; verify the tests pass and `make test` is green
- [ ] 10.3 REFACTOR: *Extract Function* `containerCheckers(root)` sharing the per-scope copy with `visibleCheckers`; *Extract Function* `record(wrapper, result)` as the single write path; verify `make test` and `make lint` pass
- [ ] 10.4 Add `ExampleScope_HealthReport` (idle container, on-demand read with TTL) and godoc for `HealthReportCacheTTL`; verify `go test -run ExampleScope_HealthReport .` passes

## 11. Report bounds and late results

- [ ] 11.1 RED: add `TestHealthReport_ReaderBound` (5 rows), `_ReaderLeaves`, `_LateResultDiscarded` and `_BoundStartsAtSlot`, and enable the HealthReport rows of tasks 3.1, 4.1, 6.1 and 7.1; verify `go test -race -run 'TestHealthReport_(ReaderBound|ReaderLeaves|LateResultDiscarded|BoundStartsAtSlot)|TestServiceLazy_HealthCheckDuringBuild|TestHealthCheck_(Panic|NoPileUp|StarterContext)' .` fails to compile naming only `DefaultHealthReportCheckTimeout`, or on the 10s row and the late result
- [ ] 11.2 GREEN: add `DefaultHealthReportCheckTimeout`; report paths use the report bound, record a timeout at it, and discard that check's late result; a caller's ctx ending records nothing (design H8, H9); verify the tests pass and `make test` is green
- [ ] 11.3 REFACTOR: *Extract Function* `reportBound(opts)`; verify `make test` and `make lint` pass

## 12. Health classification at registration

- [ ] 12.1 RED: add `TestHealthClassification_Default` (2 rows), `TestHealthTags_Constants` (2 rows), `TestHealthClassification_Tags` (4 rows), `_Optional` (3 rows), `TestHealthOptions_InvalidPanics` (5 rows) and `TestHealthClassification_CloneOverrideDefaultAlias` (5 rows); verify `go test -race -run 'TestHealthClassification_|TestHealthTags_Constants|TestHealthOptions_InvalidPanics' .` fails to compile naming only `WithHealthTags`, `AsOptionalHealth` and the tag constants
- [ ] 12.2 GREEN: add `WithHealthTags`, `AsOptionalHealth`, `HealthTagReadiness`, `HealthTagLiveness` and the two `serviceOptions` fields with registration-time validation, and expose `Tags`/`Optional` on entries (design H15); verify the tests pass and `make test` is green
- [ ] 12.3 REFACTOR: *Extract Function* `healthClassification(opts)` returning the effective tags and severity; verify `make test` and `make lint` pass

## 13. Tag-filtered reports

- [ ] 13.1 RED: add `TestHealthReport_MatchTags` (8 rows) and `_MatchTagsOnDemand`; verify `go test -race -run 'TestHealthReport_MatchTags' .` fails to compile naming only `HealthReportOption` and `MatchHealthTags`
- [ ] 13.2 GREEN: add `HealthReportOption` and `MatchHealthTags`; filter the covered entries before the overall state and the on-demand stale set (design H15); verify the tests pass and `make test` is green
- [ ] 13.3 REFACTOR: *Introduce Parameter Object* `reportQuery{scope, tags, ignorePhase}` passed through the report builder; verify `make test` and `make lint` pass
- [ ] 13.4 Add `ExampleMatchHealthTags` (readiness and liveness reports, one optional service) and godoc for the tag constants and options; verify `go test -run ExampleMatchHealthTags .` passes

## 14. A failing optional service is degraded

- [ ] 14.1 RED: add `TestHealthReport_Degraded` (5 rows) and `TestHealthCheck_IgnoresClassification` (3 rows); verify `go test -race -run 'TestHealthReport_Degraded|TestHealthCheck_IgnoresClassification' .` fails on the HealthDegraded rows (IgnoresClassification passes on arrival; record it and show it can fail by temporarily dropping optional errors, not committed)
- [ ] 14.2 GREEN: when recording a failing result of an optional service, store `HealthDegraded` with its error; forced paths keep the raw error (design H9); verify the tests pass and `make test` is green
- [ ] 14.3 REFACTOR: *Extract Function* `stateOf(result, optional)`; verify `make test` and `make lint` pass

## 15. The container phase gates readiness and liveness; reads by phase

- [ ] 15.1 RED: add `TestHealthReport_PhaseGate` (11 rows), `_PhaseUnchanged`, `_Closed` (2 rows), `_OnDemandByPhase` (the rows without a scheduler), `_ClosingKeepsResults` and `_LivenessZeroConfig` (6 rows); verify `go test -race -run 'TestHealthReport_(PhaseGate|PhaseUnchanged|Closed|OnDemandByPhase|ClosingKeepsResults|LivenessZeroConfig)' .` fails to compile naming only `IgnoreContainerPhase`, or on the gated rows and the closed state
- [ ] 15.2 GREEN: add `IgnoreContainerPhase`; `readMode()` per phase (design H10); apply the readiness and liveness gate last (design H12); report `Phase`; verify the tests pass and `make test` is green
- [ ] 15.3 REFACTOR: *Decompose Conditional* the gate into `phaseGate(phase, query) (HealthState, bool)`; verify `make test` and `make lint` pass

## 16. Services not started yet are not checked by reports

- [ ] 16.1 RED: add `TestHealthReport_UnstartedNotChecked` (2 rows) and `_IdleChecksStarters`; verify `go test -race -run 'TestHealthReport_(UnstartedNotChecked|IdleChecksStarters)' .` fails on the "does nothing else" row (the service is checked before Refresh)
- [ ] 16.2 GREEN: once the phase is not Idle, list a Start candidate not yet `isStarted` as Unknown and exclude it from report and round checks (design H10); verify the tests pass and `make test` is green
- [ ] 16.3 REFACTOR: *Extract Function* `eligibleForReport(root, wrapper)` sharing the candidacy rules of the start pipeline; verify `make test` and `make lint` pass

## 17. The HealthScheduler is called at Ready; rounds; monitored reads

- [ ] 17.1 RED: add the `manualScheduler` test double and `TestHealthScheduler_NilNoRounds`, `_NilNoGoroutine`, `_CalledAtReady` (5 rows), `_Failure` (3 rows), `TestHealthRound_Results` (3 rows), `_EachCheckOnce`, `TestHealthReport_MonitoredNoChecks`, `_MonitoredFirstCheck` (2 rows), the scheduler rows of `_OnDemandByPhase`, `TestHealthRound_PanicKeepsRecording` and the scheduler row of `TestNew_NoHealthGoroutine`; verify `go test -race -run 'TestHealthScheduler_|TestHealthRound_|TestHealthReport_(Monitored|OnDemandByPhase)|TestNew_NoHealthGoroutine' .` fails to compile naming only `HealthScheduler`
- [ ] 17.2 GREEN: add the `HealthScheduler` interface and `InjectorOpts.HealthScheduler`; call it after `markReady` and before the Ready hooks, outside `lifecycleMu`, with recovery and logging (design H1); implement `round(ctx)` over `containerCheckers` with the report bound; monitored reads check only never-checked services (design H10); verify the tests pass and `make test` is green
- [ ] 17.3 REFACTOR: *Move Function* the scheduling state and round into `health_monitor.go`; verify `make test` and `make lint` pass

## 18. Scheduling stops at root shutdown; clones are independent

- [ ] 18.1 RED: add `TestHealthScheduler_StopBeforeClosing` (3 rows), `_ShutdownCancelsRound`, `_ShutdownSkipsHungCheck`, `_Clone` (3 rows), `_CloneShutdownKeepsSource`, `TestHealthRound_AfterStop` and `TestHealthReport_NoRecordAfterShutdown`; verify `go test -race -run 'TestHealthScheduler_(Stop|Shutdown|Clone)|TestHealthRound_AfterStop|TestHealthReport_NoRecordAfterShutdown' .` fails on the stop order, the cancelled-context row, the provider-injector row or the recorded result
- [ ] 18.2 GREEN: stop first in `shutdownAsWinner` and on the root-`*Scope` bypass: set `closing`, cancel the monitor context, and call stop with `context.WithoutCancel(ctx)`, waiting for an in-progress schedule call (design H1); copy `HealthScheduler` in `InjectorOpts.copy()`; verify the tests pass and `make test` is green
- [ ] 18.3 REFACTOR: *Extract Function* `stopScheduling(ctx)`; verify `make test` and `make lint` pass
- [ ] 18.4 Add `ExampleRootScope_HealthReport` (started container, readiness gated while Closing, `IgnoreContainerPhase`, a scheduler set) and godoc for `HealthScheduler`, `DefaultHealthReportCheckTimeout` and the `Start`/`Extend` notes on unstarted services; verify `go test -run ExampleRootScope_HealthReport .` passes

## 19. Health-change hooks

- [ ] 19.1 RED: add `TestHookServiceHealthChanged` (3 rows), `_ForcedRun`, `_Serialized`, `_Reentrant` (3 rows), `TestHookContainerHealthChanged` (4 rows), `_Membership`, `TestHealthHooks_Panic` (2 rows) and `TestHealthHooks_Degraded`; verify `go test -race -run 'TestHook(Service|Container)HealthChanged|TestHealthHooks_' .` fails to compile naming only the hook fields and `Add*HealthChangedHook`
- [ ] 19.2 GREEN: add `HookServiceHealthChanged`, `HookContainerHealthChanged`, their copy-on-write `Add*` methods and their copies in `InjectorOpts.copy()`; enqueue transitions on record and prune, computing the container state without the phase gate, and deliver them in order with a single deliverer and no lock held (design H11); recover hook panics to `HookContainerPanic(root, "healthchanged", v)` or `Logf`; verify the tests pass and `make test` is green
- [ ] 19.3 REFACTOR: *Extract Function* `deliverTransitions()`; verify `make test` and `make lint` pass
- [ ] 19.4 Add `ExampleRootScope_AddServiceHealthChangedHook` and godoc stating delivery order, re-entrancy, the absence of ctx and that hooks should be fast; verify `go test -run ExampleRootScope_AddServiceHealthChangedHook .` passes

## 20. The container measures health time on its clock

- [ ] 20.1 RED: add `TestClock_HealthReportTimes`, `TestClock_HealthCacheOnClock` and `TestClock_HealthSchedulerClock` (2 rows); verify `go test -race -run 'TestClock_Health' .` fails, or passes on arrival because groups 10–18 already used the clock (record which, and show an assertion can fail by temporarily using a real clock, not committed)
- [ ] 20.2 GREEN: read every health time from `opts.Clock` and pass it to the scheduler where any path still does not; verify the tests pass and `make test` is green
- [ ] 20.3 REFACTOR: update the `InjectorOpts.Clock` godoc list; verify `make test` and `make lint` pass

## 21. The gocron adapter module

- [ ] 21.1 Scaffold `scheduler/gocron` as module `github.com/kartaladev/doger/scheduler/gocron` (package `dogergocron`) requiring the root module and `github.com/go-co-op/gocron/v2` (pin the latest v2 release at least two weeks old, ≥ v2.23.0), add it to `go.work`, and add `main_test.go` with `goleak.VerifyTestMain`; verify `cd scheduler/gocron && go build ./...` passes
- [ ] 21.2 RED: add `TestNew_InvalidOptions` (5 rows), `TestScheduler_DefaultCadence` (4 rows), `_SingletonSkipsTick`, `_Overrides` (5 rows), `_SchedulerOptions`, `_RejectedJob`, `_Shared` and `_CleanStop` (2 rows); verify `cd scheduler/gocron && go test -race ./...` fails to compile naming only `New`, the options, `DefaultInterval` and `ErrInvalidOption`
- [ ] 21.3 GREEN: implement `New`, the error-returning options and `ScheduleHealthRounds` in own and shared modes, with the defaults `WithClock(clock)`, `WithName("doger-health-round")`, `WithSingletonMode(LimitModeReschedule)` and `WithStartAt(WithStartImmediately())`, user options appended, and cleanup on a rejected job (design H16); verify `cd scheduler/gocron && go test -race ./...` passes with goleak
- [ ] 21.4 REFACTOR: *Extract Function* `jobOptions(cfg)` and `schedulerOptions(cfg, clock)`; add the `var _ doger.HealthScheduler = (*Scheduler)(nil)` check; verify `cd scheduler/gocron && go test -race ./... && golangci-lint run ./...` pass
- [ ] 21.5 Add `ExampleNew` and `ExampleWithJobDefinition` (cron), a README section with the distributed elector/locker caveat, and the module's release-tag convention; verify `cd scheduler/gocron && go test -run Example ./...` passes

## 22. Integration checks

- [ ] 22.1 Add `BenchmarkHealthReport_Monitored` (1,000 services) and record its result in design.md Test Strategy; verify `go test -run '^$' -bench BenchmarkHealthReport_Monitored -benchmem .` runs
- [ ] 22.2 Run `make test`, `make lint`, and `go test ./...` in every `http/*`, `examples/*` and `scheduler/*` module; if CI lists workspace modules explicitly, add `scheduler/gocron` there; verify all pass
- [ ] 22.3 Cross-check every `#### Scenario:` in the six spec deltas against design.md Test Strategy and a passing test; update `ROADMAP.md` (move this change to done once archived); verify `openspec validate add-scheduled-health-checks --strict` passes

## Workflow follow-up

- Open the follow-up change for the HTTP health handlers in `http/*`: readiness and liveness, serving tag-filtered `HealthReport` as JSON, 200 for Healthy and Degraded, 503 otherwise, with the phase gate on by default. Then the next items in `ROADMAP.md`.
- Archive this change after review, then verify the synced specs.
