# Proposal

## Why

Whether a health check counts as timed out depends on goroutine scheduling, not only on the check and its timeout:
- `TestRootScope_queueServiceHealthcheck` failed about 4 times in 1,000 runs on `main` when this change was proposed (see Defect).
- The library can return a bare `ctx.Err()` that does not match `ErrHealthCheckTimeout`. For a cancelled context passed to `HealthCheckNamedWithContext` this happens every time.
- A service that has no health check (a plain value, a lazy service not yet built, a transient) is reported as timed out at random by a scope-wide check on a done context. The code comment in `serviceHealthCheck` says it never should be.
- A check that ends exactly at its timeout is reported healthy or timed out at random, even on a controlled clock.

Consumers cannot test timeout behavior without real sleeps, because the container always reads the wall clock. This change makes the outcome deterministic, lets consumers supply the clock, and makes this repository's timing tests deterministic.

## What Changes

- **Behavior change (observable, defect fix):** a health check whose context is done when its result is observed returns the timeout error, whether that check is the per-service `HealthCheck*` functions or `HealthCheck`/`HealthCheckWithContext` on an injector or scope. That holds even when the check itself returned `nil` or a bare `ctx.Err()`. The error wraps both `ErrHealthCheckTimeout` and the context error with `%w`. Its message is unchanged: `DI: health check timeout: <ctx error>`.
  - Before, the result was decided at random when both were ready, and the per-service path with a deadline-free context always returned the bare `ctx.Err()`.
  - `errors.Is(err, context.DeadlineExceeded)` and `errors.Is(err, context.Canceled)` now also match timeout errors (additive).
  - The rule applies only to services that run a check. A service that is not a health checker when the check begins (a plain value, a lazy service not yet built, a transient, an alias to one of these) always returns `nil`, whatever the context. Before, a scope-wide check on a done context reported such a service as timed out about 998 times in 1,000. An unknown service name keeps returning `ErrServiceNotFound`.
  - A result observed once the injector's clock has reached a deadline the container set (`HealthCheckTimeout` or `HealthCheckGlobalTimeout`) is a timeout, even if the context has not been marked done yet. A check that ends exactly at its timeout is therefore always a timeout. Deadlines carried by the consumer's own context are judged only by that context being done.
- New option `InjectorOpts.Clock clockwork.Clock` (`github.com/jonboulle/clockwork`). `nil` means the real clock. The container reads time only from this clock: the `HealthCheckTimeout` and `HealthCheckGlobalTimeout` contexts, lazy build time (`ExplainService…` `ServiceBuildTime`), and `ShutdownReport.ShutdownTime`/`ServiceShutdownTime`.
- New runtime dependency `github.com/jonboulle/clockwork v0.5.0` (no transitive dependencies; requires Go 1.21). The "no dependencies except the Go std lib" statements in README and docs are corrected. They are already inaccurate today: the library imports `github.com/samber/go-type-to-string`.
- Tests: the wall-clock-sensitive health-check tests use `testing/synctest`:
  - `TestRootScope_queueServiceHealthcheck` and the five `TestScope_HealthCheckWithContextExpiration_*` tests;
  - their fixtures (`lazyTestHeathcheckerOKTimeout`, `scopeTestSlowHealthchecker`) are unchanged;
  - their assertions, including duration bounds such as `duration < 70ms`, are unchanged; inside the bubble they hold on every run.
- Tests: `tests/service_types_test.go: TestMixedServiceTypesPerformance` also runs inside `testing/synctest`, with assertions unchanged. It compares wall-clock durations of eager, lazy and transient invocations and fails at random (Defect 5).
- No exported identifier is removed or changed. Adding a field to `InjectorOpts` only breaks unkeyed struct literals of it, which the Go 1 compatibility guidelines exclude.

## Capabilities

### New Capabilities

- `health-check-timeouts`: what a health check returns when its context is done or a configured timeout elapses, and that a check finishing in time keeps its result.
- `container-clock`: the container's time source, `InjectorOpts.Clock`, its default, and which time reads it governs.

### Modified Capabilities

None. `module-identity` requires `ErrHealthCheckTimeout`'s message to stay `DI: health check timeout`, and it does.

## Impact

