# Tasks

Specs: `specs/health-check-timeouts/spec.md`, `specs/container-clock/spec.md`. Read design.md before each group: the checkbox lines carry only the summary. Develop on `fix/healthcheck-timeout-flake` (branched from `feat/upgrade-go-1-27`). Commands run from the repository root unless a task names a module. Fake-clock and bubble tests follow design D5: `synctest.Test`, `synctest.Wait()` before each `Advance`, no `testWithTimeout` inside a bubble, and a trailing bubble `time.Sleep` to let abandoned checks finish.

## 1. Baseline

- [ ] 1.1 Record the baseline outside the repo: the per-module test inventory (`go test -list '.*' ./...` by directory), and the flake rate of `go test -race -count=1000 -run '^TestRootScope_queueServiceHealthcheck$' .` (record failures and lines; expected around 4/1000 at `root_scope_test.go:119`/`:131`); verify `make test`, `make lint` (0 issues) and per-module `go vet ./... && go test -race ./...` pass, and stop and report if not.

## 2. A health check whose context ended returns a timeout error (defect fix; design D1–D3)

- [ ] 2.1 RED: add `healthcheck_timeout_test.go` with `TestHealthCheck_EndedContextIsTimeout` (scenario "Health check on an ended context is a timeout", 8 rows, 1,000 calls per row) and `TestHealthCheck_FinishedInTimeKeepsResult` (scenario "Health check that finishes before its context ends keeps its result", 5 rows); verify `go test -race -count=1 -run 'TestHealthCheck_EndedContextIsTimeout|TestHealthCheck_FinishedInTimeKeepsResult' .` fails in every `EndedContextIsTimeout` row on `errors.Is(err, <ctx error>)`, and the per-service cancelled rows also on the message (bare `context canceled`); record that `FinishedInTimeKeepsResult` passes on arrival (pre-fix behavior already matches) and show its assertion can fail by temporarily returning a timeout from `raceWithTimeout` (not committed).
- [ ] 2.2 GREEN: add the unexported `contextDone`, `timeoutError` (`%w: %w`, cause normalized to `context.DeadlineExceeded`) and `healthCheckResult`, and use them in `raceWithTimeout` (both branches and the deadline-free call), the non-pool `queueServiceHealthcheck` goroutine, and the `serviceEager`/`serviceLazy.healthcheck` pre-checks (design D1–D3), dropping the two `//nolint:errorlint`; verify the 2.1 tests pass, `go test -race -count=1000 -run '^TestRootScope_queueServiceHealthcheck$' .` reports 0 failures, and `make test` is green.
- [ ] 2.3 REFACTOR: *Replace Inline Code with Function Call*: make the non-pool `queueServiceHealthcheck` goroutine and `raceWithTimeout` share the one select-and-decide step instead of two copies, if the 2.2 code left them duplicated; otherwise none needed (D1 already centralizes the rule); verify `make test` and `make lint` pass.
- [ ] 2.4 Docs: in `docs/docs/service-lifecycle/healthchecker.md` document the timeout rule (a done context always yields `ErrHealthCheckTimeout`, also matching `context.Canceled`/`context.DeadlineExceeded`), and add a line to `docs/docs/migrating/migrating-from-samber-do.md` on the `errors.Is` change; verify `cd docs && npm run lint && npm run build` passes.

## 3. Injector clock (container-clock; health-check-timeouts "Configured health-check timeouts elapse on the injector's clock"; design D2, D4)

