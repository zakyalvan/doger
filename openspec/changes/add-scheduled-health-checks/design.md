# Design

## Context

See proposal.md (Why, Defect) for the motivation and the reproduced defects. The current design, read at `2012c60` and checked against PR #8's later head `4f7ff49`:

- **Container-wide runs.** `Scope.HealthCheckWithContext` (scope.go) applies `HealthCheckGlobalTimeout`, then `asyncHealthCheckWithContext` asks `RootScope.queueServiceHealthcheck` for one result channel per service in the scope and its ancestors.
  - Without a pool, each service gets its own goroutine and `awaitHealthCheck` (utils.go).
  - With `HealthCheckParallelism > 0`, the work goes through `jobPool`, which has a fixed 1,000-slot channel and workers started in `NewWithOpts`.
- **Single-service functions.** `HealthCheckNamedWithContext` (di_lifecycle.go) runs the check on the caller's goroutine through `raceWithTimeout`. Only when ctx has a deadline does it use a goroutine. Since `4f7ff49` it is bounded by `HealthCheckSingleTimeout` (0 keeps `HealthCheckTimeout`; positive is its own bound; negative means no bound), and `HealthCheckGlobalTimeout` does not apply.
- **What a check sees today** (probe): a `HealthcheckerWithContext` receives the caller's context on both paths, including its deadline and values (`deadline value=trace-1`).
- **Nested and self-referencing checks** (probes):
  - A check that calls another service's check works today even with `HealthCheckParallelism: 1`, because the single-service path skips the pool.
  - A check that calls its own service's check recurses without limit (depth 1001 reached under a guard).
- **Lazy services:**
  - `serviceLazy.healthcheck` holds the wrapper's read lock while the check runs (D3).
  - `instanceOrBuild` holds the write lock for the whole provider call, and `isHealthchecker` takes the read lock. So listing services waits for any provider in progress (D9, probe).