- Code: `injector.go` (`InjectorOpts.Clock`, defaulting), `root_scope.go` (`queueServiceHealthcheck`), `scope.go` (`HealthCheckWithContext`, `serviceHealthCheck`, shutdown timing), `utils.go` (`raceWithTimeout`), `service_lazy.go` (build time, health-check pre-check), `service_eager.go` (health-check pre-check).
- `go.mod`/`go.sum` of the root module. Every workspace module's `go.sum` (and `go.mod` indirect block) after `go mod tidy`.
- Tests: `root_scope_test.go`, `scope_test.go`, `tests/service_types_test.go`, new `healthcheck_timeout_test.go`, `clock_test.go`, and `Example*` tests for the option.
- Docs: `README.md`, `docs/docs/getting-started.md`, `docs/docs/about.md`, `docs/docs/comparison/samber-do-vs-uber-fx.md`, `docs/docs/migrating/migrating-from-dig.md` (dependency statements), and the health-check page for the clock option and the timeout rule.
- Builds on `upgrade-go-1-27` (merged in #2): `testing/synctest` needs `go 1.25` or later.
- Runs alongside `add-start-lifecycle-and-kinds` (in progress on its own branch). That branch changes `RootScope` fields and `RootScope.ShutdownWithContext` in `root_scope.go`, but adds no production time reads and no health-check code. The expected overlap is a textual merge conflict in `root_scope.go`, resolved by whichever change merges second (design, Risks).

## Defaults & Control

- **Clock.**
  - Default: `nil` → `clockwork.NewRealClock()`, which is the wall-clock behavior consumers get today.
  - Override: set `InjectorOpts.Clock`, for example to `clockwork.NewFakeClock()` in tests.
  - Child scopes and clones use the root scope's clock.
- **Timeout rule.**
  - Default: a check whose context is done, or whose container-set deadline the injector's clock has reached, when its result is observed is a timeout. A service with no health check is never a timeout.
  - Override: none on purpose. The rule removes a random outcome rather than choosing between two valid ones. A consumer who needs the check's own result can call the service's `HealthCheck` method directly.
  - A consumer who matched the bare context error keeps working: `errors.Is(err, context.Canceled)` / `context.DeadlineExceeded` now match the wrapped error.
- **Timeouts themselves.** `HealthCheckTimeout`/`HealthCheckGlobalTimeout` keep their defaults (0 = no timeout) and meaning. They are now measured on the injector's clock.

## Defect

Observed and expected behavior:
1. A health check that outlives its timeout is sometimes reported healthy.
   - `go test -race -count=1000 -run '^TestRootScope_queueServiceHealthcheck$' .` on `main` fails 4 of 1,000 runs at `root_scope_test.go:119` and `:131`: "An error is expected but got nil". The check takes 20 ms and the timeout is 10 ms.
   - The exact scheduling trigger could not be forced. The result depends on which of two `select` cases is ready first: the check result or `ctx.Done()`.
   - Expected: always the timeout error.
2. A health check on a done context returns a bare `ctx.Err()` that does not match `ErrHealthCheckTimeout`. Minimal deterministic reproduction, which returns `context canceled` with `errors.Is(err, ErrHealthCheckTimeout) == false` in 1,000 of 1,000 runs:
   ```go
   i := doger.New()
   doger.ProvideValue(i, &healthyService{}) // HealthCheck() error { return nil }
   ctx, cancel := context.WithCancel(context.Background())
   cancel()
   err := doger.HealthCheckNamedWithContext(ctx, i, doger.NameOf[*healthyService]())
   ```
   - The same call with an already-passed deadline returns the bare error 2 of 1,000 times.
   - `Injector.HealthCheckWithContext` with a cancelled context does so 3 of 1,000 times.
   - Expected: `DI: health check timeout: context canceled`, matching both `ErrHealthCheckTimeout` and `context.Canceled`.
3. A service with no health check is reported as timed out at random by a scope-wide check on a done context.
   - Reproduction (scratch test, 1,000 runs): a plain value `42`, a lazy health checker not yet invoked, and a transient, each checked with `HealthCheckWithContext` on a cancelled context. They returned the timeout error 998, 998 and 995 times. `HealthCheckNamedWithContext` on the plain value returned `nil` 1,000 times.
   - Cause: the non-pool goroutine in `queueServiceHealthcheck` selects between the (immediate `nil`) result and `ctx.Done()`.
   - Expected: always `nil`, as the comment in `serviceHealthCheck` states ("A timeout error is not triggered when the service is not a healthchecker").
4. A check that ends exactly at its timeout is decided at random.
   - Reproduction (scratch probe, 2,000 runs in `synctest.Test`): a check that finishes after 10 ms under a 10 ms timeout delivered its result before the context was marked done in 740 runs on a clockwork fake clock and in 591 runs on the real clock. "Context done when the result is observed" alone therefore reports it healthy about a third of the time.
   - Cause: the check's timer and the context's timer fire at the same instant, and two goroutines then race.
   - Expected: always the timeout error. Also comparing the injector's clock with the container-set deadline gave 2,000 timeouts of 2,000 at the limit, and 0 of 2,000 one step before, on both clocks.
5. `tests/service_types_test.go: TestMixedServiceTypesPerformance` fails at random.
   - Measured while recording this change's baseline: `go test -race -count=50 -run '^TestMixedServiceTypesPerformance$' ./tests` failed once with `"2.513506ms" is not less than "2.47933ms"` (`service_types_test.go:293`), and `make test` failed once in four runs.
   - Cause: it compares wall-clock durations of 100 eager invocations against 100 lazy invocations whose first build sleeps 1 ms.
   - Expected: deterministic. Inside `synctest.Test`, only the providers' `time.Sleep` advances time, so the comparison holds on every run.

Baseline note: in the container used to apply this change, `go test -race -count=1000 -run '^TestRootScope_queueServiceHealthcheck$' .` failed 0 times in 1,000 runs. The 4/1,000 rate above was measured when the change was proposed. The regression proof therefore rests on the deterministic reproductions (Defects 2–4), not on that rate.

The fix starts with failing regression tests (tasks groups 2 and 3).
