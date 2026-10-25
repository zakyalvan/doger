# Design

## Context

See proposal.md (Why, What Changes) and the specs `collection-invocation` and `collection-ordering`.

Current mechanics this change builds on:
- **`invokeByGenericType`** (`invoke.go:137-199`) walks `serviceForEachRec` and stops at the first service for which `serviceCanCastToGeneric[T]` is true. That check is a reflect-type check on `getReflectType()`, which every wrapper (lazy, eager, transient, alias) implements without building.
  - It detects cycles with `virtualScope.detectCircularDependency(realName)`.
  - It builds via `getInstanceAny(newVirtualScope(serviceScope, chain+realName))`.
  - It records the edge with `vScope.addDependency` **after** a successful build. `invokeAnyByName` records it before building.
  - It fires `onBeforeInvocation`/`onAfterInvocation` and `Logf` with the **requested type name** (`serviceAliasName`), but uses the **real name** for the chain, the edge and `serviceScope.onServiceInvoke`. No existing test asserts the hook name (`invoke_test.go:353-373`).
- **`serviceForEachRec`** (`scope.go:697`) visits the **current scope first, then ancestors**, over map snapshots taken under `RLock`. It is unordered within a scope. A `virtualScope` delegates to the scope where the invoking service is registered.
- **`AsNamed`** (`di.go:501`) refuses an alias unless `Initial` is assignable to `Alias`. So an alias can never match `T` while its target does not.
- **Shutdown** removes services from the scope map (`scope.go:776`). A collection after shutdown therefore sees nothing.
- **From `add-start-lifecycle-and-kinds`:**
  - a per-service registration index, unique per root container (start-lifecycle D3), kept by `Override*` and copied, with the counter, on clone;
  - service metadata carried by the opaque `ProvideOption` (start-lifecycle D1, D2), which `registration-options` explicitly lets later changes extend.
- `ServiceTypeAlias` and `getServiceType()` already exist (`service.go:36`, `service_alias.go:59`).
- **Error style** (`errors.go`, `invoke.go`): sentinel errors with a `DI:` prefix, wrapped with `fmt.Errorf("...%w...")`. `handleProviderPanic` turns a panic value into an error (`error` values kept as-is, others become `DI: %v`). `MustInvoke` uses `must1`, which panics with the error itself.

## Goals / Non-Goals

**Goals:**
- One shared collection pipeline behind `InvokeAll`, `InvokeAllMap` and their `Must*` variants.
- Per-member semantics (cycle detection, edges, hooks, invocation tracking) shared with `InvokeAs` through one helper, without changing what `InvokeAs` reports.
- Deterministic order with no dependence on map iteration.
- Linear cost in the number of visible services, plus the sort.

**Non-Goals:**
- Caching collections or invalidating them on registration (snapshot semantics only).
- Struct-tag `[]T` injection, live or lazy collections, deterministic `InvokeAs` (proposal Out of scope).
- Parallel building of members. They are built sequentially in default order, which keeps construction deterministic and matches how `Start` builds.

## Decisions

**D1. Candidate selection: nearest-name view, then self, alias and type filters.**
1. Walk the invoking scope and its ancestors with `serviceForEachRec`, keeping the **first occurrence of each name**: nearest wins. Record each candidate's scope depth (root = 0) and registration index.
2. Drop the invoking service itself: when the injector is a `virtualScope`, the candidate whose name equals the last name of `invokerChain` (D3a).
3. Drop aliases (`getServiceType() == ServiceTypeAlias`).
4. Keep only services where `serviceCanCastToGeneric[T]` is true.

Because the type filter comes last, a nearest service that is not assignable to `T` hides an ancestor that is (spec: shadowing row 4). This matches what `InvokeNamed` would resolve for that name.
- *Alternative:* filter by type first, then deduplicate names. Rejected: it would include an ancestor service that named resolution can never reach from this scope.

**D2. Default order: (scope depth asc, registration index asc, name asc).**
- Collect candidates with depth and index, then `slices.SortFunc` by that key. Iteration order of `serviceForEachRec` is irrelevant.
- Within one container, indexes are unique. start-lifecycle D3 copies the counter on clone, so clones keep unique indexes too (spec "Default order in a clone"). The name is a defensive final tie-break, so the order is total even if indexes ever collide.

**D3. Build members sequentially in default order through one shared per-member helper.**
- Extract from `invokeByGenericType` the part after selection into `invokeServiceInstance(injector, vScope, hookName, realName, scope, service) (any, error)`. In order, it does:
  - cycle detection on `realName`;
  - `onBeforeInvocation(scope, hookName)`, `getInstanceAny`, `onAfterInvocation(scope, hookName, err)`;
  - on success only: `addDependency(realName)`, `onServiceInvoke(realName)`, `Logf` with `hookName`.
