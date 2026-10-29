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
- **Bounds on a read.** A read is bounded by the caller's context and by `HealthCheckTimeout`. When `HealthCheckTimeout` is 0, the bound is `DefaultHealthReportCheckTimeout` (10s), so `HealthReport()` never hangs on a check that ignores its context. When a check waits for a `HealthCheckParallelism` slot, this per-service bound starts when the check starts running.
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
  - **Overrides:** the interval, any gocron job definition such as a cron expression, when the first round runs, scheduler and job options, or a shared `gocron.Scheduler` the application already runs.

**Executing checks.**
- **One check in flight per service.** A service never has two checks in flight. A caller that finds a check in flight waits for it, within its own bound, instead of starting another. This caps check goroutines at one per service.
- **Own bounds and the starter's context.** Each caller keeps its own context and bounds, as today. A check sees the context values and deadline of the caller that started it.
- **Parallelism limit.** `HealthCheckParallelism` limits the checks started by `HealthCheck()`, report reads and rounds, through a context-aware limiter. The single-service functions keep running at once without a slot, as today. When the check they need is waiting for a slot, they run it immediately, so composite checks never deadlock under a limit. A check holds its slot until it returns, its context ends, or the report bound has passed since it took the slot, so a hung check never stalls the checks queued behind it (code review R13).
- **Recording.** Every completed check refreshes the report. A check whose report-path bound elapsed is recorded as timed out, and its late result is discarded, so a slow check does not flap.

**Status-change hooks.**
- **Fields and adders.** New `InjectorOpts.HookServiceHealthChanged` and `HookContainerHealthChanged`, with `AddServiceHealthChangedHook` and `AddContainerHealthChangedHook`.
- **When they fire.** They are called on state changes, one at a time, in recording order, with no lock held.
- **Panics.** A hook's panic goes to `HookContainerPanic` or is logged (flagged).
- **No ctx.** The hooks take no ctx, like `HookContainerPanic`: they are delivered off any caller's request.

**Defect fixes D1-D9** (see Defect), each starting with its failing regression test.

