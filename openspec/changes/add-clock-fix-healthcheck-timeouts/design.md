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
- Scope-wide checks queue **every** service of the scope and its ancestors, not only health checkers. Every wrapper implements `healthcheck`, so `serviceHealthCheck` never reaches its panic. The wrapper returns `nil` for a value with no check, a lazy service not yet built, and a transient (`@TODO`). An alias delegates to its target.
- `serviceEager`/`serviceLazy.healthcheck` return a bare `ctx.Err()` when the context is already done. Wrapper-level tests assert that bare error directly (`service_eager_test.go` lines 331, 341, 363, 415, 425, 446, 468), so the wrappers keep returning it. The timeout error is built one level up.
- The timeout error is built in two places as `fmt.Errorf("%w: %s", ErrHealthCheckTimeout, ctx.Err())`, so `errors.Is(err, context.DeadlineExceeded)` is false.

**Sources of nondeterminism.**
- Each `select` with both cases ready picks one at random. The deadline-free direct call returns whatever the check returned, so a done context can yield `nil` or a bare `ctx.Err()`.
- The non-pool `queueServiceHealthcheck` select also races for services with no check, whose result is an immediate `nil` (Defect 3).
- A check that ends at the same instant as its deadline races the context's own cancellation goroutine. Checking "is the context done" after the result arrives does not settle it (Defect 4: 740/2,000 on a fake clock, 591/2,000 on the real clock in a bubble).
- Proposal → Defect lists the measured rates.

**Time reads in library code**
- `context.WithTimeout`: `scope.go` (global) and `root_scope.go` (per service).
- `time.Now`/`time.Since`: `service_lazy.go` (build time), `scope.go` (`ShutdownTime`, per-service `ServiceShutdownTime`).

**`github.com/jonboulle/clockwork` v0.5.0**
- Requires go 1.21 and has no dependencies.
- `clockwork.WithTimeout(parent, clock, d)` delegates to `context.WithTimeout` for a non-fake clock.
- For a `*FakeClock` it returns a context driven by the fake clock, with one goroutine per context that ends when the context is done or cancelled. Verified by probe (scratch, outside the repo):
  - **`Err()` blocks until the context is done.** This breaks the `context.Context` contract, which returns `nil` while live. `if ctx.Err() != nil` on a live fake-clock context hangs.
  - On deadline, `Err()` is `ErrFakeClockDeadlineExceeded`, with the message `clockwork.FakeClock: context deadline exceeded`; it wraps `context.DeadlineExceeded`.
  - A fake-clock child context is done when its fake-clock parent's deadline passes. The v0.5.0 code propagates every parent cancellation, despite a doc comment saying otherwise. So the shorter of the global and per-service timeouts wins. (Re-checked against the v0.5.0 source: `runCancel` copies `parent.Err()` for any parent cancellation.)
  - `Deadline()` on a fake-clock context returns only its own deadline, not the minimum with its parent's.
  - `FakeClock.Advance` sets the clock's time to each timer's expiry before firing it, under the clock's lock. A goroutine woken by that timer therefore reads `Now()` at or after the expiry.
  - `NewFakeClock()` starts at the real `time.Now()`. Advancing it can pass a deadline a consumer set on the real clock, although that context is still live.

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
- The six wall-clock-sensitive health-check tests, and `TestMixedServiceTypesPerformance`, become deterministic (D5) without changing their fixtures or assertions.
- A service without a health check is never reported as timed out (D7).
- A check ending exactly at a container-set deadline is always a timeout (D8).

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
- D7 narrows where the rule applies (services with a check only), and D8 extends "done" with a clock comparison for container-set deadlines.
- *Alternative:* only wrap error results returned while the context is done ("normalize errors only"). Rejected by decision: it leaves the healthy-vs-timeout tie random in real time.

**D2. Never call `Err()` on a context that may be live.**
- `contextDone(ctx)` uses `select { case <-ctx.Done(): return ctx.Err(); default: return nil }`. `Err()` is called only after `Done` is closed, which is safe for clockwork fake contexts as well.
- The `serviceEager`/`serviceLazy.healthcheck` pre-checks (`if ctx.Err() != nil`) switch to it. Otherwise every fake-clock health check would hang. They keep returning the bare context error (Context: wrapper-level tests).

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
- Fake-clock tests whose checks are abandoned on timeout advance the fake clock past every pending check before the bubble ends (a bubble `time.Sleep` does not move a fake clock).
- Exception: a test whose context carries a deadline on the real clock that must *not* fire (the consumer-deadline scenario) runs outside a bubble. In a bubble, once every goroutine blocks, bubble time would jump to that deadline. It waits on the fake clock with `BlockUntilContext` instead.
- `tests/service_types_test.go: TestMixedServiceTypesPerformance` is wrapped the same way (no `testWithTimeout`, assertions unchanged). In the bubble, eager invocations take 0 bubble time, lazy ones 1 ms (one build sleep), and transient ones 100 ms, so both `Less` assertions hold on every run. It then checks ordering semantics, not speed; `bench/` measures speed.
- *Alternative:* fake clock only, with `BlockUntilContext` waiter counts. Rejected: brittle counts that depend on internal goroutine structure.

