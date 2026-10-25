# Design

## Context

See proposal.md (Why, Defect) for the motivation, and `specs/health-check-timeouts/spec.md` and `specs/container-clock/spec.md` for the required behavior. This change builds on the archived `upgrade-go-1-27` (#2: Go 1.27 floor, so `testing/synctest` and `sync.WaitGroup.Go` are available).

**How a health check is decided today**
- Scope-wide checks: `(*Scope).HealthCheckWithContext` optionally wraps the context in `context.WithTimeout(HealthCheckGlobalTimeout)`. It then calls `(*RootScope).queueServiceHealthcheck` for each service.
- `queueServiceHealthcheck` optionally adds `context.WithTimeout(HealthCheckTimeout)`. Then:
  - without a pool, a goroutine selects between the check result and `ctx.Done()`;
  - with a pool (`HealthCheckParallelism > 0`), `jobPool.rpc` runs the check sequentially.
- Both paths end in `(*Scope).serviceHealthCheck` → `raceWithTimeout(ctx, service.healthcheck)`. When `ctx` has a deadline, that selects between the result and `ctx.Done()`; without a deadline it calls `fn(ctx)` directly.
- Per-service functions (`HealthCheck*`) call `serviceHealthCheck` directly. Neither `HealthCheckTimeout` nor `HealthCheckGlobalTimeout` applies to them. That is unchanged.
- `serviceEager`/`serviceLazy.healthcheck` return a bare `ctx.Err()` when the context is already done.
- The timeout error is built in two places as `fmt.Errorf("%w: %s", ErrHealthCheckTimeout, ctx.Err())`, so `errors.Is(err, context.DeadlineExceeded)` is false.

**Sources of nondeterminism.** Each `select` with both cases ready picks one at random. The deadline-free direct call returns whatever the check returned, so a done context can yield `nil` or a bare `ctx.Err()`. Proposal → Defect lists the measured rates.

**Time reads in library code**
- `context.WithTimeout`: `scope.go` (global) and `root_scope.go` (per service).
- `time.Now`/`time.Since`: `service_lazy.go` (build time), `scope.go` (`ShutdownTime`, per-service `ServiceShutdownTime`).

**`github.com/jonboulle/clockwork` v0.5.0**
- Requires go 1.21 and has no dependencies.
- `clockwork.WithTimeout(parent, clock, d)` delegates to `context.WithTimeout` for a non-fake clock.
- For a `*FakeClock` it returns a context driven by the fake clock, with one goroutine per context that ends when the context is done or cancelled. Verified by probe (scratch, outside the repo):
  - **`Err()` blocks until the context is done.** This breaks the `context.Context` contract, which returns `nil` while live. `if ctx.Err() != nil` on a live fake-clock context hangs.
  - On deadline, `Err()` is `ErrFakeClockDeadlineExceeded`, with the message `clockwork.FakeClock: context deadline exceeded`; it wraps `context.DeadlineExceeded`.
  - A fake-clock child context is done when its fake-clock parent's deadline passes. The v0.5.0 code propagates every parent cancellation, despite a doc comment saying otherwise. So the shorter of the global and per-service timeouts wins.

**Timing tests**
- `TestRootScope_queueServiceHealthcheck` uses `lazyTestHeathcheckerOKTimeout` (`time.Sleep(20ms)`) with 10/50 ms timeouts.
- The five `TestScope_HealthCheckWithContextExpiration_*` tests use `scopeTestSlowHealthchecker` (`time.NewTimer`) and assert wall-clock bounds such as `duration < 70ms`.
- A synctest probe of the first test had 0 failures at `:119`/`:131` in 4,000 runs. It also showed two things:
  - timed-out checks keep sleeping after the call returns, so a bubble must let fake time run on before it ends;
  - the 4th case could still see a bare `ctx.Err()`. That is the defect fixed here.

## Goals / Non-Goals

**Goals:**
- One rule decides every health-check result (D1), on every path, independent of scheduling.
- Every time read the container makes goes through `InjectorOpts.Clock` (D3). With the default real clock, behavior is unchanged apart from D1.
- The six wall-clock-sensitive tests become deterministic (D5) without changing their fixtures or assertions.

**Non-Goals:**
- Applying `HealthCheckTimeout`/`HealthCheckGlobalTimeout` to the per-service `HealthCheck*` functions (existing behavior, separate decision).
- Cancelling a running check that ignores its context. It still runs to completion in its goroutine, as today.
- Replacing `ctx.Err()` in the shutdown paths of `serviceEager`/`serviceLazy`. The container never creates fake-clock contexts there. A consumer passing a fake-clock context to `ShutdownWithContext` is noted under Risks.
- Health-check panic behavior, and the `tests/` package health-check tests: they assert no timeout outcome, only a hang guard.

## Decisions

**D1. "Deadline wins" in one helper.**
- One unexported function `healthCheckResult(ctx, err) error`, plus a non-blocking `contextDone(ctx) error` (D2), decides every result:
  - if the context is done when the result is observed, return the timeout error built by `timeoutError(ctx)`;
  - otherwise return the check's own `err` (nil or an error).
- It is applied:
  - in `raceWithTimeout`, after the result arrives, and on the deadline-free direct call;
  - in the `ctx.Done()` branches of `raceWithTimeout` and of the non-pool `queueServiceHealthcheck` goroutine.
- The pool path ends in `serviceHealthCheck` → `raceWithTimeout`, so it is covered too.
- *Alternative:* only wrap error results returned while the context is done ("normalize errors only"). Rejected by decision: it leaves the healthy-vs-timeout tie random in real time.

**D2. Never call `Err()` on a context that may be live.**
- `contextDone(ctx)` uses `select { case <-ctx.Done(): return ctx.Err(); default: return nil }`. `Err()` is called only after `Done` is closed, which is safe for clockwork fake contexts as well.
- The `serviceEager`/`serviceLazy.healthcheck` pre-checks (`if ctx.Err() != nil`) switch to it. Otherwise every fake-clock health check would hang.

**D3. `timeoutError(ctx)` normalizes the cause.**
- It returns `fmt.Errorf("%w: %w", ErrHealthCheckTimeout, cause)`, where `cause` is `context.DeadlineExceeded` whenever `errors.Is(ctx.Err(), context.DeadlineExceeded)`, else `ctx.Err()`.
- Result: the message is the same on any clock (`DI: health check timeout: context deadline exceeded`), and `errors.Is` matches both the sentinel and the standard context error.
- `errors.Is(err, clockwork.ErrFakeClockDeadlineExceeded)` does not match. No consumer can depend on that yet.
- The two `//nolint:errorlint` comments go away.

**D4. Clock plumbing through `InjectorOpts`.**
- `InjectorOpts.Clock clockwork.Clock`. `NewWithOpts` fills `nil` with `clockwork.NewRealClock()`, like the other defaults. `copy()` copies it, so `Clone()` shares the clock and `CloneWithOpts` uses the given options.
- Readers:
  - global timeout: `clockwork.WithTimeout(ctx, root.opts.Clock, HealthCheckGlobalTimeout)` in `scope.go`;
  - per-service timeout: the same in `root_scope.go`;
  - shutdown and per-service shutdown times: `Clock.Now`/`Clock.Since` in `scope.go`;
  - lazy build time: `serviceLazy.build(i)` reads the clock through `i.RootScope()`, because a lazy service does not know its scope until it is built.
- Child scopes reach the clock via `rootScope.opts`.
- *Alternatives:*
  - a doger-owned `Clock` interface: rejected by decision; `clockwork.Clock` is exposed directly;
  - reading the clock from the context (`clockwork.FromContext`): rejected, because it is implicit and the standard `context.WithTimeout` ignores it.

**D5. Deterministic tests with `testing/synctest`.**
- Real-clock timing tests run inside `synctest.Test`, so the existing fixtures (`time.Sleep`, `time.NewTimer`) use bubble time.
- Fake-clock tests also run inside `synctest.Test` and call `synctest.Wait()` before each `FakeClock.Advance`. That waits until every goroutine (the check, the clockwork context goroutines, the pool) is durably blocked, so no waiter counting with `BlockUntilContext` is needed.
- Bubble tests:
  - do not use `testWithTimeout`: its `time.After` runs on bubble time and would fire falsely after large fake advances. synctest reports deadlocks itself, and `go test -timeout` covers the rest;
  - end with a `time.Sleep` (bubble time) longer than any abandoned check, so goroutines left by timed-out checks finish before the bubble returns;
  - release blocking fixtures and call `Shutdown` before returning, so goleak in `TestMain` stays clean.
- *Alternative:* fake clock only, with `BlockUntilContext` waiter counts. Rejected: brittle counts that depend on internal goroutine structure.

**D6. Dependency and docs.**
- `go get github.com/jonboulle/clockwork@v0.5.0` in the root module, then `go mod tidy` in all 25 workspace modules (additions only).
- The "no dependencies except the Go std lib" statements become "minimal dependencies", naming `samber/go-type-to-string` and `jonboulle/clockwork`, in README, getting-started, about, and the uber-fx comparison verdict.
- `docs/docs/container/options.md` and `docs/docs/service-lifecycle/healthchecker.md` document `Clock`, the timeout rule, and the fake-clock caveat (Risks).

## Risks / Trade-offs

- [A check that finished just before the deadline but was observed after it is reported as a timeout] → Accepted by decision. It removes a random outcome, and the window is the scheduling delay.
- [Consumer `HealthcheckerWithContext` code that calls `ctx.Err()` on a live fake-clock context hangs (clockwork behavior)] → Documented in the healthchecker and options pages: use `select` on `ctx.Done()`. Real-clock contexts are unaffected.
- [A consumer passes a fake-clock context to `ShutdownWithContext`, whose shutdown paths still call `ctx.Err()`] → Out of scope (Non-Goals). Documented as the same caveat. The container itself never builds fake contexts for shutdown.
- [Fake-clock contexts start one goroutine each] → Every `clockwork.WithTimeout` is paired with `defer cancel()`, as today. goleak in `TestMain` checks this.
- [The exported API now exposes a third-party type; a clockwork major version would be a doger break] → Accepted by decision. Pinned at v0.5.0, and Dependabot will raise updates.
- [Bubble time hides real performance regressions in the converted tests] → They test semantics, not speed. `bench/` keeps the health-check benchmark on real time.

## Public API & Defaults

- **Added:** `InjectorOpts.Clock clockwork.Clock`.
  - Default (nil, `New()`, `NewWithOpts(nil)`): `clockwork.NewRealClock()`, which is today's wall-clock behavior.
  - Override: any `clockwork.Clock`, typically `clockwork.NewFakeClock()`.
  - Child scopes and `Clone()` share it; `CloneWithOpts` takes it from its options.
- **Changed behavior (no signature change):** health-check results on a done context are always the timeout error.
  - The message is unchanged in format (`DI: health check timeout: <context canceled | context deadline exceeded>`).
  - It now also matches `context.Canceled`/`context.DeadlineExceeded` with `errors.Is`.
  - Migration: none needed for `errors.Is(err, ErrHealthCheckTimeout)` users. Code that compared against a bare `context.Canceled` with `==` must use `errors.Is`.
- **New runtime dependency:** `github.com/jonboulle/clockwork v0.5.0`.
- **Not breaking under the Go 1 compatibility rules:** the added struct field breaks only unkeyed `InjectorOpts{...}` literals, which no doc or example uses.

## Test Strategy

Every `#### Scenario:` maps to one Go test, and each `Examples:` row is a named subtest. New tests live in `healthcheck_timeout_test.go` and `clock_test.go` (package `doger`), all run under `-race` with goleak.

| Scenario | Test | Kind |
|---|---|---|
| Health check on an ended context is a timeout (8 rows) | `healthcheck_timeout_test.go: TestHealthCheck_EndedContextIsTimeout/<row>`. Each row loops 1,000 calls; "during the check" rows run inside `synctest.Test`, cancelling after `synctest.Wait()` | table / race / synctest |
| Health check that finishes before its context ends keeps its result (5 rows) | `healthcheck_timeout_test.go: TestHealthCheck_FinishedInTimeKeepsResult/<row>` | table |
| Timeout options decide the result on the clock (9 rows) | `healthcheck_timeout_test.go: TestHealthCheck_TimeoutOptionsOnClock/<row>` (fake clock in `synctest.Test`) | table / synctest |
| Sequential health checks share the global timeout | `healthcheck_timeout_test.go: TestHealthCheck_SequentialChecksShareGlobalTimeout` | synctest / race |
| Health check does not time out before the clock reaches the timeout | `healthcheck_timeout_test.go: TestHealthCheck_NotBeforeClockReachesTimeout` | synctest |
| Zero configuration measures health-check timeouts on wall time (5 rows) | `clock_test.go: TestClock_DefaultRealClock/<row>` (`synctest.Test`) | table / synctest |
| Reported durations come from the clock (4 rows) | `clock_test.go: TestClock_ReportedDurations/<row>` (the provider or `Shutdown` calls `fc.Advance`) | table |
| Timeout errors read the same on any clock (2 rows) | `clock_test.go: TestClock_TimeoutErrorSameOnAnyClock/<row>` | table / synctest |
| Health-check timeouts wait for the clock, not for wall time (2 rows) | `clock_test.go: TestClock_TimeoutWaitsForClock/<row>` (50 ms of real wall time, fake clock not advanced; not in a bubble) | table / race |
| Derived injectors measure timeouts on the expected clock (4 rows) | `clock_test.go: TestClock_DerivedInjectors/<row>` | table / synctest |
| Default and override documented | `clock_example_test.go: ExampleInjectorOpts_clock` (fake clock, prints the timeout message) | example |

**Regression proof (rule 5):**
- `TestHealthCheck_EndedContextIsTimeout` fails on the current code in every row on its `errors.Is(err, <ctx error>)` assertion (`%s` wrapping).
- The per-service cancelled rows also fail on the message assertion, because they return the bare `context canceled`.
- The flaky test itself is checked with `go test -race -count=1000 -run '^TestRootScope_queueServiceHealthcheck$' .` before (≥1 failure expected over repeated batches, as measured: 4/1000) and after (0 failures).

**Converted tests (D5), with fixtures and assertions unchanged:**
- `root_scope_test.go: TestRootScope_queueServiceHealthcheck`;
- `scope_test.go: TestScope_HealthCheckWithContextExpiration_{Timeout,Cancellation,MultipleServices,ChildScopes,GlobalTimeoutOption}`.

**Hot paths and coverage:**
- **Scope-wide health checks** (pool and non-pool), `queueServiceHealthcheck` and `raceWithTimeout`: the new tables, plus the existing `TestScope_HealthCheck*`/`TestHealthCheck*` suites.
- **Per-service health checks:** the `EndedContextIsTimeout` and `FinishedInTimeKeepsResult` rows.
- **Lazy build and shutdown timing:** `TestClock_ReportedDurations`, plus the existing explain and shutdown suites.
- **Scope traversal:** the child-scope and inherited-service rows.
- **Concurrency:** everything under `-race` and goleak, and the converted tests at `-count=1000`.

**Behavior preservation:**
- Inventory diff against a baseline: additions only.
- `make test` and `make lint` (0 issues).
- Per-module `go vet ./... && go test -race ./...` in all 25 modules.
- A clean `go mod tidy`.
- `cd docs && npm run lint && npm run build`.

## Migration Plan

1. Baseline: record the inventory and measure the flake rate.
2. Defect fix (D1–D3): RED → GREEN → REFACTOR.
3. Clock option (D4): RED → GREEN → REFACTOR.
4. Convert the timing tests (D5).
5. Examples and docs (D6).
6. Integration checks.

Rollback: revert the commit series. The new option is additive.