- `invokeByGenericType` passes `hookName = inferServiceName[T]()`, so `InvokeAs` keeps reporting the type name. `TestInvokeAs_HookReceivesTypeName` is written before the extraction to pin this.
- Collections pass `hookName = realName = member name`.
- Edges are recorded only after a successful build, as `invokeByGenericType` does today, so a failing member records no edge.
- On the first error, return `fmt.Errorf("DI: could not invoke collection member `%s`: %w", name, err)`. The call stops, and members already built stay built (lazy caches are kept).
- *Alternative:* build every member and join the errors with `errors.Join`. Rejected: building after a failure wastes work and can trigger further side effects, while the spec only requires the first failing member.
- *Alternative:* a single `name` parameter. Rejected: it either changes `InvokeAs` hook names (an unflagged behavior change) or gives collection hooks the type name.

**D3a. Self-exclusion.**
- Only the *last* name in the invoker chain is excluded: the service whose provider is running.
- Any earlier chain member that is also a candidate is kept. `detectCircularDependency` then fails the call with `ErrCircularDependency` (spec "Deeper cycles still fail").
- Outside a provider there is no invoker, so nothing is excluded.
- *Alternative:* exclude every chain member. Rejected: it would silently hide genuine cycles.
- *Alternative:* no exclusion. Rejected: then every composite fails, and no override could make including itself work.

**D4. Ordering is a separate stable pass, for `InvokeAll` only.**
After all members are built, compute each member's order value:
1. `WithOrder` metadata, if present. `Order()` is then never called.
2. Otherwise, if `any(instance).(Ordered)` succeeds, `Order()` is called inside a `recover`.
3. Otherwise `0`.

Then run `slices.SortStableFunc` by order value over the default-ordered slice.
- An `Order()` panic becomes `fmt.Errorf("DI: Order() of collection member `%s` panicked: %w", name, cause)`. `cause` is the panic value if it is an error, or `fmt.Errorf("%v", r)` otherwise.
- `InvokeAllMap` skips this pass entirely.
- *Alternative:* sort by `(order, depth, index, name)` in a single pass. Equivalent, but it would need the order value before building, and `Order()` is an instance method.

**D5. `WithOrder` storage and acceptance.**
- `WithOrder(n)` returns a `ProvideOption` that sets `order *int` in the service metadata from start-lifecycle D1. A pointer is used so "set" is distinguishable from "set to 0".
- Because it is an ordinary `ProvideOption`, every helper that takes `...ProvideOption` accepts it. That is all 18 `Provide*`/`Override*`/`Lazy*`/`Eager*`/`Transient*` helpers (spec "WithOrder on every registration helper").
- Applying it again overwrites the value, so the last one wins. It needs no registration-time validation.
- `clone()` copies it, and `Override*` replaces the metadata wholesale, as start-lifecycle specifies. Nothing other than the ordering pass reads it.

**D6. Empty results are non-nil.**
Return `[]T{}` / `map[string]T{}` when nothing matches. On error, return `nil, err`.

**D7. `Must*` variants use `must1`**, so they panic with the exact error value, like `MustInvoke`.

**D8. Concurrency.**
- Candidate selection reads scope maps through the existing snapshotting iterators (`serviceForEachRec` under `RLock`).
- Members are built through the service wrappers, which already serialize their own builds (`serviceLazy.mu`). Concurrent callers therefore build each lazy member exactly once while no shutdown runs.
- A member removed by a concurrent shutdown after the snapshot is still built from the wrapper held in the snapshot. This is the same as a concurrent `Invoke` today, and it is race-free (spec "Concurrent collection and shutdown").
- No new shared state is introduced.

## Risks / Trade-offs

- **[O(N) scan of every visible service on every call]** → `serviceCanCastToGeneric` is a reflect check with no build. Measured by `BenchmarkInvokeAll/{10,100,1000}`. Callers in hot loops should resolve once and keep the snapshot, as documented.
- **[`InvokeAll[any]` builds every non-alias service]** → Documented as a sharp edge, and pinned by a spec scenario. Not prevented, because it is a legitimate (if rare) use.
- **[Shadowing by a non-matching nearest service surprises users]** → Documented with an example. It is consistent with named resolution, and the alternative would expose unreachable services.
- **[Visibility inside a provider is the provider's registration scope, not the requester's]** → Same rule as `Invoke`. Documented in godoc and pinned by "Collection inside a provider sees the provider's scope".
- **[The registration index depends on `add-start-lifecycle-and-kinds`]** → This change is ordered after it. Group 9's RED tests fail if the index is missing or not copied on clone.
- **[Hooks fire once per member, so N hook calls per `InvokeAll`]** → Same as calling `Invoke` N times. Documented.

