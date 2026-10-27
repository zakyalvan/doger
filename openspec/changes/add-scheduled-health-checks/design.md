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

  An error or a recovered panic is logged (`DI: health scheduler failed: ...`) or passed to `HookContainerPanic(root, "healthscheduler", v)`. The container stays unmonitored and on-demand reads keep working.
- **Stop.** Stopping comes first in `shutdownAsWinner`, and on the root-`*Scope` bypass path. Under the mutex the container sets `closing`, so nothing is recorded and no hook fires. It then cancels the monitor context, which cancels the rounds in progress, and calls stop with `context.WithoutCancel(shutdownCtx)`. That way an ended Shutdown context (shutdown-context, #14) cannot make Shutdown return before the scheduler's goroutines exit. Shutdown waits for stop. A stop error is logged. If a schedule call is still in progress, Shutdown waits for it, then calls its stop.
- **Rejected:**
  - A built-in timer loop in the root module. It is default-on in the first version and was rejected in the audit: it goes against the prior art, leaks in every `Start`-without-`Shutdown` test, and the TTL cache already solves load amplification. The user prefers gocron.
  - Importing gocron in the root module: forced dependencies on every consumer.
  - Configuring the scheduler through an `Extension`: an extension registers services into a scope, while scheduling is container infrastructure that must be known at Ready.

**H2. One monitor per root, keyed by service wrapper.**
- **State.** A `healthMonitor` on `RootScope` holds, under one mutex: `records`, `inflight`, the transition queue, the scheduling state (`idle`/`starting`/`scheduled`/`stopped`) and the stop function, all keyed by wrapper. A wrapper is unique per registration, so an Override or a re-registration starts a fresh record.
- **Pruning.** A record is pruned when its wrapper is removed: through the internal hooks for registration replacement and scope shutdown, and as a fallback when a read or round lists services.
- **Lock rule.** The monitor mutex is never held while listing services, calling a check, or calling user code (the scheduler, hooks).

**H3. An in-flight check is a shared call; each waiter has its own bound.**
- **States.** A `sharedCall` per service is queued (waiting for a slot), running, or returned. It holds a done channel, a result, a waiter count and a cancel function. The first caller creates it and runs it on a new goroutine; later callers join.
- **Waiting.** Each waiter selects on `done`, its own ctx, and a timer for its own bound, started at its call (H8). When it stops early it gets `timeoutError(cause)`.
- **Abandonment.** When the waiter count reaches zero before the call returns, the check's ctx is cancelled and the call is marked abandoned. It stays in `inflight` until its goroutine exits; a newcomer waits on its `done` within its own bound, then creates a fresh call. An abandoned result is not recorded.
- **The check's context.** It is built from `context.WithoutCancel(starter ctx)`, the earlier of the starter's deadline and the starter's bound (recorded with `withHealthCheckDeadline`), and a cancel for abandonment and shutdown.
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
- **Non-blocking lazy check (D9).** `serviceLazy` gets an atomic `built` flag, set after the build completes. `isHealthchecker` and `healthcheck` read it and copy the instance without the build lock. A service being built counts as not built. This fixes D9 at its `76404ef` location, the queued goroutine's `serviceHealthCheckFunc`.

**H5. A context-aware limiter for checks started by forced runs, reads and rounds.**
- `HealthCheckParallelism > 0` creates a `chan struct{}` of that size in `NewWithOpts`, with no goroutine. A queued call's goroutine acquires a slot before it runs; waiting honors each waiter's ctx and bound through H3. The check's own bound starts after the slot.
- **Single-service functions take no slot,** as today. When the call they need is queued, they take it over and run it at once on their goroutine; the queued goroutine later finds it done and releases its slot. So composite checks never deadlock, without a nesting marker.
- **Removed:** `jobPool`, `newJobPool`, `RootScope.healthCheckPool` (all unexported). This fixes D2 and D6.
- **Removed from v1:** the ctx marker for nesting and `ErrHealthCheckCycle` (old H16). The takeover rule makes the marker unnecessary, and cycle detection was beyond the defects (out of scope, as today).

**H6. Panics are recovered where the check runs.** As before: `ErrHealthCheckPanic`, with the error value wrapped. `HookContainerPanic(root, "healthcheck", v)` when set, whose own panic propagates. Nil means the error is the outcome (flagged deviation from #8's rule: no caller is there to receive the panic).

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
- **What is recorded.** A completed check is recorded unless it was abandoned, or a report path already recorded its timeout (the late result is discarded, so a slow check does not flap: audit item 9). Nothing is recorded once `closing` is set.
- **Severity.** It is applied when recording: a failing optional check is stored as `HealthDegraded`. Forced paths return the raw error.

**H10. Reports and read modes.** `HealthReportWithContext` reads `Phase()` and the monitor state:
- **Monitored** (a schedule is active): build from records, and check on demand only covered services with no record and no call (replaces v1's wake channel for new services).
- **Unmonitored in Idle, Failed or Ready:** check covered services whose record is missing or at least TTL old, joining calls (H3), within the report bound and ctx.
- **Starting, Started, Closing:** records only.
- **Closed:** an empty report with state Unknown.
- **Eligibility** (audit item 4). Once the phase is not Idle, a covered wrapper that is a Start candidate (the `container-start` candidacy rules) and not `isStarted` is listed Unknown and not checked by reads or rounds. Forced paths ignore this.
- **Ordering** is by scope name path, then service name.
- **The phase gate** is applied last (H12).

**H11. Hooks: ordered, serialized, re-entrant delivery.** Unchanged from v1:
- The recording goroutine enqueues transitions with a sequence number under the monitor mutex, then closes `done`.
- A single deliverer drains the queue with no lock held: service hooks, then container hooks.
- The adders are copy-on-write. Panics go to `HookContainerPanic(root, "healthchanged", v)` or `Logf`.
- **The container hook** tracks the overall state of the unfiltered report *without* the phase gate (the gate is a read-time view).
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
  func WithSchedulerOptions(opts ...gocron.SchedulerOption) Option
  func WithJobOptions(opts ...gocron.JobOption) Option
  func WithScheduler(s gocron.Scheduler) Option // shared, not owned
  func (s *Scheduler) ScheduleHealthRounds(clock clockwork.Clock, round func(context.Context)) (func(context.Context) error, error)
  var _ doger.HealthScheduler = (*Scheduler)(nil)
  ```
- **Own mode.** `gocron.NewScheduler(append([WithClock(clock)], schedulerOpts...)...)`, then `NewJob(def, NewTask(func(ctx context.Context){ round(ctx) }), append([WithName("doger-health-round"), WithSingletonMode(LimitModeReschedule), WithStartAt(WithStartImmediately())], jobOpts...)...)`, then `Start()`. On a `NewJob` error it shuts the scheduler down before returning, so no goroutine remains. Stop is `ShutdownWithContext`.
- **Shared mode.** It only calls `NewJob` and returns a stop that calls `RemoveJob(id)`.
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
- [A hung check holding a slot delays others under `HealthCheckParallelism`] → Callers stop at their bounds; documented.
- [Readiness turns Unknown when a Starter is registered after Ready, until `Refresh`] → Intended (the service is not serving); documented on `Extend` and in the report godoc.
- [A report covering no service is Healthy, so a mistyped tag yields Healthy] → Use the exported constants; documented on `MatchHealthTags`.
- [Consumers of the gocron adapter get gocron's dependencies] → Only by importing the adapter; the root module is unchanged.
- [gocron's `ShutdownWithContext` waits up to its stop timeout for a running job] → The container cancels the round ctx before stop, and a round returns at its ctx.
- [goleak may briefly see a cancelled context-aware check exiting after Shutdown] → goleak retries; scenarios release every check.
- [The monitor mutex is on the report path] → Reads copy records and do no I/O while holding it; `BenchmarkHealthReport_Monitored` guards it.

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
- `health_report.go` → `health_report_test.go`: reports, classification, the gate.
- `health_monitor.go` → `health_monitor_test.go`: records, scheduling lifecycle, hooks.
- `scheduler/gocron/scheduler.go` → `scheduler/gocron/scheduler_test.go`, which has its own `TestMain` with goleak.

Conventions:
- Concurrency tests run under `-race` with `testWithTimeout`.
- Time uses `clockwork.NewFakeClock()` (`BlockUntilContext`) or `synctest.Test`.
- A `manualScheduler` test double (in `health_monitor_test.go`) implements `HealthScheduler` for the scheduling scenarios.

| Spec | Scenario | Test | Kind |
|---|---|---|---|
| health-check-execution | Each caller keeps its own bound when it joins a check | `TestHealthCheck_JoinerOwnBound/<row>` | table, fake clock |
| health-check-execution | One caller leaving does not cancel the check for the others | `TestHealthCheck_CallerLeaves` | race |
| health-check-execution | Deadline and values reach the check | `TestHealthCheck_StarterContext/<row>` | table |
| health-check-execution | The only caller leaving cancels the check | `TestHealthCheck_LastCallerCancels` | race |
| health-check-execution | A check that ignores its context does not pile up | `TestHealthCheck_NoPileUp/<row>` (regression D1) | table, race, goleak |
| health-check-execution | Concurrent forced calls share the in-flight checks | `TestHealthCheck_ConcurrentShareInFlight` | race |
| health-check-execution | A call that joins a running check gets its result | `TestHealthCheck_JoinRunning` | race |
| health-check-execution | An abandoned check is never joined | `TestHealthCheck_AbandonedNotJoined` | race |
| health-check-execution | Sequential calls check again | `TestHealthCheck_SequentialCheckAgain/<row>` | table |
| health-check-execution | Parallelism caps running checks | `TestHealthCheck_ParallelismCap/<row>` | table, race |
| health-check-execution | Waiting for a slot honors the context | `TestHealthCheck_SlotWaitHonorsContext` (regression D2) | race |
| health-check-execution | Registration is not blocked by checks waiting for a slot | `TestHealthCheck_SlotWaitDoesNotBlockProvide` (regression D2) | race |
| health-check-execution | A report-path check's timeout does not count the wait for a slot | `TestHealthReport_BoundStartsAtSlot` | fake clock |
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
| gocron-health-scheduler | A schedule gocron rejects | `TestScheduler_RejectedJob` | goleak |
| gocron-health-scheduler | Shared scheduler | `TestScheduler_Shared` | unit |
| gocron-health-scheduler | Clean stop | `TestScheduler_CleanStop/<row>` | table, goleak |

**Hot paths and their tests:**

| Hot path | Tests |
|---|---|
| Forced run (`HealthCheckWithContext`, root with extension scopes) | `TestHealthCheck_ConcurrentShareInFlight`, `_MapShape`, `_EachCheckOnce`, `_CallerLeaves`, the existing `healthcheck_timeout_test.go` and extension suites |
| Single-service functions | `TestHealthCheck_NoPileUp/HealthCheckNamed`, `_JoinerOwnBound`, `_JoinRunning`, `_SingleTakesOverQueued` |
| Shared call: join, leave, bound, abandonment | `_JoinerOwnBound`, `_LastCallerCancels`, `_AbandonedNotJoined`, `TestHealthReport_LateResultDiscarded`, `_AbandonedNotRecorded` |
| Limiter | `_ParallelismCap`, `_SlotWait*`, `_NoQueueLimit`, `TestHealthReport_BoundStartsAtSlot` |
| Listing services | `TestServiceLazy_HealthCheckDuringBuild`, `TestHealthCheck_EachCheckOnce`, `TestHealthReport_Coverage`, `_Membership` |
| Report read: monitored, on-demand, gate | `TestHealthReport_*`, plus `BenchmarkHealthReport_Monitored` (1,000 services) |
| Scheduler lifecycle: Ready, failure, stop, clone | `TestHealthScheduler_*`, `TestHealthRound_*` |
| Hooks | `TestHook*HealthChanged*`, `TestHealthHooks_*` |
| gocron adapter | `scheduler/gocron` suite under `-race` with goleak |

**Docs:**
- `ExampleScope_HealthReport`: an idle container, on-demand reads with the TTL.
- `ExampleRootScope_HealthReport`: a started container; readiness during Closing via the gate; `IgnoreContainerPhase`.
- `ExampleMatchHealthTags`: readiness and liveness, with an optional service.
- `ExampleRootScope_AddServiceHealthChangedHook`.
- `scheduler/gocron`: `ExampleNew` (default) and `ExampleWithJobDefinition` (cron).

## Migration Plan

1. `main` (with #8, #11, #12, #14, #15) is already the base: the branch was rebased on `76404ef`. Task group 1 re-confirms names.
2. Implement the task groups in order. Each defect fix starts with its regression test (D1–D9); the probe file from the audit re-verification seeds the RED tests.
3. Release notes list the flagged behavior changes, the phase gate, and the opt-in scheduler with the gocron adapter.
4. Rollback: revert the PR. The gocron module is separate and can be unpublished independently.

## Open Questions

- The JSON shape of `HealthReport` (including `Err` and `Phase`) is deferred to the HTTP-handler follow-up.
- Release tagging of the `scheduler/gocron` module (its own version tags, like `http/*`) follows the existing workspace-module convention; to confirm at release.
