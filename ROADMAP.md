# Roadmap

This file records where doger is heading and why. It is not a commitment or a schedule. Each item becomes its own OpenSpec change (`openspec/changes/<name>/`) when work starts, and the change, not this file, is where its requirements are decided. Items keep their place here until their change is archived.

Every item follows the project rules in `CLAUDE.md`: sensible zero-config defaults that consumers can override, strict TDD, and no claimed defect without a failing test.

## In flight

| Change | PR | Summary |
|---|---|---|
| `consolidate-pending-lifecycle-work` | #8 | Container lifecycle hooks and phases, incremental start, collections, follow-ups of #4 |
| `add-scheduled-health-checks` | #9 | Scheduled health checks with cached reports, coalesced forced runs, health classification (readiness, liveness, optional services and a Degraded state), and eight health-check defect fixes. Depends on #8 |

## Next

In this order. The first is a follow-up of #9; the second and third came out of the #9 exploration, which compared doger with uber-go/fx, uber-go/dig, google/wire, Spring Boot and .NET.

### 1. HTTP health handlers (follow-up of #9)

- **Problem.** #9 adds `HealthReport` and tag filters, but the `http/*` packages only serve the debug UI. Every consumer would write the same handler.
- **Sketch.**
  - Readiness and liveness handlers in `http/std`, `gin`, `echo`, `fiber` and `chi`.
  - Each serves a tag-filtered report as JSON, with status 200 for Healthy and Degraded and 503 otherwise.
  - Each passes the request context to `HealthReportWithContext`.
- **To decide.** The JSON shape (including how `Err` is rendered), the paths, and whether a handler exposes service names, since the debug UI already warns about revealing internals.

### 2. Test helper package

- **Problem.** A test that calls `Start` must also call `Shutdown`, or it leaks goroutines. Since #9, a started container runs a health-check scheduler, so a forgotten `Shutdown` fails every goleak-based test suite. Test setup (a fake clock, a disabled scheduler, failing on a bad shutdown report) is repeated in each consumer's tests.
- **Prior art.** uber-go/fx ships `fxtest` ([source, v1.24.0](https://github.com/uber-go/fx/blob/v1.24.0/fxtest/app.go)).
  - `fxtest.New(tb, opts...)` builds the app and fails the test if `fx.New` fails.
  - `RequireStart()` and `RequireStop()` fail the test on start or stop errors.
  - It does not register `Stop` with `tb.Cleanup`: the test must call `RequireStop` itself.
- **Sketch (to be decided in the change):**
  - A `dogertest` package.
  - `dogertest.New(t, opts...)` returns a `*doger.RootScope` and registers `Shutdown` with `t.Cleanup`. It fails the test when the shutdown report has errors.
  - `dogertest.Start(t, injector)` starts the container and fails the test on a start error.
  - Test-friendly defaults that can each be overridden: for example a `clockwork` fake clock, and scheduling off unless the test turns it on.
- **To decide.**
  - Subpackage of the root module or a separate module.
  - Which test defaults to apply, given the library principle that defaults must be overridable.
  - Whether to integrate goleak or leave it to the consumer's `TestMain`.
  - Helpers for `Override` and `Clone`-based test isolation.
- **Depends on.** #9, for the scheduler defaults it controls.

### 3. Container validation

- **Problem.** doger resolves services lazily, so a missing dependency or a typo in a service name only fails when that code path first runs, possibly in production. Teams want such errors in CI.
- **Constraint that shapes the design.** A doger provider is a plain function, `func(i Injector) (T, error)`, that invokes its dependencies inside its body. Unlike fx and dig, doger cannot learn a provider's dependencies from its signature without running it. What is known before running anything:
  - dependencies declared with `DependsOn` / `DependsOnNamed`;
  - alias targets;
  - kind assignability.

  Struct-tag injection (`InvokeStruct[T]`) happens at invoke time, so a struct's tags are not known before running either.
- **Prior art.**
  - uber-go/fx: `fx.ValidateApp(opts...)` "validates that supplied graph would run and is not missing any dependencies. This method does not invoke actual input functions" ([source, v1.24.0](https://github.com/uber-go/fx/blob/v1.24.0/app.go)).
  - uber-go/dig: `dig.DryRun(true)` "disables invocation of functions supplied to Provide and Invoke" ([source, v1.19.0](https://github.com/uber-go/dig/blob/v1.19.0/container.go)).
  - google/wire validates the graph at compile time.

  All three rely on dependencies declared in types, which doger providers do not have.
- **Options to explore:**
  - **(a) Static validation:** check everything known without running a provider, and return one joined error naming each service and scope.
  - **(b) Validation by building:** build every lazy service in a clone, report the errors, and discard the clone. It catches everything, but runs provider side effects such as opening connections.
  - **(c)** Both, with (a) by default and (b) opt-in.
- **To decide.**
  - Is static validation useful enough, given how few dependencies are declared today? Should it encourage `DependsOn`?
  - Should `Start` validate first, behind an option?
  - How does validation interact with `Override` and child scopes?

## Candidates (not scheduled)

These came out of the same comparison. Each needs an exploration before it is planned.

- **Decorators.**
  - Wrap a registered service (metrics, logging, retries) without replacing it. Today `Override` replaces it.
  - Prior art: `fx.Decorate` ([source](https://github.com/uber-go/fx/blob/v1.24.0/decorate.go)) and `dig.Container.Decorate` ([source](https://github.com/uber-go/dig/blob/v1.19.0/decorate.go)).
- **Structured lifecycle events.**
  - A typed event stream: built, started, health changed, shut down, each with its duration.
  - It would complement `Logf` and the hook lists, and make slog or OpenTelemetry integration a single adapter.
  - Prior art: fx's `fxevent` package with its slog logger ([source](https://github.com/uber-go/fx/tree/v1.24.0/fxevent)).
- **Graph export.**
  - DOT or Mermaid output of the dependency graph, next to the existing explain output and HTTP UI.
  - Prior art: `fx.DotGraph` and `dig.Visualize` ([source](https://github.com/uber-go/dig/blob/v1.19.0/visualize.go)).
- **Configuration validation at start.**
  - Validate configuration values when the container starts.
  - In Go this is usually done outside DI (viper, envconfig), so it is low priority. It may fit as a component kind.
