# Proposal

## Why

The module declares `go 1.18`, which rules out standard-library and language features the upcoming lifecycle work depends on:
- `testing/synctest` (1.25), for deterministic tests of start/shutdown timeouts and concurrency (used by `add-start-lifecycle-and-kinds` and `add-container-lifecycle-hooks`)
- `context.WithCancelCause` (1.20) and `reflect.TypeFor` (1.22), used by `add-start-lifecycle-and-kinds`
- `slices`/`maps`/`cmp` (1.21–1.22), `sync.WaitGroup.Go` (1.25) and per-iteration loop variables (1.22), which simplify existing code

The planned changes need Go 1.25 at minimum; none needs a 1.27-only feature (start-lifecycle design: "Generic methods are not needed here"). The floor is set to the current release, 1.27, as a deliberate project choice: divergence from upstream is allowed, there are no external consumers yet, and tracking the current release keeps 1.27 features (such as generic methods) available to later work without another breaking bump.

## What Changes

- **BREAKING**: Minimum supported Go version becomes 1.27 (`go` directive in every `go.mod` and in `go.work`). Consumers on Go 1.26, which is still supported by the Go team, can no longer build the module.
- **BREAKING (observable output)**: A provider that panics with `nil` now makes the invocation return an error matching `*runtime.PanicNilError`. For a consumer whose own `go` line was below 1.21 it used to return the zero value with a nil error; consumers already at 1.21 or later got this error before. This follows from the floor: requiring this module raises the consumer's own `go` line to 1.27, so the `panicnil=0` default applies. Consumers can restore the old behavior with `GODEBUG=panicnil=1`.
- Keep the unexported helpers `contains`, `reverseSlice` and `coalesce`, but implement them with `slices.Contains`, `slices.Reverse` and `cmp.Or`. Existing tests stay unchanged. `keys`, `flatten`, `filter`, `mAp` and `orderedUniq` stay as they are, because no standard-library function is exactly equivalent.
- Rewrite the two goroutine fan-outs in `scope.go` with `sync.WaitGroup.Go`, dropping the per-iteration capture workarounds.
- Apply the behavior-preserving modernizers that the enabled `modernize` linter reports after the bump, one analyzer per step with `go fix`: `slicescontains`, `mapsloop`, `reflecttypefor`, `rangeint`, `waitgroupgo` (tests) and `any`. Disable `atomictypes`, because its fixes rewrite test fixture field types and assertion expressions. `make lint` ends at 0 issues.
- Finish CI for Go 1.27: pin golangci-lint in `lint.yml` and set `run.go` in `.golangci.yml`. The `test.yml` matrix (`1.27`, `stable`) and Go setup from `go.work` in every workflow already landed in #12. Update the Go-version statements in README and the docs.
- No exported identifier, signature or option changes.

## Capabilities

### New Capabilities

- `toolchain-support`: every module requires Go 1.27, and library code may use Go 1.27 language features.
- `provider-panic-handling`: what an invocation returns when its provider panics, including the nil panic value and its GODEBUG override.

### Modified Capabilities

None.

## Impact

- `go.mod` ×25 (plus new `go.sum` entries from `go mod tidy`), `go.work`, `.github/workflows/lint.yml`, `.golangci.yml`.
- `utils.go`, `scope.go`, `errors.go`, `service_*.go`, `stacktrace/stacktrace.go`, and test files touched by the modernizers. Each analyzer is its own behavior-preserving step, with `make test` and `make lint` run after it.
- New tests: `TestInvoke_ProviderPanic`, `TestInvoke_ProviderPanicNil_GODEBUGOverride`, and the root-package toolchain check `TestToolchain_GoDirective`.
- README, `docs/docs/{getting-started,about,faq}.md`, `docs/docs/comparison/*`, `docs/src/pages/index.tsx`, `docs/plugins/structured-data/index.ts` and `docs/static/llms.txt` (Go version statements).
- Unlocks `testing/synctest` (1.25) for `add-start-lifecycle-and-kinds` and `add-container-lifecycle-hooks`.
- Depends on `rename-module-doger`.

## Defaults & Control

- Go floor: 1.27, with no override. No planned change needs a 1.27-only feature (1.25 would suffice); the floor tracks the current release by deliberate project choice (see Why), recorded as a breaking change.
- `panic(nil)` in a provider: by default an error matching `*runtime.PanicNilError`. Override: set `GODEBUG=panicnil=1` in the environment, or add `//go:debug panicnil=1` to the consumer's main package, to get the old `(zero, nil)` result.