- [ ] 3.1 Scaffolding: `go get github.com/jonboulle/clockwork@v0.5.0` in the root module; verify `go.mod` requires `github.com/jonboulle/clockwork v0.5.0`, its `go.sum` entries are added, and `make test` is green.
- [ ] 3.2 RED: add `clock_test.go` with `TestClock_DefaultRealClock` (5 rows), `TestClock_ReportedDurations` (4 rows), `TestClock_TimeoutErrorSameOnAnyClock` (2 rows), `TestClock_TimeoutWaitsForClock` (2 rows) and `TestClock_DerivedInjectors` (4 rows). Add to `healthcheck_timeout_test.go` `TestHealthCheck_TimeoutOptionsOnClock` (9 rows), `TestHealthCheck_SequentialChecksShareGlobalTimeout` and `TestHealthCheck_NotBeforeClockReachesTimeout`, all per the container-clock and health-check-timeouts scenarios. Verify `go test -race -run 'TestClock_|TestHealthCheck_TimeoutOptionsOnClock|TestHealthCheck_SequentialChecksShareGlobalTimeout|TestHealthCheck_NotBeforeClockReachesTimeout' .` fails to compile only on the missing `InjectorOpts.Clock` field.
- [ ] 3.3 GREEN: add `InjectorOpts.Clock clockwork.Clock` with godoc, default it to `clockwork.NewRealClock()` in `NewWithOpts`, copy it in `copy()`, and use it for the global and per-service timeouts (`clockwork.WithTimeout`), lazy build time (via `i.RootScope()`), `ShutdownTime` and `ServiceShutdownTime` (design D4); verify the 3.2 tests pass and `make test` is green.
- [ ] 3.4 REFACTOR: *Extract Function* for the repeated `root.opts.Clock` access if it appears in three or more places (e.g. a `(*Scope).clock()` accessor); otherwise none needed; verify `make test` and `make lint` pass.
- [ ] 3.5 Add `clock_example_test.go: ExampleInjectorOpts_clock` (fake clock, `HealthCheckTimeout`, prints the timeout message) documenting the default and override; verify `go test -run ExampleInjectorOpts_clock .` passes.
- [ ] 3.6 Docs: document `Clock` in `docs/docs/container/options.md` and the fake-clock caveat (use `select` on `ctx.Done()`, not `ctx.Err()`) in `options.md` and `healthchecker.md`; replace the "no dependencies except the Go std lib" statements in `README.md`, `docs/docs/getting-started.md`, `docs/docs/about.md` (lines 31 and 99) and the uber-fx comparison verdict (design D6); verify `grep -rn 'no dependencies\|zero external dependencies' README.md docs/docs` prints nothing and `cd docs && npm run lint && npm run build` passes.

## 4. Deterministic timing tests (design D5)

- [ ] 4.1 Wrap `TestRootScope_queueServiceHealthcheck` in `synctest.Test`, dropping `testWithTimeout` and adding the trailing bubble `time.Sleep`, with fixtures and assertions unchanged; verify `go test -race -count=1000 -run '^TestRootScope_queueServiceHealthcheck$' .` reports 0 failures and `make test` is green.
- [ ] 4.2 Wrap the five `TestScope_HealthCheckWithContextExpiration_*` tests in `synctest.Test` the same way, with fixtures and assertions unchanged; verify `go test -race -count=200 -run 'TestScope_HealthCheckWithContextExpiration' .` reports 0 failures and `make test` is green.

## 5. Integration checks

- [ ] 5.1 Run `go mod tidy` in all 25 workspace modules; verify the diff only adds `clockwork` requirements (indirect in sub-modules) and `go.sum` entries, and a second tidy is clean.
- [ ] 5.2 Inventory diff against the 1.1 baseline; verify the only differences are the added tests (`TestHealthCheck_*` ×5, `TestClock_*` ×5, `ExampleInjectorOpts_clock`), with no deletions.
- [ ] 5.3 Full green run; verify `make test`, `make lint` (0 issues), per-module `go vet ./... && go test -race ./...` in all 25 modules, and `cd docs && npm run lint && npm run build` all pass, with no goleak reports.

## Workflow follow-up

- Merge `upgrade-go-1-27` first, then rebase `fix/healthcheck-timeout-flake` onto `main`.
- Run `/opsx:verify`, then archive after review.
- Separate decision: whether `HealthCheckTimeout`/`HealthCheckGlobalTimeout` should also apply to the per-service `HealthCheck*` functions (design Non-Goals).
