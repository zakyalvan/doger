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
- `New()` no longer allocates the default `Runnable` handler entry: every container starts with one shared default kind list, and `Kind.Handle` copies a list before writing it (copy-on-write), so containers and clones never see each other's handlers. This removes 2 allocs per container.
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
- Live collections: `InvokeCollection[T](i) Collection[T]`, whose `All(r)`/`Map(r)` re-resolve on each call. `r` is the reader's injector: cycle detection and edges follow the reader, as for `InvokeAll` from `r`. (The reads first took no argument; they changed before any release, so no consumer breaks.)
- **Behavior change (flagged):** `InvokeAs[T]` and the implicit-aliasing struct fallback pick the first match deterministically: nearest scope, then registration order, then name.
- **Behavior change (flagged):** per-service `HealthCheck*` functions honor `HealthCheckTimeout` when it is set.

**BREAKING (edge cases only, flagged):**
- A service literally named `*` can no longer be injected through a struct tag, because `do:"*"` now means a collection.
- Adding exported fields to `InjectorOpts` and `ExplainServiceDependencyOutput` breaks only unkeyed struct literals, which the Go 1 compatibility guidelines exclude.

**Fixes from verification (design Part V).** `/opsx:verify` and a code review found defects, each reproduced by a failing test first:
- Shutdown during a start could yield an error that does not match `ErrStartAfterShutdown` (`DI: start aborted: %!w(<nil>)`), from a race between setting the close phase and setting the cancel cause.
- With `StartParallelism > 1`, a panicking per-service start hook crashed the process. It now reaches the caller as in sequential mode.
- `Refresh`/`Scope.Start` during a running Start blocked (or deadlocked from Start-time code). They now fail fast with `ErrNotStarted`.
- A child scope shutdown could race a running start, leaving a started service unowned. It now waits for a run that is starting its services (**behavior change, flagged**).
- A live collection held by a provider failed with a false circular dependency once its provider had returned. Reads after the provider returned drop the build-time chain.
- `HookAfterBuild` ran under the wrapper lock, so a hook touching its own service deadlocked. It now runs after the lock is released.
- `InvokeAs` returned services hidden by a same-name service in a nearer scope. It now honors shadowing (**behavior change, flagged**).
- Also: `DependencySource` decodes from JSON; Override drops replaced wrappers from the start bookkeeping; `StartParallelism` beyond the number of Starters no longer wraps negative; the `*` tag requires exactly `map[string]E`.

**Fixes from the second to fourth verifications (design Parts V2, V3, V4).** Each defect was reproduced by a failing test first:
- `InvokeAs` and the struct fallback skip the service whose provider calls them, so a decorator resolves the service it wraps; a lone self-match fails with `ErrServiceNotMatch` (**behavior change, flagged**, V27). Aliases whose target chain resolves to that service are skipped too (**behavior change, flagged**, V40).
- A per-service hook panic during Start runs the Failed hooks before it is re-raised (**behavior change, flagged**, V28).
- A start that Shutdown interrupts matches `ErrStartAfterShutdown` even when the caller had cancelled its context first, and so does the Failed error after a pipeline panic (**behavior change, flagged**, V38, V39).
- `Scope.Start` on a scope that is shutting down runs no scope Starting hook (**behavior change, flagged**, V41).
- A child scope shutdown that ran entirely between a run's collection and its claim no longer makes the run fail with `ErrServiceNotFound`, or start a same-name ancestor Starter twice (V37).
- Fixes from the fifth verification and the audit that followed (design Part V5): an alias registered with a nil injector no longer makes `InvokeAs` from a provider fail with a nil pointer (V62); cycle detection compares services, so a same-name service in another scope is no longer a false cycle (V63); `ExplainNamedService` reads edges for the registering scope and no longer overflows the stack on a graph cycle (V64); the `InvokeStruct` fallback decides by name, so a nested not-found error is returned and a decorator registered under its interface resolves the service it wraps (**BREAKING edge case, flagged**, V65); overlapping shutdowns of one child scope join instead of walking it twice (**behavior change, flagged**, V66).
- Scope extension (V50–V53): the `DefaultRootScope` tests are safe under `-count` > 1; live-collection reads take the reader's injector, which removes both documented hazards (internal to this unreleased change); `InjectorOpts.HealthCheckSingleTimeout` overrides the single-service bound (additive, default unchanged); `InvokeAs` and `New` are optimized where `benchstat` proves a gain.

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
  - Core package: `root_scope.go`, `root_start.go`, `scope.go`, `kind.go`, `runnable.go`, `dag.go`, `dependency.go`, `di_explain.go`, `di_lifecycle.go`, `invoke.go`, `di.go`, `injector.go`, `options.go`, `ordering.go`, `errors.go`, `service_alias.go`, `service_lazy.go`, `service_eager.go`, `service_transient.go`, `virtual_scope.go`.
  - New files: `collection.go`, `phase.go`, `root_refresh.go`.