## Public API & Defaults

New exported identifiers, all purely additive (no breaking change):

```go
func InvokeAll[T any](i Injector) ([]T, error)
func MustInvokeAll[T any](i Injector) []T
func InvokeAllMap[T any](i Injector) (map[string]T, error)
func MustInvokeAllMap[T any](i Injector) map[string]T

type Ordered interface{ Order() int }

func WithOrder(n int) ProvideOption
```

Naming: `*Named` in this package means "the caller passes a name" (`InvokeNamed`, `ProvideNamed`, `AsNamed`). The map variant takes no name, so it is `InvokeAllMap`.

Example (after `rename-module-doger`):

```go
doger.Provide(i, NewAuthMiddleware, doger.WithOrder(-10))
doger.Provide(i, NewLoggingMiddleware)            // order 0 (or its Order())
chain := doger.MustInvokeAll[Middleware](i)       // auth, then logging
```

| Behavior | Default | Override |
|---|---|---|
| Membership | Visible, non-alias, registered type assignable to `T` | Register under another type or scope |
| Shadowing | Nearest name wins, even if not assignable | Use distinct names |
| Self-reference | The invoking service is excluded from its own collection | None needed (including it can only fail) |
| Order | Scope depth, then registration index, then name | `Ordered` on the instance; `WithOrder(n)` (wins) |
| Missing order | `0` | `WithOrder(n)` |
| Errors | First failing member, wrapped with its name; no partial result | Resolve members individually |
| `nil` injector | `DefaultRootScope` (as `Invoke`) | Pass an explicit injector |
| `InvokeAs` hooks | Unchanged: requested type name | — |

## Test Strategy

