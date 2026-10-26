# Design

## Context

See proposal.md (Why) and this change's specs. The current code shapes the approach as follows:

- **Aliases.** `serviceAlias` (`service_alias.go`) resolves its target by name from the injector it was registered in (`invokeByName[Initial](i, targetName)`). Its `shutdown` is a no-op, and it delegates `isHealthchecker`/`healthcheck` to the target. Collections skip `ServiceTypeAlias` (`collectionCandidatesFor`, `invoke.go:357`). By-type selection includes aliases (`selectByType`).
- **Scope shutdown** (`Scope.shutdownWithContext`, `scope.go:407`) shuts all child scopes down in parallel, then the scope's own services. Within a scope, `shutdownServicesInParallel` repeatedly shuts the services that have no dependents (`DAG.listServicesHavingNoDependent`), and dependents in other scopes count. A child service that a root service depends on is therefore never free while the root service is alive. The loop then falls back to shutting everything down in parallel, which is the problem behind the audit's V1.
- **Cycle detection** is by name along the invoker chain (`virtual_scope.go`). An export usually publishes under its source's name, so a by-name hop from the export to its source would look like a cycle.
- **Clone** (`Scope.clone`, `scope.go:612`) clones a scope's services *before* its child scopes, so a service's `clone(newScope)` can't hold a pointer to the cloned child scope.
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
- **Marker interface** `serviceWrapperExport { exportSource() (*Scope, string, bool) }`. Collections and shutdown use it to tell an export from an alias.
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
- The helper is `invokeWrapperInScope`, added by `add-default-registrations` (its design D2), which lands first. This change reuses it unchanged.

