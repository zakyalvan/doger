# Proposal

## Why

Consumers expose health over HTTP. Today every call to `HealthCheck()` runs every check again, so the load on dependencies grows with the number of callers: 50 concurrent calls over 10 services make 500 checks. Checks that ignore their context also pile up, one goroutine per call. That makes a public health endpoint a load amplifier. Nine defects in health-check execution make it worse (see Defect); all nine still reproduce on `main` at `76404ef`.

This change makes the cost of reading health independent of the number of readers:
- A new cached `HealthReport` with a short TTL.
- At most one check in flight per service.
- An optional pluggable scheduler that keeps the cache warm.

It also makes the report answer Kubernetes' two questions separately: "is it ready?" and "is it alive?". It does this with health tags, optional services, and a container-phase gate, following Spring Boot's availability states.

The design follows [alexliesenfeld/health](https://github.com/alexliesenfeld/health) for caching and statuses, .NET health checks for tags and the Degraded state, and Spring Boot for the lifecycle-to-probe mapping.
- **Background scheduling is opt-in.** That is the default in Spring Boot (on request), .NET (the publisher is opt-in) and alexliesenfeld (periodic checks are opt-in per check).
- **Scheduling comes through a small interface,** with a [go-co-op/gocron](https://github.com/go-co-op/gocron) adapter shipped as a separate module. The root module gains no dependency.

**Prior art on lifecycle versus health** (research summary):
- No DI framework surveyed lets health results change the container lifecycle (Spring Boot, .NET, Quarkus, Guava `ServiceManager`, uber-go/fx, dig, wire, samber/do).
- Spring Boot and Guava go the other way: the lifecycle feeds health. Spring's readiness is `REFUSING_TRAFFIC` while starting and during graceful shutdown.
- This change does the same: the container phase gates the report, and health never changes the phase.

This change builds on `consolidate-pending-lifecycle-work` (#8, merged), and keeps the health rules of `add-default-registrations` (#11), `add-scope-export` (#12) and `add-extensions` (#15).

## What Changes

**Cached health reports.**
- `RootScope.HealthReport()` and `HealthReportWithContext(ctx)` cover the whole container. `Scope.HealthReport()` and `HealthReportWithContext(ctx)` cover the services visible from that scope.
- **One entry per underlying check.** A shadowed service, a yielded default, an alias and an export add no entry of their own; an alias or export contributes its source's entry once.
- **Per service, a report gives:**
  - its tags;
  - whether it is optional;
  - its state (`HealthUnknown`, `HealthHealthy`, `HealthDegraded`, `HealthUnhealthy`) and its error;
  - when it was checked and how long the check took;
  - whether a check is in flight.
- **Per container, a report gives:**
  - an overall state;
  - the container `Phase`;
  - whether a scheduler keeps it fresh (`Monitored`).
- **Reads without a scheduler.** A read runs stale checks on demand. A result is reused for `HealthReportCacheTTL` (default 1s; a negative value turns caching off), and concurrent readers share each check.
- **Reads with a scheduler.** A read returns the cached results and only checks services that have never been checked.
- **Bounds on a read.** A read is bounded by the caller's context and by `HealthCheckTimeout`. When `HealthCheckTimeout` is 0, the bound is `DefaultHealthReportCheckTimeout` (10s), so `HealthReport()` never hangs on a check that ignores its context.
- **Unknown until the first results.** A covered critical service with no result yet keeps the overall state `HealthUnknown`, so readiness does not pass before the first results.
- **Unstarted services are not checked.** Once the container has started, a service that `Start`, `Refresh` or `Scope.Start` would start, but that is not started yet (for example one registered or installed by `Extend` after Ready), is listed as `HealthUnknown` and is not checked by reports or rounds.

**Container phase gates readiness and liveness** (Spring Boot's availability model).
- **Readiness.** A report covering readiness is `HealthUnhealthy` while the container is Starting, Started, Failed or Closing. This drains traffic during graceful shutdown.
- **Liveness.** A report covering liveness is `HealthUnhealthy` when Start failed.
- **Idle containers keep their behavior.** Containers that never call `Start` are not gated, so their reports behave exactly as if no phase existed.
- **Opt out.** `IgnoreContainerPhase()` turns the gate off for a read.
- **One direction only.** Health never changes the phase.

**Health classification.**
- **Tags.** `WithHealthTags(tags...)` sets a service's tags, with the predefined `HealthTagReadiness` and `HealthTagLiveness`.
- **Severity.** `AsOptionalHealth()` turns a failure into `HealthDegraded`.
- **Default.** An unclassified checker is tagged readiness and is critical. So with zero configuration every checker counts toward readiness, and liveness depends only on the phase. This follows Spring's and Kubernetes' advice that liveness must not depend on external systems.
- **Filtering.** `MatchHealthTags(tags...)` filters a report.
- **Overall state.** Unhealthy, else Degraded, else Unknown while a covered critical service has no result, else Healthy. The phase gate then applies.
- **Forced results unchanged.** `HealthCheck()` and the single-service functions ignore classification.

**Optional scheduled rounds** (off by default).
- **Interface.** New `HealthScheduler` interface and `InjectorOpts.HealthScheduler` option.
- **Start.** When the container reaches `PhaseReady`, the container calls `ScheduleHealthRounds(clock, round)` once, before the Ready hooks.
- **Rounds.** Each call to `round(ctx)` checks every eligible checker once and returns. Each check is bounded by `HealthCheckTimeout`, or by `DefaultHealthReportCheckTimeout` when that is 0.
- **Stop.** When root Shutdown begins, the container cancels rounds, records nothing more, and calls the returned stop function before the Closing hooks run. The stop function gets a context Shutdown's context cannot cancel, so no scheduler goroutine outlives Shutdown.
- **No scheduler without Ready.** A container that never reaches Ready never calls the scheduler. `New()` and `DefaultRootScope` start no goroutine.
- **New module `github.com/kartaladev/doger/scheduler/gocron`** (package `dogergocron`) implements `HealthScheduler` with gocron v2:
  - **Defaults:** a 30s duration job, the first round at once, the container clock, and singleton mode, so a slow round skips the next tick instead of piling up.
  - **Overrides:** the interval, any gocron job definition such as a cron expression, scheduler and job options, or a shared `gocron.Scheduler` the application already runs.

**Executing checks.**
- **One check in flight per service.** A service never has two checks in flight. A caller that finds a check in flight waits for it, within its own bound, instead of starting another. This caps check goroutines at one per service.
- **Own bounds and the starter's context.** Each caller keeps its own context and bounds, as today. A check sees the context values and deadline of the caller that started it.
- **Parallelism limit.** `HealthCheckParallelism` limits the checks started by `HealthCheck()`, report reads and rounds, through a context-aware limiter. The single-service functions keep running at once without a slot, as today. When the check they need is waiting for a slot, they run it immediately, so composite checks never deadlock under a limit.
- **Recording.** Every completed check refreshes the report. A check whose report-path bound elapsed is recorded as timed out, and its late result is discarded, so a slow check does not flap.

**Status-change hooks.**
- **Fields and adders.** New `InjectorOpts.HookServiceHealthChanged` and `HookContainerHealthChanged`, with `AddServiceHealthChangedHook` and `AddContainerHealthChangedHook`.
- **When they fire.** They are called on state changes, one at a time, in recording order, with no lock held.
- **Panics.** A hook's panic goes to `HookContainerPanic` or is logged (flagged).
- **No ctx.** The hooks take no ctx, like `HookContainerPanic`: they are delivered off any caller's request.

**Defect fixes D1-D9** (see Defect), each starting with its failing regression test.

**Behavior changes for existing consumers (flagged):**
1. A panicking health check returns an error matching the new `ErrHealthCheckPanic` instead of crashing the process.
2. A check may run on a goroutine other than the caller's, and a caller that joins a check another caller started does not pass its context values to it.
3. A lazy service's check no longer holds the service's lock, so a check may run while the service shuts down, as eager services already can.
4. Shadowed services, yielded defaults and aliases are no longer checked twice in one call. Map keys and values are unchanged; only the number of calls drops.
5. `New()` with `HealthCheckParallelism > 0` no longer starts goroutines.

There is no background scheduling unless a consumer sets `HealthScheduler`, so a test that calls `Start` without `Shutdown` leaks nothing new.

**No exported API is removed or changed in signature.** New `InjectorOpts` fields break only unkeyed struct literals, which the Go 1 compatibility guidelines exclude. New methods on the `Injector` interface break no consumer, because `Injector` has unexported methods.

**Out of scope:**
- HTTP readiness and liveness handlers in `http/*`: the next roadmap item.
- Cycle detection for a check that calls itself. It still recurses, as today.
- Coalescing of whole forced `HealthCheck()` runs: per-service sharing already bounds goroutines, and readers should use `HealthReport`.
- Per-service intervals.
- A built-in timer scheduler in the root module.

## Capabilities

### New Capabilities

- `health-check-execution`: how checks run on every path. It covers:
  - at most one check in flight per service, each caller's bound, and the starter's context;
  - the context-aware parallelism limit;
  - panic recovery and lock freedom;
  - shadowed, defaulted, aliased and exported services;
  - the shape of the forced map.
- `health-reports`: `HealthReport` / `HealthReportWithContext`. It covers:
  - content, states and scope coverage;
  - monitored and on-demand reads, the cache, and bounds;
  - recording;
  - the container-phase gate;
  - unstarted services.
- `health-classification`: health tags (readiness, liveness, custom), optional services and the Degraded state, tag-filtered reports, and how classification follows clones, overrides, defaults and aliases.
- `health-check-scheduling`: the `HealthScheduler` interface. It covers:
  - when the container calls it and stops it;
  - what a round does;
  - clone independence;
  - the status-change hooks.
- `gocron-health-scheduler`: the `scheduler/gocron` module: its defaults, overrides, shared-scheduler mode and clean stop.

### Modified Capabilities

- `container-clock`: the container also passes its clock to the health scheduler, and reads the report cache TTL, the report-path check timeout, and the report's checked-at times and durations from `InjectorOpts.Clock`.

## Impact

- **Code (root module):**
  - `injector.go`: new options and hooks.
  - `root_scope.go` and `root_start.go`: calling and stopping the scheduler, and the hook adders.
  - `scope.go` and `root_scope.go`: reports and the execution paths, preserving the export, default and extension-scope rules of `healthCheckQueued` and `queueOwnHealthChecks`.
  - `utils.go`: `jobPool` is replaced by a context-aware limiter.
  - `options.go`: `WithHealthTags`, `AsOptionalHealth`.
  - `service_lazy.go`: the check runs outside the wrapper lock, and checker detection no longer waits for a build.
  - `service_alias.go`, `export.go`, `service_default.go`, `di_lifecycle.go`.
  - `errors.go`: `ErrHealthCheckPanic`.
- **New module:** `scheduler/gocron` (`github.com/kartaladev/doger/scheduler/gocron`), added to `go.work`. It depends on `github.com/go-co-op/gocron/v2`, which brings `google/uuid` and `robfig/cron/v3` and shares `jonboulle/clockwork v0.5.0` with the root module. Only consumers who import it get these dependencies.
- **Dependencies of the root module:** none added.
- **Tests and examples:** the existing suite is unaffected by scheduling, because it is opt-in. The `examples/*` and `http/*` modules must still build and pass. `make test` does not cover workspace modules, so the new module is tested explicitly.
- **Docs:**
  - godoc and `Example*` tests for the new API;
  - the corrected `RootScope.HealthCheck` doc comment, which covers the root scope and installed extension scopes, not other child scopes;
  - a README section for the gocron adapter;
  - an updated `ROADMAP.md`.

## Defaults & Control

| Behavior | Zero-config default | Override |
|---|---|---|
| Scheduled rounds | Off: no goroutine, no background checks | `InjectorOpts.HealthScheduler`, e.g. `dogergocron.New()`, or any implementation of `HealthScheduler` |
| gocron adapter cadence | 30s duration job, first round at once, container clock, singleton (reschedule) mode | `WithInterval`, `WithJobDefinition` (e.g. cron), `WithSchedulerOptions`, `WithJobOptions`, `WithScheduler` (shared scheduler) |
| Report read without a scheduler | Runs stale checks on demand, results reused for 1s, readers share each check | `HealthReportCacheTTL`: positive sets the TTL, negative turns caching off |
| Report read with a scheduler | Cached results; only never-checked services are checked on demand | Call `HealthCheck()` to force a run; it refreshes the report |
| Bound of a check started by a read or a round | `HealthCheckTimeout` when positive; `DefaultHealthReportCheckTimeout` (10s) when 0 | `HealthCheckTimeout`: positive for its own bound, negative for no bound; the read's ctx |
| Container phase in reports | Readiness Unhealthy while Starting, Started, Failed, Closing; liveness Unhealthy when Failed; Idle and Ready ungated | `IgnoreContainerPhase()` per read |
| Services not started yet (after Start) | Listed `HealthUnknown`, not checked by reads or rounds | `Refresh` / `Scope.Start` starts them; forced `HealthCheck()` still checks them |
| Forced `HealthCheck()` | Runs every check; joins checks already in flight | None needed: each caller keeps its own context and bounds; a joining caller's context values do not reach a check another caller started (flagged) |
| At most one check in flight per service | Always | None. A second concurrent check of the same service only adds load and goroutines |
| Concurrency limit | Unlimited | `HealthCheckParallelism` caps checks started by `HealthCheck()`, reads and rounds; single-service functions take no slot |
| Panic in a check or a health hook | Recovered: a check's panic becomes an error matching `ErrHealthCheckPanic`; a hook's panic is logged | `HookContainerPanic` receives both; a panic it raises propagates |
| Health classification | Tag readiness, critical | `WithHealthTags(...)`, `AsOptionalHealth()` |
| Report coverage | Every health checker | `MatchHealthTags(...)` |
| Status-change hooks | None registered | `HookServiceHealthChanged`, `HookContainerHealthChanged`, `Add*Hook` |
| `RootScope.HealthCheck()` coverage | Root scope and installed extension scopes (unchanged); doc comment corrected | `RootScope.HealthReport()` for the whole tree |

## Defect

First reproduced at `2012c60` with probe tests in a scratch copy. **Re-verified on `main` at `76404ef` (2026-10-28):** all nine still fail, from one probe file with no goleak noise. Each becomes a failing regression test before its fix.

| # | Observed on `76404ef` | Expected | Minimal reproduction |
|---|---|---|---|
| D1 | 100 calls leave 100 extra goroutines | At most one check in flight per service | `HealthCheckTimeout: 5ms`, one `Healthchecker` that blocks; 100 × `HealthCheck()` |
| D2 | `HealthCheckWithContext(20ms ctx)` and `ProvideNamedValue` are both still blocked at 300ms | The call returns when its ctx ends; registration is never blocked | `HealthCheckParallelism: 1`, one blocking check, 1,001 concurrent `HealthCheck()` |
| D3 | `Shutdown` is still blocked at 300ms after a timed-out lazy check | Shutdown is not delayed by a health check | `HealthCheckTimeout: 5ms`, invoked lazy service whose check blocks; `HealthCheck()` then `Shutdown()` |
| D4 | The shadowed root check is still called (rootCalls=1) | It is not checked | Root and child both register `"svc"`; `child.HealthCheck()` |
| D5 | An alias runs the check twice | Once | `ProvideValue(&checker)`, `As[*checker, Healthchecker]`; `HealthCheck()` |
| D6 | 4 extra goroutines after `New` | None | `NewWithOpts(&InjectorOpts{HealthCheckParallelism: 4})` |
| D7 | The process exits (`panic: boom` in the goroutine started by `awaitHealthCheck`) | An error matching `ErrHealthCheckPanic` | A `Healthchecker` panicking `"boom"`; `HealthCheck()` with a deferred `recover()` |
| D8 | `root.HealthCheck()` returns `map[]` for a child-scope service; the doc comment says it covers descendants | The doc comment matches the behavior (root and extension scopes) | A service in child scope `"child"` |
| D9 | `HealthCheckWithContext(20ms ctx)` is still blocked at 300ms. Since `76404ef` the block is in the queued goroutine (`serviceHealthCheckFunc` → `serviceLazy.isHealthchecker` → `RLock`), before ctx is watched | Health checks never wait for a build in progress | A lazy provider blocked in another goroutine's `Invoke` |

**Checked and found correct on `main`** (no fix proposed; kept as regression rows): an exported checker is checked once from the root and from its source scope; a yielded `AsDefault` checker is never checked; collection members are checked once each; `Refresh` keeps the phase at Ready.

**Not reproduced (no fix proposed):** a race between `HealthCheck` and root `Shutdown` on the pool (2 probes under `-race`). The pool is replaced anyway (D2, D6).
