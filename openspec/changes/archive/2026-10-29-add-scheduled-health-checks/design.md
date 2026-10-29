# Design

## Context

See proposal.md (Why, Defect) for the motivation and the reproduced defects. This design was first written against `2012c60` (PR #8's head at the time). It was **revised on 2026-10-28 against `main` at `76404ef`**, after #8, #11 (`AsDefault`), #12 (scope export), #14 (shutdown context) and #15 (Extension SPI) merged, and after an audit of the first version. The audit's resolutions are in "Audit resolution" below.

Current design on `76404ef`:

- **Forced runs.**
  - `Scope.HealthCheckWithContext` → `healthCheckQueued` (applies `HealthCheckGlobalTimeout`) → `asyncHealthCheckWithContext`.
  - That calls `queueOwnHealthChecks(prefix, skip)` for the scope and each ancestor. The skip function drops yielded defaults (`isYieldedDefaultIn`) and exports whose source the requester sees (`readerSeesSource`). Shadowed ancestors are still queued (D4, the `@TODO`).
  - `RootScope.HealthCheckWithContext` adds the installed extension scopes (`extensionScopes()`), keyed `<scope name>/<name>`, as the `extensions` spec requires.
  - `RootScope.queueServiceHealthcheck` starts one goroutine per service (`awaitHealthCheck`), or uses `jobPool` (a fixed 1,000-slot channel, workers started in `NewWithOpts`) when `HealthCheckParallelism > 0`.
- **Single-service functions.** `HealthCheckNamedWithContext` runs on the caller's goroutine through `raceWithTimeout`, bounded by `HealthCheckSingleTimeout` (0 keeps `HealthCheckTimeout`; negative means no bound). It skips the pool.
- **What a check sees** (probe): its caller's context, including deadline and values.
- **Lazy services.** `serviceLazy.healthcheck` holds the wrapper's read lock while the check runs (D3). `instanceOrBuild` holds the write lock for the whole provider call. Since `76404ef`, `isHealthchecker` is reached in the queued goroutine (`serviceHealthCheckFunc`), before ctx is watched (D9).
- **Lifecycle.**
  - `startPhase`/`closePhase` change under `lifecycleMu`.
  - `markReady` moves started to ready, between the Started and Ready hooks.
  - `shutdownAsWinner` runs the Closing hooks, then shuts the services down. A Shutdown reached through the root's own `*Scope` bypasses it.
  - `Refresh` / `Scope.Start` start services added after Start, and the phase stays Ready (probe N4). `isStarted(service)` records what was started.
  - `Extend` may install services while Ready.
  - `HookContainerPanic(root, point, value)`: nil means re-panic on the Start or Shutdown caller.
- **Package init.** `DefaultRootScope = New()` runs at package init, so `New()` must stay goroutine-free.
- **Constraints.**
  - No new dependency in the root module.
  - The injector clock (clockwork v0.5.0) for every time read.
  - goleak in `TestMain`.
  - A fake-clock context's `Err()` blocks while it is live (`contextDone`).

## Prior art

| Source | Here | Note |
|---|---|---|
| alexliesenfeld/health `WithCacheDuration` (1s) | `HealthReportCacheTTL` (1s) for on-demand reads | Same default |
| alexliesenfeld `StatusUnknown` before the first result | `HealthUnknown` with `PendingSince`; a critical service without a result keeps the overall state Unknown | Same |
| alexliesenfeld periodic checks: opt-in per check | Scheduling opt-in per container through `HealthScheduler` | Per-service intervals are out of scope |
| alexliesenfeld status listeners | `HookServiceHealthChanged` / `HookContainerHealthChanged`, serialized and re-entrant | |
| alexliesenfeld panic recovery | Always recovered, reported to `HookContainerPanic` | |
| .NET `HealthCheckRegistration.Tags`, `HealthStatus.Degraded` | `WithHealthTags`, `AsOptionalHealth`, `HealthDegraded` | |
| .NET `IHealthCheckPublisher`: opt-in, Delay 5s, Period 30s | `HealthScheduler` opt-in; gocron adapter 30s, first round at once | Period matches .NET |
| Spring Boot `ApplicationAvailability`: lifecycle sets Liveness/Readiness; readiness `REFUSING_TRAFFIC` while starting and during graceful shutdown; health indicators never change the lifecycle | Phase gate on reports; health never changes the phase | Liveness stays Healthy while starting (Spring: BROKEN), to avoid restart loops on a slow Start |
| Spring Boot probe groups include no external indicator by default; "liveness should not depend on health checks for external systems" | Unclassified checkers are readiness only; liveness is the phase plus explicitly tagged checks | |
| Spring Boot Actuator: on-request checks | No scheduler: on-demand reads with a TTL | |
| Guava `ServiceManager.isHealthy()` = all services RUNNING | Lifecycle feeds health, never the reverse | |
| uber-go/fx, dig, google/wire, samber/do | No health lifecycle | No conflict |

### gocron analysis (scheduling backend)

The user prefers go-co-op/gocron for scheduling. Analysis of v2.23.0 (`b39ae51`, MIT):

| Concern | gocron v2 | Consequence |
|---|---|---|
| Module deps | `google/uuid`, `robfig/cron/v3`, `jonboulle/clockwork v0.5.0` (same version as doger) | Acceptable in an opt-in module, not in the root module (library principle: zero-config root, no forced deps) |
| Go version | go 1.22 | Fine with doger's 1.27 |
| Clock | `WithClock(clockwork.Clock)` | The container's fake clock drives rounds in tests |
| Goroutines | `NewScheduler` starts a goroutine at construction, before `Start` | Create it only inside `ScheduleHealthRounds` (at Ready), never in `New()` or `DefaultRootScope` |
| Overlap | `WithSingletonMode(LimitModeReschedule)` drops a tick while the job runs | Default; no round pile-up |
| First run | `WithStartAt(WithStartImmediately())` | Default; Unknown window is short |
| Context | A task whose first parameter is `context.Context` gets a ctx cancelled on scheduler shutdown | The round honors it |
| Panics | Recovered (`callJobWithRecover`), `AfterJobRunsWithPanic` listener | A second safety net; checks already recover in the root module |
| Shutdown | `ShutdownWithContext` waits for running jobs up to `WithStopTimeout` (10s default); a job ignoring ctx yields `ErrStopJobsTimedOut` and leaks | The container cancels the round ctx first and a round never blocks on a hung check past ctx, so stop returns promptly |
| Cron | `CronJob(crontab, withSeconds)` | Consumers get cron schedules for free via `WithJobDefinition` |
| Distributed elector/locker | Runs a job on one instance only | Wrong for per-instance health; documented as unsupported for this job |
| Monitor / logger | `WithMonitor`, `WithLogger` | Pass-through via `WithSchedulerOptions` |

Conclusion: gocron fits as the backend behind a small root-module interface. It does not fit inside the root module.

## Goals / Non-Goals

**Goals:**
- Reading health costs the same for 1 reader and for 1,000.
- At most one goroutine per service runs its check, whatever the callers do.
- The root module starts no health goroutine on its own and adds no dependency. Scheduling is opt-in and pluggable.
- Readiness and liveness follow the container lifecycle, as in Spring Boot; health never drives the lifecycle.
- No health path blocks registration, building, shutdown, or a caller past its own context and bound.
- Every opinionated choice can be overridden.

**Non-Goals:**
- The HTTP handlers (next roadmap item).
- JSON marshalling of `HealthReport`.
- Per-service intervals or timeouts.
- Failure thresholds; hooks allow deriving them.
- A startup probe tag. `PhaseReady` already says when start finished, and the gate reports it.
- Cycle detection (a check calling itself still recurses, as today).
- Coalescing whole forced `HealthCheck()` runs: per-service sharing already bounds goroutines (D1), and readers should use reports.
- A built-in timer scheduler in the root module.

## Decisions

**H1. Scheduling is an interface, opt-in, owned by the container lifecycle.**
- **Shape.**
  ```go
  type HealthScheduler interface {
      ScheduleHealthRounds(clock clockwork.Clock, round func(ctx context.Context)) (stop func(ctx context.Context) error, err error)
  }
  ```
  It returns a stop function rather than having a `Stop` method, so one scheduler value (copied by `Clone()`) can serve several containers. Each call is an independent schedule.
- **Start.** In `dispatchAfterStart`, after `markReady` and before the Ready hooks, outside `lifecycleMu`. Under the monitor mutex, the container marks scheduling as "starting" unless it is already stopped. It then calls `ScheduleHealthRounds` with recovery. Afterwards, under the mutex:
  - if shutdown began meanwhile, it calls stop at once;
  - otherwise it stores stop and sets monitored.

  An error or a recovered panic is logged (`DI: health scheduler failed: ...`) or passed to `HookContainerPanic(root, "healthscheduler", v)`. The container stays unmonitored and on-demand reads keep working. A stop function returned together with an error is called at once, through `callStop` with `context.Background()`, before Start returns, and is not kept for Shutdown: a third-party scheduler may have started part of its schedule (verification 3, S2). The state update and the closing of the "starting" signal run in a `defer`, so a panic `HookContainerPanic` raises propagates from Start and leaves scheduling failed; a later Shutdown does not wait for a schedule call that will never finish (review R3).
- **Stop.** Stopping comes first in `shutdownAsWinner`, and on the root-`*Scope` bypass path. Under the mutex the container sets `closing`, so nothing is recorded and no hook fires. It then cancels the monitor context, which cancels the rounds in progress, and calls stop with `context.WithoutCancel(shutdownCtx)`. That way an ended Shutdown context (shutdown-context, #14) cannot make Shutdown return before the scheduler's goroutines exit. Shutdown waits for stop. A stop error is logged; a stop panic is recovered and passed to `HookContainerPanic(root, "healthscheduler", v)` when set, logged otherwise (review R8). If a schedule call is still in progress, Shutdown waits for it, also past the end of Shutdown's context, then calls its stop, so no goroutine of the schedule outlives Shutdown. This differs from a blocked Starter, which Shutdown's deadline bounds (shutdown-context); the `HealthScheduler` and `ShutdownWithContext` godoc say so (verification 3, S3). The contexts of the checks in flight are cancelled with the cause `ErrStartAfterShutdown` ("DI: container is shut down"), which only a check reading `context.Cause` sees; its waiting callers get `timeoutError(context.Canceled)` (verification 3, S1). On the root-`*Scope` bypass the phase stays unchanged, yet recording and hooks end for good, as on the RootScope path (documented, W4). The checks in flight have their contexts cancelled but are not waited for: a check may still run while its service shuts down (CR4, flagged behavior change 3).
- **Rejected:**
  - A built-in timer loop in the root module. It is default-on in the first version and was rejected in the audit: it goes against the prior art, leaks in every `Start`-without-`Shutdown` test, and the TTL cache already solves load amplification. The user prefers gocron.
  - Importing gocron in the root module: forced dependencies on every consumer.
  - Configuring the scheduler through an `Extension`: an extension registers services into a scope, while scheduling is container infrastructure that must be known at Ready.

**H2. One monitor per root, keyed by service wrapper.**
- **State.** A `healthMonitor` on `RootScope` holds, under one mutex: `records`, `inflight`, the transition queue, the scheduling state (`idle`/`starting`/`scheduled`/`stopped`) and the stop function, all keyed by wrapper. A wrapper is unique per registration, so an Override or a re-registration starts a fresh record.
- **Pruning.** A record is pruned when its wrapper is removed: through `RootScope.forget`, which the registration-replacement and scope-shutdown paths already call once their scope lock is released, and as a fallback when a read or round lists services. `forget` also marks the service's call in flight as left (H9). A check started after its service left (the read or round listed it just before) is caught when it returns: `finishCall` records only while the wrapper is still registered under its name in its scope, checked before it takes the monitor mutex; a removal after that check finds the call in flight and marks it left (review R11). A report-path waiter whose bound elapsed checks the same before it takes the monitor mutex to record its timeout (review R12). Pruning is never done under a scope lock (the earlier extra call in `storeLocked` is removed: CR10).
- **Lock rule.** The monitor mutex is never held while listing services, calling a check, or calling user code (the scheduler, hooks).

**H3. An in-flight check is a shared call; each waiter has its own bound.**
- **States.** A `sharedCall` per service is queued (waiting for a slot), running, or returned. It holds a done channel, a result, a waiter count and a cancel function. The first caller creates it and runs it on a new goroutine; later callers join. A caller whose context has already ended creates none: with no call in flight it can join, it neither starts one nor queues for a fresh one, and its wait ends at once with its timeout error, as on 76404ef (verification W2).
- **Waiting.** Each waiter selects on `done`, its own ctx, and a timer for its own per-service bound, started at its call (H8), or, while its call waits for a slot, when the check starts running (H5, review R9). When it stops early it gets `timeoutError(cause)`.
- **Abandonment.** When the waiter count reaches zero before the call returns, the check's ctx is cancelled and the call is marked abandoned. It stays in `inflight` until its goroutine exits; a newcomer waits on its `done` within its own bound, started at its call and still running while the fresh call waits for a slot, so a hung abandoned check never holds it past that bound (review R2, verification W1), then creates a fresh call. A newcomer whose context ends before the abandoned call returns gets no fresh call: `finishCall` skips it, and it leaves in `wait` (code review R14). Each newcomer keeps its own wait spec, so the fresh call takes the pooled flag and bound of its own starter's path, and a joining newcomer of another path keeps its own slot rule, never the old call's (review R10). An abandoned result is not recorded.
- **The check's context.** It is built from `context.WithoutCancel(starter ctx)`, the earlier of the starter's deadline and the starter's bound (recorded with `withHealthCheckDeadline`), and a cancel for abandonment and shutdown.
- **A waiter's result** (verification 3, W1). A waiter whose own context and bound are still live when the check returns gets what the check returned. The check's context being done, which the starter's bound may have caused after the starter left, does not by itself turn that into a timeout. Only a check that returns its context's error once that context ended gives `timeoutError(cause)`. A waiter whose own context or bound ended gets its timeout as before (`callResult`). The record follows the same rule (H9).
- **Why not `x/sync/singleflight`.** Its function takes no context, a waiter leaving cannot cancel it, it has no per-waiter bound, and it would add a dependency.
- **Removed from v1:** per-scope run coalescing (old H4). Per-service sharing already caps goroutines and calls during overlap (the "Concurrent forced calls share the in-flight checks" scenario). Readers that need a cost independent of the number of callers use reports.

**H4. Listing services never blocks, and preserves main's skip rules.**
- `visibleCheckers(scope)` copies each scope's services under that scope's read lock and releases it before any check starts. For each name it returns the wrapper to check, applying, in order:
  - the nearest scope wins (D4);
  - a yielded default is skipped (`isYieldedDefaultIn`, as `76404ef`);
  - an export whose source the requester sees is skipped (`readerSeesSource`, as `76404ef`);
  - an alias or export resolves to its source's wrapper and shares the source's call (D5);
  - a non-checker is kept as a nil entry for the forced map.
- `rootForcedCheckers(root)` adds the extension scopes keyed `<scope name>/<name>`, replacing the bespoke loop in `RootScope.HealthCheckWithContext`.
- `containerCheckers(root)` walks every scope for rounds and the root report, and deduplicates by source wrapper, so an alias or export adds no report entry (health-reports).
- **Non-blocking lazy check (D9).** `serviceLazy` gets an atomic `published` pointer (the built flag and the instance copy in one, see Implementation notes), set after the build completes. `isHealthchecker` and `healthcheck` read it and copy the instance without the build lock. A service being built counts as not built. This fixes D9 at its `76404ef` location, the queued goroutine's `serviceHealthCheckFunc`.

**H5. A context-aware limiter for checks started by forced runs, reads and rounds.**
- `HealthCheckParallelism > 0` creates a `chan struct{}` of that size in `NewWithOpts`, with no goroutine. A queued call's goroutine acquires a slot before it runs; waiting honors each waiter's ctx and bound through H3. The check's own bound starts after the slot, and so does the per-service bound of every caller waiting for it; each caller's ctx and `HealthCheckGlobalTimeout` still bound its wait from its call (review R9). A caller queued behind an abandoned check is the exception: its per-service bound runs from its call (H3, verification W1). This matches the 76404ef pool, whose workers applied `HealthCheckTimeout` when they ran a job. A check holds its slot until it returns, its context ends, or the report bound has passed since it took the slot. A check that keeps running after that, ignoring its context, no longer counts against the limit, so a hung check never stalls the callers queued behind it, and their deferred per-service bound always starts (code review R13). This deviates from the 76404ef pool, where a hung worker stayed blocked (flagged).
- **Single-service functions take no slot,** as today. When the call they need is queued, they take it over and run it at once on a new goroutine, without a slot; the queued goroutine then finds it claimed and gives up its wait for a slot. So composite checks never deadlock, without a nesting marker.
- **Removed:** `jobPool`, `newJobPool`, `RootScope.healthCheckPool` (all unexported). This fixes D2 and D6.
- **Removed from v1:** the ctx marker for nesting and `ErrHealthCheckCycle` (old H16). The takeover rule makes the marker unnecessary, and cycle detection was beyond the defects (out of scope, as today).

**H6. Panics are recovered where the check runs.** As before: `ErrHealthCheckPanic`, with the error value wrapped. `HookContainerPanic(root, "healthcheck", v)` when set. Nil means the error is the outcome (flagged deviation from #8's rule: no caller is there to receive the panic). A panic the hook raises is recovered on the check's goroutine and carried with the call's outcome (`checkOutcome{err, hookPanic}`): every waiter re-raises it from `wait` on its own goroutine, so a forced, single-service or report caller can recover it, and a round re-raises it to its scheduler, which recovers job panics (gocron does). When no waiter remains, the check's goroutine re-raises it: an intended crash (review R4).

**H7. A lazy service's check runs outside its lock** (D3), via H4's atomic flag (flagged).

**H8. Every path keeps its own bound.**

| Path | Waiter bound | Check bound when it starts the check | Timeout recorded? |
|---|---|---|---|
| `HealthCheck()` / `HealthCheckWithContext` | `HealthCheckTimeout` per service, `HealthCheckGlobalTimeout`, ctx | `HealthCheckTimeout` | No |
| Single-service functions | `HealthCheckSingleTimeout` (0: `HealthCheckTimeout`), ctx | the same | No |
| Report read | report bound: `HealthCheckTimeout` > 0, else `DefaultHealthReportCheckTimeout` (10s), none when < 0; ctx | report bound | Yes, at the report bound; not when ctx ends |
| Round | report bound; the round's ctx | report bound | Yes, at the report bound |

The forced paths keep today's semantics (no default bound). Report paths get a default bound, so a `HealthReport()` with no ctx never hangs on a check that ignores its context (audit item 8).

**H9. Recording: one record per check.**
- **What is recorded.** A completed check is recorded unless it was abandoned, or a report path already recorded its timeout (the late result is discarded, so a slow check does not flap: audit item 9). It is recorded with what it returned, under H3's waiter-result rule: a forced starter leaving at its bound records no timeout (H8), so its check's later nil is recorded healthy (verification 3, W1). Nothing is recorded once `closing` is set, nor for a call marked left because its service left the container while it ran: such a call starts no fresh call for its `next` waiters, which get its result (review R1). A report-path waiter that reaches its bound while waiting in `next` for a call it could not join records its timeout too (review R2). Neither a result nor a timeout is recorded for a service that left before its check started (review R11, R12).
- **Severity.** It is applied when recording: a failing optional check is stored as `HealthDegraded`. Forced paths return the raw error.
- **Times.** `PendingSince` is when the call in flight was created, its wait for a slot included. A recorded result's `Duration`, a timeout's included, runs from when the check started running (review CR11). A fresh call started for `next` waiters is dated from when the first of them asked (code review R16).

**H10. Reports and read modes.** `HealthReportWithContext` reads `Phase()` and the monitor state:
- **Monitored** (a schedule is active): build from records, and check on demand only covered services with no record and no call (replaces v1's wake channel for new services).
- **Unmonitored in Idle, Failed or Ready:** check covered services whose record is missing or at least TTL old, joining calls (H3), within the report bound and ctx.
- **Starting, Started, Closing:** records only.
- **Closed:** an empty report with state Unknown.
- **Eligibility** (audit item 4). Once the phase is not Idle, a covered wrapper that is a Start candidate (the `container-start` candidacy rules) and not `isStarted` is listed Unknown and not checked by reads or rounds. Forced paths ignore this.
- **Services that left** (code review R15). `checkOnDemand` and `healthRound` start no check for a covered service its scope no longer holds (`stillRegistered`, before `joinOrStart`). The recording guards in `finishCall` and `wait` stay, for a service that leaves between that check and the start; a white-box test covers them, since the R11 and R12 scenarios no longer reach them.
- **Ordering** is by scope name path, then service name; each covered scope's path is computed once per read, not per comparison (review CR12).
- **The phase gate** is applied last (H12).

**H11. Hooks: ordered, serialized, re-entrant delivery.** Unchanged from v1:
- The recording goroutine enqueues transitions with a sequence number under the monitor mutex, then closes `done`.
- A single deliverer drains the queue with no lock held: service hooks, then container hooks.
- The adders are copy-on-write. Panics go to `HookContainerPanic(root, "healthchanged", v)` or `Logf`.
- **The container hook** tracks the overall state of the unfiltered report *without* the phase gate (the gate is a read-time view). The state is kept incrementally: the monitor counts the members by state and severity, updates the counts on each record, `forget` and membership change, and derives the overall state from them in constant time, so a round costs O(n) (review R7: it was O(n²), 1.34s per round at 2,000 services).
- **Membership timing.** A service leaving changes the container state at once (`forget`). A service appearing does so when the next round or root report lists it (unknown until its first result, as in the report) or when its first result is recorded; registration and builds never recompute it. This follows alexliesenfeld/health, .NET and Spring Boot, which evaluate aggregate health only when checks run or are read, and avoids an unknown blip on every Starter registered after Ready (W3).
- **Logging.** "requested health check for service" is logged when a call is created, not when a read lists a service it serves from records (CR8).
- **No ctx** (audit item 7). A transition is delivered on whichever goroutine recorded it, possibly a scheduler's, so no caller context exists to pass. `HookContainerPanic`, also a `HookContainer*` field, already takes none. Keeping the names groups them with the container hooks in docs.

**H12. The container phase gates readiness and liveness** (Spring Boot's availability model, one-way).
- **Which reports are gated.** A report covers readiness when it has no tag filter or its filter includes `HealthTagReadiness`, and covers liveness likewise with `HealthTagLiveness`.
- **Readiness gate.** Starting, Started, Failed, Closing → `HealthUnhealthy`.
- **Liveness gate.** Failed → `HealthUnhealthy`.
- **No gate.** Idle is ungated, so containers that never call `Start` behave as before; Ready is ungated.
- **Closed** is handled by H10 (empty, Unknown).
- **Applied last,** after the service-derived state.
- **`IgnoreContainerPhase()`** turns the gate off for a read.
- **Phase field.** `HealthReport.Phase` tells why a gated report is unhealthy; `Err` stays per service.
- **Never the reverse.** Health never writes `startPhase`/`closePhase`.
- **Rejected:**
  - A synthetic "container" entry in `Services`: it would collide with service names and break the one-entry-per-check rule.
  - Gating Idle: it would turn every readiness probe of a container that never calls `Start` permanently unhealthy.
  - Spring's liveness BROKEN while starting: it makes Kubernetes restart a slow start unless a startup probe is set.

**H13. API placement.** `HealthReport`/`HealthReportWithContext` are added to `Injector`, implemented by `*Scope`, `*RootScope` (whole container) and `*virtualScope` (delegates). `Injector` has unexported methods, so no outside type implements it.

**H14. Doc correction (D8).** The `RootScope.HealthCheck` doc comment is corrected to: "performs health checks on the services of the root scope and of installed extension scopes (see [Extend]); other child scopes are not covered: use [RootScope.HealthReport] for the whole container." Behavior is unchanged.

**H15. Classification is a registration option; filtering is a report option.**
- **Options.** `WithHealthTags` and `AsOptionalHealth` are `ProvideOption`s writing two fields of `serviceOptions`, validated at registration like `AsStarter` (an empty tag or a transient registration panics).
- **Inheritance.** Clone copies, Override replaces, an alias or export has none, and a yielded default takes no part.
- **Filtering.** `HealthReportOption` is opaque: `MatchHealthTags` adds to an any-match set, and `IgnoreContainerPhase` sets a flag.
- **Overall state.** Unhealthy > Degraded > Unknown (a critical service without a result) > Healthy; then the gate (H12).

**H16. The gocron adapter module.**
- **Layout.** `scheduler/gocron/` is a new module `github.com/kartaladev/doger/scheduler/gocron`, package `dogergocron` (the doger<lib> naming of the Extension SPI), added to `go.work`. It requires `github.com/go-co-op/gocron/v2` (≥ v2.23.0, pinned at implementation) and the root module.
- **API.**
  ```go
  const DefaultInterval = 30 * time.Second
  var ErrInvalidOption = errors.New("dogergocron: invalid option")
  type Option func(*config) error
  func New(opts ...Option) (*Scheduler, error)
  func WithInterval(d time.Duration) Option
  func WithJobDefinition(def gocron.JobDefinition) Option // wins over WithInterval
  func WithStartAt(opt gocron.StartAtOption) Option       // replaces the default WithStartImmediately
  func WithSchedulerOptions(opts ...gocron.SchedulerOption) Option
  func WithJobOptions(opts ...gocron.JobOption) Option
  func WithScheduler(s gocron.Scheduler) Option // shared, not owned
  func (s *Scheduler) ScheduleHealthRounds(clock clockwork.Clock, round func(context.Context)) (func(context.Context) error, error)
  var _ doger.HealthScheduler = (*Scheduler)(nil)
  ```
- **Own mode.** `gocron.NewScheduler(append([WithClock(clock)], schedulerOpts...)...)`, then `NewJob(def, NewTask(func(ctx context.Context){ round(ctx) }), append([WithName("doger-health-round"), WithSingletonMode(LimitModeReschedule), WithStartAt(startAt)], jobOpts...)...)`, then `Start()`. `startAt` is `WithStartImmediately()` unless `WithStartAt` replaced it: gocron combines start-at options (each sets its own field), so a start-at given through `WithJobOptions` cannot replace the default; hence the dedicated option (decided during implementation, 2026-10-28). On a `NewJob` error it shuts the scheduler down before returning, so no goroutine remains. Stop is `ShutdownWithContext`.
- **Shared mode.** It only calls `NewJob` (the task is built once, for the mode in use: review CR13), wrapping the round to count the rounds of this job in progress, and returns a stop that calls `RemoveJob(id)`, treats `gocron.ErrJobNotFound` as removed (the owner may have shut its scheduler down first: review R5), then waits within ctx until no round of this job runs (review R6).
- **Functional options return errors.** The design-pattern rule is to validate configuration at construction.
- **Docs.** godoc warns against gocron's distributed elector/locker for this job, since health is per instance.

## Audit resolution

| Audit item (2026-10-28) | Resolution |
|---|---|
| 1. `RootScope.HealthCheck` coverage contradicted `extensions` | health-check-execution "Forced results keep their existing shape" now includes extension scopes; H14 doc text fixed |
| 2. Main's export/default skip rules not preserved | H4 keeps them; scenario rows for exports and yielded defaults |
| 3. Inconsistent duplicate rules | Forced map: unchanged keys (alias keys kept, export rules of main). Reports: one entry per underlying check |
| 4. Prompt checks of unstarted services | Prompt wake removed; unstarted Start candidates are not checked by reports or rounds (H10) |
| 5. Scheduler wait bounded by an ended Shutdown ctx | stop gets `context.WithoutCancel` (H1) |
| 6. Scheduling on by default | Opt-in via `HealthScheduler`; gocron adapter module (H1, H16) |
| 7. Hooks without ctx | Kept without ctx, with rationale (H11) |
| 8. On-demand reads unbounded | `DefaultHealthReportCheckTimeout` 10s for report paths (H8) |
| 9. Timeout then late result flaps | Late result discarded (H9) |
| 10. Scope creep | Removed per-scope run coalescing and cycle detection; the nesting marker replaced by single-service takeover (H3, H5) |
| Research: lifecycle → health | Phase gate (H12) |

## Risks / Trade-offs

- [A joining caller's context values never reach the check] → Flagged; documented on the options.
- [A check that ignores its context stays in flight until it returns] → One goroutine; each caller stops at its own bound; report paths record a timeout.
- [A check may run while its service shuts down: root Shutdown cancels checks in flight without waiting] → Kept, as the spec forbids Shutdown from waiting for checks; documented on Shutdown and the health-check APIs (CR4, flagged behavior change 3).
- [A hung check under `HealthCheckParallelism`] → It gives its slot back once its context ends or the report bound has passed, and runs on outside the limit until it returns: more than n checks can be running, at most n of them holding slots (code review R13).
- [Readiness turns Unknown when a Starter is registered after Ready, until `Refresh`] → Intended (the service is not serving); documented on `Extend` and in the report godoc.
- [A report covering no service is Healthy, so a mistyped tag yields Healthy] → Use the exported constants; documented on `MatchHealthTags`.
- [Consumers of the gocron adapter get gocron's dependencies] → Only by importing the adapter; the root module is unchanged.
- [gocron's `ShutdownWithContext` waits up to its stop timeout for a running job] → The container cancels the round ctx before stop, and a round returns at its ctx.
- [goleak may briefly see a cancelled context-aware check exiting after Shutdown] → goleak retries; scenarios release every check.
- [The monitor mutex is on the report path] → Reads copy records and do no I/O while holding it; `BenchmarkHealthReport_Monitored` guards it.
- [Shutdown waits past its deadline for a `ScheduleHealthRounds` call still in progress] → Kept so the schedule's stop always runs (H1); documented on `HealthScheduler` ("return promptly") and `ShutdownWithContext` (verification 3, S3).

## Public API & Defaults

New in the root module, all additive:

```go
// injector.go, InjectorOpts
HealthScheduler            HealthScheduler // nil (default): no scheduled rounds
HealthReportCacheTTL       time.Duration   // 0: 1s; <0: no cache; >0: TTL (unmonitored reads)
HookServiceHealthChanged   []func(scope *Scope, serviceName string, previous, current ServiceHealth)
HookContainerHealthChanged []func(root *RootScope, previous, current HealthState)

type HealthScheduler interface {
    ScheduleHealthRounds(clock clockwork.Clock, round func(ctx context.Context)) (stop func(ctx context.Context) error, err error)
}

const DefaultHealthReportCacheTTL = time.Second
const DefaultHealthReportCheckTimeout = 10 * time.Second

var ErrHealthCheckPanic = errors.New("DI: health check panic")

// options.go
func WithHealthTags(tags ...string) ProvideOption
func AsOptionalHealth() ProvideOption
const HealthTagReadiness = "readiness"
const HealthTagLiveness = "liveness"

type HealthReportOption struct{ /* opaque */ }
func MatchHealthTags(tags ...string) HealthReportOption
func IgnoreContainerPhase() HealthReportOption

type HealthState int // HealthUnknown (zero) | HealthHealthy | HealthDegraded | HealthUnhealthy; String()
type ServiceHealth struct {
    Service      ServiceDescription
    Tags         []string // sorted
    Optional     bool
    State        HealthState
    Err          error
    CheckedAt    time.Time
    Duration     time.Duration
    PendingSince time.Time
}
type HealthReport struct {
    State     HealthState
    Phase     ContainerPhase
    Monitored bool
    CheckedAt time.Time
    Services  []ServiceHealth
}

func (s *Scope) HealthReport(opts ...HealthReportOption) HealthReport
func (s *Scope) HealthReportWithContext(ctx context.Context, opts ...HealthReportOption) HealthReport
func (s *RootScope) HealthReport(opts ...HealthReportOption) HealthReport
func (s *RootScope) HealthReportWithContext(ctx context.Context, opts ...HealthReportOption) HealthReport
func (s *RootScope) AddServiceHealthChangedHook(hook func(*Scope, string, ServiceHealth, ServiceHealth))
func (s *RootScope) AddContainerHealthChangedHook(hook func(*RootScope, HealthState, HealthState))
// Injector gains HealthReport and HealthReportWithContext (H13).
```

The new module `scheduler/gocron` API is in H16. `InjectorOpts.copy()` copies the new fields and clones the hook slices, so `Clone()` copies the scheduler and the health hooks. Every default and its override is in the proposal's Defaults & Control table.

Compared with the first version of this change, `HealthCheckInterval`, `DefaultHealthCheckInterval`, `DefaultScheduledHealthCheckTimeout` and `ErrHealthCheckCycle` are dropped. None was ever released.

**Breaking changes:** none to signatures. The behavior changes are flagged in the proposal. **Migration:**
- A consumer that relied on a health-check panic crashing re-raises it from `HookContainerPanic`.
- A check that needs a joining caller's context values must not rely on joined calls.
- To get scheduled rounds, set `HealthScheduler: dogergocron.New()` (or another implementation).

## Test Strategy

Test files follow the source files:
- `health_run.go` → `health_run_test.go`: execution.
- `health_report.go` → `health_report_test.go`: reports, classification, the gate, including monitored reads, recording and `BenchmarkHealthReport_Monitored` (moved from `health_monitor_test.go`: S1).
- `health_monitor.go` → `health_monitor_test.go`: records, scheduling lifecycle, hooks.
- `scheduler/gocron/scheduler.go` → `scheduler/gocron/scheduler_test.go`, which has its own `TestMain` with goleak.

Conventions:
- Concurrency tests run under `-race` with `testWithTimeout`.
- A loop that advances a fake clock until a call returns has a step limit and the test calls `testWithTimeout`, so a regression fails the test instead of hanging the package (code review CR14).
- Time uses `clockwork.NewFakeClock()` (`BlockUntilContext`) or `synctest.Test`.
- A `manualScheduler` test double (in `health_monitor_test.go`) implements `HealthScheduler` for the scheduling scenarios.

| Spec | Scenario | Test | Kind |
|---|---|---|---|
| health-check-execution | Each caller keeps its own bound when it joins a check | `TestHealthCheck_JoinerOwnBound/<row>` | table, fake clock |
| health-check-execution | One caller leaving does not cancel the check for the others | `TestHealthCheck_CallerLeaves` | race |
| health-check-execution | Deadline and values reach the check | `TestHealthCheck_StarterContext/<row>` | table |
| health-check-execution | The only caller leaving cancels the check | `TestHealthCheck_LastCallerCancels` | race |
| health-check-execution | A check that ignores its context does not pile up | `TestHealthCheck_NoPileUp/<row>` (regression D1); caching is off, so the HealthReport row meets the check in flight (verification 2, W1) | table, race, goleak |
| health-check-execution | Concurrent forced calls share the in-flight checks | `TestHealthCheck_ConcurrentShareInFlight` | race |
| health-check-execution | A call that joins a running check gets its result | `TestHealthCheck_JoinRunning` | race |
| health-check-execution | An abandoned check is never joined | `TestHealthCheck_AbandonedNotJoined` | race |
| health-check-execution | Sequential calls check again | `TestHealthCheck_SequentialCheckAgain/<row>` | table |
| health-check-execution | Parallelism caps running checks | `TestHealthCheck_ParallelismCap/<row>` | table, race |
| health-check-execution | Waiting for a slot honors the context | `TestHealthCheck_SlotWaitHonorsContext` (regression D2) | race |
| health-check-execution | Registration is not blocked by checks waiting for a slot | `TestHealthCheck_SlotWaitDoesNotBlockProvide` (regression D2) | race |
| health-check-execution | A caller's per-service bound does not count the wait for a slot | `TestHealthCheck_BoundStartsAtSlot/<row>` (review R9) | table, fake clock |
| health-check-execution | Many services with a small limit all complete | `TestHealthCheck_NoQueueLimit` | unit |
| health-check-execution | Nested checks complete under a limit of one | `TestHealthCheck_SingleTakesOverQueued/<row>` | table, race |
| health-check-execution | New starts no goroutine | `TestNew_NoHealthGoroutine/<row>` (regression D6) | table |
| health-check-execution | Panic results | `TestHealthCheck_Panic/<row>` (regression D7) | table |
| health-check-execution | A panic in a scheduled round does not stop later rounds | `TestHealthRound_PanicKeepsRecording` | unit |
| health-check-execution | A check in flight does not delay the service | `TestServiceLazy_HealthCheckWithoutLock/<row>` (regression D3) | table, race |
| health-check-execution | A provider in progress does not block health checks | `TestServiceLazy_HealthCheckDuringBuild/<row>` (regression D9) | table, race |
| health-check-execution | Each underlying check runs once | `TestHealthCheck_EachCheckOnce/<row>` (regressions D4, D5) | table |
| health-check-execution | Map shape is unchanged | `TestHealthCheck_MapShape/<row>` | table |
| health-reports | Report entries after one read | `TestHealthReport_Entries/<row>` | table, fake clock |
| health-reports | Each report is the caller's own copy | `TestHealthReport_Isolated` | unit |
| health-reports | Entries are ordered by scope path then name | `TestHealthReport_Order` | unit |
| health-reports | Report membership | `TestHealthReport_Membership/<row>` | table |
| health-reports | Report coverage by scope | `TestHealthReport_Coverage/<row>` | table |
| health-reports | Overall state | `TestHealthReport_OverallState/<row>` | table |
| health-reports | HealthState names | `TestHealthState_String/<row>` | table |
| health-reports | Phase gate on readiness and liveness | `TestHealthReport_PhaseGate/<row>` | table |
| health-reports | Health never changes the phase | `TestHealthReport_PhaseUnchanged` | unit |
| health-reports | A closed container reports no service | `TestHealthReport_Closed/<row>` | table |
| health-reports | A service registered after Ready waits for Refresh | `TestHealthReport_UnstartedNotChecked/<row>` | table |
| health-reports | Idle containers check every service | `TestHealthReport_IdleChecksStarters` | unit |
| health-reports | Reads on a monitored container cost nothing | `TestHealthReport_MonitoredNoChecks` | race |
| health-reports | A service never checked is checked once by a read | `TestHealthReport_MonitoredFirstCheck/<row>` | table, fake clock |
| health-reports | On-demand reads honor the cache TTL | `TestHealthReport_OnDemandTTL/<row>` | table, fake clock |
| health-reports | Concurrent on-demand reads share one check | `TestHealthReport_OnDemandShared` | race |
| health-reports | On-demand reads by phase | `TestHealthReport_OnDemandByPhase/<row>` | table |
| health-reports | Reads without on-demand checks return the last recorded results | `TestHealthReport_ClosingKeepsResults` | unit |
| health-reports | A reader that stops waiting | `TestHealthReport_ReaderBound/<row>` | table, fake clock |
| health-reports | A reader leaving does not affect other readers | `TestHealthReport_ReaderLeaves` | race |
| health-reports | Forced and single-service checks refresh the report | `TestHealthReport_RefreshedByForced/<row>` | table |
| health-reports | A timed-out check's late result is discarded | `TestHealthReport_LateResultDiscarded` | fake clock |
| health-reports | An abandoned check is not recorded | `TestHealthReport_AbandonedNotRecorded` | race |
| health-reports | Results after shutdown began are not recorded | `TestHealthReport_NoRecordAfterShutdown` | unit |
| health-classification | Default classification | `TestHealthClassification_Default/<row>` | table |
| health-classification | Predefined tag values | `TestHealthTags_Constants/<row>` | table |
| health-classification | Tags of a registration | `TestHealthClassification_Tags/<row>` | table |
| health-classification | Severity of a registration | `TestHealthClassification_Optional/<row>` | table |
| health-classification | Invalid classification | `options_test.go: TestHealthOptions_InvalidPanics/<row>` | table |
| health-classification | Classification across clone, override and alias | `TestHealthClassification_CloneOverrideDefaultAlias/<row>` | table |
| health-classification | Filtered reports | `TestHealthReport_MatchTags/<row>` | table |
| health-classification | A filtered on-demand read checks only the covered services | `TestHealthReport_MatchTagsOnDemand` | unit |
| health-classification | Degraded entries | `TestHealthReport_Degraded/<row>` | table |
| health-classification | Degraded transitions reach the hooks | `TestHealthHooks_Degraded` | unit |
| health-classification | Zero-config liveness | `TestHealthReport_LivenessZeroConfig/<row>` | table |
| health-classification | Forced results ignore classification | `TestHealthCheck_IgnoresClassification/<row>` | table |
| health-check-scheduling | No scheduler, no rounds | `TestHealthScheduler_NilNoRounds` | unit |
| health-check-scheduling | Started containers without a scheduler leave no goroutine | `TestHealthScheduler_NilNoGoroutine` | goleak |
| health-check-scheduling | The scheduler is called only at Ready | `TestHealthScheduler_CalledAtReady/<row>` | table |
| health-check-scheduling | A scheduler that fails leaves the container unmonitored | `TestHealthScheduler_Failure/<row>` | table |
| health-check-scheduling | Round results | `TestHealthRound_Results/<row>` | table, fake clock |
| health-check-scheduling | A round checks each service once | `TestHealthRound_EachCheckOnce` | race |
| health-check-scheduling | A round after scheduling stopped does nothing | `TestHealthRound_AfterStop` | unit |
| health-check-scheduling | Stop happens before the Closing hooks | `TestHealthScheduler_StopBeforeClosing/<row>` | table |
| health-check-scheduling | A context-aware check in a round is cancelled before services shut down | `TestHealthScheduler_ShutdownCancelsRound` | race |
| health-check-scheduling | Shutdown does not wait for a check that ignores its context | `TestHealthScheduler_ShutdownSkipsHungCheck` | race |
| health-check-scheduling | Clone scheduling follows the clone's lifecycle | `TestHealthScheduler_Clone/<row>` | table |
| health-check-scheduling | The source keeps scheduling after its clone shuts down | `TestHealthScheduler_CloneShutdownKeepsSource` | unit |
| health-check-scheduling | Service hook calls follow state changes | `TestHookServiceHealthChanged/<row>` | table |
| health-check-scheduling | A forced run records changes too | `TestHookServiceHealthChanged_ForcedRun` | unit |
| health-check-scheduling | Hooks run one at a time, in order of addition | `TestHookServiceHealthChanged_Serialized` | race |
| health-check-scheduling | A hook may call health APIs | `TestHookServiceHealthChanged_Reentrant/<row>` | table, race |
| health-check-scheduling | Container hook calls follow the overall state | `TestHookContainerHealthChanged/<row>` | table |
| health-check-scheduling | A service leaving changes the overall state | `TestHookContainerHealthChanged_Membership` | unit |
| health-check-scheduling | Hook panic is contained | `TestHealthHooks_Panic/<row>` | table |
| container-clock | Report times come from the clock | `clock_test.go: TestClock_HealthReportTimes` | fake clock |
| container-clock | Health caching waits for the clock, not for wall time | `clock_test.go: TestClock_HealthCacheOnClock` | fake clock |
| container-clock | The scheduler receives the injector clock | `clock_test.go: TestClock_HealthSchedulerClock/<row>` | table |
| gocron-health-scheduler | Default cadence on the container clock | `TestScheduler_DefaultCadence/<row>` | table, fake clock |
| gocron-health-scheduler | A slow round skips the next tick | `TestScheduler_SingletonSkipsTick` | fake clock |
| gocron-health-scheduler | Overridden cadence | `TestScheduler_Overrides/<row>` | table, fake clock |
| gocron-health-scheduler | Scheduler options reach gocron | `TestScheduler_SchedulerOptions` | unit |
| gocron-health-scheduler | Invalid options | `TestNew_InvalidOptions/<row>` | table |
| gocron-health-scheduler | A schedule gocron rejects | `TestScheduler_RejectedJob/<row>` (the scheduler row: verification 3, W5) | table, goleak |
| gocron-health-scheduler | Shared scheduler | `TestScheduler_Shared` | unit |
| gocron-health-scheduler | Clean stop | `TestScheduler_CleanStop/<row>` | table, goleak |
| health-check-execution | A panic HookContainerPanic raises reaches the waiting callers | `TestHealthCheck_HookPanicPropagates/<row>` (review R4) | table |
| health-reports | A wait that times out on an abandoned check records the timeout | `TestHealthReport_TimeoutWhileAbandoned/<row>` (review R2) | table |
| health-reports | A check in flight when its service leaves is not recorded | `TestHealthReport_NotRecordedAfterLeaving/<row>` (review R1) | table |
| health-check-scheduling | A re-panicking HookContainerPanic leaves scheduling stopped | `TestHealthScheduler_FailurePanicHook` (review R3) | unit |
| health-check-scheduling | A failing stop does not fail Shutdown | `TestHealthScheduler_StopFailure/<row>` (review R8) | table |
| health-check-scheduling | A service appearing changes the overall state at the next round | `TestHookContainerHealthChanged_Appearing/<row>` (W3) | table |
| gocron-health-scheduler | Stopping a shared schedule | `TestScheduler_SharedStop/<row>` (review R5, R6) | table |
| health-check-execution | Callers queued behind a check they could not join keep their own slot rule | `TestHealthCheck_QueuedKeepSlotRule/<row>` (review R10) | table |
| health-reports | A service that leaves before its check starts is not recorded | `TestHealthReport_NotRecordedLeftBeforeStart/<row>` (review R11); `TestHealthReport_LeftDuringStart/<row>` (white-box: the service leaves between the read's registration check and the start of its check) | table |
| health-reports | Report times with a limiter | `TestHealthReport_TimesWithLimiter/<row>` (review CR11) | table, fake clock |
| health-check-scheduling | A shutdown through the root's own *Scope ends health recording | `TestHealthScheduler_RootScopeShutdownEndsRecording/<row>` (W4) | table |
| health-reports | A timeout of a check started after its service left is not recorded | `TestHealthReport_TimeoutNotRecordedLeftBeforeStart/<row>` (review R12); `TestHealthReport_LeftDuringStart/<row>` (white-box, as above) | table |
| health-check-execution | A caller whose context has already ended starts no check | `TestHealthCheck_EndedContextStartsNoCheck/<row>` (verification W2) | table |
| health-check-execution | A caller queued behind an abandoned check keeps its bound from its call | `TestHealthCheck_QueuedBehindAbandonedKeepsBound/<row>` (verification W1) | table, synctest |
| health-check-execution | A check that outlives its bound gives its slot back | `TestHealthCheck_OutlivedBoundFreesSlot/<row>` (code review R13) | table, synctest |
| health-check-execution | A caller that gives up behind an abandoned check starts no fresh check | `TestHealthCheck_EndedContextQueuesNoFreshCheck/<row>` (code review R14) | table, synctest, white-box |
| health-reports | PendingSince of a check started behind an abandoned one | `TestHealthReport_PendingSinceBehindAbandoned` (code review R16) | synctest, fake clock |
| health-reports | A service that left before the read reached it is not checked | `TestHealthReport_LeftBeforeReadNotChecked/<row>` (code review R15) | table |
| health-check-execution | A joining caller gets the check's result after the starter's bound | `TestHealthCheck_JoinerGetsCheckResult/<row>` (verification 3, W1) | table, synctest, fake clock |
| health-reports | A check its starter's bound cancelled is recorded with its result | `TestHealthReport_RecordedAfterStarterBound/<row>` (verification 3, W1) | table, synctest, fake clock |
| health-check-scheduling | A stop returned with an error is called at once | `TestHealthScheduler_StopReturnedWithError` (verification 3, S2) | unit |
| health-check-execution | Each caller keeps its own bound when it joins a check, row `forced_global_30ms_at_29ms` | `TestHealthCheck_JoinerOwnBound/forced_global_30ms_at_29ms` (verification 3, S4) | table, fake clock |
| health-check-execution | HealthCheckGlobalTimeout bounds the wait for a slot from the call | `TestHealthCheck_GlobalTimeoutBoundsSlotWait/<row>` (verification 3, W5) | table, synctest, fake clock |
| health-check-execution | With no caller waiting, the hook's panic is raised on the check's goroutine | `TestHealthCheck_HookPanicNoWaiterCrashes` (child process; verification 3, W5) | subprocess |
| health-reports | The root report covers extension scopes | `TestHealthReport_CoverageExtension` (verification 3, W5) | unit |
| health-reports | Rounds skip a service not started yet, forced paths check it | `TestHealthReport_UnstartedByCaller/<row>` (verification 3, W5) | table |
| health-reports | HealthCheckGlobalTimeout does not bound a read | `TestHealthReport_GlobalTimeoutNotApplied/<row>` (verification 3, W5) | table, synctest, fake clock |
| health-reports | A nil context reads like context.Background() | `TestHealthReport_NilContext/<row>` (verification 3, W5) | table |
| health-check-scheduling | Callers of a check cancelled by Shutdown | `TestHealthScheduler_ShutdownCancelCause` (verification 3, S1) | race |
| health-check-scheduling | Shutdown waits for a scheduler call in progress | `TestHealthScheduler_ShutdownWaitsForSchedulerCall` (verification 3, S3) | synctest |
| health-check-scheduling | Clone keeps the health hooks | `TestHealthHooks_CloneKeepsHooks` (verification 3, W5) | unit |
| health-check-scheduling | Nil hooks are skipped | `TestHealthHooks_NilSkipped` (verification 3, W5) | unit |
| health-check-scheduling | Container hooks follow the service hooks of the same result | `TestHealthHooks_ContainerAfterService` (verification 3, W5) | unit |
| gocron-health-scheduler | Options win over the defaults and accumulate in order | `TestScheduler_OptionPrecedence/<row>` (verification 3, W5) | table, fake clock |

**Hot paths and their tests:**

| Hot path | Tests |
|---|---|
| Forced run (`HealthCheckWithContext`, root with extension scopes) | `TestHealthCheck_ConcurrentShareInFlight`, `_MapShape`, `_EachCheckOnce`, `_CallerLeaves`, the existing `healthcheck_timeout_test.go` and extension suites |
| Single-service functions | `TestHealthCheck_NoPileUp/HealthCheckNamed`, `_JoinerOwnBound`, `_JoinRunning`, `_SingleTakesOverQueued` |
| Shared call: join, leave, bound, abandonment | `_JoinerOwnBound`, `_LastCallerCancels`, `_AbandonedNotJoined`, `TestHealthReport_LateResultDiscarded`, `_AbandonedNotRecorded`, `_TimeoutNotRecordedLeftBeforeStart` |
| Limiter | `_ParallelismCap`, `_SlotWait*`, `_NoQueueLimit`, `_BoundStartsAtSlot`, `_QueuedKeepSlotRule`, `_OutlivedBoundFreesSlot` |
| Listing services | `TestServiceLazy_HealthCheckDuringBuild`, `TestHealthCheck_EachCheckOnce`, `TestHealthReport_Coverage`, `_Membership` |
| Report read: monitored, on-demand, gate | `TestHealthReport_*`, plus `BenchmarkHealthReport_Monitored` (1,000 services) |
| Scheduler lifecycle: Ready, failure, stop, clone | `TestHealthScheduler_*`, `TestHealthRound_*` |
| Hooks | `TestHook*HealthChanged*`, `TestHealthHooks_*` |
| Container state per result (review R7) | `TestHealthRound_AllocsLinear`: allocations of a round at 1,000 services stay under 2.5× those at 500 (they were 3.85×), plus `BenchmarkHealthRound` (1,000 services) |
| Logging on cached reads (CR8) | `TestHealthReport_CachedReadsLogNothing` |
| Report sorting (CR12) | `TestHealthReport_ReadAllocs`: a monitored read of 2,000 services across two scopes takes under 8 allocations per service (15.3 before) |
| gocron adapter | `scheduler/gocron` suite under `-race` with goleak |

**Benchmark** (`go test -run '^$' -bench BenchmarkHealthReport_Monitored -benchmem -count=3 .`, Go 1.27.3, Apple M4 Pro, 2026-10-28): a monitored read of 1,000 recorded services takes 736–800 µs/op, 1.06 MB/op and 9,053 allocs/op, and runs no check. Listing the services and building the entries dominate; the monitor mutex is held only to copy records.

`BenchmarkHealthRound` (`go test -run '^$' -bench 'BenchmarkHealthRound$' -benchmem -count=3 .`, same machine, 2026-10-28, after review R7's fix): a round re-recording 1,000 results takes 3.01–3.06 ms/op, 2.80 MB/op and 33,085 allocs/op (R7 measured 351 ms and 1.04M allocations before). The same run re-measured `BenchmarkHealthReport_Monitored` at 693–753 µs/op, 0.88 MB/op and 8,052 allocs/op.

`BenchmarkHealthReport_Monitored` after review CR12's fix (`go test -run '^$' -bench BenchmarkHealthReport_Monitored -benchmem -count=3 .`, same machine, 2026-10-28): 586–609 µs/op, 0.75 MB/op and 3,051 allocs/op (8,052 before). `TestHealthReport_ReadAllocs` measures 3.0 allocations per service for a monitored read of 2,000 services across the root and a grandchild scope, with and without `-race` (15.2 before, 51.7 under `-race`).

**Docs:**
- `ExampleScope_HealthReport`: an idle container, on-demand reads with the TTL.
- `ExampleRootScope_HealthReport`: a started container; readiness during Closing via the gate; `IgnoreContainerPhase`.
- `ExampleMatchHealthTags`: readiness and liveness, with an optional service.
- `ExampleRootScope_AddServiceHealthChangedHook`.
- `scheduler/gocron`: `ExampleNew` (default) and `ExampleWithJobDefinition` (cron).

## Implementation notes

Recorded while applying the tasks on 2026-10-28; the tests that passed on arrival were each shown to fail by temporarily removing the behavior they guard (not committed).

- H4/D9: the "atomic built flag" is `serviceLazy.published atomic.Pointer[T]` (flag and instance copy in one); set at the end of build, cleared with built at shutdown.
- 5.1: on 76404ef the export_from_root_scope, export_from_source_scope, yielded_default and both MapShape rows pass on arrival; each failed when its skip rule was dropped temporarily (yielded default: B 0 instead of 1; export: A 2 instead of 1; extension keys: "ext:redis/pool" missing).
- 5.2: listing from a snapshot (no scope lock held while queueing) exposed the pool-vs-Shutdown race guarded by TestRootScope_HealthCheckDuringShutdown (send on closed channel). Interim fix: RootScope.poolMu; removed with the pool in group 8.
- 6.1: TestHealthCheck_SequentialCheckAgain passes on arrival on 76404ef (sequential calls already check each time).
- 6.3: raceWithTimeout and awaitHealthCheck became unused once every path waits on a sharedCall; removed as dead code there (7.3's Inline of them is then a no-op). serviceHealthCheckFunc removed too (wrapperHealthCheckFunc).
- 7.1: JoinerOwnBound, StarterContext and LastCallerCancels pass on arrival after group 6 (joiners already wait with their own ctx; the check still ran with the starter's ctx). On 76404ef JoinerOwnBound fails on the call count (each caller ran its own check).
- 7.3: waitSpec is {ctx, bound, pooled}; reportPath is added in group 11 where it is first read (an unused field fails lint). perServiceHealthCheckContext became dead and was removed. raceWithTimeout/awaitHealthCheck were already removed in 6.3.
- 8.1: SlotWaitHonorsContext and SlotWaitDoesNotBlockProvide fail on 76404ef (run in a temporary worktree: blocked past 1s) but pass on arrival here: groups 5-7 already stopped holding scope locks while queueing and made every waiter honor its own ctx. NoQueueLimit and ParallelismCap pass on arrival (the pool drained its queue). The 8.1 RED failures here are the n=4 goroutine row (4 pool workers) and SingleTakesOverQueued/HealthCheck (deadlock when agg holds the only pool worker).
- 11.2: spec correction: on Go 1.27 a nil panic's message is "runtime error: panic called with nil argument" (not "... (goexit=false)"); the health-check-execution "Panic results" row was updated, and the test derives the text from runtime.PanicNilError.
- 11.1: TestHealthReport_LateResultDiscarded's hook assertion ("the hook recorded only (Unknown, Unhealthy)") needs HookServiceHealthChanged; it is added to the test in group 19.
- 11.2: a report-path waiter records its timeout only when its own bound elapsed (its caller's ctx still live); the record's Duration then runs from when the call was queued.
- 13.3: reportQuery is {targets, prune, tags}; ignorePhase joins in group 15 with its first reader (an unused field fails lint), and targets/prune stand for the spec's scope field.
- 14.1: TestHealthCheck_IgnoresClassification passes on arrival; it failed (optional rows) when optional errors were temporarily dropped on forced paths.
- 17.1: TestHealthRound_PanicKeepsRecording checks the report states; its hook assertions ((Unknown, Unhealthy, boom) then (Unhealthy, Healthy, nil)) are added in group 19 with HookServiceHealthChanged.
- 18.1: TestHealthRound_AfterStop passes on arrival (after shutdown the round finds no service to check) and TestHealthScheduler_ShutdownSkipsHungCheck too (no lock is held across a check since group 2); the RED failures are the stop order rows, the clone rows, NoRecordAfterShutdown and ShutdownCancelsRound (hung: the round is never cancelled).
- 18.2: stopScheduling also cancels every check in flight synchronously (cause ErrStartAfterShutdown): cancelling only the round ctx reached the checks asynchronously, so TestHealthScheduler_ShutdownCancelsRound flaked. 18.3: GREEN already wrote the stop as the function stopScheduling(ctx), shared by shutdownAsWinner and the root-*Scope path, so the refactor step had nothing left to extract.
- 20.1: the TestClock_Health* tests pass on arrival (groups 10-18 read every health time from opts.Clock); they failed when finishCall read a real clock temporarily. 20.2 needed no code change (no health path reads time.Now).
- 21.1: user chose to pin gocron v2.23.0 although it was released 2026-10-26 (2 days before), an exception to the two-week age rule; v2.22.0 (2026-07-09) was the alternative and has the same APIs.
- 23.2: a timeout recorded by a report-path waiter is skipped for a call marked left too, since it is a record of a service no longer in the container.
- 24.1: on the unmonitored row the read's own entry already showed its timeout (reportEntries overrides it with the waiter's result); the test also asserts the recorded state, which stayed HealthHealthy on both rows.
- 26.2: GREEN carried the hook's panic in a private error type through the existing `func(ctx) error` check signature; 26.3 replaced it with `checkOutcome`, which the check closures now return.
- 27.2/27.3: GREEN already extracted the per-member count update (`countLocked`); 27.3 renamed it `memberCountsLocked` with gopls.
- 28.2: the log line moved to `startCall`, which runs once for each call created, by `joinOrStart` and for the `next` waiters of a finished call, outside the monitor mutex (Logf is user code).
- 31.1: the ported test is renamed `TestRootScope_HealthCheckTimeouts`, since the helper it was named after is removed in 31.2; the no-timeout row goes through `HealthCheckWithContext` (both checks at once, as the queued helper ran them) and the 10ms row through `HealthCheckNamedWithContext`.
- 32.1: the Clone and CloneWithOpts rows pass on arrival. A goroutine started temporarily in `CloneWithOpts` made both fail only when it outlived goleak's retries (about 2s): one sleeping 200ms passed, one sleeping 5s failed both rows.
- 33.2: the cross-check found no test for the W3 scenario "A service appearing changes the overall state at the next round" (no task created it); `TestHookContainerHealthChanged_Appearing` was added then. It passes on arrival (W3 specified the existing behavior) and failed on both rows when `keepOnly` temporarily stopped counting an appearing member.
- 34.2: the test advances the clock after the entry returns until every check it stopped waiting for has returned, so the RED rows fail on their assertions rather than on a synctest deadlock at Shutdown. The 9s/0s row passed on arrival; 9s/2s, three 6s and `HealthCheck()` failed with timeouts, 10 runs out of 10.
- 34.3: only a waiter of a pooled call still waiting for a slot defers its bound (`newCallWaiter` with queued); a waiter of a running call, a waiter of an unpooled call and a waiter in `next` start it at their call, the latter because its bound must cover its wait on an abandoned check (review R2). `callResult` checks the started deadline, as `healthCheckResult` does for a bound recorded in the context.
- 34.4: two Extract Function steps: `startBoundLocked` (the start of a deferred bound, at claim) and `newCallWaiter` (the waiter of every `joinOrStart` branch, whose bound starts at once unless its call is queued).
- 35.2: the second row's `HealthReport()` starts while "b" waits for the slot and returns after the 1s and 20ms advances; a read begun after them waits in `next` for "b"'s abandoned check. One `HealthCheck()` queues "a" and "b" in map order, so the test starts it with "a" alone, then registers "b" and starts a second `HealthCheck()`, which joins "a" and queues "b".
- 35.3: a timeout recorded for a call that never started running has Duration 0. This supersedes note 11.2's "from when the call was queued".
- 36.4: `call.left` does not change while `finishCall` runs, so its branch is hoisted out of the loop before `startFreshLocked` is extracted.
- 37.4: extracted as the method `healthTarget.stillRegistered()`, since `root_start.go` already declares a function `stillRegistered(*startCandidate)`.
- 38.2: passed on arrival; both rows failed when `Scope.ShutdownWithContext` temporarily reset `closing` after `stopScheduling` (db recorded HealthHealthy, one hook call).
- 39.2: the read measured 15.2 allocations per service without `-race` and 51.7 with it; after 39.3, 3.0 in both.
- Flaky tests seen while applying groups 34–41, each failing at `631c942` too (run in a separate worktree): `TestScope_ShutdownWithContextExpiration_Timeout`/`_Cancellation` (wall-clock margins), `TestHealthReport_Degraded` (a real 10ms bound under `-count=200`), `TestHealthReport_NoRecordAfterShutdown` (under `-count=4`) and `TestScope_HealthCheckWithContextExpiration_ChildScopes` (57 failures in 500 runs at `631c942`, 53 with groups 34–37).
- R12 (found after 37.3, probe run 3 times, failing each time, deleted after the run): with `HealthCheckTimeout` 20ms, a hung "b" and the R11 `Logf`, a root `HealthReport()` timed out on "b" after its scope shut down and recorded the timeout (`recordTimeoutLocked` checks only `left`): "b" was recorded and kept as a member, and the tracked state was Unhealthy. The R11 requirement's text already covered it; group 42 adds its scenario and fix.
- 22.2: adding scheduler/gocron to go.work required rows in the existing guard tests TestToolchain_GoDirective and TestModuleIdentity_WorkspaceModules; release.yml pins and tags scheduler/* like http/*.
- 52.4: the slot goes back through a `sync.OnceFunc`, run by `context.AfterFunc` on the check's context, by an `AfterFunc` timer on the injector's clock at the report bound, and when the check returns; no goroutine is added per check. 52.2's mutation (a waiter in `next` deferring its bound) failed both rows with a synctest deadlock: the entry never returned.
- 56.2: after 52.4, the temporary `runCall` that never returns no longer hangs these tests: the slot comes back at the report bound, so each fails within a second on its assertions, then on synctest's deadlock check. With `holdSlot` also disabled (the hang CR14 recorded), `TestHealthCheck_BoundStartsAtSlot` stops at its step limit ("had not returned after 60s of the fake clock"); the other two fail earlier on their own assertions.
- 57.1: golangci-lint's modernize (`embedlit`) sets the promoted `Context` field directly in the `healthCheckContext` literal.
- 60.2: the two `returns_ctx_err` rows passed on arrival (a check returning its ended context's error was already a timeout); the nil and "down" rows failed with the timeout error, and the report recorded HealthUnhealthy instead of HealthHealthy or "down".
- 60.3: `callResult` keeps one case of the old rule: a check cancelled by root Shutdown (cause `ErrStartAfterShutdown`) still ends its waiters with `timeoutError(context.Canceled)`, as the requirement "Shutdown cancels the checks in flight with a container-shut-down cause" states, also for a check that ignores its context and returns nil later. The record needs no such case, since nothing is recorded once shutdown began. 60.4 extracted `checkResult`, shared by `callResult` and `finishCall`'s record.
- 61.2: `manualScheduler` gained `stopWithErr`, returning its stop function together with `err`.
- 62.1–62.3, 63.x, 64.x pass on arrival (they pin existing behavior). Each was shown to fail under a temporary mutant in a scratch copy of the working tree (not committed). 62.1: a round skipping svc. 62.2: a forced joiner taking the call's context; only the new 29ms row fails, the other six pass. 62.3: ordering by the scope's own name. 63.1–63.9 and 64.x: the mutants of Verification 3 evidence; each fails only its own rows. 63.4's `HealthCheckNamed` row needed a second mutant, in `serviceHealthCheck`, since the recorded one changes only `healthCheckTargets`. 63.11: `stopScheduling` cancelling with `context.Canceled` as the cause. 63.12: `stopScheduling` returning once its ctx ends, without waiting for the scheduler call.
- 63.10: under the recorded deferred-bound mutant the 30ms row fails 30 of 30 runs without `-race` but only 10 of 30 with it: the forced caller started the queued call itself, so its deadline also ends the call's base context, which can still return the caller at 30ms. A mutant dropping the global timeout from the caller's context (`clockwork.WithTimeout` replaced by `context.WithCancel`) fails the 30ms row 20 of 20 under `-race`, the 29ms row passing; the correct code passed 50 of 50.

**Review and verification findings (2026-10-28).** Each was proven before planning its fix, with a throwaway probe test asserting the expected behavior (deleted after the run):
- R1: a child-scope check returning after `child.Shutdown()` left records=1, members=1, and the container hook reported healthy → unhealthy for the gone service.
- R2: after `HealthCheck()` abandoned a hung check, two rounds timed out without recording; the monitored report stayed healthy with no error.
- R3: with a scheduler that panics and a re-panicking `HookContainerPanic`, `Shutdown` after a recovered `Start` did not return within 1s.
- R4: run in a child process, a re-panicking `HookContainerPanic` on a panicking single-service check killed the process from `runCall`'s goroutine; the caller's `recover` never ran.
- R5, R6 (gocron, shared mode): stop logged `DI: health scheduler stop failed: gocron: job not found` after the owner shut its scheduler down, and returned while a round still ran.
- R7: a round cost 94ms / 351ms / 1.34s and 270k / 1.04M / 4.08M allocations at 500 / 1,000 / 2,000 services.
- R8: a panic in the scheduler's stop never reached `HookContainerPanic`.
- CR4: a lazy service's `Shutdown` ran while its check was in flight at root Shutdown; the code comment claiming otherwise is corrected (behavior kept: Shutdown must not wait for checks).
- CR8: 10 cached reads of 50 services logged 500 "requested health check" lines and ran no check.
- CR9: gopls finds `queueServiceHealthcheck` referenced only by `root_scope_test.go`; CR10: both replace paths (`scope.go`, `di.go`) already call `RootScope.forget`.
- W3: a service registered after a round produced no container hook call; specified (not changed) after comparing alexliesenfeld/health, .NET and Spring Boot.

**Second review evidence (2026-10-28).** The eight candidates of the code review of PR #9 were each checked before planning with a throwaway probe test, run under `-race` in a scratch copy of the repository, outside the working tree (which was not touched); the probes seed the RED tests of groups 34–39. Each probe asserted the expected behavior:
- R9: `HealthCheckParallelism: 1`, checks taking 9s and 2s (synctest): the 9s check, which got the slot second, was recorded timed out at 10s with Duration 10s after running 8s; with three 6s checks, two were recorded timed out.
- R10: a check started by `HealthCheck()` ran while another held the only slot, because it queued behind a single-service call abandoned at 10ms; a single-service caller queued behind a pooled call abandoned at `HealthCheckTimeout` 50ms got a check context with a deadline although `HealthCheckSingleTimeout` was -1ns.
- R11: a `Logf` shut the child scope down when a root `HealthReport()` (and, second probe, a round) started the check of root `a`: child `b` was then checked, recorded and kept as a member, and the tracked container state was Unhealthy.
- W4 (finding 4): after `Shutdown()` on a provider's injector, a newly registered healthy service stayed HealthUnknown through `HealthReport()` and `HealthCheck()`, with Phase idle and no hook call; this matches the health-check-scheduling requirement, so it is documented, not changed.
- Finding 5 (not a defect): the reviewer's scenario on a joined call is H9's specified discard of a late result; on a call queued in `next` (its context already ended), the record was the same timeout with the 24.2 recording removed in a mutated copy.
- CR11: a check queued for a slot had a `PendingSince` though it had not started; a timed-out record's Duration included the wait.
- CR12: sorting 2,000 targets with `compareHealthTargets` took 64,285 allocations, 5 with paths computed once; a monitored read of 2,000 services across the root and a grandchild scope took 30,656 allocations, 6,076 with paths computed once.
- CR13: by inspection, `ScheduleHealthRounds` builds a task that its shared branch replaces.

**Verification findings (2026-10-28).** `/opsx:verify` after group 43 raised three warnings and three suggestions; each was checked before planning groups 44–47, with throwaway probes (deleted after the run) or with gopls:
- W2: probe run 40 times per tree, with a context-aware check and an already-cancelled context. At `631c942`, `HealthCheckWithContext` started the check in 3 runs out of 40; at `76404ef` (worktree), in none. `HealthCheckNamedWithContext` started it in none on either tree. The race: the waiter leaves at once, but the call it created has already started its goroutine. The same race makes `TestScope_HealthCheckWithContextExpiration_ChildScopes` flaky (32 failures in 300 runs; 57 in 500 at `631c942`): when an abandoned check returns before the second call arrives, that call, whose context has ended, starts the check again. Applying 44.3 showed a second path (4 failures in 500 with only the no-call guard): the abandoned check is still in flight when the second call arrives, so the call queues in `next`, and when the check returns before the caller's wait leaves `next`, a fresh check starts for it. `TestHealthCheck_EndedContextQueuesNoFreshCheck` reproduces it deterministically (2 calls of the check); the guard covers a call that cannot be joined too.
- W1: probe (synctest, 3 runs out of 3) with `HealthCheckParallelism` 1, `HealthCheckTimeout` 20ms, "hog" holding the slot and an abandoned "leaf" check: a `HealthCheck()` queued behind it returned after 20ms with a timeout while the check started for it waited for the slot, which the R9 requirement's text did not allow. The behavior is kept, because review R2 needs that bound to cover the wait on a hung abandoned check; the requirement now states it. The `InjectorOpts.HealthCheckParallelism` godoc (`injector.go:275`) still says that waiting for a slot "ends with each caller's context and bound", which is wrong since 34.3.
- W3: `TestHealthReport_NoRecordAfterShutdown` failed 2 runs out of 300 (it sleeps 10ms of real time for a stray hook call). Applying 46.2 showed the real cause (9 failures in 500 under synctest alone, each missing the (Unknown, Healthy) call): `HealthReport()` returns once its waiter is done, before the check's goroutine delivers the transition it recorded, and Shutdown then drops the undelivered transition, as the requirement allows (no hook once shutdown began). The test now waits (`synctest.Wait`) for that delivery before Shutdown, which its Given step assumes. `TestHealthReport_Degraded` failed once in 200 runs at both `631c942` and the working tree (a real 10ms bound under `-count=200`), and 0 times in a later 300.
- S1: gopls shows `callWaiter.parent`, `reportPath` and `bound` assigned only in `newCallWaiter`, from `spec` (`health_run.go:626`), and read only at `:529`, `:552` and in `startBoundLocked`.
- S2: `stillRegistered(*startCandidate)` (`root_start.go:761`) and `healthTarget.stillRegistered` (`health_run.go:791`) make the same check under the scope's read lock.
- S3: the `healthCheckContext` comment (`health_run.go:837`) describes only a shared check's context; `newCallWaiter` also builds a waiter's context with a deferred bound from it.

**Flaky tests after group 48 (2026-10-28).** Reproduced by running the root package's race-enabled test binary repeatedly with `-test.cpu` 1, 2 or 4 beside 14 CPU-burning processes: 23 of 60 runs failed. Every failure was a test waiting on real time while starved of CPU; none is a product defect. The two that could have hidden one were checked with temporary diagnostics: the unhealthy state in `TestHealthReport_TimeoutNotRecordedLeftBeforeStart` was member "a" timing out (its check ran 416ms against a 20ms bound), and the call in `TestHealthCheck_SingleTakesOverQueued` was runnable inside `finishCall` at the 1s mark and returned 380ms later.
- Real-time bounds and margins (`TestScope_ShutdownWithContextExpiration_*`, `TestScope_ContextExpiration_ParallelOperations`, which shutdown-context allows to skip a service reached after the cancel, `TestHealthCheck_PerServiceErrorInTime`, `TestHealthCheck_NoPileUp`, `TestHealthReport_TimeoutNotRecordedLeftBeforeStart`) and 1–2s `Eventually`/`time.After` windows (`SingleTakesOverQueued`, `NotRecordedAfterLeaving`, `ShutdownSkipsHungCheck`, the hook tests, the callers of `healthReportTestInPhase`, the slot-wait and clock tests) now run in synctest bubbles with their rows and assertions unchanged: a window there ends only once every goroutine in the bubble is blocked, so it still fails on a real hang or lost delivery, never on starvation. Mutation checks (no takeover; a pile-up; the R12 guard removed) still fail the bubbled tests.
- `testWithTimeout` (main_test.go) treats its timeout as a hang guard with a 5s floor; the inherited 100ms and 450ms guards fired on synchronous tests starved of CPU.
- `TestPhase_RaceFree` spun 20 goroutines without blocking, starving the package on one P; they now yield (`runtime.Gosched`), and an unlocked `Phase` still trips the race detector.
- After the fixes: 0 of 100 runs failed under the same load. The `tests` package (its own helper) had 0 failures in 60 such runs and is unchanged.

**Verification 2 (2026-10-28).** A second `/opsx:verify` raised one warning and two suggestions, each checked before planning groups 49–51:
- W1: the HealthReport row of `TestHealthCheck_NoPileUp` passed without exercising the rule it covers. Its 100 reads fall within the default 1s `HealthReportCacheTTL`, so after the first read records the timeout, the rest are answered from that record and never reach `joinOrStart`. Probe in a scratch copy of the working tree, with a temporary pile-up (`joinOrStart` starting a call whenever the call in flight cannot be joined):

  | Row setup | Correct code | Temporary pile-up |
  |---|---|---|
  | Default cache (today) | all rows pass | HealthCheck and HealthCheckNamed fail (100 calls); HealthReport passes |
  | Caching off (negative TTL) | all rows pass, 200 of 200 runs | all three rows fail (100 calls) |

  No report, round or scheduler test caught the pile-up either; the rule was covered for the report path only through the shared `joinOrStart`. The scenario now turns caching off. Applying 50.2 (`HealthReportCacheTTL: -time.Nanosecond`): the test passed on arrival, and with the same temporary pile-up (the `!ok` case of `joinOrStart` also taken for a call that cannot be joined) all three rows failed with 100 calls, the HealthReport row included.
- S1: the proposal's W2 row still read "finding no check in flight", narrower than the requirement widened in group 44 ("no check in flight it can join … SHALL wait for no check started later").
- S2: `tasks.md` recorded none of the test stabilization above (`main_test.go`, `phase_test.go`, the synctest bubbles); group 49 records it.

**Code review findings (2026-10-29).** The code review of the working tree (after group 51) reported seven findings. Each was checked before planning groups 52–59, in scratch copies of the working tree and of `631c942`, with throwaway probe tests (synctest; a 1h bubble-time limit stands for "never returns"):
- R13: `HealthCheckParallelism` 1, a hung "hog" whose `HealthCheck()` caller timed out at `HealthCheckTimeout` 1s. Behind it, `HealthCheck()`, `HealthReport()` and a round had not returned after 1h. With `HealthCheckTimeout` 0 and an unbounded `HealthCheck()` holding the slot, `HealthReport()` (report bound 10s) had not returned either. At `631c942` all four return. The cause is group 34: a queued call's per-service bound starts only when the call gets a slot, and a hung check kept its slot forever.
- R14: a caller queued in `next` behind an abandoned check, whose context ends before that check returns, got a fresh check: 2 calls instead of 1. The same happens at `631c942`. `TestHealthCheck_EndedContextQueuesNoFreshCheck` covered only a context that ended before queueing.
- R15: with the R11 `Logf`, "b" was checked once after its scope shut down, through `HealthReport()` and through a round; the result was discarded. The same happens at `631c942`.
- CR14: with a temporary `runCall` that never returns, `TestHealthCheck_BoundStartsAtSlot` ran until `-timeout 30s` killed the package (all 4 rows still running). `TestHealthCheck_QueuedKeepSlotRule` and `_QueuedBehindAbandonedKeepsBound` failed at once (synctest deadlock); `_EndedContextStartsNoCheck` runs no check. `TestHealthReport_TimesWithLimiter` and `TestHealthCheck_SequentialChecksShareGlobalTimeout` use the same open-ended loop.
- R16: a call queued at 00:00:01 behind a check abandoned at 00:00:00 got its fresh check at 00:00:05. A read at 00:00:06 showed PendingSince 00:00:05 (the fresh call's creation), not 00:00:01. The same happens at `631c942`.
- CR15: by inspection, `newCallWaiter` builds a `healthCheckContext` only for its `Err` mapping. The `Deadline()` it reports equals that of `context.WithCancelCause(spec.ctx)`, so the second role is not a defect.
- CR16: `BenchmarkHealthRound` (1,000 services, `-count 10`) took 3.291 ms ± 9% with `stillRegistered` and 3.258 ms ± 22% with it returning true without the lock (p = 0.971). `BenchmarkHealthReport_Monitored` runs no `finishCall`. Closed with no change.
- Prototype of the fixes (scratch copy, not committed): the full root suite passes under `-race`, golangci-lint reports 0 issues, and every probe above passes. The only existing test that needed changes was `TestHealthCheck_QueuedBehindAbandonedKeepsBound`: with "hog"'s slot given back at its own 20ms bound, the fresh "leaf" check would run, so "hog" now takes the slot 1ms after the entry's call (20 of 20 runs pass on both trees). With R15's guard, removing the recording guards of `finishCall` and `wait` fails no existing test, hence `TestHealthReport_LeftDuringStart`. `BenchmarkHealthRound` with the R15 guard: 3.242 ms ± 3% (p = 0.247), +0.55% B/op.

**Verification 3 evidence (2026-10-29).** `/opsx:verify` after group 59 reported five warnings and five suggestions. Each was checked before planning groups 60–66, in a scratch copy of the working tree (which was not touched), with throwaway probe tests (`zz_evidence_test.go` in the root and in `scheduler/gocron`) and a mutation runner. Each untested sentence was checked twice: its probe pins today's behavior, and a temporary mutant that breaks the sentence is run against the existing suite (probes skipped), then against the probe. Probes and mutants are not committed; the probes seed the tests of groups 60–64.
- W1 (defect): `HealthCheckGlobalTimeout` 30ms; a `HealthCheck()` starts the check of a "db" that ignores its context; at +10ms an unbounded `HealthCheckNamed` joins; at +60ms the check returns nil (second row: `"down"`). The joining caller got `DI: health check timeout: context deadline exceeded` on both rows, and the record was that timeout too, 20 runs out of 20. Cause: `callResult` and the record in `finishCall` turn a done check context into a timeout (`health_run.go:863-865`, `healthCheckResult` in `utils.go`), and the starter's bound cancels that context (`newHealthCheckContext`).
- W1b (refuted): the reviewer's queued variant (`HealthCheckParallelism` 1, "hog" holding the slot, a `HealthCheck()` with global 30ms queueing "db", a `HealthReport()` joining it) ended with "db" checked once and healthy in the reader's report, 20 runs out of 20.
- W2: an optional service timed out on a report read is `HealthDegraded` with an error matching `ErrHealthCheckTimeout`, as health-classification says; the health-reports sentence said `HealthUnhealthy`.
- W3: a root `HealthCheck()` alone calls the exported `"child"/"svc"` once (its map has the key `svc`), so the scenario's "svc 1 time" in total could not hold; the test asserted 2.
- W4: `TestHealthScheduler_StopBeforeClosing/provider_injector` already asserts that no Closing hook runs on the root's own `*Scope` path, which the scenario row's Then did not allow for.
- W5 (13 confirmed gaps, 1 refuted). Every probe passes on today's code. Under each mutant below the suite stayed green and the probe failed:

  | Sentence | Mutant |
  |---|---|
  | `HealthCheckGlobalTimeout` does not bound a read | `reportBound` returns the global timeout when set |
  | A nil ctx reads like `context.Background()` | the nil check in `healthReport` removed (nil dereference) |
  | Extension scopes are in the root report | `containerCheckers` skips `extensionScopes()` |
  | Rounds skip an unstarted Starter | `healthRound` without its `eligibleForReport` filter |
  | Forced paths check an unstarted Starter | `healthCheckTargets` skips ineligible targets once started |
  | `Clone()` copies the health hooks | `InjectorOpts.copy` drops both hook slices |
  | Container hooks follow the service hooks | `recordLocked` queues the container change first |
  | Nil hooks are skipped | the `hook != nil` guard removed (the nil call reached `HookContainerPanic`) |
  | A re-panicking hook leaves the container unmonitored | `startScheduling`'s `err` starts nil instead of `errSchedulerInterrupted` |
  | With no waiter, a hook panic crashes on the check's goroutine | the re-raise in `finishCall` removed (child process: exit 2, `panic: evidence-panic`; survived under the mutant) |
  | The global timeout bounds a forced caller's slot wait | the deferred-bound waiter ignores its caller's deadline, keeping cancellation |
  | gocron: a job option wins over the default; repeated job options accumulate in order | defaults appended after the consumer's options; `WithJobOptions` replacing earlier ones |
  | gocron: a scheduler option wins over the default; repeated scheduler options accumulate | `WithClock(clock)` appended last; `WithSchedulerOptions` replacing earlier ones |
  | gocron: a scheduler gocron rejects is an error | the `NewScheduler` error swallowed (`gocron.WithLocation(nil)`: "location must not be nil") |

  Refuted: "a service that left shows no result" (keeping the record in `forget` fails `TestHealthReport_NotRecordedAfterLeaving`), and "`CloneWithOpts` keeps the classification" (`Clone()` is `CloneWithOpts(s.opts.copy())`, already covered by `TestHealthClassification_CloneOverrideDefaultAlias`).
- S1 (premise refuted): at Shutdown, a forced caller of an in-flight check gets `DI: health check timeout: context canceled` (`errors.Is(err, ErrStartAfterShutdown)` false); only the check's own `context.Cause` is `ErrStartAfterShutdown`, whose message is "DI: container is shut down". Documented, not changed.
- S2 (defect): a `HealthScheduler` returning a stop function and an error: the stop was called 0 times through Start and Shutdown, 20 runs out of 20 (`callScheduler` returns `nil, err`).
- S3: `ShutdownWithContext` with a 50ms deadline had not returned after 1 minute (synctest) while `ScheduleHealthRounds` blocked at PhaseReady; with a blocked Starter instead, it returned by its deadline. Kept as designed (H1) and documented.
- S4: under a mutant where a forced joiner inherits the starter's deadline, `TestHealthCheck_JoinerOwnBound` passed (every row) and the new 29ms row failed; the suite caught that mutant only through `TestHealthCheck_SlotWaitHonorsContext`. Under a mutant ordering report entries by the scope's own name instead of its path, `TestHealthReport_Order` passed; the swapped example fails it.
- S5: read literally (the read starts after the 1s and 20ms advances), the TimesWithLimiter row gives Duration 40ms, not 20ms: the read waits in `next` behind the abandoned check (note 35.2). H4's "atomic `built` flag" text predates the `published` pointer recorded in the notes above.

## Migration Plan

1. `main` (with #8, #11, #12, #14, #15) is already the base: the branch was rebased on `76404ef`. Task group 1 re-confirms names.
2. Implement the task groups in order. Each defect fix starts with its regression test (D1–D9); the probe file from the audit re-verification seeds the RED tests.
3. Release notes list the flagged behavior changes, the phase gate, and the opt-in scheduler with the gocron adapter.
4. Rollback: revert the PR. The gocron module is separate and can be unpublished independently.

## Open Questions

- The JSON shape of `HealthReport` (including `Err` and `Phase`) is deferred to the HTTP-handler follow-up.
- Release tagging of the `scheduler/gocron` module (its own version tags, like `http/*`) follows the existing workspace-module convention; to confirm at release.
