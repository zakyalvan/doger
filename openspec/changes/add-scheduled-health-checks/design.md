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

  An error or a recovered panic is logged (`DI: health scheduler failed: ...`) or passed to `HookContainerPanic(root, "healthscheduler", v)`. The container stays unmonitored and on-demand reads keep working. The state update and the closing of the "starting" signal run in a `defer`, so a panic `HookContainerPanic` raises propagates from Start and leaves scheduling failed; a later Shutdown does not wait for a schedule call that will never finish (review R3).
- **Stop.** Stopping comes first in `shutdownAsWinner`, and on the root-`*Scope` bypass path. Under the mutex the container sets `closing`, so nothing is recorded and no hook fires. It then cancels the monitor context, which cancels the rounds in progress, and calls stop with `context.WithoutCancel(shutdownCtx)`. That way an ended Shutdown context (shutdown-context, #14) cannot make Shutdown return before the scheduler's goroutines exit. Shutdown waits for stop. A stop error is logged; a stop panic is recovered and passed to `HookContainerPanic(root, "healthscheduler", v)` when set, logged otherwise (review R8). If a schedule call is still in progress, Shutdown waits for it, then calls its stop. The checks in flight have their contexts cancelled but are not waited for: a check may still run while its service shuts down (CR4, flagged behavior change 3).
- **Rejected:**
  - A built-in timer loop in the root module. It is default-on in the first version and was rejected in the audit: it goes against the prior art, leaks in every `Start`-without-`Shutdown` test, and the TTL cache already solves load amplification. The user prefers gocron.
  - Importing gocron in the root module: forced dependencies on every consumer.
  - Configuring the scheduler through an `Extension`: an extension registers services into a scope, while scheduling is container infrastructure that must be known at Ready.

**H2. One monitor per root, keyed by service wrapper.**
- **State.** A `healthMonitor` on `RootScope` holds, under one mutex: `records`, `inflight`, the transition queue, the scheduling state (`idle`/`starting`/`scheduled`/`stopped`) and the stop function, all keyed by wrapper. A wrapper is unique per registration, so an Override or a re-registration starts a fresh record.
- **Pruning.** A record is pruned when its wrapper is removed: through `RootScope.forget`, which the registration-replacement and scope-shutdown paths already call once their scope lock is released, and as a fallback when a read or round lists services. `forget` also marks the service's call in flight as left (H9). Pruning is never done under a scope lock (the earlier extra call in `storeLocked` is removed: CR10).
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
- **What is recorded.** A completed check is recorded unless it was abandoned, or a report path already recorded its timeout (the late result is discarded, so a slow check does not flap: audit item 9). Nothing is recorded once `closing` is set, nor for a call marked left because its service left the container while it ran: such a call starts no fresh call for its `next` waiters, which get its result (review R1). A report-path waiter that reaches its bound while waiting in `next` for a call it could not join records its timeout too (review R2).
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
- **Shared mode.** It only calls `NewJob`, wrapping the round to count the rounds of this job in progress, and returns a stop that calls `RemoveJob(id)`, treats `gocron.ErrJobNotFound` as removed (the owner may have shut its scheduler down first: review R5), then waits within ctx until no round of this job runs (review R6).
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
- `health_report.go` → `health_report_test.go`: reports, classification, the gate, including monitored reads, recording and `BenchmarkHealthReport_Monitored` (moved from `health_monitor_test.go`: S1).
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
| health-check-execution | A panic HookContainerPanic raises reaches the waiting callers | `TestHealthCheck_HookPanicPropagates/<row>` (review R4) | table |
| health-reports | A wait that times out on an abandoned check records the timeout | `TestHealthReport_TimeoutWhileAbandoned/<row>` (review R2) | table |
| health-reports | A check in flight when its service leaves is not recorded | `TestHealthReport_NotRecordedAfterLeaving/<row>` (review R1) | table |
| health-check-scheduling | A re-panicking HookContainerPanic leaves scheduling stopped | `TestHealthScheduler_FailurePanicHook` (review R3) | unit |
| health-check-scheduling | A failing stop does not fail Shutdown | `TestHealthScheduler_StopFailure/<row>` (review R8) | table |
| health-check-scheduling | A service appearing changes the overall state at the next round | `TestHookContainerHealthChanged_Appearing/<row>` (W3) | table |
| gocron-health-scheduler | Stopping a shared schedule | `TestScheduler_SharedStop/<row>` (review R5, R6) | table |

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
| Container state per result (review R7) | `TestHealthRound_AllocsLinear`: allocations of a round at 1,000 services stay under 2.5× those at 500 (they were 3.85×), plus `BenchmarkHealthRound` (1,000 services) |
| Logging on cached reads (CR8) | `TestHealthReport_CachedReadsLogNothing` |
| gocron adapter | `scheduler/gocron` suite under `-race` with goleak |

**Benchmark** (`go test -run '^$' -bench BenchmarkHealthReport_Monitored -benchmem -count=3 .`, Go 1.27.3, Apple M4 Pro, 2026-10-28): a monitored read of 1,000 recorded services takes 736–800 µs/op, 1.06 MB/op and 9,053 allocs/op, and runs no check. Listing the services and building the entries dominate; the monitor mutex is held only to copy records.

`BenchmarkHealthRound` (`go test -run '^$' -bench 'BenchmarkHealthRound$' -benchmem -count=3 .`, same machine, 2026-10-28, after review R7's fix): a round re-recording 1,000 results takes 3.01–3.06 ms/op, 2.80 MB/op and 33,085 allocs/op (R7 measured 351 ms and 1.04M allocations before). The same run re-measured `BenchmarkHealthReport_Monitored` at 693–753 µs/op, 0.88 MB/op and 8,052 allocs/op.

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
- 22.2: adding scheduler/gocron to go.work required rows in the existing guard tests TestToolchain_GoDirective and TestModuleIdentity_WorkspaceModules; release.yml pins and tags scheduler/* like http/*.

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

## Migration Plan

1. `main` (with #8, #11, #12, #14, #15) is already the base: the branch was rebased on `76404ef`. Task group 1 re-confirms names.
2. Implement the task groups in order. Each defect fix starts with its regression test (D1–D9); the probe file from the audit re-verification seeds the RED tests.
3. Release notes list the flagged behavior changes, the phase gate, and the opt-in scheduler with the gocron adapter.
4. Rollback: revert the PR. The gocron module is separate and can be unpublished independently.

## Open Questions

- The JSON shape of `HealthReport` (including `Err` and `Phase`) is deferred to the HTTP-handler follow-up.
- Release tagging of the `scheduler/gocron` module (its own version tags, like `http/*`) follows the existing workspace-module convention; to confirm at release.
