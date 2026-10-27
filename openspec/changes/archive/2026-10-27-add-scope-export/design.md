# Design

## Context

See proposal.md (Why) and this change's specs. The current code shapes the approach as follows:

- **Aliases.** `serviceAlias` (`service_alias.go`) resolves its target by name from the injector it was registered in (`invokeByName[Initial](i, targetName)`). Its `shutdown` is a no-op, and it delegates `isHealthchecker`/`healthcheck` to the target. Collections skip `ServiceTypeAlias` (`collectionCandidatesFor`, `invoke.go:359`). By-type selection includes aliases (`selectByType`).
- **Scope shutdown** (`Scope.shutdownWithContext`, `scope.go:440`) shuts all child scopes down in parallel, then the scope's own services. Within a scope, `shutdownServicesInParallel` repeatedly shuts the services that have no dependents (`DAG.listServicesHavingNoDependent`), and dependents in other scopes count. A child service that a root service depends on is therefore never free while the root service is alive. The loop then falls back to shutting everything down in parallel, which is the problem behind the audit's V1.
- **Cycle detection** walks the invoker chain and compares name *and* scope (`virtualScope.chainContains`, `virtual_scope.go:167`). An export usually publishes under its source's name, but the hop from the export to its source crosses scopes, so it is not mistaken for a cycle.
- **Clone** (`Scope.clone`, `scope.go:727`) clones a scope's services *before* its child scopes, so a service's `clone(newScope)` can't hold a pointer to the cloned child scope.
- **Dependency edges** carry both scope IDs (`DAG.addDependency`, `dag.go:138`). Start ordering already follows transitive edges through non-candidates.

## Goals / Non-Goals

**Goals:**
- Exports with alias-like lifecycle: one owner, one start, one shutdown.
- Correct shutdown order across exports, including a sibling child that uses a parent dependent, with no change for scopes without exports.
- Exports that survive clone, and fail cleanly after their source scope is shut down.

**Non-Goals:**
- General cross-scope shutdown ordering for hand-written parent-to-child invocations that don't go through an export. Behaviour there is unchanged and still documented as unsupported.
- Exporting from a non-direct child. Chains are built hop by hop, as the grandchild scenarios show.
- Root `HealthCheck` covering child scopes in general. That is `add-extensions`' scope, limited to extension scopes.

## Decisions