**D6. Dependency and docs.**
- `go get github.com/jonboulle/clockwork@v0.5.0` in the root module, then `go mod tidy` in all 25 workspace modules (additions only).
- The "no dependencies except the Go std lib" statements become "minimal dependencies", naming `samber/go-type-to-string` and `jonboulle/clockwork`. Locations found by the re-audit (case-insensitive search):
  - `README.md` lines 64 ("Lightweight, no dependencies") and 76;
  - `docs/docs/getting-started.md` line 18;
  - `docs/docs/about.md` lines 31 ("zero external dependencies") and 99;
  - `docs/docs/comparison/samber-do-vs-uber-fx.md` line 9 (verdict);
  - `docs/docs/migrating/migrating-from-dig.md` line 402 ("No external dependencies").
- `docs/docs/container/options.md` and `docs/docs/service-lifecycle/healthchecker.md` document `Clock`, the timeout rule (including D7 and D8), and the fake-clock caveat (Risks).

**D7. A service without a health check is never a timeout.**
- `serviceHealthCheck` asks the wrapper `isHealthchecker()` once, before running anything. When it reports false, the result is `nil`, without racing and without consulting the context. Every wrapper already implements `isHealthchecker` (`serviceWrapperIsHealthchecker`).
- That snapshot is final for the call. A lazy service built while the call is in flight is still reported `nil`, consistent with "not a health checker when its check began".
- The non-pool `queueServiceHealthcheck` goroutine stops making its own decision in the `ctx.Done()` branch. It routes through the same decision as `raceWithTimeout` (2.3 refactor), so it cannot report a timeout for a service with no check.
- Service lookup stays first: an unknown name returns `ErrServiceNotFound` before D7 or D1 apply.
- *Alternative:* apply D1 to every service (always a timeout on a done context). Rejected by decision: it reports a timeout for a check that never ran, and contradicts the existing comment in `serviceHealthCheck`.

**D8. Container-set deadlines are also judged on the injector's clock.**
- The container records the earliest deadline it set for a check, as `clock.Now().Add(timeout)`, wherever it applies `HealthCheckGlobalTimeout` (`scope.go`) and `HealthCheckTimeout` (`root_scope.go`). It carries that deadline in an unexported context value, keeping the earlier one when both apply.
- After a result arrives, `healthCheckResult` treats the check as timed out when the context is done (D1), **or** when a container deadline is present and `!clock.Now().Before(deadline)`. In the second case the cause is `context.DeadlineExceeded` (D3), because the context may not be marked done yet.
- The clock is passed explicitly: `serviceHealthCheck` reads it from `s.rootScope.opts.Clock`.
- A deadline carried only by the consumer's context is never compared with the injector's clock. It counts when the context is done. This keeps a fake injector clock from expiring a consumer's real-time deadline (see Context: `NewFakeClock()` starts at the real `time.Now()`).
- Implemented in group 3, with the clock. Group 2 keeps the plain "context done" rule.
- Probe evidence (proposal, Defect 4): 2,000/2,000 timeouts at the limit and 0/2,000 one step before, on a fake clock and on the real clock in a bubble.
- *Alternatives:*
  - compare any `ctx.Deadline()` with the injector's clock: rejected by decision; a fake clock can pass a live consumer deadline;
  - leave the exact-limit instant unspecified: rejected by decision; it keeps the nondeterminism this change removes.

## Risks / Trade-offs

- [A check that finished just before the deadline but was observed after it is reported as a timeout] → Accepted by decision. It removes a random outcome, and the window is the scheduling delay.
- [Consumer `HealthcheckerWithContext` code that calls `ctx.Err()` on a live fake-clock context hangs (clockwork behavior)] → Documented in the healthchecker and options pages: use `select` on `ctx.Done()`. Real-clock contexts are unaffected.
- [A consumer passes a fake-clock context to `ShutdownWithContext`, whose shutdown paths still call `ctx.Err()`] → Out of scope (Non-Goals). Documented as the same caveat. The container itself never builds fake contexts for shutdown.
- [Fake-clock contexts start one goroutine each] → Every `clockwork.WithTimeout` is paired with `defer cancel()`, as today. goleak in `TestMain` checks this.
- [The exported API now exposes a third-party type; a clockwork major version would be a doger break] → Accepted by decision. Pinned at v0.5.0, and Dependabot will raise updates.
- [Bubble time hides real performance regressions in the converted tests] → They test semantics, not speed. `bench/` keeps the health-check benchmark on real time.
- [A scope-wide check on a done context now reports `nil` for services with no check, where it used to report a timeout about 998 times in 1,000 (D7)] → Accepted by decision. The previous result was random and contradicted the code comment. Documented in the healthchecker page and the migration note.
- [A check that returned in time but is observed after its container deadline on the clock is a timeout (D8)] → Same trade-off as the first risk, now including the exact limit.
- [Merge overlap with `add-start-lifecycle-and-kinds` (in progress on its own branch as of `d4b809b`)] → That branch adds `RootScope` fields and restructures `RootScope.ShutdownWithContext` in `root_scope.go`. It adds no production time reads and no health-check code. Expect a textual conflict in the `RootScope` struct and imports only. Whichever change merges second resolves it, and keeps any new time read on `opts.Clock`.

