# Design

## Context

See proposal.md (Why, Defect) for the motivation and the reproduced defects. The current design, read at `2012c60` (PR #8 head):

- **Container-wide runs.** `Scope.HealthCheckWithContext` (scope.go) applies `HealthCheckGlobalTimeout`, then `asyncHealthCheckWithContext` asks `RootScope.queueServiceHealthcheck` for one result channel per service in the scope and its ancestors.
  - Without a pool, each service gets its own goroutine and `awaitHealthCheck` (utils.go).
  - With `HealthCheckParallelism > 0`, the work goes through `jobPool`, which has a fixed 1,000-slot channel and workers started in `NewWithOpts`.
- **Single-service functions.** `HealthCheckNamedWithContext` (di_lifecycle.go) runs the check on the caller's goroutine through `raceWithTimeout`. Only when ctx has a deadline does it use a goroutine.
- **Lazy services.** `serviceLazy.healthcheck` holds the wrapper's read lock while the check runs. **Aliases.** `serviceAlias.healthcheck` calls the target's check.
- **Lifecycle.** PR #8 adds the lifecycle this design hooks into:
  - `startPhase`/`closePhase` under `lifecycleMu`.
  - `markReady` (root_start.go) moves started to ready, before the Ready hooks.
  - `shutdownAsWinner` (root_scope.go) waits for a running start, runs the Closing hooks, then shuts the services down.
  - `HookContainerPanic` and `RootScope.Phase()`.
- **Package init.** `DefaultRootScope = New()` runs at package init, so `New()` must stay goroutine-free.
- **Constraints:**
  - No new module dependency.
  - The injector clock (clockwork) for every time read.
  - goleak in `TestMain`.
  - A fake-clock context's `Err()` blocks while it is live (`contextDone` in utils.go).

## Prior art

The approach follows [alexliesenfeld/health](https://github.com/alexliesenfeld/health) (v0.8.1, [config.go](https://github.com/alexliesenfeld/health/blob/v0.8.1/config.go), [check.go](https://github.com/alexliesenfeld/health/blob/v0.8.1/check.go)).

| alexliesenfeld/health | Here | Why it differs |
|---|---|---|
| `WithPeriodicCheck(refreshPeriod, initialDelay, check)`: background checks; `Check()` returns their last result | Scheduled rounds; `HealthReport()` on a monitored container never runs a check | Same idea |
| `StatusUnknown` before the first result | `HealthUnknown` with `PendingSince` | Same idea |
| `WithCacheDuration` (default 1s) for on-request checks | `HealthReportCacheTTL` (default 1s) for on-demand reads of an unmonitored container | Same default |
| `WithStatusListener` (aggregate) and `Check.StatusListener` (per check), called synchronously | `HookContainerHealthChanged` and `HookServiceHealthChanged`, called synchronously | Hook lists follow the container's existing hook style |
| Panics recovered unless `DisablePanicRecovery` | Always recovered, reported to `HookContainerPanic` | Consumers re-raise from the hook if they want to; a background crash is never a sensible default |
| `NewChecker` autostarts unless `WithDisabledAutostart` | Starts at `PhaseReady`, stops at root Shutdown | A DI container already owns a lifecycle, and `DefaultRootScope` rules out starting at construction |
| `Check()` holds one mutex for a whole run, so concurrent callers queue | Concurrent callers share one run, and each waits on its own context | Callers are not serialized behind a slow check |
| (not in alexliesenfeld) | Tags via `WithHealthTags`, with predefined `HealthTagReadiness`/`HealthTagLiveness`, and `MatchHealthTags` report filters | Taken from .NET health-check tags (`HealthCheckRegistration.Tags` plus a predicate on the endpoint), so the Kubernetes probes work out of the box and consumers can add their own groups |
| (not in alexliesenfeld) | `AsOptionalHealth()` turns a failure into `HealthDegraded` | Taken from .NET's `HealthStatus.Degraded` and the per-registration failure status. Chosen over go-health's `Fatal` flag, which hides a failing non-critical service from the overall state |
| Global timeout 10s by default | Scheduled checks default to 10s; forced runs and on-demand reads stay unbounded unless configured | Forced paths keep today's semantics (user decision) |

Other references checked: Kubernetes probes (period 10s, timeout 1s), .NET `HealthCheckPublisherOptions` (delay 5s, period 30s), and Spring Boot Actuator (on-request, cache TTL 0 by default). Spring's and Kubernetes' guidance that liveness must not check external dependencies applies to the follow-up HTTP handler.

## Goals / Non-Goals

**Goals:**
- Reading health costs the same for 1 reader and for 1,000.
- At most one goroutine per service runs its check, whatever the callers do.
- No goroutine exists for health checks until a container reaches Ready, and none remains after its Shutdown, except checks that ignore their context.
- Every opinionated choice can be overridden through `InjectorOpts`.

**Non-Goals:**
- The HTTP handler (follow-up change).
- JSON marshalling of `HealthReport`. `Err` is an `error`, and the follow-up decides its wire shape.
- Per-service intervals or a per-service timeout option.
- Failure thresholds (`MaxContiguousFails` in alexliesenfeld). A consumer can derive them from the hooks.
- Spring-style central group definitions (groups configured by service name in `InjectorOpts`). Tags are declared at registration instead (H15).
- A startup probe tag. Consumers can define one as a custom tag; `PhaseReady` already says when start finished.

## Decisions

**H1. The scheduler belongs to the container lifecycle.**
- **Start.** It starts in the Ready transition. `markReady` starts it under `lifecycleMu`, only when `closePhase == closeOpen` and the interval is not negative, then the Ready hooks run. A Ready hook can therefore read a report: it shows Monitored, with the first round pending.
- **Stop.** It stops first thing in `shutdownAsWinner`, before `waitForStart`'s result is used and before the Closing hooks. Stopping sets a stopped flag under `lifecycleMu`, cancels the monitor context, and waits for the scheduler goroutine.
- **Why lifecycleMu.** Both steps take it, so a start finishing during a Shutdown never starts a scheduler after the close began.
- **Alternatives rejected:**
  - Start in `New()`: leaks via `DefaultRootScope`.
  - Start lazily on the first report read: leaks in tests that read without Shutdown.
  - Start at `Start()` entry: checks would run against services still starting.

**H2. One monitor per root, keyed by service wrapper.**
- **State.** A `healthMonitor` on `RootScope` holds two maps under one mutex, both keyed by the wrapper value. A wrapper is unique per registration, so an Override or a re-registration starts a fresh record.
  - `records`: scope, name, last result, checked-at, duration.
  - `inflight`: the check now running.
- **Pruning.** Records for wrappers no longer registered are pruned when a round is dispatched or a report is built.
- **Alternative rejected:** keying by name. It collides across scopes and keeps a stale result across an Override.

**H3. An in-flight check is a shared call with reference-counted cancellation.**
- **Shape.** A small internal `sharedCall` holds a done channel, a result, a waiter count and a cancel function. The first caller starts it on a new goroutine. Later callers join by incrementing the count.
- **Waiting.** Each waiter waits on `done` or on its own context.
  - A waiter that leaves decrements the count. At zero, the check's context is cancelled, and the result is marked abandoned so it is not recorded (health-reports, "Every completed check refreshes the report").
  - The scheduler holds a pinned reference, released only when the check returns or the monitor stops.
- **The check's context:**
  - It is `context.WithoutCancel` of the starter's context, so the starter's values (for example trace IDs) are kept.
  - It has its own cancel, plus the starter's bound when there is one. The bound is recorded with `withHealthCheckDeadline`, so `healthCheckResult` classifies timeouts exactly as today.
- **Why not `x/sync/singleflight`.** Its function takes no context, and a waiter leaving cannot cancel the shared call. It would also add a module dependency.
- **Effect.** This caps goroutines at one per service (D1) and gives each caller its own deadline.

**H4. Forced runs coalesce per scope, on top of per-service sharing.**
- Per-service sharing alone does not make 50 callers cost one run: a caller arriving after the fast checks finished would run them again.
- So `HealthCheckWithContext` also joins an in-flight run of the same scope (a `sharedCall` keyed by scope ID). The run holds a partial result map. A caller leaving early copies the finished entries and fills the rest with `timeoutError(ctx.Err())`.
- **The run's context:**
  - Each service check gets the run's context as its starter context.
  - `HealthCheckGlobalTimeout` bounds it from the run's start.
  - It is cancelled when its last caller leaves.
- **Alternative rejected:** a minimum refresh interval for forced runs (user decision Q3: coalesce only).

**H5. One enumeration step serves every path.**
- `visibleCheckers(scope)` copies the services under each scope's read lock and releases the lock before any check starts. That fixes D2's lock problem.
- It returns, for each name, the wrapper to check:
  - The nearest scope wins, so a shadowed ancestor is skipped (D4).
  - An alias resolves to its target's wrapper and reuses the target's shared call and result (D5).
  - A non-checker is kept as a nil entry for the map, so the `HealthCheck()` shape is unchanged.
- `containerCheckers(root)` walks every scope for rounds and the root report. Shadowing does not apply across sibling scopes, so each scope's services are kept.

**H6. A context-aware limiter replaces `jobPool`.**
- **Shape.** `HealthCheckParallelism > 0` creates a `chan struct{}` of that size in `NewWithOpts`, and no goroutine. A check goroutine acquires a slot by selecting on the channel and on its check context, before calling the check, and releases the slot when the check returns.
- **What this fixes.** No queue limit and no send under a lock (D2). No goroutines in `New()` (D6). The single-service functions now use the limit too: that is a behavior change, flagged in the proposal.
- **Removed.** `jobPool`, `newJobPool` and `RootScope.healthCheckPool`, all unexported.
- **Alternative rejected:** `errgroup.SetLimit`. It needs `x/sync` and has no per-acquire context.

**H7. Panics are recovered in the check goroutine.**
- **Error.** `ErrHealthCheckPanic = errors.New("DI: health check panic")`.
  - An error value yields `fmt.Errorf("%w: %w", ErrHealthCheckPanic, v)`.
  - A non-error value yields the same with `fmt.Errorf("%v", v)`.
  - A nil panic is `*runtime.PanicNilError`, as recovered (provider-panic-handling uses the same rule).
- **Hook.** When `HookContainerPanic` is set, it is called with `(root, "healthcheck", value)`, on the check goroutine. A panic in that hook is logged.
- **Paths covered.** Single-service functions used to propagate a panic to their caller; they now return the error, which is flagged.

**H8. A lazy service's check runs outside its lock.** `serviceLazy.healthcheck` copies `built` and `instance` under the read lock, releases it, and then calls the check (D3). A check may therefore run while the service shuts down, as eager services already can.

**H9. Fixed-delay ticks on the injector clock.**
- **Loop.** The scheduler goroutine:
  1. dispatches a round;
  2. calls `clock.NewTimer(interval)`;
  3. selects on the timer and on the monitor context.
- **Dispatch.** For each checker of the container without an in-flight check, it starts one with the scheduled bound. It does not wait for the checks.
- **Effect.** The interval is measured from the start of the previous round. A clock jump fires the timer once, so missed rounds are dropped. Rounds never overlap, because a service with a check still in flight is skipped.
- **Logging.** Each round logs one `Logf` line: `DI: health check round`.
- **Alternatives rejected:**
  - A `Ticker`, which replays missed ticks on some clocks.
  - Waiting for the round: a hung, unbounded check would stall the scheduler.

**H10. The scheduled-check bound comes from `HealthCheckTimeout`.**
- `HealthCheckTimeout > 0` uses it. `0` uses `DefaultScheduledHealthCheckTimeout` (10s). `< 0` means unbounded.
- This needs no extra option. A negative value already meant "no timeout" for forced runs, and it now means the same for scheduled checks.
- PR #8's pending single-service timeout option governs the single-service functions only. Task group 1 reconciles the name once #8 merges.

**H11. Reports.**
- **What a read does.** `HealthReportWithContext` reads `Phase()` and the monitor state:
  - Monitored (scheduler running): it builds the report from the records under the monitor mutex.
  - Idle or Failed, with scheduling on: for each covered checker whose record is missing or older than the TTL, it joins or starts a shared check. Waiting is bounded only by ctx (user decision), with checks bounded by `HealthCheckTimeout > 0` when set.
  - Any other state: it returns the records with no checks.
- **Coalescing.** Concurrent readers coalesce through per-service sharing. No run-level coalescing is needed, because the TTL makes a late reader reuse finished results.
- **Abandoned entries.** Entries a reader abandoned show `HealthUnhealthy` with `timeoutError(ctx.Err())` in that reader's report only.
- **Ordering.** Scope depth, scope name, service name. Scope IDs are random, so they cannot order the entries.

**H12. Hooks are delivered synchronously, serialized, in recording order.**
- A check goroutine records its result in this order:
  1. take `hookMu`, then the monitor mutex;
  2. update the record and compute the service and overall transitions;
  3. release the monitor mutex;
  4. call the service hooks, then the container hooks;
  5. release `hookMu`;
  6. close `done`.
- **Effect.** A caller sees a result only after its hooks ran. Hooks never run two at once.
- **Panics.** Each hook call recovers its panic into `HookContainerPanic(root, "healthchanged", v)` or `Logf`, and never re-raises it. This is not the Start/Shutdown `dispatchFirstPanic` path, because no caller is there to receive the panic.

**H13. API placement.** `HealthReport` and `HealthReportWithContext` are added to the `Injector` interface, implemented by `*Scope`, `*RootScope` (whole container) and `*virtualScope` (delegates). `Injector` has unexported methods, so no type outside the package can implement it, and adding methods breaks no consumer.

**H14. Doc correction (D8).** The `RootScope.HealthCheck` doc comment is corrected to say "root scope's services". Behavior is unchanged (user decision Q6).

**H15. Classification is a registration option; filtering is a report option.**
- **Declaration.** `WithHealthTags(tags...)` and `AsOptionalHealth()` are `ProvideOption` constructors. They write two fields of the registration's option struct: a tag set (nil means the default `{readiness}`; a non-nil empty set means no tag) and an `optional` flag.
- **Validation** at registration, before the service is stored, like the existing `AsStarter()` checks: an empty tag panics, and so does either option on a transient registration.
- **Propagation.** The fields follow the existing option rules: Clone copies them, Override replaces them, an alias has none of its own. `visibleCheckers` and `containerCheckers` carry them with each entry, an alias taking its target's.
- **Severity.** It is applied when a result is recorded: a failing optional check is stored as `HealthDegraded`. Forced paths return the raw error and never look at severity.
- **Filtering.** `HealthReportOption` is an opaque functional option. `MatchHealthTags(tags...)` adds tags to an any-match set, and an empty set means no filter. A report filters its covered entries before it computes the overall state and before it picks stale services for on-demand checks, so a liveness read on an idle container runs only liveness checks.
- **Overall state.** Unhealthy, else Degraded, else Healthy when any service is healthy or none is covered, else Unknown; a closed container reports Unknown. An empty coverage counts as healthy so that a zero-config liveness report is healthy from Ready on, before the first round.
- **Alternatives rejected** (user decisions):
  - Fixed probe kinds only: consumers could not add groups.
  - Spring-style groups in `InjectorOpts`: stringly-typed by service name, so a rename breaks them silently.
  - An instance interface: it couples service types to doger and cannot classify third-party types.

## Risks / Trade-offs

- [Checks now run every 60s by default; this costs money or has side effects for some consumers] → Flagged in the proposal and the release notes. `HealthCheckInterval: -1` opts out.
- [Tests that call `Start` without `Shutdown` now leak the scheduler] → Flagged; the doc on `Start` says Shutdown stops the scheduler. Our own suite and `examples/*` are audited in task 16.3.
- [A slow hook delays the check's result for every waiter] → Documented on both hook fields: a hook must be fast and must hand off slow work.
- [A check that ignores its context stays in flight forever] → It stays at one goroutine. Readers see `PendingSince` and the last result. Forced callers get timeouts from their own bounds.
- [Joining a check started by the scheduler gives a forced caller the scheduled 10s bound's result] → Documented: a caller that joins an in-flight check gets that check's result.
- [goleak may briefly see a cancelled context-aware check exiting after Shutdown] → goleak retries by default. Scenarios release or cancel every check before the test ends.
- [Fake-clock contexts block on `Err()`] → Keep using `contextDone`. Scheduled-check tests use checks that wait on `ctx.Done()`, as the `Clock` doc already requires.
- [A report that covers no service is Healthy, so a mistyped tag in `MatchHealthTags` yields a healthy report] → Use the exported tag constants; the godoc of `MatchHealthTags` states the rule. The rule is what makes a zero-config liveness report healthy.
- [The monitor mutex is on the report path] → Reads copy records under the mutex and do no I/O. A benchmark guards it (task 22.1).

## Public API & Defaults

New, all additive:

```go
// injector.go, InjectorOpts
HealthCheckInterval        time.Duration // 0: DefaultHealthCheckInterval (60s); <0: off; >0: interval
HealthReportCacheTTL       time.Duration // 0: 1s; <0: no cache; >0: TTL (unmonitored Idle/Failed reads only)
HookServiceHealthChanged   []func(scope *Scope, serviceName string, previous, current ServiceHealth)
HookContainerHealthChanged []func(root *RootScope, previous, current HealthState)

// options.go, ProvideOption constructors
func WithHealthTags(tags ...string) ProvideOption // replaces the default tags; repeats accumulate
func AsOptionalHealth() ProvideOption             // failures are HealthDegraded
const HealthTagReadiness = "readiness"
const HealthTagLiveness = "liveness"

type HealthReportOption struct{ /* opaque */ }
func MatchHealthTags(tags ...string) HealthReportOption

const DefaultHealthCheckInterval = 60 * time.Second
const DefaultScheduledHealthCheckTimeout = 10 * time.Second
const DefaultHealthReportCacheTTL = time.Second

var ErrHealthCheckPanic = errors.New("DI: health check panic")

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
    Monitored bool
    CheckedAt time.Time
    Services  []ServiceHealth
}

func (s *Scope) HealthReport(opts ...HealthReportOption) HealthReport
func (s *Scope) HealthReportWithContext(ctx context.Context, opts ...HealthReportOption) HealthReport
func (s *RootScope) HealthReport(opts ...HealthReportOption) HealthReport // whole container
func (s *RootScope) HealthReportWithContext(ctx context.Context, opts ...HealthReportOption) HealthReport
func (s *RootScope) AddServiceHealthChangedHook(hook func(*Scope, string, ServiceHealth, ServiceHealth))
func (s *RootScope) AddContainerHealthChangedHook(hook func(*RootScope, HealthState, HealthState))
// Injector interface gains HealthReport and HealthReportWithContext (H13).
```

`InjectorOpts.copy()` copies the new fields and clones the hook slices, as it does for the existing hooks. Every default and its override is in the proposal's Defaults & Control table.

**Breaking changes:** none to signatures. The behavior changes are flagged in the proposal: scheduled checks by default, coalesced forced runs, panics returned as errors, and parallelism applied to single-service functions. Adding `InjectorOpts` fields breaks only unkeyed literals, which Go 1 compatibility excludes. **Migration:** consumers who want today's behavior set `HealthCheckInterval: -1`. A consumer that relied on a health-check panic crashing must re-raise it from `HookContainerPanic`.

## Test Strategy

New test files follow the new source files: `health_report.go` → `health_report_test.go`, `health_monitor.go` → `health_monitor_test.go`, `health_run.go` → `health_run_test.go`. Concurrency tests run under `-race` with `testWithTimeout`. Time-based tests use `clockwork.NewFakeClock()` (with `BlockUntilContext` to sequence timers) or `synctest.Test`. goleak runs in `TestMain`.

| Spec | Scenario | Test | Kind |
|---|---|---|---|
| health-check-scheduling | Scheduler starts only at Ready | `health_monitor_test.go: TestHealthMonitor_StartsAtReady/<row>` | table, fake clock |
| health-check-scheduling | A container that is never started passes goleak | `health_monitor_test.go: TestHealthMonitor_NeverStartedNoGoroutine` (goleak.VerifyNone) | goleak |
| health-check-scheduling | Rounds follow the interval on the fake clock | `health_monitor_test.go: TestHealthMonitor_Interval/<row>` | table, fake clock |
| health-check-scheduling | Consecutive advances run one round each | `health_monitor_test.go: TestHealthMonitor_ConsecutiveRounds` | fake clock |
| health-check-scheduling | A clock jump runs at most one round | `health_monitor_test.go: TestHealthMonitor_ClockJump/<row>` | table, fake clock |
| health-check-scheduling | Services that appear later join the next round | `health_monitor_test.go: TestHealthMonitor_LateServices/<row>` | table, fake clock |
| health-check-scheduling | Removed and replaced services leave the next round | `health_monitor_test.go: TestHealthMonitor_RemovedServices/<row>` | table, fake clock |
| health-check-scheduling | Scheduled check bounds | `health_monitor_test.go: TestHealthMonitor_ScheduledTimeout/<row>` | table, fake clock |
| health-check-scheduling | Forced runs keep their unbounded default | `healthcheck_timeout_test.go: TestHealthCheck_ForcedUnboundedByDefault` | fake clock, regression |
| health-check-scheduling | No round after shutdown begins | `health_monitor_test.go: TestHealthMonitor_StopsBeforeClosing` | fake clock, goleak |
| health-check-scheduling | Shutdown does not wait for a check that ignores its context | `health_monitor_test.go: TestHealthMonitor_ShutdownSkipsHungCheck` | race, timeout |
| health-check-scheduling | A context-aware check in flight is cancelled at shutdown | `health_monitor_test.go: TestHealthMonitor_ShutdownCancelsCheck` | race |
| health-check-scheduling | Clone scheduling follows the clone's lifecycle | `health_monitor_test.go: TestHealthMonitor_Clone/<row>` | table |
| health-check-scheduling | The source keeps scheduling after its clone shuts down | `health_monitor_test.go: TestHealthMonitor_CloneShutdownKeepsSource` | fake clock |
| health-check-scheduling | Service hook calls follow state changes | `health_monitor_test.go: TestHookServiceHealthChanged/<row>` | table, fake clock |
| health-check-scheduling | A forced run records changes too | `health_monitor_test.go: TestHookServiceHealthChanged_ForcedRun` | unit |
| health-check-scheduling | Hooks run one at a time, in order of addition | `health_monitor_test.go: TestHookServiceHealthChanged_Serialized` | race |
| health-check-scheduling | Container hook calls follow the overall state | `health_monitor_test.go: TestHookContainerHealthChanged/<row>` | table, fake clock |
| health-check-scheduling | Hook panic is contained | `health_monitor_test.go: TestHealthHooks_Panic/<row>` | table |
| health-classification | Classification of a registration | `health_report_test.go: TestHealthClassification_Registration/<row>` | table |
| health-classification | Predefined tag values | `health_report_test.go: TestHealthTags_Constants/<row>` | table |
| health-classification | Invalid classification | `options_test.go: TestHealthOptions_InvalidPanics/<row>` | table |
| health-classification | Classification across clone, override and alias | `health_report_test.go: TestHealthClassification_CloneOverrideAlias/<row>` | table |
| health-classification | Filtered reports | `health_report_test.go: TestHealthReport_MatchTags/<row>` | table |
| health-classification | A filtered on-demand read checks only the covered services | `health_report_test.go: TestHealthReport_MatchTagsOnDemand` | unit |
| health-classification | Degraded entries | `health_report_test.go: TestHealthReport_Degraded/<row>` | table |
| health-classification | Degraded transitions reach the hooks | `health_monitor_test.go: TestHealthHooks_Degraded` | fake clock |
| health-classification | Zero-config liveness | `health_report_test.go: TestHealthReport_LivenessZeroConfig/<row>` | table |
| health-classification | Forced results ignore classification | `health_run_test.go: TestHealthCheck_IgnoresClassification/<row>` | table |
| health-reports | Report entries after one round | `health_report_test.go: TestHealthReport_Entries/<row>` | table, fake clock |
| health-reports | Entries are ordered by scope then name | `health_report_test.go: TestHealthReport_Order` | unit |
| health-reports | Each report is the caller's own copy | `health_report_test.go: TestHealthReport_Isolated` | unit |
| health-reports | Report membership | `health_report_test.go: TestHealthReport_Membership/<row>` | table |
| health-reports | Report coverage by scope | `health_report_test.go: TestHealthReport_Coverage/<row>` | table |
| health-reports | Overall state | `health_report_test.go: TestHealthReport_OverallState/<row>` | table |
| health-reports | HealthState names | `health_report_test.go: TestHealthState_String/<row>` | table |
| health-reports | Reads on a monitored container cost nothing | `health_report_test.go: TestHealthReport_MonitoredNoChecks` | race |
| health-reports | Before the first result | `health_report_test.go: TestHealthReport_BeforeFirstResult` | fake clock |
| health-reports | On-demand reads honor the cache TTL | `health_report_test.go: TestHealthReport_OnDemandTTL/<row>` | table, fake clock |
| health-reports | Concurrent on-demand reads share one check | `health_report_test.go: TestHealthReport_OnDemandShared` | race |
| health-reports | On-demand reads by phase | `health_report_test.go: TestHealthReport_OnDemandByPhase/<row>` | table |
| health-reports | Reads without on-demand checks return the last recorded results | `health_report_test.go: TestHealthReport_ClosingKeepsResults` | unit |
| health-reports | A closed container reports no service | `health_report_test.go: TestHealthReport_Closed` | unit |
| health-reports | A reader that stops waiting | `health_report_test.go: TestHealthReport_ReaderContext/<row>` | table, synctest |
| health-reports | A reader leaving does not affect other readers | `health_report_test.go: TestHealthReport_ReaderLeaves` | race |
| health-reports | Forced and single-service checks refresh the report | `health_report_test.go: TestHealthReport_RefreshedByForced/<row>` | table |
| health-reports | An abandoned check is not recorded | `health_report_test.go: TestHealthReport_AbandonedNotRecorded` | race |
| health-check-execution | Concurrent calls share the run | `health_run_test.go: TestHealthCheck_ConcurrentShareRun` | race |
| health-check-execution | Sequential calls run again | `health_run_test.go: TestHealthCheck_SequentialRunAgain/<row>` | table |
| health-check-execution | Runs on different scopes are separate | `health_run_test.go: TestHealthCheck_ScopesSeparate` | race |
| health-check-execution | One caller leaving does not cancel the check for the others | `health_run_test.go: TestHealthCheck_CallerLeaves` | race |
| health-check-execution | The only caller leaving cancels the check | `health_run_test.go: TestHealthCheck_LastCallerCancels` | race |
| health-check-execution | A check that ignores its context does not pile up | `health_run_test.go: TestHealthCheck_NoPileUp/<row>` (regression D1) | table, race |
| health-check-execution | A call that joins an in-flight check gets its result | `health_run_test.go: TestHealthCheck_JoinInFlight` | fake clock |
| health-check-execution | After a check finishes, the next call checks again | `health_run_test.go: TestHealthCheck_NextCallChecksAgain` | unit |
| health-check-execution | Parallelism caps running checks | `health_run_test.go: TestHealthCheck_ParallelismCap/<row>` | table, race |
| health-check-execution | Waiting for a slot honors the context | `health_run_test.go: TestHealthCheck_SlotWaitHonorsContext` (regression D2) | race |
| health-check-execution | Registration is not blocked by checks waiting for a slot | `health_run_test.go: TestHealthCheck_SlotWaitDoesNotBlockProvide` (regression D2) | race |
| health-check-execution | Many services with a small limit all complete | `health_run_test.go: TestHealthCheck_NoQueueLimit` | unit |
| health-check-execution | New starts no goroutine | `health_run_test.go: TestNew_NoHealthGoroutine/<row>` (regression D6) | table |
| health-check-execution | Panic results | `health_run_test.go: TestHealthCheck_Panic/<row>` (regression D7) | table |
| health-check-execution | A panic in a scheduled round does not stop scheduling | `health_monitor_test.go: TestHealthMonitor_PanicKeepsScheduling` | fake clock |
| health-check-execution | A check in flight does not delay the service | `service_lazy_test.go: TestServiceLazy_HealthCheckWithoutLock/<row>` (regression D3) | table, race |
| health-check-execution | Each underlying check runs once | `health_run_test.go: TestHealthCheck_ShadowAndAliasOnce/<row>` (regressions D4, D5) | table |
| health-check-execution | Map shape is unchanged | `health_run_test.go: TestHealthCheck_MapShape/<row>` | table |
| container-clock | Reported durations come from the clock | `clock_test.go: TestClock_ReportedDurations/<row>` (new row `health check`) | table |
| container-clock | Health scheduling and caching wait for the clock, not for wall time | `clock_test.go: TestClock_HealthSchedulingOnClock/<row>` | table, fake clock |
| container-clock | Timeout errors read the same on any clock | existing `clock_test.go` test, unchanged | existing |
| container-clock | Health-check timeouts wait for the clock, not for wall time | existing `clock_test.go` test, unchanged | existing |

**Hot paths and their tests:**

| Hot path | Tests |
|---|---|
| Forced run (`HealthCheckWithContext`) | `TestHealthCheck_ConcurrentShareRun`, `_CallerLeaves`, `_LastCallerCancels`, `_MapShape`, the existing `healthcheck_timeout_test.go` suite |
| Single-service functions | `TestHealthCheck_NoPileUp/HealthCheckNamed`, `_JoinInFlight`, `_NextCallChecksAgain`, `TestHealthCheck_Panic`, existing per-service tests |
| Shared call: join, leave, cancel at zero, abandoned result | `TestHealthCheck_CallerLeaves`, `_LastCallerCancels`, `TestHealthReport_AbandonedNotRecorded` |
| Limiter | `TestHealthCheck_ParallelismCap`, `_SlotWaitHonorsContext`, `_SlotWaitDoesNotBlockProvide`, `_NoQueueLimit` |
| Scheduler loop: start, tick, jump, stop | `TestHealthMonitor_*` |
| Report read: monitored and on-demand | `TestHealthReport_*`, plus `BenchmarkHealthReport_Monitored` (1,000 services) to show a read does no I/O and stays allocation-bounded |
| Classification and filtering | `TestHealthClassification_*`, `TestHealthReport_MatchTags*`, `TestHealthReport_Degraded`, `TestHealthReport_LivenessZeroConfig` |
| Hooks | `TestHookServiceHealthChanged*`, `TestHookContainerHealthChanged`, `TestHealthHooks_Panic` |
| Lifecycle edges: shutdown during start, clone | `TestHealthMonitor_StartsAtReady/shutdown during start`, `TestHealthMonitor_Clone*` |

**Docs:** `ExampleRootScope_HealthReport` shows the default, opt-out and TTL behavior. `ExampleMatchHealthTags` shows readiness and liveness reports with an optional service. `ExampleRootScope_AddServiceHealthChangedHook` shows a hook. The godoc on every new option states its default and override.

## Migration Plan

1. Wait for PR #8 to merge. Then merge `main` into this change's implementation branch and reconcile names with #8's final API: the phase names, `HookContainerPanic`'s signature, and the single-service timeout option (task group 1).
2. Implement the task groups in order. Each defect fix starts with its regression test (proposal Defect D1–D8).
3. Release notes list the five flagged behavior changes and the `HealthCheckInterval: -1` opt-out.
4. Rollback: revert the PR. Consumers can also turn scheduling off without a code rollback.

## Open Questions

- The JSON shape of `HealthReport`, including how `Err` is rendered. It is deferred to the HTTP-handler follow-up; it does not change this change's specs or tasks.
