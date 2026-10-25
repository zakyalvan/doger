# Design

## Context

See proposal.md for the motivation, and `specs/toolchain-support/spec.md` and `specs/provider-panic-handling/spec.md` for the required behavior. This is the state after `fix-stacktrace-frame-filter` and `rename-module-doger`.

**Go version declarations**
- Since #12 (framework and vulnerability bumps), the directives differ: the root `go.mod` and 15 other modules declare `go 1.18`; `http/chi`, `examples/http/chi` and `examples/web-application` declare `go 1.24`; the two fiber modules `go 1.25`; the two gin and two echo modules `go 1.26.0`; `go.work` declares `go 1.26.0`. None has a `toolchain` line.
- CI, also since #12: `.github/workflows/test.yml` tests `["1.27", "stable"]`.
  - `lint.yml`, `doc.yml`, `security.yml` and `release.yml` set up Go with `go-version-file: go.work`.
  - `lint.yml` still runs `golangci-lint-action@v9` with no pinned linter version.
- `.golangci.yml` (version 2) pins no Go version and **enables the `modernize` linter**. The baseline lint run reports 0 issues.
- These statements say Go 1.18 about this library:
  - `README.md`: the badge, the tagline, and the "uses Go 1.18+ generics" sentence;
  - `docs/docs/getting-started.md:16`, `docs/docs/about.md:3,15,25`, and `docs/docs/faq.md:21,37,91,99` (lines 37 and 99 say "requires Go 1.18 or later");
  - the three `docs/docs/comparison/*.md` tables and `docs/src/pages/index.tsx:122`;
  - `docs/plugins/structured-data/index.ts:55,61` (`runtimePlatform: 'Go 1.18+'`) and `docs/static/llms.txt:4307,4345`.

  The sibling-project lines about `samber/lo` and `samber/mo` (README, `about.md:39-40`) describe other libraries.

**Helpers in `utils.go` and their stdlib equivalents.** Equivalence must be exact, including nil versus empty results:

| Helper | Call sites (non-test) | Stdlib candidate | Exactly equivalent? |
|---|---|---|---|
| `contains` | `virtual_scope.go:115` | `slices.Contains` | Yes |
| `reverseSlice` | `di_explain.go:480` | `slices.Reverse` | Yes |
| `coalesce` | `invoke.go:234` | `cmp.Or` | Yes: both return the first non-zero argument, and both have a `comparable` constraint |
| `keys` | `dag.go:189,193`, `scope.go:440` | `slices.Collect(maps.Keys(m))` | **No**: `Collect` returns `nil` for an empty map, `keys` returns a non-nil empty slice. `dag.go` results flow into explain output, where JSON shows `null` versus `[]`. |
| `flatten` | `di_explain.go:126` | `slices.Concat(c...)` | **No**: `Concat` returns `nil` when the total length is 0 |
| `filter` | `di_explain.go:530` | none (`slices.DeleteFunc` mutates in place) | No equivalent |
| `mAp` | `invoke.go`, `di_explain.go` | none in stdlib | No equivalent |
| `orderedUniq` | `scope.go:251,281` | none (`slices.Compact` needs sorted input) | No equivalent |

**Goroutine fan-outs.**
- `(*Scope).shutdownChildrenInParallel`: `wg.Add(1)` + `go func(c *Scope, i int){…; wg.Done()}(child, index)`.
- `(*Scope).shutdownServicesWithoutDependenciesInParallel`: `wg.Add(len(serviceNames))` + `go func(n string){…; wg.Done()}(name)`.
- The `go fix -waitgroupgo` analyzer does not match either one: `Done` is not deferred, and the values are passed as parameters. They are rewritten by hand.
- `utils.go` (job pool, `raceWithTimeout`) and `root_scope.go` start goroutines without loop-variable capture. They are left alone.

**What the bump changes in lint.** A trial bump of every directive to `go 1.27` kept `make test`, the `HealthCheck` tests at `-count=5`, and `go vet` in all 25 modules green. But `golangci-lint` 2.15.0 then reports `modernize` findings that the baseline did not have. The default output says 81 issues; with per-linter limits off the counts are:

