# Proposal

## Why

A component can only depend on a single service. `InvokeAs[T]` returns one arbitrary match among the services assignable to `T`, and its selection is nondeterministic (see the TODO on `invokeByGenericType`). Common DI practice lets a component depend on *all* implementations of a type, in a defined, overridable order. Examples are event handlers, health contributors, middleware and validators. Without this, consumers maintain hand-written registries that duplicate the container's own knowledge.

## What Changes

**Collection invocation**
- Add `InvokeAll[T](i) ([]T, error)` and `MustInvokeAll[T](i) []T`. They return every visible service whose **registered type** is assignable to `T`. Services that do not match are never built.
- Add `InvokeAllMap[T](i) (map[string]T, error)` and `MustInvokeAllMap[T](i) map[string]T`. They return the same set keyed by service name.
- Visibility: the current scope and its ancestors. When a child scope and an ancestor register the same name, only the nearest one is included. Inside a provider, "current scope" is the scope where that provider's service is registered, as for `Invoke`.
- Aliases (`As`/`Bind`) are excluded, and the original service represents itself. Without this, one instance would appear twice.
- A collection invoked from inside a provider excludes the invoking service itself, so composites work. Example: a `CompositeHandler` registered as `EventHandler` collects every other `EventHandler`. Deeper cycles still return `ErrCircularDependency`.
- No match returns an empty, non-nil collection and a `nil` error, not `ErrServiceNotFound`.
- If any member fails to build, the whole call fails with an error naming that member. No partial collection is returned.
- The invoking service records a dependency edge to **every** member. Construction, start and shutdown order (from `add-start-lifecycle-and-kinds`) therefore treat the collection's members as dependencies. For example, a bus shuts down before its handlers.
- Invocation hooks fire once per member, with the member's name. `InvokeAs` keeps reporting the requested type name to hooks, unchanged.
- Transient members produce a new instance on each call, consistent with `Invoke`.
- The result is a snapshot at call time. A cached singleton does not see members registered later.

**Collection ordering**
- Add `type Ordered interface { Order() int }`, auto-detected on member instances.
- Members are sorted ascending by order value, so a lower value comes first. A member that does not implement `Ordered` counts as `0`. The sort is stable. Equal values keep the default order: ancestor scopes first, then registration order, then service name.
- Add a `WithOrder(n int)` registration option. It is a `ProvideOption` accepted by every `Provide*`, `Override*` and `Package` helper (`Lazy*`, `Eager*`, `Transient*`). It overrides `Order()`, including for third-party types the consumer cannot edit. Precedence: `WithOrder` > `Ordered` > `0`.
- `Ordered` and `WithOrder` affect **collection order only**. They never influence construction, start or shutdown order, which the dependency DAG alone defines.
- A panic in `Order()` is recovered and returned as an error naming the member.
- `InvokeAllMap` returns a map, so ordering does not apply to it.

**Out of scope**
- Struct-tag injection of `[]T` fields (e.g. a `do:"*"` tag).
- Lazy or live collections that see members registered later (phase 2).
- Making `InvokeAs[T]` deterministic, i.e. first match by registration order. It is a candidate follow-up, but it changes behavior of an existing API and must be proposed separately, flagged as such.

## Capabilities

### New Capabilities

- `collection-invocation`: resolving every visible service assignable to a type, as a slice or a name-keyed map. Covers visibility, shadowing, alias and self exclusion, empty results, error semantics, dependency recording, snapshot semantics, and compatibility of `InvokeAs`.
- `collection-ordering`: ordering of resolved collections — the `Ordered` interface, the `WithOrder` option and where it is accepted, defaults, stability and panic handling. It is kept separate from lifecycle ordering.

### Modified Capabilities

None at planning time. `registration-options` and `dependency-declaration` come from `add-start-lifecycle-and-kinds`, which is archived before this change is applied. `registration-options` already permits later option constructors, and `WithOrder`'s requirements, including where it is accepted, live in `collection-ordering`. Task 14.3 adds a MODIFIED delta that appends `WithOrder(n)` to that requirement's "currently" list once the main spec exists, so the archived spec stays accurate.

## Impact

- Core package: `di.go` (new `InvokeAll*` functions), `invoke.go` (collection resolution, ordering, the shared per-member helper), `di_lifecycle.go` or a sibling file (`Ordered`), registration options (`WithOrder`).
- Reuses the registration index (unique per root container, copied on clone), `ProvideOption` metadata and DAG edge recording from `add-start-lifecycle-and-kinds`.
- Purely additive: no existing exported identifier or behavior changes. `InvokeAs` keeps its hook names, which a new characterization test pins.
- Docs: a collection-invocation page and `Example*` tests for each default and override.
- Depends on `add-start-lifecycle-and-kinds`. Independent of `add-container-lifecycle-hooks`.

## Defaults & Control

| Behavior | Zero-config default | Override / opt-out |
|---|---|---|
| Membership | Every visible, non-alias service whose registered type is assignable to `T` | Register under a type not assignable to `T`, or in a scope that the invoking scope does not see |
| Name shadowing | Nearest scope wins | Use distinct service names to include both |
| Self-reference | A service's provider never receives itself in its own collection | None needed: including itself always fails with `ErrCircularDependency`, so there is no working behavior to opt back into |
| Empty result | Empty collection, `nil` error | Caller checks `len` if a non-empty collection is required |
| Member build failure | Whole call fails with the member named | None needed: a silently partial collection is unsafe. The caller can still resolve members individually. |
| Order | Ancestor scopes first, then registration order, then name | Implement `Ordered` on the member, or set `WithOrder(n)` at registration (takes precedence) |
| Members without `Ordered` | Order value `0` | `WithOrder(n)` |
| Lifecycle ordering | Unaffected by `Ordered` / `WithOrder` | Declare dependency edges (`DependsOn`) |