| Scenario | Test | Kind |
|----------|------|------|
| InvokeAs hooks receive the requested type name | `invoke_all_test.go: TestInvokeAs_HookReceivesTypeName` | unit (characterization) |
| Membership by registered type (4 rows) | `invoke_all_test.go: TestInvokeAll_Membership/<T>` | table |
| Eager values are members without a provider call | `invoke_all_test.go: TestInvokeAll_EagerMembers` | unit |
| Nil injector uses the default root scope | `invoke_all_test.go: TestInvokeAll_NilInjector` (restores `DefaultRootScope`) | unit |
| Typed-nil member is returned as is | `invoke_all_test.go: TestInvokeAll_TypedNilMember` | unit |
| Scope visibility and shadowing (5 rows) | `invoke_all_test.go: TestInvokeAll_Visibility/<row>` | table |
| Interface type any matches every non-alias service | `invoke_all_test.go: TestInvokeAll_AnyType` | unit |
| Collection inside a provider sees the provider's scope | `invoke_all_test.go: TestInvokeAll_ProviderScopeVisibility` | unit |
| Alias does not duplicate its target | `invoke_all_test.go: TestInvokeAll_AliasExcluded` | unit |
| Empty results (4 rows) | `invoke_all_test.go: TestInvokeAll_Empty/<function>_<services>` | table |
| Member failure modes (3 rows) | `invoke_all_test.go: TestInvokeAll_MemberFailure/<behavior>` | table |
| Must variants panic with the same error (2 rows) | `invoke_all_test.go: TestMustInvokeAll_Panics/<function>` | table |
| Must variants return the collection on success (2 rows) | `invoke_all_test.go: TestMustInvokeAll_Success/<function>` | table |
| Composite collects every other implementation | `invoke_all_test.go: TestInvokeAll_CompositeExcludesSelf` | unit |
| Deeper cycles still fail | `invoke_all_test.go: TestInvokeAll_DeeperCycle` | unit |
| Map collection keys (2 rows) | `invoke_all_test.go: TestInvokeAllMap_Keys/<from>` | table |
| Dependency edges order shutdown | `invoke_all_test.go: TestInvokeAll_EdgesOrderShutdown` | unit |
| Invocation hooks per member | `invoke_all_test.go: TestInvokeAll_HooksPerMember` | unit |
| No edges when invoked outside a provider | `invoke_all_test.go: TestInvokeAll_NoEdgesOutsideProvider` | unit |
| Transient and singleton members across calls | `invoke_all_test.go: TestInvokeAll_TransientFresh` | unit |
| Later registration is not visible in an earlier snapshot | `invoke_all_test.go: TestInvokeAll_Snapshot` | unit |
| Caller mutation does not leak | `invoke_all_test.go: TestInvokeAll_CallerMutation` | unit |
| Collections after shutdown | `invoke_all_test.go: TestInvokeAll_AfterShutdown` | unit |
| Concurrent callers build each member once | `invoke_all_test.go: TestInvokeAll_ConcurrentSingleBuild` (`testWithTimeout`, `-race`) | race |
| Concurrent registration and collection | `invoke_all_test.go: TestInvokeAll_ConcurrentRegistration` (`testWithTimeout`, `-race`) | race |
| Concurrent collection and shutdown | `invoke_all_test.go: TestInvokeAll_ConcurrentShutdown` (`testWithTimeout`, `-race`) | race |
| Default order across scopes (4 rows) | `invoke_all_order_test.go: TestInvokeAll_DefaultOrder/<row>` | table |
| Order is stable across repeated calls | `invoke_all_order_test.go: TestInvokeAll_DefaultOrderStable` | unit |
| Override replaces a registration in place | `invoke_all_order_test.go: TestInvokeAll_OverrideKeepsPosition` | unit |
| Default order in a clone | `invoke_all_order_test.go: TestInvokeAll_DefaultOrderClone` | unit |
| Ordered values (6 rows) | `invoke_all_order_test.go: TestInvokeAll_Ordered/<values>` | table |
| Order is read from each transient instance (2 rows) | `invoke_all_order_test.go: TestInvokeAll_OrderedTransient/call_<n>` | table |
| Precedence (5 rows) | `invoke_all_order_test.go: TestInvokeAll_WithOrderPrecedence/<row>` | table |
| WithOrder on every registration helper (18 rows) | `invoke_all_order_test.go: TestInvokeAll_WithOrderHelpers/<helper>` | table |
| Order() is not called when WithOrder is set | `invoke_all_order_test.go: TestInvokeAll_WithOrderSkipsOrderMethod` | unit |
| WithOrder outside collections has no effect | `invoke_all_order_test.go: TestWithOrder_NoEffectOnInvoke` | unit |
| Panicking Order() (3 rows) | `invoke_all_order_test.go: TestInvokeAll_OrderPanic/<panic>` | table |
| MustInvokeAll surfaces the Order() panic as its panic | `invoke_all_order_test.go: TestMustInvokeAll_OrderPanic` | unit |
| Shutdown ignores collection order | `invoke_all_order_test.go: TestInvokeAll_OrderDoesNotAffectShutdown` | unit |
| Start ignores collection order | `invoke_all_order_test.go: TestInvokeAll_OrderDoesNotAffectStart` | unit |
| Order() not called for map collections | `invoke_all_order_test.go: TestInvokeAllMap_IgnoresOrdered` | unit |

**Documentation tests** (`di_example_test.go`): `ExampleInvokeAll`, `ExampleInvokeAllMap`, `ExampleOrdered`, `ExampleWithOrder`. Each documents a default (empty non-nil result, registration order, order 0, self-exclusion) and the matching override.

**Hot paths and their coverage:**
- **Resolution/invocation** (shared per-member helper from D3): `TestInvokeAll_*`, plus `TestInvokeAs_HookReceivesTypeName` and the unchanged `TestInvokeAs`, `TestMustInvokeAs`, `TestInvokeByGenericType` and `TestInvokeByGenericType_race`. Together these prove the extraction did not change `invokeByGenericType`.
- **Scope traversal** (D1, D2): `TestInvokeAll_Visibility`, `TestInvokeAll_ProviderScopeVisibility`, `TestInvokeAll_DefaultOrder`, `TestInvokeAllMap_Keys`.
- **Cycles and self-exclusion** (D3a): `TestInvokeAll_CompositeExcludesSelf`, `TestInvokeAll_DeeperCycle`.
- **Concurrency:** `TestInvokeAll_ConcurrentSingleBuild`, `TestInvokeAll_ConcurrentRegistration` and `TestInvokeAll_ConcurrentShutdown` under `-race`, with goleak via `TestMain`.
- **Lifecycle interaction:** `TestInvokeAll_EdgesOrderShutdown`, `TestInvokeAll_OrderDoesNotAffect{Shutdown,Start}`, `TestInvokeAll_AfterShutdown`.
- **Performance:** `bench/do_bench_test.go: BenchmarkInvokeAll/{10,100,1000}` (visible services, half matching) and `BenchmarkInvokeAllParallel`, recorded with `-benchmem`. Not a gate, but the baseline for future changes.

## Migration Plan

Additive only. No migration. Rollback is a revert.