**Behavior changes for existing consumers (flagged):**
1. A panicking health check returns an error matching the new `ErrHealthCheckPanic` instead of crashing the process. A panic that `HookContainerPanic` raises for it is re-raised on the goroutine of every caller waiting for that check, which can recover it; with no caller waiting, it is raised on the goroutine running the check.
2. A check may run on a goroutine other than the caller's, and a caller that joins a check another caller started does not pass its context values to it.
3. A lazy service's check no longer holds the service's lock, so a check may run while the service shuts down, as eager services already can. Root `Shutdown` does not wait for checks in flight either: it cancels their contexts and goes on, so a service whose `Shutdown` releases what its check uses must guard against a check still running.
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
| gocron adapter cadence | 30s duration job, first round at once, container clock, singleton (reschedule) mode | `WithInterval`, `WithJobDefinition` (e.g. cron), `WithStartAt`, `WithSchedulerOptions`, `WithJobOptions`, `WithScheduler` (shared scheduler) |
| Report read without a scheduler | Runs stale checks on demand, results reused for 1s, readers share each check | `HealthReportCacheTTL`: positive sets the TTL, negative turns caching off |
| Report read with a scheduler | Cached results; only never-checked services are checked on demand | Call `HealthCheck()` to force a run; it refreshes the report |
| Bound of a check started by a read or a round | `HealthCheckTimeout` when positive; `DefaultHealthReportCheckTimeout` (10s) when 0 | `HealthCheckTimeout`: positive for its own bound, negative for no bound; the read's ctx |
| Container phase in reports | Readiness Unhealthy while Starting, Started, Failed, Closing; liveness Unhealthy when Failed; Idle and Ready ungated | `IgnoreContainerPhase()` per read |
| Services not started yet (after Start) | Listed `HealthUnknown`, not checked by reads or rounds | `Refresh` / `Scope.Start` starts them; forced `HealthCheck()` still checks them |
| Forced `HealthCheck()` | Runs every check; joins checks already in flight | None needed: each caller keeps its own context and bounds; a joining caller's context values do not reach a check another caller started (flagged) |
| At most one check in flight per service | Always | None. A second concurrent check of the same service only adds load and goroutines |
| Concurrency limit | Unlimited | `HealthCheckParallelism` caps checks started by `HealthCheck()`, reads and rounds; single-service functions take no slot |
| Panic in a check, a health hook or the scheduler's stop | Recovered: a check's panic becomes an error matching `ErrHealthCheckPanic`; a hook's or stop's panic is logged | `HookContainerPanic` receives each; a panic it raises for a check is re-raised on every caller waiting for that check (on the check's goroutine when none waits); for a hook or the scheduler, it propagates from the call that runs them |
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

### Review findings (2026-10-28)

A verification pass and a code review of the implementation found the defects below in this change's own code. Each was reproduced with a throwaway probe test that asserted the expected behavior and failed; each fix starts with its regression test (tasks groups 23–29).

| # | Observed | Expected | Minimal reproduction |
|---|---|---|---|
| R1 | records=1 and members=1 remain, and the container hook reports healthy → unhealthy, for a service that no longer exists | A check in flight when its service leaves the container is not recorded and changes no state | A child-scope checker blocks in `child.HealthCheck()`; `child.Shutdown()`; the check then returns an error |
| R2 | Two later rounds time out and record nothing: the monitored report stays `HealthHealthy`, `Err` nil | A report-path wait that reaches its bound records the timeout, also when it waited for a call it could not join | A scheduler, `HealthCheckTimeout` 20ms, a recorded healthy result; the check starts hanging; `HealthCheck()` abandons it; rounds run |
| R3 | `Shutdown` does not return | A re-panicking `HookContainerPanic` leaves scheduling consistent; a later `Shutdown` returns | The scheduler panics; `HookContainerPanic` re-panics; the caller recovers `Start()`, then calls `Shutdown()` |
| R4 | The process exits from the check's goroutine; the caller's `recover` never runs | The hook's panic reaches the waiting callers, which can recover it | A re-panicking `HookContainerPanic`; a panicking checker; `HealthCheckNamed` under `recover` |
| R5 | `DI: health scheduler stop failed: gocron: job not found` on a normal shutdown | Stopping a schedule whose job the shared scheduler no longer holds succeeds | `WithScheduler(S)`; the application shuts `S` down; then the container's `Shutdown` |
| R6 | The shared-mode stop returns while its round still runs | Stop returns once no round of that job runs, within its context | `WithScheduler(S)`; a round blocks; stop is called |
| R7 | One round costs 94ms, 351ms and 1.34s for 500, 1,000 and 2,000 services (×3.8 per doubling) | Linear cost: the container state is kept in constant time per result | 1,000 checkers, every result re-recorded by a round |
| R8 | A panic in the scheduler's stop never reaches `HookContainerPanic` | It is passed to `HookContainerPanic(root, "healthscheduler", v)` when set, logged otherwise | A `HealthScheduler` whose stop panics; `HookContainerPanic` set; `Shutdown()` |

Also closed without a defect: the redundant record pruning under the scope lock (`storeLocked`), log lines for checks that a cached read does not run (500 lines for 10 cached reads of 50 services), the dead `queueServiceHealthcheck`/`queueWrapperHealthcheck` helpers, the godoc of the flagged behavior changes, and the timing of the container hook for a service that appears (the next round or root report, as in alexliesenfeld/health, .NET and Spring Boot, which evaluate aggregate health only when checks run or are read).

### Second review findings (2026-10-28)

A code review of PR #9 reported eight candidates. Each was checked with a throwaway probe test in a scratch copy of the repository before planning; design.md (Implementation notes) records every probe and its result. Three are defects:

| # | Observed | Expected | Minimal reproduction |
|---|---|---|---|
| R9 | A report read recorded a 9s check as timed out at 10s though it ran 8s, because it waited 2s for the slot; with three 6s checks, two were recorded timed out, one without ever running | A caller's per-service bound starts when its check starts running; the wait for a slot counts only against its context and `HealthCheckGlobalTimeout` | `HealthCheckParallelism: 1`, checks taking 9s and 2s; `HealthReport()` |
| R10 | A check started by `HealthCheck()` ran while another held the only slot; a single-service caller's check got a 50ms deadline although `HealthCheckSingleTimeout` is negative | A caller that waited behind a check it could not join keeps its own path's slot rule and bound in the fresh check | A single-service call abandons a check; `HealthCheck()` queues behind it while another check holds the slot |
| R11 | A child-scope service shut down after a root read listed it, before its check started, was recorded and kept as a member; the container state stayed Unhealthy | A check started for a service that already left is not recorded and changes no state | A `Logf` that shuts the child scope down when the read starts the root service's check |
| R12 | Found while applying R11's fix: a report read that timed out on a check started after its service left recorded the timeout; the service stayed a member and the container state turned Unhealthy | No timeout is recorded for that service, and no state changes | As R11, with "b" hung and `HealthCheckTimeout` 20ms |

Also closed:
- **CR11:** `PendingSince` showed the time a check was requested, while its godoc said "started"; it now means requested, slot wait included, and a recorded timeout's `Duration` runs from when the check started (it counted the slot wait: 10s recorded for 8s of running).
- **CR12:** sorting report entries rebuilt both scope paths on every comparison: a monitored read of 2,000 services took 30,656 allocations; with paths computed once, 6,076.
- **CR13:** the gocron adapter built one task per call that its shared mode discarded.
- **W4:** a Shutdown through the root's own `*Scope` stops scheduling and ends health recording for good while the phase stays unchanged, as health-check-scheduling specifies; this is now documented, with a scenario.
- Not a defect: a report-path timeout recorded while queued behind a check that could not be joined. Such a check's own result is itself a timeout (its context already ended); with that recording removed in a probe, the record was identical.

### Verification findings (2026-10-28)

`/opsx:verify` raised three warnings and three suggestions. Each was checked before planning, with a throwaway probe or gopls; design.md records the evidence. One is a regression from `76404ef`:

| # | Observed | Expected | Minimal reproduction |
|---|---|---|---|
| W2 | `HealthCheckWithContext` with an already-cancelled context started the check in 3 runs out of 40 (none at `76404ef`); `TestScope_HealthCheckWithContextExpiration_ChildScopes` is flaky for the same reason | A caller whose context has already ended, finding no check in flight it can join, starts none, waits for no check started later, and gets its timeout error at once | A context-aware Healthchecker; `HealthCheckWithContext` with a cancelled context |

Also closed:
- **W1:** a caller queued behind an abandoned check keeps its per-service bound running from its call, through the slot wait of the check started for it; this is needed so a hung abandoned check never holds it past its bound (review R2). The R9 requirement now states this exception, with a scenario, and the `HealthCheckParallelism` godoc no longer says that waiting for a slot counts against the per-service bound.
- **W3:** `TestHealthReport_Degraded` and `TestHealthReport_NoRecordAfterShutdown` depend on real time and fail under load; they move to `synctest`, keeping their rows and assertions.
- **S1–S3:** redundant `callWaiter` fields removed, the two registration checks share one helper, and the `healthCheckContext` comment covers its second use.
- **Verification 2:** the HealthReport row of the pile-up scenario turns caching off, so it meets the check in flight instead of a cached result (W1); the test stabilization after group 48 is recorded as task group 49 (S2); this table's W2 row matches the widened requirement (S1). Groups 49–51.

### Code review findings (2026-10-29)

A code review of the working tree after group 51 reported seven findings. Each was checked before planning with a throwaway probe test in scratch copies of the working tree and of `631c942`; design.md (Implementation notes) records every probe and its result. Four are defects; one is a regression from group 34:

| # | Observed | Expected | Minimal reproduction |
|---|---|---|---|
| R13 | `HealthCheck()`, `HealthReport()` and rounds behind a hung check never return: the check keeps its slot, so the per-service bound of the calls queued behind it never starts (all return at `631c942`) | A check gives its slot back once its context ends or the report bound has passed since it took the slot; callers behind it return within their bounds | `HealthCheckParallelism` 1; a hung check abandoned at `HealthCheckTimeout` 1s; `HealthCheck()` |
| R14 | A caller that gave up while queued behind an abandoned check still got a fresh check (2 calls) | No fresh check starts for a caller whose context has ended; it gets its timeout error | Queue behind an abandoned check, cancel, then the abandoned check returns |
| R15 | A report read or round ran the check of a service whose scope shut down after it was listed; the result was discarded | Report reads and rounds start no check for a service that left | The R11 `Logf` |
| R16 | PendingSince of a check started behind an abandoned one was its creation time (00:00:05), not its request (00:00:01) | PendingSince is when the first caller waiting for the check asked | A call queued at 00:00:01 behind a check abandoned at 00:00:00 that returns at 00:00:05 |

Also closed:
- **CR14:** test loops that advance a fake clock until a call returns get a step limit and `testWithTimeout`; one hung the package instead of failing.
- **CR15:** the context of a waiter with a deferred bound gets its own type instead of sharing the shared check's, which only it needed for `Err`.
- **CR16 (no change):** the registration check per finished check costs nothing measurable (`BenchmarkHealthRound` 3.29 ms vs 3.26 ms, p = 0.97).

### Verification 3 findings (2026-10-29)

A third `/opsx:verify` after group 59 reported five warnings and five suggestions. Each was checked before planning with throwaway probe tests and mutation runs in a scratch copy of the working tree; design.md (Verification 3 evidence) records each result. Two are defects:

| # | Observed | Expected | Minimal reproduction |
|---|---|---|---|
| W1 | A caller that joined a check got `DI: health check timeout: context deadline exceeded`, and the record was that timeout, although the check returned nil (or `"down"`): the starter's bound had cancelled the check's context (20 of 20 runs) | A joining caller gets what the check returned within its own bound; only a check returning its ended context's error is a timeout; the record follows | `HealthCheckGlobalTimeout` 30ms; `HealthCheck()` starts a check that ignores its context; `HealthCheckNamed` joins at +10ms; the check returns at +60ms |
| S2 | A stop function returned together with an error by `ScheduleHealthRounds` was never called (20 of 20 runs) | It is called once, before Start returns; the container stays unmonitored | A `HealthScheduler` returning a stop and `errors.New("partial")`; Start, then Shutdown |

Also closed:
- **Spec corrections (W2–W4, S5):** an optional service's report timeout is `HealthDegraded` in health-reports too; the round scenario counts the round's own calls, since the root `HealthCheck()` in its Given already checks the exported "svc"; the Closing-hook row of the root's own `*Scope` path says no Closing hook runs; the TimesWithLimiter row starts the read before the clock advances.
- **Untested sentences (W5, S4):** 13 sentences that a mutant broke with the suite still green get tests: the global timeout on reads and slot waits, a nil ctx, extension scopes in the root report, unstarted Starters in rounds and forced paths, the health hooks of a clone, hook order, nil hooks, a re-panicking scheduler hook, a hook panic with no waiter, and the gocron option precedence and rejected scheduler. `TestHealthCheck_JoinerOwnBound` gets the 29ms row of its forced path, and the order scenario's example is changed so that ordering by the scope's own name fails it.
- **Documented (S1, S3):** the checks cancelled at Shutdown carry the cause `ErrStartAfterShutdown`, while their callers get `context.Canceled`; Shutdown waits for a `ScheduleHealthRounds` call in progress past its deadline, unlike for a Starter, so the schedule's stop always runs.
- **Refuted:** the joined-while-queued variant of W1, "a service that left shows no result" (already caught by `TestHealthReport_NotRecordedAfterLeaving`), `CloneWithOpts` classification (the same code as `Clone()`), and S1's premise that callers see `ErrStartAfterShutdown`.

No default changes and no exported signature changes; `HealthScheduler`'s godoc gains the stop-with-error rule.
