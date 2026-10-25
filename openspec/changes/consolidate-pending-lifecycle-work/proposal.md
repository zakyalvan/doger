# Proposal

## Why

Several separate plans and loose ends build on `add-start-lifecycle-and-kinds`: two planned changes, the follow-ups and findings from its PR (#4), its open design questions, and the work it deferred to phase 2. Planned apart, they overlap (hooks vs. Refresh, `WithOrder` vs. `registration-options`) and would drift. This change replaces them with one plan, so every lifecycle, collection and health-check decision is made once and is consistent.

## What Changes

**Folded in, unchanged in substance.** These two changes are deleted; their specs, decisions and tasks move here. (The third pending change, `add-clock-fix-healthcheck-timeouts`, was implemented and archived in #5 and is no longer pending.)
- `add-container-lifecycle-hooks`:
  - Container hooks `HookContainerStarting/Started/Ready/Failed/Closing/Closed` and `HookContainerPanic`.
  - A `ready` start phase.
- `add-collection-invocation`:
  - `InvokeAll`, `InvokeAllMap` and their `Must*` variants.
  - `Ordered` and `WithOrder`.

**Findings and follow-ups from PR #4**
- **Defect (behavior change, flagged):** an alias over an eager service shuts the target down twice. An alias now never shuts its target down; only the target's own registration does.
- **Defect (internal):** `mergeShutdownReports` reports `Succeed: false` for reports with no errors. Today callers overwrite the field, so consumers cannot see it.
- `New()` no longer allocates the default `Runnable` handler entry: it is resolved at Start. This removes +2 allocs per container.
- **Not reproduced:** a race between `HealthCheck` and root `Shutdown` on `healthCheckPool` (0 races in 2,000 runs). It gets one bounded investigation task; no fix is proposed unless a failing test reproduces it.

**Open questions of `add-start-lifecycle-and-kinds`, decided**
- `Clone`/`CloneWithOpts` copy consumer-replaced kind handlers.
- New exported `StartError{Scope, Service, Kind, Op, Cause}` with `Unwrap`. Start failures return it, and their messages are unchanged.
- `ExplainServiceDependencyOutput` gains `Source` (`invoked`, `depends-on`, `declared`); `String()` marks non-invoked edges.

**Phase 2 of the start lifecycle.** Incremental start: each service still starts at most once.
- `Scope.Start()` / `Scope.StartWithContext(ctx)` start the not-yet-started services of that scope and its descendants, once the root container has started.
- `RootScope.Refresh()` / `RefreshWithContext(ctx)` do the same for the whole container. This covers late registrations and child scopes created after `Start`. It fires per-service start hooks and kind handlers for new components only; container hooks stay once per container. Before a successful `Start`, both return the new sentinel `ErrNotStarted`.
- `InjectorOpts.StartParallelism`: `0`/`1` keeps today's sequential start; `n > 1` starts independent Starters concurrently, still in DAG order.
- `HookAfterBuild []func(*Scope, string, any)` and `AddAfterBuildHook`: the instance-aware post-build hook.

**Phase 2 of hooks and collections, and the remaining follow-ups**
- Per-scope hooks `HookScopeStarting/Started/Closing/Closed` for child scopes, and `RootScope.Phase()` returning `ContainerPhase`.
- Struct-tag injection of collections: `do:"*"` on a `[]T` field means `InvokeAll[T]`, and on a `map[string]T` field `InvokeAllMap[T]`.
- Live collections: `InvokeCollection[T](i) Collection[T]`, whose `All()`/`Map()` re-resolve on each call.
- **Behavior change (flagged):** `InvokeAs[T]` and the implicit-aliasing struct fallback pick the first match deterministically: nearest scope, then registration order, then name.
- **Behavior change (flagged):** per-service `HealthCheck*` functions honor `HealthCheckTimeout` when it is set.

**BREAKING (edge cases only, flagged):**
- A service literally named `*` can no longer be injected through a struct tag, because `do:"*"` now means a collection.
- Adding exported fields to `InjectorOpts` and `ExplainServiceDependencyOutput` breaks only unkeyed struct literals, which the Go 1 compatibility guidelines exclude.

## Capabilities

### New Capabilities

- `container-lifecycle-hooks` (folded): container-level hooks, veto, ordering, panics, shutdown during start.
- `collection-invocation` (folded): `InvokeAll`/`InvokeAllMap` membership, visibility, errors, edges and snapshots.
- `collection-ordering` (folded): `Ordered`, `WithOrder`, stability, and independence from lifecycle order.
- `container-phase`: the observable container phase (`RootScope.Phase()`).
- `scope-lifecycle-hooks`: hooks fired when a child scope starts or shuts down.
- `service-build-hooks`: `HookAfterBuild`.
- `service-aliases`: an alias resolves to its target and never shuts it down.
- `type-resolution`: deterministic first-match resolution for `InvokeAs` and the implicit-aliasing struct fallback.
- `collection-struct-injection`: `do:"*"` on slice and map fields.
- `live-collections`: `Collection[T]`, re-resolved on each call.

### Modified Capabilities

- `health-check-timeouts` (main spec since #5): per-service `HealthCheck*` functions honor `HealthCheckTimeout` (ADDED requirement).

These capabilities come from `add-start-lifecycle-and-kinds` and exist under `openspec/specs/` once it is archived (prerequisite):
- `container-start`:
  - "Registrations after Start are not started" becomes incremental start through `Scope.Start`/`Refresh`.
  - Adds parallel start, `StartError`, and `Refresh` semantics.
- `component-kinds`: clones copy replaced handlers; handlers run for new components on `Refresh`/`Scope.Start`.
- `registration-options`: `WithOrder(n)` joins the list of option constructors.
- `dependency-declaration`: explained edges report their source.

## Impact

- Code:
  - Core package: `root_scope.go`, `root_start.go`, `scope.go`, `kind.go`, `runnable.go`, `dag.go`, `di_explain.go`, `invoke.go`, `di.go`, `injector.go`, `options.go`, `errors.go`, `service_alias.go`, `utils.go`, `service_lazy.go`, `service_eager.go`, `service_transient.go`.
  - New files: `collection.go`, `phase.go`.
- Dependencies: none new. `github.com/jonboulle/clockwork` is already a dependency since #5.
- Docs: health-check, options, starter, component-kinds, container-hooks (new), collection-invocation (new), struct injection, and the upgrading page for every flagged behavior change.
- **Prerequisite:** PR #4 merged and `add-start-lifecycle-and-kinds` archived. Until then, `openspec validate --strict` cannot resolve the MODIFIED deltas.
- **Removed:** the change directories `add-container-lifecycle-hooks` and `add-collection-invocation`.

## Defaults & Control

| Behavior | Zero-config default | Override / opt-out |
|---|---|---|
| Per-service `HealthCheck*` | Unbounded unless `HealthCheckTimeout` is set; then bounded by it | Leave `HealthCheckTimeout` at 0 |
| Container hooks | None registered | `HookContainer*` fields / `AddContainer*Hook` |
| Observer hook panic | Recovered, operation completes, then re-panics | `HookContainerPanic` |
| Scope hooks | None registered | `HookScope*` fields / `AddScope*Hook` |
| Collections | Every visible non-alias service assignable to `T`, deterministic order | `Ordered`, `WithOrder(n)`, distinct names/scopes |
| Collection struct tag | `do:"*"` on `[]T`/`map[string]T` | Use a named tag to inject a single service |
| Live collection | Re-resolves on every `All()`/`Map()` | Use `InvokeAll` for a snapshot |
| `InvokeAs` pick | Nearest scope, then registration order, then name | Use `InvokeNamed` for a specific service |
| Alias shutdown | Alias never shuts its target | None needed: the target shuts down through its own registration |
| Late registrations / new scopes | Not started until `Refresh` or `Scope.Start` | Call `Refresh`/`Scope.Start`, or start nothing |
| Start parallelism | Sequential (`StartParallelism` 0/1) | `StartParallelism: n` |
| Post-build hook | None registered | `HookAfterBuild` / `AddAfterBuildHook` |
| Clone and kind handlers | Replaced handlers are copied | Re-register a different handler on the clone |
| Start failure error | `*StartError` with unchanged messages | `errors.As(err, &startErr)` to inspect |
| Explained edges | `Source` reported; invoked edges print as before | Read `Source` |
| Container phase | `Phase()` is read-only | None needed: observation only |

### Assumptions to confirm in review

These were not decided explicitly, so I chose and recorded them:
1. `Refresh`/`Scope.Start` before a successful `Start` (idle or failed) return `ErrNotStarted`. Concurrent `Refresh` calls are serialized.
2. Scope hooks fire for child scopes only, both on explicit `Scope.Start` and when a child scope shuts down, directly or as part of its parent's shutdown. The root uses container hooks.
3. `RootScope.Phase()`: a close phase dominates once Shutdown has begun.
4. `HookAfterBuild` fires after every successful provider call (each transient instance included) and on an eager value's first successful invocation. Hook panics propagate, as with other per-service hooks.
5. An edge recorded more than once keeps the strongest source: invoked > depends-on > declared.
6. `Collection[T]` captures the invoking scope, so self-exclusion and edge recording work as for `InvokeAll`.

## Defect

1. **Alias over eager: double shutdown.** Reproduced in 200/200 runs, on a scratch copy outside the repo:
   ```go
   root := doger.New()
   e := &eagerProbe{}            // Shutdown() error counts calls
   doger.ProvideValue(root, e)
   _ = doger.As[*eagerProbe, iface](root)
   _, _ = doger.Invoke[iface](root)
   root.Shutdown()               // e's Shutdown called 2 times; expected 1
   ```
2. **`mergeShutdownReports`** of two successful reports returns `Succeed: false` with no errors. Reproduced at unit level (`errors.go:91`, `out.Succeed = len(out.Errors) > 0`). Expected: `true`. This is not observable through the public API today.

Each fix starts with a failing regression test. The `healthCheckPool` race was not reproduced and gets no fix.