- Dependencies: none new. `github.com/jonboulle/clockwork` is already a dependency since #5.
- Docs: health-check, options, starter, component-kinds, container-hooks (new), collection-invocation (new), struct injection, and the upgrading page for every flagged behavior change.
- **Prerequisite:** PR #4 merged and `add-start-lifecycle-and-kinds` archived. Until then, `openspec validate --strict` cannot resolve the MODIFIED deltas.
- **Removed:** the change directories `add-container-lifecycle-hooks` and `add-collection-invocation`.

## Defaults & Control

| Behavior | Zero-config default | Override / opt-out |
|---|---|---|
| Container hooks | None registered | `HookContainer*` fields / `AddContainer*Hook` |
| Observer hook panic | Recovered, operation completes, then re-panics | `HookContainerPanic` |
| Scope hooks | None registered | `HookScope*` fields / `AddScope*Hook` |
| Collections | Every visible non-alias service assignable to `T`, deterministic order | `Ordered`, `WithOrder(n)`, distinct names/scopes |
| Collection struct tag | `do:"*"` on `[]T`/`map[string]T` | Use a named tag to inject a single service |
| Live collection | Re-resolves on every `All(r)`/`Map(r)` | Use `InvokeAll` for a snapshot |
| `InvokeAs` pick | Nearest scope, then registration order, then name | Use `InvokeNamed` for a specific service |
| Alias shutdown | Alias never shuts its target | None needed: the target shuts down through its own registration |
| Late registrations / new scopes | Not started until `Refresh` or `Scope.Start` | Call `Refresh`/`Scope.Start`, or start nothing |
| Start parallelism | Sequential (`StartParallelism` 0/1) | `StartParallelism: n` |
| Post-build hook | None registered | `HookAfterBuild` / `AddAfterBuildHook` |
| Clone and kind handlers | Replaced handlers are copied | Re-register a different handler on the clone |
| Start failure error | `*StartError` with unchanged messages | `errors.As(err, &startErr)` to inspect |
| Explained edges | `Source` reported; invoked edges print as before | Read `Source` |
| Container phase | `Phase()` is read-only | None needed: observation only |
| Incremental start during Start | Fails fast with `ErrNotStarted` | Call `Refresh` after `Start` returned |
| Child scope shutdown during a start | Waits for a run that is starting the scope's services | Bound the wait with `ShutdownWithContext` |
| Live collection read | Follows the reader's injector: a provider passes its own (edges, self-exclusion, cycles); nil records nothing | Pass the injector of the code that reads |
| Single-service health checks (`HealthCheck[T]`, `HealthCheckNamed` and their `WithContext` forms) | Unbounded unless `HealthCheckTimeout` is set; then bounded by it | `HealthCheckSingleTimeout`: positive for its own bound, negative for none; or leave `HealthCheckTimeout` at 0 |
| `InvokeAs` and shadowing | A shadowed ancestor service is not a candidate | Invoke the ancestor by name from its own scope |
| `InvokeAs` from a provider | The provider's own service, and its aliases, are not candidates | Use `InvokeNamed` to resolve a specific service |
| `Scope.Start` while the scope shuts down | Starts nothing, runs no scope hook, returns nil | Call `Scope.Start` after the shutdown returned |
| Start interrupted by Shutdown | The error matches `ErrStartAfterShutdown`, and the caller's cause when it cancelled first | None needed: match either with `errors.Is` |
| Per-service hook panic during Start | Failed hooks run, then the panic is re-raised | Recover the panic in the hook |
| Failed Starter or kind handler on `Refresh` | Tried again by the next `Refresh`/`Scope.Start` | Remove or fix the service before the next run |
| Post-build hook and locking | Runs after the wrapper lock is released | None needed |

### Assumptions to confirm in review

These were not decided explicitly, so I chose and recorded them:
1. `Refresh`/`Scope.Start` before a successful `Start` (idle or failed) return `ErrNotStarted`. Concurrent `Refresh` calls are serialized.
2. Scope hooks fire for child scopes only, both on explicit `Scope.Start` and when a child scope shuts down, directly or as part of its parent's shutdown. The root uses container hooks.
3. `RootScope.Phase()`: a close phase dominates once Shutdown has begun.
4. `HookAfterBuild` fires after every successful provider call (each transient instance included) and on an eager value's first successful invocation. Hook panics propagate, as with other per-service hooks.
5. An edge recorded more than once keeps the strongest source: invoked > depends-on > declared.
6. `Collection[T]` captures the holder's scope and name. Each read takes the reader's injector, so self-exclusion, edges and cycle detection follow the reader as for `InvokeAll`; a nil reader records no edge (V51, superseding V5).

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
