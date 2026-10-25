# Proposal

## Why

Whether a health check counts as timed out depends on goroutine scheduling, not only on the check and its timeout:
- `TestRootScope_queueServiceHealthcheck` fails about 4 times in 1,000 runs on `main` (see Defect).
- The library can return a bare `ctx.Err()` that does not match `ErrHealthCheckTimeout`. For a cancelled context passed to `HealthCheckNamedWithContext` this happens every time.

Consumers cannot test timeout behavior without real sleeps, because the container always reads the wall clock. This change makes the outcome deterministic, lets consumers supply the clock, and makes this repository's timing tests deterministic.

## What Changes

- **Behavior change (observable, defect fix):** a health check whose context is done when its result is observed returns the timeout error, whether that check is the per-service `HealthCheck*` functions or `HealthCheck`/`HealthCheckWithContext` on an injector or scope. That holds even when the check itself returned `nil` or a bare `ctx.Err()`. The error wraps both `ErrHealthCheckTimeout` and the context error with `%w`. Its message is unchanged: `DI: health check timeout: <ctx error>`.
  - Before, the result was decided at random when both were ready, and the per-service path with a deadline-free context always returned the bare `ctx.Err()`.
  - `errors.Is(err, context.DeadlineExceeded)` and `errors.Is(err, context.Canceled)` now also match timeout errors (additive).
- New option `InjectorOpts.Clock clockwork.Clock` (`github.com/jonboulle/clockwork`). `nil` means the real clock. The container reads time only from this clock: the `HealthCheckTimeout` and `HealthCheckGlobalTimeout` contexts, lazy build time (`ExplainService…` `ServiceBuildTime`), and `ShutdownReport.ShutdownTime`/`ServiceShutdownTime`.
- New runtime dependency `github.com/jonboulle/clockwork v0.5.0` (no transitive dependencies; requires Go 1.21). The "no dependencies except the Go std lib" statements in README and docs are corrected. They are already inaccurate today: the library imports `github.com/samber/go-type-to-string`.
- Tests: the wall-clock-sensitive health-check tests use `testing/synctest`:
  - `TestRootScope_queueServiceHealthcheck` and the five `TestScope_HealthCheckWithContextExpiration_*` tests;
  - their fixtures (`lazyTestHeathcheckerOKTimeout`, `scopeTestSlowHealthchecker`) are unchanged;
  - duration assertions such as `duration < 70ms` become exact fake-time assertions.
- No exported identifier is removed or changed. Adding a field to `InjectorOpts` only breaks unkeyed struct literals of it, which the Go 1 compatibility guidelines exclude.

## Capabilities

### New Capabilities

- `health-check-timeouts`: what a health check returns when its context is done or a configured timeout elapses, and that a check finishing in time keeps its result.
- `container-clock`: the container's time source, `InjectorOpts.Clock`, its default, and which time reads it governs.

### Modified Capabilities

None. `module-identity` requires `ErrHealthCheckTimeout`'s message to stay `DI: health check timeout`, and it does.

## Impact

- Code: `injector.go` (`InjectorOpts.Clock`, defaulting), `root_scope.go` (`queueServiceHealthcheck`), `scope.go` (`HealthCheckWithContext`, `serviceHealthCheck`, shutdown timing), `utils.go` (`raceWithTimeout`), `service_lazy.go` (build time).
- `go.mod`/`go.sum` of the root module. Every workspace module's `go.sum` (and `go.mod` indirect block) after `go mod tidy`.
- Tests: `root_scope_test.go`, `scope_test.go`, new `healthcheck_timeout_test.go`, `clock_test.go`, and `Example*` tests for the option.
- Docs: `README.md`, `docs/docs/getting-started.md`, `docs/docs/about.md`, `docs/docs/comparison/samber-do-vs-uber-fx.md` (dependency statements), and the health-check page for the clock option and the timeout rule.
- Builds on `upgrade-go-1-27` (merged in #2): `testing/synctest` needs `go 1.25` or later.

## Defaults & Control

- **Clock.**
  - Default: `nil` → `clockwork.NewRealClock()`, which is the wall-clock behavior consumers get today.
  - Override: set `InjectorOpts.Clock`, for example to `clockwork.NewFakeClock()` in tests.
  - Child scopes and clones use the root scope's clock.
- **Timeout rule.**
  - Default: a check whose context is done when its result is observed is a timeout.
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

The fix starts with failing regression tests for both (tasks group 1).
