# Proposal

## Why

Consumers expose health checks over HTTP. Today every call to `HealthCheck()` runs every check again, so the load on dependencies grows with the number of callers: 50 concurrent calls over 10 services make 500 checks, and checks that ignore their context pile up one goroutine per call. That makes a public health endpoint a load amplifier. This change makes the cost of reading health independent of the number of readers: checks run on a schedule, the results are cached, and callers read the cache. Callers can still force a run, and concurrent forced runs share one run.

The design follows [alexliesenfeld/health](https://github.com/alexliesenfeld/health), adapted to the container lifecycle. That library offers periodic checks with cached results, an explicit Unknown status before the first result, a short on-demand cache (1s by default), status listeners and panic recovery.

This change depends on `consolidate-pending-lifecycle-work` (PR #8): it uses the container phases (`PhaseReady`, `PhaseClosing`), `HookContainerPanic`, `HealthCheckSingleTimeout` and the container lifecycle that change adds. Implementation starts after #8 merges.

## What Changes

**Scheduled health checks (on by default).**
- New `InjectorOpts.HealthCheckInterval`: `0` uses `DefaultHealthCheckInterval` (60s), a negative value turns scheduling off, and a positive value sets the interval.
- A container that reaches `PhaseReady` starts one background scheduler, which runs the first round immediately and then one round per interval.
- At root `Shutdown`, including one reached through the root's own `*Scope`, the scheduler stops before the Closing hooks and before any service shuts down. From then on no result is recorded and no health hook fires, so a deploy does not raise alerts for services being shut down.
- Containers that never start get no scheduler. That includes `DefaultRootScope`, which is created at package init, and containers whose Start failed.
- Each round lists the services again. A health checker that appears while the container is monitored (registered, built, or in a new child scope) is checked at once, not at the next round.
- A scheduled check is bounded by `HealthCheckTimeout`, or by 10s when that is 0, measured from when the check starts running. A round also bounds a check that another path started.

**Cached health reports.**
- New `RootScope.HealthReport()` / `HealthReportWithContext(ctx)` cover the whole container. New `Scope.HealthReport()` / `HealthReportWithContext(ctx)` cover the services visible from that scope, with shadowed services left out.
- A report gives, for each service, its tags, whether it is optional, its state (`HealthUnknown`, `HealthHealthy`, `HealthDegraded`, `HealthUnhealthy`), its error, when it was checked, how long the check took and whether a check is in flight. It also gives an overall state and whether the container is monitored.
- Services without a health check do not appear in the report: plain values, transient services, lazy services not yet built.
- When the container is monitored, a read never runs a check.
- When no scheduler runs (Idle, Failed, or Ready with scheduling turned off), a read runs stale checks on demand. A result is reused for `HealthReportCacheTTL` (default 1s; a negative value turns the cache off), and concurrent readers share each check. A read is bounded by the caller's context and `HealthCheckTimeout`. So turning the scheduler off gives the on-request model of Spring Boot and alexliesenfeld/health.
- A covered critical service that has no result yet keeps the overall state `HealthUnknown`, so readiness does not pass before the first results.

**Forced runs stay, with coalescing.**
- `HealthCheck()` / `HealthCheckWithContext(ctx)` still run every check and return `map[string]error`.
- **Behavior change (flagged):** concurrent calls on the same scope share one run.
- A service never has two checks in flight. A call that finds a check in flight waits for that check's result instead of starting another, which caps check goroutines at one per service. This holds for the scheduler, forced runs, reads and the single-service functions. A check that timed out or that every caller left stays in flight until it returns, and is never joined.
- Each caller keeps its own bounds whoever started the check: `HealthCheckTimeout` and `HealthCheckGlobalTimeout` (from its own call) for `HealthCheck()`, and `HealthCheckSingleTimeout` for the single-service functions, as today.
- A check sees the context of the caller that started it, including its deadline and values, as today. A caller that joins a check does not change that context.
- A check called from inside a running check runs inline without taking a `HealthCheckParallelism` slot, so composite checks keep working under a limit. A check that depends on itself returns the new `ErrHealthCheckCycle` instead of recursing or deadlocking.
- Every completed check refreshes the cached report.

**Health classification (readiness, liveness, severity).** One container must answer "is it ready?" and "is it alive?" separately. Kubernetes and Spring Boot both warn that liveness must not depend on external systems, or one dependency outage restarts every pod.
- Tags follow .NET health-check tags: new `WithHealthTags(tags...)` registration option, with the predefined `HealthTagReadiness` ("readiness") and `HealthTagLiveness` ("liveness").
- Severity follows .NET's `Degraded` status: new `AsOptionalHealth()` registration option. A failing optional service is reported as the new state `HealthDegraded`, not `HealthUnhealthy`.
- An unclassified checker is tagged readiness and is critical. So with zero configuration every checker counts toward readiness, as every checker counts in `HealthCheck()` today, and a liveness report covers no service and is healthy.
- New `MatchHealthTags(tags...)` report option. It limits a report, its overall state and its on-demand checks to the services carrying any of the tags.
- Overall state: Unhealthy, else Degraded, else Unknown while a covered critical service has no result, else Healthy (including when no service is covered).
- `HealthCheck()` and the single-service functions ignore classification and return each error unchanged.

**Status-change hooks.**
- New `InjectorOpts.HookServiceHealthChanged` and `HookContainerHealthChanged`, with `AddServiceHealthChangedHook` and `AddContainerHealthChangedHook`.
- They are called only when a state changes (the container hook also when a service appearing or leaving changes the overall state), one at a time, in recording order, with no lock held, so a hook may call any health API. A result reaches its callers without waiting for hooks.
- Their panics are recovered and passed to `HookContainerPanic`, or logged when it is nil. With no Start or Shutdown caller to receive a re-raised panic, this deviates from PR #8's nil-means-re-panic rule (flagged).

**Defect fixes in health-check execution** (see Defect). Each one starts with a failing regression test.

**Behavior changes for existing consumers (flagged):**
1. A started container now runs every health check every 60s without being asked. That adds a `Logf` line per round, and checks with a cost or side effects now run. Opt out with `HealthCheckInterval: -1`.
2. A test that calls `Start` but never `Shutdown` now leaks the scheduler goroutine, and goleak reports it.
3. Concurrent `HealthCheck()` calls share results, as described above.
4. A panicking health check now returns an error matching the new `ErrHealthCheckPanic` instead of crashing the process.
5. With `HealthCheckParallelism > 0`, the single-service functions now also wait for a free slot (except when called from inside a check), and `New()` no longer starts goroutines.
6. A check may now run on a goroutine other than the caller's, and a caller that joins a check started by another caller does not pass its own context values to it.
7. A lazy service's check no longer holds the service's lock, so a check may run while the service shuts down, as eager services already can.
8. A health check that calls itself, directly or through other checks, now returns `ErrHealthCheckCycle`; today it recurses without limit.

**No exported API is removed or changed in signature.** New `InjectorOpts` fields break only unkeyed struct literals, which the Go 1 compatibility guidelines exclude.

**Out of scope:**
- An HTTP health handler in `http/*`. A follow-up change will add readiness and liveness handlers that serve tag-filtered reports.
- The next roadmap items (a test helper package, then container validation) are tracked in `ROADMAP.md`.
- Per-service intervals.

## Capabilities

### New Capabilities

- `health-check-scheduling`: the background scheduler: when it starts and stops, its interval, rounds, the timeout of scheduled checks, and the status-change hooks.
- `health-reports`: `HealthReport` / `HealthReportWithContext`: the report's content and states, scope views, on-demand runs and their cache.
- `health-classification`: health tags (readiness, liveness, custom), optional services and the Degraded state, tag-filtered reports, and how classification follows clones, overrides and aliases.
- `health-check-execution`: how checks run on every path: coalesced forced runs, at most one check in flight per service, bounded concurrency, panic recovery, locks, shadowed and aliased services.

### Modified Capabilities

- `container-clock`: the container also reads the scheduling interval, the report cache TTL, and the report's checked-at times and durations from `InjectorOpts.Clock`.

## Impact

- **Code:**
  - `injector.go` (new options and hooks) and `root_scope.go` (scheduler lifecycle and hook adders).
  - `scope.go` (reports, coalescing, shadowing) and `utils.go` (the job pool is replaced by a context-aware limit).
  - `options.go` (`WithHealthTags`, `AsOptionalHealth`).
  - `service_lazy.go` (the check runs outside the wrapper lock), `service_alias.go`, `di_lifecycle.go` (single-service functions) and `errors.go` (`ErrHealthCheckPanic`).
  - Tests across the root module.
- **Dependencies:** none added. Coalescing is a small internal primitive; `golang.org/x/sync` is not added.
- **Tests and examples:** existing tests that call `Start` and count health-check calls must account for the first round, or set `HealthCheckInterval: -1`. Workspace modules under `examples/*` and `http/*` must still build and pass.
- **Docs:** godoc and `Example*` tests for the new API, and the corrected `RootScope.HealthCheck` doc comment.

## Defaults & Control

| Behavior | Zero-config default | Override |
|---|---|---|
| Scheduled checks | On for containers that reach Ready, every 60s, first round immediately | `HealthCheckInterval`: positive sets the interval, negative turns scheduling off |
| Timeout of a scheduled check | `HealthCheckTimeout` when positive; 10s when it is 0 | Set `HealthCheckTimeout`: positive for its own bound, negative for no bound |
| Report when no scheduler runs (Idle, Failed, Ready with scheduling off) | Runs stale checks on demand, results reused for 1s, readers share each check | `HealthReportCacheTTL`: positive sets the TTL, negative turns caching off. A read is bounded by the caller's ctx through `HealthReportWithContext`, and by `HealthCheckTimeout` |
| Report on a monitored container | Cached results only, never runs a check | Call `HealthCheck()` to force a run; it refreshes the report |
| Forced `HealthCheck()` | Runs every check, coalesces concurrent calls | None. Each caller keeps its own context and bounds, but a caller that joins gets results of checks that may have started before its call, run with the starting caller's context values (flagged). No option is offered: an isolated run would reopen the load amplification this change removes |
| At most one check in flight per service | Always | None. A second concurrent check of the same service only adds load and goroutines. The caller gets the in-flight check's result, within its own bound |
| Concurrency limit | Unlimited (at most one goroutine per service) | `HealthCheckParallelism` caps top-level checks; checks called from inside a check do not take a slot |
| Panic in a check or a health hook | Recovered: a check's panic becomes an error matching `ErrHealthCheckPanic`; a hook's panic is logged | `HookContainerPanic` receives both; a panic it raises propagates, so a consumer can still crash on purpose |
| Health classification of a checker | Tag readiness, critical | `WithHealthTags(...)` replaces the tags; `AsOptionalHealth()` turns failures into `HealthDegraded` |
| Report coverage | Every health checker | `MatchHealthTags(...)`, for example `MatchHealthTags(HealthTagLiveness)` for a liveness probe |
| Status-change hooks | None registered | `HookServiceHealthChanged`, `HookContainerHealthChanged`, `Add*Hook` |
| `RootScope.HealthCheck()` coverage | Root services only (unchanged); the doc comment is corrected | Use `RootScope.HealthReport()` for the whole tree |

## Defect

Reproduced at `2012c60` (PR #8 head at the time) by probe tests in a scratch copy; each becomes a failing regression test in the repo before its fix.

| # | Observed | Expected | Minimal reproduction |
|---|---|---|---|
| D1 | Each call against a check that ignores ctx leaves one goroutine behind | At most one check in flight per service | `HealthCheckTimeout: 5ms`, one `Healthchecker` that blocks; 100 × `HealthCheck()` → 100 extra goroutines |
| D2 | With the pool queue full, `HealthCheckWithContext` ignores its ctx, and `Provide` blocks because the send happens under the scope read lock | The call returns when its ctx ends; registration is never blocked by a health check | `HealthCheckParallelism: 1`, one blocking check, 1,001 concurrent `HealthCheck()`; then `HealthCheckWithContext(20ms ctx)` and `ProvideNamedValue` both still blocked at 300ms |
| D3 | A lazy service's timed-out check keeps its read lock, so `Shutdown` blocks | Shutdown is not delayed by a health check | `HealthCheckTimeout: 5ms`, invoked lazy service whose check blocks; `HealthCheck()` then `Shutdown()` still blocked at 300ms |
| D4 | A shadowed ancestor service is still checked, and its result is thrown away | It is not checked | Root and child both register `"svc"`; `child.HealthCheck()` calls the root's check once |
| D5 | An alias runs its target's check again in the same call | Each service is checked once per call | `ProvideValue(&checker)`, `As[*checker, Healthchecker]`; `HealthCheck()` calls the check twice |
| D6 | `HealthCheckParallelism > 0` starts goroutines in `New()`, which leak without Shutdown | `New()` starts no goroutine | `NewWithOpts(&InjectorOpts{HealthCheckParallelism: 4})` → 4 extra goroutines |
| D7 | A panic in a check during `HealthCheck()` crashes the process; the caller cannot recover it | An error matching `ErrHealthCheckPanic` | A `Healthchecker` whose `HealthCheck` panics `"boom"`; `HealthCheck()` with a deferred `recover()` → process exits |
| D8 | The `RootScope.HealthCheck` doc comment says it covers descendant scopes; it does not | The doc comment matches the behavior | A service in child scope `"child"`; `root.HealthCheck()` returns an empty map |
| D9 | Listing services waits for a lazy service whose provider is running, ignoring the caller's ctx | Health checks never wait for a build in progress | A lazy provider blocked in another goroutine's `Invoke`; `HealthCheckWithContext(20ms ctx)` still blocked at 300ms (found by the audit) |

**Not reproduced (no fix proposed):** a race between `HealthCheck` and root `Shutdown` on the pool, 2 probes under `-race`. This matches PR #8's finding. The pool is replaced anyway (D2, D6).