- **Aliases.** `serviceAlias.healthcheck` calls the target's check.
- **Lifecycle (PR #8):**
  - `startPhase`/`closePhase` under `lifecycleMu`.
  - `markReady` (root_start.go) moves started to ready, before the Ready hooks.
  - `shutdownAsWinner` (root_scope.go) waits for a running start, runs the Closing hooks, then shuts the services down. A Shutdown reached through the root's own `*Scope` bypasses it.
  - `HookContainerPanic`: a nil value means re-panic on the Start or Shutdown caller.
  - `RootScope.Phase()`.
  - `Clone()` is `CloneWithOpts(s.opts.copy())`, so it copies the hooks.
- **Package init.** `DefaultRootScope = New()` runs at package init, so `New()` must stay goroutine-free.
- **Constraints:**
  - No new module dependency.
  - The injector clock (clockwork) for every time read.
  - goleak in `TestMain`.
  - A fake-clock context's `Err()` blocks while it is live (`contextDone` in utils.go).

## Prior art

The approach follows [alexliesenfeld/health](https://github.com/alexliesenfeld/health) (v0.8.1, [config.go](https://github.com/alexliesenfeld/health/blob/v0.8.1/config.go), [check.go](https://github.com/alexliesenfeld/health/blob/v0.8.1/check.go)). Health classification follows .NET health checks.

| alexliesenfeld/health / .NET | Here | Why it differs |
|---|---|---|
| `WithPeriodicCheck(refreshPeriod, initialDelay, check)`: background checks; `Check()` returns their last result | Scheduled rounds; `HealthReport()` on a monitored container never runs a check | Same idea |
| `StatusUnknown` before the first result | `HealthUnknown` with `PendingSince`; a critical service without a result keeps the overall state Unknown | Same idea; newly visible services are checked at once, so readiness does not flap |
| `WithCacheDuration` (default 1s) for on-request checks | `HealthReportCacheTTL` (default 1s) for on-demand reads when no scheduler runs | Same default; turning the scheduler off gives the on-request model |
| `WithStatusListener` (aggregate) and `Check.StatusListener` (per check), called synchronously | `HookContainerHealthChanged` and `HookServiceHealthChanged`: serialized, in recording order, re-entrant | A result does not wait for hooks, so a hook may call health APIs without deadlocking |
| Panics recovered unless `DisablePanicRecovery` | Always recovered, reported to `HookContainerPanic`; a panic raised by that hook propagates | Consumers re-raise from the hook if they want to; a background crash is never a sensible default |
| `NewChecker` autostarts unless `WithDisabledAutostart` | Starts at `PhaseReady`, stops at root Shutdown | A DI container already owns a lifecycle, and `DefaultRootScope` rules out starting at construction |
| `Check()` holds one mutex for a whole run, so concurrent callers queue | Concurrent callers share one run, and each waits within its own context and bound | Callers are not serialized behind a slow check |
| Global timeout 10s by default | Scheduled checks default to 10s; forced runs and reads keep today's bounds | Forced paths keep today's semantics (user decision) |
| .NET `HealthCheckRegistration.Tags` and an endpoint predicate | `WithHealthTags` with predefined `HealthTagReadiness`/`HealthTagLiveness`; `MatchHealthTags` filters | The Kubernetes probes work out of the box, and consumers can add their own groups |
| .NET `HealthStatus.Degraded` and the per-registration failure status | `AsOptionalHealth()` turns a failure into `HealthDegraded` | Chosen over go-health's `Fatal` flag, which hides a failing non-critical service from the overall state |

Other references checked: Kubernetes probes (period 10s, timeout 1s), .NET `HealthCheckPublisherOptions` (delay 5s, period 30s), and Spring Boot Actuator (on-request, cache TTL 0 by default). Spring's and Kubernetes' guidance that liveness must not check external dependencies sets the default classification (H15).

## Goals / Non-Goals

**Goals:**
- Reading health costs the same for 1 reader and for 1,000.
- At most one goroutine per service runs its check, whatever the callers do.
- No scheduler exists until a container reaches Ready, and none remains after its Shutdown.
- No health path can block registration, building, shutdown, or a caller past its own context and bound.
- Every opinionated choice can be overridden through `InjectorOpts`.

**Non-Goals:**
- The HTTP handlers (follow-up change).
- JSON marshalling of `HealthReport`. `Err` is an `error`, and the follow-up decides its wire shape.
- Per-service intervals or per-service timeout options.
- Failure thresholds (`MaxContiguousFails` in alexliesenfeld). A consumer can derive them from the hooks.
- Spring-style central group definitions. Tags are declared at registration (H15).
- A startup probe tag. Consumers can define one as a custom tag; `PhaseReady` already says when start finished.
- A minimum interval. A tiny positive interval busy-loops on a real clock; this is documented, not prevented.

## Decisions

**H1. The scheduler belongs to the container lifecycle.**
- **Start.** It starts in the Ready transition. `markReady` starts it under `lifecycleMu`, only when `closePhase == closeOpen` and the interval is not negative. It does not run user code, so the rule that `lifecycleMu` is never held while user code runs still holds. Then the Ready hooks run, and a Ready hook reading a report sees Monitored with the first round pending.
- **Stop.** It stops first thing in `shutdownAsWinner`, and also on the bypass path where Shutdown reaches the root's own `*Scope`. Stopping does three things:
  - sets `closing` on the monitor, so nothing more is recorded and no hook fires (health-reports);
  - cancels the monitor context, which cancels scheduled checks;
  - waits for the scheduler goroutine, bounded by Shutdown's ctx.
- **Why lifecycleMu.** Both steps take it, so a start finishing during a Shutdown never starts a scheduler after the close began.
- **Alternatives rejected:**
  - Start in `New()`: leaks via `DefaultRootScope`.
  - Start lazily on the first report read: leaks in tests that read without Shutdown.
  - Start at `Start()` entry: checks would run against services still starting.

**H2. One monitor per root, keyed by service wrapper.**
- **State.** A `healthMonitor` on `RootScope` holds, under one mutex, `records`, `inflight` and a transition queue, all keyed by the wrapper value. A wrapper is unique per registration, so an Override or a re-registration starts a fresh record.
- **Pruning.** A record is pruned when its wrapper is removed: through the internal hooks for registration replacement and scope shutdown, and as a fallback when a round is dispatched. So records do not grow with churn even when scheduling is off.
- **Lock rule.** The monitor mutex is never held while listing services, while calling a check, or while calling user code.
- **Alternative rejected:** keying by name. It collides across scopes and keeps a stale result across an Override.

**H3. An in-flight check is a shared call; each waiter has its own bound.**
- **States.** A `sharedCall` per service has states queued (waiting for a slot), running, and returned. It holds a done channel, a result, a waiter count and a cancel function. The first caller creates it and runs it on a new goroutine; later callers join.
- **Waiting.** Each waiter selects on `done`, its own ctx, and a timer for its own path's bound, started at its call (H10). When it stops early, it gets `timeoutError(cause)` for that service.
- **Abandonment.** When the waiter count reaches zero before the call returns, the check's ctx is cancelled and the call is marked abandoned. An abandoned call stays in `inflight` until its goroutine exits, so no second goroutine starts. A newcomer never joins it: it waits, within its own bound, on the abandoned call's `done`, then creates a fresh call. The abandoned result is not recorded.
- **The check's context.** `context.WithoutCancel(starter ctx)` keeps the starter's values. A deadline is then applied: the earlier of the starter ctx's deadline and the starter's own bound, recorded with `withHealthCheckDeadline` so `healthCheckResult` classifies timeouts as today. Then a cancel for abandonment and shutdown.
- **The check's own bound.** When it elapses, the call records a timeout for the service and publishes it to current waiters, but stays in `inflight` until the goroutine returns. The late result is then recorded too (health-reports).
- **Why not `x/sync/singleflight`.** Its function takes no context, a waiter leaving cannot cancel the call, it has no per-waiter bound, and it would add a module dependency.

**H4. Forced runs coalesce per scope, on top of per-service sharing.**
- Per-service sharing alone does not make 50 callers cost one run: a caller arriving after the fast checks finished would run them again.
- So `HealthCheckWithContext` also joins an in-flight run of the same scope (a run object keyed by scope ID).
- **Before publishing the run,** it fills a result map with the nil entries of non-checkers. Each finished service adds its entry under the run's mutex.
- **What a caller gets.** Each caller waits on the run's completion within its own ctx, its own `HealthCheckGlobalTimeout` (from its own call), and per service its own `HealthCheckTimeout`. It then copies the finished entries and fills the rest with `timeoutError(cause)`.
- **The run's context.** The run's checks are started with the first caller's context (H3). The run's checks are released when its last caller leaves.

**H5. Listing services never blocks.**
- `visibleCheckers(scope)` copies each scope's services under that scope's read lock and releases the lock before any check starts. It returns, for each name, the wrapper to check:
  - The nearest scope wins, so a shadowed ancestor is skipped (D4).
  - An alias resolves to its target's wrapper and shares the target's call (D5).
  - A non-checker is kept as a nil entry, so the `HealthCheck()` map shape is unchanged.
- `containerCheckers(root)` walks every scope for rounds and the root report.
- **Non-blocking lazy check (D9).** `serviceLazy` gets an atomic `built` flag, set after the build completes. `isHealthchecker` and `healthcheck` read it and copy the instance without waiting for the build lock. A service still being built counts as not yet built.

**H6. A context-aware limiter caps top-level checks.**
- **Shape.** `HealthCheckParallelism > 0` creates a `chan struct{}` of that size in `NewWithOpts`, and no goroutine. A queued call's goroutine acquires a slot before it runs, and releases it when the check returns. Waiting for the slot honors every waiter's ctx and bound through H3.
- **The check's own bound** starts when it starts running (after the slot), so a scheduled check is not timed out by its wait.
- **What this fixes.** No queue limit and no send under a lock (D2). No goroutines in `New()` (D6).
- **Removed.** `jobPool`, `newJobPool` and `RootScope.healthCheckPool`, all unexported.
- **Behavior change.** The single-service functions now use the limit too; flagged.
- **Alternative rejected:** `errgroup.SetLimit`. It needs `x/sync` and has no per-acquire context.

**H7. Panics are recovered where the check runs.**
- **Error.** `ErrHealthCheckPanic = errors.New("DI: health check panic")`.
  - An error value yields `fmt.Errorf("%w: %w", ErrHealthCheckPanic, v)`.
  - A non-error value yields the same with `fmt.Errorf("%v", v)`.
  - A nil panic is `*runtime.PanicNilError`, as recovered.
- **Hook.** When `HookContainerPanic` is set, it is called with `(root, "healthcheck", value)`. A panic raised inside it is not recovered, so a consumer who wants a crash gets one. With a nil `HookContainerPanic`, nothing else happens: the error is the outcome. That deviates from PR #8's nil-means-re-panic rule, because no Start or Shutdown caller is there to receive the panic (flagged; user decision Q5).

**H8. A lazy service's check runs outside its lock.** `serviceLazy.healthcheck` copies the instance (H5's atomic flag), releases the lock, and then calls the check (D3). A check may therefore run while the service shuts down, as eager services already can (flagged).

**H9. The scheduler: fixed-delay ticks, join with its own bound, prompt checks of new services.**
- **Loop.** The scheduler goroutine:
  1. dispatches a round;
  2. calls `clock.NewTimer(interval)`;
  3. selects on the timer, on a buffered `wake` channel and on the monitor context.
- **Dispatch.** For each checker of the container, it joins or starts the service's call as a pinned waiter with the scheduled bound. A pinned waiter never cancels the call by leaving; it is released when the call returns or the monitor stops. A check another path started is therefore bounded by the scheduled bound too.
- **Fixed delay.** The interval is measured from the start of the previous round. A clock jump fires the timer once, so missed rounds are dropped. A service with a call in flight is joined, not checked twice.
- **Prompt checks.** The monitor's internal registration and after-build hooks send on `wake` when a health checker becomes visible while monitored. The scheduler then dispatches only the services that have no record and no call, without resetting the timer.
- **Logging.** Each round logs one `Logf` line: `DI: health check round`.
- **Alternatives rejected:**
  - A `Ticker`, which replays missed ticks on some clocks.
  - Waiting for the round: a hung, unbounded check would stall the scheduler.

**H10. Every path keeps its own bound.**

| Path | Waiter bound, from the call | Check bound when it starts the check |
|---|---|---|
| `HealthCheck()` / `HealthCheckWithContext` | `HealthCheckTimeout` per service, `HealthCheckGlobalTimeout` per call, the caller's ctx | `HealthCheckTimeout` |
| Single-service functions | `HealthCheckSingleTimeout` (0 means `HealthCheckTimeout`), the caller's ctx | the same |
| Scheduled round | `HealthCheckTimeout` > 0, else `DefaultScheduledHealthCheckTimeout` (10s), unbounded when negative | the same |
| On-demand report read | `HealthCheckTimeout` > 0, the caller's ctx; global does not apply | `HealthCheckTimeout` |

A caller joining a check started by another path still stops at its own bound. It may get a timeout earlier if the starter's bound fired first and the check, being context-aware, returned at it; this is documented on the options.

**H11. Reports.**
- **What a read does.** `HealthReportWithContext` reads `Phase()` and the monitor state:
  - Monitored (scheduler running): it builds the report from the records and returns at once.
  - Unmonitored in Idle, Failed or Ready (scheduling off): for each covered checker whose record is missing or at least TTL old, it joins or starts a call with the on-demand bound of H10, then waits within ctx.
  - Starting, Started and Closing: the records, no checks.
  - Closed: empty and Unknown.
- **Coalescing.** Concurrent readers coalesce through H3's per-service sharing; the TTL makes a late reader reuse finished results.
- **Abandoned entries** show `HealthUnhealthy` with `timeoutError(cause)` in that reader's report only.
- **Ordering.** By the scope's name path from the root (depth first, then the path), then service name. Scope IDs are random, so they cannot order the entries.

**H12. Hooks: ordered, serialized, re-entrant delivery.**
- **Recording.** A goroutine records a result under the monitor mutex and does two things:
  - computes the service transition and the overall transition, and appends them to the transition queue with a sequence number;
  - closes `done`, so waiters get the result without waiting for hooks.
- **Delivery.** If no delivery is in progress, the recording goroutine marks itself the deliverer. It then drains the queue in order, calling the service hooks, then the container hooks, with no lock held. Other recorders only enqueue.
- **Effect.** Hooks never run two at once and keep recording order. A hook may call `HealthCheck*` or `HealthReport*`: their results are recorded, enqueued, and delivered by the same drain after the hook returns.
- **Membership changes.** Pruning (H2) and prompt checks (H9) recompute the overall state, so a service appearing or leaving can fire the container hook.
- **Shutdown.** After `closing` is set (H1), nothing is enqueued.
- **Hook lists.** `Add*HealthChangedHook` takes the monitor mutex and swaps a new slice (copy on write), so delivery reads a stable list.
- **Panics.** Each hook call recovers its panic into `HookContainerPanic(root, "healthchanged", v)`, which may propagate, or into `Logf` when it is nil.

**H13. API placement.** `HealthReport` and `HealthReportWithContext` are added to the `Injector` interface, implemented by `*Scope`, `*RootScope` (whole container) and `*virtualScope` (delegates). `Injector` has unexported methods, so no type outside the package can implement it, and adding methods breaks no consumer.

**H14. Doc correction (D8).** The `RootScope.HealthCheck` doc comment is corrected to say "root scope's services". Behavior is unchanged (user decision Q6).

**H15. Classification is a registration option; filtering is a report option.**
- **Declaration.** `WithHealthTags(tags...)` and `AsOptionalHealth()` are `ProvideOption` constructors. They write two fields of the registration's option struct: a tag set (nil means the default `{readiness}`; a non-nil empty set means no tag) and an `optional` flag.
- **Validation** at registration, before the service is stored, like the existing `AsStarter()` checks: an empty tag panics, and so does either option on a transient registration.
- **Propagation.** The fields follow the existing option rules: Clone copies them, Override replaces them, and an alias has none of its own. An alias entry shows its target's classification because it reports the target's result. That is not an inherited opt-in, so `registration-options` is unchanged; its "later changes MAY add constructors" covers these options.
- **Severity** is applied when a result is recorded: a failing optional check is stored as `HealthDegraded`. Forced paths return the raw error.
- **Filtering.** `HealthReportOption` is opaque. `MatchHealthTags(tags...)` adds tags to an any-match set, and an empty set means no filter. A report filters its entries before the overall state and before it picks stale services for on-demand checks.
- **Overall state.** Unhealthy, else Degraded, else Unknown while a covered critical service has no result, else Healthy (including no service covered). A closed container reports Unknown. Prompt checks of new services (H9) keep the Unknown window short.

**H16. Nested checks run inline; cycles are errors.**
- **The marker.** A check's context carries a marker with the chain of wrappers whose checks are running on that path.
- **Inline nesting.** A health call whose ctx has the marker is nested:
  - It takes no slot, and runs the check on its own goroutine when no call is in flight.
  - When the service's call is queued for a slot, the nested caller takes it over and runs it at once; the queued goroutine, on getting its slot, finds the call done and releases the slot.
  - When the call is already running, the nested caller joins it as usual.
- **Cycles.** If the target wrapper is already in the chain, the call returns ``fmt.Errorf("%w on service `%s`", ErrHealthCheckCycle, name)`` instead of waiting, with `ErrHealthCheckCycle = errors.New("DI: health check cycle")`.
- **Limitation.** Detection needs the check to pass on its ctx; a check that calls `HealthCheckNamed` without it is not nested, as today. Documented.

## Risks / Trade-offs

- [Checks now run every 60s by default; this costs money or has side effects for some consumers] → Flagged in the proposal and the release notes. `HealthCheckInterval: -1` opts out, and reports then check on demand behind the 1s cache.
- [Tests that call `Start` without `Shutdown` now leak the scheduler] → Flagged. The doc on `Start` says Shutdown stops the scheduler. Our own suite and `examples/*` are audited in task 16.2.
- [A joining caller's context values never reach the check, and a context-aware check may return at its starter's bound] → Flagged; documented on the options and on `HealthCheckWithContext`.
- [A check that ignores its context stays in flight until it returns] → It stays at one goroutine. Each caller still stops at its own bound, and the report records a timeout at the check's bound.
- [A hung check holding a slot under `HealthCheckParallelism` delays others] → Their callers stop at their own bounds. The slot is freed when the check returns. Documented.
- [Cycle detection needs ctx propagation] → Documented. Without ctx, a self-referencing check recurses as today.
- [A report that covers no service is Healthy, so a mistyped tag in `MatchHealthTags` yields a healthy report] → Use the exported tag constants; the godoc of `MatchHealthTags` states the rule.
- [goleak may briefly see a cancelled context-aware check exiting after Shutdown] → goleak retries by default. Scenarios release or cancel every check before the test ends.
- [Fake-clock contexts block on `Err()`] → Keep using `contextDone`. Scheduled-check tests use checks that wait on `ctx.Done()`.
- [The monitor mutex is on the report path] → Reads copy records under the mutex and do no I/O. A benchmark guards it (task 22.1).

## Public API & Defaults

New, all additive:

```go
// injector.go, InjectorOpts
HealthCheckInterval        time.Duration // 0: DefaultHealthCheckInterval (60s); <0: off; >0: interval
HealthReportCacheTTL       time.Duration // 0: 1s; <0: no cache; >0: TTL (reads when no scheduler runs)
HookServiceHealthChanged   []func(scope *Scope, serviceName string, previous, current ServiceHealth)
HookContainerHealthChanged []func(root *RootScope, previous, current HealthState)

const DefaultHealthCheckInterval = 60 * time.Second
const DefaultScheduledHealthCheckTimeout = 10 * time.Second
const DefaultHealthReportCacheTTL = time.Second

var ErrHealthCheckPanic = errors.New("DI: health check panic")
var ErrHealthCheckCycle = errors.New("DI: health check cycle")

// options.go, ProvideOption constructors
func WithHealthTags(tags ...string) ProvideOption // replaces the default tags; repeats accumulate
func AsOptionalHealth() ProvideOption             // failures are HealthDegraded
const HealthTagReadiness = "readiness"
const HealthTagLiveness = "liveness"

type HealthReportOption struct{ /* opaque */ }
func MatchHealthTags(tags ...string) HealthReportOption

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

`InjectorOpts.copy()` copies the new fields and clones the hook slices, as it does for the existing hooks, so `Clone()` copies the health hooks. Every default and its override is in the proposal's Defaults & Control table.

**Breaking changes:** none to signatures. The behavior changes are flagged in the proposal. Adding `InjectorOpts` fields breaks only unkeyed literals, which Go 1 compatibility excludes. **Migration:**
- To keep checks from running in the background, set `HealthCheckInterval: -1`.
- A consumer that relied on a health-check panic crashing re-raises it from `HookContainerPanic`.
- A check that needs a joining caller's context values must not rely on coalesced calls; it can read values from the service's own state instead.

## Test Strategy

New test files follow the new source files: `health_report.go` → `health_report_test.go`, `health_monitor.go` → `health_monitor_test.go`, `health_run.go` → `health_run_test.go`. Concurrency tests run under `-race` with `testWithTimeout`. Time-based tests use `clockwork.NewFakeClock()` (with `BlockUntilContext` to sequence timers) or `synctest.Test`. goleak runs in `TestMain`. "Wait until no health check is running" is a test helper that polls the monitor's in-flight count.

| Spec | Scenario | Test | Kind |
|---|---|---|---|
| health-check-execution | Concurrent calls share the run | `health_run_test.go: TestHealthCheck_ConcurrentShareRun` | race |
| health-check-execution | Sequential calls run again | `health_run_test.go: TestHealthCheck_SequentialRunAgain/<row>` | table |
| health-check-execution | Runs on different scopes are separate | `health_run_test.go: TestHealthCheck_ScopesSeparate` | race |
| health-check-execution | A joined run never shows a missing entry | `health_run_test.go: TestHealthCheck_JoinedRunNilEntries` | race |
| health-check-execution | Each caller keeps its own bound when it joins a check | `health_run_test.go: TestHealthCheck_JoinerOwnBound/<row>` | table, fake clock |
| health-check-execution | One caller leaving does not cancel the check for the others | `health_run_test.go: TestHealthCheck_CallerLeaves` | race |
| health-check-execution | Deadline and values reach the check | `health_run_test.go: TestHealthCheck_StarterContext/<row>` | table |
| health-check-execution | The only caller leaving cancels the check | `health_run_test.go: TestHealthCheck_LastCallerCancels` | race |
| health-check-execution | A check that ignores its context does not pile up | `health_run_test.go: TestHealthCheck_NoPileUp/<row>` (regression D1) | table, race, goleak |
| health-check-execution | A call that joins a running check gets its result | `health_run_test.go: TestHealthCheck_JoinRunning` | race |
| health-check-execution | An abandoned check is never joined | `health_run_test.go: TestHealthCheck_AbandonedNotJoined` | race |
| health-check-execution | After a check finishes, the next call checks again | `health_run_test.go: TestHealthCheck_NextCallChecksAgain` | unit |
| health-check-execution | Parallelism caps running checks | `health_run_test.go: TestHealthCheck_ParallelismCap/<row>` | table, race |
| health-check-execution | Waiting for a slot honors the context | `health_run_test.go: TestHealthCheck_SlotWaitHonorsContext` (regression D2) | race |
| health-check-execution | Registration is not blocked by checks waiting for a slot | `health_run_test.go: TestHealthCheck_SlotWaitDoesNotBlockProvide` (regression D2) | race |
| health-check-execution | A scheduled check's timeout does not count the wait for a slot | `health_monitor_test.go: TestHealthMonitor_BoundStartsAtSlot` | fake clock |
| health-check-execution | Many services with a small limit all complete | `health_run_test.go: TestHealthCheck_NoQueueLimit` | unit |
| health-check-execution | New starts no goroutine | `health_run_test.go: TestNew_NoHealthGoroutine/<row>` (regression D6) | table |
| health-check-execution | Nested checks complete under a limit of one | `health_run_test.go: TestHealthCheck_NestedInline/<row>` | table, race |
| health-check-execution | Self-referencing checks | `health_run_test.go: TestHealthCheck_Cycle/<row>` | table |
| health-check-execution | Panic results | `health_run_test.go: TestHealthCheck_Panic/<row>` (regression D7) | table |
| health-check-execution | A panic in a scheduled round does not stop scheduling | `health_monitor_test.go: TestHealthMonitor_PanicKeepsScheduling` | fake clock |
| health-check-execution | A check in flight does not delay the service | `service_lazy_test.go: TestServiceLazy_HealthCheckWithoutLock/<row>` (regression D3) | table, race |
| health-check-execution | A provider in progress does not block health checks | `service_lazy_test.go: TestServiceLazy_HealthCheckDuringBuild/<row>` (regression D9) | table, race |
| health-check-execution | Each underlying check runs once | `health_run_test.go: TestHealthCheck_ShadowAndAliasOnce/<row>` (regressions D4, D5) | table |
| health-check-execution | Map shape is unchanged | `health_run_test.go: TestHealthCheck_MapShape/<row>` | table |
| health-check-scheduling | Scheduler starts only at Ready | `health_monitor_test.go: TestHealthMonitor_StartsAtReady/<row>` | table, fake clock |
| health-check-scheduling | Containers that are never started leave no goroutine | `health_monitor_test.go: TestHealthMonitor_NeverStartedNoGoroutine/<row>` (goleak.VerifyNone) | goleak |
| health-check-scheduling | Rounds follow the interval on the fake clock | `health_monitor_test.go: TestHealthMonitor_Interval/<row>` | table, fake clock |
| health-check-scheduling | A negative interval runs no round | `health_monitor_test.go: TestHealthMonitor_NegativeInterval` | fake clock |
| health-check-scheduling | Consecutive advances run one round each | `health_monitor_test.go: TestHealthMonitor_ConsecutiveRounds` | fake clock |
| health-check-scheduling | A clock jump runs at most one round | `health_monitor_test.go: TestHealthMonitor_ClockJump/<row>` | table, fake clock |
| health-check-scheduling | Removed and replaced services leave the next round | `health_monitor_test.go: TestHealthMonitor_RemovedServices/<row>` | table, fake clock |
| health-check-scheduling | Services that appear later are checked at once | `health_monitor_test.go: TestHealthMonitor_PromptCheck/<row>` | table, fake clock |
| health-check-scheduling | Scheduled check bounds | `health_monitor_test.go: TestHealthMonitor_ScheduledTimeout/<row>` | table, fake clock |
| health-check-scheduling | A round bounds a check another path started | `health_monitor_test.go: TestHealthMonitor_RoundBoundsForeignCheck` | fake clock |
| health-check-scheduling | Forced runs keep their unbounded default | `healthcheck_timeout_test.go: TestHealthCheck_ForcedUnboundedByDefault` | fake clock |
| health-check-scheduling | No round after shutdown begins | `health_monitor_test.go: TestHealthMonitor_StopsBeforeClosing/<row>` | table, fake clock, goleak |
| health-check-scheduling | Shutdown does not wait for a check that ignores its context | `health_monitor_test.go: TestHealthMonitor_ShutdownSkipsHungCheck` | race |
| health-check-scheduling | A context-aware check in flight is cancelled before services shut down | `health_monitor_test.go: TestHealthMonitor_ShutdownCancelsCheck` | race |
| health-check-scheduling | Clone scheduling follows the clone's lifecycle | `health_monitor_test.go: TestHealthMonitor_Clone/<row>` | table, fake clock |
| health-check-scheduling | The source keeps scheduling after its clone shuts down | `health_monitor_test.go: TestHealthMonitor_CloneShutdownKeepsSource` | fake clock |
| health-check-scheduling | Service hook calls follow state changes | `health_monitor_test.go: TestHookServiceHealthChanged/<row>` | table, fake clock |
| health-check-scheduling | A forced run records changes too | `health_monitor_test.go: TestHookServiceHealthChanged_ForcedRun` | unit |
| health-check-scheduling | Hooks run one at a time, in order of addition | `health_monitor_test.go: TestHookServiceHealthChanged_Serialized` | race |
| health-check-scheduling | A hook may call health APIs | `health_monitor_test.go: TestHookServiceHealthChanged_Reentrant/<row>` | table, race |
| health-check-scheduling | Container hook calls follow the overall state | `health_monitor_test.go: TestHookContainerHealthChanged/<row>` | table, fake clock |
| health-check-scheduling | A service leaving changes the overall state | `health_monitor_test.go: TestHookContainerHealthChanged_Membership` | fake clock |
| health-check-scheduling | Hook panic is contained | `health_monitor_test.go: TestHealthHooks_Panic/<row>` | table |
| health-reports | Report entries after one round | `health_report_test.go: TestHealthReport_Entries/<row>` | table, fake clock |
| health-reports | Each report is the caller's own copy | `health_report_test.go: TestHealthReport_Isolated` | unit |
| health-reports | Entries are ordered by scope path then name | `health_report_test.go: TestHealthReport_Order` | unit |
| health-reports | Report membership | `health_report_test.go: TestHealthReport_Membership/<row>` | table |
| health-reports | Report coverage by scope | `health_report_test.go: TestHealthReport_Coverage/<row>` | table |
| health-reports | Overall state | `health_report_test.go: TestHealthReport_OverallState/<row>` | table |
| health-reports | HealthState names | `health_report_test.go: TestHealthState_String/<row>` | table |
| health-reports | A closed container reports no service | `health_report_test.go: TestHealthReport_Closed` | unit |
| health-reports | Reads on a monitored container cost nothing | `health_report_test.go: TestHealthReport_MonitoredNoChecks` | race |
| health-reports | Before the first result | `health_report_test.go: TestHealthReport_BeforeFirstResult` | fake clock |
| health-reports | On-demand reads honor the cache TTL | `health_report_test.go: TestHealthReport_OnDemandTTL/<row>` | table, fake clock |
| health-reports | Concurrent on-demand reads share one check | `health_report_test.go: TestHealthReport_OnDemandShared` | race |
| health-reports | On-demand reads by phase | `health_report_test.go: TestHealthReport_OnDemandByPhase/<row>` | table |
| health-reports | Reads without on-demand checks return the last recorded results | `health_report_test.go: TestHealthReport_ClosingKeepsResults` | unit |
| health-reports | A reader that stops waiting | `health_report_test.go: TestHealthReport_ReaderContext/<row>` | table, synctest |
| health-reports | A reader leaving does not affect other readers | `health_report_test.go: TestHealthReport_ReaderLeaves` | race |
| health-reports | Forced and single-service checks refresh the report | `health_report_test.go: TestHealthReport_RefreshedByForced/<row>` | table |
| health-reports | A timed-out check is recorded at its bound and again when it returns | `health_report_test.go: TestHealthReport_TimeoutThenLateResult` | fake clock |
| health-reports | An abandoned check is not recorded | `health_report_test.go: TestHealthReport_AbandonedNotRecorded` | race |
| health-reports | Results after shutdown began are not recorded | `health_report_test.go: TestHealthReport_NoRecordAfterShutdown` | unit |
| health-classification | Default classification | `health_report_test.go: TestHealthClassification_Default/<row>` | table |
| health-classification | Predefined tag values | `health_report_test.go: TestHealthTags_Constants/<row>` | table |
| health-classification | Tags of a registration | `health_report_test.go: TestHealthClassification_Tags/<row>` | table |
| health-classification | Severity of a registration | `health_report_test.go: TestHealthClassification_Optional/<row>` | table |
| health-classification | Invalid classification | `options_test.go: TestHealthOptions_InvalidPanics/<row>` | table |
| health-classification | Classification across clone, override and alias | `health_report_test.go: TestHealthClassification_CloneOverrideAlias/<row>` | table |
| health-classification | Filtered reports | `health_report_test.go: TestHealthReport_MatchTags/<row>` | table |
| health-classification | A filtered on-demand read checks only the covered services | `health_report_test.go: TestHealthReport_MatchTagsOnDemand` | unit |
| health-classification | Degraded entries | `health_report_test.go: TestHealthReport_Degraded/<row>` | table |
| health-classification | Degraded transitions reach the hooks | `health_monitor_test.go: TestHealthHooks_Degraded` | fake clock |
| health-classification | Zero-config liveness | `health_report_test.go: TestHealthReport_LivenessZeroConfig/<row>` | table |
| health-classification | Forced results ignore classification | `health_run_test.go: TestHealthCheck_IgnoresClassification/<row>` | table |
| container-clock | Report times come from the clock | `clock_test.go: TestClock_HealthReportTimes` | fake clock |
| container-clock | Health scheduling and caching wait for the clock, not for wall time | `clock_test.go: TestClock_HealthSchedulingOnClock/<row>` | table, fake clock |

**Hot paths and their tests:**

| Hot path | Tests |
|---|---|
| Forced run (`HealthCheckWithContext`) | `TestHealthCheck_ConcurrentShareRun`, `_JoinedRunNilEntries`, `_CallerLeaves`, `_MapShape`, the existing `healthcheck_timeout_test.go` suite |
| Single-service functions | `TestHealthCheck_NoPileUp/HealthCheckNamed`, `_JoinerOwnBound`, `_JoinRunning`, `_NextCallChecksAgain`, `_NestedInline`, `_Cycle`, existing per-service tests |
| Shared call: join, leave, bound, abandonment, late result | `TestHealthCheck_JoinerOwnBound`, `_LastCallerCancels`, `_AbandonedNotJoined`, `TestHealthReport_TimeoutThenLateResult`, `_AbandonedNotRecorded` |
| Limiter and nesting | `TestHealthCheck_ParallelismCap`, `_SlotWaitHonorsContext`, `_SlotWaitDoesNotBlockProvide`, `_NoQueueLimit`, `_NestedInline`, `TestHealthMonitor_BoundStartsAtSlot` |
| Listing services | `TestServiceLazy_HealthCheckDuringBuild`, `TestHealthCheck_ShadowAndAliasOnce`, `TestHealthReport_Coverage` |
| Scheduler loop: start, tick, jump, wake, stop | `TestHealthMonitor_*` |
| Report read: monitored and on-demand | `TestHealthReport_*`, plus `BenchmarkHealthReport_Monitored` (1,000 services) to show a read does no I/O and stays allocation-bounded |
| Hooks | `TestHookServiceHealthChanged*`, `TestHookContainerHealthChanged*`, `TestHealthHooks_*` |
| Lifecycle edges: shutdown during start, bypass path, clone | `TestHealthMonitor_StartsAtReady/shutdown during start`, `TestHealthMonitor_StopsBeforeClosing/<provider injector row>`, `TestHealthMonitor_Clone*` |

**Docs:**
- `ExampleRootScope_HealthReport` shows the default, the opt-out, and on-demand reads with their TTL.
- `ExampleMatchHealthTags` shows readiness and liveness reports with an optional service.
- `ExampleRootScope_AddServiceHealthChangedHook` shows a hook.

## Migration Plan

1. Wait for PR #8 to merge. Then merge `main` into this change's implementation branch, and reconcile names with #8's final API (task group 1): the phase names, `HookContainerPanic`'s signature, and `HealthCheckSingleTimeout`.
2. Implement the task groups in order. Each defect fix starts with its regression test (proposal Defect D1–D9).
3. Release notes list the flagged behavior changes and the `HealthCheckInterval: -1` opt-out.
4. Rollback: revert the PR. Consumers can also turn scheduling off without a code rollback.

## Open Questions

- The JSON shape of `HealthReport`, including how `Err` is rendered. It is deferred to the HTTP-handler follow-up; it does not change this change's specs or tasks.