### D1. A new wrapper, `serviceExport[T]`, alongside `serviceAlias`
- **Fields:** the owner scope (the parent), the source child's *name* (not a pointer), the source service name, the published name, the registration index, and invocation frames.
- **`getServiceType()`** returns `ServiceTypeAlias`, so listings and explain show "alias" and Start skips it through its existing alias check.
- **Marker interface** `serviceWrapperExport { exportSource() (*Scope, string, bool); withinSource(*Scope) bool }`. Collections and shutdown use it to tell an export from an alias. `withinSource` reports whether a scope is the source scope or a descendant by comparing names and parents, which never change, so it takes no lock (the health walk calls it while holding the owner's read lock). Rules never assert the interface directly: a decorator (a default, D8) exposes one method, `unwrapExport() (export serviceWrapperExport, yielded bool)`, and two helpers derive from it: `asExport` (an export, or a decorated one that has not yielded: the live check used by every rule) and `wrapsExport` (an export, or any decorated one: the structural check used by the counter, D7).
- *Alternative:* extending `serviceAlias` with an optional source scope. Rejected: `serviceAlias` resolves by name through the invoker's scope, and every branch would need an "is export" check. A separate type keeps aliases untouched (compatibility) and keeps export rules in one place.

### D2. Resolving the source by scope name, at every invocation
- `exportSource()` looks the child up among the owner's *direct* children (`owner.childScopes[childName]` under `owner.mu.RLock()`), then the source wrapper with the child's own `serviceGet(sourceName)`. Each lookup is one short read lock, and no lock is held across the two. `ChildByName` is not used: it searches the whole subtree in map order, so a same-name grandchild could hijack the export (verified by the audit in 31 of 200 runs).
- If the child scope or the source service is gone, the export returns an error matching `ErrServiceNotFound` that names both scopes.
- **Why names:**
  - Clone works with no special handling: a cloned export resolves the direct child on the cloned owner, which holds the cloned child once `clone` finishes.
  - After a parent shutdown (children removed from the map) or a direct child shutdown (service removed), resolution fails as the spec requires.

### D3. Invoking the source directly, one hop, with hooks and an edge
- `getInstance(vs)` invokes the source wrapper directly:
  - with a child virtual scope whose predecessor is the source scope;
  - running the before/after-invocation hooks for the source scope;
  - recording `addDependency(owner/published → child/source)`;
  - calling `onServiceInvoke` on the source scope.
- Cycle detection is already scope-qualified (`virtualScope.chainContains` compares name and scope), so the hop needs no special case even though the published name and the source name are often equal. A real cycle (the scenario "Source provider depends back on the parent dependent") is still reported, and a loop through an export and a default is `ErrCircularDependency` instead of a stack overflow.
- The helper is `invokeWrapperInScope` (`service_default.go`), which already exists from `add-default-registrations` (its design D2). This change reuses it unchanged.

### D4. Lifecycle forwarding
| Method | Behaviour |
|---|---|
| `shutdown` | no-op (the wrapper is removed from the parent's map, as for aliases) |
| `isShutdowner` | `false` |
| `isHealthchecker` / `healthcheck` | delegate to the source wrapper (`nil`/`false` if the source is gone). `Scope.asyncHealthCheckWithContext` gets the requesting scope passed down its ancestor walk, and skips an export when the requesting scope is the export's source scope or a descendant of it, so a check from the source scope or its descendants does not run the source twice or report it under a second name |
| `clone(newOwner)` | a new `serviceExport` with the same names and a fresh frame set |
| Start / kinds | never a candidate (`ServiceTypeAlias`) |

### D5. Shutdown order with exports
In `shutdownWithContext`, when the scope holds at least one export (per-scope count, see D7), shutdown orders services by dependency path across the scope and its *exporting descendants*. Without exports it is unchanged.

```
1. non-exporting children of the scope and of every exporting descendant   (parallel, full shutdowns, as today)
2. the scope's Closing hook
3. one dependency-ordered pass over the services of the scope and of every exporting descendant:
   a service shuts down once no service still running in the pass depends on it
4. the scope's Closed hook
```

- **The pass set.** The scope plus, recursively, every child that exports into its parent (export chains). Their non-exporting children are not in the pass: nothing can depend on them across scopes except through a hand-written forwarding provider (a non-goal), so they shut down first, children before their parent as before.
- **One snapshot.** Dependents are read from one snapshot of the DAG edges among the pass set, taken before anything is removed. Removing a service from the DAG during the pass would erase transitive paths (`x` in the child → `p2` → `p1` → export → pool: once `p1` and `p2` are gone, the pool looks free while `x` still uses it). Dependents outside the pass set are ignored, as they are alive scopes this shutdown does not own.
- **Why the review cases work.** A child service that depends on a parent dependent of the export (`x` → `p` → export → pool) is ordered by the path, not by scope: `x`, `p`, export, pool. Exporting siblings (`a/x` → root export of `b`'s pool) are in one pass, so `x` shuts down before the pool. The previous four steps could express neither: step 2 fell back to parallel shutdown and step 3 shut exporting siblings down in parallel.
- **Cycles:** a true cycle in the snapshot makes the pass fall back to shutting the remaining services down in parallel, as the existing loop does.
- **Scope hooks:** the scope's Closing hook runs before the pass and its Closed hook after it, so they bracket the scope's own services as before. An exporting descendant's Closing hook runs just before its first service shuts down (or, for a scope with no service left, once its exporting descendants closed), and its Closed hook once its services and its exporting descendants are done. Exporting children therefore close *inside* their parent's Closing/Closed pair. That is an exception to "children before their parent", specified as a MODIFIED requirement in `scope-lifecycle-hooks` and flagged as a behavior change for exporting scopes only.
- **Joins and start waits.** Each exporting descendant still takes part in the child-scope shutdown protocol (`joinClose`, `waitForStartingRun`, `finishScopeClose`), so a concurrent `Shutdown` of it joins this one and receives its part of the report. If it is already shutting down elsewhere, this shutdown waits for that one (bounded by its context) and leaves its services out of the pass. A panic in the pass (for example from a custom `Logf`) releases every joined descendant that has not finished with a failed report containing "DI: scope shutdown did not complete", as a single child scope's shutdown does.

### D6. Collections and by-type selection
- `collectionCandidatesFor` keeps the alias exclusion, except for exports. An export is skipped only when the reader's scope is the export's source scope or one of its descendants (an ancestor walk from `source.from`).
- `selectByType` already includes aliases. Read from inside the source scope, the source itself is nearer and wins. It skips an alias of the invoking provider (`aliasResolvesTo`); that check also follows an export to its source and compares **scope and name** with the provider (its virtual scope's scope and name), so a decorator never selects its own export renamed with `ExportAs`. Comparing names only would skip an unrelated same-name service of another scope.
- One helper, `excludedAlias(service, reader)`, decides the alias exclusion of collections with a single `asExport` call: an `As`/`Bind` alias is excluded, an export is excluded only when the reader is inside its source scope. Both collection walks use it.
- An export's collection order is its own registration index in the owner scope, as for any member; `WithOrder` on the source does not move it.

### D7. Registration and counters
- `Export`/`ExportNamed` validate (nil `from`, no parent, source not in `from` itself, type not assignable via `typeCanCastToType`, empty name). `validateExport` returns the source wrapper it found, and the default decision (D8) uses that same wrapper, so validation and wrapping read one lookup. They then register through `add-default-registrations`' `registerLocked` helper (its D4 and task 3.3) under the parent's write lock, so check-then-set is atomic, and return its error instead of panicking. A name taken by a non-default, or by a resolved default, returns the duplicate error; an explicit export replaces an unresolved default of the published name; an export of a default follows D8.
- The registration hooks run as `registerLocked` runs them for `provide`: before-hook first, after-hook only when stored or replaced.
- Each scope keeps an export count, an `atomic.Int32` sharing one word with the default count (narrowed from `Int64` to `Int32`), so `Scope` stays 128 bytes and `RootScope`, which embeds it, stays in its 352-byte size class. It counts structurally (`wrapsExport`: an export, or a default decorating one, whatever the default decided), so increments and decrements stay paired. It is updated in `storeLocked` and `serviceShutdown`, and set by `Scope.clone` from the wrappers it copies. When the count is zero, shutdown runs the old code path unchanged, the health skip is not evaluated, and the collection walk drops the export rule; that rule is guarded by `chainHasExports` on the reader, captured as a single pointer so the walk's closure keeps its allocation size class.

### D8. An export of a default is a default
- `Export` reads the source wrapper. If it is a `serviceDefault`, the export wrapper is wrapped as a default too (`serviceDefault[T]` around `serviceExport[T]`), so in the parent it follows every default rule: it yields to ancestors, an explicit same-name registration replaces it while unresolved, and registering it over an explicit service is skipped (`Export` returns nil).
- **No recursion:** the source default's `decide()` skips default wrappers, so it never yields to its own export, which is a default. This is what fixes the loop the audit found (B1), where an explicit export made the private default yield to the export, which resolved the private default again.
- **Application wins:** when the application's explicit service is in the parent (before or after `Export`), the parent's name resolves to it, and the private default also yields to it (it is in an ancestor). Every reader sees the application's instance.
- **The export marker survives the wrapping:** `serviceDefault` implements `unwrapExport()`, returning the decorated export and whether the default yielded (once it yielded it is an alias of the application's service, not an export). `asExport` uses it, so every rule keyed on the marker (D4 health skip, D5 pass set, D6 collections and by-type, and `add-extensions`' health skip) sees exports of defaults, which is the shape every extension export takes. `serviceDefault` does not implement `serviceWrapperExport` itself: if it did, every default registration would look like an export to the shutdown planner.

## Risks / Trade-offs

- [The four-step shutdown changes ordering for scopes that hold exports] → it is limited to such scopes (counter). The scenario "Scopes without exports keep children-first shutdown" and the full existing suite pin the unchanged path.
- [Lookup by child name breaks if the user re-creates a same-name child after shutting the original down] → `Scope(name)` panics on a reserved name today (audit V4), so it can't happen. It is documented in the godoc of `Export`.
- [The cross-scope pass snapshots the DAG edges among the pass set at each shutdown of a scope holding exports] → it only runs when the export count is non-zero, and shutdown is not a hot path; the benchmark gate is repeated after it.
- [Each exporting descendant joins the shutdown protocol from its ancestor's pass] → it reuses `joinClose`/`finishScopeClose`; the existing join tests and a new concurrent `Shutdown` of an exporting child during a root shutdown pin it. A benchmark is not required, but the existing `bench/` suite is compared with `benchstat` for `Invoke` and `InvokeAll`, to check that the collection check costs nothing without exports.

## Public API & Defaults

| Identifier | Kind | Default / Override |
|---|---|---|
| `Export[T any](from *Scope, opts ...ExportOption) error` | new func | Publishes `NameOf[T]()` under the same name in `from`'s parent |
| `ExportNamed[T any](from *Scope, name string, opts ...ExportOption) error` | new func | Publishes `name` under the same name |
| `MustExport[T any](from *Scope, opts ...ExportOption)`, `MustExportNamed[T any](from *Scope, name string, opts ...ExportOption)` | new funcs | Panic with the error |
| `ExportOption` | new opaque func type | Values only from this package's constructors |
| `ExportAs(name string) ExportOption` | new option | Overrides the published name; empty is an error |

No exported identifier changes and nothing is BREAKING. `Export` takes `*Scope` because the source must be a concrete scope with a parent. A `*RootScope` can't be passed, so the "no parent" case is reachable only through the root's own `*Scope` (for example from a hook), and it is rejected.

## Test Strategy

The production code goes in `export.go` (new), tested in `export_test.go`; the cross-scope shutdown pass (D5) goes in `export_shutdown.go` (new), tested with the other shutdown ordering tests in `scope_test.go`. The shutdown ordering is tested in `scope_test.go`, with an example in `export_example_test.go`. Tests are table-driven with named subtests and testify, run under `-race`, and use `testWithTimeout` around shutdown tests.

| Scenario | Test | Kind |
|---|---|---|
| Export publishes under the source or a chosen name | export_test.go: `TestExport_Publishes/{source_name,export_as,export_named_as}` | unit, table |
| Exported services stay private otherwise | export_test.go: `TestExport_OthersStayPrivate` | unit |
| Export from a grandchild through two scopes | export_test.go: `TestExport_Chained` | unit |
| Invalid exports are rejected | export_test.go: `TestExport_Invalid/{nil_scope,root_scope,missing,type_mismatch,empty_name,duplicate}` | unit, table |
| A service visible only through an ancestor cannot be exported | export_test.go: `TestExport_SourceMustBeOwn` | unit |
| Must variants panic with the error | export_test.go: `TestMustExport_Panics/{must_export,must_export_named}` | unit, table |
| Invocation scope does not change the target | export_test.go: `TestExport_ResolvesFromSource/{root,app,redis}` | unit, table |
| An export is listed as an alias | export_test.go: `TestExport_ListedAsAlias` | unit |
| Exported Starter and Shutdowner run once | export_test.go: `TestExport_Lifecycle/{start,shutdown,healthcheck}` | unit, table |
| Root health check reports the exported service | export_test.go: `TestExport_RootHealthCheck` | unit |
| Parent Starter starts after the exported one | export_test.go: `TestExport_StartOrder` | unit |
| Collection membership of an export | export_test.go: `TestExport_CollectionMembership/{root,app,redis,default_source_root,default_source_redis}` | unit, table |
| InvokeAs in the parent finds the export | export_test.go: `TestExport_InvokeAs` | unit |
| Source provider depends back on the parent dependent | export_test.go: `TestExport_Cycle` | unit |
| Clone re-binds the export | export_test.go: `TestExport_Clone` | unit |
| Invoking an export after its source scope shut down | export_test.go: `TestExport_AfterSourceShutdown` | unit |
| One build under concurrent invocation | export_test.go: `TestExport_ConcurrentFirstInvocation` (100 goroutines) | race |
| Parent dependents shut down before the exporting child | scope_test.go: `TestScopeShutdown_ExportOrder/{direct,transitive,declared,default_source,cloned}` | unit, table, timeout |
| Chained exports order shutdown across three scopes | scope_test.go: `TestScopeShutdown_ChainedExportOrder` | unit, timeout |
| A sibling child that uses a parent dependent shuts down first | scope_test.go: `TestScopeShutdown_SiblingBeforeExportDependent` | unit, timeout |
| Scopes without exports keep children-first shutdown | scope_test.go: `TestScopeShutdown_WithoutExportsUnchanged` | unit |
| Closing and Closed around child scope shutdown | scope_hooks_test.go: existing `TestScopeHooks_ClosingClosed` (unchanged) | unit, table |
| Root scope fires no scope hook | scope_hooks_test.go: existing `TestScopeHooks_RootFiresNone` (unchanged) | unit |
| Overlapping shutdowns of a child scope join | root_refresh_test.go: existing `TestScopeShutdown_ConcurrentShutdownsJoin`, `TestScopeShutdown_JoinBoundedByContext`, `TestScopeShutdown_JoinedWinnerPanics` (unchanged) | unit, synctest |
| Closing and Closed around an exporting child | scope_test.go: `TestScopeShutdown_ExportingChildHookOrder/{parent_dependent,child_depends_on_parent}` | unit, table, timeout |
| A same-name grandchild does not capture the export | export_test.go: `TestExport_DirectChildOnly` (100 runs) | unit |
| An export replaces an unresolved default in the parent | export_test.go: `TestExport_ReplacesUnresolvedDefault` | unit |
| Exported target is shut down once | export_test.go: `TestExport_TargetShutdownOnce/{root,published_name,owning_scope}` | unit, table |
| The application's service wins over an exported default | export_test.go: `TestExport_DefaultSource/{export_only_root,export_only_redis,app_first_root,app_first_redis,app_after_root,app_after_redis}` | unit, table |
| Exporting a default where an explicit service exists succeeds without effect | export_test.go: `TestExport_DefaultOverExplicitSkipped` | unit |
| A health check from the source scope checks the exported service once | export_test.go: `TestExport_SourceScopeHealthCheck/{explicit_source,default_source}` | unit, table |
| Alias does not duplicate its target | invoke_all_test.go: existing `TestInvokeAll_AliasExcluded` (unchanged; the MODIFIED text only clarifies exports) | unit |
| An exporting child's service that depends on a parent dependent shuts down first | scope_test.go: `TestScopeShutdown_ChildDependsOnParentDependent/{direct,chain}` (each 50 runs) | unit, table, timeout |
| Exporting siblings order shutdown through their exports | scope_test.go: `TestScopeShutdown_ExportingSiblings` (200 runs) | unit, timeout, race |
| A provider does not select its own export by type | export_test.go: `TestExport_InvokeAsSkipsOwnExport/{direct,chained}` | unit, table |
| An exporting child with no service left closes once | scope_test.go: `TestScopeShutdown_ExportingChildHookOrder/no_service_left` | unit, table, timeout |
| Shutdowns of an exporting child join its parent's shutdown | scope_test.go: `TestScopeShutdown_ExportingChildJoin/{root_first,child_first}`, `TestScopeShutdown_ExportingChildJoinAfterPanic` | unit, table, synctest |
| A dependency cycle through an export still shuts every service down | scope_test.go: `TestScopeShutdown_ExportCycle` | unit, timeout |
| The shutdown report covers the exporting descendants | scope_test.go: `TestScopeShutdown_ExportReport` | unit |
| An exporting child waits for a running start | scope_test.go: `TestScopeShutdown_ExportingChildWaitsForStart` | unit, synctest |

The Risks section adds `TestExport_ShutdownOrderRepeated`: the direct row 200 times under `-race`, which pins that the order doesn't depend on scheduling.

Examples (docs): `ExampleExport` (a private pool in a child scope, the client exported and used by an app in the root) and `ExampleExportAs`, both with `// Output:`.

**Hot paths and their coverage:**
- Export resolution: the publishes, resolves-from-source and concurrent tests.
- Scope shutdown with and without exports: the `TestScopeShutdown_*` export tests and the existing scope shutdown suite.
- The shutdown pass (`export_shutdown.go`): every branch, including a join of a descendant already shutting down elsewhere, the cycle fallback, a descendant with no service left, a descendant's start-wait error and the release of joined descendants after a panic. Only panics inside the per-service goroutines are not exercised; they are unrecoverable, as in the scope shutdown without exports.
- Collection candidates: the membership test and the existing collection suite.
- Start ordering: the start-order test.
- Health check: the lifecycle and root health check tests.
- Clone: the clone test.
- `goleak` in `TestMain` covers the parallel shutdown goroutines.

## Migration Plan

Additive. No consumer migration. Docs: a "Scope export" section on the scopes page and the godoc of the new functions. Rollback is a revert.

Order: archive `consolidate-pending-lifecycle-work` first (this change adds to its `service-aliases` and `collection-invocation` capabilities), then `add-default-registrations`, then this change, then `add-extensions`.