| Analyzer | Findings | Where |
|---|---|---|
| `rangeint` | 76 | mostly tests, plus `stacktrace/stacktrace.go` |
| `reflecttypefor` | 19 | `utils.go:248,260`, `utils_test.go` |
| `atomictypes` | 5 | `scope_test.go:1463,1493,1523,1551`, `tests/race_conditions_test.go:23` |
| `waitgroupgo` | 3 | `scope_test.go:1622,1758,1963` |
| `mapsloop` | 3 | `errors.go:79`, `scope.go:537,540` |
| `slicescontains` | 1 | `utils.go:129` |

Apply found more than this trial (it predates test files added by `fix-stacktrace-frame-filter` and `rename-module-doger`): `reflecttypefor` 21 (also `service_*.go`, `stacktrace/stacktrace.go`, `main_test.go`, `module_identity_test.go`), plus `forvar` 18, `stringscutprefix` 1 and `stringsseq` 1, all in test files. Those three were applied as extra steps in D4, by decision during apply.

**`interface{}`.** 16 code occurrences, all in `examples/*`. There are 3 comment occurrences: `invoke.go:14,21` and `invoke_test.go:69`.

**Directive-sensitive semantics.** Raising the `go` directive changes the language semantics of this module's code. In workspace mode, the `go.work` line sets the GODEBUG defaults of this repository's own test binaries.
- Because `go.work` is already `go 1.26.0` (#12), workspace-mode tests (`make test`, CI) already run with `panicnil=0`. Only module mode (`GOWORK=off`, root `go.mod` at `go 1.18`) still shows the pre-bump `panic(nil)` result. Verified with a probe test: workspace mode recovers `runtime error: panic called with nil argument`, `GOWORK=off` recovers `<nil>`. The provider-panic RED step therefore runs with `GOWORK=off`.
- **Loop variables become per-iteration (1.22).** This is a language change to this module's code, decided by its own directive.
- **`panicnil` (1.21).** `panic(nil)` in a provider reaches `handleProviderPanic` as `*runtime.PanicNilError`. Verified in a trial: before the bump, `err=<nil>`; after it, `err="runtime error: panic called with nil argument"`, with `errors.As` true. With `GODEBUG=panicnil=1` the result is `<nil>` again.
  - GODEBUG defaults follow the main module. Requiring this module raises a consumer's `go` line to at least 1.27, so consumers get `panicnil=0` by default as well. That is why it is in a spec.
- **`asynctimerchan` (1.23)** affects only timer channels. No production code uses one: `raceWithTimeout` waits on `ctx.Done()`. Only tests do: `scope_test.go:1474,1504`, `main_test.go:50`, `tests/concurrent_operations_test.go:744`, `tests/complex_scenarios_test.go:340`, `tests/helpers_test.go:19`.

## Goals / Non-Goals

**Goals:**
- Satisfy `specs/toolchain-support` and `specs/provider-panic-handling`.
- `go 1.27` in every `go.mod` and in `go.work`, with no `toolchain` line. CI tests `1.27` and `stable`, and lints with Go and golangci-lint that understand 1.27.
- `make lint` at 0 issues with `modernize` still enabled.
- Exactly equivalent helpers use the standard library behind their existing names, so existing tests stay unchanged.
- The two fan-outs use `sync.WaitGroup.Go`. `interface{}` becomes `any`.

**Non-Goals:**
- Replacing `keys`, `flatten`, `filter`, `mAp` or `orderedUniq`, which have no exact equivalent (see table).
- Adopting new Go features in the public API. That belongs to the feature changes.
- Changing `make test` to cover the sub-modules (separate tooling follow-up).
- Changing GODEBUG settings of this module. Consumers keep control through their own main module.

## Decisions

**D1. Bump the directives with `go mod edit -go=1.27` in every module, and `go work edit -go=1.27`.**
- No `toolchain` line is added. The trial confirmed that neither command nor `go mod tidy` adds one.
- `go mod tidy` then adds `go.sum` entries. The trial ran on the tree before #12 (24 modules gained 81 lines, e.g. `go.uber.org/goleak v1.2.1/go.mod h1:…` in `http/gin/go.sum`), so expect different counts now. Additions are expected and committed; removals or version changes are not. On apply, tidy also moved direct requirements out of the indirect `require` block in 9 `go.mod` files (the go ≥ 1.17 layout); the set of (path, version, indirect) requirements stayed identical.
- *Alternative:* `go 1.27.0` with a `toolchain` pin. Rejected: churn without benefit for a library.

**D2. Helpers: *Substitute Algorithm* inside the existing unexported helpers.**
- `contains` becomes `return slices.Contains(list, elem)`. This is exactly what `go fix -slicescontains` produces.
- `reverseSlice` becomes `slices.Reverse(s)`.
- `coalesce` becomes `return cmp.Or(v...)`.
- Call sites and `utils_test.go` stay unchanged, so no test is edited or deleted.
- *Alternative:* inline the stdlib calls at the call sites and delete the helpers and their tests. Rejected: it deletes `TestUtilsContains` and `TestUtilsCoalesce`, which the project rules treat as a behavior change to a test. The wrappers cost one line each.

**D3. `sync.WaitGroup.Go` for the two fan-outs (by hand; D4's analyzer does not match them).**
- Replace `wg.Add(n)` + `go func(x T){…; wg.Done()}(v)` with `wg.Go(func(){…})`, capturing the per-iteration loop variables.
- Same concurrency and same `Wait` semantics. `Go` adds 1 before starting the goroutine and calls `Done` when `f` returns.
- The only difference is that `Done` also runs if `f` panics. That is unobservable, because an unrecovered goroutine panic terminates the process.

**D4. Modernizers: one `go fix` analyzer per refactoring step, ending with `make lint` at 0 issues.**
- The Go 1.27 `go fix` runs the modernizers, and each analyzer can be selected with its own flag: `go fix -<analyzer> -diff ./...` to review, then `go fix -<analyzer> ./...`.
- Applied, in this order, each verified by `make test` and by that analyzer's lint findings dropping to 0:
  1. `slicescontains` (D2's `contains`);
  2. `mapsloop` (`maps.Copy`, same iteration and assignment);
  3. `reflecttypefor` (`reflect.TypeFor[T]()` for `reflect.TypeOf((*T)(nil)).Elem()`);
  4. `rangeint` (the analyzer only rewrites loops whose variable and bound are not modified in the body);
  5. `waitgroupgo` (the 3 test sites);
  6. `any`, run in every module. The 3 `interface{}` comments are hand-edited.
  - Added during apply, each its own step before step 6: `forvar` (redundant `x := x` copies), `stringscutprefix` and `stringsseq`, all in test files.
- These rewrites in test files change loop headers, `reflect` calls and goroutine start-up. They change no test input or assertion value.
- **Disabled: `atomictypes`**, as `linters.settings.modernize.disable: [atomictypes]` in `.golangci.yml`. Reason: its fixes change test fixture field types (`int32` → `atomic.Int32`) and rewrite assertion expressions. That is not a mechanical rewrite, and it brings no benefit to consumers (only tests are affected).
- Trial evidence: with `atomictypes` disabled and the analyzers applied, `golangci-lint run` reports `0 issues`, and `go test -race ./...` passes.
- `.golangci.yml` also gets `run.go: "1.27"`.
- *Alternative:* disable every new finding and modernize later. Rejected: it leaves the floor bump without its payoff, and the lint config drifting from the code.

**D5. CI.**
- `test.yml` matrix: `["1.27", "stable"]`. Already in place since #12; this change only verifies it.
- `lint.yml`: `actions/setup-go` keeps `go-version-file: go.work` (set in #12; `go.work` is always at least every module's version), and `golangci-lint-action` gets `version: v2.15.0`. golangci-lint refuses targets newer than the Go it was built with. The `v2.15.0` release binary was built with `go1.27.0` (`go version golangci-lint-2.15.0-linux-amd64/golangci-lint` prints `go1.27.0`), and local 2.15.0 lints the bumped tree.
- `release.yml`, `doc.yml` and `security.yml` already follow `go.work` (since #12), so they pick up 1.27 from the `go.work` bump.
- CodeQL's Go 1.27 support is unconfirmed. Check it on the first CI run.

**D6. Docs.** Every statement that gives this library's minimum Go version moves to 1.27. Statements that only say generics arrived in Go 1.18 are reworded to "Go generics" plus "requires Go 1.27+" where they give a version.
- Files are listed in Context.
- The sibling-project lines (`samber/lo`, `samber/mo`) stay.

## Risks / Trade-offs

- [Per-iteration loop variables change an existing closure that relied on the shared variable] → Run the full race suite right after the directive bump, before any other edit (task 2.3). In the trial it was green.
- [The `panicnil` default changes what consumers see for `panic(nil)`] → Specified, flagged BREAKING, and covered by `TestInvoke_ProviderPanic`, with the GODEBUG override as migration.
- [Timer channel semantics change] → Affects only the test helpers listed in Context. The `HealthCheck` and `Shutdown` tests run with `-count=5`/`-count=3` after the bump.
- [A modernizer rewrite is not equivalent] → One analyzer per step, `-diff` review first, and `make test` after each. `atomictypes` is disabled (D4).
- [Dropping Go 1.26 users] → Accepted and flagged BREAKING.
- [The pinned golangci-lint version ages] → Dependabot `github-actions` updates do not cover the `version:` input. Bump it together with Go releases.

## Public API & Defaults

- **BREAKING: the minimum Go version is 1.27.** Migration: build with Go 1.27 or newer. `GOTOOLCHAIN=auto`, the go command's default, fetches it automatically.
- **BREAKING: a provider that panics with `nil` returns an error matching `*runtime.PanicNilError`** instead of `(zero, nil)`. Migration: handle the error, or restore the old behavior with `GODEBUG=panicnil=1` or `//go:debug panicnil=1` in the main package.
- No exported identifier, signature, default or option changes.

## Test Strategy

Every `#### Scenario:` maps to one Go test. `Examples:` rows are named subtests.

| Scenario | Go test | Kind |
|---|---|---|
| Provider panic values become invocation errors | `invoke_test.go: TestInvoke_ProviderPanic/{lazy_nil,transient_nil,lazy_error,transient_error,lazy_string,lazy_int}`, through the public `Invoke`/`InvokeNamed` with `Provide`/`ProvideNamedTransient` | table |
| Consumers can restore the legacy nil-panic behavior | `invoke_test.go: TestInvoke_ProviderPanicNil_GODEBUGOverride`. Re-executes the test binary with `-test.run=^TestInvoke_ProviderPanicNil_GODEBUGOverride$`, `GODEBUG=panicnil=1` and a marker env var; the child asserts `(0, nil)`. Wrapped in `testWithTimeout` | unit (subprocess) |
| Module go directives declare 1.27 | `toolchain_test.go: TestToolchain_GoDirective/<file>` (root package) (26 rows). Reads each file relative to the repository root, and fails if `go.work` lists a module without a row | table |

`TestToolchain_GoDirective` lives in a root-package `_test.go` file, so it adds no importable code and no new package. There is deliberately no compiler test for generic methods: the go directive sets the language version, so the directive rows already guarantee it.

**Hot paths and their coverage:**
- **Resolution and invocation.** `invoke_test.go`, `service_*_test.go` and the new provider-panic tests.
- **Lifecycle.** `di_lifecycle_test.go`, plus the health-check timeout tests (`-run HealthCheck -count=5`).
- **Scope traversal and the D3 fan-outs.** `scope_test.go` and `root_scope_test.go` shutdown tests (`-run Shutdown -count=3`).
- **Concurrency.** The race suite with goleak (`main_test.go`).
- **Helpers (D2).** `utils_test.go` (unchanged).

**Behavior-preservation checks:**

| Check | How | Kind |
|---|---|---|
| Suite green right after the bump, before any refactor | `make test`, `go test -race -count=5 -run HealthCheck .`, per-module `go vet ./... && go test -race ./...` | unit / race / goleak |
| Test inventory | Before and after: `go test -list '.*' ./...` per module, by directory. Only additions: the three tests above. No deletions | regression |
| Each modernizer step | `make test`, plus `golangci-lint run --enable-only modernize` showing no findings for that analyzer | race / lint |
| Final lint | `make lint` reports 0 issues | lint |
| Modules tidy | `go mod tidy` diff is only the `go` line plus `go.sum` additions | tooling |
| CI workflows valid | `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` on the edited workflows | tooling |
| Docs | `cd docs && npm ci && npm run lint && npm run build` | docs |

## Migration Plan

1. Group 1: record the baseline.
2. Group 2: RED (the spec tests) → GREEN (the directive bump) → REFACTOR (tidy, lint config, then the modernizers one by one) → docs.
3. Group 3: helper bodies (D2) and fan-outs (D3), as behavior-preserving refactorings.
4. Group 4: CI. Group 5: integration checks.

Rollback: revert the commit series. No published artifact depends on it.