### D4. Lifecycle forwarding
| Method | Behaviour |
|---|---|
| `shutdown` | no-op (the wrapper is removed from the parent's map, as for aliases) |
| `isShutdowner` | `false` |
| `isHealthchecker` / `healthcheck` | delegate to the source wrapper (`nil`/`false` if the source is gone). `Scope.asyncHealthCheckWithContext` gets the requesting scope passed down its ancestor walk, and skips an export when the requesting scope is the export's source scope or a descendant of it, so a check from the source scope or its descendants does not run the source twice or report it under a second name |
| `clone(newOwner)` | a new `serviceExport` with the same names and a fresh frame set |
| Start / kinds | never a candidate (`ServiceTypeAlias`) |

### D5. Shutdown order with exports
In `shutdownWithContext`, when the scope holds at least one export (per-scope count, see D7), shutdown has four steps. Without exports it is unchanged.

```
1. non-exporting children   (parallel, as today)
2. own services that have a dependency path to an export, plus the export wrappers,
   in dependency order (the existing no-dependents loop restricted to this set)
3. exporting children       (parallel)
4. remaining own services   (the existing loop)
```

- **Why the sibling case works:** a non-exporting sibling (`web`) can only reach the exporting child (`redis`) through a parent service. It shuts down in step 1, before that parent service shuts down in step 2.
- **Why chained exports work:** each scope applies the rule to its own exports when it shuts down.
- **Cycles:** a cycle (exporting child → parent service in the set → export) makes step 2's loop fall back to its existing parallel shutdown, as today.
- **Building the step-2 set:** a reverse walk over the DAG from the scope's export wrappers, under one DAG read lock.
- **Scope hooks:** the scope's Closing hook runs before step 2 and its Closed hook after step 4, so they bracket the scope's own services as before. Exporting children (step 3) therefore close *inside* their parent's Closing/Closed pair, not before it. That is an exception to "children before their parent", specified as a MODIFIED requirement in `scope-lifecycle-hooks` and flagged as a behavior change for exporting scopes only.

### D6. Collections and by-type selection
- `collectionCandidatesFor` keeps the alias exclusion, except for exports. An export is skipped only when the reader's scope is the export's source scope or one of its descendants (an ancestor walk from `source.from`).
- `selectByType` already includes aliases; exports need no change there. Read from inside the source scope, the source itself is nearer and wins.
- An export's collection order is its own registration index in the owner scope, as for any member; `WithOrder` on the source does not move it.

### D7. Registration and counters
- `Export`/`ExportNamed` validate (nil `from`, no parent, source not in `from` itself, type not assignable via `typeCanCastToType`, empty name). They then register through `add-default-registrations`' `registerLocked` helper (its D4 and task 3.3) under the parent's write lock, so check-then-set is atomic, and return its error instead of panicking. A name taken by a non-default, or by a resolved default, returns the duplicate error; an explicit export replaces an unresolved default of the published name; an export of a default follows D8.
- The registration hooks run as `registerLocked` runs them for `provide`: before-hook first, after-hook only when stored or replaced.
- Each scope keeps an `exportCount` (atomic), increased when an export is registered and decreased when its wrapper is removed. `Scope.clone` sets the clone's count from the wrappers it copies, because it bypasses `serviceSet`. The four-step shutdown, the collection check and the health skip run only when it is non-zero.

### D8. An export of a default is a default
- `Export` reads the source wrapper. If it is a `serviceDefault`, the export wrapper is wrapped as a default too (`serviceDefault[T]` around `serviceExport[T]`), so in the parent it follows every default rule: it yields to ancestors, an explicit same-name registration replaces it while unresolved, and registering it over an explicit service is skipped (`Export` returns nil).
- **No recursion:** the source default's `decide()` skips default wrappers, so it never yields to its own export, which is a default. This is what fixes the loop the audit found (B1), where an explicit export made the private default yield to the export, which resolved the private default again.
- **Application wins:** when the application's explicit service is in the parent (before or after `Export`), the parent's name resolves to it, and the private default also yields to it (it is in an ancestor). Every reader sees the application's instance.
- **The export marker survives the wrapping:** `serviceDefault` forwards `exportSource()` while it is unresolved or chosen, and reports no export once it yielded (it is then an alias of the application's service, not an export). Without this forwarding, every rule keyed on the marker (D4 health skip, D5 step 2, D6 collections, and `add-extensions`' health skip) would miss exports of defaults, which is the shape every extension export takes. This change adds that method to `serviceDefault`'s forwarded interfaces.

## Risks / Trade-offs

- [The four-step shutdown changes ordering for scopes that hold exports] → it is limited to such scopes (counter). The scenario "Scopes without exports keep children-first shutdown" and the full existing suite pin the unchanged path.
- [Lookup by child name breaks if the user re-creates a same-name child after shutting the original down] → `Scope(name)` panics on a reserved name today (audit V4), so it can't happen. It is documented in the godoc of `Export`.
- [Skipping one name check hides a real cycle] → only the export-to-source hop is exempt, and a RED test pins a real cycle.
- [Step 2's reverse walk costs O(edges) per scope shutdown] → it only runs in scopes with exports, and shutdown is not a hot path. A benchmark is not required, but the existing `bench/` suite is compared with `benchstat` for `Invoke` and `InvokeAll`, to check that the collection check costs nothing without exports.

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

The production code goes in `export.go` (new), tested in `export_test.go`. The shutdown ordering is tested in `scope_test.go`, with an example in `export_example_test.go`. Tests are table-driven with named subtests and testify, run under `-race`, and use `testWithTimeout` around shutdown tests.

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
| Closing and Closed around an exporting child | scope_test.go: `TestScopeShutdown_ExportingChildHookOrder` | unit, timeout |
| A same-name grandchild does not capture the export | export_test.go: `TestExport_DirectChildOnly` (100 runs) | unit |
| An export replaces an unresolved default in the parent | export_test.go: `TestExport_ReplacesUnresolvedDefault` | unit |
| Exported target is shut down once | export_test.go: `TestExport_TargetShutdownOnce/{root,published_name,owning_scope}` | unit, table |
| The application's service wins over an exported default | export_test.go: `TestExport_DefaultSource/{export_only_root,export_only_redis,app_first_root,app_first_redis,app_after_root,app_after_redis}` | unit, table |
| Exporting a default where an explicit service exists succeeds without effect | export_test.go: `TestExport_DefaultOverExplicitSkipped` | unit |
| A health check from the source scope checks the exported service once | export_test.go: `TestExport_SourceScopeHealthCheck/{explicit_source,default_source}` | unit, table |
| Alias does not duplicate its target | invoke_all_test.go: existing `TestInvokeAll_AliasExcluded` (unchanged; the MODIFIED text only clarifies exports) | unit |

The Risks section adds `TestExport_ShutdownOrderRepeated`: the direct row 200 times under `-race`, which pins that the order doesn't depend on scheduling.

Examples (docs): `ExampleExport` (a private pool in a child scope, the client exported and used by an app in the root) and `ExampleExportAs`, both with `// Output:`.

**Hot paths and their coverage:**
- Export resolution: the publishes, resolves-from-source and concurrent tests.
- Scope shutdown with and without exports: the four `TestScopeShutdown_*` tests and the existing scope shutdown suite.
- Collection candidates: the membership test and the existing collection suite.
- Start ordering: the start-order test.
- Health check: the lifecycle and root health check tests.
- Clone: the clone test.
- `goleak` in `TestMain` covers the parallel shutdown goroutines.

## Migration Plan

Additive. No consumer migration. Docs: a "Scope export" section on the scopes page and the godoc of the new functions. Rollback is a revert.

Order: archive `consolidate-pending-lifecycle-work` first (this change adds to its `service-aliases` and `collection-invocation` capabilities), then `add-default-registrations`, then this change, then `add-extensions`.