## Public API & Defaults

- **Added:** `InjectorOpts.Clock clockwork.Clock`.
  - Default (nil, `New()`, `NewWithOpts(nil)`): `clockwork.NewRealClock()`, which is today's wall-clock behavior.
  - Override: any `clockwork.Clock`, typically `clockwork.NewFakeClock()`.
  - Child scopes and `Clone()` share it; `CloneWithOpts` takes it from its options.
- **Changed behavior (no signature change):** health-check results on a done context are always the timeout error.
  - The message is unchanged in format (`DI: health check timeout: <context canceled | context deadline exceeded>`).
  - It now also matches `context.Canceled`/`context.DeadlineExceeded` with `errors.Is`.
  - Migration: none needed for `errors.Is(err, ErrHealthCheckTimeout)` users. Code that compared against a bare `context.Canceled` with `==` must use `errors.Is`.
  - Services with no health check always report `nil`, on a done context too (D7).
  - A check observed at or after a container-set deadline on the injector's clock is a timeout (D8).
- **New runtime dependency:** `github.com/jonboulle/clockwork v0.5.0`.
- **Not breaking under the Go 1 compatibility rules:** the added struct field breaks only unkeyed `InjectorOpts{...}` literals, which no doc or example uses.

## Test Strategy

Every `#### Scenario:` maps to one Go test, and each `Examples:` row is a named subtest. New tests live in `healthcheck_timeout_test.go` and `clock_test.go` (package `doger`), all run under `-race` with goleak.

| Scenario | Test | Kind |
|---|---|---|
| Health check on an ended context is a timeout (8 rows) | `healthcheck_timeout_test.go: TestHealthCheck_EndedContextIsTimeout/<row>`. Each row loops 1,000 calls; "during the check" rows run inside `synctest.Test`, cancelling after `synctest.Wait()` | table / race / synctest |
| Health check that finishes before its context ends keeps its result (5 rows) | `healthcheck_timeout_test.go: TestHealthCheck_FinishedInTimeKeepsResult/<row>` | table |
| Service without a health check on an ended context (8 rows) | `healthcheck_timeout_test.go: TestHealthCheck_ServiceWithoutCheckNeverTimesOut/<row>`; each row loops 1,000 calls | table / race |
| Timeout options decide the result on the clock (9 rows) | `healthcheck_timeout_test.go: TestHealthCheck_TimeoutOptionsOnClock/<row>` (fake clock in `synctest.Test`; each row loops 1,000 runs) | table / synctest |
| A consumer's own deadline is not measured on the injector's clock (2 rows) | `healthcheck_timeout_test.go: TestHealthCheck_ConsumerDeadlineOnContextOnly/<row>` (not in a bubble; waits with `BlockUntilContext`) | table / race |
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
- `TestHealthCheck_ServiceWithoutCheckNeverTimesOut` fails on the current code in its scope-wide rows (about 998/1,000 timeouts). Its per-service and "missing" rows pass on arrival and pin existing behavior.
- The exact-limit row of `TestHealthCheck_TimeoutOptionsOnClock` (10 ms / 10 ms) proves D8. With D1 alone it fails about a third of its 1,000 runs (Defect 4).
- `TestMixedServiceTypesPerformance` is checked with `go test -race -count=1000 -run '^TestMixedServiceTypesPerformance$' ./tests` before (≥1 failure expected; measured 1/50) and after (0 failures).
- The flaky test itself is checked with `go test -race -count=1000 -run '^TestRootScope_queueServiceHealthcheck$' .` before (≥1 failure expected over repeated batches, as measured: 4/1000) and after (0 failures).

**Converted tests (D5), with fixtures and assertions unchanged:**
- `root_scope_test.go: TestRootScope_queueServiceHealthcheck`;
- `scope_test.go: TestScope_HealthCheckWithContextExpiration_{Timeout,Cancellation,MultipleServices,ChildScopes,GlobalTimeoutOption}`;
- `tests/service_types_test.go: TestMixedServiceTypesPerformance`.

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
2. Defect fix (D1–D3, D7): RED → GREEN → REFACTOR.
3. Clock option (D4, D8): RED → GREEN → REFACTOR.
4. Convert the timing tests (D5), including `TestMixedServiceTypesPerformance`.
5. Examples and docs (D6).
6. Integration checks.

Rollback: revert the commit series. The new option is additive.
